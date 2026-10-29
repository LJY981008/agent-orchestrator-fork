-- LADO fork: parallel parts (docs/LJY-PRD/TDD.md section 5.4). A plan with
-- parts runs one worker per part, one per merge that conflicts and one
-- integration worker, each an execution of the same queue entry. So
-- lado_executions loses the one-execution-per-entry UNIQUE constraint and is
-- rebuilt; executions record their role, part, branch and start commit, and
-- one entry still has at most one execution that opens its pull request
-- (role '' or 'integration'). lado_part_runs records an entry's integration
-- branch and lado_task_parts each part's progress. Queue entries record when
-- their parts run started, the parts run worker the dispatcher starts now
-- (the worker gate grants that spawn only) and what a waiting entry waits
-- for. The dispatch settings cap LADO workers and hold work while the disk
-- is low; the project settings hold a task while another task's live run may
-- touch the same files. max_concurrent keeps its columns but counts tasks.
-- Five verification tables reference lado_executions, so the rebuild defers
-- foreign keys: dropping the old table counts their rows as violations and
-- inserting the same sessions into the new one settles them before commit.
-- No lado_cdc_* trigger exists while the LADO track runs (lado_migrate.go).

-- +goose Up
PRAGMA defer_foreign_keys = ON;

CREATE TABLE lado_executions_next (
    session_id              TEXT PRIMARY KEY CHECK (session_id <> ''),
    task_id                 TEXT NOT NULL REFERENCES lado_tasks (id),
    queue_entry_id          TEXT NOT NULL REFERENCES lado_queue_entries (id),
    plan_revision_id        TEXT NOT NULL REFERENCES lado_plan_revisions (id),
    plan_approval_id        TEXT NOT NULL REFERENCES lado_plan_approvals (id),
    policy_snapshot_id      TEXT NOT NULL REFERENCES lado_policy_snapshots (id),
    attempt                 INTEGER NOT NULL CHECK (attempt > 0),
    plan_base_sha           TEXT NOT NULL DEFAULT '',
    dispatch_base_sha       TEXT NOT NULL DEFAULT '',
    base_commits_ahead      INTEGER,
    started_at              TIMESTAMP NOT NULL,
    deadline_at             TIMESTAMP,
    timed_out_at            TIMESTAMP,
    base_branch             TEXT NOT NULL DEFAULT '',
    base_remote             TEXT NOT NULL DEFAULT '',
    dispatch_local_base_sha TEXT NOT NULL DEFAULT '',
    integration_gated       INTEGER NOT NULL DEFAULT 0 CHECK (integration_gated IN (0, 1)),
    settled_at              TIMESTAMP,
    settled_pr_url          TEXT NOT NULL DEFAULT '',
    settled_pr_state        TEXT NOT NULL DEFAULT '',
    role                    TEXT NOT NULL DEFAULT '' CHECK (role IN ('', 'part', 'merge', 'integration')),
    part_id                 TEXT NOT NULL DEFAULT '',
    branch                  TEXT NOT NULL DEFAULT '',
    start_sha               TEXT NOT NULL DEFAULT ''
);
INSERT INTO lado_executions_next (
    session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id,
    attempt, plan_base_sha, dispatch_base_sha, base_commits_ahead, started_at, deadline_at, timed_out_at,
    base_branch, base_remote, dispatch_local_base_sha, integration_gated, settled_at, settled_pr_url,
    settled_pr_state
)
SELECT session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id,
    attempt, plan_base_sha, dispatch_base_sha, base_commits_ahead, started_at, deadline_at, timed_out_at,
    base_branch, base_remote, dispatch_local_base_sha, integration_gated, settled_at, settled_pr_url,
    settled_pr_state
