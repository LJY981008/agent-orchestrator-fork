package ladopolicy_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

const (
	projectA domain.ProjectID  = "proj-a"
	projectB domain.ProjectID  = "proj-b"
	taskA    domain.LadoTaskID = "task-a"
	taskB    domain.LadoTaskID = "task-b"

	blocking = domain.LadoSeverityBlocking
	warning  = domain.LadoSeverityWarning
	info     = domain.LadoSeverityInfo
)

// seededAt is when the tests' daemon seeded the built-in rules.
var seededAt = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

// claudeCode is the agent most tests run tasks with. It honors every
// capability, so only the rules under test raise problems.
var claudeCode = ladopolicy.AgentChoice{Harness: domain.HarnessClaudeCode, Model: "opus", Source: "task"}

// seeded returns the built-in rules as the store lists them after seeding,
// with version ids and creation times.
func seeded() []domain.LadoRule {
	rules := ladopolicy.Builtins()
	for i := range rules {
		rules[i].CreatedAt = seededAt
		rules[i].Version.ID = versionID(rules[i].ID, 1)
		rules[i].Version.CreatedAt = seededAt
	}
	return rules
}

func versionID(id domain.LadoRuleID, number int) domain.LadoRuleVersionID {
	return domain.LadoRuleVersionID(fmt.Sprintf("ver-%s-%d", id, number))
}

// promptRule is version 1 of a developer's prompt rule, created minutes after
// the built-ins. It is off until a test binds it.
func promptRule(id domain.LadoRuleID, name, instruction string, minutes int) domain.LadoRule {
	rule := domain.LadoRule{ID: id, Global: domain.LadoBindingOff, CreatedAt: seededAt.Add(time.Duration(minutes) * time.Minute)}
	return withContent(rule, domain.LadoRuleContent{
		Name:        name,
		Category:    "team",
		Instruction: instruction,
		Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementPrompt},
	})
}

// withContent gives the rule its next version, which says content.
func withContent(rule domain.LadoRule, content domain.LadoRuleContent) domain.LadoRule {
	content = content.Normalize()
	number := rule.Version.Number + 1
	rule.Version = domain.LadoRuleVersion{
		ID: versionID(rule.ID, number), RuleID: rule.ID, Number: number,
		Content: content, SHA256: content.SHA256(), Actor: "api", CreatedAt: rule.CreatedAt,
	}
	return rule
}

// ruleIn returns the rule with id in rules, to change in place.
func ruleIn(t *testing.T, rules []domain.LadoRule, id domain.LadoRuleID) *domain.LadoRule {
	t.Helper()
	i := slices.IndexFunc(rules, func(r domain.LadoRule) bool { return r.ID == id })
	if i < 0 {
		t.Fatalf("no rule %q", id)
	}
	return &rules[i]
}

// setCeiling makes mode the permission ceiling of rules.
func setCeiling(t *testing.T, rules []domain.LadoRule, mode domain.PermissionMode) {
	t.Helper()
	ceiling := ruleIn(t, rules, domain.LadoRulePermissionCeiling)
	content := ceiling.Version.Content
	content.Config = []byte(fmt.Sprintf(`{"mode":%q}`, mode))
	*ceiling = withContent(*ceiling, content)
}

func projectBinding(id domain.ProjectID, state domain.LadoBindingState) domain.LadoRuleBinding {
	return domain.LadoRuleBinding{Scope: domain.LadoScopeProject, ScopeID: string(id), State: state}
}

func taskBinding(id domain.LadoTaskID, state domain.LadoBindingState) domain.LadoRuleBinding {
	return domain.LadoRuleBinding{Scope: domain.LadoScopeTask, ScopeID: string(id), State: state}
}

// taskInput resolves rules for task-a of proj-a, run by agent.
func taskInput(rules []domain.LadoRule, agent ladopolicy.AgentChoice) ladopolicy.Input {
	return ladopolicy.Input{Rules: rules, ProjectID: projectA, TaskID: taskA, Agent: agent, AOBuild: "1.0.0"}
}

