package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// LadoSetupTool is the agent tool a setup item belongs to. Its values are the
// AgentHarness names of the two tools LADO manages setup for.
type LadoSetupTool string

// Setup tools.
const (
	LadoSetupToolClaudeCode = LadoSetupTool(HarnessClaudeCode)
	LadoSetupToolCodex      = LadoSetupTool(HarnessCodex)
)

// LadoSetupKind is what a setup item is to its tool (docs/LJY-PRD/HARNESS.md
// §9.1).
type LadoSetupKind string

// Setup item kinds.
const (
	// LadoSetupKindInstructions is a user instructions file: a top-level
	// Markdown file of the Claude folder, or Codex's AGENTS.md or
	// AGENTS.override.md.
	LadoSetupKindInstructions LadoSetupKind = "instructions"
	// LadoSetupKindSetting is one top-level key of Claude's settings.json.
	LadoSetupKindSetting LadoSetupKind = "setting"
	// LadoSetupKindSkill, LadoSetupKindAgent, LadoSetupKindCommand,
	// LadoSetupKindOutputStyle and LadoSetupKindHookScript are entries of
	// the Claude folder's skills/, agents/, commands/, output-styles/ and
	// hooks/.
	LadoSetupKindSkill       LadoSetupKind = "skill"
	LadoSetupKindAgent       LadoSetupKind = "agent"
	LadoSetupKindCommand     LadoSetupKind = "command"
	LadoSetupKindOutputStyle LadoSetupKind = "output_style"
	LadoSetupKindHookScript  LadoSetupKind = "hook_script"
	// LadoSetupKindMCPServer is one user MCP server: an entry of mcpServers
	// in ~/.claude.json, or a [mcp_servers.<name>] table of Codex's
	// config.toml.
	LadoSetupKindMCPServer LadoSetupKind = "mcp_server"
)

// LadoSetupContentKind is the form of an item version's content.
type LadoSetupContentKind string

// Content kinds.
const (
	// LadoSetupContentFile is one file's bytes.
	LadoSetupContentFile LadoSetupContentKind = "file"
	// LadoSetupContentJSON is one JSON value, such as a settings key's or an
	// MCP server's.
	LadoSetupContentJSON LadoSetupContentKind = "json"
	// LadoSetupContentTree is a folder, described by its LadoSetupTree.
	LadoSetupContentTree LadoSetupContentKind = "tree"
)

// LadoSetupItemID identifies a setup item.
type LadoSetupItemID string

// LadoSetupItemVersionID identifies one version of a setup item.
type LadoSetupItemVersionID string

// LadoSetupAdoptionID identifies an adoption.
type LadoSetupAdoptionID string

// LadoConflictAcceptanceID identifies an accepted setup conflict.
type LadoConflictAcceptanceID string

// LadoSetupItem is one piece of the developer's setup that LADO manages
// (docs/LJY-PRD/HARNESS.md §9), adopted from a tool folder or created in
// LADO. Times are zero where the column is NULL.
type LadoSetupItem struct {
	ID   LadoSetupItemID
	Tool LadoSetupTool
	Kind LadoSetupKind
	// Name is the item's key within its tool and kind: the file name of
	// instructions and output styles, the settings key, the entry under
	// skills/ or hooks/, the path under agents/ or commands/, or the MCP
	// server's name. Only one live item holds a name.
	Name string
	// OriginPath is where adoption found the item, an absolute path with
	// "#<key>" after the file for a key. It is empty, as is AdoptionID, for
	// an item created in LADO.
	OriginPath string
	AdoptionID LadoSetupAdoptionID
	CreatedAt  time.Time
	// DeletedAt and RestoredAt end the item: an item that was deleted, or
	// whose adoption was restored, is neither resolved nor delivered. Its
	// versions stay readable.
	DeletedAt  time.Time
	RestoredAt time.Time
}

// Live reports whether the item is neither deleted nor restored.
func (i LadoSetupItem) Live() bool {
	return i.DeletedAt.IsZero() && i.RestoredAt.IsZero()
}

