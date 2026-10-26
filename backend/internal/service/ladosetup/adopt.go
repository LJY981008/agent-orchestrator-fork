package ladosetup

// Adoption (plan §7, docs/LJY-PRD/HARNESS.md §9.2), the only LADO code that
// removes the developer's files. A preview says what moves, where to, how
// sessions get it, which commands are rewritten and what holds the adoption
// back, and hashes the plan; the desktop app's confirmation signs that hash.
// Applying runs in a fixed order, one function a stage, each fed only the
// previous stage's success: check the plan and the preconditions again, back
// up and read the backup back, copy into blobs and the managed folder and
// read those back, remove the originals, record. Nothing is removed before
// the backup is verified, and only adoptable entries move, so credentials and
// app state are never touched.

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
)

// Error codes of adoption, sent to the renderer.
const (
	// CodeAdoptionInvalid is a request that names no entry, an entry twice,
	// an unknown tool, kind or action, or no plan hash when it applies.
	CodeAdoptionInvalid = "LADO_SETUP_ADOPTION_INVALID"
	// CodeNotAdoptable is an entry that is not adoptable; details say why.
	CodeNotAdoptable = "LADO_SETUP_NOT_ADOPTABLE"
	// CodeChanged is a plan hash that is not the current one: the setup
	// changed after the preview.
	CodeChanged = "LADO_SETUP_CHANGED"
	// CodeOSUnverified is an OS where delivery of a tool's setup was not
	// measured (domain.LadoSetupDeliveryVerified).
	CodeOSUnverified = "LADO_SETUP_OS_UNVERIFIED"
	// CodeToolRunning is a live AO session or another process of a tool
	// whose setup would move.
	CodeToolRunning = "LADO_SETUP_TOOL_RUNNING"
	// CodeAdoptionFailed is an adoption that stopped; details name the
	// stage and the original.
	CodeAdoptionFailed = "LADO_SETUP_ADOPTION_FAILED"
	// CodeUnavailable is a service without its store, blobs or state dir.
	CodeUnavailable = "LADO_SETUP_UNAVAILABLE"
)

// Warning codes of an adoption.
const (
	// WarningDirectUse means Claude Code and Codex used outside AO stop
	// loading the moved items.
	WarningDirectUse = "LADO_SETUP_DIRECT_USE_STOPS"
	// WarningBaselineFailed means the drift baseline could not be saved.
	WarningBaselineFailed = "LADO_SETUP_BASELINE_FAILED"
)

// Reasons of CodeNotAdoptable besides the inventory's.
const (
	reasonNotFound       = "not_found"
	reasonAlreadyManaged = "already_managed"
)

// How sessions get an adopted item (plan decisions b and d).
const (
	// DeliveryHome is Claude Code's per-session config home.
	DeliveryHome = "home"
	// DeliveryOverlay is Codex's launch options on the real CODEX_HOME.
	DeliveryOverlay = "overlay"
)

// Verification checks of an adopted item.
const (
	// CheckManagedCopy compares the managed copy with the recorded version.
	CheckManagedCopy = "managed_copy"
	// CheckCodexOverride parses a Codex server's launch option back.
	CheckCodexOverride = "codex_override"
)

const adoptionPlanFormat = "lado-setup-adoption/1"

// Audit of an adoption.
const (
	actorHuman       = "human"
	actorDevPresence = "human:dev-presence"
	actionAdopted    = "setup.adopted"
	subjectAdoption  = "setup_adoption"
)

// Adoption stages, in order. A failure names its stage.
const (
	stageBackup         = "backup"
	stageBackupVerified = "backup_verified"
	stageCopy           = "copy"
	stageCopyVerified   = "copy_verified"
	stageRemove         = "remove"
	stageRecord         = "record"
)

// AdoptEntry names an inventory entry and what to do with it.
type AdoptEntry struct {
	Tool   domain.LadoSetupTool
	Kind   domain.LadoSetupKind
	Name   string
	Action domain.LadoSetupAction
}

// AdoptRequest previews an adoption, or runs the one a preview hashed.
type AdoptRequest struct {
	// Entries are the adoptable entries to move or keep. A kept entry is
	// listed in the preview, and nothing happens to it.
	Entries []AdoptEntry
	// PlanSHA256 is the preview's hash; applying refuses another one.
	PlanSHA256 string
	// DryRun previews and writes nothing; it needs no presence.
	DryRun bool
	// Presence returns the human-presence proof of the request for action
	// and subject. It is nil when the request carried none.
	Presence func(action, subject string) (domain.LadoPresenceProof, error)
}

// AdoptionPreview is what an adoption would do (plan §7.1).
type AdoptionPreview struct {
	// PlanSHA256 is the hash the desktop app's confirmation signs.
	PlanSHA256 string
	OS         string
	// Entries are the requested entries in tool, kind and name order.
	Entries []PreviewEntry
	// Rewrites are the hooks and statusLine commands the moved settings get.
	Rewrites []HookRewrite
	// Conflicts are the conflict check results about the moved entries, and
	// every reference to a moved path that cannot be rewritten, which blocks
	// the adoption.
	Conflicts     []Conflict
	Preconditions Preconditions
	Warnings      []string
}

// PreviewEntry is one entry of a preview.
type PreviewEntry struct {
	Tool   domain.LadoSetupTool
	Kind   domain.LadoSetupKind
	Name   string
	Action domain.LadoSetupAction
	// OriginPath, SHA256 and Size are the inventory's.
	OriginPath string
	SHA256     string
	Size       int64
	// ManagedPath and Delivery say where a moved entry goes and how sessions
	// get it; both are empty for a kept one.
	ManagedPath string
	Delivery    string
	// KeptInPlace are the login variables a moved env setting leaves.
	KeptInPlace []string
}

