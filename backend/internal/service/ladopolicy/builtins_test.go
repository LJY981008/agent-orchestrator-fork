package ladopolicy_test

import (
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

// koreanCommitsInstruction is the step 5 plan's text for the Korean commit
// rule (docs/work-history/2026-10-22-lado-rules-core-plan.md §5).
const koreanCommitsInstruction = "Write Git commit subjects and bodies primarily in Korean. Conventional Commit prefixes such as feat, fix, docs, refactor, test and chore may stay in English."

// setupPresenceInstruction says what setup.change.requires_presence covers
// (step 7 plan decision i). It names no prompt enforcement, so no worker gets
// it.
const setupPresenceInstruction = "Adopting or restoring local setup, and every change but a drift revert to a hook script, an MCP server, a setting outside the safe keys, or a command, skill or agent that gives itself tools or hooks or runs a shell command, need a confirmation in the AO desktop app."

func TestBuiltinsAreTheStepFiveCatalog(t *testing.T) {
	wants := []struct {
		id          domain.LadoRuleID
		name        string
		category    string
		instruction string
		enforcement domain.LadoEnforcement
		config      string
		protected   bool
		global      domain.LadoBindingState
	}{
		{domain.LadoRulePlanApproval, "Plan approval required", "workflow", "", domain.LadoEnforcementWorkflowGate, `{}`, true, domain.LadoBindingOn},
		{domain.LadoRulePermissionCeiling, "Permission ceiling", "tools", "", domain.LadoEnforcementToolPolicy, `{"mode":"auto"}`, true, domain.LadoBindingOn},
		{domain.LadoRuleKoreanCommits, "Commit messages in Korean", "git", koreanCommitsInstruction, domain.LadoEnforcementPrompt, `{}`, false, domain.LadoBindingOff},
		{domain.LadoRuleSetupPresence, "Local setup changes need the desktop app's confirmation", "", setupPresenceInstruction, domain.LadoEnforcementWorkflowGate, `{}`, true, domain.LadoBindingOn},
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
		if !slices.Equal(content.Enforcement, []domain.LadoEnforcement{want.enforcement}) {
			t.Errorf("%s: enforcement %v, want only %q", rule.ID, content.Enforcement, want.enforcement)
		}
		if string(content.Config) != want.config || len(content.Conflicts) != 0 {
			t.Errorf("%s: config %s, conflicts %v; want config %s and no conflicts", rule.ID, content.Config, content.Conflicts, want.config)
		}
		version := rule.Version
		if version.RuleID != want.id || version.Number != 1 || version.Actor != "lado" {
			t.Errorf("%s: version rule %q, number %d, actor %q; want %q, 1, lado", rule.ID, version.RuleID, version.Number, version.Actor, want.id)
		}
		if version.SHA256 != content.SHA256() {
			t.Errorf("%s: version sha256 %s, want Content.SHA256() %s", rule.ID, version.SHA256, content.SHA256())
		}
		// The service adds the version id and the times when it seeds.
		if version.ID != "" || !version.CreatedAt.IsZero() || !rule.CreatedAt.IsZero() || !rule.ArchivedAt.IsZero() {
			t.Errorf("%s: version id %q, times %v %v %v; want them empty", rule.ID, version.ID, version.CreatedAt, rule.CreatedAt, rule.ArchivedAt)
		}
		if rule.Protected {
			protected++
		}
	}
	if protected != 3 {
		t.Errorf("%d protected built-ins, want 3", protected)
	}

	// Each call builds new rules, so a caller that changes one cannot change
	// the next call's result.
	rules[1].Version.Content.Config[2] = 'X'
	if again := ladopolicy.Builtins(); string(again[1].Version.Content.Config) != `{"mode":"auto"}` {
		t.Errorf("a changed result leaked into the next call: ceiling config %s", again[1].Version.Content.Config)
	}
}
