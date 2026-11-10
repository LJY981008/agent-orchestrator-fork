package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"testing"
)

// The test file keeps four top-level tests: the sqlite-race CI shards split
// this package's test list by position (knowledge note sqlite-race-shard-shift).

// ladoAccountLimit0012Columns are lado_account_limits' columns before 0018.
var ladoAccountLimit0012Columns = []string{"account_key", "session_id", "reset_at", "reset_guess", "detected_at", "updated_at"}

// insertLadoAccountLimitRow writes the device Claude account's limit with
// 0012's columns alone.
func insertLadoAccountLimitRow(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO lado_account_limits (account_key, session_id, reset_at, reset_guess, detected_at, updated_at)
		VALUES ('claude:device-claude', 's', CURRENT_TIMESTAMP, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
}

// 0018 adds the identity and release columns; a limit recorded before it
// holds on with an unknown identity and no release.
func TestLadoMigrate0018AddsAccountLimitReleaseColumns(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want || got < 18 {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 17); err != nil {
		t.Fatalf("down to 0017: %v", err)
	}
	insertLadoAccountLimitRow(t, db)
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to 0018: %v", err)
	}
	want := append(slices.Clone(ladoAccountLimit0012Columns), "identity", "released_at", "release_reason", "released_waiting")
	if got := tableColumns(t, db, "lado_account_limits"); !slices.Equal(got, want) {
		t.Fatalf("lado_account_limits columns = %v, want %v", got, want)
	}
	var identity, reason, session string
	var released sql.NullTime
	var waiting, guess int
	if err := db.QueryRow(`SELECT identity, released_at, release_reason, released_waiting, session_id, reset_guess
		FROM lado_account_limits WHERE account_key = 'claude:device-claude'`).Scan(&identity, &released, &reason, &waiting, &session, &guess); err != nil {
		t.Fatal(err)
	}
	if identity != "" || released.Valid || reason != "" || waiting != 0 || session != "s" || guess != 1 {
		t.Fatalf("existing limit = identity %q released %v reason %q waits %d session %q guess %d; want it held, unknown, as it was",
			identity, released, reason, waiting, session, guess)
	}
}

// The release columns take each stored reason and nothing else: no unknown
// reason, no NULL identity or reason, no negative waits.
func TestLadoMigrate0018ReleaseChecks(t *testing.T) {
	db := openLadoDatabase(t, 1)
	insertLadoAccountLimitRow(t, db)
	for _, reason := range []string{"reset", "account_changed", "manual", "turn_completed", ""} {
		mustExec(t, db, `UPDATE lado_account_limits SET release_reason = '`+reason+`', released_at = CURRENT_TIMESTAMP, released_waiting = 3, identity = 'sha256:ab'`)
	}
	for name, query := range map[string]string{
		"an unknown reason":    `UPDATE lado_account_limits SET release_reason = 'expired'`,
		"a NULL reason":        `UPDATE lado_account_limits SET release_reason = NULL`,
		"a NULL identity":      `UPDATE lado_account_limits SET identity = NULL`,
		"negative waits":       `UPDATE lado_account_limits SET released_waiting = -1`,
		"NULL waits":           `UPDATE lado_account_limits SET released_waiting = NULL`,
		"a second limit":       `INSERT INTO lado_account_limits (account_key, reset_at, detected_at, updated_at) VALUES ('claude:device-claude', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"an unknown new limit": `INSERT INTO lado_account_limits (account_key, reset_at, detected_at, updated_at, release_reason) VALUES ('codex:x', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'x')`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	// A new limit holds by default.
	mustExec(t, db, `INSERT INTO lado_account_limits (account_key, reset_at, detected_at, updated_at) VALUES ('codex:device-codex', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	var held int
	if err := db.QueryRow(`SELECT COUNT(*) FROM lado_account_limits WHERE account_key = 'codex:device-codex'
		AND identity = '' AND released_at IS NULL AND release_reason = '' AND released_waiting = 0`).Scan(&held); err != nil || held != 1 {
		t.Fatalf("new limit defaults = %d, %v; want a held limit", held, err)
	}
}

// Account limits stay out of change_log: recording, releasing and recording
// again publish nothing.
func TestLadoMigrate0018ReleasePublishesNothing(t *testing.T) {
	db := openLadoDatabase(t, 1)
	var before int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	insertLadoAccountLimitRow(t, db)
	mustExec(t, db, `UPDATE lado_account_limits SET released_at = CURRENT_TIMESTAMP, release_reason = 'manual', released_waiting = 2`)
	mustExec(t, db, `UPDATE lado_account_limits SET released_at = NULL, release_reason = '', released_waiting = 0, identity = 'sha256:cd'`)
	mustExec(t, db, `DELETE FROM lado_account_limits`)
	var after int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("change_log moved from %d to %d, want no change", before, after)
	}
}

// The down migration drops only 0018's columns, keeping the limit and 0017's
// tables, and the round trip adds them again.
func TestLadoMigrate0018RoundTripKeeps0017(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	insertLadoAccountLimitRow(t, db)
	mustExec(t, db, `UPDATE lado_account_limits SET identity = 'sha256:ab', released_at = CURRENT_TIMESTAMP, release_reason = 'manual', released_waiting = 2`)

	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 17); err != nil {
		t.Fatalf("down to 0017: %v", err)
	}
	if got := tableColumns(t, db, "lado_account_limits"); !slices.Equal(got, ladoAccountLimit0012Columns) {
		t.Fatalf("lado_account_limits columns after the down migration = %v, want %v", got, ladoAccountLimit0012Columns)
	}
	for _, table := range []string{"lado_log_watch_settings", "lado_log_fingerprints"} {
		if !tableExists(t, db, table) {
			t.Fatalf("the 0018 down migration dropped 0017's %s", table)
		}
	}
	var session string
	if err := db.QueryRow(`SELECT session_id FROM lado_account_limits WHERE account_key = 'claude:device-claude'`).Scan(&session); err != nil || session != "s" {
		t.Fatalf("limit after the down migration = %q, %v; want it kept", session, err)
	}
	if got := appliedVersion(t, db, ladoVersionTable); got != 17 {
		t.Fatalf("%s after the down migration = %d, want 17", ladoVersionTable, got)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	var identity string
	var released sql.NullTime
	if err := db.QueryRow(`SELECT identity, released_at FROM lado_account_limits WHERE account_key = 'claude:device-claude'`).Scan(&identity, &released); err != nil ||
		identity != "" || released.Valid {
		t.Fatalf("limit after the round trip = identity %q released %v, %v; want it held and unknown", identity, released, err)
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
}
