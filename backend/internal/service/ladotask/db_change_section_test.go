package ladotask

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoknowledge"
)

// fakeDBChanges answers LadoDBChangeSettings like the store.
type fakeDBChanges struct {
	settings []domain.LadoDBChangeSettings
	err      error
	asked    []domain.ProjectID
}

func (f *fakeDBChanges) LadoDBChangeSettings(_ context.Context, projectID domain.ProjectID) ([]domain.LadoDBChangeSettings, error) {
	f.asked = append(f.asked, projectID)
	return f.settings, f.err
}

// dbChangeSettingsOn are a workspace's settings: back turned on, front off.
func dbChangeSettingsOn() []domain.LadoDBChangeSettings {
	return []domain.LadoDBChangeSettings{
		{ProjectID: "demo", Repo: "back", Enabled: true, Paths: []string{"scripts/migrations/*.sql", "db/**/*.sql"}, Command: "psql -f {file}"},
		{ProjectID: "demo", Repo: "front", Enabled: false, Paths: []string{"sql/*.sql"}, Command: "psql -f {file}"},
	}
}

// Every role of a project with settings turned on gets the section: the
// orchestrator neither asks about nor applies the SQL, the worker adds new
// files and applies none, the designer plans the files with db_schema and the
// critic checks that. Only repositories turned on are listed.
func TestDBChangeSectionReachesEveryLadoRole(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	orchestrator, worker, designer, critic := knowledgeSessions(t, pf)
	knowledge := &fakeKnowledge{index: ladoknowledge.Index{ProjectID: "demo", Dir: "/state/lado/knowledge/demo"}}
	source := PromptSource{Settings: pf.store, Sessions: pf.store, Policies: pf.store, Knowledge: knowledge, DBChanges: &fakeDBChanges{settings: dbChangeSettingsOn()}}

	common := []string{
		"## Production database changes (LADO)",
		"the developer's integration approval in the AO desktop app signs those files",
		"applies its pre-merge SQL files one at a time in byte order of their paths, and merges only when every file applied",
		"A file whose first line is `-- lado:after-deploy` waits until after the deploy",
		"- repository `back`: `scripts/migrations/*.sql`, `db/**/*.sql`\n",
	}
	for _, tc := range []struct {
		name string
		kind domain.SessionKind
		id   domain.SessionID
		role string
		want []string
	}{
		{"orchestrator", domain.KindOrchestrator, orchestrator, "## LADO project", []string{
			"do not ask the developer whether to apply them to the production database, and do not apply them yourself",
			"the integration approval applies the SQL and merges the PR/MR together",
			"a file applied outside LADO is not recorded and would run again",
			"a PR/MR merged outside LADO with its SQL not applied",
		}},
		{"worker", domain.KindWorker, worker, "", []string{
			"Add each database change as a new SQL file under these paths. Do not apply it to any production database and do not ask anyone to",
			"Never edit or delete a SQL file that is already on the base branch",
			"with the line `-- lado:after-deploy` as its first line",
		}},
		{"designer", domain.KindWorker, designer, "## LADO plan worker", []string{
			"plan the SQL as new files under these paths: name them in affected_paths and add db_schema to change_kinds",
			"Plan no step that applies SQL to a production database",
			"whose first line is `-- lado:after-deploy`",
		}},
		{"critic", domain.KindWorker, critic, "## LADO plan critic", []string{
			"names its SQL files under these paths in affected_paths and db_schema in change_kinds",
			"Flag a step that applies SQL to a production database",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			standing, err := source.Standing(ctx, tc.kind, "demo", tc.id, domain.HarnessClaudeCode)
			if err != nil {
				t.Fatalf("Standing: %v", err)
			}
			_, section, ok := strings.Cut(standing.Text, dbChangeHeading)
			if !ok {
				t.Fatalf("no database changes section:\n%s", standing.Text)
			}
			section, _, _ = strings.Cut(section, "\n## ")
			for _, want := range append(append([]string{}, common...), tc.want...) {
				if !strings.Contains(dbChangeHeading+section, want) {
					t.Fatalf("section lacks %q:\n%s", want, section)
				}
			}
			if strings.Contains(section, "front") || strings.Contains(section, "sql/*.sql") {
				t.Fatalf("a repository turned off is listed:\n%s", section)
			}
			if strings.Contains(section, "psql") {
				t.Fatalf("the section shows the command:\n%s", section)
			}
			db := strings.Index(standing.Text, dbChangeHeading)
			know := strings.Index(standing.Text, "## Project knowledge (LADO)")
			// The critic, with no notes to read, gets no knowledge section.
			if (know >= 0 && know < db) || (tc.name != "critic" && know < 0) || (tc.role != "" && strings.Index(standing.Text, tc.role) > db) {
				t.Fatalf("the section is out of order:\n%s", standing.Text)
			}
		})
	}
	// The other roles' bullets stay out of each role's section.
	if got := renderDBChangeSection(dbChangeWorker, dbChangeSettingsOn()[:1]); strings.Contains(got, "do not ask the developer whether") || strings.Contains(got, "change_kinds") {
		t.Fatalf("the worker's section holds another role's bullets:\n%s", got)
	}
	// A single-repository project names its repository, and a glob with a
	// backtick cannot end the code.
	single := renderDBChangeSection(dbChangeWorker, []domain.LadoDBChangeSettings{{Enabled: true, Paths: []string{"db/`x`.sql"}}})
	if !strings.Contains(single, "- the project's repository: \"db/`x`.sql\"\n") {
		t.Fatalf("single-repository section:\n%s", single)
	}
}

