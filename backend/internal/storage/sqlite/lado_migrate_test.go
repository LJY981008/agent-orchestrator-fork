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
	"time"

	"github.com/pressly/goose/v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
	// An adoption is recorded pending before it removes the first original.
	mustExec(t, db, `INSERT INTO lado_setup_adoptions (id, created_at, actor, os, plan_sha256, state)
		VALUES ('adoption-pending', CURRENT_TIMESTAMP, 'human', 'linux', ?, 'pending')`, hash)
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

// ladoVerifyTables are the tables 0008 creates, with their columns in order.
var ladoVerifyTables = []struct {
	name    string
	columns []string
}{
	{"lado_check_runs", []string{"id", "execution_session_id", "head_sha", "name", "command", "status", "exit_code", "log_sha256", "error_message", "worktree_sha256", "started_at", "finished_at"}},
	{"lado_verifications", []string{"execution_session_id", "head_sha", "pr_url", "outcome", "reason", "content_json", "sha256", "notified_json", "protection_error", "computed_at"}},
	{"lado_execution_reviews", []string{"review_run_id", "execution_session_id", "head_sha", "round", "created_at", "cancelled_at"}},
	{"lado_evidence_links", []string{"id", "execution_session_id", "head_sha", "criterion", "state", "ref_kind", "ref_key", "sha", "created_at"}},
	{"lado_integration_approvals", []string{"id", "execution_session_id", "pr_url", "head_sha", "target_branch", "verification_sha256", "acknowledged_json", "proof_nonce", "actor", "approved_at", "status_state", "status_attempts", "status_posted_at", "status_error"}},
}

// ladoVerifyColumns are the columns 0008 adds to earlier LADO tables, in order.
var ladoVerifyColumns = []struct {
	table   string
	columns []string
}{
	{"lado_executions", []string{"base_branch", "base_remote", "dispatch_local_base_sha", "integration_gated", "settled_at", "settled_pr_url", "settled_pr_state"}},
	{"lado_project_settings", []string{"check_commands", "check_timeout_minutes"}},
}

// ladoVerifyHead is the PR head the verification fixtures name.
var ladoVerifyHead = strings.Repeat("b", 40)

// insertLadoExecutionFixture writes execution "session" of task "task"
// (project "p") and its queue entry "entry" on top of
// insertLadoSnapshotFixture. It writes no session row.
func insertLadoExecutionFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	insertLadoSnapshotFixture(t, db)
	mustExec(t, db, `INSERT INTO lado_queue_entries (id, task_id, project_id, plan_approval_id, policy_snapshot_id, enqueued_at)
		VALUES ('entry', 'task', 'p', 'approval', 'snapshot', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO lado_executions (session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id, attempt, started_at)
		VALUES ('session', 'task', 'entry', 'revision', 'approval', 'snapshot', 1, CURRENT_TIMESTAMP)`)
}

func insertLadoPresenceProof(t *testing.T, db *sql.DB, nonce string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO lado_presence_proofs (nonce, action, subject_id, expires_at, used_at)
		VALUES (?, 'integration.approve', 'session', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, nonce)
}

// insertLadoVerifyRow writes one row of table, a table 0008 creates, for
// execution "session" at ladoVerifyHead. The approval spends presence proof
// "integration-nonce", which it stores first.
func insertLadoVerifyRow(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	hash := strings.Repeat("c", 64)
	switch table {
	case "lado_check_runs":
		mustExec(t, db, `INSERT INTO lado_check_runs (id, execution_session_id, head_sha, name, command, status, started_at)
			VALUES ('run', 'session', ?, 'test', 'go test ./...', 'running', CURRENT_TIMESTAMP)`, ladoVerifyHead)
	case "lado_verifications":
		mustExec(t, db, `INSERT INTO lado_verifications (execution_session_id, head_sha, pr_url, outcome, reason, content_json, sha256, computed_at)
			VALUES ('session', ?, 'https://github.com/o/r/pull/1', 'wait', 'checks_pending', '{}', ?, CURRENT_TIMESTAMP)`, ladoVerifyHead, hash)
	case "lado_execution_reviews":
		mustExec(t, db, `INSERT INTO lado_execution_reviews (review_run_id, execution_session_id, head_sha, round, created_at)
			VALUES ('review-run', 'session', ?, 1, CURRENT_TIMESTAMP)`, ladoVerifyHead)
	case "lado_evidence_links":
		mustExec(t, db, `INSERT INTO lado_evidence_links (id, execution_session_id, head_sha, criterion, state, ref_kind, created_at)
			VALUES ('link', 'session', ?, 1, 'claimed', 'claim', CURRENT_TIMESTAMP)`, ladoVerifyHead)
	case "lado_integration_approvals":
		insertLadoPresenceProof(t, db, "integration-nonce")
		mustExec(t, db, `INSERT INTO lado_integration_approvals (id, execution_session_id, pr_url, head_sha, verification_sha256, proof_nonce, actor, approved_at, status_state)
			VALUES ('integration', 'session', 'https://github.com/o/r/pull/1', ?, ?, 'integration-nonce', 'human', CURRENT_TIMESTAMP, 'pending')`, ladoVerifyHead, hash)
	default:
		t.Fatalf("no fixture row for %s", table)
	}
}

