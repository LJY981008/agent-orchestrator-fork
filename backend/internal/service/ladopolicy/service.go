package ladopolicy

// The rule service (docs/LJY-PRD/HARNESS.md §3-§5, §8 and §14): it keeps the
// rules and their bindings with an audit event for every change, shows what
// a change reaches before it is made, and resolves the rules of a project or
// a task for the app and for plan approval.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
)

// Limits on what a user rule may hold.
const (
	maxNameRunes        = 120
	maxCategoryRunes    = 40
	maxInstructionRunes = 8000
	maxConflicts        = 20
)

// Audit actors. A change through the API is "api", because nothing verifies
// who sent it; a protected change carries a presence proof, so a human made
// it.
const (
	actorAPI         = "api"
	actorHuman       = "human"
	actorDevPresence = "human:dev-presence"
)

// Audit actions of rule changes.
const (
	actionCreated          = "rule.created"
	actionUpdated          = "rule.updated"
	actionArchived         = "rule.archived"
	actionUnarchived       = "rule.unarchived"
	actionDeleted          = "rule.deleted"
	actionBindingSet       = "rule.binding_set"
	actionProtectedChanged = "rule.protected_changed"
)

// builtinPayload is the payload of a built-in rule's rule.created.
const builtinPayload = `{"version":1,"builtin":true}`

// Store keeps the rules and reads the tasks and snapshots they apply to.
type Store interface {
	EnsureLadoBuiltinRules(ctx context.Context, seeds []domain.LadoRuleSeed) ([]domain.LadoRuleID, error)
	ListLadoRules(ctx context.Context, includeArchived bool) ([]domain.LadoRule, error)
	LadoRule(ctx context.Context, id domain.LadoRuleID) (domain.LadoRule, bool, error)
	ListLadoRuleVersions(ctx context.Context, id domain.LadoRuleID) ([]domain.LadoRuleVersion, error)
	CreateLadoRule(ctx context.Context, rule domain.LadoRule, event domain.LadoAuditEvent) (domain.LadoRule, error)
	AddLadoRuleVersion(ctx context.Context, v domain.LadoRuleVersion, base int, proof *domain.LadoPresenceProof, event domain.LadoAuditEvent) (domain.LadoRule, error)
	SetLadoRuleArchived(ctx context.Context, id domain.LadoRuleID, archivedAt time.Time, event domain.LadoAuditEvent) (domain.LadoRule, error)
	DeleteLadoRule(ctx context.Context, id domain.LadoRuleID, event domain.LadoAuditEvent) error
	SetLadoRuleBinding(ctx context.Context, b domain.LadoRuleBinding, event domain.LadoAuditEvent) error
	ListLadoModeProjectIDs(ctx context.Context) ([]domain.ProjectID, error)
	ListLiveLadoTasks(ctx context.Context) ([]domain.LadoTask, error)
	LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error)
	LadoPolicySnapshot(ctx context.Context, approvalID domain.LadoPlanApprovalID) (domain.LadoPolicySnapshot, bool, error)
}

// Projects reads AO's project registry.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// Blobs reads pinned plans by their sha256.
type Blobs interface {
	Get(sum string, limit int64) ([]byte, error)
}

// Deps wires a Service.
type Deps struct {
	Store    Store
	Projects Projects
	Blobs    Blobs
	// AOBuild is the running AO build, which every snapshot carries, so an
	// AO update asks for new approvals.
	AOBuild string
	// Clock stamps changes; nil uses time.Now.
	Clock func() time.Time
	// NewID returns a new unique id; nil uses uuid.NewString.
	NewID func() string
	Log   *slog.Logger
}

// Service keeps LADO's rules and resolves them.
type Service struct {
	deps Deps

	mu sync.Mutex
	// ceilings caches the permission ceiling each plan revision asks for. A
	// revision's pinned plan never changes.
	ceilings map[domain.LadoPlanRevisionID]domain.PermissionMode
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Service{deps: deps, ceilings: map[domain.LadoPlanRevisionID]domain.PermissionMode{}}
}

// EnsureBuiltins seeds the built-in rules that do not exist yet, each as
// version 1 by "lado" with its audit event. A rule that exists keeps its
// versions and bindings, so the developer's changes survive restarts.
func (s *Service) EnsureBuiltins(ctx context.Context) error {
	now := s.now()
	builtins := Builtins()
	seeds := make([]domain.LadoRuleSeed, 0, len(builtins))
	for _, rule := range builtins {
		rule.CreatedAt = now
		rule.Version.ID = s.newVersionID()
		rule.Version.CreatedAt = now
		seeds = append(seeds, domain.LadoRuleSeed{
			Rule:  rule,
			Event: s.event(builtinActor, actionCreated, rule.ID, now, json.RawMessage(builtinPayload)),
		})
	}
	added, err := s.deps.Store.EnsureLadoBuiltinRules(ctx, seeds)
	if err != nil {
		return fmt.Errorf("seed LADO built-in rules: %w", err)
	}
	if len(added) > 0 {
		s.deps.Log.Info("LADO seeded built-in rules", "rules", added)
	}
	return nil
}

// RuleView is a rule with names for the scopes of its overrides.
type RuleView struct {
	Rule domain.LadoRule
	// Labels names each override's scope by LabelKey: a project's name or a
	// task's issue title. A scope LADO no longer finds has no label.
	Labels map[string]string
}

// LabelKey is the RuleView.Labels key of a scope.
func LabelKey(scope domain.LadoRuleScope, scopeID string) string {
	return string(scope) + ":" + scopeID
}

