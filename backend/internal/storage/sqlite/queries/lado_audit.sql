-- name: GetLatestLadoAuditEvent :one
SELECT id, at, actor, action, subject_kind, subject_id, payload_json, prev_sha256
FROM lado_audit_events
ORDER BY id DESC
LIMIT 1;

-- name: InsertLadoAuditEvent :one
INSERT INTO lado_audit_events (
    at, actor, action, subject_kind, subject_id, payload_json, prev_sha256
) VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id, at, actor, action, subject_kind, subject_id, payload_json, prev_sha256;
