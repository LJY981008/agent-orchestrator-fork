-- LADO fork: account limit release (issue #92).
--
-- lado_account_limits keeps a row when LADO releases it, so LADO knows the
-- limit was let go before its reset and can resume the workers it stopped.
-- identity is the fingerprint ("sha256:<hex>") of the account the key was
-- logged in to when LADO recorded the limit, '' when unknown; it is never an
-- email. released_at is NULL while the limit holds; release_reason says why
-- it was released and released_waiting how many account_limited waits there
-- were then. Existing rows hold with an unknown identity.

-- +goose Up
ALTER TABLE lado_account_limits ADD COLUMN identity TEXT NOT NULL DEFAULT '';
ALTER TABLE lado_account_limits ADD COLUMN released_at TIMESTAMP;
ALTER TABLE lado_account_limits ADD COLUMN release_reason TEXT NOT NULL DEFAULT ''
    CHECK (release_reason IN ('', 'reset', 'account_changed', 'manual', 'turn_completed'));
ALTER TABLE lado_account_limits ADD COLUMN released_waiting INTEGER NOT NULL DEFAULT 0 CHECK (released_waiting >= 0);

-- +goose Down
ALTER TABLE lado_account_limits DROP COLUMN released_waiting;
ALTER TABLE lado_account_limits DROP COLUMN release_reason;
ALTER TABLE lado_account_limits DROP COLUMN released_at;
ALTER TABLE lado_account_limits DROP COLUMN identity;
