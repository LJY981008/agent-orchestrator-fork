package ladosetup

// The AO system layer (plan §6): what AO adds to every session it launches,
// whatever the developer's setup. A rule snapshot records its hash as
// aoSystem, so an AO update that changes the layer asks for review again, as
// a changed rule does. The inputs come from the code that installs them.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

// aoMCPServerName and aoMCPServerArgs are the tool server every Chat session
// gets, `ao mcp` (session_manager's aoMCPServers).
const aoMCPServerName = "ao"

var aoMCPServerArgs = []string{"mcp"}

// AOSystem is the layer AO adds to every session. Its canonical JSON is the
// fields in this order, the events sorted.
type AOSystem struct {
	// ClaudeHookEvents are the Claude Code hook events AO installs, and
	// ClaudeHookCommandPrefix starts each of their commands.
	ClaudeHookEvents        []string `json:"claudeHookEvents"`
	ClaudeHookCommandPrefix string   `json:"claudeHookCommandPrefix"`
	// CodexHookEvents are the Codex hook events AO passes at launch.
	CodexHookEvents []string `json:"codexHookEvents"`
	// MCPServer is AO's own tool server.
	MCPServer AOMCPServer `json:"mcpServer"`
	// UsingAoSkillSHA256 is the sha256 of the using-ao skill's tree manifest
	// (domain.LadoSetupTree), as LADO hashes a folder item.
	UsingAoSkillSHA256 string `json:"usingAoSkillSHA256"`
}

// AOMCPServer is the name and the arguments of AO's tool server. The command
// is the daemon's own binary, which differs per computer, so it is left out.
type AOMCPServer struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
}

// currentAOSystem is the layer of this build, computed once per daemon.
var currentAOSystem = sync.OnceValues(func() (AOSystem, error) {
	skill, err := skillassets.Files()
	if err != nil {
		return AOSystem{}, fmt.Errorf("read the using-ao skill: %w", err)
	}
	tree, err := treeOfFS(skill)
	if err != nil {
		return AOSystem{}, fmt.Errorf("read the using-ao skill: %w", err)
	}
	skillSum, err := tree.SHA256()
	if err != nil {
		return AOSystem{}, fmt.Errorf("hash the using-ao skill: %w", err)
	}
	return AOSystem{
		ClaudeHookEvents:        claudecode.ManagedHookEvents(),
		ClaudeHookCommandPrefix: claudecode.HookCommandPrefix(),
		CodexHookEvents:         codex.ManagedHookEvents(),
		MCPServer:               AOMCPServer{Name: aoMCPServerName, Args: slices.Clone(aoMCPServerArgs)},
		UsingAoSkillSHA256:      skillSum,
	}, nil
})

// CurrentAOSystem returns the AO system layer of this build and the sha256
// of its canonical JSON, which a rule snapshot records as aoSystem.
func CurrentAOSystem() (AOSystem, string, error) {
	layer, err := currentAOSystem()
	if err != nil {
		return AOSystem{}, "", err
	}
	sum, err := layer.SHA256()
	if err != nil {
		return AOSystem{}, "", err
	}
	layer.ClaudeHookEvents = slices.Clone(layer.ClaudeHookEvents)
	layer.CodexHookEvents = slices.Clone(layer.CodexHookEvents)
	layer.MCPServer.Args = slices.Clone(layer.MCPServer.Args)
	return layer, sum, nil
}

// CanonicalJSON is the layer's hashed form: compact, the fields in
// declaration order, the event lists sorted without repeats, and HTML
// characters left as they are.
func (a AOSystem) CanonicalJSON() ([]byte, error) {
	a.ClaudeHookEvents = sortedUnique(a.ClaudeHookEvents)
	a.CodexHookEvents = sortedUnique(a.CodexHookEvents)
	if a.MCPServer.Args == nil {
		a.MCPServer.Args = []string{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(a); err != nil {
		return nil, fmt.Errorf("encode the AO system layer: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// SHA256 returns the hex sha256 of the canonical JSON.
func (a AOSystem) SHA256() (string, error) {
	data, err := a.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// sortedUnique returns a sorted copy of list without repeats, empty rather
// than nil.
func sortedUnique(list []string) []string {
	out := append([]string{}, list...)
	slices.Sort(out)
	return slices.Compact(out)
}

// treeOfFS builds the manifest of the files of fsys, as treeOf does for a
// folder on disk.
func treeOfFS(fsys fs.FS) (domain.LadoSetupTree, error) {
	var tree domain.LadoSetupTree
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		mode := domain.LadoSetupModeFile
		if info.Mode().Perm()&0o111 != 0 {
			mode = domain.LadoSetupModeExec
		}
		tree.Entries = append(tree.Entries, domain.LadoSetupTreeEntry{Path: path, Mode: mode, Size: int64(len(data)), SHA256: sha256Hex(data)})
		return nil
	})
	return tree, err
}
