-- LADO fork: verification of an execution's PR head, its evidence and the
-- integration approval (docs/LJY-PRD/TDD.md sections 7 and 8). Keep this file
-- ASCII: sqlc 1.31 cuts a query after a non-ASCII character.

-- name: InsertLadoCheckRun :exec
INSERT INTO lado_check_runs (
    id, execution_session_id, head_sha, name, command, status, started_at
) VALUES (?, ?, ?, ?, ?, 'running', ?);

-- name: FinishLadoCheckRun :execrows
-- Only a running run finishes: a final state stays as first recorded.
UPDATE lado_check_runs
SET status = sqlc.arg(status),
    exit_code = sqlc.narg(exit_code),
    log_sha256 = sqlc.arg(log_sha256),
    error_message = sqlc.arg(error_message),
    finished_at = sqlc.arg(finished_at)
WHERE lado_check_runs.id = sqlc.arg(id) AND lado_check_runs.status = 'running';

-- name: CancelRunningLadoCheckRuns :execrows
UPDATE lado_check_runs
SET status = 'cancelled', finished_at = sqlc.arg(finished_at)
WHERE lado_check_runs.status = 'running';

-- name: GetLadoCheckRun :one
SELECT id, execution_session_id, head_sha, name, command, status, exit_code, log_sha256, error_message,
    started_at, finished_at
FROM lado_check_runs
WHERE id = ?;

-- name: ListLadoCheckRunsForHead :many
SELECT id, execution_session_id, head_sha, name, command, status, exit_code, log_sha256, error_message,
    started_at, finished_at
FROM lado_check_runs
WHERE execution_session_id = ? AND head_sha = ?
ORDER BY started_at, id;

-- name: GetLadoVerification :one
SELECT execution_session_id, head_sha, pr_url, outcome, reason, content_json, sha256, notified_json, computed_at
FROM lado_verifications
WHERE execution_session_id = ? AND head_sha = ?;

