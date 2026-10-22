-- name: InsertLadoRule :exec
INSERT INTO lado_rules (id, builtin, protected, created_at)
VALUES (?, ?, ?, ?);

-- name: GetLadoRuleRow :one
SELECT id, builtin, protected, created_at, archived_at
FROM lado_rules
WHERE id = ?;

-- name: ListLadoRuleRows :many
SELECT id, builtin, protected, created_at, archived_at
FROM lado_rules
WHERE CAST(sqlc.arg(include_archived) AS BOOLEAN) OR archived_at IS NULL
ORDER BY created_at, id;

-- name: SetLadoRuleArchivedAt :execrows
UPDATE lado_rules
SET archived_at = ?
WHERE id = ?;

-- name: DeleteLadoRuleRow :execrows
DELETE FROM lado_rules
WHERE id = ?;

-- name: InsertLadoRuleVersion :one
INSERT INTO lado_rule_versions (
    id, rule_id, number, name, category, instruction,
    enforcement_json, config_json, conflicts_json, sha256, actor, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, rule_id, number, name, category, instruction,
    enforcement_json, config_json, conflicts_json, sha256, actor, created_at;

-- name: GetLatestLadoRuleVersion :one
SELECT id, rule_id, number, name, category, instruction,
    enforcement_json, config_json, conflicts_json, sha256, actor, created_at
FROM lado_rule_versions
WHERE rule_id = ?
ORDER BY number DESC
LIMIT 1;

-- name: ListLatestLadoRuleVersions :many
SELECT v.id, v.rule_id, v.number, v.name, v.category, v.instruction,
    v.enforcement_json, v.config_json, v.conflicts_json, v.sha256, v.actor, v.created_at
FROM lado_rule_versions v
WHERE v.number = (SELECT MAX(v2.number) FROM lado_rule_versions v2 WHERE v2.rule_id = v.rule_id)
ORDER BY v.rule_id;

-- name: ListLadoRuleVersions :many
SELECT id, rule_id, number, name, category, instruction,
    enforcement_json, config_json, conflicts_json, sha256, actor, created_at
FROM lado_rule_versions
WHERE rule_id = ?
ORDER BY number;

-- name: CountLadoRuleVersion :one
SELECT COUNT(*)
FROM lado_rule_versions
WHERE id = ? AND rule_id = ?;

-- name: DeleteLadoRuleVersions :exec
DELETE FROM lado_rule_versions
WHERE rule_id = ?;

-- name: UpsertLadoRuleBinding :exec
INSERT INTO lado_rule_bindings (rule_id, scope, scope_id, state, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (rule_id, scope, scope_id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at;

-- name: DeleteLadoRuleBinding :execrows
DELETE FROM lado_rule_bindings
WHERE rule_id = ? AND scope = ? AND scope_id = ?;

-- name: DeleteLadoRuleBindingsForRule :exec
DELETE FROM lado_rule_bindings
WHERE rule_id = ?;

-- name: ListLadoRuleBindings :many
SELECT rule_id, scope, scope_id, state, updated_at
FROM lado_rule_bindings
ORDER BY rule_id, scope, scope_id;

-- name: ListLadoRuleBindingsForRule :many
SELECT rule_id, scope, scope_id, state, updated_at
FROM lado_rule_bindings
WHERE rule_id = ?
ORDER BY scope, scope_id;

-- name: InsertLadoPolicySnapshot :one
INSERT INTO lado_policy_snapshots (id, approval_id, sha256, snapshot_json, blocking, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id, approval_id, sha256, snapshot_json, blocking, created_at;

-- name: InsertLadoPolicySnapshotRule :exec
INSERT INTO lado_policy_snapshot_rules (snapshot_id, rule_id, rule_version_id)
VALUES (?, ?, ?);

-- name: GetLadoPolicySnapshotByApproval :one
SELECT id, approval_id, sha256, snapshot_json, blocking, created_at
FROM lado_policy_snapshots
WHERE approval_id = ?;

-- name: ListLadoPolicySnapshotRules :many
SELECT snapshot_id, rule_id, rule_version_id
FROM lado_policy_snapshot_rules
WHERE snapshot_id = ?
ORDER BY rule_id, rule_version_id;

-- name: ListLadoPolicySnapshotsByProject :many
SELECT s.id, s.approval_id, s.sha256, s.snapshot_json, s.blocking, s.created_at
FROM lado_policy_snapshots s
JOIN lado_plan_approvals a ON a.id = s.approval_id
JOIN lado_tasks t ON t.id = a.task_id
WHERE t.project_id = ?
ORDER BY a.task_id, a.approved_at, a.id;

-- name: CountLadoSnapshotRulesForRule :one
SELECT COUNT(*)
FROM lado_policy_snapshot_rules
WHERE rule_id = ?;

-- name: ListLadoModeProjectIDs :many
SELECT project_id
FROM lado_project_settings
WHERE lado_mode = 1
ORDER BY project_id;
