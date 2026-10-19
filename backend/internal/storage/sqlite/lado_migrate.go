package sqlite

// LADO fork: the LADO migration track (docs/LJY-PRD/TDD.md §4.1 and
// docs/LJY-PRD/adr/0004-data-same-db-separate-migration-track.md). LADO tables
// share the database, but their migrations never enter upstream's
// goose_db_version: they run from their own directory and version table, after
// every upstream migration and repair, so fresh and upgraded databases apply
// the two tracks in the same order.

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations_lado/*.sql
var ladoMigrationsFS embed.FS

// ladoVersionTable is the LADO track's goose ledger.
const ladoVersionTable = "lado_goose_db_version"

// ladoCDCTriggers holds the CREATE TRIGGER statements, each named lado_cdc_*,
// that write LADO rows into change_log (TDD §4.3). Upstream rebuilds
// change_log and other tables by copy and rename, dropping only the triggers it
// knows, and a trigger it does not know fails that rename. So these are dropped
// before upstream runs and recreated after the LADO track on every open, and
// LADO migrations never create lado_cdc_* triggers themselves. Empty until a
// LADO table publishes change events.
var ladoCDCTriggers []string

// dropLadoCDCTriggers removes every lado_cdc_* trigger ahead of upstream.
func dropLadoCDCTriggers(db *sql.DB) error {
	// Collect the names first: the writer pool has one connection, and open
	// rows would hold it while the drops wait for it.
	names, err := ladoCDCTriggerNames(db)
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + quoteSQLiteIdent(name)); err != nil {
			return fmt.Errorf("drop %s: %w", name, err)
		}
	}
	return nil
}

func ladoCDCTriggerNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name GLOB 'lado_cdc_*'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// migrateLado applies the LADO track, then recreates the LADO change triggers.
func migrateLado(db *sql.DB) error {
	migrations, err := fs.Sub(ladoMigrationsFS, "migrations_lado")
	if err != nil {
		return fmt.Errorf("open LADO migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations,
		goose.WithTableName(ladoVersionTable),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("prepare LADO migrations: %w", err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		return fmt.Errorf("run LADO migrations: %w", err)
	}
	for _, statement := range ladoCDCTriggers {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("create LADO change trigger: %w", err)
		}
	}
	return nil
}
