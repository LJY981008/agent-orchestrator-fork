package controllers

// LADO fork: DTOs for the local setup LADO manages (docs/LJY-PRD/HARNESS.md
// §9 and §10, step 7 plan §13).

import (
	"encoding/json"
	"time"
)

// LadoSetupItemIDParam names a managed setup item.
type LadoSetupItemIDParam struct {
	ItemID string `path:"itemId" description:"LADO setup item id."`
}

// LadoSetupAdoptionIDParam names an adoption of local setup.
type LadoSetupAdoptionIDParam struct {
	AdoptionID string `path:"adoptionId" description:"LADO setup adoption id."`
}

// LadoSetupAcceptanceIDParam names a conflict acceptance.
type LadoSetupAcceptanceIDParam struct {
	AcceptanceID string `path:"acceptanceId" description:"LADO setup conflict acceptance id."`
}

// LadoSetupScopeQuery names the scope of a setup view.
type LadoSetupScopeQuery struct {
	ProjectID string `query:"projectId,omitempty" description:"Project to resolve; without projectId and taskId the view is Global."`
	TaskID    string `query:"taskId,omitempty" description:"Task to resolve, whose project then applies. With projectId too, the task must be in that project."`
}

// LadoSetupDryRunQuery asks a change without a body to show itself only.
type LadoSetupDryRunQuery struct {
	DryRun bool `query:"dryRun,omitempty" description:"Show the change and what it reaches without making it."`
}

// LadoSetupEntryResponse is one line of the setup inventory: something found
// in a tool folder, in a project, or in LADO's managed folder.
type LadoSetupEntryResponse struct {
	Tool string `json:"tool" enum:"claude-code,codex"`
	Kind string `json:"kind" description:"The item kind the entry is or would be adopted as: instructions, setting, skill, agent, command, output_style, hook_script or mcp_server. Empty for an entry that is no setup item, such as credentials."`
	Name string `json:"name" description:"The item's name in its tool and kind; for an entry that is no item, its file name, its path in the tool folder or its config key."`
	// Location is where the scan found the entry.
	Location string `json:"location" enum:"claude_dir,claude_json,codex_home,project,managed"`
	Path     string `json:"path" description:"Absolute path, with #<key> after the file for a key. Entries of one name can share it, so the app keys its list by path."`
	Size     int64  `json:"size" description:"Content size of an adoptable, managed or repository-tracked entry; 0 for the others."`
	SHA256   string `json:"sha256" description:"Content hash of an adoptable, managed or repository-tracked entry; empty for the others."`
	Tracked  bool   `json:"tracked" description:"The project's repository tracks the file."`
	Status   string `json:"status" enum:"managed,adoptable,not_adoptable,never,not_managed,repo_tracked"`
	Reason   string `json:"reason" description:"Why the entry is not adoptable, a stable code the app translates; empty for managed and adoptable entries."`
	// DefaultAction is what the adoption dialog proposes.
	DefaultAction string   `json:"defaultAction" description:"move or keep for an adoptable entry; empty for the others."`
	KeptInPlace   []string `json:"keptInPlace" description:"What stays at the entry's location when it is adopted: the login variables of Claude Code's env setting."`
	ItemID        string   `json:"itemId" description:"The managed item of a managed entry; empty for the others."`
	ProjectID     string   `json:"projectId" description:"The project of an entry found in a project; empty for the others."`
}

// LadoSetupRootResponse is a tool folder or file the scan read.
type LadoSetupRootResponse struct {
	Tool     string `json:"tool" enum:"claude-code,codex"`
	Location string `json:"location" enum:"claude_dir,claude_json,codex_home"`
	Path     string `json:"path" description:"Empty when the daemon knows none."`
	Found    bool   `json:"found"`
	Error    string `json:"error" description:"Why it could not be read; its entries are then missing."`
}

// LadoSetupProjectScanResponse is a project whose main checkout the scan read.
type LadoSetupProjectScanResponse struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Error string `json:"error" description:"Why the project could not be read; its entries are then missing."`
}

