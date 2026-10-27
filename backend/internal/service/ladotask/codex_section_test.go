package ladotask

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fakeCodexSetup answers like ladosetup's CodexInstructions: the text an
// execution pinned for its client request id, else the current text.
type fakeCodexSetup struct {
	pinned  map[string]string
	current string
	err     error
	asked   []codexSetupCall
}

type codexSetupCall struct {
	session domain.SessionID
	request string
	project domain.ProjectID
}

func (f *fakeCodexSetup) CodexInstructions(_ context.Context, sessionID domain.SessionID, clientRequestID string, projectID domain.ProjectID) (string, error) {
	f.asked = append(f.asked, codexSetupCall{session: sessionID, request: clientRequestID, project: projectID})
	if text, ok := f.pinned[clientRequestID]; ok {
		return text, f.err
	}
	return f.current, f.err
}

func TestPromptSectionCarriesAdoptedAgentsMd(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	rules := "## LADO rules\n\nWrite commit messages in Korean."
	content, err := json.Marshal(map[string]string{"permissionCeiling": "accept-edits", "prompt": rules})
	if err != nil {
		t.Fatal(err)
	}
	pf.policy.content = content
	rev := pf.planned(t)
	if _, err := pf.svc.ApprovePlan(ctx, rev.ID, rev.SHA256, policyA, pf.proof(rev.ID)); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	worker := pf.run(t, pf.claim(t))
	rec, _, err := pf.store.GetSession(ctx, worker)
	if err != nil {
		t.Fatal(err)
	}
	setup := &fakeCodexSetup{pinned: map[string]string{rec.ClientRequestID: "pinned codex rules\n"}, current: "\ncurrent codex rules\n"}
	source := PromptSource{Settings: pf.store, Sessions: pf.store, Policies: pf.store, Setup: setup}
	section := func(t *testing.T, kind domain.SessionKind, project domain.ProjectID, id domain.SessionID, harness domain.AgentHarness) string {
		t.Helper()
		got, err := source.Section(ctx, kind, project, id, harness)
		if err != nil {
			t.Fatalf("Section(%s, %s, %s): %v", kind, id, harness, err)
		}
		return got
	}
	const heading = "## User instructions (managed by LADO)\n\n"

	// A Codex worker LADO dispatched gets the text its execution pinned, then
	// its rules; on Claude Code it gets the rules alone.
	if got, want := section(t, domain.KindWorker, "demo", worker, domain.HarnessCodex), heading+"pinned codex rules\n\n"+rules; got != want {
		t.Fatalf("Codex execution's section = %q, want %q", got, want)
	}
	if want := []codexSetupCall{{session: worker, request: rec.ClientRequestID, project: "demo"}}; !reflect.DeepEqual(setup.asked, want) {
		t.Fatalf("asked %+v, want the session's client request and project", setup.asked)
	}
	if got := section(t, domain.KindWorker, "demo", worker, domain.HarnessClaudeCode); got != rules {
		t.Fatalf("Claude Code execution's section = %q, want the rules alone", got)
	}
	// Without the agent, the session's row names it.
	if want := map[bool]string{true: heading + "pinned codex rules\n\n" + rules, false: rules}[rec.Harness == domain.HarnessCodex]; section(t, domain.KindWorker, "demo", worker, "") != want {
		t.Fatalf("the section of a %s row without the agent is not %q", rec.Harness, want)
	}
	// Other Codex sessions get the current text, in a LADO project or not,
	// and without a project.
	if got, want := section(t, domain.KindOrchestrator, "demo", "demo-orchestrator-1", domain.HarnessCodex), heading+"current codex rules\n\n"+orchestratorSection; got != want {
		t.Fatalf("Codex orchestrator's section = %q, want %q", got, want)
	}
	for _, project := range []domain.ProjectID{"other", ""} {
		if got := section(t, domain.KindWorker, project, "scratch-1", domain.HarnessCodex); got != heading+"current codex rules" {
			t.Fatalf("Codex worker in %q gets %q, want the current text", project, got)
		}
		if got := section(t, domain.KindOrchestrator, project, "scratch-orchestrator", domain.HarnessClaudeCode); got != "" {
			t.Fatalf("Claude Code orchestrator in %q gets %q, want none", project, got)
		}
	}

	// Blank text adds no section; a failure fails it.
	setup.current = " \n"
	if got := section(t, domain.KindOrchestrator, "other", "other-orchestrator-1", domain.HarnessCodex); got != "" {
		t.Fatalf("section with blank text = %q, want none", got)
	}
	setup.err = errors.New("LADO_SETUP_CODEX_FAILED")
	if _, err := source.Section(ctx, domain.KindWorker, "demo", worker, domain.HarnessCodex); !errors.Is(err, setup.err) {
		t.Fatalf("Section with a failing setup = %v, want its error", err)
	}
	// Without the setup source, Codex sessions get LADO's own sections only.
	source.Setup = nil
	if got := section(t, domain.KindWorker, "demo", worker, domain.HarnessCodex); got != rules {
		t.Fatalf("section without a setup source = %q, want the rules alone", got)
	}
}
