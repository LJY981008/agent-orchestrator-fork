-- LADO fork: queue entries, executions and dispatch settings (docs/LJY-PRD/TDD.md
-- sections 4.2 and 5.3). Approving a plan queues one entry; the dispatcher
-- leases it, runs the checks and links the worker session it starts as an
-- execution. session_id and dispatched_session_id name sessions.id without a
-- FOREIGN KEY, because upstream deletes its own rows (TDD section 4.2).
-- project_id is copied from the task so dispatch can filter without a join.
-- No dispatch settings row means the defaults, and a project without a
-- lado_project_settings row has cap 1. Entry and execution states stay
-- derived at read time.

-- +goose Up
CREATE TABLE lado_queue_entries (
    id                     TEXT PRIMARY KEY CHECK (id <> ''),
    task_id                TEXT NOT NULL REFERENCES lado_tasks (id),
    project_id             TEXT NOT NULL CHECK (project_id <> ''),
    plan_approval_id       TEXT NOT NULL UNIQUE REFERENCES lado_plan_approvals (id),
    policy_snapshot_id     TEXT NOT NULL REFERENCES lado_policy_snapshots (id),
    enqueued_at            TIMESTAMP NOT NULL,
    attempt_count          INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at        TIMESTAMP,
    lease_token            TEXT,
    claimed_at             TIMESTAMP,
    lease_expires_at       TIMESTAMP,
    waiting_reason         TEXT NOT NULL DEFAULT '',
    review_required_reason TEXT NOT NULL DEFAULT '',
    blocked_reason         TEXT NOT NULL DEFAULT '',
    error_message          TEXT NOT NULL DEFAULT '',
    dispatched_session_id  TEXT,
    failed_at              TIMESTAMP,
    superseded_at          TIMESTAMP,
    cancelled_at           TIMESTAMP
);
CREATE INDEX lado_queue_entries_by_task ON lado_queue_entries (task_id, enqueued_at);
CREATE INDEX lado_queue_entries_open ON lado_queue_entries (enqueued_at)
    WHERE dispatched_session_id IS NULL AND failed_at IS NULL AND superseded_at IS NULL
      AND cancelled_at IS NULL AND review_required_reason = '' AND blocked_reason = '';

CREATE TABLE lado_executions (
    session_id         TEXT PRIMARY KEY CHECK (session_id <> ''),
    task_id            TEXT NOT NULL REFERENCES lado_tasks (id),
    queue_entry_id     TEXT NOT NULL UNIQUE REFERENCES lado_queue_entries (id),
    plan_revision_id   TEXT NOT NULL REFERENCES lado_plan_revisions (id),
    plan_approval_id   TEXT NOT NULL REFERENCES lado_plan_approvals (id),
    policy_snapshot_id TEXT NOT NULL REFERENCES lado_policy_snapshots (id),
    attempt            INTEGER NOT NULL CHECK (attempt > 0),
    plan_base_sha      TEXT NOT NULL DEFAULT '',
    dispatch_base_sha  TEXT NOT NULL DEFAULT '',
    base_commits_ahead INTEGER,
    started_at         TIMESTAMP NOT NULL,
    deadline_at        TIMESTAMP,
    timed_out_at       TIMESTAMP
);
CREATE INDEX lado_executions_by_task ON lado_executions (task_id, started_at);

CREATE TABLE lado_dispatch_settings (
    id                   INTEGER PRIMARY KEY CHECK (id = 1),
    max_concurrent       INTEGER NOT NULL CHECK (max_concurrent BETWEEN 1 AND 8),
    max_run_minutes      INTEGER NOT NULL CHECK (max_run_minutes >= 0),
    hold_on_memory_tight INTEGER NOT NULL CHECK (hold_on_memory_tight IN (0, 1)),
    updated_at           TIMESTAMP NOT NULL
);

ALTER TABLE lado_project_settings
    ADD COLUMN max_concurrent INTEGER NOT NULL DEFAULT 1 CHECK (max_concurrent BETWEEN 1 AND 8);

-- +goose Down
ALTER TABLE lado_project_settings DROP COLUMN max_concurrent;
DROP TABLE lado_dispatch_settings;
DROP TABLE lado_executions;
DROP TABLE lado_queue_entries;
