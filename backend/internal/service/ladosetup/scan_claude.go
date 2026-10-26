package ladosetup

// The Claude Code part of the inventory (plan decision e): the Claude folder
// and ~/.claude.json. Top-level Markdown, settings keys, the entries of the
// five item folders and the user MCP servers are adoptable; login keys,
// credentials, app state and plugins/ are never adopted; anything else is
// left to Claude.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// claudeItemDirs are the Claude folder's item folders and the kind of their
// entries. The entries of agents/ and commands/ are files at any depth, named
// by their path under the folder.
var claudeItemDirs = []struct {
	name   string
	kind   domain.LadoSetupKind
	nested bool
}{
	{"skills", domain.LadoSetupKindSkill, false},
	{"agents", domain.LadoSetupKindAgent, true},
	{"commands", domain.LadoSetupKindCommand, true},
	{"output-styles", domain.LadoSetupKindOutputStyle, false},
	{"hooks", domain.LadoSetupKindHookScript, false},
}

// claudeAppState are entries of the Claude folder that Claude writes as it
// runs. Every entry whose name starts with a dot is app state as well.
var claudeAppState = []string{
	"projects", "sessions", "history.jsonl", "todos", "file-history", "shell-snapshots", "session-env",
	"paste-cache", "tasks", "plans", "transcripts", "statsig", "ide", "debug", "telemetry", "cache",
	"downloads", "backups", "stats-cache.json", "mcp-needs-auth-cache.json",
}

const (
	claudeCredentials = ".credentials.json"
	claudeSettings    = "settings.json"
	claudeEnvKey      = "env"
	claudeMCPKey      = "mcpServers"
)

// claudeDir lists the Claude folder.
func (sc *scanner) claudeDir(ctx context.Context) {
	dir := sc.deps.ClaudeDir
	root := Root{Tool: domain.LadoSetupToolClaudeCode, Location: domain.LadoSetupLocationClaudeDir, Path: dir}
	defer func() { sc.roots = append(sc.roots, root) }()
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return
	case err != nil:
		root.Error = err.Error()
		return
	}
	root.Found = true
	for _, d := range entries {
		sc.claudeEntry(ctx, dir, d)
	}
}

func (sc *scanner) claudeEntry(ctx context.Context, dir string, d fs.DirEntry) {
	name, typ := d.Name(), d.Type()
	path := filepath.Join(dir, name)
	e := domain.LadoSetupEntry{Tool: domain.LadoSetupToolClaudeCode, Name: name, Location: domain.LadoSetupLocationClaudeDir, Path: path}
	if sc.deps.ClaudeJSON != "" && filepath.Clean(path) == filepath.Clean(sc.deps.ClaudeJSON) {
		// Claude's global config inside $CLAUDE_CONFIG_DIR, read as itself.
		return
	}
	for _, items := range claudeItemDirs {
		if name != items.name {
			continue
		}
		switch {
		case typ.IsDir():
			sc.claudeItems(ctx, dir, name, items.kind, items.nested)
		case typ&fs.ModeSymlink != 0:
			sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
		default:
			sc.add(e, domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonUnknownLocation)
		}
		return
	}
	switch {
	case name == claudeCredentials:
		sc.add(e, domain.LadoSetupStatusNever, domain.LadoSetupReasonCredentials)
	case name == claudeSettings:
		sc.claudeSettings(e, typ)
	case name == "plugins":
		sc.add(e, domain.LadoSetupStatusNever, domain.LadoSetupReasonPluginsStay)
	case slices.Contains(claudeAppState, name) || strings.HasPrefix(name, "."):
		sc.add(e, domain.LadoSetupStatusNever, domain.LadoSetupReasonAppState)
	case isMarkdown(name) && !typ.IsDir():
		e.Kind = domain.LadoSetupKindInstructions
		sc.item(ctx, e, typ)
	default:
		sc.add(e, domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonUnknownLocation)
	}
}

// claudeItems lists the entries of one item folder. A name starting with a
// dot, such as .DS_Store or .git, is no item; its line is named by its path
// under the Claude folder, and a folder of that kind is not read.
func (sc *scanner) claudeItems(ctx context.Context, claudeDir, folder string, kind domain.LadoSetupKind, nested bool) {
	base := filepath.Join(claudeDir, folder)
	// The walk stops only when the callback fails, and it never does.
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		// line is what is at path, named by its path under the Claude folder.
		line := domain.LadoSetupEntry{Tool: domain.LadoSetupToolClaudeCode, Name: slashRel(claudeDir, path), Location: domain.LadoSetupLocationClaudeDir, Path: path}
		switch {
		case err != nil:
			// A folder that cannot be listed.
			sc.unreadable(line, err)
			return nil
		case path == base:
			return nil
		case strings.HasPrefix(d.Name(), "."):
			sc.add(line, domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonUnknownLocation)
			return skipDir(d)
		case nested && d.IsDir():
			return nil
		}
		sc.item(ctx, domain.LadoSetupEntry{Tool: domain.LadoSetupToolClaudeCode, Kind: kind, Name: slashRel(base, path), Location: domain.LadoSetupLocationClaudeDir, Path: path}, d.Type())
		return skipDir(d)
	})
}

