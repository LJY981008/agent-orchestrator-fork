package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// LadoRuleID identifies a LADO rule. Built-in rules have fixed ids, such as
// LadoRulePlanApproval.
type LadoRuleID string

// LadoRuleVersionID identifies one version of a rule.
type LadoRuleVersionID string

// LadoRuleScope is where a rule binding applies (docs/LJY-PRD/HARNESS.md §3).
type LadoRuleScope string

// Rule scopes.
const (
	LadoScopeGlobal  LadoRuleScope = "global"
	LadoScopeProject LadoRuleScope = "project"
	LadoScopeTask    LadoRuleScope = "task"
)

// LadoBindingState is the value of a rule binding (docs/LJY-PRD/HARNESS.md
// §4). The Global binding is on or off; a Project or Task binding forces the
// rule on or off. Inherit means no binding: a request may ask for it, which
// removes the binding, but it is never stored.
type LadoBindingState string

// Binding states.
const (
	LadoBindingOn       LadoBindingState = "on"
	LadoBindingOff      LadoBindingState = "off"
	LadoBindingForceOn  LadoBindingState = "force_on"
	LadoBindingForceOff LadoBindingState = "force_off"
	LadoBindingInherit  LadoBindingState = "inherit"
)

// LadoEnforcement is one way LADO applies a rule (docs/LJY-PRD/HARNESS.md
// §6).
type LadoEnforcement string

// Enforcement classes.
const (
	LadoEnforcementPrompt       LadoEnforcement = "prompt"
	LadoEnforcementValidator    LadoEnforcement = "validator"
	LadoEnforcementToolPolicy   LadoEnforcement = "tool_policy"
	LadoEnforcementWorkflowGate LadoEnforcement = "workflow_gate"
	// LadoEnforcementPlanRole makes a rule a plan worker role's instructions
	// (docs/LJY-PRD/TDD.md §5.1): its text reaches the plan worker sessions of
	// that role only, never an execution, and the rule stays out of rule
	// snapshots, so editing it changes no approval.
	LadoEnforcementPlanRole LadoEnforcement = "plan_role"
)

// LadoRuleCategoryPlanning is the category of the plan worker roles' rules.
const LadoRuleCategoryPlanning = "planning"

// LadoStrength is how firmly a rule holds, which follows from its
// enforcement (docs/LJY-PRD/HARNESS.md §6).
type LadoStrength string

// Rule strengths, firmest first.
const (
	LadoStrengthPrevents LadoStrength = "prevents"
	LadoStrengthDetects  LadoStrength = "detects"
	LadoStrengthAdvisory LadoStrength = "advisory"
)

// Built-in rules. LADO seeds them at daemon start.
const (
	// LadoRulePlanApproval is the protected gate that a task runs only from
	// an approved plan.
	LadoRulePlanApproval LadoRuleID = "plan.approval.required"
	// LadoRulePermissionCeiling is the protected rule whose LadoCeilingConfig
	// caps the permission mode of LADO executions.
	LadoRulePermissionCeiling LadoRuleID = "agent.permission.ceiling"
	// LadoRuleKoreanCommits asks workers to write commit messages in Korean.
	LadoRuleKoreanCommits LadoRuleID = "git.commit.message.korean"
	// LadoRuleSetupPresence is the protected gate that local setup changes
	// which can run commands or widen access need the desktop app's
	// confirmation (LadoSetupNeedsPresence).
	LadoRuleSetupPresence LadoRuleID = "setup.change.requires_presence"
	// LadoRuleIntegrationApproval is the protected gate that a PR head
	// merges only with the developer's integration approval
	// (docs/LJY-PRD/TDD.md §7.1, §7.2).
	LadoRuleIntegrationApproval LadoRuleID = "integration.approval.required"
	// LadoRuleNoDirectWrite is the protected gate that workers never write
	// to the base branch, locally or on the remote (docs/LJY-PRD/TDD.md
	// §7.1, §7.3).
	LadoRuleNoDirectWrite LadoRuleID = "git.protected_branch.no_direct_write"
	// LadoRuleTestsMustPass is the protected gate that the required CI
	// checks and the project's check commands pass on the head
	// (docs/LJY-PRD/TDD.md §8.1, §8.2).
	LadoRuleTestsMustPass LadoRuleID = "test.command.must_pass"
	// LadoRuleProtectedPaths is the protected gate whose
	// LadoProtectedPathsConfig names the paths a PR changes only when its
	// approved plan names them.
	LadoRuleProtectedPaths LadoRuleID = "paths.protected"
	// LadoRulePlanDesigner and LadoRulePlanCritic are the instructions of the
	// plan workers' designer and critic (LadoEnforcementPlanRole). They are
	// global rules the developer edits like any other.
	LadoRulePlanDesigner LadoRuleID = "plan.role.designer"
	LadoRulePlanCritic   LadoRuleID = "plan.role.critic"
)

