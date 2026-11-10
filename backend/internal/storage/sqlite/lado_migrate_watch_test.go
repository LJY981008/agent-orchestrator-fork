package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
)

// ladoWatchTables are the tables 0012 creates, with their columns in order
// on the latest migration.
var ladoWatchTables = []struct {
	name    string
	columns []string
}{
	{"lado_watch_settings", []string{"project_id", "compact_window", "task_cost_cents", "session_cost_cents", "session_compactions", "stall_minutes", "wake_enabled", "updated_at"}},
	{"lado_session_watch", []string{
		"session_id", "task_id", "read_offsets_json", "compactions", "compacted_at",
		"limit_state", "limit_reset_at", "limit_reset_guess", "limit_seen_at", "resumes", "resumed_at", "limit_stops",
		"stall_state", "stalled_at", "nudges", "nudged_at", "stalls", "created_at", "updated_at",
	}},
	// 0018 adds the identity and release columns.
	{"lado_account_limits", []string{
		"account_key", "session_id", "reset_at", "reset_guess", "detected_at", "updated_at",
		"identity", "released_at", "release_reason", "released_waiting",
	}},
	{"lado_alerts", []string{"id", "task_id", "session_id", "kind", "level", "value", "threshold", "created_at", "seen_at"}},
}

// A fresh database has the four tables, their checks hold, and they keep
// their foreign keys among LADO tables (TDD section 4.2).
func TestLadoMigrate0012CreatesWatchTables(t *testing.T) {
	db := openLadoDatabase(t, 1)
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want || got < 12 {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	for _, table := range ladoWatchTables {
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
	insertLadoSnapshotFixture(t, db)

	mustExec(t, db, `INSERT INTO lado_watch_settings (project_id, updated_at) VALUES ('', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_watch_settings (project_id, compact_window, task_cost_cents, session_cost_cents, session_compactions, stall_minutes, wake_enabled, updated_at)
		VALUES ('p', 100000, 1, 1, 1, 1, 0, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_session_watch (session_id, task_id, created_at, updated_at) VALUES ('s', 'task', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_account_limits (account_key, reset_at, detected_at, updated_at) VALUES ('claude:device-claude', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_alerts (task_id, kind, created_at) VALUES ('task', 'task_cost', CURRENT_TIMESTAMP)`)
	var offsets, session string
	if err := db.QueryRow(`SELECT read_offsets_json FROM lado_session_watch`).Scan(&offsets); err != nil || offsets != "{}" {
		t.Fatalf("new watch offsets = %q, %v", offsets, err)
	}
	if err := db.QueryRow(`SELECT session_id FROM lado_alerts`).Scan(&session); err != nil || session != "" {
		t.Fatalf("task alert session = %q, %v; want ''", session, err)
	}
	for name, query := range map[string]string{
		"a compact window below 100000":  `UPDATE lado_watch_settings SET compact_window = 99999 WHERE project_id = 'p'`,
		"a compact window above 1000000": `UPDATE lado_watch_settings SET compact_window = 1000001 WHERE project_id = 'p'`,
		"a zero task cost":               `UPDATE lado_watch_settings SET task_cost_cents = 0`,
		"a wake flag of 2":               `UPDATE lado_watch_settings SET wake_enabled = 2`,
		"a watch of no task":             `INSERT INTO lado_session_watch (session_id, task_id, created_at, updated_at) VALUES ('s2', 'missing', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"an empty watched session":       `INSERT INTO lado_session_watch (session_id, task_id, created_at, updated_at) VALUES ('', 'task', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"offsets that are not an object": `UPDATE lado_session_watch SET read_offsets_json = '[]'`,
		"an unknown limit state":         `UPDATE lado_session_watch SET limit_state = 'paused'`,
		"an unknown stall state":         `UPDATE lado_session_watch SET stall_state = 'asleep'`,
		"negative compactions":           `UPDATE lado_session_watch SET compactions = -1`,
		"an empty account":               `INSERT INTO lado_account_limits (account_key, reset_at, detected_at, updated_at) VALUES ('', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"a NULL alert session":           `INSERT INTO lado_alerts (task_id, session_id, kind, created_at) VALUES ('task', NULL, 'stalled', CURRENT_TIMESTAMP)`,
		"an alert of no task":            `INSERT INTO lado_alerts (task_id, kind, created_at) VALUES ('missing', 'stalled', CURRENT_TIMESTAMP)`,
		"an alert level of 0":            `INSERT INTO lado_alerts (task_id, kind, level, created_at) VALUES ('task', 'stalled', 0, CURRENT_TIMESTAMP)`,
		// The same task alert, with no session, raised again.
		"a second task alert": `INSERT INTO lado_alerts (task_id, kind, created_at) VALUES ('task', 'task_cost', CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// The change triggers write change_log with the session only when AO has it:
// a task alert, whose session is empty, and an alert of a gone session publish
// with a NULL session. A session watch publishes only when its limit state,
// stall state or compactions change.
func TestLadoMigrate0012ChangeTriggers(t *testing.T) {
	db := openLadoDatabase(t, 1)
	insertLadoSnapshotFixture(t, db)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/p', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, created_at, updated_at, activity_last_at)
		VALUES ('s', 'p', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	var seq int64
	changes := func() []string {
		t.Helper()
		rows, err := db.Query(`SELECT seq, COALESCE(session_id, '<null>'), json_extract(payload, '$.kind'), COALESCE(json_extract(payload, '$.op'), '')
			FROM change_log WHERE seq > ? AND json_extract(payload, '$.kind') GLOB 'lado_*' ORDER BY seq`, seq)
		if err != nil {
			t.Fatalf("read change_log: %v", err)
		}
		defer func() { _ = rows.Close() }()
		var got []string
		for rows.Next() {
			var session, kind, op string
			if err := rows.Scan(&seq, &session, &kind, &op); err != nil {
				t.Fatalf("scan change_log: %v", err)
			}
			got = append(got, strings.Join(strings.Fields(kind+" "+op+" "+session), " "))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return got
	}
	changes()
	expect := func(step string, want ...string) {
		t.Helper()
		if got := changes(); !slices.Equal(got, want) {
			t.Fatalf("%s: changes %q, want %q", step, got, want)
		}
	}

	mustExec(t, db, `INSERT INTO lado_alerts (task_id, kind, created_at) VALUES ('task', 'task_cost', CURRENT_TIMESTAMP)`)
	expect("task alert", "lado_alert insert <null>")
	mustExec(t, db, `INSERT INTO lado_alerts (task_id, session_id, kind, created_at) VALUES ('task', 's', 'stalled', CURRENT_TIMESTAMP)`)
	expect("session alert", "lado_alert insert s")
	mustExec(t, db, `INSERT INTO lado_alerts (task_id, session_id, kind, created_at) VALUES ('task', 'gone', 'stalled', CURRENT_TIMESTAMP)`)
	expect("alert of a gone session", "lado_alert insert <null>")
	mustExec(t, db, `UPDATE lado_alerts SET seen_at = CURRENT_TIMESTAMP WHERE session_id = 's'`)
	expect("seen", "lado_alert update s")
	mustExec(t, db, `UPDATE lado_alerts SET value = 7 WHERE session_id = 's'`)
	expect("value alone", nil...)

	mustExec(t, db, `INSERT INTO lado_session_watch (session_id, task_id, created_at, updated_at) VALUES ('s', 'task', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	expect("new watch", nil...)
	for _, step := range []struct {
		set  string
		want []string
	}{
		{`read_offsets_json = '{"a":1}', updated_at = CURRENT_TIMESTAMP`, nil},
		{`compactions = 1`, []string{"lado_watch s"}},
		{`limit_state = 'limited'`, []string{"lado_watch s"}},
		{`limit_reset_at = CURRENT_TIMESTAMP, resumes = 1, resumed_at = CURRENT_TIMESTAMP`, nil},
		{`stall_state = 'stalled'`, []string{"lado_watch s"}},
		{`nudges = 1, nudged_at = CURRENT_TIMESTAMP, stalled_at = CURRENT_TIMESTAMP`, nil},
	} {
		mustExec(t, db, `UPDATE lado_session_watch SET `+step.set)
		expect(step.set, step.want...)
	}
}

// The down migration drops only 0012's tables, keeping 0011's tables and
// rows, and the round trip creates them again.
func TestLadoMigrate0012RoundTripKeeps0011(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	insertLadoExecutionFixture(t, db)
	mustExec(t, db, `INSERT INTO lado_execution_timings (session_id, pr_url, pr_seen_at) VALUES ('session', 'https://github.com/o/r/pull/1', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_session_metrics (session_id, task_id, role, tool_calls, updated_at) VALUES ('session', 'task', 'worker', 5, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_alerts (task_id, kind, created_at) VALUES ('task', 'task_cost', CURRENT_TIMESTAMP)`)

	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 11); err != nil {
		t.Fatalf("down to 0011: %v", err)
	}
	for _, table := range ladoWatchTables {
		if tableExists(t, db, table.name) {
			t.Fatalf("table %s survived the 0012 down migration", table.name)
		}
	}
	keeps0011 := func(step string) {
		t.Helper()
		for _, table := range ladoTimingsTables {
			if !tableExists(t, db, table) {
				t.Fatalf("%s: table %s is missing", step, table)
			}
		}
		var url string
		var calls int
		if err := db.QueryRow(`SELECT pr_url FROM lado_execution_timings WHERE session_id = 'session'`).Scan(&url); err != nil || url != "https://github.com/o/r/pull/1" {
			t.Fatalf("%s: execution timings = %q, %v", step, url, err)
		}
		if err := db.QueryRow(`SELECT tool_calls FROM lado_session_metrics WHERE session_id = 'session'`).Scan(&calls); err != nil || calls != 5 {
			t.Fatalf("%s: session metrics = %d, %v", step, calls, err)
		}
	}
	keeps0011("down")
	if got := appliedVersion(t, db, ladoVersionTable); got != 11 {
		t.Fatalf("%s after the down migration = %d, want 11", ladoVersionTable, got)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	keeps0011("up again")
	for _, table := range ladoWatchTables {
		if !tableExists(t, db, table.name) {
			t.Fatalf("table %s missing after migrating up again", table.name)
		}
	}
	var alerts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM lado_alerts`).Scan(&alerts); err != nil || alerts != 0 {
		t.Fatalf("alerts after the round trip = %d, %v; want a new empty table", alerts, err)
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	var none sql.NullString
	if err := db.QueryRow(`SELECT MAX(project_id) FROM lado_watch_settings`).Scan(&none); err != nil || none.Valid {
		t.Fatalf("settings after the round trip = %v, %v; want none", none, err)
	}
}
