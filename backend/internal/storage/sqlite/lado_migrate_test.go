package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// ladoChangeTriggerFixture is a LADO table whose trigger writes change_log, the
// shape TDD §4.3 gives LADO change events.
const ladoChangeTriggerFixture = `
CREATE TABLE lado_fixture (id INTEGER PRIMARY KEY, project_id TEXT NOT NULL);
CREATE TRIGGER lado_cdc_fixture_insert
AFTER INSERT ON lado_fixture
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'session_updated', json_object('kind', 'lado_fixture'), datetime('now'));
END;`

const ladoAuditChangeTrigger = `
CREATE TRIGGER lado_cdc_audit_fixture
AFTER INSERT ON lado_audit_events
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.subject_id, NULL, 'session_updated', json_object('kind', 'lado_fixture'), datetime('now'));
END`

// useLadoCDCTriggers swaps the production trigger set for one test. The package
// runs its tests serially; do not combine this with t.Parallel.
func useLadoCDCTriggers(t *testing.T, statements ...string) {
	t.Helper()
	saved := ladoCDCTriggers
	ladoCDCTriggers = statements
	t.Cleanup(func() { ladoCDCTriggers = saved })
}

// openLadoDatabase runs the production Open path the given number of times on
// an upstream database already at the latest version (a full replay costs
// about 12s under -race) and returns a plain connection to the file.
func openLadoDatabase(t *testing.T, opens int) *sql.DB {
	t.Helper()
	upstream, err := expectedMigrationVersion()
	if err != nil {
		t.Fatalf("expected migration version: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ao.db"), migratedDatabaseSnapshot(t, upstream), 0o600); err != nil {
		t.Fatalf("seed upstream database: %v", err)
	}
	for range opens {
		openAndClose(t, dir)
	}
	return openLadoInspection(t, dir)
}

func openAndClose(t *testing.T, dir string) {
	t.Helper()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func openLadoInspection(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatalf("open inspection connection: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func appliedVersion(t *testing.T, db *sql.DB, versionTable string) int64 {
	t.Helper()
	var version int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(version_id), 0) FROM ` + versionTable + ` WHERE is_applied = 1`).Scan(&version); err != nil {
		t.Fatalf("read %s: %v", versionTable, err)
	}
	return version
}

// latestLadoMigration reads the highest NNNN prefix in migrations_lado/.
func latestLadoMigration(t *testing.T) int64 {
	t.Helper()
	entries, err := os.ReadDir("migrations_lado")
	if err != nil {
		t.Fatalf("read LADO migrations: %v", err)
	}
	var latest int64
	for _, entry := range entries {
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			t.Fatalf("LADO migration %s has no numeric prefix", entry.Name())
		}
		latest = max(latest, version)
	}
	if latest == 0 {
		t.Fatal("no LADO migrations found")
	}
	return latest
}

func triggerExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, name).Scan(&count); err != nil {
		t.Fatalf("look up trigger %s: %v", name, err)
	}
	return count == 1
}

func insertLadoAuditFixture(t *testing.T, db *sql.DB, subjectID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO lado_audit_events (at, actor, action, subject_kind, subject_id, payload_json, prev_sha256)
		VALUES (CURRENT_TIMESTAMP, 'system', 'fixture', 'project', ?, '{}', ?)`, subjectID, strings.Repeat("0", 64)); err != nil {
		t.Fatalf("insert audit event: %v", err)
	}
}

func upstreamUpTo(db *sql.DB, version int64) error {
	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return goose.UpTo(db, "migrations", version)
}

func TestOpenAppliesLadoTrackInItsOwnVersionTable(t *testing.T) {
	// The one fresh database in this file: every upstream migration, then LADO.
	dir := t.TempDir()
	openAndClose(t, dir)
	db := openLadoInspection(t, dir)
	upstream, err := expectedMigrationVersion()
	if err != nil {
		t.Fatalf("expected migration version: %v", err)
	}
	if got := appliedVersion(t, db, "goose_db_version"); got != upstream {
		t.Fatalf("goose_db_version = %d, want upstream's %d", got, upstream)
	}
	if got, want := appliedVersion(t, db, "lado_goose_db_version"), latestLadoMigration(t); got != want {
		t.Fatalf("lado_goose_db_version = %d, want %d", got, want)
	}
}

