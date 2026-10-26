package ladopolicy

// Local setup in the rules (step 7 plan §11): the setup items LADO manages
// for the agent tool that runs a task, the AO system layer and the conflict
// warnings the developer accepted are part of what a plan approval fixes. An
// edit, a toggle or an AO update after the approval gives another snapshot,
// so the task needs review as after a rule change.

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// SetupSource resolves the local setup LADO manages for a project or a task.
// service/ladosetup implements it.
type SetupSource interface {
	EffectiveSetup(ctx context.Context, in SetupScope) (SetupResolution, error)
}

// SetupScope is what EffectiveSetup resolves for.
type SetupScope struct {
	ProjectID domain.ProjectID
	// TaskID is empty for a project.
	TaskID domain.LadoTaskID
	// Harness is the agent that runs the task, or the project's worker agent:
	// the items of its tool apply. Another agent, or none, gets no items.
	Harness domain.AgentHarness
	// Ceiling is the rules' permission ceiling, which the conflict checks
	// compare the setup's default permission mode with.
	Ceiling domain.PermissionMode
	// Change, when set, resolves the setup as it would be after the change.
	Change *SetupChange
}

// SetupResolution is the setup a scope's sessions get.
type SetupResolution struct {
	// Items are the live items of the agent's tool, on or off, in tool, kind
	// and name order.
	Items []SetupItemState
	// Problems are the conflicts of the on items that no acceptance covers.
	Problems []domain.LadoPolicyProblem
	// Accepted are the warnings a live acceptance covers, which leave the
	// problems.
	Accepted []AcceptedConflict
	// AOSystemSHA256 is the hash of what AO adds to every session it
	// launches.
	AOSystemSHA256 string
}

// SetupItemState is one setup item at its latest version, and whether it
// applies.
type SetupItemState struct {
	ItemID    domain.LadoSetupItemID
	Tool      domain.LadoSetupTool
	Kind      domain.LadoSetupKind
	Name      string
	VersionID domain.LadoSetupItemVersionID
	Version   int
	SHA256    string
	// On reports whether the item applies, and Source is the binding that
	// decided it.
	On     bool
	Source Source
	// Global is the item's Global binding, on without a stored one. Project
	// and Task are its force_on or force_off bindings for the resolved
	// project and task, LadoBindingInherit without one.
	Global, Project, Task domain.LadoBindingState
	// InstalledSHA256 is the hash of what an on enabledPlugins setting turns
	// on as Claude Code has it installed. Plugins stay where Claude Code
	// installs them, so their content is no item version; empty for every
	// other item.
	InstalledSHA256 string
}

// AcceptedConflict is a setup conflict warning that a live acceptance covers.
type AcceptedConflict struct {
	Code   string
	ItemID domain.LadoSetupItemID
	// Detail tells apart the warnings of one code on one item.
	Detail       string
	AcceptanceID domain.LadoConflictAcceptanceID
}

// SetupChange is a change to the managed setup that LADO has not made, so its
// impact shows first. Each field is one part of a change; the zero value
// changes nothing.
type SetupChange struct {
	// Item is an item as the change leaves it, with its new version: a new
	// item, an edit or a kept drift. Content is that version's content for a
	// setting or an MCP server, which the conflict checks read before it is
	// stored.
	Item    *domain.LadoSetupItemWithVersion
	Content []byte
	// Remove is an item the change ends, as deleting it does.
	Remove domain.LadoSetupItemID
	// Binding is a binding as the change sets it. LadoBindingInherit removes
	// the binding at its scope, which at Global turns the item on.
	Binding *domain.LadoSetupBinding
	// Accept is an acceptance the change gives, and Revoke one it revokes.
	Accept *domain.LadoConflictAcceptance
	Revoke domain.LadoConflictAcceptanceID
}

// CodeSetupLoadFailed is local setup that could not be resolved.
const CodeSetupLoadFailed = "LADO_SETUP_LOAD_FAILED"

// setupOf resolves the local setup of the agent in's rules apply to, with
// change applied when it is set. Without a SetupSource there is none.
func (s *Service) setupOf(ctx context.Context, in Input, change *SetupChange) (SetupResolution, error) {
	if s.deps.Setup == nil {
		return SetupResolution{}, nil
	}
	setup, err := s.deps.Setup.EffectiveSetup(ctx, SetupScope{
		ProjectID: in.ProjectID,
		TaskID:    in.TaskID,
		Harness:   in.Agent.Harness,
		Ceiling:   rulesCeiling(in.Rules),
		Change:    change,
	})
	if err != nil {
		return SetupResolution{}, s.setupFailed(err)
	}
	return setup, nil
}

