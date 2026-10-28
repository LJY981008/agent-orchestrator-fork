-- name: InsertLadoPlanRequest :one
INSERT INTO lado_plan_requests (
    id, task_id, session_id, issue_snapshot_sha256, source_comment_id, note,
    based_on_revision_id, requested_at, kind
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, task_id, session_id, issue_snapshot_sha256, source_comment_id, note,
    based_on_revision_id, requested_at, delivered_at, delivery_attempts, delivery_error, failed_at,
    kind, waiting_reason, worker_attempts, worker_started_at, draft_sha256, draft_submitted_at,
    feedback_sent_at, final_sha256, submitted_at, fix_requested_at, reminded_at, cleaned_at, failure_code;

-- name: GetLadoPlanRequest :one
SELECT id, task_id, session_id, issue_snapshot_sha256, source_comment_id, note,
    based_on_revision_id, requested_at, delivered_at, delivery_attempts, delivery_error, failed_at,
    kind, waiting_reason, worker_attempts, worker_started_at, draft_sha256, draft_submitted_at,
    feedback_sent_at, final_sha256, submitted_at, fix_requested_at, reminded_at, cleaned_at, failure_code
FROM lado_plan_requests
WHERE id = ?;

-- name: ListLadoPlanRequestsByTask :many
SELECT id, task_id, session_id, issue_snapshot_sha256, source_comment_id, note,
    based_on_revision_id, requested_at, delivered_at, delivery_attempts, delivery_error, failed_at,
    kind, waiting_reason, worker_attempts, worker_started_at, draft_sha256, draft_submitted_at,
    feedback_sent_at, final_sha256, submitted_at, fix_requested_at, reminded_at, cleaned_at, failure_code
FROM lado_plan_requests
WHERE task_id = ?
ORDER BY requested_at, id;

-- name: ListLadoPlanRequestsForLiveTasks :many
SELECT r.id, r.task_id, r.session_id, r.issue_snapshot_sha256, r.source_comment_id, r.note,
    r.based_on_revision_id, r.requested_at, r.delivered_at, r.delivery_attempts, r.delivery_error, r.failed_at,
    r.kind, r.waiting_reason, r.worker_attempts, r.worker_started_at, r.draft_sha256, r.draft_submitted_at,
    r.feedback_sent_at, r.final_sha256, r.submitted_at, r.fix_requested_at, r.reminded_at, r.cleaned_at, r.failure_code
FROM lado_plan_requests r
JOIN lado_tasks t ON t.id = r.task_id
WHERE t.cancelled_at IS NULL
ORDER BY r.task_id, r.requested_at, r.id;

-- name: ListLadoPlanRequestsByProject :many
SELECT r.id, r.task_id, r.session_id, r.issue_snapshot_sha256, r.source_comment_id, r.note,
    r.based_on_revision_id, r.requested_at, r.delivered_at, r.delivery_attempts, r.delivery_error, r.failed_at,
    r.kind, r.waiting_reason, r.worker_attempts, r.worker_started_at, r.draft_sha256, r.draft_submitted_at,
    r.feedback_sent_at, r.final_sha256, r.submitted_at, r.fix_requested_at, r.reminded_at, r.cleaned_at, r.failure_code
FROM lado_plan_requests r
JOIN lado_tasks t ON t.id = r.task_id
WHERE t.project_id = ?
ORDER BY r.task_id, r.requested_at, r.id;

-- name: MarkLadoPlanRequestDelivered :execrows
UPDATE lado_plan_requests
SET delivered_at = ?,
    delivery_attempts = delivery_attempts + 1,
    delivery_error = ''
WHERE id = ? AND kind = 'orchestrator' AND delivered_at IS NULL AND failed_at IS NULL;

-- name: RecordLadoPlanRequestAttempt :execrows
UPDATE lado_plan_requests
SET delivery_attempts = delivery_attempts + 1,
    delivery_error = ?
WHERE id = ? AND kind = 'orchestrator' AND delivered_at IS NULL AND failed_at IS NULL;

-- name: FailLadoPlanRequest :execrows
UPDATE lado_plan_requests
SET failed_at = ?,
    delivery_error = ?
WHERE id = ? AND kind = 'orchestrator' AND delivered_at IS NULL AND failed_at IS NULL;

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

-- name: ListOpenLadoPlanWorkerRequests :many
SELECT r.id, r.task_id, r.session_id, r.issue_snapshot_sha256, r.source_comment_id, r.note,
    r.based_on_revision_id, r.requested_at, r.delivered_at, r.delivery_attempts, r.delivery_error, r.failed_at,
    r.kind, r.waiting_reason, r.worker_attempts, r.worker_started_at, r.draft_sha256, r.draft_submitted_at,
    r.feedback_sent_at, r.final_sha256, r.submitted_at, r.fix_requested_at, r.reminded_at, r.cleaned_at, r.failure_code
FROM lado_plan_requests r
WHERE r.kind = 'worker' AND r.failed_at IS NULL AND r.cleaned_at IS NULL
ORDER BY r.requested_at, r.id;

-- name: SetLadoPlanRequestWaiting :execrows
UPDATE lado_plan_requests
SET waiting_reason = ?
WHERE id = ? AND kind = 'worker' AND worker_started_at IS NULL AND failed_at IS NULL AND cleaned_at IS NULL;

-- name: BeginLadoPlanWorkerAttempt :one
UPDATE lado_plan_requests
SET worker_attempts = worker_attempts + 1
WHERE id = ? AND kind = 'worker' AND worker_started_at IS NULL AND failed_at IS NULL AND cleaned_at IS NULL
RETURNING worker_attempts;

-- name: StartLadoPlanWorker :execrows
UPDATE lado_plan_requests
SET session_id = ?,
    worker_started_at = ?,
    waiting_reason = ''
WHERE id = ? AND kind = 'worker' AND worker_started_at IS NULL AND failed_at IS NULL AND cleaned_at IS NULL;

-- name: RecordLadoPlanDraft :execrows
UPDATE lado_plan_requests
SET draft_sha256 = ?,
    draft_submitted_at = ?
WHERE id = ? AND kind = 'worker' AND worker_started_at IS NOT NULL AND draft_submitted_at IS NULL
    AND failed_at IS NULL AND cleaned_at IS NULL;

-- name: RecordLadoPlanFeedback :execrows
UPDATE lado_plan_requests
SET feedback_sent_at = ?
WHERE id = ? AND kind = 'worker' AND draft_submitted_at IS NOT NULL AND feedback_sent_at IS NULL
    AND failed_at IS NULL AND cleaned_at IS NULL;

-- name: RecordLadoPlanFinal :execrows
UPDATE lado_plan_requests
SET final_sha256 = ?,
    submitted_at = ?
WHERE id = ? AND kind = 'worker' AND feedback_sent_at IS NOT NULL AND failed_at IS NULL AND cleaned_at IS NULL;

-- name: RecordLadoPlanFixRequested :execrows
UPDATE lado_plan_requests
SET fix_requested_at = ?
WHERE id = ? AND kind = 'worker' AND fix_requested_at IS NULL AND failed_at IS NULL AND cleaned_at IS NULL;

-- name: RecordLadoPlanReminder :execrows
UPDATE lado_plan_requests
SET reminded_at = ?
WHERE id = ? AND kind = 'worker' AND failed_at IS NULL AND cleaned_at IS NULL;

-- name: CleanLadoPlanRequest :execrows
UPDATE lado_plan_requests
SET cleaned_at = ?
WHERE id = ? AND kind = 'worker' AND failed_at IS NULL AND cleaned_at IS NULL;

-- name: FailLadoPlanWorkerRequest :execrows
UPDATE lado_plan_requests
SET failed_at = ?,
    failure_code = ?,
    delivery_error = ?
WHERE id = ? AND kind = 'worker' AND failed_at IS NULL AND cleaned_at IS NULL;

-- name: CountRunningLadoPlanWorkers :one
SELECT COUNT(*)
FROM lado_plan_requests
WHERE kind = 'worker' AND worker_started_at IS NOT NULL AND failed_at IS NULL AND cleaned_at IS NULL;

-- name: InsertLadoPlanCritique :exec
INSERT INTO lado_plan_critiques (
    id, request_id, attempt, session_id, state, skip_reason, failure_reason, findings_json, started_at, finished_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetLadoPlanCritique :one
SELECT id, request_id, attempt, session_id, state, skip_reason, failure_reason, findings_json, started_at, finished_at
FROM lado_plan_critiques
WHERE request_id = ? AND attempt = ?;

-- name: ListLadoPlanCritiquesByRequest :many
SELECT id, request_id, attempt, session_id, state, skip_reason, failure_reason, findings_json, started_at, finished_at
FROM lado_plan_critiques
WHERE request_id = ?
ORDER BY attempt;

-- name: ListLadoPlanCritiquesByTask :many
SELECT c.id, c.request_id, c.attempt, c.session_id, c.state, c.skip_reason, c.failure_reason, c.findings_json,
    c.started_at, c.finished_at
FROM lado_plan_critiques c
JOIN lado_plan_requests r ON r.id = c.request_id
WHERE r.task_id = ?
ORDER BY r.requested_at, r.id, c.attempt;

-- name: ListLatestLadoPlanCritiquesByProject :many
SELECT c.id, c.request_id, c.attempt, c.session_id, c.state, c.skip_reason, c.failure_reason, c.findings_json,
    c.started_at, c.finished_at
FROM lado_plan_critiques c
JOIN lado_plan_requests r ON r.id = c.request_id
JOIN lado_tasks t ON t.id = r.task_id
WHERE t.project_id = ?
  AND c.attempt = (SELECT MAX(c2.attempt) FROM lado_plan_critiques c2 WHERE c2.request_id = c.request_id);

-- name: SetLadoPlanCritiqueSession :execrows
UPDATE lado_plan_critiques
SET session_id = ?
WHERE lado_plan_critiques.request_id = ? AND lado_plan_critiques.attempt = ?
    AND lado_plan_critiques.state = 'running' AND lado_plan_critiques.session_id = '';

-- name: FinishLadoPlanCritique :execrows
UPDATE lado_plan_critiques
SET state = ?,
    failure_reason = ?,
    findings_json = ?,
    finished_at = ?
WHERE lado_plan_critiques.request_id = ? AND lado_plan_critiques.attempt = ?
    AND lado_plan_critiques.state = 'running';

-- name: UpsertLadoPlanRevisionMetrics :exec
INSERT INTO lado_plan_revision_metrics (
    revision_id, request_id, design_ms, critique_ms, revise_ms, review, p1_count, major_count, minor_count,
    addressed_count, not_addressed_count, sessions_json, change_kinds_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (revision_id) DO UPDATE SET
    design_ms = excluded.design_ms,
    critique_ms = excluded.critique_ms,
    revise_ms = excluded.revise_ms,
    review = excluded.review,
    p1_count = excluded.p1_count,
    major_count = excluded.major_count,
    minor_count = excluded.minor_count,
    addressed_count = excluded.addressed_count,
    not_addressed_count = excluded.not_addressed_count,
    sessions_json = excluded.sessions_json,
    change_kinds_json = excluded.change_kinds_json,
    updated_at = excluded.updated_at;

-- name: ListLadoPlanRevisionMetricsByTask :many
SELECT m.revision_id, m.request_id, m.design_ms, m.critique_ms, m.revise_ms, m.review, m.p1_count, m.major_count,
    m.minor_count, m.addressed_count, m.not_addressed_count, m.sessions_json, m.change_kinds_json, m.updated_at
FROM lado_plan_revision_metrics m
JOIN lado_plan_revisions v ON v.id = m.revision_id
WHERE v.task_id = ?
ORDER BY v.number;

-- name: GetLadoPlanRevisionMetrics :one
SELECT revision_id, request_id, design_ms, critique_ms, revise_ms, review, p1_count, major_count,
    minor_count, addressed_count, not_addressed_count, sessions_json, change_kinds_json, updated_at
FROM lado_plan_revision_metrics
WHERE revision_id = ?;

-- name: SumLadoSessionUsage :one
SELECT
    CAST(COALESCE(SUM(mue.input_tokens), 0) AS INTEGER) AS input_tokens,
    CAST(COALESCE(SUM(mue.output_tokens), 0) AS INTEGER) AS output_tokens,
    CAST(COALESCE(SUM(mue.cached_input_tokens), 0) AS INTEGER) AS cache_read_tokens,
    CAST(COALESCE(SUM(CASE WHEN json_valid(mue.provider_usage_json)
        THEN json_extract(mue.provider_usage_json, '$.cache_creation_input_tokens') END), 0) AS INTEGER) AS cache_write_tokens,
    CAST(COUNT(mue.estimated_cost_nanos) AS INTEGER) AS priced_events,
    CAST(COALESCE(SUM(mue.estimated_cost_nanos), 0) AS INTEGER) AS cost_nanos
FROM model_usage_events mue
JOIN usage_bindings ub ON ub.id = mue.binding_id
WHERE ub.session_id = ?;
