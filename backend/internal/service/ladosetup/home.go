package ladosetup

// Claude Code delivery (plan §9, decisions a, b and c). Once LADO manages
// Claude Code setup, every Claude Code session AO launches runs on a config
// home of its own, <state>/lado/homes/<session-id>/claude/: CLAUDE_CONFIG_DIR
// names the home and CLAUDE_SECURESTORAGE_CONFIG_DIR the folder that keeps
// the session's login, so it signs in where it always did (S01 measured both
// with the real binary). A home is made from the session's base folder, the
// account's folder or the computer's own Claude folder, and the managed
// items:
//
//   - the base's state entries are links, created empty in the base first
//     when it has none, as AO's account linker does;
//   - skills/, agents/, commands/, output-styles/ and hooks/ are real
//     folders with a link per base entry and the managed items, which win a
//     name clash: a folder item links to its tree, made once under
//     <state>/lado/harness/trees/, and a file item is a copy;
//   - top-level Markdown is a link per base file and a copy per managed one;
//   - settings.json is the base's file with the managed settings' keys;
//   - .claude.json is a copy of the base's whose mcpServers hold the base's
//     servers and the managed ones. Claude's changes to the copy stay.
//
// Every launch reconciles the home instead of making it again: what LADO
// placed before and no longer wants goes, and what Claude made there stays.
// A record beside the home names what LADO placed. Homes are made from stored
// versions only, never from the managed folder.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.LadoSetupHome = (*Service)(nil)

// CodeHomeFailed is a session config home LADO could not build. Its details
// name the stage, never a path or content.
const CodeHomeFailed = "LADO_SETUP_HOME_FAILED"

// Stages of building a home.
const (
	homeStageItems    = "items"
	homeStageBase     = "base"
	homeStageContent  = "content"
	homeStageSettings = "settings"
	homeStageEntries  = "entries"
	homeStageConfig   = "claude_json"
	homeStageRecord   = "record"
)

const (
	envClaudeConfigDir     = "CLAUDE_CONFIG_DIR"
	envClaudeSecureStorage = "CLAUDE_SECURESTORAGE_CONFIG_DIR"
	claudeConfigName       = ".claude.json"
	homeRecordName         = "placed.json"
	homeRecordFormat       = "lado-setup-home/1"
)

// claudeStateDirs and claudeStateFile are the base entries a home links to:
// what Claude writes as it runs, so transcripts, history and plugins stay the
// base's. They are the shared entries of AO's account linker without the item
// folders, which a home merges instead.
var claudeStateDirs = []string{
	"projects", "sessions", "session-env", "file-history", "shell-snapshots", "todos",
	"paste-cache", "tasks", "plans", "transcripts", "plugins",
}

const claudeStateFile = "history.jsonl"

// homeGrace is how long a home stays after it was prepared even when its
// session reads as ended: a restore builds the home before it marks the
// session live again.
const homeGrace = 10 * time.Minute

