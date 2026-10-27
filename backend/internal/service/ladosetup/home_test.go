package ladosetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agentaccount"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// homeAdoption adopts the fixture's Claude Code setup but agents/a.md and
// the model and theme settings, which stay in the Claude folder.
func homeAdoption(f *adoptionFixture, svc *Service) AdoptResult {
	f.t.Helper()
	return f.mustAdopt(svc,
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		claudeMove(domain.LadoSetupKindSetting, "env"),
		claudeMove(domain.LadoSetupKindSetting, "permissions"),
		claudeMove(domain.LadoSetupKindSetting, "hooks"),
		claudeMove(domain.LadoSetupKindSkill, "review"),
		claudeMove(domain.LadoSetupKindAgent, "team/b.md"),
		claudeMove(domain.LadoSetupKindCommand, "c.md"),
		claudeMove(domain.LadoSetupKindHookScript, "check.sh"),
		claudeMove(domain.LadoSetupKindOutputStyle, "terse.md"),
		claudeMove(domain.LadoSetupKindMCPServer, "docs"),
	)
}

func mustPrepare(t *testing.T, svc *Service, in ports.LadoSetupHomeInput) map[string]string {
	t.Helper()
	env, err := svc.PrepareClaudeHome(context.Background(), in)
	if err != nil {
		t.Fatalf("PrepareClaudeHome: %v", err)
	}
	return env
}

// sessionHome is where a session's config home is.
func (f *adoptionFixture) sessionHome(id domain.SessionID) string {
	return filepath.Join(f.state, "lado", "homes", string(id), "claude")
}

// treeDir is where the tree of an adopted folder item is made.
func (f *adoptionFixture) treeDir(res AdoptResult, name string) string {
	f.t.Helper()
	for _, it := range res.Items {
		if it.Name == name {
			return filepath.Join(f.state, "lado", "harness", "trees", it.SHA256)
		}
	}
	f.t.Fatalf("no adopted item %s", name)
	return ""
}

func wantLink(t *testing.T, path, target string) {
	t.Helper()
	if got, err := os.Readlink(path); err != nil || got != target {
		t.Fatalf("%s links to %q (%v), want %q", path, got, err, target)
	}
}

func wantFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		t.Fatalf("%s is %v (%v), want a regular file with mode %v", path, info.Mode(), err, mode)
	}
	if got := readFile(t, path); got != content {
		t.Fatalf("%s holds %q, want %q", path, got, content)
	}
}

// jsonKeys reads the members of the JSON object in the file at path.
func jsonKeys(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	keys, err := jsonObject([]byte(readFile(t, path)))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return keys
}

func wantJSON(t *testing.T, what string, got json.RawMessage, want string) {
	t.Helper()
	g, err := canonicalJSON(got)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	w, err := canonicalJSON([]byte(want))
	if err != nil {
		t.Fatal(err)
	}
	if string(g) != string(w) {
		t.Fatalf("%s is %s, want %s", what, g, w)
	}
}

