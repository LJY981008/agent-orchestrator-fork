package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count); err != nil {
		t.Fatalf("look up table %s: %v", name, err)
	}
	return count == 1
}

// insertLadoSnapshotFixture writes policy snapshot "snapshot" of approval
// "approval", with the task "task" (project "p"), request "request", revision
// "revision" and presence proof "nonce" an approval needs.
func insertLadoSnapshotFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	hash := strings.Repeat("a", 64)
	mustExec(t, db, `INSERT INTO lado_tasks (id, project_id, issue_repo, issue_ref, created_at, updated_at)
		VALUES ('task', 'p', 'github:o/r', 'github:o/r#1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_plan_requests (id, task_id, session_id, issue_snapshot_sha256, requested_at)
		VALUES ('request', 'task', 's', ?, CURRENT_TIMESTAMP)`, hash)
	mustExec(t, db, `INSERT INTO lado_plan_revisions (id, task_id, request_id, number, session_id, path, artifact_version, source, sha256, issue_snapshot_sha256, created_at)
		VALUES ('revision', 'task', 'request', 1, 's', 'plan.md', 1, 'agent', ?, ?, CURRENT_TIMESTAMP)`, hash, hash)
	mustExec(t, db, `INSERT INTO lado_presence_proofs (nonce, action, subject_id, expires_at, used_at)
		VALUES ('nonce', 'plan.approve', 'revision', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_plan_approvals (id, revision_id, task_id, sha256, proof_nonce, approved_at)
		VALUES ('approval', 'revision', 'task', ?, 'nonce', CURRENT_TIMESTAMP)`, hash)
	mustExec(t, db, `INSERT INTO lado_policy_snapshots (id, approval_id, sha256, snapshot_json, blocking, created_at)
		VALUES ('snapshot', 'approval', ?, '{}', 0, CURRENT_TIMESTAMP)`, hash)
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

func TestLadoMigrateCreatesRuleTables(t *testing.T) {
	db := openLadoDatabase(t, 1)
	if got := appliedVersion(t, db, ladoVersionTable); got < 5 {
		t.Fatalf("%s = %d, want 0005 applied", ladoVersionTable, got)
	}
	for _, name := range []string{"lado_rules", "lado_rule_versions", "lado_rule_bindings", "lado_policy_snapshots", "lado_policy_snapshot_rules"} {
		if !tableExists(t, db, name) {
			t.Fatalf("table %s missing after the LADO track", name)
		}
	}

	// A global binding is on or off; a project or task binding forces one
	// scope, and INHERIT is a missing row.
	mustExec(t, db, `INSERT INTO lado_rules (id, builtin, protected, created_at) VALUES ('rule', 0, 0, CURRENT_TIMESTAMP)`)
	insertBinding := func(scope, scopeID, state string) error {
		_, err := db.Exec(`INSERT INTO lado_rule_bindings (rule_id, scope, scope_id, state, updated_at)
			VALUES ('rule', ?, ?, ?, CURRENT_TIMESTAMP)`, scope, scopeID, state)
		return err
	}
	for _, row := range [][3]string{
		{"global", "x", "on"},
		{"project", "", "force_on"},
		{"task", "t", "on"},
		{"global", "", "force_on"},
		{"project", "p", "inherit"},
	} {
		if err := insertBinding(row[0], row[1], row[2]); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("binding %v: err = %v, want the CHECK refusal", row, err)
		}
	}
	for _, row := range [][3]string{{"global", "", "on"}, {"project", "p", "force_off"}} {
		if err := insertBinding(row[0], row[1], row[2]); err != nil {
			t.Fatalf("binding %v: %v", row, err)
		}
	}

	insertLadoSnapshotFixture(t, db)
	if _, err := db.Exec(`INSERT INTO lado_policy_snapshot_rules (snapshot_id, rule_id, rule_version_id)
		VALUES ('snapshot', 'rule', 'missing')`); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("snapshot rule naming a missing version: err = %v, want the FOREIGN KEY refusal", err)
	}
	mustExec(t, db, `INSERT INTO lado_rule_versions (id, rule_id, number, name, enforcement_json, sha256, actor, created_at)
		VALUES ('rule-v1', 'rule', 1, 'Rule', '["prompt"]', ?, 'lado', CURRENT_TIMESTAMP)`, strings.Repeat("a", 64))
	mustExec(t, db, `INSERT INTO lado_policy_snapshot_rules (snapshot_id, rule_id, rule_version_id)
		VALUES ('snapshot', 'rule', 'rule-v1')`)
}

