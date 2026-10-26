package ladopolicy

import (
	"bytes"
	"cmp"
	"encoding/json"
	"slices"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Compiler names the snapshot format. SnapshotContent's fields, their JSON
// names and their order are the format: changing any of them needs a new
// name.
const Compiler = "lado-policy/2"

// SnapshotContent is what applied to one approval (docs/LJY-PRD/HARNESS.md
// §8): the on rules with the binding behind each, the AO worker
// instructions, the on setup items of the agent's tool with the binding
// behind each, the AO system layer, the accepted setup warnings, the worker's
// rule text, the agent and its permission mode, and the problems found. Lists
// are never nil, so they encode as [].
type SnapshotContent struct {
	Compiler          string                `json:"compiler"`
	AOBuild           string                `json:"aoBuild"`
	ProjectID         domain.ProjectID      `json:"projectId"`
	TaskID            domain.LadoTaskID     `json:"taskId"`
	Agent             SnapshotAgent         `json:"agent"`
	PermissionCeiling domain.PermissionMode `json:"permissionCeiling"`
	Rules             []SnapshotRule        `json:"rules"`
	AORules           []SnapshotAORule      `json:"aoRules"`
	// Setup are the on items of the agent tool's local setup.
	Setup             []SnapshotSetupItem        `json:"setup"`
	AOSystem          SnapshotAOSystem           `json:"aoSystem"`
	AcceptedConflicts []SnapshotAcceptedConflict `json:"acceptedConflicts"`
	Prompt            string                     `json:"prompt"`
	// WorkflowGates are the on rules the daemon enforces as gates.
	WorkflowGates []domain.LadoRuleID        `json:"workflowGates"`
	Capabilities  []SnapshotCapability       `json:"capabilities"`
	Problems      []domain.LadoPolicyProblem `json:"problems"`
}

// SnapshotAgent is the agent that runs the task and the permission mode it
// runs with.
type SnapshotAgent struct {
	Harness        domain.AgentHarness   `json:"harness"`
	Model          string                `json:"model"`
	PermissionMode domain.PermissionMode `json:"permissionMode"`
}

// SnapshotRule is one on rule: the version that applied and why it applied.
type SnapshotRule struct {
	RuleID      domain.LadoRuleID        `json:"ruleId"`
	VersionID   domain.LadoRuleVersionID `json:"versionId"`
	Version     int                      `json:"version"`
	SHA256      string                   `json:"sha256"`
	Name        string                   `json:"name"`
	Protected   bool                     `json:"protected"`
	Enforcement []domain.LadoEnforcement `json:"enforcement"`
	Strength    domain.LadoStrength      `json:"strength"`
	Source      SnapshotSource           `json:"source"`
}

// SnapshotSource is the binding behind a SnapshotRule.
type SnapshotSource struct {
	Scope   domain.LadoRuleScope    `json:"scope"`
	ScopeID string                  `json:"scopeId"`
	State   domain.LadoBindingState `json:"state"`
}

// SnapshotAORule is one AO worker instruction. SHA256 is empty for a file
// that was not read.
type SnapshotAORule struct {
	Kind   AORuleKind `json:"kind"`
	Path   string     `json:"path"`
	SHA256 string     `json:"sha256"`
}

// SnapshotSetupItem is one on setup item: the version that applied and why
// it applied. InstalledSHA256 is set for an enabledPlugins setting only
// (SetupItemState).
type SnapshotSetupItem struct {
	ItemID          domain.LadoSetupItemID        `json:"itemId"`
	Tool            domain.LadoSetupTool          `json:"tool"`
	Kind            domain.LadoSetupKind          `json:"kind"`
	Name            string                        `json:"name"`
	VersionID       domain.LadoSetupItemVersionID `json:"versionId"`
	Version         int                           `json:"version"`
	SHA256          string                        `json:"sha256"`
	Source          SnapshotSource                `json:"source"`
	InstalledSHA256 string                        `json:"installedSha256,omitempty"`
}

// SnapshotAOSystem is the AO system layer: what AO adds to every session.
// SHA256 is empty when LADO resolved no local setup.
type SnapshotAOSystem struct {
	SHA256 string `json:"sha256"`
}

// SnapshotAcceptedConflict is a setup conflict warning the developer
// accepted, which left the problems.
type SnapshotAcceptedConflict struct {
	Code         string                          `json:"code"`
	ItemID       domain.LadoSetupItemID          `json:"itemId"`
	Detail       string                          `json:"detail"`
	AcceptanceID domain.LadoConflictAcceptanceID `json:"acceptanceId"`
}

// SnapshotCapability is what the agent can do about one class the rules
// need.
type SnapshotCapability struct {
	Class  CapabilityClass  `json:"class"`
	Status CapabilityStatus `json:"status"`
}

// snapshotContent builds the snapshot of r. Only on rules, on setup items and
// the worker kinds of AO instructions go in; the orchestrator's do not reach
// workers.
func snapshotContent(in Input, r Resolution) SnapshotContent {
	setup, accepted := snapshotSetup(in.Setup)
	c := SnapshotContent{
		Compiler:          Compiler,
		AOBuild:           in.AOBuild,
		ProjectID:         in.ProjectID,
		TaskID:            in.TaskID,
		Agent:             SnapshotAgent{Harness: in.Agent.Harness, Model: in.Agent.Model, PermissionMode: r.Mode},
		PermissionCeiling: r.Ceiling,
		Rules:             []SnapshotRule{},
		AORules:           []SnapshotAORule{},
		Setup:             setup,
		AOSystem:          SnapshotAOSystem{SHA256: in.Setup.AOSystemSHA256},
		AcceptedConflicts: accepted,
		Prompt:            r.Prompt,
		WorkflowGates:     []domain.LadoRuleID{},
		Capabilities:      []SnapshotCapability{},
		Problems:          r.Problems,
	}
	for _, e := range r.Entries {
		if !e.On {
			continue
		}
		content := e.Rule.Version.Content.Normalize()
		c.Rules = append(c.Rules, SnapshotRule{
			RuleID:      e.Rule.ID,
			VersionID:   e.Rule.Version.ID,
			Version:     e.Rule.Version.Number,
			SHA256:      e.Rule.Version.SHA256,
			Name:        content.Name,
			Protected:   e.Rule.Protected,
			Enforcement: content.Enforcement,
			Strength:    content.Strength(),
			Source:      SnapshotSource{Scope: e.Source.Scope, ScopeID: e.Source.ScopeID, State: e.Source.State},
		})
		if content.Has(domain.LadoEnforcementWorkflowGate) {
			c.WorkflowGates = append(c.WorkflowGates, e.Rule.ID)
		}
	}
	for _, rule := range in.AORules {
		if rule.Kind == AORuleWorkerInstructions || rule.Kind == AORuleWorkerInstructionsFile {
			c.AORules = append(c.AORules, SnapshotAORule{Kind: rule.Kind, Path: rule.Path, SHA256: rule.SHA256})
		}
	}
	slices.SortStableFunc(c.AORules, func(a, b SnapshotAORule) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Path, b.Path))
	})
	for _, capability := range r.Capabilities {
		c.Capabilities = append(c.Capabilities, SnapshotCapability(capability))
	}
	return c
}

// encodeCanonical returns the snapshot's canonical bytes: the fields in
// declaration order, HTML characters as they are, no trailing newline.
func encodeCanonical(c SnapshotContent) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// The content holds only strings, numbers, booleans and string maps,
	// which cannot fail to encode.
	_ = enc.Encode(c)
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}
