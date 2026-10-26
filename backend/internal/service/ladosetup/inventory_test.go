package ladosetup

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agentaccount"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// HAR-010 and plan decision e: every Claude kind is listed with its status,
// reason and proposed action; a folder item is hashed as its tree manifest,
// and a link is never followed.
func TestInventoryListsClaudeItems(t *testing.T) {
	h := newFakeHome(t)
	claudeMD := "# Me\nAlways answer in Korean.\n"
	h.write(filepath.Join(h.claude, "CLAUDE.md"), claudeMD)
	h.write(filepath.Join(h.claude, "RULES.md"), "@CLAUDE.md\n")
	h.write(filepath.Join(h.claude, "settings.json"), `{
  "model": "opus",
  "theme": "dark",
  "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "echo <done> && true"}]}], "A": 1.50},
  "verbose": true
}`)
	skillMD := "---\nname: review\ndescription: Review a diff.\n---\nReview it.\n"
	runSH := "#!/bin/sh\necho review\n"
	h.write(filepath.Join(h.claude, "skills", "review", "SKILL.md"), skillMD)
	h.writeMode(filepath.Join(h.claude, "skills", "review", "scripts", "run.sh"), runSH, 0o755)
	h.write(filepath.Join(h.root, "dotfiles", "shared", "SKILL.md"), skillMD)
	h.symlink(filepath.Join(h.root, "dotfiles", "shared"), filepath.Join(h.claude, "skills", "shared"))
	h.write(filepath.Join(h.claude, "skills", ".DS_Store"), "junk")
	h.write(filepath.Join(h.claude, "agents", "reviewer.md"), "agent")
	h.write(filepath.Join(h.claude, "agents", "team", "planner.md"), "planner")
	h.write(filepath.Join(h.claude, "commands", "ship.md"), "ship it")
	h.write(filepath.Join(h.root, "dotfiles", "deploy.md"), "deploy")
	h.symlink(filepath.Join(h.root, "dotfiles", "deploy.md"), filepath.Join(h.claude, "commands", "ops", "deploy.md"))
	h.write(filepath.Join(h.claude, "output-styles", "terse.md"), "terse")
	guard := "#!/bin/sh\nexit 0\n"
	h.writeMode(filepath.Join(h.claude, "hooks", "guard.sh"), guard, 0o755)
	h.write(filepath.Join(h.claude, "plugins", "installed_plugins.json"), "{}")
	h.write(filepath.Join(h.claude, "keybindings.json"), "[]")
	h.write(h.claudeJSON, `{"numStartups": 3, "mcpServers": {"figma": {"url": "https://mcp.figma.com/mcp", "type": "http"}}}`)

	inv := mustScan(t, h.deps())
	claudeDir := domain.LadoSetupLocationClaudeDir

	// Top-level Markdown.
	md := entryAt(t, inv, claudeDir, domain.LadoSetupKindInstructions, "CLAUDE.md")
	wantAdoptable(t, md, []byte(claudeMD), domain.LadoSetupActionMove)
	if md.Tool != domain.LadoSetupToolClaudeCode || md.Path != filepath.Join(h.claude, "CLAUDE.md") {
		t.Fatalf("CLAUDE.md is %s at %s", md.Tool, md.Path)
	}
	wantAdoptable(t, entryAt(t, inv, claudeDir, domain.LadoSetupKindInstructions, "RULES.md"), []byte("@CLAUDE.md\n"), domain.LadoSetupActionMove)

	// One setting per key, as its canonical JSON value; keys outside the
	// moved list stay in place by default.
	model := entryAt(t, inv, claudeDir, domain.LadoSetupKindSetting, "model")
	wantAdoptable(t, model, []byte(`"opus"`), domain.LadoSetupActionMove)
	if model.Path != filepath.Join(h.claude, "settings.json")+"#model" {
		t.Fatalf("model is at %s", model.Path)
	}
	wantAdoptable(t, entryAt(t, inv, claudeDir, domain.LadoSetupKindSetting, "hooks"),
		[]byte(`{"A":1.50,"Stop":[{"hooks":[{"command":"echo <done> && true","type":"command"}]}]}`), domain.LadoSetupActionMove)
	wantAdoptable(t, entryAt(t, inv, claudeDir, domain.LadoSetupKindSetting, "theme"), []byte(`"dark"`), domain.LadoSetupActionKeep)
	wantAdoptable(t, entryAt(t, inv, claudeDir, domain.LadoSetupKindSetting, "verbose"), []byte(`true`), domain.LadoSetupActionKeep)

	// A skill folder is one item, hashed as its tree.
	tree := domain.LadoSetupTree{Entries: []domain.LadoSetupTreeEntry{
		{Path: "scripts/run.sh", Mode: domain.LadoSetupModeExec, Size: int64(len(runSH)), SHA256: sumOf([]byte(runSH))},
		{Path: "SKILL.md", Mode: domain.LadoSetupModeFile, Size: int64(len(skillMD)), SHA256: sumOf([]byte(skillMD))},
	}}
	treeSum, err := tree.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	skill := entryAt(t, inv, claudeDir, domain.LadoSetupKindSkill, "review")
	wantStatus(t, skill, domain.LadoSetupStatusAdoptable, "")
	if skill.SHA256 != treeSum || skill.Size != tree.Size() || skill.DefaultAction != domain.LadoSetupActionMove {
		t.Fatalf("skill review has sum %s, size %d, action %q; want the tree's %s, %d and move", skill.SHA256, skill.Size, skill.DefaultAction, treeSum, tree.Size())
	}
	// Links are listed and never followed, at the top of a folder and
	// deeper.
	wantStatus(t, entryAt(t, inv, claudeDir, domain.LadoSetupKindSkill, "shared"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
	wantStatus(t, entryAt(t, inv, claudeDir, domain.LadoSetupKindCommand, "ops/deploy.md"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
	wantStatus(t, entryAt(t, inv, claudeDir, "", "skills/.DS_Store"), domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonUnknownLocation)

	// Agents and commands are files at any depth, named by their path.
	wantAdoptable(t, entryAt(t, inv, claudeDir, domain.LadoSetupKindAgent, "reviewer.md"), []byte("agent"), domain.LadoSetupActionMove)
	wantAdoptable(t, entryAt(t, inv, claudeDir, domain.LadoSetupKindAgent, "team/planner.md"), []byte("planner"), domain.LadoSetupActionMove)
	wantAdoptable(t, entryAt(t, inv, claudeDir, domain.LadoSetupKindCommand, "ship.md"), []byte("ship it"), domain.LadoSetupActionMove)
	wantAdoptable(t, entryAt(t, inv, claudeDir, domain.LadoSetupKindOutputStyle, "terse.md"), []byte("terse"), domain.LadoSetupActionMove)
	wantAdoptable(t, entryAt(t, inv, claudeDir, domain.LadoSetupKindHookScript, "guard.sh"), []byte(guard), domain.LadoSetupActionMove)

	// plugins/ stays as one line; what LADO does not know is not managed.
	wantStatus(t, entryAt(t, inv, claudeDir, "", "plugins"), domain.LadoSetupStatusNever, domain.LadoSetupReasonPluginsStay)
	wantStatus(t, entryAt(t, inv, claudeDir, "", "keybindings.json"), domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonUnknownLocation)

	// A user MCP server of ~/.claude.json; the rest of the file is state.
	figma := entryAt(t, inv, domain.LadoSetupLocationClaudeJSON, domain.LadoSetupKindMCPServer, "figma")
	wantAdoptable(t, figma, []byte(`{"type":"http","url":"https://mcp.figma.com/mcp"}`), domain.LadoSetupActionMove)
	if figma.Path != h.claudeJSON+"#mcpServers.figma" {
		t.Fatalf("figma is at %s", figma.Path)
	}
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationClaudeJSON, "", ".claude.json"), domain.LadoSetupStatusNever, domain.LadoSetupReasonAppState)

	// Every kind has an adoptable entry.
	kinds := map[domain.LadoSetupKind]bool{}
	for _, e := range inv.Entries {
		if e.Status == domain.LadoSetupStatusAdoptable && e.Tool == domain.LadoSetupToolClaudeCode {
			kinds[e.Kind] = true
		}
	}
	for _, kind := range []domain.LadoSetupKind{
		domain.LadoSetupKindInstructions, domain.LadoSetupKindSetting, domain.LadoSetupKindSkill, domain.LadoSetupKindAgent,
		domain.LadoSetupKindCommand, domain.LadoSetupKindOutputStyle, domain.LadoSetupKindHookScript, domain.LadoSetupKindMCPServer,
	} {
		if !kinds[kind] {
			t.Errorf("no adoptable Claude %s", kind)
		}
	}
	if want := (Root{Tool: domain.LadoSetupToolClaudeCode, Location: claudeDir, Path: h.claude, Found: true}); !slices.Contains(inv.Roots, want) {
		t.Fatalf("roots %+v miss %+v", inv.Roots, want)
	}
}

// A settings file that is a link is read, but adoption would rewrite the
// link's target, so its keys are not adoptable.
func TestInventoryDoesNotAdoptThroughALinkedSettingsFile(t *testing.T) {
	h := newFakeHome(t)
	target := h.write(filepath.Join(h.root, "dotfiles", "settings.json"), `{"model": "opus", "apiKeyHelper": "pass claude", "env": {"ANTHROPIC_API_KEY": "sk", "EDITOR": "vim"}}`)
	h.symlink(target, filepath.Join(h.claude, "settings.json"))
	h.write(filepath.Join(h.root, "dotfiles", "claude.json"), `{"mcpServers": {"figma": {"url": "https://x"}}}`)
	h.symlink(filepath.Join(h.root, "dotfiles", "claude.json"), h.claudeJSON)
	h.write(filepath.Join(h.root, "dotfiles", "config.toml"), "model = \"gpt-5.5\"\n\n[mcp_servers.figma]\nurl = \"https://x\"\n")
	h.symlink(filepath.Join(h.root, "dotfiles", "config.toml"), filepath.Join(h.codex, "config.toml"))

	inv := mustScan(t, h.deps())
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSetting, "model"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSetting, "apiKeyHelper"), domain.LadoSetupStatusNever, domain.LadoSetupReasonLoginKey)
	env := entryAt(t, inv, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSetting, "env")
	wantStatus(t, env, domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
	if env.KeptInPlace != nil {
		t.Fatalf("env that is not adopted keeps %v in place", env.KeptInPlace)
	}
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationClaudeJSON, domain.LadoSetupKindMCPServer, "figma"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationCodexHome, domain.LadoSetupKindMCPServer, "figma"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
	wantStatus(t, entryAtPath(t, inv, filepath.Join(h.codex, "config.toml")+"#model"), domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonAOSetsPerSession)
}

// A file that was a link when it was opened is not hashed as the item's
// content, even if it looked like a file before.
func TestFileSumRefusesALink(t *testing.T) {
	h := newFakeHome(t)
	target := h.write(filepath.Join(h.root, "secret.txt"), "not the item")
	link := filepath.Join(h.claude, "CLAUDE.md")
	h.symlink(target, link)
	if _, _, err := fileSum(link); !errors.Is(err, errLink) {
		t.Fatalf("fileSum of a link = %v, want errLink", err)
	}
	if size, sum, err := fileSum(target); err != nil || sum != sumOf([]byte("not the item")) || size != 12 {
		t.Fatalf("fileSum of a file = %d, %s, %v", size, sum, err)
	}
}

// What a version cannot hold as it is, LADO lists with the reason: a link
// inside a folder item, a file name a tree manifest refuses, and files that
// do not parse. A root that does not parse keeps the error.
func TestInventoryRefusesWhatAVersionCannotHold(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a backslash is a path separator on Windows")
	}
	h := newFakeHome(t)
	h.write(filepath.Join(h.claude, "skills", "linked", "SKILL.md"), "skill")
	h.symlink("SKILL.md", filepath.Join(h.claude, "skills", "linked", "README.md"))
	h.write(filepath.Join(h.claude, "skills", "odd", `a\b.md`), "odd")
	h.write(filepath.Join(h.claude, "settings.json"), `{"model": `)
	h.write(filepath.Join(h.codex, "config.toml"), "model = [")
	h.write(h.claudeJSON, "{")

	inv := mustScan(t, h.deps())
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSkill, "linked"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonSymlink)
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSkill, "odd"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonUnreadable)
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationClaudeDir, "", "settings.json"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonUnreadable)
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationCodexHome, "", "config.toml"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonUnreadable)
	for _, root := range inv.Roots {
		if root.Location == domain.LadoSetupLocationClaudeJSON && (!root.Found || root.Error == "") {
			t.Fatalf("~/.claude.json root is %+v, want found with its error", root)
		}
	}
	for _, e := range inv.Entries {
		if e.Location == domain.LadoSetupLocationClaudeJSON {
			t.Fatalf("a ~/.claude.json that does not parse lists %q", e.Name)
		}
	}
}