FROM lado_executions;
DROP TABLE lado_executions;
CREATE TABLE lado_executions (
    session_id              TEXT PRIMARY KEY CHECK (session_id <> ''),
    task_id                 TEXT NOT NULL REFERENCES lado_tasks (id),
    queue_entry_id          TEXT NOT NULL REFERENCES lado_queue_entries (id),
    plan_revision_id        TEXT NOT NULL REFERENCES lado_plan_revisions (id),
    plan_approval_id        TEXT NOT NULL REFERENCES lado_plan_approvals (id),
    policy_snapshot_id      TEXT NOT NULL REFERENCES lado_policy_snapshots (id),
    attempt                 INTEGER NOT NULL CHECK (attempt > 0),
    plan_base_sha           TEXT NOT NULL DEFAULT '',
    dispatch_base_sha       TEXT NOT NULL DEFAULT '',
    base_commits_ahead      INTEGER,
    started_at              TIMESTAMP NOT NULL,
    deadline_at             TIMESTAMP,
    timed_out_at            TIMESTAMP,
    base_branch             TEXT NOT NULL DEFAULT '',
    base_remote             TEXT NOT NULL DEFAULT '',
    dispatch_local_base_sha TEXT NOT NULL DEFAULT '',
    integration_gated       INTEGER NOT NULL DEFAULT 0 CHECK (integration_gated IN (0, 1)),
    settled_at              TIMESTAMP,
    settled_pr_url          TEXT NOT NULL DEFAULT '',
    settled_pr_state        TEXT NOT NULL DEFAULT '',
    role                    TEXT NOT NULL DEFAULT '' CHECK (role IN ('', 'part', 'merge', 'integration')),
    part_id                 TEXT NOT NULL DEFAULT '',
    branch                  TEXT NOT NULL DEFAULT '',
    start_sha               TEXT NOT NULL DEFAULT ''
);
INSERT INTO lado_executions SELECT * FROM lado_executions_next;
DROP TABLE lado_executions_next;
CREATE INDEX lado_executions_by_task ON lado_executions (task_id, started_at);
CREATE UNIQUE INDEX lado_executions_one_pr_worker ON lado_executions (queue_entry_id)
    WHERE role IN ('', 'integration');
CREATE INDEX lado_executions_by_entry ON lado_executions (queue_entry_id, role, part_id);

ALTER TABLE lado_queue_entries ADD COLUMN parts_started_at TIMESTAMP;
ALTER TABLE lado_queue_entries ADD COLUMN part_spawn_request TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_queue_entries ADD COLUMN part_spawn_expires_at TIMESTAMP;
ALTER TABLE lado_queue_entries ADD COLUMN waiting_detail TEXT NOT NULL DEFAULT '';
DROP INDEX lado_queue_entries_open;
CREATE INDEX lado_queue_entries_open ON lado_queue_entries (enqueued_at)
    WHERE dispatched_session_id IS NULL AND failed_at IS NULL AND superseded_at IS NULL
      AND cancelled_at IS NULL AND review_required_reason = '' AND blocked_reason = ''
      AND parts_started_at IS NULL;

CREATE TABLE lado_part_runs (
    queue_entry_id          TEXT PRIMARY KEY REFERENCES lado_queue_entries (id),
    task_id                 TEXT NOT NULL REFERENCES lado_tasks (id),
    project_id              TEXT NOT NULL CHECK (project_id <> ''),
    plan_revision_id        TEXT NOT NULL REFERENCES lado_plan_revisions (id),
    repo_dir                TEXT NOT NULL CHECK (repo_dir <> ''),
    branch                  TEXT NOT NULL CHECK (branch <> ''),
    base_sha                TEXT NOT NULL CHECK (base_sha <> ''),
    head_sha                TEXT NOT NULL CHECK (head_sha <> ''),
    plan_base_sha           TEXT NOT NULL DEFAULT '',
    base_commits_ahead      INTEGER,
    base_branch             TEXT NOT NULL DEFAULT '',
    base_remote             TEXT NOT NULL DEFAULT '',
    dispatch_local_base_sha TEXT NOT NULL DEFAULT '',
    started_at              TIMESTAMP NOT NULL,
    integration_attempts    INTEGER NOT NULL DEFAULT 0 CHECK (integration_attempts >= 0),
    integration_retry_at    TIMESTAMP
);

