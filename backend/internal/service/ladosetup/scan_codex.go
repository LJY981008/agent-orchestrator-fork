package ladosetup

// The Codex part of the inventory (plan decisions d and e): the Codex folder.
// LADO delivers Codex setup through launch options on the real CODEX_HOME, so
// only AGENTS.md, AGENTS.override.md and MCP servers it can cut out of
// config.toml and put on a command line are adoptable.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	codexConfig     = "config.toml"
	codexServersKey = "mcp_servers"
)

// codexInstructions are the Codex folder's instruction files.
var codexInstructions = []string{"AGENTS.md", "AGENTS.override.md"}

// codexAppState are entries of the Codex folder that Codex writes as it runs.
// Every name that starts with a dot or holds ".sqlite" (a database or its
// journal) is app state as well.
var codexAppState = []string{
	"sessions", "archived_sessions", "history.jsonl", "session_index.jsonl", "log", "logs",
	"cache", "models_cache.json", "shell_snapshots", "tmp", "installation_id", "version.json",
}

// codexToolState are config.toml keys Codex writes itself.
var codexToolState = []string{"projects", "notice", "hooks", "skills"}

// codexPerSession are config.toml keys AO sets for each session.
var codexPerSession = []string{"model", "model_reasoning_effort"}

// codexServerName is the MCP server name a -c option can carry without
// quoting.
var codexServerName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// codexServerHeader matches the [mcp_servers.<name>] header line of a server
// written as a table, the block adoption cuts out of config.toml. name passes
// codexServerName.
func codexServerHeader(name string) *regexp.Regexp {
	segment := func(s string) string {
		q := regexp.QuoteMeta(s)
		return `(?:` + q + `|"` + q + `"|'` + q + `')`
	}
	return regexp.MustCompile(`(?m)^[ \t]*\[[ \t]*` + segment(codexServersKey) + `[ \t]*\.[ \t]*` + segment(name) + `[ \t]*\][ \t]*(?:#[^\r\n]*)?\r?$`)
}

// codexHome lists the Codex folder.
func (sc *scanner) codexHome(ctx context.Context) {
	dir := sc.deps.CodexHome
	root := Root{Tool: domain.LadoSetupToolCodex, Location: domain.LadoSetupLocationCodexHome, Path: dir}
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
		sc.codexEntry(ctx, dir, d)
	}
}

func (sc *scanner) codexEntry(ctx context.Context, dir string, d fs.DirEntry) {
	name, typ := d.Name(), d.Type()
	e := domain.LadoSetupEntry{Tool: domain.LadoSetupToolCodex, Name: name, Location: domain.LadoSetupLocationCodexHome, Path: filepath.Join(dir, name)}
	switch {
	case slices.Contains(codexInstructions, name) && !typ.IsDir():
		e.Kind = domain.LadoSetupKindInstructions
		sc.item(ctx, e, typ)
	case name == codexConfig:
		sc.codexConfig(e, typ)
	case name == "skills" || name == "plugins":
		sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonNoTUIPath)
	case name == "prompts":
		sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonDeprecated)
	case name == "rules":
		sc.add(e, domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonToolState)
	case name == "auth.json" || name == "secrets":
		sc.add(e, domain.LadoSetupStatusNever, domain.LadoSetupReasonCredentials)
	case slices.Contains(codexAppState, name) || strings.HasPrefix(name, ".") || strings.Contains(name, ".sqlite"):
		sc.add(e, domain.LadoSetupStatusNever, domain.LadoSetupReasonAppState)
	default:
		sc.add(e, domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonUnknownLocation)
	}
}

// codexConfig lists the top-level keys of config.toml, one line per MCP
// server. A link is read through, but its servers are not adopted: adoption
// rewrites the file.
func (sc *scanner) codexConfig(file domain.LadoSetupEntry, typ fs.FileMode) {
	data, err := os.ReadFile(file.Path)
	if err != nil {
		sc.unreadable(file, err)
		return
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		sc.unreadable(file, err)
		return
	}
	for _, key := range sortedKeys(doc) {
		e := domain.LadoSetupEntry{Tool: domain.LadoSetupToolCodex, Name: key, Location: domain.LadoSetupLocationCodexHome, Path: file.Path + "#" + key}
		switch {
		case key == codexServersKey:
			sc.codexServers(e, data, doc[key], typ&fs.ModeSymlink != 0)
		case slices.Contains(codexToolState, key):
			sc.add(e, domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonToolState)
		case slices.Contains(codexPerSession, key):
			sc.add(e, domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonAOSetsPerSession)
		default:
			sc.add(e, domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonUnknownLocation)
		}
	}
}

// codexServers lists the MCP servers of config.toml. A server is adoptable
// when its name needs no quoting, it is written as a [mcp_servers.<name>]
// table, and it holds no secret a -c option would put on a command line.
func (sc *scanner) codexServers(key domain.LadoSetupEntry, data []byte, value any, linked bool) {
	servers, ok := value.(map[string]any)
	if !ok {
		sc.add(key, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonUnreadable)
		return
	}
	for _, name := range sortedKeys(servers) {
		e := domain.LadoSetupEntry{Tool: domain.LadoSetupToolCodex, Kind: domain.LadoSetupKindMCPServer, Name: name, Location: domain.LadoSetupLocationCodexHome, Path: key.Path + "." + name}
		table, isTable := servers[name].(map[string]any)
		switch {
		case !codexServerName.MatchString(name):
			sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonNameUnsupported)
		case !isTable || !codexServerHeader(name).Match(data):
			sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonInlineTable)
		case literalCredentials(table):
			sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonLiteralCredentials)
		case linked:
			sc.add(e, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
		default:
			content, err := marshalCanonical(table)
			if err != nil {
				sc.unreadable(e, err)
				continue
			}
			sc.adoptable(e, int64(len(content)), sha256Hex(content))
		}
	}
}

// literalCredentials reports whether a Codex MCP server holds secret values:
// env with values, http_headers or bearer_token. Their environment variable
// forms (env_vars, env_http_headers, bearer_token_env_var) hold names only.
func literalCredentials(server map[string]any) bool {
	if env, ok := server["env"]; ok {
		if vars, isTable := env.(map[string]any); !isTable || len(vars) > 0 {
			return true
		}
	}
	_, headers := server["http_headers"]
	_, token := server["bearer_token"]
	return headers || token
}
