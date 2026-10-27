package ladosetup

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestEditRefusesPendingDrift(t *testing.T) {
	ctx := context.Background()
	f := newChangeFixture(t)
	ids := f.adopt(claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"))
	md := filepath.Join(f.state, "lado", "harness", "claude", "CLAUDE.md")
	f.writeMode(md, "# me, edited by hand\n", 0o600)
	edit := Change{Op: OpEdit, ItemID: ids["CLAUDE.md"], BaseVersion: 1, Content: jsonText("# new\n")}
	for _, c := range []Change{edit, {Op: OpDelete, ItemID: ids["CLAUDE.md"]}} {
		for _, dry := range []bool{true, false} {
			c.DryRun = dry
			_, err := f.svc.Apply(ctx, c, f.presence())
			if details := wantAPIError(t, err, CodeDriftPending); details["item"] != string(ids["CLAUDE.md"]) {
				t.Fatalf("%s details %v", c.Op, details)
			}
		}
	}
	if got := readFile(t, md); got != "# me, edited by hand\n" {
		t.Fatalf("the managed copy holds %q after the refusals", got)
	}
	// A binding writes no content and goes on.
	f.mustApply(Change{Op: OpBinding, ItemID: ids["CLAUDE.md"], Binding: ChangeBinding{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOff}}, false)
	// Reverted, the edit goes through; kept, so does the delete.
	f.mustApply(Change{Op: OpDriftRevert, ItemID: ids["CLAUDE.md"]}, false)
	if res := f.mustApply(edit, false); !res.Applied || res.Version.Version != 2 {
		t.Fatalf("edit after the revert %+v", res)
	}
	f.writeMode(md, "# newer, by hand\n", 0o600)
	f.mustApply(Change{Op: OpDriftKeep, ItemID: ids["CLAUDE.md"], DriftSHA256: sumOf([]byte("# newer, by hand\n"))}, false)
	if res := f.mustApply(Change{Op: OpDelete, ItemID: ids["CLAUDE.md"]}, false); !res.Applied {
		t.Fatalf("delete after the keep %+v", res)
	}
}

// countingProjects counts the scans that list the projects.
type countingProjects struct{ n *int }

func (c countingProjects) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	*c.n++
	return nil, nil
}

func TestAdoptionRecordsPendingBeforeRemoving(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	scans := 0
	f.deps.Projects = countingProjects{&scans}
	svc := f.service()
	presence := f.presence()
	proof, _ := presence("", "")
	var seen []domain.LadoSetupAdoption
	nonceUsed := false
	svc.ops.removeAll = func(path string) error {
		if seen == nil {
			seen = adoptions(t, f.store)
			used, err := f.store.LadoPresenceProofUsed(ctx, proof.Nonce)
			if err != nil {
				t.Fatal(err)
			}
			nonceUsed = used
		}
		return os.RemoveAll(path)
	}
	entries := []AdoptEntry{claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"), claudeMove(domain.LadoSetupKindSkill, "review")}
	p := f.preview(svc, entries...)
	res, err := svc.Adopt(ctx, AdoptRequest{Entries: entries, PlanSHA256: p.PlanSHA256, Presence: presence})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].ID != res.AdoptionID || seen[0].State != domain.LadoSetupAdoptionPending || !nonceUsed {
		t.Fatalf("at the first removal: adoptions %+v, nonce stored %v; want the adoption pending with its proof", seen, nonceUsed)
	}
	if list := adoptions(t, f.store); len(list) != 1 || list[0].State != domain.LadoSetupAdoptionCompleted {
		t.Fatalf("adoptions %+v, want it completed", list)
	}

	// The proof is checked before the rescan: a reused one and an expired
	// one stop before anything is read or written.
	before := f.homeState()
	more := []AdoptEntry{claudeMove(domain.LadoSetupKindAgent, "a.md")}
	plan := f.preview(svc, more...).PlanSHA256
	expired := func(string, string) (domain.LadoPresenceProof, error) {
		return domain.LadoPresenceProof{}, apierr.Forbidden("LADO_PRESENCE_EXPIRED", "The confirmation expired.")
	}
	for code, proofOf := range map[string]func(string, string) (domain.LadoPresenceProof, error){"LADO_PRESENCE_REPLAYED": presence, "LADO_PRESENCE_EXPIRED": expired} {
		scans = 0
		_, err := svc.Adopt(ctx, AdoptRequest{Entries: more, PlanSHA256: plan, Presence: proofOf})
		wantAPIError(t, err, code)
		if scans != 0 {
			t.Fatalf("%s: the adoption scanned %d times before it checked the proof", code, scans)
		}
	}
	// A proof that another request stores while this one runs stops it at
	// its record, before the first removal.
	fresh := f.presence()
	again, _ := fresh(domain.LadoPresenceSetupAdopt, strings.Repeat("b", 64))
	svc.afterBackup = func(string) {
		other := domain.LadoSetupAdoption{ID: "other", CreatedAt: scanAt, Actor: actorHuman, OS: "linux", PlanSHA256: strings.Repeat("b", 64), State: domain.LadoSetupAdoptionPending}
		if err := f.store.BeginLadoSetupAdoption(ctx, other, &again); err != nil {
			t.Fatal(err)
		}
	}
	_, err = svc.Adopt(ctx, AdoptRequest{Entries: more, PlanSHA256: plan, Presence: fresh})
	wantAPIError(t, err, "LADO_PRESENCE_REPLAYED")
	if diffs := diffStates(before, f.homeState()); len(diffs) > 0 {
		t.Fatalf("refused adoptions changed the tool folders:\n%s", strings.Join(diffs, "\n"))
	}
	wantMissing(t, filepath.Join(f.state, "lado", "harness", "claude", "agents", "a.md"))
}