// LadoSetupItemVersion is one version of a setup item's content. Versions
// never change; an edit or a kept drift adds the next one.
type LadoSetupItemVersion struct {
	ID     LadoSetupItemVersionID
	ItemID LadoSetupItemID
	// Version counts the item's versions from 1. The highest is current.
	Version     int
	ContentKind LadoSetupContentKind
	// SHA256 is the hex sha256 of the content, which names its blob: the
	// file's bytes, the JSON value, or the tree's canonical JSON
	// (LadoSetupTree.SHA256).
	SHA256 string
	// Size is the content's size in bytes; a tree's is the sum of its files
	// (LadoSetupTree.Size).
	Size int64
	// Executable marks a file item that runs, such as a hook script. A tree
	// keeps the mode of each file in its manifest.
	Executable bool
	// Actor is who made the version, as in the audit log.
	Actor     string
	CreatedAt time.Time
}

// LadoSetupAdoptionState is how far an adoption got.
type LadoSetupAdoptionState string

// Adoption states.
const (
	// LadoSetupAdoptionCompleted means every moved original was removed.
	LadoSetupAdoptionCompleted LadoSetupAdoptionState = "completed"
	// LadoSetupAdoptionPartial means removing the originals stopped with an
	// error. The adopted items are live, and restore puts back what was
	// removed.
	LadoSetupAdoptionPartial LadoSetupAdoptionState = "partial"
)

// LadoSetupAdoption is one adoption of local setup (docs/LJY-PRD/HARNESS.md
// §9.2): what it backed up and how it went. RestoredAt is zero until it is
// restored.
type LadoSetupAdoption struct {
	ID        LadoSetupAdoptionID
	CreatedAt time.Time
	Actor     string
	// OS is the GOOS the adoption ran on.
	OS string
	// PlanSHA256 is the hash of the adoption plan the presence proof signed,
	// and ManifestSHA256 the hash of the backup's manifest.
	PlanSHA256     string
	ManifestSHA256 string
	State          LadoSetupAdoptionState
	// ErrorMessage is why a partial adoption stopped.
	ErrorMessage string
	RestoredAt   time.Time
	// RestoreReport is the restore's JSON report, nil before a restore.
	RestoreReport json.RawMessage
}

// LadoSetupBinding turns a setup item on or off for one scope, with the
// scopes and states of rule bindings (docs/LJY-PRD/HARNESS.md §4). Unlike a
// rule, an item without a Global binding is on: adopted items were active
// where they came from.
type LadoSetupBinding struct {
	ItemID    LadoSetupItemID
	Scope     LadoRuleScope
	ScopeID   string
	State     LadoBindingState
	UpdatedAt time.Time
}

// ErrLadoSetupBindingInvalid means a setup binding cannot be stored.
var ErrLadoSetupBindingInvalid = errors.New("invalid setup binding")

// Validate refuses a binding that cannot be stored. The Global binding is on
// or off and has no scope id; a Project or Task binding names its project or
// task and forces the item on or off. Inherit is never stored: asking for it
// removes the binding.
func (b LadoSetupBinding) Validate() error {
	if b.ItemID == "" {
		return fmt.Errorf("%w: no item", ErrLadoSetupBindingInvalid)
	}
	switch b.Scope {
	case LadoScopeGlobal:
		if b.ScopeID != "" {
			return fmt.Errorf("%w: the Global binding has no scope id, got %q", ErrLadoSetupBindingInvalid, b.ScopeID)
		}
		if b.State != LadoBindingOn && b.State != LadoBindingOff {
			return fmt.Errorf("%w: the Global binding is on or off, got %q", ErrLadoSetupBindingInvalid, b.State)
		}
	case LadoScopeProject, LadoScopeTask:
		if b.ScopeID == "" {
			return fmt.Errorf("%w: a %s binding names its %s", ErrLadoSetupBindingInvalid, b.Scope, b.Scope)
		}
		if b.State != LadoBindingForceOn && b.State != LadoBindingForceOff {
			return fmt.Errorf("%w: a %s binding is force_on or force_off, got %q", ErrLadoSetupBindingInvalid, b.Scope, b.State)
		}
	default:
		return fmt.Errorf("%w: unknown scope %q", ErrLadoSetupBindingInvalid, b.Scope)
	}
	return nil
}