// List returns the rules oldest first, archived ones only when
// includeArchived.
func (s *Service) List(ctx context.Context, includeArchived bool) ([]RuleView, error) {
	rules, err := s.deps.Store.ListLadoRules(ctx, includeArchived)
	if err != nil {
		return nil, s.loadFailed(err)
	}
	return s.views(ctx, rules...), nil
}

// Rule returns one rule, archived or not, with its versions oldest first.
func (s *Service) Rule(ctx context.Context, id domain.LadoRuleID) (RuleView, []domain.LadoRuleVersion, error) {
	rule, err := s.rule(ctx, id)
	if err != nil {
		return RuleView{}, nil, err
	}
	versions, err := s.deps.Store.ListLadoRuleVersions(ctx, id)
	if err != nil {
		return RuleView{}, nil, s.loadFailed(err)
	}
	return s.views(ctx, rule)[0], versions, nil
}

// ChangeResult is a rule change: the rule as it is after the change, or as
// it would be after a dry run, and what the change reaches.
type ChangeResult struct {
	Rule   RuleView
	Impact Impact
	// Applied is false for a dry run.
	Applied bool
}

// CreateInput is a new user rule. Its instruction reaches workers as text.
type CreateInput struct {
	Name, Category, Instruction string
	Conflicts                   []domain.LadoRuleID
	// Scope and ScopeID say where the rule starts. At Global it is on or off
	// for every LADO project as On says; for a project or a task it is off at
	// Global and forced on there.
	Scope   domain.LadoRuleScope
	ScopeID string
	On      bool
	// DryRun shows the change without making it.
	DryRun bool
}

// Create adds a user rule as version 1. A dry run keeps nothing, so the
// rule's id is not reserved.
func (s *Service) Create(ctx context.Context, in CreateInput) (ChangeResult, error) {
	rules, err := s.rules(ctx)
	if err != nil {
		return ChangeResult{}, err
	}
	now := s.now()
	rule := domain.LadoRule{
		ID:        domain.LadoRuleID("lado-rule-" + s.deps.NewID()),
		Global:    domain.LadoBindingOff,
		Overrides: []domain.LadoRuleBinding{},
		CreatedAt: now,
	}
	content := domain.LadoRuleContent{
		Name:        in.Name,
		Category:    in.Category,
		Instruction: in.Instruction,
		Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementPrompt},
		Conflicts:   in.Conflicts,
	}.Normalize()
	if err := checkContent(rule.ID, content, rules, nil); err != nil {
		return ChangeResult{}, err
	}
	if in.Scope == domain.LadoScopeGlobal {
		if err := checkBinding(in.Scope, in.ScopeID, domain.LadoBindingOn); err != nil {
			return ChangeResult{}, err
		}
		if in.On {
			rule.Global = domain.LadoBindingOn
		}
	} else {
		if err := checkBinding(in.Scope, in.ScopeID, domain.LadoBindingForceOn); err != nil {
			return ChangeResult{}, err
		}
		if err := s.checkScope(ctx, in.Scope, in.ScopeID, domain.LadoBindingForceOn); err != nil {
			return ChangeResult{}, err
		}
		rule.Overrides = []domain.LadoRuleBinding{{RuleID: rule.ID, Scope: in.Scope, ScopeID: in.ScopeID, State: domain.LadoBindingForceOn, UpdatedAt: now}}
	}
	rule.Version = s.newVersion(rule.ID, 1, content, actorAPI, now)
	impact, err := s.impact(ctx, rules, append(slices.Clone(rules), rule))
	if err != nil {
		return ChangeResult{}, err
	}
	if in.DryRun {
		return ChangeResult{Rule: s.views(ctx, rule)[0], Impact: impact}, nil
	}
	created, err := s.deps.Store.CreateLadoRule(ctx, rule, s.event(actorAPI, actionCreated, rule.ID, now, map[string]any{
		"version": 1,
		"scope":   in.Scope,
		"scopeId": in.ScopeID,
		"global":  rule.Global,
	}))
	if err != nil {
		return ChangeResult{}, s.saveFailed(err)
	}
	return ChangeResult{Rule: s.views(ctx, created)[0], Impact: impact, Applied: true}, nil
}

// UpdateInput changes a rule. A nil field keeps its value.
type UpdateInput struct {
	// BaseVersion is the active version the change started from.
	BaseVersion                 int
	Name, Category, Instruction *string
	Conflicts                   *[]domain.LadoRuleID
	// CeilingMode is the permission ceiling's new mode. Only that rule takes
	// one, and only with presence.
	CeilingMode *domain.PermissionMode
	// Archived archives or unarchives the rule.
	Archived *bool
	// Reason goes into the audit log.
	Reason string
	// DryRun shows the change without making it; it needs no presence.
	DryRun bool
	// Presence returns the human-presence proof of the request for action
	// and subject. It is nil when the request carried none.
	Presence func(action, subject string) (domain.LadoPresenceProof, error)
}