// HARNESS §15 item 10: a session on the computer's own login and one on an
// account both get the managed items in a home of their own.
func TestClaudeHomeCarriesManagedItems(t *testing.T) {
	f := newAdoption(t)
	f.write(filepath.Join(f.claude, "skills", "other", "SKILL.md"), "other\n")
	f.write(filepath.Join(f.claude, "agents", "team", "c.md"), "agent c\n")
	f.write(filepath.Join(f.claude, "RULES.md"), "rules\n")
	svc := f.service()
	res := homeAdoption(f, svc)
	// Claude Code used directly brings some names back; the managed items
	// win the clash.
	f.write(filepath.Join(f.claude, "CLAUDE.md"), "# direct\n")
	f.write(filepath.Join(f.claude, "skills", "review", "SKILL.md"), "direct review\n")
	settings := filepath.Join(f.claude, "settings.json")
	f.write(settings, strings.Replace(readFile(t, settings), `"model"`, `"permissions": {"deny": ["Bash(rm)"]},
  "model"`, 1))
	f.write(f.claudeJSON, strings.Replace(readFile(t, f.claudeJSON), `"search"`, `"docs": {"command": "direct-docs"},
    "search"`, 1))
	review := f.treeDir(res, "review")

	t.Run("own login", func(t *testing.T) {
		env := mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "s-1", ProjectID: "p", Env: map[string]string{"PATH": "/bin"}})
		home := f.sessionHome("s-1")
		if len(env) != 2 || env["CLAUDE_CONFIG_DIR"] != home || env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != f.claude {
			t.Fatalf("env %v, want the home and the Claude folder for the login", env)
		}
		// State entries link to the Claude folder, which gets the missing ones.
		for _, name := range append(slices.Clone(claudeStateDirs), claudeStateFile) {
			wantLink(t, filepath.Join(home, name), filepath.Join(f.claude, name))
			if _, err := os.Stat(filepath.Join(f.claude, name)); err != nil {
				t.Fatalf("the Claude folder has no %s: %v", name, err)
			}
		}
		wantHomeItems(t, f, home, f.claude, review)
		// settings.json: the folder's file, login keys included, with the
		// managed keys; env keeps the login variable left in place.
		keys := jsonKeys(t, filepath.Join(home, "settings.json"))
		wantJSON(t, "env", keys["env"], `{"DEBUG":"1","ANTHROPIC_API_KEY":"sk-test"}`)
		wantJSON(t, "permissions", keys["permissions"], `{"allow":["Bash(ls)"]}`)
		wantJSON(t, "hooks", keys["hooks"], `{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"\"$CLAUDE_CONFIG_DIR\"/hooks/check.sh"}]}]}`)
		wantJSON(t, "apiKeyHelper", keys["apiKeyHelper"], `"~/bin/key.sh"`)
		wantJSON(t, "model", keys["model"], `"opus"`)
		// .claude.json: a copy of ~/.claude.json with the managed server.
		config := filepath.Join(home, ".claude.json")
		keys = jsonKeys(t, config)
		wantJSON(t, "numStartups", keys["numStartups"], `3`)
		wantJSON(t, "mcpServers", keys["mcpServers"], `{"search":{"url":"https://search"},"docs":{"command":"docs-mcp","args":["--port","7"]}}`)
		if info, err := os.Stat(config); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf(".claude.json mode %v (%v), want 0600", info.Mode(), err)
		}
	})

	t.Run("account", func(t *testing.T) {
		account := filepath.Join(f.root, "accounts", "work")
		if err := (&agentaccount.Claude{DeviceHome: f.claude, DeviceConfig: f.claudeJSON}).Prepare(account); err != nil {
			t.Fatal(err)
		}
		accountConfig := filepath.Join(account, ".claude.json")
		data, err := jsonWith([]byte(readFile(t, accountConfig)), "oauthAccount", []byte(`{"emailAddress":"work@example.com"}`))
		if err != nil {
			t.Fatal(err)
		}
		f.writeMode(accountConfig, string(data), 0o600)
		env := mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "s-2", ProjectID: "p", Env: map[string]string{
			"CLAUDE_CONFIG_DIR": account, "CLAUDE_SECURESTORAGE_CONFIG_DIR": account,
		}})
		home := f.sessionHome("s-2")
		if env["CLAUDE_CONFIG_DIR"] != home || env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != account {
			t.Fatalf("env %v, want the home and the account's folder for the login", env)
		}
		for _, name := range append(slices.Clone(claudeStateDirs), claudeStateFile) {
			wantLink(t, filepath.Join(home, name), filepath.Join(account, name))
		}
		wantHomeItems(t, f, home, account, review)
		keys := jsonKeys(t, filepath.Join(home, ".claude.json"))
		wantJSON(t, "oauthAccount", keys["oauthAccount"], `{"emailAddress":"work@example.com"}`)
		// The account's shared servers, and the managed one over the copy
		// AO shared.
		wantJSON(t, "mcpServers", keys["mcpServers"], `{"search":{"url":"https://search"},"docs":{"command":"docs-mcp","args":["--port","7"]}}`)
		if _, err := os.Lstat(filepath.Join(home, ".credentials.json")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("the home has a login file (%v)", err)
		}
	})
}

