-- name: GetLadoPRResolve :one
SELECT *
FROM lado_pr_resolves
WHERE execution_session_id = ?;

-- name: GetLadoPRResolveBySpawnRequest :one
SELECT *
FROM lado_pr_resolves
WHERE spawn_request = sqlc.arg(request) AND spawn_request <> '';

-- name: ListLadoPRResolvesForSessions :many
SELECT *
FROM lado_pr_resolves
WHERE execution_session_id IN (sqlc.slice('session_ids'));

-- name: InsertLadoPRResolve :execrows
-- Starts the resolve row of an execution at version 1, unless it has one.
INSERT INTO lado_pr_resolves (
    execution_session_id, task_id, queue_entry_id, state, attempts, session_id, start_head_sha, base_tip_sha,
    done_seen_at, notified_head_sha, error, version, created_at, updated_at, pr_url
) VALUES (
    sqlc.arg(execution_session_id), sqlc.arg(task_id), sqlc.arg(queue_entry_id), sqlc.arg(state), sqlc.arg(attempts),
    sqlc.arg(session_id), sqlc.arg(start_head_sha), sqlc.arg(base_tip_sha), sqlc.narg(done_seen_at),
    sqlc.arg(notified_head_sha), sqlc.arg(error), 1, sqlc.arg(created_at), sqlc.arg(created_at), sqlc.arg(pr_url)
)
ON CONFLICT (execution_session_id) DO NOTHING;

-- name: UpdateLadoPRResolve :execrows
-- Writes the fields the resolver owns over the version the caller read. The
-- spawn request is not one of them.
UPDATE lado_pr_resolves
SET state = sqlc.arg(state),
    attempts = sqlc.arg(attempts),
    session_id = sqlc.arg(session_id),
    start_head_sha = sqlc.arg(start_head_sha),
    base_tip_sha = sqlc.arg(base_tip_sha),
    done_seen_at = sqlc.narg(done_seen_at),
    notified_head_sha = sqlc.arg(notified_head_sha),
    error = sqlc.arg(error),
    pr_url = sqlc.arg(pr_url),
    version = lado_pr_resolves.version + 1,
    updated_at = sqlc.arg(updated_at)
WHERE lado_pr_resolves.execution_session_id = sqlc.arg(execution_session_id)
  AND lado_pr_resolves.version = sqlc.arg(old_version);

-- name: MaxLadoResolveSpawns :one
-- The most resolve workers started for one PR of an entry's executions.
SELECT CAST(COALESCE(MAX(spawns), 0) AS INTEGER) AS spawns
FROM lado_pr_resolves
WHERE queue_entry_id = ?;

-- name: SetLadoResolveSpawnRequest :execrows
-- Records the resolve worker being started, while no other one is.
UPDATE lado_pr_resolves
SET spawn_request = sqlc.arg(request),
    spawn_expires_at = sqlc.arg(expires_at),
    spawns = sqlc.arg(spawns),
    updated_at = sqlc.arg(updated_at)
WHERE lado_pr_resolves.execution_session_id = sqlc.arg(execution_session_id)
  AND lado_pr_resolves.spawn_request = '';

-- name: ClearLadoResolveSpawnRequest :execrows
UPDATE lado_pr_resolves
SET spawn_request = '',
    spawn_expires_at = NULL
WHERE lado_pr_resolves.execution_session_id = sqlc.arg(execution_session_id)
  AND lado_pr_resolves.spawn_request = sqlc.arg(request)
  AND lado_pr_resolves.spawn_request <> '';

-- name: LinkLadoResolveSpawn :execrows
-- Ends a spawn request with the worker that started for it: the row resolves
-- with that session from now on.
UPDATE lado_pr_resolves
SET spawn_request = '',
    spawn_expires_at = NULL,
    state = 'resolving',
    session_id = sqlc.arg(session_id),
    done_seen_at = NULL,
    version = lado_pr_resolves.version + 1,
    updated_at = sqlc.arg(updated_at)
WHERE lado_pr_resolves.execution_session_id = sqlc.arg(execution_session_id)
  AND lado_pr_resolves.spawn_request = sqlc.arg(request)
  AND lado_pr_resolves.spawn_request <> '';

-- name: ListExpiredLadoResolveSpawns :many
SELECT *
FROM lado_pr_resolves
WHERE spawn_request <> '' AND spawn_expires_at <= sqlc.arg(now)
ORDER BY spawn_expires_at, execution_session_id;

