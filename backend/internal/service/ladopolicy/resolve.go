package ladopolicy

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Where an agent choice comes from.
const (
	AgentFromTask    = "task"
	AgentFromProject = "project"
)

// AgentChoice is the agent that runs a task.
type AgentChoice struct {
	Harness domain.AgentHarness
	Model   string
	// Source is AgentFromTask, AgentFromProject, or empty when nobody chose
	// an agent.
	Source string
}

// ChooseAgent returns the agent that runs the task: the task's own choice,
// else the project's worker agent with the model AO gives that worker at
// spawn (the worker's model, else the project's).
func ChooseAgent(task domain.LadoTask, project domain.ProjectRecord) AgentChoice {
	if agent := strings.TrimSpace(task.Agent); agent != "" {
		return AgentChoice{Harness: domain.AgentHarness(agent), Model: strings.TrimSpace(task.Model), Source: AgentFromTask}
	}
	cfg := project.Config
	if cfg.Worker.Harness == "" {
		return AgentChoice{}
	}
	model := cfg.Worker.AgentConfig.Model
	if model == "" {
		model = cfg.AgentConfig.Model
	}
	return AgentChoice{Harness: cfg.Worker.Harness, Model: model, Source: AgentFromProject}
}

// Input is what Resolve reads. TaskID is empty when resolving for the
// project, which checks no agent.
type Input struct {
	Rules     []domain.LadoRule
	ProjectID domain.ProjectID
	TaskID    domain.LadoTaskID
	Agent     AgentChoice
	// PlanCeiling is the permission ceiling the approved plan asks for. Empty
	// or default leaves the rules' ceiling.
	PlanCeiling domain.PermissionMode
	AORules     []AORule
	// AOBuild is the running AO build, so an AO update changes the snapshot.
	AOBuild string
}

// Source is the binding that decided whether a rule applies.
type Source struct {
	Scope   domain.LadoRuleScope
	ScopeID string
	State   domain.LadoBindingState
}

// Entry is one rule as it resolved.
type Entry struct {
	Rule   domain.LadoRule
	On     bool
	Source Source
	// Project and Task are the rule's force_on or force_off bindings for the
	// resolved project and task, LadoBindingInherit without one. A protected
	// rule ignores them.
	Project, Task domain.LadoBindingState
}

// Resolution is what the rules give a project or a task
// (docs/LJY-PRD/HARNESS.md §4, §7, §8).
type Resolution struct {
	// Entries are the rules that are not archived, oldest first, then by id.
	Entries []Entry
	// Ceiling is the rules' permission ceiling; Mode is the permission mode
	// an execution runs with under it.
	Ceiling, Mode domain.PermissionMode
	// Prompt is the rule text a worker gets, empty when no rule has any.
	Prompt string
	// Capabilities are what a task's rules need from its agent, empty for a
	// project or without an agent.
	Capabilities []Capability
	// Problems are sorted by severity, code and rules.
	Problems []domain.LadoPolicyProblem
	// Content is the snapshot, JSON its canonical bytes and SHA256 their hex
	// sha256.
	Content SnapshotContent
	JSON    []byte
	SHA256  string
}

// Problem codes (docs/LJY-PRD/HARNESS.md §7). The app shows them in the
// developer's language, so they never change.
const (
	ProblemCeilingMissing        = "LADO_CEILING_MISSING"
	ProblemPlanCeilingClamped    = "LADO_PLAN_CEILING_CLAMPED"
	ProblemAgentNotChosen        = "LADO_AGENT_NOT_CHOSEN"
	ProblemCapabilityUnsupported = "LADO_AGENT_CAPABILITY_UNSUPPORTED"
	ProblemCapabilityUnverified  = "LADO_AGENT_CAPABILITY_UNVERIFIED"
	ProblemCapabilityDegraded    = "LADO_AGENT_CAPABILITY_DEGRADED"
	ProblemCodexAsksApproval     = "LADO_CODEX_ASKS_APPROVAL"
	ProblemRulesConflict         = "LADO_RULES_CONFLICT"
	ProblemAORulesFileUnreadable = "LADO_AO_RULES_FILE_UNREADABLE"
)

