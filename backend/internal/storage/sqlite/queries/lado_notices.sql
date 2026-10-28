-- name: GetLadoNoticeSince :one
SELECT since FROM lado_notice_state WHERE id = 1;

-- name: InsertLadoOrchestratorNotice :execrows
INSERT INTO lado_orchestrator_notices (project_id, notice_key, text, occurred_at, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (notice_key) DO NOTHING;

-- name: ListWaitingLadoOrchestratorNotices :many
SELECT id, project_id, notice_key, text, occurred_at, created_at, sent_at, sent_to, dropped_at
FROM lado_orchestrator_notices
WHERE sent_at IS NULL AND dropped_at IS NULL
ORDER BY project_id, occurred_at, id;

-- name: ListLadoOrchestratorNoticesByProject :many
SELECT id, project_id, notice_key, text, occurred_at, created_at, sent_at, sent_to, dropped_at
FROM lado_orchestrator_notices
WHERE project_id = ?
ORDER BY occurred_at, id;

-- name: MarkLadoOrchestratorNoticeSent :execrows
UPDATE lado_orchestrator_notices
SET sent_at = ?,
    sent_to = ?
WHERE id = ? AND sent_at IS NULL AND dropped_at IS NULL;

-- name: DropLadoOrchestratorNotice :execrows
UPDATE lado_orchestrator_notices
SET dropped_at = ?
WHERE id = ? AND sent_at IS NULL AND dropped_at IS NULL;

-- name: ListLadoPlanRequestNoticeFacts :many
SELECT r.id, t.project_id, t.issue_ref, r.kind, r.requested_at, r.failed_at, r.failure_code
FROM lado_plan_requests r
JOIN lado_tasks t ON t.id = r.task_id
WHERE t.issue_ref IS NOT NULL AND (r.requested_at >= sqlc.arg(since) OR r.failed_at >= sqlc.arg(since))
ORDER BY r.requested_at, r.id;

-- name: ListLadoPlanRevisionNoticeFacts :many
SELECT v.task_id, t.project_id, t.issue_ref, v.number, v.created_at
FROM lado_plan_revisions v
JOIN lado_tasks t ON t.id = v.task_id
WHERE t.issue_ref IS NOT NULL AND v.created_at >= sqlc.arg(since)
ORDER BY v.created_at, v.task_id, v.number;

-- name: ListLadoProjectsWithRunsSince :many
SELECT DISTINCT t.project_id
FROM lado_tasks t
WHERE EXISTS (
        SELECT 1 FROM lado_executions e WHERE e.task_id = t.id AND e.started_at >= sqlc.arg(since)
    ) OR EXISTS (
        SELECT 1 FROM lado_queue_entries q WHERE q.task_id = t.id AND q.failed_at >= sqlc.arg(since)
    )
ORDER BY t.project_id;