func TestLadoMigrateCreatesVerifyTables(t *testing.T) {
	db := openLadoDatabase(t, 1)
	if got := appliedVersion(t, db, ladoVersionTable); got < 8 {
		t.Fatalf("%s = %d, want 0008 applied", ladoVersionTable, got)
	}
	for _, table := range ladoVerifyTables {
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
	for _, added := range ladoVerifyColumns {
		got := tableColumns(t, db, added.table)
		if len(got) < len(added.columns) || !slices.Equal(got[len(got)-len(added.columns):], added.columns) {
			t.Fatalf("%s columns = %v, want them to end with %v", added.table, got, added.columns)
		}
	}

	// A project has no check commands and 30 minutes until it sets them, and
	// an execution starts without base facts and unsettled.
	mustExec(t, db, `INSERT INTO lado_project_settings (project_id, lado_mode, updated_at) VALUES ('p', 1, CURRENT_TIMESTAMP)`)
	var commands string
	var timeout int
	if err := db.QueryRow(`SELECT check_commands, check_timeout_minutes FROM lado_project_settings WHERE project_id = 'p'`).Scan(&commands, &timeout); err != nil {
		t.Fatalf("read project check settings: %v", err)
	}
	if commands != "[]" || timeout != 30 {
		t.Fatalf("project check settings = %s and %d minutes, want [] and the default 30", commands, timeout)
	}
	insertLadoExecutionFixture(t, db)
	var branch, remote, localBase string
	var settledAt sql.NullTime
	if err := db.QueryRow(`SELECT base_branch, base_remote, dispatch_local_base_sha, settled_at FROM lado_executions WHERE session_id = 'session'`).Scan(&branch, &remote, &localBase, &settledAt); err != nil {
		t.Fatalf("read execution base facts: %v", err)
	}
	if branch != "" || remote != "" || localBase != "" || settledAt.Valid {
		t.Fatalf("execution base facts = %q, %q, %q, settled %v; want empty and unsettled", branch, remote, localBase, settledAt)
	}

	for _, table := range ladoVerifyTables {
		insertLadoVerifyRow(t, db, table.name)
	}
	var attempts int
	var acknowledged, notified string
	if err := db.QueryRow(`SELECT status_attempts, acknowledged_json FROM lado_integration_approvals WHERE id = 'integration'`).Scan(&attempts, &acknowledged); err != nil {
		t.Fatalf("read approval: %v", err)
	}
	if err := db.QueryRow(`SELECT notified_json FROM lado_verifications WHERE execution_session_id = 'session'`).Scan(&notified); err != nil {
		t.Fatalf("read record: %v", err)
	}
	if attempts != 0 || acknowledged != "[]" || notified != "[]" {
		t.Fatalf("defaults = %d status attempts, acknowledged %s, notified %s; want 0, [] and []", attempts, acknowledged, notified)
	}
	// A SHA-256 repository's head has 64 hex digits.
	mustExec(t, db, `UPDATE lado_check_runs SET head_sha = ? WHERE id = 'run'`, strings.Repeat("d", 64))

	for _, statement := range []string{
		`UPDATE lado_project_settings SET check_timeout_minutes = 4 WHERE project_id = 'p'`,
		`UPDATE lado_project_settings SET check_timeout_minutes = 121 WHERE project_id = 'p'`,
		`UPDATE lado_project_settings SET check_commands = 'not json' WHERE project_id = 'p'`,
		`UPDATE lado_check_runs SET status = 'queued' WHERE id = 'run'`,
		`UPDATE lado_check_runs SET head_sha = 'abc1234' WHERE id = 'run'`,
		`UPDATE lado_check_runs SET command = '' WHERE id = 'run'`,
		`UPDATE lado_verifications SET outcome = 'info' WHERE execution_session_id = 'session'`,
		`UPDATE lado_verifications SET content_json = 'not json' WHERE execution_session_id = 'session'`,
		`UPDATE lado_verifications SET notified_json = 'not json' WHERE execution_session_id = 'session'`,
		`UPDATE lado_verifications SET sha256 = 'short' WHERE execution_session_id = 'session'`,
		`UPDATE lado_execution_reviews SET round = 4 WHERE review_run_id = 'review-run'`,
		`UPDATE lado_execution_reviews SET round = 0 WHERE review_run_id = 'review-run'`,
		`UPDATE lado_evidence_links SET criterion = -1 WHERE id = 'link'`,
		`UPDATE lado_evidence_links SET state = 'missing' WHERE id = 'link'`,
		`UPDATE lado_evidence_links SET ref_kind = 'url' WHERE id = 'link'`,
		`UPDATE lado_integration_approvals SET status_state = 'success' WHERE id = 'integration'`,
		`UPDATE lado_integration_approvals SET acknowledged_json = 'not json' WHERE id = 'integration'`,
		`UPDATE lado_integration_approvals SET verification_sha256 = 'short' WHERE id = 'integration'`,
	} {
		if _, err := db.Exec(statement); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("%s: err = %v, want the CHECK refusal", statement, err)
		}
	}

	// Every row belongs to an execution.
	for _, table := range ladoVerifyTables {
		statement := `UPDATE ` + table.name + ` SET execution_session_id = 'missing'`
		if _, err := db.Exec(statement); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			t.Fatalf("%s: err = %v, want the FOREIGN KEY refusal", statement, err)
		}
	}
	// One record per execution and head.
	if _, err := db.Exec(`INSERT INTO lado_verifications (execution_session_id, head_sha, pr_url, outcome, content_json, sha256, computed_at)
		VALUES ('session', ?, 'https://github.com/o/r/pull/1', 'pass', '{}', ?, CURRENT_TIMESTAMP)`, ladoVerifyHead, strings.Repeat("c", 64)); err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("second record of one head: err = %v, want the UNIQUE refusal", err)
	}

	// An approval spends a stored presence proof once, and approves one head
	// of an execution once.
	insertApproval := func(id, head, nonce string) error {
		_, err := db.Exec(`INSERT INTO lado_integration_approvals (id, execution_session_id, pr_url, head_sha, verification_sha256, proof_nonce, actor, approved_at, status_state)
			VALUES (?, 'session', 'https://github.com/o/r/pull/1', ?, ?, ?, 'human', CURRENT_TIMESTAMP, 'not_applicable')`, id, head, strings.Repeat("c", 64), nonce)
		return err
	}
	otherHead := strings.Repeat("e", 40)
	if err := insertApproval("integration-2", otherHead, "missing"); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("approval naming a missing proof: err = %v, want the FOREIGN KEY refusal", err)
	}
	if err := insertApproval("integration-2", otherHead, "integration-nonce"); err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("approval replaying a proof: err = %v, want the UNIQUE refusal", err)
	}
	insertLadoPresenceProof(t, db, "integration-nonce-2")
	if err := insertApproval("integration-2", ladoVerifyHead, "integration-nonce-2"); err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("second approval of one head: err = %v, want the UNIQUE refusal", err)
	}
	if err := insertApproval("integration-2", otherHead, "integration-nonce-2"); err != nil {
		t.Fatalf("approval of another head: %v", err)
	}
}

