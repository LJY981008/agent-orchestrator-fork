-- name: InsertLadoTask :one
INSERT INTO lado_tasks (
    id, project_id, issue_repo, issue_ref, agent, model,
    issue_title, issue_state, issue_url, issue_snapshot_sha256, issue_observed_at,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, project_id, issue_repo, issue_ref, agent, model,
    issue_title, issue_state, issue_url, issue_snapshot_sha256, issue_observed_at,
    created_at, updated_at, cancelled_at;

-- name: GetLadoTask :one
SELECT id, project_id, issue_repo, issue_ref, agent, model,
    issue_title, issue_state, issue_url, issue_snapshot_sha256, issue_observed_at,
    created_at, updated_at, cancelled_at
FROM lado_tasks
WHERE id = ?;

-- name: ListLadoTasksByProject :many
SELECT id, project_id, issue_repo, issue_ref, agent, model,
    issue_title, issue_state, issue_url, issue_snapshot_sha256, issue_observed_at,
    created_at, updated_at, cancelled_at
FROM lado_tasks
WHERE project_id = ? AND issue_ref IS NOT NULL
ORDER BY created_at, id;

-- name: ListPendingLadoTasks :many
SELECT id, project_id, issue_repo, issue_ref, agent, model,
    issue_title, issue_state, issue_url, issue_snapshot_sha256, issue_observed_at,
    created_at, updated_at, cancelled_at
FROM lado_tasks
WHERE issue_ref IS NULL
ORDER BY created_at, id;

-- name: ListLadoTasksToObserve :many
SELECT id, project_id, issue_repo, issue_ref, agent, model,
    issue_title, issue_state, issue_url, issue_snapshot_sha256, issue_observed_at,
    created_at, updated_at, cancelled_at
FROM lado_tasks
WHERE issue_ref IS NOT NULL AND cancelled_at IS NULL
ORDER BY issue_observed_at, id;

-- name: ListLiveLadoTasks :many
SELECT id, project_id, issue_repo, issue_ref, agent, model,
    issue_title, issue_state, issue_url, issue_snapshot_sha256, issue_observed_at,
    created_at, updated_at, cancelled_at
FROM lado_tasks
WHERE issue_ref IS NOT NULL AND cancelled_at IS NULL
ORDER BY project_id, created_at, id;

-- name: ListLiveLadoTaskIssueRefs :many
SELECT issue_ref
FROM lado_tasks
WHERE issue_ref IS NOT NULL AND cancelled_at IS NULL;

-- name: LinkLadoTaskIssue :one
UPDATE lado_tasks
SET issue_ref = ?,
    issue_title = ?,
    issue_state = ?,
    issue_url = ?,
    issue_snapshot_sha256 = ?,
    issue_observed_at = ?,
    updated_at = ?
WHERE id = ? AND issue_ref IS NULL
RETURNING id, project_id, issue_repo, issue_ref, agent, model,
    issue_title, issue_state, issue_url, issue_snapshot_sha256, issue_observed_at,
    created_at, updated_at, cancelled_at;

-- name: ObserveLadoTaskIssue :execrows
UPDATE lado_tasks
SET issue_title = ?,
    issue_state = ?,
    issue_url = ?,
    issue_snapshot_sha256 = ?,
    issue_observed_at = ?,
    updated_at = ?
WHERE id = ? AND issue_ref IS NOT NULL;

-- name: SetLadoTaskPreferences :one
UPDATE lado_tasks
SET agent = ?,
    model = ?,
    updated_at = ?
WHERE id = ? AND issue_ref IS NOT NULL
RETURNING id, project_id, issue_repo, issue_ref, agent, model,
    issue_title, issue_state, issue_url, issue_snapshot_sha256, issue_observed_at,
    created_at, updated_at, cancelled_at;

-- name: CancelLadoTask :execrows
UPDATE lado_tasks
SET cancelled_at = ?,
    updated_at = ?
WHERE id = ? AND issue_ref IS NOT NULL AND cancelled_at IS NULL;

-- name: DeletePendingLadoTask :execrows
DELETE FROM lado_tasks
WHERE id = ? AND issue_ref IS NULL;
