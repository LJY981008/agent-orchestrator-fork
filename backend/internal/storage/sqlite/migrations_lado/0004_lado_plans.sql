-- LADO fork: plan requests, plan revisions, human-presence proofs and plan
-- approvals (docs/LJY-PRD/TDD.md sections 3.1, 5.1 and 5.2). session_id
-- refers to sessions.id without a FOREIGN KEY, because upstream deletes its
-- own rows (TDD section 4.2); foreign keys join LADO tables only. A revision
-- is a pinned copy of one AO artifact version: the bytes live in LADO's blob
-- store under their sha256, so AO pruning cannot remove an approved plan.
-- Each presence proof's nonce is stored once, so a proof cannot be replayed,
-- also not after a daemon restart. Task states stay derived at read time.

-- +goose Up
CREATE TABLE lado_plan_requests (
    id                    TEXT PRIMARY KEY CHECK (id <> ''),
    task_id               TEXT NOT NULL REFERENCES lado_tasks (id),
    session_id            TEXT NOT NULL CHECK (session_id <> ''),
    issue_snapshot_sha256 TEXT NOT NULL CHECK (length(issue_snapshot_sha256) = 64),
    source_comment_id     TEXT NOT NULL DEFAULT '',
    note                  TEXT NOT NULL DEFAULT '',
    based_on_revision_id  TEXT NOT NULL DEFAULT '',
    requested_at          TIMESTAMP NOT NULL,
    delivered_at          TIMESTAMP,
    delivery_attempts     INTEGER NOT NULL DEFAULT 0,
    delivery_error        TEXT NOT NULL DEFAULT '',
    failed_at             TIMESTAMP
);

CREATE INDEX lado_plan_requests_by_task ON lado_plan_requests (task_id, requested_at);

CREATE TABLE lado_plan_revisions (
    id                    TEXT PRIMARY KEY CHECK (id <> ''),
    task_id               TEXT NOT NULL REFERENCES lado_tasks (id),
    request_id            TEXT NOT NULL REFERENCES lado_plan_requests (id),
    number                INTEGER NOT NULL CHECK (number > 0),
    session_id            TEXT NOT NULL CHECK (session_id <> ''),
    path                  TEXT NOT NULL CHECK (path <> ''),
    artifact_version      INTEGER NOT NULL CHECK (artifact_version > 0),
    source                TEXT NOT NULL CHECK (source IN ('agent', 'user')),
    sha256                TEXT NOT NULL CHECK (length(sha256) = 64),
    base_ref              TEXT NOT NULL DEFAULT '',
    base_sha              TEXT NOT NULL DEFAULT '',
    issue_snapshot_sha256 TEXT NOT NULL CHECK (length(issue_snapshot_sha256) = 64),
    problems_json         TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(problems_json)),
    created_at            TIMESTAMP NOT NULL,
    UNIQUE (task_id, number),
    UNIQUE (session_id, path, artifact_version, sha256)
);

CREATE TABLE lado_presence_proofs (
    nonce      TEXT PRIMARY KEY CHECK (nonce <> ''),
    action     TEXT NOT NULL CHECK (action <> ''),
    subject_id TEXT NOT NULL CHECK (subject_id <> ''),
    dev        INTEGER NOT NULL DEFAULT 0 CHECK (dev IN (0, 1)),
    expires_at TIMESTAMP NOT NULL,
    used_at    TIMESTAMP NOT NULL
);

CREATE TABLE lado_plan_approvals (
    id          TEXT PRIMARY KEY CHECK (id <> ''),
    revision_id TEXT NOT NULL REFERENCES lado_plan_revisions (id),
    task_id     TEXT NOT NULL REFERENCES lado_tasks (id),
    sha256      TEXT NOT NULL CHECK (length(sha256) = 64),
    proof_nonce TEXT NOT NULL UNIQUE REFERENCES lado_presence_proofs (nonce),
    approved_at TIMESTAMP NOT NULL
);

CREATE INDEX lado_plan_approvals_by_task ON lado_plan_approvals (task_id, approved_at);

-- +goose Down
DROP TABLE lado_plan_approvals;
DROP TABLE lado_presence_proofs;
DROP TABLE lado_plan_revisions;
DROP TABLE lado_plan_requests;
