package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
)

// The test file keeps four top-level tests: the sqlite-race CI shards split
// this package's test list by position (knowledge note sqlite-race-shard-shift).

// ladoDBChangeTables are the tables 0020 creates, with their columns in order.
var ladoDBChangeTables = []struct {
	name    string
	columns []string
}{
	{"lado_db_change_settings", []string{"project_id", "repo", "enabled", "paths_json", "command", "timeout_minutes",
		"secret_names_json", "baseline_commit", "enabled_at", "updated_at"}},
	{"lado_db_change_runs", []string{"id", "project_id", "repo", "execution_session_id", "task_id", "pr_url", "head_sha",
		"kind", "run_trigger", "state", "approval_id", "attempts", "error", "created_at", "updated_at"}},
	{"lado_db_change_applications", []string{"id", "project_id", "repo", "path", "blob_sha", "phase", "result", "run_id",
		"execution_session_id", "task_id", "pr_url", "head_sha", "exit_code", "output_sha256", "message", "actor",
		"started_at", "finished_at"}},
	{"lado_db_change_status_holds", []string{"approval_id", "held_at"}},
}

// ladoDBChangeBlob is a 40-character blob id of one repeated hex digit.
func ladoDBChangeBlob(c byte) string { return strings.Repeat(string(c), 40) }

