package ladosetup

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Owner decision 2026-10-27: a home links every entry of its base but the
// few LADO handles itself, so a session keeps what it found in its folder.
func TestHomePassesTheRestOfTheFolderThrough(t *testing.T) {
	f := newAdoption(t)
	f.write(filepath.Join(f.claude, "keybindings.json"), "[]\n")
	f.write(filepath.Join(f.claude, "rules", "style.md"), "style\n")
	f.write(filepath.Join(f.claude, "mystery", "x"), "x\n")
	f.write(filepath.Join(f.claude, "backups", "b.json"), "{}\n")
	f.write(filepath.Join(f.claude, ".claude.json.lock"), "")
	svc := f.service()
	// Only a command among the item folders' kinds is managed.
	f.mustAdopt(svc, claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"), claudeMove(domain.LadoSetupKindCommand, "c.md"))
	mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "s-1", ProjectID: "p"})
	home := f.sessionHome("s-1")
	for _, name := range []string{"keybindings.json", "rules", "mystery", "skills", "agents", "output-styles", "hooks", "projects", "plugins", "history.jsonl"} {
		wantLink(t, filepath.Join(home, name), filepath.Join(f.claude, name))
	}
	for _, name := range []string{".credentials.json", "backups", ".claude.json.lock"} {
		wantMissing(t, filepath.Join(home, name))
	}
	wantFile(t, filepath.Join(home, "CLAUDE.md"), "# me\n", 0o600)
	if info, err := os.Lstat(filepath.Join(home, "commands")); err != nil || !info.IsDir() {
		t.Fatalf("commands is no real folder in the home (%v)", err)
	}
	wantFile(t, filepath.Join(home, "commands", "c.md"), "command c\n", 0o600)

	// What the session makes in a linked folder lands in the base.
	f.write(filepath.Join(home, "agents", "made.md"), "made\n")
	wantFile(t, filepath.Join(f.claude, "agents", "made.md"), "made\n", 0o644)
	// Once agents are managed, agents/ is merged, and the agent the session
	// made is a link there.
	f.mustAdopt(svc, claudeMove(domain.LadoSetupKindAgent, "a.md"))
	mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "s-1", ProjectID: "p"})
	wantFile(t, filepath.Join(home, "agents", "a.md"), "agent a\n", 0o600)
	wantLink(t, filepath.Join(home, "agents", "made.md"), filepath.Join(f.claude, "agents", "made.md"))
	wantLink(t, filepath.Join(home, "agents", "team"), filepath.Join(f.claude, "agents", "team"))

	// A base without an item folder gets it first, as it gets the state
	// entries.
	account := f.mkdir(filepath.Join(f.root, "accounts", "work"))
	mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "s-2", Env: map[string]string{"CLAUDE_CONFIG_DIR": account, "CLAUDE_SECURESTORAGE_CONFIG_DIR": account}})
	for _, name := range []string{"skills", "output-styles", "hooks", "projects"} {
		wantLink(t, filepath.Join(f.sessionHome("s-2"), name), filepath.Join(account, name))
		if info, err := os.Stat(filepath.Join(account, name)); err != nil || !info.IsDir() {
			t.Fatalf("the account has no %s folder (%v)", name, err)
		}
	}
}

// driftOf finds the drift at path, failing the test when there is none.
func driftOf(t *testing.T, drift []domain.LadoSetupDrift, kind domain.LadoSetupDriftKind, path string) domain.LadoSetupDrift {
	t.Helper()
	for _, d := range drift {
		if d.Kind == kind && d.Path == path {
			return d
		}
	}
	t.Fatalf("no %s drift at %s in %+v", kind, path, drift)
	return domain.LadoSetupDrift{}
}

