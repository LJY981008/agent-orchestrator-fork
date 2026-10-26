package ladosetup

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	claudeTool = domain.LadoSetupToolClaudeCode
	codexTool  = domain.LadoSetupToolCodex
)

// managedSetting is an on Claude Code setting item with a JSON value.
func managedSetting(id, key, value string) SetupContent {
	return SetupContent{ItemID: domain.LadoSetupItemID(id), Tool: claudeTool, Kind: domain.LadoSetupKindSetting, Name: key, SHA256: sha256Hex([]byte(value)), Content: []byte(value)}
}

// managedItem is an on item checked by its name only.
func managedItem(id string, tool domain.LadoSetupTool, kind domain.LadoSetupKind, name string) SetupContent {
	return SetupContent{ItemID: domain.LadoSetupItemID(id), Tool: tool, Kind: kind, Name: name, SHA256: sha256Hex([]byte(name))}
}

// localSetup is setup that stays at its original location.
func localSetup(c SetupContent, path string) SetupContent {
	c.ItemID, c.Path = "", path
	return c
}

func conflictsOf(t *testing.T, claudeDir string, in ConflictInput) []Conflict {
	t.Helper()
	return New(Deps{ClaudeDir: claudeDir}).Conflicts(in)
}

func withCode(conflicts []Conflict, code string) []Conflict {
	var out []Conflict
	for _, c := range conflicts {
		if c.Code == code {
			out = append(out, c)
		}
	}
	return out
}

func dumpConflicts(conflicts []Conflict) string {
	var b strings.Builder
	for _, c := range conflicts {
		b.WriteString("  " + c.Code + " " + string(c.Severity) + " item=" + string(c.ItemID) + " path=" + c.Path + " detail=" + c.Detail + " accepted=" + string(c.AcceptanceID) + "\n")
	}
	return b.String()
}

// TestConflictMCPServerNamedAO is HARNESS §15 item 13: an MCP server named
// ao, in any case and for either tool, blocks, and cannot be accepted.
func TestConflictMCPServerNamedAO(t *testing.T) {
	claudeAO := managedItem("claude-ao", claudeTool, domain.LadoSetupKindMCPServer, "ao")
	codexAO := managedItem("codex-ao", codexTool, domain.LadoSetupKindMCPServer, "Ao")
	localAO := localSetup(managedItem("", claudeTool, domain.LadoSetupKindMCPServer, "AO"), "/home/dev/.claude.json#mcpServers.AO")
	others := []SetupContent{
		managedItem("figma", codexTool, domain.LadoSetupKindMCPServer, "figma"),
		managedItem("aox", claudeTool, domain.LadoSetupKindMCPServer, "aox"),
		managedItem("ao-tools", claudeTool, domain.LadoSetupKindMCPServer, "ao-tools"),
	}
	in := ConflictInput{
		Setup: append([]SetupContent{claudeAO, codexAO, localAO}, others...),
		// A stale acceptance row does not make a blocking result accepted.
		Acceptances: []domain.LadoConflictAcceptance{{ID: "acc", Code: ConflictMCPNameTaken, ItemID: claudeAO.ItemID, ContentSHA256: claudeAO.SHA256, Scope: domain.LadoScopeGlobal}},
	}
	got := conflictsOf(t, "", in)
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3:\n%s", len(got), dumpConflicts(got))
	}
	for _, c := range got {
		if c.Code != ConflictMCPNameTaken || c.Severity != domain.LadoSeverityBlocking || c.Accepted() {
			t.Errorf("result %+v, want a blocking %s that is not accepted", c, ConflictMCPNameTaken)
		}
	}
	// Setup without an item sorts first.
	if local := got[0]; local.ItemID != "" || local.Path != localAO.Path || local.Details["origin"] != localAO.Path {
		t.Errorf("local result = %+v, want the server at %s", local, localAO.Path)
	}
	for i, want := range []SetupContent{claudeAO, codexAO} {
		if c := got[i+1]; c.ItemID != want.ItemID || c.Details["item"] != string(want.ItemID) || c.ContentSHA256 != want.SHA256 {
			t.Errorf("result %d = %+v, want item %s with its content", i+1, c, want.ItemID)
		}
	}
	if p := got[1].Problem(); p.Code != ConflictMCPNameTaken || p.Severity != domain.LadoSeverityBlocking || p.Details["item"] != string(claudeAO.ItemID) {
		t.Errorf("Problem() = %+v", p)
	}
	if err := checkAcceptable(ConflictMCPNameTaken); !errors.Is(err, ErrConflictNotAcceptable) {
		t.Errorf("checkAcceptable(%s) = %v, want ErrConflictNotAcceptable", ConflictMCPNameTaken, err)
	}
}