// Preconditions are what holds an adoption back (plan decision h), for the
// tools whose setup would move.
type Preconditions struct {
	OS string
	// Unverified are the tools whose delivery was not measured on OS.
	Unverified []domain.LadoSetupTool
	// Sessions and Processes run those tools now.
	Sessions  []LiveSession
	Processes []ToolProcess
	// ProcessesIgnored means the daemon leaves the processes out.
	ProcessesIgnored bool
}

// OK reports whether nothing holds the adoption back.
func (p Preconditions) OK() bool {
	return len(p.Unverified) == 0 && len(p.Sessions) == 0 && len(p.Processes) == 0
}

// AdoptResult is a preview or an adoption.
type AdoptResult struct {
	Preview AdoptionPreview
	// Applied is set when the adoption was recorded.
	Applied    bool
	AdoptionID domain.LadoSetupAdoptionID
	State      domain.LadoSetupAdoptionState
	// Items are the recorded items with their first versions.
	Items []AdoptedItem
	// Verification is the structural check of what was recorded.
	Verification []VerifyCheck
	Warnings     []string
}

// AdoptedItem is an item an adoption recorded.
type AdoptedItem struct {
	ItemID      domain.LadoSetupItemID
	VersionID   domain.LadoSetupItemVersionID
	Tool        domain.LadoSetupTool
	Kind        domain.LadoSetupKind
	Name        string
	ManagedPath string
	SHA256      string
}

// VerifyCheck is one check of an adopted item.
type VerifyCheck struct {
	ItemID domain.LadoSetupItemID
	Check  string
	OK     bool
	Error  string
}

// planEntry is a requested entry with what the preview found.
type planEntry struct {
	entry  domain.LadoSetupEntry
	action domain.LadoSetupAction
	// content is a key item's canonical JSON as it will be stored, after
	// any rewrite; nil for files and folders.
	content  []byte
	rewrites []HookRewrite
}

// adoptionPlan is a checked request and its preview.
type adoptionPlan struct {
	preview AdoptionPreview
	entries []planEntry
	// moved are the Claude folder entries that move, as slash paths under
	// it.
	moved []string
	inv   Inventory
}

// moves are the entries that move, in plan order.
func (p adoptionPlan) moves() []planEntry {
	var out []planEntry
	for _, pe := range p.entries {
		if pe.action == domain.LadoSetupActionMove {
			out = append(out, pe)
		}
	}
	return out
}

// tools are the tools whose setup moves, in order.
func (p adoptionPlan) tools() []domain.LadoSetupTool {
	var tools []domain.LadoSetupTool
	for _, pe := range p.moves() {
		if !slices.Contains(tools, pe.entry.Tool) {
			tools = append(tools, pe.entry.Tool)
		}
	}
	slices.Sort(tools)
	return tools
}

// approval is a confirmed adoption: the presence proof and who gave it.
type approval struct {
	proof domain.LadoPresenceProof
	actor string
}

// stageError is an adoption that stopped at a stage, at one original when
// path is set.
type stageError struct {
	stage string
	root  domain.LadoSetupLocation
	// path is under root, with slashes: never the absolute path, which
	// names the developer's home.
	path string
	err  error
}

func (e *stageError) Error() string {
	if e.path == "" {
		return fmt.Sprintf("%s: %v", e.stage, e.err)
	}
	return fmt.Sprintf("%s %s: %v", e.stage, e.path, e.err)
}

func (e *stageError) Unwrap() error { return e.err }

// Adopt previews an adoption, or runs it when req.DryRun is false. A partial
// adoption, which stopped while removing originals, returns its result with
// an error.
func (s *Service) Adopt(ctx context.Context, req AdoptRequest) (AdoptResult, error) {
	if req.DryRun {
		plan, err := s.plan(ctx, req.Entries)
		if err != nil {
			return AdoptResult{}, err
		}
		return AdoptResult{Preview: plan.preview}, nil
	}
	if err := s.lockWrite(ctx); err != nil {
		return AdoptResult{}, err
	}
	defer s.unlockWrite()
	plan, err := s.plan(ctx, req.Entries)
	if err != nil {
		return AdoptResult{}, err
	}
	ap, err := s.confirm(req, plan)
	if err != nil {
		return AdoptResult{}, err
	}
	id := domain.LadoSetupAdoptionID(s.deps.NewID())
	backup, err := s.backup(ctx, id, plan)
	if err != nil {
		return AdoptResult{}, s.adoptionFailed(id, err, false)
	}
	copied, err := s.copyManaged(ctx, backup)
	if err != nil {
		return AdoptResult{}, s.adoptionFailed(id, err, false)
	}
	// From the first removal on, the adoption runs to its record: a caller
	// that goes away must not leave originals removed and nothing recorded.
	ctx = context.WithoutCancel(ctx)
	removed := s.removeOriginals(ctx, copied)
	rec, err := s.record(ctx, removed, ap)
	if err != nil {
		return AdoptResult{}, s.adoptionFailed(id, err, false)
	}
	res := s.finish(ctx, rec)
	if removed.failure != nil {
		return res, s.adoptionFailed(id, removed.failure, true)
	}
	return res, nil
}