// PlanRole returns the plan worker role whose instructions the rule holds, and
// whether it is a role's rule.
func (c LadoRuleContent) PlanRole(id LadoRuleID) (LadoPlanRole, bool) {
	if !c.Has(LadoEnforcementPlanRole) {
		return "", false
	}
	switch id {
	case LadoRulePlanDesigner:
		return LadoPlanRoleDesigner, true
	case LadoRulePlanCritic:
		return LadoPlanRoleCritic, true
	}
	return "", false
}

// LadoRuleContent is what one rule version says (docs/LJY-PRD/HARNESS.md
// §5). Its hash tells versions apart: an edit that leaves the hash as it was
// adds no version.
type LadoRuleContent struct {
	Name        string            `json:"name"`
	Category    string            `json:"category"`
	Instruction string            `json:"instruction"`
	Enforcement []LadoEnforcement `json:"enforcement"`
	// Config holds the rule's settings as JSON, "{}" for a rule without
	// any. The permission ceiling's is a LadoCeilingConfig.
	Config json.RawMessage `json:"config"`
	// Conflicts names the rules that cannot be on together with this one.
	Conflicts []LadoRuleID `json:"conflictsWith"`
}

// Normalize returns the content as it is hashed and stored: text trimmed,
// conflicts sorted without duplicates, the config compacted and no nil
// slices. A config that is empty, null or not JSON becomes "{}".
func (c LadoRuleContent) Normalize() LadoRuleContent {
	c.Name = strings.TrimSpace(c.Name)
	c.Category = strings.TrimSpace(c.Category)
	c.Instruction = strings.TrimSpace(c.Instruction)
	if c.Enforcement == nil {
		c.Enforcement = []LadoEnforcement{}
	}
	// Sort a copy: the caller's slice stays as it was.
	conflicts := append([]LadoRuleID{}, c.Conflicts...)
	slices.Sort(conflicts)
	c.Conflicts = slices.Compact(conflicts)
	var config bytes.Buffer
	if err := json.Compact(&config, c.Config); err != nil || config.String() == "null" {
		c.Config = json.RawMessage("{}")
	} else {
		c.Config = config.Bytes()
	}
	return c
}

// CanonicalJSON is the content's hashed form: the normalized fields in
// declaration order, with HTML characters left as they are. Changing it
// changes every rule version hash.
func (c LadoRuleContent) CanonicalJSON() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Normalize leaves strings, string slices and compacted JSON, which
	// cannot fail to encode.
	_ = enc.Encode(c.Normalize())
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// SHA256 returns the hex sha256 of the canonical JSON.
func (c LadoRuleContent) SHA256() string {
	sum := sha256.Sum256(c.CanonicalJSON())
	return hex.EncodeToString(sum[:])
}

// Strength reports how firmly the rule holds: a workflow gate or a tool
// policy prevents, a validator detects, and a rule that only reaches the
// agent's prompt is advisory.
func (c LadoRuleContent) Strength() LadoStrength {
	switch {
	case c.Has(LadoEnforcementWorkflowGate), c.Has(LadoEnforcementToolPolicy):
		return LadoStrengthPrevents
	case c.Has(LadoEnforcementValidator):
		return LadoStrengthDetects
	default:
		return LadoStrengthAdvisory
	}
}

// Has reports whether the rule uses the enforcement class.
func (c LadoRuleContent) Has(e LadoEnforcement) bool {
	return slices.Contains(c.Enforcement, e)
}

// LadoRuleVersion is one version of a rule. Versions never change; an edit
// adds the next one (docs/LJY-PRD/HARNESS.md §8).
type LadoRuleVersion struct {
	ID     LadoRuleVersionID
	RuleID LadoRuleID
	// Number counts the rule's versions from 1. The highest is active.
	Number  int
	Content LadoRuleContent
	// SHA256 is Content.SHA256().
	SHA256 string
	// Actor is who made the version, as in the audit log: "lado" for a
	// built-in's seed, "api" for an unverified caller.
	Actor     string
	CreatedAt time.Time
}

// LadoRuleBinding turns a rule on or off for one scope. ScopeID is empty for
// the Global binding and names a project or a task otherwise.
type LadoRuleBinding struct {
	RuleID    LadoRuleID
	Scope     LadoRuleScope
	ScopeID   string
	State     LadoBindingState
	UpdatedAt time.Time
}

