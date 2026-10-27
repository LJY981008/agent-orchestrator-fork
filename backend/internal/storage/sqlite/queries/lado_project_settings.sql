-- name: GetLadoProjectSettings :one
SELECT project_id, lado_mode, updated_at, max_concurrent, check_commands, check_timeout_minutes
FROM lado_project_settings
WHERE project_id = ?;

-- name: UpsertLadoProjectSettings :one
INSERT INTO lado_project_settings (project_id, lado_mode, updated_at)
VALUES (?, ?, ?)
ON CONFLICT (project_id) DO UPDATE SET
    lado_mode = excluded.lado_mode,
    updated_at = excluded.updated_at
RETURNING project_id, lado_mode, updated_at, max_concurrent, check_commands, check_timeout_minutes;