// plan checks the request against a new scan and builds its preview. It
// writes nothing.
func (s *Service) plan(ctx context.Context, req []AdoptEntry) (adoptionPlan, error) {
	if err := checkAdoptEntries(req); err != nil {
		return adoptionPlan{}, err
	}
	if s.deps.Store == nil || s.deps.Blobs == nil || s.deps.StateDir == "" {
		return adoptionPlan{}, apierr.Unavailable(CodeUnavailable, "LADO's local setup is not available in this daemon.")
	}
	inv, err := s.Scan(ctx)
	if err != nil {
		return adoptionPlan{}, err
	}
	plan := adoptionPlan{inv: inv}
	for _, r := range req {
		e, err := findAdoptable(inv, r)
		if err != nil {
			return adoptionPlan{}, err
		}
		plan.entries = append(plan.entries, planEntry{entry: e, action: r.Action})
	}
	slices.SortFunc(plan.entries, func(a, b planEntry) int {
		return cmp.Or(cmp.Compare(a.entry.Tool, b.entry.Tool), cmp.Compare(a.entry.Kind, b.entry.Kind), cmp.Compare(a.entry.Name, b.entry.Name))
	})
	for _, pe := range plan.moves() {
		if e := pe.entry; e.Location == domain.LadoSetupLocationClaudeDir && !isKeyKind(e.Kind) {
			plan.moved = append(plan.moved, slashRel(s.deps.ClaudeDir, e.Path))
		}
	}
	files := keyFileReader{}
	for i := range plan.entries {
		if err := s.planContent(&plan.entries[i], plan.moved, files); err != nil {
			return adoptionPlan{}, err
		}
	}
	conflicts, err := s.adoptionConflicts(ctx, plan, files)
	if err != nil {
		return adoptionPlan{}, err
	}
	pre, err := s.preconditions(ctx, plan.tools())
	if err != nil {
		return adoptionPlan{}, err
	}
	plan.preview = AdoptionPreview{
		PlanSHA256:    planSHA256(s.deps.GOOS, plan.entries),
		OS:            s.deps.GOOS,
		Conflicts:     conflicts,
		Preconditions: pre,
	}
	for _, pe := range plan.entries {
		e := pe.entry
		p := PreviewEntry{Tool: e.Tool, Kind: e.Kind, Name: e.Name, Action: pe.action, OriginPath: e.Path, SHA256: e.SHA256, Size: e.Size}
		if pe.action == domain.LadoSetupActionMove {
			p.ManagedPath = managedPath(s.deps.StateDir, e.Tool, e.Kind, e.Name)
			p.Delivery = DeliveryHome
			if e.Tool == domain.LadoSetupToolCodex {
				p.Delivery = DeliveryOverlay
			}
			p.KeptInPlace = slices.Clone(e.KeptInPlace)
			plan.preview.Rewrites = append(plan.preview.Rewrites, pe.rewrites...)
		}
		plan.preview.Entries = append(plan.preview.Entries, p)
	}
	if len(plan.moves()) > 0 {
		plan.preview.Warnings = append(plan.preview.Warnings, WarningDirectUse)
	}
	return plan, nil
}

// setupKinds are the kinds an adoption names.
var setupKinds = []domain.LadoSetupKind{
	domain.LadoSetupKindInstructions, domain.LadoSetupKindSetting, domain.LadoSetupKindSkill, domain.LadoSetupKindAgent,
	domain.LadoSetupKindCommand, domain.LadoSetupKindOutputStyle, domain.LadoSetupKindHookScript, domain.LadoSetupKindMCPServer,
}

// checkAdoptEntries refuses a request that cannot name a plan.
func checkAdoptEntries(req []AdoptEntry) error {
	if len(req) == 0 {
		return apierr.Invalid(CodeAdoptionInvalid, "Choose the entries to adopt.", nil)
	}
	seen := map[AdoptEntry]bool{}
	for _, r := range req {
		details := map[string]any{"tool": string(r.Tool), "kind": string(r.Kind), "name": r.Name}
		switch {
		case !slices.Contains(setupTools, r.Tool) || !slices.Contains(setupKinds, r.Kind) || r.Name == "":
			return apierr.Invalid(CodeAdoptionInvalid, "An entry names no setup item LADO knows.", details)
		case r.Action != domain.LadoSetupActionMove && r.Action != domain.LadoSetupActionKeep:
			return apierr.Invalid(CodeAdoptionInvalid, "An entry's action is move or keep.", details)
		}
		key := AdoptEntry{Tool: r.Tool, Kind: r.Kind, Name: r.Name}
		if seen[key] {
			return apierr.Invalid(CodeAdoptionInvalid, "An entry is named twice.", details)
		}
		seen[key] = true
	}
	return nil
}

// isOriginal reports whether a location is one adoption moves entries out
// of.
func isOriginal(loc domain.LadoSetupLocation) bool {
	return loc == domain.LadoSetupLocationClaudeDir || loc == domain.LadoSetupLocationClaudeJSON || loc == domain.LadoSetupLocationCodexHome
}

// isKeyKind reports whether items of a kind are keys of a file.
func isKeyKind(kind domain.LadoSetupKind) bool {
	return kind == domain.LadoSetupKindSetting || kind == domain.LadoSetupKindMCPServer
}

// findAdoptable finds the requested entry at an original location. It must
// be adoptable, and no live item may hold its name.
func findAdoptable(inv Inventory, r AdoptEntry) (domain.LadoSetupEntry, error) {
	refuse := func(reason string) error {
		return apierr.Invalid(CodeNotAdoptable, "LADO cannot adopt this entry.", map[string]any{
			"tool": string(r.Tool), "kind": string(r.Kind), "name": r.Name, "reason": reason,
		})
	}
	for _, e := range inv.Entries {
		if e.Location == domain.LadoSetupLocationManaged && e.Tool == r.Tool && e.Kind == r.Kind && e.Name == r.Name {
			return domain.LadoSetupEntry{}, refuse(reasonAlreadyManaged)
		}
	}
	for _, e := range inv.Entries {
		if !isOriginal(e.Location) || e.Tool != r.Tool || e.Kind != r.Kind || e.Name != r.Name {
			continue
		}
		if e.Status != domain.LadoSetupStatusAdoptable {
			return domain.LadoSetupEntry{}, refuse(string(e.Reason))
		}
		return e, nil
	}
	return domain.LadoSetupEntry{}, refuse(reasonNotFound)
}

