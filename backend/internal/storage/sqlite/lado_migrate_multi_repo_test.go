package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"testing"
)

// ladoMultiRepoTables are the tables 0015 creates, with their columns in
// order.
var ladoMultiRepoTables = []struct {
	name    string
	columns []string
}{
	{"lado_execution_prs", []string{"execution_session_id", "pr_url", "repo_key", "position", "tracked_at", "replaced_at", "settled_at", "settled_pr_state"}},
	{"lado_part_run_repos", []string{"queue_entry_id", "relative_path", "position", "repo_dir", "base_sha", "head_sha", "base_branch", "base_remote", "dispatch_local_base_sha", "base_commits_ahead"}},
	{"lado_task_part_repos", []string{"queue_entry_id", "part_id", "relative_path", "start_sha", "merge_from_sha", "merge_tip_sha", "merged_sha"}},
}

// ladoMultiRepoColumns are the columns 0015 adds to tables before it.
var ladoMultiRepoColumns = []struct {
	table   string
	columns []string
}{
	{"lado_executions", []string{"pr_repos_json", "pr_repos_firm"}},
	{"lado_task_parts", []string{"merge_repo"}},
	{"lado_pr_resolves", []string{"pr_url"}},
	{"lado_execution_reviews", []string{"pr_url"}},
}

// insertLadoPartRunFixture writes parts run "entry" with part "a" for the
// execution fixture's entry.
func insertLadoPartRunFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO lado_part_runs (queue_entry_id, task_id, project_id, plan_revision_id, repo_dir, branch, base_sha, head_sha, started_at)
		VALUES ('entry', 'task', 'p', 'revision', '/w/back', 'lado/t/e/integration', 'base', 'base', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_task_parts (queue_entry_id, part_id, task_id, position, updated_at)
		VALUES ('entry', 'a', 'task', 0, CURRENT_TIMESTAMP)`)
}

// insertLadoMultiRepoRows writes a row in each table 0015 creates.
func insertLadoMultiRepoRows(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO lado_execution_prs (execution_session_id, pr_url, repo_key, position, tracked_at)
		VALUES ('session', 'https://github.com/o/back/pull/1', 'back', 0, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_part_run_repos (queue_entry_id, relative_path, position, repo_dir, base_sha, head_sha)
		VALUES ('entry', 'back', 0, '/w/back', 'base', 'base')`)
	mustExec(t, db, `INSERT INTO lado_task_part_repos (queue_entry_id, part_id, relative_path) VALUES ('entry', 'a', 'back')`)
}

