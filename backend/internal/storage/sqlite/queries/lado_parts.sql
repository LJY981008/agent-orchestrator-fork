-- name: StartLadoPartRunEntry :execrows
-- Compare-and-set: the claimed entry starts its parts run and gives up its
-- lease; it is no longer open.
UPDATE lado_queue_entries
SET parts_started_at = sqlc.arg(parts_started_at),
    lease_token = NULL,
    claimed_at = NULL,
    lease_expires_at = NULL,
    waiting_reason = '',
    waiting_detail = ''
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: InsertLadoPartRun :exec
INSERT INTO lado_part_runs (
    queue_entry_id, task_id, project_id, plan_revision_id, repo_dir, branch, base_sha, head_sha,
    plan_base_sha, base_commits_ahead, base_branch, base_remote, dispatch_local_base_sha, started_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertLadoTaskPart :exec
INSERT INTO lado_task_parts (
    queue_entry_id, part_id, task_id, title, position, phase, steps_json, paths_json, depends_on_json,
    state, waiting_reason, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'waiting', ?, ?);

-- name: GetLadoPartRun :one
SELECT queue_entry_id, task_id, project_id, plan_revision_id, repo_dir, branch, base_sha, head_sha,
    plan_base_sha, base_commits_ahead, base_branch, base_remote, dispatch_local_base_sha, started_at,
    integration_attempts
FROM lado_part_runs
WHERE queue_entry_id = ?;

-- name: ListActiveLadoPartRuns :many
-- Parts runs whose integration worker has not started, that neither failed
-- nor were superseded or cancelled, in the order their plans were approved.
SELECT sqlc.embed(r), sqlc.embed(e)
FROM lado_part_runs r
JOIN lado_queue_entries e ON e.id = r.queue_entry_id
WHERE e.parts_started_at IS NOT NULL
  AND e.dispatched_session_id IS NULL AND e.failed_at IS NULL
  AND e.superseded_at IS NULL AND e.cancelled_at IS NULL
ORDER BY e.enqueued_at, e.id;

-- name: ListLadoTaskParts :many
SELECT queue_entry_id, part_id, task_id, title, position, phase, steps_json, paths_json, depends_on_json,
    state, attempts, merge_attempts, waiting_reason, waiting_detail, session_id, branch, start_sha,
    merge_from_sha, merge_tip_sha, merged_sha, merge_session_id, merge_branch, summary, error_message,
    updated_at
FROM lado_task_parts
WHERE queue_entry_id = ?
ORDER BY position, part_id;

-- name: ListLadoTaskPartsForEntries :many
SELECT queue_entry_id, part_id, task_id, title, position, phase, steps_json, paths_json, depends_on_json,
    state, attempts, merge_attempts, waiting_reason, waiting_detail, session_id, branch, start_sha,
    merge_from_sha, merge_tip_sha, merged_sha, merge_session_id, merge_branch, summary, error_message,
    updated_at
FROM lado_task_parts
WHERE queue_entry_id IN (sqlc.slice('entry_ids'))
ORDER BY queue_entry_id, position, part_id;

-- name: UpdateLadoTaskPart :execrows
-- Writes a part's progress. The caller read the row and passes its state as
-- old_state, so a write from a stale read changes nothing.
UPDATE lado_task_parts
SET state = sqlc.arg(state),
    attempts = sqlc.arg(attempts),
    merge_attempts = sqlc.arg(merge_attempts),
    waiting_reason = sqlc.arg(waiting_reason),
    waiting_detail = sqlc.arg(waiting_detail),
    session_id = sqlc.arg(session_id),
    branch = sqlc.arg(branch),
    start_sha = sqlc.arg(start_sha),
    merge_from_sha = sqlc.arg(merge_from_sha),
    merge_tip_sha = sqlc.arg(merge_tip_sha),
    merged_sha = sqlc.arg(merged_sha),
    merge_session_id = sqlc.arg(merge_session_id),
    merge_branch = sqlc.arg(merge_branch),
    summary = sqlc.arg(summary),
    error_message = sqlc.arg(error_message),
    updated_at = sqlc.arg(updated_at)
WHERE lado_task_parts.queue_entry_id = sqlc.arg(queue_entry_id)
  AND lado_task_parts.part_id = sqlc.arg(part_id)
  AND lado_task_parts.state = sqlc.arg(old_state);

-- name: SetLadoPartRunHead :execrows
-- Compare-and-set: moves the recorded integration head.
UPDATE lado_part_runs
SET head_sha = sqlc.arg(head_sha)
WHERE lado_part_runs.queue_entry_id = sqlc.arg(queue_entry_id)
  AND lado_part_runs.head_sha = sqlc.arg(old_head_sha);

-- name: FailLadoPartRun :execrows
UPDATE lado_queue_entries
SET failed_at = sqlc.arg(failed_at),
    error_message = sqlc.arg(error_message),
    part_spawn_request = '',
    part_spawn_expires_at = NULL
WHERE lado_queue_entries.id = sqlc.arg(id)
  AND lado_queue_entries.parts_started_at IS NOT NULL
  AND lado_queue_entries.dispatched_session_id IS NULL
  AND lado_queue_entries.failed_at IS NULL;

-- name: SupersedeLadoPartRun :execrows
UPDATE lado_queue_entries
SET superseded_at = sqlc.arg(at),
    part_spawn_request = '',
    part_spawn_expires_at = NULL
WHERE lado_queue_entries.id = sqlc.arg(id)
  AND lado_queue_entries.parts_started_at IS NOT NULL
  AND lado_queue_entries.dispatched_session_id IS NULL
  AND lado_queue_entries.superseded_at IS NULL AND lado_queue_entries.cancelled_at IS NULL;

-- name: CancelLadoPartRun :execrows
UPDATE lado_queue_entries
SET cancelled_at = sqlc.arg(at),
    part_spawn_request = '',
    part_spawn_expires_at = NULL
WHERE lado_queue_entries.id = sqlc.arg(id)
  AND lado_queue_entries.parts_started_at IS NOT NULL
  AND lado_queue_entries.dispatched_session_id IS NULL
  AND lado_queue_entries.superseded_at IS NULL AND lado_queue_entries.cancelled_at IS NULL;

-- name: SetLadoPartSpawnRequest :execrows
-- The dispatcher is about to start the worker of request; the worker gate
-- grants that spawn until expires_at. Another request still held keeps the
-- entry.
UPDATE lado_queue_entries
SET part_spawn_request = sqlc.arg(request),
    part_spawn_expires_at = sqlc.arg(expires_at)
WHERE lado_queue_entries.id = sqlc.arg(id)
  AND lado_queue_entries.parts_started_at IS NOT NULL
  AND lado_queue_entries.dispatched_session_id IS NULL
  AND lado_queue_entries.failed_at IS NULL
  AND lado_queue_entries.superseded_at IS NULL AND lado_queue_entries.cancelled_at IS NULL
  AND (lado_queue_entries.part_spawn_request = '' OR lado_queue_entries.part_spawn_request = sqlc.arg(request));

-- name: ClearLadoPartSpawnRequest :execrows
UPDATE lado_queue_entries
SET part_spawn_request = '',
    part_spawn_expires_at = NULL
WHERE lado_queue_entries.id = sqlc.arg(id)
  AND lado_queue_entries.part_spawn_request = sqlc.arg(request);

-- name: SetLadoPartRunDispatched :execrows
-- The integration worker started: the entry is dispatched to it.
UPDATE lado_queue_entries
SET dispatched_session_id = sqlc.arg(dispatched_session_id),
    part_spawn_request = '',
    part_spawn_expires_at = NULL
WHERE lado_queue_entries.id = sqlc.arg(id)
  AND lado_queue_entries.part_spawn_request = sqlc.arg(request)
  AND lado_queue_entries.dispatched_session_id IS NULL;

-- name: ListLadoPartSpawnRequests :many
SELECT id, task_id, project_id, plan_approval_id, policy_snapshot_id, enqueued_at,
    attempt_count, next_attempt_at, lease_token, claimed_at, lease_expires_at,
    waiting_reason, review_required_reason, blocked_reason, error_message,
    dispatched_session_id, failed_at, superseded_at, cancelled_at,
    parts_started_at, part_spawn_request, part_spawn_expires_at, waiting_detail
FROM lado_queue_entries
WHERE part_spawn_request <> ''
ORDER BY enqueued_at, id;

-- name: ListLadoExecutionsForEntry :many
-- An entry's executions of every role, oldest first, with whether the
-- session ended.
SELECT sqlc.embed(e),
    NOT EXISTS (
        SELECT 1 FROM sessions s WHERE s.id = e.session_id AND s.is_terminated = 0
    ) AS session_terminated
FROM lado_executions e
WHERE e.queue_entry_id = ?
ORDER BY e.started_at, e.session_id;

-- name: ListLiveLadoRunTasks :many
-- What takes a task slot: the tasks with a live execution, an active lease
-- or a running parts run, with their project, issue and the plan revision
-- they run. A running parts run whose part sessions all ended between two
-- parts still takes its slot.
SELECT DISTINCT t.id AS task_id, t.project_id, COALESCE(t.issue_ref, '') AS issue_ref, run.plan_revision_id
FROM (
    SELECT e.task_id, e.plan_revision_id
    FROM lado_executions e
    JOIN sessions s ON s.id = e.session_id
    WHERE s.is_terminated = 0
    UNION
    SELECT q.task_id, a.revision_id AS plan_revision_id
    FROM lado_queue_entries q
    JOIN lado_plan_approvals a ON a.id = q.plan_approval_id
    WHERE q.lease_token IS NOT NULL AND q.lease_expires_at > sqlc.arg(now)
    UNION
    SELECT r.task_id, r.plan_revision_id
    FROM lado_part_runs r
    JOIN lado_queue_entries q ON q.id = r.queue_entry_id
    WHERE q.dispatched_session_id IS NULL AND q.failed_at IS NULL
      AND q.superseded_at IS NULL AND q.cancelled_at IS NULL
) AS run
JOIN lado_tasks t ON t.id = run.task_id
ORDER BY t.project_id, t.id;

-- name: CountLiveLadoWorkers :one
-- What takes a worker slot: live execution sessions of every role, active
-- leases, and parts run workers the dispatcher is starting. A worker that
-- started but is not linked yet counts twice for a moment, never not at all.
SELECT
    (SELECT COUNT(*) FROM lado_executions e JOIN sessions s ON s.id = e.session_id WHERE s.is_terminated = 0)
    + (SELECT COUNT(*) FROM lado_queue_entries q WHERE q.lease_token IS NOT NULL AND q.lease_expires_at > sqlc.arg(now))
    + (SELECT COUNT(*) FROM lado_queue_entries q
        WHERE q.part_spawn_request <> '' AND q.part_spawn_expires_at > sqlc.arg(now)) AS live;

-- name: GetLadoPartSpawnGrant :one
-- The entry whose current parts run spawn is the client request id, with
-- the dispatch hash its approval gives.
SELECT sqlc.embed(e), r.sha256 AS plan_sha256, p.sha256 AS snapshot_sha256
FROM lado_queue_entries e
JOIN lado_plan_approvals a ON a.id = e.plan_approval_id
JOIN lado_plan_revisions r ON r.id = a.revision_id
JOIN lado_policy_snapshots p ON p.id = e.policy_snapshot_id
WHERE e.id = sqlc.arg(id);

-- name: LatestSessionReportState :one
-- The state and note of a session's newest report.
SELECT state, note
FROM reports
WHERE session_id = ?
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: BumpLadoPartRunIntegrationAttempts :execrows
-- Counts one more start of the integration worker, while it still has the
-- count the caller read.
UPDATE lado_part_runs
SET integration_attempts = integration_attempts + 1
WHERE lado_part_runs.queue_entry_id = sqlc.arg(queue_entry_id)
  AND lado_part_runs.integration_attempts = sqlc.arg(old_attempts);
