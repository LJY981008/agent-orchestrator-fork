package sqlite

import (
	"context"
	"slices"
	"testing"
)

// ladoPartsTables are the tables 0010 creates.
var ladoPartsTables = []string{"lado_part_runs", "lado_task_parts"}

// 0010 rebuilds lado_executions without its one-execution-per-entry UNIQUE
// constraint while verification rows reference it: the earlier execution and
// its check run survive with role '', an entry then takes part workers beside
// its one pull request worker, and the round trip removes the part workers
// and gives the constraint back.
func TestLadoMigrate0010RebuildsExecutionsAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 9); err != nil {
		t.Fatalf("down to 0009: %v", err)
	}
	for _, table := range ladoPartsTables {
		if tableExists(t, db, table) {
			t.Fatalf("table %s survived the 0010 down migration", table)
		}
	}
	insertLadoExecutionFixture(t, db)
	mustExec(t, db, `INSERT INTO lado_check_runs (id, execution_session_id, head_sha, name, command, status, started_at)
		VALUES ('run', 'session', '`+ladoVerifyHead+`', 'test', 'go test', 'passed', CURRENT_TIMESTAMP)`)

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to 0010: %v", err)
	}
	var role string
	if err := db.QueryRow(`SELECT role FROM lado_executions WHERE session_id = 'session'`).Scan(&role); err != nil || role != "" {
		t.Fatalf("earlier execution role = %q, %v; want it kept with role ''", role, err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign key check: %v", err)
	}
	if rows.Next() {
		t.Fatal("the rebuild left a foreign key violation")
	}
	_ = rows.Close()
	mustExec(t, db, `INSERT INTO lado_executions (session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id, attempt, started_at, role, part_id)
		VALUES ('part-a', 'task', 'entry', 'revision', 'approval', 'snapshot', 1, CURRENT_TIMESTAMP, 'part', 'a')`)
	if _, err := db.Exec(`INSERT INTO lado_executions (session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id, attempt, started_at, role)
		VALUES ('second', 'task', 'entry', 'revision', 'approval', 'snapshot', 1, CURRENT_TIMESTAMP, 'integration')`); err == nil {
		t.Fatal("an entry took a second pull request worker")
	}
	if _, err := db.Exec(`UPDATE lado_executions SET role = 'other' WHERE session_id = 'part-a'`); err == nil {
		t.Fatal("an unknown role was accepted")
	}
	if _, err := db.Exec(`INSERT INTO lado_check_runs (id, execution_session_id, head_sha, name, command, status, started_at)
		VALUES ('orphan', 'missing', '`+ladoVerifyHead+`', 'test', 'go test', 'passed', CURRENT_TIMESTAMP)`); err == nil {
		t.Fatal("a check run of no execution was accepted: the rebuilt table lost its references")
	}
	var workers, wait, disk int
	mustExec(t, db, `INSERT INTO lado_dispatch_settings (id, max_concurrent, max_run_minutes, hold_on_memory_tight, updated_at)
		VALUES (1, 4, 0, 0, CURRENT_TIMESTAMP)`)
	if err := db.QueryRow(`SELECT max_workers, hold_on_disk_low, min_free_disk_gb FROM lado_dispatch_settings`).Scan(&workers, &wait, &disk); err != nil ||
		workers != 6 || wait != 1 || disk != 10 {
		t.Fatalf("dispatch settings defaults = %d, %d, %d, %v; want 6 workers and 10 GB", workers, wait, disk, err)
	}

	if _, err := provider.DownTo(ctx, 9); err != nil {
		t.Fatalf("down to 0009 again: %v", err)
	}
	var executions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM lado_executions`).Scan(&executions); err != nil || executions != 1 {
		t.Fatalf("executions after the down migration = %d, %v; want the earlier one only", executions, err)
	}
	if slices.Contains(tableColumns(t, db, "lado_executions"), "role") {
		t.Fatal("lado_executions.role survived the down migration")
	}
	if _, err := db.Exec(`INSERT INTO lado_executions (session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id, attempt, started_at)
		VALUES ('again', 'task', 'entry', 'revision', 'approval', 'snapshot', 1, CURRENT_TIMESTAMP)`); err == nil {
		t.Fatal("the down migration lost the one-execution-per-entry constraint")
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
}
