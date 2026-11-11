-- LADO fork: production database changes tied to the integration approval
-- (issue #97, docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md).
--
-- lado_db_change_settings holds one repository's settings: repo is '' in a
-- single-repository project and the child repository's folder in a
-- workspace. paths_json and secret_names_json are JSON arrays of strings;
-- secret values never sit in the database. timeout_minutes 0 is the default.
-- baseline_commit is the default branch's commit whose matching files were
-- recorded as applied when the settings were turned on or their paths moved.
--
-- lado_db_change_runs are one PR head's application of its pre_merge or
-- after_deploy files, one per PR, head and kind. run_trigger says why the run
-- exists (an approval, a merge outside LADO, after-deploy files after a merge).
--
-- lado_db_change_applications record every application of one SQL file, and
-- the baseline rows. A file counts as applied once it has an applied or
-- baseline row for its project, repository and path. exit_code and
-- finished_at stay NULL until the command ends; a row left running by a
-- daemon that stopped becomes interrupted on the next start. output_sha256
-- names the blob with the end of the redacted output; message is one line
-- without secrets.
--
-- lado_db_change_status_holds names the integration approvals whose
-- lado/integration status waits for their pre-merge SQL: the status retry
-- list skips them. lado_integration_approvals itself is not changed.
--
-- The change triggers live in lado_migrate.go, never here.

-- +goose Up
CREATE TABLE lado_db_change_settings (
    project_id        TEXT NOT NULL CHECK (project_id <> ''),
    repo              TEXT NOT NULL DEFAULT '',
    enabled           INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    paths_json        TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(paths_json) AND json_type(paths_json, '$') = 'array'),
    command           TEXT NOT NULL DEFAULT '',
    timeout_minutes   INTEGER NOT NULL DEFAULT 0 CHECK (timeout_minutes = 0 OR timeout_minutes BETWEEN 1 AND 120),
    secret_names_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(secret_names_json) AND json_type(secret_names_json, '$') = 'array'),
    baseline_commit   TEXT NOT NULL DEFAULT '' CHECK (baseline_commit = '' OR length(baseline_commit) IN (40, 64)),
    enabled_at        TIMESTAMP,
    updated_at        TIMESTAMP NOT NULL,
    PRIMARY KEY (project_id, repo)
);

CREATE TABLE lado_db_change_runs (
    id                   TEXT PRIMARY KEY CHECK (id <> ''),
    project_id           TEXT NOT NULL CHECK (project_id <> ''),
    repo                 TEXT NOT NULL DEFAULT '',
    execution_session_id TEXT NOT NULL DEFAULT '',
    task_id              TEXT NOT NULL DEFAULT '',
    pr_url               TEXT NOT NULL CHECK (pr_url <> ''),
    head_sha             TEXT NOT NULL CHECK (length(head_sha) IN (40, 64)),
    kind                 TEXT NOT NULL CHECK (kind IN ('pre_merge', 'after_deploy')),
    run_trigger          TEXT NOT NULL CHECK (run_trigger IN ('approval', 'merged_unapplied', 'after_merge')),
    state                TEXT NOT NULL CHECK (state IN ('awaiting_human', 'queued', 'applying', 'failed', 'applied',
        'merging', 'merge_failed', 'done')),
    approval_id          TEXT NOT NULL DEFAULT '',
    attempts             INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    error                TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMP NOT NULL,
    updated_at           TIMESTAMP NOT NULL,
    UNIQUE (pr_url, head_sha, kind)
);
CREATE INDEX lado_db_change_runs_by_state ON lado_db_change_runs (state, updated_at);
CREATE INDEX lado_db_change_runs_by_execution ON lado_db_change_runs (execution_session_id, created_at);
CREATE INDEX lado_db_change_runs_by_project ON lado_db_change_runs (project_id, state);

CREATE TABLE lado_db_change_applications (
    id                   TEXT PRIMARY KEY CHECK (id <> ''),
    project_id           TEXT NOT NULL CHECK (project_id <> ''),
    repo                 TEXT NOT NULL DEFAULT '',
    path                 TEXT NOT NULL CHECK (path <> ''),
    blob_sha             TEXT NOT NULL CHECK (length(blob_sha) IN (40, 64)),
    phase                TEXT NOT NULL CHECK (phase IN ('pre_merge', 'after_deploy')),
    result               TEXT NOT NULL CHECK (result IN ('running', 'applied', 'failed', 'interrupted', 'baseline')),
    run_id               TEXT NOT NULL DEFAULT '',
    execution_session_id TEXT NOT NULL DEFAULT '',
    task_id              TEXT NOT NULL DEFAULT '',
    pr_url               TEXT NOT NULL DEFAULT '',
    head_sha             TEXT NOT NULL DEFAULT '',
    exit_code            INTEGER,
    output_sha256        TEXT NOT NULL DEFAULT '' CHECK (output_sha256 = '' OR length(output_sha256) = 64),
    message              TEXT NOT NULL DEFAULT '',
    actor                TEXT NOT NULL DEFAULT '',
    started_at           TIMESTAMP NOT NULL,
    finished_at          TIMESTAMP
);
CREATE INDEX lado_db_change_applications_by_path ON lado_db_change_applications (project_id, repo, path, started_at);

CREATE TABLE lado_db_change_status_holds (
    approval_id TEXT PRIMARY KEY REFERENCES lado_integration_approvals (id) ON DELETE CASCADE,
    held_at     TIMESTAMP NOT NULL
);

-- +goose Down
-- The tables' triggers go with them.
DROP TABLE lado_db_change_status_holds;
DROP TABLE lado_db_change_applications;
DROP TABLE lado_db_change_runs;
DROP TABLE lado_db_change_settings;
