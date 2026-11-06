package sqlite

import (
	"context"
	"slices"
	"testing"
)

// insertLadoReportedPRRefusal writes a refusal of the execution fixture's
// reported PR for reason.
func insertLadoReportedPRRefusal(t *testing.T, reason string) string {
	t.Helper()
	return `INSERT INTO lado_reported_pr_refusals (execution_session_id, pr_url, reason, refused_at)
		VALUES ('session', 'https://github.com/o/r/pull/7', '` + reason + `', CURRENT_TIMESTAMP)`
}

// 0016 creates the refusal table with its checks, keyed by execution and PR,
// that goes with its execution.
func TestLadoMigrate0016CreatesReportedPRRefusals(t *testing.T) {
	db := openLadoDatabase(t, 1)
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want || got < 16 {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	if got, want := tableColumns(t, db, "lado_reported_pr_refusals"), []string{"execution_session_id", "pr_url", "reason", "refused_at"}; !slices.Equal(got, want) {
		t.Fatalf("lado_reported_pr_refusals columns = %v, want %v", got, want)
	}
	var target, onDelete string
	if err := db.QueryRow(`SELECT "table", on_delete FROM pragma_foreign_key_list('lado_reported_pr_refusals')`).Scan(&target, &onDelete); err != nil ||
		target != "lado_executions" || onDelete != "CASCADE" {
		t.Fatalf("foreign key = %q %q, %v; want lado_executions ON DELETE CASCADE", target, onDelete, err)
	}
	insertLadoExecutionFixture(t, db)
	for _, reason := range []string{"owned_elsewhere", "outside_project", "not_open", "invalid", "session_not_claimable", "unreachable"} {
		mustExec(t, db, `DELETE FROM lado_reported_pr_refusals`)
		mustExec(t, db, insertLadoReportedPRRefusal(t, reason))
	}
	for name, query := range map[string]string{
		"an unknown reason":        `UPDATE lado_reported_pr_refusals SET reason = 'nope'`,
		"an empty PR":              `INSERT INTO lado_reported_pr_refusals (execution_session_id, pr_url, reason, refused_at) VALUES ('session', '', 'invalid', CURRENT_TIMESTAMP)`,
		"a second refusal":         insertLadoReportedPRRefusal(t, "invalid"),
		"a refusal without a time": `INSERT INTO lado_reported_pr_refusals (execution_session_id, pr_url, reason) VALUES ('session', 'u', 'invalid')`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// A refusal publishes its execution's change when it is recorded and when
// its reason changes, not when only its time does.
func TestLadoMigrate0016ChangeTriggers(t *testing.T) {
	db := openLadoDatabase(t, 1)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/p', CURRENT_TIMESTAMP)`)
	insertLadoExecutionFixture(t, db)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, created_at, updated_at, activity_last_at)
		VALUES ('session', 'p', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	var seq int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	step := func(statement string, want int) {
		t.Helper()
		mustExec(t, db, statement)
		var got int
		var last int64
		if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(seq), ?) FROM change_log
			WHERE seq > ? AND session_id = 'session' AND json_extract(payload, '$.kind') = 'lado_execution'
			AND json_extract(payload, '$.taskId') = 'task'`, seq, seq).Scan(&got, &last); err != nil {
			t.Fatalf("%s: read change_log: %v", statement, err)
		}
		if got != want {
			t.Fatalf("%s: %d execution changes, want %d", statement, got, want)
		}
		seq = max(seq, last)
	}
	step(insertLadoReportedPRRefusal(t, "not_open"), 1)
	step(`UPDATE lado_reported_pr_refusals SET refused_at = CURRENT_TIMESTAMP`, 0)
	step(`UPDATE lado_reported_pr_refusals SET reason = 'unreachable'`, 1)
}

// The down migration drops only the refusal table, keeping the execution
// and 0015's tables, and the round trip adds it again.
func TestLadoMigrate0016RoundTripKeeps0015(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	insertLadoExecutionFixture(t, db)
	mustExec(t, db, insertLadoReportedPRRefusal(t, "invalid"))

	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 15); err != nil {
		t.Fatalf("down to 0015: %v", err)
	}
	if tableExists(t, db, "lado_reported_pr_refusals") {
		t.Fatal("lado_reported_pr_refusals survived the 0016 down migration")
	}
	if !tableExists(t, db, "lado_execution_prs") {
		t.Fatal("the 0016 down migration dropped 0015's lado_execution_prs")
	}
	var executions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM lado_executions`).Scan(&executions); err != nil || executions != 1 {
		t.Fatalf("executions after the down migration = %d, %v; want 1", executions, err)
	}
	if got := appliedVersion(t, db, ladoVersionTable); got != 15 {
		t.Fatalf("%s after the down migration = %d, want 15", ladoVersionTable, got)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	mustExec(t, db, insertLadoReportedPRRefusal(t, "invalid"))
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
}