var (
	homeFolderName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	treeFolderName = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// homesRoot holds the session config homes, a folder per session.
func homesRoot(stateDir string) string { return filepath.Join(stateDir, "lado", "homes") }

// homeDir is a session's Claude Code config home.
func homeDir(stateDir string, id domain.SessionID) string {
	return filepath.Join(homesRoot(stateDir), string(id), "claude")
}

// treesRoot holds the trees of folder items, a folder per tree sha256.
func treesRoot(stateDir string) string { return filepath.Join(stateDir, "lado", "harness", "trees") }

// PrepareClaudeHome builds or reconciles a Claude Code session's config home
// and returns CLAUDE_CONFIG_DIR and CLAUDE_SECURESTORAGE_CONFIG_DIR for it
// (ports.LadoSetupHome). Without a live managed Claude Code item it returns
// nothing, and the session launches as it would without LADO (decision a). A
// failure is CodeHomeFailed, which fails the launch.
func (s *Service) PrepareClaudeHome(ctx context.Context, in ports.LadoSetupHomeInput) (map[string]string, error) {
	if s.deps.Store == nil {
		return nil, nil
	}
	items, deliver, err := s.sessionItems(ctx, domain.LadoSetupToolClaudeCode, in.ProjectID, in.ClientRequestID)
	if err != nil {
		return nil, s.homeFailed(in.SessionID, homeStageItems, err)
	}
	if !deliver {
		return nil, nil
	}
	if s.deps.Blobs == nil || s.deps.StateDir == "" {
		return nil, s.homeFailed(in.SessionID, homeStageItems, errors.New("LADO setup has no blob store or state dir"))
	}
	if !homeFolderName.MatchString(string(in.SessionID)) {
		return nil, s.homeFailed(in.SessionID, homeStageBase, fmt.Errorf("the session id %q names no folder", in.SessionID))
	}
	base, ok, err := s.homeBase(in.Env)
	if err != nil {
		return nil, s.homeFailed(in.SessionID, homeStageBase, err)
	}
	if !ok {
		return nil, nil
	}
	home := homeDir(s.deps.StateDir, in.SessionID)
	s.markPrepared(in.SessionID)
	if err := s.buildHome(ctx, home, base, items, true); err != nil {
		return nil, s.homeFailed(in.SessionID, homeStageEntries, err)
	}
	return map[string]string{envClaudeConfigDir: home, envClaudeSecureStorage: base.secure}, nil
}

// sessionItems returns the items of a tool that a session gets, and whether
// it gets any delivery at all: only while a live managed item of the tool
// exists (decision a). A LADO execution gets the versions its rule snapshot
// pinned, live or not since, so a later change never reaches it; any other
// session gets the current versions that are on for its project (decision c).
func (s *Service) sessionItems(ctx context.Context, tool domain.LadoSetupTool, project domain.ProjectID, clientRequestID string) ([]domain.LadoSetupItemWithVersion, bool, error) {
	live, err := s.deps.Store.ListLiveLadoSetupItems(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("list the managed setup items: %w", err)
	}
	live = itemsOf(live, tool)
	if len(live) == 0 {
		return nil, false, nil
	}
	if clientRequestID != "" {
		pinned, execution, err := s.deps.Store.LadoSetupVersionsForClientRequest(ctx, clientRequestID)
		if err != nil {
			return nil, false, fmt.Errorf("read the setup an execution pinned: %w", err)
		}
		if execution {
			return itemsOf(pinned, tool), true, nil
		}
	}
	off, err := s.offForProject(ctx, project)
	if err != nil {
		return nil, false, err
	}
	return slices.DeleteFunc(live, func(iv domain.LadoSetupItemWithVersion) bool { return off[iv.Item.ID] }), true, nil
}

func itemsOf(items []domain.LadoSetupItemWithVersion, tool domain.LadoSetupTool) []domain.LadoSetupItemWithVersion {
	var out []domain.LadoSetupItemWithVersion
	for _, iv := range items {
		if iv.Item.Tool == tool {
			out = append(out, iv)
		}
	}
	return out
}

// offForProject returns the items that are off for a project: a Project
// binding forces an item on or off, else its Global binding decides, and an
// item without one is on. A Task binding counts for an execution only, whose
// snapshot pinned its result.
func (s *Service) offForProject(ctx context.Context, project domain.ProjectID) (map[domain.LadoSetupItemID]bool, error) {
	bindings, err := s.deps.Store.ListLadoSetupGlobalBindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the setup Global bindings: %w", err)
	}
	if project != "" {
		scoped, err := s.deps.Store.ListLadoSetupBindings(ctx, domain.LadoScopeProject, string(project))
		if err != nil {
			return nil, fmt.Errorf("list the setup bindings of a project: %w", err)
		}
		// After the Global ones, so they win.
		bindings = append(bindings, scoped...)
	}
	off := map[domain.LadoSetupItemID]bool{}
	for _, b := range bindings {
		off[b.ItemID] = b.State == domain.LadoBindingOff || b.State == domain.LadoBindingForceOff
	}
	return off, nil
}

// homeBase is the folder a home is made from.
type homeBase struct {
	// dir is the base Claude folder and config its .claude.json, empty when
	// there is none.
	dir, config string
	// secure is the session's CLAUDE_SECURESTORAGE_CONFIG_DIR, the folder
	// that keeps its login.
	secure string
}

// homeBase finds a session's base from its launch environment: the folder
// CLAUDE_CONFIG_DIR names, an account's as AO sets it, whose login stays where
// CLAUDE_SECURESTORAGE_CONFIG_DIR names it; or else the computer's own Claude
// folder, which keeps its own login, except on macOS, where only an empty
// value reaches the Keychain item of the default folder. ok is false when the
// environment names a LADO home already, or a relative folder: the session
// then launches as it is.
func (s *Service) homeBase(env map[string]string) (homeBase, bool, error) {
	if raw, set := s.envValue(env, envClaudeConfigDir); set && strings.TrimSpace(raw) != "" {
		dir := strings.TrimSpace(raw)
		if !filepath.IsAbs(dir) {
			s.deps.Log.Warn("LADO gives no config home to a Claude Code session whose CLAUDE_CONFIG_DIR is relative")
			return homeBase{}, false, nil
		}
		if s.deps.StateDir != "" && within(homesRoot(s.deps.StateDir), dir) {
			return homeBase{}, false, nil
		}
		secure, set := s.envValue(env, envClaudeSecureStorage)
		if !set {
			// Claude keeps the login of the folder it is pointed at there,
			// keyed on the path as written.
			secure = raw
		}
		return homeBase{dir: dir, config: filepath.Join(dir, claudeConfigName), secure: secure}, true, nil
	}
	if s.deps.ClaudeDir == "" {
		return homeBase{}, false, errors.New("the daemon gave no Claude folder")
	}
	b := homeBase{dir: s.deps.ClaudeDir, config: s.deps.ClaudeJSON, secure: s.deps.ClaudeDir}
	if !s.deps.ClaudeDirFromEnv && s.deps.GOOS == "darwin" {
		b.secure = ""
	}
	return b, true, nil
}