// slashRel is path relative to dir with slashes. path is inside dir.
func slashRel(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// skipDir keeps a walk out of a folder it has listed as one entry.
func skipDir(d fs.DirEntry) error {
	if d.IsDir() {
		return fs.SkipDir
	}
	return nil
}

// claudeSettings lists the keys of settings.json. A link is read through,
// but its keys are not adopted: adoption rewrites the file.
func (sc *scanner) claudeSettings(file domain.LadoSetupEntry, typ fs.FileMode) {
	data, err := os.ReadFile(file.Path)
	if err != nil {
		sc.unreadable(file, err)
		return
	}
	keys, err := jsonObject(data)
	if err != nil {
		sc.unreadable(file, err)
		return
	}
	for _, key := range sortedKeys(keys) {
		e := domain.LadoSetupEntry{Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: key, Location: domain.LadoSetupLocationClaudeDir, Path: file.Path + "#" + key}
		value := keys[key]
		if slices.Contains(domain.LadoSetupClaudeLoginKeys, key) {
			sc.add(e, domain.LadoSetupStatusNever, domain.LadoSetupReasonLoginKey)
			continue
		}
		if key == claudeEnvKey {
			if value, e.KeptInPlace, err = withoutLoginEnv(value); err != nil {
				sc.unreadable(e, err)
				continue
			}
			if value == nil {
				// Nothing but login variables, which stay.
				sc.add(e, domain.LadoSetupStatusNever, domain.LadoSetupReasonLoginKey)
				continue
			}
		}
		if typ&fs.ModeSymlink != 0 {
			sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
			continue
		}
		sc.jsonItem(e, value)
	}
}

// withoutLoginEnv takes the login variables (domain.LadoSetupClaudeLoginEnv)
// out of the env setting's value, which stay in settings.json when the rest
// is adopted, and names them. A value with nothing else is nil; a value that
// is no object is kept as it is.
func withoutLoginEnv(value json.RawMessage) (json.RawMessage, []string, error) {
	if trimmed := bytes.TrimSpace(value); len(trimmed) == 0 || trimmed[0] != '{' {
		// Not an object: adopted as it is, with no login variable in it.
		return value, nil, nil
	}
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(value, &vars); err != nil {
		return nil, nil, err
	}
	var kept []string
	for _, name := range domain.LadoSetupClaudeLoginEnv {
		if _, ok := vars[name]; ok {
			kept = append(kept, name)
			delete(vars, name)
		}
	}
	if len(kept) == 0 {
		return value, nil, nil
	}
	if len(vars) == 0 {
		return nil, kept, nil
	}
	rest, err := json.Marshal(vars)
	return rest, kept, err
}

// claudeJSON lists the user MCP servers of ~/.claude.json and one line for
// the rest, which is Claude's state.
func (sc *scanner) claudeJSON() {
	path := sc.deps.ClaudeJSON
	root := Root{Tool: domain.LadoSetupToolClaudeCode, Location: domain.LadoSetupLocationClaudeJSON, Path: path}
	defer func() { sc.roots = append(sc.roots, root) }()
	if path == "" {
		return
	}
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return
	case err != nil:
		root.Error = err.Error()
		return
	}
	root.Found = true
	data, err := os.ReadFile(path)
	if err != nil {
		root.Error = err.Error()
		return
	}
	keys, err := jsonObject(data)
	if err != nil {
		root.Error = err.Error()
		return
	}
	others := len(keys)
	if value, ok := keys[claudeMCPKey]; ok {
		sc.claudeServers(path, value, info.Mode()&fs.ModeSymlink != 0)
		others--
	}
	if others > 0 {
		sc.add(domain.LadoSetupEntry{Tool: domain.LadoSetupToolClaudeCode, Name: filepath.Base(path), Location: domain.LadoSetupLocationClaudeJSON, Path: path},
			domain.LadoSetupStatusNever, domain.LadoSetupReasonAppState)
	}
}

func (sc *scanner) claudeServers(path string, value json.RawMessage, linked bool) {
	servers, err := jsonObject(value)
	if err != nil {
		sc.unreadable(domain.LadoSetupEntry{Tool: domain.LadoSetupToolClaudeCode, Name: claudeMCPKey, Location: domain.LadoSetupLocationClaudeJSON, Path: path + "#" + claudeMCPKey}, err)
		return
	}
	for _, name := range sortedKeys(servers) {
		e := domain.LadoSetupEntry{Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindMCPServer, Name: name, Location: domain.LadoSetupLocationClaudeJSON, Path: path + "#" + claudeMCPKey + "." + name}
		if linked {
			sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
			continue
		}
		sc.jsonItem(e, servers[name])
	}
}
