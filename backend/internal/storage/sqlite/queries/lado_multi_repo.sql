-- LADO fork: one task changing several repositories of a workspace (issue
-- #64, docs/LJY-PRD/TDD.md sections 5.4, 7 and 8): the PRs LADO tracks for an
-- execution, a parts run's integration branch in each repository, and the
-- worker's newest report. Keep this file ASCII: sqlc 1.31 cuts a query after
-- a non-ASCII character.

-- name: ListLadoExecutionTrackedPRs :many
-- The PRs LADO tracks for the given executions, replaced ones too, with the
-- PR as AO last observed it (zero values when AO has no row), live rows first
-- and in merge order.
SELECT x.execution_session_id, x.pr_url, x.repo_key, x.position, x.tracked_at, x.replaced_at, x.settled_at,
    x.settled_pr_state,
    CAST(COALESCE(p.number, 0) AS INTEGER) AS pr_number,
    CAST(COALESCE(p.pr_state, '') AS TEXT) AS pr_state,
    CAST(COALESCE(p.head_sha, '') AS TEXT) AS pr_head_sha,
    CAST(COALESCE(p.target_branch, '') AS TEXT) AS pr_target_branch,
    CAST(COALESCE(p.title, '') AS TEXT) AS pr_title,
    CAST(COALESCE(p.mergeability, 'unknown') AS TEXT) AS pr_mergeability,
    CAST(COALESCE(p.source_branch, '') AS TEXT) AS pr_source_branch
FROM lado_execution_prs x
LEFT JOIN pr p ON p.url = x.pr_url
WHERE x.execution_session_id IN (sqlc.slice('session_ids'))
ORDER BY x.execution_session_id, x.replaced_at IS NOT NULL, x.position, x.tracked_at, x.pr_url;

-- name: ListLadoExecutionsWithUntrackedPRs :many
-- Executions that ran a PR (role empty or integration) whose session has a PR
-- LADO does not track for them yet, newest first, with whether the session
-- ended: an open one, or a merged or closed one while the execution is not
-- settled or still has a live tracked PR that is not.
SELECT sqlc.embed(e),
    NOT EXISTS (
        SELECT 1 FROM sessions s WHERE s.id = e.session_id AND s.is_terminated = 0
    ) AS session_terminated
FROM lado_executions e
WHERE e.role IN ('', 'integration')
  AND EXISTS (
        SELECT 1 FROM pr p
        WHERE p.session_id = e.session_id
          AND NOT EXISTS (
                SELECT 1 FROM lado_execution_prs x
                WHERE x.execution_session_id = e.session_id AND x.pr_url = p.url
            )
          AND (
                p.pr_state IN ('open', 'draft')
                OR e.settled_at IS NULL
                OR EXISTS (
                    SELECT 1 FROM lado_execution_prs u
                    WHERE u.execution_session_id = e.session_id AND u.replaced_at IS NULL AND u.settled_at IS NULL
                )
            )
    )
ORDER BY e.started_at DESC, e.session_id DESC
LIMIT sqlc.arg(max_executions);

-- name: GetLadoExecutionTrackedPR :one
SELECT execution_session_id, pr_url, repo_key, position, tracked_at, replaced_at, settled_at, settled_pr_state
FROM lado_execution_prs
WHERE execution_session_id = ? AND pr_url = ?;

-- name: CountLadoExecutionTrackedPRs :one
SELECT COUNT(*)
FROM lado_execution_prs
WHERE execution_session_id = ?;

-- name: InsertLadoExecutionPR :execrows
-- Starts tracking a PR for an execution; a PR it tracks already, or a
-- repository with a live tracked PR, writes nothing.
INSERT INTO lado_execution_prs (
    execution_session_id, pr_url, repo_key, position, tracked_at, settled_at, settled_pr_state
) VALUES (
    sqlc.arg(execution_session_id), sqlc.arg(pr_url), sqlc.arg(repo_key), sqlc.arg(position), sqlc.arg(tracked_at),
    sqlc.narg(settled_at), sqlc.arg(settled_pr_state)
)
ON CONFLICT DO NOTHING;

-- name: MarkLadoExecutionPRReplaced :execrows
UPDATE lado_execution_prs
SET replaced_at = sqlc.arg(replaced_at)
WHERE lado_execution_prs.execution_session_id = sqlc.arg(execution_session_id)
  AND lado_execution_prs.pr_url = sqlc.arg(pr_url)
  AND lado_execution_prs.replaced_at IS NULL;

-- name: SettleLadoExecutionPR :execrows
-- Settles a live tracked PR in its merged or closed state, unless it was
-- settled in that state already.
UPDATE lado_execution_prs
SET settled_at = sqlc.arg(settled_at),
    settled_pr_state = sqlc.arg(pr_state)
WHERE lado_execution_prs.execution_session_id = sqlc.arg(execution_session_id)
  AND lado_execution_prs.pr_url = sqlc.arg(pr_url)
  AND lado_execution_prs.replaced_at IS NULL
  AND lado_execution_prs.settled_pr_state <> sqlc.arg(pr_state);

-- name: CountUnsettledLadoExecutionPRs :one
-- The live tracked PRs of an execution not settled yet, or open again since.
SELECT COUNT(*)
FROM lado_execution_prs x
LEFT JOIN pr p ON p.url = x.pr_url
WHERE x.execution_session_id = sqlc.arg(execution_session_id)
  AND x.replaced_at IS NULL
  AND (x.settled_at IS NULL OR p.pr_state IN ('open', 'draft'));

-- name: ListLatestLadoSessionReportStates :many
-- The state of each given session's newest report.
SELECT r.session_id, r.state
FROM reports r
WHERE r.session_id IN (sqlc.slice('session_ids'))
  AND NOT EXISTS (
        SELECT 1 FROM reports n
        WHERE n.session_id = r.session_id
          AND (n.created_at > r.created_at OR (n.created_at = r.created_at AND n.id > r.id))
    );

-- name: InsertLadoPartRunRepo :exec
INSERT INTO lado_part_run_repos (
    queue_entry_id, relative_path, position, repo_dir, base_sha, head_sha, base_branch, base_remote,
    dispatch_local_base_sha, base_commits_ahead
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListLadoPartRunRepos :many
SELECT queue_entry_id, relative_path, position, repo_dir, base_sha, head_sha, base_branch, base_remote,
    dispatch_local_base_sha, base_commits_ahead
FROM lado_part_run_repos
WHERE queue_entry_id = ?
ORDER BY position;

-- name: SetLadoPartRunRepoHead :execrows
-- Compare-and-set: moves the recorded integration head of one repository.
UPDATE lado_part_run_repos
SET head_sha = sqlc.arg(head_sha)
WHERE lado_part_run_repos.queue_entry_id = sqlc.arg(queue_entry_id)
  AND lado_part_run_repos.relative_path = sqlc.arg(relative_path)
  AND lado_part_run_repos.head_sha = sqlc.arg(old_head_sha);

-- name: SetLadoPartRunFirstRepoHead :exec
-- Mirrors the run's own head into its first repository's row, if it has one.
UPDATE lado_part_run_repos
SET head_sha = sqlc.arg(head_sha)
WHERE lado_part_run_repos.queue_entry_id = sqlc.arg(queue_entry_id)
  AND lado_part_run_repos.position = 0;

-- name: UpsertLadoTaskPartRepo :exec
INSERT INTO lado_task_part_repos (
    queue_entry_id, part_id, relative_path, start_sha, merge_from_sha, merge_tip_sha, merged_sha
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (queue_entry_id, part_id, relative_path) DO UPDATE SET
    start_sha = excluded.start_sha,
    merge_from_sha = excluded.merge_from_sha,
    merge_tip_sha = excluded.merge_tip_sha,
    merged_sha = excluded.merged_sha;

-- name: ListLadoTaskPartRepos :many
-- The repositories of each part of an entry's run, in the run's order.
SELECT tp.queue_entry_id, tp.part_id, tp.relative_path, tp.start_sha, tp.merge_from_sha, tp.merge_tip_sha,
    tp.merged_sha
FROM lado_task_part_repos tp
JOIN lado_part_run_repos rr ON rr.queue_entry_id = tp.queue_entry_id AND rr.relative_path = tp.relative_path
WHERE tp.queue_entry_id = ?
ORDER BY tp.part_id, rr.position;

-- name: RenewLadoIntegrationApproval :execrows
-- Approves again a head LADO revoked because a sibling PR's base moved
-- (sibling_revoked): the row is the head's and target's only one.
UPDATE lado_integration_approvals
SET verification_sha256 = sqlc.arg(verification_sha256),
    acknowledged_json = sqlc.arg(acknowledged_json),
    proof_nonce = sqlc.arg(proof_nonce),
    actor = sqlc.arg(actor),
    approved_at = sqlc.arg(approved_at),
    status_state = sqlc.arg(status_state),
    status_attempts = sqlc.arg(status_attempts),
    status_posted_at = sqlc.narg(status_posted_at),
    status_error = sqlc.arg(status_error),
    approved_base_sha = sqlc.arg(approved_base_sha),
    revoked_at = NULL,
    revoked_reason = ''
WHERE lado_integration_approvals.id = sqlc.arg(id)
  AND lado_integration_approvals.revoked_reason = 'sibling_revoked'
  AND lado_integration_approvals.revoked_at IS NOT NULL;

-- name: CountLadoExecutionPRReviews :one
-- The review rounds of one PR of an execution: its own and those written
-- before rounds named a PR, which count for every PR.
SELECT COUNT(*)
FROM lado_execution_reviews
WHERE execution_session_id = sqlc.arg(execution_session_id)
  AND (pr_url = sqlc.arg(pr_url) OR pr_url = '');

-- name: ListLadoExecutionPRReviews :many
SELECT review_run_id, execution_session_id, head_sha, round, created_at, cancelled_at, pr_url
FROM lado_execution_reviews
WHERE execution_session_id = sqlc.arg(execution_session_id)
  AND (pr_url = sqlc.arg(pr_url) OR pr_url = '')
ORDER BY round, created_at, review_run_id;
