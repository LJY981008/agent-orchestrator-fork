-- name: ListLadoStageAgents :many
-- One scope's stage settings; the empty project id holds the global defaults.
SELECT project_id, stage, harness, model, effort, updated_at
FROM lado_stage_agents
WHERE project_id = ?
ORDER BY stage;

-- name: DeleteLadoStageAgents :exec
DELETE FROM lado_stage_agents
WHERE project_id = ?;

-- name: InsertLadoStageAgent :exec
INSERT INTO lado_stage_agents (project_id, stage, harness, model, effort, updated_at)
VALUES (?, ?, ?, ?, ?, ?);