func TestCleanupRescuesWhatSessionsCreated(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	now := scanAt
	f.deps.Clock = func() time.Time { return now }
	svc := f.service()
	ids := itemIDs(homeAdoption(f, svc))
	account := f.mkdir(filepath.Join(f.root, "accounts", "work"))
	mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "s-1", ProjectID: "p"})
	mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "s-2", Env: map[string]string{"CLAUDE_CONFIG_DIR": account, "CLAUDE_SECURESTORAGE_CONFIG_DIR": account}})
	home, managed := f.sessionHome("s-1"), filepath.Join(f.state, "lado", "harness", "claude")

	// The session makes an agent and a skill, adds a server and changes its
	// memory and a command, whose managed copy was changed by hand as well.
	// Meanwhile the Claude folder gets an agent of a name the session used.
	f.write(filepath.Join(home, "agents", "new.md"), "new agent\n")
	f.write(filepath.Join(home, "agents", "later.md"), "the session's\n")
	f.write(filepath.Join(f.claude, "agents", "later.md"), "used directly\n")
	f.write(filepath.Join(home, "skills", "made-here", "SKILL.md"), "made here\n")
	f.write(filepath.Join(home, "CLAUDE.md"), "# me, and a memory\n")
	f.write(filepath.Join(home, "commands", "c.md"), "command c, changed in the session\n")
	f.write(filepath.Join(managed, "commands", "c.md"), "command c, changed by hand\n")
	config := filepath.Join(home, ".claude.json")
	data, err := jsonWithMCPServer([]byte(readFile(t, config)), "added-here", []byte(`{"command":"mine"}`))
	if err != nil {
		t.Fatal(err)
	}
	f.writeMode(config, string(data), 0o600)
	// The account's skills/ is a file, so its session's skill cannot go.
	f.write(filepath.Join(f.sessionHome("s-2"), "skills", "made-there", "SKILL.md"), "made there\n")
	if err := os.RemoveAll(filepath.Join(account, "skills")); err != nil {
		t.Fatal(err)
	}
	f.write(filepath.Join(account, "skills"), "not a folder\n")

	now = now.Add(time.Hour)
	if err := svc.CleanupHomes(ctx); err == nil {
		t.Fatal("CleanupHomes reported no rescue failure")
	}
	wantMissing(t, filepath.Join(f.state, "lado", "homes", "s-1"))
	wantFile(t, filepath.Join(f.claude, "agents", "new.md"), "new agent\n", 0o644)
	wantFile(t, filepath.Join(f.claude, "agents", "later.md"), "used directly\n", 0o644)
	wantFile(t, filepath.Join(f.claude, "agents", "later.md.lado-restored"), "the session's\n", 0o644)
	wantFile(t, filepath.Join(f.claude, "skills", "made-here", "SKILL.md"), "made here\n", 0o644)
	if got := readFile(t, filepath.Join(managed, "CLAUDE.md")); got != "# me, and a memory\n" {
		t.Fatalf("the managed CLAUDE.md holds %q, want the session's memory", got)
	}
	if got := readFile(t, filepath.Join(managed, "commands", "c.md")); got != "command c, changed by hand\n" {
		t.Fatalf("the managed command holds %q, want the change by hand", got)
	}
	if got := readFile(t, filepath.Join(managed, "commands", "c.md.lado-session-s-1")); got != "command c, changed in the session\n" {
		t.Fatalf("the session's command beside it holds %q", got)
	}
	wantJSON(t, "~/.claude.json mcpServers", jsonKeys(t, f.claudeJSON)["mcpServers"], `{"search":{"url":"https://search"},"added-here":{"command":"mine"}}`)
	// The account's home stays for the next cleanup.
	wantFile(t, filepath.Join(f.sessionHome("s-2"), "skills", "made-there", "SKILL.md"), "made there\n", 0o644)

	// The next scan shows what was rescued.
	drift := mustDrift(t, svc)
	for _, path := range []string{filepath.Join(f.claude, "agents", "new.md"), filepath.Join(f.claude, "skills", "made-here"), f.claudeJSON + "#mcpServers.added-here"} {
		driftOf(t, drift, domain.LadoSetupDriftNewLocal, path)
	}
	if d := driftOf(t, drift, domain.LadoSetupDriftEdited, filepath.Join(managed, "CLAUDE.md")); d.ItemID != ids["CLAUDE.md"] {
		t.Fatalf("CLAUDE.md drift %+v", d)
	}
	driftOf(t, drift, domain.LadoSetupDriftEdited, filepath.Join(managed, "commands", "c.md"))

	// Once the account can take the skill, the next cleanup finishes.
	if err := os.Remove(filepath.Join(account, "skills")); err != nil {
		t.Fatal(err)
	}
	f.mkdir(filepath.Join(account, "skills"))
	if err := svc.CleanupHomes(ctx); err != nil {
		t.Fatalf("CleanupHomes: %v", err)
	}
	wantMissing(t, filepath.Join(f.state, "lado", "homes", "s-2"))
	wantFile(t, filepath.Join(account, "skills", "made-there", "SKILL.md"), "made there\n", 0o644)
}