// crashingStore stops the daemon, as a panic, when an adoption would be
// recorded.
type crashingStore struct{ *recordingStore }

func (crashingStore) CompleteLadoSetupAdoption(context.Context, domain.LadoSetupAdoption, []domain.LadoSetupItem, []domain.LadoSetupItemVersion, domain.LadoAuditEvent) error {
	panic("the daemon stopped")
}

func TestInterruptedAdoptionCanBeRestored(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	f.age(oldTime)
	paths := []string{filepath.Join(f.claude, "CLAUDE.md"), filepath.Join(f.claude, "skills", "review"), filepath.Join(f.claude, "settings.json"), f.claudeJSON, filepath.Join(f.codex, "config.toml")}
	before := exactState(t, paths...)
	f.deps.Store = crashingStore{f.store}
	entries := []AdoptEntry{
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		claudeMove(domain.LadoSetupKindSetting, "model"),
		claudeMove(domain.LadoSetupKindSkill, "review"),
		claudeMove(domain.LadoSetupKindMCPServer, "docs"),
		codexMove(domain.LadoSetupKindMCPServer, "figma"),
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the adoption ran to its record")
			}
		}()
		_, _ = f.adopt(f.service(), entries...)
	}()
	wantMissing(t, filepath.Join(f.claude, "CLAUDE.md"))
	list := adoptions(t, f.store)
	if len(list) != 1 || list[0].State != domain.LadoSetupAdoptionPending {
		t.Fatalf("adoptions %+v, want one left pending", list)
	}

	// The daemon starts again.
	f.deps.Store = f.store
	svc := f.service()
	if err := svc.RecoverAdoptions(ctx); err != nil {
		t.Fatal(err)
	}
	adoption, _, err := f.store.LadoSetupAdoption(ctx, list[0].ID)
	if err != nil || adoption.State != domain.LadoSetupAdoptionPartial || !strings.Contains(adoption.ErrorMessage, "interrupted") {
		t.Fatalf("adoption %+v (%v), want it partial as interrupted", adoption, err)
	}
	res, err := f.restore(svc, adoption.ID)
	if err != nil || !res.Applied {
		t.Fatalf("restore = %+v, %v", res, err)
	}
	if got := statuses(res.Report); len(got) != len(entries) || slices.ContainsFunc(got, func(s string) bool { return !strings.HasSuffix(s, " "+RestoreRestored) }) {
		t.Fatalf("restore report %v, want every original restored", got)
	}
	if diffs := diffStates(before, exactState(t, paths...)); len(diffs) > 0 {
		t.Fatalf("restore left differences:\n%s", strings.Join(diffs, "\n"))
	}
}

