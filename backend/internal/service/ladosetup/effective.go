package ladosetup

// The effective setup of a scope (plan §11): the managed items of the agent
// tool that runs a task, on or off for a project or a task with the scopes
// and states of rule bindings, and the conflicts of the items that are on.
// Rule policy fixes them in a plan approval's snapshot, so a change after the
// approval asks for review. Resolution reads stored versions only: a drifted
// managed copy never changes it (decision n).

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

var _ ladopolicy.SetupSource = (*Service)(nil)

// pluginsFormat names the canonical JSON that InstalledSHA256 hashes.
const pluginsFormat = "lado-setup-plugins/1"

// enabledPluginsKey is the Claude Code setting that turns plugins on.
const enabledPluginsKey = "enabledPlugins"

// EffectiveSetup resolves the setup a scope's sessions get
// (ladopolicy.SetupSource): every live item of the agent's tool at its latest
// version, on or off by the nearest of its bindings (the task's, the
// project's, then the Global one, which is on without a stored row), the
// conflicts of the on items that no acceptance covers as problems, the
// warnings one covers, and the AO system layer. An agent of another tool, or
// none, gets no items. A change in the scope is applied to what the store
// holds first, so a dry run resolves the setup as the change would leave it.
func (s *Service) EffectiveSetup(ctx context.Context, in ladopolicy.SetupScope) (ladopolicy.SetupResolution, error) {
	_, aoSum, err := CurrentAOSystem()
	if err != nil {
		return ladopolicy.SetupResolution{}, err
	}
	res := ladopolicy.SetupResolution{
		Items:          []ladopolicy.SetupItemState{},
		Problems:       []domain.LadoPolicyProblem{},
		Accepted:       []ladopolicy.AcceptedConflict{},
		AOSystemSHA256: aoSum,
	}
	tool, ok := toolOf(in.Harness)
	if !ok || s.deps.Store == nil {
		return res, nil
	}
	st, err := s.readScope(ctx, in.ProjectID, in.TaskID)
	if err != nil {
		return ladopolicy.SetupResolution{}, err
	}
	if in.Change != nil {
		st.apply(*in.Change)
	}
	items, setup, err := s.onItems(st, []domain.LadoSetupTool{tool})
	if err != nil {
		return ladopolicy.SetupResolution{}, err
	}
	res.Items = items
	for _, c := range s.Conflicts(ConflictInput{
		ProjectID: in.ProjectID, TaskID: in.TaskID, Setup: setup, Moved: st.moved(), Ceiling: in.Ceiling, Acceptances: st.acceptances,
	}) {
		if c.Accepted() {
			res.Accepted = append(res.Accepted, ladopolicy.AcceptedConflict{Code: c.Code, ItemID: c.ItemID, Detail: c.Detail, AcceptanceID: c.AcceptanceID})
			continue
		}
		res.Problems = append(res.Problems, c.Problem())
	}
	return res, nil
}

// ScopeConflictsInput names a scope as ConflictInput does: neither id for
// Global, the project, or the task, whose project then applies.
type ScopeConflictsInput struct {
	ProjectID domain.ProjectID
	TaskID    domain.LadoTaskID
	// Ceiling is the rules' permission ceiling. Empty asks the policy for it,
	// and without a policy skips the default mode check.
	Ceiling domain.PermissionMode
}

