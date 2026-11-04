package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"testing"
)

// ladoResolveColumns are lado_pr_resolves' columns, in order.
var ladoResolveColumns = []string{
	"execution_session_id", "task_id", "queue_entry_id", "state", "attempts", "spawns", "session_id",
	"start_head_sha", "base_tip_sha", "spawn_request", "spawn_expires_at", "done_seen_at",
	"notified_head_sha", "error", "version", "created_at", "updated_at",
}

// ladoRevokeColumns are the columns 0013 adds to lado_integration_approvals.
var ladoRevokeColumns = []string{"approved_base_sha", "revoked_at", "revoked_reason"}

func insertLadoResolveRow(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO lado_pr_resolves (execution_session_id, task_id, queue_entry_id, created_at, updated_at)
		VALUES ('session', 'task', 'entry', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
}

// 0013 creates lado_pr_resolves with its checks and adds the revocation
// columns to approvals; an approval written before 0013 reads as not revoked.
func TestLadoMigrate0013CreatesResolveTable(t *testing.T) {
	db := openLadoDatabase(t, 1)
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want || got < 13 {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	if got := tableColumns(t, db, "lado_pr_resolves"); !slices.Equal(got, ladoResolveColumns) {
		t.Fatalf("lado_pr_resolves columns = %v, want %v", got, ladoResolveColumns)
	}
	var upstreamKeys int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list('lado_pr_resolves') WHERE substr("table", 1, 5) <> 'lado_'`).Scan(&upstreamKeys); err != nil {
		t.Fatalf("read foreign keys: %v", err)
	}
	if upstreamKeys != 0 {
		t.Fatalf("lado_pr_resolves has %d FOREIGN KEYs to upstream tables, want none", upstreamKeys)
	}
	if got := tableColumns(t, db, "lado_integration_approvals"); !hasColumnRun(got, ladoRevokeColumns) {
		t.Fatalf("lado_integration_approvals columns = %v, want them to end with %v", got, ladoRevokeColumns)
	}

	insertLadoExecutionFixture(t, db)
	insertLadoResolveRow(t, db)
	var state, request string
	var attempts, spawns, version int
	if err := db.QueryRow(`SELECT state, attempts, spawns, spawn_request, version FROM lado_pr_resolves`).Scan(&state, &attempts, &spawns, &request, &version); err != nil {
		t.Fatalf("read resolve: %v", err)
	}
	if state != "" || attempts != 0 || spawns != 0 || request != "" || version != 1 {
		t.Fatalf("new resolve = %q %d %d %q v%d, want '' 0 0 '' v1", state, attempts, spawns, request, version)
	}
	insertLadoVerifyRow(t, db, "lado_integration_approvals")
	var base, reason string
	var revoked sql.NullTime
	if err := db.QueryRow(`SELECT approved_base_sha, revoked_at, revoked_reason FROM lado_integration_approvals`).Scan(&base, &revoked, &reason); err != nil {
		t.Fatalf("read approval: %v", err)
	}
	if base != "" || revoked.Valid || reason != "" {
		t.Fatalf("approval revocation = %q %v %q, want none", base, revoked, reason)
	}

	// A held spawn request is unique and has an expiry.
	mustExec(t, db, `UPDATE lado_pr_resolves SET spawn_request = 'lado:entry/_resolve/1', spawn_expires_at = CURRENT_TIMESTAMP, state = 'resolving'`)
	for name, query := range map[string]string{
		"an unknown state":             `UPDATE lado_pr_resolves SET state = 'merged'`,
		"negative attempts":            `UPDATE lado_pr_resolves SET attempts = -1`,
		"negative spawns":              `UPDATE lado_pr_resolves SET spawns = -1`,
		"a version of 0":               `UPDATE lado_pr_resolves SET version = 0`,
		"a request without an expiry":  `UPDATE lado_pr_resolves SET spawn_expires_at = NULL`,
		"a second resolve of a run":    `INSERT INTO lado_pr_resolves (execution_session_id, task_id, queue_entry_id, created_at, updated_at) VALUES ('session', 'task', 'entry', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"a resolve of no execution":    `INSERT INTO lado_pr_resolves (execution_session_id, task_id, queue_entry_id, created_at, updated_at) VALUES ('missing', 'task', 'entry', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"a resolve of no entry":        `UPDATE lado_pr_resolves SET queue_entry_id = 'missing'`,
		"the same request on two rows": `INSERT INTO lado_pr_resolves (execution_session_id, task_id, queue_entry_id, spawn_request, spawn_expires_at, created_at, updated_at) VALUES ('other', 'task', 'entry', 'lado:entry/_resolve/1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	} {
		if name == "the same request on two rows" {
			mustExec(t, db, `INSERT INTO lado_executions (session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id, attempt, started_at, role)
				VALUES ('other', 'task', 'entry', 'revision', 'approval', 'snapshot', 2, CURRENT_TIMESTAMP, 'merge')`)
		}
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("%s was accepted", name)
		}
		if name == "the same request on two rows" {
			mustExec(t, db, `DELETE FROM lado_executions WHERE session_id = 'other'`)
		}
	}
	// Empty requests do not collide. A second execution of one entry is a
	// worker without a PR of its own (lado_executions_one_pr_worker).
	mustExec(t, db, `INSERT INTO lado_executions (session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id, attempt, started_at, role)
		VALUES ('other', 'task', 'entry', 'revision', 'approval', 'snapshot', 2, CURRENT_TIMESTAMP, 'merge')`)
	mustExec(t, db, `UPDATE lado_pr_resolves SET spawn_request = '', spawn_expires_at = NULL`)
	mustExec(t, db, `INSERT INTO lado_pr_resolves (execution_session_id, task_id, queue_entry_id, created_at, updated_at)
		VALUES ('other', 'task', 'entry', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
}

// A resolve publishes its execution's change when it is made and when its
// state, attempts, worker or error changes; an approval also publishes when
// it is revoked.
func TestLadoMigrate0013ChangeTriggers(t *testing.T) {
	db := openLadoDatabase(t, 1)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/p', CURRENT_TIMESTAMP)`)
	insertLadoExecutionFixture(t, db)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, created_at, updated_at, activity_last_at)
		VALUES ('session', 'p', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	var seq int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	count := func(step string, want int) {
		t.Helper()
		var got int
		var last int64
		if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(seq), ?) FROM change_log
			WHERE seq > ? AND session_id = 'session' AND json_extract(payload, '$.kind') = 'lado_execution'`, seq, seq).Scan(&got, &last); err != nil {
			t.Fatalf("%s: read change_log: %v", step, err)
		}
		if got != want {
			t.Fatalf("%s: %d execution changes, want %d", step, got, want)
		}
		seq = max(seq, last)
	}
	step := func(statement string, want int) {
		t.Helper()
		mustExec(t, db, statement)
		count(statement, want)
	}

	insertLadoResolveRow(t, db)
	count("resolve insert", 1)
	step(`UPDATE lado_pr_resolves SET spawn_request = 'lado:entry/_resolve/1', spawn_expires_at = CURRENT_TIMESTAMP, spawns = 1`, 0)
	step(`UPDATE lado_pr_resolves SET spawn_request = '', spawn_expires_at = NULL, state = 'resolving', session_id = 'w1'`, 1)
	step(`UPDATE lado_pr_resolves SET done_seen_at = CURRENT_TIMESTAMP, notified_head_sha = 'abc', version = version + 1`, 0)
	step(`UPDATE lado_pr_resolves SET attempts = 1`, 1)
	step(`UPDATE lado_pr_resolves SET error = 'push rejected'`, 1)
	step(`UPDATE lado_pr_resolves SET state = 'needs_human'`, 1)

	insertLadoVerifyRow(t, db, "lado_integration_approvals")
	count("approval", 1)
	step(`UPDATE lado_integration_approvals SET approved_base_sha = 'abc'`, 0)
	step(`UPDATE lado_integration_approvals SET revoked_at = CURRENT_TIMESTAMP, revoked_reason = 'base moved'`, 1)
}

// The down migration removes only what 0013 added, keeping 0012's tables and
// the approvals written before it, and the round trip adds it again.
func TestLadoMigrate0013RoundTripKeeps0012(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	insertLadoExecutionFixture(t, db)
	insertLadoVerifyRow(t, db, "lado_integration_approvals")
	mustExec(t, db, `INSERT INTO lado_alerts (task_id, kind, created_at) VALUES ('task', 'task_cost', CURRENT_TIMESTAMP)`)
	insertLadoResolveRow(t, db)
	mustExec(t, db, `UPDATE lado_integration_approvals SET approved_base_sha = 'abc', revoked_at = CURRENT_TIMESTAMP`)

	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 12); err != nil {
		t.Fatalf("down to 0012: %v", err)
	}
	if tableExists(t, db, "lado_pr_resolves") {
		t.Fatal("lado_pr_resolves survived the 0013 down migration")
	}
	keeps0012 := func(step string) {
		t.Helper()
		for _, table := range ladoWatchTables {
			if !tableExists(t, db, table.name) {
				t.Fatalf("%s: table %s is missing", step, table.name)
			}
		}
		var alerts, approvals int
		if err := db.QueryRow(`SELECT COUNT(*) FROM lado_alerts`).Scan(&alerts); err != nil || alerts != 1 {
			t.Fatalf("%s: alerts = %d, %v", step, alerts, err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM lado_integration_approvals WHERE id = 'integration'`).Scan(&approvals); err != nil || approvals != 1 {
			t.Fatalf("%s: approvals = %d, %v", step, approvals, err)
		}
	}
	keeps0012("down")
	if got := tableColumns(t, db, "lado_integration_approvals"); !slices.Equal(got, ladoVerifyTables[4].columns[:14]) {
		t.Fatalf("approval columns after the down migration = %v", got)
	}
	if got := appliedVersion(t, db, ladoVersionTable); got != 12 {
		t.Fatalf("%s after the down migration = %d, want 12", ladoVersionTable, got)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	keeps0012("up again")
	if got := tableColumns(t, db, "lado_pr_resolves"); !slices.Equal(got, ladoResolveColumns) {
		t.Fatalf("lado_pr_resolves columns after the round trip = %v", got)
	}
	var revoked sql.NullTime
	var base string
	if err := db.QueryRow(`SELECT approved_base_sha, revoked_at FROM lado_integration_approvals`).Scan(&base, &revoked); err != nil || base != "" || revoked.Valid {
		t.Fatalf("approval after the round trip = %q %v, %v; want not revoked", base, revoked, err)
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
}