func entryOf(r ladopolicy.Resolution, id domain.LadoRuleID) (ladopolicy.Entry, bool) {
	for _, e := range r.Entries {
		if e.Rule.ID == id {
			return e, true
		}
	}
	return ladopolicy.Entry{}, false
}

func entryIDs(r ladopolicy.Resolution) []domain.LadoRuleID {
	ids := []domain.LadoRuleID{}
	for _, e := range r.Entries {
		ids = append(ids, e.Rule.ID)
	}
	return ids
}

// problemsWith returns the problems whose code starts with one of prefixes,
// in resolution order.
func problemsWith(r ladopolicy.Resolution, prefixes ...string) []domain.LadoPolicyProblem {
	var found []domain.LadoPolicyProblem
	for _, p := range r.Problems {
		for _, prefix := range prefixes {
			if strings.HasPrefix(p.Code, prefix) {
				found = append(found, p)
				break
			}
		}
	}
	return found
}

// sameList compares lists, taking nil and empty as the same.
func sameList[T any](got, want []T) bool {
	return len(got) == 0 && len(want) == 0 || reflect.DeepEqual(got, want)
}

func TestResolveBindings(t *testing.T) {
	const (
		on       = domain.LadoBindingOn
		off      = domain.LadoBindingOff
		forceOn  = domain.LadoBindingForceOn
		forceOff = domain.LadoBindingForceOff
		inherit  = domain.LadoBindingInherit
	)
	global := func(state domain.LadoBindingState) ladopolicy.Source {
		return ladopolicy.Source{Scope: domain.LadoScopeGlobal, State: state}
	}
	fromProject := func(state domain.LadoBindingState) ladopolicy.Source {
		return ladopolicy.Source{Scope: domain.LadoScopeProject, ScopeID: string(projectA), State: state}
	}
	fromTask := func(state domain.LadoBindingState) ladopolicy.Source {
		return ladopolicy.Source{Scope: domain.LadoScopeTask, ScopeID: string(taskA), State: state}
	}
	cases := []struct {
		name      string
		protected bool
		global    domain.LadoBindingState
		overrides []domain.LadoRuleBinding
		// projectScope resolves for proj-a instead of task-a.
		projectScope bool
		wantOn       bool
		wantSource   ladopolicy.Source
		wantProject  domain.LadoBindingState
		wantTask     domain.LadoBindingState
	}{
		{
			name:   "PRD gate 3 and HARNESS 15.1: global on, project force_off, task force_on resolves on from the task",
			global: on, overrides: []domain.LadoRuleBinding{projectBinding(projectA, forceOff), taskBinding(taskA, forceOn)},
			wantOn: true, wantSource: fromTask(forceOn), wantProject: forceOff, wantTask: forceOn,
		},
		{
			name:   "HARNESS 15.2: project inherit follows global on",
			global: on,
			wantOn: true, wantSource: global(on), wantProject: inherit, wantTask: inherit,
		},
		{
			name:   "HARNESS 15.2: project inherit follows global off",
			global: off,
			wantOn: false, wantSource: global(off), wantProject: inherit, wantTask: inherit,
		},
		{
			name:   "HARNESS 4: global off, project force_on resolves on",
			global: off, overrides: []domain.LadoRuleBinding{projectBinding(projectA, forceOn)},
			wantOn: true, wantSource: fromProject(forceOn), wantProject: forceOn, wantTask: inherit,
		},
		{
			name:   "HARNESS 4: global on, project force_off, task inherit resolves off",
			global: on, overrides: []domain.LadoRuleBinding{projectBinding(projectA, forceOff)},
			wantOn: false, wantSource: fromProject(forceOff), wantProject: forceOff, wantTask: inherit,
		},
		{
			name:   "task force_off wins over project force_on",
			global: off, overrides: []domain.LadoRuleBinding{projectBinding(projectA, forceOn), taskBinding(taskA, forceOff)},
			wantOn: false, wantSource: fromTask(forceOff), wantProject: forceOn, wantTask: forceOff,
		},
		{
			name:   "bindings of another project and another task are ignored",
			global: on, overrides: []domain.LadoRuleBinding{projectBinding(projectB, forceOff), taskBinding(taskB, forceOff)},
			wantOn: true, wantSource: global(on), wantProject: inherit, wantTask: inherit,
		},
		{
			name:   "project scope ignores task bindings",
			global: off, overrides: []domain.LadoRuleBinding{taskBinding(taskA, forceOn)},
			projectScope: true,
			wantOn:       false, wantSource: global(off), wantProject: inherit, wantTask: inherit,
		},
		{
			name:      "HARNESS 15.3: a protected gate stays on despite project and task force_off",
			protected: true, global: on,
			overrides: []domain.LadoRuleBinding{projectBinding(projectA, forceOff), taskBinding(taskA, forceOff)},
			wantOn:    true, wantSource: global(on), wantProject: forceOff, wantTask: forceOff,
		},
		{
			name:      "HARNESS 15.3: a protected gate stays on when its global binding says off",
			protected: true, global: off,
			wantOn: true, wantSource: global(on), wantProject: inherit, wantTask: inherit,
		},
		{
			// The store's CHECK refuses such rows; resolution does not trust it.
			name:   "a binding with a state its scope cannot hold counts as inherit",
			global: off, overrides: []domain.LadoRuleBinding{projectBinding(projectA, on), taskBinding(taskA, inherit)},
			wantOn: false, wantSource: global(off), wantProject: inherit, wantTask: inherit,
		},
		{
			name:   "a rule without a global binding is off",
			global: "",
			wantOn: false, wantSource: global(off), wantProject: inherit, wantTask: inherit,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rule := promptRule("team.tests", "Tests before commits", "Run the unit tests before each commit.", 1)
			rule.Protected = c.protected
			rule.Global = c.global
			rule.Overrides = c.overrides
			in := taskInput([]domain.LadoRule{rule}, claudeCode)
			if c.projectScope {
				in.TaskID = ""
			}
			entry, ok := entryOf(ladopolicy.Resolve(in), rule.ID)
			if !ok {
				t.Fatal("the rule is missing from the entries")
			}
			if entry.On != c.wantOn || entry.Source != c.wantSource {
				t.Errorf("on %v from %+v, want on %v from %+v", entry.On, entry.Source, c.wantOn, c.wantSource)
			}
			if entry.Project != c.wantProject || entry.Task != c.wantTask {
				t.Errorf("project %q, task %q; want %q, %q", entry.Project, entry.Task, c.wantProject, c.wantTask)
			}
		})
	}

	t.Run("archived rules are left out", func(t *testing.T) {
		kept := promptRule("team.kept", "Kept", "Kept rule.", 1)
		kept.Global = on
		archived := promptRule("team.archived", "Archived", "Archived rule.", 2)
		archived.Global = on
		archived.ArchivedAt = seededAt.Add(time.Hour)
		r := ladopolicy.Resolve(taskInput([]domain.LadoRule{archived, kept}, claudeCode))
		if got := entryIDs(r); !slices.Equal(got, []domain.LadoRuleID{"team.kept"}) {
			t.Errorf("entries %v, want only team.kept", got)
		}
		if strings.Contains(r.Prompt, "Archived rule.") || strings.Contains(string(r.JSON), "team.archived") {
			t.Errorf("the archived rule reached the prompt or the snapshot: %s", r.JSON)
		}
	})

	t.Run("entries follow creation time, then id", func(t *testing.T) {
		rules := append(seeded(),
			promptRule("b.later", "B", "b", 5), promptRule("a.later", "A", "a", 5), promptRule("c.earlier", "C", "c", -1))
		slices.Reverse(rules)
		want := []domain.LadoRuleID{
			"c.earlier", domain.LadoRulePermissionCeiling, domain.LadoRuleKoreanCommits, domain.LadoRulePlanApproval, domain.LadoRuleSetupPresence,
			"a.later", "b.later",
		}
		if got := entryIDs(ladopolicy.Resolve(taskInput(rules, claudeCode))); !slices.Equal(got, want) {
			t.Errorf("entries %v, want %v", got, want)
		}
	})
}

