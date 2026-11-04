package agent

// LADO fork: the check LADO runs before it starts a worker (issue #42, plan
// decision 4). A start that fails spends one of the worker's attempts and
// burns its client request id, so LADO checks the agent first and holds the
// worker, without spending an attempt, while the check fails. The check is
// AO's own launch readiness after the cached install is dropped, so it never
// disagrees with what AO shows; readiness only resolves paths, so the
// executable is also run once with --version.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// launchVersionTimeout bounds the --version run, and launchVersionWaitDelay
// the wait for its output once it is stopped.
const (
	launchVersionTimeout   = 20 * time.Second
	launchVersionWaitDelay = 2 * time.Second
)

// versionProbedHarnesses are the harnesses whose CLI answers --version
// without starting a session. Others get the readiness check alone.
var versionProbedHarnesses = map[domain.AgentHarness]bool{
	domain.HarnessClaudeCode: true,
	domain.HarnessCodex:      true,
}

// runLaunchVersion runs path --version; tests replace it.
var runLaunchVersion = func(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, launchVersionTimeout)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, path, "--version")
	// A timeout stops the whole tree, a Windows .cmd shim's child included,
	// and a child that still holds the output pipes is not waited for long.
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = launchVersionWaitDelay
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		text := strings.TrimSpace(out.String())
		if len(text) > 200 {
			text = text[:200]
		}
		if text != "" {
			return fmt.Errorf("%w: %s", err, text)
		}
		return err
	}
	return nil
}

// PreflightLaunch checks that harness can start a session now: it drops the
// cached install observation and executable path (invalidateLaunchInstall),
// asks for launch readiness
// (EnsureAgentReadiness with the launch purpose) and runs the executable
// with --version. An agent that is not installed, or whose executable does
// not run, fails with a wrapped ports.ErrAgentBinaryNotFound. Sign-in is left
// to the start, as AO's own spawn does for a Claude Code login.
func (s *Service) PreflightLaunch(ctx context.Context, harness domain.AgentHarness) error {
	id := string(harness)
	if _, ok := s.agent(id); !ok {
		return fmt.Errorf("agent %s: %w", id, ports.ErrAgentBinaryNotFound)
	}
	s.invalidateLaunchInstall(id)
	snapshot, err := s.EnsureAgentReadiness(ctx, id, domain.AgentReadinessPurposeLaunch)
	if err != nil {
		return fmt.Errorf("check %s before a launch: %w", id, err)
	}
	if snapshot.Installation.State == domain.AgentInstallationNotInstalled {
		return fmt.Errorf("%s is not installed: %w", id, ports.ErrAgentBinaryNotFound)
	}
	if !versionProbedHarnesses[harness] {
		return nil
	}
	path, err := s.ResolveAgentBinary(ctx, id)
	if err != nil {
		if errors.Is(err, ports.ErrAgentBinaryNotFound) {
			return fmt.Errorf("%s is not installed: %w", id, err)
		}
		return fmt.Errorf("resolve %s before a launch: %w", id, err)
	}
	if err := runLaunchVersion(ctx, path); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The next check resolves the executable again.
		s.invalidateLaunchInstall(id)
		return fmt.Errorf("%s at %s does not run: %w: %w", id, path, ports.ErrAgentBinaryNotFound, err)
	}
	return nil
}

// invalidateLaunchInstall is InvalidateAgentInstallation without the model
// catalogs: LADO checks before every worker it starts, and while a worker
// waits once a minute, so dropping the catalogs would rediscover every
// project's models each time. A changed install still refreshes them through
// AO's own invalidation paths.
func (s *Service) invalidateLaunchInstall(agentID string) {
	s.readiness.Invalidate(agentID, readinessInvalidateInstallation)
	if item, ok := s.agent(agentID); ok {
		if invalidator, ok := item.Agent.(ports.AgentBinaryResolutionInvalidator); ok {
			invalidator.InvalidateBinaryResolution()
		}
	}
}
