package ladopolicy_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

// koreanCommitsInstruction is the step 5 plan's text for the Korean commit
// rule (docs/work-history/2026-10-22-lado-rules-core-plan.md §5).
const koreanCommitsInstruction = "Write Git commit subjects and bodies primarily in Korean. Conventional Commit prefixes such as feat, fix, docs, refactor, test and chore may stay in English."

// koreanCommitsConfig is the Korean commit rule's config from its built-in
// version 2 (step 8 plan decision o): the language and the Conventional
// Commit types whose prefix the validator strips.
const koreanCommitsConfig = `{"language":"ko","allowPrefixes":["feat","fix","docs","refactor","test","chore","style","perf","build","ci","revert"]}`

// protectedPathsConfig is the protected paths' config as LADO seeds it.
const protectedPathsConfig = `{"globs":[".github/**",".gitlab-ci.yml",".gitlab/**","**/CLAUDE.md","**/AGENTS.md",".claude/**",".codex/**",".mcp.json",".ao/**"]}`

// setupPresenceInstruction says what setup.change.requires_presence covers
// (step 7 plan decision i). It names no prompt enforcement, so no worker gets
// it.
const setupPresenceInstruction = "Adopting or restoring local setup, and every change but a drift revert to a hook script, an MCP server, a setting outside the safe keys, or a command, skill or agent that gives itself tools or hooks or runs a shell command, need a confirmation in the AO desktop app."

// The instructions of the protected gates of a pull request (step 8 plan
// decision o), which tell the worker what LADO checks.
const (
	integrationApprovalInstruction = "Never merge your pull request or ask anyone to merge it; the developer approves the integration in LADO."
	noDirectWriteInstruction       = "Never push to, reset or update the base branch, locally or on the remote; work only on your own branch and open a pull request."
	testsMustPassInstruction       = "Before you push, run the project's tests and checks; LADO runs the project's check commands and reads the required CI checks on your pull request's head."
	protectedPathsInstruction      = "Do not change protected paths unless the approved plan names them."
)

// rules0Instruction is a plan worker role's built-in instruction, which
// TestPlanRoleInstructionsSayWhatTheRoleDoes checks.
func rules0Instruction(id domain.LadoRuleID) string {
	for _, rule := range ladopolicy.Builtins() {
		if rule.ID == id {
			return rule.Version.Content.Instruction
		}
	}
	return ""
}

