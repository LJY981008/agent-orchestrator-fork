-- LADO fork: per-project LADO settings (docs/LJY-PRD/TDD.md section 4.2).
-- project_id refers to projects.id without a FOREIGN KEY, because upstream
-- rebuilds and deletes its own rows (TDD section 4.2). No row means LADO mode off.

-- +goose Up
CREATE TABLE lado_project_settings (
    project_id TEXT PRIMARY KEY CHECK (project_id <> ''),
    lado_mode  INTEGER NOT NULL CHECK (lado_mode IN (0, 1)),
    updated_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE lado_project_settings;