func TestLadoMigrateCreatesQueueTables(t *testing.T) {
	db := openLadoDatabase(t, 1)
	if got := appliedVersion(t, db, ladoVersionTable); got < 6 {
		t.Fatalf("%s = %d, want 0006 applied", ladoVersionTable, got)
	}
	for _, name := range []string{"lado_queue_entries", "lado_executions", "lado_dispatch_settings"} {
		if !tableExists(t, db, name) {
			t.Fatalf("table %s missing after the LADO track", name)
		}
	}
	// Entries and executions publish through ladoCDCTriggers; the settings
	// tables publish nothing.
	for _, name := range []string{"lado_cdc_queue_entries_insert", "lado_cdc_queue_entries_update", "lado_cdc_executions_insert", "lado_cdc_executions_update"} {
		if !triggerExists(t, db, name) {
			t.Fatalf("trigger %s missing after open", name)
		}
	}
	var settingsTriggers int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND tbl_name IN ('lado_dispatch_settings', 'lado_project_settings')`).Scan(&settingsTriggers); err != nil {
		t.Fatalf("count settings triggers: %v", err)
	}
	if settingsTriggers != 0 {
		t.Fatalf("settings tables have %d triggers, want none", settingsTriggers)
	}

	// A project's cap defaults to 1, and the added column keeps its CHECK.
	mustExec(t, db, `INSERT INTO lado_project_settings (project_id, lado_mode, updated_at) VALUES ('p', 1, CURRENT_TIMESTAMP)`)
	var projectCap int
	if err := db.QueryRow(`SELECT max_concurrent FROM lado_project_settings WHERE project_id = 'p'`).Scan(&projectCap); err != nil {
		t.Fatalf("read project cap: %v", err)
	}
	if projectCap != 1 {
		t.Fatalf("lado_project_settings.max_concurrent = %d, want the default 1", projectCap)
	}
	for _, n := range []int{0, 9} {
		if _, err := db.Exec(`UPDATE lado_project_settings SET max_concurrent = ? WHERE project_id = 'p'`, n); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("project cap %d: err = %v, want the CHECK refusal", n, err)
		}
	}

	// Dispatch settings are a single row with a cap of 1-8.
	insertSettings := func(id, maxConcurrent int) error {
		_, err := db.Exec(`INSERT INTO lado_dispatch_settings (id, max_concurrent, max_run_minutes, hold_on_memory_tight, updated_at)
			VALUES (?, ?, 480, 1, CURRENT_TIMESTAMP)`, id, maxConcurrent)
		return err
	}
	for _, row := range [][2]int{{2, 2}, {1, 0}, {1, 9}} {
		if err := insertSettings(row[0], row[1]); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("dispatch settings id %d cap %d: err = %v, want the CHECK refusal", row[0], row[1], err)
		}
	}
	if err := insertSettings(1, 2); err != nil {
		t.Fatalf("dispatch settings: %v", err)
	}

	// One queue entry per approval, and one execution per entry.
	insertLadoSnapshotFixture(t, db)
	insertEntry := func(id, approvalID string) error {
		_, err := db.Exec(`INSERT INTO lado_queue_entries (id, task_id, project_id, plan_approval_id, policy_snapshot_id, enqueued_at)
			VALUES (?, 'task', 'p', ?, 'snapshot', CURRENT_TIMESTAMP)`, id, approvalID)
		return err
	}
	if err := insertEntry("entry-orphan", "missing"); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("entry naming a missing approval: err = %v, want the FOREIGN KEY refusal", err)
	}
	if err := insertEntry("entry", "approval"); err != nil {
		t.Fatalf("queue entry: %v", err)
	}
	if err := insertEntry("entry-again", "approval"); err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("second entry for one approval: err = %v, want the UNIQUE refusal", err)
	}
	insertExecution := func(sessionID string) error {
		_, err := db.Exec(`INSERT INTO lado_executions (session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id, attempt, started_at)
			VALUES (?, 'task', 'entry', 'revision', 'approval', 'snapshot', 1, CURRENT_TIMESTAMP)`, sessionID)
		return err
	}
	if err := insertExecution("session-1"); err != nil {
		t.Fatalf("execution: %v", err)
	}
	if err := insertExecution("session-2"); err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("second execution of one entry: err = %v, want the UNIQUE refusal", err)
	}
}

// ladoMigrationProvider opens the LADO track on db the way migrateLado does.
func ladoMigrationProvider(t *testing.T, db *sql.DB) *goose.Provider {
	t.Helper()
	migrations, err := fs.Sub(ladoMigrationsFS, "migrations_lado")
	if err != nil {
		t.Fatalf("open LADO migrations: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations,
		goose.WithTableName(ladoVersionTable),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		t.Fatalf("prepare LADO migrations: %v", err)
	}
	return provider
}

func TestLadoQueueMigrationRoundTrip(t *testing.T) {
	db := openLadoDatabase(t, 1)
	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(context.Background(), 5); err != nil {
		t.Fatalf("down to 0005: %v", err)
	}
	for _, name := range []string{"lado_queue_entries", "lado_executions", "lado_dispatch_settings"} {
		if tableExists(t, db, name) {
			t.Fatalf("table %s survived the 0006 down migration", name)
		}
	}
	if slices.Contains(tableColumns(t, db, "lado_project_settings"), "max_concurrent") {
		t.Fatal("lado_project_settings.max_concurrent survived the 0006 down migration")
	}

	// A project row written before 0006 gets the default cap on the way up.
	mustExec(t, db, `INSERT INTO lado_project_settings (project_id, lado_mode, updated_at) VALUES ('p', 1, CURRENT_TIMESTAMP)`)
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatalf("up again: %v", err)
	}
	var mode, projectCap int
	if err := db.QueryRow(`SELECT lado_mode, max_concurrent FROM lado_project_settings WHERE project_id = 'p'`).Scan(&mode, &projectCap); err != nil {
		t.Fatalf("read project settings: %v", err)
	}
	if mode != 1 || projectCap != 1 {
		t.Fatalf("project settings after the round trip = mode %d, cap %d; want mode 1, cap 1", mode, projectCap)
	}
}

func TestLadoMigrateCreatesSetupTables(t *testing.T) {
	db := openLadoDatabase(t, 1)
	if got := appliedVersion(t, db, ladoVersionTable); got < 7 {
		t.Fatalf("%s = %d, want 0007 applied", ladoVersionTable, got)
	}
	tables := []struct {
		name    string
		columns []string
	}{
		{"lado_setup_adoptions", []string{"id", "created_at", "actor", "os", "plan_sha256", "manifest_sha256", "state", "error_message", "restored_at", "restore_report"}},
		{"lado_setup_items", []string{"id", "tool", "kind", "name", "origin_path", "adoption_id", "created_at", "deleted_at", "restored_at"}},
		{"lado_setup_item_versions", []string{"id", "item_id", "version", "content_kind", "sha256", "size_bytes", "executable", "actor", "created_at"}},
		{"lado_setup_bindings", []string{"item_id", "scope", "scope_id", "state", "updated_at"}},
		{"lado_conflict_acceptances", []string{"id", "code", "item_id", "detail", "content_sha256", "scope", "scope_id", "actor", "created_at", "revoked_at"}},
		{"lado_policy_snapshot_setup", []string{"snapshot_id", "item_version_id"}},
	}
	for _, table := range tables {
		if got := tableColumns(t, db, table.name); !slices.Equal(got, table.columns) {
			t.Fatalf("%s columns = %v, want %v", table.name, got, table.columns)
		}
		// Foreign keys stay among LADO tables (TDD section 4.2).
		var upstreamKeys int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list(?) WHERE substr("table", 1, 5) <> 'lado_'`, table.name).Scan(&upstreamKeys); err != nil {
			t.Fatalf("read %s foreign keys: %v", table.name, err)
		}
		if upstreamKeys != 0 {
			t.Fatalf("%s has %d FOREIGN KEYs to upstream tables, want none", table.name, upstreamKeys)
		}
	}

	// One live item per tool, kind and name: a deleted or restored item frees
	// its name, and the same name in another kind is another item.
	hash := strings.Repeat("a", 64)
	mustExec(t, db, `INSERT INTO lado_setup_adoptions (id, created_at, actor, os, plan_sha256, state)
		VALUES ('adoption', CURRENT_TIMESTAMP, 'human', 'linux', ?, 'completed')`, hash)
	insertItem := func(id, kind string, adoptionID any) error {
		_, err := db.Exec(`INSERT INTO lado_setup_items (id, tool, kind, name, origin_path, adoption_id, created_at)
			VALUES (?, 'claude-code', ?, 'review', '/home/u/.claude/skills/review', ?, CURRENT_TIMESTAMP)`, id, kind, adoptionID)
		return err
	}
	if err := insertItem("item-orphan", "skill", "missing"); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("item naming a missing adoption: err = %v, want the FOREIGN KEY refusal", err)
	}
	if err := insertItem("item", "skill", "adoption"); err != nil {
		t.Fatalf("adopted item: %v", err)
	}
	if err := insertItem("item-again", "skill", nil); err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("second live item with one name: err = %v, want the UNIQUE refusal", err)
	}
	if err := insertItem("item-command", "command", nil); err != nil {
		t.Fatalf("same name in another kind: %v", err)
	}
	mustExec(t, db, `UPDATE lado_setup_items SET deleted_at = CURRENT_TIMESTAMP WHERE id = 'item'`)
	if err := insertItem("item-created", "skill", nil); err != nil {
		t.Fatalf("item after deleting the live one: %v", err)
	}
	mustExec(t, db, `UPDATE lado_setup_items SET restored_at = CURRENT_TIMESTAMP WHERE id = 'item-created'`)
	if err := insertItem("item-readopted", "skill", "adoption"); err != nil {
		t.Fatalf("item after restoring the live one: %v", err)
	}

	// Versions count from 1, once each.
	insertVersion := func(id string, version int) error {
		_, err := db.Exec(`INSERT INTO lado_setup_item_versions (id, item_id, version, content_kind, sha256, size_bytes, actor, created_at)
			VALUES (?, 'item-readopted', ?, 'tree', ?, 12, 'human', CURRENT_TIMESTAMP)`, id, version, hash)
		return err
	}
	if err := insertVersion("version", 1); err != nil {
		t.Fatalf("version 1: %v", err)
	}
	if err := insertVersion("version-again", 1); err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("second version 1: err = %v, want the UNIQUE refusal", err)
	}
	for _, statement := range []string{
		`INSERT INTO lado_setup_adoptions (id, created_at, actor, os, plan_sha256, state) VALUES ('a2', CURRENT_TIMESTAMP, 'human', 'linux', '` + hash + `', 'restored')`,
		`INSERT INTO lado_setup_adoptions (id, created_at, actor, os, plan_sha256, state) VALUES ('a2', CURRENT_TIMESTAMP, 'human', 'linux', 'short', 'completed')`,
		`UPDATE lado_setup_adoptions SET restore_report = 'not json' WHERE id = 'adoption'`,
		`INSERT INTO lado_setup_items (id, tool, kind, name, created_at) VALUES ('i2', 'cursor', 'skill', 'x', CURRENT_TIMESTAMP)`,
		`INSERT INTO lado_setup_items (id, tool, kind, name, created_at) VALUES ('i2', 'codex', 'plugin', 'x', CURRENT_TIMESTAMP)`,
		`INSERT INTO lado_setup_items (id, tool, kind, name, created_at) VALUES ('i2', 'codex', 'mcp_server', '', CURRENT_TIMESTAMP)`,
		`INSERT INTO lado_setup_item_versions (id, item_id, version, content_kind, sha256, size_bytes, actor, created_at) VALUES ('v0', 'item-readopted', 0, 'file', '` + hash + `', 1, 'human', CURRENT_TIMESTAMP)`,
		`INSERT INTO lado_setup_item_versions (id, item_id, version, content_kind, sha256, size_bytes, actor, created_at) VALUES ('v2', 'item-readopted', 2, 'dir', '` + hash + `', 1, 'human', CURRENT_TIMESTAMP)`,
		`INSERT INTO lado_setup_item_versions (id, item_id, version, content_kind, sha256, size_bytes, executable, actor, created_at) VALUES ('v2', 'item-readopted', 2, 'file', '` + hash + `', 1, 2, 'human', CURRENT_TIMESTAMP)`,
		`INSERT INTO lado_conflict_acceptances (id, code, item_id, content_sha256, scope, actor, created_at) VALUES ('c', 'LADO_SETUP_DENIES_GIT', 'item-readopted', 'short', 'global', 'human', CURRENT_TIMESTAMP)`,
		`INSERT INTO lado_conflict_acceptances (id, code, item_id, content_sha256, scope, actor, created_at) VALUES ('c', 'LADO_SETUP_DENIES_GIT', 'item-readopted', '` + hash + `', 'repo', 'human', CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(statement); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("%s: err = %v, want the CHECK refusal", statement, err)
		}
	}

	// A Global binding is on or off with no scope id; a project or task
	// binding forces one scope on or off, and INHERIT is a missing row.
	insertBinding := func(scope, scopeID, state string) error {
		_, err := db.Exec(`INSERT INTO lado_setup_bindings (item_id, scope, scope_id, state, updated_at)
			VALUES ('item-readopted', ?, ?, ?, CURRENT_TIMESTAMP)`, scope, scopeID, state)
		return err
	}
	for _, row := range [][3]string{
		{"global", "x", "on"},
		{"global", "", "force_off"},
		{"project", "", "force_on"},
		{"task", "", "force_off"},
		{"project", "p", "on"},
		{"task", "t", "off"},
		{"project", "p", "inherit"},
		{"repo", "p", "force_on"},
	} {
		if err := insertBinding(row[0], row[1], row[2]); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("binding %v: err = %v, want the CHECK refusal", row, err)
		}
	}
	for _, row := range [][3]string{{"global", "", "off"}, {"project", "p", "force_on"}, {"task", "t", "force_off"}} {
		if err := insertBinding(row[0], row[1], row[2]); err != nil {
			t.Fatalf("binding %v: %v", row, err)
		}
	}

	// A snapshot pins stored versions of its own.
	insertLadoSnapshotFixture(t, db)
	insertPin := func(snapshotID, versionID string) error {
		_, err := db.Exec(`INSERT INTO lado_policy_snapshot_setup (snapshot_id, item_version_id) VALUES (?, ?)`, snapshotID, versionID)
		return err
	}
	for _, pin := range [][2]string{{"snapshot", "missing"}, {"missing", "version"}} {
		if err := insertPin(pin[0], pin[1]); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			t.Fatalf("pin %v: err = %v, want the FOREIGN KEY refusal", pin, err)
		}
	}
	if err := insertPin("snapshot", "version"); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if err := insertPin("snapshot", "version"); err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("second pin of one version: err = %v, want the UNIQUE refusal", err)
	}

	// The down migration drops every setup table, with the rows above in them.
	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(context.Background(), 6); err != nil {
		t.Fatalf("down to 0006: %v", err)
	}
	for _, table := range tables {
		if tableExists(t, db, table.name) {
			t.Fatalf("table %s survived the 0007 down migration", table.name)
		}
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatalf("up again: %v", err)
	}
	for _, table := range tables {
		if !tableExists(t, db, table.name) {
			t.Fatalf("table %s missing after migrating up again", table.name)
		}
	}
}
