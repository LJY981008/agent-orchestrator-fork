-- LADO fork: issue-backed tasks (docs/LJY-PRD/TDD.md section 3.1).
-- project_id refers to projects.id without a FOREIGN KEY, because upstream
-- rebuilds and deletes its own rows (TDD section 4.2). issue_repo is the
-- canonical id of the repository whose tracker holds the issue; a workspace
-- project has several. issue_ref is the canonical issue id; it is NULL only
-- while LADO creates the issue, and one issue has at most one task that is
-- not cancelled. The issue_* columns are
-- what LADO last observed; the tracker owns the issue. Task states are derived
-- at read time, never stored.

-- +goose Up
CREATE TABLE lado_tasks (
    id                    TEXT PRIMARY KEY CHECK (id <> ''),
    project_id            TEXT NOT NULL CHECK (project_id <> ''),
    issue_repo            TEXT NOT NULL CHECK (issue_repo <> ''),
    issue_ref             TEXT CHECK (issue_ref IS NULL OR issue_ref <> ''),
    agent                 TEXT NOT NULL DEFAULT '',
    model                 TEXT NOT NULL DEFAULT '',
    issue_title           TEXT NOT NULL DEFAULT '',
    issue_state           TEXT NOT NULL DEFAULT '',
    issue_url             TEXT NOT NULL DEFAULT '',
    issue_snapshot_sha256 TEXT NOT NULL DEFAULT '',
    issue_observed_at     TIMESTAMP,
    created_at            TIMESTAMP NOT NULL,
    updated_at            TIMESTAMP NOT NULL,
    cancelled_at          TIMESTAMP
);

CREATE UNIQUE INDEX lado_tasks_one_live_task_per_issue
    ON lado_tasks (issue_ref)
    WHERE issue_ref IS NOT NULL AND cancelled_at IS NULL;

CREATE INDEX lado_tasks_by_project ON lado_tasks (project_id, created_at);

-- +goose Down
DROP TABLE lado_tasks;