// insertLadoDBChangeFixture writes project "p"'s settings for repository
// "back", run "run" at ladoVerifyHead and application "app" of it, and holds
// approval "integration" of execution "session".
func insertLadoDBChangeFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	insertLadoExecutionFixture(t, db)
	insertLadoVerifyRow(t, db, "lado_integration_approvals")
	mustExec(t, db, `INSERT INTO lado_db_change_settings (project_id, repo, enabled, paths_json, command, secret_names_json, updated_at)
		VALUES ('p', 'back', 1, '["db/*.sql"]', 'psql -f {file}', '["DSN"]', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_db_change_runs (id, project_id, repo, execution_session_id, task_id, pr_url, head_sha,
		kind, run_trigger, state, created_at, updated_at)
		VALUES ('run', 'p', 'back', 'session', 'task', 'https://github.com/o/r/pull/1', ?, 'pre_merge', 'approval', 'queued',
		CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, ladoVerifyHead)
	mustExec(t, db, `INSERT INTO lado_db_change_applications (id, project_id, repo, path, blob_sha, phase, result, run_id, started_at)
		VALUES ('app', 'p', 'back', 'db/001.sql', ?, 'pre_merge', 'running', 'run', CURRENT_TIMESTAMP)`, ladoDBChangeBlob('1'))
	mustExec(t, db, `INSERT INTO lado_db_change_status_holds (approval_id, held_at) VALUES ('integration', CURRENT_TIMESTAMP)`)
}

// 0020 creates the four database change tables with their keys, and leaves
// the approvals table as it was.
func TestLadoMigrate0020CreatesDBChangeTables(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want || got < 20 {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	approvalColumns := tableColumns(t, db, "lado_integration_approvals")
	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 19); err != nil {
		t.Fatalf("down to 0019: %v", err)
	}
	for _, table := range ladoDBChangeTables {
		if tableExists(t, db, table.name) {
			t.Fatalf("%s exists before 0020", table.name)
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to 0020: %v", err)
	}
	for _, table := range ladoDBChangeTables {
		if got := tableColumns(t, db, table.name); !slices.Equal(got, table.columns) {
			t.Errorf("%s columns = %v, want %v", table.name, got, table.columns)
		}
	}
	if got := tableColumns(t, db, "lado_integration_approvals"); !slices.Equal(got, approvalColumns) {
		t.Errorf("lado_integration_approvals columns = %v, want them unchanged: %v", got, approvalColumns)
	}
	var target, onDelete string
	if err := db.QueryRow(`SELECT "table", on_delete FROM pragma_foreign_key_list('lado_db_change_status_holds')`).Scan(&target, &onDelete); err != nil ||
		target != "lado_integration_approvals" || onDelete != "CASCADE" {
		t.Errorf("lado_db_change_status_holds foreign key = %q %q, %v; want lado_integration_approvals CASCADE", target, onDelete, err)
	}
	for _, table := range []string{"lado_db_change_settings", "lado_db_change_runs", "lado_db_change_applications"} {
		if got := countRows(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_list('`+table+`')`); got != 0 {
			t.Errorf("%s has %d foreign keys, want none", table, got)
		}
	}
	for _, index := range []string{"lado_db_change_applications_by_path", "lado_db_change_runs_by_state",
		"lado_db_change_runs_by_execution", "lado_db_change_runs_by_project"} {
		if got := countRows(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = '`+index+`'`); got != 1 {
			t.Errorf("index %s missing", index)
		}
	}
}

// The checks refuse unknown kinds, triggers, states, phases and results, a
// second run of one PR, head and kind, and a hold of no approval; holds go
// with their approval.
func TestLadoMigrate0020ChecksAndKeys(t *testing.T) {
	db := openLadoDatabase(t, 1)
	insertLadoDBChangeFixture(t, db)
	for name, query := range map[string]string{
		"an empty project":            `UPDATE lado_db_change_settings SET project_id = ''`,
		"enabled of 2":                `UPDATE lado_db_change_settings SET enabled = 2`,
		"paths that are no array":     `UPDATE lado_db_change_settings SET paths_json = '{}'`,
		"secret names of no JSON":     `UPDATE lado_db_change_settings SET secret_names_json = 'x'`,
		"a timeout of 121":            `UPDATE lado_db_change_settings SET timeout_minutes = 121`,
		"a negative timeout":          `UPDATE lado_db_change_settings SET timeout_minutes = -1`,
		"a short baseline commit":     `UPDATE lado_db_change_settings SET baseline_commit = 'abc'`,
		"a second settings row":       `INSERT INTO lado_db_change_settings (project_id, repo, updated_at) VALUES ('p', 'back', CURRENT_TIMESTAMP)`,
		"an unknown run kind":         `UPDATE lado_db_change_runs SET kind = 'post_merge'`,
		"an unknown run trigger":      `UPDATE lado_db_change_runs SET run_trigger = 'cron'`,
		"an unknown run state":        `UPDATE lado_db_change_runs SET state = 'paused'`,
		"negative attempts":           `UPDATE lado_db_change_runs SET attempts = -1`,
		"a short run head":            `UPDATE lado_db_change_runs SET head_sha = 'abc'`,
		"an empty run PR":             `UPDATE lado_db_change_runs SET pr_url = ''`,
		"an unknown phase":            `UPDATE lado_db_change_applications SET phase = 'later'`,
		"an unknown result":           `UPDATE lado_db_change_applications SET result = 'skipped'`,
		"an empty path":               `UPDATE lado_db_change_applications SET path = ''`,
		"a short blob":                `UPDATE lado_db_change_applications SET blob_sha = 'abc'`,
		"a short output hash":         `UPDATE lado_db_change_applications SET output_sha256 = 'abc'`,
		"a hold of no approval":       `INSERT INTO lado_db_change_status_holds (approval_id, held_at) VALUES ('nope', CURRENT_TIMESTAMP)`,
		"a second hold":               `INSERT INTO lado_db_change_status_holds (approval_id, held_at) VALUES ('integration', CURRENT_TIMESTAMP)`,
		"a second run of a PR's head": `INSERT INTO lado_db_change_runs (id, project_id, pr_url, head_sha, kind, run_trigger, state, created_at, updated_at) SELECT 'run2', project_id, pr_url, head_sha, kind, 'merged_unapplied', 'awaiting_human', created_at, updated_at FROM lado_db_change_runs`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	// Each accepted value, a run of the head's other kind and the root
	// repository's settings.
	for _, state := range []string{"awaiting_human", "applying", "failed", "applied", "merging", "merge_failed", "done", "queued"} {
		mustExec(t, db, `UPDATE lado_db_change_runs SET state = ?`, state)
	}
	for _, result := range []string{"applied", "failed", "interrupted", "baseline", "running"} {
		mustExec(t, db, `UPDATE lado_db_change_applications SET result = ?`, result)
	}
	mustExec(t, db, `UPDATE lado_db_change_settings SET timeout_minutes = 120, baseline_commit = ?`, ladoDBChangeBlob('2'))
	mustExec(t, db, `INSERT INTO lado_db_change_runs (id, project_id, pr_url, head_sha, kind, run_trigger, state, created_at, updated_at)
		SELECT 'run2', project_id, pr_url, head_sha, 'after_deploy', 'after_merge', 'awaiting_human', created_at, updated_at FROM lado_db_change_runs`)
	mustExec(t, db, `INSERT INTO lado_db_change_settings (project_id, updated_at) VALUES ('p', CURRENT_TIMESTAMP)`)
	var paths, secrets string
	var enabled, timeout int
	if err := db.QueryRow(`SELECT enabled, paths_json, secret_names_json, timeout_minutes FROM lado_db_change_settings
		WHERE project_id = 'p' AND repo = ''`).Scan(&enabled, &paths, &secrets, &timeout); err != nil ||
		enabled != 0 || paths != "[]" || secrets != "[]" || timeout != 0 {
		t.Fatalf("default settings = %d %q %q %d, %v; want off and empty", enabled, paths, secrets, timeout, err)
	}

	mustExec(t, db, `DELETE FROM lado_integration_approvals WHERE id = 'integration'`)
	if got := countRows(t, db, `SELECT COUNT(*) FROM lado_db_change_status_holds`); got != 0 {
		t.Fatalf("holds after their approval went = %d, want 0", got)
	}
}

// Settings saves, run moves and application results publish a
// lado_db_change change in the row's project; baseline rows, moves that
// change nothing the app shows and rows of an unregistered project publish
// nothing, and holds publish nothing.
func TestLadoMigrate0020ChangeTriggers(t *testing.T) {
	db := openLadoDatabase(t, 1)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/p', CURRENT_TIMESTAMP)`)
	var seq int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	type change struct {
		session sql.NullString
		op      string
	}
	step := func(statement string, args []any, want ...change) {
		t.Helper()
		mustExec(t, db, statement, args...)
		rows, err := db.Query(`SELECT seq, json_extract(payload, '$.sessionId'), json_extract(payload, '$.op')
			FROM change_log
			WHERE seq > ? AND project_id = 'p' AND session_id IS NULL AND event_type = 'session_updated'
			AND json_extract(payload, '$.kind') = 'lado_db_change' AND json_extract(payload, '$.projectId') = 'p'
			ORDER BY seq`, seq)
		if err != nil {
			t.Fatalf("%s: read change_log: %v", statement, err)
		}
		defer func() { _ = rows.Close() }()
		var got []change
		for rows.Next() {
			var c change
			if err := rows.Scan(&seq, &c.session, &c.op); err != nil {
				t.Fatal(err)
			}
			got = append(got, c)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s: changes = %+v, want %+v", statement, got, want)
		}
		if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&seq); err != nil {
			t.Fatal(err)
		}
	}
	settings := change{op: "settings"}
	ofSession := func(op string) change { return change{session: sql.NullString{String: "s1", Valid: true}, op: op} }
	head := []any{ladoVerifyHead}

	step(`INSERT INTO lado_db_change_settings (project_id, repo, updated_at) VALUES ('p', '', CURRENT_TIMESTAMP)`, nil, settings)
	step(`UPDATE lado_db_change_settings SET enabled = 1, updated_at = CURRENT_TIMESTAMP`, nil, settings)
	step(`INSERT INTO lado_db_change_applications (id, project_id, path, blob_sha, phase, result, started_at)
		VALUES ('base', 'p', 'db/000.sql', ?, 'pre_merge', 'baseline', CURRENT_TIMESTAMP)`, []any{ladoDBChangeBlob('0')})
	step(`INSERT INTO lado_db_change_runs (id, project_id, execution_session_id, pr_url, head_sha, kind, run_trigger, state, created_at, updated_at)
		VALUES ('run', 'p', 's1', 'https://github.com/o/r/pull/1', ?, 'pre_merge', 'approval', 'queued', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		head, ofSession("run"))
	step(`UPDATE lado_db_change_runs SET state = 'applying'`, nil, ofSession("run"))
	step(`UPDATE lado_db_change_runs SET attempts = 1`, nil, ofSession("run"))
	step(`UPDATE lado_db_change_runs SET error = 'x'`, nil, ofSession("run"))
	step(`UPDATE lado_db_change_runs SET updated_at = '2000-01-01 00:00:00'`, nil)
	step(`INSERT INTO lado_db_change_applications (id, project_id, path, blob_sha, phase, result, run_id, execution_session_id, started_at)
		VALUES ('app', 'p', 'db/001.sql', ?, 'pre_merge', 'running', 'run', 's1', CURRENT_TIMESTAMP)`, []any{ladoDBChangeBlob('1')},
		ofSession("application"))
	step(`UPDATE lado_db_change_applications SET result = 'applied', exit_code = 0, finished_at = CURRENT_TIMESTAMP WHERE id = 'app'`, nil,
		ofSession("application"))
	step(`UPDATE lado_db_change_applications SET message = 'done' WHERE id = 'app'`, nil)
	step(`INSERT INTO lado_db_change_settings (project_id, repo, updated_at) VALUES ('unregistered', '', CURRENT_TIMESTAMP)`, nil)
	step(`INSERT INTO lado_db_change_runs (id, project_id, pr_url, head_sha, kind, run_trigger, state, created_at, updated_at)
		VALUES ('other', 'unregistered', 'https://github.com/o/r/pull/2', ?, 'pre_merge', 'approval', 'queued', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, head)

	// A hold publishes nothing.
	insertLadoExecutionFixture(t, db)
	insertLadoVerifyRow(t, db, "lado_integration_approvals")
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	step(`INSERT INTO lado_db_change_status_holds (approval_id, held_at) VALUES ('integration', CURRENT_TIMESTAMP)`, nil)
	step(`DELETE FROM lado_db_change_status_holds`, nil)
}