// LadoConflictAcceptance is the developer's acceptance of a warning that the
// setup conflict checks found (docs/LJY-PRD/HARNESS.md §10). It holds for the
// item content it was given for, in its scope and the narrower ones, until it
// is revoked. RevokedAt is zero while it holds.
type LadoConflictAcceptance struct {
	ID     LadoConflictAcceptanceID
	Code   string
	ItemID LadoSetupItemID
	// Detail tells apart results of one code on one item, such as the hook
	// event; it is empty when there is only one.
	Detail string
	// ContentSHA256 is the sha256 of the item version that was accepted.
	// Other content needs an acceptance of its own.
	ContentSHA256 string
	Scope         LadoRuleScope
	ScopeID       string
	Actor         string
	CreatedAt     time.Time
	RevokedAt     time.Time
}

// LadoSetupFileMode is how a file of a tree is written.
type LadoSetupFileMode string

// File modes.
const (
	LadoSetupModeFile LadoSetupFileMode = "file"
	LadoSetupModeExec LadoSetupFileMode = "exec"
)

// LadoSetupTree is the canonical manifest of a folder item, such as a skill
// with more than one file. Its sha256 is the folder item version's, and LADO
// writes the folder from it and the blobs of its files.
type LadoSetupTree struct {
	Entries []LadoSetupTreeEntry `json:"entries"`
}

// LadoSetupTreeEntry is one file of a tree. Path is relative to the folder,
// with slashes; SHA256 names the file's blob.
type LadoSetupTreeEntry struct {
	Path   string            `json:"path"`
	Mode   LadoSetupFileMode `json:"mode"`
	Size   int64             `json:"size"`
	SHA256 string            `json:"sha256"`
}

// ErrLadoSetupTreeInvalid means a tree manifest cannot describe a folder.
var ErrLadoSetupTreeInvalid = errors.New("invalid setup tree")

// Normalize returns the tree as it is hashed and stored: its entries sorted
// by path, in a new slice. It refuses a path that is not clean, relative and
// slash-separated (empty, absolute, with a backslash, a "." or ".." segment,
// an empty segment, a NUL or bytes that are not UTF-8), a path listed twice
// or used both as a file and as a folder, a mode other than file or exec, a
// negative size and a sha256 that is not 64 lowercase hex digits.
func (t LadoSetupTree) Normalize() (LadoSetupTree, error) {
	// Sort a copy: the caller's slice stays as it was.
	entries := append([]LadoSetupTreeEntry{}, t.Entries...)
	slices.SortFunc(entries, func(a, b LadoSetupTreeEntry) int { return strings.Compare(a.Path, b.Path) })
	files := make(map[string]bool, len(entries))
	for _, e := range entries {
		if err := e.validate(); err != nil {
			return LadoSetupTree{}, err
		}
		if files[e.Path] {
			return LadoSetupTree{}, fmt.Errorf("%w: %q is listed twice", ErrLadoSetupTreeInvalid, e.Path)
		}
		files[e.Path] = true
	}
	for _, e := range entries {
		for dir := path.Dir(e.Path); dir != "."; dir = path.Dir(dir) {
			if files[dir] {
				return LadoSetupTree{}, fmt.Errorf("%w: %q is a file and the folder of %q", ErrLadoSetupTreeInvalid, dir, e.Path)
			}
		}
	}
	return LadoSetupTree{Entries: entries}, nil
}

