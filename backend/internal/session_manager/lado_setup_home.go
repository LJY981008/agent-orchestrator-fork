package sessionmanager

// LADO fork: the config home that carries LADO's managed setup into every
// Claude Code session AO launches (docs/LJY-PRD/HARNESS.md §9). A terminal
// launch gets it at the top of prepareWorkspace, before AO writes the
// workspace's trust into <CLAUDE_CONFIG_DIR>/.claude.json, so the trust lands
// in the home. A Chat controller gets it on the environment its
// PrepareControllerEnv returns, which the Chat drivers launch with in place
// of the base environment.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SetLadoSetupHome installs LADO's Claude Code config homes. The daemon calls
// it before anything can spawn; nil keeps upstream behavior.
func (m *Manager) SetLadoSetupHome(home ports.LadoSetupHome) {
	m.ladoSetupHome = home
}

// applyLadoSetupHome points a Claude Code launch environment at the session's
// LADO config home. It runs after augmentAgentRuntimeEnv, whose setup-token
// injection skips a launch that names CLAUDE_CONFIG_DIR. An error fails the
// launch rather than start the session without its managed setup. A LADO
// plan worker's environment is marked too (domain.LadoPlanWorkerEnv): AO's
// hook command then decides each of its tool calls, and the Chat driver passes
// its tool lists (docs/LJY-PRD/TDD.md §5.1).
func (m *Manager) applyLadoSetupHome(ctx context.Context, agent ports.Agent, id domain.SessionID, env map[string]string) error {
	if m.ladoSetupHome == nil || env == nil || !m.isClaudeCodeAgent(agent) {
		return nil
	}
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return fmt.Errorf("LADO setup home: %w", err)
	}
	if domain.IsLadoPlanWorkerClientRequestID(rec.ClientRequestID) {
		setProtectedEnv(env, domain.LadoPlanWorkerEnv, "1", envKeysCaseInsensitive)
	}
	vars, err := m.ladoSetupHome.PrepareClaudeHome(ctx, ports.LadoSetupHomeInput{
		SessionID:       id,
		ProjectID:       rec.ProjectID,
		ClientRequestID: rec.ClientRequestID,
		Env:             env,
	})
	if err != nil {
		return err
	}
	for key, value := range vars {
		setProtectedEnv(env, key, value, envKeysCaseInsensitive)
	}
	return nil
}

// isClaudeCodeAgent reports whether agent is the adapter AO resolves for
// Claude Code.
func (m *Manager) isClaudeCodeAgent(agent ports.Agent) bool {
	if m.agents == nil || agent == nil {
		return false
	}
	claude, ok := m.agents.Agent(domain.HarnessClaudeCode)
	if !ok || claude == nil {
		return false
	}
	// == on interfaces panics for an adapter type that is not comparable.
	t := reflect.TypeOf(agent)
	return t == reflect.TypeOf(claude) && t.Comparable() && agent == claude
}

// ladoSafeNativeTranscriptPath is safeNativeTranscriptPath for a session
// whose config folder may be a LADO config home, which links projects to the
// folder that keeps the transcripts, so they resolve outside the home: in a
// home LADO built, a transcript under that folder is accepted too. Any other
// config folder, an account's included, is checked as upstream does.
func (m *Manager) ladoSafeNativeTranscriptPath(ctx context.Context, path, configDir string) string {
	if safe := safeNativeTranscriptPath(ctx, path, configDir); safe != "" || m.ladoSetupHome == nil || !m.ladoSetupHome.OwnsHome(configDir) {
		return safe
	}
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	realPath, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil || ctx.Err() != nil || !ladoProjectsLinkHolds(configDir, realPath) {
		return ""
	}
	if info, err := os.Stat(realPath); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return realPath
}

// ladoProjectsLinkHolds reports whether realPath, a transcript path with its
// links resolved, lies under the folder the config folder's projects entry
// resolves to. A LADO config home, like an account folder, links projects to
// the folder that keeps the transcripts, so they resolve outside it.
func ladoProjectsLinkHolds(configDir, realPath string) bool {
	projects, err := filepath.EvalSymlinks(filepath.Join(filepath.Clean(configDir), "projects"))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(projects, realPath)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
