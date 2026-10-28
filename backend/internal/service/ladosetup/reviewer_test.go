package ladosetup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// reviewerSection returns the section for the reviewer on harness of worker
// and fails the test on an error.
func reviewerSection(t *testing.T, svc *Service, worker domain.SessionID, harness domain.AgentHarness) string {
	t.Helper()
	got, err := svc.ReviewerSection(context.Background(), worker, harness)
	if err != nil {
		t.Fatalf("ReviewerSection(%s, %s): %v", worker, harness, err)
	}
	return got
}

// wantReviewerSection is the section holding the files, given as name and
// text pairs, in order.
func wantReviewerSection(files ...string) string {
	s := "## User instructions (managed by LADO)\n\n" + reviewerInstructionsLead
	for i := 0; i+1 < len(files); i += 2 {
		s += "\n\n### " + files[i] + "\n\n" + files[i+1]
	}
	return s
}

// TestReviewerSectionUsesPinnedInstructions is plan decision s: AO's reviewer
// gets the adopted instructions of its own tool. A Claude Code reviewer of a
// Claude Code execution reads the CLAUDE.md the execution's rule snapshot
// pinned, never the edit made after the approval; a Codex reviewer of the
// same execution reads the current AGENTS.md that is on for the project,
// since the snapshot pinned Claude Code's items only. Before an adoption,
// for another reviewer tool and for a worker AO has no record of there is
// no section.
func TestReviewerSectionUsesPinnedInstructions(t *testing.T) {
	f := newAdoption(t)
	f.write(filepath.Join(f.claude, "ABOUT.md"), "about me\n")
	request := domain.LadoClientRequestID("lado-queue-1", 1)
	// The workers are live, which would hold an adoption back, so only the
	// services that read reviewer sections list them.
	reviewers := func() *Service {
		deps := f.deps
		deps.Sessions = fakeSessions{
			{ID: "worker", ProjectID: "p", Harness: domain.HarnessClaudeCode, ClientRequestID: request},
			{ID: "other", ProjectID: "p", Harness: domain.HarnessClaudeCode, ClientRequestID: "ui-request-1"},
		}
		return New(deps)
	}
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		if got := reviewerSection(t, reviewers(), "worker", harness); got != "" {
			t.Fatalf("%s reviewer before any adoption got %q, want no section", harness, got)
		}
	}

	ids := itemIDs(f.mustAdopt(f.service(),
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		claudeMove(domain.LadoSetupKindInstructions, "ABOUT.md"),
	))
	codexIDs := itemIDs(f.mustAdopt(f.service(), codexMove(domain.LadoSetupKindInstructions, "AGENTS.md")))
	// The approval pinned CLAUDE.md; ABOUT.md was off for the task then.
	f.deps.Store = &pinningStore{recordingStore: f.store, request: request, pins: map[domain.LadoSetupItemID]int{ids["CLAUDE.md"]: 1}}
	svc := reviewers()
	// After the approval CLAUDE.md and AGENTS.md are edited.
	f.addVersion(ids["CLAUDE.md"], 1, "# edited\n")
	f.addVersion(codexIDs["AGENTS.md"], 1, "codex rules v2\n")

	if got, want := reviewerSection(t, svc, "worker", domain.HarnessClaudeCode), wantReviewerSection("CLAUDE.md", "# me"); got != want {
		t.Fatalf("Claude Code reviewer of the execution got\n%q\nwant the pinned CLAUDE.md\n%q", got, want)
	}
	if got, want := reviewerSection(t, svc, "worker", domain.HarnessCodex), wantReviewerSection("AGENTS.md", "codex rules v2"); got != want {
		t.Fatalf("Codex reviewer of the execution got\n%q\nwant the current AGENTS.md\n%q", got, want)
	}
	// The reviewer of a worker that is no execution reads the current
	// versions, CLAUDE.md first.
	if got, want := reviewerSection(t, svc, "other", domain.HarnessClaudeCode), wantReviewerSection("CLAUDE.md", "# edited", "ABOUT.md", "about me"); got != want {
		t.Fatalf("Claude Code reviewer of another worker got\n%q\nwant the current files\n%q", got, want)
	}
	for _, c := range []struct {
		worker  domain.SessionID
		harness domain.AgentHarness
	}{{"worker", domain.HarnessOpenCode}, {"gone", domain.HarnessClaudeCode}} {
		if got := reviewerSection(t, svc, c.worker, c.harness); got != "" {
			t.Fatalf("%s reviewer of %s got %q, want no section", c.harness, c.worker, got)
		}
	}

	// Bindings turn the current versions off, never the pinned ones.
	setBinding(t, f, ids["CLAUDE.md"], domain.LadoScopeGlobal, "", domain.LadoBindingOff)
	setBinding(t, f, codexIDs["AGENTS.md"], domain.LadoScopeProject, "p", domain.LadoBindingForceOff)
	if got, want := reviewerSection(t, svc, "worker", domain.HarnessClaudeCode), wantReviewerSection("CLAUDE.md", "# me"); got != want {
		t.Fatalf("Claude Code reviewer of the execution after the bindings got\n%q\nwant the pinned CLAUDE.md\n%q", got, want)
	}
	if got, want := reviewerSection(t, svc, "other", domain.HarnessClaudeCode), wantReviewerSection("ABOUT.md", "about me"); got != want {
		t.Fatalf("Claude Code reviewer of another worker after the bindings got\n%q\nwant ABOUT.md only\n%q", got, want)
	}
	if got := reviewerSection(t, svc, "worker", domain.HarnessCodex); got != "" {
		t.Fatalf("Codex reviewer with AGENTS.md off for the project got %q, want no section", got)
	}
}