func (e LadoSetupTreeEntry) validate() error {
	switch {
	case !ladoSetupValidPath(e.Path):
		return fmt.Errorf("%w: %q is not a clean relative path with slashes", ErrLadoSetupTreeInvalid, e.Path)
	case e.Mode != LadoSetupModeFile && e.Mode != LadoSetupModeExec:
		return fmt.Errorf("%w: %q has mode %q, want file or exec", ErrLadoSetupTreeInvalid, e.Path, e.Mode)
	case e.Size < 0:
		return fmt.Errorf("%w: %q has size %d", ErrLadoSetupTreeInvalid, e.Path, e.Size)
	case !ladoSetupValidSum(e.SHA256):
		return fmt.Errorf("%w: %q has sha256 %q, want 64 lowercase hex digits", ErrLadoSetupTreeInvalid, e.Path, e.SHA256)
	}
	return nil
}

// ladoSetupValidPath reports whether p names a file inside the folder:
// clean, relative, slash-separated and valid UTF-8. path.Clean leaves ".."
// only at the start of a relative path.
func ladoSetupValidPath(p string) bool {
	return p != "" && p != "." && p != ".." && path.Clean(p) == p && !path.IsAbs(p) &&
		!strings.HasPrefix(p, "../") && !strings.ContainsRune(p, '\\') && !strings.ContainsRune(p, 0) &&
		utf8.ValidString(p)
}

// ladoSetupValidSum reports whether s is a hex sha256 as LADO writes them.
func ladoSetupValidSum(s string) bool {
	return len(s) == sha256.Size*2 && strings.Trim(s, "0123456789abcdef") == ""
}