// Resolve applies the rules to in's project or task: it decides which rules
// are on, the permission mode, the worker's rule text and what blocks the
// task, and builds the canonical snapshot of it.
func Resolve(in Input) Resolution {
	r := Resolution{Entries: resolveEntries(in), Capabilities: []Capability{}, Problems: []domain.LadoPolicyProblem{}}
	r.Ceiling = r.ceiling()
	r.Mode = r.Ceiling
	if in.TaskID != "" {
		r.Mode = r.taskMode(in.PlanCeiling)
	}
	r.Prompt = promptText(r.Entries)
	if in.TaskID != "" {
		r.checkAgent(in.Agent.Harness)
	}
	r.checkConflicts()
	r.checkAORules(in.AORules)
	slices.SortStableFunc(r.Problems, func(a, b domain.LadoPolicyProblem) int {
		return cmp.Or(
			cmp.Compare(severityRank(a.Severity), severityRank(b.Severity)),
			strings.Compare(a.Code, b.Code),
			slices.Compare(a.Rules, b.Rules),
		)
	})
	r.Content = snapshotContent(in, r)
	r.JSON = encodeCanonical(r.Content)
	sum := sha256.Sum256(r.JSON)
	r.SHA256 = hex.EncodeToString(sum[:])
	return r
}

// Blocking reports whether a problem blocks the task.
func (r Resolution) Blocking() bool {
	return slices.ContainsFunc(r.Problems, func(p domain.LadoPolicyProblem) bool {
		return p.Severity == domain.LadoSeverityBlocking
	})
}

// SnapshotRules are the rule versions the snapshot uses, which the store
// keeps from being deleted.
func (r Resolution) SnapshotRules() []domain.LadoPolicySnapshotRule {
	rules := make([]domain.LadoPolicySnapshotRule, 0, len(r.Content.Rules))
	for _, rule := range r.Content.Rules {
		rules = append(rules, domain.LadoPolicySnapshotRule{RuleID: rule.RuleID, VersionID: rule.VersionID})
	}
	return rules
}