// TestConflictChecksTable runs each code of plan §6 on setup that has the
// conflict and on setup that does not.
func TestConflictChecksTable(t *testing.T) {
	claudeDir := filepath.Join(t.TempDir(), ".claude")
	absHook := filepath.ToSlash(claudeDir) + "/hooks/check.sh"
	moved := []string{"hooks/check.sh", "hooks/status.sh", "skills/review"}
	hooks := func(event, group string) string { return `{"` + event + `":[` + group + `]}` }
	command := func(cmd string) string { return `{"hooks":[{"type":"command","command":` + quoteJSON(cmd) + `}]}` }
	matcher := func(m string) string {
		return `{"matcher":` + quoteJSON(m) + `,"hooks":[{"type":"command","command":"true"}]}`
	}
	deny := func(rules ...string) string {
		quoted := make([]string, 0, len(rules))
		for _, r := range rules {
			quoted = append(quoted, quoteJSON(r))
		}
		return `{"deny":[` + strings.Join(quoted, ",") + `]}`
	}
	mode := func(m string) string { return `{"defaultMode":"` + m + `"}` }
	type row struct {
		name     string
		setup    SetupContent
		ceiling  domain.PermissionMode
		project  string
		code     string
		hit      bool
		severity domain.LadoProblemSeverity
		detail   string
	}
	const (
		blocking = domain.LadoSeverityBlocking
		warning  = domain.LadoSeverityWarning
		info     = domain.LadoSeverityInfo
	)
	rows := []row{
		{name: "mcp ao", setup: managedItem("s", claudeTool, domain.LadoSetupKindMCPServer, "AO"), code: ConflictMCPNameTaken, hit: true, severity: blocking},
		{name: "mcp other", setup: managedItem("s", codexTool, domain.LadoSetupKindMCPServer, "aos"), code: ConflictMCPNameTaken},

		{name: "skill using-ao", setup: managedItem("s", claudeTool, domain.LadoSetupKindSkill, "using-ao"), code: ConflictSkillNameTaken, hit: true, severity: blocking},
		{name: "command using-ao", setup: managedItem("s", claudeTool, domain.LadoSetupKindCommand, "Using-AO.md"), code: ConflictSkillNameTaken, hit: true, severity: blocking},
		{name: "skill using-ao-extra", setup: managedItem("s", claudeTool, domain.LadoSetupKindSkill, "using-ao-extra"), code: ConflictSkillNameTaken},
		{name: "command in a folder", setup: managedItem("s", claudeTool, domain.LadoSetupKindCommand, "tools/using-ao.md"), code: ConflictSkillNameTaken},
		{name: "agent using-ao", setup: managedItem("s", claudeTool, domain.LadoSetupKindAgent, "using-ao.md"), code: ConflictSkillNameTaken},

		{name: "env AO_", setup: managedSetting("env", "env", `{"AO_DATA_DIR":"/x","EDITOR":"vi"}`), code: ConflictEnvReserved, hit: true, severity: blocking, detail: "AO_DATA_DIR"},
		{name: "env CLAUDE_CONFIG_DIR", setup: managedSetting("env", "env", `{"CLAUDE_CONFIG_DIR":"/x"}`), code: ConflictEnvReserved, hit: true, severity: blocking, detail: "CLAUDE_CONFIG_DIR"},
		{name: "env secure storage", setup: managedSetting("env", "env", `{"CLAUDE_SECURESTORAGE_CONFIG_DIR":"/x"}`), code: ConflictEnvReserved, hit: true, severity: blocking, detail: "CLAUDE_SECURESTORAGE_CONFIG_DIR"},
		{name: "env CODEX_HOME", setup: managedSetting("env", "env", `{"codex_home":"/x"}`), code: ConflictEnvReserved, hit: true, severity: blocking, detail: "codex_home"},
		{name: "env others", setup: managedSetting("env", "env", `{"AOX":"1","MY_AO_DIR":"1","CLAUDE_CODE_MAX_OUTPUT_TOKENS":"1"}`), code: ConflictEnvReserved},

		{name: "deny Bash(ao:*)", setup: managedSetting("p", "permissions", deny("Bash(ao:*)")), code: ConflictDeniesAO, hit: true, severity: blocking, detail: "Bash(ao:*)"},
		{name: "deny Bash(ao *)", setup: managedSetting("p", "permissions", deny("Bash(ao *)")), code: ConflictDeniesAO, hit: true, severity: blocking, detail: "Bash(ao *)"},
		{name: "deny Bash(ao)", setup: managedSetting("p", "permissions", deny("Bash(ao)")), code: ConflictDeniesAO, hit: true, severity: blocking, detail: "Bash(ao)"},
		{name: "deny Bash(ao spawn:*)", setup: managedSetting("p", "permissions", deny("Bash(ao spawn:*)")), code: ConflictDeniesAO, hit: true, severity: blocking, detail: "Bash(ao spawn:*)"},
		{name: "deny mcp__ao", setup: managedSetting("p", "permissions", deny("mcp__ao")), code: ConflictDeniesAO, hit: true, severity: blocking, detail: "mcp__ao"},
		{name: "deny mcp__ao__*", setup: managedSetting("p", "permissions", deny("mcp__ao__*")), code: ConflictDeniesAO, hit: true, severity: blocking, detail: "mcp__ao__*"},
		{name: "deny mcp__ao__html_render", setup: managedSetting("p", "permissions", deny("mcp__ao__html_render")), code: ConflictDeniesAO, hit: true, severity: blocking, detail: "mcp__ao__html_render"},
		{name: "deny all of Bash", setup: managedSetting("p", "permissions", deny("Bash")), code: ConflictDeniesAO, hit: true, severity: blocking, detail: "Bash"},
		{name: "deny others", setup: managedSetting("p", "permissions", deny("Bash(aox:*)", "Bash(npm run *)", "mcp__aox", "mcp__figma__*", "Read(~/.ssh/**)")), code: ConflictDeniesAO},
		{name: "allow ao", setup: managedSetting("p", "permissions", `{"allow":["Bash(ao:*)","mcp__ao"]}`), code: ConflictDeniesAO},

		{name: "hook names a moved script", setup: managedSetting("h", "hooks", hooks("PreToolUse", command("bash ~/.claude/hooks/check.sh"))), code: ConflictHookPathMoved, hit: true, severity: blocking, detail: "PreToolUse hooks/check.sh"},
		{name: "hook names a moved script by $HOME", setup: managedSetting("h", "hooks", hooks("Stop", command(`"$HOME"/.claude/skills/review/run.sh`))), code: ConflictHookPathMoved, hit: true, severity: blocking, detail: "Stop skills/review"},
		{name: "hook names it by the folder", setup: localSetup(managedSetting("", "hooks", hooks("Stop", command("python3 '"+absHook+"'"))), "/home/dev/.claude/settings.json#hooks"), code: ConflictHookPathMoved, hit: true, severity: blocking, detail: "Stop hooks/check.sh"},
		{name: "status line names a moved script", setup: managedSetting("sl", "statusLine", `{"type":"command","command":"${HOME}/.claude/hooks/status.sh"}`), code: ConflictHookPathMoved, hit: true, severity: blocking, detail: "hooks/status.sh"},
		{name: "rewritten hook", setup: managedSetting("h", "hooks", hooks("PreToolUse", command(`bash "$CLAUDE_CONFIG_DIR"/hooks/check.sh`))), code: ConflictHookPathMoved},
		{name: "hook names a script that stays", setup: managedSetting("h", "hooks", hooks("PreToolUse", command("bash ~/.claude/hooks/other.sh"))), code: ConflictHookPathMoved},
		{name: "prompt hook", setup: managedSetting("h", "hooks", hooks("Stop", `{"hooks":[{"type":"prompt","prompt":"~/.claude/hooks/check.sh"}]}`)), code: ConflictHookPathMoved},

		{name: "deny Bash(git push:*)", setup: managedSetting("p", "permissions", deny("Bash(git push:*)")), code: ConflictDeniesGit, hit: true, severity: warning, detail: "Bash(git push:*)"},
		{name: "deny Bash(git *)", setup: managedSetting("p", "permissions", deny("Bash(git *)")), code: ConflictDeniesGit, hit: true, severity: warning, detail: "Bash(git *)"},
		{name: "deny Bash(gh pr merge:*)", setup: managedSetting("p", "permissions", deny("Bash(gh pr merge:*)")), code: ConflictDeniesGit, hit: true, severity: warning, detail: "Bash(gh pr merge:*)"},
		{name: "deny gitk and ghost", setup: managedSetting("p", "permissions", deny("Bash(gitk)", "Bash(ghost:*)", "Bash(ao:*)")), code: ConflictDeniesGit},

		{name: "PreToolUse Bash", setup: managedSetting("h", "hooks", hooks("PreToolUse", matcher("Bash"))), code: ConflictHookMayBlockTools, hit: true, severity: warning, detail: "Bash"},
		{name: "PreToolUse *", setup: managedSetting("h", "hooks", hooks("PreToolUse", matcher("*"))), code: ConflictHookMayBlockTools, hit: true, severity: warning, detail: "*"},
		{name: "PreToolUse without a matcher", setup: managedSetting("h", "hooks", hooks("PreToolUse", command("true"))), code: ConflictHookMayBlockTools, hit: true, severity: warning, detail: ""},
		{name: "PreToolUse .*", setup: managedSetting("h", "hooks", hooks("PreToolUse", matcher(".*"))), code: ConflictHookMayBlockTools, hit: true, severity: warning, detail: ".*"},
		{name: "PreToolUse Edit|Bash", setup: managedSetting("h", "hooks", hooks("PreToolUse", matcher("Edit|Bash"))), code: ConflictHookMayBlockTools, hit: true, severity: warning, detail: "Edit|Bash"},
		{name: "PreToolUse mcp__ao__.*", setup: managedSetting("h", "hooks", hooks("PreToolUse", matcher("mcp__ao__.*"))), code: ConflictHookMayBlockTools, hit: true, severity: warning, detail: "mcp__ao__.*"},
		{name: "PreToolUse mcp__ao__html_render", setup: managedSetting("h", "hooks", hooks("PreToolUse", matcher("mcp__ao__html_render"))), code: ConflictHookMayBlockTools, hit: true, severity: warning, detail: "mcp__ao__html_render"},
		{name: "PreToolUse Write|Edit", setup: managedSetting("h", "hooks", hooks("PreToolUse", matcher("Write|Edit"))), code: ConflictHookMayBlockTools},
		{name: "PreToolUse mcp__aox__.*", setup: managedSetting("h", "hooks", hooks("PreToolUse", matcher("mcp__aox__.*"))), code: ConflictHookMayBlockTools},
		{name: "PostToolUse Bash", setup: managedSetting("h", "hooks", hooks("PostToolUse", matcher("Bash"))), code: ConflictHookMayBlockTools},

		{name: "UserPromptSubmit command", setup: managedSetting("h", "hooks", hooks("UserPromptSubmit", command("cat notes.md"))), code: ConflictHookAddsPromptText, hit: true, severity: warning, detail: "UserPromptSubmit"},
		{name: "SessionStart command", setup: managedSetting("h", "hooks", hooks("SessionStart", matcher("startup"))), code: ConflictHookAddsPromptText, hit: true, severity: warning, detail: "SessionStart"},
		{name: "UserPromptSubmit prompt hook", setup: managedSetting("h", "hooks", hooks("UserPromptSubmit", `{"hooks":[{"type":"prompt","prompt":"check"}]}`)), code: ConflictHookAddsPromptText},
		{name: "Stop command", setup: managedSetting("h", "hooks", hooks("Stop", command("true"))), code: ConflictHookAddsPromptText},

		{name: "bypass over auto", setup: managedSetting("p", "permissions", mode("bypassPermissions")), ceiling: domain.PermissionModeAuto, code: ConflictDefaultModeAboveCeiling, hit: true, severity: warning},
		{name: "auto over accept edits", setup: managedSetting("p", "permissions", mode("auto")), ceiling: domain.PermissionModeAcceptEdits, code: ConflictDefaultModeAboveCeiling, hit: true, severity: warning},
		{name: "accept edits under auto", setup: managedSetting("p", "permissions", mode("acceptEdits")), ceiling: domain.PermissionModeAuto, code: ConflictDefaultModeAboveCeiling},
		{name: "auto at auto", setup: managedSetting("p", "permissions", mode("auto")), ceiling: domain.PermissionModeAuto, code: ConflictDefaultModeAboveCeiling},
		{name: "plan", setup: managedSetting("p", "permissions", mode("plan")), ceiling: domain.PermissionModeAcceptEdits, code: ConflictDefaultModeAboveCeiling},
		{name: "default", setup: managedSetting("p", "permissions", mode("default")), ceiling: domain.PermissionModeAcceptEdits, code: ConflictDefaultModeAboveCeiling},
		{name: "no ceiling given", setup: managedSetting("p", "permissions", mode("bypassPermissions")), code: ConflictDefaultModeAboveCeiling},

		{name: "hook on Stop", setup: managedSetting("h", "hooks", hooks("Stop", command("true"))), code: ConflictHookSharesAOEvent, hit: true, severity: info, detail: "Stop"},
		{name: "hook on PreCompact", setup: managedSetting("h", "hooks", hooks("PreCompact", command("true"))), code: ConflictHookSharesAOEvent},

		{name: "project sets model", setup: managedSetting("m", "model", `"opus"`), project: `{"model":"sonnet"}`, code: ConflictProjectOverrides, hit: true, severity: info},
		{name: "project sets other keys", setup: managedSetting("m", "model", `"opus"`), project: `{"permissions":{}}`, code: ConflictProjectOverrides},
		{name: "project without settings", setup: managedSetting("m", "model", `"opus"`), code: ConflictProjectOverrides},
		{name: "project settings that do not parse", setup: managedSetting("m", "model", `"opus"`), project: `{"model":`, code: ConflictProjectOverrides},
	}
	covered := map[string]bool{}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			in := ConflictInput{Setup: []SetupContent{r.setup}, Moved: moved, Ceiling: r.ceiling}
			if r.project != "" {
				in.ProjectID, in.ProjectSettings = "proj", []byte(r.project)
			}
			all := conflictsOf(t, claudeDir, in)
			got := withCode(all, r.code)
			if !r.hit {
				if len(got) != 0 {
					t.Fatalf("got %s, want none; all:\n%s", r.code, dumpConflicts(all))
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("got %d %s results, want 1; all:\n%s", len(got), r.code, dumpConflicts(all))
			}
			c := got[0]
			if c.Severity != r.severity || c.Detail != r.detail || c.ItemID != r.setup.ItemID || c.ContentSHA256 != r.setup.SHA256 {
				t.Errorf("result %+v, want severity %s, detail %q, item %q", c, r.severity, r.detail, r.setup.ItemID)
			}
		})
		if r.hit {
			covered[r.code] = true
		}
	}
	for code := range conflictSeverity {
		if code != ConflictPluginHooksUnreadable && !covered[code] {
			t.Errorf("no row finds %s", code)
		}
	}
}

