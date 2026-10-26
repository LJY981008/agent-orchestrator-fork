package sqlite

// LADO fork: the LADO migration track (docs/LJY-PRD/TDD.md §4.1 and
// docs/LJY-PRD/adr/0004-data-same-db-separate-migration-track.md). LADO tables
// share the database, but their migrations never enter upstream's
// goose_db_version: they run from their own directory and version table, after
// every upstream migration and repair, so fresh and upgraded databases apply
// the two tracks in the same order.

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations_lado/*.sql
var ladoMigrationsFS embed.FS

// ladoVersionTable is the LADO track's goose ledger.
const ladoVersionTable = "lado_goose_db_version"

// ladoCDCTriggers holds the CREATE TRIGGER statements, each named lado_cdc_*,
// that write LADO rows into change_log (TDD §4.3). Upstream rebuilds
// change_log and other tables by copy and rename, dropping only the triggers it
// knows, and a trigger it does not know fails that rename. So these are dropped
// before upstream runs and recreated after the LADO track on every open, and
// LADO migrations never create lado_cdc_* triggers themselves.
var ladoCDCTriggers = []string{
	// A task becomes visible once it has an issue. Rows without a session use
	// session_updated with a NULL session id, one row per project, and a
	// project that is not registered gets no row (TDD §4.3).
	`CREATE TRIGGER lado_cdc_tasks_insert
AFTER INSERT ON lado_tasks
WHEN NEW.issue_ref IS NOT NULL
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, NULL, 'session_updated',
        json_object('kind', 'lado_task', 'taskId', NEW.id, 'projectId', projects.id),
        datetime('now')
    FROM projects
    WHERE projects.id = NEW.project_id;
END`,
	// Refreshing an issue that did not change touches only issue_observed_at
	// and updated_at, and publishes nothing.
	`CREATE TRIGGER lado_cdc_tasks_update
AFTER UPDATE ON lado_tasks
WHEN NEW.issue_ref IS NOT NULL AND (
    OLD.issue_ref IS NOT NEW.issue_ref OR
    OLD.agent IS NOT NEW.agent OR
    OLD.model IS NOT NEW.model OR
    OLD.issue_title IS NOT NEW.issue_title OR
    OLD.issue_state IS NOT NEW.issue_state OR
    OLD.issue_url IS NOT NEW.issue_url OR
    OLD.issue_snapshot_sha256 IS NOT NEW.issue_snapshot_sha256 OR
    OLD.cancelled_at IS NOT NEW.cancelled_at
)
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, NULL, 'session_updated',
        json_object('kind', 'lado_task', 'taskId', NEW.id, 'projectId', projects.id),
        datetime('now')
    FROM projects
    WHERE projects.id = NEW.project_id;
END`,
	// Plan requests, revisions and approvals change a task's state, so they
	// publish the task's change. A delivery retry that changes only the
	// attempt count publishes nothing; delivery and giving up do.
	ladoPlanCDCTrigger("lado_cdc_plan_requests_insert", "AFTER INSERT ON lado_plan_requests", ""),
	ladoPlanCDCTrigger("lado_cdc_plan_requests_update", "AFTER UPDATE ON lado_plan_requests",
		"WHEN OLD.delivered_at IS NOT NEW.delivered_at OR OLD.failed_at IS NOT NEW.failed_at"),
	ladoPlanCDCTrigger("lado_cdc_plan_revisions_insert", "AFTER INSERT ON lado_plan_revisions", ""),
	ladoPlanCDCTrigger("lado_cdc_plan_approvals_insert", "AFTER INSERT ON lado_plan_approvals", ""),
	// A queue entry publishes its task's change when it is made and when a
	// fact the task's state or its reason reads changes; lease times, the
	// next attempt and the error text change only with one of those.
	ladoPlanCDCTrigger("lado_cdc_queue_entries_insert", "AFTER INSERT ON lado_queue_entries", ""),
	ladoPlanCDCTrigger("lado_cdc_queue_entries_update", "AFTER UPDATE ON lado_queue_entries",
		`WHEN OLD.lease_token IS NOT NEW.lease_token OR
    OLD.waiting_reason IS NOT NEW.waiting_reason OR
    OLD.review_required_reason IS NOT NEW.review_required_reason OR
    OLD.blocked_reason IS NOT NEW.blocked_reason OR
    OLD.dispatched_session_id IS NOT NEW.dispatched_session_id OR
    OLD.failed_at IS NOT NEW.failed_at OR
    OLD.superseded_at IS NOT NEW.superseded_at OR
    OLD.cancelled_at IS NOT NEW.cancelled_at OR
    OLD.attempt_count IS NOT NEW.attempt_count`),
	// An execution publishes on its session when it starts and when it times
	// out. Dispatch settings and project caps publish nothing.
	ladoExecutionCDCTrigger("lado_cdc_executions_insert", "AFTER INSERT ON lado_executions", ""),
	ladoExecutionCDCTrigger("lado_cdc_executions_update", "AFTER UPDATE ON lado_executions",
		"WHEN OLD.timed_out_at IS NOT NEW.timed_out_at"),
	// Rules apply in every LADO project, so a rule, a new version or a Global
	// binding publishes a lado_rules change in each project with LADO mode on.
	// Versions never change, and their deletes follow the rule's.
	ladoRulesCDCTrigger("lado_cdc_rules_insert", "AFTER INSERT ON lado_rules"),
	ladoRulesCDCTrigger("lado_cdc_rules_update", "AFTER UPDATE ON lado_rules"),
	ladoRulesCDCTrigger("lado_cdc_rules_delete", "AFTER DELETE ON lado_rules"),
	ladoRulesCDCTrigger("lado_cdc_rule_versions_insert", "AFTER INSERT ON lado_rule_versions"),
	ladoBindingCDCTrigger("lado_cdc_rule_bindings_insert", "AFTER INSERT ON lado_rule_bindings", "NEW"),
	ladoBindingCDCTrigger("lado_cdc_rule_bindings_update", "AFTER UPDATE ON lado_rule_bindings", "NEW"),
	ladoBindingCDCTrigger("lado_cdc_rule_bindings_delete", "AFTER DELETE ON lado_rule_bindings", "OLD"),
}

// ladoPlanCDCTrigger publishes a change of the task that NEW.task_id names,
// in the task's project.
func ladoPlanCDCTrigger(name, event, when string) string {
	if when != "" {
		when = "\n" + when
	}
	return `CREATE TRIGGER ` + name + `
` + event + when + `
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, NULL, 'session_updated',
        json_object('kind', 'lado_task', 'taskId', lado_tasks.id, 'projectId', projects.id),
        datetime('now')
    FROM lado_tasks
    JOIN projects ON projects.id = lado_tasks.project_id
    WHERE lado_tasks.id = NEW.task_id;
END`
}

// ladoExecutionCDCTrigger publishes a change of the execution NEW names, on
// its session, in its task's project. change_log names only sessions that
// exist, so an execution whose session row is gone publishes without one.
func ladoExecutionCDCTrigger(name, event, when string) string {
	if when != "" {
		when = "\n" + when
	}
	return `CREATE TRIGGER ` + name + `
` + event + when + `
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, (SELECT sessions.id FROM sessions WHERE sessions.id = NEW.session_id), 'session_updated',
        json_object('kind', 'lado_execution', 'sessionId', NEW.session_id, 'taskId', lado_tasks.id, 'projectId', projects.id),
        datetime('now')
    FROM lado_tasks
    JOIN projects ON projects.id = lado_tasks.project_id
    WHERE lado_tasks.id = NEW.task_id;
END`
}

// ladoRulesChangeInsert writes one lado_rules change per project the FROM and
// WHERE clauses that follow it select.
const ladoRulesChangeInsert = `INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, NULL, 'session_updated',
        json_object('kind', 'lado_rules', 'projectId', projects.id),
        datetime('now')`

// ladoRulesCDCTrigger publishes a rule change in every registered project
// with LADO mode on.
func ladoRulesCDCTrigger(name, event string) string {
	return `CREATE TRIGGER ` + name + `
` + event + `
BEGIN
    ` + ladoRulesChangeInsert + `
    FROM projects
    JOIN lado_project_settings ON lado_project_settings.project_id = projects.id
    WHERE lado_project_settings.lado_mode = 1;
END`
}

// ladoBindingCDCTrigger publishes the change of the binding that row names,
// NEW or OLD for a delete: a Global binding in every registered project with
// LADO mode on, a project binding in that project and a task binding in the
// task's project.
func ladoBindingCDCTrigger(name, event, row string) string {
	return `CREATE TRIGGER ` + name + `
` + event + `
BEGIN
    ` + ladoRulesChangeInsert + `
    FROM projects
    WHERE (` + row + `.scope = 'global' AND projects.id IN (
            SELECT project_id FROM lado_project_settings WHERE lado_mode = 1))
        OR (` + row + `.scope = 'project' AND projects.id = ` + row + `.scope_id)
        OR (` + row + `.scope = 'task' AND projects.id = (
            SELECT project_id FROM lado_tasks WHERE id = ` + row + `.scope_id));
END`
}

// dropLadoCDCTriggers removes every lado_cdc_* trigger ahead of upstream.
func dropLadoCDCTriggers(db *sql.DB) error {
	// Collect the names first: the writer pool has one connection, and open
	// rows would hold it while the drops wait for it.
	names, err := ladoCDCTriggerNames(db)
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + quoteSQLiteIdent(name)); err != nil {
			return fmt.Errorf("drop %s: %w", name, err)
		}
	}
	return nil
}

func ladoCDCTriggerNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name GLOB 'lado_cdc_*'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// migrateLado applies the LADO track, then recreates the LADO change triggers.
func migrateLado(db *sql.DB) error {
	migrations, err := fs.Sub(ladoMigrationsFS, "migrations_lado")
	if err != nil {
		return fmt.Errorf("open LADO migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations,
		goose.WithTableName(ladoVersionTable),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("prepare LADO migrations: %w", err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		return fmt.Errorf("run LADO migrations: %w", err)
	}
	for _, statement := range ladoCDCTriggers {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("create LADO change trigger: %w", err)
		}
	}
	return nil
}