// planContent reads a key entry's content from its file and rewrites a
// moved hooks or statusLine value. Content that no longer hashes to the
// scan's changed in between.
func (s *Service) planContent(pe *planEntry, moved []string, files keyFileReader) error {
	e := pe.entry
	if !isKeyKind(e.Kind) {
		return nil
	}
	// The scan read the file a moment ago: failing to read it now, or
	// reading other content, means it changed in between.
	data, err := files.read(s.originalKeyFile(e.Tool, e.Kind))
	if err != nil {
		return setupChanged("")
	}
	value, err := keyValue(e.Tool, e.Kind, e.Name, data)
	if err != nil || sha256Hex(value) != e.SHA256 {
		return setupChanged("")
	}
	pe.content = value
	if pe.action == domain.LadoSetupActionMove && e.Tool == domain.LadoSetupToolClaudeCode && e.Kind == domain.LadoSetupKindSetting {
		pe.content, pe.rewrites, err = rewriteHookPaths(e.Name, value, s.deps.ClaudeDir, moved)
	}
	return err
}

// keyFileReader reads each key file once.
type keyFileReader map[string]keyFileRead

type keyFileRead struct {
	data []byte
	err  error
}

func (r keyFileReader) read(path string) ([]byte, error) {
	if got, ok := r[path]; ok {
		return got.data, got.err
	}
	data, err := readRegularNoLink(path)
	r[path] = keyFileRead{data: data, err: err}
	return data, err
}

// adoptionConflicts runs the conflict checks over the setup as it would be
// after the adoption: the moved entries with their rewritten values, the
// settings and servers that stay where they are, and the managed items. It
// keeps the results about the moved entries and every reference to a moved
// path that cannot be rewritten.
func (s *Service) adoptionConflicts(ctx context.Context, plan adoptionPlan, files keyFileReader) ([]Conflict, error) {
	var setup []SetupContent
	moving := map[string]bool{}
	for _, pe := range plan.moves() {
		e := pe.entry
		moving[e.Path] = true
		sum := e.SHA256
		if pe.content != nil {
			sum = sha256Hex(pe.content)
		}
		setup = append(setup, SetupContent{Tool: e.Tool, Kind: e.Kind, Name: e.Name, Path: e.Path, SHA256: sum, Content: pe.content})
	}
	for _, e := range plan.inv.Entries {
		if moving[e.Path] || !isOriginal(e.Location) || e.Status != domain.LadoSetupStatusAdoptable || !isKeyKind(e.Kind) {
			continue
		}
		data, err := files.read(s.originalKeyFile(e.Tool, e.Kind))
		if err != nil {
			continue
		}
		if value, err := keyValue(e.Tool, e.Kind, e.Name, data); err == nil {
			setup = append(setup, SetupContent{Tool: e.Tool, Kind: e.Kind, Name: e.Name, Path: e.Path, SHA256: e.SHA256, Content: value})
		}
	}
	if len(plan.moved) > 0 {
		managed, err := s.deps.Store.ListLiveLadoSetupItems(ctx)
		if err != nil {
			return nil, fmt.Errorf("list the managed setup items: %w", err)
		}
		for _, iv := range managed {
			c := SetupContent{ItemID: iv.Item.ID, Tool: iv.Item.Tool, Kind: iv.Item.Kind, Name: iv.Item.Name, SHA256: iv.Version.SHA256}
			if isKeyKind(iv.Item.Kind) {
				if content, err := s.deps.Blobs.Get(iv.Version.SHA256, maxBlobBytes); err == nil {
					c.Content = content
				}
			}
			setup = append(setup, c)
		}
	}
	var out []Conflict
	for _, c := range s.Conflicts(ConflictInput{Setup: setup, Moved: plan.moved}) {
		if moving[c.Path] || c.Code == ConflictHookPathMoved {
			out = append(out, c)
		}
	}
	return out, nil
}

// preconditions checks the OS and what runs the tools whose setup moves.
func (s *Service) preconditions(ctx context.Context, tools []domain.LadoSetupTool) (Preconditions, error) {
	p := Preconditions{OS: s.deps.GOOS}
	if len(tools) == 0 {
		return p, nil
	}
	for _, tool := range tools {
		if !domain.LadoSetupDeliveryVerified(s.deps.GOOS, tool) {
			p.Unverified = append(p.Unverified, tool)
		}
	}
	running, err := s.RunningTools(ctx)
	if err != nil {
		return Preconditions{}, err
	}
	for _, ss := range running.Sessions {
		if slices.Contains(tools, ss.Tool) {
			p.Sessions = append(p.Sessions, ss)
		}
	}
	for _, proc := range running.Processes {
		if slices.Contains(tools, proc.Tool) {
			p.Processes = append(p.Processes, proc)
		}
	}
	p.ProcessesIgnored = running.ProcessesIgnored
	return p, nil
}

// planSHA256 hashes a plan's canonical JSON (plan §7.1): the format, the OS
// and every entry with its origin, content hash and action, in tool, kind
// and name order.
func planSHA256(goos string, entries []planEntry) string {
	list := make([]map[string]any, 0, len(entries))
	for _, pe := range entries {
		list = append(list, map[string]any{
			"tool": pe.entry.Tool, "kind": pe.entry.Kind, "name": pe.entry.Name,
			"originPath": pe.entry.Path, "sha256": pe.entry.SHA256, "action": pe.action,
		})
	}
	// Strings, and maps and lists of them, always encode.
	data, _ := marshalCanonical(map[string]any{"format": adoptionPlanFormat, "os": goos, "entries": list})
	return sha256Hex(data)
}