// rulesCeiling is the permission ceiling Resolve finds in the rules.
func rulesCeiling(rules []domain.LadoRule) domain.PermissionMode {
	r := Resolution{Entries: resolveEntries(Input{Rules: rules})}
	return r.ceiling()
}

func (s *Service) setupFailed(err error) error {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return err
	}
	s.deps.Log.Error("LADO could not resolve the local setup", "err", err)
	return apierr.Internal(CodeSetupLoadFailed, "Failed to load the local setup")
}

// ImpactOfSetupChange is what a setup change reaches, before it is made: the
// LADO projects whose rules and setup it changes, and the approved tasks that
// would need a new approval, as Impact is for a rule change. The setup of a
// project or a task is its agent tool's, so a change to one tool's item
// reaches only what runs that tool.
func (s *Service) ImpactOfSetupChange(ctx context.Context, change SetupChange) (Impact, error) {
	rules, err := s.rules(ctx)
	if err != nil {
		return Impact{}, err
	}
	return s.impactOf(ctx, rules, func(ctx context.Context, in Input) (Input, error) {
		setup, err := s.setupOf(ctx, in, &change)
		if err != nil {
			return Input{}, err
		}
		in.Setup = setup
		return in, nil
	})
}

// SnapshotSetup are the setup item versions the snapshot uses, which the
// approval pins.
func (r Resolution) SnapshotSetup() []domain.LadoSetupItemVersionID {
	ids := make([]domain.LadoSetupItemVersionID, 0, len(r.Content.Setup))
	for _, item := range r.Content.Setup {
		ids = append(ids, item.VersionID)
	}
	return ids
}

// diffSetup compares the on setup items of two snapshots by id: added are on
// now but not then, removed on then but not now, and changed on in both with
// another version, binding or installed plugins.
func diffSetup(then, now []SnapshotSetupItem) (added, removed, changed []domain.LadoSetupItemID) {
	old := make(map[domain.LadoSetupItemID]SnapshotSetupItem, len(then))
	for _, item := range then {
		old[item.ItemID] = item
	}
	added, changed = []domain.LadoSetupItemID{}, []domain.LadoSetupItemID{}
	for _, item := range now {
		prev, ok := old[item.ItemID]
		switch {
		case !ok:
			added = append(added, item.ItemID)
		case prev.VersionID != item.VersionID || prev.Source != item.Source || prev.InstalledSHA256 != item.InstalledSHA256:
			changed = append(changed, item.ItemID)
		}
		delete(old, item.ItemID)
	}
	removed = make([]domain.LadoSetupItemID, 0, len(old))
	for id := range old {
		removed = append(removed, id)
	}
	slices.Sort(added)
	slices.Sort(removed)
	slices.Sort(changed)
	return added, removed, changed
}

// snapshotSetup returns the on items of a resolved setup and its accepted
// warnings as a snapshot holds them: items by tool, kind and name, warnings
// by code, item and detail.
func snapshotSetup(setup SetupResolution) ([]SnapshotSetupItem, []SnapshotAcceptedConflict) {
	items := []SnapshotSetupItem{}
	for _, item := range setup.Items {
		if !item.On {
			continue
		}
		items = append(items, SnapshotSetupItem{
			ItemID:          item.ItemID,
			Tool:            item.Tool,
			Kind:            item.Kind,
			Name:            item.Name,
			VersionID:       item.VersionID,
			Version:         item.Version,
			SHA256:          item.SHA256,
			Source:          SnapshotSource{Scope: item.Source.Scope, ScopeID: item.Source.ScopeID, State: item.Source.State},
			InstalledSHA256: item.InstalledSHA256,
		})
	}
	slices.SortFunc(items, func(a, b SnapshotSetupItem) int {
		return cmp.Or(cmp.Compare(a.Tool, b.Tool), cmp.Compare(a.Kind, b.Kind), strings.Compare(a.Name, b.Name), cmp.Compare(a.ItemID, b.ItemID))
	})
	accepted := make([]SnapshotAcceptedConflict, 0, len(setup.Accepted))
	for _, a := range setup.Accepted {
		accepted = append(accepted, SnapshotAcceptedConflict(a))
	}
	slices.SortFunc(accepted, func(a, b SnapshotAcceptedConflict) int {
		return cmp.Or(strings.Compare(a.Code, b.Code), cmp.Compare(a.ItemID, b.ItemID), strings.Compare(a.Detail, b.Detail),
			cmp.Compare(a.AcceptanceID, b.AcceptanceID))
	})
	return items, accepted
}