// Update changes a rule's content, which adds a version when the content's
// hash changes, and archives or unarchives it. A protected rule refuses both:
// only the permission ceiling's mode changes, with a human-presence proof
// (docs/LJY-PRD/HARNESS.md §14).
func (s *Service) Update(ctx context.Context, id domain.LadoRuleID, in UpdateInput) (ChangeResult, error) {
	edits := in.Name != nil || in.Category != nil || in.Instruction != nil || in.Conflicts != nil || in.Archived != nil
	if !edits && in.CeilingMode == nil {
		return ChangeResult{}, apierr.Invalid("LADO_RULE_UPDATE_EMPTY", "Send a change to the rule.", nil)
	}
	rules, err := s.rules(ctx)
	if err != nil {
		return ChangeResult{}, err
	}
	rule, ok := findRule(rules, id)
	switch {
	case !ok:
		return ChangeResult{}, ruleNotFound(id)
	case rule.Protected && (edits || id != domain.LadoRulePermissionCeiling):
		return ChangeResult{}, ruleProtected(id)
	case in.CeilingMode != nil && id != domain.LadoRulePermissionCeiling:
		return ChangeResult{}, apierr.Invalid("LADO_RULE_CEILING_INVALID", "Only the permission ceiling has a mode.", map[string]any{"rule": string(id)})
	case in.BaseVersion != rule.Version.Number:
		return ChangeResult{}, ruleChanged(in.BaseVersion)
	}
	now := s.now()
	if in.CeilingMode != nil {
		return s.changeCeiling(ctx, rules, rule, in, now)
	}
	content := rule.Version.Content
	if in.Name != nil {
		content.Name = *in.Name
	}
	if in.Category != nil {
		content.Category = *in.Category
	}
	if in.Instruction != nil {
		content.Instruction = *in.Instruction
	}
	if in.Conflicts != nil {
		content.Conflicts = *in.Conflicts
	}
	content = content.Normalize()
	after := rule
	if content.SHA256() != rule.Version.Content.SHA256() {
		if err := checkContent(id, content, rules, rule.Version.Content.Conflicts); err != nil {
			return ChangeResult{}, err
		}
		after.Version = s.newVersion(id, rule.Version.Number+1, content, actorAPI, now)
	}
	if in.Archived != nil {
		switch {
		case !*in.Archived:
			after.ArchivedAt = time.Time{}
		case rule.ArchivedAt.IsZero():
			after.ArchivedAt = now
		}
	}
	versioned := after.Version.Number != rule.Version.Number
	archiving := after.ArchivedAt.IsZero() != rule.ArchivedAt.IsZero()
	if !versioned && !archiving {
		return ChangeResult{Rule: s.views(ctx, rule)[0], Impact: noImpact(), Applied: !in.DryRun}, nil
	}
	impact, err := s.impact(ctx, rules, replaceRule(rules, after))
	if err != nil {
		return ChangeResult{}, err
	}
	if in.DryRun {
		return ChangeResult{Rule: s.views(ctx, after)[0], Impact: impact}, nil
	}
	reason := strings.TrimSpace(in.Reason)
	updated := rule
	if versioned {
		updated, err = s.deps.Store.AddLadoRuleVersion(ctx, after.Version, rule.Version.Number, nil, s.event(actorAPI, actionUpdated, id, now, map[string]any{
			"version": after.Version.Number,
			"sha256":  after.Version.SHA256,
			"reason":  reason,
		}))
		if err != nil {
			return ChangeResult{}, s.versionFailed(err, in.BaseVersion)
		}
	}
	if archiving {
		action := actionArchived
		if after.ArchivedAt.IsZero() {
			action = actionUnarchived
		}
		updated, err = s.deps.Store.SetLadoRuleArchived(ctx, id, after.ArchivedAt, s.event(actorAPI, action, id, now, map[string]any{
			"version": updated.Version.Number,
			"reason":  reason,
		}))
		if err != nil {
			return ChangeResult{}, s.saveFailed(err)
		}
	}
	return ChangeResult{Rule: s.views(ctx, updated)[0], Impact: impact, Applied: true}, nil
}

// changeCeiling sets the permission ceiling's mode. The change needs a
// human-presence proof, because the ceiling bounds what every agent may do;
// a dry run needs none.
func (s *Service) changeCeiling(ctx context.Context, rules []domain.LadoRule, rule domain.LadoRule, in UpdateInput, now time.Time) (ChangeResult, error) {
	mode := *in.CeilingMode
	if domain.LadoPermissionRank(mode) == 0 {
		return ChangeResult{}, apierr.Invalid("LADO_RULE_CEILING_INVALID", "Choose accept-edits, auto or bypass-permissions as the permission ceiling.",
			map[string]any{"mode": string(mode)})
	}
	content := rule.Version.Content
	// A mode always encodes.
	content.Config, _ = json.Marshal(domain.LadoCeilingConfig{Mode: mode})
	content = content.Normalize()
	if content.SHA256() == rule.Version.Content.SHA256() {
		return ChangeResult{Rule: s.views(ctx, rule)[0], Impact: noImpact(), Applied: !in.DryRun}, nil
	}
	after := rule
	after.Version = s.newVersion(rule.ID, rule.Version.Number+1, content, actorHuman, now)
	impact, err := s.impact(ctx, rules, replaceRule(rules, after))
	if err != nil {
		return ChangeResult{}, err
	}
	if in.DryRun {
		return ChangeResult{Rule: s.views(ctx, after)[0], Impact: impact}, nil
	}
	if in.Presence == nil {
		return ChangeResult{}, apierr.Forbidden("LADO_PRESENCE_REQUIRED",
			"Change the permission ceiling in the AO desktop app. It asks you to confirm, then sends the change itself.")
	}
	proof, err := in.Presence(domain.LadoPresenceRuleChange, string(rule.ID))
	if err != nil {
		return ChangeResult{}, err
	}
	if proof.Dev {
		after.Version.Actor = actorDevPresence
	}
	var from domain.LadoCeilingConfig
	_ = json.Unmarshal(rule.Version.Content.Config, &from)
	updated, err := s.deps.Store.AddLadoRuleVersion(ctx, after.Version, rule.Version.Number, &proof, s.event(after.Version.Actor, actionProtectedChanged, rule.ID, now, map[string]any{
		"from":    from.Mode,
		"to":      mode,
		"version": after.Version.Number,
		"proof":   proof.Nonce,
		"dev":     proof.Dev,
		"reason":  strings.TrimSpace(in.Reason),
	}))
	if errors.Is(err, domain.ErrLadoPresenceReplayed) {
		return ChangeResult{}, ladopresence.Replayed()
	}
	if err != nil {
		return ChangeResult{}, s.versionFailed(err, in.BaseVersion)
	}
	return ChangeResult{Rule: s.views(ctx, updated)[0], Impact: impact, Applied: true}, nil
}