// LadoRule is a rule definition with its active version and its bindings
// (docs/LJY-PRD/HARNESS.md §3). Definitions are global; bindings say where a
// rule applies.
type LadoRule struct {
	ID LadoRuleID
	// Builtin rules come with LADO and are never deleted. Protected rules are
	// always on: they refuse bindings and archiving, and only their settings
	// change.
	Builtin   bool
	Protected bool
	// Version is the active version, the one with the highest number.
	Version LadoRuleVersion
	// Global is the Global binding, on or off.
	Global LadoBindingState
	// Overrides are the Project and Task bindings. A scope without one
	// inherits.
	Overrides []LadoRuleBinding
	CreatedAt time.Time
	// ArchivedAt is when the rule was archived, zero while it is not.
	ArchivedAt time.Time
}

// LadoRuleSeed is a built-in rule to create when it is missing, with the
// audit event that records it.
type LadoRuleSeed struct {
	Rule  LadoRule
	Event LadoAuditEvent
}

// LadoCeilingConfig is the Config of LadoRulePermissionCeiling: the highest
// permission mode a LADO execution may run with.
type LadoCeilingConfig struct {
	Mode PermissionMode `json:"mode"`
}

// LadoCeilingModes are the modes a ceiling may name, lowest first.
// PermissionModeDefault is never a ceiling, because each adapter decides what
// it allows.
var LadoCeilingModes = []PermissionMode{PermissionModeAcceptEdits, PermissionModeAuto, PermissionModeBypassPermissions}

// LadoPermissionRank orders permission modes against the ceiling: 1 to 3 for
// LadoCeilingModes, lowest first, and 0 for any other mode.
func LadoPermissionRank(m PermissionMode) int {
	return slices.Index(LadoCeilingModes, m) + 1
}

// LadoProtectedPathsConfig is the Config of LadoRuleProtectedPaths: globs in
// the syntax of a plan's affected_paths, relative to the project root.
type LadoProtectedPathsConfig struct {
	Globs []string `json:"globs"`
}

// DefaultLadoProtectedPaths returns the globs LadoRuleProtectedPaths starts
// with: CI configuration and the agents' instructions and settings.
func DefaultLadoProtectedPaths() []string {
	return []string{
		".github/**", ".gitlab-ci.yml", ".gitlab/**", "**/CLAUDE.md", "**/AGENTS.md",
		".claude/**", ".codex/**", ".mcp.json", ".ao/**",
	}
}

// LadoProtectedPathsMax is the most globs LadoRuleProtectedPaths may hold.
const LadoProtectedPathsMax = 50

// ErrLadoProtectedPathsInvalid means a protected path list is out of range or
// holds a glob that a plan could not name in affected_paths. The services
// report it as LADO_PROTECTED_PATHS_INVALID.
var ErrLadoProtectedPathsInvalid = errors.New("invalid protected paths")

// ValidateLadoProtectedPaths refuses an empty list, more than
// LadoProtectedPathsMax globs, and a glob that is not a valid affected_paths
// entry: blank or padded with spaces, absolute, with "..", a backslash, a
// drive letter or NUL, or not a valid pattern.
func ValidateLadoProtectedPaths(globs []string) error {
	if len(globs) == 0 || len(globs) > LadoProtectedPathsMax {
		return fmt.Errorf("%w: want 1 to %d globs, got %d", ErrLadoProtectedPathsInvalid, LadoProtectedPathsMax, len(globs))
	}
	for i, glob := range globs {
		if glob == "" || strings.TrimSpace(glob) != glob || !validLadoAffectedPath(glob) {
			return fmt.Errorf("%w: glob %d %q", ErrLadoProtectedPathsInvalid, i, glob)
		}
	}
	return nil
}

// LadoCommitLocaleConfig is the Config of LadoRuleKoreanCommits from its
// version 2: the language commit messages must use, and the Conventional
// Commit types whose prefix the validator strips before judging a subject.
type LadoCommitLocaleConfig struct {
	Language      string   `json:"language"`
	AllowPrefixes []string `json:"allowPrefixes"`
}

// DefaultLadoCommitLocaleConfig is the Korean commit rule's config: Korean,
// with the common Conventional Commit types.
func DefaultLadoCommitLocaleConfig() LadoCommitLocaleConfig {
	return LadoCommitLocaleConfig{
		Language:      "ko",
		AllowPrefixes: []string{"feat", "fix", "docs", "refactor", "test", "chore", "style", "perf", "build", "ci", "revert"},
	}
}

// ErrLadoRuleChanged means the rule got a newer version than the one an edit
// started from.
var ErrLadoRuleChanged = errors.New("the rule has a newer version")

// ErrLadoRuleReferenced means a rule snapshot uses one of the rule's
// versions, so the rule cannot be deleted. It can be archived instead.
var ErrLadoRuleReferenced = errors.New("a rule snapshot uses the rule")