func TestMigrateAppliesLadoTrackAfterUpstreamOnUpgrade(t *testing.T) {
	upstream, err := expectedMigrationVersion()
	if err != nil {
		t.Fatalf("expected migration version: %v", err)
	}
	db := openMigratedDatabaseCopy(t, 206)
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := appliedVersion(t, db, "goose_db_version"); got != upstream {
		t.Fatalf("goose_db_version = %d, want %d", got, upstream)
	}
	if got, want := appliedVersion(t, db, "lado_goose_db_version"), latestLadoMigration(t); got != want {
		t.Fatalf("lado_goose_db_version = %d, want %d", got, want)
	}
}

func TestReopenLeavesLadoTrackUnchanged(t *testing.T) {
	db := openLadoDatabase(t, 2)
	var rows int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM lado_goose_db_version`).Scan(&rows); err != nil {
		t.Fatalf("count LADO versions: %v", err)
	}
	// goose records version 0 when it creates the table, then one row per migration.
	if want := latestLadoMigration(t) + 1; rows != want {
		t.Fatalf("lado_goose_db_version has %d rows after two opens, want %d", rows, want)
	}
}

func TestReopenKeepsTheAuditLogAppendOnly(t *testing.T) {
	db := openLadoDatabase(t, 2)
	insertLadoAuditFixture(t, db, "p")
	for _, statement := range []string{`UPDATE lado_audit_events SET actor = 'edited'`, `DELETE FROM lado_audit_events`} {
		if _, err := db.Exec(statement); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s after reopening: err = %v, want the append-only refusal", statement, err)
		}
	}
}

func TestOpenRecreatesLadoChangeTriggersAfterUpstream(t *testing.T) {
	useLadoCDCTriggers(t, ladoAuditChangeTrigger)

	db := openLadoDatabase(t, 2)
	if !triggerExists(t, db, "lado_cdc_audit_fixture") {
		t.Fatal("lado_cdc_audit_fixture missing after reopening")
	}
	if _, err := db.Exec(`INSERT INTO projects (id, path, registered_at) VALUES ('lado-project', '/lado-project', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	insertLadoAuditFixture(t, db, "lado-project")
	var events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM change_log WHERE json_extract(payload, '$.kind') = 'lado_fixture'`).Scan(&events); err != nil {
		t.Fatalf("count change events: %v", err)
	}
	if events != 1 {
		t.Fatalf("recreated trigger wrote %d change events, want 1", events)
	}

	// An upgrade creates it after upstream's pending migrations and the LADO track.
	upgraded := openMigratedDatabaseCopy(t, 206)
	if err := migrate(upgraded); err != nil {
		t.Fatalf("migrate from 206: %v", err)
	}
	if !triggerExists(t, upgraded, "lado_cdc_audit_fixture") {
		t.Fatal("lado_cdc_audit_fixture missing after upgrading from 206")
	}
}

func TestMigrateDropsLadoChangeTriggersBeforeUpstreamRebuilds(t *testing.T) {
	// 0103 rebuilds change_log by copy and rename, dropping only the triggers it knows.
	control := openMigratedDatabaseCopy(t, 102)
	if _, err := control.Exec(ladoChangeTriggerFixture); err != nil {
		t.Fatalf("seed control fixture: %v", err)
	}
	if err := upstreamUpTo(control, 103); err == nil {
		t.Fatal("upstream rebuilt change_log around a trigger it does not know; the LADO pre-step would be unnecessary")
	}

	db := openMigratedDatabaseCopy(t, 102)
	if _, err := db.Exec(ladoChangeTriggerFixture); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate with a LADO change trigger present: %v", err)
	}
	if triggerExists(t, db, "lado_cdc_fixture_insert") {
		t.Fatal("lado_cdc_fixture_insert survived; only ladoCDCTriggers may be recreated after upstream")
	}
}
