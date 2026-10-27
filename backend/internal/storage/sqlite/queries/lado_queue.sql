-- name: InsertLadoQueueEntry :exec
INSERT INTO lado_queue_entries (
    id, task_id, project_id, plan_approval_id, policy_snapshot_id, enqueued_at
) VALUES (?, ?, ?, ?, ?, ?);

-- name: SupersedeOpenLadoQueueEntries :execrows
UPDATE lado_queue_entries
SET superseded_at = ?
WHERE task_id = ?
  AND dispatched_session_id IS NULL AND failed_at IS NULL AND superseded_at IS NULL
  AND cancelled_at IS NULL AND review_required_reason = '' AND blocked_reason = '';

-- name: GetLadoQueueEntry :one
SELECT id, task_id, project_id, plan_approval_id, policy_snapshot_id, enqueued_at,
    attempt_count, next_attempt_at, lease_token, claimed_at, lease_expires_at,
    waiting_reason, review_required_reason, blocked_reason, error_message,
    dispatched_session_id, failed_at, superseded_at, cancelled_at
FROM lado_queue_entries
WHERE id = ?;

-- name: ListLadoQueueEntriesForTasks :many
SELECT id, task_id, project_id, plan_approval_id, policy_snapshot_id, enqueued_at,
    attempt_count, next_attempt_at, lease_token, claimed_at, lease_expires_at,
    waiting_reason, review_required_reason, blocked_reason, error_message,
    dispatched_session_id, failed_at, superseded_at, cancelled_at
FROM lado_queue_entries
WHERE task_id IN (sqlc.slice('task_ids'))
ORDER BY task_id, enqueued_at DESC, id DESC;

-- name: CancelOpenLadoQueueEntriesForTask :execrows
UPDATE lado_queue_entries
SET cancelled_at = ?
WHERE task_id = ?
  AND dispatched_session_id IS NULL AND failed_at IS NULL AND superseded_at IS NULL
  AND cancelled_at IS NULL AND review_required_reason = '' AND blocked_reason = '';

-- name: ListClaimableLadoQueueEntries :many
-- Open entries whose lease is empty or expired and whose wait is over, in
-- projects with LADO mode on, oldest first. full_projects is a JSON array of
-- the projects whose run slots are all taken; their entries are left alone.
SELECT id, task_id, project_id, plan_approval_id, policy_snapshot_id, enqueued_at,
    attempt_count, next_attempt_at, lease_token, claimed_at, lease_expires_at,
    waiting_reason, review_required_reason, blocked_reason, error_message,
    dispatched_session_id, failed_at, superseded_at, cancelled_at
FROM lado_queue_entries
WHERE dispatched_session_id IS NULL AND failed_at IS NULL AND superseded_at IS NULL
  AND cancelled_at IS NULL AND review_required_reason = '' AND blocked_reason = ''
  AND (lease_token IS NULL OR lease_expires_at <= sqlc.arg(now))
  AND (next_attempt_at IS NULL OR next_attempt_at <= sqlc.arg(now))
  AND project_id IN (SELECT project_id FROM lado_project_settings WHERE lado_mode = 1)
  AND project_id NOT IN (SELECT CAST(j.value AS TEXT) FROM json_each(sqlc.arg(full_projects)) AS j)
ORDER BY enqueued_at, id
LIMIT sqlc.arg(max_entries);

-- name: ClaimLadoQueueEntry :execrows
-- Compare-and-set: leases the entry only while it is still claimable, as
-- ListClaimableLadoQueueEntries reads it. Each claim counts one attempt.
UPDATE lado_queue_entries
SET lease_token = sqlc.arg(lease_token),
    claimed_at = sqlc.arg(now),
    lease_expires_at = sqlc.arg(lease_expires_at),
    attempt_count = attempt_count + 1,
    waiting_reason = ''
WHERE id = sqlc.arg(id)
  AND dispatched_session_id IS NULL AND failed_at IS NULL AND superseded_at IS NULL
  AND cancelled_at IS NULL AND review_required_reason = '' AND blocked_reason = ''
  AND (lease_token IS NULL OR lease_expires_at <= sqlc.arg(now))
  AND (next_attempt_at IS NULL OR next_attempt_at <= sqlc.arg(now))
  AND project_id IN (SELECT project_id FROM lado_project_settings WHERE lado_mode = 1)
  AND project_id NOT IN (SELECT CAST(j.value AS TEXT) FROM json_each(sqlc.arg(full_projects)) AS j);