// wantHomeItems checks the merged folders and the Markdown of a home made
// from base after homeAdoption.
func wantHomeItems(t *testing.T, f *adoptionFixture, home, base, review string) {
	t.Helper()
	wantFile(t, filepath.Join(home, "CLAUDE.md"), "# me\n", 0o600)
	wantLink(t, filepath.Join(home, "RULES.md"), filepath.Join(base, "RULES.md"))
	wantLink(t, filepath.Join(home, "skills", "review"), review)
	wantFile(t, filepath.Join(review, "SKILL.md"), "review\n", 0o600)
	wantFile(t, filepath.Join(review, "scripts", "run.sh"), "#!/bin/sh\n", 0o700)
	wantLink(t, filepath.Join(home, "skills", "other"), filepath.Join(base, "skills", "other"))
	wantLink(t, filepath.Join(home, "agents", "a.md"), filepath.Join(base, "agents", "a.md"))
	wantFile(t, filepath.Join(home, "agents", "team", "b.md"), "agent b\n", 0o600)
	wantLink(t, filepath.Join(home, "agents", "team", "c.md"), filepath.Join(base, "agents", "team", "c.md"))
	wantFile(t, filepath.Join(home, "commands", "c.md"), "command c\n", 0o600)
	wantFile(t, filepath.Join(home, "hooks", "check.sh"), "#!/bin/sh\nexit 0\n", 0o700)
	wantFile(t, filepath.Join(home, "output-styles", "terse.md"), "terse\n", 0o600)
	for _, folder := range []string{"skills", "agents", "agents/team", "commands", "hooks", "output-styles"} {
		if info, err := os.Lstat(filepath.Join(home, folder)); err != nil || !info.IsDir() {
			t.Fatalf("%s is no real folder in the home (%v)", folder, err)
		}
	}
}

