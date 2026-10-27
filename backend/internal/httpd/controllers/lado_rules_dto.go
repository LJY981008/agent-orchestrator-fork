package controllers

// LADO fork: DTOs for rules, bindings and the effective rules
// (docs/LJY-PRD/TDD.md §13, docs/LJY-PRD/HARNESS.md §3-§8 and §14).

import "time"

// LadoRuleIDParam names a rule in /api/v1/lado/rules routes.
type LadoRuleIDParam struct {
	RuleID string `path:"ruleId" description:"LADO rule id, such as agent.permission.ceiling."`
}

// ListLadoRulesQuery selects the rules to list.
type ListLadoRulesQuery struct {
	IncludeArchived bool `query:"includeArchived,omitempty" description:"When true, archived rules are listed too."`
}

// LadoEffectiveQuery names the project or the task whose rules are resolved.
type LadoEffectiveQuery struct {
	ProjectID string `query:"projectId,omitempty" description:"Project to resolve. Name exactly one of projectId and taskId."`
	TaskID    string `query:"taskId,omitempty" description:"Task to resolve at its latest plan revision. Name exactly one of projectId and taskId."`
}

// LadoRuleCeiling is the permission ceiling's setting.
type LadoRuleCeiling struct {
	Mode string `json:"mode" enum:"accept-edits,auto,bypass-permissions" description:"The highest permission mode a LADO execution may run with."`
}

// LadoRulePaths is the protected paths' setting.
type LadoRulePaths struct {
	Globs []string `json:"globs" description:"1 to 50 globs, each relative with / separators, no .. and path.Match syntax per segment; ** matches any number of segments and a glob without a wildcard covers everything below it. A changed path one matches fails verification unless the approved plan's affected_paths names it."`
}

// LadoRuleOverrideResponse forces a rule on or off for one project or task.
type LadoRuleOverrideResponse struct {
	Scope   string `json:"scope" enum:"project,task"`
	ScopeID string `json:"scopeId"`
	State   string `json:"state" enum:"force_on,force_off"`
	Label   string `json:"label" description:"The project's name or the task's issue title; empty when LADO no longer finds the scope."`
}

// LadoRuleResponse is a rule with its active version and its bindings.
type LadoRuleResponse struct {
	ID            string                     `json:"id"`
	Name          string                     `json:"name"`
	Category      string                     `json:"category"`
	Instruction   string                     `json:"instruction" description:"The text workers get while the rule is on."`
	Builtin       bool                       `json:"builtin" description:"LADO's own rule, which is never deleted."`
	Protected     bool                       `json:"protected" description:"Always on: it takes no binding and is never archived; only the permission ceiling's mode changes."`
	Archived      bool                       `json:"archived"`
	Version       int                        `json:"version" description:"The active version's number; an edit sends it back as baseVersion."`
	VersionID     string                     `json:"versionId"`
	SHA256        string                     `json:"sha256" description:"Hash of the active version's content."`
	Enforcement   []string                   `json:"enforcement" description:"How LADO applies the rule: prompt, validator, tool_policy or workflow_gate."`
	Strength      string                     `json:"strength" enum:"prevents,detects,advisory"`
	Ceiling       *LadoRuleCeiling           `json:"ceiling,omitempty" description:"The permission ceiling's setting; only that rule has one."`
	Paths         *LadoRulePaths             `json:"paths,omitempty" description:"The protected paths' globs; only paths.protected has them."`
	ConflictsWith []string                   `json:"conflictsWith" description:"Rules that cannot be on together with this one."`
	Global        string                     `json:"global" enum:"on,off" description:"The Global binding, which applies to every LADO project."`
	Overrides     []LadoRuleOverrideResponse `json:"overrides" description:"Project and task bindings; a scope without one inherits."`
	CreatedAt     time.Time                  `json:"createdAt"`
	UpdatedAt     time.Time                  `json:"updatedAt" description:"When the active version was made."`
	ArchivedAt    *time.Time                 `json:"archivedAt,omitempty"`
}

// LadoRuleListResponse lists the rules, oldest first.
type LadoRuleListResponse struct {
	Rules []LadoRuleResponse `json:"rules"`
}

// LadoRuleVersionResponse is one version of a rule. Versions never change.
type LadoRuleVersionResponse struct {
	ID            string           `json:"id"`
	Number        int              `json:"number"`
	Name          string           `json:"name"`
	Category      string           `json:"category"`
	Instruction   string           `json:"instruction"`
	Enforcement   []string         `json:"enforcement"`
	Ceiling       *LadoRuleCeiling `json:"ceiling,omitempty"`
	Paths         *LadoRulePaths   `json:"paths,omitempty"`
	ConflictsWith []string         `json:"conflictsWith"`
	SHA256        string           `json:"sha256"`
	Actor         string           `json:"actor" description:"Who made the version: lado for a built-in's seed, api for an unverified caller, human or human:dev-presence for a confirmed change."`
	CreatedAt     time.Time        `json:"createdAt"`
}