// CanonicalJSON is the tree's hashed form: the normalized entries, fields in
// declaration order, with HTML characters left as they are. Changing it
// changes the hash of every folder item version.
func (t LadoSetupTree) CanonicalJSON() ([]byte, error) {
	normalized, err := t.Normalize()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Normalize leaves UTF-8 strings and integers, which cannot fail to
	// encode.
	_ = enc.Encode(normalized)
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// SHA256 returns the hex sha256 of the canonical JSON.
func (t LadoSetupTree) SHA256() (string, error) {
	data, err := t.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Size is the sum of the tree's file sizes, the size of a folder item
// version.
func (t LadoSetupTree) Size() int64 {
	var size int64
	for _, e := range t.Entries {
		size += e.Size
	}
	return size
}

// LadoSetupLocation is where the inventory found an entry. The first three
// are also the roots of an adoption's backup.
type LadoSetupLocation string

// Inventory locations.
const (
	// LadoSetupLocationClaudeDir is the Claude folder, $CLAUDE_CONFIG_DIR or
	// ~/.claude.
	LadoSetupLocationClaudeDir LadoSetupLocation = "claude_dir"
	// LadoSetupLocationClaudeJSON is Claude's global config, ~/.claude.json.
	LadoSetupLocationClaudeJSON LadoSetupLocation = "claude_json"
	// LadoSetupLocationCodexHome is the Codex folder, $CODEX_HOME or
	// ~/.codex.
	LadoSetupLocationCodexHome LadoSetupLocation = "codex_home"
	// LadoSetupLocationProject is the main checkout of a registered project.
	LadoSetupLocationProject LadoSetupLocation = "project"
	// LadoSetupLocationManaged is LADO's managed folder, <state>/lado/harness,
	// where adopted and created items live.
	LadoSetupLocationManaged LadoSetupLocation = "managed"
)

// LadoSetupStatus is what LADO can do with an inventory entry.
type LadoSetupStatus string

// Inventory statuses.
const (
	// LadoSetupStatusManaged is a live setup item.
	LadoSetupStatusManaged LadoSetupStatus = "managed"
	// LadoSetupStatusAdoptable can be adopted.
	LadoSetupStatusAdoptable LadoSetupStatus = "adoptable"
	// LadoSetupStatusNotAdoptable is setup the tool loads that LADO cannot
	// take over, in this form or at all.
	LadoSetupStatusNotAdoptable LadoSetupStatus = "not_adoptable"
	// LadoSetupStatusNever is never adopted, whatever its form: credentials,
	// login keys, app state, plugins and organization-managed settings.
	LadoSetupStatusNever LadoSetupStatus = "never"
	// LadoSetupStatusNotManaged is left to the tool or to AO: the tool's own
	// state, values AO sets for each session, files AO's worktrees never
	// load, and anything LADO does not know.
	LadoSetupStatusNotManaged LadoSetupStatus = "not_managed"
	// LadoSetupStatusRepoTracked is a file the project's repository tracks.
	// The tool loads it from the worktree, and LADO shows it read-only.
	LadoSetupStatusRepoTracked LadoSetupStatus = "repo_tracked"
)

// LadoSetupReason says why an inventory entry is not adoptable. The codes are
// stable strings, sent to the renderer, which shows them in the developer's
// language.
type LadoSetupReason string

// Inventory reasons.
const (
	// LadoSetupReasonCredentials is a login: Claude's .credentials.json, or
	// Codex's auth.json or secrets/.
	LadoSetupReasonCredentials LadoSetupReason = "credentials"
	// LadoSetupReasonLoginKey is a settings key that picks a login or a key
	// (LadoSetupClaudeLoginKeys).
	LadoSetupReasonLoginKey LadoSetupReason = "login_key"
	// LadoSetupReasonAppState is what the tool writes as it runs: history,
	// sessions, caches and the rest of ~/.claude.json.
	LadoSetupReasonAppState LadoSetupReason = "app_state"
	// LadoSetupReasonPluginsStay is Claude's plugins/ folder, which stays in
	// place. Adopting the enabledPlugins and extraKnownMarketplaces settings
	// manages which plugins are on.
	LadoSetupReasonPluginsStay LadoSetupReason = "plugins_stay"
	// LadoSetupReasonToolState is configuration the tool writes itself, such
	// as Codex's projects table or its rules/.
	LadoSetupReasonToolState LadoSetupReason = "tool_state"
	// LadoSetupReasonNoTUIPath is a kind no launch option delivers to the
	// tool's terminal sessions, such as Codex skills. LADO adopts only what
	// reaches both the terminal and the Chat sessions (HARNESS §9.4).
	LadoSetupReasonNoTUIPath LadoSetupReason = "no_tui_path"
	// LadoSetupReasonDeprecated is a kind its tool deprecated, such as Codex
	// prompts.
	LadoSetupReasonDeprecated LadoSetupReason = "deprecated"
	// LadoSetupReasonInlineTable is a Codex MCP server that is not written as
	// a [mcp_servers.<name>] table, so adoption cannot cut it out of
	// config.toml.
	LadoSetupReasonInlineTable LadoSetupReason = "inline_table"
	// LadoSetupReasonLiteralCredentials is a Codex MCP server with secret
	// values (env values, http_headers, bearer_token), which its delivery
	// would put on a command line.
	LadoSetupReasonLiteralCredentials LadoSetupReason = "literal_credentials" //nolint:gosec // G101: a reason code, not a credential.
	// LadoSetupReasonNameUnsupported is a Codex MCP server whose name has
	// characters other than A-Z, a-z, 0-9, "_" and "-".
	LadoSetupReasonNameUnsupported LadoSetupReason = "name_unsupported"
	// LadoSetupReasonUnknownLocation is anything else in a tool folder.
	LadoSetupReasonUnknownLocation LadoSetupReason = "unknown_location"
	// LadoSetupReasonWorktreesDoNotLoad is an untracked project file, such as
	// .claude/settings.local.json, that AO's worktrees never contain.
	LadoSetupReasonWorktreesDoNotLoad LadoSetupReason = "worktrees_do_not_load"
	// LadoSetupReasonAOSetsPerSession is a value AO sets for each session,
	// such as Codex's model.
	LadoSetupReasonAOSetsPerSession LadoSetupReason = "ao_sets_per_session"
	// LadoSetupReasonRepoTracked is a file the project's repository tracks.
	LadoSetupReasonRepoTracked LadoSetupReason = "repo_tracked"
	// LadoSetupReasonManagedSettings is settings an organization manages.
	LadoSetupReasonManagedSettings LadoSetupReason = "managed_settings"
	// LadoSetupReasonSymlink is a symbolic link, or a folder or file that
	// holds one. LADO never adopts through a link: removing or rewriting it
	// would change the link or what it points to.
	LadoSetupReasonSymlink LadoSetupReason = "symlink"
	// LadoSetupReasonUnreadable is something LADO could not read as setup: a
	// file it may not open, a settings file that does not parse, or content
	// a version cannot hold, such as a special file, a file name that is not
	// UTF-8 or a TOML value JSON cannot hold.
	LadoSetupReasonUnreadable LadoSetupReason = "unreadable"
)

// LadoSetupAction is what an adoption does with an entry.
type LadoSetupAction string

// Adoption actions.
const (
	// LadoSetupActionMove backs the entry up, copies it into LADO's managed
	// home and removes it from its original location.
	LadoSetupActionMove LadoSetupAction = "move"
	// LadoSetupActionKeep leaves the entry where it is. The adoption preview
	// lists it, and nothing happens to it.
	LadoSetupActionKeep LadoSetupAction = "keep"
)

// LadoSetupEntry is one line of the setup inventory (docs/LJY-PRD/HARNESS.md
// §9.1): something found in a tool folder or a project, and what LADO can do
// with it.
type LadoSetupEntry struct {
	Tool LadoSetupTool
	// Kind and Name are the item the entry is or would be adopted as. Kind is
	// empty for an entry that is no setup item, such as credentials; Name is
	// then its file name, its path under the tool folder or its config key.
	Kind     LadoSetupKind
	Name     string
	Location LadoSetupLocation
	// Path is where the entry is: an absolute path, with "#<key>" after the
	// file for a key.
	Path string
	// Size and SHA256 describe the content of an adoptable, managed or
	// repo-tracked entry; a folder's are its tree's (LadoSetupTree).
	Size   int64
	SHA256 string
	// Tracked marks a file the project's repository tracks.
	Tracked bool
	Status  LadoSetupStatus
	// Reason says why the entry is not adoptable; it is empty for managed and
	// adoptable entries.
	Reason LadoSetupReason
	// DefaultAction is what the adoption preview proposes for an adoptable
	// entry (LadoSetupDefaultAction).
	DefaultAction LadoSetupAction
	// KeptInPlace names what stays at the entry's location when it is
	// adopted: the login variables of Claude's env setting, which are not
	// part of its content.
	KeptInPlace []string
	// ItemID is the live item of a managed entry.
	ItemID LadoSetupItemID
	// ProjectID is the project of an entry found in a project.
	ProjectID ProjectID
}

// LadoSetupDriftKind says how setup changed outside LADO.
type LadoSetupDriftKind string

// Drift kinds (docs/LJY-PRD/HARNESS.md §9.6).
const (
	// LadoSetupDriftEdited means a managed file or folder no longer hashes to
	// its item's latest version.
	LadoSetupDriftEdited LadoSetupDriftKind = "edited"
	// LadoSetupDriftNewLocal means an original location holds an adoptable
	// entry that the adoption baseline does not have. It is offered for
	// adoption and never moved by itself.
	LadoSetupDriftNewLocal LadoSetupDriftKind = "new_local"
)

// LadoSetupDrift is one change made outside LADO. Drift never changes what a
// session gets, since homes are built from stored versions.
type LadoSetupDrift struct {
	// ItemID is the managed item that drifted; for a new local entry, the
	// live item with its tool, kind and name, if there is one.
	ItemID   LadoSetupItemID
	Kind     LadoSetupDriftKind
	Tool     LadoSetupTool
	ItemKind LadoSetupKind
	Name     string
	// Path is what changed: the file or folder in the managed home for an
	// edit, the entry at its original location for a new local one. SHA256
	// is its content now.
	Path   string
	SHA256 string
}

// LadoSetupDefaultAction is what the adoption preview proposes for an
// adoptable entry: move, except that settings keys stay in place unless they
// are Claude's hooks, permissions, env, model, modelSettings, statusLine,
// outputStyle, enabledPlugins, extraKnownMarketplaces or includeCoAuthoredBy.
func LadoSetupDefaultAction(tool LadoSetupTool, kind LadoSetupKind, name string) LadoSetupAction {
	if kind == LadoSetupKindSetting && (tool != LadoSetupToolClaudeCode || !slices.Contains(ladoSetupMovedSettings, name)) {
		return LadoSetupActionKeep
	}
	return LadoSetupActionMove
}

// ladoSetupMovedSettings are the Claude settings keys an adoption moves by
// default.
var ladoSetupMovedSettings = []string{
	"hooks", "permissions", "env", "model", "modelSettings", "statusLine", "outputStyle",
	"enabledPlugins", "extraKnownMarketplaces", "includeCoAuthoredBy",
}

// LadoSetupNeedsPresence reports whether creating, editing, turning on or
// keeping the drift of an item needs the desktop app's confirmation
// (setup.change.requires_presence). Hook scripts, MCP servers and the
// settings keys hooks, permissions, env, statusLine, enabledPlugins and
// extraKnownMarketplaces can run code or widen access.
func LadoSetupNeedsPresence(kind LadoSetupKind, name string) bool {
	switch kind {
	case LadoSetupKindHookScript, LadoSetupKindMCPServer:
		return true
	case LadoSetupKindSetting:
		return slices.Contains(ladoSetupPresenceSettings, name)
	default:
		return false
	}
}

// ladoSetupPresenceSettings are the Claude settings keys whose changes need
// presence.
var ladoSetupPresenceSettings = []string{"hooks", "permissions", "env", "statusLine", "enabledPlugins", "extraKnownMarketplaces"}

// LadoSetupDeliveryVerified reports whether sessions on goos were measured to
// get the tool's adopted setup (docs/LJY-PRD/HARNESS.md §9.4; the probe in
// service/ladosetup/probe). Only Linux is measured, for both tools. Adoption
// is refused on every other OS until it is measured there; restore runs
// anywhere.
func LadoSetupDeliveryVerified(goos string, tool LadoSetupTool) bool {
	return goos == "linux" && (tool == LadoSetupToolClaudeCode || tool == LadoSetupToolCodex)
}

// LadoSetupClaudeLoginKeys are the top-level Claude settings keys that pick a
// login or a key. They are never adopted and stay in the original
// settings.json. The list is the one AO's account copier drops
// (adapters/agentaccount/claude.go), which domain does not import.
var LadoSetupClaudeLoginKeys = []string{"apiKeyHelper", "awsAuthRefresh", "awsCredentialExport", "forceLoginMethod", "forceLoginOrgUUID"}

// LadoSetupClaudeLoginEnv are the variables that make Claude Code use another
// login than its folder's. An adopted env setting leaves them in the original
// settings.json. The list is the account copier's as well.
var LadoSetupClaudeLoginEnv = []string{
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL",
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
}

// LadoSetupItemWithVersion is a setup item with one of its versions: the
// latest one for a live item, or the one a rule snapshot pinned.
type LadoSetupItemWithVersion struct {
	Item    LadoSetupItem
	Version LadoSetupItemVersion
}

// ErrLadoSetupItemExists means a live item already holds the tool, kind and
// name.
var ErrLadoSetupItemExists = errors.New("a live setup item has that name")

// ErrLadoSetupItemChanged means the item got a newer version than the one an
// edit started from.
var ErrLadoSetupItemChanged = errors.New("the setup item has a newer version")

// ErrLadoSetupItemNotLive means the item does not exist, or was deleted or
// restored.
var ErrLadoSetupItemNotLive = errors.New("the setup item is not live")

// ErrLadoSetupAlreadyRestored means the adoption was restored before.
var ErrLadoSetupAlreadyRestored = errors.New("the adoption was already restored")

// ErrLadoConflictAcceptanceNotLive means the acceptance does not exist or was
// revoked before.
var ErrLadoConflictAcceptanceNotLive = errors.New("the conflict acceptance is not live")
