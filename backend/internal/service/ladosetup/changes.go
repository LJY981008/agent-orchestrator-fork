package ladosetup

// Changes of the managed setup (plan §12, docs/LJY-PRD/HARNESS.md §9.5).
// Creating an item, editing a text item, deleting one, turning one on or off
// for a scope, keeping or reverting its drift, and accepting a conflict
// warning or revoking the acceptance all go through Apply. A dry run writes
// nothing and shows the change: the content before and after, the conflicts
// it adds or ends, what it reaches (the LADO projects and the approved tasks
// it sends back to review) and changeSha256, the hash the desktop app's
// confirmation signs. A change that can run code or widen access (decision
// i) is made only with that confirmation. Folder items are not edited here:
// they are edited in the managed folder, where drift offers the edit to keep
// or revert (owner decision 3).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
)

// ChangeOp is what a change does.
type ChangeOp string

// Change ops.
const (
	OpCreate      ChangeOp = "create"
	OpEdit        ChangeOp = "edit"
	OpDelete      ChangeOp = "delete"
	OpBinding     ChangeOp = "binding"
	OpDriftKeep   ChangeOp = "drift_keep"
	OpDriftRevert ChangeOp = "drift_revert"
	OpAccept      ChangeOp = "accept"
	OpRevoke      ChangeOp = "revoke"
)

// Error codes of changes.
const (
	// CodeChangeInvalid is a change that names no op, misses what its op
	// needs, carries what its op does not take, or names an item LADO does
	// not make; details name the field and why.
	CodeChangeInvalid = "LADO_SETUP_CHANGE_INVALID"
	// CodeContentInvalid is content its item cannot hold; details say why.
	CodeContentInvalid = "LADO_SETUP_CONTENT_INVALID"
	// CodeLoginKey is a setting that picks a login: a login key, or an env
	// setting with a login variable. Logins stay where the tools keep them.
	CodeLoginKey = "LADO_SETUP_LOGIN_KEY"
	// CodeItemExists is a new item whose tool, kind and name a live item
	// holds.
	CodeItemExists = "LADO_SETUP_ITEM_EXISTS"
	// CodeFolderItem is an edit of a folder item. Its files are edited in the
	// managed folder, and drift keeps the edit.
	CodeFolderItem = "LADO_SETUP_FOLDER_ITEM"
	// CodeDriftPending is an edit or a delete of an item whose managed copy
	// holds a change that was neither kept nor reverted; details name the
	// item.
	CodeDriftPending = "LADO_SETUP_DRIFT_PENDING"
	// CodeConflictNotFound is an acceptance of a warning the item's content
	// does not give.
	CodeConflictNotFound = "LADO_SETUP_CONFLICT_NOT_FOUND"
)

// The codes of rule bindings and scopes, which setup bindings and
// acceptances share.
const (
	codeBindingScopeInvalid = "LADO_BINDING_SCOPE_INVALID"
	codeBindingStateInvalid = "LADO_BINDING_STATE_INVALID"
	codeScopeNotFound       = "LADO_SCOPE_NOT_FOUND"
	codeTaskCancelled       = "LADO_TASK_CANCELLED"
)

// Warnings of changes.
const (
	// WarningManagedWriteFailed means a change was recorded, and the managed
	// folder could not be written to match it. Sessions get the stored
	// version, and drift shows the managed copy until it is reverted.
	WarningManagedWriteFailed = "LADO_SETUP_MANAGED_WRITE_FAILED"
	// WarningPreviewIncomplete means LADO could not read part of what it
	// shows about a change, such as content whose blob is gone: the content
	// it replaces, its conflicts or its impact. The change goes ahead, so an
	// item LADO cannot read can still be deleted or turned off.
	WarningPreviewIncomplete = "LADO_SETUP_PREVIEW_INCOMPLETE"
)

// Why CodeChangeInvalid and CodeContentInvalid refuse, in details["reason"].
// Codex MCP servers use the inventory's reasons too
// (domain.LadoSetupReasonNameUnsupported, LadoSetupReasonLiteralCredentials,
// LadoSetupReasonUnreadable).
const (
	reasonUnknown     = "unknown"
	reasonMissing     = "missing"
	reasonNotTaken    = "not_taken"
	reasonInvalid     = "invalid"
	reasonNotForTool  = "not_for_tool"
	reasonNotCreated  = "not_created"
	reasonNotText     = "not_text"
	reasonTooLarge    = "too_large"
	reasonNotJSON     = "not_json"
	reasonNotObject   = "not_object"
	reasonFrontMatter = "front_matter"
	reasonServerShape = "server_shape"
)

// Audit of changes.
const (
	actionItemCreated      = "setup.item_created"
	actionItemUpdated      = "setup.item_updated"
	actionItemDeleted      = "setup.item_deleted"
	actionBindingSet       = "setup.binding_set"
	actionConflictAccepted = "setup.conflict_accepted"
	actionConflictRevoked  = "setup.conflict_revoked"
	subjectAcceptance      = "setup_conflict_acceptance"
)

// maxChangeBytes bounds the content a change brings: a text or a JSON value.
const maxChangeBytes = 1 << 20

// skillFile is the file a skill of one file holds.
const skillFile = "SKILL.md"

// Change is one change of the managed setup. Each op takes its own fields,
// and the others stay empty.
type Change struct {
	Op ChangeOp
	// ItemID names the item every op but create and revoke changes.
	ItemID domain.LadoSetupItemID
	// Tool, Kind and Name name the item create makes.
	Tool domain.LadoSetupTool
	Kind domain.LadoSetupKind
	Name string
	// Content is the content create and edit give the item, as one JSON
	// value: a string, the text, for a file or a skill's SKILL.md, and the
	// value itself for a setting or an MCP server.
	Content json.RawMessage
	// BaseVersion is the latest version an edit started from.
	BaseVersion int
	// Binding is the binding a binding change sets. Inherit removes the
	// binding at its scope, which at Global turns the item on.
	Binding ChangeBinding
	// Acceptance is the warning accept accepts for the item, and where.
	Acceptance ChangeAcceptance
	// AcceptanceID names the acceptance revoke revokes.
	AcceptanceID domain.LadoConflictAcceptanceID
	// DriftSHA256 is the managed copy a drift keep keeps, as the developer
	// reviewed it.
	DriftSHA256 string
	// DryRun shows the change and writes nothing; it needs no confirmation.
	DryRun bool
}

// ChangeBinding is a binding a change sets.
type ChangeBinding struct {
	Scope   domain.LadoRuleScope
	ScopeID string
	State   domain.LadoBindingState
}

// ChangeAcceptance is a warning a change accepts, in a scope and the
// narrower ones.
type ChangeAcceptance struct {
	Code    string
	Detail  string
	Scope   domain.LadoRuleScope
	ScopeID string
}

// ChangeResult is a change shown or made.
type ChangeResult struct {
	Op ChangeOp
	// ChangeSHA256 is the hash of the change without DryRun, which the
	// desktop app's confirmation signs.
	ChangeSHA256 string
	// NeedsPresence tells that making the change needs the desktop app's
	// confirmation for ChangeSHA256.
	NeedsPresence bool
	// Applied is set when the change was made, or when it changes nothing.
	Applied bool
	// Actor made the change: human, or human:dev-presence in a lab, with a
	// confirmation, and api without one. It is empty for a dry run.
	Actor string
	// Item is the item the change is about, as the change leaves it. A dry
	// run's new item has no id yet.
	Item domain.LadoSetupItem
	// Version is the item's version after a create, an edit or a drift
	// keep or revert; a dry run's new version has no id yet.
	Version *domain.LadoSetupItemVersion
	// Before and After are the item's content before and after the change,
	// for a diff: create has no Before and delete no After, and bindings
	// and acceptances change no content.
	Before, After *ChangeContent
	// ConflictsAdded and ConflictsRemoved are the conflict results the
	// change brings to its scope and the ones it ends, leaving out those an
	// acceptance covers. Item changes are compared at Global, bindings and
	// acceptances in their scope, against the rules' permission ceiling.
	ConflictsAdded, ConflictsRemoved []Conflict
	// Impact is what the change reaches.
	Impact ladopolicy.Impact
	// Pins counts the rule snapshots that pinned a version of a deleted
	// item. They keep reading it.
	Pins int
	// Binding is the binding a binding change sets.
	Binding *domain.LadoSetupBinding
	// Acceptance is the acceptance accept gives, or revoke ends.
	Acceptance *domain.LadoConflictAcceptance
	Warnings   []string
}

