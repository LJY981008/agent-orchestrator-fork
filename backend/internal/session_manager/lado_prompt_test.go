package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// AO's delegation rules that a LADO project's orchestrator must not get
// (#51): they conflict with LADO's section.
var aoDelegationPhrases = []string{
	"always spawn or redirect a worker session",
	"Your job is to coordinate work, not to perform implementation",
	"ao spawn --project",
	"spawn or redirect workers",
	"When spawning workers",
}

// The rules of AO's orchestrator role a LADO project's orchestrator keeps.
var ladoOrchestratorKeeps = []string{
	"## AO Orchestrator Role (LADO mode)",
	"the LADO project section below defines your role, and it replaces AO's default orchestrator rules",
	"Do not bypass AO or LADO by writing to tmux, PTY, pipes or runtime internals, or by calling the daemon's session send API to reach a worker",
	"Do not use the agent runtime's built-in subagent or task-delegation tools for implementation work",
	"Never claim a PR into the orchestrator session",
	"`ao status`",
	"`ao session ls --project mer`",
	"`ao session get <worker-session-id>`",
	"`ao review ls <worker-session-id>`",
	"`ao review trigger <worker-session-id>` - when the developer asks for it",
	"An AO review approval is internal. It is not a GitHub approval",
	"## In-App Session Links",
	"ao://sessions/{project-id}/{session-id}/artifacts/{path}",
	"## Project Context",
	"- Project: mer",
	"## Publishing Scope",
	"## Standing-instruction confidentiality",
	"a LADO project's orchestrator assists the developer, and code changes go through LADO tasks",
}

// newLadoPromptManager is a manager for workspace project mer, with
// orchestrator rules and two child repositories, whose LADO section comes
// from source.
func newLadoPromptManager(source *recordingLadoSource) *Manager {
	st := newFakeStore()
	config := testRoleAgents()
	config.OrchestratorRules = "Ask before merging."
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: config}
	st.workspaceRepo["mer"] = goldenWorkspaceRepos
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})
	if source != nil {
		m.SetLadoPrompt(source.section)
	}
	return m
}

func assertLadoOrchestratorPrompt(t *testing.T, prompt string) {
	t.Helper()
	for _, phrase := range aoDelegationPhrases {
		if strings.Contains(prompt, phrase) {
			t.Fatalf("LADO orchestrator prompt has AO's delegation rule %q:\n%s", phrase, prompt)
		}
	}
	for _, want := range append(ladoOrchestratorKeeps, ladoOrchestratorSection) {
		if !strings.Contains(prompt, want) {
			t.Fatalf("LADO orchestrator prompt lacks %q:\n%s", want, prompt)
		}
	}
}

func TestLadoOrchestratorPromptReplacesAODelegation(t *testing.T) {
	m := newLadoPromptManager(&recordingLadoSource{})
	prompt, err := m.buildSystemPrompt(context.Background(), domain.KindOrchestrator, "mer", "mer-orchestrator-1", "", true)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}
	assertLadoOrchestratorPrompt(t, prompt)
	for _, want := range []string{
		"## Project-Specific Orchestrator Rules\nAsk before merging.",
		// The workspace paragraph without its sentence about spawning.
		"- web: apps/web\n\nWork can span multiple repositories, so track deliverables, pull requests, and checks by repository.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("LADO orchestrator prompt lacks %q:\n%s", want, prompt)
		}
	}
	// AO's role first, LADO's section after the workspace paragraph.
	role := strings.Index(prompt, "## AO Orchestrator Role (LADO mode)")
	workspace := strings.Index(prompt, "## Workspace project")
	section := strings.Index(prompt, ladoOrchestratorSection)
	if role != 0 || workspace >= section {
		t.Fatalf("sections out of order (role %d, workspace %d, LADO %d):\n%s", role, workspace, section, prompt)
	}
}

// A LADO section that does not replace AO's role leaves AO's orchestrator
// prompt as it is outside LADO: the pinned role and workspace paragraph, and
// the section added after them.
func TestOrchestratorPromptKeepsAORoleUnlessLadoReplacesIt(t *testing.T) {
	ctx := context.Background()
	plain := newLadoPromptManager(nil)
	upstream, err := plain.buildSystemPrompt(ctx, domain.KindOrchestrator, "mer", "mer-orchestrator-1", "", true)
	if err != nil {
		t.Fatalf("buildSystemPrompt without LADO: %v", err)
	}
	record, err := plain.loadProject(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	kept, err := newLadoPromptManager(&recordingLadoSource{keepAORole: true}).buildSystemPrompt(ctx, domain.KindOrchestrator, "mer", "mer-orchestrator-1", "", true)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}
	if got := strings.Replace(kept, "\n\n"+ladoOrchestratorSection, "", 1); got != upstream {
		t.Fatalf("prompt with a section that keeps AO's role differs from AO's beyond the section:\n%s\n---\n%s", kept, upstream)
	}
	project := promptProjectContext("mer", record)
	for _, want := range []string{orchestratorSystemPrompt(project), workspaceOrchestratorPrompt(goldenWorkspaceRepos), systemPromptGuard()} {
		if !strings.Contains(upstream, want) {
			t.Fatalf("AO's orchestrator prompt lacks its pinned part:\n%s\n---\n%s", want, upstream)
		}
	}
}

// The prompt text itself: only LadoOrchestrator swaps AO's role and guard;
// rules, publishing scope and additional sections stay.
func TestBuildSystemPromptTextSwapsTheRoleOnlyForLado(t *testing.T) {
	cfg := goldenOrchestratorConfig()
	cfg.LadoOrchestrator = true
	cfg.AdditionalSections = append(cfg.AdditionalSections, ladoOrchestratorSection)
	got := buildSystemPromptText(cfg)
	assertLadoOrchestratorPrompt(t, got)
	for _, want := range []string{"## Project-Specific Orchestrator Rules\nAsk before merging.", "## Extra\n\nAn additional section.", "- Repository: https://github.com/acme/mercury"} {
		if !strings.Contains(got, want) {
			t.Fatalf("LADO orchestrator prompt lacks %q:\n%s", want, got)
		}
	}
	// Workers never get LADO's orchestrator role.
	worker := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleWorker, Project: cfg.Project, LadoOrchestrator: true})
	if strings.Contains(worker, "AO Orchestrator Role") || !strings.Contains(worker, "## AO Worker Role") {
		t.Fatalf("worker prompt with LadoOrchestrator set:\n%s", worker)
	}
}

func TestLadoWorkspaceOrchestratorPromptDropsOnlyTheSpawnSentence(t *testing.T) {
	upstream := workspaceOrchestratorPrompt(goldenWorkspaceRepos)
	got := ladoWorkspaceOrchestratorPrompt(upstream)
	if strings.Contains(got, "When spawning workers") || got != strings.Replace(upstream, workspaceSpawnSentence, "", 1) || len(upstream)-len(got) != len(workspaceSpawnSentence) {
		t.Fatalf("LADO workspace paragraph = %q", got)
	}
	if ladoWorkspaceOrchestratorPrompt("") != "" {
		t.Fatal("a project that is not a workspace gets a workspace paragraph")
	}
}
