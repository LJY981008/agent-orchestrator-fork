-- LADO fork: PRs a worker reported it created that its session does not
-- have (issue #71).
--
-- A LADO worker that opens its PR from a branch AO does not match to its
-- session leaves the PR only as a pr_created report output. LADO's
-- verification loop attaches such a PR to the session as
-- `ao session claim-pr` does. lado_reported_pr_refusals records a reported
-- PR LADO found it cannot attach, and why: another session has it
-- (owned_elsewhere), it is outside the project's repositories
-- (outside_project), it is not open (not_open), the reference names no PR
-- (invalid), the session takes no PR (session_not_claimable), or attaching
-- it kept failing (unreachable). A refused PR no longer holds the
-- execution's integration approval, also after a restart.
--
-- No existing table changes. The change triggers live in lado_migrate.go,
-- never here.

-- +goose Up
CREATE TABLE lado_reported_pr_refusals (
    execution_session_id TEXT NOT NULL REFERENCES lado_executions (session_id) ON DELETE CASCADE,
    pr_url               TEXT NOT NULL CHECK (pr_url <> ''),
    reason               TEXT NOT NULL CHECK (reason IN (
        'owned_elsewhere', 'outside_project', 'not_open', 'invalid', 'session_not_claimable', 'unreachable'
    )),
    refused_at           TIMESTAMP NOT NULL,
    PRIMARY KEY (execution_session_id, pr_url)
);

-- +goose Down
-- The table's triggers go with it.
DROP TABLE lado_reported_pr_refusals;
