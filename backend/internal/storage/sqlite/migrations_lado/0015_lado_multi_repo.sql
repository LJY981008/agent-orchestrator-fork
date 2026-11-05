-- LADO fork: one task changing several repositories of a workspace (issue
-- #64, docs/LJY-PRD/TDD.md sections 5.4, 7 and 8).
--
-- lado_execution_prs pins the PRs LADO tracks for an execution: one row per
-- PR, at most one live row (replaced_at NULL) per repository. repo_key is the
-- repository's slash-separated folder under the workspace's, '' for a
-- single-repository project. position is the PR's place in the merge order.
-- A closed PR the worker replaced with a newer one in the same repository
-- keeps its row with replaced_at. settled_at and settled_pr_state say when
-- verification settled the PR merged or closed; lado_executions.settled_*
-- take the last tracked PR's values once every live row is settled. An
-- execution without rows tracks its session's newest PR, as before.
--
-- lado_part_run_repos holds a parts run's integration branch in each
-- repository it runs in, and lado_task_part_repos how each part's merge went
-- there. Position 0 repeats lado_part_runs' and lado_task_parts' own
-- columns; a run started before 0015 has no rows and reads as that one
-- repository. lado_task_parts.merge_repo names the repository whose merge
-- conflicted.
--
-- lado_executions gains the repositories the execution is expected to open a
-- PR in, in merge order (pr_repos_json), and whether that list is a fact
-- (pr_repos_firm: an integration execution's changed repositories) rather
-- than the plan's declaration. lado_pr_resolves and lado_execution_reviews
-- name the PR they are for; '' is a row written before 0015, which counts for
-- every PR of its execution.
--
-- Existing tables are not rebuilt and existing rows are not changed. The
-- change triggers live in lado_migrate.go, never here.

-- +goose Up
CREATE TABLE lado_execution_prs (
    execution_session_id TEXT NOT NULL REFERENCES lado_executions (session_id),
    pr_url               TEXT NOT NULL CHECK (pr_url <> ''),
    repo_key             TEXT NOT NULL DEFAULT '',
    position             INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
    tracked_at           TIMESTAMP NOT NULL,
    replaced_at          TIMESTAMP,
    settled_at           TIMESTAMP,
    settled_pr_state     TEXT NOT NULL DEFAULT '' CHECK (settled_pr_state IN ('', 'merged', 'closed')),
    PRIMARY KEY (execution_session_id, pr_url),
    CHECK ((settled_at IS NULL) = (settled_pr_state = ''))
);
CREATE UNIQUE INDEX lado_execution_prs_one_per_repo ON lado_execution_prs (execution_session_id, repo_key)
    WHERE replaced_at IS NULL;
CREATE INDEX lado_execution_prs_by_url ON lado_execution_prs (pr_url);

CREATE TABLE lado_part_run_repos (
    queue_entry_id          TEXT NOT NULL REFERENCES lado_part_runs (queue_entry_id),
    relative_path           TEXT NOT NULL,
    position                INTEGER NOT NULL CHECK (position >= 0),
    repo_dir                TEXT NOT NULL CHECK (repo_dir <> ''),
    base_sha                TEXT NOT NULL CHECK (base_sha <> ''),
    head_sha                TEXT NOT NULL CHECK (head_sha <> ''),
    base_branch             TEXT NOT NULL DEFAULT '',
    base_remote             TEXT NOT NULL DEFAULT '',
    dispatch_local_base_sha TEXT NOT NULL DEFAULT '',
    base_commits_ahead      INTEGER,
    PRIMARY KEY (queue_entry_id, relative_path)
);
CREATE UNIQUE INDEX lado_part_run_repos_by_position ON lado_part_run_repos (queue_entry_id, position);

CREATE TABLE lado_task_part_repos (
    queue_entry_id TEXT NOT NULL,
    part_id        TEXT NOT NULL,
    relative_path  TEXT NOT NULL,
    start_sha      TEXT NOT NULL DEFAULT '',
    merge_from_sha TEXT NOT NULL DEFAULT '',
    merge_tip_sha  TEXT NOT NULL DEFAULT '',
    merged_sha     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (queue_entry_id, part_id, relative_path),
    FOREIGN KEY (queue_entry_id, part_id) REFERENCES lado_task_parts (queue_entry_id, part_id),
    FOREIGN KEY (queue_entry_id, relative_path) REFERENCES lado_part_run_repos (queue_entry_id, relative_path)
);

ALTER TABLE lado_executions ADD COLUMN pr_repos_json TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(pr_repos_json) AND json_type(pr_repos_json, '$') = 'array');
ALTER TABLE lado_executions ADD COLUMN pr_repos_firm INTEGER NOT NULL DEFAULT 0 CHECK (pr_repos_firm IN (0, 1));
ALTER TABLE lado_task_parts ADD COLUMN merge_repo TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_pr_resolves ADD COLUMN pr_url TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_execution_reviews ADD COLUMN pr_url TEXT NOT NULL DEFAULT '';

-- +goose Down
-- The parts' update trigger reads merge_repo; the next open creates it again.
-- The triggers of the dropped tables go with them.
DROP TRIGGER IF EXISTS lado_cdc_task_parts_update;
ALTER TABLE lado_execution_reviews DROP COLUMN pr_url;
ALTER TABLE lado_pr_resolves DROP COLUMN pr_url;
ALTER TABLE lado_task_parts DROP COLUMN merge_repo;
ALTER TABLE lado_executions DROP COLUMN pr_repos_firm;
ALTER TABLE lado_executions DROP COLUMN pr_repos_json;
DROP TABLE lado_task_part_repos;
DROP INDEX lado_part_run_repos_by_position;
DROP TABLE lado_part_run_repos;
DROP INDEX lado_execution_prs_by_url;
DROP INDEX lado_execution_prs_one_per_repo;
DROP TABLE lado_execution_prs;
