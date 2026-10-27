// Package ladopolicy is LADO's rule policy (docs/LJY-PRD/HARNESS.md §3-§8):
// the built-in rules, what each agent can honor, the AO project instructions
// that LADO shows next to its own rules, and resolution, which turns them
// into the rules that apply to a project or a task and their snapshot.
package ladopolicy

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// builtinActor is the audit actor of a built-in rule's first version and of
// its upgrades.
const builtinActor = "lado"

// builtinDef is a built-in rule with its current content, and the contents
// of its earlier built-in versions, oldest first. Changing a built-in's
// content moves the old content into previous, so EnsureBuiltins upgrades
// the stores that still hold it unchanged.
type builtinDef struct {
	rule     domain.LadoRule
	previous []domain.LadoRuleContent
}

// Builtins returns the rules LADO seeds at daemon start: the protected gates,
// which are always on, and a Korean commit rule that starts off. Each is
// version 1 by "lado" with the current built-in content; the service adds the
// version id and the times. Every call builds new rules, so the caller may
// change them.
func Builtins() []domain.LadoRule {
	defs := builtinDefs()
	rules := make([]domain.LadoRule, 0, len(defs))
	for _, def := range defs {
		rules = append(rules, def.rule)
	}
	return rules
}

// builtinDefs returns the built-in rules with their earlier contents, in the
// order LADO seeds them.
func builtinDefs() []builtinDef {
	koreanV1 := domain.LadoRuleContent{
		Name:        "Commit messages in Korean",
		Category:    "git",
		Instruction: "Write Git commit subjects and bodies primarily in Korean. Conventional Commit prefixes such as feat, fix, docs, refactor, test and chore may stay in English.",
		Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementPrompt},
	}
	// Version 2 (step 8) adds the commit message validator and its settings,
	// a domain.LadoCommitLocaleConfig.
	koreanV2 := koreanV1
	koreanV2.Enforcement = []domain.LadoEnforcement{domain.LadoEnforcementPrompt, domain.LadoEnforcementValidator}
	koreanV2.Config = configJSON(domain.DefaultLadoCommitLocaleConfig())
	return []builtinDef{
		{rule: builtin(domain.LadoRule{ID: domain.LadoRulePlanApproval, Protected: true, Global: domain.LadoBindingOn}, domain.LadoRuleContent{
			Name:        "Plan approval required",
			Category:    "workflow",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementWorkflowGate},
		})},
		{rule: builtin(domain.LadoRule{ID: domain.LadoRulePermissionCeiling, Protected: true, Global: domain.LadoBindingOn}, domain.LadoRuleContent{
			Name:        "Permission ceiling",
			Category:    "tools",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementToolPolicy},
			// A domain.LadoCeilingConfig. The ceiling starts at auto: Codex
			// then asks before commits, pushes and network calls
			// (docs/LJY-PRD/TDD.md §6.6).
			Config: json.RawMessage(`{"mode":"auto"}`),
		})},
		{
			rule:     builtin(domain.LadoRule{ID: domain.LadoRuleKoreanCommits, Global: domain.LadoBindingOff}, koreanV2),
			previous: []domain.LadoRuleContent{koreanV1},
		},
		// The desktop app's presence channel enforces it (step 7). Its
		// instruction says what needs confirmation; without prompt
		// enforcement it never reaches a worker.
		{rule: builtin(domain.LadoRule{ID: domain.LadoRuleSetupPresence, Protected: true, Global: domain.LadoBindingOn}, domain.LadoRuleContent{
			Name:        "Local setup changes need the desktop app's confirmation",
			Instruction: "Adopting or restoring local setup, and every change but a drift revert to a hook script, an MCP server, a setting outside the safe keys, or a command, skill or agent that gives itself tools or hooks or runs a shell command, need a confirmation in the AO desktop app.",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementWorkflowGate},
		})},
		// The gates of a pull request (docs/LJY-PRD/HARNESS.md §14, step 8):
		// LADO's verification and integration approval enforce them, and
		// their instructions tell the worker what LADO checks.
		{rule: builtin(domain.LadoRule{ID: domain.LadoRuleIntegrationApproval, Protected: true, Global: domain.LadoBindingOn}, domain.LadoRuleContent{
			Name:        "Integration approval required",
			Category:    "workflow",
			Instruction: "Never merge your pull request or ask anyone to merge it; the developer approves the integration in LADO.",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementWorkflowGate, domain.LadoEnforcementPrompt},
		})},
		{rule: builtin(domain.LadoRule{ID: domain.LadoRuleNoDirectWrite, Protected: true, Global: domain.LadoBindingOn}, domain.LadoRuleContent{
			Name:        "No direct writes to the base branch",
			Category:    "git",
			Instruction: "Never push to, reset or update the base branch, locally or on the remote; work only on your own branch and open a pull request.",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementWorkflowGate, domain.LadoEnforcementPrompt},
		})},
		{rule: builtin(domain.LadoRule{ID: domain.LadoRuleTestsMustPass, Protected: true, Global: domain.LadoBindingOn}, domain.LadoRuleContent{
			Name:        "Tests must pass",
			Category:    "workflow",
			Instruction: "Before you push, run the project's tests and checks; LADO runs the project's check commands and reads the required CI checks on your pull request's head.",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementValidator, domain.LadoEnforcementPrompt},
		})},
		// Its globs change only with the desktop app's confirmation; the
		// worker's text lists them (promptText).
		{rule: builtin(domain.LadoRule{ID: domain.LadoRuleProtectedPaths, Protected: true, Global: domain.LadoBindingOn}, domain.LadoRuleContent{
			Name:        "Protected paths",
			Category:    "git",
			Instruction: "Do not change protected paths unless the approved plan names them.",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementValidator, domain.LadoEnforcementPrompt},
			Config:      configJSON(domain.LadoProtectedPathsConfig{Globs: domain.DefaultLadoProtectedPaths()}),
		})},
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

// configJSON encodes a rule config. Configs hold strings and string lists,
// which always encode.
func configJSON(config any) json.RawMessage {
	data, _ := json.Marshal(config)
	return data
}
