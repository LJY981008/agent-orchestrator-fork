package ladosetup

// What the app shows of the managed setup besides changes (plan §13): the
// inventory with the managed items and the size of each adoption, the items
// as they resolve in a scope, one item with the content of every version, and
// one adoption with the originals its backup holds. Views only read: none
// writes the store, the managed folder or a tool folder.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

// CodeScopeInvalid is a view of a task together with a project the task is
// not in.
const CodeScopeInvalid = "LADO_SETUP_SCOPE_INVALID"

// Overview is the inventory as the app's Local setup section shows it.
type Overview struct {
	Inventory
	// Items are the live managed items of both tools as they resolve at
	// Global, in tool, kind and name order.
	Items []ladopolicy.SetupItemState
	// AdoptionItems counts the items each adoption recorded, deleted and
	// restored ones included.
	AdoptionItems map[domain.LadoSetupAdoptionID]int
}

// Overview returns the last scan, or a new one when rescan is set or there is
// none, with the managed items at Global and the item count of each
// adoption. After a rescan it removes the config homes of ended sessions, as
// the daemon does after its first scan; a failure there is logged.
func (s *Service) Overview(ctx context.Context, rescan bool) (Overview, error) {
	read := s.Inventory
	if rescan {
		read = s.Scan
	}
	inv, err := read(ctx)
	if err != nil {
		return Overview{}, err
	}
	if rescan {
		if err := s.CleanupHomes(ctx); err != nil {
			s.deps.Log.Warn("LADO could not remove the config homes of ended sessions", "err", err)
		}
	}
	items, err := s.ScopeItems(ctx, "", "")
	if err != nil {
		return Overview{}, err
	}
	counts := make(map[domain.LadoSetupAdoptionID]int, len(inv.Adoptions))
	for _, a := range inv.Adoptions {
		recorded, err := s.deps.Store.ListLadoSetupItemsForAdoption(ctx, a.ID)
		if err != nil {
			return Overview{}, fmt.Errorf("list the items of adoption %s: %w", a.ID, err)
		}
		counts[a.ID] = len(recorded)
	}
	return Overview{Inventory: inv, Items: items, AdoptionItems: counts}, nil
}

// ScopeItems returns the live managed items of both tools as they resolve in
// a scope: Global without ids, a project, or a task, whose project then
// applies. A snapshot takes only the agent tool's items; this view holds
// both tools', so the app can bind any item in any scope.
func (s *Service) ScopeItems(ctx context.Context, projectID domain.ProjectID, taskID domain.LadoTaskID) ([]ladopolicy.SetupItemState, error) {
	if s.deps.Store == nil {
		return []ladopolicy.SetupItemState{}, nil
	}
	scope, err := s.viewScope(ctx, projectID, taskID)
	if err != nil {
		return nil, err
	}
	st, err := s.readScope(ctx, scope.project, scope.task)
	if err != nil {
		return nil, err
	}
	items := make([]ladopolicy.SetupItemState, 0, len(st.items))
	for _, iv := range st.items {
		items = append(items, st.state(iv))
	}
	return items, nil
}

// viewScope checks the scope a view names: Global without ids, a project
// LADO knows that is not archived, or a task with an issue, whose project
// applies. A cancelled task can still be viewed.
func (s *Service) viewScope(ctx context.Context, projectID domain.ProjectID, taskID domain.LadoTaskID) (scopeRef, error) {
	if taskID == "" {
		if projectID == "" {
			return scopeRef{}, nil
		}
		return s.scopeOf(ctx, domain.LadoScopeProject, string(projectID), false)
	}
	scope, err := s.scopeOf(ctx, domain.LadoScopeTask, string(taskID), false)
	if err != nil {
		return scopeRef{}, err
	}
	if projectID != "" && projectID != scope.project {
		return scopeRef{}, apierr.Invalid(CodeScopeInvalid, "The task belongs to another project.",
			map[string]any{"projectId": string(projectID), "taskId": string(taskID)})
	}
	return scope, nil
}

// ceiling is the rules' permission ceiling, empty without a policy.
func (s *Service) ceiling(ctx context.Context) (domain.PermissionMode, error) {
	s.mu.Lock()
	policy := s.policy
	s.mu.Unlock()
	if policy == nil {
		return "", nil
	}
	return policy.Ceiling(ctx)
}

// ItemView is a managed item with its versions, oldest first.
type ItemView struct {
	Item     domain.LadoSetupItem
	Versions []VersionView
	// ManagedPath is where a live item's managed copy is, with "#<key>"
	// after the file for a key. A folder item's files are edited there, and
	// drift offers the edit to keep.
	ManagedPath string
	// Warnings hold WarningPreviewIncomplete when the content of a version
	// could not be read.
	Warnings []string
}