// confirm checks that the request applies the current plan and that nothing
// holds it back, then takes the desktop app's confirmation.
func (s *Service) confirm(req AdoptRequest, plan adoptionPlan) (approval, error) {
	p := plan.preview
	switch {
	case req.PlanSHA256 == "":
		return approval{}, apierr.Invalid(CodeAdoptionInvalid, "Preview the adoption first, then confirm it.", nil)
	case req.PlanSHA256 != p.PlanSHA256:
		return approval{}, setupChanged(p.PlanSHA256)
	case len(plan.moves()) == 0:
		return approval{}, apierr.Invalid(CodeAdoptionInvalid, "Choose at least one entry to move.", nil)
	}
	for _, c := range p.Conflicts {
		if c.Code == ConflictHookPathMoved {
			details := map[string]any{}
			for k, v := range c.Details {
				details[k] = v
			}
			return approval{}, apierr.Conflict(ConflictHookPathMoved,
				"A command names a file that would move, and LADO cannot rewrite it. Move the setting that holds it too, or keep the file in place.", details)
		}
	}
	pre := p.Preconditions
	if len(pre.Unverified) > 0 {
		return approval{}, apierr.Conflict(CodeOSUnverified, "Adoption is not available on this OS yet: LADO has not checked that sessions get the adopted setup here.",
			map[string]any{"os": pre.OS, "tools": pre.Unverified})
	}
	if len(pre.Sessions) > 0 || len(pre.Processes) > 0 {
		return approval{}, apierr.Conflict(CodeToolRunning, "Close the Claude Code and Codex sessions and programs listed, then adopt again.", runningDetails(pre))
	}
	if req.Presence == nil {
		return approval{}, apierr.Forbidden("LADO_PRESENCE_REQUIRED",
			"Adopt your local setup in the AO desktop app. It asks you to confirm, then sends the adoption itself.")
	}
	proof, err := req.Presence(domain.LadoPresenceSetupAdopt, p.PlanSHA256)
	if err != nil {
		return approval{}, err
	}
	ap := approval{proof: proof, actor: actorHuman}
	if proof.Dev {
		ap.actor = actorDevPresence
	}
	return ap, nil
}

// runningDetails lists the sessions by id and the processes by PID and
// program name, never by command line.
func runningDetails(p Preconditions) map[string]any {
	sessions := make([]map[string]any, 0, len(p.Sessions))
	for _, ss := range p.Sessions {
		sessions = append(sessions, map[string]any{"id": string(ss.ID), "tool": string(ss.Tool)})
	}
	processes := make([]map[string]any, 0, len(p.Processes))
	for _, proc := range p.Processes {
		processes = append(processes, map[string]any{"pid": proc.PID, "name": programName(proc.Command), "tool": string(proc.Tool)})
	}
	return map[string]any{"sessions": sessions, "processes": processes}
}

