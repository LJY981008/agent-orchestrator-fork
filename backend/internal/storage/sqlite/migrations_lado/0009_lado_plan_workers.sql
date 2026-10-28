-- LADO fork: plan workers and orchestrator notices (docs/LJY-PRD/TDD.md
-- sections 4.2 and 5.1, docs/LJY-PRD/adr/0007-plan-workers.md). A plan
-- request no longer goes to the orchestrator: LADO starts a read-only
-- designer session for it, has a critic session review the draft unless the
-- plan is small, and pins the final plan the designer submits. Requests made
-- before this migration keep kind 'orchestrator', so the plans their
-- orchestrators write are still pinned. A worker request holds the
-- placeholder 'lado:plan/<id>' in session_id until its designer starts, which
-- names no session. Critiques and revision metrics belong to a request, so
-- foreign keys join LADO tables only (TDD section 4.2). Notices wait in an
-- outbox until the project's orchestrator is idle; lado_notice_state.since is
-- when they started, so events from before this migration are never told.
-- Request phases stay derived at read time.

-- +goose Up
ALTER TABLE lado_plan_requests ADD COLUMN kind TEXT NOT NULL DEFAULT 'orchestrator' CHECK (kind IN ('orchestrator', 'worker'));
ALTER TABLE lado_plan_requests ADD COLUMN waiting_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_plan_requests ADD COLUMN worker_attempts INTEGER NOT NULL DEFAULT 0 CHECK (worker_attempts >= 0);
ALTER TABLE lado_plan_requests ADD COLUMN worker_started_at TIMESTAMP;
ALTER TABLE lado_plan_requests ADD COLUMN draft_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_plan_requests ADD COLUMN draft_submitted_at TIMESTAMP;
ALTER TABLE lado_plan_requests ADD COLUMN feedback_sent_at TIMESTAMP;
ALTER TABLE lado_plan_requests ADD COLUMN final_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_plan_requests ADD COLUMN submitted_at TIMESTAMP;
ALTER TABLE lado_plan_requests ADD COLUMN fix_requested_at TIMESTAMP;
ALTER TABLE lado_plan_requests ADD COLUMN reminded_at TIMESTAMP;
ALTER TABLE lado_plan_requests ADD COLUMN cleaned_at TIMESTAMP;
ALTER TABLE lado_plan_requests ADD COLUMN failure_code TEXT NOT NULL DEFAULT '';

CREATE TABLE lado_plan_critiques (
    id             TEXT PRIMARY KEY CHECK (id <> ''),
    request_id     TEXT NOT NULL REFERENCES lado_plan_requests (id),
    -- 1 and 2 for the critic sessions, 0 for a skipped review.
    attempt        INTEGER NOT NULL CHECK (attempt BETWEEN 0 AND 2),
    session_id     TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL CHECK (state IN ('running', 'done', 'failed', 'skipped')),
    skip_reason    TEXT NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    findings_json  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(findings_json)),
    started_at     TIMESTAMP NOT NULL,
    finished_at    TIMESTAMP,
    UNIQUE (request_id, attempt)
);

CREATE TABLE lado_plan_revision_metrics (
    revision_id         TEXT PRIMARY KEY REFERENCES lado_plan_revisions (id),
    request_id          TEXT NOT NULL REFERENCES lado_plan_requests (id),
    design_ms           INTEGER NOT NULL DEFAULT 0 CHECK (design_ms >= 0),
    critique_ms         INTEGER NOT NULL DEFAULT 0 CHECK (critique_ms >= 0),
    revise_ms           INTEGER NOT NULL DEFAULT 0 CHECK (revise_ms >= 0),
    review              TEXT NOT NULL CHECK (review IN ('done', 'skipped', 'failed', 'none')),
    p1_count            INTEGER NOT NULL DEFAULT 0,
    major_count         INTEGER NOT NULL DEFAULT 0,
    minor_count         INTEGER NOT NULL DEFAULT 0,
    addressed_count     INTEGER NOT NULL DEFAULT 0,
    not_addressed_count INTEGER NOT NULL DEFAULT 0,
    sessions_json       TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(sessions_json)),
    change_kinds_json   TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(change_kinds_json)),
    updated_at          TIMESTAMP NOT NULL
);