func TestPutBackFailuresAreReported(t *testing.T) {
	f := newAdoption(t)
	settings := filepath.Join(f.claude, "settings.json")
	// Claude Code, used meanwhile, rewrites settings.json, so LADO cannot
	// put its key back.
	f.deps.Store = &failingStore{recordingStore: f.store, before: func() {
		f.write(settings, readFile(t, settings)+"\n")
	}}
	_, err := f.adopt(f.service(), claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"), claudeMove(domain.LadoSetupKindSetting, "model"))
	details := wantAPIError(t, err, CodeAdoptionFailed)
	if failed, ok := details["putBackFailed"].([]string); !ok || !slices.Equal(failed, []string{"settings.json"}) || details["state"] != string(domain.LadoSetupAdoptionPartial) {
		t.Fatalf("details %v, want settings.json among the originals not put back", details)
	}
	wantFile(t, filepath.Join(f.claude, "CLAUDE.md"), "# me\n", 0o644)
	// The adoption stays partial, so restore finds the key in the backup.
	list := adoptions(t, f.store)
	if len(list) != 1 || list[0].State != domain.LadoSetupAdoptionPartial {
		t.Fatalf("adoptions %+v, want one partial", list)
	}
	res, err := f.restore(f.service(), list[0].ID)
	if err != nil || !slices.Equal(statuses(res.Report), []string{"model " + RestoreRestored}) {
		t.Fatalf("restore = %v (%v), want the model key back", statuses(res.Report), err)
	}
	wantJSON(t, "model", jsonKeys(t, settings)["model"], `"opus"`)
}

func TestRestoreSkipsItemsDeletedInLADO(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	svc := f.service()
	res := f.mustAdopt(svc,
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		claudeMove(domain.LadoSetupKindCommand, "c.md"),
		claudeMove(domain.LadoSetupKindSetting, "model"),
		claudeMove(domain.LadoSetupKindSetting, "permissions"),
	)
	ids := itemIDs(res)
	at := scanAt.Add(time.Hour)
	for _, name := range []string{"c.md", "model"} {
		if err := f.store.DeleteLadoSetupItem(ctx, ids[name], at, domain.LadoAuditEvent{At: at, Actor: "api", Action: "setup.item_deleted", SubjectKind: "setup_item", SubjectID: string(ids[name])}); err != nil {
			t.Fatal(err)
		}
	}
	f.addVersion(ids["CLAUDE.md"], 1, "# edited in LADO\n")

	out, err := f.restore(svc, res.AdoptionID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"CLAUDE.md " + RestoreRestored, "c.md " + RestoreSkippedDeleted, "model " + RestoreSkippedDeleted, "permissions " + RestoreRestored}
	if got := statuses(out.Report); !slices.Equal(got, want) {
		t.Fatalf("report %v, want %v", got, want)
	}
	for _, e := range out.Report.Entries {
		if wantNote := map[bool]string{true: RestoreNoteKeptInLado}[e.Name == "CLAUDE.md"]; e.Note != wantNote {
			t.Fatalf("%s has note %q, want %q", e.Name, e.Note, wantNote)
		}
	}
	wantFile(t, filepath.Join(f.claude, "CLAUDE.md"), "# me\n", 0o644)
	wantMissing(t, filepath.Join(f.claude, "commands", "c.md"))
	keys := jsonKeys(t, filepath.Join(f.claude, "settings.json"))
	wantJSON(t, "permissions", keys["permissions"], `{"allow":["Bash(ls)"]}`)
	if _, ok := keys["model"]; ok {
		t.Fatalf("restore put back the deleted model setting: %v", keys)
	}
}

// linkRoot moves a tool folder or file into dotfiles and links it back.
func (f *adoptionFixture) linkRoot(root, name string) string {
	f.t.Helper()
	target := filepath.Join(f.root, "dotfiles", name)
	f.mkdir(filepath.Dir(target))
	if err := os.Rename(root, target); err != nil {
		f.t.Fatal(err)
	}
	f.symlink(target, root)
	return target
}

func TestLinkedRootsRefuseAdoption(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	f.linkRoot(f.claude, "claude")
	f.linkRoot(f.claudeJSON, "claude.json")
	f.linkRoot(f.codex, "codex")
	svc := f.service()
	inv := mustScan(t, f.deps)
	for _, e := range []struct {
		location domain.LadoSetupLocation
		kind     domain.LadoSetupKind
		name     string
	}{
		{domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindInstructions, "CLAUDE.md"},
		{domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSkill, "review"},
		{domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSetting, "model"},
		{domain.LadoSetupLocationClaudeJSON, domain.LadoSetupKindMCPServer, "docs"},
		{domain.LadoSetupLocationCodexHome, domain.LadoSetupKindInstructions, "AGENTS.md"},
		{domain.LadoSetupLocationCodexHome, domain.LadoSetupKindMCPServer, "figma"},
	} {
		wantStatus(t, entryAt(t, inv, e.location, e.kind, e.name), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonRootIsLink)
	}
	dotfiles := treeState(t, filepath.Join(f.root, "dotfiles"))
	for _, entry := range []AdoptEntry{claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"), claudeMove(domain.LadoSetupKindMCPServer, "docs"), codexMove(domain.LadoSetupKindInstructions, "AGENTS.md")} {
		_, err := svc.Adopt(ctx, AdoptRequest{Entries: []AdoptEntry{entry}, DryRun: true})
		wantAPIError(t, err, CodeRootIsLink)
		_, err = svc.Adopt(ctx, AdoptRequest{Entries: []AdoptEntry{entry}, PlanSHA256: strings.Repeat("a", 64), Presence: f.presence()})
		wantAPIError(t, err, CodeRootIsLink)
	}
	if diffs := diffStates(dotfiles, treeState(t, filepath.Join(f.root, "dotfiles"))); len(diffs) > 0 {
		t.Fatalf("refused adoptions changed the dotfiles:\n%s", strings.Join(diffs, "\n"))
	}

	// Restore is not held back: an adoption made before the folder was
	// linked puts its original back through the link.
	g := newAdoption(t)
	res := g.mustAdopt(g.service(), claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"))
	target := g.linkRoot(g.claude, "claude")
	if out, err := g.restore(g.service(), res.AdoptionID); err != nil || !out.Applied {
		t.Fatalf("restore = %+v, %v", out, err)
	}
	wantFile(t, filepath.Join(target, "CLAUDE.md"), "# me\n", 0o644)
}
