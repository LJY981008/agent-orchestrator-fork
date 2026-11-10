-- LADO fork: worker watch (issue #42). Settings, the session watch, account
-- limits and alerts are migrations_lado/0012. Keep this file ASCII: sqlc
-- miscounts offsets after non-ASCII text.

-- name: GetLadoWatchSettings :one
SELECT project_id, compact_window, task_cost_cents, session_cost_cents, session_compactions,
    stall_minutes, wake_enabled, updated_at
FROM lado_watch_settings
WHERE project_id = ?;

-- name: UpsertLadoWatchSettings :exec
-- Writes a whole settings row; a NULL follows the row below it.
INSERT INTO lado_watch_settings (
    project_id, compact_window, task_cost_cents, session_cost_cents, session_compactions,
    stall_minutes, wake_enabled, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id) DO UPDATE SET
    compact_window = excluded.compact_window,
    task_cost_cents = excluded.task_cost_cents,
    session_cost_cents = excluded.session_cost_cents,
    session_compactions = excluded.session_compactions,
    stall_minutes = excluded.stall_minutes,
    wake_enabled = excluded.wake_enabled,
    updated_at = excluded.updated_at;

-- name: EnsureLadoSessionWatch :execrows
-- Makes a session's watch row once; an existing row stays as it is.
INSERT OR IGNORE INTO lado_session_watch (session_id, task_id, created_at, updated_at)
VALUES (sqlc.arg(session_id), sqlc.arg(task_id), sqlc.arg(at), sqlc.arg(at));

-- name: UpdateLadoSessionWatchCompactions :execrows
-- The compaction counter's columns only.
UPDATE lado_session_watch
SET read_offsets_json = sqlc.arg(read_offsets_json),
    compactions = sqlc.arg(compactions),
    compacted_at = sqlc.arg(compacted_at),
    updated_at = sqlc.arg(updated_at)
WHERE session_id = sqlc.arg(session_id);

-- name: UpdateLadoSessionWatchLimit :execrows
-- The limit watch's columns only.
UPDATE lado_session_watch
SET limit_state = sqlc.arg(limit_state),
    limit_reset_at = sqlc.arg(limit_reset_at),
    limit_reset_guess = sqlc.arg(limit_reset_guess),
    limit_seen_at = sqlc.arg(limit_seen_at),
    resumes = sqlc.arg(resumes),
    resumed_at = sqlc.arg(resumed_at),
    limit_stops = sqlc.arg(limit_stops),
    updated_at = sqlc.arg(updated_at)
WHERE session_id = sqlc.arg(session_id);

-- name: UpdateLadoSessionWatchStall :execrows
-- The stall watch's columns only.
UPDATE lado_session_watch
SET stall_state = sqlc.arg(stall_state),
    stalled_at = sqlc.arg(stalled_at),
    nudges = sqlc.arg(nudges),
    nudged_at = sqlc.arg(nudged_at),
    stalls = sqlc.arg(stalls),
    updated_at = sqlc.arg(updated_at)
WHERE session_id = sqlc.arg(session_id);

-- name: GetLadoSessionWatch :one
SELECT session_id, task_id, read_offsets_json, compactions, compacted_at,
    limit_state, limit_reset_at, limit_reset_guess, limit_seen_at, resumes, resumed_at, limit_stops,
    stall_state, stalled_at, nudges, nudged_at, stalls, created_at, updated_at
FROM lado_session_watch
WHERE session_id = ?;

-- name: ListLadoSessionWatchByTask :many
SELECT session_id, task_id, read_offsets_json, compactions, compacted_at,
    limit_state, limit_reset_at, limit_reset_guess, limit_seen_at, resumes, resumed_at, limit_stops,
    stall_state, stalled_at, nudges, nudged_at, stalls, created_at, updated_at
FROM lado_session_watch
WHERE task_id = ?
ORDER BY created_at, session_id;

-- name: UpsertLadoAccountLimit :exec
-- Records an account at its limit. A record over a live row (not released,
-- reset still ahead) replaces the reset time and keeps when LADO first saw the
-- limit and the first known identity. A record over a released or expired row
-- starts the limit again: a new detected_at and identity, and no release.
-- Every value in SET reads the row as it was before the update.
INSERT INTO lado_account_limits (account_key, session_id, identity, reset_at, reset_guess, detected_at, updated_at)
VALUES (sqlc.arg(account_key), sqlc.arg(session_id), sqlc.arg(identity), sqlc.arg(reset_at), sqlc.arg(reset_guess), sqlc.arg(detected_at), sqlc.arg(updated_at))
ON CONFLICT (account_key) DO UPDATE SET
    session_id = excluded.session_id,
    identity = CASE
        WHEN lado_account_limits.released_at IS NULL AND lado_account_limits.reset_at > excluded.detected_at
            AND lado_account_limits.identity <> '' THEN lado_account_limits.identity
        ELSE excluded.identity
    END,
    reset_at = excluded.reset_at,
    reset_guess = excluded.reset_guess,
    detected_at = CASE
        WHEN lado_account_limits.released_at IS NULL AND lado_account_limits.reset_at > excluded.detected_at
            THEN lado_account_limits.detected_at
        ELSE excluded.detected_at
    END,
    released_at = NULL,
    release_reason = '',
    released_waiting = 0,
    updated_at = excluded.updated_at;

-- name: MarkLadoAccountLimitReleased :execrows
-- Releases a limit that holds; a released row is not written again, so of
-- callers racing to release it only one sees a row.
UPDATE lado_account_limits
SET released_at = sqlc.arg(released_at),
    release_reason = sqlc.arg(release_reason),
    released_waiting = 0,
    updated_at = sqlc.arg(released_at)
WHERE account_key = sqlc.arg(account_key) AND released_at IS NULL;

-- name: SetLadoAccountLimitReleasedWaiting :execrows
-- Records how many waits a released limit left to start.
UPDATE lado_account_limits
SET released_waiting = sqlc.arg(released_waiting)
WHERE account_key = sqlc.arg(account_key) AND released_at IS NOT NULL;

-- name: DeleteLadoAccountLimit :execrows
DELETE FROM lado_account_limits WHERE account_key = ?;

-- name: GetLadoAccountLimit :one
SELECT account_key, session_id, reset_at, reset_guess, detected_at, updated_at,
    identity, released_at, release_reason, released_waiting
FROM lado_account_limits
WHERE account_key = ?;

-- name: ListLadoAccountLimits :many
SELECT account_key, session_id, reset_at, reset_guess, detected_at, updated_at,
    identity, released_at, release_reason, released_waiting
FROM lado_account_limits
ORDER BY account_key;

-- name: CountLadoAccountLimitedWaits :one
-- Counts what waits now for an account limit: open queue entries no lease
-- holds, waiting parts and plan worker requests not started, whose waiting
-- reason is the given one. A wait does not record the provider.
SELECT CAST(
    (SELECT COUNT(*) FROM lado_queue_entries q
        WHERE q.waiting_reason = sqlc.arg(reason)
          AND q.dispatched_session_id IS NULL AND q.failed_at IS NULL AND q.superseded_at IS NULL
          AND q.cancelled_at IS NULL AND q.review_required_reason = '' AND q.blocked_reason = ''
          AND q.parts_started_at IS NULL AND q.lease_token IS NULL)
    + (SELECT COUNT(*) FROM lado_task_parts p
        WHERE p.waiting_reason = sqlc.arg(reason) AND p.state = 'waiting')
    + (SELECT COUNT(*) FROM lado_plan_requests r
        WHERE r.waiting_reason = sqlc.arg(reason) AND r.kind = 'worker'
          AND r.worker_started_at IS NULL AND r.failed_at IS NULL AND r.cleaned_at IS NULL)
    AS INTEGER) AS waits;

-- name: InsertLadoAlert :one
-- Raises an alert once; a second raise of the same task, session, kind and
-- level returns no row.
INSERT INTO lado_alerts (task_id, session_id, kind, level, value, threshold, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (task_id, session_id, kind, level) DO NOTHING
RETURNING id;

-- name: MarkLadoAlertSeen :execrows
UPDATE lado_alerts
SET seen_at = sqlc.arg(seen_at)
WHERE id = sqlc.arg(id) AND seen_at IS NULL;

-- name: GetLadoAlert :one
SELECT a.id, a.task_id, a.session_id, a.kind, a.level, a.value, a.threshold, a.created_at, a.seen_at,
    t.project_id, t.issue_ref
FROM lado_alerts a
JOIN lado_tasks t ON t.id = a.task_id
WHERE a.id = ?;

-- name: ListLadoAlertsByProject :many
-- A project's alerts, newest first; only the unseen ones when unseen_only.
SELECT a.id, a.task_id, a.session_id, a.kind, a.level, a.value, a.threshold, a.created_at, a.seen_at,
    t.project_id, t.issue_ref
FROM lado_alerts a
JOIN lado_tasks t ON t.id = a.task_id
WHERE t.project_id = sqlc.arg(project_id)
    AND (CAST(sqlc.arg(unseen_only) AS INTEGER) = 0 OR a.seen_at IS NULL)
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg(max_rows);

-- name: CountLadoUnseenAlertsByProject :many
-- How many alerts of each of a project's tasks nobody marked seen, over
-- every alert, for the board's badges.
SELECT a.task_id, CAST(COUNT(*) AS INTEGER) AS unseen
FROM lado_alerts a
JOIN lado_tasks t ON t.id = a.task_id
WHERE t.project_id = sqlc.arg(project_id) AND a.seen_at IS NULL
GROUP BY a.task_id
ORDER BY a.task_id;

-- name: ListLadoAlertsByTask :many
SELECT a.id, a.task_id, a.session_id, a.kind, a.level, a.value, a.threshold, a.created_at, a.seen_at,
    t.project_id, t.issue_ref
FROM lado_alerts a
JOIN lado_tasks t ON t.id = a.task_id
WHERE a.task_id = ?
ORDER BY a.created_at DESC, a.id DESC;

-- name: ListLadoAlertsSince :many
-- Alerts raised at or after since, oldest first, with the task's project and
-- issue.
SELECT a.id, a.task_id, a.session_id, a.kind, a.level, a.value, a.threshold, a.created_at, a.seen_at,
    t.project_id, t.issue_ref
FROM lado_alerts a
JOIN lado_tasks t ON t.id = a.task_id
WHERE a.created_at >= sqlc.arg(since)
ORDER BY a.created_at, a.id;

-- name: ListLadoPlanWorkerSessionsByTask :many
-- Every plan worker session of a task's plan requests, each attempt and
-- role: sessions whose client request id starts with lado:plan/<request>/.
SELECT r.id AS request_id, s.id AS session_id, s.client_request_id, s.created_at
FROM lado_plan_requests r
JOIN sessions s ON substr(s.client_request_id, 1, length('lado:plan/' || r.id || '/')) = 'lado:plan/' || r.id || '/'
WHERE r.task_id = ?
ORDER BY s.created_at, s.id;

-- name: GetLadoSessionSubagentUsage :one
-- What a session's subagents used: the events of its usage sources that
-- name a subagent.
SELECT
    CAST(COUNT(mue.id) AS INTEGER) AS event_count,
    CAST(COALESCE(SUM(mue.input_tokens), 0) AS INTEGER) AS input_tokens,
    CAST(COALESCE(SUM(mue.output_tokens), 0) AS INTEGER) AS output_tokens,
    CAST(COUNT(mue.estimated_cost_nanos) AS INTEGER) AS priced_event_count,
    CAST(COALESCE(SUM(mue.estimated_cost_nanos), 0) AS INTEGER) AS priced_total_nanos
FROM model_usage_events mue
JOIN usage_bindings ub ON ub.id = mue.binding_id
JOIN usage_sources us ON us.id = mue.usage_source_id
WHERE ub.session_id = sqlc.arg(session_id) AND us.subagent_id <> '';

-- name: ListLadoSessionTranscriptSources :many
-- The conversation files of a session that the usage pipeline reads: Claude's
-- main transcript and the session's own Codex rollouts. A subagent's rollout,
-- which the collector registers under its parent's binding, is left out.
SELECT DISTINCT us.kind, us.artifact_path
FROM usage_sources us
JOIN usage_bindings ub ON ub.id = us.binding_id
WHERE ub.session_id = sqlc.arg(session_id) AND us.kind IN ('claude_main', 'codex_rollout') AND us.subagent_id = ''
ORDER BY us.kind, us.artifact_path;
