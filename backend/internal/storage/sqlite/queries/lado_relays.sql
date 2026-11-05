-- name: InsertLadoRelay :exec
INSERT INTO lado_relays (
    id, task_id, project_id, orchestrator_session_id, worker_session_id, worker_role, part_id,
    instruction, evidence, evidence_message_id, evidence_at, beyond_evidence, outside_plan,
    outside_plan_reasons_json, state, failure, created_at, delivered_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'sending', '', ?, NULL);

-- name: MarkLadoRelayDelivered :execrows
-- Only a relay still sending changes.
UPDATE lado_relays
SET state = 'delivered', delivered_at = sqlc.arg(delivered_at)
WHERE lado_relays.id = sqlc.arg(id) AND lado_relays.state = 'sending';

-- name: MarkLadoRelayFailed :execrows
-- Only a relay still sending changes.
UPDATE lado_relays
SET state = 'failed', failure = sqlc.arg(failure)
WHERE lado_relays.id = sqlc.arg(id) AND lado_relays.state = 'sending';

-- name: GetLadoRelay :one
SELECT *
FROM lado_relays
WHERE id = ?;

-- name: ListLadoRelaysByTask :many
-- Newest first.
SELECT *
FROM lado_relays
WHERE task_id = ?
ORDER BY created_at DESC, id DESC;

-- name: ListLadoRelayExecutionsByTask :many
-- Every execution of a task, of every role, newest first, with whether its
-- session ended and the session's activity. An execution without a session
-- row counts as ended, as in ListLadoExecutionsForTasks, and has no activity.
SELECT sqlc.embed(e),
    NOT EXISTS (
        SELECT 1 FROM sessions s WHERE s.id = e.session_id AND s.is_terminated = 0
    ) AS session_terminated,
    CAST(COALESCE((SELECT s.activity_state FROM sessions s WHERE s.id = e.session_id), '') AS TEXT) AS session_activity
FROM lado_executions e
WHERE e.task_id = ?
ORDER BY e.started_at DESC, e.session_id DESC;