CREATE TABLE lado_task_parts (
    queue_entry_id      TEXT NOT NULL REFERENCES lado_part_runs (queue_entry_id),
    part_id             TEXT NOT NULL CHECK (part_id <> ''),
    task_id             TEXT NOT NULL REFERENCES lado_tasks (id),
    title               TEXT NOT NULL DEFAULT '',
    position            INTEGER NOT NULL CHECK (position >= 0),
    phase               INTEGER NOT NULL DEFAULT 0,
    steps_json          TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(steps_json)),
    paths_json          TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(paths_json)),
    depends_on_json     TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(depends_on_json)),
    state               TEXT NOT NULL DEFAULT 'waiting'
        CHECK (state IN ('waiting', 'running', 'done', 'merging', 'merged', 'conflict', 'failed')),
    attempts            INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    merge_attempts      INTEGER NOT NULL DEFAULT 0 CHECK (merge_attempts >= 0),
    waiting_reason      TEXT NOT NULL DEFAULT '',
    waiting_detail      TEXT NOT NULL DEFAULT '',
    session_id          TEXT NOT NULL DEFAULT '',
    branch              TEXT NOT NULL DEFAULT '',
    start_sha           TEXT NOT NULL DEFAULT '',
    merge_from_sha      TEXT NOT NULL DEFAULT '',
    merge_tip_sha       TEXT NOT NULL DEFAULT '',
    merged_sha          TEXT NOT NULL DEFAULT '',
    merge_session_id    TEXT NOT NULL DEFAULT '',
    merge_branch        TEXT NOT NULL DEFAULT '',
    summary             TEXT NOT NULL DEFAULT '',
    error_message       TEXT NOT NULL DEFAULT '',
    updated_at          TIMESTAMP NOT NULL,
    PRIMARY KEY (queue_entry_id, part_id)
);
CREATE INDEX lado_task_parts_by_task ON lado_task_parts (task_id);

ALTER TABLE lado_dispatch_settings ADD COLUMN max_workers INTEGER NOT NULL DEFAULT 6 CHECK (max_workers BETWEEN 1 AND 12);
ALTER TABLE lado_dispatch_settings ADD COLUMN hold_on_disk_low INTEGER NOT NULL DEFAULT 1 CHECK (hold_on_disk_low IN (0, 1));
ALTER TABLE lado_dispatch_settings ADD COLUMN min_free_disk_gb INTEGER NOT NULL DEFAULT 10 CHECK (min_free_disk_gb BETWEEN 1 AND 1000);

ALTER TABLE lado_project_settings ADD COLUMN overlap_wait INTEGER NOT NULL DEFAULT 1 CHECK (overlap_wait IN (0, 1));
ALTER TABLE lado_project_settings ADD COLUMN overlap_ignore_json TEXT CHECK (overlap_ignore_json IS NULL OR json_valid(overlap_ignore_json));

-- +goose Down
PRAGMA defer_foreign_keys = ON;
-- The queue trigger reads columns this drops; the next open recreates it.
DROP TRIGGER IF EXISTS lado_cdc_queue_entries_update;
ALTER TABLE lado_project_settings DROP COLUMN overlap_ignore_json;
ALTER TABLE lado_project_settings DROP COLUMN overlap_wait;
ALTER TABLE lado_dispatch_settings DROP COLUMN min_free_disk_gb;
ALTER TABLE lado_dispatch_settings DROP COLUMN hold_on_disk_low;
ALTER TABLE lado_dispatch_settings DROP COLUMN max_workers;
DROP TABLE lado_task_parts;
DROP TABLE lado_part_runs;
DROP INDEX lado_queue_entries_open;
CREATE INDEX lado_queue_entries_open ON lado_queue_entries (enqueued_at)
    WHERE dispatched_session_id IS NULL AND failed_at IS NULL AND superseded_at IS NULL
      AND cancelled_at IS NULL AND review_required_reason = '' AND blocked_reason = '';