func TestClaudeHomeSecureStorageFollowsTheLogin(t *testing.T) {
	f := newAdoption(t)
	f.mustAdopt(f.service(), claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"))
	account := f.mkdir(filepath.Join(f.root, "accounts", "work"))
	for _, c := range []struct {
		name    string
		goos    string
		fromEnv bool
		env     map[string]string
		base    string
		want    string
	}{
		{name: "own login", goos: "linux", base: f.claude, want: f.claude},
		{name: "own login from CLAUDE_CONFIG_DIR", goos: "darwin", fromEnv: true, base: f.claude, want: f.claude},
		{name: "own login on macOS", goos: "darwin", base: f.claude, want: ""},
		{name: "account", goos: "darwin", env: map[string]string{"CLAUDE_CONFIG_DIR": account, "CLAUDE_SECURESTORAGE_CONFIG_DIR": account}, base: account, want: account},
		{name: "account with an empty login folder", goos: "linux", env: map[string]string{"CLAUDE_CONFIG_DIR": account, "CLAUDE_SECURESTORAGE_CONFIG_DIR": ""}, base: account, want: ""},
		{name: "folder without its login variable", goos: "linux", env: map[string]string{"CLAUDE_CONFIG_DIR": account + "/"}, base: account, want: account + "/"},
		{name: "account on Windows", goos: "windows", env: map[string]string{"Claude_Config_Dir": account, "claude_securestorage_config_dir": account}, base: account, want: account},
	} {
		t.Run(c.name, func(t *testing.T) {
			deps := f.deps
			deps.GOOS, deps.ClaudeDirFromEnv = c.goos, c.fromEnv
			id := domain.SessionID("s-" + strings.ReplaceAll(c.name, " ", "-"))
			env := mustPrepare(t, New(deps), ports.LadoSetupHomeInput{SessionID: id, Env: c.env})
			if got, ok := env["CLAUDE_SECURESTORAGE_CONFIG_DIR"]; !ok || got != c.want || env["CLAUDE_CONFIG_DIR"] != f.sessionHome(id) {
				t.Fatalf("env %v, want the login in %q", env, c.want)
			}
			wantLink(t, filepath.Join(f.sessionHome(id), "projects"), filepath.Join(c.base, "projects"))
		})
	}
	// An environment that names a LADO home already changes nothing.
	env := mustPrepare(t, f.service(), ports.LadoSetupHomeInput{SessionID: "s-again", Env: map[string]string{"CLAUDE_CONFIG_DIR": f.sessionHome("s-own-login")}})
	if len(env) != 0 {
		t.Fatalf("env %v for a launch on a LADO home, want nothing", env)
	}
}

func TestNoHomeWithoutManagedItems(t *testing.T) {
	f := newAdoption(t)
	svc := f.service()
	before := treeState(t, f.root)
	in := ports.LadoSetupHomeInput{SessionID: "s-1", ProjectID: "p", Env: map[string]string{"PATH": "/bin"}}
	if env := mustPrepare(t, svc, in); env != nil {
		t.Fatalf("env %v before any adoption, want none", env)
	}
	if diffs := diffStates(before, treeState(t, f.root)); len(diffs) > 0 {
		t.Fatalf("a launch without managed items wrote:\n%s", strings.Join(diffs, "\n"))
	}
	// Managed Codex setup alone gives Claude Code sessions no home either.
	f.mustAdopt(svc, codexMove(domain.LadoSetupKindMCPServer, "figma"))
	if env := mustPrepare(t, svc, in); env != nil {
		t.Fatalf("env %v with Codex items only, want none", env)
	}
	wantMissing(t, filepath.Join(f.state, "lado", "homes"))
	if err := svc.CleanupHomes(context.Background()); err != nil {
		t.Fatalf("CleanupHomes without homes: %v", err)
	}
}

// HARNESS §15 item 10, the other half: Claude Code used directly no longer
// loads what moved, and a session home does.
func TestDirectUseNoLongerSeesAdoptedItems(t *testing.T) {
	f := newAdoption(t)
	svc := f.service()
	res := homeAdoption(f, svc)
	for _, path := range []string{
		filepath.Join(f.claude, "CLAUDE.md"),
		filepath.Join(f.claude, "skills", "review"),
		filepath.Join(f.claude, "agents", "team", "b.md"),
		filepath.Join(f.claude, "commands", "c.md"),
		filepath.Join(f.claude, "hooks", "check.sh"),
		filepath.Join(f.claude, "output-styles", "terse.md"),
	} {
		wantMissing(t, path)
	}
	direct := jsonKeys(t, filepath.Join(f.claude, "settings.json"))
	wantJSON(t, "the Claude folder's env", direct["env"], `{"ANTHROPIC_API_KEY":"sk-test"}`)
	if _, ok := direct["permissions"]; ok {
		t.Fatalf("the Claude folder still sets permissions: %v", direct)
	}
	wantJSON(t, "~/.claude.json mcpServers", jsonKeys(t, f.claudeJSON)["mcpServers"], `{"search":{"url":"https://search"}}`)

	mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "s-1"})
	home := f.sessionHome("s-1")
	wantFile(t, filepath.Join(home, "CLAUDE.md"), "# me\n", 0o600)
	wantLink(t, filepath.Join(home, "skills", "review"), f.treeDir(res, "review"))
	wantFile(t, filepath.Join(home, "agents", "team", "b.md"), "agent b\n", 0o600)
	wantFile(t, filepath.Join(home, "commands", "c.md"), "command c\n", 0o600)
	wantFile(t, filepath.Join(home, "hooks", "check.sh"), "#!/bin/sh\nexit 0\n", 0o700)
	wantFile(t, filepath.Join(home, "output-styles", "terse.md"), "terse\n", 0o600)
	keys := jsonKeys(t, filepath.Join(home, "settings.json"))
	wantJSON(t, "the home's permissions", keys["permissions"], `{"allow":["Bash(ls)"]}`)
	wantJSON(t, "the home's mcpServers", jsonKeys(t, filepath.Join(home, ".claude.json"))["mcpServers"],
		`{"search":{"url":"https://search"},"docs":{"command":"docs-mcp","args":["--port","7"]}}`)
}