// LadoSetupDriftResponse is one change made outside LADO.
type LadoSetupDriftResponse struct {
	ItemID string `json:"itemId" description:"The managed item that drifted; for a new local entry, the live item of its tool, kind and name if there is one."`
	Kind   string `json:"kind" enum:"edited,new_local" description:"edited: a managed copy no longer holds its item's latest version. new_local: an original location holds new adoptable setup, which is never moved by itself."`
	Tool   string `json:"tool" enum:"claude-code,codex"`
	// ItemKind is the setup item kind of what changed.
	ItemKind string `json:"itemKind" enum:"instructions,setting,skill,agent,command,output_style,hook_script,mcp_server"`
	Name     string `json:"name"`
	Path     string `json:"path" description:"What changed: the managed copy for an edit, the entry at its original location for a new local one."`
	SHA256   string `json:"sha256" description:"Its content hash now; keeping an edit sends it back."`
}

// LadoSetupAdoptionResponse is one adoption of local setup.
type LadoSetupAdoptionResponse struct {
	ID             string     `json:"id"`
	CreatedAt      time.Time  `json:"createdAt"`
	Actor          string     `json:"actor" description:"human, or human:dev-presence in a lab."`
	OS             string     `json:"os" description:"The GOOS the adoption ran on."`
	PlanSHA256     string     `json:"planSha256" description:"Hash of the adoption plan the desktop app's confirmation signed."`
	ManifestSHA256 string     `json:"manifestSha256" description:"Hash of the backup's manifest."`
	State          string     `json:"state" enum:"pending,completed,partial" description:"pending: removing the originals is under way. partial: removing them stopped, or a daemon stopped meanwhile; what moved is managed, and restore puts back what was removed."`
	ErrorMessage   string     `json:"errorMessage" description:"Why a partial adoption stopped."`
	RestoredAt     *time.Time `json:"restoredAt,omitempty" description:"When the adoption was restored; omitted before."`
	ItemCount      int        `json:"itemCount" description:"How many items the adoption recorded."`
}

// LadoSetupItemStateResponse is a managed item at its latest version, and
// how it resolves in a scope.
type LadoSetupItemStateResponse struct {
	ItemID    string                 `json:"itemId"`
	Tool      string                 `json:"tool" enum:"claude-code,codex"`
	Kind      string                 `json:"kind" enum:"instructions,setting,skill,agent,command,output_style,hook_script,mcp_server"`
	Name      string                 `json:"name"`
	VersionID string                 `json:"versionId"`
	Version   int                    `json:"version"`
	SHA256    string                 `json:"sha256"`
	On        bool                   `json:"on" description:"Sessions of the scope get the item."`
	Source    LadoRuleSourceResponse `json:"source" description:"The binding that decided it; Global without a stored binding is on."`
	Global    string                 `json:"global" enum:"on,off" description:"The Global binding; an item without one is on."`
	Project   string                 `json:"project" enum:"inherit,force_on,force_off" description:"The binding for the resolved project; inherit at Global."`
	Task      string                 `json:"task" enum:"inherit,force_on,force_off" description:"The binding for the resolved task; inherit for Global and a project."`
	// InstalledSHA256 is set for an on enabledPlugins setting.
	InstalledSHA256 string `json:"installedSha256" description:"Hash of the plugins an on enabledPlugins setting turns on, as Claude Code has them installed; empty for every other item."`
}

// LadoSetupInventoryResponse is the local setup as the scan found it, with
// the managed items at Global.
type LadoSetupInventoryResponse struct {
	OS        string          `json:"os" description:"The GOOS the daemon runs on."`
	Verified  map[string]bool `json:"verified" nullable:"false" description:"For each tool, whether LADO measured that sessions get adopted setup on this OS; adoption is refused where it did not."`
	ScannedAt time.Time       `json:"scannedAt"`
	// Entries are the managed items, then the tool folders, ~/.claude.json
	// and the projects, each in name order.
	Entries   []LadoSetupEntryResponse       `json:"entries"`
	Roots     []LadoSetupRootResponse        `json:"roots"`
	Projects  []LadoSetupProjectScanResponse `json:"projects"`
	Drift     []LadoSetupDriftResponse       `json:"drift"`
	Adoptions []LadoSetupAdoptionResponse    `json:"adoptions" description:"Oldest first."`
	Items     []LadoSetupItemStateResponse   `json:"items" description:"The managed items of both tools as they resolve at Global."`
}

// LadoSetupItemListResponse lists the managed items of both tools as they
// resolve in a scope.
type LadoSetupItemListResponse struct {
	Items []LadoSetupItemStateResponse `json:"items"`
}