// VersionView is one version of an item with its content as the app shows
// it, nil when LADO could not read it.
type VersionView struct {
	Version domain.LadoSetupItemVersion
	Content *ChangeContent
}

// Item returns a managed item with the content of every version. An item
// that was deleted, or whose adoption was restored, is returned too: its
// versions stay readable.
func (s *Service) Item(ctx context.Context, id domain.LadoSetupItemID) (ItemView, error) {
	if err := s.available(); err != nil {
		return ItemView{}, err
	}
	it, versions, ok, err := s.deps.Store.LadoSetupItem(ctx, id)
	if err != nil {
		return ItemView{}, fmt.Errorf("read the setup item: %w", err)
	}
	if !ok {
		return ItemView{}, apierr.NotFound(CodeNotFound, "LADO does not manage this setup item.")
	}
	view := ItemView{Item: it, Versions: make([]VersionView, 0, len(versions)), Warnings: []string{}}
	if it.Live() {
		view.ManagedPath = managedPath(s.deps.StateDir, it.Tool, it.Kind, it.Name)
	}
	for _, v := range versions {
		content, err := s.versionView(it, v)
		if err != nil {
			s.deps.Log.Warn("LADO could not read a setup item version to show it", "item", it.ID, "version", v.Version, "err", err)
			view.Warnings = appendWarning(view.Warnings, WarningPreviewIncomplete)
		}
		view.Versions = append(view.Versions, VersionView{Version: v, Content: content})
	}
	return view, nil
}

// AdoptionView is an adoption with the items it recorded and the originals
// its backup holds.
type AdoptionView struct {
	Adoption domain.LadoSetupAdoption
	// Items are the items the adoption recorded, deleted and restored ones
	// included, in tool, kind and name order. A restore puts back the
	// originals of all of them.
	Items []domain.LadoSetupItem
	// Backup lists the originals the backup holds, in backup order. It is
	// nil when the manifest cannot be read as the adoption recorded it,
	// which a restore refuses as well.
	Backup []BackupEntry
	// Report is the restore's report, nil before a restore.
	Report *RestoreReport
}

// BackupEntry is one original an adoption's backup holds. Root and Path
// place it as a restore report does, under its root with slashes and
// "#<key>" after a key's file, so neither names the developer's home.
type BackupEntry struct {
	Tool   domain.LadoSetupTool
	Kind   domain.LadoSetupKind
	Name   string
	Action domain.LadoSetupAction
	Root   domain.LadoSetupLocation
	Path   string
	// Type is file, dir, or keys for keys taken out of a file.
	Type   string
	Size   int64
	SHA256 string
}

// Adoption returns an adoption with its items, the originals its backup
// holds and its restore's report.
func (s *Service) Adoption(ctx context.Context, id domain.LadoSetupAdoptionID) (AdoptionView, error) {
	if err := s.available(); err != nil {
		return AdoptionView{}, err
	}
	adoption, ok, err := s.deps.Store.LadoSetupAdoption(ctx, id)
	if err != nil {
		return AdoptionView{}, fmt.Errorf("read the adoption: %w", err)
	}
	if !ok {
		return AdoptionView{}, apierr.NotFound(CodeNotFound, "LADO has no such adoption.")
	}
	items, err := s.deps.Store.ListLadoSetupItemsForAdoption(ctx, id)
	if err != nil {
		return AdoptionView{}, fmt.Errorf("list the adoption's items: %w", err)
	}
	view := AdoptionView{Adoption: adoption, Items: items}
	if _, m, err := s.readBackupManifest(adoption); err != nil {
		s.deps.Log.Warn("LADO cannot read an adoption's backup manifest", "adoption", id, "err", err)
	} else {
		view.Backup = make([]BackupEntry, 0, len(m.Entries))
		for _, be := range m.Entries {
			path := be.RelPath
			if be.Type == backupTypeKeys {
				path += "#" + keySuffix(be)
			}
			view.Backup = append(view.Backup, BackupEntry{
				Tool: be.Tool, Kind: be.Kind, Name: be.Name, Action: be.Action,
				Root: be.Root, Path: path, Type: be.Type, Size: be.Size, SHA256: be.SHA256,
			})
		}
	}
	if len(adoption.RestoreReport) > 0 {
		var report RestoreReport
		if err := json.Unmarshal(adoption.RestoreReport, &report); err != nil {
			s.deps.Log.Warn("LADO cannot read an adoption's restore report", "adoption", id, "err", err)
		} else {
			view.Report = &report
		}
	}
	return view, nil
}