// ChangeContent is one side of a change's diff: an item version's content as
// the app shows it.
type ChangeContent struct {
	ContentKind domain.LadoSetupContentKind
	SHA256      string
	Size        int64
	Executable  bool
	// Text is a file's text, a skill's SKILL.md when the skill is that one
	// file, or a JSON value written out with two-space indents. It is empty
	// for any other folder and for a file that is not text.
	Text string
	// Files are a folder's files.
	Files []domain.LadoSetupTreeEntry
}

// Apply shows a change of the managed setup, or makes it when c.DryRun is
// false. A change that needs the desktop app's confirmation asks presence
// for it (action setup.change, subject ChangeSHA256); without presence it is
// refused with LADO_PRESENCE_REQUIRED. Every write records its audit event in
// the same transaction, content goes to blobs first, and the managed folder
// is written once the store committed.
func (s *Service) Apply(ctx context.Context, c Change, presence func(action, subject string) (domain.LadoPresenceProof, error)) (ChangeResult, error) {
	if err := s.available(); err != nil {
		return ChangeResult{}, err
	}
	if err := c.check(); err != nil {
		return ChangeResult{}, err
	}
	sum, err := c.sha256()
	if err != nil {
		return ChangeResult{}, err
	}
	if c.Op == OpDriftKeep || c.Op == OpDriftRevert {
		// The drift functions take the write lock themselves.
		return s.applyDrift(ctx, c, sum, presence)
	}
	ap := approval{actor: actorAPI}
	if !c.DryRun {
		// The desktop app's proof lives a minute, so it is checked first, as
		// an adoption's is: before the wait for the write lock and the
		// conflict and impact checks, which can take longer.
		first, err := s.prepare(ctx, c)
		if err != nil {
			return ChangeResult{}, err
		}
		if first.needsPresence && !first.noop {
			if ap, err = s.changeApproval(ctx, presence, sum); err != nil {
				return ChangeResult{}, err
			}
		}
		// The checks and the write see the same setup: no adoption or other
		// change lands in between.
		if err := s.lockWrite(ctx); err != nil {
			return ChangeResult{}, err
		}
		defer s.unlockWrite()
	}
	p, err := s.prepare(ctx, c)
	if err != nil {
		return ChangeResult{}, err
	}
	res := p.result(c.Op, sum)
	if p.noop {
		res.Applied = !c.DryRun
		return res, nil
	}
	if !c.DryRun && p.needsPresence && ap.proof.Nonce == "" {
		// The item changed while the change waited for the lock.
		if ap, err = s.changeApproval(ctx, presence, sum); err != nil {
			return ChangeResult{}, err
		}
	}
	s.preview(ctx, p.scope, p.change, &res)
	if c.DryRun {
		p.hideNewIDs(&res)
		return res, nil
	}
	// From the first write on, the change runs to its end.
	ctx = context.WithoutCancel(ctx)
	if err := s.write(ctx, p, ap, sum, &res); err != nil {
		return ChangeResult{}, err
	}
	res.Applied, res.Actor = true, ap.actor
	return res, nil
}

// preview fills in the conflicts a change adds and ends and what it reaches.
// They inform the change and never hold it back: setup LADO cannot read,
// such as a version whose blob is gone, must not stop the change that ends
// it. A part that fails stays empty, with WarningPreviewIncomplete.
func (s *Service) preview(ctx context.Context, scope scopeRef, change ladopolicy.SetupChange, res *ChangeResult) {
	added, removed, err := s.conflictDiff(ctx, scope, change)
	if err == nil {
		res.ConflictsAdded, res.ConflictsRemoved = added, removed
		res.Impact, err = s.impactOf(ctx, change)
	}
	if err != nil {
		s.deps.Log.Warn("LADO could not show all of a setup change", "op", res.Op, "item", res.Item.Name, "err", err)
		res.Warnings = appendWarning(res.Warnings, WarningPreviewIncomplete)
	}
}

// beforeView shows the version a change replaces or ends, nil with a
// warning when LADO cannot read it.
func (s *Service) beforeView(it domain.LadoSetupItem, v domain.LadoSetupItemVersion, warnings *[]string) *ChangeContent {
	view, err := s.versionView(it, v)
	if err != nil {
		s.deps.Log.Warn("LADO could not read a setup item's content to show a change", "item", it.Name, "err", err)
		*warnings = appendWarning(*warnings, WarningPreviewIncomplete)
		return nil
	}
	return view
}

func appendWarning(warnings []string, w string) []string {
	if slices.Contains(warnings, w) {
		return warnings
	}
	return append(warnings, w)
}

// changeFields are the fields of a Change an op can take, by their JSON
// names.
var changeFields = []string{"itemId", "tool", "kind", "name", "content", "baseVersion", "binding", "acceptance", "acceptanceId", "driftSha256"}

// opFields are the fields each op takes; it needs all of them.
var opFields = map[ChangeOp][]string{
	OpCreate:      {"tool", "kind", "name", "content"},
	OpEdit:        {"itemId", "content", "baseVersion"},
	OpDelete:      {"itemId"},
	OpBinding:     {"itemId", "binding"},
	OpDriftKeep:   {"itemId", "driftSha256"},
	OpDriftRevert: {"itemId"},
	OpAccept:      {"itemId", "acceptance"},
	OpRevoke:      {"acceptanceId"},
}

// check refuses a change whose op is unknown, or that misses a field its op
// takes or sets one it does not take. The values are checked against the
// store later.
func (c Change) check() error {
	takes, ok := opFields[c.Op]
	if !ok {
		return changeInvalid(c.Op, "op", reasonUnknown)
	}
	set := map[string]bool{
		"itemId": c.ItemID != "", "tool": c.Tool != "", "kind": c.Kind != "", "name": c.Name != "",
		"content": len(c.Content) > 0, "baseVersion": c.BaseVersion != 0, "binding": c.Binding != ChangeBinding{},
		"acceptance": c.Acceptance != ChangeAcceptance{}, "acceptanceId": c.AcceptanceID != "", "driftSha256": c.DriftSHA256 != "",
	}
	for _, field := range changeFields {
		switch {
		case slices.Contains(takes, field) && !set[field]:
			return changeInvalid(c.Op, field, reasonMissing)
		case !slices.Contains(takes, field) && set[field]:
			return changeInvalid(c.Op, field, reasonNotTaken)
		}
	}
	switch {
	case c.Op == OpCreate && !slices.Contains(setupTools, c.Tool):
		return changeInvalid(c.Op, "tool", reasonUnknown)
	case c.Op == OpCreate && !slices.Contains(setupKinds, c.Kind):
		return changeInvalid(c.Op, "kind", reasonUnknown)
	case c.Op == OpEdit && c.BaseVersion < 1:
		return changeInvalid(c.Op, "baseVersion", reasonInvalid)
	case c.Op == OpAccept && c.Acceptance.Code == "":
		return changeInvalid(c.Op, "acceptance.code", reasonMissing)
	}
	return nil
}

// sha256 is changeSha256: the sha256 of the change's canonical JSON without
// DryRun. Fields are named as in a request, content counts in its canonical
// form, and empty fields, which the op does not take, are left out.
func (c Change) sha256() (string, error) {
	form := map[string]any{"op": string(c.Op)}
	for key, value := range map[string]string{
		"itemId": string(c.ItemID), "tool": string(c.Tool), "kind": string(c.Kind), "name": c.Name,
		"acceptanceId": string(c.AcceptanceID), "driftSha256": c.DriftSHA256,
	} {
		if value != "" {
			form[key] = value
		}
	}
	if len(c.Content) > 0 {
		content, err := canonicalJSON(c.Content)
		if err != nil {
			return "", contentInvalid(reasonNotJSON, nil)
		}
		form["content"] = json.RawMessage(content)
	}
	if c.BaseVersion != 0 {
		form["baseVersion"] = c.BaseVersion
	}
	if c.Binding != (ChangeBinding{}) {
		form["binding"] = map[string]any{"scope": string(c.Binding.Scope), "scopeId": c.Binding.ScopeID, "state": string(c.Binding.State)}
	}
	if c.Acceptance != (ChangeAcceptance{}) {
		a := c.Acceptance
		form["acceptance"] = map[string]any{"code": a.Code, "detail": a.Detail, "scope": string(a.Scope), "scopeId": a.ScopeID}
	}
	// Strings, numbers, canonical JSON and maps of them always encode.
	data, err := marshalCanonical(form)
	if err != nil {
		return "", err
	}
	return sha256Hex(data), nil
}