// LadoSetupConflictResponse is one result of the setup conflict checks.
type LadoSetupConflictResponse struct {
	Code          string            `json:"code" description:"Stable conflict code the app translates."`
	Severity      string            `json:"severity" enum:"blocking,warning,info" description:"Only a warning can be accepted."`
	ItemID        string            `json:"itemId" description:"The managed item; empty for setup without an item, which path names."`
	Path          string            `json:"path"`
	Detail        string            `json:"detail" description:"Tells apart results of one code on one item, such as the hook event; an acceptance names it."`
	ContentSHA256 string            `json:"contentSha256" description:"The content the result was found in; an acceptance holds for that content only."`
	Details       map[string]string `json:"details" nullable:"false" description:"The values the app shows: item or origin, and by code event, matcher, rule, key, mode, ceiling, plugin and path."`
	AcceptanceID  string            `json:"acceptanceId" description:"The live acceptance that covers a warning; empty when none does."`
}

// LadoSetupConflictListResponse lists the conflict results of a scope.
type LadoSetupConflictListResponse struct {
	Conflicts []LadoSetupConflictResponse `json:"conflicts" description:"By severity, code and item; accepted warnings are marked."`
}

// LadoSetupAdoptEntryRequest names an adoptable entry and what to do with it.
type LadoSetupAdoptEntryRequest struct {
	Tool   string `json:"tool" enum:"claude-code,codex"`
	Kind   string `json:"kind" enum:"instructions,setting,skill,agent,command,output_style,hook_script,mcp_server"`
	Name   string `json:"name"`
	Action string `json:"action" enum:"move,keep"`
}

// AdoptLadoSetupRequest previews an adoption, or makes the one a preview
// hashed. Making it needs a human-presence proof for the plan's hash in
// X-LADO-Presence, which the desktop app adds after the developer confirms.
type AdoptLadoSetupRequest struct {
	Entries    []LadoSetupAdoptEntryRequest `json:"entries"`
	PlanSHA256 string                       `json:"planSha256,omitempty" description:"The preview's planSha256; an adoption refuses another one."`
	DryRun     bool                         `json:"dryRun,omitempty" description:"Preview the adoption without making it; needs no presence proof."`
}

// LadoSetupPreviewEntryResponse is one entry of an adoption preview.
type LadoSetupPreviewEntryResponse struct {
	Tool        string   `json:"tool" enum:"claude-code,codex"`
	Kind        string   `json:"kind" enum:"instructions,setting,skill,agent,command,output_style,hook_script,mcp_server"`
	Name        string   `json:"name"`
	Action      string   `json:"action" enum:"move,keep"`
	OriginPath  string   `json:"originPath"`
	SHA256      string   `json:"sha256"`
	Size        int64    `json:"size"`
	ManagedPath string   `json:"managedPath" description:"Where a moved entry goes; empty for a kept one."`
	Delivery    string   `json:"delivery" description:"How sessions get a moved entry: home (Claude Code's per-session config home) or overlay (Codex launch options); empty for a kept one."`
	KeptInPlace []string `json:"keptInPlace" description:"The login variables a moved env setting leaves in place."`
}

// LadoSetupHookRewriteResponse is a hooks or statusLine command an adoption
// rewrites to name the moved file in the session's config folder.
type LadoSetupHookRewriteResponse struct {
	Setting string   `json:"setting" enum:"hooks,statusLine"`
	Event   string   `json:"event" description:"The hook event; empty for statusLine."`
	Matcher string   `json:"matcher"`
	Before  string   `json:"before"`
	After   string   `json:"after"`
	Paths   []string `json:"paths" description:"The moved entries the command names, under the Claude folder."`
}

// LadoSetupRunningSessionResponse is a live AO session of a tool whose setup
// would move.
type LadoSetupRunningSessionResponse struct {
	ID   string `json:"id"`
	Tool string `json:"tool" enum:"claude-code,codex"`
}

// LadoSetupRunningProcessResponse is a Claude Code or Codex program running
// outside AO, by its program name, never its command line.
type LadoSetupRunningProcessResponse struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
	Tool string `json:"tool" enum:"claude-code,codex"`
}