// HARNESS §15 item 11: AO's account linker, run after an adoption, creates
// nothing in the Claude folder, since adoption keeps the item folders.
func TestAccountLinkerCreatesNothingAfterAdoption(t *testing.T) {
	f := newAdoption(t)
	// A Claude folder Claude Code has run in holds every shared entry.
	for _, name := range append(slices.Clone(claudeStateDirs), "skills", "agents", "commands", "output-styles", "hooks") {
		f.mkdir(filepath.Join(f.claude, name))
	}
	svc := f.service()
	inv, err := svc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var entries []AdoptEntry
	for _, e := range inv.Entries {
		if e.Location == domain.LadoSetupLocationClaudeDir && e.Status == domain.LadoSetupStatusAdoptable {
			entries = append(entries, claudeMove(e.Kind, e.Name))
		}
	}
	f.mustAdopt(svc, entries...)
	if left := filepath.Join(f.claude, "CLAUDE.md"); fileExists(left) {
		t.Fatalf("%s is still there after the adoption", left)
	}
	before := treeState(t, f.claude)
	config := readFile(t, f.claudeJSON)

	account := filepath.Join(f.root, "accounts", "work")
	linker := &agentaccount.Claude{DeviceHome: f.claude, DeviceConfig: f.claudeJSON}
	for range 2 {
		if err := linker.Prepare(account); err != nil {
			t.Fatalf("Prepare: %v", err)
		}
	}
	if diffs := diffStates(before, treeState(t, f.claude)); len(diffs) > 0 {
		t.Fatalf("the account linker changed the Claude folder:\n%s", strings.Join(diffs, "\n"))
	}
	if got := readFile(t, f.claudeJSON); got != config {
		t.Fatalf("the account linker changed ~/.claude.json:\n%s", got)
	}
	// What the linker links is what a home links and merges.
	var linked []string
	list, err := os.ReadDir(account)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range list {
		if d.Type()&fs.ModeSymlink != 0 {
			linked = append(linked, d.Name())
		}
	}
	want := append(slices.Clone(claudeStateDirs), claudeStateFile)
	for _, folder := range claudeItemDirs {
		want = append(want, folder.name)
	}
	slices.Sort(linked)
	slices.Sort(want)
	if !slices.Equal(linked, want) {
		t.Fatalf("the account linker links %v, a home %v", linked, want)
	}
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// pinningStore is the real store with the setup an approval pinned for one
// execution, which the rule snapshot would hold, and another execution that
// pinned none.
type pinningStore struct {
	*recordingStore
	request, unpinned string
	pins              map[domain.LadoSetupItemID]int
}

func (p *pinningStore) LadoSetupVersionsForClientRequest(ctx context.Context, id string) ([]domain.LadoSetupItemWithVersion, bool, error) {
	if id != "" && id == p.unpinned {
		return nil, true, nil
	}
	if id != p.request {
		return p.recordingStore.LadoSetupVersionsForClientRequest(ctx, id)
	}
	var out []domain.LadoSetupItemWithVersion
	for itemID, number := range p.pins {
		item, versions, ok, err := p.LadoSetupItem(ctx, itemID)
		if err != nil || !ok {
			return nil, false, errors.Join(err, errors.New("no pinned item"))
		}
		out = append(out, domain.LadoSetupItemWithVersion{Item: item, Version: versions[number-1]})
	}
	return out, true, nil
}

// addVersion stores content as the next version of an item.
func (f *adoptionFixture) addVersion(itemID domain.LadoSetupItemID, base int, content string) {
	f.t.Helper()
	sum, err := f.deps.Blobs.Put([]byte(content))
	if err != nil {
		f.t.Fatal(err)
	}
	at := scanAt.Add(time.Hour)
	v := domain.LadoSetupItemVersion{
		ID: domain.LadoSetupItemVersionID(fmt.Sprintf("%s-v%d", itemID, base+1)), ItemID: itemID,
		ContentKind: domain.LadoSetupContentFile, SHA256: sum, Size: int64(len(content)), Actor: "api", CreatedAt: at,
	}
	if err := f.store.AddLadoSetupItemVersion(context.Background(), itemID, base, v,
		domain.LadoAuditEvent{At: at, Actor: "api", Action: "setup.item_updated", SubjectKind: "setup_item", SubjectID: string(itemID)}, nil); err != nil {
		f.t.Fatal(err)
	}
}

// HARNESS §15 item 12, the execution side: setup changed after an approval
// never reaches the execution's home.
func TestExecutionHomeUsesPinnedVersions(t *testing.T) {
	f := newAdoption(t)
	res := f.mustAdopt(f.service(),
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		claudeMove(domain.LadoSetupKindCommand, "c.md"),
	)
	ids := itemIDs(res)
	request := domain.LadoClientRequestID("lado-queue-1", 1)
	f.deps.Store = &pinningStore{recordingStore: f.store, request: request, pins: map[domain.LadoSetupItemID]int{ids["CLAUDE.md"]: 1, ids["c.md"]: 1}}
	svc := f.service()
	// After the approval: CLAUDE.md is edited, the command deleted and a new
	// item adopted.
	f.addVersion(ids["CLAUDE.md"], 1, "# edited\n")
	at := scanAt.Add(time.Hour)
	if err := f.store.DeleteLadoSetupItem(context.Background(), ids["c.md"], at,
		domain.LadoAuditEvent{At: at, Actor: "api", Action: "setup.item_deleted", SubjectKind: "setup_item", SubjectID: string(ids["c.md"])}); err != nil {
		t.Fatal(err)
	}
	f.mustAdopt(svc, claudeMove(domain.LadoSetupKindOutputStyle, "terse.md"))

	mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "worker", ProjectID: "p", ClientRequestID: request})
	execution := f.sessionHome("worker")
	wantFile(t, filepath.Join(execution, "CLAUDE.md"), "# me\n", 0o600)
	wantFile(t, filepath.Join(execution, "commands", "c.md"), "command c\n", 0o600)
	wantMissing(t, filepath.Join(execution, "output-styles", "terse.md"))

	// Any other session gets the setup as it is now.
	mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "other", ProjectID: "p", ClientRequestID: "ui-request-1"})
	other := f.sessionHome("other")
	wantFile(t, filepath.Join(other, "CLAUDE.md"), "# edited\n", 0o600)
	wantMissing(t, filepath.Join(other, "commands", "c.md"))
	wantFile(t, filepath.Join(other, "output-styles", "terse.md"), "terse\n", 0o600)
	// A rebuild of the execution keeps the pinned versions.
	mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "worker", ProjectID: "p", ClientRequestID: request})
	wantFile(t, filepath.Join(execution, "CLAUDE.md"), "# me\n", 0o600)
}