// LadoRuleDetailResponse is a rule with its versions, oldest first.
type LadoRuleDetailResponse struct {
	Rule     LadoRuleResponse          `json:"rule"`
	Versions []LadoRuleVersionResponse `json:"versions"`
}

// LadoImpactProjectResponse is a LADO project whose rules a change changes.
type LadoImpactProjectResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// LadoImpactTaskResponse is an approved task a change sends back to review.
type LadoImpactTaskResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Title     string `json:"title" description:"The issue's title as LADO last read it."`
	IssueRef  string `json:"issueRef"`
}

// LadoRuleImpactResponse is what a rule change reaches.
type LadoRuleImpactResponse struct {
	Projects []LadoImpactProjectResponse `json:"projects"`
	Tasks    []LadoImpactTaskResponse    `json:"tasks" description:"Tasks whose approval matches the rules now and would need a new one after the change."`
}

// LadoRuleChangeResponse is a rule change, made or shown by a dry run.
type LadoRuleChangeResponse struct {
	Applied bool                   `json:"applied" description:"False for a dry run, which changes nothing."`
	Rule    *LadoRuleResponse      `json:"rule,omitempty" description:"The rule after the change, or as it would be after a dry run."`
	Impact  LadoRuleImpactResponse `json:"impact"`
}

// CreateLadoRuleRequest creates a user rule, whose instruction reaches
// workers as text.
type CreateLadoRuleRequest struct {
	Name          string   `json:"name"`
	Category      string   `json:"category,omitempty"`
	Instruction   string   `json:"instruction"`
	ConflictsWith []string `json:"conflictsWith,omitempty" description:"Rules that cannot be on together with this one."`
	Scope         string   `json:"scope" enum:"global,project,task" description:"Where the rule starts. At global it is on or off for every LADO project as on says; for a project or a task it is off at Global and forced on there."`
	ScopeID       string   `json:"scopeId,omitempty" description:"The project or task id; empty at global."`
	On            bool     `json:"on,omitempty" description:"Turn the rule on at Global; only for scope global."`
	DryRun        bool     `json:"dryRun,omitempty" description:"Show the change and what it reaches without making it."`
}

// UpdateLadoRuleRequest changes a rule. An omitted field keeps its value.
// Changing the permission ceiling or the protected paths needs a
// human-presence proof in X-LADO-Presence, which the desktop app adds after
// the developer confirms.
type UpdateLadoRuleRequest struct {
	BaseVersion   int              `json:"baseVersion" description:"The version the change started from; a newer one refuses the change."`
	Name          *string          `json:"name,omitempty"`
	Category      *string          `json:"category,omitempty"`
	Instruction   *string          `json:"instruction,omitempty"`
	ConflictsWith *[]string        `json:"conflictsWith,omitempty"`
	Ceiling       *LadoRuleCeiling `json:"ceiling,omitempty" description:"The permission ceiling's new mode; only that rule takes one."`
	Paths         *LadoRulePaths   `json:"paths,omitempty" description:"The protected paths' new globs; only paths.protected takes them. Invalid globs refuse with LADO_PROTECTED_PATHS_INVALID and the first one's index and reason."`
	Archived      *bool            `json:"archived,omitempty" description:"Archive or unarchive the rule."`
	Reason        string           `json:"reason,omitempty" description:"Goes into the audit log."`
	DryRun        bool             `json:"dryRun,omitempty" description:"Show the change and what it reaches without making it; needs no presence proof."`
}

// SetLadoBindingRequest turns a rule on or off at Global, or forces it on or
// off for one project or task.
type SetLadoBindingRequest struct {
	RuleID  string `json:"ruleId"`
	Scope   string `json:"scope" enum:"global,project,task"`
	ScopeID string `json:"scopeId,omitempty" description:"The project or task id; empty at global."`
	State   string `json:"state" enum:"on,off,inherit,force_on,force_off" description:"on or off at global; inherit, force_on or force_off for a project or a task. inherit removes the binding."`
	DryRun  bool   `json:"dryRun,omitempty" description:"Show the change and what it reaches without making it."`
}

// LadoEffectiveAgentResponse is the agent that runs a task.
type LadoEffectiveAgentResponse struct {
	Harness string `json:"harness" description:"Empty when nobody chose an agent."`
	Model   string `json:"model"`
	Source  string `json:"source" description:"task or project: whose choice it is; empty when nobody chose an agent."`
}

// LadoRuleSourceResponse is the binding that decided whether a rule applies.
type LadoRuleSourceResponse struct {
	Scope   string `json:"scope" enum:"global,project,task"`
	ScopeID string `json:"scopeId"`
	State   string `json:"state" enum:"on,off,force_on,force_off"`
}

// LadoEffectiveRuleResponse is one rule that is not archived, as it
// resolved.
type LadoEffectiveRuleResponse struct {
	RuleID      string                 `json:"ruleId"`
	Name        string                 `json:"name"`
	Category    string                 `json:"category"`
	Builtin     bool                   `json:"builtin"`
	Protected   bool                   `json:"protected"`
	Version     int                    `json:"version"`
	On          bool                   `json:"on"`
	Strength    string                 `json:"strength" enum:"prevents,detects,advisory"`
	Enforcement []string               `json:"enforcement"`
	Global      string                 `json:"global" enum:"on,off"`
	Project     string                 `json:"project" enum:"inherit,force_on,force_off" description:"The rule's binding for the project; a protected rule ignores it."`
	Task        string                 `json:"task" enum:"inherit,force_on,force_off" description:"The rule's binding for the task; inherit for a project."`
	Source      LadoRuleSourceResponse `json:"source"`
}