// A project with no repository turned on keeps its instructions byte for
// byte; so does a source without settings, a project outside LADO mode and a
// session that is no orchestrator or worker. A failed read is logged and
// leaves only the section out.
func TestDBChangeSectionLeftOut(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	orchestrator, worker, designer, _ := knowledgeSessions(t, pf)
	base := PromptSource{Settings: pf.store, Sessions: pf.store, Policies: pf.store}
	standing := func(t *testing.T, source PromptSource, kind domain.SessionKind, project domain.ProjectID, id domain.SessionID) string {
		t.Helper()
		got, err := source.Standing(ctx, kind, project, id, domain.HarnessClaudeCode)
		if err != nil {
			t.Fatalf("Standing: %v", err)
		}
		return got.Text
	}
	// The orchestrator's instructions without the section are the LADO
	// section as it was, with its sentence on SQL the developer asks for.
	if got := standing(t, base, domain.KindOrchestrator, "demo", orchestrator); got != orchestratorSection ||
		!strings.Contains(got, "such as a merge, a deploy check or applying SQL in production") {
		t.Fatalf("orchestrator instructions without settings:\n%s", got)
	}

	off := dbChangeSettingsOn()
	off[0].Enabled = false
	noPaths := dbChangeSettingsOn()[:1]
	noPaths[0].Paths = nil
	for name, settings := range map[string][]domain.LadoDBChangeSettings{"none": nil, "all off": off, "on without paths": noPaths} {
		source := base
		source.DBChanges = &fakeDBChanges{settings: settings}
		for _, s := range []struct {
			kind domain.SessionKind
			id   domain.SessionID
		}{{domain.KindOrchestrator, orchestrator}, {domain.KindWorker, worker}, {domain.KindWorker, designer}} {
			if got, want := standing(t, source, s.kind, "demo", s.id), standing(t, base, s.kind, "demo", s.id); got != want {
				t.Fatalf("%s: settings changed the %s instructions:\n%s\nwant\n%s", name, s.kind, got, want)
			}
		}
	}

	dbChanges := &fakeDBChanges{settings: dbChangeSettingsOn()}
	source := base
	source.DBChanges = dbChanges
	if !strings.Contains(standing(t, source, domain.KindWorker, "demo", worker), dbChangeHeading) {
		t.Fatal("a worker of a project with settings on got no section")
	}
	// Outside LADO mode: no section, and the settings are not read.
	dbChanges.asked = nil
	if got := source.DBChangeSection(ctx, domain.KindOrchestrator, "other", orchestrator); got != "" || len(dbChanges.asked) != 0 {
		t.Fatalf("a project outside LADO mode got %q (asked %v)", got, dbChanges.asked)
	}
	if _, err := pf.store.SetLadoMode(ctx, "demo", false, pf.now); err != nil {
		t.Fatal(err)
	}
	if got := source.DBChangeSection(ctx, domain.KindWorker, "demo", worker); got != "" {
		t.Fatalf("a project whose LADO mode is off got %q", got)
	}
	if _, err := pf.store.SetLadoMode(ctx, "demo", true, pf.now); err != nil {
		t.Fatal(err)
	}
	if got := source.DBChangeSection(ctx, domain.SessionKind("reviewer"), "demo", worker); got != "" {
		t.Fatalf("a reviewer got %q", got)
	}
	if got := source.DBChangeSection(ctx, domain.KindWorker, "", worker); got != "" {
		t.Fatalf("a session without a project got %q", got)
	}

	var logged bytes.Buffer
	dbChanges.err = errors.New("disk on fire")
	source.Log = slog.New(slog.NewTextHandler(&logged, nil))
	if got := standing(t, source, domain.KindOrchestrator, "demo", orchestrator); got != orchestratorSection {
		t.Fatalf("a failed read = %q; want the LADO section without database changes", got)
	}
	if !strings.Contains(logged.String(), "disk on fire") {
		t.Fatalf("the failure was not logged: %q", logged.String())
	}
}
