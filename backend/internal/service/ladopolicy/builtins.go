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
		// The plan workers' role instructions (docs/LJY-PRD/TDD.md §5.1): they
		// reach the designer and the critic sessions only and stay out of
		// resolution, so editing them changes no approval. The developer
		// edits their text; they take no binding.
		{rule: builtin(domain.LadoRule{ID: domain.LadoRulePlanDesigner, Global: domain.LadoBindingOn}, domain.LadoRuleContent{
			Name:        "Plan designer",
			Category:    domain.LadoRuleCategoryPlanning,
			Instruction: planDesignerInstruction,
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementPlanRole},
		})},
		{rule: builtin(domain.LadoRule{ID: domain.LadoRulePlanCritic, Global: domain.LadoBindingOn}, domain.LadoRuleContent{
			Name:        "Plan critic",
			Category:    domain.LadoRuleCategoryPlanning,
			Instruction: planCriticInstruction,
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementPlanRole},
		})},
	}
}

// planDesignerInstruction is the designer's built-in instruction. LADO adds
// the plan format, the issue, the rules and how to submit to every request.
const planDesignerInstruction = "You design the plan for one issue; another agent carries it out after the developer approves it, without asking you anything. " +
	"Read the issue, the rules and the code before you decide. Cover the scope, the approach, the decisions with their reasons and the questions left open. " +
	"List the steps in the order they run, and make every acceptance criterion something a test or a command can check. " +
	"Keep affected_paths as narrow as the work allows, and declare change_kinds for any database schema, external API, authentication or data deletion change. " +
	"Name the test commands, the risks and how the work handles them. " +
	"Write the plan's prose in Korean and keep code, paths and commands as they are. " +
	"End the plan with a \"## 계획 검수\" section that answers the review the way LADO asks."

// planCriticInstruction is the critic's built-in instruction. LADO gives the
// critic the draft only, with how to submit its findings.
const planCriticInstruction = "You review a plan adversarially before anyone carries it out. Read it as someone trying to break it, and check its claims against the repository. " +
	"Raise a finding as P1 or major only when you can describe how the work fails because of it: a missing requirement, a wrong fact about the code, " +
	"steps in the wrong order or a missing dependency, an acceptance criterion no test or command can check, a security or data loss risk, or scope beyond the issue. " +
	"Everything else is minor. Give each finding an id, its severity, a title, the failure scenario, where in the plan or the code it is, and a suggestion."

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