// scopeRef is where a change's conflicts are compared: Global without ids,
// a project, or a task with its project.
type scopeRef struct {
	project domain.ProjectID
	task    domain.LadoTaskID
}

// preparedChange is a checked change and what it writes.
type preparedChange struct {
	op   ChangeOp
	item domain.LadoSetupItem
	// latest is the item's latest version before the change; zero for a new
	// item.
	latest domain.LadoSetupItemVersion
	// content and version are the version a create or an edit adds.
	content *plannedContent
	version domain.LadoSetupItemVersion
	// binding is the binding a binding change sets, inherit removing its
	// row, and from the state stored at its scope before.
	binding *domain.LadoSetupBinding
	inherit bool
	from    domain.LadoBindingState
	// acceptance is the acceptance accept gives or revoke ends.
	acceptance *domain.LadoConflictAcceptance
	pins       int
	// before and after are the diff's sides.
	before, after *ChangeContent
	// scope is where the conflicts are compared, and change the change as
	// the policy resolves it.
	scope  scopeRef
	change ladopolicy.SetupChange
	// needsPresence asks for the desktop app's confirmation; noop changes
	// nothing.
	needsPresence bool
	noop          bool
	warnings      []string
}

// prepare checks a change against the store and the managed setup and
// works out what it writes. It writes nothing.
func (s *Service) prepare(ctx context.Context, c Change) (*preparedChange, error) {
	switch c.Op {
	case OpCreate:
		return s.prepareCreate(ctx, c)
	case OpEdit:
		return s.prepareEdit(ctx, c)
	case OpDelete:
		return s.prepareDelete(ctx, c)
	case OpBinding:
		return s.prepareBinding(ctx, c)
	case OpAccept:
		return s.prepareAccept(ctx, c)
	case OpRevoke:
		return s.prepareRevoke(ctx, c)
	}
	return nil, fmt.Errorf("unknown setup change %q", c.Op)
}

// prepareCreate checks a new item's name and content. No live item may
// hold its tool, kind and name.
func (s *Service) prepareCreate(ctx context.Context, c Change) (*preparedChange, error) {
	if err := checkNewName(c.Tool, c.Kind, c.Name); err != nil {
		return nil, err
	}
	// A new item reaches sessions as an adopted one does, which this OS was
	// not measured for.
	if !domain.LadoSetupDeliveryVerified(s.deps.GOOS, c.Tool) {
		return nil, apierr.Conflict(CodeOSUnverified, "Creating setup items is not available on this OS yet: LADO has not checked that sessions get them here.",
			map[string]any{"os": s.deps.GOOS, "tools": []domain.LadoSetupTool{c.Tool}})
	}
	content, err := newContent(c.Tool, c.Kind, c.Name, c.Content)
	if err != nil {
		return nil, err
	}
	items, err := s.deps.Store.ListLiveLadoSetupItems(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the managed setup items: %w", err)
	}
	for _, iv := range items {
		if iv.Item.Tool == c.Tool && iv.Item.Kind == c.Kind && (iv.Item.Name == c.Name || !isKeyKind(c.Kind) && samePlace(iv.Item.Name, c.Name)) {
			return nil, itemExists(iv.Item)
		}
	}
	now := s.now()
	item := domain.LadoSetupItem{ID: domain.LadoSetupItemID(s.deps.NewID()), Tool: c.Tool, Kind: c.Kind, Name: c.Name, CreatedAt: now}
	v := content.version
	v.ID, v.ItemID, v.Version, v.CreatedAt = domain.LadoSetupItemVersionID(s.deps.NewID()), item.ID, 1, now
	return &preparedChange{
		op: OpCreate, item: item, content: &content, version: v, after: content.view,
		change:        ladopolicy.SetupChange{Item: &domain.LadoSetupItemWithVersion{Item: item, Version: v}, Content: content.value},
		needsPresence: domain.LadoSetupNeedsPresence(c.Kind, c.Name, content.blobs[0]),
	}, nil
}

