package sessionmanager

// LADO fork: values LADO puts on an agent launch environment besides its
// config home (docs/LJY-PRD/TDD.md §1.2). applyLadoSetupHome calls
// applyLadoLaunchEnv on every path that starts an agent: a terminal launch
// (prepareWorkspace), Chat spawn, resume (hibernation wake, daemon restart,
// TUI→Chat), the new controller of a message edit or /clear, and a Chat agent
// switch. Each start gets the setting as it is at that moment.

import (
	"context"
	"path/filepath"
	"slices"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LadoClaudeChrome decides a controller start's CLAUDE_CODE_ENABLE_CFC value.
// harness is the agent being started; false leaves the environment alone.
type LadoClaudeChrome interface {
	LaunchEnv(rec domain.SessionRecord, harness domain.AgentHarness) (string, bool)
}

// SetLadoClaudeChrome installs the Claude in Chrome source. The daemon calls
// it before anything can spawn; nil keeps upstream behavior.
func (m *Manager) SetLadoClaudeChrome(chrome LadoClaudeChrome) {
	m.ladoClaudeChrome = chrome
}

// LadoLaunchValues returns the environment values LADO puts on a start of
// harness for the session rec (issue #42): the compact window of a project in
// LADO mode, for a Claude Code or Codex start. env is the launch environment
// so far, which it reads only. An error leaves the launch without them.
type LadoLaunchValues interface {
	LaunchValues(ctx context.Context, rec domain.SessionRecord, harness domain.AgentHarness, env map[string]string) (map[string]string, error)
}

// SetLadoLaunchValues installs the source of LADO's launch values. The daemon
// calls it before anything can spawn; nil keeps upstream behavior.
func (m *Manager) SetLadoLaunchValues(values LadoLaunchValues) {
	m.ladoLaunchValues = values
}

// LadoReadDirectories returns the folders outside the workspace a Chat
// session of rec may read without asking: the folder of a LADO run's pinned
// plan file (ladotask.PlanDirResolver), nil for any other session.
type LadoReadDirectories func(ctx context.Context, rec domain.SessionRecord) ([]string, error)

// SetLadoAdditionalDirectories installs the source of LADO's read folders.
// The daemon calls it before anything can spawn; nil keeps upstream behavior.
func (m *Manager) SetLadoAdditionalDirectories(dirs LadoReadDirectories) {
	m.ladoReadDirs = dirs
}

// applyLadoLaunchEnv sets LADO's launch values on env for the agent being
// started, harness. They win over the project's and the daemon's
// environment, so an inherited CLAUDE_CODE_ENABLE_CFC=1 cannot turn Chrome on
// for a LADO worker, and a project's own CLAUDE_CODE_AUTO_COMPACT_WINDOW does
// not undo LADO's.
func (m *Manager) applyLadoLaunchEnv(ctx context.Context, rec domain.SessionRecord, harness domain.AgentHarness, env map[string]string) {
	if env == nil {
		return
	}
	if m.ladoClaudeChrome != nil {
		if value, ok := m.ladoClaudeChrome.LaunchEnv(rec, harness); ok {
			setProtectedEnv(env, domain.LadoClaudeChromeEnv, value, envKeysCaseInsensitive)
		}
	}
	if m.ladoLaunchValues != nil {
		values, err := m.ladoLaunchValues.LaunchValues(ctx, rec, harness, env)
		if err != nil {
			m.logger.Warn("LADO could not read its launch values; the session starts without them",
				"sessionID", rec.ID, "harness", harness, "err", err)
		}
		for key, value := range values {
			setProtectedEnv(env, key, value, envKeysCaseInsensitive)
		}
	}
}

// applyLadoCodexLaunchEnv puts LADO's launch values on a Codex start, which
// gets no config home. Launch values never fail a launch.
func (m *Manager) applyLadoCodexLaunchEnv(ctx context.Context, id domain.SessionID, env map[string]string) {
	if m.ladoLaunchValues == nil || env == nil {
		return
	}
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return
	}
	m.applyLadoLaunchEnv(ctx, rec, domain.HarnessCodex, env)
}

// isAgentFor reports whether agent is the adapter AO resolves for harness.
func (m *Manager) isAgentFor(agent ports.Agent, harness domain.AgentHarness) bool {
	if m.agents == nil || agent == nil {
		return false
	}
	resolved, ok := m.agents.Agent(harness)
	if !ok || resolved == nil {
		return false
	}
	return sameAgent(agent, resolved)
}

// ladoChatDirectories adds the folders LADO lets a Chat session of rec read
// to dirs. A failed lookup leaves dirs as they are: the session may then
// ask before it reads the plan file, which never blocks the start.
func (m *Manager) ladoChatDirectories(ctx context.Context, rec domain.SessionRecord, dirs []string) []string {
	if m.ladoReadDirs == nil {
		return dirs
	}
	extra, err := m.ladoReadDirs(ctx, rec)
	if err != nil {
		m.logger.Warn("LADO could not find the plan folder of a run's session; it starts without it",
			"sessionID", rec.ID, "err", err)
		return dirs
	}
	out := slices.Clone(dirs)
	for _, dir := range extra {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		if !slices.Contains(out, dir) {
			out = append(out, dir)
		}
	}
	return out
}