// Delete removes a user rule that no approval's snapshot used. Built-in
// rules stay, and a used rule can be archived instead.
func (s *Service) Delete(ctx context.Context, id domain.LadoRuleID) error {
	rule, err := s.rule(ctx, id)
	if err != nil {
		return err
	}
	if rule.Builtin {
		return apierr.New(apierr.KindForbidden, "LADO_RULE_BUILTIN", "LADO's built-in rules cannot be deleted.", map[string]any{"rule": string(id)})
	}
	err = s.deps.Store.DeleteLadoRule(ctx, id, s.event(actorAPI, actionDeleted, id, s.now(), map[string]any{
		"version": rule.Version.Number,
		"name":    rule.Version.Content.Name,
	}))
	switch {
	case errors.Is(err, domain.ErrLadoRuleReferenced):
		return apierr.Conflict("LADO_RULE_REFERENCED", "An approved plan used this rule, so LADO keeps it. Archive it instead.", map[string]any{"rule": string(id)})
	case err != nil:
		return s.saveFailed(err)
	}
	return nil
}

// BindingInput sets a rule's binding at one scope.
type BindingInput struct {
	RuleID  domain.LadoRuleID
	Scope   domain.LadoRuleScope
	ScopeID string
	// State is on or off at Global, and inherit, force_on or force_off for a
	// project or a task. Inherit removes the binding.
	State  domain.LadoBindingState
	DryRun bool
}

// SetBinding turns a rule on or off at Global, or forces it on or off for one
// project or task. A protected rule is always on and takes no binding, and an
// archived rule takes none until it is unarchived.
func (s *Service) SetBinding(ctx context.Context, in BindingInput) (ChangeResult, error) {
	rules, err := s.rules(ctx)
	if err != nil {
		return ChangeResult{}, err
	}
	rule, ok := findRule(rules, in.RuleID)
	switch {
	case !ok:
		return ChangeResult{}, ruleNotFound(in.RuleID)
	case rule.Protected:
		return ChangeResult{}, ruleProtected(rule.ID)
	case !rule.ArchivedAt.IsZero():
		return ChangeResult{}, apierr.Conflict("LADO_RULE_ARCHIVED", "This rule is archived. Unarchive it first.", map[string]any{"rule": string(rule.ID)})
	}
	if err := checkBinding(in.Scope, in.ScopeID, in.State); err != nil {
		return ChangeResult{}, err
	}
	if err := s.checkScope(ctx, in.Scope, in.ScopeID, in.State); err != nil {
		return ChangeResult{}, err
	}
	from := bindingState(rule, in.Scope, in.ScopeID)
	if from == in.State {
		return ChangeResult{Rule: s.views(ctx, rule)[0], Impact: noImpact(), Applied: !in.DryRun}, nil
	}
	now := s.now()
	binding := domain.LadoRuleBinding{RuleID: rule.ID, Scope: in.Scope, ScopeID: in.ScopeID, State: in.State, UpdatedAt: now}
	after := withBinding(rule, binding)
	impact, err := s.impact(ctx, rules, replaceRule(rules, after))
	if err != nil {
		return ChangeResult{}, err
	}
	if in.DryRun {
		return ChangeResult{Rule: s.views(ctx, after)[0], Impact: impact}, nil
	}
	if err := s.deps.Store.SetLadoRuleBinding(ctx, binding, s.event(actorAPI, actionBindingSet, rule.ID, now, map[string]any{
		"scope":   in.Scope,
		"scopeId": in.ScopeID,
		"from":    from,
		"to":      in.State,
	})); err != nil {
		return ChangeResult{}, s.saveFailed(err)
	}
	updated, err := s.rule(ctx, rule.ID)
	if err != nil {
		return ChangeResult{}, err
	}
	return ChangeResult{Rule: s.views(ctx, updated)[0], Impact: impact, Applied: true}, nil
}

// EffectiveInput names a project, or a task, whose project then applies.
type EffectiveInput struct {
	ProjectID domain.ProjectID
	TaskID    domain.LadoTaskID
}

// EffectiveView is what the rules give a project or a task now.
type EffectiveView struct {
	Resolution
	// Agent is the task's agent, or the project's worker agent.
	Agent AgentChoice
	// AORules are all of the project's AO instructions, also the
	// orchestrator's, which the snapshot leaves out.
	AORules []AORule
	// Task is the resolved task, nil for a project. While its approval is
	// valid its PolicySHA256 is filled, so its state is current.
	Task *domain.LadoTask
	// Approved compares the task's valid approval with now. It is nil without
	// one, and for an approval given before LADO had rules.
	Approved *ApprovedView
}

// ApprovedView is a task's approval next to the rules now.
type ApprovedView struct {
	Snapshot       domain.LadoPolicySnapshot
	Content        SnapshotContent
	RevisionNumber int
	ApprovedAt     time.Time
	// Matches reports whether the rules still give the approved snapshot.
	Matches bool
	// Added are the rules on now but not then, Removed the rules on then but
	// not now, and Changed the rules on in both with another version or
	// binding, each sorted.
	Added, Removed, Changed []domain.LadoRuleID
}

