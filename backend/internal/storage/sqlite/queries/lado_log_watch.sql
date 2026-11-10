-- LADO fork: production log watch (issue #87, migrations_lado/0017).

-- name: GetLadoLogWatchSettings :one
SELECT * FROM lado_log_watch_settings WHERE project_id = ?;

-- name: ListEnabledLadoLogWatchSettings :many
SELECT * FROM lado_log_watch_settings WHERE enabled = 1 ORDER BY project_id;

-- name: UpsertLadoLogWatchSettings :exec
-- A save writes what the developer sets; burst_notified_at stays.
INSERT INTO lado_log_watch_settings (
    project_id, enabled, repository, include_json, exclude_json, hourly_issue_cap,
    auto_plan, auto_plan_daily_cap, auto_plan_min_count, disconnect_alert_minutes, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id) DO UPDATE SET
    enabled = excluded.enabled,
    repository = excluded.repository,
    include_json = excluded.include_json,
    exclude_json = excluded.exclude_json,
    hourly_issue_cap = excluded.hourly_issue_cap,
    auto_plan = excluded.auto_plan,
    auto_plan_daily_cap = excluded.auto_plan_daily_cap,
    auto_plan_min_count = excluded.auto_plan_min_count,
    disconnect_alert_minutes = excluded.disconnect_alert_minutes,
    updated_at = excluded.updated_at;

-- name: SetLadoLogBurstNotified :execrows
UPDATE lado_log_watch_settings SET burst_notified_at = ? WHERE project_id = ?;

-- name: ListLadoLogSourcesByProject :many
SELECT * FROM lado_log_sources WHERE project_id = ? ORDER BY position, id;

-- name: GetLadoLogSourceProject :one
SELECT project_id FROM lado_log_sources WHERE id = ?;

-- name: InsertLadoLogSource :exec
INSERT INTO lado_log_sources (
    id, project_id, kind, display_name, endpoint, tls, cert_key, paused, position, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateLadoLogSource :execrows
-- A save writes what the developer sets; the connection state and the
-- certificate key stay.
UPDATE lado_log_sources
SET kind = ?,
    display_name = ?,
    endpoint = ?,
    tls = ?,
    paused = ?,
    position = ?,
    updated_at = ?
WHERE id = ? AND project_id = ?;

-- name: DeleteLadoLogSource :execrows
DELETE FROM lado_log_sources WHERE id = ? AND project_id = ?;

-- name: SetLadoLogSourceDown :execrows
UPDATE lado_log_sources SET down_since = ?, down_notified_at = ? WHERE id = ?;

-- name: SetLadoLogSourceCertKey :execrows
UPDATE lado_log_sources SET cert_key = ? WHERE id = ?;

-- name: ListLadoLogTargetsByProject :many
SELECT * FROM lado_log_targets
WHERE source_id IN (SELECT lado_log_sources.id FROM lado_log_sources WHERE lado_log_sources.project_id = ?)
ORDER BY source_id, position, id;

-- name: InsertLadoLogTarget :exec
INSERT INTO lado_log_targets (
    id, source_id, kind, value, compose_project, service, paused, position, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateLadoLogTarget :execrows
-- A target may move to another source of the same project.
UPDATE lado_log_targets
SET source_id = ?,
    kind = ?,
    value = ?,
    compose_project = ?,
    service = ?,
    paused = ?,
    position = ?,
    updated_at = ?
WHERE lado_log_targets.id = ? AND lado_log_targets.source_id IN (
    SELECT lado_log_sources.id FROM lado_log_sources WHERE lado_log_sources.project_id = ?
);

-- name: DeleteLadoLogTarget :execrows
DELETE FROM lado_log_targets WHERE id = ?;

-- name: CountLadoLogTargets :one
SELECT COUNT(*) FROM lado_log_targets WHERE id = ?;

-- name: GetLadoLogCursor :one
SELECT * FROM lado_log_cursors WHERE target_id = ? AND instance = ?;

-- name: UpsertLadoLogCursor :exec
INSERT INTO lado_log_cursors (target_id, instance, since, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (target_id, instance) DO UPDATE SET
    since = excluded.since,
    updated_at = excluded.updated_at;

-- name: GetLadoLogFingerprint :one
SELECT * FROM lado_log_fingerprints WHERE project_id = ? AND fingerprint = ?;

-- name: GetLadoLogFingerprintByID :one
SELECT * FROM lado_log_fingerprints WHERE id = ?;

-- name: InsertLadoLogFingerprint :one
INSERT INTO lado_log_fingerprints (
    project_id, fingerprint, service, exception_kind, location, count,
    first_seen_at, last_seen_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: UpdateLadoLogFingerprintCounts :exec
UPDATE lado_log_fingerprints
SET count = ?,
    first_seen_at = ?,
    last_seen_at = ?,
    updated_at = ?
WHERE id = ?;

-- name: UpdateLadoLogFingerprintLink :execrows
UPDATE lado_log_fingerprints
SET task_id = ?,
    issue_ref = ?,
    previous_issue_ref = ?,
    issue_created_at = ?,
    auto_plan_requested_at = ?,
    auto_plan_held = ?,
    updated_at = ?
WHERE id = ?;

-- name: SetLadoLogAutoPlan :execrows
UPDATE lado_log_fingerprints
SET auto_plan_requested_at = ?,
    auto_plan_held = ?,
    updated_at = ?
WHERE id = ?;

-- name: SetLadoLogFingerprintIgnored :execrows
UPDATE lado_log_fingerprints
SET ignored = ?,
    ignored_at = ?,
    updated_at = ?
WHERE id = ?;

-- name: CountLadoLogIssuesSince :one
SELECT COUNT(*) FROM lado_log_fingerprints
WHERE project_id = sqlc.arg(project_id) AND issue_created_at >= sqlc.arg(since);

-- name: CountLadoLogAutoPlansSince :one
SELECT COUNT(*) FROM lado_log_fingerprints
WHERE project_id = sqlc.arg(project_id) AND auto_plan_requested_at >= sqlc.arg(since);

-- name: ListLadoLogOpenIncidents :many
-- The fingerprints whose task is live: not cancelled and its issue neither
-- done nor cancelled. A task still waiting for its issue is live.
SELECT * FROM lado_log_fingerprints
WHERE lado_log_fingerprints.project_id = ? AND lado_log_fingerprints.task_id IN (
    SELECT lado_tasks.id FROM lado_tasks
    WHERE lado_tasks.cancelled_at IS NULL AND lado_tasks.issue_state NOT IN ('done', 'cancelled')
)
ORDER BY lado_log_fingerprints.last_seen_at DESC, lado_log_fingerprints.id DESC;

-- name: ListLadoLogOtherIncidents :many
-- The newest fingerprints without a live task.
SELECT * FROM lado_log_fingerprints
WHERE lado_log_fingerprints.project_id = sqlc.arg(project_id) AND (
    lado_log_fingerprints.task_id IS NULL OR lado_log_fingerprints.task_id NOT IN (
        SELECT lado_tasks.id FROM lado_tasks
        WHERE lado_tasks.cancelled_at IS NULL AND lado_tasks.issue_state NOT IN ('done', 'cancelled')
    )
)
ORDER BY lado_log_fingerprints.last_seen_at DESC, lado_log_fingerprints.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListLadoLogInstanceCountsByProject :many
SELECT * FROM lado_log_instance_counts
WHERE fingerprint_id IN (
    SELECT lado_log_fingerprints.id FROM lado_log_fingerprints WHERE lado_log_fingerprints.project_id = ?
)
ORDER BY fingerprint_id, instance;

-- name: ListLadoLogInstanceCounts :many
SELECT * FROM lado_log_instance_counts WHERE fingerprint_id = ? ORDER BY instance;

-- name: GetLadoLogInstanceCount :one
SELECT * FROM lado_log_instance_counts WHERE fingerprint_id = ? AND instance = ?;

-- name: UpsertLadoLogInstanceCount :exec
INSERT INTO lado_log_instance_counts (fingerprint_id, instance, image_tag, count, last_seen_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (fingerprint_id, instance) DO UPDATE SET
    image_tag = excluded.image_tag,
    count = excluded.count,
    last_seen_at = excluded.last_seen_at;
