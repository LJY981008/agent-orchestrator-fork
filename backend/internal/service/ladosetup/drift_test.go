package ladosetup

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func mustDrift(t *testing.T, svc *Service) []domain.LadoSetupDrift {
	t.Helper()
	drift, err := svc.Drift(context.Background())
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	return drift
}

// itemIDs maps an adoption's items by name.
func itemIDs(res AdoptResult) map[string]domain.LadoSetupItemID {
	ids := map[string]domain.LadoSetupItemID{}
	for _, it := range res.Items {
		ids[it.Name] = it.ItemID
	}
	return ids
}

// wantEdited checks that drift is one edit of item, whose managed copy now
// hashes to sum.
func wantEdited(t *testing.T, drift []domain.LadoSetupDrift, item domain.LadoSetupItemID, path, sum string) {
	t.Helper()
	if len(drift) != 1 || drift[0].Kind != domain.LadoSetupDriftEdited || drift[0].ItemID != item || drift[0].Path != path || drift[0].SHA256 != sum {
		t.Fatalf("drift %+v, want one edit of %s at %s hashing to %s", drift, item, path, sum)
	}
}

// HARNESS §15 item 15: setup that appears where adoption took setup from is
// new local setup, shown and never moved by itself.
func TestDriftShowsNewLocalItems(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	svc := f.service()
	// Before the first adoption there is no baseline, so nothing is new.
	if drift := mustDrift(t, svc); len(drift) != 0 {
		t.Fatalf("drift before any adoption: %+v", drift)
	}
	res := f.mustAdopt(svc,
		claudeMove(domain.LadoSetupKindSetting, "hooks"),
		claudeMove(domain.LadoSetupKindSkill, "review"),
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
	)
	if inv, err := svc.Inventory(ctx); err != nil || len(inv.Drift) != 0 {
		t.Fatalf("drift right after the adoption: %+v (%v)", inv.Drift, err)
	}

	// Claude Code used directly writes hooks again, a new key and another
	// theme; env, permissions and model keep their values.
	settings := filepath.Join(f.claude, "settings.json")
	f.write(settings, `{
  "env": {"DEBUG": "1", "ANTHROPIC_API_KEY": "sk-test"},
  "apiKeyHelper": "~/bin/key.sh",
  "permissions": {"allow": ["Bash(ls)"]},
  "model": "opus",
  "theme": "light",
  "verbose": true,
  "hooks": {"Stop": []}
}
`)
	f.write(filepath.Join(f.claude, "skills", "fresh", "SKILL.md"), "fresh\n")
	home := f.homeState()
	lado := treeState(t, filepath.Join(f.state, "lado"))

	drift := mustDrift(t, svc)
	inv, err := svc.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(inv.Drift, drift) {
		t.Fatalf("the cached inventory's drift %+v, want %+v", inv.Drift, drift)
	}
	hooksItem := itemIDs(res)["hooks"]
	want := map[string]domain.LadoSetupItemID{
		settings + "#hooks":                        hooksItem,
		settings + "#theme":                        "",
		settings + "#verbose":                      "",
		filepath.Join(f.claude, "skills", "fresh"): "",
	}
	if len(drift) != len(want) {
		t.Fatalf("drift %+v, want new local setup at %v", drift, want)
	}
	for _, d := range drift {
		item, ok := want[d.Path]
		if !ok || d.Kind != domain.LadoSetupDriftNewLocal || d.ItemID != item {
			t.Fatalf("drift %+v, want new local setup at %v", d, want)
		}
		e := entryAtPath(t, inv, d.Path)
		if e.Status != domain.LadoSetupStatusAdoptable || d.SHA256 != e.SHA256 || d.Tool != e.Tool || d.ItemKind != e.Kind || d.Name != e.Name {
			t.Fatalf("drift %+v for the entry %+v", d, e)
		}
	}
	// Finding drift moves nothing: the originals and LADO's folder are as
	// they were.
	if diffs := diffStates(home, f.homeState()); len(diffs) > 0 {
		t.Fatalf("drift changed the home:\n%s", strings.Join(diffs, "\n"))
	}
	if diffs := diffStates(lado, treeState(t, filepath.Join(f.state, "lado"))); len(diffs) > 0 {
		t.Fatalf("drift changed LADO's folder:\n%s", strings.Join(diffs, "\n"))
	}
	if got := liveItemNames(t, f.store); len(got) != 3 {
		t.Fatalf("live items %v, want the adoption's three", got)
	}

	// Setup that goes away, or gets its old content back, is no longer new.
	if err := os.RemoveAll(filepath.Join(f.claude, "skills", "fresh")); err != nil {
		t.Fatal(err)
	}
	f.write(settings, strings.Replace(readFile(t, settings), `"light"`, `"dark"`, 1))
	var paths []string
	for _, d := range mustDrift(t, svc) {
		paths = append(paths, d.Path)
	}
	slices.Sort(paths)
	if want := []string{settings + "#hooks", settings + "#verbose"}; !slices.Equal(paths, want) {
		t.Fatalf("drift at %v, want %v", paths, want)
	}
}