-- name: UpsertLadoVerification :exec
-- One record per execution and head. Only a change of outcome, reason or
-- sha256 publishes a change event (the update trigger's condition).
INSERT INTO lado_verifications (
    execution_session_id, head_sha, pr_url, outcome, reason, content_json, sha256, notified_json, computed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (execution_session_id, head_sha) DO UPDATE SET
    pr_url = excluded.pr_url,
    outcome = excluded.outcome,
    reason = excluded.reason,
    content_json = excluded.content_json,
    sha256 = excluded.sha256,
    notified_json = excluded.notified_json,
    computed_at = excluded.computed_at;

-- name: ListLadoVerificationOutcomes :many
-- The outcome and reason of the records of the given executions and heads.
-- The caller keeps the (execution, head) pairs it asked for.
SELECT execution_session_id, head_sha, outcome, reason
FROM lado_verifications
WHERE execution_session_id IN (sqlc.slice('session_ids'))
  AND head_sha IN (sqlc.slice('head_shas'));

-- name: InsertLadoExecutionReview :exec
INSERT INTO lado_execution_reviews (
    review_run_id, execution_session_id, head_sha, round, created_at
) VALUES (?, ?, ?, ?, ?);

-- name: CountLadoExecutionReviews :one
SELECT COUNT(*)
FROM lado_execution_reviews
WHERE execution_session_id = ?;

-- name: ListLadoExecutionReviews :many
SELECT review_run_id, execution_session_id, head_sha, round, created_at, cancelled_at
FROM lado_execution_reviews
WHERE execution_session_id = ?
ORDER BY round, created_at, review_run_id;

-- name: GetLadoExecutionReview :one
SELECT review_run_id, execution_session_id, head_sha, round, created_at, cancelled_at
FROM lado_execution_reviews
WHERE review_run_id = ?;

-- name: MarkLadoExecutionReviewCancelled :execrows
UPDATE lado_execution_reviews
SET cancelled_at = sqlc.arg(cancelled_at)
WHERE lado_execution_reviews.review_run_id = sqlc.arg(review_run_id)
  AND lado_execution_reviews.cancelled_at IS NULL;

-- name: DeleteLadoEvidenceLinksForHead :exec
DELETE FROM lado_evidence_links
WHERE execution_session_id = ? AND head_sha = ?;

-- name: InsertLadoEvidenceLink :exec
INSERT INTO lado_evidence_links (
    id, execution_session_id, head_sha, criterion, state, ref_kind, ref_key, sha, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListLadoEvidenceLinksForHead :many
SELECT id, execution_session_id, head_sha, criterion, state, ref_kind, ref_key, sha, created_at
FROM lado_evidence_links
WHERE execution_session_id = ? AND head_sha = ?
ORDER BY criterion, id;

-- name: ListLadoCommitChecks :many
-- The CI checks AO recorded for one commit of a PR.
SELECT pr_url, name, commit_hash, status, url, log_tail, created_at, conclusion, details
FROM pr_checks
WHERE pr_url = ? AND commit_hash = ?
ORDER BY name, created_at;

-- name: ListLadoExecutionPRs :many
-- The PRs of the given sessions, newest first within each session: by update
-- time, then by URL, as ListLadoExecutionsToVerify picks the newest.
SELECT pr.session_id, pr.url, pr.number, pr.pr_state, pr.head_sha, pr.target_branch, pr.title
FROM pr
WHERE pr.session_id IN (sqlc.slice('session_ids'))
ORDER BY pr.session_id, pr.updated_at DESC, pr.url DESC;

-- name: ListLadoExecutionsToVerify :many
-- Executions whose session's newest PR is open, or merged or closed and not
-- settled yet, oldest first, with that PR and whether the session ended (as
-- in GetLadoExecution).
SELECT sqlc.embed(e),
    p.url AS pr_url, p.number AS pr_number, p.pr_state AS pr_state, p.head_sha AS pr_head_sha,
    p.target_branch AS pr_target_branch, p.title AS pr_title,
    NOT EXISTS (
        SELECT 1 FROM sessions s WHERE s.id = e.session_id AND s.is_terminated = 0
    ) AS session_terminated
FROM lado_executions e
JOIN pr p ON p.session_id = e.session_id
WHERE NOT EXISTS (
        SELECT 1 FROM pr newer
        WHERE newer.session_id = p.session_id
          AND (newer.updated_at > p.updated_at OR (newer.updated_at = p.updated_at AND newer.url > p.url))
    )
  AND (p.pr_state NOT IN ('merged', 'closed') OR e.settled_at IS NULL)
ORDER BY e.started_at, e.session_id
LIMIT sqlc.arg(max_executions);

-- name: SettleLadoExecution :execrows
UPDATE lado_executions
SET settled_at = sqlc.arg(settled_at)
WHERE lado_executions.session_id = sqlc.arg(session_id) AND lado_executions.settled_at IS NULL;

-- name: InsertLadoIntegrationApproval :exec
INSERT INTO lado_integration_approvals (
    id, execution_session_id, pr_url, head_sha, verification_sha256, acknowledged_json, proof_nonce, actor,
    approved_at, status_state, status_attempts, status_posted_at, status_error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetLadoIntegrationApprovalForHead :one
SELECT id, execution_session_id, pr_url, head_sha, verification_sha256, acknowledged_json, proof_nonce, actor,
    approved_at, status_state, status_attempts, status_posted_at, status_error
FROM lado_integration_approvals
WHERE execution_session_id = ? AND head_sha = ?;

-- name: SetLadoIntegrationStatus :execrows
UPDATE lado_integration_approvals
SET status_state = sqlc.arg(status_state),
    status_attempts = sqlc.arg(status_attempts),
    status_posted_at = sqlc.narg(status_posted_at),
    status_error = sqlc.arg(status_error)
WHERE lado_integration_approvals.id = sqlc.arg(id);

-- name: ListLadoIntegrationStatusRetries :many
-- Approvals whose commit status is still to post, with attempts left, oldest
-- first.
SELECT id, execution_session_id, pr_url, head_sha, verification_sha256, acknowledged_json, proof_nonce, actor,
    approved_at, status_state, status_attempts, status_posted_at, status_error
FROM lado_integration_approvals
WHERE status_state IN ('pending', 'failed') AND status_attempts < sqlc.arg(max_attempts)
ORDER BY approved_at, id
LIMIT sqlc.arg(max_approvals);

-- name: ListLadoIntegrationApprovalStates :many
-- The status state of the approvals of the given executions and heads. The
-- caller keeps the (execution, head) pairs it asked for.
SELECT execution_session_id, head_sha, status_state
FROM lado_integration_approvals
WHERE execution_session_id IN (sqlc.slice('session_ids'))
  AND head_sha IN (sqlc.slice('head_shas'));
