-- LADO fork: rules, rule versions, rule bindings and rule snapshots
-- (docs/LJY-PRD/HARNESS.md sections 3, 8 and 13). Rule definitions are global.
-- A binding turns a rule on or off globally, or forces it on or off for one
-- project or task; no project or task binding means INHERIT. scope_id names
-- projects.id or lado_tasks.id without a FOREIGN KEY (TDD section 4.2).
-- Versions never change. A snapshot is the canonical JSON of what applied to
-- one plan approval, and lado_policy_snapshot_rules lists the versions it
-- used, so a used rule is never deleted.

-- +goose Up
CREATE TABLE lado_rules (
    id          TEXT PRIMARY KEY CHECK (id <> ''),
    builtin     INTEGER NOT NULL CHECK (builtin IN (0, 1)),
    protected   INTEGER NOT NULL CHECK (protected IN (0, 1)),
    created_at  TIMESTAMP NOT NULL,
    archived_at TIMESTAMP
);

CREATE TABLE lado_rule_versions (
    id               TEXT PRIMARY KEY CHECK (id <> ''),
    rule_id          TEXT NOT NULL REFERENCES lado_rules (id),
    number           INTEGER NOT NULL CHECK (number > 0),
    name             TEXT NOT NULL CHECK (name <> ''),
    category         TEXT NOT NULL DEFAULT '',
    instruction      TEXT NOT NULL DEFAULT '',
    enforcement_json TEXT NOT NULL CHECK (json_valid(enforcement_json)),
    config_json      TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(config_json)),
    conflicts_json   TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(conflicts_json)),
    sha256           TEXT NOT NULL CHECK (length(sha256) = 64),
    actor            TEXT NOT NULL CHECK (actor <> ''),
    created_at       TIMESTAMP NOT NULL,
    UNIQUE (rule_id, number)
);

CREATE TABLE lado_rule_bindings (
    rule_id    TEXT NOT NULL REFERENCES lado_rules (id),
    scope      TEXT NOT NULL CHECK (scope IN ('global', 'project', 'task')),
    scope_id   TEXT NOT NULL,
    state      TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (rule_id, scope, scope_id),
    CHECK (
        (scope = 'global' AND scope_id = '' AND state IN ('on', 'off')) OR
        (scope <> 'global' AND scope_id <> '' AND state IN ('force_on', 'force_off'))
    )
);

CREATE INDEX lado_rule_bindings_by_scope ON lado_rule_bindings (scope, scope_id);

CREATE TABLE lado_policy_snapshots (
    id            TEXT PRIMARY KEY CHECK (id <> ''),
    approval_id   TEXT NOT NULL UNIQUE REFERENCES lado_plan_approvals (id),
    sha256        TEXT NOT NULL CHECK (length(sha256) = 64),
    snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json)),
    blocking      INTEGER NOT NULL CHECK (blocking IN (0, 1)),
    created_at    TIMESTAMP NOT NULL
);

CREATE TABLE lado_policy_snapshot_rules (
    snapshot_id     TEXT NOT NULL REFERENCES lado_policy_snapshots (id),
    rule_id         TEXT NOT NULL REFERENCES lado_rules (id),
    rule_version_id TEXT NOT NULL REFERENCES lado_rule_versions (id),
    PRIMARY KEY (snapshot_id, rule_version_id)
);

CREATE INDEX lado_policy_snapshot_rules_by_rule ON lado_policy_snapshot_rules (rule_id);

-- +goose Down
DROP TABLE lado_policy_snapshot_rules;
DROP TABLE lado_policy_snapshots;
DROP TABLE lado_rule_bindings;
DROP TABLE lado_rule_versions;
DROP TABLE lado_rules;
