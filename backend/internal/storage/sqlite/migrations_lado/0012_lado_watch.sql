-- LADO fork: worker watch (issue #42, docs/LJY-PRD/TDD.md section 4.2).
--
-- lado_watch_settings holds the watch settings: the row with project_id ''
-- is the global row, a project id row overrides it. A NULL column follows the
-- global row, and a NULL in the global row the built-in default
-- (domain.LadoWatchDefaults). Amounts are whole cents.
--
-- lado_session_watch is what LADO watches of one task session. Three writers
-- share a row and each updates only its own group of columns: the compaction
-- counter (read offsets, compactions), the limit watch (limit state, reset
-- time, resumes, limit stops) and the stall watch (stall state, nudges,
-- stalls). The stop and stall counts number each one's alert.
--
-- lado_account_limits names the agent accounts that hit a usage limit and
-- when the limit resets; LADO starts no worker on such an account until then.
--
-- lado_alerts holds the one-line alerts. session_id is '' for an alert of a
-- task's total, never NULL: SQLite treats NULLs in a UNIQUE key as distinct,
-- so a NULL session would add a row each time the same alert is raised.
--
-- session_id names sessions.id and project_id projects.id without a foreign
-- key (TDD section 4.2). The lado_cdc_* triggers live in lado_migrate.go.
-- 0011's tables are unchanged.

-- +goose Up
CREATE TABLE lado_watch_settings (
    project_id          TEXT PRIMARY KEY,
    compact_window      INTEGER CHECK (compact_window IS NULL OR compact_window BETWEEN 100000 AND 1000000),
    task_cost_cents     INTEGER CHECK (task_cost_cents IS NULL OR task_cost_cents > 0),
    session_cost_cents  INTEGER CHECK (session_cost_cents IS NULL OR session_cost_cents > 0),
    session_compactions INTEGER CHECK (session_compactions IS NULL OR session_compactions > 0),
    stall_minutes       INTEGER CHECK (stall_minutes IS NULL OR stall_minutes > 0),
    wake_enabled        INTEGER CHECK (wake_enabled IS NULL OR wake_enabled IN (0, 1)),
    updated_at          TIMESTAMP NOT NULL
);

CREATE TABLE lado_session_watch (
    session_id         TEXT PRIMARY KEY CHECK (session_id <> ''),
    task_id            TEXT NOT NULL REFERENCES lado_tasks (id),
    read_offsets_json  TEXT NOT NULL DEFAULT '{}'
        CHECK (json_valid(read_offsets_json) AND json_type(read_offsets_json, '$') = 'object'),
    compactions        INTEGER NOT NULL DEFAULT 0 CHECK (compactions >= 0),
    compacted_at       TIMESTAMP,
    limit_state        TEXT NOT NULL DEFAULT '' CHECK (limit_state IN ('', 'limited')),
    limit_reset_at     TIMESTAMP,
    limit_reset_guess  INTEGER NOT NULL DEFAULT 0 CHECK (limit_reset_guess IN (0, 1)),
    limit_seen_at      TIMESTAMP,
    resumes            INTEGER NOT NULL DEFAULT 0 CHECK (resumes >= 0),
    resumed_at         TIMESTAMP,
    limit_stops        INTEGER NOT NULL DEFAULT 0 CHECK (limit_stops >= 0),
    stall_state        TEXT NOT NULL DEFAULT '' CHECK (stall_state IN ('', 'stalled')),
    stalled_at         TIMESTAMP,
    nudges             INTEGER NOT NULL DEFAULT 0 CHECK (nudges >= 0),
    nudged_at          TIMESTAMP,
    stalls             INTEGER NOT NULL DEFAULT 0 CHECK (stalls >= 0),
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL
);
CREATE INDEX lado_session_watch_by_task ON lado_session_watch (task_id);

CREATE TABLE lado_account_limits (
    account_key  TEXT PRIMARY KEY CHECK (account_key <> ''),
    session_id   TEXT NOT NULL DEFAULT '',
    reset_at     TIMESTAMP NOT NULL,
    reset_guess  INTEGER NOT NULL DEFAULT 0 CHECK (reset_guess IN (0, 1)),
    detected_at  TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL
);

CREATE TABLE lado_alerts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     TEXT NOT NULL REFERENCES lado_tasks (id),
    session_id  TEXT NOT NULL DEFAULT '',
    kind        TEXT NOT NULL CHECK (kind <> ''),
    level       INTEGER NOT NULL DEFAULT 1 CHECK (level >= 1),
    value       INTEGER NOT NULL DEFAULT 0,
    threshold   INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMP NOT NULL,
    seen_at     TIMESTAMP,
    UNIQUE (task_id, session_id, kind, level)
);
CREATE INDEX lado_alerts_by_created ON lado_alerts (created_at);

-- +goose Down
DROP TABLE lado_alerts;
DROP TABLE lado_account_limits;
DROP TABLE lado_session_watch;
DROP TABLE lado_watch_settings;
