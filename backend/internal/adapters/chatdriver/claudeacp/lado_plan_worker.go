package claudeacp

// LADO fork: a LADO plan worker's Claude Code session runs with the read-only
// tool lists of an AO reviewer (docs/LJY-PRD/TDD.md §5.1). The session
// manager marks its environment (domain.LadoPlanWorkerEnv), and the lists go
// to claude-agent-acp in the session's _meta.claudeCode.options, which it
// passes to the Claude Agent SDK. AO's hook command decides each tool call
// as well, so a prompt never waits for a person.

import (
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// withLadoPlanWorkerTools adds a plan worker's tool lists to meta; any other
// session's meta comes back as it is.
func withLadoPlanWorkerTools(cfg acpdriver.LaunchConfig, meta map[string]any) map[string]any {
	if cfg.Env[domain.LadoPlanWorkerEnv] != "1" {
		return meta
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["claudeCode"] = map[string]any{
		"options": map[string]any{
			"allowedTools":    append([]string(nil), domain.LadoPlanWorkerAllowedTools...),
			"disallowedTools": append([]string(nil), domain.LadoPlanWorkerDisallowedTools...),
		},
	}
	return meta
}