// 0015 creates the tracked PR and repository tables with their checks and
// adds its columns at the end, giving rows written before it the defaults:
// no expected repositories, no conflicted repository and no PR.
func TestLadoMigrate0015CreatesMultiRepoTables(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want || got < 15 {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	for _, table := range ladoMultiRepoTables {
		if got := tableColumns(t, db, table.name); !slices.Equal(got, table.columns) {
			t.Fatalf("%s columns = %v, want %v", table.name, got, table.columns)
		}
		var upstreamKeys int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list(?) WHERE substr("table", 1, 5) <> 'lado_'`, table.name).Scan(&upstreamKeys); err != nil {
			t.Fatalf("read %s foreign keys: %v", table.name, err)
		}
		if upstreamKeys != 0 {
			t.Fatalf("%s has %d FOREIGN KEYs to upstream tables, want none", table.name, upstreamKeys)
		}
	}
	for _, added := range ladoMultiRepoColumns {
		if got := tableColumns(t, db, added.table); !hasColumnRun(got, added.columns) || !slices.Equal(got[len(got)-len(added.columns):], added.columns) {
			t.Fatalf("%s columns = %v, want them to end with %v", added.table, got, added.columns)
		}
	}

	// Rows written before 0015 stay as they were and read the defaults.
	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 14); err != nil {
		t.Fatalf("down to 0014: %v", err)
	}
	insertLadoExecutionFixture(t, db)
	insertLadoPartRunFixture(t, db)
	insertLadoResolveRow(t, db)
	insertLadoVerifyRow(t, db, "lado_execution_reviews")
	mustExec(t, db, `UPDATE lado_executions SET settled_at = CURRENT_TIMESTAMP, settled_pr_url = 'https://github.com/o/r/pull/1', settled_pr_state = 'merged'`)
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to 0015: %v", err)
	}
	var repos, settledURL, mergeRepo, resolvePR, reviewPR string
	var firm int
	if err := db.QueryRow(`SELECT pr_repos_json, pr_repos_firm, settled_pr_url FROM lado_executions WHERE session_id = 'session'`).Scan(&repos, &firm, &settledURL); err != nil {
		t.Fatalf("read execution: %v", err)
	}
	if repos != "[]" || firm != 0 || settledURL != "https://github.com/o/r/pull/1" {
		t.Fatalf("execution after 0015 = %q %d %q, want [] 0 and its settled PR kept", repos, firm, settledURL)
	}
	if err := db.QueryRow(`SELECT merge_repo FROM lado_task_parts`).Scan(&mergeRepo); err != nil || mergeRepo != "" {
		t.Fatalf("part merge_repo = %q, %v; want ''", mergeRepo, err)
	}
	if err := db.QueryRow(`SELECT pr_url FROM lado_pr_resolves`).Scan(&resolvePR); err != nil || resolvePR != "" {
		t.Fatalf("resolve pr_url = %q, %v; want ''", resolvePR, err)
	}
	if err := db.QueryRow(`SELECT pr_url FROM lado_execution_reviews`).Scan(&reviewPR); err != nil || reviewPR != "" {
		t.Fatalf("review pr_url = %q, %v; want ''", reviewPR, err)
	}
	var tracked, runRepos int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM lado_execution_prs), (SELECT COUNT(*) FROM lado_part_run_repos)`).Scan(&tracked, &runRepos); err != nil || tracked != 0 || runRepos != 0 {
		t.Fatalf("0015 wrote %d tracked PRs and %d run repositories, %v; want none", tracked, runRepos, err)
	}

	insertLadoMultiRepoRows(t, db)
	// A second live PR of a repository is refused; a replaced one makes room.
	mustExec(t, db, `INSERT INTO lado_execution_prs (execution_session_id, pr_url, repo_key, position, tracked_at)
		VALUES ('session', 'https://github.com/o/front/pull/2', 'front', 1, CURRENT_TIMESTAMP)`)
	if _, err := db.Exec(`INSERT INTO lado_execution_prs (execution_session_id, pr_url, repo_key, position, tracked_at)
		VALUES ('session', 'https://github.com/o/back/pull/3', 'back', 0, CURRENT_TIMESTAMP)`); err == nil {
		t.Fatal("a second live PR of one repository was accepted")
	}
	mustExec(t, db, `UPDATE lado_execution_prs SET replaced_at = CURRENT_TIMESTAMP WHERE pr_url = 'https://github.com/o/back/pull/1'`)
	mustExec(t, db, `INSERT INTO lado_execution_prs (execution_session_id, pr_url, repo_key, position, tracked_at)
		VALUES ('session', 'https://github.com/o/back/pull/3', 'back', 0, CURRENT_TIMESTAMP)`)
	for name, query := range map[string]string{
		"an empty PR":                   `INSERT INTO lado_execution_prs (execution_session_id, pr_url, tracked_at) VALUES ('session', '', CURRENT_TIMESTAMP)`,
		"a PR of no execution":          `INSERT INTO lado_execution_prs (execution_session_id, pr_url, tracked_at) VALUES ('missing', 'u', CURRENT_TIMESTAMP)`,
		"a negative position":           `UPDATE lado_execution_prs SET position = -1`,
		"an open settled state":         `UPDATE lado_execution_prs SET settled_at = CURRENT_TIMESTAMP, settled_pr_state = 'open'`,
		"a settled time without state":  `UPDATE lado_execution_prs SET settled_at = CURRENT_TIMESTAMP`,
		"a state without a settle time": `UPDATE lado_execution_prs SET settled_pr_state = 'merged'`,
		"a repos list that is no array": `UPDATE lado_executions SET pr_repos_json = '{}'`,
		"an invalid repos list":         `UPDATE lado_executions SET pr_repos_json = 'back'`,
		"a firm flag of 2":              `UPDATE lado_executions SET pr_repos_firm = 2`,
		"a repository of no parts run":  `INSERT INTO lado_part_run_repos (queue_entry_id, relative_path, position, repo_dir, base_sha, head_sha) VALUES ('missing', 'front', 1, '/w/front', 'b', 'b')`,
		"two repositories at a place":   `INSERT INTO lado_part_run_repos (queue_entry_id, relative_path, position, repo_dir, base_sha, head_sha) VALUES ('entry', 'front', 0, '/w/front', 'b', 'b')`,
		"a run repository without dir":  `INSERT INTO lado_part_run_repos (queue_entry_id, relative_path, position, repo_dir, base_sha, head_sha) VALUES ('entry', 'front', 1, '', 'b', 'b')`,
		"a part repository of no part":  `INSERT INTO lado_task_part_repos (queue_entry_id, part_id, relative_path) VALUES ('entry', 'missing', 'back')`,
		"a part repository of no run's": `INSERT INTO lado_task_part_repos (queue_entry_id, part_id, relative_path) VALUES ('entry', 'a', 'front')`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	mustExec(t, db, `UPDATE lado_execution_prs SET settled_at = CURRENT_TIMESTAMP, settled_pr_state = 'merged' WHERE pr_url = 'https://github.com/o/back/pull/3'`)
	mustExec(t, db, `UPDATE lado_executions SET pr_repos_json = '["back","front"]', pr_repos_firm = 1`)
}

// A tracked PR publishes its execution's change when it is tracked, settled,
// replaced or moved; a repository's integration head and a part's conflicted
// repository publish their task's change.
func TestLadoMigrate0015ChangeTriggers(t *testing.T) {
	db := openLadoDatabase(t, 1)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/p', CURRENT_TIMESTAMP)`)
	insertLadoExecutionFixture(t, db)
	insertLadoPartRunFixture(t, db)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, created_at, updated_at, activity_last_at)
		VALUES ('session', 'p', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	var seq int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	count := func(step, kind string, want int) {
		t.Helper()
		var got int
		var last int64
		if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(seq), ?) FROM change_log
			WHERE seq > ? AND json_extract(payload, '$.kind') = ? AND json_extract(payload, '$.taskId') = 'task'`, seq, seq, kind).Scan(&got, &last); err != nil {
			t.Fatalf("%s: read change_log: %v", step, err)
		}
		if got != want {
			t.Fatalf("%s: %d %s changes, want %d", step, got, kind, want)
		}
		seq = max(seq, last)
	}
	step := func(statement, kind string, want int) {
		t.Helper()
		mustExec(t, db, statement)
		count(statement, kind, want)
	}

	insertLadoMultiRepoRows(t, db)
	count("tracked PR insert", "lado_execution", 1)
	step(`UPDATE lado_execution_prs SET tracked_at = CURRENT_TIMESTAMP`, "lado_execution", 0)
	step(`UPDATE lado_execution_prs SET position = 1`, "lado_execution", 1)
	step(`UPDATE lado_execution_prs SET settled_at = CURRENT_TIMESTAMP, settled_pr_state = 'closed'`, "lado_execution", 1)
	step(`UPDATE lado_execution_prs SET replaced_at = CURRENT_TIMESTAMP`, "lado_execution", 1)

	step(`UPDATE lado_part_run_repos SET base_branch = 'main'`, "lado_task", 0)
	step(`UPDATE lado_part_run_repos SET head_sha = 'moved'`, "lado_task", 1)
	step(`UPDATE lado_task_part_repos SET merged_sha = 'moved'`, "lado_task", 0)
	step(`UPDATE lado_task_parts SET merge_repo = 'back'`, "lado_task", 1)
	step(`UPDATE lado_executions SET pr_repos_json = '["back"]', pr_repos_firm = 1`, "lado_execution", 0)
}

// The down migration removes only what 0015 added, keeping the rows written
// before it and 0014's tables, and the round trip adds it again.
func TestLadoMigrate0015RoundTripKeeps0014(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	insertLadoExecutionFixture(t, db)
	insertLadoPartRunFixture(t, db)
	insertLadoResolveRow(t, db)
	insertLadoVerifyRow(t, db, "lado_execution_reviews")
	insertLadoMultiRepoRows(t, db)
	mustExec(t, db, `UPDATE lado_executions SET pr_repos_json = '["back"]', pr_repos_firm = 1`)
	mustExec(t, db, `UPDATE lado_task_parts SET merge_repo = 'back'`)

	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 14); err != nil {
		t.Fatalf("down to 0014: %v", err)
	}
	for _, table := range ladoMultiRepoTables {
		if tableExists(t, db, table.name) {
			t.Fatalf("table %s survived the 0015 down migration", table.name)
		}
	}
	for _, added := range ladoMultiRepoColumns {
		got := tableColumns(t, db, added.table)
		for _, column := range added.columns {
			if slices.Contains(got, column) {
				t.Fatalf("%s.%s survived the 0015 down migration", added.table, column)
			}
		}
	}
	if !tableExists(t, db, "lado_relays") {
		t.Fatal("the 0015 down migration dropped 0014's lado_relays")
	}
	var executions, parts, resolves, reviews int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM lado_executions), (SELECT COUNT(*) FROM lado_task_parts),
		(SELECT COUNT(*) FROM lado_pr_resolves), (SELECT COUNT(*) FROM lado_execution_reviews)`).Scan(&executions, &parts, &resolves, &reviews); err != nil ||
		executions != 1 || parts != 1 || resolves != 1 || reviews != 1 {
		t.Fatalf("rows after the down migration = %d %d %d %d, %v; want one each", executions, parts, resolves, reviews, err)
	}
	if got := appliedVersion(t, db, ladoVersionTable); got != 14 {
		t.Fatalf("%s after the down migration = %d, want 14", ladoVersionTable, got)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	for _, table := range ladoMultiRepoTables {
		if got := tableColumns(t, db, table.name); !slices.Equal(got, table.columns) {
			t.Fatalf("%s columns after the round trip = %v", table.name, got)
		}
	}
	var repos string
	if err := db.QueryRow(`SELECT pr_repos_json FROM lado_executions`).Scan(&repos); err != nil || repos != "[]" {
		t.Fatalf("execution repos after the round trip = %q, %v; want []", repos, err)
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
}