// The migrations step one at a time: down from 0020 drops only the database
// change tables and keeps 0019's columns and the approvals; going up one
// step brings them back empty.
func TestLadoMigrate0020StepsKeep0019(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	insertLadoDBChangeFixture(t, db)

	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 20); err != nil {
		t.Fatalf("down to 0020: %v", err)
	}
	if got := appliedVersion(t, db, ladoVersionTable); got != 20 {
		t.Fatalf("%s = %d, want 20", ladoVersionTable, got)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("down from 0020: %v", err)
	}
	if got := appliedVersion(t, db, ladoVersionTable); got != 19 {
		t.Fatalf("%s after the down migration = %d, want 19", ladoVersionTable, got)
	}
	for _, table := range ladoDBChangeTables {
		if tableExists(t, db, table.name) {
			t.Fatalf("%s survived the 0020 down migration", table.name)
		}
	}
	if !slices.Contains(tableColumns(t, db, "lado_account_limits"), "released_waiting") {
		t.Fatal("the 0020 down migration dropped 0019's lado_account_limits.released_waiting")
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM lado_integration_approvals WHERE id = 'integration'`); got != 1 {
		t.Fatalf("approvals after the down migration = %d, want 1", got)
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("up to 0020: %v", err)
	}
	if got := appliedVersion(t, db, ladoVersionTable); got != 20 {
		t.Fatalf("%s after the up migration = %d, want 20", ladoVersionTable, got)
	}
	for _, table := range ladoDBChangeTables {
		if got := countRows(t, db, `SELECT COUNT(*) FROM `+table.name); got != 0 {
			t.Fatalf("%s after the round trip has %d rows, want 0", table.name, got)
		}
	}
	mustExec(t, db, `INSERT INTO lado_db_change_status_holds (approval_id, held_at) VALUES ('integration', CURRENT_TIMESTAMP)`)

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to the latest: %v", err)
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
}
