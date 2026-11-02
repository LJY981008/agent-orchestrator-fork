package sessionmanager

// LADO fork: values LADO puts on a Claude Code launch environment besides
// its config home (docs/LJY-PRD/TDD.md §1.2). applyLadoSetupHome calls
// applyLadoLaunchEnv on every path that starts a Claude Code controller:
// Chat spawn, resume (hibernation wake, daemon restart, TUI→Chat), the new
// controller of a message edit or /clear, and a Chat agent switch. Each start
// gets the setting as it is at that moment.

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

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

// applyLadoLaunchEnv sets LADO's launch values on env. They win over the
// project's and the daemon's environment, so an inherited
// CLAUDE_CODE_ENABLE_CFC=1 cannot turn Chrome on for a LADO worker.
func (m *Manager) applyLadoLaunchEnv(rec domain.SessionRecord, harness domain.AgentHarness, env map[string]string) {
	if env == nil || m.ladoClaudeChrome == nil {
		return
	}
	if value, ok := m.ladoClaudeChrome.LaunchEnv(rec, harness); ok {
		setProtectedEnv(env, domain.LadoClaudeChromeEnv, value, envKeysCaseInsensitive)
	}
}