-- name: ReleaseLadoQueueEntry :execrows
UPDATE lado_queue_entries
SET lease_token = NULL,
    claimed_at = NULL,
    lease_expires_at = NULL,
    next_attempt_at = sqlc.narg(next_attempt_at),
    waiting_reason = sqlc.arg(waiting_reason)
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: HoldLadoQueueEntry :execrows
-- A hold is not an attempt: it gives back the attempt its claim counted.
UPDATE lado_queue_entries
SET lease_token = NULL,
    claimed_at = NULL,
    lease_expires_at = NULL,
    attempt_count = attempt_count - 1,
    waiting_reason = sqlc.arg(waiting_reason),
    next_attempt_at = sqlc.narg(next_attempt_at)
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: SetLadoQueueWaiting :execrows
-- Why open entries no lease holds wait, in projects with LADO mode on, of one
-- project or of all when project_id is NULL. Attempts and next attempts stay.
-- An entry that already waits for the reason is not written again.
UPDATE lado_queue_entries
SET waiting_reason = sqlc.arg(reason)
WHERE dispatched_session_id IS NULL AND failed_at IS NULL AND superseded_at IS NULL
  AND cancelled_at IS NULL AND review_required_reason = '' AND blocked_reason = ''
  AND lease_token IS NULL
  AND waiting_reason <> sqlc.arg(reason)
  AND (lado_queue_entries.project_id = sqlc.narg(project_id) OR sqlc.narg(project_id) IS NULL)
  AND lado_queue_entries.project_id IN (SELECT project_id FROM lado_project_settings WHERE lado_mode = 1);

-- name: SetLadoQueueWaitingModeOff :execrows
-- SetLadoQueueWaiting for the entries of projects with LADO mode off.
UPDATE lado_queue_entries
SET waiting_reason = sqlc.arg(reason)
WHERE dispatched_session_id IS NULL AND failed_at IS NULL AND superseded_at IS NULL
  AND cancelled_at IS NULL AND review_required_reason = '' AND blocked_reason = ''
  AND lease_token IS NULL
  AND waiting_reason <> sqlc.arg(reason)
  AND (lado_queue_entries.project_id = sqlc.narg(project_id) OR sqlc.narg(project_id) IS NULL)
  AND lado_queue_entries.project_id NOT IN (SELECT project_id FROM lado_project_settings WHERE lado_mode = 1);

-- name: MarkLadoQueueEntryReviewRequired :execrows
UPDATE lado_queue_entries
SET lease_token = NULL,
    claimed_at = NULL,
    lease_expires_at = NULL,
    review_required_reason = sqlc.arg(reason)
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: MarkLadoQueueEntryBlocked :execrows
UPDATE lado_queue_entries
SET lease_token = NULL,
    claimed_at = NULL,
    lease_expires_at = NULL,
    blocked_reason = sqlc.arg(reason)
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: FailLadoQueueEntry :execrows
UPDATE lado_queue_entries
SET lease_token = NULL,
    claimed_at = NULL,
    lease_expires_at = NULL,
    failed_at = sqlc.arg(failed_at),
    error_message = sqlc.arg(error_message)
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: CancelLadoQueueEntry :execrows
UPDATE lado_queue_entries
SET lease_token = NULL,
    claimed_at = NULL,
    lease_expires_at = NULL,
    cancelled_at = sqlc.arg(cancelled_at)
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: SetLadoQueueEntryDispatched :execrows
UPDATE lado_queue_entries
SET dispatched_session_id = sqlc.arg(dispatched_session_id),
    lease_token = NULL,
    claimed_at = NULL,
    lease_expires_at = NULL
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: GetLadoQueueEntryPlan :one
-- The entry with the revision its approval names, that revision's sha256 and
-- base SHA, and the sha256 of the entry's rule snapshot.
SELECT sqlc.embed(e), r.id AS plan_revision_id, r.sha256 AS plan_sha256, r.base_sha AS plan_base_sha,
    p.sha256 AS snapshot_sha256
FROM lado_queue_entries e
JOIN lado_plan_approvals a ON a.id = e.plan_approval_id
JOIN lado_plan_revisions r ON r.id = a.revision_id
JOIN lado_policy_snapshots p ON p.id = e.policy_snapshot_id
WHERE e.id = ?;

-- name: GetLadoQueueEntrySnapshot :one
-- The rule snapshot JSON of an entry.
SELECT p.snapshot_json
FROM lado_queue_entries e
JOIN lado_policy_snapshots p ON p.id = e.policy_snapshot_id
WHERE e.id = ?;

-- name: ListOpenLadoQueueEntriesWithIssues :many
-- The queue view: open entries, of one project or of all when project_id is
-- NULL, with their task's issue, in the order the dispatcher takes them.
SELECT sqlc.embed(e), t.issue_ref, t.issue_title
FROM lado_queue_entries e
JOIN lado_tasks t ON t.id = e.task_id
WHERE (e.project_id = sqlc.narg(project_id) OR sqlc.narg(project_id) IS NULL)
  AND e.dispatched_session_id IS NULL AND e.failed_at IS NULL AND e.superseded_at IS NULL
  AND e.cancelled_at IS NULL AND e.review_required_reason = '' AND e.blocked_reason = ''
ORDER BY e.enqueued_at, e.id;