// Plan §8: an edit in the managed folder is drift. Keeping it makes the
// managed content the next version; reverting writes the latest version
// over it. Items that can run commands keep drift only with the desktop
// app's confirmation.
func TestDriftEditedManagedFile(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	svc := f.service()
	res := f.mustAdopt(svc,
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		claudeMove(domain.LadoSetupKindSkill, "review"),
		claudeMove(domain.LadoSetupKindSetting, "model"),
		claudeMove(domain.LadoSetupKindSetting, "hooks"),
		claudeMove(domain.LadoSetupKindHookScript, "check.sh"),
	)
	ids := itemIDs(res)
	managed := filepath.Join(f.state, "lado", "harness", "claude")
	md := filepath.Join(managed, "CLAUDE.md")

	edited := "# me, edited in the managed folder\n"
	f.writeMode(md, edited, 0o600)
	wantEdited(t, mustDrift(t, svc), ids["CLAUDE.md"], md, sumOf([]byte(edited)))

	// Keeping needs the content the developer reviewed.
	_, err := svc.KeepDrift(ctx, ids["CLAUDE.md"], sumOf([]byte("something else")))
	if details := wantAPIError(t, err, CodeChanged); details["sha256"] != sumOf([]byte(edited)) {
		t.Fatalf("details %v", details)
	}
	v, err := svc.KeepDrift(ctx, ids["CLAUDE.md"], sumOf([]byte(edited)))
	if err != nil {
		t.Fatalf("keep: %v", err)
	}
	if v.Version != 2 || v.SHA256 != sumOf([]byte(edited)) || v.Actor != actorAPI || v.ContentKind != domain.LadoSetupContentFile || v.Executable {
		t.Fatalf("kept version %+v", v)
	}
	if _, versions, _, err := f.store.LadoSetupItem(ctx, ids["CLAUDE.md"]); err != nil || len(versions) != 2 || versions[1].ID != v.ID {
		t.Fatalf("versions %+v (%v)", versions, err)
	}
	if data, err := f.deps.Blobs.Get(v.SHA256, maxBlobBytes); err != nil || string(data) != edited {
		t.Fatalf("the kept version's blob is %q (%v)", data, err)
	}
	ev, payload := f.store.lastEvent(t)
	if ev.Action != actionDriftKept || ev.SubjectKind != subjectItem || ev.SubjectID != string(ids["CLAUDE.md"]) || ev.Actor != actorAPI || payload["version"] != float64(2) {
		t.Fatalf("audit event %+v with payload %v", ev, payload)
	}
	if drift := mustDrift(t, svc); len(drift) != 0 {
		t.Fatalf("drift after keeping: %+v", drift)
	}
	// Keeping again keeps nothing.
	if again, err := svc.KeepDrift(ctx, ids["CLAUDE.md"], v.SHA256); err != nil || again.ID != v.ID {
		t.Fatalf("keeping again gave %+v (%v), want version 2", again, err)
	}

	// Reverting writes the latest version over the managed copy.
	f.writeMode(md, "# scribbled\n", 0o600)
	got, err := svc.RevertDrift(ctx, ids["CLAUDE.md"])
	if err != nil || got.ID != v.ID {
		t.Fatalf("revert gave %+v (%v), want version 2", got, err)
	}
	if data := readFile(t, md); data != edited {
		t.Fatalf("managed CLAUDE.md is %q, want version 2", data)
	}
	if ev, _ := f.store.lastEvent(t); ev.Action != actionDriftReverted || ev.SubjectID != string(ids["CLAUDE.md"]) || ev.Actor != actorAPI {
		t.Fatalf("audit event %+v", ev)
	}
	// A managed copy that is gone cannot be kept, only reverted.
	if err := os.Remove(md); err != nil {
		t.Fatal(err)
	}
	wantEdited(t, mustDrift(t, svc), ids["CLAUDE.md"], md, "")
	_, err = svc.KeepDrift(ctx, ids["CLAUDE.md"], "")
	wantAPIError(t, err, CodeDriftUnreadable)
	if _, err := svc.RevertDrift(ctx, ids["CLAUDE.md"]); err != nil || readFile(t, md) != edited {
		t.Fatalf("revert of a missing copy: %v", err)
	}
	// Nor a copy that changed form: a folder where the item is a file.
	if err := os.Remove(md); err != nil {
		t.Fatal(err)
	}
	f.write(filepath.Join(md, "inner.md"), "inner\n")
	drift := mustDrift(t, svc)
	if len(drift) != 1 || drift[0].SHA256 == "" {
		t.Fatalf("drift %+v, want the folder's edit", drift)
	}
	_, err = svc.KeepDrift(ctx, ids["CLAUDE.md"], drift[0].SHA256)
	wantAPIError(t, err, CodeDriftUnreadable)
	if _, err := svc.RevertDrift(ctx, ids["CLAUDE.md"]); err != nil || readFile(t, md) != edited {
		t.Fatalf("revert of a folder copy: %v", err)
	}

	// A folder item: a file added in its managed folder is kept as a new
	// tree; a file deleted there comes back with a revert.
	review := filepath.Join(managed, "skills", "review")
	f.write(filepath.Join(review, "notes.md"), "notes\n")
	tree, err := treeOf(ctx, review)
	if err != nil {
		t.Fatal(err)
	}
	treeSum, err := tree.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	wantEdited(t, mustDrift(t, svc), ids["review"], review, treeSum)
	kept, err := svc.KeepDrift(ctx, ids["review"], treeSum)
	if err != nil || kept.ContentKind != domain.LadoSetupContentTree || kept.Version != 2 || kept.Size != tree.Size() {
		t.Fatalf("kept tree %+v (%v)", kept, err)
	}
	stored, err := svc.treeOfVersion(kept)
	if err != nil || len(stored.Entries) != 3 {
		t.Fatalf("the kept tree is %+v (%v)", stored, err)
	}
	if data, err := f.deps.Blobs.Get(sumOf([]byte("notes\n")), maxBlobBytes); err != nil || string(data) != "notes\n" {
		t.Fatalf("notes.md's blob is %q (%v)", data, err)
	}
	if err := os.Remove(filepath.Join(review, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RevertDrift(ctx, ids["review"]); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(review, "SKILL.md")); got != "review\n" {
		t.Fatalf("SKILL.md is %q after the revert", got)
	}

	// A key item: its value edited in the managed settings.json.
	settings := filepath.Join(managed, "settings.json")
	f.writeMode(settings, strings.Replace(readFile(t, settings), `"opus"`, `"haiku"`, 1), 0o600)
	wantEdited(t, mustDrift(t, svc), ids["model"], settings+"#model", sumOf([]byte(`"haiku"`)))
	if _, err := svc.RevertDrift(ctx, ids["model"]); err != nil {
		t.Fatal(err)
	}
	if value, _, err := jsonMemberValue([]byte(readFile(t, settings)), "model"); err != nil || string(value) != `"opus"` {
		t.Fatalf("managed model is %s (%v)", value, err)
	}
	// A managed key file that no longer parses is written again from its
	// items' versions.
	f.writeMode(settings, "{ not json", 0o600)
	if _, err := svc.RevertDrift(ctx, ids["model"]); err != nil {
		t.Fatalf("revert of an unreadable settings.json: %v", err)
	}
	if drift := mustDrift(t, svc); len(drift) != 0 {
		t.Fatalf("drift after the reverts: %+v", drift)
	}

	// A hook script can run commands: keeping its drift needs the desktop
	// app's confirmation, which the change flow passes with its proof.
	script := filepath.Join(managed, "hooks", "check.sh")
	f.writeMode(script, "#!/bin/sh\nexit 1\n", 0o700)
	_, err = svc.KeepDrift(ctx, ids["check.sh"], sumOf([]byte("#!/bin/sh\nexit 1\n")))
	wantAPIError(t, err, "LADO_PRESENCE_REQUIRED")
	proof, err := f.presence()(domain.LadoPresenceSetupChange, "change")
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := svc.keepDrift(ctx, ids["check.sh"], sumOf([]byte("#!/bin/sh\nexit 1\n")), approvalOf(proof))
	if err != nil || confirmed.Actor != actorHuman || !confirmed.Executable || confirmed.Version != 2 {
		t.Fatalf("confirmed keep %+v (%v)", confirmed, err)
	}
	var kept2 map[string]any
	ev, kept2 = f.store.lastEvent(t)
	if ev.Action != actionDriftKept || kept2["proof"] != proof.Nonce {
		t.Fatalf("audit event %+v with payload %v", ev, kept2)
	}
}

// Plan decision n: drift never changes what resolves or what sessions get.
// While managed copies are edited, the live items keep their versions, and
// the inventory lists them with the versions' hashes.
func TestDriftNeverChangesResolution(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	svc := f.service()
	f.mustAdopt(svc,
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		claudeMove(domain.LadoSetupKindSetting, "model"),
		codexMove(domain.LadoSetupKindMCPServer, "figma"),
	)
	before, err := f.store.ListLiveLadoSetupItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(f.state, "lado", "harness")
	f.writeMode(filepath.Join(harness, "claude", "CLAUDE.md"), "# edited\n", 0o600)
	settings := filepath.Join(harness, "claude", "settings.json")
	f.writeMode(settings, strings.Replace(readFile(t, settings), `"opus"`, `"haiku"`, 1), 0o600)
	config := filepath.Join(harness, "codex", "config.toml")
	f.writeMode(config, strings.Replace(readFile(t, config), "https://figma", "https://figma.example", 1), 0o600)

	drift := mustDrift(t, svc)
	if len(drift) != 3 {
		t.Fatalf("drift %+v, want the three edits", drift)
	}
	for _, d := range drift {
		if d.Kind != domain.LadoSetupDriftEdited || d.SHA256 == "" {
			t.Fatalf("drift %+v, want an edit", d)
		}
	}
	after, err := f.store.ListLiveLadoSetupItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("live items %+v, were %+v", after, before)
	}
	inv, err := svc.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range before {
		if after[i].Version.ID != before[i].Version.ID || after[i].Version.SHA256 != before[i].Version.SHA256 {
			t.Fatalf("item %s is at %+v, was %+v", after[i].Item.Name, after[i].Version, before[i].Version)
		}
		e := entryAt(t, inv, domain.LadoSetupLocationManaged, before[i].Item.Kind, before[i].Item.Name)
		if e.SHA256 != before[i].Version.SHA256 || e.ItemID != before[i].Item.ID {
			t.Fatalf("managed entry %+v, want the version's hash %s", e, before[i].Version.SHA256)
		}
		for _, d := range drift {
			if d.ItemID == before[i].Item.ID && d.SHA256 == before[i].Version.SHA256 {
				t.Fatalf("drift %+v names the version's hash", d)
			}
		}
	}
	// Scans leave the edits where they are until they are kept or reverted.
	if got := readFile(t, filepath.Join(harness, "claude", "CLAUDE.md")); got != "# edited\n" {
		t.Fatalf("managed CLAUDE.md is %q", got)
	}
	for _, ev := range f.store.events {
		if ev.Action != actionAdopted {
			t.Fatalf("finding drift recorded %+v", ev)
		}
	}
}