// LadoAORuleResponse is one AO project instruction, which LADO shows
// read-only.
type LadoAORuleResponse struct {
	Kind      string `json:"kind" enum:"worker_instructions,worker_instructions_file,orchestrator_instructions"`
	Path      string `json:"path" description:"The configured agentRulesFile; empty for the other kinds."`
	SHA256    string `json:"sha256" description:"Hash of the trimmed text; empty when the file was not read."`
	Chars     int    `json:"chars"`
	ReadError string `json:"readError" description:"Why the file was not read; empty when it was."`
}

// LadoCapabilityResponse is what the agent can do about one class the rules
// need.
type LadoCapabilityResponse struct {
	Class  string `json:"class" enum:"prompt,permission_mode"`
	Status string `json:"status" enum:"supported,degraded,unsupported,unverified"`
}

// LadoPolicyProblemResponse is one thing rule resolution found.
type LadoPolicyProblemResponse struct {
	Code     string            `json:"code" description:"Stable problem code the app translates."`
	Severity string            `json:"severity" enum:"blocking,warning,info" description:"A blocking problem blocks the task."`
	Rules    []string          `json:"rules"`
	Details  map[string]string `json:"details" nullable:"false" description:"The values the code is about, such as the plan's and the rules' ceiling."`
}

// LadoApprovedRulesResponse compares a task's valid approval with the rules
// now.
type LadoApprovedRulesResponse struct {
	SnapshotSHA256 string    `json:"snapshotSha256" description:"Hash of the rule snapshot fixed in the approval."`
	ApprovedAt     time.Time `json:"approvedAt"`
	Revision       int       `json:"revision" description:"The approved plan revision's number."`
	Matches        bool      `json:"matches" description:"The rules still give the approved snapshot."`
	Blocking       bool      `json:"blocking" description:"The approved snapshot holds a blocking problem."`
	Added          []string  `json:"added" description:"Rules on now but not at the approval."`
	Removed        []string  `json:"removed" description:"Rules on at the approval but not now."`
	Changed        []string  `json:"changed" description:"Rules on in both with another version or binding."`
	// The local setup the approval fixed, next to now.
	SetupAdded     []string                            `json:"setupAdded" description:"Setup items on now but not at the approval."`
	SetupRemoved   []string                            `json:"setupRemoved" description:"Setup items on at the approval but not now; setup names them."`
	SetupChanged   []string                            `json:"setupChanged" description:"Setup items on in both with another version or binding, or with other plugins installed for an enabledPlugins setting."`
	Setup          []LadoSnapshotSetupItemResponse     `json:"setup" description:"The setup items on at the approval."`
	AOSystemSHA256 string                              `json:"aoSystemSha256" description:"The AO system layer at the approval; empty when it fixed no local setup."`
	Accepted       []LadoSetupAcceptedConflictResponse `json:"accepted" description:"The setup warnings accepted at the approval."`
}

// LadoEffectiveResponse is what the rules give a project, or a task at its
// latest plan revision, now: the snapshot an approval would fix.
type LadoEffectiveResponse struct {
	ProjectID         string                      `json:"projectId"`
	TaskID            string                      `json:"taskId" description:"Empty for a project."`
	Agent             LadoEffectiveAgentResponse  `json:"agent"`
	PermissionCeiling string                      `json:"permissionCeiling" enum:"accept-edits,auto,bypass-permissions"`
	PermissionMode    string                      `json:"permissionMode" enum:"accept-edits,auto,bypass-permissions" description:"The mode an execution runs with: the plan's ceiling, lowered to the rules' ceiling."`
	Rules             []LadoEffectiveRuleResponse `json:"rules" description:"Rules that are not archived, oldest first."`
	AORules           []LadoAORuleResponse        `json:"aoRules" description:"The project's AO instructions; the snapshot keeps only the worker kinds."`
	Prompt            string                      `json:"prompt" description:"The rule text a worker gets; empty when no rule has any."`
	Capabilities      []LadoCapabilityResponse    `json:"capabilities" description:"What a task's rules need from its agent; empty for a project."`
	Problems          []LadoPolicyProblemResponse `json:"problems" description:"Sorted by severity, code and rules."`
	Blocking          bool                        `json:"blocking"`
	SnapshotSHA256    string                      `json:"snapshotSha256" description:"Hash of the rule snapshot; an approval sends it as policySha256."`
	Setup             LadoEffectiveSetupResponse  `json:"setup" description:"The local setup of the agent's tool, which the snapshot fixes with the rules."`
	Approved          *LadoApprovedRulesResponse  `json:"approved,omitempty" description:"The task's valid approval next to now; missing without one, and for an approval given before LADO had rules."`
}