// LadoSetupPreconditionsResponse is what holds an adoption, or a restore,
// back.
type LadoSetupPreconditionsResponse struct {
	OS               string                            `json:"os"`
	Unverified       []string                          `json:"unverified" description:"Tools whose delivery LADO has not measured on this OS."`
	Sessions         []LadoSetupRunningSessionResponse `json:"sessions"`
	Processes        []LadoSetupRunningProcessResponse `json:"processes"`
	ProcessesIgnored bool                              `json:"processesIgnored" description:"A lab daemon leaves the programs out."`
	OK               bool                              `json:"ok" description:"Nothing holds the adoption back."`
}

// LadoSetupAdoptionPreviewResponse is what an adoption would do.
type LadoSetupAdoptionPreviewResponse struct {
	PlanSHA256    string                          `json:"planSha256" description:"The hash the desktop app's confirmation signs."`
	OS            string                          `json:"os"`
	Entries       []LadoSetupPreviewEntryResponse `json:"entries"`
	Rewrites      []LadoSetupHookRewriteResponse  `json:"rewrites"`
	Conflicts     []LadoSetupConflictResponse     `json:"conflicts" description:"What the moved entries bring; a value naming a moved file that cannot follow it (LADO_SETUP_HOOK_PATH_MOVED, LADO_SETUP_PATH_USED_IN_PLACE) blocks the adoption."`
	Preconditions LadoSetupPreconditionsResponse  `json:"preconditions"`
	Warnings      []string                        `json:"warnings" description:"Stable codes, such as LADO_SETUP_DIRECT_USE_STOPS."`
}

// LadoSetupAdoptedItemResponse is an item an adoption recorded.
type LadoSetupAdoptedItemResponse struct {
	ItemID      string `json:"itemId"`
	VersionID   string `json:"versionId"`
	Tool        string `json:"tool" enum:"claude-code,codex"`
	Kind        string `json:"kind" enum:"instructions,setting,skill,agent,command,output_style,hook_script,mcp_server"`
	Name        string `json:"name"`
	ManagedPath string `json:"managedPath"`
	SHA256      string `json:"sha256"`
}

// LadoSetupVerifyCheckResponse is one structural check of an adopted item.
type LadoSetupVerifyCheckResponse struct {
	ItemID string `json:"itemId"`
	Check  string `json:"check" enum:"managed_copy,codex_override,home"`
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
}

// LadoSetupAdoptResponse is an adoption preview, or an adoption made.
type LadoSetupAdoptResponse struct {
	Preview      LadoSetupAdoptionPreviewResponse `json:"preview"`
	Applied      bool                             `json:"applied" description:"False for a preview, which changes nothing."`
	AdoptionID   string                           `json:"adoptionId" description:"Empty for a preview."`
	State        string                           `json:"state" description:"completed for an adoption made; empty for a preview."`
	Items        []LadoSetupAdoptedItemResponse   `json:"items"`
	Verification []LadoSetupVerifyCheckResponse   `json:"verification"`
	Warnings     []string                         `json:"warnings"`
}

