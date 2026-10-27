package ladosetup

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// B4: the variables that pick Claude Code's login, provider or proxy stay
// in the original settings.json when env is adopted, an env that turns a
// cloud provider on is not adopted at all, and no change can add them.
func TestProviderLoginEnvStaysInPlace(t *testing.T) {
	ctx := context.Background()
	settings := `{
  "env": {
    "AWS_REGION": "us-east-1",
    "AWS_PROFILE": "work",
    "CLOUD_ML_REGION": "us-east5",
    "VERTEX_REGION_CLAUDE_3_7_SONNET": "europe-west1",
    "GOOGLE_APPLICATION_CREDENTIALS": "/keys/gcp.json",
    "AZURE_API_KEY": "az",
    "https_proxy": "http://proxy:3128",
    "NO_PROXY": "localhost",
    "ANTHROPIC_MODEL": "opus",
    "DEBUG": "1"
  },
  "model": "opus"
}
`
	kept := []string{"ANTHROPIC_MODEL", "AWS_PROFILE", "AWS_REGION", "AZURE_API_KEY", "CLOUD_ML_REGION", "GOOGLE_APPLICATION_CREDENTIALS",
		"NO_PROXY", "VERTEX_REGION_CLAUDE_3_7_SONNET", "https_proxy"}
	f := newChangeFixture(t)
	path := filepath.Join(f.claude, "settings.json")
	f.write(path, settings)
	env := entryAt(t, mustScan(t, f.deps), domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSetting, "env")
	wantStatus(t, env, domain.LadoSetupStatusAdoptable, "")
	if !slices.Equal(env.KeptInPlace, kept) {
		t.Fatalf("env keeps %v in place, want %v", env.KeptInPlace, kept)
	}
	ids := f.adopt(claudeMove(domain.LadoSetupKindSetting, "env"))
	var left struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(readFile(t, path)), &left); err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(left.Env); !slices.Equal(got, kept) {
		t.Fatalf("settings.json keeps env %v, want %v", got, kept)
	}
	_, versions, _, err := f.store.LadoSetupItem(ctx, ids["env"])
	if err != nil || len(versions) != 1 {
		t.Fatalf("env versions %+v (%v)", versions, err)
	}
	adopted, err := f.deps.Blobs.Get(versions[0].SHA256, maxBlobBytes)
	if err != nil {
		t.Fatal(err)
	}
	if string(adopted) != `{"DEBUG":"1"}` {
		t.Fatalf("env adopted as %s", adopted)
	}

	// An edit or a new env item cannot bring them back.
	for _, name := range []string{"AWS_REGION", "aws_secret_access_key", "CLAUDE_CODE_USE_VERTEX", "VERTEX_REGION_X", "GOOGLE_CLOUD_PROJECT", "AZURE_OPENAI_KEY", "http_proxy", "HTTPS_PROXY"} {
		_, err := f.svc.Apply(ctx, Change{Op: OpEdit, ItemID: ids["env"], BaseVersion: 1, Content: json.RawMessage(`{"DEBUG":"1","` + name + `":"x"}`)}, f.presence())
		if details := wantAPIError(t, err, CodeLoginKey); details["key"] != name {
			t.Fatalf("%s: details %v", name, details)
		}
	}

	// A provider switch that is on keeps the whole env in place.
	for _, on := range []string{`"1"`, `"true"`, `1`} {
		h := newFakeHome(t)
		h.write(filepath.Join(h.claude, "settings.json"), `{"env":{"CLAUDE_CODE_USE_BEDROCK":`+on+`,"AWS_REGION":"us-east-1","DEBUG":"1"}}`)
		wantStatus(t, entryAt(t, mustScan(t, h.deps()), domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSetting, "env"),
			domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonProviderLoginEnv)
	}
	h := newFakeHome(t)
	h.write(filepath.Join(h.claude, "settings.json"), `{"env":{"CLAUDE_CODE_USE_FOUNDRY":"0","DEBUG":"1"}}`)
	off := entryAt(t, mustScan(t, h.deps()), domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSetting, "env")
	wantStatus(t, off, domain.LadoSetupStatusAdoptable, "")
	if !slices.Equal(off.KeptInPlace, []string{"CLAUDE_CODE_USE_FOUNDRY"}) {
		t.Fatalf("a switch that is off is kept as %v", off.KeptInPlace)
	}
	f.mustApply(Change{Op: OpDelete, ItemID: ids["env"]}, true)
	f.write(path, `{"env":{"CLAUDE_CODE_USE_BEDROCK":"1","DEBUG":"2"}}`)
	_, err = f.svc.Adopt(ctx, AdoptRequest{Entries: []AdoptEntry{claudeMove(domain.LadoSetupKindSetting, "env")}, DryRun: true})
	if details := wantAPIError(t, err, CodeNotAdoptable); details["reason"] != string(domain.LadoSetupReasonProviderLoginEnv) {
		t.Fatalf("details %v", details)
	}
}

