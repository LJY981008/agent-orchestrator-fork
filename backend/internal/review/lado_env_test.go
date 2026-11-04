package review

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// An AO reviewer of a project in LADO mode starts with LADO's compact window
// over the project's own value (issue #42); a failing source leaves it out and
// the review goes on.
func TestReviewerLaunchCarriesLadosCompactWindow(t *testing.T) {
	t.Cleanup(func() { SetLadoReviewerEnv(nil) })
	var asked []string
	SetLadoReviewerEnv(func(_ context.Context, project domain.ProjectID, harness domain.AgentHarness) (map[string]string, error) {
		asked = append(asked, string(project)+" "+string(harness))
		if project != "mer" {
			return nil, nil
		}
		return map[string]string{domain.LadoClaudeCompactWindowEnv: "200000"}, nil
	})
	launch := func(spec LaunchSpec) map[string]string {
		t.Helper()
		rt := &fakeRuntime{}
		l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, t.TempDir())
		if _, err := l.Spawn(context.Background(), spec); err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		return rt.createCfg.Env
	}

	spec := launchSpec()
	spec.ProjectEnv = map[string]string{domain.LadoClaudeCompactWindowEnv: "900000"}
	if env := launch(spec); env[domain.LadoClaudeCompactWindowEnv] != "200000" {
		t.Fatalf("reviewer env %s = %q, want LADO's 200000 over the project's", domain.LadoClaudeCompactWindowEnv, env[domain.LadoClaudeCompactWindowEnv])
	}
	if !slices.Equal(asked, []string{"mer claude-code"}) {
		t.Fatalf("the source was asked %q, want once for project mer and claude-code", asked)
	}

	other := launchSpec()
	other.ProjectID = "plain"
	if env := launch(other); env[domain.LadoClaudeCompactWindowEnv] != "" {
		t.Fatalf("a project without LADO values got %q", env[domain.LadoClaudeCompactWindowEnv])
	}

	SetLadoReviewerEnv(func(context.Context, domain.ProjectID, domain.AgentHarness) (map[string]string, error) {
		return map[string]string{domain.LadoClaudeCompactWindowEnv: "1"}, errors.New("the store is closed")
	})
	if env := launch(spec); env[domain.LadoClaudeCompactWindowEnv] != "900000" {
		t.Fatalf("a failing source changed the env to %q", env[domain.LadoClaudeCompactWindowEnv])
	}

	SetLadoReviewerEnv(nil)
	if env := launch(spec); env[domain.LadoClaudeCompactWindowEnv] != "900000" {
		t.Fatalf("without a source the env is %q, want the project's", env[domain.LadoClaudeCompactWindowEnv])
	}
}
