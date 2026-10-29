-- LADO fork: stage timings and investigation metrics (issue #34, step 0;
-- docs/LJY-PRD/TDD.md). A task's stage times are read from the records LADO
-- already keeps; two were missing. lado_execution_timings records when the
-- verification loop first saw an execution's PR and when its head first
-- passed or asked for confirmation: lado_verifications.computed_at is
-- rewritten on every recompute. lado_session_metrics stores what a plan or
-- execution session spent on investigation, counted from its conversation's
-- activities. session_id names sessions.id without a foreign key (TDD section
-- 4.2). Existing tables are unchanged, and neither table has a lado_cdc_*
-- trigger: verification records and settling already publish the execution.
--
-- Executions that already have a PR (a pr row or a verification record) get
-- estimated times, marked estimated: the PR was first seen no later than the
-- first check run, else the first verification record of any head, else the
-- first review round; it verified at the first passing or confirming
-- record's computed_at, which recomputes move later, so that estimate is an
-- upper bound.

-- +goose Up
CREATE TABLE lado_execution_timings (
    session_id         TEXT PRIMARY KEY REFERENCES lado_executions (session_id),
    pr_url             TEXT NOT NULL DEFAULT '',
    pr_seen_at         TIMESTAMP,
    pr_seen_estimated  INTEGER NOT NULL DEFAULT 0 CHECK (pr_seen_estimated IN (0, 1)),
    verified_at        TIMESTAMP,
    verified_estimated INTEGER NOT NULL DEFAULT 0 CHECK (verified_estimated IN (0, 1))
);

CREATE TABLE lado_session_metrics (
    session_id        TEXT PRIMARY KEY CHECK (session_id <> ''),
    task_id           TEXT NOT NULL REFERENCES lado_tasks (id),
    role              TEXT NOT NULL CHECK (role IN ('designer', 'critic', 'worker', 'part', 'merge', 'integration')),
    tool_calls        INTEGER NOT NULL DEFAULT 0 CHECK (tool_calls >= 0),
    read_calls        INTEGER NOT NULL DEFAULT 0 CHECK (read_calls >= 0),
    search_calls      INTEGER NOT NULL DEFAULT 0 CHECK (search_calls >= 0),
    command_calls     INTEGER NOT NULL DEFAULT 0 CHECK (command_calls >= 0),
    edit_calls        INTEGER NOT NULL DEFAULT 0 CHECK (edit_calls >= 0),
    first_activity_at TIMESTAMP,
    first_edit_at     TIMESTAMP,
    last_activity_at  TIMESTAMP,
    investigate_ms    INTEGER NOT NULL DEFAULT 0 CHECK (investigate_ms >= 0),
    updated_at        TIMESTAMP NOT NULL
);
CREATE INDEX lado_session_metrics_by_task ON lado_session_metrics (task_id);

INSERT INTO lado_execution_timings (session_id, pr_url, pr_seen_at, pr_seen_estimated, verified_at, verified_estimated)
SELECT session_id, pr_url, pr_seen_at, pr_seen_at IS NOT NULL, verified_at, verified_at IS NOT NULL
FROM (
    SELECT e.session_id AS session_id,
        COALESCE(
            (SELECT pr.url FROM pr WHERE pr.session_id = e.session_id ORDER BY pr.updated_at DESC, pr.url DESC LIMIT 1),
            (SELECT v.pr_url FROM lado_verifications v WHERE v.execution_session_id = e.session_id
                ORDER BY v.computed_at DESC, v.head_sha DESC LIMIT 1),
            e.settled_pr_url
        ) AS pr_url,
        COALESCE(
            (SELECT MIN(r.started_at) FROM lado_check_runs r WHERE r.execution_session_id = e.session_id),
            (SELECT MIN(v.computed_at) FROM lado_verifications v WHERE v.execution_session_id = e.session_id),
            (SELECT MIN(x.created_at) FROM lado_execution_reviews x WHERE x.execution_session_id = e.session_id)
        ) AS pr_seen_at,
        (SELECT MIN(v.computed_at) FROM lado_verifications v
            WHERE v.execution_session_id = e.session_id AND v.outcome IN ('pass', 'confirm')) AS verified_at
    FROM lado_executions e
    WHERE EXISTS (SELECT 1 FROM pr WHERE pr.session_id = e.session_id)
       OR EXISTS (SELECT 1 FROM lado_verifications v WHERE v.execution_session_id = e.session_id)
);

-- +goose Down
DROP TABLE lado_session_metrics;
DROP TABLE lado_execution_timings;
