package codexappserver

// LADO fork: an MCP server LADO adopted from config.toml keeps its whole
// table in a Chat thread (docs/LJY-PRD/HARNESS.md §9).

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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

// LADO's compact limit (issue #42) reaches a Codex Chat thread through its
// config, on thread/start and thread/resume, and never reaches app-server's
// own environment.
func TestThreadConfigCarriesLadosCompactLimit(t *testing.T) {
	type sent struct {
		config map[string]any
		env    []string
	}
	run := func(t *testing.T, resume bool, base map[string]string, prepared map[string]string) sent {
		t.Helper()
		d, srv := newTestDriver(t)
		var env []string
		spawn := d.spawn
		d.spawn = func(ctx context.Context, bin, dir string, e []string) (*process, error) {
			env = e
			return spawn(ctx, bin, dir, e)
		}
		var prepare func(context.Context) (map[string]string, error)
		if prepared != nil {
			prepare = func(context.Context) (map[string]string, error) { return prepared, nil }
		}
		method := "thread/start"
		var err error
		var conv ports.ChatConversation
		if resume {
			method = "thread/resume"
			conv, err = d.Resume(context.Background(), ports.ChatResumeConfig{
				SessionID: "ao-1", ProviderConversationID: "thread-1", WorkspacePath: "/tmp/ws", Env: base, PrepareEnv: prepare,
			})
		} else {
			conv, err = d.Start(context.Background(), ports.ChatStartConfig{
				SessionID: "ao-1", WorkspacePath: "/tmp/ws", Effort: "high", Env: base, PrepareEnv: prepare,
			})
		}
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		defer func() { _ = conv.Close() }()
		f := srv.awaitFrame(func(f frame) bool { return f.Method == method })
		var params struct {
			Config map[string]any `json:"config"`
		}
		if err := json.Unmarshal(f.Params, &params); err != nil {
			t.Fatal(err)
		}
		return sent{config: params.Config, env: env}
	}
	carries := func(t *testing.T, s sent, want float64) {
		t.Helper()
		if got := s.config[domain.LadoCodexCompactLimitKey]; got != want {
			t.Fatalf("%s = %v, want %v (config %v)", domain.LadoCodexCompactLimitKey, got, want, s.config)
		}
		for _, kv := range s.env {
			if strings.HasPrefix(kv, domain.LadoCodexCompactLimitEnv+"=") {
				t.Fatalf("app-server env holds %s", kv)
			}
		}
	}

	t.Run("start from the base env", func(t *testing.T) {
		s := run(t, false, map[string]string{domain.LadoCodexCompactLimitEnv: "150000", "KEEP": "1"}, nil)
		carries(t, s, 150000)
		if s.config["model_reasoning_effort"] != "high" {
			t.Fatalf("effort lost: %v", s.config)
		}
		if !slices.Contains(s.env, "KEEP=1") {
			t.Fatalf("app-server env lost the other variables: %q", s.env)
		}
	})
	t.Run("resume from the prepared env", func(t *testing.T) {
		carries(t, run(t, true, map[string]string{}, map[string]string{domain.LadoCodexCompactLimitEnv: "200000"}), 200000)
	})
	t.Run("no limit", func(t *testing.T) {
		s := run(t, false, map[string]string{"KEEP": "1"}, nil)
		if _, ok := s.config[domain.LadoCodexCompactLimitKey]; ok {
			t.Fatalf("config = %v, want no compact limit", s.config)
		}
	})
	t.Run("an unreadable value is dropped", func(t *testing.T) {
		s := run(t, false, map[string]string{domain.LadoCodexCompactLimitEnv: "lots"}, nil)
		if _, ok := s.config[domain.LadoCodexCompactLimitKey]; ok {
			t.Fatalf("config = %v, want no compact limit", s.config)
		}
		for _, kv := range s.env {
			if strings.HasPrefix(kv, domain.LadoCodexCompactLimitEnv+"=") {
				t.Fatalf("app-server env holds %s", kv)
			}
		}
	})
}
