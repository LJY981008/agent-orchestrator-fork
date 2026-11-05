-- LADO fork: relaying a developer's instruction to a running worker (issue
-- #49, docs/LJY-PRD/adr/0010-worker-relay.md).
--
-- lado_relays holds one row per relay. LADO writes it as 'sending' before it
-- delivers the message and then sets 'delivered' with delivered_at, or
-- 'failed' with failure, so a delivery never goes unrecorded. Refused
-- requests have no row; they go to lado_audit_events.
--
-- evidence is the developer sentence as the orchestrator quoted it, and
-- evidence_message_id and evidence_at name the developer message of the
-- orchestrator's conversation that contains it. beyond_evidence marks an
-- instruction that is not part of that message, outside_plan one that may
-- leave the approved plan, for the reasons in outside_plan_reasons_json.
--
-- The session and project columns name sessions.id and projects.id without a
-- foreign key (TDD section 4.2). The lado_cdc_* triggers live in
-- lado_migrate.go, never here.

-- +goose Up
CREATE TABLE lado_relays (
    id                         TEXT PRIMARY KEY CHECK (id <> ''),
    task_id                    TEXT NOT NULL REFERENCES lado_tasks (id),
    project_id                 TEXT NOT NULL CHECK (project_id <> ''),
    orchestrator_session_id    TEXT NOT NULL CHECK (orchestrator_session_id <> ''),
    worker_session_id          TEXT NOT NULL CHECK (worker_session_id <> ''),
    worker_role                TEXT NOT NULL DEFAULT '' CHECK (worker_role IN ('', 'part', 'merge', 'integration')),
    part_id                    TEXT NOT NULL DEFAULT '',
    instruction                TEXT NOT NULL CHECK (instruction <> ''),
    evidence                   TEXT NOT NULL CHECK (evidence <> ''),
    evidence_message_id        TEXT NOT NULL CHECK (evidence_message_id <> ''),
    evidence_at                TIMESTAMP NOT NULL,
    beyond_evidence            INTEGER NOT NULL DEFAULT 0 CHECK (beyond_evidence IN (0, 1)),
    outside_plan               INTEGER NOT NULL DEFAULT 0 CHECK (outside_plan IN (0, 1)),
    outside_plan_reasons_json  TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(outside_plan_reasons_json) AND json_type(outside_plan_reasons_json, '$') = 'array'),
    state                      TEXT NOT NULL DEFAULT 'sending' CHECK (state IN ('sending', 'delivered', 'failed')),
    failure                    TEXT NOT NULL DEFAULT '',
    created_at                 TIMESTAMP NOT NULL,
    delivered_at               TIMESTAMP,
    CHECK ((state = 'delivered') = (delivered_at IS NOT NULL))
);
CREATE INDEX lado_relays_by_task ON lado_relays (task_id, created_at);

-- +goose Down
DROP INDEX lado_relays_by_task;
DROP TABLE lado_relays;
