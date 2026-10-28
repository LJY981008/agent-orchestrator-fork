-- LADO fork: verification, evidence and integration approval
-- (docs/LJY-PRD/TDD.md sections 3.2, 7 and 8). The verification loop runs the
-- project's check commands on an execution's PR head, stores one record per
-- execution and head, the review rounds it starts and the evidence it
-- resolves for a head; the developer approves the integration of one head
-- with a presence proof, which is stored once (0004), so a proof approves
-- once. Executions gain the base branch facts the dispatcher resolved and the
-- time the loop settled their PR, and project settings gain the check
-- commands and their time limit. Foreign keys join LADO tables only (TDD
-- section 4.2). Task states stay derived at read time: a record is evidence
-- about one head, like a check run. Evidence criterion 0 with state 'file'
-- records the pinned evidence file itself.
--
-- Executions dispatched before this migration had no integration gate: they
-- keep integration_gated 0, so their merges never count as merges without
-- approval, and those whose newest PR already merged or closed are settled
-- here, at the PR's last update.

-- +goose Up
ALTER TABLE lado_executions ADD COLUMN base_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_executions ADD COLUMN base_remote TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_executions ADD COLUMN dispatch_local_base_sha TEXT NOT NULL DEFAULT '';
-- 1 for an execution dispatched under integration approval.
ALTER TABLE lado_executions ADD COLUMN integration_gated INTEGER NOT NULL DEFAULT 0 CHECK (integration_gated IN (0, 1));
-- When the loop settled the execution's newest PR, and which PR in which
-- state: a PR that opens again, or a newer PR, is settled again.
ALTER TABLE lado_executions ADD COLUMN settled_at TIMESTAMP;
ALTER TABLE lado_executions ADD COLUMN settled_pr_url TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_executions ADD COLUMN settled_pr_state TEXT NOT NULL DEFAULT '';

UPDATE lado_executions
SET settled_at = (
        SELECT pr.updated_at FROM pr WHERE pr.session_id = lado_executions.session_id
        ORDER BY pr.updated_at DESC, pr.url DESC LIMIT 1
    ),
    settled_pr_url = (
        SELECT pr.url FROM pr WHERE pr.session_id = lado_executions.session_id
        ORDER BY pr.updated_at DESC, pr.url DESC LIMIT 1
    ),
    settled_pr_state = (
        SELECT pr.pr_state FROM pr WHERE pr.session_id = lado_executions.session_id
        ORDER BY pr.updated_at DESC, pr.url DESC LIMIT 1
    )
WHERE (
    SELECT pr.pr_state FROM pr WHERE pr.session_id = lado_executions.session_id
    ORDER BY pr.updated_at DESC, pr.url DESC LIMIT 1
) IN ('merged', 'closed');

ALTER TABLE lado_project_settings ADD COLUMN check_commands TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(check_commands));
ALTER TABLE lado_project_settings ADD COLUMN check_timeout_minutes INTEGER NOT NULL DEFAULT 30 CHECK (check_timeout_minutes BETWEEN 5 AND 120);

CREATE TABLE lado_check_runs (
    id                   TEXT PRIMARY KEY CHECK (id <> ''),
    execution_session_id TEXT NOT NULL REFERENCES lado_executions (session_id),
    head_sha             TEXT NOT NULL CHECK (length(head_sha) IN (40, 64)),
    name                 TEXT NOT NULL CHECK (name <> ''),
    command              TEXT NOT NULL CHECK (command <> ''),
    status               TEXT NOT NULL CHECK (status IN ('running', 'passed', 'failed', 'timed_out', 'cancelled', 'error')),
    exit_code            INTEGER,
    log_sha256           TEXT NOT NULL DEFAULT '',
    error_message        TEXT NOT NULL DEFAULT '',
    -- sha256 of the worktree's git status after the run; '' when unread.
    worktree_sha256      TEXT NOT NULL DEFAULT '',
    started_at           TIMESTAMP NOT NULL,
    finished_at          TIMESTAMP
);
CREATE INDEX lado_check_runs_by_head ON lado_check_runs (execution_session_id, head_sha, name, started_at);

