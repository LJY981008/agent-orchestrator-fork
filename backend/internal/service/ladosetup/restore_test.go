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

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// The recording store keeps the audit events of restores, kept drift and
// reverts too.
func (r *recordingStore) RestoreLadoSetupAdoption(ctx context.Context, id domain.LadoSetupAdoptionID, report json.RawMessage, at time.Time, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error {
	err := r.Store.RestoreLadoSetupAdoption(ctx, id, report, at, event, proof)
	if err == nil {
		r.events = append(r.events, event)
	}
	return err
}

func (r *recordingStore) AddLadoSetupItemVersion(ctx context.Context, itemID domain.LadoSetupItemID, baseVersion int, version domain.LadoSetupItemVersion, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error {
	err := r.Store.AddLadoSetupItemVersion(ctx, itemID, baseVersion, version, event, proof)
	if err == nil {
		r.events = append(r.events, event)
	}
	return err
}

func (r *recordingStore) AppendLadoAuditEvent(ctx context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error) {
	appended, err := r.Store.AppendLadoAuditEvent(ctx, ev)
	if err == nil {
		r.events = append(r.events, ev)
	}
	return appended, err
}

// lastEvent is the last audit event the store recorded, with its payload.
func (r *recordingStore) lastEvent(t *testing.T) (domain.LadoAuditEvent, map[string]any) {
	t.Helper()
	if len(r.events) == 0 {
		t.Fatal("no audit event was recorded")
	}
	ev := r.events[len(r.events)-1]
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	return ev, payload
}

// oldTime is long before the tests run, so a restore that writes the time
// now instead of the original's shows.
var oldTime = time.Date(2024, 3, 4, 5, 6, 7, 890, time.UTC)

// age sets the mtime of everything in the home to at.
func (f *adoptionFixture) age(at time.Time) {
	f.t.Helper()
	for _, root := range []string{f.claude, f.claudeJSON, f.codex} {
		err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return os.Chtimes(path, at, at)
		})
		if err != nil {
			f.t.Fatal(err)
		}
	}
}

