package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"testing"
)

// The test file keeps four top-level tests: the sqlite-race CI shards split
// this package's test list by position (knowledge note sqlite-race-shard-shift).

// insertLadoLogWatchFixture writes project "p" with a settings row, source
// "src", target "tgt" and fingerprint 1 of task "task".
func insertLadoLogWatchFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO lado_tasks (id, project_id, issue_repo, created_at, updated_at, source)
		VALUES ('task', 'p', 'github:o/r', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'log')`)
	mustExec(t, db, `INSERT INTO lado_log_watch_settings (project_id, updated_at) VALUES ('p', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_log_sources (id, project_id, kind, display_name, endpoint, created_at, updated_at)
		VALUES ('src', 'p', 'docker_tcp', 'prod1', 'tcp://192.0.2.10:2375', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_log_targets (id, source_id, kind, value, created_at, updated_at)
		VALUES ('tgt', 'src', 'compose_project', 'grp', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_log_cursors (target_id, instance, since, updated_at)
		VALUES ('tgt', 'grp-api-1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_log_fingerprints (id, project_id, fingerprint, count, first_seen_at, last_seen_at, task_id, created_at, updated_at)
		VALUES (1, 'p', 'fp', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'task', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_log_instance_counts (fingerprint_id, instance, count, last_seen_at)
		VALUES (1, 'prod1/grp-api-1', 1, CURRENT_TIMESTAMP)`)
}

func countRows(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// 0017 adds lado_tasks.source, ” for the tasks that were there, and the six
// log watch tables with their foreign keys.
func TestLadoMigrate0017CreatesLogWatchTables(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want || got < 17 {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 16); err != nil {
		t.Fatalf("down to 0016: %v", err)
	}
	mustExec(t, db, `INSERT INTO lado_tasks (id, project_id, issue_repo, issue_ref, created_at, updated_at)
		VALUES ('old', 'p', 'github:o/r', 'github:o/r#1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to 0017: %v", err)
	}
	var source string
	if err := db.QueryRow(`SELECT source FROM lado_tasks WHERE id = 'old'`).Scan(&source); err != nil || source != "" {
		t.Fatalf("existing task source = %q, %v; want ''", source, err)
	}
	for table, columns := range map[string][]string{
		"lado_log_watch_settings": {"project_id", "enabled", "repository", "include_json", "exclude_json", "hourly_issue_cap",
			"auto_plan", "auto_plan_daily_cap", "auto_plan_min_count", "disconnect_alert_minutes", "burst_notified_at", "updated_at"},
		"lado_log_sources": {"id", "project_id", "kind", "display_name", "endpoint", "tls", "cert_key", "paused", "position",
			"down_since", "down_notified_at", "created_at", "updated_at"},
		"lado_log_targets": {"id", "source_id", "kind", "value", "compose_project", "service", "paused", "position", "created_at", "updated_at"},
		"lado_log_cursors": {"target_id", "instance", "since", "updated_at"},
		"lado_log_fingerprints": {"id", "project_id", "fingerprint", "service", "exception_kind", "location", "count",
			"first_seen_at", "last_seen_at", "task_id", "issue_ref", "previous_issue_ref", "issue_created_at",
			"auto_plan_requested_at", "auto_plan_held", "ignored", "ignored_at", "created_at", "updated_at"},
		"lado_log_instance_counts": {"fingerprint_id", "instance", "image_tag", "count", "last_seen_at"},
	} {
		if got := tableColumns(t, db, table); !slices.Equal(got, columns) {
			t.Errorf("%s columns = %v, want %v", table, got, columns)
		}
	}
	for table, want := range map[string]string{
		"lado_log_sources":         "lado_log_watch_settings CASCADE",
		"lado_log_targets":         "lado_log_sources CASCADE",
		"lado_log_cursors":         "lado_log_targets CASCADE",
		"lado_log_fingerprints":    "lado_tasks SET NULL",
		"lado_log_instance_counts": "lado_log_fingerprints CASCADE",
	} {
		var target, onDelete string
		if err := db.QueryRow(`SELECT "table", on_delete FROM pragma_foreign_key_list(?)`, table).Scan(&target, &onDelete); err != nil ||
			target+" "+onDelete != want {
			t.Errorf("%s foreign key = %q %q, %v; want %s", table, target, onDelete, err, want)
		}
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_list('lado_log_watch_settings')`); got != 0 {
		t.Errorf("lado_log_watch_settings has %d foreign keys, want none", got)
	}
}

// The checks refuse unknown values, one fingerprint per project, and the
// foreign keys remove a target's cursors with it and let a pending task go
// while its fingerprint stays.
func TestLadoMigrate0017ChecksAndDeletes(t *testing.T) {
	db := openLadoDatabase(t, 1)
	insertLadoLogWatchFixture(t, db)
	for name, query := range map[string]string{
		"an unknown task source":    `UPDATE lado_tasks SET source = 'cron'`,
		"an unknown target kind":    `UPDATE lado_log_targets SET kind = 'regex'`,
		"an empty target value":     `UPDATE lado_log_targets SET value = ''`,
		"an unknown hold reason":    `UPDATE lado_log_fingerprints SET auto_plan_held = 'maybe'`,
		"a hourly cap of 0":         `UPDATE lado_log_watch_settings SET hourly_issue_cap = 0`,
		"include that is no array":  `UPDATE lado_log_watch_settings SET include_json = '{}'`,
		"a source of no settings":   `INSERT INTO lado_log_sources (id, project_id, kind, display_name, endpoint, created_at, updated_at) VALUES ('s2', 'q', 'docker_tcp', 'x', 'tcp://h:1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"a second fingerprint":      `INSERT INTO lado_log_fingerprints (project_id, fingerprint, first_seen_at, last_seen_at, created_at, updated_at) VALUES ('p', 'fp', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"a cursor of no target":     `INSERT INTO lado_log_cursors (target_id, instance, since, updated_at) VALUES ('nope', 'i', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"a fingerprint of no task":  `UPDATE lado_log_fingerprints SET task_id = 'nope'`,
		"a negative instance count": `UPDATE lado_log_instance_counts SET count = -1`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	mustExec(t, db, `INSERT INTO lado_log_fingerprints (project_id, fingerprint, first_seen_at, last_seen_at, created_at, updated_at)
		VALUES ('other', 'fp', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)

	mustExec(t, db, `DELETE FROM lado_log_targets WHERE id = 'tgt'`)
	if got := countRows(t, db, `SELECT COUNT(*) FROM lado_log_cursors`); got != 0 {
		t.Fatalf("cursors after the target went = %d, want 0", got)
	}
	mustExec(t, db, `DELETE FROM lado_tasks WHERE id = 'task' AND issue_ref IS NULL`)
	var task sql.NullString
	if err := db.QueryRow(`SELECT task_id FROM lado_log_fingerprints WHERE id = 1`).Scan(&task); err != nil || task.Valid {
		t.Fatalf("fingerprint task after the task went = %v, %v; want NULL", task, err)
	}
	mustExec(t, db, `DELETE FROM lado_log_watch_settings WHERE project_id = 'p'`)
	if got := countRows(t, db, `SELECT COUNT(*) FROM lado_log_sources`); got != 0 {
		t.Fatalf("sources after the settings went = %d, want 0", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM lado_log_fingerprints`); got != 2 {
		t.Fatalf("fingerprints after the settings went = %d, want 2", got)
	}
	mustExec(t, db, `DELETE FROM lado_log_fingerprints WHERE id = 1`)
	if got := countRows(t, db, `SELECT COUNT(*) FROM lado_log_instance_counts`); got != 0 {
		t.Fatalf("instance counts after the fingerprint went = %d, want 0", got)
	}
}

// A fingerprint publishes a lado_log_watch change when it is recorded and when
// what the incidents read changes; the settings when they are saved and a
// source when its connection state changes. Cursors and instance counts
// publish nothing.
func TestLadoMigrate0017ChangeTriggers(t *testing.T) {
	db := openLadoDatabase(t, 1)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/p', CURRENT_TIMESTAMP)`)
	var seq int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	type change struct {
		fingerprint sql.NullInt64
		op          string
	}
	step := func(statement string, want ...change) {
		t.Helper()
		mustExec(t, db, statement)
		rows, err := db.Query(`SELECT seq, json_extract(payload, '$.fingerprintId'), json_extract(payload, '$.op')
			FROM change_log
			WHERE seq > ? AND project_id = 'p' AND session_id IS NULL AND event_type = 'session_updated'
			AND json_extract(payload, '$.kind') = 'lado_log_watch' AND json_extract(payload, '$.projectId') = 'p'
			ORDER BY seq`, seq)
		if err != nil {
			t.Fatalf("%s: read change_log: %v", statement, err)
		}
		defer func() { _ = rows.Close() }()
		var got []change
		for rows.Next() {
			var c change
			if err := rows.Scan(&seq, &c.fingerprint, &c.op); err != nil {
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
	}
	settings := change{op: "settings"}
	fp := func(op string) change { return change{fingerprint: sql.NullInt64{Int64: 1, Valid: true}, op: op} }

	step(`INSERT INTO lado_log_watch_settings (project_id, updated_at) VALUES ('p', CURRENT_TIMESTAMP)`, settings)
	step(`UPDATE lado_log_watch_settings SET enabled = 1, updated_at = CURRENT_TIMESTAMP`, settings)
	step(`INSERT INTO lado_log_sources (id, project_id, kind, display_name, endpoint, created_at, updated_at)
		VALUES ('src', 'p', 'docker_tcp', 'prod1', 'tcp://192.0.2.10:2375', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	step(`UPDATE lado_log_sources SET down_since = CURRENT_TIMESTAMP`, settings)
	step(`UPDATE lado_log_sources SET cert_key = 'abc'`, settings)
	step(`UPDATE lado_log_sources SET updated_at = '2000-01-01 00:00:00'`)
	step(`INSERT INTO lado_log_targets (id, source_id, kind, value, created_at, updated_at)
		VALUES ('tgt', 'src', 'name', 'api', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	step(`INSERT INTO lado_log_cursors (target_id, instance, since, updated_at) VALUES ('tgt', 'api', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	step(`INSERT INTO lado_log_fingerprints (id, project_id, fingerprint, count, first_seen_at, last_seen_at, created_at, updated_at)
		VALUES (1, 'p', 'fp', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, fp("insert"))
	step(`INSERT INTO lado_log_instance_counts (fingerprint_id, instance, count, last_seen_at) VALUES (1, 'prod1/api', 1, CURRENT_TIMESTAMP)`)
	step(`UPDATE lado_log_fingerprints SET count = 2`, fp("update"))
	step(`UPDATE lado_log_fingerprints SET ignored = 1`, fp("update"))
	step(`UPDATE lado_log_fingerprints SET updated_at = '2000-01-01 00:00:00'`)
	step(`INSERT INTO lado_log_fingerprints (project_id, fingerprint, first_seen_at, last_seen_at, created_at, updated_at)
		VALUES ('unregistered', 'fp', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
}

// The down migration drops the log watch tables and lado_tasks.source,
// keeping the tasks and 0016's table, and the round trip adds them again.
func TestLadoMigrate0017RoundTripKeeps0016(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	insertLadoLogWatchFixture(t, db)

	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 16); err != nil {
		t.Fatalf("down to 0016: %v", err)
	}
	for _, table := range []string{"lado_log_watch_settings", "lado_log_sources", "lado_log_targets", "lado_log_cursors",
		"lado_log_fingerprints", "lado_log_instance_counts"} {
		if tableExists(t, db, table) {
			t.Fatalf("%s survived the 0017 down migration", table)
		}
	}
	if slices.Contains(tableColumns(t, db, "lado_tasks"), "source") {
		t.Fatal("lado_tasks.source survived the 0017 down migration")
	}
	if !tableExists(t, db, "lado_reported_pr_refusals") {
		t.Fatal("the 0017 down migration dropped 0016's lado_reported_pr_refusals")
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM lado_tasks`); got != 1 {
		t.Fatalf("tasks after the down migration = %d, want 1", got)
	}
	if got := appliedVersion(t, db, ladoVersionTable); got != 16 {
		t.Fatalf("%s after the down migration = %d, want 16", ladoVersionTable, got)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	mustExec(t, db, `DELETE FROM lado_tasks`)
	insertLadoLogWatchFixture(t, db)
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
}