func TestLadoVerifyTablesPublishExecutionEvents(t *testing.T) {
	db := openLadoDatabase(t, 1)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/p', CURRENT_TIMESTAMP)`)
	insertLadoExecutionFixture(t, db)

	var seq int64
	skip := func() {
		t.Helper()
		if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&seq); err != nil {
			t.Fatalf("read change_log position: %v", err)
		}
	}
	// Every event names the execution's session, task and project; expect
	// checks the change_log session id of each event since the last call.
	const payload = `{"kind":"lado_execution","sessionId":"session","taskId":"task","projectId":"p"}`
	const (
		withoutSession = "NULL"
		onSession      = "session"
	)
	expect := func(step string, want ...string) {
		t.Helper()
		rows, err := db.Query(`SELECT seq, project_id, session_id, event_type, payload FROM change_log WHERE seq > ? ORDER BY seq`, seq)
		if err != nil {
			t.Fatalf("%s: read change_log: %v", step, err)
		}
		defer func() { _ = rows.Close() }()
		var got []string
		for rows.Next() {
			var projectID, eventType, eventPayload string
			var sessionID sql.NullString
			if err := rows.Scan(&seq, &projectID, &sessionID, &eventType, &eventPayload); err != nil {
				t.Fatalf("%s: scan change_log: %v", step, err)
			}
			if projectID != "p" || eventType != "session_updated" || eventPayload != payload {
				t.Fatalf("%s: event %s %s %s, want session_updated %s in p", step, projectID, eventType, eventPayload, payload)
			}
			if !sessionID.Valid {
				sessionID.String = withoutSession
			}
			got = append(got, sessionID.String)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("%s: read change_log: %v", step, err)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s: events on sessions %q, want %q", step, got, want)
		}
	}
	step := func(statement string, want ...string) {
		t.Helper()
		mustExec(t, db, statement)
		expect(statement, want...)
	}
	upsertRecord := func(outcome, reason string) string {
		return `INSERT INTO lado_verifications (execution_session_id, head_sha, pr_url, outcome, reason, content_json, sha256, computed_at)
			VALUES ('session', '` + ladoVerifyHead + `', 'https://github.com/o/r/pull/1', '` + outcome + `', '` + reason + `', '{}', '` + strings.Repeat("d", 64) + `', datetime('now', '+2 minutes'))
			ON CONFLICT (execution_session_id, head_sha) DO UPDATE SET outcome = excluded.outcome, reason = excluded.reason,
				content_json = excluded.content_json, sha256 = excluded.sha256, computed_at = excluded.computed_at`
	}
	skip()

	// change_log names only sessions that exist, so a record of a session AO
	// does not have publishes without one; its payload still names it.
	insertLadoVerifyRow(t, db, "lado_verifications")
	expect("record without a session row", withoutSession)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, created_at, updated_at, activity_last_at)
		VALUES ('session', 'p', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	skip()

	// A record publishes when its outcome, reason or hash changes, and not
	// when it is only recomputed or a fix request is noted.
	step(`UPDATE lado_verifications SET outcome = 'pass'`, onSession)
	step(`UPDATE lado_verifications SET reason = ''`, onSession)
	step(`UPDATE lado_verifications SET sha256 = '`+strings.Repeat("d", 64)+`'`, onSession)
	step(`UPDATE lado_verifications SET computed_at = datetime('now', '+1 minute')`)
	step(`UPDATE lado_verifications SET notified_json = '["commands"]'`)
	step(`UPDATE lado_verifications SET outcome = 'pass'`)
	// The loop's upsert of an existing record runs as an update: an unchanged
	// record publishes nothing, a changed one once.
	step(upsertRecord("pass", ""))
	step(upsertRecord("fail", "commands_failed"), onSession)

	// An approval publishes when it is made and when its status state
	// changes; a retry that fails again publishes nothing.
	insertLadoVerifyRow(t, db, "lado_integration_approvals")
	expect("approval", onSession)
	step(`UPDATE lado_integration_approvals SET status_state = 'failed', status_attempts = 1, status_error = 'HTTP 502'`, onSession)
	step(`UPDATE lado_integration_approvals SET status_attempts = 2, status_error = 'HTTP 503'`)
	step(`UPDATE lado_integration_approvals SET status_state = 'posted', status_attempts = 3, status_posted_at = CURRENT_TIMESTAMP, status_error = ''`, onSession)

	// A check run publishes when it starts and when its status changes.
	insertLadoVerifyRow(t, db, "lado_check_runs")
	expect("check run", onSession)
	step(`UPDATE lado_check_runs SET log_sha256 = '` + strings.Repeat("f", 64) + `'`)
	step(`UPDATE lado_check_runs SET status = 'passed', exit_code = 0, finished_at = CURRENT_TIMESTAMP`, onSession)

	// Evidence links and review rounds publish nothing; the record covers them.
	insertLadoVerifyRow(t, db, "lado_evidence_links")
	insertLadoVerifyRow(t, db, "lado_execution_reviews")
	step(`UPDATE lado_execution_reviews SET cancelled_at = CURRENT_TIMESTAMP`)
}

