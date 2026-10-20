-- name: InsertLadoPlanRequest :one
INSERT INTO lado_plan_requests (
    id, task_id, session_id, issue_snapshot_sha256, source_comment_id, note,
    based_on_revision_id, requested_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, task_id, session_id, issue_snapshot_sha256, source_comment_id, note,
    based_on_revision_id, requested_at, delivered_at, delivery_attempts, delivery_error, failed_at;

-- name: GetLadoPlanRequest :one
SELECT id, task_id, session_id, issue_snapshot_sha256, source_comment_id, note,
    based_on_revision_id, requested_at, delivered_at, delivery_attempts, delivery_error, failed_at
FROM lado_plan_requests
WHERE id = ?;

-- name: ListLadoPlanRequestsByTask :many
SELECT id, task_id, session_id, issue_snapshot_sha256, source_comment_id, note,
    based_on_revision_id, requested_at, delivered_at, delivery_attempts, delivery_error, failed_at
FROM lado_plan_requests
WHERE task_id = ?
ORDER BY requested_at, id;

-- name: ListLadoPlanRequestsForLiveTasks :many
SELECT r.id, r.task_id, r.session_id, r.issue_snapshot_sha256, r.source_comment_id, r.note,
    r.based_on_revision_id, r.requested_at, r.delivered_at, r.delivery_attempts, r.delivery_error, r.failed_at
FROM lado_plan_requests r
JOIN lado_tasks t ON t.id = r.task_id
WHERE t.cancelled_at IS NULL
ORDER BY r.task_id, r.requested_at, r.id;

-- name: ListLadoPlanRequestsByProject :many
SELECT r.id, r.task_id, r.session_id, r.issue_snapshot_sha256, r.source_comment_id, r.note,
    r.based_on_revision_id, r.requested_at, r.delivered_at, r.delivery_attempts, r.delivery_error, r.failed_at
FROM lado_plan_requests r
JOIN lado_tasks t ON t.id = r.task_id
WHERE t.project_id = ?
ORDER BY r.task_id, r.requested_at, r.id;

-- name: MarkLadoPlanRequestDelivered :execrows
UPDATE lado_plan_requests
SET delivered_at = ?,
    delivery_attempts = delivery_attempts + 1,
    delivery_error = ''
WHERE id = ? AND delivered_at IS NULL AND failed_at IS NULL;

-- name: RecordLadoPlanRequestAttempt :execrows
UPDATE lado_plan_requests
SET delivery_attempts = delivery_attempts + 1,
    delivery_error = ?
WHERE id = ? AND delivered_at IS NULL AND failed_at IS NULL;

-- name: FailLadoPlanRequest :execrows
UPDATE lado_plan_requests
SET failed_at = ?,
    delivery_error = ?
WHERE id = ? AND delivered_at IS NULL AND failed_at IS NULL;

-- name: InsertLadoPlanRevision :one
INSERT INTO lado_plan_revisions (
    id, task_id, request_id, number, session_id, path, artifact_version, source,
    sha256, base_ref, base_sha, issue_snapshot_sha256, problems_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, task_id, request_id, number, session_id, path, artifact_version, source,
    sha256, base_ref, base_sha, issue_snapshot_sha256, problems_json, created_at;

-- name: GetLadoPlanRevision :one
SELECT id, task_id, request_id, number, session_id, path, artifact_version, source,
    sha256, base_ref, base_sha, issue_snapshot_sha256, problems_json, created_at
FROM lado_plan_revisions
WHERE id = ?;

-- name: ListLadoPlanRevisionsByTask :many
SELECT id, task_id, request_id, number, session_id, path, artifact_version, source,
    sha256, base_ref, base_sha, issue_snapshot_sha256, problems_json, created_at
FROM lado_plan_revisions
WHERE task_id = ?
ORDER BY number;

-- name: GetLatestLadoPlanRevision :one
SELECT id, task_id, request_id, number, session_id, path, artifact_version, source,
    sha256, base_ref, base_sha, issue_snapshot_sha256, problems_json, created_at
FROM lado_plan_revisions
WHERE task_id = ?
ORDER BY number DESC
LIMIT 1;

-- name: ListLatestLadoPlanRevisionsByProject :many
SELECT r.id, r.task_id, r.request_id, r.number, r.session_id, r.path, r.artifact_version, r.source,
    r.sha256, r.base_ref, r.base_sha, r.issue_snapshot_sha256, r.problems_json, r.created_at
FROM lado_plan_revisions r
JOIN lado_tasks t ON t.id = r.task_id
WHERE t.project_id = ?
  AND r.number = (SELECT MAX(r2.number) FROM lado_plan_revisions r2 WHERE r2.task_id = r.task_id);

-- name: CountLadoPlanRevisionsForTask :one
SELECT COUNT(*) FROM lado_plan_revisions WHERE task_id = ?;

-- name: CountPinnedLadoArtifactVersion :one
SELECT COUNT(*)
FROM lado_plan_revisions
WHERE session_id = ? AND path = ? AND artifact_version = ? AND sha256 = ?;

-- name: InsertLadoPresenceProof :exec
INSERT INTO lado_presence_proofs (nonce, action, subject_id, dev, expires_at, used_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: InsertLadoPlanApproval :one
INSERT INTO lado_plan_approvals (id, revision_id, task_id, sha256, proof_nonce, approved_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id, revision_id, task_id, sha256, proof_nonce, approved_at;

-- name: GetLatestLadoPlanApproval :one
SELECT id, revision_id, task_id, sha256, proof_nonce, approved_at
FROM lado_plan_approvals
WHERE task_id = ?
ORDER BY approved_at DESC, id DESC
LIMIT 1;

-- name: ListLadoPlanApprovalsByProject :many
SELECT a.id, a.revision_id, a.task_id, a.sha256, a.proof_nonce, a.approved_at
FROM lado_plan_approvals a
JOIN lado_tasks t ON t.id = a.task_id
WHERE t.project_id = ?
ORDER BY a.task_id, a.approved_at, a.id;

-- name: ListLadoPlanApprovalsByTask :many
SELECT id, revision_id, task_id, sha256, proof_nonce, approved_at
FROM lado_plan_approvals
WHERE task_id = ?
ORDER BY approved_at, id;