// LadoSetupBackupEntryResponse is one original an adoption's backup holds.
type LadoSetupBackupEntryResponse struct {
	Tool   string `json:"tool" enum:"claude-code,codex"`
	Kind   string `json:"kind" enum:"instructions,setting,skill,agent,command,output_style,hook_script,mcp_server"`
	Name   string `json:"name"`
	Action string `json:"action" enum:"move,keep"`
	Root   string `json:"root" enum:"claude_dir,claude_json,codex_home"`
	Path   string `json:"path" description:"Under root, with slashes and #<key> after a key's file; the inventory's roots give the folder."`
	Type   string `json:"type" enum:"file,dir,keys"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// LadoSetupRestoredEntryResponse is the original of one item and what a
// restore did with it.
type LadoSetupRestoredEntryResponse struct {
	ItemID string `json:"itemId"`
	Tool   string `json:"tool" enum:"claude-code,codex"`
	Kind   string `json:"kind" enum:"instructions,setting,skill,agent,command,output_style,hook_script,mcp_server"`
	Name   string `json:"name"`
	Root   string `json:"root" enum:"claude_dir,claude_json,codex_home"`
	Path   string `json:"path" description:"Under root, with slashes and #<key> after a key's file."`
	Status string `json:"status" enum:"restored,restored_beside,key_kept,failed,skipped_deleted" description:"restored_beside: something new held the path, so the original is beside it; key_kept: the file has the key now, which stays; skipped_deleted: the item was deleted in LADO, so its original stays out."`
	Beside string `json:"beside" description:"The copy written beside the path, under root; empty unless restored_beside."`
	Error  string `json:"error" description:"Why a failed original could not be put back."`
	Note   string `json:"note,omitempty" enum:"kept_in_lado" description:"kept_in_lado: LADO made later versions of the item, which stay readable in LADO; omitted otherwise."`
}

// LadoSetupRestoreReportResponse is what a restore did.
type LadoSetupRestoreReportResponse struct {
	Entries []LadoSetupRestoredEntryResponse `json:"entries"`
}

// LadoSetupAdoptionDetailResponse is an adoption with its items and its
// backup.
type LadoSetupAdoptionDetailResponse struct {
	Adoption       LadoSetupAdoptionResponse       `json:"adoption"`
	Items          []LadoSetupItemResponse         `json:"items" description:"The items the adoption recorded; a restore puts back the originals of all of them."`
	BackupReadable bool                            `json:"backupReadable" description:"False when the backup's manifest cannot be read as the adoption recorded it; a restore refuses then too."`
	Backup         []LadoSetupBackupEntryResponse  `json:"backup" description:"The originals the backup holds, in backup order; empty when it is unreadable."`
	Report         *LadoSetupRestoreReportResponse `json:"report,omitempty" description:"The restore's report; omitted before a restore."`
	Preconditions  *LadoSetupPreconditionsResponse `json:"preconditions,omitempty" description:"What holds a restore back now: the live AO sessions and the programs of the tools it puts back (unverified is always empty: restore runs on every OS). Omitted once restored or when the backup cannot be restored."`
}

// LadoSetupRestoreResponse is a restore made.
type LadoSetupRestoreResponse struct {
	AdoptionID string                         `json:"adoptionId"`
	Applied    bool                           `json:"applied"`
	RestoredAt time.Time                      `json:"restoredAt"`
	Report     LadoSetupRestoreReportResponse `json:"report"`
	Warnings   []string                       `json:"warnings"`
}

// LadoSetupItemResponse is a managed setup item.
type LadoSetupItemResponse struct {
	ID         string     `json:"id"`
	Tool       string     `json:"tool" enum:"claude-code,codex"`
	Kind       string     `json:"kind" enum:"instructions,setting,skill,agent,command,output_style,hook_script,mcp_server"`
	Name       string     `json:"name"`
	OriginPath string     `json:"originPath" description:"Where adoption found the item; empty for an item made in LADO."`
	AdoptionID string     `json:"adoptionId" description:"Empty for an item made in LADO."`
	Live       bool       `json:"live" description:"Neither deleted nor restored: only a live item is delivered."`
	CreatedAt  time.Time  `json:"createdAt"`
	DeletedAt  *time.Time `json:"deletedAt,omitempty"`
	RestoredAt *time.Time `json:"restoredAt,omitempty"`
}

// LadoSetupItemVersionResponse is one version of an item. Versions never
// change.
type LadoSetupItemVersionResponse struct {
	ID          string    `json:"id" description:"Empty in a dry run, for a version not made yet."`
	ItemID      string    `json:"itemId"`
	Version     int       `json:"version" description:"Counts from 1; an edit sends the latest back as baseVersion."`
	ContentKind string    `json:"contentKind" enum:"file,json,tree"`
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	Executable  bool      `json:"executable"`
	Actor       string    `json:"actor" description:"Who made the version: api, human, or human:dev-presence in a lab."`
	CreatedAt   time.Time `json:"createdAt"`
}

// LadoSetupTreeFileResponse is one file of a folder item.
type LadoSetupTreeFileResponse struct {
	Path   string `json:"path"`
	Mode   string `json:"mode" enum:"file,exec"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// LadoSetupContentResponse is an item version's content as the app shows it.
type LadoSetupContentResponse struct {
	ContentKind string                      `json:"contentKind" enum:"file,json,tree"`
	SHA256      string                      `json:"sha256"`
	Size        int64                       `json:"size"`
	Executable  bool                        `json:"executable"`
	Text        string                      `json:"text" description:"A file's text, a one-file skill's SKILL.md, or a JSON value with two-space indents; empty for a folder and for content that is no text or larger than 1 MiB."`
	Files       []LadoSetupTreeFileResponse `json:"files" description:"A folder's files."`
}

// LadoSetupVersionDetailResponse is one version of an item with its content.
type LadoSetupVersionDetailResponse struct {
	Version LadoSetupItemVersionResponse `json:"version"`
	Content *LadoSetupContentResponse    `json:"content,omitempty" description:"Omitted when LADO could not read it."`
}

// LadoSetupItemDetailResponse is an item with every version.
type LadoSetupItemDetailResponse struct {
	Item        LadoSetupItemResponse            `json:"item"`
	ManagedPath string                           `json:"managedPath" description:"Where a live item's managed copy is; a folder item's files are edited there. Empty for an item that is not live."`
	Versions    []LadoSetupVersionDetailResponse `json:"versions" description:"Oldest first."`
	Warnings    []string                         `json:"warnings"`
}

// CreateLadoSetupItemRequest makes a managed item in LADO. A hook script or
// an MCP server needs a human-presence proof for the change's hash.
type CreateLadoSetupItemRequest struct {
	Tool    string          `json:"tool" enum:"claude-code,codex"`
	Kind    string          `json:"kind" enum:"instructions,setting,skill,agent,command,output_style,mcp_server"`
	Name    string          `json:"name"`
	Content json.RawMessage `json:"content" description:"A string with the text for a file or a one-file skill's SKILL.md; the value itself for a setting or an MCP server."`
	DryRun  bool            `json:"dryRun,omitempty" description:"Show the change without making it; needs no presence proof."`
}

// UpdateLadoSetupItemRequest edits a text item. Content that can run code
// or widen access needs a human-presence proof for the change's hash.
type UpdateLadoSetupItemRequest struct {
	BaseVersion int             `json:"baseVersion" description:"The version the edit started from; a newer one refuses the edit."`
	Content     json.RawMessage `json:"content" description:"The new content, shaped as for creation."`
	DryRun      bool            `json:"dryRun,omitempty" description:"Show the change without making it; needs no presence proof."`
}

// SetLadoSetupBindingRequest turns an item on or off at Global, or for one
// project or task. Turning on an item that can run code or widen access needs
// a human-presence proof for the change's hash.
type SetLadoSetupBindingRequest struct {
	ItemID  string `json:"itemId"`
	Scope   string `json:"scope" enum:"global,project,task"`
	ScopeID string `json:"scopeId,omitempty" description:"The project or task id; empty at global."`
	State   string `json:"state" enum:"on,off,inherit,force_on,force_off" description:"on, off or inherit at global; inherit, force_on or force_off for a project or a task. inherit removes the binding, which at Global turns the item on."`
	DryRun  bool   `json:"dryRun,omitempty" description:"Show the change without making it; needs no presence proof."`
}

// ResolveLadoSetupDriftRequest keeps a managed copy's drift as a new version,
// or reverts the copy to the latest version.
type ResolveLadoSetupDriftRequest struct {
	Action string `json:"action" enum:"keep,revert" description:"keep needs a human-presence proof for an item that can run code or widen access."`
	SHA256 string `json:"sha256,omitempty" description:"For keep: the drift's sha256 as reviewed; another one refuses the keep."`
	DryRun bool   `json:"dryRun,omitempty" description:"Show the change without making it; needs no presence proof."`
}

// AcceptLadoSetupConflictRequest accepts a conflict warning for an item's
// current content, in a scope and the narrower ones.
type AcceptLadoSetupConflictRequest struct {
	ItemID  string `json:"itemId"`
	Code    string `json:"code" description:"A warning's code; blocking and info results cannot be accepted."`
	Detail  string `json:"detail,omitempty" description:"The result's detail."`
	Scope   string `json:"scope" enum:"global,project,task"`
	ScopeID string `json:"scopeId,omitempty" description:"The project or task id; empty at global."`
	DryRun  bool   `json:"dryRun,omitempty" description:"Show the change without making it."`
}

// LadoSetupBindingResponse is a binding a change sets.
type LadoSetupBindingResponse struct {
	ItemID    string    `json:"itemId"`
	Scope     string    `json:"scope" enum:"global,project,task"`
	ScopeID   string    `json:"scopeId"`
	State     string    `json:"state" enum:"on,off,inherit,force_on,force_off"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// LadoSetupAcceptanceResponse is an acceptance a change gives or revokes.
type LadoSetupAcceptanceResponse struct {
	ID            string     `json:"id" description:"Empty in a dry run, for an acceptance not given yet."`
	Code          string     `json:"code"`
	ItemID        string     `json:"itemId"`
	Detail        string     `json:"detail"`
	ContentSHA256 string     `json:"contentSha256"`
	Scope         string     `json:"scope" enum:"global,project,task"`
	ScopeID       string     `json:"scopeId"`
	Actor         string     `json:"actor"`
	CreatedAt     time.Time  `json:"createdAt"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
}

// LadoSetupChangeResponse is a change of the managed setup, made or shown by
// a dry run.
type LadoSetupChangeResponse struct {
	Op               string                        `json:"op" enum:"create,edit,delete,binding,drift_keep,drift_revert,accept,revoke"`
	ChangeSHA256     string                        `json:"changeSha256" description:"Hash of the change without dryRun, which the desktop app's confirmation signs."`
	NeedsPresence    bool                          `json:"needsPresence" description:"Making the change needs the desktop app's confirmation for changeSha256."`
	Applied          bool                          `json:"applied" description:"False for a dry run, which changes nothing."`
	Actor            string                        `json:"actor" description:"Who made the change: human or human:dev-presence with a confirmation, api without one; empty for a dry run."`
	Item             LadoSetupItemResponse         `json:"item" description:"The item the change is about, as the change leaves it; a dry run's new item has no id."`
	Version          *LadoSetupItemVersionResponse `json:"version,omitempty" description:"The item's version after a create, an edit or a drift keep or revert."`
	Before           *LadoSetupContentResponse     `json:"before,omitempty" description:"The content before the change; omitted for a create, a binding or an acceptance."`
	After            *LadoSetupContentResponse     `json:"after,omitempty" description:"The content after the change; omitted for a delete, a binding or an acceptance."`
	ConflictsAdded   []LadoSetupConflictResponse   `json:"conflictsAdded"`
	ConflictsRemoved []LadoSetupConflictResponse   `json:"conflictsRemoved"`
	Impact           LadoRuleImpactResponse        `json:"impact"`
	Pins             int                           `json:"pins" description:"Rule snapshots that pinned a version of a deleted item; they keep reading it."`
	Binding          *LadoSetupBindingResponse     `json:"binding,omitempty"`
	Acceptance       *LadoSetupAcceptanceResponse  `json:"acceptance,omitempty"`
	Warnings         []string                      `json:"warnings" description:"Stable codes, such as LADO_SETUP_PREVIEW_INCOMPLETE."`
}

// LadoSetupAcceptedConflictResponse is a setup warning an acceptance covers.
type LadoSetupAcceptedConflictResponse struct {
	Code         string `json:"code"`
	ItemID       string `json:"itemId"`
	Detail       string `json:"detail"`
	AcceptanceID string `json:"acceptanceId"`
}

// LadoEffectiveSetupResponse is the local setup of the agent's tool in a
// project or a task.
type LadoEffectiveSetupResponse struct {
	Items          []LadoSetupItemStateResponse        `json:"items" description:"The managed items of the agent's tool, on or off; empty without an agent LADO manages setup for."`
	AOSystemSHA256 string                              `json:"aoSystemSha256" description:"Hash of what AO adds to every session; empty when LADO resolved no local setup."`
	Conflicts      []LadoPolicyProblemResponse         `json:"conflicts" description:"The conflicts of the on items that no acceptance covers; problems holds them too."`
	Accepted       []LadoSetupAcceptedConflictResponse `json:"accepted" description:"The warnings an acceptance covers."`
}

// LadoSnapshotSetupItemResponse is one setup item a rule snapshot fixed.
type LadoSnapshotSetupItemResponse struct {
	ItemID          string                 `json:"itemId"`
	Tool            string                 `json:"tool" enum:"claude-code,codex"`
	Kind            string                 `json:"kind" enum:"instructions,setting,skill,agent,command,output_style,hook_script,mcp_server"`
	Name            string                 `json:"name"`
	VersionID       string                 `json:"versionId"`
	Version         int                    `json:"version"`
	SHA256          string                 `json:"sha256"`
	Source          LadoRuleSourceResponse `json:"source"`
	InstalledSHA256 string                 `json:"installedSha256"`
}

// LadoExecutionSetupItemResponse is one setup item of an execution's
// snapshot.
type LadoExecutionSetupItemResponse struct {
	ItemID  string `json:"itemId"`
	Tool    string `json:"tool" enum:"claude-code,codex"`
	Kind    string `json:"kind" enum:"instructions,setting,skill,agent,command,output_style,hook_script,mcp_server"`
	Name    string `json:"name"`
	Version int    `json:"version"`
}
