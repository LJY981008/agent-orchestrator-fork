package codexappserver

// LADO fork: an MCP server LADO adopted from config.toml keeps its whole
// table in a Chat thread (docs/LJY-PRD/HARNESS.md §9).

import (
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestThreadConfigCarriesAdoptedServerTables(t *testing.T) {
	config := threadConfig("", []ports.ChatMCPServerConfig{{
		Name: "zeta", Type: "stdio", Command: "zeta-mcp", Args: []string{"-y"},
		CodexConfig: map[string]any{
			"enabled_tools":       []any{"read"},
			"startup_timeout_sec": json.Number("20"),
			"tools":               map[string]any{"search": map[string]any{"approval_mode": "approve"}},
			// The typed fields win over a stray copy.
			"command": "other",
		},
	}, {
		Name: "alpha", Type: "http", URL: "https://alpha.example/mcp",
		CodexConfig: map[string]any{"enabled": false, "bearer_token_env_var": "ALPHA_TOKEN"},
	}})
	got, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"mcp_servers":{"alpha":{"bearer_token_env_var":"ALPHA_TOKEN","enabled":false,"url":"https://alpha.example/mcp"},` +
		`"zeta":{"args":["-y"],"command":"zeta-mcp","enabled_tools":["read"],"startup_timeout_sec":20,"tools":{"search":{"approval_mode":"approve"}}}}}`
	if string(got) != want {
		t.Fatalf("config = %s\nwant     %s", got, want)
	}
}