// samePlace reports whether two file or folder items of a kind would share a
// place in the managed folder and in session homes: one name in any case, as
// Windows and macOS compare names, or one inside the other.
func samePlace(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// prepareEdit checks an edit of a text item against its latest version.
func (s *Service) prepareEdit(ctx context.Context, c Change) (*preparedChange, error) {
	iv, err := s.liveItem(ctx, c.ItemID)
	if err != nil {
		return nil, err
	}
	it, latest := iv.Item, iv.Version
	var tree domain.LadoSetupTree
	if latest.ContentKind == domain.LadoSetupContentTree {
		if tree, err = s.treeOfVersion(latest); err != nil {
			return nil, fmt.Errorf("read %s: %w", it.Name, err)
		}
		if !oneFileSkill(it, tree) {
			return nil, apierr.Conflict(CodeFolderItem,
				"This item is a folder. Edit its files in LADO's managed folder, then keep the change from drift.", itemDetails(it))
		}
	}
	// Before the dry run: its before is the version, not the managed copy.
	if err := s.refuseOverDrift(ctx, iv); err != nil {
		return nil, err
	}
	if c.BaseVersion != latest.Version {
		return nil, itemChanged(it, latest.Version)
	}
	content, err := editContent(it, latest, tree, c.Content)
	if err != nil {
		return nil, err
	}
	p := &preparedChange{op: OpEdit, item: it, latest: latest, after: content.view}
	p.before = s.beforeView(it, latest, &p.warnings)
	before := p.before
	if content.version.SHA256 == latest.SHA256 && content.version.Executable == latest.Executable {
		p.noop, p.after, p.version = true, before, latest
		return p, nil
	}
	v := content.version
	v.ID, v.ItemID, v.Version, v.CreatedAt = domain.LadoSetupItemVersionID(s.deps.NewID()), it.ID, latest.Version+1, s.now()
	p.content, p.version = &content, v
	p.change = ladopolicy.SetupChange{Item: &domain.LadoSetupItemWithVersion{Item: it, Version: v}, Content: content.value}
	// An edit that makes an item safe changes one that was not.
	p.needsPresence = s.versionNeedsPresence(it, latest) || domain.LadoSetupNeedsPresence(it.Kind, it.Name, content.blobs[0])
	return p, nil
}

// prepareDelete ends a live item. Its versions stay, and so do the rule
// snapshots that pinned one, which the result counts.
func (s *Service) prepareDelete(ctx context.Context, c Change) (*preparedChange, error) {
	iv, err := s.liveItem(ctx, c.ItemID)
	if err != nil {
		return nil, err
	}
	if err := s.refuseOverDrift(ctx, iv); err != nil {
		return nil, err
	}
	it, latest := iv.Item, iv.Version
	pins, err := s.deps.Store.CountLadoSetupItemPins(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	p := &preparedChange{op: OpDelete, item: it, latest: latest, pins: pins, change: ladopolicy.SetupChange{Remove: it.ID},
		needsPresence: s.versionNeedsPresence(it, latest)}
	p.before = s.beforeView(it, latest, &p.warnings)
	return p, nil
}

// refuseOverDrift refuses to edit or delete an item whose managed copy no
// longer holds its latest version: the edit would write over the change,
// and the delete would take it away. Keep or revert it first; bindings and
// acceptances write no content and go on.
func (s *Service) refuseOverDrift(ctx context.Context, iv domain.LadoSetupItemWithVersion) error {
	if len(s.editedDrift(ctx, []domain.LadoSetupItemWithVersion{iv})) == 0 {
		return nil
	}
	return apierr.Conflict(CodeDriftPending,
		"This item was changed in LADO's managed folder. Keep or revert that change first, then edit or delete the item.",
		map[string]any{"item": string(iv.Item.ID)})
}

// prepareBinding checks a binding. Any binding of an item that can run code
// or widen access needs presence: turning it off can lift a deny or a guard
// hook as turning it on can add a command (owner decision 2026-10-27).
func (s *Service) prepareBinding(ctx context.Context, c Change) (*preparedChange, error) {
	iv, err := s.liveItem(ctx, c.ItemID)
	if err != nil {
		return nil, err
	}
	b := c.Binding
	if err := checkSetupBinding(b); err != nil {
		return nil, err
	}
	scope, err := s.scopeOf(ctx, b.Scope, b.ScopeID, b.State != domain.LadoBindingInherit)
	if err != nil {
		return nil, err
	}
	from, err := s.storedBinding(ctx, iv.Item.ID, b.Scope, b.ScopeID)
	if err != nil {
		return nil, err
	}
	binding := domain.LadoSetupBinding{ItemID: iv.Item.ID, Scope: b.Scope, ScopeID: b.ScopeID, State: b.State, UpdatedAt: s.now()}
	p := &preparedChange{
		op: OpBinding, item: iv.Item, latest: iv.Version, binding: &binding, inherit: b.State == domain.LadoBindingInherit, from: from,
		scope: scope, change: ladopolicy.SetupChange{Binding: &binding},
	}
	if from == b.State {
		p.noop = true
		return p, nil
	}
	p.needsPresence = s.versionNeedsPresence(iv.Item, iv.Version)
	return p, nil
}

// prepareAccept checks an acceptance of a warning the item's latest version
// gives. It holds for that version's content. Accepting what a live
// acceptance already covers changes nothing.
func (s *Service) prepareAccept(ctx context.Context, c Change) (*preparedChange, error) {
	a := c.Acceptance
	if err := checkAcceptable(a.Code); err != nil {
		return nil, apierr.Invalid(CodeConflictNotAcceptable, "Only a warning can be accepted. Change the setup to end a blocking result.",
			map[string]any{"code": a.Code})
	}
	iv, err := s.liveItem(ctx, c.ItemID)
	if err != nil {
		return nil, err
	}
	it, latest := iv.Item, iv.Version
	if (a.Scope == domain.LadoScopeGlobal) != (a.ScopeID == "") || !slices.Contains([]domain.LadoRuleScope{domain.LadoScopeGlobal, domain.LadoScopeProject, domain.LadoScopeTask}, a.Scope) {
		return nil, apierr.Invalid(codeBindingScopeInvalid, "Accept the warning at Global, or for a named project or task.",
			map[string]any{"scope": string(a.Scope), "scopeId": a.ScopeID})
	}
	scope, err := s.scopeOf(ctx, a.Scope, a.ScopeID, true)
	if err != nil {
		return nil, err
	}
	found, err := s.itemGives(iv, a.Code, a.Detail)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, apierr.New(apierr.KindNotFound, CodeConflictNotFound, "This item's content does not give that warning. Check the conflicts again.",
			map[string]any{"item": string(it.ID), "code": a.Code, "detail": a.Detail})
	}
	acceptance := domain.LadoConflictAcceptance{
		ID: domain.LadoConflictAcceptanceID(s.deps.NewID()), Code: a.Code, ItemID: it.ID, Detail: a.Detail, ContentSHA256: latest.SHA256,
		Scope: a.Scope, ScopeID: a.ScopeID, Actor: actorAPI, CreatedAt: s.now(),
	}
	p := &preparedChange{op: OpAccept, item: it, latest: latest, acceptance: &acceptance, scope: scope,
		change: ladopolicy.SetupChange{Accept: &acceptance}}
	live, err := s.deps.Store.ListLiveLadoConflictAcceptances(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the setup conflict acceptances: %w", err)
	}
	for _, have := range live {
		if have.Code == a.Code && have.ItemID == it.ID && have.Detail == a.Detail && have.ContentSHA256 == latest.SHA256 &&
			have.Scope == a.Scope && have.ScopeID == a.ScopeID {
			p.noop, p.acceptance = true, &have
			break
		}
	}
	return p, nil
}

// prepareRevoke ends a live acceptance, whatever its item's state.
func (s *Service) prepareRevoke(ctx context.Context, c Change) (*preparedChange, error) {
	live, err := s.deps.Store.ListLiveLadoConflictAcceptances(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the setup conflict acceptances: %w", err)
	}
	i := slices.IndexFunc(live, func(a domain.LadoConflictAcceptance) bool { return a.ID == c.AcceptanceID })
	if i < 0 {
		return nil, acceptanceNotFound()
	}
	a := live[i]
	it, _, ok, err := s.deps.Store.LadoSetupItem(ctx, a.ItemID)
	if err != nil {
		return nil, fmt.Errorf("read the setup item: %w", err)
	}
	if !ok {
		return nil, acceptanceNotFound()
	}
	scope := scopeRef{}
	switch a.Scope {
	case domain.LadoScopeProject:
		scope.project = domain.ProjectID(a.ScopeID)
	case domain.LadoScopeTask:
		scope.task = domain.LadoTaskID(a.ScopeID)
		// A task LADO no longer has is compared without its project.
		if task, ok, err := s.deps.Store.LadoTask(ctx, scope.task); err != nil {
			return nil, fmt.Errorf("read the task: %w", err)
		} else if ok {
			scope.project = task.ProjectID
		}
	}
	return &preparedChange{op: OpRevoke, item: it, acceptance: &a, scope: scope, change: ladopolicy.SetupChange{Revoke: a.ID}}, nil
}

// result is the change's result before conflicts and impact.
func (p *preparedChange) result(op ChangeOp, sum string) ChangeResult {
	res := ChangeResult{
		Op: op, ChangeSHA256: sum, NeedsPresence: p.needsPresence && !p.noop, Item: p.item,
		Before: p.before, After: p.after, Pins: p.pins, Warnings: p.warnings,
		ConflictsAdded: []Conflict{}, ConflictsRemoved: []Conflict{}, Impact: noImpact(),
	}
	if p.op == OpCreate || p.op == OpEdit {
		v := p.version
		res.Version = &v
	}
	if p.binding != nil {
		b := *p.binding
		res.Binding = &b
	}
	if p.acceptance != nil {
		a := *p.acceptance
		res.Acceptance = &a
	}
	return res
}

// hideNewIDs takes out of a dry run's result the ids it made up for what
// does not exist yet: a new item, version or acceptance.
func (p *preparedChange) hideNewIDs(res *ChangeResult) {
	if res.Version != nil && res.Version.ID != p.latest.ID {
		res.Version.ID = ""
	}
	if p.op == OpAccept && res.Acceptance != nil {
		res.Acceptance.ID = ""
	}
	if p.op != OpCreate {
		return
	}
	id := res.Item.ID
	res.Item.ID, res.Version.ItemID = "", ""
	for _, list := range [][]Conflict{res.ConflictsAdded, res.ConflictsRemoved} {
		for i := range list {
			if list[i].ItemID == id {
				list[i].ItemID = ""
				delete(list[i].Details, "item")
			}
		}
	}
}

// write makes a prepared change.
func (s *Service) write(ctx context.Context, p *preparedChange, ap approval, sum string, res *ChangeResult) error {
	it := p.item
	now := s.now()
	switch p.op {
	case OpCreate:
		p.version.Actor = ap.actor
		if err := s.putBlobs(p.content.blobs); err != nil {
			return fmt.Errorf("store %s: %w", it.Name, err)
		}
		event := s.changeEvent(ap, actionItemCreated, subjectItem, string(it.ID), map[string]any{
			"tool": it.Tool, "kind": it.Kind, "name": it.Name, "version": 1, "sha256": p.version.SHA256, "change": sum,
		})
		if err := s.deps.Store.CreateLadoSetupItem(ctx, it, p.version, event, ap.storedProof()); err != nil {
			return changeFailed(err, it)
		}
		res.Version = &p.version
		res.Warnings = append(res.Warnings, s.syncManaged(ctx, domain.LadoSetupItemWithVersion{Item: it, Version: p.version})...)
	case OpEdit:
		p.version.Actor = ap.actor
		if err := s.putBlobs(p.content.blobs); err != nil {
			return fmt.Errorf("store %s: %w", it.Name, err)
		}
		event := s.changeEvent(ap, actionItemUpdated, subjectItem, string(it.ID), map[string]any{
			"version": p.version.Version, "sha256": p.version.SHA256, "previousSha256": p.latest.SHA256, "change": sum,
		})
		if err := s.deps.Store.AddLadoSetupItemVersion(ctx, it.ID, p.latest.Version, p.version, event, ap.storedProof()); err != nil {
			return changeFailed(err, it)
		}
		res.Version = &p.version
		res.Warnings = append(res.Warnings, s.syncManaged(ctx, domain.LadoSetupItemWithVersion{Item: it, Version: p.version})...)
	case OpDelete:
		event := s.changeEvent(ap, actionItemDeleted, subjectItem, string(it.ID), map[string]any{
			"tool": it.Tool, "kind": it.Kind, "name": it.Name, "version": p.latest.Version, "pins": p.pins, "change": sum,
		})
		if err := s.deps.Store.DeleteLadoSetupItem(ctx, it.ID, now, event); err != nil {
			return changeFailed(err, it)
		}
		res.Item.DeletedAt = now
		// The blobs stay: the versions and their pins read them.
		if err := s.removeManaged(it); err != nil {
			s.deps.Log.Warn("LADO could not take a deleted item out of the managed folder", "item", it.Name, "err", err)
			res.Warnings = append(res.Warnings, WarningManagedWriteFailed)
		}
		s.rescan(ctx)
	case OpBinding:
		b := *p.binding
		event := s.changeEvent(ap, actionBindingSet, subjectItem, string(it.ID), map[string]any{
			"scope": b.Scope, "scopeId": b.ScopeID, "from": p.from, "to": b.State, "change": sum,
		})
		if err := s.deps.Store.SetLadoSetupBinding(ctx, b, p.inherit, event, ap.storedProof()); err != nil {
			return changeFailed(err, it)
		}
	case OpAccept:
		a := *p.acceptance
		event := s.changeEvent(ap, actionConflictAccepted, subjectAcceptance, string(a.ID), map[string]any{
			"item": a.ItemID, "code": a.Code, "detail": a.Detail, "contentSha256": a.ContentSHA256, "scope": a.Scope, "scopeId": a.ScopeID, "change": sum,
		})
		if err := s.deps.Store.AcceptLadoConflict(ctx, a, event); err != nil {
			return changeFailed(err, it)
		}
	case OpRevoke:
		a := *p.acceptance
		event := s.changeEvent(ap, actionConflictRevoked, subjectAcceptance, string(a.ID), map[string]any{
			"item": a.ItemID, "code": a.Code, "detail": a.Detail, "scope": a.Scope, "scopeId": a.ScopeID, "change": sum,
		})
		if err := s.deps.Store.RevokeLadoConflictAcceptance(ctx, a.ID, now, event); err != nil {
			if errors.Is(err, domain.ErrLadoConflictAcceptanceNotLive) {
				return acceptanceNotFound()
			}
			return fmt.Errorf("revoke the acceptance: %w", err)
		}
		res.Acceptance.RevokedAt = now
	}
	return nil
}

// syncManaged writes a new version into the managed folder and renews the
// cached inventory. A failure is a warning: the store holds the version
// sessions get, and drift shows the managed copy.
func (s *Service) syncManaged(ctx context.Context, iv domain.LadoSetupItemWithVersion) []string {
	var warnings []string
	if err := s.writeManaged(iv); err != nil {
		s.deps.Log.Warn("LADO could not write a changed item into the managed folder", "item", iv.Item.Name, "err", err)
		warnings = append(warnings, WarningManagedWriteFailed)
	}
	s.rescan(ctx)
	return warnings
}

// putBlobs stores content before the version that names it.
func (s *Service) putBlobs(blobs [][]byte) error {
	for _, data := range blobs {
		sum, err := s.deps.Blobs.Put(data)
		if err != nil {
			return err
		}
		if sum != sha256Hex(data) {
			return fmt.Errorf("the blob is named %s, not %s", sum, sha256Hex(data))
		}
	}
	return nil
}

// changeEvent is a change's audit event. A confirmed change names its proof.
func (s *Service) changeEvent(ap approval, action, subjectKind, subjectID string, payload map[string]any) domain.LadoAuditEvent {
	if ap.proof.Nonce != "" {
		payload["proof"], payload["dev"] = ap.proof.Nonce, ap.proof.Dev
	}
	// Strings, numbers and booleans always encode.
	data, _ := json.Marshal(payload)
	return domain.LadoAuditEvent{At: s.now(), Actor: ap.actor, Action: action, SubjectKind: subjectKind, SubjectID: subjectID, Payload: data}
}

// storedProof is the proof the store records with the change, nil without
// one.
func (ap approval) storedProof() *domain.LadoPresenceProof {
	if ap.proof.Nonce == "" {
		return nil
	}
	proof := ap.proof
	return &proof
}

// changeApproval takes the desktop app's confirmation of a change.
func (s *Service) changeApproval(ctx context.Context, presence func(action, subject string) (domain.LadoPresenceProof, error), sum string) (approval, error) {
	if presence == nil {
		return approval{}, apierr.Forbidden("LADO_PRESENCE_REQUIRED",
			"Make this change in the AO desktop app. It can run commands or widen access, so the app asks you to confirm it.")
	}
	proof, err := presence(domain.LadoPresenceSetupChange, sum)
	if err != nil {
		return approval{}, err
	}
	// The store refuses a reused proof when it records the change; this
	// refuses it before any check runs.
	used, err := s.deps.Store.LadoPresenceProofUsed(ctx, proof.Nonce)
	if err != nil {
		return approval{}, err
	}
	if used {
		return approval{}, ladopresence.Replayed()
	}
	return approvalOf(proof), nil
}

// changeFailed is the API error of a store write that refused a change.
func changeFailed(err error, it domain.LadoSetupItem) error {
	switch {
	case errors.Is(err, domain.ErrLadoSetupItemExists):
		return itemExists(it)
	case errors.Is(err, domain.ErrLadoSetupItemChanged):
		return itemChanged(it, 0)
	case errors.Is(err, domain.ErrLadoSetupItemNotLive):
		return itemNotFound()
	case errors.Is(err, domain.ErrLadoPresenceReplayed):
		return ladopresence.Replayed()
	}
	return fmt.Errorf("change %s: %w", it.Name, err)
}

// applyDrift keeps or reverts an item's drift. Keep makes the managed copy
// the item's next version, as reviewed by its sha256; revert writes the
// latest version over the copy and needs no confirmation.
func (s *Service) applyDrift(ctx context.Context, c Change, sum string, presence func(action, subject string) (domain.LadoPresenceProof, error)) (ChangeResult, error) {
	iv, err := s.liveItem(ctx, c.ItemID)
	if err != nil {
		return ChangeResult{}, err
	}
	it, latest := iv.Item, iv.Version
	res := ChangeResult{
		Op: c.Op, ChangeSHA256: sum, Item: it,
		ConflictsAdded: []Conflict{}, ConflictsRemoved: []Conflict{}, Impact: noImpact(),
	}
	stored := s.beforeView(it, latest, &res.Warnings)
	if c.Op == OpDriftRevert {
		// A copy LADO cannot read has no Before; reverting replaces it.
		if mc, err := s.readManaged(ctx, it, latest); err == nil {
			res.Before = managedView(it, mc)
		}
		res.After = stored
		v := latest
		if !c.DryRun {
			if v, err = s.RevertDrift(ctx, it.ID); err != nil {
				return ChangeResult{}, err
			}
			res.Applied, res.Actor = true, actorAPI
		}
		res.Version = &v
		return res, nil
	}
	mc, err := s.readManaged(ctx, it, latest)
	if err != nil {
		return ChangeResult{}, apierr.Conflict(CodeDriftUnreadable,
			"LADO cannot read the managed copy of this item as its content. Revert it to the latest version instead.",
			map[string]any{"item": string(it.ID), "error": plainError(err)})
	}
	v := mc.version
	switch {
	case v.ContentKind != latest.ContentKind:
		return ChangeResult{}, apierr.Conflict(CodeDriftUnreadable,
			"The managed copy of this item is a folder where the item is a file, or the other way round. Revert it to the latest version instead.",
			map[string]any{"item": string(it.ID), "error": fmt.Sprintf("the managed copy is %s content, the item %s", v.ContentKind, latest.ContentKind)})
	case v.SHA256 != c.DriftSHA256:
		return ChangeResult{}, apierr.Conflict(CodeChanged, "The managed copy changed since you reviewed it. Review it again.",
			map[string]any{"item": string(it.ID), "sha256": v.SHA256})
	}
	res.Before, res.After = stored, managedView(it, mc)
	if v.SHA256 == latest.SHA256 && v.Executable == latest.Executable {
		kept := latest
		res.Version, res.Applied = &kept, !c.DryRun
		return res, nil
	}
	if err := checkKeepable(it, mc); err != nil {
		return ChangeResult{}, err
	}
	v.ID, v.ItemID, v.Version, v.CreatedAt = domain.LadoSetupItemVersionID(s.deps.NewID()), it.ID, latest.Version+1, s.now()
	change := ladopolicy.SetupChange{Item: &domain.LadoSetupItemWithVersion{Item: it, Version: v}}
	if isKeyKind(it.Kind) {
		change.Content = mc.blobs[0]
	}
	res.NeedsPresence = s.versionNeedsPresence(it, latest) || managedNeedsPresence(it, mc)
	ap := approval{actor: actorAPI}
	if !c.DryRun && res.NeedsPresence {
		// Before the conflict and impact checks, which can outlast the proof.
		if ap, err = s.changeApproval(ctx, presence, sum); err != nil {
			return ChangeResult{}, err
		}
	}
	s.preview(ctx, scopeRef{}, change, &res)
	if c.DryRun {
		v.ID = ""
		res.Version = &v
		return res, nil
	}
	kept, err := s.keepDrift(ctx, it.ID, c.DriftSHA256, ap)
	if err != nil {
		return ChangeResult{}, err
	}
	res.Version, res.Applied, res.Actor = &kept, true, ap.actor
	return res, nil
}

// conflictDiff runs the conflict checks over a scope's setup before and
// after a change and returns the results it adds and the ones it ends,
// leaving out those an acceptance covers. Results are told apart by code,
// item, path and detail, so an edit that keeps a warning neither adds nor
// ends it, and one that loses its acceptance adds it.
func (s *Service) conflictDiff(ctx context.Context, scope scopeRef, change ladopolicy.SetupChange) (added, removed []Conflict, err error) {
	before, err := s.scopeProblems(ctx, scope, nil)
	if err != nil {
		return nil, nil, err
	}
	after, err := s.scopeProblems(ctx, scope, &change)
	if err != nil {
		return nil, nil, err
	}
	key := func(c Conflict) string {
		return strings.Join([]string{c.Code, string(c.ItemID), c.Path, c.Detail}, "\x00")
	}
	had := map[string]bool{}
	for _, c := range before {
		had[key(c)] = true
	}
	has := map[string]bool{}
	added, removed = []Conflict{}, []Conflict{}
	for _, c := range after {
		has[key(c)] = true
		if !had[key(c)] {
			added = append(added, c)
		}
	}
	for _, c := range before {
		if !has[key(c)] {
			removed = append(removed, c)
		}
	}
	return added, removed, nil
}

// scopeProblems are the conflict results of the items on in a scope that no
// acceptance covers, with change applied when it is set.
func (s *Service) scopeProblems(ctx context.Context, scope scopeRef, change *ladopolicy.SetupChange) ([]Conflict, error) {
	st, err := s.readScope(ctx, scope.project, scope.task)
	if err != nil {
		return nil, err
	}
	if change != nil {
		st.apply(*change)
	}
	_, setup, err := s.onItems(st, setupTools)
	if err != nil {
		return nil, err
	}
	project, err := s.projectSettings(ctx, scope.project)
	if err != nil {
		return nil, err
	}
	ceiling, err := s.ceiling(ctx)
	if err != nil {
		return nil, err
	}
	var out []Conflict
	for _, c := range s.Conflicts(ConflictInput{
		ProjectID: scope.project, TaskID: scope.task, Setup: setup, Moved: st.moved(), Ceiling: ceiling, ProjectSettings: project,
		Acceptances: st.acceptances,
	}) {
		if !c.Accepted() {
			out = append(out, c)
		}
	}
	return out, nil
}

// impactOf is what a change reaches, nothing without a policy.
func (s *Service) impactOf(ctx context.Context, change ladopolicy.SetupChange) (ladopolicy.Impact, error) {
	s.mu.Lock()
	policy := s.policy
	s.mu.Unlock()
	if policy == nil {
		return noImpact(), nil
	}
	return policy.ImpactOfSetupChange(ctx, change)
}

// noImpact is an impact that reaches nothing.
func noImpact() ladopolicy.Impact {
	return ladopolicy.Impact{Projects: []ladopolicy.ImpactProject{}, Tasks: []ladopolicy.ImpactTask{}}
}

// itemGives reports whether an item's latest version gives a warning of code
// with detail on its own. The default mode is checked against the lowest
// ceiling, so a warning any ceiling can give can be accepted.
func (s *Service) itemGives(iv domain.LadoSetupItemWithVersion, code, detail string) (bool, error) {
	it := iv.Item
	c := SetupContent{ItemID: it.ID, Tool: it.Tool, Kind: it.Kind, Name: it.Name, SHA256: iv.Version.SHA256}
	if isKeyKind(it.Kind) {
		content, err := s.deps.Blobs.Get(iv.Version.SHA256, maxBlobBytes)
		if err != nil {
			return false, fmt.Errorf("read %s from its blob: %w", it.Name, err)
		}
		c.Content = content
	}
	for _, r := range s.Conflicts(ConflictInput{Setup: []SetupContent{c}, Ceiling: domain.LadoCeilingModes[0]}) {
		if r.Code == code && r.ItemID == it.ID && r.Detail == detail {
			return true, nil
		}
	}
	return false, nil
}

// checkSetupBinding refuses a binding no scope takes: the Global binding is
// on, off or inherit and has no scope id; a project or task binding names
// its scope and is inherit, force_on or force_off.
func checkSetupBinding(b ChangeBinding) error {
	states := []domain.LadoBindingState{domain.LadoBindingInherit, domain.LadoBindingForceOn, domain.LadoBindingForceOff}
	switch {
	case b.Scope == domain.LadoScopeGlobal && b.ScopeID == "":
		states = []domain.LadoBindingState{domain.LadoBindingOn, domain.LadoBindingOff, domain.LadoBindingInherit}
	case (b.Scope == domain.LadoScopeProject || b.Scope == domain.LadoScopeTask) && b.ScopeID != "":
	default:
		return apierr.Invalid(codeBindingScopeInvalid, "Bind the item at Global, or to a named project or task.",
			map[string]any{"scope": string(b.Scope), "scopeId": b.ScopeID})
	}
	if !slices.Contains(states, b.State) {
		return apierr.Invalid(codeBindingStateInvalid, "The Global binding is on, off or inherit; a project or task binding is inherit, force_on or force_off.",
			map[string]any{"scope": string(b.Scope), "state": string(b.State)})
	}
	return nil
}

// scopeOf checks that a project or task scope names one LADO knows, a
// project that is not archived or a task with an issue, and returns where
// its conflicts are compared. A cancelled task takes nothing that forces an
// item or accepts a warning.
func (s *Service) scopeOf(ctx context.Context, scope domain.LadoRuleScope, id string, forcing bool) (scopeRef, error) {
	notFound := apierr.New(apierr.KindNotFound, codeScopeNotFound, fmt.Sprintf("LADO does not know the %s %s.", scope, id),
		map[string]any{"scope": string(scope), "scopeId": id})
	switch scope {
	case domain.LadoScopeProject:
		if s.deps.Projects == nil {
			return scopeRef{}, notFound
		}
		projects, err := s.deps.Projects.ListProjects(ctx)
		if err != nil {
			return scopeRef{}, fmt.Errorf("list the projects: %w", err)
		}
		if !slices.ContainsFunc(projects, func(p domain.ProjectRecord) bool { return p.ID == id && p.ArchivedAt.IsZero() }) {
			return scopeRef{}, notFound
		}
		return scopeRef{project: domain.ProjectID(id)}, nil
	case domain.LadoScopeTask:
		task, ok, err := s.deps.Store.LadoTask(ctx, domain.LadoTaskID(id))
		if err != nil {
			return scopeRef{}, fmt.Errorf("read the task: %w", err)
		}
		if !ok || task.IssueRef == "" {
			return scopeRef{}, notFound
		}
		if !task.CancelledAt.IsZero() && forcing {
			return scopeRef{}, apierr.Conflict(codeTaskCancelled, "This task was cancelled.", map[string]any{"task": id})
		}
		return scopeRef{project: task.ProjectID, task: task.ID}, nil
	}
	return scopeRef{}, nil
}

// storedBinding is an item's stored binding at a scope, inherit without
// one. At Global, inherit means on.
func (s *Service) storedBinding(ctx context.Context, item domain.LadoSetupItemID, scope domain.LadoRuleScope, id string) (domain.LadoBindingState, error) {
	var bindings []domain.LadoSetupBinding
	var err error
	if scope == domain.LadoScopeGlobal {
		bindings, err = s.deps.Store.ListLadoSetupGlobalBindings(ctx)
	} else {
		bindings, err = s.deps.Store.ListLadoSetupBindings(ctx, scope, id)
	}
	if err != nil {
		return "", fmt.Errorf("list the setup bindings: %w", err)
	}
	for _, b := range bindings {
		if b.ItemID == item && b.Scope == scope && b.ScopeID == id {
			return b.State, nil
		}
	}
	return domain.LadoBindingInherit, nil
}

// plannedContent is an item version's content as a change brings it.
type plannedContent struct {
	// version has no ids, number or actor yet.
	version domain.LadoSetupItemVersion
	// blobs are stored before the version: a file's bytes, a folder's file
	// and its manifest, or a JSON value.
	blobs [][]byte
	// value is a setting's or an MCP server's canonical JSON, which the
	// conflict checks read before it is stored.
	value []byte
	view  *ChangeContent
}

// newContent checks the content of a new item. A file kind holds text, a
// skill its SKILL.md, a setting or an MCP server its JSON value.
func newContent(tool domain.LadoSetupTool, kind domain.LadoSetupKind, name string, raw json.RawMessage) (plannedContent, error) {
	switch kind {
	case domain.LadoSetupKindSetting, domain.LadoSetupKindMCPServer:
		return valueContent(tool, kind, name, raw)
	case domain.LadoSetupKindSkill:
		return skillContent(raw, domain.LadoSetupModeFile)
	}
	return textContent(raw, false)
}

// editContent checks an edit's content in the form of the latest version: a
// file's text keeps its executable bit, and a skill's lone SKILL.md its mode.
func editContent(it domain.LadoSetupItem, latest domain.LadoSetupItemVersion, tree domain.LadoSetupTree, raw json.RawMessage) (plannedContent, error) {
	switch latest.ContentKind {
	case domain.LadoSetupContentJSON:
		return valueContent(it.Tool, it.Kind, it.Name, raw)
	case domain.LadoSetupContentTree:
		return skillContent(raw, tree.Entries[0].Mode)
	}
	return textContent(raw, latest.Executable)
}

// text reads content that must be text: a JSON string of UTF-8 without NUL,
// at most maxChangeBytes long.
func text(raw json.RawMessage) (string, error) {
	var s string
	// null decodes into a string without an error, so the value must be one.
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '"' || !utf8.Valid(raw) ||
		json.Unmarshal(raw, &s) != nil || strings.ContainsRune(s, 0) {
		return "", contentInvalid(reasonNotText, nil)
	}
	if len(s) > maxChangeBytes {
		return "", contentInvalid(reasonTooLarge, map[string]any{"limit": maxChangeBytes})
	}
	return s, nil
}

// textContent is a file holding text, executable when exec is set.
func textContent(raw json.RawMessage, exec bool) (plannedContent, error) {
	s, err := text(raw)
	if err != nil {
		return plannedContent{}, err
	}
	data := []byte(s)
	v := domain.LadoSetupItemVersion{ContentKind: domain.LadoSetupContentFile, SHA256: sha256Hex(data), Size: int64(len(data)), Executable: exec}
	return plannedContent{version: v, blobs: [][]byte{data}, view: &ChangeContent{
		ContentKind: v.ContentKind, SHA256: v.SHA256, Size: v.Size, Executable: exec, Text: s,
	}}, nil
}

// skillContent is a skill of one SKILL.md, whose front matter names the
// skill and says what it does.
func skillContent(raw json.RawMessage, mode domain.LadoSetupFileMode) (plannedContent, error) {
	s, err := text(raw)
	if err != nil {
		return plannedContent{}, err
	}
	if err := checkSkillFrontMatter(s); err != nil {
		return plannedContent{}, err
	}
	data := []byte(s)
	tree := domain.LadoSetupTree{Entries: []domain.LadoSetupTreeEntry{{Path: skillFile, Mode: mode, Size: int64(len(data)), SHA256: sha256Hex(data)}}}
	manifest, err := tree.CanonicalJSON()
	if err != nil {
		return plannedContent{}, err
	}
	v := domain.LadoSetupItemVersion{ContentKind: domain.LadoSetupContentTree, SHA256: sha256Hex(manifest), Size: tree.Size()}
	return plannedContent{version: v, blobs: [][]byte{data, manifest}, view: &ChangeContent{
		ContentKind: v.ContentKind, SHA256: v.SHA256, Size: v.Size, Text: s, Files: tree.Entries,
	}}, nil
}

// checkSkillFrontMatter refuses SKILL.md text without YAML front matter that
// gives the skill a name and a description, which Claude Code reads.
func checkSkillFrontMatter(s string) error {
	front, _, ok := domain.SplitLadoFrontMatter(s)
	var fields map[string]any
	if ok && yaml.Unmarshal([]byte(front), &fields) != nil {
		ok = false
	}
	for _, field := range []string{"name", "description"} {
		value, _ := fields[field].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return contentInvalid(reasonFrontMatter, map[string]any{"field": field})
		}
	}
	return nil
}