// ScopeConflicts returns the conflict results of the items of both tools that
// are on in a scope, the accepted ones marked, for the app's list of what
// needs attention. A project's scope also compares the adopted settings with
// the keys of the project's repository .claude/settings.json in its main
// checkout. Claude Code loads that file from the worktree, so the result is
// only shown and never part of a snapshot (decision l).
func (s *Service) ScopeConflicts(ctx context.Context, in ScopeConflictsInput) ([]Conflict, error) {
	if s.deps.Store == nil {
		return []Conflict{}, nil
	}
	scope, err := s.viewScope(ctx, in.ProjectID, in.TaskID)
	if err != nil {
		return nil, err
	}
	if in.Ceiling == "" {
		if in.Ceiling, err = s.ceiling(ctx); err != nil {
			return nil, err
		}
	}
	st, err := s.readScope(ctx, scope.project, scope.task)
	if err != nil {
		return nil, err
	}
	_, setup, err := s.onItems(st, setupTools)
	if err != nil {
		return nil, err
	}
	project, err := s.projectSettings(ctx, scope.project)
	if err != nil {
		return nil, err
	}
	out := s.Conflicts(ConflictInput{
		ProjectID: scope.project, TaskID: scope.task, Setup: setup, Moved: st.moved(), Ceiling: in.Ceiling,
		ProjectSettings: project, Acceptances: st.acceptances,
	})
	if out == nil {
		out = []Conflict{}
	}
	return out, nil
}

// projectSettings reads the repository .claude/settings.json of a project's
// main checkout, nil without a project or the file.
func (s *Service) projectSettings(ctx context.Context, id domain.ProjectID) ([]byte, error) {
	if id == "" || s.deps.Projects == nil {
		return nil, nil
	}
	projects, err := s.deps.Projects.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the projects: %w", err)
	}
	for _, p := range projects {
		if domain.ProjectID(p.ID) == id && p.Path != "" {
			// Claude Code reads nothing from a file it cannot read, and
			// neither does the comparison.
			data, _ := readRegular(filepath.Join(p.Path, ".claude", claudeSettings))
			return data, nil
		}
	}
	return nil, nil
}

// toolOf is the setup tool of an agent, false for an agent LADO manages no
// setup for.
func toolOf(harness domain.AgentHarness) (domain.LadoSetupTool, bool) {
	tool := domain.LadoSetupTool(harness)
	return tool, slices.Contains(setupTools, tool)
}

// scopeSetup is what the store holds for one scope's setup.
type scopeSetup struct {
	project domain.ProjectID
	task    domain.LadoTaskID
	// items are the live items at their latest versions, in tool, kind and
	// name order.
	items []domain.LadoSetupItemWithVersion
	// global, projects and tasks are the bindings of the scope by item: a
	// Global one, and the force_on or force_off of its project and task.
	global, projects, tasks map[domain.LadoSetupItemID]domain.LadoBindingState
	acceptances             []domain.LadoConflictAcceptance
	// changed holds the content of a version a change brings, by its id.
	changed map[domain.LadoSetupItemVersionID][]byte
}

func (s *Service) readScope(ctx context.Context, project domain.ProjectID, task domain.LadoTaskID) (*scopeSetup, error) {
	st := &scopeSetup{
		project:  project,
		task:     task,
		global:   map[domain.LadoSetupItemID]domain.LadoBindingState{},
		projects: map[domain.LadoSetupItemID]domain.LadoBindingState{},
		tasks:    map[domain.LadoSetupItemID]domain.LadoBindingState{},
		changed:  map[domain.LadoSetupItemVersionID][]byte{},
	}
	var err error
	if st.items, err = s.deps.Store.ListLiveLadoSetupItems(ctx); err != nil {
		return nil, fmt.Errorf("list the managed setup items: %w", err)
	}
	global, err := s.deps.Store.ListLadoSetupGlobalBindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the setup Global bindings: %w", err)
	}
	st.bind(global)
	for _, scope := range []struct {
		scope domain.LadoRuleScope
		id    string
	}{{domain.LadoScopeProject, string(project)}, {domain.LadoScopeTask, string(task)}} {
		if scope.id == "" {
			continue
		}
		bindings, err := s.deps.Store.ListLadoSetupBindings(ctx, scope.scope, scope.id)
		if err != nil {
			return nil, fmt.Errorf("list the setup bindings of a %s: %w", scope.scope, err)
		}
		st.bind(bindings)
	}
	if st.acceptances, err = s.deps.Store.ListLiveLadoConflictAcceptances(ctx); err != nil {
		return nil, fmt.Errorf("list the setup conflict acceptances: %w", err)
	}
	return st, nil
}