// Effective resolves the rules for a project, or for a task at its latest
// plan revision: what Snapshot would fix if the developer approved now.
func (s *Service) Effective(ctx context.Context, in EffectiveInput) (EffectiveView, error) {
	if in.TaskID == "" {
		return s.effectiveProject(ctx, in.ProjectID)
	}
	task, err := s.task(ctx, in.TaskID)
	if err != nil {
		return EffectiveView{}, err
	}
	if in.ProjectID != "" && in.ProjectID != task.ProjectID {
		return EffectiveView{}, apierr.Invalid("LADO_EFFECTIVE_SCOPE_INVALID", "The task belongs to another project.",
			map[string]any{"projectId": string(in.ProjectID), "taskId": string(in.TaskID)})
	}
	r, input, err := s.resolveTask(ctx, task, task.Plan.Latest)
	if err != nil {
		return EffectiveView{}, err
	}
	view := EffectiveView{Resolution: r, Agent: input.Agent, AORules: input.AORules}
	if task.Plan.ApprovalValid() {
		task.Plan.PolicySHA256 = r.SHA256
		if view.Approved, err = s.approved(ctx, task, r); err != nil {
			return EffectiveView{}, err
		}
	}
	view.Task = &task
	return view, nil
}

func (s *Service) effectiveProject(ctx context.Context, id domain.ProjectID) (EffectiveView, error) {
	if id == "" {
		return EffectiveView{}, apierr.Invalid("LADO_EFFECTIVE_SCOPE_INVALID", "Name a project or a task.", nil)
	}
	rules, err := s.rules(ctx)
	if err != nil {
		return EffectiveView{}, err
	}
	scope, err := s.projectScope(ctx, id)
	if err != nil {
		return EffectiveView{}, err
	}
	if !scope.record.ArchivedAt.IsZero() {
		return EffectiveView{}, scopeNotFound(domain.LadoScopeProject, string(id))
	}
	in := s.projectInput(scope)
	in.Rules = rules
	return EffectiveView{Resolution: Resolve(in), Agent: in.Agent, AORules: in.AORules}, nil
}

// approved reads the snapshot of the task's valid approval and compares it
// with now. An approval without a snapshot gives nil.
func (s *Service) approved(ctx context.Context, task domain.LadoTask, now Resolution) (*ApprovedView, error) {
	snapshot, ok, err := s.deps.Store.LadoPolicySnapshot(ctx, task.Plan.Approval.ID)
	if err != nil {
		return nil, s.loadFailed(err)
	}
	if !ok {
		return nil, nil
	}
	var content SnapshotContent
	if err := json.Unmarshal(snapshot.Content, &content); err != nil {
		return nil, s.loadFailed(fmt.Errorf("decode rule snapshot %s: %w", snapshot.ID, err))
	}
	view := &ApprovedView{
		Snapshot:       snapshot,
		Content:        content,
		RevisionNumber: task.Plan.Latest.Number,
		ApprovedAt:     task.Plan.Approval.ApprovedAt,
		Matches:        snapshot.SHA256 == now.SHA256,
	}
	view.Added, view.Removed, view.Changed = diffRules(content.Rules, now.Content.Rules)
	return view, nil
}

// diffRules compares the on rules of two snapshots by id.
func diffRules(then, now []SnapshotRule) (added, removed, changed []domain.LadoRuleID) {
	old := make(map[domain.LadoRuleID]SnapshotRule, len(then))
	for _, rule := range then {
		old[rule.RuleID] = rule
	}
	added, changed = []domain.LadoRuleID{}, []domain.LadoRuleID{}
	for _, rule := range now {
		prev, ok := old[rule.RuleID]
		switch {
		case !ok:
			added = append(added, rule.RuleID)
		case prev.VersionID != rule.VersionID || prev.Source != rule.Source:
			changed = append(changed, rule.RuleID)
		}
		delete(old, rule.RuleID)
	}
	removed = make([]domain.LadoRuleID, 0, len(old))
	for id := range old {
		removed = append(removed, id)
	}
	slices.Sort(added)
	slices.Sort(removed)
	slices.Sort(changed)
	return added, removed, changed
}

// Snapshot fixes the rules for approving a task's plan revision: the
// canonical snapshot and its hash, whether it blocks the task, and the rule
// versions it used. The store gives it the approval's id and time.
func (s *Service) Snapshot(ctx context.Context, task domain.LadoTask, rev domain.LadoPlanRevision) (domain.LadoPolicySnapshot, error) {
	r, _, err := s.resolveTask(ctx, task, rev)
	if err != nil {
		return domain.LadoPolicySnapshot{}, err
	}
	return domain.LadoPolicySnapshot{
		ID:        domain.LadoPolicySnapshotID("lado-snapshot-" + s.deps.NewID()),
		SHA256:    r.SHA256,
		Content:   r.JSON,
		Blocking:  r.Blocking(),
		Rules:     r.SnapshotRules(),
		CreatedAt: s.now(),
	}, nil
}

// CurrentSHA256 returns the snapshot hash the rules give the task's latest
// plan revision now, which is the approved one while the approval is valid.
// An approval whose snapshot has another hash needs renewing.
func (s *Service) CurrentSHA256(ctx context.Context, task domain.LadoTask) (string, error) {
	r, _, err := s.resolveTask(ctx, task, task.Plan.Latest)
	if err != nil {
		return "", err
	}
	return r.SHA256, nil
}