func quoteJSON(s string) string {
	data, err := marshalCanonical(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// TestConflictPluginHooksAreChecked reads the hooks of the plugins
// enabledPlugins turns on: an enabled plugin's UserPromptSubmit hook is a
// warning, a disabled plugin is not read, and hooks that cannot be read are
// info.
func TestConflictPluginHooksAreChecked(t *testing.T) {
	h := newFakeHome(t)
	cache := filepath.Join(h.claude, "plugins", "cache", "market")
	prompt := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/hooks/inject.sh"}]}]}}`
	h.write(filepath.Join(cache, "notes", "1.0.0", "hooks", "hooks.json"), prompt)
	h.write(filepath.Join(cache, "off", "1.0.0", "hooks", "hooks.json"), prompt)
	h.write(filepath.Join(cache, "broken", "1.0.0", "hooks", "hooks.json"), `{"hooks":`)
	h.mkdir(filepath.Join(cache, "quiet", "1.0.0"))
	h.write(filepath.Join(cache, "old", "hooks", "hooks.json"), `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"guard"}]}]}}`)
	install := func(name, dir string) string {
		return quoteJSON(name) + `:[{"scope":"project","projectPath":"/elsewhere","installPath":"/elsewhere"},{"scope":"user","installPath":` + quoteJSON(dir) + `}]`
	}
	h.write(filepath.Join(h.claude, "plugins", "installed_plugins.json"), `{"version":2,"plugins":{`+
		install("notes@market", filepath.Join(cache, "notes", "1.0.0"))+","+
		install("off@market", filepath.Join(cache, "off", "1.0.0"))+","+
		install("broken@market", filepath.Join(cache, "broken", "1.0.0"))+","+
		install("quiet@market", filepath.Join(cache, "quiet", "1.0.0"))+","+
		// Version 1 of the file had one install per plugin.
		quoteJSON("old@market")+`:{"installPath":`+quoteJSON(filepath.Join(cache, "old"))+`}`+
		`}}`)
	enabled := managedSetting("plugins", "enabledPlugins", `{"notes@market":true,"off@market":false,"broken@market":true,"quiet@market":true,"old@market":true,"missing@market":true}`)
	in := ConflictInput{Setup: []SetupContent{enabled}}

	got := New(h.deps()).Conflicts(in)
	type key struct{ code, plugin, detail string }
	var keys []key
	for _, c := range got {
		if c.ItemID != enabled.ItemID || c.Details["item"] != string(enabled.ItemID) {
			t.Errorf("result %+v is not on the enabledPlugins item", c)
		}
		if c.Details["plugin"] == "off@market" {
			t.Errorf("a disabled plugin was checked: %+v", c)
		}
		keys = append(keys, key{c.Code, c.Details["plugin"], c.Detail})
	}
	want := []key{
		{ConflictHookAddsPromptText, "notes@market", "notes@market UserPromptSubmit"},
		{ConflictHookMayBlockTools, "old@market", "old@market Bash"},
		{ConflictHookSharesAOEvent, "notes@market", "notes@market UserPromptSubmit"},
		{ConflictHookSharesAOEvent, "old@market", "old@market PreToolUse"},
		{ConflictPluginHooksUnreadable, "broken@market", "broken@market"},
		{ConflictPluginHooksUnreadable, "missing@market", "missing@market"},
	}
	if !slices.Equal(keys, want) {
		t.Fatalf("results:\n%s\nwant %v", dumpConflicts(got), want)
	}
	if c := withCode(got, ConflictHookAddsPromptText)[0]; c.Severity != domain.LadoSeverityWarning || c.Details["event"] != "UserPromptSubmit" {
		t.Errorf("enabled plugin's prompt hook = %+v, want a warning on UserPromptSubmit", c)
	}
	for _, c := range withCode(got, ConflictPluginHooksUnreadable) {
		if c.Severity != domain.LadoSeverityInfo {
			t.Errorf("unreadable hooks = %+v, want info", c)
		}
	}

	t.Run("without the plugin list", func(t *testing.T) {
		h := newFakeHome(t)
		got := New(h.deps()).Conflicts(ConflictInput{Setup: []SetupContent{managedSetting("plugins", "enabledPlugins", `{"a@m":true,"b@m":true,"c@m":false}`)}})
		if len(got) != 2 || got[0].Code != ConflictPluginHooksUnreadable || got[0].Details["plugin"] != "a@m" || got[1].Details["plugin"] != "b@m" {
			t.Fatalf("results:\n%s\nwant info for a@m and b@m", dumpConflicts(got))
		}
	})
}

// TestConflictAcceptanceMatching marks a warning accepted only for the
// accepted item content, in the acceptance's scope and the narrower ones,
// and refuses to accept blocking and info results.
func TestConflictAcceptanceMatching(t *testing.T) {
	perms := managedSetting("perms", "permissions", `{"deny":["Bash(git push:*)","Bash(ao:*)"]}`)
	accept := func(id string, scope domain.LadoRuleScope, scopeID string) domain.LadoConflictAcceptance {
		return domain.LadoConflictAcceptance{
			ID: domain.LadoConflictAcceptanceID(id), Code: ConflictDeniesGit, ItemID: perms.ItemID, Detail: "Bash(git push:*)",
			ContentSHA256: perms.SHA256, Scope: scope, ScopeID: scopeID,
		}
	}
	project := ConflictInput{ProjectID: "p1"}
	task := ConflictInput{ProjectID: "p1", TaskID: "t1"}
	otherTask := ConflictInput{ProjectID: "p1", TaskID: "t2"}
	otherProject := ConflictInput{ProjectID: "p2", TaskID: "t3"}
	revoked := accept("revoked", domain.LadoScopeGlobal, "")
	revoked.RevokedAt = scanAt
	otherDetail := accept("other-detail", domain.LadoScopeGlobal, "")
	otherDetail.Detail = "Bash(git *)"
	otherItem := accept("other-item", domain.LadoScopeGlobal, "")
	otherItem.ItemID = "other"
	oldVersion := accept("old-version", domain.LadoScopeGlobal, "")
	oldVersion.ContentSHA256 = sha256Hex([]byte(`{"deny":["Bash(git push:*)"]}`))
	blockingRow := accept("blocking", domain.LadoScopeGlobal, "")
	blockingRow.Code, blockingRow.Detail = ConflictDeniesAO, "Bash(ao:*)"

	cases := []struct {
		name        string
		scope       ConflictInput
		acceptances []domain.LadoConflictAcceptance
		want        domain.LadoConflictAcceptanceID
	}{
		{"global covers global", ConflictInput{}, []domain.LadoConflictAcceptance{accept("g", domain.LadoScopeGlobal, "")}, "g"},
		{"global covers a project", project, []domain.LadoConflictAcceptance{accept("g", domain.LadoScopeGlobal, "")}, "g"},
		{"global covers a task", task, []domain.LadoConflictAcceptance{accept("g", domain.LadoScopeGlobal, "")}, "g"},
		{"a project covers itself", project, []domain.LadoConflictAcceptance{accept("p", domain.LadoScopeProject, "p1")}, "p"},
		{"a project covers its tasks", task, []domain.LadoConflictAcceptance{accept("p", domain.LadoScopeProject, "p1")}, "p"},
		{"a project does not cover Global", ConflictInput{}, []domain.LadoConflictAcceptance{accept("p", domain.LadoScopeProject, "p1")}, ""},
		{"a project does not cover another", otherProject, []domain.LadoConflictAcceptance{accept("p", domain.LadoScopeProject, "p1")}, ""},
		{"a task covers itself", task, []domain.LadoConflictAcceptance{accept("t", domain.LadoScopeTask, "t1")}, "t"},
		{"a task does not cover its project", project, []domain.LadoConflictAcceptance{accept("t", domain.LadoScopeTask, "t1")}, ""},
		{"a task does not cover another", otherTask, []domain.LadoConflictAcceptance{accept("t", domain.LadoScopeTask, "t1")}, ""},
		{"a new version is not accepted", project, []domain.LadoConflictAcceptance{oldVersion}, ""},
		{"a revoked acceptance holds nothing", project, []domain.LadoConflictAcceptance{revoked}, ""},
		{"another detail", project, []domain.LadoConflictAcceptance{otherDetail}, ""},
		{"another item", project, []domain.LadoConflictAcceptance{otherItem}, ""},
		{"the first that holds", task, []domain.LadoConflictAcceptance{revoked, accept("t", domain.LadoScopeTask, "t1"), accept("g", domain.LadoScopeGlobal, "")}, "t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.scope
			in.Setup = []SetupContent{perms}
			in.Acceptances = slices.Concat(tc.acceptances, []domain.LadoConflictAcceptance{blockingRow})
			got := conflictsOf(t, "", in)
			git := withCode(got, ConflictDeniesGit)
			if len(git) != 1 || git[0].AcceptanceID != tc.want || git[0].Accepted() != (tc.want != "") {
				t.Fatalf("results:\n%s\nwant %s accepted by %q", dumpConflicts(got), ConflictDeniesGit, tc.want)
			}
			if ao := withCode(got, ConflictDeniesAO); len(ao) != 1 || ao[0].Accepted() {
				t.Errorf("blocking result %+v, want it not accepted", ao)
			}
		})
	}

	t.Run("an edit drops the acceptance", func(t *testing.T) {
		edited := managedSetting("perms", "permissions", `{"deny":["Bash(git push:*)"]}`)
		got := conflictsOf(t, "", ConflictInput{Setup: []SetupContent{edited}, Acceptances: []domain.LadoConflictAcceptance{accept("g", domain.LadoScopeGlobal, "")}})
		if len(got) != 1 || got[0].Accepted() {
			t.Fatalf("results:\n%s\nwant the warning on the new version not accepted", dumpConflicts(got))
		}
	})

	t.Run("only warnings can be accepted", func(t *testing.T) {
		for code, severity := range conflictSeverity {
			err := checkAcceptable(code)
			if severity == domain.LadoSeverityWarning {
				if err != nil {
					t.Errorf("checkAcceptable(%s) = %v, want nil", code, err)
				}
				continue
			}
			if !errors.Is(err, ErrConflictNotAcceptable) {
				t.Errorf("checkAcceptable(%s) = %v, want ErrConflictNotAcceptable", code, err)
			}
		}
		if err := checkAcceptable("LADO_SETUP_UNKNOWN"); !errors.Is(err, ErrConflictNotAcceptable) {
			t.Errorf("checkAcceptable(unknown) = %v, want ErrConflictNotAcceptable", err)
		}
		if CodeConflictNotAcceptable != "LADO_SETUP_CONFLICT_NOT_ACCEPTABLE" {
			t.Errorf("CodeConflictNotAcceptable = %q", CodeConflictNotAcceptable)
		}
	})
}

// TestMovedPathReferences finds the four ways a command names a moved entry
// of the Claude folder, ignores entries that stay and a quoted "~", and
// gives replacements that keep the command's quoting.
func TestMovedPathReferences(t *testing.T) {
	claudeDir := filepath.Join(t.TempDir(), ".claude")
	abs := filepath.ToSlash(claudeDir)
	moved := []string{"hooks/check.sh", "skills/review", "CLAUDE.md"}
	cases := []struct {
		command   string
		names     []string
		rewritten string
	}{
		{"bash ~/.claude/hooks/check.sh --fast", []string{"hooks/check.sh"}, `bash "$CLAUDE_CONFIG_DIR"/hooks/check.sh --fast`},
		{`"$HOME/.claude/hooks/check.sh" arg`, []string{"hooks/check.sh"}, `"$CLAUDE_CONFIG_DIR/hooks/check.sh" arg`},
		{"${HOME}/.claude/skills/review/run.sh", []string{"skills/review"}, `"$CLAUDE_CONFIG_DIR"/skills/review/run.sh`},
		{"python3 '" + abs + "/hooks/check.sh'", []string{"hooks/check.sh"}, `python3 ''"$CLAUDE_CONFIG_DIR"'/hooks/check.sh'`},
		{"cat " + abs + "/CLAUDE.md", []string{"CLAUDE.md"}, `cat "$CLAUDE_CONFIG_DIR"/CLAUDE.md`},
		{`"$HOME"/.claude/hooks/check.sh`, []string{"hooks/check.sh"}, `"$CLAUDE_CONFIG_DIR"/hooks/check.sh`},
		{"X=~/.claude/hooks/check.sh run; cat ~/.claude/CLAUDE.md", []string{"hooks/check.sh", "CLAUDE.md"}, `X="$CLAUDE_CONFIG_DIR"/hooks/check.sh run; cat "$CLAUDE_CONFIG_DIR"/CLAUDE.md`},
		{`~/.claude/hooks/my\ check.sh`, nil, ""},
		// Entries that stay, and names that only start like a moved one.
		{"bash ~/.claude/hooks/other.sh", nil, ""},
		{"bash ~/.claude/hooks/check.sh.bak", nil, ""},
		{"cat ~/.claude/CLAUDE.md.orig ~/.claude/settings.json", nil, ""},
		{"ls ~/.claude/skills", nil, ""},
		// "~" in quotes, escaped or inside a word is not the home folder, and
		// single quotes keep $HOME as it is.
		{`bash "~/.claude/hooks/check.sh"`, nil, ""},
		{`bash '~/.claude/hooks/check.sh'`, nil, ""},
		{`bash \~/.claude/hooks/check.sh`, nil, ""},
		{`bash x~/.claude/hooks/check.sh`, nil, ""},
		{`bash '$HOME/.claude/hooks/check.sh'`, nil, ""},
		{`bash \$HOME/.claude/hooks/check.sh`, nil, ""},
		// Another folder that ends like the Claude folder.
		{"bash /x" + abs + "/hooks/check.sh", nil, ""},
		{`bash "$CLAUDE_CONFIG_DIR"/hooks/check.sh`, nil, ""},
	}
	for _, tc := range cases {
		refs := movedPathReferences(tc.command, claudeDir, moved)
		var names []string
		for _, r := range refs {
			names = append(names, r.Name)
		}
		if !slices.Equal(names, tc.names) {
			t.Errorf("movedPathReferences(%q) names %q, want %q", tc.command, names, tc.names)
			continue
		}
		if len(refs) == 0 {
			continue
		}
		rewritten := tc.command
		for i := len(refs) - 1; i >= 0; i-- {
			rewritten = rewritten[:refs[i].Start] + refs[i].Replacement + rewritten[refs[i].End:]
		}
		if rewritten != tc.rewritten {
			t.Errorf("rewriting %q gives %q, want %q", tc.command, rewritten, tc.rewritten)
		}
	}
	if refs := movedPathReferences(`bash ~/.claude/hooks/my\ check.sh`, claudeDir, []string{"hooks/my check.sh"}); len(refs) != 1 {
		t.Errorf("an escaped space in a name: %+v, want one reference", refs)
	}
	if refs := movedPathReferences("bash ~/.claude/hooks/check.sh", claudeDir, nil); refs != nil {
		t.Errorf("nothing moved: %+v, want none", refs)
	}
}