// The dry run of a change compares the conflicts against the rules'
// permission ceiling.
func TestChangeDryRunChecksTheCeiling(t *testing.T) {
	f := newChangeFixture(t)
	f.svc.SetPolicy(&impactPolicy{})
	create := Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: "permissions",
		Content: json.RawMessage(`{"defaultMode":"bypassPermissions"}`)}
	res := f.dryRun(create)
	if !slices.ContainsFunc(res.ConflictsAdded, func(c Conflict) bool {
		return c.Code == ConflictDefaultModeAboveCeiling && c.Details["mode"] == "bypassPermissions" && c.Details["ceiling"] == string(domain.PermissionModeAuto)
	}) {
		t.Fatalf("conflicts added %+v, want the default mode above the auto ceiling", res.ConflictsAdded)
	}
	id := f.mustApply(create, true).Item.ID
	edit := f.dryRun(Change{Op: OpEdit, ItemID: id, BaseVersion: 1, Content: json.RawMessage(`{"defaultMode":"acceptEdits"}`)})
	if !slices.ContainsFunc(edit.ConflictsRemoved, func(c Conflict) bool { return c.Code == ConflictDefaultModeAboveCeiling }) {
		t.Fatalf("conflicts removed %+v, want the default mode above the ceiling", edit.ConflictsRemoved)
	}
}

// Items reach sessions on a verified OS only, so making one elsewhere is
// refused as adoption is, dry run or not, and nothing is written.
func TestCreateRefusedOnUnverifiedOS(t *testing.T) {
	f := newChangeFixture(t)
	for _, goos := range []string{"darwin", "windows"} {
		f.deps.GOOS = goos
		svc := New(f.deps)
		for _, c := range []Change{
			{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindInstructions, Name: "RULES.md", Content: jsonText("# rules\n")},
			{Op: OpCreate, Tool: domain.LadoSetupToolCodex, Kind: domain.LadoSetupKindMCPServer, Name: "docs", Content: json.RawMessage(`{"url":"https://docs"}`)},
		} {
			for _, dryRun := range []bool{true, false} {
				db := f.dbState()
				c.DryRun = dryRun
				_, err := svc.Apply(context.Background(), c, f.presence())
				details := wantAPIError(t, err, CodeOSUnverified)
				if details["os"] != goos || !strings.Contains(strings.Join(anyStrings(details["tools"]), ","), string(c.Tool)) {
					t.Fatalf("%s %s: details %v", goos, c.Tool, details)
				}
				wantSameDB(t, "a refused create", db, f.dbState())
			}
		}
	}
	f.deps.GOOS = "linux"
	f.svc = New(f.deps)
	f.mustApply(Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindInstructions, Name: "RULES.md", Content: jsonText("# rules\n")}, false)
}

