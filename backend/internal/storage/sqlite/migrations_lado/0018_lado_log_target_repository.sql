-- LADO fork: per-target issue repository for the log watch (issue #91).
--
-- lado_log_targets.repository is the canonical id of the repository a
-- target's issues go to. Empty inherits the project's
-- lado_log_watch_settings.repository, so the targets that were there keep
-- going to the project's repository.

-- +goose Up
ALTER TABLE lado_log_targets ADD COLUMN repository TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE lado_log_targets DROP COLUMN repository;