// valueContent checks a setting's or an MCP server's JSON value.
func valueContent(tool domain.LadoSetupTool, kind domain.LadoSetupKind, name string, raw json.RawMessage) (plannedContent, error) {
	value, err := canonicalJSON(raw)
	if err != nil || !utf8.Valid(raw) {
		return plannedContent{}, contentInvalid(reasonNotJSON, nil)
	}
	if len(value) > maxChangeBytes {
		return plannedContent{}, contentInvalid(reasonTooLarge, map[string]any{"limit": maxChangeBytes})
	}
	if kind == domain.LadoSetupKindSetting {
		err = checkSettingValue(name, value)
	} else {
		err = checkServerValue(tool, name, value)
	}
	if err != nil {
		return plannedContent{}, err
	}
	v := domain.LadoSetupItemVersion{ContentKind: domain.LadoSetupContentJSON, SHA256: sha256Hex(value), Size: int64(len(value))}
	return plannedContent{version: v, blobs: [][]byte{value}, value: value, view: &ChangeContent{
		ContentKind: v.ContentKind, SHA256: v.SHA256, Size: v.Size, Text: indentJSON(value),
	}}, nil
}

// checkSettingValue refuses a login: a login key, and an env setting that
// is no object or sets a login variable. Variable names are compared in any
// case, as Windows reads them.
func checkSettingValue(name string, value []byte) error {
	if slices.Contains(domain.LadoSetupClaudeLoginKeys, name) {
		return loginKey(name)
	}
	if name != claudeEnvKey {
		return nil
	}
	if !isJSONObject(value) {
		return contentInvalid(reasonNotObject, nil)
	}
	vars, err := jsonObject(value)
	if err != nil {
		return contentInvalid(reasonNotObject, nil)
	}
	for _, v := range sortedKeys(vars) {
		if domain.LadoSetupIsClaudeLoginEnv(v) {
			return loginKey(v)
		}
	}
	return nil
}

