-- LADO fork: production database changes tied to the integration approval
-- (issue #97). Keep this file ASCII: sqlc mangles queries with other bytes.

-- name: ListLadoDBChangeSettings :many
SELECT * FROM lado_db_change_settings
WHERE project_id = sqlc.arg(project_id)
ORDER BY repo;

-- name: GetLadoDBChangeSetting :one
SELECT * FROM lado_db_change_settings
WHERE project_id = sqlc.arg(project_id) AND repo = sqlc.arg(repo);

-- name: UpsertLadoDBChangeSetting :exec
INSERT INTO lado_db_change_settings (
    project_id, repo, enabled, paths_json, command, timeout_minutes, secret_names_json,
    baseline_commit, enabled_at, updated_at
) VALUES (
    sqlc.arg(project_id), sqlc.arg(repo), sqlc.arg(enabled), sqlc.arg(paths_json), sqlc.arg(command),
    sqlc.arg(timeout_minutes), sqlc.arg(secret_names_json), sqlc.arg(baseline_commit),
    sqlc.narg(enabled_at), sqlc.arg(updated_at)
)
ON CONFLICT (project_id, repo) DO UPDATE SET
    enabled = excluded.enabled,
    paths_json = excluded.paths_json,
    command = excluded.command,
    timeout_minutes = excluded.timeout_minutes,
    secret_names_json = excluded.secret_names_json,
    baseline_commit = excluded.baseline_commit,
    enabled_at = excluded.enabled_at,
    updated_at = excluded.updated_at;

-- name: InsertLadoDBChangeApplication :exec
INSERT INTO lado_db_change_applications (
    id, project_id, repo, path, blob_sha, phase, result, run_id, execution_session_id, task_id,
    pr_url, head_sha, exit_code, output_sha256, message, actor, started_at, finished_at
) VALUES (
    sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(repo), sqlc.arg(path), sqlc.arg(blob_sha), sqlc.arg(phase),
    sqlc.arg(result), sqlc.arg(run_id), sqlc.arg(execution_session_id), sqlc.arg(task_id), sqlc.arg(pr_url),
    sqlc.arg(head_sha), sqlc.narg(exit_code), sqlc.arg(output_sha256), sqlc.arg(message), sqlc.arg(actor),
    sqlc.arg(started_at), sqlc.narg(finished_at)
);

-- name: GetLadoDBChangeApplication :one
SELECT * FROM lado_db_change_applications WHERE id = sqlc.arg(id);

-- name: FinishLadoDBChangeApplication :execrows
-- Ends a running application. Rows that are no longer running stay.
UPDATE lado_db_change_applications
SET result = sqlc.arg(result),
    exit_code = sqlc.narg(exit_code),
    output_sha256 = sqlc.arg(output_sha256),
    message = sqlc.arg(message),
    finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id) AND result = 'running';

-- name: InterruptLadoDBChangeApplications :execrows
-- Marks the applications a stopped daemon left running as interrupted.
UPDATE lado_db_change_applications
SET result = 'interrupted', finished_at = sqlc.arg(finished_at)
WHERE result = 'running';

-- name: ListLadoDBChangeApplications :many
-- A repository's applications and baseline rows, oldest first.
SELECT * FROM lado_db_change_applications
WHERE project_id = sqlc.arg(project_id) AND repo = sqlc.arg(repo)
ORDER BY started_at, id;

-- name: ListLadoDBChangeApplicationsByPath :many
-- A repository's applications and baseline rows of the given paths, oldest
-- first.
SELECT * FROM lado_db_change_applications
WHERE project_id = sqlc.arg(project_id) AND repo = sqlc.arg(repo)
  AND path IN (sqlc.slice('paths'))
ORDER BY started_at, id;

-- name: InsertLadoDBChangeRun :execrows
-- Creates a run unless its PR, head and kind have one already.
INSERT INTO lado_db_change_runs (
    id, project_id, repo, execution_session_id, task_id, pr_url, head_sha, kind, run_trigger, state,
    approval_id, attempts, error, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(repo), sqlc.arg(execution_session_id), sqlc.arg(task_id),
    sqlc.arg(pr_url), sqlc.arg(head_sha), sqlc.arg(kind), sqlc.arg(run_trigger), sqlc.arg(state),
    sqlc.arg(approval_id), sqlc.arg(attempts), sqlc.arg(error), sqlc.arg(created_at), sqlc.arg(updated_at)
)
ON CONFLICT (pr_url, head_sha, kind) DO NOTHING;

-- name: GetLadoDBChangeRun :one
SELECT * FROM lado_db_change_runs WHERE id = sqlc.arg(id);

-- name: GetLadoDBChangeRunByHead :one
SELECT * FROM lado_db_change_runs
WHERE pr_url = sqlc.arg(pr_url) AND head_sha = sqlc.arg(head_sha) AND kind = sqlc.arg(kind);

-- name: TransitionLadoDBChangeRun :execrows
-- Moves a run that is in one of the given states.
UPDATE lado_db_change_runs
SET state = sqlc.arg(to_state),
    attempts = sqlc.arg(attempts),
    error = sqlc.arg(error),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND state IN (sqlc.slice('from_states'));

-- name: SetLadoDBChangeRun :execrows
-- Moves a run whatever its state.
UPDATE lado_db_change_runs
SET state = sqlc.arg(to_state),
    attempts = sqlc.arg(attempts),
    error = sqlc.arg(error),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: ListLadoDBChangeRunsByState :many
-- Runs in the given states, least recently moved first. The caller cuts the
-- list: sqlc numbers a parameter after a slice wrongly.
SELECT * FROM lado_db_change_runs
WHERE state IN (sqlc.slice('states'))
ORDER BY updated_at, id;

-- name: ListLadoDBChangeRunsForExecution :many
SELECT * FROM lado_db_change_runs
WHERE execution_session_id = sqlc.arg(execution_session_id)
ORDER BY created_at, id;

-- name: ListLadoDBChangeRunsForProject :many
-- A project's runs that are not done, oldest first.
SELECT * FROM lado_db_change_runs
WHERE project_id = sqlc.arg(project_id) AND state <> 'done'
ORDER BY created_at, id;

-- name: HoldLadoIntegrationStatus :exec
-- Keeps the first time an approval's status was held.
INSERT INTO lado_db_change_status_holds (approval_id, held_at)
VALUES (sqlc.arg(approval_id), sqlc.arg(held_at))
ON CONFLICT (approval_id) DO NOTHING;

-- name: ReleaseLadoIntegrationStatus :exec
DELETE FROM lado_db_change_status_holds WHERE approval_id = sqlc.arg(approval_id);

-- name: CountLadoIntegrationStatusHolds :one
SELECT COUNT(*) FROM lado_db_change_status_holds WHERE approval_id = sqlc.arg(approval_id);
