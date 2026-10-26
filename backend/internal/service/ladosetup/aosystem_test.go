package ladosetup

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

// TestAOSystemHashFollowsAOSources checks that the AO system layer holds
// what AO's code installs, and that each input changes its hash.
func TestAOSystemHashFollowsAOSources(t *testing.T) {
	layer, sum, err := CurrentAOSystem()
	if err != nil {
		t.Fatalf("CurrentAOSystem: %v", err)
	}

	// Claude Code: the hooks AO writes into a session's workspace.
	workspace := t.TempDir()
	if err := claudecode.New().GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace}); err != nil {
		t.Fatalf("install AO's Claude Code hooks: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	var installed struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &installed); err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(installed.Hooks); !slices.Equal(got, sortedUnique(layer.ClaudeHookEvents)) {
		t.Errorf("Claude Code hook events %q, AO installs %q", layer.ClaudeHookEvents, got)
	}
	for event, groups := range installed.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if layer.ClaudeHookCommandPrefix == "" || !strings.HasPrefix(h.Command, layer.ClaudeHookCommandPrefix) {
					t.Errorf("AO's %s hook runs %q, which does not start with %q", event, h.Command, layer.ClaudeHookCommandPrefix)
				}
			}
		}
	}

	// Codex: the events of the hooks AO passes at launch.
	if want := codex.ManagedHookEvents(); len(want) == 0 || !slices.Equal(layer.CodexHookEvents, want) {
		t.Errorf("Codex hook events %q, AO passes %q", layer.CodexHookEvents, want)
	}

	// The tool server every Chat session gets (session_manager's
	// aoMCPServers).
	name, args := chatMCPServerInSource(t, filepath.Join("..", "..", "session_manager", "chat_spawn.go"))
	if layer.MCPServer.Name != name || !slices.Equal(layer.MCPServer.Args, args) {
		t.Errorf("MCP server %+v, AO gives Chat sessions name %q args %q", layer.MCPServer, name, args)
	}

	// The using-ao skill, hashed as LADO hashes a folder item.
	dir := filepath.Join(t.TempDir(), skillassets.SkillName)
	if err := skillassets.Materialize(dir); err != nil {
		t.Fatal(err)
	}
	tree, err := treeOf(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if want, err := tree.SHA256(); err != nil || layer.UsingAoSkillSHA256 != want {
		t.Errorf("using-ao skill sha256 %s, the installed skill hashes to %s (%v)", layer.UsingAoSkillSHA256, want, err)
	}

	if want, err := layer.SHA256(); err != nil || sum != want {
		t.Errorf("CurrentAOSystem sha256 %s, the layer hashes to %s (%v)", sum, want, err)
	}
	// A caller cannot change the layer the daemon computed once.
	layer.ClaudeHookEvents[0] = "Changed"
	if again, sumAgain, err := CurrentAOSystem(); err != nil || sumAgain != sum || again.ClaudeHookEvents[0] == "Changed" {
		t.Errorf("CurrentAOSystem changed after a caller edited its result: %s, %v", sumAgain, err)
	}

	// The canonical form: fields in order, events sorted without repeats.
	small := AOSystem{
		ClaudeHookEvents:        []string{"Stop", "SessionStart", "Stop"},
		ClaudeHookCommandPrefix: "ao hooks claude-code ",
		CodexHookEvents:         []string{"Stop"},
		MCPServer:               AOMCPServer{Name: "ao", Args: []string{"mcp"}},
		UsingAoSkillSHA256:      strings.Repeat("a", 64),
	}
	canonical, err := small.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"claudeHookEvents":["SessionStart","Stop"],"claudeHookCommandPrefix":"ao hooks claude-code ","codexHookEvents":["Stop"],` +
		`"mcpServer":{"name":"ao","args":["mcp"]},"usingAoSkillSHA256":"` + strings.Repeat("a", 64) + `"}`
	if string(canonical) != want {
		t.Errorf("canonical JSON\n%s\nwant\n%s", canonical, want)
	}
	reordered := small
	reordered.ClaudeHookEvents = []string{"SessionStart", "Stop"}
	if a, b := mustSum(t, small), mustSum(t, reordered); a != b {
		t.Errorf("reordering AO's events changed the hash")
	}

	base := mustSum(t, small)
	inputs := map[string]func(a *AOSystem){
		"a Claude Code event":   func(a *AOSystem) { a.ClaudeHookEvents = []string{"SessionStart", "Stop", "PreCompact"} },
		"the command prefix":    func(a *AOSystem) { a.ClaudeHookCommandPrefix = "ao hook claude-code " },
		"a Codex event":         func(a *AOSystem) { a.CodexHookEvents = []string{"Stop", "SessionStart"} },
		"the MCP server's name": func(a *AOSystem) { a.MCPServer.Name = "ao2" },
		"the MCP server's args": func(a *AOSystem) { a.MCPServer.Args = []string{"mcp", "--stdio"} },
		"the using-ao skill":    func(a *AOSystem) { a.UsingAoSkillSHA256 = strings.Repeat("b", 64) },
	}
	for input, change := range inputs {
		changed := small
		change(&changed)
		if mustSum(t, changed) == base {
			t.Errorf("changing %s left the hash as it was", input)
		}
	}
}

func mustSum(t *testing.T, a AOSystem) string {
	t.Helper()
	sum, err := a.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

// chatMCPServerInSource reads the Name and Args of the one
// ports.ChatMCPServerConfig literal in aoMCPServers of the Go file at path.
func chatMCPServerInSource(t *testing.T, path string) (string, []string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var (
		name  string
		args  []string
		found int
	)
	str := func(e ast.Expr) string {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Fatalf("%s: not a string literal: %#v", path, e)
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "aoMCPServers" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			isServer := false
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "Name":
					name, isServer = str(kv.Value), true
				case "Args":
					list, ok := kv.Value.(*ast.CompositeLit)
					if !ok {
						t.Fatalf("%s: Args is not a literal", path)
					}
					for _, a := range list.Elts {
						args = append(args, str(a))
					}
				}
			}
			if isServer {
				found++
				return false
			}
			return true
		})
	}
	if found != 1 {
		t.Fatalf("%s: found %d tool server literals in aoMCPServers, want 1", path, found)
	}
	return name, args
}