// bind keeps the bindings that belong to the scope with a state their scope
// takes: on or off at Global, force_on or force_off for the project and the
// task. Inherit removes a binding, as storing it does.
func (st *scopeSetup) bind(bindings []domain.LadoSetupBinding) {
	for _, b := range bindings {
		states, takes := st.global, []domain.LadoBindingState{domain.LadoBindingOn, domain.LadoBindingOff}
		switch {
		case b.Scope == domain.LadoScopeGlobal && b.ScopeID == "":
		case b.Scope == domain.LadoScopeProject && st.project != "" && b.ScopeID == string(st.project):
			states, takes = st.projects, []domain.LadoBindingState{domain.LadoBindingForceOn, domain.LadoBindingForceOff}
		case b.Scope == domain.LadoScopeTask && st.task != "" && b.ScopeID == string(st.task):
			states, takes = st.tasks, []domain.LadoBindingState{domain.LadoBindingForceOn, domain.LadoBindingForceOff}
		default:
			continue
		}
		switch {
		case b.State == domain.LadoBindingInherit:
			delete(states, b.ItemID)
		case slices.Contains(takes, b.State):
			states[b.ItemID] = b.State
		}
	}
}

// apply changes what the store holds as the change would.
func (st *scopeSetup) apply(c ladopolicy.SetupChange) {
	if c.Item != nil {
		st.items = slices.DeleteFunc(st.items, func(iv domain.LadoSetupItemWithVersion) bool { return iv.Item.ID == c.Item.Item.ID })
		st.items = append(st.items, *c.Item)
		if c.Content != nil {
			st.changed[c.Item.Version.ID] = c.Content
		}
		slices.SortFunc(st.items, func(a, b domain.LadoSetupItemWithVersion) int {
			return cmp.Or(cmp.Compare(a.Item.Tool, b.Item.Tool), cmp.Compare(a.Item.Kind, b.Item.Kind), strings.Compare(a.Item.Name, b.Item.Name))
		})
	}
	if c.Remove != "" {
		st.items = slices.DeleteFunc(st.items, func(iv domain.LadoSetupItemWithVersion) bool { return iv.Item.ID == c.Remove })
	}
	if c.Binding != nil {
		st.bind([]domain.LadoSetupBinding{*c.Binding})
	}
	if c.Accept != nil {
		st.acceptances = append(st.acceptances, *c.Accept)
	}
	if c.Revoke != "" {
		st.acceptances = slices.DeleteFunc(slices.Clone(st.acceptances), func(a domain.LadoConflictAcceptance) bool { return a.ID == c.Revoke })
	}
}

// state is how an item resolves in the scope: the task's binding, else the
// project's, else the Global one decides.
func (st *scopeSetup) state(iv domain.LadoSetupItemWithVersion) ladopolicy.SetupItemState {
	it, v := iv.Item, iv.Version
	item := ladopolicy.SetupItemState{
		ItemID: it.ID, Tool: it.Tool, Kind: it.Kind, Name: it.Name,
		VersionID: v.ID, Version: v.Version, SHA256: v.SHA256,
		Global: domain.LadoBindingOn, Project: domain.LadoBindingInherit, Task: domain.LadoBindingInherit,
	}
	if state, ok := st.global[it.ID]; ok {
		item.Global = state
	}
	if state, ok := st.projects[it.ID]; ok {
		item.Project = state
	}
	if state, ok := st.tasks[it.ID]; ok {
		item.Task = state
	}
	switch {
	case item.Task != domain.LadoBindingInherit:
		item.On = item.Task == domain.LadoBindingForceOn
		item.Source = ladopolicy.Source{Scope: domain.LadoScopeTask, ScopeID: string(st.task), State: item.Task}
	case item.Project != domain.LadoBindingInherit:
		item.On = item.Project == domain.LadoBindingForceOn
		item.Source = ladopolicy.Source{Scope: domain.LadoScopeProject, ScopeID: string(st.project), State: item.Project}
	default:
		item.On = item.Global == domain.LadoBindingOn
		item.Source = ladopolicy.Source{Scope: domain.LadoScopeGlobal, State: item.Global}
	}
	return item
}

