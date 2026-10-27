package gitlab

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestGitLabIntegrationIsUnsupported(t *testing.T) {
	f := newFakeGL(t)
	issues := newTrackerForTest(t, f).LadoIssues()
	if rules, err := issues.BranchRules(ctx(), ladoTestRepo(), "main"); !errors.Is(err, ports.ErrLadoUnsupported) || len(rules.Rules) != 0 {
		t.Fatalf("BranchRules = %+v, %v; want ErrLadoUnsupported", rules, err)
	}
	status := ports.LadoCommitStatus{State: "success", Context: "lado/integration", Description: "Approved in LADO"}
	if err := issues.PostCommitStatus(ctx(), ladoTestRepo(), "0123456789abcdef0123456789abcdef01234567", status); !errors.Is(err, ports.ErrLadoUnsupported) {
		t.Fatalf("PostCommitStatus = %v, want ErrLadoUnsupported", err)
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("requests = %+v, want none", calls)
	}
}
