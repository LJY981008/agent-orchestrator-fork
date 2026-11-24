package sessionmanager

// LADO fork: lets LADO's stage settings (issue #103) read a Claude family
// alias's efforts the way a spawn does. manager.go stays as upstream has it.

import "github.com/aoagents/agent-orchestrator/backend/internal/ports"

// NewestClaudeFamilyModel returns the newest concrete catalog model of the
// family a bare alias such as "opus" or "opus[1m]" names, the model whose
// efforts a Claude Code spawn checks the alias's effort against; nil when
// alias is not a family alias or the catalog has no such model.
func NewestClaudeFamilyModel(models []ports.AgentModelInfo, alias string) *ports.AgentModelInfo {
	return newestClaudeFamilyModel(models, alias)
}