-- name: ListLeasedLadoQueueEntries :many
SELECT id, task_id, project_id, plan_approval_id, policy_snapshot_id, enqueued_at,
    attempt_count, next_attempt_at, lease_token, claimed_at, lease_expires_at,
    waiting_reason, review_required_reason, blocked_reason, error_message,
    dispatched_session_id, failed_at, superseded_at, cancelled_at
FROM lado_queue_entries
WHERE lease_token IS NOT NULL
ORDER BY claimed_at, id;

-- name: InsertLadoExecution :exec
INSERT INTO lado_executions (
    session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id,
    attempt, plan_base_sha, dispatch_base_sha, base_commits_ahead, started_at, deadline_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetLadoExecution :one
-- With whether the session ended, as in ListLadoExecutionsForTasks.
SELECT sqlc.embed(e),
    NOT EXISTS (
        SELECT 1 FROM sessions s WHERE s.id = e.session_id AND s.is_terminated = 0
    ) AS session_terminated
FROM lado_executions e
WHERE e.session_id = ?;

-- name: ListLadoExecutionsForTasks :many
-- With whether the session ended. An execution without a session row counts
-- as ended, as in CountLiveLadoExecutionsByProject.
SELECT sqlc.embed(e),
    NOT EXISTS (
        SELECT 1 FROM sessions s WHERE s.id = e.session_id AND s.is_terminated = 0
    ) AS session_terminated
FROM lado_executions e
WHERE e.task_id IN (sqlc.slice('task_ids'))
ORDER BY e.task_id, e.started_at DESC, e.session_id DESC;

-- name: ListLiveLadoExecutions :many
-- Executions whose session still runs, oldest first.
SELECT e.session_id, e.task_id, e.queue_entry_id, e.plan_revision_id, e.plan_approval_id, e.policy_snapshot_id,
    e.attempt, e.plan_base_sha, e.dispatch_base_sha, e.base_commits_ahead, e.started_at, e.deadline_at, e.timed_out_at,
    e.base_branch, e.base_remote, e.dispatch_local_base_sha, e.settled_at
FROM lado_executions e
JOIN sessions s ON s.id = e.session_id
WHERE s.is_terminated = 0
ORDER BY e.started_at, e.session_id;

-- name: CountLiveLadoExecutionsByProject :many
SELECT t.project_id, COUNT(*) AS live
FROM lado_executions e
JOIN sessions s ON s.id = e.session_id
JOIN lado_tasks t ON t.id = e.task_id
WHERE s.is_terminated = 0
GROUP BY t.project_id
ORDER BY t.project_id;

-- name: CountActiveLadoLeasesByProject :many
SELECT project_id, COUNT(*) AS leased
FROM lado_queue_entries
WHERE lease_token IS NOT NULL AND lease_expires_at > sqlc.arg(now)
GROUP BY project_id
ORDER BY project_id;

-- name: ListLadoExecutionsPastDeadline :many
-- Executions past their deadline whose session still runs. A NULL deadline
-- means no limit.
SELECT e.session_id, e.task_id, e.queue_entry_id, e.plan_revision_id, e.plan_approval_id, e.policy_snapshot_id,
    e.attempt, e.plan_base_sha, e.dispatch_base_sha, e.base_commits_ahead, e.started_at, e.deadline_at, e.timed_out_at,
    e.base_branch, e.base_remote, e.dispatch_local_base_sha, e.settled_at
FROM lado_executions e
JOIN sessions s ON s.id = e.session_id
WHERE e.deadline_at <= sqlc.arg(now)
  AND e.timed_out_at IS NULL
  AND s.is_terminated = 0
ORDER BY e.deadline_at, e.session_id;

-- name: MarkLadoExecutionTimedOut :execrows
UPDATE lado_executions
SET timed_out_at = ?
WHERE session_id = ? AND timed_out_at IS NULL;

-- name: GetLadoDispatchSettings :one
SELECT id, max_concurrent, max_run_minutes, hold_on_memory_tight, updated_at
FROM lado_dispatch_settings
WHERE id = 1;

-- name: UpsertLadoDispatchSettings :exec
INSERT INTO lado_dispatch_settings (id, max_concurrent, max_run_minutes, hold_on_memory_tight, updated_at)
VALUES (1, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    max_concurrent = excluded.max_concurrent,
    max_run_minutes = excluded.max_run_minutes,
    hold_on_memory_tight = excluded.hold_on_memory_tight,
    updated_at = excluded.updated_at;

-- name: GetLadoProjectMaxConcurrent :one
SELECT max_concurrent
FROM lado_project_settings
WHERE project_id = ?;

-- name: UpsertLadoProjectMaxConcurrent :exec
-- A project without a settings row gets one with LADO mode off.
INSERT INTO lado_project_settings (project_id, lado_mode, max_concurrent, updated_at)
VALUES (?, 0, ?, ?)
ON CONFLICT (project_id) DO UPDATE SET max_concurrent = excluded.max_concurrent, updated_at = excluded.updated_at;