// envValue reads a variable of a launch environment, in any case on Windows,
// whose environment ignores it.
func (s *Service) envValue(env map[string]string, key string) (string, bool) {
	if value, ok := env[key]; ok {
		return value, true
	}
	if s.deps.GOOS == "windows" {
		for k, value := range env {
			if strings.EqualFold(k, key) {
				return value, true
			}
		}
	}
	return "", false
}

// within reports whether p is dir or inside it.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// markPrepared notes that a session's home is being prepared, which keeps
// CleanupHomes off it for homeGrace.
func (s *Service) markPrepared(id domain.SessionID) {
	s.homesMu.Lock()
	defer s.homesMu.Unlock()
	if s.homesPrepared == nil {
		s.homesPrepared = map[domain.SessionID]time.Time{}
	}
	s.homesPrepared[id] = s.deps.Clock()
}

// homeError is a home that could not be built at a stage.
type homeError struct {
	stage string
	err   error
}

func (e *homeError) Error() string { return e.stage + ": " + e.err.Error() }

func (e *homeError) Unwrap() error { return e.err }

// atStage names the stage of an error that names none yet.
func atStage(stage string, err error) error {
	var he *homeError
	if err == nil || errors.As(err, &he) {
		return err
	}
	return &homeError{stage: stage, err: err}
}

// homeFailed logs why a home could not be built and returns CodeHomeFailed,
// which names the stage only.
func (s *Service) homeFailed(id domain.SessionID, stage string, err error) error {
	var he *homeError
	if errors.As(err, &he) {
		stage = he.stage
	}
	s.deps.Log.Error("LADO could not prepare the config home of a Claude Code session", "session", id, "stage", stage, "err", err)
	return apierr.New(apierr.KindInternal, CodeHomeFailed,
		"LADO could not prepare this session's Claude Code setup, so the session did not start. The daemon log says why.",
		map[string]any{"stage": stage})
}

// homeNode is an entry a home should hold: a link, a file, or a real folder
// with entries of its own.
type homeNode struct {
	link string
	file *homeFile
	dir  map[string]*homeNode
	// lado marks an entry that is LADO's whatever is there now: settings.json,
	// which Claude rewrites when a session changes a setting.
	lado bool
}

type homeFile struct {
	data []byte
	mode fs.FileMode
}

// buildHome reconciles the config home at home from base and items: it
// places what the home should hold, takes away what LADO placed before and
// no longer wants, sets the servers of .claude.json and records beside the
// home what it placed. createMissing creates the base's missing state entries
// first; without it they get no link.
func (s *Service) buildHome(ctx context.Context, home string, base homeBase, items []domain.LadoSetupItemWithVersion, createMissing bool) error {
	plan, err := s.homePlan(ctx, base, items, createMissing)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, privateDir); err != nil {
		return atStage(homeStageEntries, err)
	}
	recordPath := filepath.Join(filepath.Dir(home), homeRecordName)
	prev := readHomeRecord(recordPath)
	next := &homeRecord{Format: homeRecordFormat, Entries: map[string]string{}}
	err = atStage(homeStageEntries, s.place(home, "", plan, prev, next))
	if err == nil {
		err = atStage(homeStageEntries, removeStale(home, prev, next))
	}
	if err == nil {
		err = atStage(homeStageConfig, s.reconcileConfig(home, base, items, prev, next))
	}
	if err != nil {
		// What LADO placed before the failure stays claimed, so the next
		// launch can take it away.
		_ = writeHomeRecord(recordPath, next.with(prev))
		return err
	}
	return atStage(homeStageRecord, writeHomeRecord(recordPath, next))
}