func TestResolveCapabilities(t *testing.T) {
	const (
		prompt     = ladopolicy.CapabilityPrompt
		permission = ladopolicy.CapabilityPermissionMode
	)
	korean := []domain.LadoRuleID{domain.LadoRuleKoreanCommits}
	ceiling := []domain.LadoRuleID{domain.LadoRulePermissionCeiling}
	agentProblem := func(code string, severity domain.LadoProblemSeverity, rules []domain.LadoRuleID, agent domain.AgentHarness, class ladopolicy.CapabilityClass) domain.LadoPolicyProblem {
		return domain.LadoPolicyProblem{Code: code, Severity: severity, Rules: rules, Details: map[string]string{"agent": string(agent), "capability": string(class)}}
	}
	chosen := func(harness domain.AgentHarness) ladopolicy.AgentChoice {
		return ladopolicy.AgentChoice{Harness: harness, Source: "task"}
	}
	both := func(promptStatus, permissionStatus ladopolicy.CapabilityStatus) []ladopolicy.Capability {
		return []ladopolicy.Capability{{Class: prompt, Status: promptStatus}, {Class: permission, Status: permissionStatus}}
	}
	cases := []struct {
		name  string
		agent ladopolicy.AgentChoice
		// promptRules turns the Korean commit rule on, so the worker gets
		// rule text.
		promptRules  bool
		projectScope bool
		wantCaps     []ladopolicy.Capability
		// wantProblems are the LADO_AGENT_* problems.
		wantProblems []domain.LadoPolicyProblem
	}{
		{
			name:  "HARNESS 15.7: cursor cannot take prompt rules and nobody verified its permission modes",
			agent: chosen(domain.HarnessCursor), promptRules: true,
			wantCaps: both(ladopolicy.CapabilityUnsupported, ladopolicy.CapabilityUnverified),
			wantProblems: []domain.LadoPolicyProblem{
				agentProblem("LADO_AGENT_CAPABILITY_UNSUPPORTED", blocking, korean, domain.HarnessCursor, prompt),
				agentProblem("LADO_AGENT_CAPABILITY_UNVERIFIED", blocking, ceiling, domain.HarnessCursor, permission),
			},
		},
		{
			name:     "HARNESS 15.7: without prompt rules cursor still needs permission modes",
			agent:    chosen(domain.HarnessCursor),
			wantCaps: []ladopolicy.Capability{{Class: permission, Status: ladopolicy.CapabilityUnverified}},
			wantProblems: []domain.LadoPolicyProblem{
				agentProblem("LADO_AGENT_CAPABILITY_UNVERIFIED", blocking, ceiling, domain.HarnessCursor, permission),
			},
		},
		{
			name:  "fx takes the rule text degraded",
			agent: chosen(domain.HarnessFX), promptRules: true,
			wantCaps: both(ladopolicy.CapabilityDegraded, ladopolicy.CapabilityUnverified),
			wantProblems: []domain.LadoPolicyProblem{
				agentProblem("LADO_AGENT_CAPABILITY_UNVERIFIED", blocking, ceiling, domain.HarnessFX, permission),
				agentProblem("LADO_AGENT_CAPABILITY_DEGRADED", warning, korean, domain.HarnessFX, prompt),
			},
		},
		{
			name:  "gemini is unverified for both classes",
			agent: chosen(domain.HarnessGemini), promptRules: true,
			wantCaps: both(ladopolicy.CapabilityUnverified, ladopolicy.CapabilityUnverified),
			wantProblems: []domain.LadoPolicyProblem{
				agentProblem("LADO_AGENT_CAPABILITY_UNVERIFIED", blocking, ceiling, domain.HarnessGemini, permission),
				agentProblem("LADO_AGENT_CAPABILITY_UNVERIFIED", blocking, korean, domain.HarnessGemini, prompt),
			},
		},
		{
			name:  "claude-code honors both classes",
			agent: chosen(domain.HarnessClaudeCode), promptRules: true,
			wantCaps: both(ladopolicy.CapabilitySupported, ladopolicy.CapabilitySupported),
		},
		{
			name:  "codex honors both classes",
			agent: chosen(domain.HarnessCodex), promptRules: true,
			wantCaps: both(ladopolicy.CapabilitySupported, ladopolicy.CapabilitySupported),
		},
		{
			name:         "no agent blocks once, without capability checks",
			promptRules:  true,
			wantProblems: []domain.LadoPolicyProblem{{Code: "LADO_AGENT_NOT_CHOSEN", Severity: blocking}},
		},
		{
			name:  "project scope checks no agent",
			agent: chosen(domain.HarnessCursor), promptRules: true, projectScope: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rules := seeded()
			if c.promptRules {
				ruleIn(t, rules, domain.LadoRuleKoreanCommits).Global = domain.LadoBindingOn
			}
			in := taskInput(rules, c.agent)
			if c.projectScope {
				in.TaskID = ""
			}
			r := ladopolicy.Resolve(in)
			if r.Capabilities == nil {
				t.Error("Capabilities is nil; lists in a resolution are never nil")
			}
			if !sameList(r.Capabilities, c.wantCaps) {
				t.Errorf("capabilities %+v, want %+v", r.Capabilities, c.wantCaps)
			}
			if got := problemsWith(r, "LADO_AGENT_"); !sameList(got, c.wantProblems) {
				t.Errorf("problems %+v, want %+v", got, c.wantProblems)
			}
			wantBlocking := slices.ContainsFunc(c.wantProblems, func(p domain.LadoPolicyProblem) bool { return p.Severity == blocking })
			if r.Blocking() != wantBlocking {
				t.Errorf("Blocking() = %v, want %v (problems %+v)", r.Blocking(), wantBlocking, r.Problems)
			}
		})
	}

	t.Run("a task without an agent runs the project's worker agent", func(t *testing.T) {
		project := domain.ProjectRecord{ID: string(projectA), Config: domain.ProjectConfig{
			AgentConfig: domain.AgentConfig{Model: "project-model"},
			Worker:      domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "gpt-5.1-codex"}},
		}}
		task := domain.LadoTask{ID: taskA, ProjectID: projectA}
		agent := ladopolicy.ChooseAgent(task, project)
		if want := (ladopolicy.AgentChoice{Harness: domain.HarnessCodex, Model: "gpt-5.1-codex", Source: "project"}); agent != want {
			t.Fatalf("ChooseAgent() = %+v, want %+v", agent, want)
		}
		rules := seeded()
		ruleIn(t, rules, domain.LadoRuleKoreanCommits).Global = domain.LadoBindingOn
		r := ladopolicy.Resolve(taskInput(rules, agent))
		wantAgent := ladopolicy.SnapshotAgent{Harness: domain.HarnessCodex, Model: "gpt-5.1-codex", PermissionMode: domain.PermissionModeAuto}
		if r.Content.Agent != wantAgent {
			t.Errorf("snapshot agent %+v, want %+v", r.Content.Agent, wantAgent)
		}
		if got := problemsWith(r, "LADO_AGENT_"); len(got) != 0 {
			t.Errorf("problems %+v, want none for codex", got)
		}

		// Without a worker model the project's model applies, as at AO's spawn.
		project.Config.Worker.AgentConfig.Model = ""
		if got := ladopolicy.ChooseAgent(task, project); got.Model != "project-model" {
			t.Errorf("ChooseAgent().Model = %q, want project-model", got.Model)
		}
		// The task's own choice wins, model included.
		task.Agent, task.Model = " cursor ", " composer "
		if got, want := ladopolicy.ChooseAgent(task, project), (ladopolicy.AgentChoice{Harness: domain.HarnessCursor, Model: "composer", Source: "task"}); got != want {
			t.Errorf("ChooseAgent() = %+v, want %+v", got, want)
		}
		if got := ladopolicy.ChooseAgent(domain.LadoTask{ID: taskA}, domain.ProjectRecord{ID: string(projectA)}); got != (ladopolicy.AgentChoice{}) {
			t.Errorf("ChooseAgent() = %+v without any choice, want none", got)
		}
	})
}

