package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"testing"
)

// The test file keeps four top-level tests: the sqlite-race CI shards split
// this package's test list by position (knowledge note sqlite-race-shard-shift).

// ladoLogWatchTriggers are 0017's change triggers, which 0018 leaves alone.
var ladoLogWatchTriggers = []string{
	"lado_cdc_log_fingerprints_insert", "lado_cdc_log_fingerprints_update",
	"lado_cdc_log_watch_settings_insert", "lado_cdc_log_watch_settings_update",
	"lado_cdc_log_sources_update",
}

func ladoLogTargetRepository(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var repo string
	if err := db.QueryRow(`SELECT repository FROM lado_log_targets WHERE id = ?`, id).Scan(&repo); err != nil {
		t.Fatalf("read the repository of target %s: %v", id, err)
	}
	return repo
}

// 0018 adds lado_log_targets.repository, NOT NULL with the empty string as
// default, so the targets that were there read empty and inherit the
// project's repository.
func TestLadoMigrate0018ExistingTargetsReadEmptyRepository(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want || got < 18 {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 17); err != nil {
		t.Fatalf("down to 0017: %v", err)
	}
	insertLadoLogWatchFixture(t, db)
	if _, err := provider.UpTo(ctx, 18); err != nil {
		t.Fatalf("up to 0018: %v", err)
	}
	if got := appliedVersion(t, db, ladoVersionTable); got != 18 {
		t.Fatalf("%s after up to 0018 = %d, want 18", ladoVersionTable, got)
	}
	if got := ladoLogTargetRepository(t, db, "tgt"); got != "" {
		t.Fatalf("existing target repository = %q, want ''", got)
	}
	var notNull int
	var dflt sql.NullString
	if err := db.QueryRow(`SELECT "notnull", dflt_value FROM pragma_table_info('lado_log_targets') WHERE name = 'repository'`).
		Scan(&notNull, &dflt); err != nil || notNull != 1 || dflt.String != "''" {
		t.Fatalf("lado_log_targets.repository = notnull %d, default %v, %v; want NOT NULL DEFAULT ''", notNull, dflt, err)
	}
	if _, err := db.Exec(`UPDATE lado_log_targets SET repository = NULL`); err == nil {
		t.Fatal("a NULL target repository was accepted")
	}
}

// The down migration drops only lado_log_targets.repository: 0017's tables,
// their rows and the log watch change triggers stay. Going down to 0017 also
// undoes the migrations above 0018 (0019 and later).
func TestLadoMigrate0018DownKeeps0017(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	insertLadoLogWatchFixture(t, db)
	mustExec(t, db, `UPDATE lado_log_targets SET repository = 'github:o/web' WHERE id = 'tgt'`)

	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 17); err != nil {
		t.Fatalf("down to 0017: %v", err)
	}
	if got := appliedVersion(t, db, ladoVersionTable); got != 17 {
		t.Fatalf("%s after the down migration = %d, want 17", ladoVersionTable, got)
	}
	want := []string{"id", "source_id", "kind", "value", "compose_project", "service", "paused", "position", "created_at", "updated_at"}
	if got := tableColumns(t, db, "lado_log_targets"); !slices.Equal(got, want) {
		t.Fatalf("lado_log_targets columns after the down migration = %v, want %v", got, want)
	}
	for table, want := range map[string]int{
		"lado_tasks": 1, "lado_log_watch_settings": 1, "lado_log_sources": 1, "lado_log_targets": 1,
		"lado_log_cursors": 1, "lado_log_fingerprints": 1, "lado_log_instance_counts": 1,
	} {
		if got := countRows(t, db, `SELECT COUNT(*) FROM `+table); got != want {
			t.Errorf("%s rows after the down migration = %d, want %d", table, got, want)
		}
	}
	for _, trigger := range ladoLogWatchTriggers {
		if !triggerExists(t, db, trigger) {
			t.Errorf("%s did not survive the 0018 down migration", trigger)
		}
	}
	mustExec(t, db, `DELETE FROM lado_log_targets WHERE id = 'tgt'`)
	if got := countRows(t, db, `SELECT COUNT(*) FROM lado_log_cursors`); got != 0 {
		t.Fatalf("cursors after the target went = %d, want 0", got)
	}
}

// After 0018 a settings save that writes target repositories still publishes
// one lado_log_watch change: the settings row's, not one per target.
func TestLadoMigrate0018SettingsSavePublishesOnce(t *testing.T) {
	db := openLadoDatabase(t, 1)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/p', CURRENT_TIMESTAMP)`)
	insertLadoLogWatchFixture(t, db)
	var seq int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&seq); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE lado_log_watch_settings SET repository = 'github:o/api', updated_at = '2026-11-10 09:00:00' WHERE project_id = 'p'`,
		`UPDATE lado_log_targets SET repository = 'github:o/web', updated_at = '2026-11-10 09:00:00' WHERE id = 'tgt'`,
		`INSERT INTO lado_log_targets (id, source_id, kind, value, repository, created_at, updated_at)
			VALUES ('tgt-2', 'src', 'name', 'worker', 'github:o/api', '2026-11-10 09:00:00', '2026-11-10 09:00:00')`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			_ = tx.Rollback()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Query(`SELECT json_extract(payload, '$.op'), json_extract(payload, '$.fingerprintId') IS NULL
		FROM change_log
		WHERE seq > ? AND project_id = 'p' AND json_extract(payload, '$.kind') = 'lado_log_watch'
		ORDER BY seq`, seq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ops []string
	for rows.Next() {
		var op string
		var settings bool
		if err := rows.Scan(&op, &settings); err != nil {
			t.Fatal(err)
		}
		if !settings {
			op += " with a fingerprint"
		}
		ops = append(ops, op)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ops, []string{"settings"}) {
		t.Fatalf("changes of one settings save = %v, want [settings]", ops)
	}
	if got := ladoLogTargetRepository(t, db, "tgt-2"); got != "github:o/api" {
		t.Fatalf("new target repository = %q, want github:o/api", got)
	}
}

// Down and up again brings the column back with its default, and targets can
// be written with a repository again.
func TestLadoMigrate0018RoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	insertLadoLogWatchFixture(t, db)
	mustExec(t, db, `UPDATE lado_log_targets SET repository = 'github:o/web' WHERE id = 'tgt'`)

	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 17); err != nil {
		t.Fatalf("down to 0017: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	if got := ladoLogTargetRepository(t, db, "tgt"); got != "" {
		t.Fatalf("target repository after the round trip = %q, want '' (the down migration dropped it)", got)
	}
	mustExec(t, db, `INSERT INTO lado_log_targets (id, source_id, kind, value, repository, created_at, updated_at)
		VALUES ('tgt-2', 'src', 'name', 'worker', 'github:o/api', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if got := ladoLogTargetRepository(t, db, "tgt-2"); got != "github:o/api" {
		t.Fatalf("target repository written after the round trip = %q, want github:o/api", got)
	}
}
