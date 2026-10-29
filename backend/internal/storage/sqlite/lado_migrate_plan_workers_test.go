package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"testing"
)

// ladoPlanWorkerRequestColumns are the columns 0009 adds to
// lado_plan_requests, in order.
var ladoPlanWorkerRequestColumns = []string{
	"kind", "waiting_reason", "worker_attempts", "worker_started_at", "draft_sha256", "draft_submitted_at",
	"feedback_sent_at", "final_sha256", "submitted_at", "fix_requested_at", "reminded_at", "cleaned_at", "failure_code",
}

// ladoPlanWorkerTables are the tables 0009 creates.
var ladoPlanWorkerTables = []string{"lado_plan_critiques", "lado_plan_revision_metrics", "lado_orchestrator_notices", "lado_notice_state"}

// ladoPlanWorkerSettingsColumns are the columns 0009 adds to
// lado_dispatch_settings, in order.
var ladoPlanWorkerSettingsColumns = []string{"max_plan_workers", "plan_review_skip", "plan_review_skip_max_paths", "plan_review_patterns"}

// A request made before plan workers went to the orchestrator, so 0009 marks
// it so and leaves its worker facts empty; the dispatch settings row keeps its
// values and gets the plan worker defaults. The round trip removes only what
// 0009 added.
func TestLadoMigrate0009KeepsEarlierRequestsAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 8); err != nil {
		t.Fatalf("down to 0008: %v", err)
	}
	for _, table := range ladoPlanWorkerTables {
		if tableExists(t, db, table) {
			t.Fatalf("table %s survived the 0009 down migration", table)
		}
	}
	for _, column := range ladoPlanWorkerRequestColumns {
		if slices.Contains(tableColumns(t, db, "lado_plan_requests"), column) {
			t.Fatalf("lado_plan_requests.%s survived the 0009 down migration", column)
		}
	}
	insertLadoSnapshotFixture(t, db)
	mustExec(t, db, `INSERT INTO lado_dispatch_settings (id, max_concurrent, max_run_minutes, hold_on_memory_tight, updated_at)
		VALUES (1, 4, 0, 0, CURRENT_TIMESTAMP)`)

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to 0009: %v", err)
	}
	if got := tableColumns(t, db, "lado_plan_requests"); !slices.Equal(got[len(got)-len(ladoPlanWorkerRequestColumns):], ladoPlanWorkerRequestColumns) {
		t.Fatalf("lado_plan_requests columns = %v, want them to end with %v", got, ladoPlanWorkerRequestColumns)
	}
	var kind, failure string
	var attempts int
	var started sql.NullTime
	if err := db.QueryRow(`SELECT kind, worker_attempts, worker_started_at, failure_code FROM lado_plan_requests WHERE id = 'request'`).
		Scan(&kind, &attempts, &started, &failure); err != nil {
		t.Fatalf("read the earlier request: %v", err)
	}
	if kind != "orchestrator" || attempts != 0 || started.Valid || failure != "" {
		t.Fatalf("earlier request = kind %q, %d attempts, started %v, failure %q; want an orchestrator request", kind, attempts, started.Valid, failure)
	}
	var maxConcurrent, maxPlanWorkers, skip, maxPaths int
	var patterns string
	if err := db.QueryRow(`SELECT max_concurrent, max_plan_workers, plan_review_skip, plan_review_skip_max_paths, plan_review_patterns FROM lado_dispatch_settings`).
		Scan(&maxConcurrent, &maxPlanWorkers, &skip, &maxPaths, &patterns); err != nil {
		t.Fatalf("read dispatch settings: %v", err)
	}
	if maxConcurrent != 4 || maxPlanWorkers != 2 || skip != 1 || maxPaths != 3 || patterns != "" {
		t.Fatalf("dispatch settings = %d, %d, %d, %d, %q; want 4 kept and the plan worker defaults", maxConcurrent, maxPlanWorkers, skip, maxPaths, patterns)
	}
	var since sql.NullTime
	if err := db.QueryRow(`SELECT since FROM lado_notice_state WHERE id = 1`).Scan(&since); err != nil || !since.Valid {
		t.Fatalf("notice start = %v, %v; want the time 0009 ran", since, err)
	}
	if _, err := db.Exec(`UPDATE lado_plan_requests SET kind = 'other' WHERE id = 'request'`); err == nil {
		t.Fatal("an unknown request kind was accepted")
	}
	if _, err := db.Exec(`UPDATE lado_dispatch_settings SET max_plan_workers = 9`); err == nil {
		t.Fatal("nine plan workers were accepted")
	}

	if _, err := provider.DownTo(ctx, 8); err != nil {
		t.Fatalf("down to 0008 again: %v", err)
	}
	var requests int
	if err := db.QueryRow(`SELECT COUNT(*) FROM lado_plan_requests WHERE id = 'request'`).Scan(&requests); err != nil || requests != 1 {
		t.Fatalf("the earlier request after the down migration = %d, %v", requests, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	if got := tableColumns(t, db, "lado_dispatch_settings"); !hasColumnRun(got, ladoPlanWorkerSettingsColumns) {
		t.Fatalf("lado_dispatch_settings columns = %v", got)
	}
}