// Plan decision e and HARNESS §9.8: login keys, the credentials file and
// Claude's state are never adopted, and the env setting's content leaves the
// login variables where they are.
func TestInventoryKeepsLoginKeysAndCredentialsOut(t *testing.T) {
	h := newFakeHome(t)
	h.write(filepath.Join(h.claude, ".credentials.json"), `{"claudeAiOauth": {"accessToken": "secret"}}`)
	h.write(filepath.Join(h.claude, "settings.json"), `{
  "apiKeyHelper": "pass claude",
  "awsAuthRefresh": "aws sso login",
  "awsCredentialExport": "aws-export",
  "forceLoginMethod": "claudeai",
  "forceLoginOrgUUID": "org",
  "env": {"ANTHROPIC_API_KEY": "sk-1", "EDITOR": "vim", "CLAUDE_CODE_OAUTH_TOKEN": "tok", "BASH_DEFAULT_TIMEOUT_MS": "60000"}
}`)
	state := []string{
		"projects", "sessions", "history.jsonl", "todos", "file-history", "shell-snapshots", "session-env", "paste-cache",
		"tasks", "plans", "transcripts", "statsig", "ide", "debug", "telemetry", "cache", "downloads", "backups",
		"stats-cache.json", ".session-stats.json", ".claude.json.lock",
	}
	for _, name := range state {
		if filepath.Ext(name) != "" {
			h.write(filepath.Join(h.claude, name), "state")
		} else {
			h.write(filepath.Join(h.claude, name, "x"), "state")
		}
	}
	h.write(h.claudeJSON, `{"oauthAccount": {"emailAddress": "me@example.com"}, "projects": {"/work": {"mcpServers": {"local": {}}}}}`)

	inv := mustScan(t, h.deps())
	claudeDir := domain.LadoSetupLocationClaudeDir

	for _, key := range domain.LadoSetupClaudeLoginKeys {
		wantStatus(t, entryAt(t, inv, claudeDir, domain.LadoSetupKindSetting, key), domain.LadoSetupStatusNever, domain.LadoSetupReasonLoginKey)
	}
	env := entryAt(t, inv, claudeDir, domain.LadoSetupKindSetting, "env")
	wantAdoptable(t, env, []byte(`{"BASH_DEFAULT_TIMEOUT_MS":"60000","EDITOR":"vim"}`), domain.LadoSetupActionMove)
	if want := []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}; !slices.Equal(env.KeptInPlace, want) {
		t.Fatalf("env keeps %v in place, want %v", env.KeptInPlace, want)
	}
	if env.Path != filepath.Join(h.claude, "settings.json")+"#env" || env.Reason != "" {
		t.Fatalf("env is at %s with reason %q", env.Path, env.Reason)
	}

	wantStatus(t, entryAt(t, inv, claudeDir, "", ".credentials.json"), domain.LadoSetupStatusNever, domain.LadoSetupReasonCredentials)
	for _, name := range state {
		wantStatus(t, entryAt(t, inv, claudeDir, "", name), domain.LadoSetupStatusNever, domain.LadoSetupReasonAppState)
	}
	// The rest of ~/.claude.json, local MCP servers included, is one line of
	// state.
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationClaudeJSON, "", ".claude.json"), domain.LadoSetupStatusNever, domain.LadoSetupReasonAppState)
	for _, e := range inv.Entries {
		if e.Location == domain.LadoSetupLocationClaudeJSON && e.Kind != "" {
			t.Fatalf("~/.claude.json without user MCP servers lists %s %q", e.Kind, e.Name)
		}
	}

	t.Run("an env of login variables only stays whole", func(t *testing.T) {
		h := newFakeHome(t)
		h.write(filepath.Join(h.claude, "settings.json"), `{"env": {"ANTHROPIC_BASE_URL": "https://proxy", "CLAUDE_CODE_USE_BEDROCK": "1"}}`)
		env := entryAt(t, mustScan(t, h.deps()), claudeDir, domain.LadoSetupKindSetting, "env")
		wantStatus(t, env, domain.LadoSetupStatusNever, domain.LadoSetupReasonLoginKey)
		if want := []string{"ANTHROPIC_BASE_URL", "CLAUDE_CODE_USE_BEDROCK"}; !slices.Equal(env.KeptInPlace, want) {
			t.Fatalf("env keeps %v in place, want %v", env.KeptInPlace, want)
		}
	})
}