// homePlan is what a home should hold besides the servers of its
// .claude.json.
func (s *Service) homePlan(ctx context.Context, base homeBase, items []domain.LadoSetupItemWithVersion, createMissing bool) (map[string]*homeNode, error) {
	plan := map[string]*homeNode{}
	for _, name := range append(slices.Clone(claudeStateDirs), claudeStateFile) {
		target := filepath.Join(base.dir, name)
		err := ensureBaseEntry(target, name == claudeStateFile, createMissing)
		switch {
		case errors.Is(err, fs.ErrNotExist) && !createMissing:
			continue
		case err != nil:
			return nil, atStage(homeStageBase, err)
		}
		plan[name] = &homeNode{link: target}
	}
	entries, err := os.ReadDir(base.dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, atStage(homeStageBase, err)
	}
	for _, d := range entries {
		if isMarkdown(d.Name()) && !d.IsDir() {
			plan[d.Name()] = &homeNode{link: filepath.Join(base.dir, d.Name())}
		}
	}
	for _, folder := range claudeItemDirs {
		plan[folder.name] = &homeNode{dir: baseLinks(filepath.Join(base.dir, folder.name))}
	}
	var settings []domain.LadoSetupItemWithVersion
	for _, iv := range items {
		switch iv.Item.Kind {
		case domain.LadoSetupKindSetting:
			settings = append(settings, iv)
			continue
		case domain.LadoSetupKindMCPServer:
			continue
		}
		folder, at, err := itemPlace(plan, iv.Item)
		if err != nil {
			return nil, atStage(homeStageContent, err)
		}
		node, err := s.itemNode(ctx, iv)
		if err != nil {
			return nil, atStage(homeStageContent, err)
		}
		put(folder, at, node)
	}
	data, err := s.homeSettings(base, settings)
	if err != nil {
		return nil, atStage(homeStageSettings, err)
	}
	if data != nil {
		plan[claudeSettings] = &homeNode{file: &homeFile{data: data, mode: privateFile}, lado: true}
	}
	return plan, nil
}

// ensureBaseEntry makes sure the base holds a state entry, creating it empty,
// a folder or else a file, when it has none and create is set. A missing
// entry it may not create is fs.ErrNotExist.
func ensureBaseEntry(p string, file, create bool) error {
	if _, err := os.Lstat(p); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !create {
		return fs.ErrNotExist
	}
	if !file {
		return os.MkdirAll(p, privateDir)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, privateFile)
	if err != nil {
		return err
	}
	return f.Close()
}

// baseLinks is a link per entry of the base folder dir; none when the base
// has no such folder.
func baseLinks(dir string) map[string]*homeNode {
	links := map[string]*homeNode{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return links
	}
	for _, d := range entries {
		links[d.Name()] = &homeNode{link: filepath.Join(dir, d.Name())}
	}
	return links
}

// itemPlace finds the folder a file or folder item goes in and its path
// under it: the home itself for instructions, else its item folder. A name
// that would leave the folder is refused.
func itemPlace(plan map[string]*homeNode, it domain.LadoSetupItem) (*homeNode, []string, error) {
	segments := strings.Split(it.Name, "/")
	if slices.ContainsFunc(segments, func(seg string) bool {
		return seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "\\\x00")
	}) {
		return nil, nil, fmt.Errorf("the %s name %q leaves its folder", it.Kind, it.Name)
	}
	if it.Kind == domain.LadoSetupKindInstructions {
		if len(segments) != 1 || !isMarkdown(it.Name) {
			return nil, nil, fmt.Errorf("the instructions name %q is not a Markdown file name", it.Name)
		}
		return &homeNode{dir: plan}, segments, nil
	}
	for _, folder := range claudeItemDirs {
		if folder.kind != it.Kind {
			continue
		}
		if !folder.nested && len(segments) != 1 {
			return nil, nil, fmt.Errorf("the %s name %q is not an entry of %s/", it.Kind, it.Name, folder.name)
		}
		return plan[folder.name], segments, nil
	}
	return nil, nil, fmt.Errorf("a %s item has no place in a config home", it.Kind)
}

// put places node at path under folder, making real folders on the way. A
// base folder linked on the way opens up into links of its own entries, and
// anything else there gives way: a managed item wins a name clash.
func put(folder *homeNode, at []string, node *homeNode) {
	for _, seg := range at[:len(at)-1] {
		next := folder.dir[seg]
		if next == nil || next.dir == nil {
			opened := &homeNode{dir: map[string]*homeNode{}}
			if next != nil && next.link != "" {
				opened.dir = baseLinks(next.link)
			}
			folder.dir[seg] = opened
			next = opened
		}
		folder = next
	}
	folder.dir[at[len(at)-1]] = node
}

// itemNode is how a home holds a file or folder item version: a copy of the
// file, or a link to the folder's tree.
func (s *Service) itemNode(ctx context.Context, iv domain.LadoSetupItemWithVersion) (*homeNode, error) {
	v := iv.Version
	switch v.ContentKind {
	case domain.LadoSetupContentFile:
		data, err := s.deps.Blobs.Get(v.SHA256, maxBlobBytes)
		if err != nil {
			return nil, fmt.Errorf("read %s from its blob: %w", iv.Item.Name, err)
		}
		mode := privateFile
		if v.Executable {
			mode = privateExec
		}
		return &homeNode{file: &homeFile{data: data, mode: mode}}, nil
	case domain.LadoSetupContentTree:
		dir, err := s.materializeTree(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("make the tree of %s: %w", iv.Item.Name, err)
		}
		return &homeNode{link: dir}, nil
	}
	return nil, fmt.Errorf("a %s item cannot hold %s content", iv.Item.Kind, v.ContentKind)
}