// CurrentSHA256s is CurrentSHA256 for many tasks, as a task list needs it:
// it reads the rules once and each project with its AO instructions once. A
// task whose project or pinned plan cannot be read is left out, which reads
// as no change, as when CurrentSHA256 fails for it; only rules that cannot
// be read fail the call.
func (s *Service) CurrentSHA256s(ctx context.Context, tasks []domain.LadoTask) (map[domain.LadoTaskID]string, error) {
	rules, err := s.rules(ctx)
	if err != nil {
		return nil, err
	}
	// A nil scope is a project that cannot be read.
	scopes := map[domain.ProjectID]*projectScope{}
	sums := make(map[domain.LadoTaskID]string, len(tasks))
	for _, task := range tasks {
		scope, read := scopes[task.ProjectID]
		if !read {
			if got, err := s.projectScope(ctx, task.ProjectID); err != nil {
				s.deps.Log.Debug("LADO could not read a project for its tasks' rules", "project", task.ProjectID, "err", err)
			} else {
				scope = &got
			}
			scopes[task.ProjectID] = scope
		}
		if scope == nil {
			continue
		}
		r, _, err := s.resolveTaskWith(ctx, rules, *scope, task, task.Plan.Latest)
		if err != nil {
			s.deps.Log.Debug("LADO could not resolve a task's rules", "task", task.ID, "err", err)
			continue
		}
		sums[task.ID] = r.SHA256
	}
	return sums, nil
}

// projectScope is a project with its AO instructions, read once for every
// resolution of the project and its tasks.
type projectScope struct {
	record  domain.ProjectRecord
	aoRules []AORule
}

// projectScope reads a registered project, archived or not.
func (s *Service) projectScope(ctx context.Context, id domain.ProjectID) (projectScope, error) {
	project, ok, err := s.deps.Projects.GetProject(ctx, string(id))
	if err != nil {
		return projectScope{}, s.loadFailed(err)
	}
	if !ok {
		return projectScope{}, scopeNotFound(domain.LadoScopeProject, string(id))
	}
	return projectScope{record: project, aoRules: ReadAORules(ctx, project)}, nil
}

// projectInput is what resolution reads for a project. Its agent is the
// project's worker agent, as for a task without its own.
func (s *Service) projectInput(scope projectScope) Input {
	return Input{
		ProjectID: domain.ProjectID(scope.record.ID),
		Agent:     ChooseAgent(domain.LadoTask{}, scope.record),
		AORules:   scope.aoRules,
		AOBuild:   s.deps.AOBuild,
	}
}

// taskInput is what resolution reads for a task at a plan revision, which
// may be empty.
func (s *Service) taskInput(ctx context.Context, task domain.LadoTask, scope projectScope, rev domain.LadoPlanRevision) (Input, error) {
	ceiling, err := s.planCeiling(ctx, rev)
	if err != nil {
		return Input{}, err
	}
	return Input{
		ProjectID:   task.ProjectID,
		TaskID:      task.ID,
		Agent:       ChooseAgent(task, scope.record),
		PlanCeiling: ceiling,
		AORules:     scope.aoRules,
		AOBuild:     s.deps.AOBuild,
	}, nil
}

// resolveTask resolves the rules for a task at a plan revision.
func (s *Service) resolveTask(ctx context.Context, task domain.LadoTask, rev domain.LadoPlanRevision) (Resolution, Input, error) {
	rules, err := s.rules(ctx)
	if err != nil {
		return Resolution{}, Input{}, err
	}
	scope, err := s.projectScope(ctx, task.ProjectID)
	if err != nil {
		return Resolution{}, Input{}, err
	}
	return s.resolveTaskWith(ctx, rules, scope, task, rev)
}

// resolveTaskWith resolves the rules for a task at a plan revision with the
// rules and the task's project as read before.
func (s *Service) resolveTaskWith(ctx context.Context, rules []domain.LadoRule, scope projectScope, task domain.LadoTask, rev domain.LadoPlanRevision) (Resolution, Input, error) {
	in, err := s.taskInput(ctx, task, scope, rev)
	if err != nil {
		s.deps.Log.Warn("LADO could not read a pinned plan for its rules", "revision", rev.ID, "err", err)
		return Resolution{}, Input{}, apierr.Conflict("LADO_PLAN_BLOB_MISSING",
			"The pinned copy of this plan is missing or changed on disk, so LADO cannot tell which rules apply to it.",
			map[string]any{"revision": string(rev.ID)})
	}
	in.Rules = rules
	return Resolve(in), in, nil
}