// checkServerValue checks an MCP server: an object whose type, when set, is
// a string, that runs a command or names a URL. A Codex server must also go
// on a command line safely, as adoption requires (plan decision d).
func checkServerValue(tool domain.LadoSetupTool, name string, value []byte) error {
	if !isJSONObject(value) {
		return contentInvalid(reasonNotObject, nil)
	}
	fields, err := jsonObject(value)
	if err != nil {
		return contentInvalid(reasonNotObject, nil)
	}
	var kind string
	if raw, ok := fields["type"]; ok && (json.Unmarshal(raw, &kind) != nil || kind == "") {
		return contentInvalid(reasonServerShape, map[string]any{"field": "type"})
	}
	if jsonString(fields["command"]) == "" && jsonString(fields["url"]) == "" {
		return contentInvalid(reasonServerShape, map[string]any{"field": "command"})
	}
	if tool != domain.LadoSetupToolCodex {
		return nil
	}
	table, err := codexServerTable(name, value)
	if err != nil {
		return contentInvalid(string(domain.LadoSetupReasonUnreadable), nil)
	}
	if literalCredentials(table) {
		return contentInvalid(string(domain.LadoSetupReasonLiteralCredentials), nil)
	}
	if err := checkCodexOverride(name, value); err != nil {
		return contentInvalid(string(domain.LadoSetupReasonUnreadable), nil)
	}
	return nil
}