func TestLadoMigrate0008DownRemovesWhatItAdded(t *testing.T) {
	db := openLadoDatabase(t, 1)
	mustExec(t, db, `INSERT INTO lado_project_settings (project_id, lado_mode, updated_at, check_commands, check_timeout_minutes)
		VALUES ('p', 1, CURRENT_TIMESTAMP, '[{"name":"test","command":"go test ./..."}]', 45)`)
	insertLadoExecutionFixture(t, db)
	mustExec(t, db, `UPDATE lado_executions SET base_branch = 'main', base_remote = 'origin', dispatch_local_base_sha = ?, settled_at = CURRENT_TIMESTAMP`, strings.Repeat("a", 40))
	for _, table := range ladoVerifyTables {
		insertLadoVerifyRow(t, db, table.name)
	}
	// The rows of the earlier tables outlive the round trip.
	expectEarlierRows := func(step string) {
		t.Helper()
		var settings, executions int
		if err := db.QueryRow(`SELECT COUNT(*) FROM lado_project_settings WHERE project_id = 'p' AND lado_mode = 1`).Scan(&settings); err != nil {
			t.Fatalf("%s: count project settings: %v", step, err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM lado_executions WHERE session_id = 'session' AND task_id = 'task'`).Scan(&executions); err != nil {
			t.Fatalf("%s: count executions: %v", step, err)
		}
		if settings != 1 || executions != 1 {
			t.Fatalf("%s: %d project settings rows and %d executions, want 1 and 1", step, settings, executions)
		}
	}

	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(context.Background(), 7); err != nil {
		t.Fatalf("down to 0007: %v", err)
	}
	for _, table := range ladoVerifyTables {
		if tableExists(t, db, table.name) {
			t.Fatalf("table %s survived the 0008 down migration", table.name)
		}
	}
	for _, added := range ladoVerifyColumns {
		got := tableColumns(t, db, added.table)
		for _, column := range added.columns {
			if slices.Contains(got, column) {
				t.Fatalf("%s.%s survived the 0008 down migration", added.table, column)
			}
		}
	}
	expectEarlierRows("after the down migration")

	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d after migrating up again, want %d", ladoVersionTable, got, want)
	}
	for _, table := range ladoVerifyTables {
		if got := tableColumns(t, db, table.name); !slices.Equal(got, table.columns) {
			t.Fatalf("%s columns after migrating up again = %v, want %v", table.name, got, table.columns)
		}
	}
	for _, added := range ladoVerifyColumns {
		got := tableColumns(t, db, added.table)
		if len(got) < len(added.columns) || !slices.Equal(got[len(got)-len(added.columns):], added.columns) {
			t.Fatalf("%s columns after migrating up again = %v, want them to end with %v", added.table, got, added.columns)
		}
	}
	expectEarlierRows("after migrating up again")
	// The kept rows get the defaults back.
	var commands, branch string
	var timeout int
	var settledAt sql.NullTime
	if err := db.QueryRow(`SELECT check_commands, check_timeout_minutes FROM lado_project_settings WHERE project_id = 'p'`).Scan(&commands, &timeout); err != nil {
		t.Fatalf("read project check settings: %v", err)
	}
	if err := db.QueryRow(`SELECT base_branch, settled_at FROM lado_executions WHERE session_id = 'session'`).Scan(&branch, &settledAt); err != nil {
		t.Fatalf("read execution base facts: %v", err)
	}
	if commands != "[]" || timeout != 30 || branch != "" || settledAt.Valid {
		t.Fatalf("kept rows = commands %s, %d minutes, base %q, settled %v; want the defaults", commands, timeout, branch, settledAt)
	}
}

// Executions dispatched before 0008 had no integration gate (V4 of #15). 0008
// settles those whose newest PR already merged or closed, at the PR's last
// update, so the verification loop never lists them to audit a merge
// without approval, and their tasks stay Completed without that reason. An
// execution whose newest PR is open stays to verify. None of them counts as
// gated; the dispatcher gates every execution it links from now on.
func TestLadoMigrate0008SettlesEarlierExecutions(t *testing.T) {
	ctx := context.Background()
	upstream, err := expectedMigrationVersion()
	if err != nil {
		t.Fatalf("expected migration version: %v", err)
	}
	for _, tc := range []struct {
		newest, older string
		settled       bool
	}{
		{newest: "merged", settled: true},
		{newest: "closed", settled: true},
		{newest: "open"},
		{newest: "merged", older: "open", settled: true},
		{newest: "open", older: "merged"},
	} {
		t.Run(tc.newest+"_after_"+tc.older, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "ao.db"), migratedDatabaseSnapshot(t, upstream), 0o600); err != nil {
				t.Fatalf("seed upstream database: %v", err)
			}
			openAndClose(t, dir)
			db := openLadoInspection(t, dir)
			if _, err := ladoMigrationProvider(t, db).DownTo(ctx, 7); err != nil {
				t.Fatalf("down to 0007: %v", err)
			}
			insertLadoExecutionFixture(t, db)
			mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/p', CURRENT_TIMESTAMP)`)
			mustExec(t, db, `INSERT INTO sessions (id, project_id, num, created_at, updated_at, activity_last_at)
				VALUES ('session', 'p', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
			if tc.older != "" {
				mustExec(t, db, `INSERT INTO pr (url, session_id, number, pr_state, head_sha, updated_at) VALUES ('https://github.com/o/r/pull/1', 'session', 1, ?, ?, '2026-10-20 10:00:00+00:00')`, tc.older, ladoVerifyHead)
			}
			mustExec(t, db, `INSERT INTO pr (url, session_id, number, pr_state, head_sha, updated_at) VALUES ('https://github.com/o/r/pull/2', 'session', 2, ?, ?, '2026-10-21 10:00:00+00:00')`, tc.newest, ladoVerifyHead)
			if _, err := ladoMigrationProvider(t, db).Up(ctx); err != nil {
				t.Fatalf("up to 0008: %v", err)
			}
			var gated int
			var settledURL, settledState string
			var settledAt sql.NullTime
			if err := db.QueryRow(`SELECT integration_gated, settled_at, settled_pr_url, settled_pr_state FROM lado_executions WHERE session_id = 'session'`).
				Scan(&gated, &settledAt, &settledURL, &settledState); err != nil {
				t.Fatalf("read the execution: %v", err)
			}
			if gated != 0 {
				t.Errorf("integration_gated = %d, want 0 for an execution dispatched before 0008", gated)
			}
			switch {
			case tc.settled && (!settledAt.Valid || settledURL != "https://github.com/o/r/pull/2" || settledState != tc.newest):
				t.Errorf("settled = %v at %v as %q %q, want PR 2 %s at its last update", settledAt.Valid, settledAt.Time, settledURL, settledState, tc.newest)
			case tc.settled && !settledAt.Time.Equal(time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)):
				t.Errorf("settled at %s, want the PR's last update", settledAt.Time)
			case !tc.settled && (settledAt.Valid || settledURL != "" || settledState != ""):
				t.Errorf("an execution whose newest PR is open settled at %v as %q %q", settledAt.Time, settledURL, settledState)
			}
			_ = db.Close()

			st, err := Open(dir)
			if err != nil {
				t.Fatalf("open the store: %v", err)
			}
			defer func() { _ = st.Close() }()
			listed, err := st.ListLadoExecutionsToVerify(ctx, 50)
			if err != nil {
				t.Fatalf("ListLadoExecutionsToVerify: %v", err)
			}
			if got := len(listed) > 0; got == tc.settled {
				t.Errorf("listed to verify = %v, want %v", got, !tc.settled)
			}
			task, ok, err := st.LadoTask(ctx, "task")
			if err != nil || !ok {
				t.Fatalf("read the task = (%v, %v)", ok, err)
			}
			if tc.newest == "merged" && (task.State() != domain.LadoTaskCompleted || task.StateReason() != "") {
				t.Errorf("task = %s %q, want completed without a reason", task.State(), task.StateReason())
			}
		})
	}
}
