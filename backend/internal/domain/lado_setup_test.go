package domain_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func ladoSetupSum(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestLadoSetupTreeIsCanonical(t *testing.T) {
	skill := ladoSetupSum("---\nname: review\n---\n")
	script := ladoSetupSum("#!/bin/sh\necho ok\n")
	notes := ladoSetupSum("notes")
	entries := []domain.LadoSetupTreeEntry{
		{Path: "scripts/run.sh", Mode: domain.LadoSetupModeExec, Size: 18, SHA256: script},
		{Path: "SKILL.md", Mode: domain.LadoSetupModeFile, Size: 21, SHA256: skill},
		{Path: "docs/a&b <notes>.md", Mode: domain.LadoSetupModeFile, Size: 5, SHA256: notes},
	}
	tree := domain.LadoSetupTree{Entries: entries}
	want := `{"entries":[` +
		`{"path":"SKILL.md","mode":"file","size":21,"sha256":"` + skill + `"},` +
		`{"path":"docs/a&b <notes>.md","mode":"file","size":5,"sha256":"` + notes + `"},` +
		`{"path":"scripts/run.sh","mode":"exec","size":18,"sha256":"` + script + `"}]}`
	got, err := tree.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if string(got) != want {
		t.Fatalf("CanonicalJSON = %s\nwant %s", got, want)
	}
	sum, err := tree.SHA256()
	if err != nil {
		t.Fatalf("SHA256: %v", err)
	}
	if sum != ladoSetupSum(want) {
		t.Fatalf("SHA256 = %s, want the sha256 of the canonical JSON", sum)
	}
	if entries[0].Path != "scripts/run.sh" {
		t.Fatalf("normalizing reordered the caller's entries: %+v", entries)
	}
	normalized, err := tree.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	var paths []string
	for _, e := range normalized.Entries {
		paths = append(paths, e.Path)
	}
	if want := []string{"SKILL.md", "docs/a&b <notes>.md", "scripts/run.sh"}; !slices.Equal(paths, want) {
		t.Fatalf("Normalize paths = %q, want %q", paths, want)
	}
	if got := tree.Size(); got != 18+21+5 {
		t.Fatalf("Size = %d, want the sum of the file sizes, 44", got)
	}

	// Another order gives the same bytes and hash.
	reversed := domain.LadoSetupTree{Entries: slices.Clone(entries)}
	slices.Reverse(reversed.Entries)
	if got, err := reversed.CanonicalJSON(); err != nil || string(got) != want {
		t.Fatalf("reversed CanonicalJSON = %s, %v\nwant %s", got, err, want)
	}
	if got, err := reversed.SHA256(); err != nil || got != sum {
		t.Fatalf("reversed SHA256 = %s, %v, want %s", got, err, sum)
	}

	// Any change to one file changes the hash.
	for name, edit := range map[string]func(*domain.LadoSetupTreeEntry){
		"content": func(e *domain.LadoSetupTreeEntry) { e.SHA256 = ladoSetupSum("#!/bin/sh\necho changed\n") },
		"size":    func(e *domain.LadoSetupTreeEntry) { e.Size++ },
		"mode":    func(e *domain.LadoSetupTreeEntry) { e.Mode = domain.LadoSetupModeFile },
		"path":    func(e *domain.LadoSetupTreeEntry) { e.Path = "scripts/start.sh" },
	} {
		changed := domain.LadoSetupTree{Entries: slices.Clone(entries)}
		edit(&changed.Entries[0])
		if got, err := changed.SHA256(); err != nil || got == sum {
			t.Errorf("a changed %s hashes %s, %v; want a hash other than %s", name, got, err, sum)
		}
	}
	if got, err := (domain.LadoSetupTree{Entries: entries[1:]}).SHA256(); err != nil || got == sum {
		t.Errorf("a tree without one file hashes %s, %v; want a hash other than %s", got, err, sum)
	}

	if got, err := (domain.LadoSetupTree{}).CanonicalJSON(); err != nil || string(got) != `{"entries":[]}` {
		t.Fatalf("empty tree CanonicalJSON = %s, %v; want {\"entries\":[]}", got, err)
	}
	// Names that share a prefix with a folder are no clash.
	siblings := domain.LadoSetupTree{Entries: []domain.LadoSetupTreeEntry{
		{Path: "a/b", Mode: domain.LadoSetupModeFile, Size: 1, SHA256: notes},
		{Path: "a-b", Mode: domain.LadoSetupModeFile, Size: 1, SHA256: notes},
		{Path: "a.md", Mode: domain.LadoSetupModeFile, Size: 1, SHA256: notes},
		{Path: "ab/a", Mode: domain.LadoSetupModeFile, Size: 1, SHA256: notes},
	}}
	if _, err := siblings.Normalize(); err != nil {
		t.Fatalf("siblings of a folder: Normalize = %v, want nil", err)
	}

	refused := func(name string, bad domain.LadoSetupTree) {
		t.Helper()
		if _, err := bad.Normalize(); !errors.Is(err, domain.ErrLadoSetupTreeInvalid) {
			t.Errorf("%s: Normalize = %v, want ErrLadoSetupTreeInvalid", name, err)
		}
		if _, err := bad.CanonicalJSON(); !errors.Is(err, domain.ErrLadoSetupTreeInvalid) {
			t.Errorf("%s: CanonicalJSON = %v, want ErrLadoSetupTreeInvalid", name, err)
		}
		if _, err := bad.SHA256(); !errors.Is(err, domain.ErrLadoSetupTreeInvalid) {
			t.Errorf("%s: SHA256 = %v, want ErrLadoSetupTreeInvalid", name, err)
		}
	}
	file := func(path string) domain.LadoSetupTreeEntry {
		return domain.LadoSetupTreeEntry{Path: path, Mode: domain.LadoSetupModeFile, Size: 5, SHA256: notes}
	}
	backslash := "scripts" + string(rune(0x5c)) + "run.sh"
	for name, path := range map[string]string{
		"empty":          "",
		"absolute":       "/etc/passwd",
		"backslash":      backslash,
		"parent":         "..",
		"parent folder":  "../outside",
		"parent inside":  "scripts/../../outside",
		"dot":            ".",
		"dot segment":    "./SKILL.md",
		"empty segment":  "scripts//run.sh",
		"trailing slash": "scripts/",
		"nul":            "bad" + string(rune(0)) + "name",
		"not utf-8":      string([]byte{0xff}) + "name",
	} {
		refused(name+" path", domain.LadoSetupTree{Entries: []domain.LadoSetupTreeEntry{file("SKILL.md"), file(path)}})
	}
	for name, paths := range map[string][]string{
		"duplicate":           {"SKILL.md", "SKILL.md"},
		"file is a folder":    {"scripts", "scripts/run.sh"},
		"folder is a file":    {"scripts/run.sh", "scripts"},
		"nested file clashes": {"a/b/c", "a/b"},
	} {
		bad := domain.LadoSetupTree{}
		for _, p := range paths {
			bad.Entries = append(bad.Entries, file(p))
		}
		refused(name, bad)
	}
	for name, edit := range map[string]func(*domain.LadoSetupTreeEntry){
		"unknown mode":     func(e *domain.LadoSetupTreeEntry) { e.Mode = "dir" },
		"no mode":          func(e *domain.LadoSetupTreeEntry) { e.Mode = "" },
		"negative size":    func(e *domain.LadoSetupTreeEntry) { e.Size = -1 },
		"uppercase sha256": func(e *domain.LadoSetupTreeEntry) { e.SHA256 = "AB" + notes[2:] },
		"short sha256":     func(e *domain.LadoSetupTreeEntry) { e.SHA256 = notes[:63] },
		"no sha256":        func(e *domain.LadoSetupTreeEntry) { e.SHA256 = "" },
	} {
		bad := domain.LadoSetupTree{Entries: []domain.LadoSetupTreeEntry{file("SKILL.md"), file("notes.md")}}
		edit(&bad.Entries[1])
		refused(name, bad)
	}
}

func TestLadoSetupBindingValidate(t *testing.T) {
	const (
		global  = domain.LadoScopeGlobal
		project = domain.LadoScopeProject
		task    = domain.LadoScopeTask
	)
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	binding := func(scope domain.LadoRuleScope, scopeID string, state domain.LadoBindingState) domain.LadoSetupBinding {
		return domain.LadoSetupBinding{ItemID: "lado-setup-item-1", Scope: scope, ScopeID: scopeID, State: state, UpdatedAt: at}
	}
	for _, b := range []domain.LadoSetupBinding{
		binding(global, "", domain.LadoBindingOn),
		binding(global, "", domain.LadoBindingOff),
		binding(project, "demo", domain.LadoBindingForceOn),
		binding(project, "demo", domain.LadoBindingForceOff),
		binding(task, "lado-task-1", domain.LadoBindingForceOn),
		binding(task, "lado-task-1", domain.LadoBindingForceOff),
	} {
		if err := b.Validate(); err != nil {
			t.Errorf("%+v: Validate = %v, want nil", b, err)
		}
	}
	noItem := binding(global, "", domain.LadoBindingOn)
	noItem.ItemID = ""
	for name, b := range map[string]domain.LadoSetupBinding{
		"global force_on":            binding(global, "", domain.LadoBindingForceOn),
		"global force_off":           binding(global, "", domain.LadoBindingForceOff),
		"global inherit":             binding(global, "", domain.LadoBindingInherit),
		"global without a state":     binding(global, "", ""),
		"global with a scope id":     binding(global, "demo", domain.LadoBindingOn),
		"project on":                 binding(project, "demo", domain.LadoBindingOn),
		"project off":                binding(project, "demo", domain.LadoBindingOff),
		"project inherit":            binding(project, "demo", domain.LadoBindingInherit),
		"project without a scope id": binding(project, "", domain.LadoBindingForceOn),
		"task on":                    binding(task, "lado-task-1", domain.LadoBindingOn),
		"task without a scope id":    binding(task, "", domain.LadoBindingForceOff),
		"unknown scope":              binding("workspace", "demo", domain.LadoBindingForceOn),
		"no scope":                   binding("", "", domain.LadoBindingOn),
		"no item":                    noItem,
	} {
		if err := b.Validate(); !errors.Is(err, domain.ErrLadoSetupBindingInvalid) {
			t.Errorf("%s: Validate = %v, want ErrLadoSetupBindingInvalid", name, err)
		}
	}
}

func TestLadoSetupItemLive(t *testing.T) {
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	adopted := domain.LadoSetupItem{
		ID:         "lado-setup-item-1",
		Tool:       domain.LadoSetupToolClaudeCode,
		Kind:       domain.LadoSetupKindSkill,
		Name:       "review",
		OriginPath: "/home/dev/.claude/skills/review",
		AdoptionID: "lado-setup-adoption-1",
		CreatedAt:  at,
	}
	created := domain.LadoSetupItem{
		ID:        "lado-setup-item-2",
		Tool:      domain.LadoSetupToolCodex,
		Kind:      domain.LadoSetupKindMCPServer,
		Name:      "figma",
		CreatedAt: at,
	}
	for name, item := range map[string]domain.LadoSetupItem{"adopted": adopted, "created in LADO": created} {
		if !item.Live() {
			t.Errorf("%s item %+v: Live = false, want true", name, item)
		}
	}
	deleted := adopted
	deleted.DeletedAt = at.Add(time.Hour)
	restored := adopted
	restored.RestoredAt = at.Add(2 * time.Hour)
	both := deleted
	both.RestoredAt = at.Add(2 * time.Hour)
	for name, item := range map[string]domain.LadoSetupItem{"deleted": deleted, "restored": restored, "deleted and restored": both} {
		if item.Live() {
			t.Errorf("%s item %+v: Live = true, want false", name, item)
		}
	}
}

func TestLadoSetupDeliveryVerified(t *testing.T) {
	for _, tool := range []domain.LadoSetupTool{domain.LadoSetupToolClaudeCode, domain.LadoSetupToolCodex} {
		if !domain.LadoSetupDeliveryVerified("linux", tool) {
			t.Errorf("LadoSetupDeliveryVerified(linux, %s) = false, want true: S01 measured it", tool)
		}
		for _, goos := range []string{"windows", "darwin", "freebsd", ""} {
			if domain.LadoSetupDeliveryVerified(goos, tool) {
				t.Errorf("LadoSetupDeliveryVerified(%q, %s) = true, want false until it is measured", goos, tool)
			}
		}
	}
	for _, tool := range []domain.LadoSetupTool{"aider", ""} {
		if domain.LadoSetupDeliveryVerified("linux", tool) {
			t.Errorf("LadoSetupDeliveryVerified(linux, %q) = true, want false for a tool LADO does not manage", tool)
		}
	}
}

func TestLadoSetupNeedsPresence(t *testing.T) {
	const (
		instructions = domain.LadoSetupKindInstructions
		setting      = domain.LadoSetupKindSetting
		skill        = domain.LadoSetupKindSkill
		agent        = domain.LadoSetupKindAgent
		command      = domain.LadoSetupKindCommand
		outputStyle  = domain.LadoSetupKindOutputStyle
		hookScript   = domain.LadoSetupKindHookScript
		mcpServer    = domain.LadoSetupKindMCPServer
	)
	for _, tc := range []struct {
		kind domain.LadoSetupKind
		name string
		want bool
	}{
		// Changes that can run code or widen access.
		{hookScript, "notify.sh", true},
		{hookScript, "lint", true},
		{mcpServer, "figma", true},
		{mcpServer, "notion", true},
		{setting, "hooks", true},
		{setting, "permissions", true},
		{setting, "env", true},
		{setting, "statusLine", true},
		{setting, "enabledPlugins", true},
		{setting, "extraKnownMarketplaces", true},
		{setting, "otelHeadersHelper", true},
		{setting, "enableAllProjectMcpServers", true},
		{setting, "enabledMcpjsonServers", true},
		// Everything else.
		{setting, "model", false},
		{setting, "modelSettings", false},
		{setting, "outputStyle", false},
		{setting, "includeCoAuthoredBy", false},
		{setting, "theme", false},
		{setting, "verbose", false},
		{instructions, "CLAUDE.md", false},
		{instructions, "AGENTS.md", false},
		{skill, "review", false},
		{agent, "reviewer.md", false},
		{command, "git/commit.md", false},
		{outputStyle, "terse.md", false},
		// The key names count only for settings.
		{skill, "hooks", false},
		{instructions, "env", false},
	} {
		if got := domain.LadoSetupNeedsPresence(tc.kind, tc.name); got != tc.want {
			t.Errorf("LadoSetupNeedsPresence(%s, %q) = %v, want %v", tc.kind, tc.name, got, tc.want)
		}
	}
}

func TestLadoSetupDefaultAction(t *testing.T) {
	const (
		claude = domain.LadoSetupToolClaudeCode
		codex  = domain.LadoSetupToolCodex
	)
	for _, key := range []string{
		"hooks", "permissions", "env", "model", "modelSettings", "statusLine", "outputStyle",
		"enabledPlugins", "extraKnownMarketplaces", "includeCoAuthoredBy",
	} {
		if got := domain.LadoSetupDefaultAction(claude, domain.LadoSetupKindSetting, key); got != domain.LadoSetupActionMove {
			t.Errorf("Claude setting %q defaults to %q, want move", key, got)
		}
	}
	for _, key := range []string{
		"theme", "verbose", "showClearContextOnPlanAccept", "inputNeededNotifEnabled", "agentPushNotifEnabled",
		"cleanupPeriodDays", "Hooks",
	} {
		if got := domain.LadoSetupDefaultAction(claude, domain.LadoSetupKindSetting, key); got != domain.LadoSetupActionKeep {
			t.Errorf("Claude setting %q defaults to %q, want keep", key, got)
		}
	}
	// The list names Claude's settings keys; a setting of another tool keeps.
	if got := domain.LadoSetupDefaultAction(codex, domain.LadoSetupKindSetting, "model"); got != domain.LadoSetupActionKeep {
		t.Errorf("Codex setting model defaults to %q, want keep", got)
	}
	for _, tc := range []struct {
		tool domain.LadoSetupTool
		kind domain.LadoSetupKind
		name string
	}{
		{claude, domain.LadoSetupKindInstructions, "CLAUDE.md"},
		{claude, domain.LadoSetupKindSkill, "review"},
		{claude, domain.LadoSetupKindAgent, "reviewer.md"},
		{claude, domain.LadoSetupKindCommand, "git/commit.md"},
		{claude, domain.LadoSetupKindOutputStyle, "terse.md"},
		{claude, domain.LadoSetupKindHookScript, "notify.sh"},
		{claude, domain.LadoSetupKindMCPServer, "figma"},
		{codex, domain.LadoSetupKindInstructions, "AGENTS.md"},
		{codex, domain.LadoSetupKindInstructions, "AGENTS.override.md"},
		{codex, domain.LadoSetupKindMCPServer, "notion"},
		// A key name means nothing outside settings.
		{claude, domain.LadoSetupKindSkill, "theme"},
	} {
		if got := domain.LadoSetupDefaultAction(tc.tool, tc.kind, tc.name); got != domain.LadoSetupActionMove {
			t.Errorf("%s %s %q defaults to %q, want move", tc.tool, tc.kind, tc.name, got)
		}
	}
}

// TestLadoSetupCodesAreStable pins the strings the database checks, the
// renderer shows in the developer's language and presence proofs sign.
func TestLadoSetupCodesAreStable(t *testing.T) {
	for group, codes := range map[string][][2]string{
		"tool": {
			{string(domain.LadoSetupToolClaudeCode), "claude-code"},
			{string(domain.LadoSetupToolCodex), "codex"},
		},
		"kind": {
			{string(domain.LadoSetupKindInstructions), "instructions"},
			{string(domain.LadoSetupKindSetting), "setting"},
			{string(domain.LadoSetupKindSkill), "skill"},
			{string(domain.LadoSetupKindAgent), "agent"},
			{string(domain.LadoSetupKindCommand), "command"},
			{string(domain.LadoSetupKindOutputStyle), "output_style"},
			{string(domain.LadoSetupKindHookScript), "hook_script"},
			{string(domain.LadoSetupKindMCPServer), "mcp_server"},
		},
		"content kind": {
			{string(domain.LadoSetupContentFile), "file"},
			{string(domain.LadoSetupContentJSON), "json"},
			{string(domain.LadoSetupContentTree), "tree"},
		},
		"file mode": {
			{string(domain.LadoSetupModeFile), "file"},
			{string(domain.LadoSetupModeExec), "exec"},
		},
		"adoption state": {
			{string(domain.LadoSetupAdoptionCompleted), "completed"},
			{string(domain.LadoSetupAdoptionPartial), "partial"},
		},
		"location": {
			{string(domain.LadoSetupLocationClaudeDir), "claude_dir"},
			{string(domain.LadoSetupLocationClaudeJSON), "claude_json"},
			{string(domain.LadoSetupLocationCodexHome), "codex_home"},
			{string(domain.LadoSetupLocationProject), "project"},
			{string(domain.LadoSetupLocationManaged), "managed"},
		},
		"status": {
			{string(domain.LadoSetupStatusManaged), "managed"},
			{string(domain.LadoSetupStatusAdoptable), "adoptable"},
			{string(domain.LadoSetupStatusNotAdoptable), "not_adoptable"},
			{string(domain.LadoSetupStatusNever), "never"},
			{string(domain.LadoSetupStatusNotManaged), "not_managed"},
			{string(domain.LadoSetupStatusRepoTracked), "repo_tracked"},
		},
		"reason": {
			{string(domain.LadoSetupReasonCredentials), "credentials"},
			{string(domain.LadoSetupReasonLoginKey), "login_key"},
			{string(domain.LadoSetupReasonAppState), "app_state"},
			{string(domain.LadoSetupReasonPluginsStay), "plugins_stay"},
			{string(domain.LadoSetupReasonToolState), "tool_state"},
			{string(domain.LadoSetupReasonNoTUIPath), "no_tui_path"},
			{string(domain.LadoSetupReasonDeprecated), "deprecated"},
			{string(domain.LadoSetupReasonInlineTable), "inline_table"},
			{string(domain.LadoSetupReasonLiteralCredentials), "literal_credentials"},
			{string(domain.LadoSetupReasonNameUnsupported), "name_unsupported"},
			{string(domain.LadoSetupReasonUnknownLocation), "unknown_location"},
			{string(domain.LadoSetupReasonWorktreesDoNotLoad), "worktrees_do_not_load"},
			{string(domain.LadoSetupReasonAOSetsPerSession), "ao_sets_per_session"},
			{string(domain.LadoSetupReasonRepoTracked), "repo_tracked"},
			{string(domain.LadoSetupReasonManagedSettings), "managed_settings"},
			{string(domain.LadoSetupReasonSymlink), "symlink"},
			{string(domain.LadoSetupReasonUnreadable), "unreadable"},
		},
		"action": {
			{string(domain.LadoSetupActionMove), "move"},
			{string(domain.LadoSetupActionKeep), "keep"},
		},
		"drift kind": {
			{string(domain.LadoSetupDriftEdited), "edited"},
			{string(domain.LadoSetupDriftNewLocal), "new_local"},
		},
		"presence action": {
			{domain.LadoPresenceSetupAdopt, "setup.adopt"},
			{domain.LadoPresenceSetupRestore, "setup.restore"},
			{domain.LadoPresenceSetupChange, "setup.change"},
		},
	} {
		for _, code := range codes {
			if code[0] != code[1] {
				t.Errorf("a %s code is %q, want %q", group, code[0], code[1])
			}
		}
	}
	// A tool is named as its agent harness.
	if domain.LadoSetupToolClaudeCode != domain.LadoSetupTool(domain.HarnessClaudeCode) || domain.LadoSetupToolCodex != domain.LadoSetupTool(domain.HarnessCodex) {
		t.Fatalf("setup tools %q, %q differ from the agent harnesses %q, %q",
			domain.LadoSetupToolClaudeCode, domain.LadoSetupToolCodex, domain.HarnessClaudeCode, domain.HarnessCodex)
	}
}