-- name: ListLadoExecutionsToResolve :many
-- Executions that ran a PR (role empty or integration) with an open PR whose
-- head AO observed and that conflicts with its base, or whose record of that
-- head failed with base_overlap, and those whose resolve row still has a
-- state or a spawn request, whatever their PRs. An execution with tracked
-- PRs (lado_execution_prs, issue #64) looks at each live one; one without
-- looks at its session's newest PR. Each comes with its task's project,
-- whether its session ended, whether the session still works (not ended,
-- agent not exited) and whether the resolve row's worker still works.
SELECT sqlc.embed(e),
    t.project_id AS project_id,
    NOT EXISTS (
        SELECT 1 FROM sessions s WHERE s.id = e.session_id AND s.is_terminated = 0
    ) AS session_terminated,
    EXISTS (
        SELECT 1 FROM sessions s WHERE s.id = e.session_id AND s.is_terminated = 0 AND s.activity_state <> 'exited'
    ) AS session_live,
    EXISTS (
        SELECT 1 FROM lado_pr_resolves r JOIN sessions s ON s.id = r.session_id
        WHERE r.execution_session_id = e.session_id AND r.session_id <> ''
          AND s.is_terminated = 0 AND s.activity_state <> 'exited'
    ) AS resolve_session_live
FROM lado_executions e
JOIN lado_tasks t ON t.id = e.task_id
WHERE e.role IN ('', 'integration')
  AND (
        EXISTS (
            SELECT 1 FROM lado_pr_resolves r
            WHERE r.execution_session_id = e.session_id AND (r.state <> '' OR r.spawn_request <> '')
        )
        OR EXISTS (
            SELECT 1 FROM lado_execution_prs x
            JOIN pr p ON p.url = x.pr_url
            WHERE x.execution_session_id = e.session_id AND x.replaced_at IS NULL
              AND p.pr_state NOT IN ('merged', 'closed') AND p.head_sha <> ''
              AND (
                    p.mergeability = 'conflicting'
                    OR EXISTS (
                        SELECT 1 FROM lado_verifications v
                        WHERE v.execution_session_id = e.session_id AND v.head_sha = p.head_sha AND v.pr_url = p.url
                          AND v.content_json LIKE '%"kind":"base_branch"%' AND v.content_json LIKE '%"code":"base_overlap"%'
                    )
                )
        )
        OR (
            NOT EXISTS (SELECT 1 FROM lado_execution_prs x WHERE x.execution_session_id = e.session_id)
            AND EXISTS (
                SELECT 1 FROM pr p
                WHERE p.session_id = e.session_id
                  AND NOT EXISTS (
                        SELECT 1 FROM pr newer
                        WHERE newer.session_id = p.session_id
                          AND (newer.updated_at > p.updated_at OR (newer.updated_at = p.updated_at AND newer.url > p.url))
                    )
                  AND p.pr_state NOT IN ('merged', 'closed') AND p.head_sha <> ''
                  AND (
                        p.mergeability = 'conflicting'
                        OR EXISTS (
                            SELECT 1 FROM lado_verifications v
                            WHERE v.execution_session_id = e.session_id AND v.head_sha = p.head_sha AND v.pr_url = p.url
                              AND v.content_json LIKE '%"kind":"base_branch"%' AND v.content_json LIKE '%"code":"base_overlap"%'
                        )
                    )
            )
        )
    )
ORDER BY e.started_at, e.session_id
LIMIT sqlc.arg(max_executions);

-- name: ListLadoExecutionSessionsToObserve :many
-- The sessions of the executions that ran a PR (role empty or integration)
-- with a PR still open, whether the session ended or not: AO keeps observing
-- those PRs for LADO. An execution with tracked PRs stays while any live one
-- is open, so a second repository's PR is still observed after the first
-- merged (issue #64); one without looks at its session's newest PR.
SELECT e.session_id
FROM lado_executions e
WHERE e.role IN ('', 'integration')
  AND (
        EXISTS (
            SELECT 1 FROM lado_execution_prs x
            JOIN pr p ON p.url = x.pr_url
            WHERE x.execution_session_id = e.session_id AND x.replaced_at IS NULL
              AND p.pr_state IN ('open', 'draft')
        )
        OR (
            NOT EXISTS (SELECT 1 FROM lado_execution_prs x WHERE x.execution_session_id = e.session_id)
            AND EXISTS (
                SELECT 1 FROM pr p
                WHERE p.session_id = e.session_id
                  AND p.pr_state IN ('open', 'draft')
                  AND NOT EXISTS (
                        SELECT 1 FROM pr newer
                        WHERE newer.session_id = p.session_id
                          AND (newer.updated_at > p.updated_at OR (newer.updated_at = p.updated_at AND newer.url > p.url))
                    )
                  AND NOT (e.settled_pr_url = p.url AND e.settled_pr_state = p.pr_state)
            )
        )
    )
ORDER BY e.session_id;

-- name: ListLadoOverlapHolders :many
-- What holds a task's paths for overlap waiting: as ListLiveLadoRunTasks,
-- except that an execution no longer holds once it timed out, its entry or
-- task was cancelled, or its entry's PR worker is done with its PRs, even
-- while its session runs. A PR worker with tracked PRs is done once every
-- live one settled merged or closed and none is open again, and no
-- repository it is expected to open a PR in is without one (issue #64: a
-- declared repository stops counting once the worker reported done or its
-- session ended). One without tracked PRs is done once its session's newest
-- PR merged or closed.
SELECT DISTINCT t.id AS task_id, t.project_id, COALESCE(t.issue_ref, '') AS issue_ref, run.plan_revision_id
FROM (
    SELECT e.task_id, e.plan_revision_id
    FROM lado_executions e
    JOIN sessions s ON s.id = e.session_id
    JOIN lado_queue_entries q ON q.id = e.queue_entry_id
    JOIN lado_tasks et ON et.id = e.task_id
    WHERE s.is_terminated = 0
      AND e.timed_out_at IS NULL
      AND q.cancelled_at IS NULL
      AND et.cancelled_at IS NULL
      AND NOT EXISTS (
            SELECT 1 FROM lado_executions w
            WHERE w.queue_entry_id = e.queue_entry_id AND w.role IN ('', 'integration')
              AND (
                    (
                        EXISTS (SELECT 1 FROM lado_execution_prs x WHERE x.execution_session_id = w.session_id)
                        AND NOT EXISTS (
                            SELECT 1 FROM lado_execution_prs x
                            LEFT JOIN pr p ON p.url = x.pr_url
                            WHERE x.execution_session_id = w.session_id AND x.replaced_at IS NULL
                              AND (x.settled_at IS NULL OR p.pr_state IN ('open', 'draft'))
                        )
                        AND NOT (
                            EXISTS (
                                SELECT 1 FROM json_each(w.pr_repos_json) AS j
                                WHERE NOT EXISTS (
                                    SELECT 1 FROM lado_execution_prs x
                                    WHERE x.execution_session_id = w.session_id AND x.replaced_at IS NULL
                                      AND x.repo_key = CAST(j.value AS TEXT)
                                )
                            )
                            AND (
                                w.pr_repos_firm = 1
                                OR (
                                    EXISTS (SELECT 1 FROM sessions ws WHERE ws.id = w.session_id AND ws.is_terminated = 0)
                                    AND COALESCE((
                                        SELECT rp.state FROM reports rp WHERE rp.session_id = w.session_id
                                        ORDER BY rp.created_at DESC, rp.id DESC LIMIT 1
                                    ), '') <> 'done'
                                )
                            )
                        )
                    )
                    OR (
                        NOT EXISTS (SELECT 1 FROM lado_execution_prs x WHERE x.execution_session_id = w.session_id)
                        AND EXISTS (
                            SELECT 1 FROM pr p
                            WHERE p.session_id = w.session_id
                              AND p.pr_state IN ('merged', 'closed')
                              AND NOT EXISTS (
                                    SELECT 1 FROM pr newer
                                    WHERE newer.session_id = p.session_id
                                      AND (newer.updated_at > p.updated_at OR (newer.updated_at = p.updated_at AND newer.url > p.url))
                                )
                        )
                    )
                )
        )
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

-- name: ListLadoQueuedOverlapCandidates :many
-- The entries of the entry's project, of other tasks, whose plan was
-- approved before the entry's (ties by entry id) and that still wait for a
-- worker: queued, waiting or dispatching. Entries that are blocked, need a
-- review, failed, ran parts, were dispatched, superseded or cancelled are
-- not among them.
SELECT t.id AS task_id, t.project_id, COALESCE(t.issue_ref, '') AS issue_ref, a.revision_id AS plan_revision_id
FROM lado_queue_entries me
JOIN lado_plan_approvals mya ON mya.id = me.plan_approval_id
JOIN lado_queue_entries q ON q.project_id = me.project_id AND q.task_id <> me.task_id
JOIN lado_plan_approvals a ON a.id = q.plan_approval_id
JOIN lado_tasks t ON t.id = q.task_id
WHERE me.id = sqlc.arg(entry_id)
  AND (a.approved_at < mya.approved_at OR (a.approved_at = mya.approved_at AND q.id < me.id))
  AND q.dispatched_session_id IS NULL AND q.failed_at IS NULL
  AND q.superseded_at IS NULL AND q.cancelled_at IS NULL
  AND q.parts_started_at IS NULL
  AND q.review_required_reason = '' AND q.blocked_reason = ''
  AND t.cancelled_at IS NULL
ORDER BY a.approved_at, q.id;
