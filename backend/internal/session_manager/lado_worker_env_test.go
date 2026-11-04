package sessionmanager

import (
	"context"
	"errors"
	"slices"
	"testing"

	claudeagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Every LADO execution worker's Claude Code launch is marked for the push
// guard, with its project's base branch; a plan worker's and another
// session's are not, even when the project's environment sets the marks.
func TestApplyLadoSetupHomeMarksExecutionWorkers(t *testing.T) {
	ctx := context.Background()
	claude := claudeagent.New()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Kind: domain.ProjectKindSingleRepo, Config: domain.ProjectConfig{DefaultBranch: "trunk"}}
	const entry = domain.LadoQueueEntryID("lado-queue-1")
	ids := map[domain.SessionID]string{
		"single":      "lado:lado-queue-1/1",
		"part":        "lado:lado-queue-1/scm/1",
		"merge":       "lado:lado-queue-1/merge-scm/1",
		"integration": "lado:lado-queue-1/integration/1",
		"plan":        domain.LadoPlanWorkerClientRequestID("req-1", domain.LadoPlanRoleCritic, 1),
		"plain":       "",
		"other":       "client-7",
	}
	// The literal ids are the ones LADO's dispatcher makes.
	for id, want := range map[domain.SessionID]string{
		"single":      domain.LadoClientRequestID(entry, 1),
		"part":        domain.LadoRunClientRequestID(domain.LadoRunRequest{Entry: entry, Role: domain.LadoRolePart, Part: "scm", Attempt: 1}),
		"merge":       domain.LadoRunClientRequestID(domain.LadoRunRequest{Entry: entry, Role: domain.LadoRoleMerge, Part: "scm", Attempt: 1}),
		"integration": domain.LadoRunClientRequestID(domain.LadoRunRequest{Entry: entry, Role: domain.LadoRoleIntegration, Attempt: 1}),
	} {
		if ids[id] != want {
			t.Fatalf("%s: id %q, the dispatcher makes %q", id, ids[id], want)
		}
	}
	for id, crid := range ids {
		st.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "mer", ClientRequestID: crid}
	}
	m := New(Deps{Store: st, Agents: switchTestAgents{domain.HarnessClaudeCode: claude, domain.HarnessCodex: fakeAgent{}}})
	m.SetLadoSetupHome(&fakeLadoHome{home: t.TempDir()})
	for id := range ids {
		env := map[string]string{"PATH": "/bin", domain.LadoWorkerEnv: "1", domain.LadoBaseBranchesEnv: "x"}
		if err := m.applyLadoSetupHome(ctx, claude, id, env); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		worker := id != "plan" && id != "plain" && id != "other"
		if got := env[domain.LadoWorkerEnv] == "1"; got != worker {
			t.Fatalf("%s: %s = %q, want marked %v", id, domain.LadoWorkerEnv, env[domain.LadoWorkerEnv], worker)
		}
		wantBranches := ""
		if worker {
			wantBranches = "trunk"
		}
		if got := env[domain.LadoBaseBranchesEnv]; got != wantBranches {
			t.Fatalf("%s: %s = %q, want %q", id, domain.LadoBaseBranchesEnv, got, wantBranches)
		}
	}
}

// A workspace project's worker lists each child repository's default branch
// besides the project's own setting; auto and blanks are left out.
func TestApplyLadoSetupHomeListsWorkspaceBaseBranches(t *testing.T) {
	ctx := context.Background()
	claude := claudeagent.New()
	st := newFakeStore()
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace, Config: domain.ProjectConfig{DefaultBranch: domain.DefaultBranchAuto}}
	st.workspaceRepo["ws"] = []domain.WorkspaceRepoRecord{
		{ProjectID: "ws", Name: "api", RelativePath: "api", DefaultBranch: "develop"},
		{ProjectID: "ws", Name: "web", RelativePath: "web", DefaultBranch: "main"},
		{ProjectID: "ws", Name: "tools", RelativePath: "tools", DefaultBranch: ""},
		{ProjectID: "ws", Name: "docs", RelativePath: "docs", DefaultBranch: "develop"},
	}
	st.sessions["s-1"] = domain.SessionRecord{ID: "s-1", ProjectID: "ws", ClientRequestID: "lado:lado-queue-1/integration/1"}
	m := New(Deps{Store: st, Agents: switchTestAgents{domain.HarnessClaudeCode: claude, domain.HarnessCodex: fakeAgent{}}})
	m.SetLadoSetupHome(&fakeLadoHome{home: t.TempDir()})
	env := map[string]string{"PATH": "/bin"}
	if err := m.applyLadoSetupHome(ctx, claude, "s-1", env); err != nil {
		t.Fatal(err)
	}
	if env[domain.LadoWorkerEnv] != "1" {
		t.Fatalf("%s = %q", domain.LadoWorkerEnv, env[domain.LadoWorkerEnv])
	}
	got := domain.SplitLadoBaseBranches(env[domain.LadoBaseBranchesEnv])
	if !slices.Equal(got, []string{"develop", "main"}) {
		t.Fatalf("%s = %q, want develop and main", domain.LadoBaseBranchesEnv, env[domain.LadoBaseBranchesEnv])
	}
}

// A project the store cannot read still leaves the worker marked: the hook
// then guards main, master and the remote's default branch.
func TestApplyLadoSetupHomeMarksWorkerWithoutProject(t *testing.T) {
	ctx := context.Background()
	claude := claudeagent.New()
	st := newFakeStore()
	st.sessions["s-1"] = domain.SessionRecord{ID: "s-1", ProjectID: "mer", ClientRequestID: "lado:lado-queue-1/1"}
	m := New(Deps{Store: st, Agents: switchTestAgents{domain.HarnessClaudeCode: claude, domain.HarnessCodex: fakeAgent{}}})
	m.SetLadoSetupHome(&fakeLadoHome{home: t.TempDir()})
	env := map[string]string{"PATH": "/bin"}
	if err := m.applyLadoSetupHome(ctx, claude, "s-1", env); err != nil {
		t.Fatal(err)
	}
	if env[domain.LadoWorkerEnv] != "1" {
		t.Fatalf("%s = %q", domain.LadoWorkerEnv, env[domain.LadoWorkerEnv])
	}
	if _, ok := env[domain.LadoBaseBranchesEnv]; ok {
		t.Fatalf("%s = %q, want unset", domain.LadoBaseBranchesEnv, env[domain.LadoBaseBranchesEnv])
	}
	st.getProjectErr = errors.New("disk gone")
	env = map[string]string{"PATH": "/bin"}
	m.applyLadoWorkerEnv(ctx, st.sessions["s-1"], env)
	if env[domain.LadoWorkerEnv] != "1" {
		t.Fatalf("after a failed lookup %s = %q", domain.LadoWorkerEnv, env[domain.LadoWorkerEnv])
	}
}
