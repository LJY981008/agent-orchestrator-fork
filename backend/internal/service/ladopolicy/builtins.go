// Package ladopolicy is LADO's rule policy (docs/LJY-PRD/HARNESS.md §3-§8):
// the built-in rules, what each agent can honor, the AO project instructions
// that LADO shows next to its own rules, and resolution, which turns them
// into the rules that apply to a project or a task and their snapshot.
package ladopolicy

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// builtinActor is the audit actor of a built-in rule's first version.
const builtinActor = "lado"

// Builtins returns the rules LADO seeds at daemon start: the protected gates,
// which are always on, and a Korean commit rule that starts off. Each is
// version 1 by "lado"; the service adds the version id and the times. Every
// call builds new rules, so the caller may change them.
func Builtins() []domain.LadoRule {
	return []domain.LadoRule{
		builtin(domain.LadoRule{ID: domain.LadoRulePlanApproval, Protected: true, Global: domain.LadoBindingOn}, domain.LadoRuleContent{
			Name:        "Plan approval required",
			Category:    "workflow",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementWorkflowGate},
		}),
		builtin(domain.LadoRule{ID: domain.LadoRulePermissionCeiling, Protected: true, Global: domain.LadoBindingOn}, domain.LadoRuleContent{
			Name:        "Permission ceiling",
			Category:    "tools",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementToolPolicy},
			// A domain.LadoCeilingConfig. The ceiling starts at auto: Codex
			// then asks before commits, pushes and network calls
			// (docs/LJY-PRD/TDD.md §6.6).
			Config: json.RawMessage(`{"mode":"auto"}`),
		}),
		builtin(domain.LadoRule{ID: domain.LadoRuleKoreanCommits, Global: domain.LadoBindingOff}, domain.LadoRuleContent{
			Name:        "Commit messages in Korean",
			Category:    "git",
			Instruction: "Write Git commit subjects and bodies primarily in Korean. Conventional Commit prefixes such as feat, fix, docs, refactor, test and chore may stay in English.",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementPrompt},
		}),
		// The desktop app's presence channel enforces it (step 7). Its
		// instruction says what needs confirmation; without prompt
		// enforcement it never reaches a worker.
		builtin(domain.LadoRule{ID: domain.LadoRuleSetupPresence, Protected: true, Global: domain.LadoBindingOn}, domain.LadoRuleContent{
			Name:        "Local setup changes need the desktop app's confirmation",
			Instruction: "Adopting or restoring local setup, and every change but a drift revert to a hook script, an MCP server, a setting outside the safe keys, or a command, skill or agent that gives itself tools or hooks or runs a shell command, need a confirmation in the AO desktop app.",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementWorkflowGate},
		}),
	}
}

// builtin completes a built-in rule with version 1 of content, normalized as
// the store keeps it.
func builtin(rule domain.LadoRule, content domain.LadoRuleContent) domain.LadoRule {
	content = content.Normalize()
	rule.Builtin = true
	rule.Version = domain.LadoRuleVersion{
		RuleID:  rule.ID,
		Number:  1,
		Content: content,
		SHA256:  content.SHA256(),
		Actor:   builtinActor,
	}
	return rule
}