CREATE TABLE lado_verifications (
    execution_session_id TEXT NOT NULL REFERENCES lado_executions (session_id),
    head_sha             TEXT NOT NULL CHECK (length(head_sha) IN (40, 64)),
    pr_url               TEXT NOT NULL CHECK (pr_url <> ''),
    outcome              TEXT NOT NULL CHECK (outcome IN ('pass', 'wait', 'fail', 'confirm')),
    reason               TEXT NOT NULL DEFAULT '',
    content_json         TEXT NOT NULL CHECK (json_valid(content_json)),
    sha256               TEXT NOT NULL CHECK (length(sha256) = 64),
    notified_json        TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(notified_json)),
    -- The failed protection read's message, outside the content and its hash.
    protection_error     TEXT NOT NULL DEFAULT '',
    computed_at          TIMESTAMP NOT NULL,
    PRIMARY KEY (execution_session_id, head_sha)
);

CREATE TABLE lado_execution_reviews (
    review_run_id        TEXT PRIMARY KEY CHECK (review_run_id <> ''),
    execution_session_id TEXT NOT NULL REFERENCES lado_executions (session_id),
    head_sha             TEXT NOT NULL,
    round                INTEGER NOT NULL CHECK (round BETWEEN 1 AND 3),
    created_at           TIMESTAMP NOT NULL,
    cancelled_at         TIMESTAMP
);

CREATE TABLE lado_evidence_links (
    id                   TEXT PRIMARY KEY CHECK (id <> ''),
    execution_session_id TEXT NOT NULL REFERENCES lado_executions (session_id),
    head_sha             TEXT NOT NULL,
    criterion            INTEGER NOT NULL CHECK (criterion >= 0),
    state                TEXT NOT NULL CHECK (state IN ('linked', 'stale', 'claimed', 'inferred', 'file')),
    ref_kind             TEXT NOT NULL CHECK (ref_kind IN ('pr_check', 'review_run', 'artifact_version', 'lado_check_run', 'commit', 'claim', 'none')),
    ref_key              TEXT NOT NULL DEFAULT '',
    sha                  TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMP NOT NULL
);
CREATE INDEX lado_evidence_links_by_head ON lado_evidence_links (execution_session_id, head_sha, criterion);

CREATE TABLE lado_integration_approvals (
    id                   TEXT PRIMARY KEY CHECK (id <> ''),
    execution_session_id TEXT NOT NULL REFERENCES lado_executions (session_id),
    pr_url               TEXT NOT NULL CHECK (pr_url <> ''),
    head_sha             TEXT NOT NULL CHECK (length(head_sha) IN (40, 64)),
    -- The branch the PR merged into when the head was verified: an approval
    -- holds for that PR, head and target only.
    target_branch        TEXT NOT NULL DEFAULT '',
    verification_sha256  TEXT NOT NULL CHECK (length(verification_sha256) = 64),
    acknowledged_json    TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(acknowledged_json)),
    proof_nonce          TEXT NOT NULL UNIQUE REFERENCES lado_presence_proofs (nonce),
    actor                TEXT NOT NULL CHECK (actor <> ''),
    approved_at          TIMESTAMP NOT NULL,
    status_state         TEXT NOT NULL CHECK (status_state IN ('pending', 'posted', 'failed', 'not_applicable')),
    status_attempts      INTEGER NOT NULL DEFAULT 0,
    status_posted_at     TIMESTAMP,
    status_error         TEXT NOT NULL DEFAULT '',
    UNIQUE (execution_session_id, pr_url, head_sha, target_branch)
);
CREATE INDEX lado_integration_approvals_by_pr ON lado_integration_approvals (pr_url, head_sha);

-- +goose Down
DROP TABLE lado_integration_approvals;
DROP TABLE lado_evidence_links;
DROP TABLE lado_execution_reviews;
DROP TABLE lado_verifications;
DROP TABLE lado_check_runs;
ALTER TABLE lado_project_settings DROP COLUMN check_timeout_minutes;
ALTER TABLE lado_project_settings DROP COLUMN check_commands;
ALTER TABLE lado_executions DROP COLUMN settled_pr_state;
ALTER TABLE lado_executions DROP COLUMN settled_pr_url;
ALTER TABLE lado_executions DROP COLUMN settled_at;
ALTER TABLE lado_executions DROP COLUMN integration_gated;
ALTER TABLE lado_executions DROP COLUMN dispatch_local_base_sha;
ALTER TABLE lado_executions DROP COLUMN base_remote;
ALTER TABLE lado_executions DROP COLUMN base_branch;