// checkNewName refuses an item LADO does not make and a name its kind
// cannot have: Codex gets instructions and MCP servers only, hook scripts
// are adopted, not made, and a name is where the item's file goes.
func checkNewName(tool domain.LadoSetupTool, kind domain.LadoSetupKind, name string) error {
	nameInvalid := func(reason string) error { return changeInvalid(OpCreate, "name", reason) }
	switch {
	case tool == domain.LadoSetupToolCodex && kind == domain.LadoSetupKindInstructions:
		if !slices.Contains(codexInstructions, name) {
			return nameInvalid(reasonInvalid)
		}
	case kind == domain.LadoSetupKindMCPServer:
		if !codexServerName.MatchString(name) {
			return nameInvalid(string(domain.LadoSetupReasonNameUnsupported))
		}
	case tool == domain.LadoSetupToolCodex:
		return changeInvalid(OpCreate, "kind", reasonNotForTool)
	case kind == domain.LadoSetupKindHookScript:
		return changeInvalid(OpCreate, "kind", reasonNotCreated)
	case kind == domain.LadoSetupKindSetting:
		if slices.Contains(domain.LadoSetupClaudeLoginKeys, name) {
			return loginKey(name)
		}
	case kind == domain.LadoSetupKindSkill:
		if !validItemName(name, false) {
			return nameInvalid(reasonInvalid)
		}
	default:
		// Instructions, agents, commands and output styles are Markdown
		// files; agents and commands can sit in subfolders.
		nested := kind == domain.LadoSetupKindAgent || kind == domain.LadoSetupKindCommand
		if !validItemName(name, nested) || !isMarkdown(name) {
			return nameInvalid(reasonInvalid)
		}
	}
	return nil
}

