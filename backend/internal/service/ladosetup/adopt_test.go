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

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// The fake home's files. settings.json and ~/.claude.json are written the
// way Claude writes them, two-space indented.
const (
	testSettings = `{
  "env": {
    "ANTHROPIC_API_KEY": "sk-test",
    "DEBUG": "1"
  },
  "apiKeyHelper": "~/bin/key.sh",
  "permissions": {
    "allow": [
      "Bash(ls)"
    ]
  },
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "~/.claude/hooks/check.sh"
          }
        ]
      }
    ]
  },
  "theme": "dark"
}
`
	testClaudeJSON = `{
  "numStartups": 3,
  "mcpServers": {
    "docs": {
      "command": "docs-mcp",
      "args": [
        "--port",
        "7"
      ]
    },
    "search": {
      "url": "https://search"
    }
  },
  "projects": {
    "/x": {
      "allowedTools": []
    }
  }
}
`
	testCodexConfig = `# my codex config
model = "gpt-5"

[mcp_servers.figma]
url = "https://figma"

# notion is for docs
[mcp_servers.notion]
url = "https://notion"
args = ["--x"]

[projects."/x"]
trust_level = "trusted"
`
)

// recordingStore is the real store, keeping the audit events of the
// adoptions it recorded.
type recordingStore struct {
	*sqlite.Store
	events []domain.LadoAuditEvent
}

func (r *recordingStore) CompleteLadoSetupAdoption(ctx context.Context, adoption domain.LadoSetupAdoption, items []domain.LadoSetupItem, versions []domain.LadoSetupItemVersion, event domain.LadoAuditEvent) error {
	err := r.Store.CompleteLadoSetupAdoption(ctx, adoption, items, versions, event)
	if err == nil {
		r.events = append(r.events, event)
	}
	return err
}

// adoptionFixture is a fake home with the files above, and a service over
// it with a real store and blob store. Nothing runs Claude Code or Codex.
type adoptionFixture struct {
	*fakeHome
	t     *testing.T
	store *recordingStore
	deps  Deps
	// nonces counts the presence proofs handed out.
	nonces int
}

func newAdoption(t *testing.T) *adoptionFixture {
	t.Helper()
	h := newFakeHome(t)
	h.write(filepath.Join(h.claude, "CLAUDE.md"), "# me\n")
	h.write(filepath.Join(h.claude, "settings.json"), testSettings)
	h.write(filepath.Join(h.claude, "skills", "review", "SKILL.md"), "review\n")
	h.writeMode(filepath.Join(h.claude, "skills", "review", "scripts", "run.sh"), "#!/bin/sh\n", 0o755)
	h.write(filepath.Join(h.claude, "agents", "a.md"), "agent a\n")
	h.write(filepath.Join(h.claude, "agents", "team", "b.md"), "agent b\n")
	h.write(filepath.Join(h.claude, "commands", "c.md"), "command c\n")
	h.writeMode(filepath.Join(h.claude, "hooks", "check.sh"), "#!/bin/sh\nexit 0\n", 0o755)
	h.write(filepath.Join(h.claude, "output-styles", "terse.md"), "terse\n")
	h.writeMode(filepath.Join(h.claude, ".credentials.json"), `{"token":"secret"}`, 0o600)
	h.write(filepath.Join(h.claude, "projects", "-x", "abc.jsonl"), "{}\n")
	h.write(filepath.Join(h.claude, "history.jsonl"), "{}\n")
	h.write(h.claudeJSON, testClaudeJSON)
	h.write(filepath.Join(h.codex, "AGENTS.md"), "codex rules\n")
	h.write(filepath.Join(h.codex, "config.toml"), testCodexConfig)
	h.writeMode(filepath.Join(h.codex, "auth.json"), `{"key":"secret"}`, 0o600)
	h.write(filepath.Join(h.codex, "sessions", "s.jsonl"), "{}\n")

	f := &adoptionFixture{fakeHome: h, t: t, store: &recordingStore{Store: sqlitetest.MustOpen(t)}}
	f.deps = h.deps()
	f.deps.Store = f.store
	blobs := ladoblob.New(h.state)
	t.Cleanup(func() { _ = blobs.Close() })
	f.deps.Blobs = blobs
	f.deps.Sessions = fakeSessions{}
	f.deps.Processes = func(context.Context) ([]procmem.Process, error) { return nil, nil }
	return f
}

// homeState is treeState of the tool folders and ~/.claude.json, without
// the state dir.
func (f *adoptionFixture) homeState() map[string]string {
	state := map[string]string{}
	for _, root := range []string{f.claude, f.claudeJSON, f.codex} {
		for path, s := range treeState(f.t, root) {
			state[path] = s
		}
	}
	return state
}

// service returns a service over the fixture's deps as they are now.
func (f *adoptionFixture) service() *Service { return New(f.deps) }

// presence hands out a fresh proof for whatever it is asked.
func (f *adoptionFixture) presence() func(action, subject string) (domain.LadoPresenceProof, error) {
	f.nonces++
	nonce := fmt.Sprintf("test-nonce-%04d-abcdefgh", f.nonces)
	return func(action, subject string) (domain.LadoPresenceProof, error) {
		return domain.LadoPresenceProof{Nonce: nonce, Action: action, SubjectID: subject, ExpiresAt: scanAt.Add(time.Minute), UsedAt: scanAt}, nil
	}
}

// preview runs a dry run and fails the test on an error.
func (f *adoptionFixture) preview(svc *Service, entries ...AdoptEntry) AdoptionPreview {
	f.t.Helper()
	res, err := svc.Adopt(context.Background(), AdoptRequest{Entries: entries, DryRun: true})
	if err != nil {
		f.t.Fatalf("preview: %v", err)
	}
	return res.Preview
}

