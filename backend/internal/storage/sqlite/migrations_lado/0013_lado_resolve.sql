-- LADO fork: overlapping runs (issue #53, docs/LJY-PRD/TDD.md sections 5.4,
-- 7.3 and 8).
--
-- lado_pr_resolves is LADO's row for resolving one execution's open PR when
-- it conflicts with its base, or the base moved under its head with a change
-- to the same paths, and the execution's own session no longer runs. A
-- resolve worker is a merge worker without a part. state is '' while none
-- runs and none gave up, 'resolving' while one runs or LADO waits for its push,
-- and 'needs_human' once LADO stopped. attempts counts failed workers in a row,
-- spawns every worker started, which numbers the next client request id
-- lado:<entry>/_resolve/<spawns+1>. spawn_request is the id of a worker being
-- started, which the worker gate grants until spawn_expires_at; it is unique
-- while set. version counts the row's writes for the optimistic upsert.
--
-- lado_integration_approvals gains the remote target branch's commit when the
-- head was approved (approved_base_sha, '' when LADO did not read it), and
-- when and why LADO revoked the approval (revoked_at, revoked_reason). A
-- revoked row stays, so the same head and target cannot be approved again.
--
-- The change triggers live in lado_migrate.go, never here.

-- +goose Up
CREATE TABLE lado_pr_resolves (
    execution_session_id TEXT PRIMARY KEY REFERENCES lado_executions (session_id),
    task_id              TEXT NOT NULL REFERENCES lado_tasks (id),
    queue_entry_id       TEXT NOT NULL REFERENCES lado_queue_entries (id),
    state                TEXT NOT NULL DEFAULT '' CHECK (state IN ('', 'resolving', 'needs_human')),
    attempts             INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    spawns               INTEGER NOT NULL DEFAULT 0 CHECK (spawns >= 0),
    session_id           TEXT NOT NULL DEFAULT '',
    start_head_sha       TEXT NOT NULL DEFAULT '',
    base_tip_sha         TEXT NOT NULL DEFAULT '',
    spawn_request        TEXT NOT NULL DEFAULT '',
    spawn_expires_at     TIMESTAMP,
    done_seen_at         TIMESTAMP,
    notified_head_sha    TEXT NOT NULL DEFAULT '',
    error                TEXT NOT NULL DEFAULT '',
    version              INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at           TIMESTAMP NOT NULL,
    updated_at           TIMESTAMP NOT NULL,
    CHECK (spawn_request = '' OR spawn_expires_at IS NOT NULL)
);
CREATE UNIQUE INDEX lado_pr_resolves_by_spawn_request ON lado_pr_resolves (spawn_request) WHERE spawn_request <> '';
CREATE INDEX lado_pr_resolves_by_entry ON lado_pr_resolves (queue_entry_id);

ALTER TABLE lado_integration_approvals ADD COLUMN approved_base_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_integration_approvals ADD COLUMN revoked_at TIMESTAMP;
ALTER TABLE lado_integration_approvals ADD COLUMN revoked_reason TEXT NOT NULL DEFAULT '';

-- +goose Down
-- The approvals' update trigger reads revoked_at; the next open creates it
-- again.
DROP TRIGGER IF EXISTS lado_cdc_integration_approvals_update;
ALTER TABLE lado_integration_approvals DROP COLUMN revoked_reason;
ALTER TABLE lado_integration_approvals DROP COLUMN revoked_at;
ALTER TABLE lado_integration_approvals DROP COLUMN approved_base_sha;
DROP INDEX lado_pr_resolves_by_entry;
DROP INDEX lado_pr_resolves_by_spawn_request;
DROP TABLE lado_pr_resolves;