// resolveEntries decides each rule that is not archived, oldest first. A
// protected rule is on whatever its bindings say. Otherwise the nearest
// binding wins: the task's, then the project's, then the Global one, which
// is off unless it says on. Bindings of other projects and tasks do not
// count.
func resolveEntries(in Input) []Entry {
	rules := slices.DeleteFunc(slices.Clone(in.Rules), func(rule domain.LadoRule) bool { return !rule.ArchivedAt.IsZero() })
	slices.SortFunc(rules, func(a, b domain.LadoRule) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	entries := make([]Entry, 0, len(rules))
	for _, rule := range rules {
		e := Entry{Rule: rule, Project: domain.LadoBindingInherit, Task: domain.LadoBindingInherit}
		for _, b := range rule.Overrides {
			// Only force_on and force_off are bindings at these scopes;
			// the store never keeps anything else there.
			if b.State != domain.LadoBindingForceOn && b.State != domain.LadoBindingForceOff {
				continue
			}
			switch {
			case b.Scope == domain.LadoScopeProject && b.ScopeID == string(in.ProjectID):
				e.Project = b.State
			case b.Scope == domain.LadoScopeTask && in.TaskID != "" && b.ScopeID == string(in.TaskID):
				e.Task = b.State
			}
		}
		switch {
		case rule.Protected:
			e.On, e.Source = true, Source{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOn}
		case e.Task != domain.LadoBindingInherit:
			e.On, e.Source = e.Task == domain.LadoBindingForceOn, Source{Scope: domain.LadoScopeTask, ScopeID: string(in.TaskID), State: e.Task}
		case e.Project != domain.LadoBindingInherit:
			e.On, e.Source = e.Project == domain.LadoBindingForceOn, Source{Scope: domain.LadoScopeProject, ScopeID: string(in.ProjectID), State: e.Project}
		default:
			e.On = rule.Global == domain.LadoBindingOn
			e.Source = Source{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOff}
			if e.On {
				e.Source.State = domain.LadoBindingOn
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// ceiling returns the mode of the permission ceiling rule. Without that rule
// or a ceiling mode in its config it blocks and falls back to the lowest
// ceiling.
func (r *Resolution) ceiling() domain.PermissionMode {
	for _, e := range r.Entries {
		if e.Rule.ID != domain.LadoRulePermissionCeiling {
			continue
		}
		var cfg domain.LadoCeilingConfig
		if json.Unmarshal(e.Rule.Version.Content.Config, &cfg) == nil && domain.LadoPermissionRank(cfg.Mode) > 0 {
			return cfg.Mode
		}
	}
	r.problem(ProblemCeilingMissing, domain.LadoSeverityBlocking, []domain.LadoRuleID{domain.LadoRulePermissionCeiling}, nil)
	return domain.LadoCeilingModes[0]
}

// taskMode returns the mode a task runs with: the plan's ceiling, lowered to
// the rules' ceiling with a warning. A plan without a ceiling mode, empty or
// default, runs at the rules' ceiling.
func (r *Resolution) taskMode(plan domain.PermissionMode) domain.PermissionMode {
	rank := domain.LadoPermissionRank(plan)
	if rank == 0 {
		return r.Ceiling
	}
	if rank > domain.LadoPermissionRank(r.Ceiling) {
		r.problem(ProblemPlanCeilingClamped, domain.LadoSeverityWarning, []domain.LadoRuleID{domain.LadoRulePermissionCeiling},
			map[string]string{"plan": string(plan), "ceiling": string(r.Ceiling)})
		return r.Ceiling
	}
	return plan
}

// checkAgent checks that the task's agent can honor the rules
// (docs/LJY-PRD/HARNESS.md §7). Every execution needs permission modes; the
// rule text needs the prompt class. A problem names the rules that need the
// class. Without an agent nothing is checked: the task is blocked until one
// is chosen.
func (r *Resolution) checkAgent(agent domain.AgentHarness) {
	if agent == "" {
		r.problem(ProblemAgentNotChosen, domain.LadoSeverityBlocking, nil, nil)
		return
	}
	type need struct {
		class CapabilityClass
		rules []domain.LadoRuleID
	}
	var needs []need
	if r.Prompt != "" {
		needs = append(needs, need{CapabilityPrompt, r.ruleIDs(inPrompt)})
	}
	needs = append(needs, need{CapabilityPermissionMode, r.ruleIDs(func(e Entry) bool {
		return e.On && e.Rule.Version.Content.Has(domain.LadoEnforcementToolPolicy)
	})})
	for _, n := range needs {
		status := CapabilityOf(agent, n.class)
		r.Capabilities = append(r.Capabilities, Capability{Class: n.class, Status: status})
		details := map[string]string{"agent": string(agent), "capability": string(n.class)}
		switch status {
		case CapabilityUnsupported:
			r.problem(ProblemCapabilityUnsupported, domain.LadoSeverityBlocking, n.rules, details)
		case CapabilityUnverified:
			r.problem(ProblemCapabilityUnverified, domain.LadoSeverityBlocking, n.rules, details)
		case CapabilityDegraded:
			r.problem(ProblemCapabilityDegraded, domain.LadoSeverityWarning, n.rules, details)
		}
	}
	// Below bypass-permissions Codex runs with on-request approvals and no
	// network, so it asks before commits, pushes and network calls
	// (docs/LJY-PRD/TDD.md §6.6).
	if agent == domain.HarnessCodex && domain.LadoPermissionRank(r.Mode) < domain.LadoPermissionRank(domain.PermissionModeBypassPermissions) {
		r.problem(ProblemCodexAsksApproval, domain.LadoSeverityInfo, nil, map[string]string{"mode": string(r.Mode)})
	}
}

// checkConflicts blocks once for each pair of on rules where either names the
// other as a conflict.
func (r *Resolution) checkConflicts() {
	var on []domain.LadoRule
	for _, e := range r.Entries {
		if e.On {
			on = append(on, e.Rule)
		}
	}
	for i, a := range on {
		for _, b := range on[i+1:] {
			if slices.Contains(a.Version.Content.Conflicts, b.ID) || slices.Contains(b.Version.Content.Conflicts, a.ID) {
				r.problem(ProblemRulesConflict, domain.LadoSeverityBlocking, []domain.LadoRuleID{a.ID, b.ID}, nil)
			}
		}
	}
}

// checkAORules blocks when AO's agentRulesFile could not be read: LADO cannot
// say what the worker would get.
func (r *Resolution) checkAORules(rules []AORule) {
	for _, rule := range rules {
		if rule.Kind == AORuleWorkerInstructionsFile && rule.ReadError != "" {
			r.problem(ProblemAORulesFileUnreadable, domain.LadoSeverityBlocking, nil,
				map[string]string{"path": rule.Path, "reason": rule.ReadError})
		}
	}
}

// problem adds a problem with its rules sorted.
func (r *Resolution) problem(code string, severity domain.LadoProblemSeverity, rules []domain.LadoRuleID, details map[string]string) {
	if len(rules) == 0 {
		rules = nil
	} else {
		rules = slices.Sorted(slices.Values(rules))
	}
	r.Problems = append(r.Problems, domain.LadoPolicyProblem{Code: code, Severity: severity, Rules: rules, Details: details})
}

// ruleIDs returns the ids of the entries that match, in entry order.
func (r *Resolution) ruleIDs(match func(Entry) bool) []domain.LadoRuleID {
	var ids []domain.LadoRuleID
	for _, e := range r.Entries {
		if match(e) {
			ids = append(ids, e.Rule.ID)
		}
	}
	return ids
}

// severityRank orders problems blocking first.
func severityRank(s domain.LadoProblemSeverity) int {
	switch s {
	case domain.LadoSeverityBlocking:
		return 0
	case domain.LadoSeverityWarning:
		return 1
	default:
		return 2
	}
}