func setBinding(t *testing.T, f *adoptionFixture, item domain.LadoSetupItemID, scope domain.LadoRuleScope, scopeID string, state domain.LadoBindingState) {
	t.Helper()
	b := domain.LadoSetupBinding{ItemID: item, Scope: scope, ScopeID: scopeID, State: state, UpdatedAt: scanAt}
	if err := f.store.SetLadoSetupBinding(context.Background(), b, false,
		domain.LadoAuditEvent{At: scanAt, Actor: "api", Action: "setup.binding_set", SubjectKind: "setup_item", SubjectID: string(item)}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestHomeReconcileKeepsSessionFiles(t *testing.T) {
	f := newAdoption(t)
	f.write(filepath.Join(f.claude, "skills", "other", "SKILL.md"), "other\n")
	svc := f.service()
	res := homeAdoption(f, svc)
	ids := itemIDs(res)
	in := ports.LadoSetupHomeInput{SessionID: "s-1", ProjectID: "p"}
	mustPrepare(t, svc, in)
	home := f.sessionHome("s-1")

	// The session's Claude writes in its home, and AO records the
	// workspace's trust there.
	f.write(filepath.Join(home, "backups", "b.json"), "{}\n")
	f.write(filepath.Join(home, "skills", "made-here", "SKILL.md"), "made in the session\n")
	f.write(filepath.Join(home, ".claude.json.lock"), "")
	config := filepath.Join(home, ".claude.json")
	data := []byte(readFile(t, config))
	var err error
	for _, edit := range []struct{ key, value string }{
		{"tipsHistory", `{"x": 1}`},
		{"projects", `{"/ws": {"hasTrustDialogAccepted": true}}`},
	} {
		if data, err = jsonWith(data, edit.key, []byte(edit.value)); err != nil {
			t.Fatal(err)
		}
	}
	if data, err = jsonWithMCPServer(data, "added-here", []byte(`{"command":"mine"}`)); err != nil {
		t.Fatal(err)
	}
	f.writeMode(config, string(data), 0o600)

	// The command and the docs server are turned off, the hook script off
	// in this project only, and a base skill goes.
	setBinding(t, f, ids["c.md"], domain.LadoScopeGlobal, "", domain.LadoBindingOff)
	setBinding(t, f, ids["docs"], domain.LadoScopeGlobal, "", domain.LadoBindingOff)
	setBinding(t, f, ids["check.sh"], domain.LadoScopeProject, "p", domain.LadoBindingForceOff)
	if err := os.RemoveAll(filepath.Join(f.claude, "skills", "other")); err != nil {
		t.Fatal(err)
	}
	mustPrepare(t, svc, in)

	wantFile(t, filepath.Join(home, "backups", "b.json"), "{}\n", 0o644)
	wantFile(t, filepath.Join(home, "skills", "made-here", "SKILL.md"), "made in the session\n", 0o644)
	if !fileExists(filepath.Join(home, ".claude.json.lock")) {
		t.Fatal("the lock file Claude made is gone")
	}
	keys := jsonKeys(t, config)
	wantJSON(t, "tipsHistory", keys["tipsHistory"], `{"x":1}`)
	wantJSON(t, "projects", keys["projects"], `{"/ws":{"hasTrustDialogAccepted":true}}`)
	wantJSON(t, "mcpServers", keys["mcpServers"], `{"search":{"url":"https://search"},"added-here":{"command":"mine"}}`)
	wantMissing(t, filepath.Join(home, "commands", "c.md"))
	wantMissing(t, filepath.Join(home, "hooks", "check.sh"))
	wantMissing(t, filepath.Join(home, "skills", "other"))
	wantFile(t, filepath.Join(home, "CLAUDE.md"), "# me\n", 0o600)
	wantLink(t, filepath.Join(home, "skills", "review"), f.treeDir(res, "review"))
	// Taking links away never follows them.
	wantFile(t, filepath.Join(f.claude, "agents", "a.md"), "agent a\n", 0o644)

	// Another project still gets the hook script, and a session that turns
	// the command back on gets it again.
	mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "s-2", ProjectID: "q"})
	wantFile(t, filepath.Join(f.sessionHome("s-2"), "hooks", "check.sh"), "#!/bin/sh\nexit 0\n", 0o700)
	setBinding(t, f, ids["c.md"], domain.LadoScopeProject, "p", domain.LadoBindingForceOn)
	mustPrepare(t, svc, in)
	wantFile(t, filepath.Join(home, "commands", "c.md"), "command c\n", 0o600)
}

