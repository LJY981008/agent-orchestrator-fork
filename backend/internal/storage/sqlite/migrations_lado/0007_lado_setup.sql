-- LADO fork: adopted local setup (docs/LJY-PRD/HARNESS.md sections 8 to 10
-- and 13). An adoption moves user-level Claude Code and Codex setup into
-- LADO's managed folder after a backup; each item it moved, or the app
-- created (no adoption), keeps every version as a blob named by sha256.
-- Items are never hard deleted: deleted_at or restored_at stops delivery and
-- frees the name. No Global binding row means ON, unlike rules; a project or
-- task binding forces one scope on or off, and no row there means INHERIT.
-- scope_id names projects.id or lado_tasks.id without a FOREIGN KEY (TDD
-- section 4.2). An acceptance waives one warning about one item's content in
-- one scope until it is revoked. lado_policy_snapshot_setup lists the item
-- versions a rule snapshot used, so they stay readable.

-- +goose Up
CREATE TABLE lado_setup_adoptions (
    id              TEXT PRIMARY KEY CHECK (id <> ''),
    created_at      TIMESTAMP NOT NULL,
    actor           TEXT NOT NULL CHECK (actor <> ''),
    os              TEXT NOT NULL CHECK (os <> ''),
    plan_sha256     TEXT NOT NULL CHECK (length(plan_sha256) = 64),
    manifest_sha256 TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL CHECK (state IN ('completed', 'partial')),
    error_message   TEXT NOT NULL DEFAULT '',
    restored_at     TIMESTAMP,
    restore_report  TEXT NOT NULL DEFAULT '' CHECK (restore_report = '' OR json_valid(restore_report))
);

CREATE TABLE lado_setup_items (
    id          TEXT PRIMARY KEY CHECK (id <> ''),
    tool        TEXT NOT NULL CHECK (tool IN ('claude-code', 'codex')),
    kind        TEXT NOT NULL CHECK (kind IN ('instructions', 'setting', 'skill', 'agent', 'command', 'output_style', 'hook_script', 'mcp_server')),
    name        TEXT NOT NULL CHECK (name <> ''),
    origin_path TEXT NOT NULL DEFAULT '',
    adoption_id TEXT REFERENCES lado_setup_adoptions (id),
    created_at  TIMESTAMP NOT NULL,
    deleted_at  TIMESTAMP,
    restored_at TIMESTAMP
);
CREATE UNIQUE INDEX lado_setup_items_live ON lado_setup_items (tool, kind, name)
    WHERE deleted_at IS NULL AND restored_at IS NULL;

CREATE TABLE lado_setup_item_versions (
    id           TEXT PRIMARY KEY CHECK (id <> ''),
    item_id      TEXT NOT NULL REFERENCES lado_setup_items (id),
    version      INTEGER NOT NULL CHECK (version > 0),
    content_kind TEXT NOT NULL CHECK (content_kind IN ('file', 'json', 'tree')),
    sha256       TEXT NOT NULL CHECK (length(sha256) = 64),
    size_bytes   INTEGER NOT NULL CHECK (size_bytes >= 0),
    executable   INTEGER NOT NULL DEFAULT 0 CHECK (executable IN (0, 1)),
    actor        TEXT NOT NULL CHECK (actor <> ''),
    created_at   TIMESTAMP NOT NULL,
    UNIQUE (item_id, version)
);

CREATE TABLE lado_setup_bindings (
    item_id    TEXT NOT NULL REFERENCES lado_setup_items (id),
    scope      TEXT NOT NULL CHECK (scope IN ('global', 'project', 'task')),
    scope_id   TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL CHECK (state IN ('on', 'off', 'force_on', 'force_off')),
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (item_id, scope, scope_id),
    CHECK ((scope = 'global' AND scope_id = '' AND state IN ('on', 'off'))
        OR (scope <> 'global' AND scope_id <> '' AND state IN ('force_on', 'force_off')))
);

CREATE TABLE lado_conflict_acceptances (
    id             TEXT PRIMARY KEY CHECK (id <> ''),
    code           TEXT NOT NULL CHECK (code <> ''),
    item_id        TEXT NOT NULL REFERENCES lado_setup_items (id),
    detail         TEXT NOT NULL DEFAULT '',
    content_sha256 TEXT NOT NULL CHECK (length(content_sha256) = 64),
    scope          TEXT NOT NULL CHECK (scope IN ('global', 'project', 'task')),
    scope_id       TEXT NOT NULL DEFAULT '',
    actor          TEXT NOT NULL CHECK (actor <> ''),
    created_at     TIMESTAMP NOT NULL,
    revoked_at     TIMESTAMP
);

CREATE TABLE lado_policy_snapshot_setup (
    snapshot_id     TEXT NOT NULL REFERENCES lado_policy_snapshots (id),
    item_version_id TEXT NOT NULL REFERENCES lado_setup_item_versions (id),
    PRIMARY KEY (snapshot_id, item_version_id)
);

-- +goose Down
DROP TABLE lado_policy_snapshot_setup;
DROP TABLE lado_conflict_acceptances;
DROP TABLE lado_setup_bindings;
DROP TABLE lado_setup_item_versions;
DROP TABLE lado_setup_items;
DROP TABLE lado_setup_adoptions;