func TestResolveConflicts(t *testing.T) {
	// Created against id order, so problems must sort their rules.
	tabs := promptRule("z.indent.tabs", "Indent with tabs", "Indent with tabs.", 1)
	wide := promptRule("m.lines.wide", "Wide lines", "Allow lines up to 160 columns.", 2)
	spaces := promptRule("a.indent.spaces", "Indent with spaces", "Indent with four spaces.", 3)
	naming := func(rule domain.LadoRule, ids ...domain.LadoRuleID) domain.LadoRule {
		content := rule.Version.Content
		content.Conflicts = ids
		return withContent(rule, content)
	}
	conflict := func(ids ...domain.LadoRuleID) domain.LadoPolicyProblem {
		return domain.LadoPolicyProblem{Code: "LADO_RULES_CONFLICT", Severity: blocking, Rules: ids}
	}
	cases := []struct {
		name  string
		rules []domain.LadoRule
		// off stays off; every other rule is on.
		off  domain.LadoRuleID
		want []domain.LadoPolicyProblem
	}{
		{
			name:  "HARNESS 15.8: two on rules where one names the other block once",
			rules: []domain.LadoRule{spaces, naming(tabs, spaces.ID)},
			want:  []domain.LadoPolicyProblem{conflict(spaces.ID, tabs.ID)},
		},
		{
			name:  "HARNESS 15.8: a newer rule naming an older one blocks too",
			rules: []domain.LadoRule{naming(spaces, tabs.ID), tabs},
			want:  []domain.LadoPolicyProblem{conflict(spaces.ID, tabs.ID)},
		},
		{
			name:  "HARNESS 15.8: rules that name each other still block once",
			rules: []domain.LadoRule{naming(spaces, tabs.ID), naming(tabs, spaces.ID)},
			want:  []domain.LadoPolicyProblem{conflict(spaces.ID, tabs.ID)},
		},
		{
			name:  "an off rule conflicts with nothing",
			rules: []domain.LadoRule{spaces, naming(tabs, spaces.ID)},
			off:   tabs.ID,
		},
		{
			name:  "a conflict with a rule that is not there is no problem",
			rules: []domain.LadoRule{naming(tabs, "x.gone")},
		},
		{
			name:  "each conflicting pair blocks once",
			rules: []domain.LadoRule{spaces, wide, naming(tabs, wide.ID, spaces.ID)},
			want:  []domain.LadoPolicyProblem{conflict(spaces.ID, tabs.ID), conflict(wide.ID, tabs.ID)},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rules := slices.Clone(c.rules)
			for i := range rules {
				if rules[i].ID != c.off {
					rules[i].Global = domain.LadoBindingOn
				}
			}
			r := ladopolicy.Resolve(taskInput(append(seeded(), rules...), claudeCode))
			if got := problemsWith(r, "LADO_RULES_CONFLICT"); !sameList(got, c.want) {
				t.Errorf("problems %+v, want %+v", got, c.want)
			}
			if r.Blocking() != (len(c.want) > 0) {
				t.Errorf("Blocking() = %v with problems %+v", r.Blocking(), r.Problems)
			}
		})
	}
}

