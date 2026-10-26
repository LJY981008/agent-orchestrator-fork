package ladosetup

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// tomlControl is a TOML escape of a control character without a short form.
const tomlControl = `\` + "u0001"

// richCodexConfig holds MCP servers that need every part of the inline
// encoder: nested tables, a key that needs quotes, integers, floats,
// booleans, arrays and strings that need escapes.
const richCodexConfig = `model = "gpt-5"

[mcp_servers.zeta]
command = "npx"
args = ["-y", "@scope/zeta-mcp", "say \"hi\"", "C:\\tmp\\x", "tab\there", "line1\nline2", "bell\b", "한글", "ctl` + tomlControl + `"]
env_vars = ["ZETA_TOKEN"]
startup_timeout_sec = 20
tool_timeout_sec = 1.5
enabled = true

[mcp_servers.zeta.tools."search.v2"]
approval_mode = "approve"

[mcp_servers.alpha]
url = "https://alpha.example/mcp"
bearer_token_env_var = "ALPHA_TOKEN"
enabled_tools = ["read", "list"]
enabled = false
`

// addJSONVersion stores a JSON value as the next version of an item, as an
// edit in the app does.
func (f *adoptionFixture) addJSONVersion(itemID domain.LadoSetupItemID, base int, value string) {
	f.t.Helper()
	content, err := canonicalJSON([]byte(value))
	if err != nil {
		f.t.Fatal(err)
	}
	sum, err := f.deps.Blobs.Put(content)
	if err != nil {
		f.t.Fatal(err)
	}
	at := scanAt.Add(time.Hour)
	v := domain.LadoSetupItemVersion{
		ID: domain.LadoSetupItemVersionID(fmt.Sprintf("%s-v%d", itemID, base+1)), ItemID: itemID,
		ContentKind: domain.LadoSetupContentJSON, SHA256: sum, Size: int64(len(content)), Actor: "api", CreatedAt: at,
	}
	if err := f.store.AddLadoSetupItemVersion(context.Background(), itemID, base, v,
		domain.LadoAuditEvent{At: at, Actor: "api", Action: "setup.item_updated", SubjectKind: "setup_item", SubjectID: string(itemID)}, nil); err != nil {
		f.t.Fatal(err)
	}
}

func TestCodexOverridesInlineTables(t *testing.T) {
	f := newAdoption(t)
	f.write(filepath.Join(f.codex, "config.toml"), richCodexConfig)
	svc := f.service()
	overrides := func() []string {
		t.Helper()
		args, err := svc.CodexOverrides(context.Background(), "s-1", "", "p")
		if err != nil {
			t.Fatalf("CodexOverrides: %v", err)
		}
		return args
	}
	// Nothing managed, then managed instructions only: no options.
	if args := overrides(); len(args) != 0 {
		t.Fatalf("options %q before any adoption, want none", args)
	}
	f.mustAdopt(svc, codexMove(domain.LadoSetupKindInstructions, "AGENTS.md"))
	if args := overrides(); len(args) != 0 {
		t.Fatalf("options %q without a managed server, want none", args)
	}

	f.mustAdopt(svc, codexMove(domain.LadoSetupKindMCPServer, "zeta"), codexMove(domain.LadoSetupKindMCPServer, "alpha"))
	args := overrides()
	var doc map[string]any
	if err := toml.Unmarshal([]byte(richCodexConfig), &doc); err != nil {
		t.Fatal(err)
	}
	original := doc[codexServersKey].(map[string]any)
	names := []string{"alpha", "zeta"}
	if len(args) != 2*len(names) {
		t.Fatalf("options %q, want one -c pair per server", args)
	}
	for i, name := range names {
		prefix := "mcp_servers." + name + "="
		if args[2*i] != "-c" || !strings.HasPrefix(args[2*i+1], prefix) {
			t.Fatalf("pair %d is %q %q, want -c %s<table> in name order", i, args[2*i], args[2*i+1], prefix)
		}
		// The value reads back as the table config.toml held.
		var x map[string]any
		if err := toml.Unmarshal([]byte("x = "+strings.TrimPrefix(args[2*i+1], prefix)), &x); err != nil {
			t.Fatalf("%s does not parse: %v", args[2*i+1], err)
		}
		if !reflect.DeepEqual(x["x"], original[name]) {
			t.Fatalf("%s reads back as %#v, want %#v", name, x["x"], original[name])
		}
	}
	// One inline table a server, keys sorted.
	if want := `mcp_servers.alpha={bearer_token_env_var = "ALPHA_TOKEN", enabled = false, enabled_tools = ["read", "list"], url = "https://alpha.example/mcp"}`; args[1] != want {
		t.Fatalf("alpha = %s\nwant    %s", args[1], want)
	}
	for _, part := range []string{
		`"say \"hi\""`, `"C:\\tmp\\x"`, `"tab\there"`, `"line1\nline2"`, `"bell\b"`, `"한글"`, `"ctl` + tomlControl + `"`,
		`startup_timeout_sec = 20`, `tool_timeout_sec = 1.5`, `tools = {"search.v2" = {approval_mode = "approve"}}`,
	} {
		if !strings.Contains(args[3], part) {
			t.Fatalf("zeta = %s\nlacks %s", args[3], part)
		}
	}
}

// codexDelivery is what a Codex session gets.
type codexDelivery struct {
	overrides    []string
	chat         []ports.ChatMCPServerConfig
	instructions string
}

func deliverCodex(t *testing.T, svc *Service, project domain.ProjectID, request string) codexDelivery {
	t.Helper()
	ctx := context.Background()
	overrides, err := svc.CodexOverrides(ctx, "s-1", request, project)
	if err != nil {
		t.Fatalf("CodexOverrides: %v", err)
	}
	chat, err := svc.CodexChatServers(ctx, "s-1", request, project)
	if err != nil {
		t.Fatalf("CodexChatServers: %v", err)
	}
	instructions, err := svc.CodexInstructions(ctx, "s-1", request, project)
	if err != nil {
		t.Fatalf("CodexInstructions: %v", err)
	}
	return codexDelivery{overrides: overrides, chat: chat, instructions: instructions}
}

// HARNESS §15 item 12 for Codex: setup changed after an approval never
// reaches the execution, and every other session gets what is on for its
// project now.
func TestCodexDeliveryUsesPinnedVersions(t *testing.T) {
	f := newAdoption(t)
	res := f.mustAdopt(f.service(),
		codexMove(domain.LadoSetupKindInstructions, "AGENTS.md"),
		codexMove(domain.LadoSetupKindMCPServer, "figma"),
		codexMove(domain.LadoSetupKindMCPServer, "notion"),
	)
	ids := itemIDs(res)
	request := domain.LadoClientRequestID("lado-queue-1", 1)
	f.deps.Store = &pinningStore{recordingStore: f.store, request: request, pins: map[domain.LadoSetupItemID]int{
		ids["AGENTS.md"]: 1, ids["figma"]: 1, ids["notion"]: 1,
	}}
	svc := f.service()
	// After the approval: AGENTS.md and figma are edited, notion deleted.
	f.addVersion(ids["AGENTS.md"], 1, "codex rules v2\n")
	f.addJSONVersion(ids["figma"], 1, `{"url":"https://figma.example/v2"}`)
	at := scanAt.Add(time.Hour)
	if err := f.store.DeleteLadoSetupItem(context.Background(), ids["notion"], at,
		domain.LadoAuditEvent{At: at, Actor: "api", Action: "setup.item_deleted", SubjectKind: "setup_item", SubjectID: string(ids["notion"])}); err != nil {
		t.Fatal(err)
	}

	pinned := codexDelivery{
		overrides: []string{"-c", `mcp_servers.figma={url = "https://figma"}`, "-c", `mcp_servers.notion={args = ["--x"], url = "https://notion"}`},
		chat: []ports.ChatMCPServerConfig{
			{Name: "figma", Type: "http", URL: "https://figma"},
			{Name: "notion", Type: "http", URL: "https://notion", CodexConfig: map[string]any{"args": []any{"--x"}}},
		},
		instructions: "codex rules",
	}
	if got := deliverCodex(t, svc, "p", request); !reflect.DeepEqual(got, pinned) {
		t.Fatalf("execution got %+v, want the pinned versions %+v", got, pinned)
	}
	current := codexDelivery{
		overrides:    []string{"-c", `mcp_servers.figma={url = "https://figma.example/v2"}`},
		chat:         []ports.ChatMCPServerConfig{{Name: "figma", Type: "http", URL: "https://figma.example/v2"}},
		instructions: "codex rules v2",
	}
	if got := deliverCodex(t, svc, "p", "ui-request-1"); !reflect.DeepEqual(got, current) {
		t.Fatalf("other session got %+v, want the current versions %+v", got, current)
	}

	// A Project binding turns figma off in one project, a Global one
	// AGENTS.md everywhere; neither reaches the execution.
	setBinding(t, f, ids["figma"], domain.LadoScopeProject, "p2", domain.LadoBindingForceOff)
	setBinding(t, f, ids["AGENTS.md"], domain.LadoScopeGlobal, "", domain.LadoBindingOff)
	if got := deliverCodex(t, svc, "p2", ""); len(got.overrides) != 0 || len(got.chat) != 0 || got.instructions != "" {
		t.Fatalf("session in p2 got %+v, want nothing", got)
	}
	if got := deliverCodex(t, svc, "p", ""); !reflect.DeepEqual(got.overrides, current.overrides) || got.instructions != "" {
		t.Fatalf("session in p got %+v, want figma and no instructions", got)
	}
	if got := deliverCodex(t, svc, "p", request); !reflect.DeepEqual(got, pinned) {
		t.Fatalf("execution after the bindings got %+v, want the pinned versions %+v", got, pinned)
	}
}

// Codex uses the first of AGENTS.override.md and AGENTS.md that has text.
func TestCodexInstructionsPreferTheOverride(t *testing.T) {
	f := newAdoption(t)
	f.write(filepath.Join(f.codex, "AGENTS.override.md"), "  \n")
	res := f.mustAdopt(f.service(),
		codexMove(domain.LadoSetupKindInstructions, "AGENTS.md"),
		codexMove(domain.LadoSetupKindInstructions, "AGENTS.override.md"),
	)
	ids := itemIDs(res)
	svc := f.service()
	if got := deliverCodex(t, svc, "p", "").instructions; got != "codex rules" {
		t.Fatalf("instructions with a blank override = %q, want AGENTS.md's", got)
	}
	f.addVersion(ids["AGENTS.override.md"], 1, "\noverride rules\n")
	if got := deliverCodex(t, svc, "p", "").instructions; got != "override rules" {
		t.Fatalf("instructions = %q, want the override's", got)
	}
	setBinding(t, f, ids["AGENTS.override.md"], domain.LadoScopeProject, "p", domain.LadoBindingForceOff)
	if got := deliverCodex(t, svc, "p", "").instructions; got != "codex rules" {
		t.Fatalf("instructions with the override off = %q, want AGENTS.md's", got)
	}
}

// A server's content as a Chat tool server keeps every key of its table.
func TestCodexChatServersKeepTheTable(t *testing.T) {
	stdio, err := chatServer("zeta", []byte(`{"args":["-y","zeta-mcp"],"command":"npx","enabled":false,"env":{},"env_vars":["ZETA_TOKEN"],"startup_timeout_sec":20}`))
	if err != nil {
		t.Fatal(err)
	}
	want := ports.ChatMCPServerConfig{
		Name: "zeta", Type: "stdio", Command: "npx", Args: []string{"-y", "zeta-mcp"}, Env: map[string]string{},
		CodexConfig: map[string]any{"enabled": false, "env_vars": []any{"ZETA_TOKEN"}, "startup_timeout_sec": json.Number("20")},
	}
	if !reflect.DeepEqual(stdio, want) {
		t.Fatalf("stdio server = %+v, want %+v", stdio, want)
	}
	for _, content := range []string{`{"enabled":true}`, `{"command":7}`, `{"command":"npx","args":["-y",1]}`, `{"url":"https://x","http_headers":{"A":1}}`} {
		if _, err := chatServer("zeta", []byte(content)); err == nil {
			t.Fatalf("chatServer(%s) succeeded, want an error", content)
		}
	}
}