// materializeTree makes a folder item version's files once, at
// trees/<sha256>/, where every home that holds the version links. A tree that
// no longer hashes to its version, as when a session wrote through its link,
// is made again.
func (s *Service) materializeTree(ctx context.Context, v domain.LadoSetupItemVersion) (string, error) {
	if !treeFolderName.MatchString(v.SHA256) {
		return "", fmt.Errorf("the version sha256 %q names no folder", v.SHA256)
	}
	root := treesRoot(s.deps.StateDir)
	dir := filepath.Join(root, v.SHA256)
	if treeHolds(ctx, dir, v.SHA256) {
		return dir, nil
	}
	tree, err := s.treeOfVersion(v)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, privateDir); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(root, "."+v.SHA256+".lado-")
	if err != nil {
		return "", err
	}
	// Gone after the rename below.
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := s.writeTreeFiles(tmp, tree); err != nil {
		return "", err
	}
	if _, err := os.Lstat(dir); err == nil {
		// A tree that failed its check goes aside; the homes keep their links.
		aside := filepath.Join(root, "."+v.SHA256+".lado-stale-"+s.deps.NewID())
		if err := os.Rename(dir, aside); err != nil {
			return "", err
		}
		defer func() { _ = os.RemoveAll(aside) }()
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Another launch made it in the meantime.
		if treeHolds(ctx, dir, v.SHA256) {
			return dir, nil
		}
		return "", err
	}
	return dir, nil
}

// treeHolds reports whether the folder at dir hashes to the tree sum.
func treeHolds(ctx context.Context, dir, sum string) bool {
	tree, err := treeOf(ctx, dir)
	if err != nil {
		return false
	}
	got, err := tree.SHA256()
	return err == nil && got == sum
}

// homeSettings is a home's settings.json: the base's file as it is, login
// keys included, with each managed setting's key set to its version. The
// managed env keeps the login variables of the base's env, which adoption
// left in place. nil means the home has none.
func (s *Service) homeSettings(base homeBase, settings []domain.LadoSetupItemWithVersion) ([]byte, error) {
	data, err := readRegular(filepath.Join(base.dir, claudeSettings))
	if errors.Is(err, fs.ErrNotExist) {
		data, err = nil, nil
	}
	if err != nil || len(settings) == 0 {
		return data, err
	}
	if _, err := jsonMembers(data); err != nil {
		s.deps.Log.Warn("LADO gives a Claude Code session home the managed settings only: the base settings.json is not a JSON object", "err", err)
		data = nil
	}
	for _, iv := range settings {
		content, err := s.deps.Blobs.Get(iv.Version.SHA256, maxBlobBytes)
		if err != nil {
			return nil, fmt.Errorf("read the setting %s from its blob: %w", iv.Item.Name, err)
		}
		if iv.Item.Name == claudeEnvKey {
			content = withLoginEnvOf(content, data)
		}
		if data, err = jsonWith(data, iv.Item.Name, content); err != nil {
			return nil, err
		}
	}
	return data, nil
}

// withLoginEnvOf returns a managed env value with the login variables of the
// env in settings that it does not set itself. A value that is no object
// comes back as it is.
func withLoginEnvOf(content, settings []byte) []byte {
	if trimmed := bytes.TrimSpace(content); len(trimmed) == 0 || trimmed[0] != '{' {
		return content
	}
	vars, err := jsonMembers(content)
	if err != nil {
		return content
	}
	baseEnv, ok, err := jsonMemberValue(settings, claudeEnvKey)
	if err != nil || !ok {
		return content
	}
	baseVars, err := jsonMembers(baseEnv)
	if err != nil {
		return content
	}
	added := false
	for _, m := range baseVars {
		if slices.Contains(domain.LadoSetupClaudeLoginEnv, m.Key) && !slices.ContainsFunc(vars, func(v jsonMember) bool { return v.Key == m.Key }) {
			vars, added = append(vars, m), true
		}
	}
	if !added {
		return content
	}
	out, err := formatJSONObject(vars, false)
	if err != nil {
		return content
	}
	return out
}