// adopt previews and applies with a fresh proof.
func (f *adoptionFixture) adopt(svc *Service, entries ...AdoptEntry) (AdoptResult, error) {
	f.t.Helper()
	p := f.preview(svc, entries...)
	return svc.Adopt(context.Background(), AdoptRequest{Entries: entries, PlanSHA256: p.PlanSHA256, Presence: f.presence()})
}

func (f *adoptionFixture) mustAdopt(svc *Service, entries ...AdoptEntry) AdoptResult {
	f.t.Helper()
	res, err := f.adopt(svc, entries...)
	if err != nil {
		f.t.Fatalf("adopt: %v", err)
	}
	return res
}

func move(tool domain.LadoSetupTool, kind domain.LadoSetupKind, name string) AdoptEntry {
	return AdoptEntry{Tool: tool, Kind: kind, Name: name, Action: domain.LadoSetupActionMove}
}

func claudeMove(kind domain.LadoSetupKind, name string) AdoptEntry {
	return move(domain.LadoSetupToolClaudeCode, kind, name)
}

func codexMove(kind domain.LadoSetupKind, name string) AdoptEntry {
	return move(domain.LadoSetupToolCodex, kind, name)
}

// treeState lists everything under root: each file's mode, mtime and bytes,
// each folder's mode. Equal states mean nothing was written.
func treeState(t *testing.T, root string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == root {
			return fs.SkipAll
		}
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			state[path] = "dir " + info.Mode().String()
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		state[path] = fmt.Sprintf("%s %s %s", info.Mode(), info.ModTime().Format(time.RFC3339Nano), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func diffStates(before, after map[string]string) []string {
	var diffs []string
	for path, was := range before {
		if now, ok := after[path]; !ok {
			diffs = append(diffs, "removed "+path)
		} else if now != was {
			diffs = append(diffs, "changed "+path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			diffs = append(diffs, "added "+path)
		}
	}
	slices.Sort(diffs)
	return diffs
}

// wantAPIError checks an error's code and returns its details.
func wantAPIError(t *testing.T, err error, code string) map[string]any {
	t.Helper()
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("error %v, want %s", err, code)
	}
	return ae.Details
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func wantMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s exists (%v), want it gone", path, err)
	}
}

func liveItemNames(t *testing.T, store *recordingStore) []string {
	t.Helper()
	items, err := store.ListLiveLadoSetupItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, iv := range items {
		names = append(names, string(iv.Item.Kind)+":"+iv.Item.Name)
	}
	return names
}

func adoptions(t *testing.T, store *recordingStore) []domain.LadoSetupAdoption {
	t.Helper()
	list, err := store.ListLadoSetupAdoptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestAdoptionPreviewHashesThePlan(t *testing.T) {
	f := newAdoption(t)
	svc := f.service()
	before := treeState(t, f.root)

	claudeMD := claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md")
	model := AdoptEntry{Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: "model", Action: domain.LadoSetupActionKeep}
	figma := codexMove(domain.LadoSetupKindMCPServer, "figma")
	first := f.preview(svc, claudeMD, model, figma)
	again := f.preview(svc, figma, claudeMD, model)
	if first.PlanSHA256 == "" || first.PlanSHA256 != again.PlanSHA256 {
		t.Fatalf("the same choice hashes to %q and %q", first.PlanSHA256, again.PlanSHA256)
	}

	// The hash is the plan's canonical JSON, entries in tool, kind and name
	// order.
	md := filepath.Join(f.claude, "CLAUDE.md")
	only := f.preview(svc, claudeMD)
	canonical := fmt.Sprintf(`{"entries":[{"action":"move","kind":"instructions","name":"CLAUDE.md","originPath":%q,"sha256":%q,"tool":"claude-code"}],"format":"lado-setup-adoption/1","os":"linux"}`,
		md, sumOf([]byte("# me\n")))
	if only.PlanSHA256 != sumOf([]byte(canonical)) {
		t.Fatalf("plan hash %s, want the sha256 of %s", only.PlanSHA256, canonical)
	}

	// The preview says where each moved entry goes and how sessions get it.
	var got []string
	for _, e := range first.Entries {
		got = append(got, fmt.Sprintf("%s %s %s %s %s", e.Tool, e.Name, e.Action, e.Delivery, e.ManagedPath))
	}
	want := []string{
		fmt.Sprintf("claude-code CLAUDE.md move home %s", filepath.Join(f.state, "lado", "harness", "claude", "CLAUDE.md")),
		"claude-code model keep  ",
		fmt.Sprintf("codex figma move overlay %s", filepath.Join(f.state, "lado", "harness", "codex", "config.toml")+"#mcp_servers.figma"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("preview entries\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !slices.Contains(first.Warnings, WarningDirectUse) || !first.Preconditions.OK() || first.OS != "linux" {
		t.Fatalf("preview warnings %v, preconditions %+v, os %q", first.Warnings, first.Preconditions, first.OS)
	}

	// Another move or another file is another plan.
	keepMD := claudeMD
	keepMD.Action = domain.LadoSetupActionKeep
	if p := f.preview(svc, keepMD, model, figma); p.PlanSHA256 == first.PlanSHA256 {
		t.Fatal("keeping instead of moving hashes the same")
	}
	h := f.write(md, "# me, edited\n")
	if p := f.preview(svc, claudeMD, model, figma); p.PlanSHA256 == first.PlanSHA256 {
		t.Fatal("a changed file hashes the same")
	}
	f.write(h, "# me\n")

	// Entries LADO cannot adopt are refused with the reason.
	for _, tc := range []struct {
		entry  AdoptEntry
		reason string
	}{
		{claudeMove(domain.LadoSetupKindSetting, "apiKeyHelper"), string(domain.LadoSetupReasonLoginKey)},
		{claudeMove(domain.LadoSetupKindInstructions, "MISSING.md"), reasonNotFound},
	} {
		_, err := svc.Adopt(context.Background(), AdoptRequest{Entries: []AdoptEntry{claudeMD, tc.entry}, DryRun: true})
		details := wantAPIError(t, err, CodeNotAdoptable)
		if details["name"] != tc.entry.Name || details["reason"] != tc.reason || details["kind"] != string(tc.entry.Kind) || details["tool"] != string(tc.entry.Tool) {
			t.Fatalf("refusal details %v, want %s with reason %s", details, tc.entry.Name, tc.reason)
		}
	}
	for _, bad := range [][]AdoptEntry{nil, {claudeMD, claudeMD}, {{Tool: "claude-code", Kind: "instructions", Name: "CLAUDE.md", Action: "copy"}}} {
		_, err := svc.Adopt(context.Background(), AdoptRequest{Entries: bad, DryRun: true})
		wantAPIError(t, err, CodeAdoptionInvalid)
	}

	// Previews write nothing: the home and the state dir are as they were,
	// but for the file this test edited.
	before[h] = treeState(t, f.root)[h]
	if diffs := diffStates(before, treeState(t, f.root)); len(diffs) > 0 {
		t.Fatalf("previews wrote:\n%s", strings.Join(diffs, "\n"))
	}
	if list := adoptions(t, f.store); len(list) != 0 {
		t.Fatalf("previews recorded %v", list)
	}
}

func TestAdoptionBacksUpBeforeRemoving(t *testing.T) {
	f := newAdoption(t)
	svc := f.service()
	var order []string
	svc.onStage = func(stage string) { order = append(order, stage) }
	svc.ops = fileOps{
		removeAll: func(path string) error { order = append(order, "removeAll "+path); return os.RemoveAll(path) },
		rename:    func(from, to string) error { order = append(order, "rename "+to); return os.Rename(from, to) },
	}
	var asked []string
	entries := []AdoptEntry{
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		claudeMove(domain.LadoSetupKindHookScript, "check.sh"),
		claudeMove(domain.LadoSetupKindSetting, "hooks"),
		claudeMove(domain.LadoSetupKindSkill, "review"),
		codexMove(domain.LadoSetupKindMCPServer, "figma"),
	}
	p := f.preview(svc, entries...)
	presence := f.presence()
	res, err := svc.Adopt(context.Background(), AdoptRequest{Entries: entries, PlanSHA256: p.PlanSHA256, Presence: func(action, subject string) (domain.LadoPresenceProof, error) {
		asked = append(asked, action+" "+subject)
		return presence(action, subject)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{domain.LadoPresenceSetupAdopt + " " + p.PlanSHA256}; !slices.Equal(asked, want) {
		t.Fatalf("presence asked for %v, want %v", asked, want)
	}
	firstChange := slices.IndexFunc(order, func(s string) bool { return strings.HasPrefix(s, "removeAll ") || strings.HasPrefix(s, "rename ") })
	verified, copied := slices.Index(order, stageBackupVerified), slices.Index(order, stageCopyVerified)
	if firstChange < 0 || verified < 0 || copied < 0 || verified > firstChange || copied > firstChange {
		t.Fatalf("stages ran in the order\n%s", strings.Join(order, "\n"))
	}
	if res.State != domain.LadoSetupAdoptionCompleted || len(res.Items) != len(entries) {
		t.Fatalf("result %+v", res)
	}

	// The backup holds every original, owner-only, with the manifest's
	// modes and mtimes.
	dir := backupDir(f.state, res.AdoptionID)
	var m backupManifest
	data := readFile(t, filepath.Join(dir, "manifest.json"))
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := f.store.LadoSetupAdoption(context.Background(), res.AdoptionID)
	if err != nil || !ok || stored.ManifestSHA256 != sumOf([]byte(data)) || stored.PlanSHA256 != p.PlanSHA256 {
		t.Fatalf("recorded adoption %+v (%v), want the manifest's and the plan's hashes", stored, err)
	}
	if m.Format != backupFormat || len(m.Entries) != len(entries) {
		t.Fatalf("manifest %+v", m)
	}
	byName := map[string]backupEntry{}
	for _, be := range m.Entries {
		byName[be.Name] = be
	}
	check := byName["check.sh"]
	if check.Type != backupTypeFile || check.Mode != "0755" || check.RelPath != "hooks/check.sh" || check.Root != domain.LadoSetupLocationClaudeDir || check.SHA256 != sumOf([]byte("#!/bin/sh\nexit 0\n")) {
		t.Fatalf("check.sh backup entry %+v", check)
	}
	info, err := os.Stat(check.copyPath(dir))
	if err != nil || info.Mode().Perm() != 0o600 || !info.ModTime().Equal(check.MTime) {
		t.Fatalf("check.sh copy %v (%v), want 0600 with the original mtime %v", info.Mode(), err, check.MTime)
	}
	review := byName["review"]
	if review.Type != backupTypeDir || len(review.Files) != 3 || review.Files[1].Path != "scripts" || !review.Files[1].Dir || review.Files[2].Mode != "0755" {
		t.Fatalf("review backup entry %+v", review)
	}
	hooks := byName["hooks"]
	if hooks.Type != backupTypeKeys || hooks.RelPath != "settings.json" || hooks.SHA256 != sumOf([]byte(testSettings)) || len(hooks.RemovedKeys) != 1 || hooks.RemovedKeys[0].Key != "hooks" {
		t.Fatalf("hooks backup entry %+v", hooks)
	}
	if got := readFile(t, hooks.copyPath(dir)); got != testSettings {
		t.Fatalf("settings.json copy is\n%s", got)
	}
	figma := byName["figma"]
	if figma.Root != domain.LadoSetupLocationCodexHome || figma.RelPath != "config.toml" || figma.RemovedKeys[0].Member != "figma" {
		t.Fatalf("figma backup entry %+v", figma)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("backup folder %v (%v), want 0700", info.Mode(), err)
	}

	// A copy that does not read back as it should stops the adoption before
	// anything is removed or recorded.
	f = newAdoption(t)
	svc = f.service()
	removed := false
	svc.ops = fileOps{
		removeAll: func(string) error { removed = true; return errors.New("must not remove") },
		rename:    func(string, string) error { removed = true; return errors.New("must not rename") },
	}
	svc.afterBackup = func(dir string) {
		path := filepath.Join(dir, "files", "claude_dir", "CLAUDE.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data[0] ^= 1
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	home := f.homeState()
	_, err = f.adopt(svc, entries...)
	details := wantAPIError(t, err, CodeAdoptionFailed)
	if details["stage"] != stageBackup || details["path"] != "CLAUDE.md" || details["root"] != "claude_dir" {
		t.Fatalf("failure details %v", details)
	}
	if removed {
		t.Fatal("an original was removed after the backup failed its check")
	}
	if list := adoptions(t, f.store); len(list) != 0 {
		t.Fatalf("a failed backup recorded %v", list)
	}
	if diffs := diffStates(home, f.homeState()); len(diffs) > 0 {
		t.Fatalf("a failed backup changed:\n%s", strings.Join(diffs, "\n"))
	}
	wantMissing(t, filepath.Join(f.state, "lado", "harness"))
}

func TestAdoptionNeverTouchesCredentials(t *testing.T) {
	f := newAdoption(t)
	svc := f.service()
	untouched := []string{
		filepath.Join(f.claude, ".credentials.json"),
		filepath.Join(f.claude, "projects"),
		filepath.Join(f.claude, "history.jsonl"),
		filepath.Join(f.codex, "auth.json"),
		filepath.Join(f.codex, "sessions"),
	}
	before := map[string]map[string]string{}
	for _, path := range untouched {
		before[path] = treeState(t, path)
	}

	// Adopt everything adoptable.
	inv, err := svc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var entries []AdoptEntry
	for _, e := range inv.Entries {
		if isOriginal(e.Location) && e.Status == domain.LadoSetupStatusAdoptable {
			entries = append(entries, move(e.Tool, e.Kind, e.Name))
		}
	}
	res := f.mustAdopt(svc, entries...)
	if len(res.Items) != len(entries) || len(entries) < 15 {
		t.Fatalf("adopted %d of %d entries", len(res.Items), len(entries))
	}
	for _, check := range res.Verification {
		if !check.OK {
			t.Fatalf("verification failed: %+v", check)
		}
	}

	for _, path := range untouched {
		if diffs := diffStates(before[path], treeState(t, path)); len(diffs) > 0 {
			t.Fatalf("adoption touched %s:\n%s", path, strings.Join(diffs, "\n"))
		}
	}
	// ~/.claude.json lost its servers only.
	was, err := jsonObject([]byte(testClaudeJSON))
	if err != nil {
		t.Fatal(err)
	}
	now, err := jsonObject([]byte(readFile(t, f.claudeJSON)))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"numStartups", "projects"} {
		if string(now[key]) != string(was[key]) {
			t.Fatalf("~/.claude.json %s is %s, was %s", key, now[key], was[key])
		}
	}
	if string(now["mcpServers"]) != "{}" || len(now) != 3 {
		t.Fatalf("~/.claude.json is\n%s", readFile(t, f.claudeJSON))
	}
	// The login key and the login variable stay in settings.json.
	want := "{\n  \"env\": {\n    \"ANTHROPIC_API_KEY\": \"sk-test\"\n  },\n  \"apiKeyHelper\": \"~/bin/key.sh\"\n}\n"
	if got := readFile(t, filepath.Join(f.claude, "settings.json")); got != want {
		t.Fatalf("settings.json is\n%s\nwant\n%s", got, want)
	}
}

func TestAdoptionRemovesOnlyAdoptedKeys(t *testing.T) {
	f := newAdoption(t)
	svc := f.service()
	settingsInfo, err := os.Stat(filepath.Join(f.claude, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	res := f.mustAdopt(svc,
		claudeMove(domain.LadoSetupKindSetting, "env"),
		claudeMove(domain.LadoSetupKindSetting, "permissions"),
		claudeMove(domain.LadoSetupKindSetting, "hooks"),
		AdoptEntry{Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: "theme", Action: domain.LadoSetupActionKeep},
		claudeMove(domain.LadoSetupKindMCPServer, "docs"),
		codexMove(domain.LadoSetupKindMCPServer, "figma"),
	)
	if len(res.Items) != 5 {
		t.Fatalf("adopted %+v", res.Items)
	}

	settings := filepath.Join(f.claude, "settings.json")
	want := "{\n  \"env\": {\n    \"ANTHROPIC_API_KEY\": \"sk-test\"\n  },\n  \"apiKeyHelper\": \"~/bin/key.sh\",\n  \"model\": \"opus\",\n  \"theme\": \"dark\"\n}\n"
	if got := readFile(t, settings); got != want {
		t.Fatalf("settings.json is\n%s\nwant\n%s", got, want)
	}
	if info, err := os.Stat(settings); err != nil || info.Mode() != settingsInfo.Mode() {
		t.Fatalf("settings.json mode %v (%v), want %v", info.Mode(), err, settingsInfo.Mode())
	}
	want = "{\n  \"numStartups\": 3,\n  \"mcpServers\": {\n    \"search\": {\n      \"url\": \"https://search\"\n    }\n  },\n  \"projects\": {\n    \"/x\": {\n      \"allowedTools\": []\n    }\n  }\n}\n"
	if got := readFile(t, f.claudeJSON); got != want {
		t.Fatalf("~/.claude.json is\n%s\nwant\n%s", got, want)
	}
	want = "# my codex config\nmodel = \"gpt-5\"\n\n# notion is for docs\n[mcp_servers.notion]\nurl = \"https://notion\"\nargs = [\"--x\"]\n\n[projects.\"/x\"]\ntrust_level = \"trusted\"\n"
	if got := readFile(t, filepath.Join(f.codex, "config.toml")); got != want {
		t.Fatalf("config.toml is\n%s\nwant\n%s", got, want)
	}

	// The managed folder holds the adopted keys only, env without its login
	// variable.
	managed := filepath.Join(f.state, "lado", "harness")
	want = "{\n  \"env\": {\n    \"DEBUG\": \"1\"\n  },\n  \"hooks\": {\n    \"PreToolUse\": [\n      {\n        \"hooks\": [\n          {\n            \"command\": \"~/.claude/hooks/check.sh\",\n            \"type\": \"command\"\n          }\n        ],\n        \"matcher\": \"Bash\"\n      }\n    ]\n  },\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(ls)\"\n    ]\n  }\n}\n"
	if got := readFile(t, filepath.Join(managed, "claude", "settings.json")); got != want {
		t.Fatalf("managed settings.json is\n%s\nwant\n%s", got, want)
	}
	want = "{\n  \"mcpServers\": {\n    \"docs\": {\n      \"args\": [\n        \"--port\",\n        \"7\"\n      ],\n      \"command\": \"docs-mcp\"\n    }\n  }\n}\n"
	if got := readFile(t, filepath.Join(managed, "claude", "mcp.json")); got != want {
		t.Fatalf("managed mcp.json is\n%s\nwant\n%s", got, want)
	}
	if got, want := readFile(t, filepath.Join(managed, "codex", "config.toml")), "[mcp_servers.figma]\nurl = \"https://figma\"\n"; got != want {
		t.Fatalf("managed config.toml is\n%s\nwant\n%s", got, want)
	}
	var codexChecked bool
	for _, check := range res.Verification {
		codexChecked = codexChecked || check.Check == CheckCodexOverride
		if !check.OK {
			t.Fatalf("verification failed: %+v", check)
		}
	}
	if !codexChecked {
		t.Fatalf("no Codex launch option was checked: %+v", res.Verification)
	}
	if got := liveItemNames(t, f.store); !slices.Equal(got, []string{"mcp_server:docs", "setting:env", "setting:hooks", "setting:permissions", "mcp_server:figma"}) {
		t.Fatalf("live items %v", got)
	}

	// A second adoption adds to the managed key files and keeps what they
	// hold.
	f.mustAdopt(svc, claudeMove(domain.LadoSetupKindSetting, "model"), codexMove(domain.LadoSetupKindMCPServer, "notion"))
	got, err := jsonObject([]byte(readFile(t, filepath.Join(managed, "claude", "settings.json"))))
	if err != nil || len(got) != 4 || string(got["model"]) != `"opus"` {
		t.Fatalf("managed settings.json after a second adoption: %v (%v)", got, err)
	}
	if got := readFile(t, filepath.Join(managed, "codex", "config.toml")); got != "[mcp_servers.figma]\nurl = \"https://figma\"\n\n[mcp_servers.notion]\nargs = [\"--x\"]\nurl = \"https://notion\"\n" {
		t.Fatalf("managed config.toml after a second adoption:\n%s", got)
	}
}

func TestAdoptionKeepsContainers(t *testing.T) {
	f := newAdoption(t)
	f.write(filepath.Join(f.claude, "skills", "notes.md"), "a skill in one file\n")
	svc := f.service()
	f.mustAdopt(svc,
		claudeMove(domain.LadoSetupKindSkill, "review"),
		claudeMove(domain.LadoSetupKindSkill, "notes.md"),
		claudeMove(domain.LadoSetupKindAgent, "team/b.md"),
		claudeMove(domain.LadoSetupKindHookScript, "check.sh"),
		claudeMove(domain.LadoSetupKindSetting, "hooks"),
	)
	for _, dir := range []string{"skills", "hooks", filepath.Join("agents", "team")} {
		entries, err := os.ReadDir(filepath.Join(f.claude, dir))
		if err != nil {
			t.Fatalf("%s is gone: %v", dir, err)
		}
		if len(entries) != 0 {
			t.Fatalf("%s still holds %v", dir, entries)
		}
	}
	if got := readFile(t, filepath.Join(f.claude, "agents", "a.md")); got != "agent a\n" {
		t.Fatalf("agents/a.md is %q", got)
	}
	managed := filepath.Join(f.state, "lado", "harness", "claude")
	if got := readFile(t, filepath.Join(managed, "skills", "review", "scripts", "run.sh")); got != "#!/bin/sh\n" {
		t.Fatalf("managed run.sh is %q", got)
	}
	if info, err := os.Stat(filepath.Join(managed, "skills", "review", "scripts", "run.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("managed run.sh mode %v (%v), want executable", info.Mode(), err)
	}
	if got := readFile(t, filepath.Join(managed, "agents", "team", "b.md")); got != "agent b\n" {
		t.Fatalf("managed team/b.md is %q", got)
	}
}

func TestAdoptionRewritesHookPaths(t *testing.T) {
	f := newAdoption(t)
	settings := strings.Replace(testSettings, `  "theme": "dark"`, `  "statusLine": {
    "type": "command",
    "command": "bash $HOME/.claude/hooks/status.sh --short"
  },
  "theme": "dark"`, 1)
	f.write(filepath.Join(f.claude, "settings.json"), settings)
	f.writeMode(filepath.Join(f.claude, "hooks", "status.sh"), "#!/bin/sh\necho ok\n", 0o755)
	svc := f.service()
	entries := []AdoptEntry{
		claudeMove(domain.LadoSetupKindSetting, "hooks"),
		claudeMove(domain.LadoSetupKindSetting, "statusLine"),
		claudeMove(domain.LadoSetupKindHookScript, "check.sh"),
		claudeMove(domain.LadoSetupKindHookScript, "status.sh"),
	}
	p := f.preview(svc, entries...)
	want := []HookRewrite{
		{Setting: "hooks", Event: "PreToolUse", Matcher: "Bash", Before: "~/.claude/hooks/check.sh", After: `"$CLAUDE_CONFIG_DIR"/hooks/check.sh`, Paths: []string{"hooks/check.sh"}},
		{Setting: "statusLine", Before: "bash $HOME/.claude/hooks/status.sh --short", After: `bash "$CLAUDE_CONFIG_DIR"/hooks/status.sh --short`, Paths: []string{"hooks/status.sh"}},
	}
	if fmt.Sprint(p.Rewrites) != fmt.Sprint(want) {
		t.Fatalf("rewrites %+v\nwant %+v", p.Rewrites, want)
	}
	for _, c := range p.Conflicts {
		if c.Code == ConflictHookPathMoved {
			t.Fatalf("a rewritten reference is still a conflict: %+v", c)
		}
	}
	res := f.mustAdopt(svc, entries...)

	managed, err := jsonObject([]byte(readFile(t, filepath.Join(f.state, "lado", "harness", "claude", "settings.json"))))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(managed["hooks"]), `"\"$CLAUDE_CONFIG_DIR\"/hooks/check.sh"`) || strings.Contains(string(managed["hooks"]), "~/.claude") {
		t.Fatalf("managed hooks are %s", managed["hooks"])
	}
	if !strings.Contains(string(managed["statusLine"]), `"bash \"$CLAUDE_CONFIG_DIR\"/hooks/status.sh --short"`) {
		t.Fatalf("managed statusLine is %s", managed["statusLine"])
	}
	for _, it := range res.Items {
		if it.Name == "hooks" {
			content, err := canonicalJSON(managed["hooks"])
			if err != nil || it.SHA256 != sumOf(content) {
				t.Fatalf("the hooks version %s is not the rewritten value's hash (%v)", it.SHA256, err)
			}
		}
	}
	if got := readFile(t, filepath.Join(backupDir(f.state, res.AdoptionID), "files", "claude_dir", "settings.json")); got != settings {
		t.Fatal("the backup does not keep the commands as they were")
	}

	// A reference adoption cannot rewrite blocks it: hooks stay in place
	// while the script they run would move.
	f = newAdoption(t)
	svc = f.service()
	check := claudeMove(domain.LadoSetupKindHookScript, "check.sh")
	p = f.preview(svc, check)
	var blocking []Conflict
	for _, c := range p.Conflicts {
		if c.Code == ConflictHookPathMoved && c.Severity == domain.LadoSeverityBlocking {
			blocking = append(blocking, c)
		}
	}
	if len(blocking) != 1 || blocking[0].Details["path"] != "hooks/check.sh" || len(p.Rewrites) != 0 {
		t.Fatalf("preview conflicts %+v, rewrites %+v", p.Conflicts, p.Rewrites)
	}
	before := treeState(t, f.root)
	_, err = svc.Adopt(context.Background(), AdoptRequest{Entries: []AdoptEntry{check}, PlanSHA256: p.PlanSHA256, Presence: f.presence()})
	if details := wantAPIError(t, err, ConflictHookPathMoved); details["path"] != "hooks/check.sh" {
		t.Fatalf("refusal details %v", details)
	}
	if diffs := diffStates(before, treeState(t, f.root)); len(diffs) > 0 {
		t.Fatalf("a refused adoption wrote:\n%s", strings.Join(diffs, "\n"))
	}
}

func TestAdoptionRefusesWhilePreconditionsFail(t *testing.T) {
	entries := []AdoptEntry{claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md")}
	claudeProcess := func(context.Context) ([]procmem.Process, error) {
		return []procmem.Process{{PID: 4242, Command: "/usr/local/bin/claude --resume abc"}, {PID: 7, Command: "/usr/bin/vim"}}, nil
	}
	cases := []struct {
		name    string
		setup   func(*adoptionFixture)
		code    string
		details map[string]any
	}{
		{
			name:    "an OS whose delivery was not measured",
			setup:   func(f *adoptionFixture) { f.deps.GOOS = "darwin" },
			code:    CodeOSUnverified,
			details: map[string]any{"os": "darwin", "tools": []domain.LadoSetupTool{domain.LadoSetupToolClaudeCode}},
		},
		{
			name: "a live AO session of the tool",
			setup: func(f *adoptionFixture) {
				f.deps.Sessions = fakeSessions{
					{ID: "s-claude", ProjectID: "p", Harness: domain.HarnessClaudeCode},
					{ID: "s-codex", ProjectID: "p", Harness: domain.HarnessCodex},
					{ID: "s-done", ProjectID: "p", Harness: domain.HarnessClaudeCode, IsTerminated: true},
				}
			},
			code: CodeToolRunning,
			details: map[string]any{
				"sessions":  []map[string]any{{"id": "s-claude", "tool": "claude-code"}},
				"processes": []map[string]any{},
			},
		},
		{
			name:  "a Claude Code process outside AO",
			setup: func(f *adoptionFixture) { f.deps.Processes = claudeProcess },
			code:  CodeToolRunning,
			details: map[string]any{
				"sessions":  []map[string]any{},
				"processes": []map[string]any{{"pid": 4242, "name": "claude", "tool": "claude-code"}},
			},
		},
		{
			name: "ignoring processes still counts sessions",
			setup: func(f *adoptionFixture) {
				f.deps.Processes = claudeProcess
				f.deps.DevIgnoreProcesses = true
				f.deps.Sessions = fakeSessions{{ID: "s-claude", ProjectID: "p", Harness: domain.HarnessClaudeCode}}
			},
			code: CodeToolRunning,
			details: map[string]any{
				"sessions":  []map[string]any{{"id": "s-claude", "tool": "claude-code"}},
				"processes": []map[string]any{},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdoption(t)
			tc.setup(f)
			svc := f.service()
			p := f.preview(svc, entries...)
			if p.Preconditions.OK() {
				t.Fatalf("preview preconditions %+v, want a refusal", p.Preconditions)
			}
			before := treeState(t, f.root)
			_, err := svc.Adopt(context.Background(), AdoptRequest{Entries: entries, PlanSHA256: p.PlanSHA256, Presence: f.presence()})
			if details := wantAPIError(t, err, tc.code); fmt.Sprint(details) != fmt.Sprint(tc.details) {
				t.Fatalf("details %v, want %v", details, tc.details)
			}
			if diffs := diffStates(before, treeState(t, f.root)); len(diffs) > 0 {
				t.Fatalf("a refused adoption wrote:\n%s", strings.Join(diffs, "\n"))
			}
		})
	}

	// A Codex session does not hold back Claude Code's setup.
	f := newAdoption(t)
	f.deps.Sessions = fakeSessions{{ID: "s-codex", ProjectID: "p", Harness: domain.HarnessCodex}}
	f.mustAdopt(f.service(), entries...)

	// The lab switch lets the processes pass and says so in the audit log.
	f = newAdoption(t)
	f.deps.Processes = claudeProcess
	f.deps.DevIgnoreProcesses = true
	svc := f.service()
	if p := f.preview(svc, entries...); !p.Preconditions.OK() || !p.Preconditions.ProcessesIgnored {
		t.Fatalf("preview preconditions %+v", p.Preconditions)
	}
	res := f.mustAdopt(svc, entries...)
	if len(f.store.events) != 1 {
		t.Fatalf("audit events %+v", f.store.events)
	}
	var payload map[string]any
	if err := json.Unmarshal(f.store.events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	ev := f.store.events[0]
	if ev.Action != actionAdopted || ev.SubjectID != string(res.AdoptionID) || ev.Actor != actorHuman || payload["processesIgnored"] != true ||
		payload["items"] != float64(1) || payload["rewrites"] != float64(0) || fmt.Sprint(payload["tools"]) != "map[claude-code:1]" {
		t.Fatalf("audit event %+v with payload %v", ev, payload)
	}
}

func TestAdoptionPartialFailureLeavesRestore(t *testing.T) {
	f := newAdoption(t)
	for _, name := range []string{"c.md", "d.md"} {
		f.write(filepath.Join(f.claude, "agents", name), "agent "+name+"\n")
	}
	svc := f.service()
	calls := 0
	svc.ops.removeAll = func(path string) error {
		calls++
		if calls == 3 {
			return &fs.PathError{Op: "remove", Path: path, Err: errors.New("the file is locked")}
		}
		return os.RemoveAll(path)
	}
	entries := []AdoptEntry{
		claudeMove(domain.LadoSetupKindAgent, "a.md"),
		claudeMove(domain.LadoSetupKindAgent, "c.md"),
		claudeMove(domain.LadoSetupKindAgent, "d.md"),
		claudeMove(domain.LadoSetupKindAgent, "team/b.md"),
	}
	res, err := f.adopt(svc, entries...)
	details := wantAPIError(t, err, CodeAdoptionFailed)
	if details["stage"] != stageRemove || details["path"] != "agents/d.md" || details["state"] != "partial" || details["adoption"] != string(res.AdoptionID) {
		t.Fatalf("failure details %v", details)
	}
	if !res.Applied || res.State != domain.LadoSetupAdoptionPartial || len(res.Items) != 2 {
		t.Fatalf("result %+v", res)
	}

	list := adoptions(t, f.store)
	if len(list) != 1 || list[0].State != domain.LadoSetupAdoptionPartial || !strings.Contains(list[0].ErrorMessage, "agents/d.md: remove: the file is locked") ||
		strings.Contains(list[0].ErrorMessage, f.root) {
		t.Fatalf("adoptions %+v", list)
	}
	if got := liveItemNames(t, f.store); !slices.Equal(got, []string{"agent:a.md", "agent:c.md"}) {
		t.Fatalf("live items %v", got)
	}
	managed := filepath.Join(f.state, "lado", "harness", "claude", "agents")
	for _, name := range []string{"a.md", "c.md"} {
		wantMissing(t, filepath.Join(f.claude, "agents", name))
		if got := readFile(t, filepath.Join(managed, name)); got != "agent "+strings.TrimSuffix(name, ".md")+"\n" && got != "agent "+name+"\n" {
			t.Fatalf("managed %s is %q", name, got)
		}
	}
	for _, name := range []string{"d.md", "team/b.md"} {
		if _, err := os.Stat(filepath.Join(f.claude, "agents", filepath.FromSlash(name))); err != nil {
			t.Fatalf("the original %s is gone: %v", name, err)
		}
		wantMissing(t, filepath.Join(managed, filepath.FromSlash(name)))
	}
	// The backup holds every original, so restore can put back what moved.
	var m backupManifest
	data := readFile(t, filepath.Join(backupDir(f.state, res.AdoptionID), "manifest.json"))
	if err := json.Unmarshal([]byte(data), &m); err != nil || len(m.Entries) != 4 || list[0].ManifestSHA256 != sumOf([]byte(data)) {
		t.Fatalf("manifest %+v (%v)", m, err)
	}
	for _, be := range m.Entries[:2] {
		if got := readFile(t, be.copyPath(backupDir(f.state, res.AdoptionID))); !strings.HasPrefix(got, "agent ") {
			t.Fatalf("backup of %s is %q", be.Name, got)
		}
	}
}

func TestAdoptionPlanMismatchWritesNothing(t *testing.T) {
	f := newAdoption(t)
	svc := f.service()
	entries := []AdoptEntry{
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		codexMove(domain.LadoSetupKindMCPServer, "figma"),
	}
	p := f.preview(svc, entries...)
	f.write(filepath.Join(f.codex, "config.toml"), strings.Replace(testCodexConfig, "https://figma", "https://figma.example", 1))
	before := treeState(t, f.root)
	_, err := svc.Adopt(context.Background(), AdoptRequest{Entries: entries, PlanSHA256: p.PlanSHA256, Presence: f.presence()})
	details := wantAPIError(t, err, CodeChanged)
	now := f.preview(svc, entries...)
	if details["planSha256"] != now.PlanSHA256 || now.PlanSHA256 == p.PlanSHA256 {
		t.Fatalf("details %v, want the new plan hash %s", details, now.PlanSHA256)
	}
	if diffs := diffStates(before, treeState(t, f.root)); len(diffs) > 0 {
		t.Fatalf("a changed plan wrote:\n%s", strings.Join(diffs, "\n"))
	}
	wantMissing(t, filepath.Join(f.state, "lado", "backups"))
	if list := adoptions(t, f.store); len(list) != 0 {
		t.Fatalf("a changed plan recorded %v", list)
	}

	// Applying needs the preview's hash.
	_, err = svc.Adopt(context.Background(), AdoptRequest{Entries: entries, Presence: f.presence()})
	wantAPIError(t, err, CodeAdoptionInvalid)
	// And the desktop app's confirmation.
	_, err = svc.Adopt(context.Background(), AdoptRequest{Entries: entries, PlanSHA256: now.PlanSHA256})
	wantAPIError(t, err, "LADO_PRESENCE_REQUIRED")
	wantMissing(t, filepath.Join(f.state, "lado", "backups"))
}

// TestAdoptionPutsBackWhenRecordFails covers the one failure after removal
// that records nothing: the store refuses the adoption (here, a replayed
// presence proof). The removed originals come back from the backup, and the
// managed folder loses the unrecorded items.
// failingStore fails to record adoptions, after doing to the original
// folders what it is given.
type failingStore struct {
	*recordingStore
	before func()
}

func (f *failingStore) CompleteLadoSetupAdoption(context.Context, domain.LadoSetupAdoption, []domain.LadoSetupItem, []domain.LadoSetupItemVersion, domain.LadoAuditEvent) error {
	if f.before != nil {
		f.before()
	}
	return errors.New("the database is gone")
}

func TestAdoptionPutsBackWhenRecordFails(t *testing.T) {
	f := newAdoption(t)
	f.mustAdopt(f.service(), claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"))
	original := []string{filepath.Join(f.claude, "agents", "a.md"), filepath.Join(f.claude, "skills", "review"), filepath.Join(f.claude, "settings.json")}
	before := map[string]map[string]string{}
	for _, path := range original {
		before[path] = treeState(t, path)
	}
	f.deps.Store = &failingStore{recordingStore: f.store}
	_, err := f.adopt(f.service(),
		claudeMove(domain.LadoSetupKindAgent, "a.md"),
		claudeMove(domain.LadoSetupKindSkill, "review"),
		claudeMove(domain.LadoSetupKindSetting, "model"),
		claudeMove(domain.LadoSetupKindSetting, "env"),
	)
	details := wantAPIError(t, err, CodeAdoptionFailed)
	if details["stage"] != stageRecord || details["putBackFailed"] != nil {
		t.Fatalf("details %v, want the record stage with everything put back", details)
	}
	for _, path := range original {
		if diffs := diffStates(before[path], treeState(t, path)); len(diffs) > 0 {
			t.Fatalf("%s was not put back as it was:\n%s", path, strings.Join(diffs, "\n"))
		}
	}
	managed := filepath.Join(f.state, "lado", "harness", "claude")
	wantMissing(t, filepath.Join(managed, "agents", "a.md"))
	wantMissing(t, filepath.Join(managed, "skills", "review"))
	if keys, err := jsonObject([]byte(readFile(t, filepath.Join(managed, "settings.json")))); err != nil || len(keys) != 0 {
		t.Fatalf("the managed settings.json holds %v (%v)", keys, err)
	}
	if got := liveItemNames(t, f.store); !slices.Equal(got, []string{"instructions:CLAUDE.md"}) {
		t.Fatalf("live items %v", got)
	}
	// Everything went back, so the pending adoption leaves no row.
	if list := adoptions(t, f.store); len(list) != 1 || list[0].State != domain.LadoSetupAdoptionCompleted {
		t.Fatalf("adoptions %+v, want the first one only", list)
	}
}