func TestResolvePermissionMode(t *testing.T) {
	const (
		acceptEdits = domain.PermissionModeAcceptEdits
		auto        = domain.PermissionModeAuto
		bypass      = domain.PermissionModeBypassPermissions
	)
	ceilingRule := []domain.LadoRuleID{domain.LadoRulePermissionCeiling}
	missing := domain.LadoPolicyProblem{Code: "LADO_CEILING_MISSING", Severity: blocking, Rules: ceilingRule}
	clamped := func(plan, ceiling domain.PermissionMode) domain.LadoPolicyProblem {
		return domain.LadoPolicyProblem{
			Code: "LADO_PLAN_CEILING_CLAMPED", Severity: warning, Rules: ceilingRule,
			Details: map[string]string{"plan": string(plan), "ceiling": string(ceiling)},
		}
	}
	cases := []struct {
		name string
		// config is the ceiling rule's config; empty leaves the rule out.
		config       string
		plan         domain.PermissionMode
		projectScope bool
		wantCeiling  domain.PermissionMode
		wantMode     domain.PermissionMode
		want         []domain.LadoPolicyProblem
	}{
		{name: "a plan above the ceiling is clamped to it", config: `{"mode":"auto"}`, plan: bypass, wantCeiling: auto, wantMode: auto, want: []domain.LadoPolicyProblem{clamped(bypass, auto)}},
		{name: "a plan without a mode runs at the ceiling", config: `{"mode":"auto"}`, wantCeiling: auto, wantMode: auto},
		{name: "a plan asking for default runs at the ceiling", config: `{"mode":"auto"}`, plan: domain.PermissionModeDefault, wantCeiling: auto, wantMode: auto},
		{name: "a plan below the ceiling keeps its mode", config: `{"mode":"auto"}`, plan: acceptEdits, wantCeiling: auto, wantMode: acceptEdits},
		{name: "a plan at the ceiling keeps it", config: `{"mode":"auto"}`, plan: auto, wantCeiling: auto, wantMode: auto},
		{name: "a higher ceiling leaves the plan's mode", config: `{"mode":"bypass-permissions"}`, plan: auto, wantCeiling: bypass, wantMode: auto},
		{name: "a bypass ceiling runs a plan without a mode at bypass", config: `{"mode":"bypass-permissions"}`, wantCeiling: bypass, wantMode: bypass},
		{name: "a missing ceiling rule falls back to accept-edits and blocks", wantCeiling: acceptEdits, wantMode: acceptEdits, want: []domain.LadoPolicyProblem{missing}},
		{name: "the fallback ceiling clamps the plan too", plan: auto, wantCeiling: acceptEdits, wantMode: acceptEdits, want: []domain.LadoPolicyProblem{missing, clamped(auto, acceptEdits)}},
		{name: "a ceiling naming default counts as missing", config: `{"mode":"default"}`, wantCeiling: acceptEdits, wantMode: acceptEdits, want: []domain.LadoPolicyProblem{missing}},
		{name: "a ceiling without a mode counts as missing", config: `{}`, wantCeiling: acceptEdits, wantMode: acceptEdits, want: []domain.LadoPolicyProblem{missing}},
		{name: "project scope runs at the ceiling", config: `{"mode":"auto"}`, plan: acceptEdits, projectScope: true, wantCeiling: auto, wantMode: auto},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rules := seeded()
			if c.config == "" {
				rules = slices.DeleteFunc(rules, func(r domain.LadoRule) bool { return r.ID == domain.LadoRulePermissionCeiling })
			} else {
				ceiling := ruleIn(t, rules, domain.LadoRulePermissionCeiling)
				content := ceiling.Version.Content
				content.Config = []byte(c.config)
				*ceiling = withContent(*ceiling, content)
			}
			in := taskInput(rules, claudeCode)
			in.PlanCeiling = c.plan
			if c.projectScope {
				in.TaskID = ""
			}
			r := ladopolicy.Resolve(in)
			if r.Ceiling != c.wantCeiling || r.Mode != c.wantMode {
				t.Errorf("ceiling %q, mode %q; want %q, %q", r.Ceiling, r.Mode, c.wantCeiling, c.wantMode)
			}
			if r.Content.PermissionCeiling != c.wantCeiling || r.Content.Agent.PermissionMode != c.wantMode {
				t.Errorf("snapshot ceiling %q, mode %q; want %q, %q", r.Content.PermissionCeiling, r.Content.Agent.PermissionMode, c.wantCeiling, c.wantMode)
			}
			if got := problemsWith(r, "LADO_CEILING_", "LADO_PLAN_CEILING_"); !sameList(got, c.want) {
				t.Errorf("problems %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestResolveCodexAsksApproval(t *testing.T) {
	codex := ladopolicy.AgentChoice{Harness: domain.HarnessCodex, Model: "gpt-5.1-codex", Source: "task"}
	asks := func(mode domain.PermissionMode) []domain.LadoPolicyProblem {
		return []domain.LadoPolicyProblem{{Code: "LADO_CODEX_ASKS_APPROVAL", Severity: info, Details: map[string]string{"mode": string(mode)}}}
	}
	cases := []struct {
		name         string
		agent        ladopolicy.AgentChoice
		ceiling      domain.PermissionMode
		plan         domain.PermissionMode
		projectScope bool
		want         []domain.LadoPolicyProblem
	}{
		{name: "codex at auto asks before commits, pushes and network calls", agent: codex, ceiling: domain.PermissionModeAuto, want: asks(domain.PermissionModeAuto)},
		{name: "codex at accept-edits asks too", agent: codex, ceiling: domain.PermissionModeAuto, plan: domain.PermissionModeAcceptEdits, want: asks(domain.PermissionModeAcceptEdits)},
		{name: "codex at bypass-permissions does not ask", agent: codex, ceiling: domain.PermissionModeBypassPermissions},
		{name: "claude-code gets no such note", agent: claudeCode, ceiling: domain.PermissionModeAuto},
		{name: "project scope runs no agent", agent: codex, ceiling: domain.PermissionModeAuto, projectScope: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rules := seeded()
			setCeiling(t, rules, c.ceiling)
			in := taskInput(rules, c.agent)
			in.PlanCeiling = c.plan
			if c.projectScope {
				in.TaskID = ""
			}
			r := ladopolicy.Resolve(in)
			if got := problemsWith(r, "LADO_CODEX_"); !sameList(got, c.want) {
				t.Errorf("problems %+v, want %+v", got, c.want)
			}
			if r.Blocking() {
				t.Errorf("Blocking() with problems %+v; the note is info only", r.Problems)
			}
		})
	}
}

func TestResolveAORulesFileUnreadable(t *testing.T) {
	text := ladopolicy.AORule{Kind: ladopolicy.AORuleWorkerInstructions, SHA256: hexSHA256("Use pnpm."), Chars: 9}
	unreadable := ladopolicy.AORule{Kind: ladopolicy.AORuleWorkerInstructionsFile, Path: "docs/agents.md", ReadError: "file not found"}
	orchestrator := ladopolicy.AORule{Kind: ladopolicy.AORuleOrchestratorInstructions, SHA256: hexSHA256("Plan first."), Chars: 11}
	want := []domain.LadoPolicyProblem{{
		Code: "LADO_AO_RULES_FILE_UNREADABLE", Severity: blocking,
		Details: map[string]string{"path": "docs/agents.md", "reason": "file not found"},
	}}
	// Snapshots carry the worker instructions only; the file that was not
	// read has no hash.
	wantAORules := []ladopolicy.SnapshotAORule{
		{Kind: ladopolicy.AORuleWorkerInstructions, SHA256: text.SHA256},
		{Kind: ladopolicy.AORuleWorkerInstructionsFile, Path: "docs/agents.md"},
	}
	for _, taskID := range []domain.LadoTaskID{taskA, ""} {
		in := taskInput(seeded(), claudeCode)
		in.TaskID = taskID
		in.AORules = []ladopolicy.AORule{text, unreadable, orchestrator}
		r := ladopolicy.Resolve(in)
		if got := problemsWith(r, "LADO_AO_RULES_"); !sameList(got, want) {
			t.Errorf("task %q: problems %+v, want %+v", taskID, got, want)
		}
		if !r.Blocking() {
			t.Errorf("task %q: an unreadable agentRulesFile does not block", taskID)
		}
		if !reflect.DeepEqual(r.Content.AORules, wantAORules) {
			t.Errorf("task %q: snapshot AO rules %+v, want %+v", taskID, r.Content.AORules, wantAORules)
		}
	}

	// The reason is what ReadAORules found.
	project := domain.ProjectRecord{ID: string(projectA), Path: t.TempDir(), Config: domain.ProjectConfig{AgentRulesFile: "docs/agents.md"}}
	in := taskInput(seeded(), claudeCode)
	in.AORules = ladopolicy.ReadAORules(context.Background(), project)
	if got := problemsWith(ladopolicy.Resolve(in), "LADO_AO_RULES_"); !sameList(got, want) {
		t.Errorf("missing file: problems %+v, want %+v", got, want)
	}

	// A file that was read raises nothing.
	writeFile(t, project.Path, "docs/agents.md", "Run make test.")
	in.AORules = ladopolicy.ReadAORules(context.Background(), project)
	r := ladopolicy.Resolve(in)
	if got := problemsWith(r, "LADO_AO_RULES_"); len(got) != 0 || r.Blocking() {
		t.Errorf("readable file: problems %+v, want none", r.Problems)
	}
}