// place makes the folder dir hold what plan wants and records under rel what
// LADO placed. An entry LADO did not place, which Claude or the developer
// made, stays: only a link, which loses nothing, and an empty folder give
// way.
func (s *Service) place(dir, rel string, plan map[string]*homeNode, prev, next *homeRecord) error {
	for _, name := range sortedKeys(plan) {
		node := plan[name]
		key := path.Join(rel, name)
		p := filepath.Join(dir, name)
		info, err := os.Lstat(p)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		exists := err == nil
		isLink := exists && info.Mode()&fs.ModeSymlink != 0
		isDir := exists && info.IsDir()
		yields := !exists || isLink || node.lado || prev.has(key) || (isDir && emptyFolder(p))
		switch {
		case node.link != "":
			if isLink {
				if target, err := os.Readlink(p); err == nil && target == node.link {
					next.Entries[key] = placedLink
					continue
				}
			}
			if !yields {
				s.leftInHome(key)
				continue
			}
			if exists {
				if err := os.RemoveAll(p); err != nil {
					return err
				}
			}
			if err := os.Symlink(node.link, p); err != nil {
				return err
			}
			next.Entries[key] = placedLink
		case node.file != nil:
			if exists && info.Mode().IsRegular() && holdsFile(p, info, node.file) {
				next.Entries[key] = placedFile
				continue
			}
			if !yields {
				s.leftInHome(key)
				continue
			}
			if isDir {
				if err := os.RemoveAll(p); err != nil {
					return err
				}
			}
			if err := atomicWrite(p, node.file.data, node.file.mode, os.Rename); err != nil {
				return err
			}
			next.Entries[key] = placedFile
		default:
			if !isDir {
				if exists && !yields {
					s.leftInHome(key)
					continue
				}
				if exists {
					if err := os.RemoveAll(p); err != nil {
						return err
					}
				}
				if err := os.Mkdir(p, privateDir); err != nil {
					return err
				}
			}
			next.Entries[key] = placedFolder
			if err := s.place(p, key, node.dir, prev, next); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) leftInHome(key string) {
	s.deps.Log.Warn("LADO leaves an entry it did not place in a Claude Code session home", "entry", key)
}

// holdsFile reports whether the regular file at p already holds f.
func holdsFile(p string, info fs.FileInfo, f *homeFile) bool {
	if runtime.GOOS != "windows" && info.Mode().Perm() != f.mode {
		return false
	}
	data, err := readRegularNoLink(p)
	return err == nil && bytes.Equal(data, f.data)
}

func emptyFolder(p string) bool {
	entries, err := os.ReadDir(p)
	return err == nil && len(entries) == 0
}

// removeStale takes away what LADO placed in a home before and no longer
// wants: links, which are never followed, files, and folders LADO made once
// they are empty. An entry under a link, which now stands where LADO's folder
// was, is not the home's and stays.
func removeStale(home string, prev, next *homeRecord) error {
	var stale []string
	for key := range prev.Entries {
		if _, ok := next.Entries[key]; !ok {
			stale = append(stale, key)
		}
	}
	// Deepest first, so a folder has lost LADO's entries by its turn.
	slices.SortFunc(stale, func(a, b string) int {
		return cmp.Or(cmp.Compare(strings.Count(b, "/"), strings.Count(a, "/")), cmp.Compare(a, b))
	})
	for _, key := range stale {
		if !inRealFolders(home, key) {
			continue
		}
		p := filepath.Join(home, filepath.FromSlash(key))
		info, err := os.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return err
		case info.IsDir():
			// A folder Claude keeps something in stays.
			_ = os.Remove(p)
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// inRealFolders reports whether every folder between home and the entry key
// is a real folder and no link.
func inRealFolders(home, key string) bool {
	segments := strings.Split(key, "/")
	p := home
	for _, seg := range segments[:len(segments)-1] {
		p = filepath.Join(p, seg)
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// reconcileConfig keeps the home's .claude.json, a copy of the base's that
// Claude changes as it runs, and sets its mcpServers: the base's servers and
// the managed ones, which win a name clash. A server LADO put there before and
// no longer delivers goes; one the session added stays. A copy that is
// missing or is no JSON object is taken from the base again.
func (s *Service) reconcileConfig(home string, base homeBase, items []domain.LadoSetupItemWithVersion, prev, next *homeRecord) error {
	p := filepath.Join(home, claudeConfigName)
	current, err := readRegularNoLink(p)
	if err != nil || !isJSONObject(current) {
		if err == nil || !errors.Is(err, fs.ErrNotExist) {
			s.deps.Log.Warn("LADO copies a Claude Code session home's .claude.json from the base again: it is no JSON object", "err", err)
		}
		current = nil
	}
	baseData, err := readBaseConfig(base.config)
	if err != nil {
		if current == nil {
			return err
		}
		// The home's copy stands until the base can be read again.
		s.deps.Log.Warn("LADO keeps the MCP servers of a Claude Code session home as they are: the base .claude.json cannot be read now", "err", err)
		next.Servers = prev.Servers
		return nil
	}
	servers, err := s.homeServers(baseData, items)
	if err != nil {
		return err
	}
	data := current
	if data == nil {
		data = baseData
	}
	wanted := map[string]bool{}
	for _, m := range servers {
		wanted[m.Key] = true
	}
	for _, name := range prev.Servers {
		if !wanted[name] {
			if data, err = withoutServer(data, name); err != nil {
				return err
			}
		}
	}
	for _, m := range servers {
		if data, err = jsonWithMCPServer(data, m.Key, m.Value); err != nil {
			return err
		}
	}
	next.Servers = sortedKeys(wanted)
	if data == nil || (current != nil && bytes.Equal(data, current)) {
		return nil
	}
	return atomicWrite(p, data, privateFile, os.Rename)
}

// readBaseConfig reads the base's .claude.json, through a link as Claude
// does. A base without one is empty. One that is no JSON object, which Claude
// can leave for a moment as it rewrites the file, is an error rather than a
// base without servers.
func readBaseConfig(p string) ([]byte, error) {
	if p == "" {
		return nil, nil
	}
	data, err := readRegular(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !isJSONObject(data) {
		return nil, errors.New("the base .claude.json is no JSON object")
	}
	return data, nil
}

func isJSONObject(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	_, err := jsonMembers(data)
	return err == nil
}

// homeServers are the MCP servers a home's .claude.json holds: the base's in
// their order, then the managed ones, each in the place of a base server of
// its name.
func (s *Service) homeServers(baseData []byte, items []domain.LadoSetupItemWithVersion) ([]jsonMember, error) {
	var servers []jsonMember
	value, ok, err := jsonMemberValue(baseData, claudeMCPKey)
	if err != nil {
		return nil, err
	}
	if ok {
		if servers, err = jsonMembers(value); err != nil {
			return nil, fmt.Errorf("the base's %s: %w", claudeMCPKey, err)
		}
	}
	for _, iv := range items {
		if iv.Item.Kind != domain.LadoSetupKindMCPServer {
			continue
		}
		content, err := s.deps.Blobs.Get(iv.Version.SHA256, maxBlobBytes)
		if err != nil {
			return nil, fmt.Errorf("read the MCP server %s from its blob: %w", iv.Item.Name, err)
		}
		m := jsonMember{Key: iv.Item.Name, Value: content}
		if i := slices.IndexFunc(servers, func(b jsonMember) bool { return b.Key == m.Key }); i >= 0 {
			servers[i] = m
		} else {
			servers = append(servers, m)
		}
	}
	return servers, nil
}

// withoutServer returns data without the MCP server name; data without it
// comes back as it was.
func withoutServer(data []byte, name string) ([]byte, error) {
	servers, ok, err := jsonMemberValue(data, claudeMCPKey)
	if err != nil || !ok {
		return data, err
	}
	if _, has, err := jsonMemberValue(servers, name); err != nil || !has {
		return data, err
	}
	return jsonWithoutMCPServer(data, name)
}

// Kinds of entries a home record names.
const (
	placedLink   = "link"
	placedFile   = "file"
	placedFolder = "dir"
)

// homeRecord names what LADO placed in a home: entries by their slash path
// under it, with their kind, and the MCP servers of its .claude.json.
type homeRecord struct {
	Format  string            `json:"format"`
	Entries map[string]string `json:"entries"`
	Servers []string          `json:"servers"`
}

func (r *homeRecord) has(key string) bool {
	_, ok := r.Entries[key]
	return ok
}

// with is r with what prev names and r does not.
func (r *homeRecord) with(prev *homeRecord) *homeRecord {
	out := &homeRecord{Format: homeRecordFormat, Entries: map[string]string{}}
	for key, kind := range prev.Entries {
		out.Entries[key] = kind
	}
	for key, kind := range r.Entries {
		out.Entries[key] = kind
	}
	out.Servers = slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(prev.Servers), r.Servers...))))
	return out
}

// readHomeRecord reads the record beside a home. A missing or unreadable one
// names nothing.
func readHomeRecord(p string) *homeRecord {
	r := &homeRecord{Format: homeRecordFormat, Entries: map[string]string{}}
	data, err := readRegularNoLink(p)
	if err != nil {
		return r
	}
	var got homeRecord
	if json.Unmarshal(data, &got) != nil || got.Format != homeRecordFormat {
		return r
	}
	if got.Entries != nil {
		r.Entries = got.Entries
	}
	r.Servers = got.Servers
	return r
}

func writeHomeRecord(p string, r *homeRecord) error {
	if r.Servers == nil {
		r.Servers = []string{}
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(p, append(data, '\n'), privateFile, os.Rename)
}

// CleanupHomes removes the config homes of sessions that ended or no longer
// exist (plan §9). os.RemoveAll removes a home's links without following
// them, so what they point at stays. A home prepared within homeGrace stays
// even when its session reads as ended, since a restore builds the home
// before it marks the session live again. The daemon calls it when it starts
// and after scans.
func (s *Service) CleanupHomes(ctx context.Context) error {
	if s.deps.StateDir == "" {
		return nil
	}
	root := homesRoot(s.deps.StateDir)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.deps.Sessions == nil {
		return errors.New("LADO setup cannot list AO's sessions")
	}
	records, err := s.deps.Sessions.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("list AO's sessions: %w", err)
	}
	live := map[domain.SessionID]bool{}
	for _, rec := range records {
		if !rec.IsTerminated {
			live[rec.ID] = true
		}
	}
	s.homesMu.Lock()
	defer s.homesMu.Unlock()
	now := s.deps.Clock()
	var errs []error
	for _, d := range entries {
		id := domain.SessionID(d.Name())
		if live[id] || now.Sub(s.homesPrepared[id]) < homeGrace {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, d.Name())); err != nil {
			errs = append(errs, err)
			continue
		}
		delete(s.homesPrepared, id)
	}
	return errors.Join(errs...)
}

// verifyHome builds a config home for the computer's own login in a folder
// of its own and checks that each adopted Claude Code item is there with its
// version's content (plan §7.7). It creates nothing in the Claude folder and
// removes the folder when done.
func (s *Service) verifyHome(ctx context.Context, adopted []domain.LadoSetupItemWithVersion) []VerifyCheck {
	home, cleanup, err := s.probeHome(ctx)
	defer cleanup()
	checks := make([]VerifyCheck, 0, len(adopted))
	for _, iv := range adopted {
		check := VerifyCheck{ItemID: iv.Item.ID, Check: CheckHome, OK: true}
		failed := err
		if failed == nil {
			failed = checkHomeItem(ctx, home, iv)
		}
		if failed != nil {
			check.OK, check.Error = false, plainError(failed)
		}
		checks = append(checks, check)
	}
	return checks
}

// probeHome builds, in a new folder under the state dir, the home that a
// session on the computer's own login outside any project gets. cleanup
// removes the folder.
func (s *Service) probeHome(ctx context.Context) (string, func(), error) {
	parent := filepath.Join(s.deps.StateDir, "lado")
	if err := os.MkdirAll(parent, privateDir); err != nil {
		return "", func() {}, err
	}
	root, err := os.MkdirTemp(parent, "verify-home-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	items, _, err := s.sessionItems(ctx, domain.LadoSetupToolClaudeCode, "", "")
	if err != nil {
		return "", cleanup, err
	}
	base, _, err := s.homeBase(nil)
	if err != nil {
		return "", cleanup, err
	}
	home := filepath.Join(root, "claude")
	return home, cleanup, s.buildHome(ctx, home, base, items, false)
}

// checkHomeItem compares an item in a home with its version: its key's
// content in settings.json or .claude.json, its file's bytes or its folder's
// tree.
func checkHomeItem(ctx context.Context, home string, iv domain.LadoSetupItemWithVersion) error {
	it := iv.Item
	var sum string
	switch it.Kind {
	case domain.LadoSetupKindSetting, domain.LadoSetupKindMCPServer:
		file := claudeSettings
		if it.Kind == domain.LadoSetupKindMCPServer {
			file = claudeConfigName
		}
		data, err := readRegularNoLink(filepath.Join(home, file))
		if err != nil {
			return err
		}
		content, err := keyValue(it.Tool, it.Kind, it.Name, data)
		if err != nil {
			return err
		}
		sum = sha256Hex(content)
	default:
		p, err := homeItemPath(home, it)
		if err != nil {
			return err
		}
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			return err
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		if info.IsDir() {
			tree, err := treeOf(ctx, resolved)
			if err != nil {
				return err
			}
			if sum, err = tree.SHA256(); err != nil {
				return err
			}
		} else if _, sum, err = fileSum(resolved); err != nil {
			return err
		}
	}
	if sum != iv.Version.SHA256 {
		return fmt.Errorf("the home holds %s, not %s", sum, iv.Version.SHA256)
	}
	return nil
}

// homeItemPath is where a home holds a file or folder item.
func homeItemPath(home string, it domain.LadoSetupItem) (string, error) {
	if it.Kind == domain.LadoSetupKindInstructions {
		return filepath.Join(home, it.Name), nil
	}
	for _, folder := range claudeItemDirs {
		if folder.kind == it.Kind {
			return filepath.Join(home, folder.name, filepath.FromSlash(it.Name)), nil
		}
	}
	return "", fmt.Errorf("a %s item has no place in a config home", it.Kind)
}