// onItems resolves the items of tools and returns them with the content the
// conflict checks read for the on ones: a setting's or an MCP server's JSON
// from its blob, and only the name of the other kinds.
func (s *Service) onItems(st *scopeSetup, tools []domain.LadoSetupTool) ([]ladopolicy.SetupItemState, []SetupContent, error) {
	items := []ladopolicy.SetupItemState{}
	var setup []SetupContent
	for _, iv := range st.items {
		if !slices.Contains(tools, iv.Item.Tool) {
			continue
		}
		item := st.state(iv)
		if item.On {
			c := SetupContent{ItemID: item.ItemID, Tool: item.Tool, Kind: item.Kind, Name: item.Name, SHA256: item.SHA256}
			if isKeyKind(item.Kind) {
				content, err := s.versionContent(st, iv.Version)
				if err != nil {
					return nil, nil, fmt.Errorf("read the content of %s %s %s: %w", item.Tool, item.Kind, item.Name, err)
				}
				c.Content = content
			}
			if item.Tool == domain.LadoSetupToolClaudeCode && item.Kind == domain.LadoSetupKindSetting && item.Name == enabledPluginsKey {
				item.InstalledSHA256 = s.installedSum(c.Content)
			}
			setup = append(setup, c)
		}
		items = append(items, item)
	}
	return items, setup, nil
}

// versionContent is a version's content: what a change brings, else its blob.
func (s *Service) versionContent(st *scopeSetup, v domain.LadoSetupItemVersion) ([]byte, error) {
	if content, ok := st.changed[v.ID]; ok {
		return content, nil
	}
	if s.deps.Blobs == nil {
		return nil, errors.New("LADO setup has no blob store")
	}
	return s.deps.Blobs.Get(v.SHA256, maxBlobBytes)
}

// moved are the slash paths in the Claude folder of the live Claude Code file
// and folder items: a command that names one there misses the managed copy.
func (st *scopeSetup) moved() []string {
	var names []string
	for _, iv := range st.items {
		if name, ok := claudeFolderPath(iv.Item); ok {
			names = append(names, name)
		}
	}
	return names
}

// claudeFolderPath is where a Claude Code file or folder item sits in a
// Claude folder, as a slash path: an instructions file at the top, and the
// other kinds in their item folder.
func claudeFolderPath(it domain.LadoSetupItem) (string, bool) {
	if it.Tool != domain.LadoSetupToolClaudeCode {
		return "", false
	}
	if it.Kind == domain.LadoSetupKindInstructions {
		return it.Name, true
	}
	for _, folder := range claudeItemDirs {
		if folder.kind == it.Kind {
			return path.Join(folder.name, it.Name), true
		}
	}
	return "", false
}

// installedSum hashes what an enabledPlugins value turns on as Claude Code
// has it installed: for each plugin set to true, the folder, version and
// commit of its user install, or the name alone without one. A plugins file
// that cannot be read counts as well, so reading it again changes the hash.
func (s *Service) installedSum(value []byte) string {
	plugins, _ := jsonObject(value)
	installs, err := installedPlugins(s.deps.ClaudeDir)
	readable := err == nil || errors.Is(err, fs.ErrNotExist)
	list := []map[string]any{}
	for _, name := range sortedKeys(plugins) {
		var on bool
		if json.Unmarshal(plugins[name], &on) != nil || !on {
			continue
		}
		entry := map[string]any{"name": name}
		if in, ok := installs[name]; ok {
			entry["installPath"] = in.InstallPath
			entry["version"] = in.Version
			entry["gitCommitSha"] = in.GitCommitSha
		}
		list = append(list, entry)
	}
	// Strings, booleans and lists and maps of them always encode.
	data, _ := marshalCanonical(map[string]any{"format": pluginsFormat, "readable": readable, "plugins": list})
	return sha256Hex(data)
}