ALTER TABLE lado_queue_entries DROP COLUMN waiting_detail;
ALTER TABLE lado_queue_entries DROP COLUMN part_spawn_expires_at;
ALTER TABLE lado_queue_entries DROP COLUMN part_spawn_request;
ALTER TABLE lado_queue_entries DROP COLUMN parts_started_at;
DELETE FROM lado_executions WHERE role IN ('part', 'merge');
CREATE TABLE lado_executions_next (
    session_id              TEXT PRIMARY KEY CHECK (session_id <> ''),
    task_id                 TEXT NOT NULL REFERENCES lado_tasks (id),
    queue_entry_id          TEXT NOT NULL UNIQUE REFERENCES lado_queue_entries (id),
    plan_revision_id        TEXT NOT NULL REFERENCES lado_plan_revisions (id),
    plan_approval_id        TEXT NOT NULL REFERENCES lado_plan_approvals (id),
    policy_snapshot_id      TEXT NOT NULL REFERENCES lado_policy_snapshots (id),
    attempt                 INTEGER NOT NULL CHECK (attempt > 0),
    plan_base_sha           TEXT NOT NULL DEFAULT '',
    dispatch_base_sha       TEXT NOT NULL DEFAULT '',
    base_commits_ahead      INTEGER,
    started_at              TIMESTAMP NOT NULL,
    deadline_at             TIMESTAMP,
    timed_out_at            TIMESTAMP,
    base_branch             TEXT NOT NULL DEFAULT '',
    base_remote             TEXT NOT NULL DEFAULT '',
    dispatch_local_base_sha TEXT NOT NULL DEFAULT '',
    integration_gated       INTEGER NOT NULL DEFAULT 0 CHECK (integration_gated IN (0, 1)),
    settled_at              TIMESTAMP,
    settled_pr_url          TEXT NOT NULL DEFAULT '',
    settled_pr_state        TEXT NOT NULL DEFAULT ''
);
INSERT INTO lado_executions_next SELECT session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id,
    attempt, plan_base_sha, dispatch_base_sha, base_commits_ahead, started_at, deadline_at, timed_out_at,
    base_branch, base_remote, dispatch_local_base_sha, integration_gated, settled_at, settled_pr_url,
    settled_pr_state
FROM lado_executions;
DROP TABLE lado_executions;
CREATE TABLE lado_executions (
    session_id              TEXT PRIMARY KEY CHECK (session_id <> ''),
    task_id                 TEXT NOT NULL REFERENCES lado_tasks (id),
    queue_entry_id          TEXT NOT NULL UNIQUE REFERENCES lado_queue_entries (id),
    plan_revision_id        TEXT NOT NULL REFERENCES lado_plan_revisions (id),
    plan_approval_id        TEXT NOT NULL REFERENCES lado_plan_approvals (id),
    policy_snapshot_id      TEXT NOT NULL REFERENCES lado_policy_snapshots (id),
    attempt                 INTEGER NOT NULL CHECK (attempt > 0),
    plan_base_sha           TEXT NOT NULL DEFAULT '',
    dispatch_base_sha       TEXT NOT NULL DEFAULT '',
    base_commits_ahead      INTEGER,
    started_at              TIMESTAMP NOT NULL,
    deadline_at             TIMESTAMP,
    timed_out_at            TIMESTAMP,
    base_branch             TEXT NOT NULL DEFAULT '',
    base_remote             TEXT NOT NULL DEFAULT '',
    dispatch_local_base_sha TEXT NOT NULL DEFAULT '',
    integration_gated       INTEGER NOT NULL DEFAULT 0 CHECK (integration_gated IN (0, 1)),
    settled_at              TIMESTAMP,
    settled_pr_url          TEXT NOT NULL DEFAULT '',
    settled_pr_state        TEXT NOT NULL DEFAULT ''
);
INSERT INTO lado_executions SELECT * FROM lado_executions_next;
DROP TABLE lado_executions_next;
CREATE INDEX lado_executions_by_task ON lado_executions (task_id, started_at);