// programName is the base name of a command line's program.
func programName(command string) string {
	program, _, _ := strings.Cut(strings.TrimSpace(command), " ")
	return program[strings.LastIndexAny(program, `/\`)+1:]
}

func setupChanged(current string) error {
	var details map[string]any
	if current != "" {
		details = map[string]any{"planSha256": current}
	}
	return apierr.Conflict(CodeChanged, "Your local setup changed since the preview. Review the new preview and confirm again.", details)
}

// managedCopy is the adopted content, stored as blobs and written to the
// managed folder.
type managedCopy struct {
	backup verifiedBackup
	items  []adoptedItem
}

// adoptedItem is an item an adoption copied, with its first version.
type adoptedItem struct {
	entry    backupEntry
	iv       domain.LadoSetupItemWithVersion
	rewrites []HookRewrite
}

// copyManaged stores every backed-up original as blobs, writes the managed
// folder from them and reads it back. On failure the managed folder loses
// what this adoption wrote.
func (s *Service) copyManaged(ctx context.Context, b verifiedBackup) (managedCopy, error) {
	s.stage(stageCopy)
	c := managedCopy{backup: b}
	now := s.now()
	moves := b.plan.moves()
	for i, be := range b.manifest.Entries {
		item := domain.LadoSetupItem{
			ID: domain.LadoSetupItemID(s.deps.NewID()), Tool: be.Tool, Kind: be.Kind, Name: be.Name,
			OriginPath: moves[i].entry.Path, CreatedAt: now,
		}
		v, rewrites, err := s.storeContent(b.dir, be, b.plan.moved)
		if err != nil {
			return managedCopy{}, &stageError{stage: stageCopy, root: be.Root, path: be.RelPath, err: err}
		}
		v.ID, v.ItemID, v.Version, v.CreatedAt = domain.LadoSetupItemVersionID(s.deps.NewID()), item.ID, 1, now
		c.items = append(c.items, adoptedItem{entry: be, iv: domain.LadoSetupItemWithVersion{Item: item, Version: v}, rewrites: rewrites})
	}
	for i, it := range c.items {
		if err := s.writeManaged(it.iv); err != nil {
			s.discardManaged(c.items[:i+1])
			return managedCopy{}, &stageError{stage: stageCopy, root: it.entry.Root, path: it.entry.RelPath, err: err}
		}
	}
	for _, it := range c.items {
		if err := s.checkManaged(ctx, it.iv); err != nil {
			s.discardManaged(c.items)
			return managedCopy{}, &stageError{stage: stageCopy, root: it.entry.Root, path: it.entry.RelPath, err: err}
		}
	}
	s.stage(stageCopyVerified)
	return c, nil
}

// storeContent stores the backed-up content of an entry as blobs and returns
// its version without ids: a file's bytes, a folder's files and tree, or a
// key's canonical JSON with its hook paths rewritten.
func (s *Service) storeContent(dir string, be backupEntry, moved []string) (domain.LadoSetupItemVersion, []HookRewrite, error) {
	put := func(data []byte, want string) error {
		sum, err := s.deps.Blobs.Put(data)
		if err == nil && sum != want {
			err = fmt.Errorf("the blob is named %s, not %s", sum, want)
		}
		return err
	}
	switch be.Type {
	case backupTypeFile:
		data, err := readRegularNoLink(be.copyPath(dir))
		if err != nil {
			return domain.LadoSetupItemVersion{}, nil, err
		}
		if err := put(data, be.SHA256); err != nil {
			return domain.LadoSetupItemVersion{}, nil, err
		}
		mode, err := parseMode(be.Mode)
		if err != nil {
			return domain.LadoSetupItemVersion{}, nil, err
		}
		return domain.LadoSetupItemVersion{ContentKind: domain.LadoSetupContentFile, SHA256: be.SHA256, Size: int64(len(data)), Executable: mode&0o111 != 0}, nil, nil
	case backupTypeDir:
		var tree domain.LadoSetupTree
		for _, f := range be.Files {
			if f.Dir {
				continue
			}
			data, err := readRegularNoLink(filepath.Join(be.copyPath(dir), filepath.FromSlash(f.Path)))
			if err != nil {
				return domain.LadoSetupItemVersion{}, nil, err
			}
			if err := put(data, f.SHA256); err != nil {
				return domain.LadoSetupItemVersion{}, nil, err
			}
			mode, err := parseMode(f.Mode)
			if err != nil {
				return domain.LadoSetupItemVersion{}, nil, err
			}
			tree.Entries = append(tree.Entries, domain.LadoSetupTreeEntry{Path: f.Path, Mode: treeMode(mode), Size: f.Size, SHA256: f.SHA256})
		}
		manifest, err := tree.CanonicalJSON()
		if err != nil {
			return domain.LadoSetupItemVersion{}, nil, err
		}
		if err := put(manifest, be.SHA256); err != nil {
			return domain.LadoSetupItemVersion{}, nil, err
		}
		return domain.LadoSetupItemVersion{ContentKind: domain.LadoSetupContentTree, SHA256: be.SHA256, Size: tree.Size()}, nil, nil
	case backupTypeKeys:
		file, err := readRegularNoLink(be.copyPath(dir))
		if err != nil {
			return domain.LadoSetupItemVersion{}, nil, err
		}
		value, err := keyValue(be.Tool, be.Kind, be.Name, file)
		if err != nil {
			return domain.LadoSetupItemVersion{}, nil, err
		}
		var rewrites []HookRewrite
		if be.Tool == domain.LadoSetupToolClaudeCode && be.Kind == domain.LadoSetupKindSetting {
			if value, rewrites, err = rewriteHookPaths(be.Name, value, s.deps.ClaudeDir, moved); err != nil {
				return domain.LadoSetupItemVersion{}, nil, err
			}
		}
		sum := sha256Hex(value)
		if err := put(value, sum); err != nil {
			return domain.LadoSetupItemVersion{}, nil, err
		}
		return domain.LadoSetupItemVersion{ContentKind: domain.LadoSetupContentJSON, SHA256: sum, Size: int64(len(value))}, rewrites, nil
	}
	return domain.LadoSetupItemVersion{}, nil, fmt.Errorf("unknown backup type %q", be.Type)
}

// discardManaged takes items this adoption wrote out of the managed folder
// again. A failure is logged: the managed folder is LADO's, and drift shows
// what is left.
func (s *Service) discardManaged(items []adoptedItem) {
	for _, it := range items {
		if err := s.removeManaged(it.iv.Item); err != nil {
			s.deps.Log.Warn("LADO could not take an unrecorded item out of the managed folder", "item", it.iv.Item.Name, "err", err)
		}
	}
}

// removal is how far removing the originals got.
type removal struct {
	copied managedCopy
	// removed are the items whose originals are gone, in order.
	removed []adoptedItem
	// written are the key files LADO rewrote, with the sha256 of the bytes
	// it wrote last.
	written map[string]string
	// failure stopped the removal; nil when every original is gone.
	failure *stageError
}

// removeOriginals removes the originals one item at a time, in plan order,
// and stops at the first failure. Each original must still hash to its
// backup, so nothing the backup does not hold is removed.
func (s *Service) removeOriginals(ctx context.Context, c managedCopy) removal {
	s.stage(stageRemove)
	r := removal{copied: c, written: map[string]string{}}
	for _, it := range c.items {
		if err := s.removeOriginal(ctx, it.entry, r.written); err != nil {
			r.failure = &stageError{stage: stageRemove, root: it.entry.Root, path: it.entry.RelPath, err: err}
			return r
		}
		r.removed = append(r.removed, it)
	}
	return r
}

var errChangedSinceBackup = errors.New("it changed after the backup")

// removeOriginal removes one original: a file, a folder (renamed aside
// first, so a failure leaves it whole) or keys of a file, which is
// rewritten in place with its mode. The folders that hold items stay
// (plan decision f).
func (s *Service) removeOriginal(ctx context.Context, be backupEntry, written map[string]string) error {
	switch be.Type {
	case backupTypeKeys:
		data, err := readRegularNoLink(be.Path)
		if err != nil {
			return err
		}
		want := be.SHA256
		if sum, ok := written[be.Path]; ok {
			want = sum
		}
		if sha256Hex(data) != want {
			return errChangedSinceBackup
		}
		out, err := withoutAdoptedKey(be, data)
		if err != nil {
			return err
		}
		info, err := os.Lstat(be.Path)
		if err != nil {
			return err
		}
		if err := atomicWrite(be.Path, out, info.Mode().Perm(), s.ops.rename); err != nil {
			return err
		}
		written[be.Path] = sha256Hex(out)
		return nil
	case backupTypeFile:
		if _, sum, err := fileSum(be.Path); err != nil || sum != be.SHA256 {
			return cmp.Or(err, errChangedSinceBackup)
		}
		return s.ops.removeAll(be.Path)
	case backupTypeDir:
		tree, err := treeOf(ctx, be.Path)
		if err != nil {
			return err
		}
		if sum, err := tree.SHA256(); err != nil || sum != be.SHA256 {
			return cmp.Or(err, errChangedSinceBackup)
		}
		aside := filepath.Join(filepath.Dir(be.Path), "."+filepath.Base(be.Path)+".lado-removed-"+s.deps.NewID())
		if err := s.ops.rename(be.Path, aside); err != nil {
			return err
		}
		if err := s.ops.removeAll(aside); err != nil {
			// The original is gone from its place; what is left aside is
			// junk the inventory lists as an unknown entry.
			s.deps.Log.Warn("LADO could not delete a moved folder it set aside", "path", aside, "err", err)
		}
		return nil
	}
	return fmt.Errorf("unknown backup type %q", be.Type)
}

// withoutAdoptedKey takes an entry's key out of the file's bytes. The env
// setting keeps its login variables, in their order; without any it goes.
func withoutAdoptedKey(be backupEntry, data []byte) ([]byte, error) {
	switch {
	case be.Tool == domain.LadoSetupToolCodex:
		rest, _, err := tomlWithoutServer(data, be.Name)
		return rest, err
	case be.Kind == domain.LadoSetupKindMCPServer:
		return jsonWithoutMCPServer(data, be.Name)
	case be.Name == claudeEnvKey:
		value, _, err := jsonMemberValue(data, claudeEnvKey)
		if err != nil {
			return nil, err
		}
		vars, err := jsonMembers(value)
		if err != nil {
			// Not an object: adopted as it was.
			return jsonWithout(data, claudeEnvKey)
		}
		vars = slices.DeleteFunc(vars, func(m jsonMember) bool { return !slices.Contains(domain.LadoSetupClaudeLoginEnv, m.Key) })
		if len(vars) == 0 {
			return jsonWithout(data, claudeEnvKey)
		}
		kept, err := formatJSONObject(vars, false)
		if err != nil {
			return nil, err
		}
		return jsonWith(data, claudeEnvKey, kept)
	default:
		return jsonWithout(data, be.Name)
	}
}

// recorded is an adoption the store holds.
type recorded struct {
	removal  removal
	adoption domain.LadoSetupAdoption
	approval approval
}

// record stores the adoption with the items whose originals are gone, their
// first versions, the presence proof and the audit event in one
// transaction. A removal that stopped records a partial adoption; one that
// removed nothing records nothing. When nothing is recorded, the removed
// originals are put back and the managed folder loses this adoption's
// items.
func (s *Service) record(ctx context.Context, r removal, ap approval) (recorded, error) {
	s.stage(stageRecord)
	c := r.copied
	if len(r.removed) == 0 {
		s.discardManaged(c.items)
		return recorded{}, r.failure
	}
	adoption := domain.LadoSetupAdoption{
		ID:             c.backup.manifest.AdoptionID,
		CreatedAt:      s.now(),
		Actor:          ap.actor,
		OS:             s.deps.GOOS,
		PlanSHA256:     c.backup.plan.preview.PlanSHA256,
		ManifestSHA256: c.backup.sha256,
		State:          domain.LadoSetupAdoptionCompleted,
	}
	if r.failure != nil {
		adoption.State = domain.LadoSetupAdoptionPartial
		adoption.ErrorMessage = fmt.Sprintf("Adoption stopped while removing %s: %s. Restore puts back what was moved.", r.failure.path, plainError(r.failure.err))
	}
	var (
		items    []domain.LadoSetupItem
		versions []domain.LadoSetupItemVersion
		rewrites int
	)
	tools := map[string]int{}
	for _, it := range r.removed {
		v := it.iv.Version
		v.Actor = ap.actor
		items = append(items, it.iv.Item)
		versions = append(versions, v)
		rewrites += len(it.rewrites)
		tools[string(it.iv.Item.Tool)]++
	}
	pre := c.backup.plan.preview.Preconditions
	payload, _ := json.Marshal(map[string]any{
		"state":            adoption.State,
		"items":            len(items),
		"tools":            tools,
		"rewrites":         rewrites,
		"processesIgnored": pre.ProcessesIgnored,
		"planSha256":       adoption.PlanSHA256,
		"proof":            ap.proof.Nonce,
		"dev":              ap.proof.Dev,
	})
	event := domain.LadoAuditEvent{At: adoption.CreatedAt, Actor: ap.actor, Action: actionAdopted, SubjectKind: subjectAdoption, SubjectID: string(adoption.ID), Payload: payload}
	proof := ap.proof
	if err := s.deps.Store.RecordLadoSetupAdoption(ctx, adoption, items, versions, event, &proof); err != nil {
		s.putBack(r)
		s.discardManaged(c.items)
		if errors.Is(err, domain.ErrLadoPresenceReplayed) {
			return recorded{}, ladopresence.Replayed()
		}
		return recorded{}, &stageError{stage: stageRecord, err: err}
	}
	if r.failure != nil {
		s.discardManaged(c.items[len(r.removed):])
	}
	return recorded{removal: r, adoption: adoption, approval: ap}, nil
}

// putBack undoes a removal the store did not record: removed files and
// folders come back from the backup, and a key file LADO rewrote gets its
// backed-up bytes again unless it changed since. What cannot be put back is
// logged; the backup keeps it.
func (s *Service) putBack(r removal) {
	dir := r.copied.backup.dir
	restored := map[string]bool{}
	for i := len(r.removed) - 1; i >= 0; i-- {
		be := r.removed[i].entry
		var err error
		switch be.Type {
		case backupTypeKeys:
			if restored[be.Path] {
				continue
			}
			restored[be.Path] = true
			err = putBackKeyFile(dir, be, r.written[be.Path])
		default:
			err = restoreCopy(dir, be)
		}
		if err != nil {
			s.deps.Log.Error("LADO could not put back an original it removed; the backup keeps it",
				"backup", dir, "path", be.RelPath, "err", err)
		}
	}
}

func putBackKeyFile(dir string, be backupEntry, written string) error {
	data, err := readRegularNoLink(be.Path)
	if err != nil {
		return err
	}
	if sha256Hex(data) != written {
		return errors.New("it changed after LADO rewrote it")
	}
	original, err := readRegularNoLink(be.copyPath(dir))
	if err != nil {
		return err
	}
	mode, err := parseMode(be.Mode)
	if err != nil {
		return err
	}
	if err := atomicWrite(be.Path, original, mode, os.Rename); err != nil {
		return err
	}
	return os.Chtimes(be.Path, be.MTime, be.MTime)
}

// finish saves the drift baseline and checks what was recorded.
func (s *Service) finish(ctx context.Context, rec recorded) AdoptResult {
	res := AdoptResult{
		Preview:    rec.removal.copied.backup.plan.preview,
		Applied:    true,
		AdoptionID: rec.adoption.ID,
		State:      rec.adoption.State,
		Warnings:   slices.Clone(rec.removal.copied.backup.plan.preview.Warnings),
	}
	for _, it := range rec.removal.removed {
		iv := it.iv
		res.Items = append(res.Items, AdoptedItem{
			ItemID: iv.Item.ID, VersionID: iv.Version.ID, Tool: iv.Item.Tool, Kind: iv.Item.Kind, Name: iv.Item.Name,
			ManagedPath: managedPath(s.deps.StateDir, iv.Item.Tool, iv.Item.Kind, iv.Item.Name), SHA256: iv.Version.SHA256,
		})
	}
	if err := s.saveBaseline(ctx); err != nil {
		s.deps.Log.Warn("LADO could not save the setup drift baseline", "err", err)
		res.Warnings = append(res.Warnings, WarningBaselineFailed)
	}
	res.Verification = s.verifyAdopted(ctx, rec.removal.removed)
	return res
}

// verifyAdopted checks every recorded item structurally (plan §7.7): its
// managed copy holds the recorded version, and a Codex server's launch
// option parses back to its content. S09 adds a probe home for Claude.
func (s *Service) verifyAdopted(ctx context.Context, items []adoptedItem) []VerifyCheck {
	var checks []VerifyCheck
	for _, it := range items {
		iv := it.iv
		check := VerifyCheck{ItemID: iv.Item.ID, Check: CheckManagedCopy, OK: true}
		if err := s.checkManaged(ctx, iv); err != nil {
			check.OK, check.Error = false, err.Error()
		}
		checks = append(checks, check)
		if iv.Item.Tool != domain.LadoSetupToolCodex || iv.Item.Kind != domain.LadoSetupKindMCPServer {
			continue
		}
		check = VerifyCheck{ItemID: iv.Item.ID, Check: CheckCodexOverride, OK: true}
		content, err := s.deps.Blobs.Get(iv.Version.SHA256, maxBlobBytes)
		if err == nil {
			err = checkCodexOverride(iv.Item.Name, content)
		}
		if err != nil {
			check.OK, check.Error = false, err.Error()
		}
		checks = append(checks, check)
	}
	return checks
}

// adoptionFailed is the API error of an adoption that stopped: its stage and
// the original, by its path under its root. recorded tells a partial
// adoption, which restore can undo, from one that changed nothing.
func (s *Service) adoptionFailed(id domain.LadoSetupAdoptionID, err error, recorded bool) error {
	var se *stageError
	if !errors.As(err, &se) {
		return err
	}
	s.deps.Log.Error("LADO setup adoption stopped", "adoption", id, "stage", se.stage, "path", se.path, "recorded", recorded, "err", se.err)
	details := map[string]any{"stage": se.stage, "adoption": string(id)}
	if se.path != "" {
		details["root"], details["path"] = string(se.root), se.path
	}
	message := "The adoption stopped before anything was removed. Your files are where they were."
	switch {
	case recorded:
		details["state"] = string(domain.LadoSetupAdoptionPartial)
		message = "The adoption stopped while removing originals. What was moved is managed now, and Restore puts it back."
	case se.stage == stageRecord:
		message = "LADO could not record the adoption, so it put back what it had removed."
	}
	return apierr.New(apierr.KindInternal, CodeAdoptionFailed, message, details)
}

// plainError is an error's text without the absolute path a file system
// error carries.
func plainError(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Op + ": " + pathErr.Err.Error()
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return linkErr.Op + ": " + linkErr.Err.Error()
	}
	return err.Error()
}

// The drift baseline (plan §7.6): what stays at the original locations after
// an adoption. An adoptable entry missing from it, or with another hash, is
// new local setup.
const baselineFormat = "lado-setup-baseline/1"

type setupBaseline struct {
	Format  string          `json:"format"`
	SavedAt time.Time       `json:"savedAt"`
	Entries []baselineEntry `json:"entries"`
}

type baselineEntry struct {
	Tool   domain.LadoSetupTool `json:"tool"`
	Kind   domain.LadoSetupKind `json:"kind"`
	Name   string               `json:"name"`
	Path   string               `json:"path"`
	SHA256 string               `json:"sha256"`
}

// baselinePath is where the drift baseline lives.
func baselinePath(stateDir string) string {
	return filepath.Join(stateDir, "lado", "setup-baseline.json")
}

// saveBaseline scans again, which also renews the cached inventory, and
// saves the adoptable entries at the original locations.
func (s *Service) saveBaseline(ctx context.Context) error {
	inv, err := s.Scan(ctx)
	if err != nil {
		return err
	}
	b := setupBaseline{Format: baselineFormat, SavedAt: inv.ScannedAt, Entries: []baselineEntry{}}
	for _, e := range inv.Entries {
		if isOriginal(e.Location) && e.Status == domain.LadoSetupStatusAdoptable {
			b.Entries = append(b.Entries, baselineEntry{Tool: e.Tool, Kind: e.Kind, Name: e.Name, Path: e.Path, SHA256: e.SHA256})
		}
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	path := baselinePath(s.deps.StateDir)
	if err := os.MkdirAll(filepath.Dir(path), privateDir); err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), privateFile, os.Rename)
}

// stage tells a test which adoption stage starts.
func (s *Service) stage(name string) {
	if s.onStage != nil {
		s.onStage(name)
	}
}

func (s *Service) now() time.Time { return s.deps.Clock().UTC() }

func (s *Service) lockWrite(ctx context.Context) error {
	select {
	case s.writing <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for another setup change: %w", ctx.Err())
	}
}

func (s *Service) unlockWrite() { <-s.writing }
