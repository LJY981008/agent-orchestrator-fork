package sqlite

import (
	"context"
	"slices"
	"testing"
)

// The test file keeps two top-level tests: the sqlite-race CI shards split
// this package's tests by measured time, and every migration test opens a
// whole database (knowledge note sqlite-race-shard-shift).

// ladoStageAgentColumns are lado_stage_agents' columns in order.
var ladoStageAgentColumns = []string{"project_id", "stage", "harness", "model", "effort", "updated_at"}

// 0021 creates lado_stage_agents with its checks and its (project, stage)
// key; the global scope is the empty project id.
func TestLadoMigrate0021StageAgentsTable(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want || got < 21 {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 20); err != nil {
		t.Fatalf("down to 0020: %v", err)
	}
	if tableExists(t, db, "lado_stage_agents") {
		t.Fatal("lado_stage_agents exists before 0021")
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to 0021: %v", err)
	}
	if got := tableColumns(t, db, "lado_stage_agents"); !slices.Equal(got, ladoStageAgentColumns) {
		t.Fatalf("lado_stage_agents columns = %v, want %v", got, ladoStageAgentColumns)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_list('lado_stage_agents')`); got != 0 {
		t.Errorf("lado_stage_agents has %d foreign keys, want none", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND tbl_name = 'lado_stage_agents'`); got != 0 {
		t.Errorf("lado_stage_agents has %d triggers, want none", got)
	}

	for _, stage := range []string{"plan", "critic", "execution", "integration", "resolve"} {
		mustExec(t, db, `INSERT INTO lado_stage_agents (project_id, stage, harness, model, updated_at)
			VALUES ('', ?, 'claude-code', 'opus', CURRENT_TIMESTAMP)`, stage)
	}
	mustExec(t, db, `INSERT INTO lado_stage_agents (project_id, stage, harness, model, effort, updated_at)
		VALUES ('p', 'plan', 'codex', 'gpt-5', 'high', CURRENT_TIMESTAMP)`)
	var effort string
	if err := db.QueryRow(`SELECT effort FROM lado_stage_agents WHERE project_id = '' AND stage = 'plan'`).Scan(&effort); err != nil || effort != "" {
		t.Fatalf("default effort = %q, %v; want empty", effort, err)
	}
	for name, query := range map[string]string{
		"an unknown stage":   `INSERT INTO lado_stage_agents (project_id, stage, harness, model, updated_at) VALUES ('p', 'review', 'codex', 'gpt-5', CURRENT_TIMESTAMP)`,
		"an empty stage":     `INSERT INTO lado_stage_agents (project_id, stage, harness, model, updated_at) VALUES ('p', '', 'codex', 'gpt-5', CURRENT_TIMESTAMP)`,
		"an empty model":     `INSERT INTO lado_stage_agents (project_id, stage, harness, model, updated_at) VALUES ('p', 'critic', 'codex', '', CURRENT_TIMESTAMP)`,
		"an empty harness":   `INSERT INTO lado_stage_agents (project_id, stage, harness, model, updated_at) VALUES ('p', 'critic', '', 'gpt-5', CURRENT_TIMESTAMP)`,
		"a second plan row":  `INSERT INTO lado_stage_agents (project_id, stage, harness, model, updated_at) VALUES ('p', 'plan', 'codex', 'gpt-5', CURRENT_TIMESTAMP)`,
		"a second global":    `INSERT INTO lado_stage_agents (project_id, stage, harness, model, updated_at) VALUES ('', 'resolve', 'codex', 'gpt-5', CURRENT_TIMESTAMP)`,
		"no time":            `INSERT INTO lado_stage_agents (project_id, stage, harness, model) VALUES ('p', 'critic', 'codex', 'gpt-5')`,
		"a null effort":      `INSERT INTO lado_stage_agents (project_id, stage, harness, model, effort, updated_at) VALUES ('p', 'critic', 'codex', 'gpt-5', NULL, CURRENT_TIMESTAMP)`,
		"a model set empty":  `UPDATE lado_stage_agents SET model = '' WHERE project_id = 'p'`,
		"a stage set wrong":  `UPDATE lado_stage_agents SET stage = 'design' WHERE project_id = 'p'`,
		"a null project id":  `INSERT INTO lado_stage_agents (project_id, stage, harness, model, updated_at) VALUES (NULL, 'critic', 'codex', 'gpt-5', CURRENT_TIMESTAMP)`,
		"a harness set null": `UPDATE lado_stage_agents SET harness = NULL WHERE project_id = 'p'`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM lado_stage_agents`); got != 6 {
		t.Fatalf("rows = %d, want 6", got)
	}
}

// Down from 0021 drops only lado_stage_agents and keeps 0020's tables and
// rows; going up again brings the table back empty.
func TestLadoMigrate0021StepsKeep0020(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	insertLadoDBChangeFixture(t, db)
	mustExec(t, db, `INSERT INTO lado_stage_agents (project_id, stage, harness, model, updated_at)
		VALUES ('p', 'execution', 'claude-code', 'opus', CURRENT_TIMESTAMP)`)

	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 21); err != nil {
		t.Fatalf("down to 0021: %v", err)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("down from 0021: %v", err)
	}
	if got := appliedVersion(t, db, ladoVersionTable); got != 20 {
		t.Fatalf("%s after the down migration = %d, want 20", ladoVersionTable, got)
	}
	if tableExists(t, db, "lado_stage_agents") {
		t.Fatal("lado_stage_agents survived the 0021 down migration")
	}
	for _, table := range ladoDBChangeTables {
		if got := tableColumns(t, db, table.name); !slices.Equal(got, table.columns) {
			t.Fatalf("%s after the 0021 down migration = %v, want %v", table.name, got, table.columns)
		}
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM lado_db_change_settings WHERE project_id = 'p'`); got != 1 {
		t.Fatalf("0020 settings rows after the down migration = %d, want 1", got)
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("up to 0021: %v", err)
	}
	if got := appliedVersion(t, db, ladoVersionTable); got != 21 {
		t.Fatalf("%s after the up migration = %d, want 21", ladoVersionTable, got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM lado_stage_agents`); got != 0 {
		t.Fatalf("lado_stage_agents after the round trip has %d rows, want 0", got)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to the latest: %v", err)
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
}
