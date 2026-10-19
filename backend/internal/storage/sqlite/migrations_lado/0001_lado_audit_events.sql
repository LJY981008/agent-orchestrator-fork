-- LADO fork: append-only, hash-chained audit log (docs/LJY-PRD/TDD.md section 4.4).
-- Never pruned, unlike change_log. The store computes prev_sha256.

-- +goose Up
CREATE TABLE lado_audit_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    at           TIMESTAMP NOT NULL,
    actor        TEXT NOT NULL CHECK (actor <> ''),
    action       TEXT NOT NULL CHECK (action <> ''),
    subject_kind TEXT NOT NULL CHECK (subject_kind <> ''),
    subject_id   TEXT NOT NULL,
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    prev_sha256  TEXT NOT NULL CHECK (length(prev_sha256) = 64)
);

-- +goose StatementBegin
CREATE TRIGGER lado_audit_events_no_update
BEFORE UPDATE ON lado_audit_events
BEGIN
    SELECT RAISE(ABORT, 'lado_audit_events is append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER lado_audit_events_no_delete
BEFORE DELETE ON lado_audit_events
BEGIN
    SELECT RAISE(ABORT, 'lado_audit_events is append-only');
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE lado_audit_events;
