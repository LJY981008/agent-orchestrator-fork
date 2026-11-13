-- name: GetLadoProjectSettings :one
SELECT project_id, lado_mode, updated_at, max_concurrent, check_commands, check_timeout_minutes,
    overlap_wait, overlap_ignore_json
FROM lado_project_settings
WHERE project_id = ?;

-- name: UpsertLadoProjectSettings :one
-- A new row stores overlap_wait 0: new projects do not wait on overlap (#101).
-- Every query that creates a row must do the same; the column default stays 1.
INSERT INTO lado_project_settings (project_id, lado_mode, overlap_wait, updated_at)
VALUES (?, ?, 0, ?)
ON CONFLICT (project_id) DO UPDATE SET
    lado_mode = excluded.lado_mode,
    updated_at = excluded.updated_at
RETURNING project_id, lado_mode, updated_at, max_concurrent, check_commands, check_timeout_minutes,
    overlap_wait, overlap_ignore_json;

-- name: GetLadoProjectChecks :one
SELECT check_commands, check_timeout_minutes
FROM lado_project_settings
WHERE project_id = ?;

-- name: UpsertLadoProjectCheckCommands :exec
-- A project without a settings row gets one with LADO mode off and
-- overlap_wait 0 (#101).
INSERT INTO lado_project_settings (project_id, lado_mode, overlap_wait, check_commands, updated_at)
VALUES (?, 0, 0, ?, ?)
ON CONFLICT (project_id) DO UPDATE SET check_commands = excluded.check_commands, updated_at = excluded.updated_at;

-- name: UpsertLadoProjectCheckTimeout :exec
-- A project without a settings row gets one with LADO mode off and
-- overlap_wait 0 (#101).
INSERT INTO lado_project_settings (project_id, lado_mode, overlap_wait, check_timeout_minutes, updated_at)
VALUES (?, 0, 0, ?, ?)
ON CONFLICT (project_id) DO UPDATE SET check_timeout_minutes = excluded.check_timeout_minutes, updated_at = excluded.updated_at;

-- name: UpsertLadoProjectOverlap :exec
-- A project without a settings row gets one with LADO mode off.
INSERT INTO lado_project_settings (project_id, lado_mode, overlap_wait, overlap_ignore_json, updated_at)
VALUES (?, 0, ?, ?, ?)
ON CONFLICT (project_id) DO UPDATE SET
    overlap_wait = excluded.overlap_wait,
    overlap_ignore_json = excluded.overlap_ignore_json,
    updated_at = excluded.updated_at;