// anyStrings reads an error detail that holds a list.
func anyStrings(v any) []string {
	var out []string
	switch v := v.(type) {
	case []string:
		out = v
	case []domain.LadoSetupTool:
		for _, s := range v {
			out = append(out, string(s))
		}
	case []any:
		for _, s := range v {
			if s, ok := s.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// B3: a value that names a script the adoption moves must follow it. A
// shell command that moves along (hooks, statusLine, otelHeadersHelper) is
// rewritten to "$CLAUDE_CONFIG_DIR"/…; a value that stays in place (a login
// key, a moved env's login variable, a server that is not adopted) or that
// no shell reads (an env value, an MCP server's command and args) blocks
// the adoption with LADO_SETUP_PATH_USED_IN_PLACE, and nothing is written.
func TestMovedPathReferencesEverywhere(t *testing.T) {
	ctx := context.Background()
	hookScript := func(name string) AdoptEntry { return claudeMove(domain.LadoSetupKindHookScript, name) }
	setting := func(key string) AdoptEntry { return claudeMove(domain.LadoSetupKindSetting, key) }
	for _, tc := range []struct {
		name       string
		settings   string
		claudeJSON string
		codex      string
		entries    []AdoptEntry
		key, path  string
	}{
		{name: "login key", settings: `{"apiKeyHelper":"~/.claude/hooks/key.sh"}`, entries: []AdoptEntry{hookScript("key.sh")}, key: "apiKeyHelper", path: "hooks/key.sh"},
		{name: "a moved env's login variable", settings: `{"env":{"AWS_SHARED_CREDENTIALS_FILE":"$HOME/.claude/hooks/key.sh","DEBUG":"1"}}`,
			entries: []AdoptEntry{setting("env"), hookScript("key.sh")}, key: "env.AWS_SHARED_CREDENTIALS_FILE", path: "hooks/key.sh"},
		{name: "a moved env value", settings: `{"env":{"HELPER":"~/.claude/hooks/key.sh"}}`, entries: []AdoptEntry{setting("env"), hookScript("key.sh")}, key: "env.HELPER", path: "hooks/key.sh"},
		{name: "a setting kept in place", settings: `{"otelHeadersHelper":"~/.claude/hooks/key.sh"}`, entries: []AdoptEntry{hookScript("key.sh")}, key: "otelHeadersHelper", path: "hooks/key.sh"},
		{name: "a Claude MCP server's args", settings: `{}`, claudeJSON: `{"mcpServers":{"docs":{"command":"node","args":["~/.claude/hooks/key.sh"]}}}`,
			entries: []AdoptEntry{hookScript("key.sh")}, key: "args[0]", path: "hooks/key.sh"},
		{name: "a moved Claude MCP server's command", settings: `{}`, claudeJSON: `{"mcpServers":{"docs":{"command":"~/.claude/hooks/key.sh"}}}`,
			entries: []AdoptEntry{claudeMove(domain.LadoSetupKindMCPServer, "docs"), hookScript("key.sh")}, key: "command", path: "hooks/key.sh"},
		{name: "a Codex MCP server's env", settings: `{}`, codex: "[mcp_servers.lint]\ncommand = \"node\"\nenv_vars = [\"X\"]\nargs = [\"--helper\", \"~/.claude/hooks/key.sh\"]\n",
			entries: []AdoptEntry{hookScript("key.sh")}, key: "args[1]", path: "hooks/key.sh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdoption(t)
			f.write(filepath.Join(f.claude, "settings.json"), tc.settings)
			f.writeMode(filepath.Join(f.claude, "hooks", "key.sh"), "#!/bin/sh\necho key\n", 0o755)
			if tc.claudeJSON != "" {
				f.write(f.claudeJSON, tc.claudeJSON)
			}
			if tc.codex != "" {
				f.write(filepath.Join(f.codex, "config.toml"), tc.codex)
			}
			svc := f.service()
			p := f.preview(svc, tc.entries...)
			i := slices.IndexFunc(p.Conflicts, func(c Conflict) bool { return c.Code == ConflictPathUsedInPlace })
			if i < 0 {
				t.Fatalf("conflicts %+v, want %s", p.Conflicts, ConflictPathUsedInPlace)
			}
			if c := p.Conflicts[i]; c.Severity != domain.LadoSeverityBlocking || c.Details["key"] != tc.key || c.Details["path"] != tc.path {
				t.Fatalf("conflict %+v, want key %s and path %s", c, tc.key, tc.path)
			}
			home := f.homeState()
			_, err := svc.Adopt(ctx, AdoptRequest{Entries: tc.entries, PlanSHA256: p.PlanSHA256, Presence: f.presence()})
			if details := wantAPIError(t, err, ConflictPathUsedInPlace); details["key"] != tc.key || details["path"] != tc.path {
				t.Fatalf("details %v", details)
			}
			if diffs := diffStates(home, f.homeState()); len(diffs) > 0 {
				t.Fatalf("a refused adoption changed the home:\n%s", strings.Join(diffs, "\n"))
			}
		})
	}

	t.Run("shell commands that move along are rewritten", func(t *testing.T) {
		f := newAdoption(t)
		f.write(filepath.Join(f.claude, "settings.json"), `{
  "otelHeadersHelper": "~/.claude/hooks/key.sh --json",
  "statusLine": {"type": "command", "command": "${HOME}/.claude/hooks/key.sh"},
  "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "bash ~/.claude/hooks/key.sh"}]}]}
}
`)
		f.writeMode(filepath.Join(f.claude, "hooks", "key.sh"), "#!/bin/sh\necho key\n", 0o755)
		svc := f.service()
		entries := []AdoptEntry{hookScript("key.sh"), setting("otelHeadersHelper"), setting("statusLine"), setting("hooks")}
		p := f.preview(svc, entries...)
		if slices.ContainsFunc(p.Conflicts, func(c Conflict) bool { return slices.Contains(movedPathCodes, c.Code) }) {
			t.Fatalf("conflicts %+v, want none about moved paths", p.Conflicts)
		}
		var otel *HookRewrite
		for i, rw := range p.Rewrites {
			if rw.Setting == "otelHeadersHelper" {
				otel = &p.Rewrites[i]
			}
		}
		if otel == nil || otel.After != `"$CLAUDE_CONFIG_DIR"/hooks/key.sh --json` || !slices.Equal(otel.Paths, []string{"hooks/key.sh"}) || len(p.Rewrites) != 3 {
			t.Fatalf("rewrites %+v", p.Rewrites)
		}
		f.mustAdopt(svc, entries...)
		var managed map[string]json.RawMessage
		if err := json.Unmarshal([]byte(readFile(t, filepath.Join(f.state, "lado", "harness", "claude", "settings.json"))), &managed); err != nil {
			t.Fatal(err)
		}
		if got := string(managed["otelHeadersHelper"]); got != `"\"$CLAUDE_CONFIG_DIR\"/hooks/key.sh --json"` {
			t.Fatalf("managed otelHeadersHelper = %s", got)
		}
	})
}

// B6: a Codex MCP server whose args carry a header or a token, or whose url
// has a query or user info, would put the secret on a command line. It is
// not adoptable, it cannot be made, and no error says what it holds.
func TestCodexSecretsInArgsAreNotAdoptable(t *testing.T) {
	const secret = "sk-zzz-secret-value"
	config := `[mcp_servers.header]
command = "npx"
args = ["mcp-remote", "https://mcp.example", "--header", "X-Team: ` + secret + `"]

[mcp_servers.short]
command = "npx"
args = ["-H", "X-Team: ` + secret + `"]

[mcp_servers.bearer]
command = "npx"
args = ["--auth=Bearer ` + secret + `"]

[mcp_servers.apikey]
command = "srv"
args = ["--API_KEY=` + secret + `"]

[mcp_servers.password]
command = "srv"
args = ["--password", "` + secret + `"]

[mcp_servers.query]
url = "https://mcp.example/sse?token=` + secret + `"

[mcp_servers.userinfo]
url = "https://me:` + secret + `@mcp.example/sse"

[mcp_servers.plain]
command = "npx"
args = ["-y", "@acme/mcp", "--port", "8"]
url = "https://mcp.example/sse"
`
	f := newChangeFixture(t)
	f.write(filepath.Join(f.codex, "config.toml"), config)
	inv := mustScan(t, f.deps)
	for _, name := range []string{"header", "short", "bearer", "apikey", "password", "query", "userinfo"} {
		wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationCodexHome, domain.LadoSetupKindMCPServer, name),
			domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonLiteralCredentials)
	}
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationCodexHome, domain.LadoSetupKindMCPServer, "plain"), domain.LadoSetupStatusAdoptable, "")

	noSecret := func(what string, err error, details map[string]any) {
		t.Helper()
		if strings.Contains(err.Error(), secret) || strings.Contains(fmt.Sprint(details), secret) {
			t.Fatalf("%s: the error tells the secret: %v %v", what, err, details)
		}
	}
	_, err := f.svc.Adopt(context.Background(), AdoptRequest{Entries: []AdoptEntry{codexMove(domain.LadoSetupKindMCPServer, "header")}, DryRun: true})
	details := wantAPIError(t, err, CodeNotAdoptable)
	if details["reason"] != string(domain.LadoSetupReasonLiteralCredentials) {
		t.Fatalf("details %v", details)
	}
	noSecret("adopt", err, details)
	for _, content := range []string{
		`{"command":"npx","args":["--header","Authorization: Bearer ` + secret + `"]}`,
		`{"command":"npx","args":["token=` + secret + `"]}`,
		`{"command":"npx","args":["--x-api-key","` + secret + `"]}`,
		`{"url":"https://mcp.example/?key=` + secret + `"}`,
		`{"url":"https://u:` + secret + `@mcp.example/"}`,
	} {
		_, err := f.svc.Apply(context.Background(), Change{Op: OpCreate, Tool: domain.LadoSetupToolCodex, Kind: domain.LadoSetupKindMCPServer, Name: "made",
			Content: json.RawMessage(content), DryRun: true}, nil)
		details := wantAPIError(t, err, CodeContentInvalid)
		if details["reason"] != string(domain.LadoSetupReasonLiteralCredentials) {
			t.Fatalf("%s: details %v", content, details)
		}
		noSecret("create", err, details)
	}
	// A value that reads back otherwise names the server only.
	err = checkCodexOverride("odd", []byte(`{"command":"`+secret+`","n":1.50}`))
	if err == nil {
		t.Fatal("1.50 reads back as 1.5 and was not refused")
	}
	noSecret("checkCodexOverride", err, nil)
}