// mustAdoptAll moves every adoptable entry of the home.
func (f *adoptionFixture) mustAdoptAll(svc *Service) AdoptResult {
	f.t.Helper()
	inv, err := svc.Scan(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	var entries []AdoptEntry
	for _, e := range inv.Entries {
		if isOriginal(e.Location) && e.Status == domain.LadoSetupStatusAdoptable {
			entries = append(entries, move(e.Tool, e.Kind, e.Name))
		}
	}
	return f.mustAdopt(svc, entries...)
}

// restore restores an adoption with a fresh proof.
func (f *adoptionFixture) restore(svc *Service, id domain.LadoSetupAdoptionID) (RestoreResult, error) {
	return svc.Restore(context.Background(), id, f.presence())
}

// exactState lists everything at the paths: each file's mode, mtime and
// bytes, each folder's mode and mtime. Equal states mean byte for byte the
// same, with the same modes and mtimes.
func exactState(t *testing.T, paths ...string) map[string]string {
	t.Helper()
	state := map[string]string{}
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			stamp := info.Mode().String() + " " + info.ModTime().UTC().Format(time.RFC3339Nano)
			if d.IsDir() {
				state[path] = "dir " + stamp
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			state[path] = stamp + " " + string(data)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return state
}

// rebase moves a state's paths from one root to another.
func rebase(state map[string]string, from, to string) map[string]string {
	out := map[string]string{}
	for path, s := range state {
		out[to+strings.TrimPrefix(path, from)] = s
	}
	return out
}

// statuses lists a report as "<name> <status> <beside>".
func statuses(r RestoreReport) []string {
	var out []string
	for _, e := range r.Entries {
		out = append(out, strings.TrimSpace(fmt.Sprintf("%s %s %s", e.Name, e.Status, e.Beside)))
	}
	slices.Sort(out)
	return out
}

// PRD gate 14, HARNESS §15 item 9: restore puts back what adoption moved
// byte for byte, with the modes and mtimes of files and folders, and key
// files nothing changed since come back as they were.
func TestRestoreIsByteIdentical(t *testing.T) {
	f := newAdoption(t)
	f.writeMode(filepath.Join(f.claude, "agents", "team", "b.md"), "agent b\n", 0o640)
	if err := os.Chmod(filepath.Join(f.claude, "skills", "review", "scripts"), 0o750); err != nil {
		t.Fatal(err)
	}
	f.age(oldTime)
	moved := []string{
		filepath.Join(f.claude, "CLAUDE.md"),
		filepath.Join(f.claude, "skills", "review"),
		filepath.Join(f.claude, "agents", "a.md"),
		filepath.Join(f.claude, "agents", "team", "b.md"),
		filepath.Join(f.claude, "commands", "c.md"),
		filepath.Join(f.claude, "hooks", "check.sh"),
		filepath.Join(f.claude, "output-styles", "terse.md"),
		filepath.Join(f.codex, "AGENTS.md"),
	}
	keyFiles := []string{filepath.Join(f.claude, "settings.json"), f.claudeJSON, filepath.Join(f.codex, "config.toml")}
	untouched := []string{filepath.Join(f.claude, ".credentials.json"), filepath.Join(f.claude, "projects"), filepath.Join(f.codex, "auth.json")}
	before := exactState(t, append(append(slices.Clone(moved), keyFiles...), untouched...)...)

	svc := f.service()
	res := f.mustAdoptAll(svc)
	for _, path := range moved {
		wantMissing(t, path)
	}
	rr, err := f.restore(svc, res.AdoptionID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !rr.Applied || len(rr.Report.Entries) != len(res.Items) {
		t.Fatalf("restore %+v of %d items", rr, len(res.Items))
	}
	for _, e := range rr.Report.Entries {
		if e.Status != RestoreRestored || e.Beside != "" || e.Error != "" {
			t.Fatalf("report entry %+v, want restored", e)
		}
	}
	after := exactState(t, append(append(slices.Clone(moved), keyFiles...), untouched...)...)
	if diffs := diffStates(before, after); len(diffs) > 0 {
		t.Fatalf("restore did not put back byte for byte with modes and mtimes:\n%s", strings.Join(diffs, "\n"))
	}
}

// Plan decision o: what took an original's path after the adoption stays,
// and the original goes beside it.
func TestRestoreKeepsBothWhenOccupied(t *testing.T) {
	f := newAdoption(t)
	f.age(oldTime)
	md := filepath.Join(f.claude, "CLAUDE.md")
	review := filepath.Join(f.claude, "skills", "review")
	agent := filepath.Join(f.claude, "agents", "a.md")
	originals := map[string]map[string]string{md: exactState(t, md), review: exactState(t, review), agent: exactState(t, agent)}
	svc := f.service()
	res := f.mustAdopt(svc,
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		claudeMove(domain.LadoSetupKindSkill, "review"),
		claudeMove(domain.LadoSetupKindAgent, "a.md"),
	)
	// New setup took two of the paths, and an older copy is beside one.
	f.write(md, "# me, written again\n")
	f.write(filepath.Join(review, "SKILL.md"), "another review\n")
	f.write(md+besideSuffix, "an older copy\n")
	occupants := exactState(t, md, review, md+besideSuffix)

	rr, err := f.restore(svc, res.AdoptionID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	want := []string{"CLAUDE.md restored_beside CLAUDE.md.lado-restored-2", "a.md restored", "review restored_beside skills/review.lado-restored"}
	if got := statuses(rr.Report); !slices.Equal(got, want) {
		t.Fatalf("report %v, want %v", got, want)
	}
	if diffs := diffStates(occupants, exactState(t, md, review, md+besideSuffix)); len(diffs) > 0 {
		t.Fatalf("restore changed what took the paths:\n%s", strings.Join(diffs, "\n"))
	}
	for path, beside := range map[string]string{md: md + besideSuffix + "-2", review: review + besideSuffix, agent: agent} {
		if diffs := diffStates(rebase(originals[path], path, beside), exactState(t, beside)); len(diffs) > 0 {
			t.Fatalf("the original of %s is not at %s as it was:\n%s", path, beside, strings.Join(diffs, "\n"))
		}
	}
}

// Plan decision o: a key goes back only where its file has none. A key the
// file has now stays and is reported kept; the other keys keep their order
// and values, and the keys put back sit where they were.
func TestRestorePutsKeysBackOnlyWhereAbsent(t *testing.T) {
	f := newAdoption(t)
	svc := f.service()
	res := f.mustAdopt(svc,
		claudeMove(domain.LadoSetupKindSetting, "env"),
		claudeMove(domain.LadoSetupKindSetting, "permissions"),
		claudeMove(domain.LadoSetupKindSetting, "hooks"),
		claudeMove(domain.LadoSetupKindSetting, "model"),
		claudeMove(domain.LadoSetupKindMCPServer, "docs"),
		claudeMove(domain.LadoSetupKindMCPServer, "search"),
		codexMove(domain.LadoSetupKindMCPServer, "figma"),
		codexMove(domain.LadoSetupKindMCPServer, "notion"),
	)
	// Claude Code and Codex used directly wrote these after the adoption.
	settings := filepath.Join(f.claude, "settings.json")
	f.write(settings, `{
  "env": {
    "ANTHROPIC_API_KEY": "sk-test",
    "NEW_VAR": "x"
  },
  "apiKeyHelper": "~/bin/key.sh",
  "theme": "dark",
  "model": "sonnet",
  "verbose": true
}
`)
	f.write(f.claudeJSON, `{
  "numStartups": 4,
  "mcpServers": {
    "search": {
      "url": "https://search.example"
    },
    "local": {
      "command": "local-mcp"
    }
  },
  "projects": {
    "/x": {
      "allowedTools": []
    }
  }
}
`)
	config := filepath.Join(f.codex, "config.toml")
	f.write(config, readFile(t, config)+"\n[mcp_servers.notion]\nurl = \"https://notion.example\"\n")

	rr, err := f.restore(svc, res.AdoptionID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	want := []string{"docs restored", "env restored", "figma restored", "hooks restored", "model key_kept", "notion key_kept", "permissions restored", "search key_kept"}
	if got := statuses(rr.Report); !slices.Equal(got, want) {
		t.Fatalf("report %v, want %v", got, want)
	}
	for _, e := range rr.Report.Entries {
		if e.Name == "model" && (e.Root != domain.LadoSetupLocationClaudeDir || e.Path != "settings.json#model") {
			t.Fatalf("report entry %+v, want settings.json#model under the Claude folder", e)
		}
	}

	members := func(data string) ([]string, map[string]string) {
		t.Helper()
		list, err := jsonMembers([]byte(data))
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		values := map[string]string{}
		for _, m := range list {
			keys = append(keys, m.Key)
			canonical, err := canonicalJSON(m.Value)
			if err != nil {
				t.Fatal(err)
			}
			values[m.Key] = string(canonical)
		}
		return keys, values
	}
	_, was := members(testSettings)
	keys, now := members(readFile(t, settings))
	if want := []string{"env", "apiKeyHelper", "permissions", "theme", "model", "hooks", "verbose"}; !slices.Equal(keys, want) {
		t.Fatalf("settings.json keys %v, want %v", keys, want)
	}
	for key, value := range map[string]string{"model": `"sonnet"`, "verbose": "true", "theme": `"dark"`, "apiKeyHelper": `"~/bin/key.sh"`, "permissions": was["permissions"], "hooks": was["hooks"]} {
		if now[key] != value {
			t.Fatalf("settings.json %s is %s, want %s", key, now[key], value)
		}
	}
	envValue, _, err := jsonMemberValue([]byte(readFile(t, settings)), "env")
	if err != nil {
		t.Fatal(err)
	}
	if envKeys, env := members(string(envValue)); !slices.Equal(envKeys, []string{"ANTHROPIC_API_KEY", "DEBUG", "NEW_VAR"}) || env["DEBUG"] != `"1"` || env["NEW_VAR"] != `"x"` {
		t.Fatalf("settings.json env is %s", envValue)
	}

	_, wasJSON := members(testClaudeJSON)
	keys, now = members(readFile(t, f.claudeJSON))
	if !slices.Equal(keys, []string{"numStartups", "mcpServers", "projects"}) || now["numStartups"] != "4" || now["projects"] != wasJSON["projects"] {
		t.Fatalf("~/.claude.json is\n%s", readFile(t, f.claudeJSON))
	}
	serversValue, _, err := jsonMemberValue([]byte(readFile(t, f.claudeJSON)), "mcpServers")
	if err != nil {
		t.Fatal(err)
	}
	_, wasServers := members(wasJSON["mcpServers"])
	if names, servers := members(string(serversValue)); !slices.Equal(names, []string{"docs", "search", "local"}) ||
		servers["docs"] != wasServers["docs"] || servers["search"] != `{"url":"https://search.example"}` {
		t.Fatalf("~/.claude.json mcpServers is %s", serversValue)
	}

	text := readFile(t, config)
	var doc map[string]any
	if err := toml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("config.toml does not parse: %v\n%s", err, text)
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	figma, _ := servers["figma"].(map[string]any)
	notion, _ := servers["notion"].(map[string]any)
	if figma["url"] != "https://figma" || notion["url"] != "https://notion.example" || doc["model"] != "gpt-5" || doc["projects"] == nil || !strings.HasPrefix(text, "# my codex config\n") {
		t.Fatalf("config.toml is\n%s", text)
	}
}

// Plan §8: a restored adoption's items end, so nothing resolves or delivers
// them, while their versions stay readable; the inventory lists the
// originals as adoptable again; a second restore is refused.
func TestRestoreStopsDelivery(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	svc := f.service()
	res := f.mustAdopt(svc,
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		claudeMove(domain.LadoSetupKindSkill, "review"),
		claudeMove(domain.LadoSetupKindSetting, "model"),
		codexMove(domain.LadoSetupKindMCPServer, "figma"),
	)
	id := res.AdoptionID

	// Without the desktop app's confirmation nothing happens.
	before := treeState(t, f.root)
	_, err := svc.Restore(ctx, id, nil)
	wantAPIError(t, err, "LADO_PRESENCE_REQUIRED")
	if diffs := diffStates(before, treeState(t, f.root)); len(diffs) > 0 {
		t.Fatalf("an unconfirmed restore wrote:\n%s", strings.Join(diffs, "\n"))
	}

	var asked []string
	presence := f.presence()
	rr, err := svc.Restore(ctx, id, func(action, subject string) (domain.LadoPresenceProof, error) {
		asked = append(asked, action+" "+subject)
		return presence(action, subject)
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if want := []string{domain.LadoPresenceSetupRestore + " " + string(id)}; !slices.Equal(asked, want) {
		t.Fatalf("presence asked for %v, want %v", asked, want)
	}
	if !rr.Applied || !rr.RestoredAt.Equal(scanAt) || len(rr.Report.Entries) != 4 {
		t.Fatalf("restore result %+v", rr)
	}
	adoption, ok, err := f.store.LadoSetupAdoption(ctx, id)
	if err != nil || !ok || !adoption.RestoredAt.Equal(rr.RestoredAt) {
		t.Fatalf("adoption %+v (%v, %v), want it restored", adoption, ok, err)
	}
	var stored RestoreReport
	if err := json.Unmarshal(adoption.RestoreReport, &stored); err != nil || fmt.Sprint(stored) != fmt.Sprint(rr.Report) || stored.Format != restoreReportFormat {
		t.Fatalf("stored report %s (%v), want %+v", adoption.RestoreReport, err, rr.Report)
	}

	// The items end: they are no longer live, which is what resolution and
	// session homes read, and their versions stay readable.
	if got := liveItemNames(t, f.store); len(got) != 0 {
		t.Fatalf("live items after the restore: %v", got)
	}
	for _, it := range res.Items {
		item, versions, ok, err := f.store.LadoSetupItem(ctx, it.ItemID)
		if err != nil || !ok || item.Live() || !item.RestoredAt.Equal(rr.RestoredAt) || len(versions) != 1 || versions[0].ID != it.VersionID {
			t.Fatalf("item %s is %+v with versions %+v (%v, %v)", it.Name, item, versions, ok, err)
		}
		if _, err := f.deps.Blobs.Get(it.SHA256, maxBlobBytes); err != nil {
			t.Fatalf("the version of %s cannot be read: %v", it.Name, err)
		}
	}
	// The managed copies and the backup stay.
	if got := readFile(t, filepath.Join(f.state, "lado", "harness", "claude", "CLAUDE.md")); got != "# me\n" {
		t.Fatalf("managed CLAUDE.md is %q", got)
	}
	if _, err := os.Stat(filepath.Join(backupDir(f.state, id), "manifest.json")); err != nil {
		t.Fatalf("the backup is gone: %v", err)
	}

	// The inventory lists the originals as adoptable again, nothing as
	// managed, and no drift: what restore put back is in the baseline.
	inv, err := svc.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []domain.LadoSetupEntry{
		entryAt(t, inv, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindInstructions, "CLAUDE.md"),
		entryAt(t, inv, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSkill, "review"),
		entryAt(t, inv, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSetting, "model"),
		entryAt(t, inv, domain.LadoSetupLocationCodexHome, domain.LadoSetupKindMCPServer, "figma"),
	} {
		if e.Status != domain.LadoSetupStatusAdoptable || e.ItemID != "" {
			t.Fatalf("%s is %s with item %q, want adoptable", e.Name, e.Status, e.ItemID)
		}
	}
	for _, e := range inv.Entries {
		if e.Location == domain.LadoSetupLocationManaged {
			t.Fatalf("a restored item is listed as managed: %+v", e)
		}
	}
	if len(inv.Drift) != 0 {
		t.Fatalf("drift after the restore: %+v", inv.Drift)
	}

	// The audit log has the restore with its counts.
	ev, payload := f.store.lastEvent(t)
	if ev.Action != actionRestored || ev.SubjectKind != subjectAdoption || ev.SubjectID != string(id) || ev.Actor != actorHuman ||
		payload["items"] != float64(4) || payload["restored"] != float64(4) || payload["keyKept"] != float64(0) || payload["proof"] == "" {
		t.Fatalf("audit event %+v with payload %v", ev, payload)
	}

	// A second restore is refused, and so is an unknown adoption.
	if details := wantAPIError(t, func() error { _, err := f.restore(svc, id); return err }(), CodeAlreadyRestored); details["adoption"] != string(id) {
		t.Fatalf("details %v", details)
	}
	_, err = f.restore(svc, "no-such-adoption")
	wantAPIError(t, err, CodeNotFound)
}

// Plan decision h: restore waits for what runs the tools, as adoption does,
// but not for the OS: the way back stays open where adoption is refused.
func TestRestoreIsAllowedOnAnUnverifiedOS(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	res := f.mustAdopt(f.service(), claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"), codexMove(domain.LadoSetupKindInstructions, "AGENTS.md"))

	// The same home on an OS whose delivery was not measured.
	f.deps.GOOS = "windows"
	svc := f.service()
	agent := []AdoptEntry{claudeMove(domain.LadoSetupKindAgent, "a.md")}
	p := f.preview(svc, agent...)
	if !slices.Contains(p.Preconditions.Unverified, domain.LadoSetupToolClaudeCode) {
		t.Fatalf("preview preconditions %+v, want Claude Code unverified", p.Preconditions)
	}
	_, err := svc.Adopt(ctx, AdoptRequest{Entries: agent, PlanSHA256: p.PlanSHA256, Presence: f.presence()})
	wantAPIError(t, err, CodeOSUnverified)

	// A Claude Code process holds restore back.
	f.deps.Processes = func(context.Context) ([]procmem.Process, error) {
		return []procmem.Process{{PID: 4242, Command: "/usr/local/bin/claude --resume abc"}}, nil
	}
	before := treeState(t, f.root)
	_, err = f.restore(f.service(), res.AdoptionID)
	if details := wantAPIError(t, err, CodeToolRunning); fmt.Sprint(details["processes"]) != "[map[name:claude pid:4242 tool:claude-code]]" {
		t.Fatalf("details %v", details)
	}
	if diffs := diffStates(before, treeState(t, f.root)); len(diffs) > 0 {
		t.Fatalf("a refused restore wrote:\n%s", strings.Join(diffs, "\n"))
	}

	f.deps.Processes = func(context.Context) ([]procmem.Process, error) { return nil, nil }
	rr, err := f.restore(f.service(), res.AdoptionID)
	if err != nil || !rr.Applied {
		t.Fatalf("restore on an unverified OS: %+v, %v", rr, err)
	}
	if got := readFile(t, filepath.Join(f.claude, "CLAUDE.md")); got != "# me\n" {
		t.Fatalf("CLAUDE.md is %q", got)
	}
	if got := readFile(t, filepath.Join(f.codex, "AGENTS.md")); got != "codex rules\n" {
		t.Fatalf("AGENTS.md is %q", got)
	}
}

// Plan §8 step 3: a restore that cannot put back an original records nothing
// and names the first failure; what it put back stays, and restoring again
// finishes the rest without writing anything twice.
func TestRestoreFinishesWhatAFailedRestoreLeft(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	svc := f.service()
	res := f.mustAdopt(svc,
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		claudeMove(domain.LadoSetupKindAgent, "a.md"),
		claudeMove(domain.LadoSetupKindSetting, "model"),
	)
	copyPath := filepath.Join(backupDir(f.state, res.AdoptionID), "files", "claude_dir", "agents", "a.md")
	good := readFile(t, copyPath)
	if err := os.WriteFile(copyPath, []byte("damaged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rr, err := f.restore(svc, res.AdoptionID)
	details := wantAPIError(t, err, CodeRestoreFailed)
	if details["stage"] != restoreStagePutBack || details["path"] != "agents/a.md" || details["name"] != "a.md" || details["root"] != "claude_dir" {
		t.Fatalf("failure details %v", details)
	}
	if want := []string{"CLAUDE.md restored", "a.md failed", "model restored"}; rr.Applied || !slices.Equal(statuses(rr.Report), want) {
		t.Fatalf("result %+v, want %v", rr, want)
	}
	if adoption, _, _ := f.store.LadoSetupAdoption(ctx, res.AdoptionID); !adoption.RestoredAt.IsZero() {
		t.Fatalf("a failed restore was recorded: %+v", adoption)
	}
	if got := liveItemNames(t, f.store); len(got) != 3 {
		t.Fatalf("live items %v, want the adoption's three", got)
	}
	wantMissing(t, filepath.Join(f.claude, "agents", "a.md"))

	if err := os.WriteFile(copyPath, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	rr, err = f.restore(svc, res.AdoptionID)
	if err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if want := []string{"CLAUDE.md restored", "a.md restored", "model restored"}; !rr.Applied || !slices.Equal(statuses(rr.Report), want) {
		t.Fatalf("result %+v, want %v", rr, want)
	}
	if got := readFile(t, filepath.Join(f.claude, "agents", "a.md")); got != "agent a\n" {
		t.Fatalf("a.md is %q", got)
	}
	wantMissing(t, filepath.Join(f.claude, "CLAUDE.md"+besideSuffix))
	if got := readFile(t, filepath.Join(f.claude, "settings.json")); got != testSettings {
		t.Fatalf("settings.json is\n%s\nwant\n%s", got, testSettings)
	}
}

// A partial adoption recorded the originals it removed; restore puts back
// those and leaves the ones it never removed. A manifest that is not the one
// the adoption recorded stops the restore before it writes anything.
func TestRestoreOfAPartialAdoption(t *testing.T) {
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
	res, err := f.adopt(svc,
		claudeMove(domain.LadoSetupKindAgent, "a.md"),
		claudeMove(domain.LadoSetupKindAgent, "c.md"),
		claudeMove(domain.LadoSetupKindAgent, "d.md"),
		claudeMove(domain.LadoSetupKindAgent, "team/b.md"),
	)
	wantAPIError(t, err, CodeAdoptionFailed)
	if res.State != domain.LadoSetupAdoptionPartial {
		t.Fatalf("adoption %+v, want partial", res)
	}

	manifest := filepath.Join(backupDir(f.state, res.AdoptionID), "manifest.json")
	good := readFile(t, manifest)
	if err := os.WriteFile(manifest, []byte(strings.Replace(good, `"agent"`, `"command"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	before := treeState(t, f.root)
	_, err = f.restore(f.service(), res.AdoptionID)
	if details := wantAPIError(t, err, CodeRestoreFailed); details["stage"] != restoreStageManifest {
		t.Fatalf("failure details %v", details)
	}
	if diffs := diffStates(before, treeState(t, f.root)); len(diffs) > 0 {
		t.Fatalf("a restore with another manifest wrote:\n%s", strings.Join(diffs, "\n"))
	}
	if err := os.WriteFile(manifest, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}

	others := treeState(t, filepath.Join(f.claude, "agents", "team"))
	d := exactState(t, filepath.Join(f.claude, "agents", "d.md"))
	rr, err := f.restore(f.service(), res.AdoptionID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if want := []string{"a.md restored", "c.md restored"}; !slices.Equal(statuses(rr.Report), want) {
		t.Fatalf("report %v, want %v", statuses(rr.Report), want)
	}
	for _, name := range []string{"a.md", "c.md"} {
		if got := readFile(t, filepath.Join(f.claude, "agents", name)); got != "agent "+strings.TrimSuffix(name, ".md")+"\n" && got != "agent "+name+"\n" {
			t.Fatalf("%s is %q", name, got)
		}
	}
	if diffs := diffStates(d, exactState(t, filepath.Join(f.claude, "agents", "d.md"))); len(diffs) > 0 {
		t.Fatalf("restore wrote over an original the adoption never removed:\n%s", strings.Join(diffs, "\n"))
	}
	if diffs := diffStates(others, treeState(t, filepath.Join(f.claude, "agents", "team"))); len(diffs) > 0 {
		t.Fatalf("restore wrote beside an original the adoption never removed:\n%s", strings.Join(diffs, "\n"))
	}
	wantMissing(t, filepath.Join(f.claude, "agents", "d.md"+besideSuffix))
}