// validItemName reports whether a name is a clean relative path inside its
// item folder: segments that are not empty and do not start with a dot, so
// none is "." or "..", no backslash or NUL, and one segment unless nested.
func validItemName(name string, nested bool) bool {
	if !utf8.ValidString(name) || strings.ContainsAny(name, "\\\x00") {
		return false
	}
	segments := strings.Split(name, "/")
	if !nested && len(segments) > 1 {
		return false
	}
	for _, seg := range segments {
		if seg == "" || strings.HasPrefix(seg, ".") {
			return false
		}
	}
	return true
}

// oneFileSkill reports whether a folder item is a skill of one SKILL.md,
// which is edited as text.
func oneFileSkill(it domain.LadoSetupItem, tree domain.LadoSetupTree) bool {
	return it.Kind == domain.LadoSetupKindSkill && len(tree.Entries) == 1 && tree.Entries[0].Path == skillFile
}

// versionView shows a stored version: a file or a JSON value as text, a
// folder as its files, and a skill's lone SKILL.md as text too. Text longer
// than a change can bring is not read.
func (s *Service) versionView(it domain.LadoSetupItem, v domain.LadoSetupItemVersion) (*ChangeContent, error) {
	view := &ChangeContent{ContentKind: v.ContentKind, SHA256: v.SHA256, Size: v.Size, Executable: v.Executable}
	sum := v.SHA256
	if v.ContentKind == domain.LadoSetupContentTree {
		tree, err := s.treeOfVersion(v)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", it.Name, err)
		}
		view.Files = tree.Entries
		if !oneFileSkill(it, tree) {
			return view, nil
		}
		sum = tree.Entries[0].SHA256
	}
	if v.Size > maxChangeBytes {
		return view, nil
	}
	data, err := s.deps.Blobs.Get(sum, maxChangeBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s from its blob: %w", it.Name, err)
	}
	if v.ContentKind == domain.LadoSetupContentJSON {
		view.Text = indentJSON(data)
	} else {
		view.Text = textOf(data)
	}
	return view, nil
}

// managedView shows an item's managed copy as versionView shows a version.
func managedView(it domain.LadoSetupItem, mc managedContent) *ChangeContent {
	v := mc.version
	view := &ChangeContent{ContentKind: v.ContentKind, SHA256: v.SHA256, Size: v.Size, Executable: v.Executable}
	switch v.ContentKind {
	case domain.LadoSetupContentJSON:
		view.Text = indentJSON(mc.blobs[0])
	case domain.LadoSetupContentTree:
		// readManaged puts the manifest it built last.
		var tree domain.LadoSetupTree
		if json.Unmarshal(mc.blobs[len(mc.blobs)-1], &tree) == nil {
			view.Files = tree.Entries
			if oneFileSkill(it, tree) {
				view.Text = textOf(mc.blobs[0])
			}
		}
	default:
		view.Text = textOf(mc.blobs[0])
	}
	return view
}

// textOf is data as text, or empty when it is not UTF-8 without NUL.
func textOf(data []byte) string {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return ""
	}
	return string(data)
}

// indentJSON writes a JSON value with two-space indents for a diff.
func indentJSON(value []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, value, "", "  "); err != nil {
		return string(value)
	}
	return buf.String()
}

// versionNeedsPresence reports whether changing an item whose version is v
// needs the desktop app's confirmation (domain.LadoSetupNeedsPresence). A
// command's, skill's or agent's text is read from its blob; text LADO
// cannot read counts as running code.
func (s *Service) versionNeedsPresence(it domain.LadoSetupItem, v domain.LadoSetupItemVersion) bool {
	if !readsText(it.Kind) {
		return domain.LadoSetupNeedsPresence(it.Kind, it.Name, nil)
	}
	sum := v.SHA256
	if v.ContentKind == domain.LadoSetupContentTree {
		tree, err := s.treeOfVersion(v)
		if err != nil {
			return true
		}
		i := slices.IndexFunc(tree.Entries, func(e domain.LadoSetupTreeEntry) bool { return e.Path == skillFile })
		if i < 0 {
			// Claude Code loads no skill without its SKILL.md.
			return false
		}
		sum = tree.Entries[i].SHA256
	}
	if s.deps.Blobs == nil {
		return true
	}
	data, err := s.deps.Blobs.Get(sum, maxBlobBytes)
	if err != nil {
		return true
	}
	return domain.LadoSetupNeedsPresence(it.Kind, it.Name, data)
}

// managedNeedsPresence reports whether keeping an item's managed copy
// needs the desktop app's confirmation for what the copy holds.
func managedNeedsPresence(it domain.LadoSetupItem, mc managedContent) bool {
	if !readsText(it.Kind) || mc.version.ContentKind != domain.LadoSetupContentTree {
		return domain.LadoSetupNeedsPresence(it.Kind, it.Name, mc.blobs[0])
	}
	// readManaged puts the files in the manifest's order, the manifest last.
	var tree domain.LadoSetupTree
	if json.Unmarshal(mc.blobs[len(mc.blobs)-1], &tree) != nil {
		return true
	}
	for i, e := range tree.Entries {
		if e.Path == skillFile && i < len(mc.blobs)-1 {
			return domain.LadoSetupNeedsPresence(it.Kind, it.Name, mc.blobs[i])
		}
	}
	return false
}

// readsText reports whether domain.LadoSetupNeedsPresence reads an item
// kind's text.
func readsText(kind domain.LadoSetupKind) bool {
	return kind == domain.LadoSetupKindCommand || kind == domain.LadoSetupKindSkill || kind == domain.LadoSetupKindAgent
}

func changeInvalid(op ChangeOp, field, reason string) error {
	return apierr.Invalid(CodeChangeInvalid, "This setup change is not one LADO can make.",
		map[string]any{"op": string(op), "field": field, "reason": reason})
}

func contentInvalid(reason string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	details["reason"] = reason
	return apierr.Invalid(CodeContentInvalid, "This content does not fit the item.", details)
}

func loginKey(key string) error {
	return apierr.Invalid(CodeLoginKey, "Logins stay where Claude Code keeps them. LADO does not manage login keys or login variables.",
		map[string]any{"key": key})
}

func itemExists(it domain.LadoSetupItem) error {
	details := itemDetails(it)
	details["item"] = string(it.ID)
	return apierr.Conflict(CodeItemExists, "LADO already manages an item of that name.", details)
}

// itemChanged is CodeItemChanged; latest is the item's latest version when
// the change knows it.
func itemChanged(it domain.LadoSetupItem, latest int) error {
	details := map[string]any{"item": string(it.ID)}
	if latest > 0 {
		details["version"] = latest
	}
	return apierr.Conflict(CodeItemChanged, "The item got another version meanwhile. Review it again.", details)
}

func acceptanceNotFound() error {
	return apierr.NotFound(CodeNotFound, "LADO does not have this conflict acceptance, or it was revoked.")
}

func itemDetails(it domain.LadoSetupItem) map[string]any {
	return map[string]any{"tool": string(it.Tool), "kind": string(it.Kind), "name": it.Name}
}
