package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"testing"
)

// LADO fork: migration 0014 (lado_relays, issue #49). The plan named it 0013,
// which 0013_lado_resolve.sql had already taken.

// ladoRelayColumns are lado_relays' columns, in order.
var ladoRelayColumns = []string{
	"id", "task_id", "project_id", "orchestrator_session_id", "worker_session_id", "worker_role", "part_id",
	"instruction", "evidence", "evidence_message_id", "evidence_at", "beyond_evidence", "outside_plan",
	"outside_plan_reasons_json", "state", "failure", "created_at", "delivered_at",
}

// insertLadoRelayRow writes relay id of task "task" to worker "session" as
// sending.
func insertLadoRelayRow(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO lado_relays (id, task_id, project_id, orchestrator_session_id, worker_session_id,
			instruction, evidence, evidence_message_id, evidence_at, created_at)
		VALUES (?, 'task', 'p', 'orchestrator', 'session', 'go on', 'go on please', 'msg', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, id)
}

// 0014 creates lado_relays with its checks and without a key to upstream
// tables; a new relay is sending with no marks.
func TestLadoMigrate0014CreatesRelayTable(t *testing.T) {
	db := openLadoDatabase(t, 1)
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want || got < 14 {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	if got := tableColumns(t, db, "lado_relays"); !slices.Equal(got, ladoRelayColumns) {
		t.Fatalf("lado_relays columns = %v, want %v", got, ladoRelayColumns)
	}
	var upstreamKeys int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list('lado_relays') WHERE substr("table", 1, 5) <> 'lado_'`).Scan(&upstreamKeys); err != nil {
		t.Fatalf("read foreign keys: %v", err)
	}
	if upstreamKeys != 0 {
		t.Fatalf("lado_relays has %d FOREIGN KEYs to upstream tables, want none", upstreamKeys)
	}
	var index int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'lado_relays_by_task'`).Scan(&index); err != nil || index != 1 {
		t.Fatalf("lado_relays_by_task = %d, %v", index, err)
	}

	insertLadoExecutionFixture(t, db)
	insertLadoRelayRow(t, db, "relay")
	var state, role, reasons, failure string
	var beyond, outside int
	var delivered sql.NullTime
	if err := db.QueryRow(`SELECT state, worker_role, outside_plan_reasons_json, failure, beyond_evidence, outside_plan, delivered_at FROM lado_relays`).
		Scan(&state, &role, &reasons, &failure, &beyond, &outside, &delivered); err != nil {
		t.Fatalf("read relay: %v", err)
	}
	if state != "sending" || role != "" || reasons != "[]" || failure != "" || beyond != 0 || outside != 0 || delivered.Valid {
		t.Fatalf("new relay = %q %q %q %q %d %d %v", state, role, reasons, failure, beyond, outside, delivered)
	}

	mustExec(t, db, `UPDATE lado_relays SET state = 'delivered', delivered_at = CURRENT_TIMESTAMP`)
	mustExec(t, db, `UPDATE lado_relays SET worker_role = 'integration', outside_plan = 1, beyond_evidence = 1, outside_plan_reasons_json = '["x"]'`)
	for name, query := range map[string]string{
		"an unknown state":                `UPDATE lado_relays SET state = 'queued'`,
		"delivered without a time":        `UPDATE lado_relays SET delivered_at = NULL`,
		"a delivery time while sending":   `UPDATE lado_relays SET state = 'sending'`,
		"an unknown role":                 `UPDATE lado_relays SET worker_role = 'plan'`,
		"a beyond-evidence mark of 2":     `UPDATE lado_relays SET beyond_evidence = 2`,
		"an outside-plan mark of 2":       `UPDATE lado_relays SET outside_plan = 2`,
		"reasons that are not an array":   `UPDATE lado_relays SET outside_plan_reasons_json = '{}'`,
		"reasons that are not JSON":       `UPDATE lado_relays SET outside_plan_reasons_json = 'x'`,
		"an empty instruction":            `UPDATE lado_relays SET instruction = ''`,
		"empty evidence":                  `UPDATE lado_relays SET evidence = ''`,
		"no evidence message":             `UPDATE lado_relays SET evidence_message_id = ''`,
		"no worker":                       `UPDATE lado_relays SET worker_session_id = ''`,
		"no orchestrator":                 `UPDATE lado_relays SET orchestrator_session_id = ''`,
		"no project":                      `UPDATE lado_relays SET project_id = ''`,
		"a relay of no task":              `UPDATE lado_relays SET task_id = 'missing'`,
		"a second relay with the same id": `INSERT INTO lado_relays (id, task_id, project_id, orchestrator_session_id, worker_session_id, instruction, evidence, evidence_message_id, evidence_at, created_at) VALUES ('relay', 'task', 'p', 'o', 's', 'i', 'e', 'm', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	mustExec(t, db, `UPDATE lado_relays SET state = 'failed', delivered_at = NULL, failure = 'agent exited'`)
}

// A relay publishes its task's change when it is recorded and when its state
// changes; nothing else about it publishes.
func TestLadoMigrate0014ChangeTriggers(t *testing.T) {
	db := openLadoDatabase(t, 1)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/p', CURRENT_TIMESTAMP)`)
	insertLadoExecutionFixture(t, db)
	for _, name := range []string{"lado_cdc_relays_insert", "lado_cdc_relays_update"} {
		if !triggerExists(t, db, name) {
			t.Fatalf("trigger %s is missing", name)
		}
	}
	var seq int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	count := func(step string, want int) {
		t.Helper()
		var got int
		var last int64
		if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(seq), ?) FROM change_log
			WHERE seq > ? AND project_id = 'p' AND session_id IS NULL AND event_type = 'session_updated'
			  AND json_extract(payload, '$.kind') = 'lado_task' AND json_extract(payload, '$.taskId') = 'task'`, seq, seq).Scan(&got, &last); err != nil {
			t.Fatalf("%s: read change_log: %v", step, err)
		}
		var all int
		if err := db.QueryRow(`SELECT COUNT(*) FROM change_log WHERE seq > ?`, seq).Scan(&all); err != nil {
			t.Fatalf("%s: read change_log: %v", step, err)
		}
		if got != want || all != want {
			t.Fatalf("%s: %d task changes of %d, want %d", step, got, all, want)
		}
		seq = max(seq, last)
	}
	step := func(statement string, want int) {
		t.Helper()
		mustExec(t, db, statement)
		count(statement, want)
	}

	insertLadoRelayRow(t, db, "relay")
	count("relay insert", 1)
	step(`UPDATE lado_relays SET outside_plan = 1, outside_plan_reasons_json = '["x"]'`, 0)
	step(`UPDATE lado_relays SET state = 'delivered', delivered_at = CURRENT_TIMESTAMP`, 1)
	insertLadoRelayRow(t, db, "relay-2")
	count("second relay insert", 1)
	step(`UPDATE lado_relays SET state = 'failed', failure = 'agent exited' WHERE id = 'relay-2'`, 1)
}

// The down migration removes only lado_relays, keeping 0013's table and its
// rows, and the round trip adds it again.
func TestLadoMigrate0014RoundTripKeeps0013(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	insertLadoExecutionFixture(t, db)
	insertLadoResolveRow(t, db)
	insertLadoRelayRow(t, db, "relay")

	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 13); err != nil {
		t.Fatalf("down to 0013: %v", err)
	}
	if tableExists(t, db, "lado_relays") {
		t.Fatal("lado_relays survived the 0014 down migration")
	}
	keeps0013 := func(step string) {
		t.Helper()
		var resolves int
		if err := db.QueryRow(`SELECT COUNT(*) FROM lado_pr_resolves`).Scan(&resolves); err != nil || resolves != 1 {
			t.Fatalf("%s: resolves = %d, %v", step, resolves, err)
		}
	}
	keeps0013("down")
	if got := appliedVersion(t, db, ladoVersionTable); got != 13 {
		t.Fatalf("%s after the down migration = %d, want 13", ladoVersionTable, got)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	keeps0013("up again")
	if got := tableColumns(t, db, "lado_relays"); !slices.Equal(got, ladoRelayColumns) {
		t.Fatalf("lado_relays columns after the round trip = %v", got)
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
}