// TestBuiltinsIncludeTheVerificationGates pins the built-in catalog: the
// rules of steps 5 and 7, the Korean commit rule at its built-in version 2
// with the commit message validator, the four protected gates of a pull
// request (docs/LJY-PRD/HARNESS.md §14) and the plan worker roles'
// instructions. The English names are the ones the renderer's rule table
// matches.
func TestBuiltinsIncludeTheVerificationGates(t *testing.T) {
	const (
		gate      = domain.LadoEnforcementWorkflowGate
		tools     = domain.LadoEnforcementToolPolicy
		validator = domain.LadoEnforcementValidator
		prompt    = domain.LadoEnforcementPrompt
		planRole  = domain.LadoEnforcementPlanRole
		on        = domain.LadoBindingOn
		off       = domain.LadoBindingOff
	)
	wants := []struct {
		id          domain.LadoRuleID
		name        string
		category    string
		instruction string
		enforcement []domain.LadoEnforcement
		strength    domain.LadoStrength
		config      string
		protected   bool
		global      domain.LadoBindingState
	}{
		{domain.LadoRulePlanApproval, "Plan approval required", "workflow", "", []domain.LadoEnforcement{gate}, domain.LadoStrengthPrevents, `{}`, true, on},
		{domain.LadoRulePermissionCeiling, "Permission ceiling", "tools", "", []domain.LadoEnforcement{tools}, domain.LadoStrengthPrevents, `{"mode":"auto"}`, true, on},
		{domain.LadoRuleKoreanCommits, "Commit messages in Korean", "git", koreanCommitsInstruction, []domain.LadoEnforcement{prompt, validator}, domain.LadoStrengthDetects, koreanCommitsConfig, false, off},
		{domain.LadoRuleSetupPresence, "Local setup changes need the desktop app's confirmation", "", setupPresenceInstruction, []domain.LadoEnforcement{gate}, domain.LadoStrengthPrevents, `{}`, true, on},
		{domain.LadoRuleIntegrationApproval, "Integration approval required", "workflow", integrationApprovalInstruction, []domain.LadoEnforcement{gate, prompt}, domain.LadoStrengthPrevents, `{}`, true, on},
		{domain.LadoRuleNoDirectWrite, "No direct writes to the base branch", "git", noDirectWriteInstruction, []domain.LadoEnforcement{gate, prompt}, domain.LadoStrengthPrevents, `{}`, true, on},
		{domain.LadoRuleTestsMustPass, "Tests must pass", "workflow", testsMustPassInstruction, []domain.LadoEnforcement{validator, prompt}, domain.LadoStrengthDetects, `{}`, true, on},
		{domain.LadoRuleProtectedPaths, "Protected paths", "git", protectedPathsInstruction, []domain.LadoEnforcement{validator, prompt}, domain.LadoStrengthDetects, protectedPathsConfig, true, on},
		{domain.LadoRulePlanDesigner, "Plan designer", "planning", rules0Instruction(domain.LadoRulePlanDesigner), []domain.LadoEnforcement{planRole}, domain.LadoStrengthAdvisory, `{}`, false, on},
		{domain.LadoRulePlanCritic, "Plan critic", "planning", rules0Instruction(domain.LadoRulePlanCritic), []domain.LadoEnforcement{planRole}, domain.LadoStrengthAdvisory, `{}`, false, on},
	}

	rules := ladopolicy.Builtins()
	if len(rules) != len(wants) {
		t.Fatalf("Builtins() returned %d rules, want %d", len(rules), len(wants))
	}
	protected := 0
	for i, want := range wants {
		rule := rules[i]
		if rule.ID != want.id {
			t.Fatalf("rule %d is %q, want %q", i, rule.ID, want.id)
		}
		if !rule.Builtin || rule.Protected != want.protected || rule.Global != want.global || len(rule.Overrides) != 0 {
			t.Errorf("%s: builtin %v, protected %v, global %q, overrides %v; want builtin, protected %v, global %q, no overrides",
				rule.ID, rule.Builtin, rule.Protected, rule.Global, rule.Overrides, want.protected, want.global)
		}
		content := rule.Version.Content
		if content.Name != want.name || content.Category != want.category || content.Instruction != want.instruction {
			t.Errorf("%s: name %q, category %q, instruction %q; want %q, %q, %q",
				rule.ID, content.Name, content.Category, content.Instruction, want.name, want.category, want.instruction)
		}
		if !slices.Equal(content.Enforcement, want.enforcement) || content.Strength() != want.strength {
			t.Errorf("%s: enforcement %v (%s), want %v (%s)", rule.ID, content.Enforcement, content.Strength(), want.enforcement, want.strength)
		}
		if string(content.Config) != want.config || len(content.Conflicts) != 0 {
			t.Errorf("%s: config %s, conflicts %v; want config %s and no conflicts", rule.ID, content.Config, content.Conflicts, want.config)
		}
		version := rule.Version
		if version.RuleID != want.id || version.Number != 1 || version.Actor != "lado" {
			t.Errorf("%s: version rule %q, number %d, actor %q; want %q, 1, lado", rule.ID, version.RuleID, version.Number, version.Actor, want.id)
		}
		if version.SHA256 != content.SHA256() || version.SHA256 != content.Normalize().SHA256() {
			t.Errorf("%s: version sha256 %s, want Content.SHA256() %s of the normalized content", rule.ID, version.SHA256, content.SHA256())
		}
		// The service adds the version id and the times when it seeds.
		if version.ID != "" || !version.CreatedAt.IsZero() || !rule.CreatedAt.IsZero() || !rule.ArchivedAt.IsZero() {
			t.Errorf("%s: version id %q, times %v %v %v; want them empty", rule.ID, version.ID, version.CreatedAt, rule.CreatedAt, rule.ArchivedAt)
		}
		if rule.Protected {
			protected++
		}
	}
	if protected != 7 {
		t.Errorf("%d protected built-ins, want 7", protected)
	}

	// The configs are the domain's defaults: nine protected globs and the
	// Korean commit validator's settings.
	var paths domain.LadoProtectedPathsConfig
	if err := json.Unmarshal(rules[7].Version.Content.Config, &paths); err != nil || len(paths.Globs) != 9 || !slices.Equal(paths.Globs, domain.DefaultLadoProtectedPaths()) {
		t.Errorf("protected paths = %v (err %v), want the 9 defaults %v", paths.Globs, err, domain.DefaultLadoProtectedPaths())
	}
	var locale domain.LadoCommitLocaleConfig
	if err := json.Unmarshal(rules[2].Version.Content.Config, &locale); err != nil || locale.Language != "ko" ||
		!slices.Equal(locale.AllowPrefixes, domain.DefaultLadoCommitLocaleConfig().AllowPrefixes) {
		t.Errorf("Korean config = %+v (err %v), want %+v", locale, err, domain.DefaultLadoCommitLocaleConfig())
	}

	// Each call builds new rules, so a caller that changes one cannot change
	// the next call's result.
	rules[1].Version.Content.Config[2] = 'X'
	rules[7].Version.Content.Config[2] = 'X'
	again := ladopolicy.Builtins()
	if string(again[1].Version.Content.Config) != `{"mode":"auto"}` || string(again[7].Version.Content.Config) != protectedPathsConfig {
		t.Errorf("a changed result leaked into the next call: configs %s and %s", again[1].Version.Content.Config, again[7].Version.Content.Config)
	}
}