CREATE TABLE lado_orchestrator_notices (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT NOT NULL CHECK (project_id <> ''),
    notice_key  TEXT NOT NULL UNIQUE CHECK (notice_key <> ''),
    text        TEXT NOT NULL CHECK (text <> ''),
    occurred_at TIMESTAMP NOT NULL,
    created_at  TIMESTAMP NOT NULL,
    sent_at     TIMESTAMP,
    sent_to     TEXT NOT NULL DEFAULT '',
    dropped_at  TIMESTAMP
);
CREATE INDEX lado_orchestrator_notices_waiting ON lado_orchestrator_notices (project_id, occurred_at)
    WHERE sent_at IS NULL AND dropped_at IS NULL;

CREATE TABLE lado_notice_state (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    since TIMESTAMP NOT NULL
);
INSERT INTO lado_notice_state (id, since) VALUES (1, datetime('now'));

ALTER TABLE lado_dispatch_settings ADD COLUMN max_plan_workers INTEGER NOT NULL DEFAULT 2 CHECK (max_plan_workers BETWEEN 1 AND 8);
ALTER TABLE lado_dispatch_settings ADD COLUMN plan_review_skip INTEGER NOT NULL DEFAULT 1 CHECK (plan_review_skip IN (0, 1));
ALTER TABLE lado_dispatch_settings ADD COLUMN plan_review_skip_max_paths INTEGER NOT NULL DEFAULT 3 CHECK (plan_review_skip_max_paths BETWEEN 1 AND 20);
-- '' keeps the default patterns, which may improve with LADO.
ALTER TABLE lado_dispatch_settings ADD COLUMN plan_review_patterns TEXT NOT NULL DEFAULT '' CHECK (plan_review_patterns = '' OR json_valid(plan_review_patterns));

-- +goose Down
-- The request change trigger names the columns this migration adds; LADO
-- recreates it on the next open (lado_migrate.go).
DROP TRIGGER IF EXISTS lado_cdc_plan_requests_update;
ALTER TABLE lado_dispatch_settings DROP COLUMN plan_review_patterns;
ALTER TABLE lado_dispatch_settings DROP COLUMN plan_review_skip_max_paths;
ALTER TABLE lado_dispatch_settings DROP COLUMN plan_review_skip;
ALTER TABLE lado_dispatch_settings DROP COLUMN max_plan_workers;
DROP TABLE lado_notice_state;
DROP TABLE lado_orchestrator_notices;
DROP TABLE lado_plan_revision_metrics;
DROP TABLE lado_plan_critiques;
ALTER TABLE lado_plan_requests DROP COLUMN failure_code;
ALTER TABLE lado_plan_requests DROP COLUMN cleaned_at;
ALTER TABLE lado_plan_requests DROP COLUMN reminded_at;
ALTER TABLE lado_plan_requests DROP COLUMN fix_requested_at;
ALTER TABLE lado_plan_requests DROP COLUMN submitted_at;
ALTER TABLE lado_plan_requests DROP COLUMN final_sha256;
ALTER TABLE lado_plan_requests DROP COLUMN feedback_sent_at;
ALTER TABLE lado_plan_requests DROP COLUMN draft_submitted_at;
ALTER TABLE lado_plan_requests DROP COLUMN draft_sha256;
ALTER TABLE lado_plan_requests DROP COLUMN worker_started_at;
ALTER TABLE lado_plan_requests DROP COLUMN worker_attempts;
ALTER TABLE lado_plan_requests DROP COLUMN waiting_reason;
ALTER TABLE lado_plan_requests DROP COLUMN kind;