func TestExecutionHomeUsesPinsWithoutLiveItems(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	ids := itemIDs(f.mustAdopt(f.service(), claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md")))
	request, early := domain.LadoClientRequestID("lado-queue-1", 1), domain.LadoClientRequestID("lado-queue-2", 1)
	f.deps.Store = &pinningStore{recordingStore: f.store, request: request, unpinned: early, pins: map[domain.LadoSetupItemID]int{ids["CLAUDE.md"]: 1}}
	svc := f.service()
	deleteItem := func(id domain.LadoSetupItemID) {
		at := scanAt.Add(time.Hour)
		if err := f.store.DeleteLadoSetupItem(ctx, id, at, domain.LadoAuditEvent{At: at, Actor: "api", Action: "setup.item_deleted", SubjectKind: "setup_item", SubjectID: string(id)}); err != nil {
			t.Fatal(err)
		}
	}
	// After the approval the item is deleted: nothing is live, and the
	// execution still gets what its rules pinned.
	deleteItem(ids["CLAUDE.md"])
	env := mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "worker", ProjectID: "p", ClientRequestID: request})
	if env["CLAUDE_CONFIG_DIR"] != f.sessionHome("worker") {
		t.Fatalf("env %v, want the worker's home", env)
	}
	wantFile(t, filepath.Join(f.sessionHome("worker"), "CLAUDE.md"), "# me\n", 0o600)
	if env := mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "other", ProjectID: "p"}); env != nil {
		t.Fatalf("env %v for a session outside any execution, want none", env)
	}

	// An execution approved before the adoption pinned nothing: it gets the
	// setup as it is now.
	cmd := itemIDs(f.mustAdopt(svc, claudeMove(domain.LadoSetupKindCommand, "c.md")))["c.md"]
	mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "early", ProjectID: "p", ClientRequestID: early})
	wantFile(t, filepath.Join(f.sessionHome("early"), "commands", "c.md"), "command c\n", 0o600)
	wantMissing(t, filepath.Join(f.sessionHome("early"), "CLAUDE.md"))
	// With nothing live and nothing pinned, it launches as it would without
	// LADO.
	deleteItem(cmd)
	if env := mustPrepare(t, svc, ports.LadoSetupHomeInput{SessionID: "early-2", ProjectID: "p", ClientRequestID: early}); env != nil {
		t.Fatalf("env %v with nothing pinned or live, want none", env)
	}
}

// accountWith makes an account folder whose .claude.json holds config.
func (f *adoptionFixture) accountWith(name, config string) string {
	f.t.Helper()
	dir := f.mkdir(filepath.Join(f.root, "accounts", name))
	f.writeMode(filepath.Join(dir, ".claude.json"), config, 0o600)
	return dir
}

func TestHomeRecopiesConfigWhenTheBaseChanges(t *testing.T) {
	f := newAdoption(t)
	svc := f.service()
	f.mustAdopt(svc, claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"), claudeMove(domain.LadoSetupKindMCPServer, "docs"))
	work := f.accountWith("work", `{"oauthAccount": {"emailAddress": "work@example.com"}, "mcpServers": {"w": {"url": "https://w"}}}`)
	other := f.accountWith("other", `{"oauthAccount": {"emailAddress": "other@example.com"}, "mcpServers": {"o": {"url": "https://o"}}}`)
	on := func(account string) ports.LadoSetupHomeInput {
		return ports.LadoSetupHomeInput{SessionID: "s-1", Env: map[string]string{"CLAUDE_CONFIG_DIR": account, "CLAUDE_SECURESTORAGE_CONFIG_DIR": account}}
	}
	mustPrepare(t, svc, on(work))
	config := filepath.Join(f.sessionHome("s-1"), ".claude.json")
	data, err := jsonWithMCPServer([]byte(readFile(t, config)), "mine", []byte(`{"command":"mine"}`))
	if err != nil {
		t.Fatal(err)
	}
	f.writeMode(config, string(data), 0o600)

	// The session moves to the other account.
	mustPrepare(t, svc, on(other))
	keys := jsonKeys(t, config)
	wantJSON(t, "oauthAccount", keys["oauthAccount"], `{"emailAddress":"other@example.com"}`)
	wantJSON(t, "mcpServers", keys["mcpServers"], `{"o":{"url":"https://o"},"docs":{"command":"docs-mcp","args":["--port","7"]}}`)
	// The server the session added went to the account it was added on.
	wantJSON(t, "the work account's servers", jsonKeys(t, filepath.Join(work, ".claude.json"))["mcpServers"], `{"w":{"url":"https://w"},"mine":{"command":"mine"}}`)
	if r := readHomeRecord(filepath.Join(filepath.Dir(f.sessionHome("s-1")), homeRecordName)); r.Base != other || r.BaseConfig != filepath.Join(other, ".claude.json") ||
		!slices.Equal(r.Servers, []string{"docs", "o"}) {
		t.Fatalf("home record %+v, want the other account as its base", r)
	}
	// Launched on it again, the copy stays.
	data, err = jsonWith([]byte(readFile(t, config)), "tipsHistory", []byte(`{"x": 1}`))
	if err != nil {
		t.Fatal(err)
	}
	f.writeMode(config, string(data), 0o600)
	mustPrepare(t, svc, on(other))
	wantJSON(t, "tipsHistory", jsonKeys(t, config)["tipsHistory"], `{"x":1}`)
}
