-- LADO fork: stage timings and investigation metrics (issue #34, step 0).
-- A task's timeline is read from the records LADO already keeps; the
-- execution timings and session metrics are migrations_lado/0011.

-- name: GetLadoTaskTimingHead :one
SELECT id, project_id, issue_ref, issue_title, created_at
FROM lado_tasks
WHERE id = ?;

-- name: ListLadoPlanRequestTimings :many
SELECT id, kind, session_id, requested_at
FROM lado_plan_requests
WHERE task_id = ?
ORDER BY requested_at, id;

-- name: ListLadoPlanCritiqueSessionsByTask :many
SELECT c.session_id
FROM lado_plan_critiques c
JOIN lado_plan_requests r ON r.id = c.request_id
WHERE r.task_id = ? AND c.session_id <> ''
ORDER BY c.started_at, c.id;

-- name: ListLadoPlanRevisionTimes :many
SELECT created_at
FROM lado_plan_revisions
WHERE task_id = ?
ORDER BY created_at, id;

-- name: ListLadoPlanApprovalTimes :many
SELECT id, approved_at
FROM lado_plan_approvals
WHERE task_id = ?
ORDER BY approved_at, id;

-- name: ListLadoQueueEntryTimings :many
SELECT id, plan_approval_id, parts_started_at
FROM lado_queue_entries
WHERE task_id = ?
ORDER BY enqueued_at, id;

-- name: ListLadoExecutionTimings :many
-- Every execution of a task with the times the verification loop recorded
-- for it; an execution without a timings row has none.
SELECT e.session_id, e.role, e.part_id, e.plan_approval_id, e.queue_entry_id, e.started_at,
    e.settled_at, e.settled_pr_state,
    t.pr_seen_at, COALESCE(t.pr_seen_estimated, 0) AS pr_seen_estimated,
    t.verified_at, COALESCE(t.verified_estimated, 0) AS verified_estimated
FROM lado_executions e
LEFT JOIN lado_execution_timings t ON t.session_id = e.session_id
WHERE e.task_id = ?
ORDER BY e.started_at, e.session_id;

-- name: ListLadoIntegrationApprovalTimes :many
SELECT a.execution_session_id, a.approved_at
FROM lado_integration_approvals a
JOIN lado_executions e ON e.session_id = a.execution_session_id
WHERE e.task_id = ?
ORDER BY a.approved_at, a.id;

-- name: ListLadoTaskIDsForTimings :many
-- A project's tasks that have an issue, newest first.
SELECT id
FROM lado_tasks
WHERE project_id = ? AND issue_ref IS NOT NULL
ORDER BY created_at DESC, id DESC
LIMIT ?;

-- name: MarkLadoExecutionPRSeen :exec
-- The first time the loop sees an execution's PR; a time already recorded,
-- estimated or not, stays.
INSERT INTO lado_execution_timings (session_id, pr_url, pr_seen_at, pr_seen_estimated)
VALUES (sqlc.arg(session_id), sqlc.arg(pr_url), sqlc.arg(pr_seen_at), 0)
ON CONFLICT (session_id) DO UPDATE SET
    pr_url = excluded.pr_url,
    pr_seen_at = excluded.pr_seen_at,
    pr_seen_estimated = 0
WHERE lado_execution_timings.pr_seen_at IS NULL;

-- name: MarkLadoExecutionVerified :exec
-- The first time an execution's head passes or asks for confirmation; a time
-- already recorded stays.
INSERT INTO lado_execution_timings (session_id, verified_at, verified_estimated)
VALUES (sqlc.arg(session_id), sqlc.arg(verified_at), 0)
ON CONFLICT (session_id) DO UPDATE SET
    verified_at = excluded.verified_at,
    verified_estimated = 0
WHERE lado_execution_timings.verified_at IS NULL;

-- name: ListLadoSessionActivityFacts :many
-- What the investigation metrics read of a session's activities: the kind,
-- the ACP tool kind, the provider's tool name and the command, unwrapped from
-- its shell when the driver did that.
SELECT a.kind,
    CAST(CASE WHEN json_valid(a.detail_json)
        THEN COALESCE(json_extract(a.detail_json, '$.toolKind'), '') ELSE '' END AS TEXT) AS tool_kind,
    CAST(CASE WHEN json_valid(a.detail_json)
        THEN COALESCE(json_extract(a.detail_json, '$.providerToolName'), '') ELSE '' END AS TEXT) AS tool_name,
    CAST(CASE WHEN json_valid(a.detail_json)
        THEN COALESCE(NULLIF(json_extract(a.detail_json, '$.command'), ''), json_extract(a.detail_json, '$.rawCommand'), '')
        ELSE '' END AS TEXT) AS command,
    a.created_at, a.updated_at
FROM conversation_activities a
JOIN conversations c ON c.id = a.conversation_id
WHERE c.session_id = sqlc.arg(session_id)
ORDER BY a.conversation_id, a.sequence;

-- name: UpsertLadoSessionMetrics :exec
INSERT INTO lado_session_metrics (
    session_id, task_id, role, tool_calls, read_calls, search_calls, command_calls, edit_calls,
    first_activity_at, first_edit_at, last_activity_at, investigate_ms, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id) DO UPDATE SET
    task_id = excluded.task_id,
    role = excluded.role,
    tool_calls = excluded.tool_calls,
    read_calls = excluded.read_calls,
    search_calls = excluded.search_calls,
    command_calls = excluded.command_calls,
    edit_calls = excluded.edit_calls,
    first_activity_at = excluded.first_activity_at,
    first_edit_at = excluded.first_edit_at,
    last_activity_at = excluded.last_activity_at,
    investigate_ms = excluded.investigate_ms,
    updated_at = excluded.updated_at;

-- name: ListLadoSessionMetricsByTask :many
SELECT session_id, task_id, role, tool_calls, read_calls, search_calls, command_calls, edit_calls,
    first_activity_at, first_edit_at, last_activity_at, investigate_ms, updated_at
FROM lado_session_metrics
WHERE task_id = ?
ORDER BY first_activity_at, session_id;