func TestHomesOfEndedSessionsAreRemoved(t *testing.T) {
	f := newAdoption(t)
	res := homeAdoption(f, f.service())
	now := scanAt
	f.deps.Clock = func() time.Time { return now }
	sessions := fakeSessions{
		{ID: "live", ProjectID: "p", Harness: domain.HarnessClaudeCode},
		{ID: "ended", ProjectID: "p", Harness: domain.HarnessClaudeCode},
		{ID: "restoring", ProjectID: "p", Harness: domain.HarnessClaudeCode, IsTerminated: true},
	}
	f.deps.Sessions = sessions
	svc := f.service()
	for _, id := range []domain.SessionID{"live", "ended", "gone"} {
		mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: id, ProjectID: "p"})
	}
	before := treeState(t, f.claude)
	tree := treeState(t, f.treeDir(res, "review"))

	// Later, the session ended and another session's row is gone; one that
	// a restore is building right now still reads as ended.
	now = now.Add(time.Hour)
	sessions[1].IsTerminated = true
	mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "restoring", ProjectID: "p"})
	if err := svc.CleanupHomes(context.Background()); err != nil {
		t.Fatalf("CleanupHomes: %v", err)
	}
	for _, id := range []domain.SessionID{"ended", "gone"} {
		wantMissing(t, filepath.Join(f.state, "lado", "homes", string(id)))
	}
	for _, id := range []domain.SessionID{"live", "restoring"} {
		wantLink(t, filepath.Join(f.sessionHome(id), "projects"), filepath.Join(f.claude, "projects"))
	}
	// What the removed homes linked to stays.
	if diffs := diffStates(before, treeState(t, f.claude)); len(diffs) > 0 {
		t.Fatalf("removing homes changed the Claude folder:\n%s", strings.Join(diffs, "\n"))
	}
	if diffs := diffStates(tree, treeState(t, f.treeDir(res, "review"))); len(diffs) > 0 {
		t.Fatalf("removing homes changed a tree:\n%s", strings.Join(diffs, "\n"))
	}
	// Once the grace is over, the ended session's home goes too.
	now = now.Add(homeGrace)
	if err := svc.CleanupHomes(context.Background()); err != nil {
		t.Fatalf("CleanupHomes: %v", err)
	}
	wantMissing(t, filepath.Join(f.state, "lado", "homes", "restoring"))
	wantLink(t, filepath.Join(f.sessionHome("live"), "projects"), filepath.Join(f.claude, "projects"))
}
