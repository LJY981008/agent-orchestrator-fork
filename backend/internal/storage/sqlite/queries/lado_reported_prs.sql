-- LADO fork: PRs a worker reported it created that its session does not have
-- (issue #71), and the refusals LADO records for those it cannot attach to
-- the session. Keep this file ASCII: sqlc 1.31 cuts a query after a
-- non-ASCII character.

-- name: ListLadoSessionReportedPRs :many
-- The pr_created report outputs of the given sessions, oldest report first
-- and in output order within a report.
SELECT r.session_id, o.reference
FROM reports r
JOIN report_outputs o ON o.report_id = r.id
WHERE r.session_id IN (sqlc.slice('session_ids'))
  AND o.kind = 'pr_created'
ORDER BY r.session_id, r.created_at, r.id, o.position;

-- name: ListLadoReportedPRRefusals :many
-- The refused reported PRs of the given executions.
SELECT execution_session_id, pr_url, reason, refused_at
FROM lado_reported_pr_refusals
WHERE execution_session_id IN (sqlc.slice('session_ids'));

-- name: UpsertLadoReportedPRRefusal :exec
-- Records that LADO refuses to attach a reported PR, or the newer reason.
INSERT INTO lado_reported_pr_refusals (execution_session_id, pr_url, reason, refused_at)
VALUES (sqlc.arg(execution_session_id), sqlc.arg(pr_url), sqlc.arg(reason), sqlc.arg(refused_at))
ON CONFLICT (execution_session_id, pr_url) DO UPDATE SET
    reason = excluded.reason,
    refused_at = excluded.refused_at;

-- name: ListLadoExecutionsWithReportedPRs :many
-- Executions that open a PR (role empty or integration), not settled, whose
-- session still runs and reported a PR no PR row has at that exact address
-- (nor an alias of one) and LADO did not refuse, oldest first.
SELECT sqlc.embed(e)
FROM lado_executions e
JOIN sessions s ON s.id = e.session_id
WHERE e.role IN ('', 'integration')
  AND e.settled_at IS NULL
  AND s.is_terminated = 0
  AND EXISTS (
        SELECT 1 FROM reports r
        JOIN report_outputs o ON o.report_id = r.id
        WHERE r.session_id = e.session_id
          AND o.kind = 'pr_created'
          AND NOT EXISTS (SELECT 1 FROM pr p WHERE p.url = o.reference)
          AND NOT EXISTS (SELECT 1 FROM pr_url_alias a WHERE a.alias_url = o.reference)
          AND NOT EXISTS (
                SELECT 1 FROM lado_reported_pr_refusals f
                WHERE f.execution_session_id = e.session_id AND f.pr_url = o.reference
            )
    )
ORDER BY e.started_at, e.session_id
LIMIT sqlc.arg(max_executions);
