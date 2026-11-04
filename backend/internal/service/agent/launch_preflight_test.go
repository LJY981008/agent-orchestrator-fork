package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// preflightAgent caches its path as the Claude and Codex adapters do, until
// InvalidateBinaryResolution drops it.
type preflightAgent struct {
	readinessTestAgent
	invalidations atomic.Int32
}

func (a *preflightAgent) InvalidateBinaryResolution() { a.invalidations.Add(1) }

func preflightService(t *testing.T, harness domain.AgentHarness, resolve func(context.Context) (string, error)) (*Service, *preflightAgent) {
	t.Helper()
	agent := &preflightAgent{readinessTestAgent: readinessTestAgent{
		resolve: resolve,
		auth:    func(context.Context) (ports.AgentAuthStatus, error) { return ports.AgentAuthStatusAuthorized, nil },
	}}
	svc := newService([]agentregistry.HarnessAgent{readinessHarness(string(harness), string(harness), agent)}, nil, nil, nil)
	return svc, agent
}

func stubLaunchVersion(t *testing.T, fn func(context.Context, string) error) *[]string {
	t.Helper()
	var ran []string
	old := runLaunchVersion
	runLaunchVersion = func(ctx context.Context, path string) error {
		ran = append(ran, path)
		return fn(ctx, path)
	}
	t.Cleanup(func() { runLaunchVersion = old })
	return &ran
}

func TestPreflightLaunchFollowsLaunchReadinessAfterInvalidation(t *testing.T) {
	ran := stubLaunchVersion(t, func(context.Context, string) error { return nil })
	path := "/opt/claude-old"
	svc, agent := preflightService(t, domain.HarnessClaudeCode, func(context.Context) (string, error) { return path, nil })
	ctx := context.Background()

	if err := svc.PreflightLaunch(ctx, domain.HarnessClaudeCode); err != nil {
		t.Fatalf("PreflightLaunch = %v, want nil for an installed agent", err)
	}
	// A second check resolves again: the cached install is dropped first,
	// so a path that changed in between is the one checked.
	path = "/opt/claude-new"
	if err := svc.PreflightLaunch(ctx, domain.HarnessClaudeCode); err != nil {
		t.Fatalf("second PreflightLaunch = %v", err)
	}
	if got := agent.invalidations.Load(); got < 2 {
		t.Fatalf("InvalidateBinaryResolution ran %d times, want once per check", got)
	}
	if len(*ran) != 2 || (*ran)[1] != "/opt/claude-new" {
		t.Fatalf("--version ran on %q, want the path resolved after the invalidation", *ran)
	}
	snapshot, err := svc.EnsureAgentReadiness(ctx, string(domain.HarnessClaudeCode), domain.AgentReadinessPurposeLaunch)
	if err != nil || snapshot.Installation.State != domain.AgentInstallationInstalled {
		t.Fatalf("readiness after the check = %+v, %v; want installed", snapshot.Installation, err)
	}
}

func TestPreflightLaunchFailsWhenTheAgentIsNotInstalled(t *testing.T) {
	ran := stubLaunchVersion(t, func(context.Context, string) error { return nil })
	svc, _ := preflightService(t, domain.HarnessClaudeCode, func(context.Context) (string, error) {
		return "", ports.ErrAgentBinaryNotFound
	})
	err := svc.PreflightLaunch(context.Background(), domain.HarnessClaudeCode)
	if !errors.Is(err, ports.ErrAgentBinaryNotFound) {
		t.Fatalf("PreflightLaunch = %v, want ErrAgentBinaryNotFound", err)
	}
	if len(*ran) != 0 {
		t.Fatalf("--version ran on %q for an agent readiness found missing", *ran)
	}
}

func TestPreflightLaunchFailsWhenTheExecutableDoesNotRun(t *testing.T) {
	// The npm launcher of 2026-10-30 resolved but printed "native binary not
	// found": readiness sees a path, the run does not.
	stubLaunchVersion(t, func(context.Context, string) error { return errors.New("exit status 1") })
	svc, agent := preflightService(t, domain.HarnessCodex, func(context.Context) (string, error) { return "/opt/codex", nil })
	before := agent.invalidations.Load()
	err := svc.PreflightLaunch(context.Background(), domain.HarnessCodex)
	if !errors.Is(err, ports.ErrAgentBinaryNotFound) {
		t.Fatalf("PreflightLaunch = %v, want ErrAgentBinaryNotFound", err)
	}
	if agent.invalidations.Load()-before < 2 {
		t.Fatalf("a failed run must drop the cached path again for the next check")
	}
}

func TestPreflightLaunchSkipsTheVersionRunForOtherHarnesses(t *testing.T) {
	ran := stubLaunchVersion(t, func(context.Context, string) error { return errors.New("not run") })
	svc, _ := preflightService(t, domain.HarnessGemini, func(context.Context) (string, error) { return "/opt/gemini", nil })
	if err := svc.PreflightLaunch(context.Background(), domain.HarnessGemini); err != nil {
		t.Fatalf("PreflightLaunch = %v, want readiness alone for a harness without a version probe", err)
	}
	if len(*ran) != 0 {
		t.Fatalf("--version ran on %q", *ran)
	}
}

func TestPreflightLaunchRefusesAnUnknownHarness(t *testing.T) {
	svc, _ := preflightService(t, domain.HarnessClaudeCode, func(context.Context) (string, error) { return "/opt/claude", nil })
	if err := svc.PreflightLaunch(context.Background(), "no-such-agent"); !errors.Is(err, ports.ErrAgentBinaryNotFound) {
		t.Fatalf("PreflightLaunch = %v, want ErrAgentBinaryNotFound", err)
	}
}