// planCeiling returns the permission ceiling a plan revision asks for, read
// from its pinned plan once. Without a revision there is none. A cancelled
// request reads no plan.
func (s *Service) planCeiling(ctx context.Context, rev domain.LadoPlanRevision) (domain.PermissionMode, error) {
	if rev.ID == "" {
		return "", nil
	}
	s.mu.Lock()
	ceiling, ok := s.ceilings[rev.ID]
	s.mu.Unlock()
	if ok {
		return ceiling, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	content, err := s.deps.Blobs.Get(rev.SHA256, domain.LadoPlanMaxBytes)
	if err != nil {
		return "", err
	}
	// A plan with problems still names its ceiling; one that names none, or
	// no ceiling mode, runs at the rules' ceiling.
	plan, _ := domain.ParseLadoPlan(content, domain.LadoPlanExpectations{})
	s.mu.Lock()
	s.ceilings[rev.ID] = plan.PermissionCeiling
	s.mu.Unlock()
	return plan.PermissionCeiling, nil
}

// checkContent checks a user rule's normalized content. A conflict in kept,
// the conflicts of the rule's active version, passes as it is, so the rule
// stays editable after a rule it names was deleted; any other must name a
// rule that exists, is not the rule itself and is not protected.
func checkContent(id domain.LadoRuleID, c domain.LadoRuleContent, rules []domain.LadoRule, kept []domain.LadoRuleID) error {
	switch {
	case c.Name == "":
		return apierr.Invalid("LADO_RULE_NAME_REQUIRED", "Name the rule.", nil)
	case utf8.RuneCountInString(c.Name) > maxNameRunes:
		return apierr.Invalid("LADO_RULE_NAME_TOO_LONG", fmt.Sprintf("Keep the name within %d characters.", maxNameRunes), map[string]any{"max": maxNameRunes})
	case utf8.RuneCountInString(c.Category) > maxCategoryRunes:
		return apierr.Invalid("LADO_RULE_CATEGORY_TOO_LONG", fmt.Sprintf("Keep the category within %d characters.", maxCategoryRunes), map[string]any{"max": maxCategoryRunes})
	case c.Instruction == "":
		return apierr.Invalid("LADO_RULE_INSTRUCTION_REQUIRED", "Write the instruction workers get.", nil)
	case utf8.RuneCountInString(c.Instruction) > maxInstructionRunes:
		return apierr.Invalid("LADO_RULE_INSTRUCTION_TOO_LONG", fmt.Sprintf("Keep the instruction within %d characters.", maxInstructionRunes), map[string]any{"max": maxInstructionRunes})
	case len(c.Conflicts) > maxConflicts:
		return apierr.Invalid("LADO_RULE_CONFLICTS_TOO_MANY", fmt.Sprintf("Name at most %d conflicting rules.", maxConflicts), map[string]any{"max": maxConflicts})
	}
	for _, other := range c.Conflicts {
		if slices.Contains(kept, other) {
			continue
		}
		rule, ok := findRule(rules, other)
		switch {
		case other == id:
			return apierr.Invalid("LADO_RULE_CONFLICT_SELF", "A rule cannot conflict with itself.", map[string]any{"rule": string(other)})
		case !ok:
			return apierr.Invalid("LADO_RULE_CONFLICT_UNKNOWN", fmt.Sprintf("There is no rule %s.", other), map[string]any{"rule": string(other)})
		case rule.Protected:
			// A protected rule is always on, so the conflict would block every
			// task the rule applies to.
			return apierr.Invalid("LADO_RULE_CONFLICT_PROTECTED", "A rule cannot conflict with a protected rule, which is always on.", map[string]any{"rule": string(other)})
		}
	}
	return nil
}

// checkBinding checks that the scope takes the state: on or off at Global,
// which has no scope id; inherit, force_on or force_off for a named project
// or task.
func checkBinding(scope domain.LadoRuleScope, scopeID string, state domain.LadoBindingState) error {
	scopeInvalid := apierr.Invalid("LADO_BINDING_SCOPE_INVALID", "Bind the rule at Global, or to a named project or task.",
		map[string]any{"scope": string(scope), "scopeId": scopeID})
	states := []domain.LadoBindingState{domain.LadoBindingInherit, domain.LadoBindingForceOn, domain.LadoBindingForceOff}
	switch scope {
	case domain.LadoScopeGlobal:
		if scopeID != "" {
			return scopeInvalid
		}
		states = []domain.LadoBindingState{domain.LadoBindingOn, domain.LadoBindingOff}
	case domain.LadoScopeProject, domain.LadoScopeTask:
		if scopeID == "" {
			return scopeInvalid
		}
	default:
		return scopeInvalid
	}
	if !slices.Contains(states, state) {
		return apierr.Invalid("LADO_BINDING_STATE_INVALID", "The Global binding is on or off; a project or task binding is inherit, force_on or force_off.",
			map[string]any{"scope": string(scope), "state": string(state)})
	}
	return nil
}

// checkScope checks that a project or task binding names a scope LADO knows:
// a project that is not archived, or a task with an issue. A cancelled task
// takes only inherit, which removes a binding.
func (s *Service) checkScope(ctx context.Context, scope domain.LadoRuleScope, id string, state domain.LadoBindingState) error {
	switch scope {
	case domain.LadoScopeProject:
		project, ok, err := s.deps.Projects.GetProject(ctx, id)
		if err != nil {
			return s.loadFailed(err)
		}
		if !ok || !project.ArchivedAt.IsZero() {
			return scopeNotFound(scope, id)
		}
	case domain.LadoScopeTask:
		task, err := s.task(ctx, domain.LadoTaskID(id))
		if err != nil {
			return err
		}
		if !task.CancelledAt.IsZero() && state != domain.LadoBindingInherit {
			return apierr.Conflict("LADO_TASK_CANCELLED", "This task was cancelled.", map[string]any{"task": id})
		}
	}
	return nil
}

// bindingState returns the rule's binding at a scope, inherit without one.
func bindingState(rule domain.LadoRule, scope domain.LadoRuleScope, scopeID string) domain.LadoBindingState {
	if scope == domain.LadoScopeGlobal {
		return rule.Global
	}
	for _, b := range rule.Overrides {
		if b.Scope == scope && b.ScopeID == scopeID {
			return b.State
		}
	}
	return domain.LadoBindingInherit
}

// withBinding returns the rule with b in place of its binding at b's scope.
// The rule's own overrides stay as they were.
func withBinding(rule domain.LadoRule, b domain.LadoRuleBinding) domain.LadoRule {
	if b.Scope == domain.LadoScopeGlobal {
		rule.Global = b.State
		return rule
	}
	overrides := make([]domain.LadoRuleBinding, 0, len(rule.Overrides)+1)
	for _, o := range rule.Overrides {
		if o.Scope != b.Scope || o.ScopeID != b.ScopeID {
			overrides = append(overrides, o)
		}
	}
	if b.State != domain.LadoBindingInherit {
		overrides = append(overrides, b)
	}
	rule.Overrides = overrides
	return rule
}

func findRule(rules []domain.LadoRule, id domain.LadoRuleID) (domain.LadoRule, bool) {
	i := slices.IndexFunc(rules, func(rule domain.LadoRule) bool { return rule.ID == id })
	if i < 0 {
		return domain.LadoRule{}, false
	}
	return rules[i], true
}

// replaceRule returns a copy of rules with after in place of the rule with
// its id.
func replaceRule(rules []domain.LadoRule, after domain.LadoRule) []domain.LadoRule {
	out := slices.Clone(rules)
	for i := range out {
		if out[i].ID == after.ID {
			out[i] = after
		}
	}
	return out
}

// views labels the scopes of the rules' overrides, looking each scope up
// once.
func (s *Service) views(ctx context.Context, rules ...domain.LadoRule) []RuleView {
	known := map[string]string{}
	views := make([]RuleView, 0, len(rules))
	for _, rule := range rules {
		labels := map[string]string{}
		for _, b := range rule.Overrides {
			key := LabelKey(b.Scope, b.ScopeID)
			label, ok := known[key]
			if !ok {
				label = s.label(ctx, b.Scope, b.ScopeID)
				known[key] = label
			}
			if label != "" {
				labels[key] = label
			}
		}
		views = append(views, RuleView{Rule: rule, Labels: labels})
	}
	return views
}

// label names a scope for the app: a project's name, a task's issue title. A
// scope that cannot be read gets none.
func (s *Service) label(ctx context.Context, scope domain.LadoRuleScope, id string) string {
	switch scope {
	case domain.LadoScopeProject:
		project, ok, err := s.deps.Projects.GetProject(ctx, id)
		if err != nil || !ok {
			s.deps.Log.Debug("LADO could not name a rule's project", "project", id, "err", err)
			return ""
		}
		return projectName(project)
	case domain.LadoScopeTask:
		task, ok, err := s.deps.Store.LadoTask(ctx, domain.LadoTaskID(id))
		if err != nil || !ok {
			s.deps.Log.Debug("LADO could not name a rule's task", "task", id, "err", err)
			return ""
		}
		return task.Issue.Title
	}
	return ""
}

func projectName(project domain.ProjectRecord) string {
	if project.DisplayName != "" {
		return project.DisplayName
	}
	return project.ID
}

func (s *Service) rules(ctx context.Context) ([]domain.LadoRule, error) {
	rules, err := s.deps.Store.ListLadoRules(ctx, true)
	if err != nil {
		return nil, s.loadFailed(err)
	}
	return rules, nil
}

func (s *Service) rule(ctx context.Context, id domain.LadoRuleID) (domain.LadoRule, error) {
	rule, ok, err := s.deps.Store.LadoRule(ctx, id)
	if err != nil {
		return domain.LadoRule{}, s.loadFailed(err)
	}
	if !ok {
		return domain.LadoRule{}, ruleNotFound(id)
	}
	return rule, nil
}

// task reads a task LADO shows: one with an issue.
func (s *Service) task(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, error) {
	task, ok, err := s.deps.Store.LadoTask(ctx, id)
	if err != nil {
		return domain.LadoTask{}, s.loadFailed(err)
	}
	if !ok || task.IssueRef == "" {
		return domain.LadoTask{}, scopeNotFound(domain.LadoScopeTask, string(id))
	}
	return task, nil
}

func (s *Service) newVersion(id domain.LadoRuleID, number int, content domain.LadoRuleContent, actor string, at time.Time) domain.LadoRuleVersion {
	return domain.LadoRuleVersion{
		ID:        s.newVersionID(),
		RuleID:    id,
		Number:    number,
		Content:   content,
		SHA256:    content.SHA256(),
		Actor:     actor,
		CreatedAt: at,
	}
}

func (s *Service) newVersionID() domain.LadoRuleVersionID {
	return domain.LadoRuleVersionID("lado-rule-version-" + s.deps.NewID())
}

// event is a rule change's audit event.
func (s *Service) event(actor, action string, id domain.LadoRuleID, at time.Time, payload any) domain.LadoAuditEvent {
	// Payloads hold strings, numbers and booleans, which always encode.
	data, _ := json.Marshal(payload)
	return domain.LadoAuditEvent{At: at, Actor: actor, Action: action, SubjectKind: "rule", SubjectID: string(id), Payload: data}
}

func (s *Service) now() time.Time {
	return s.deps.Clock().UTC()
}

func (s *Service) loadFailed(err error) error {
	s.deps.Log.Error("LADO could not load its rules", "err", err)
	return apierr.Internal("LADO_RULES_LOAD_FAILED", "Failed to load the rules")
}

func (s *Service) saveFailed(err error) error {
	s.deps.Log.Error("LADO could not save a rule change", "err", err)
	return apierr.Internal("LADO_RULES_SAVE_FAILED", "Failed to save the rule change")
}

// versionFailed maps a refused version: another change added one first.
func (s *Service) versionFailed(err error, base int) error {
	if errors.Is(err, domain.ErrLadoRuleChanged) {
		return ruleChanged(base)
	}
	return s.saveFailed(err)
}

func ruleNotFound(id domain.LadoRuleID) error {
	return apierr.New(apierr.KindNotFound, "LADO_RULE_NOT_FOUND", "Unknown rule", map[string]any{"rule": string(id)})
}

func ruleProtected(id domain.LadoRuleID) error {
	return apierr.New(apierr.KindForbidden, "LADO_RULE_PROTECTED",
		"This rule is protected: it is always on, and only the permission ceiling's mode can change.", map[string]any{"rule": string(id)})
}

func ruleChanged(base int) error {
	return apierr.Conflict("LADO_RULE_CHANGED", "The rule changed since you opened it. Review the latest version and try again.",
		map[string]any{"baseVersion": base})
}

func scopeNotFound(scope domain.LadoRuleScope, id string) error {
	return apierr.New(apierr.KindNotFound, "LADO_SCOPE_NOT_FOUND", fmt.Sprintf("LADO does not know the %s %s.", scope, id),
		map[string]any{"scope": string(scope), "scopeId": id})
}
