-- LADO fork: production log watch (issue #87).
--
-- lado_tasks.source tells a task the log watch created ('log') from one the
-- developer created or imported (''). Existing rows keep ''.
--
-- lado_log_watch_settings holds a project's settings; lado_log_sources its
-- Docker Engine API hosts and lado_log_targets the containers each follows,
-- in the order the developer set them (position). Removing a source removes
-- its targets, and removing a target its read cursors.
--
-- lado_log_cursors is where reading one instance (container name) of a target
-- goes on: the Docker timestamp, on the host's clock, of the last line LADO
-- handled.
--
-- lado_log_fingerprints are a project's incident kinds, one per service-level
-- fingerprint. They outlive the sources and targets, so issues and the ignore
-- list stay. project_id names projects.id without a foreign key, so a
-- fingerprint stays without a settings row. task_id is the task LADO created;
-- ON DELETE SET NULL lets DeletePendingLadoTask remove a task whose issue was
-- never confirmed. lado_log_instance_counts counts each fingerprint per
-- instance.
--
-- Logs themselves are never stored. The change triggers live in
-- lado_migrate.go, never here.

-- +goose Up
ALTER TABLE lado_tasks ADD COLUMN source TEXT NOT NULL DEFAULT '' CHECK (source IN ('', 'log'));

CREATE TABLE lado_log_watch_settings (
    project_id               TEXT PRIMARY KEY CHECK (project_id <> ''),
    enabled                  INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    repository               TEXT NOT NULL DEFAULT '',
    include_json             TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(include_json) AND json_type(include_json, '$') = 'array'),
    exclude_json             TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(exclude_json) AND json_type(exclude_json, '$') = 'array'),
    hourly_issue_cap         INTEGER NOT NULL DEFAULT 5 CHECK (hourly_issue_cap BETWEEN 1 AND 50),
    auto_plan                INTEGER NOT NULL DEFAULT 1 CHECK (auto_plan IN (0, 1)),
    auto_plan_daily_cap      INTEGER NOT NULL DEFAULT 5 CHECK (auto_plan_daily_cap BETWEEN 1 AND 50),
    auto_plan_min_count      INTEGER NOT NULL DEFAULT 1 CHECK (auto_plan_min_count >= 1),
    disconnect_alert_minutes INTEGER NOT NULL DEFAULT 10 CHECK (disconnect_alert_minutes BETWEEN 1 AND 1440),
    burst_notified_at        TIMESTAMP,
    updated_at               TIMESTAMP NOT NULL
);

CREATE TABLE lado_log_sources (
    id               TEXT PRIMARY KEY CHECK (id <> ''),
    project_id       TEXT NOT NULL REFERENCES lado_log_watch_settings (project_id) ON DELETE CASCADE,
    kind             TEXT NOT NULL CHECK (kind <> ''),
    display_name     TEXT NOT NULL CHECK (display_name <> ''),
    endpoint         TEXT NOT NULL CHECK (endpoint <> ''),
    tls              INTEGER NOT NULL DEFAULT 0 CHECK (tls IN (0, 1)),
    cert_key         TEXT NOT NULL DEFAULT '',
    paused           INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)),
    position         INTEGER NOT NULL DEFAULT 0,
    down_since       TIMESTAMP,
    down_notified_at TIMESTAMP,
    created_at       TIMESTAMP NOT NULL,
    updated_at       TIMESTAMP NOT NULL
);
CREATE INDEX lado_log_sources_by_project ON lado_log_sources (project_id, position);

CREATE TABLE lado_log_targets (
    id              TEXT PRIMARY KEY CHECK (id <> ''),
    source_id       TEXT NOT NULL REFERENCES lado_log_sources (id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('compose_project', 'compose_service', 'name', 'label')),
    value           TEXT NOT NULL CHECK (value <> ''),
    compose_project TEXT NOT NULL DEFAULT '',
    service         TEXT NOT NULL DEFAULT '',
    paused          INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)),
    position        INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMP NOT NULL,
    updated_at      TIMESTAMP NOT NULL
);
CREATE INDEX lado_log_targets_by_source ON lado_log_targets (source_id, position);

CREATE TABLE lado_log_cursors (
    target_id  TEXT NOT NULL REFERENCES lado_log_targets (id) ON DELETE CASCADE,
    instance   TEXT NOT NULL CHECK (instance <> ''),
    since      TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (target_id, instance)
);

CREATE TABLE lado_log_fingerprints (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id             TEXT NOT NULL CHECK (project_id <> ''),
    fingerprint            TEXT NOT NULL CHECK (fingerprint <> ''),
    service                TEXT NOT NULL DEFAULT '',
    exception_kind         TEXT NOT NULL DEFAULT '',
    location               TEXT NOT NULL DEFAULT '',
    count                  INTEGER NOT NULL DEFAULT 0 CHECK (count >= 0),
    first_seen_at          TIMESTAMP NOT NULL,
    last_seen_at           TIMESTAMP NOT NULL,
    task_id                TEXT REFERENCES lado_tasks (id) ON DELETE SET NULL,
    issue_ref              TEXT CHECK (issue_ref IS NULL OR issue_ref <> ''),
    previous_issue_ref     TEXT NOT NULL DEFAULT '',
    issue_created_at       TIMESTAMP,
    auto_plan_requested_at TIMESTAMP,
    auto_plan_held         TEXT NOT NULL DEFAULT '' CHECK (auto_plan_held IN ('', 'cap', 'min_count', 'off', 'failed')),
    ignored                INTEGER NOT NULL DEFAULT 0 CHECK (ignored IN (0, 1)),
    ignored_at             TIMESTAMP,
    created_at             TIMESTAMP NOT NULL,
    updated_at             TIMESTAMP NOT NULL,
    UNIQUE (project_id, fingerprint)
);
CREATE INDEX lado_log_fingerprints_by_last_seen ON lado_log_fingerprints (project_id, last_seen_at);
CREATE INDEX lado_log_fingerprints_by_task ON lado_log_fingerprints (task_id);
CREATE INDEX lado_log_fingerprints_by_issue_created ON lado_log_fingerprints (project_id, issue_created_at);
CREATE INDEX lado_log_fingerprints_by_auto_plan ON lado_log_fingerprints (project_id, auto_plan_requested_at);

CREATE TABLE lado_log_instance_counts (
    fingerprint_id INTEGER NOT NULL REFERENCES lado_log_fingerprints (id) ON DELETE CASCADE,
    instance       TEXT NOT NULL CHECK (instance <> ''),
    image_tag      TEXT NOT NULL DEFAULT '',
    count          INTEGER NOT NULL DEFAULT 0 CHECK (count >= 0),
    last_seen_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (fingerprint_id, instance)
);

-- +goose Down
-- The tables' triggers go with them.
DROP TABLE lado_log_instance_counts;
DROP TABLE lado_log_fingerprints;
DROP TABLE lado_log_cursors;
DROP TABLE lado_log_targets;
DROP TABLE lado_log_sources;
DROP TABLE lado_log_watch_settings;
ALTER TABLE lado_tasks DROP COLUMN source;