// With CLAUDE_CONFIG_DIR, Claude's global config is the .claude.json inside
// the Claude folder. It is read once, as itself.
func TestInventoryReadsClaudeJSONInsideTheClaudeFolderOnce(t *testing.T) {
	h := newFakeHome(t)
	deps := h.deps()
	deps.ClaudeJSON, deps.ClaudeDirFromEnv = filepath.Join(h.claude, ".claude.json"), true
	h.write(deps.ClaudeJSON, `{"mcpServers": {"figma": {"url": "https://x"}}}`)

	inv := mustScan(t, deps)
	entryAt(t, inv, domain.LadoSetupLocationClaudeJSON, domain.LadoSetupKindMCPServer, "figma")
	for _, e := range inv.Entries {
		if e.Location == domain.LadoSetupLocationClaudeDir {
			t.Fatalf("the Claude folder lists %q", e.Name)
		}
	}
}

// Plan decisions d and e: of Codex, the instruction files and the MCP
// servers written as tables without secrets are adoptable; every other key
// and folder says why not.
func TestInventoryListsCodexItems(t *testing.T) {
	h := newFakeHome(t)
	agents := "# Codex\nRun the tests.\n"
	h.write(filepath.Join(h.codex, "AGENTS.md"), agents)
	h.write(filepath.Join(h.codex, "AGENTS.override.md"), "override")
	h.write(filepath.Join(h.codex, "config.toml"), `model = "gpt-5.5"
model_reasoning_effort = "high"
approval_policy = "on-request"

[projects."/work/app"]
trust_level = "trusted"

[notice]
hide_full_access_warning = true

[hooks.state]
seen = true

[skills]
enabled = true

[mcp_servers]
inline = { command = "npx", args = ["-y", "inline-server"] }

[mcp_servers.figma]
url = "https://mcp.figma.com/mcp"

[ mcp_servers . spaced ]  # written loosely
command = "spaced"

[mcp_servers.notion]
command = "npx"
args = ["-y", "@notionhq/notion-mcp-server"]
env_vars = ["NOTION_TOKEN"]
bearer_token_env_var = "NOTION_BEARER"

[mcp_servers.secret]
command = "secret-server"

[mcp_servers.secret.env]
API_KEY = "s3cr3t"

[mcp_servers.headers]
url = "https://headers.example"
http_headers = { Authorization = "Bearer x" }

[mcp_servers.token]
url = "https://token.example"
bearer_token = "abc"

[mcp_servers."bad name"]
command = "bad"
`)
	for _, dir := range []string{"skills", "plugins", "prompts", "rules", "secrets", "sessions", "log", "vendor_imports"} {
		h.write(filepath.Join(h.codex, dir, "x"), "x")
	}
	for _, file := range []string{"auth.json", "history.jsonl", "state_5.sqlite", "logs_2.sqlite-wal", ".personality_migration"} {
		h.write(filepath.Join(h.codex, file), "x")
	}

	inv := mustScan(t, h.deps())
	codexHome := domain.LadoSetupLocationCodexHome
	server := func(name string) domain.LadoSetupEntry {
		t.Helper()
		e := entryAt(t, inv, codexHome, domain.LadoSetupKindMCPServer, name)
		if e.Tool != domain.LadoSetupToolCodex || e.Path != filepath.Join(h.codex, "config.toml")+"#mcp_servers."+name {
			t.Fatalf("server %s is %s at %s", name, e.Tool, e.Path)
		}
		return e
	}

	agentsMD := entryAt(t, inv, codexHome, domain.LadoSetupKindInstructions, "AGENTS.md")
	wantAdoptable(t, agentsMD, []byte(agents), domain.LadoSetupActionMove)
	if agentsMD.Tool != domain.LadoSetupToolCodex {
		t.Fatalf("AGENTS.md is %s", agentsMD.Tool)
	}
	wantAdoptable(t, entryAt(t, inv, codexHome, domain.LadoSetupKindInstructions, "AGENTS.override.md"), []byte("override"), domain.LadoSetupActionMove)

	wantAdoptable(t, server("figma"), []byte(`{"url":"https://mcp.figma.com/mcp"}`), domain.LadoSetupActionMove)
	wantAdoptable(t, server("spaced"), []byte(`{"command":"spaced"}`), domain.LadoSetupActionMove)
	wantAdoptable(t, server("notion"), []byte(`{"args":["-y","@notionhq/notion-mcp-server"],"bearer_token_env_var":"NOTION_BEARER","command":"npx","env_vars":["NOTION_TOKEN"]}`), domain.LadoSetupActionMove)
	wantStatus(t, server("inline"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonInlineTable)
	for _, name := range []string{"secret", "headers", "token"} {
		wantStatus(t, server(name), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonLiteralCredentials)
	}
	wantStatus(t, server("bad name"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonNameUnsupported)

	for key, want := range map[string][2]string{
		"model":                  {string(domain.LadoSetupStatusNotManaged), string(domain.LadoSetupReasonAOSetsPerSession)},
		"model_reasoning_effort": {string(domain.LadoSetupStatusNotManaged), string(domain.LadoSetupReasonAOSetsPerSession)},
		"projects":               {string(domain.LadoSetupStatusNotManaged), string(domain.LadoSetupReasonToolState)},
		"notice":                 {string(domain.LadoSetupStatusNotManaged), string(domain.LadoSetupReasonToolState)},
		"hooks":                  {string(domain.LadoSetupStatusNotManaged), string(domain.LadoSetupReasonToolState)},
		"skills":                 {string(domain.LadoSetupStatusNotManaged), string(domain.LadoSetupReasonToolState)},
		"approval_policy":        {string(domain.LadoSetupStatusNotManaged), string(domain.LadoSetupReasonUnknownLocation)},
	} {
		e := entryAtPath(t, inv, filepath.Join(h.codex, "config.toml")+"#"+key)
		if e.Tool != domain.LadoSetupToolCodex || e.Kind != "" || e.Name != key || e.Location != codexHome {
			t.Fatalf("key %s is %+v", key, e)
		}
		wantStatus(t, e, domain.LadoSetupStatus(want[0]), domain.LadoSetupReason(want[1]))
	}

	for name, want := range map[string][2]string{
		"skills":                 {string(domain.LadoSetupStatusNotAdoptable), string(domain.LadoSetupReasonNoTUIPath)},
		"plugins":                {string(domain.LadoSetupStatusNotAdoptable), string(domain.LadoSetupReasonNoTUIPath)},
		"prompts":                {string(domain.LadoSetupStatusNotAdoptable), string(domain.LadoSetupReasonDeprecated)},
		"rules":                  {string(domain.LadoSetupStatusNotManaged), string(domain.LadoSetupReasonToolState)},
		"auth.json":              {string(domain.LadoSetupStatusNever), string(domain.LadoSetupReasonCredentials)},
		"secrets":                {string(domain.LadoSetupStatusNever), string(domain.LadoSetupReasonCredentials)},
		"sessions":               {string(domain.LadoSetupStatusNever), string(domain.LadoSetupReasonAppState)},
		"log":                    {string(domain.LadoSetupStatusNever), string(domain.LadoSetupReasonAppState)},
		"history.jsonl":          {string(domain.LadoSetupStatusNever), string(domain.LadoSetupReasonAppState)},
		"state_5.sqlite":         {string(domain.LadoSetupStatusNever), string(domain.LadoSetupReasonAppState)},
		"logs_2.sqlite-wal":      {string(domain.LadoSetupStatusNever), string(domain.LadoSetupReasonAppState)},
		".personality_migration": {string(domain.LadoSetupStatusNever), string(domain.LadoSetupReasonAppState)},
		"vendor_imports":         {string(domain.LadoSetupStatusNotManaged), string(domain.LadoSetupReasonUnknownLocation)},
	} {
		e := entryAtPath(t, inv, filepath.Join(h.codex, name))
		if e.Tool != domain.LadoSetupToolCodex || e.Kind != "" || e.Name != name || e.Location != codexHome {
			t.Fatalf("%s is %+v", name, e)
		}
		wantStatus(t, e, domain.LadoSetupStatus(want[0]), domain.LadoSetupReason(want[1]))
	}
}

// Plan §5: a live item is listed as managed with its item id and latest
// version, where the managed folder keeps it. Ended items are not listed, and
// an entry left at an original location stays adoptable.
func TestInventoryMarksManagedItems(t *testing.T) {
	ctx := context.Background()
	h := newFakeHome(t)
	store := sqlitetest.MustOpen(t)
	at := scanAt.Add(-time.Hour)
	event := func(action, subject string) domain.LadoAuditEvent {
		return domain.LadoAuditEvent{At: at, Actor: "human", Action: action, SubjectKind: "setup_item", SubjectID: subject}
	}
	skill := domain.LadoSetupItem{ID: "item-skill", Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSkill, Name: "review", OriginPath: filepath.Join(h.claude, "skills", "review"), CreatedAt: at}
	skillV1 := domain.LadoSetupItemVersion{ID: "item-skill-v1", ItemID: skill.ID, ContentKind: domain.LadoSetupContentTree, SHA256: sumOf([]byte("tree v1")), Size: 40, Actor: "human", CreatedAt: at}
	if err := store.RecordLadoSetupAdoption(ctx, domain.LadoSetupAdoption{ID: "adoption-1", CreatedAt: at, Actor: "human", OS: "linux", PlanSHA256: sumOf([]byte("plan")), State: domain.LadoSetupAdoptionCompleted},
		[]domain.LadoSetupItem{skill}, []domain.LadoSetupItemVersion{skillV1}, event("setup.adopted", "adoption-1"), nil); err != nil {
		t.Fatal(err)
	}
	skillV2 := domain.LadoSetupItemVersion{ID: "item-skill-v2", ItemID: skill.ID, ContentKind: domain.LadoSetupContentTree, SHA256: sumOf([]byte("tree v2")), Size: 41, Actor: "human", CreatedAt: at}
	if err := store.AddLadoSetupItemVersion(ctx, skill.ID, 1, skillV2, event("setup.item_updated", string(skill.ID)), nil); err != nil {
		t.Fatal(err)
	}
	server := domain.LadoSetupItem{ID: "item-figma", Tool: domain.LadoSetupToolCodex, Kind: domain.LadoSetupKindMCPServer, Name: "figma", CreatedAt: at}
	serverV1 := domain.LadoSetupItemVersion{ID: "item-figma-v1", ItemID: server.ID, ContentKind: domain.LadoSetupContentJSON, SHA256: sumOf([]byte(`{"url":"https://x"}`)), Size: 19, Actor: "human", CreatedAt: at}
	if err := store.CreateLadoSetupItem(ctx, server, serverV1, event("setup.item_created", string(server.ID)), nil); err != nil {
		t.Fatal(err)
	}
	gone := domain.LadoSetupItem{ID: "item-gone", Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindInstructions, Name: "OLD.md", CreatedAt: at}
	goneV1 := domain.LadoSetupItemVersion{ID: "item-gone-v1", ItemID: gone.ID, ContentKind: domain.LadoSetupContentFile, SHA256: sumOf([]byte("old")), Size: 3, Actor: "human", CreatedAt: at}
	if err := store.CreateLadoSetupItem(ctx, gone, goneV1, event("setup.item_created", string(gone.ID)), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteLadoSetupItem(ctx, gone.ID, at, event("setup.item_deleted", string(gone.ID))); err != nil {
		t.Fatal(err)
	}
	// The same skill appeared again where it came from.
	h.write(filepath.Join(h.claude, "skills", "review", "SKILL.md"), "new")

	deps := h.deps()
	deps.Store = store
	inv := mustScan(t, deps)

	managed := entryAt(t, inv, domain.LadoSetupLocationManaged, domain.LadoSetupKindSkill, "review")
	wantStatus(t, managed, domain.LadoSetupStatusManaged, "")
	if managed.ItemID != skill.ID || managed.SHA256 != skillV2.SHA256 || managed.Size != skillV2.Size || managed.Tool != domain.LadoSetupToolClaudeCode {
		t.Fatalf("managed skill is %+v, want item %s at version 2", managed, skill.ID)
	}
	if want := filepath.Join(h.state, "lado", "harness", "claude", "skills", "review"); managed.Path != want {
		t.Fatalf("managed skill is at %s, want %s", managed.Path, want)
	}
	figma := entryAt(t, inv, domain.LadoSetupLocationManaged, domain.LadoSetupKindMCPServer, "figma")
	wantStatus(t, figma, domain.LadoSetupStatusManaged, "")
	if figma.ItemID != server.ID || figma.SHA256 != serverV1.SHA256 || figma.Tool != domain.LadoSetupToolCodex {
		t.Fatalf("managed server is %+v, want item %s", figma, server.ID)
	}
	if want := filepath.Join(h.state, "lado", "harness", "codex", "config.toml") + "#mcp_servers.figma"; figma.Path != want {
		t.Fatalf("managed server is at %s, want %s", figma.Path, want)
	}
	for _, e := range inv.Entries {
		if e.ItemID == gone.ID || e.Name == gone.Name {
			t.Fatalf("a deleted item is listed: %+v", e)
		}
	}
	local := entryAt(t, inv, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSkill, "review")
	if local.Status != domain.LadoSetupStatusAdoptable || local.ItemID != "" {
		t.Fatalf("the skill at its original location is %s with item %q, want adoptable", local.Status, local.ItemID)
	}
	if len(inv.Adoptions) != 1 || inv.Adoptions[0].ID != "adoption-1" {
		t.Fatalf("adoptions %+v, want adoption-1", inv.Adoptions)
	}
}

// Plan decision 4: adoption is allowed only where delivery was measured,
// Linux for now.
func TestInventoryOSVerification(t *testing.T) {
	for goos, want := range map[string]bool{"linux": true, "windows": false, "darwin": false} {
		h := newFakeHome(t)
		deps := h.deps()
		deps.GOOS = goos
		inv := mustScan(t, deps)
		if inv.OS != goos {
			t.Fatalf("inventory OS is %q, want %q", inv.OS, goos)
		}
		for _, tool := range []domain.LadoSetupTool{domain.LadoSetupToolClaudeCode, domain.LadoSetupToolCodex} {
			if got, ok := inv.Verified[tool]; !ok || got != want {
				t.Fatalf("on %s %s is verified %v (listed %v), want %v", goos, tool, got, ok, want)
			}
		}
		if !inv.ScannedAt.Equal(scanAt) {
			t.Fatalf("scanned at %s, want %s", inv.ScannedAt, scanAt)
		}
	}
	deps := newFakeHome(t).deps()
	deps.GOOS = ""
	if inv := mustScan(t, deps); inv.OS != runtime.GOOS {
		t.Fatalf("without GOOS the inventory OS is %q, want %q", inv.OS, runtime.GOOS)
	}
}

// The login keys LADO keeps in place are the ones AO's account copier drops.
func TestClaudeLoginKeysMatchAgentAccount(t *testing.T) {
	if got := agentaccount.ClaudeLoginSettingKeys(); !slices.Equal(domain.LadoSetupClaudeLoginKeys, got) {
		t.Fatalf("domain.LadoSetupClaudeLoginKeys = %v, the account copier drops %v", domain.LadoSetupClaudeLoginKeys, got)
	}
	if got := agentaccount.ClaudeLoginEnvNames(); !slices.Equal(domain.LadoSetupClaudeLoginEnv, got) {
		t.Fatalf("domain.LadoSetupClaudeLoginEnv = %v, an account launch neutralizes %v", domain.LadoSetupClaudeLoginEnv, got)
	}
}

// A folder the daemon gave no path for, or that does not exist, lists
// nothing and is not an error.
func TestInventoryListsNothingForMissingFolders(t *testing.T) {
	h := newFakeHome(t)
	deps := h.deps()
	deps.ClaudeDir, deps.ClaudeJSON = "", ""
	inv := mustScan(t, deps)
	if len(inv.Entries) != 0 {
		t.Fatalf("entries without folders:\n%s", dumpEntries(inv))
	}
	want := []Root{
		{Tool: domain.LadoSetupToolClaudeCode, Location: domain.LadoSetupLocationClaudeDir},
		{Tool: domain.LadoSetupToolClaudeCode, Location: domain.LadoSetupLocationClaudeJSON},
		{Tool: domain.LadoSetupToolCodex, Location: domain.LadoSetupLocationCodexHome, Path: h.codex},
	}
	if !slices.Equal(inv.Roots, want) {
		t.Fatalf("roots %+v, want %+v", inv.Roots, want)
	}
}

// Inventory answers from the last scan until the next one, and a caller
// cannot change what it keeps.
func TestInventoryKeepsTheLastScan(t *testing.T) {
	ctx := context.Background()
	h := newFakeHome(t)
	h.write(filepath.Join(h.claude, "CLAUDE.md"), "v1")
	h.write(filepath.Join(h.claude, "settings.json"), `{"env": {"ANTHROPIC_API_KEY": "sk", "EDITOR": "vim"}}`)
	svc := New(h.deps())

	first, err := svc.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first.Entries {
		first.Entries[i].Name += " changed by a caller"
		if len(first.Entries[i].KeptInPlace) > 0 {
			first.Entries[i].KeptInPlace[0] = "changed by a caller"
		}
	}
	h.write(filepath.Join(h.claude, "CLAUDE.md"), "v2")
	again, err := svc.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if md := entryAt(t, again, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindInstructions, "CLAUDE.md"); md.SHA256 != sumOf([]byte("v1")) {
		t.Fatalf("Inventory read the folder again: %s", md.SHA256)
	}
	if env := entryAt(t, again, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSetting, "env"); !slices.Equal(env.KeptInPlace, []string{"ANTHROPIC_API_KEY"}) {
		t.Fatalf("a caller changed the kept scan: env keeps %v", env.KeptInPlace)
	}
	rescanned, err := svc.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if md := entryAt(t, rescanned, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindInstructions, "CLAUDE.md"); md.SHA256 != sumOf([]byte("v2")) {
		t.Fatalf("Scan kept the old content: %s", md.SHA256)
	}
	after, err := svc.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if md := entryAt(t, after, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindInstructions, "CLAUDE.md"); md.SHA256 != sumOf([]byte("v2")) {
		t.Fatalf("Inventory after Scan answers %s", md.SHA256)
	}
}
