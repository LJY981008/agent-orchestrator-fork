-- name: InsertLadoSetupAdoption :exec
INSERT INTO lado_setup_adoptions (
    id, created_at, actor, os, plan_sha256, manifest_sha256, state, error_message
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: CompleteLadoSetupAdoption :execrows
-- Compare-and-set: a pending adoption ends once, completed or partial.
UPDATE lado_setup_adoptions
SET state = ?, error_message = ?
WHERE id = ? AND state = 'pending';

-- name: DeletePendingLadoSetupAdoption :execrows
-- A pending adoption that removed nothing leaves no row.
DELETE FROM lado_setup_adoptions
WHERE id = ? AND state = 'pending';

-- name: MarkPendingLadoSetupAdoptionsInterrupted :execrows
-- The adoptions a daemon that stopped left pending. Restore stays available.
UPDATE lado_setup_adoptions
SET state = 'partial', error_message = ?
WHERE state = 'pending';

-- name: CountLadoPresenceProofsByNonce :one
-- Whether a presence proof was used, read before an adoption rescans.
SELECT COUNT(*) FROM lado_presence_proofs
WHERE nonce = ?;

-- name: MarkLadoSetupAdoptionPartial :execrows
-- An adoption that stopped while removing the originals. The items it
-- recorded stay live, and restore stays available.
UPDATE lado_setup_adoptions
SET state = 'partial', error_message = ?
WHERE id = ?;

-- name: ListLadoSetupAdoptions :many
SELECT id, created_at, actor, os, plan_sha256, manifest_sha256, state, error_message,
    restored_at, restore_report
FROM lado_setup_adoptions
ORDER BY created_at, id;

-- name: GetLadoSetupAdoption :one
SELECT id, created_at, actor, os, plan_sha256, manifest_sha256, state, error_message,
    restored_at, restore_report
FROM lado_setup_adoptions
WHERE id = ?;

-- name: MarkLadoSetupAdoptionRestored :execrows
-- Compare-and-set: an adoption is restored once.
UPDATE lado_setup_adoptions
SET restored_at = ?, restore_report = ?
WHERE id = ? AND restored_at IS NULL;

-- name: MarkLadoSetupItemsRestoredForAdoption :execrows
-- Every item the adoption recorded, deleted ones included, so none of them
-- is delivered again. Their versions stay.
UPDATE lado_setup_items
SET restored_at = ?
WHERE adoption_id = ? AND restored_at IS NULL;

-- name: InsertLadoSetupItem :exec
INSERT INTO lado_setup_items (id, tool, kind, name, origin_path, adoption_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetLadoSetupItem :one
SELECT id, tool, kind, name, origin_path, adoption_id, created_at, deleted_at, restored_at
FROM lado_setup_items
WHERE id = ?;

-- name: ListLadoSetupItemsForAdoption :many
-- Every item an adoption recorded, deleted and restored ones included:
-- restore puts back the originals of all of them.
SELECT id, tool, kind, name, origin_path, adoption_id, created_at, deleted_at, restored_at
FROM lado_setup_items
WHERE adoption_id = ?
ORDER BY tool, kind, name, id;

-- name: GetLiveLadoSetupItemByName :one
-- The live item lado_setup_items_live allows for a tool, kind and name.
SELECT id, tool, kind, name, origin_path, adoption_id, created_at, deleted_at, restored_at
FROM lado_setup_items
WHERE tool = ? AND kind = ? AND name = ? AND deleted_at IS NULL AND restored_at IS NULL;

-- name: SoftDeleteLadoSetupItem :execrows
-- Only a live item is deleted. Its versions and bindings stay.
UPDATE lado_setup_items
SET deleted_at = ?
WHERE id = ? AND deleted_at IS NULL AND restored_at IS NULL;

-- name: InsertLadoSetupItemVersion :exec
INSERT INTO lado_setup_item_versions (
    id, item_id, version, content_kind, sha256, size_bytes, executable, actor, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: LatestLadoSetupItemVersion :one
SELECT id, item_id, version, content_kind, sha256, size_bytes, executable, actor, created_at
FROM lado_setup_item_versions
WHERE item_id = ?
ORDER BY version DESC
LIMIT 1;

-- name: ListLiveLadoSetupItemsWithLatestVersion :many
-- Each live item with its latest version, in snapshot order.
SELECT sqlc.embed(i), sqlc.embed(v)
FROM lado_setup_items i
JOIN lado_setup_item_versions v ON v.item_id = i.id
WHERE i.deleted_at IS NULL AND i.restored_at IS NULL
  AND v.version = (SELECT MAX(v2.version) FROM lado_setup_item_versions v2 WHERE v2.item_id = i.id)
ORDER BY i.tool, i.kind, i.name;

-- name: ListLadoSetupItemVersions :many
SELECT id, item_id, version, content_kind, sha256, size_bytes, executable, actor, created_at
FROM lado_setup_item_versions
WHERE item_id = ?
ORDER BY version;

-- name: GetLadoSetupItemVersion :one
SELECT id, item_id, version, content_kind, sha256, size_bytes, executable, actor, created_at
FROM lado_setup_item_versions
WHERE id = ?;

-- name: CountLiveLadoSetupItemVersion :one
-- Whether a version exists and its item is still live, for the pins of a
-- snapshot in the approval transaction.
SELECT COUNT(*)
FROM lado_setup_item_versions v
JOIN lado_setup_items i ON i.id = v.item_id
WHERE v.id = ? AND i.deleted_at IS NULL AND i.restored_at IS NULL;

-- name: UpsertLadoSetupBinding :exec
INSERT INTO lado_setup_bindings (item_id, scope, scope_id, state, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (item_id, scope, scope_id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at;

-- name: DeleteLadoSetupBinding :execrows
DELETE FROM lado_setup_bindings
WHERE item_id = ? AND scope = ? AND scope_id = ?;

-- name: ListLadoSetupGlobalBindings :many
SELECT item_id, scope, scope_id, state, updated_at
FROM lado_setup_bindings
WHERE scope = 'global'
ORDER BY item_id;

-- name: ListLadoSetupBindingsForScope :many
SELECT item_id, scope, scope_id, state, updated_at
FROM lado_setup_bindings
WHERE scope = ? AND scope_id = ?
ORDER BY item_id;

-- name: InsertLadoConflictAcceptance :exec
INSERT INTO lado_conflict_acceptances (
    id, code, item_id, detail, content_sha256, scope, scope_id, actor, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: RevokeLadoConflictAcceptance :execrows
-- Compare-and-set: an acceptance is revoked once.
UPDATE lado_conflict_acceptances
SET revoked_at = ?
WHERE id = ? AND revoked_at IS NULL;

-- name: ListLiveLadoConflictAcceptances :many
SELECT id, code, item_id, detail, content_sha256, scope, scope_id, actor, created_at, revoked_at
FROM lado_conflict_acceptances
WHERE revoked_at IS NULL
ORDER BY created_at, id;

-- name: InsertLadoPolicySnapshotSetup :exec
INSERT INTO lado_policy_snapshot_setup (snapshot_id, item_version_id)
VALUES (?, ?);

-- name: ListLadoPolicySnapshotSetupVersions :many
-- The item versions a snapshot pinned, with their items, in snapshot order.
SELECT sqlc.embed(i), sqlc.embed(v)
FROM lado_policy_snapshot_setup p
JOIN lado_setup_item_versions v ON v.id = p.item_version_id
JOIN lado_setup_items i ON i.id = v.item_id
WHERE p.snapshot_id = ?
ORDER BY i.tool, i.kind, i.name, i.id;

-- name: GetLadoPolicySnapshotCreatedAt :one
-- When a snapshot was taken: the time its setup pins were taken.
SELECT created_at FROM lado_policy_snapshots
WHERE id = ?;

-- name: CountLadoPolicySnapshotSetupForItem :one
-- How many snapshot pins name a version of the item.
SELECT COUNT(*)
FROM lado_policy_snapshot_setup p
JOIN lado_setup_item_versions v ON v.id = p.item_version_id
WHERE v.item_id = ?;
