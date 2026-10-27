package ladosetup

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

// The app's views read what changes and adoptions left: the managed items
// at Global, each adoption's size, every version of an item, also once it is
// deleted, and an adoption's backup and restore report.
func TestViewsShowItemsVersionsAndAdoptions(t *testing.T) {
	ctx := context.Background()
	f := newChangeFixture(t)
	adopted := f.mustAdopt(f.svc, claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"), claudeMove(domain.LadoSetupKindSetting, "model"))
	ids := itemIDs(adopted)
	f.mustApply(Change{Op: OpEdit, ItemID: ids["CLAUDE.md"], BaseVersion: 1, Content: jsonText("# me, edited\n")}, false)
	f.mustApply(Change{Op: OpBinding, ItemID: ids["model"], Binding: ChangeBinding{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOff}}, false)

	overview, err := f.svc.Overview(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	global := map[string]domain.LadoBindingState{}
	for _, it := range overview.Items {
		global[it.Name] = it.Global
	}
	if global["CLAUDE.md"] != domain.LadoBindingOn || global["model"] != domain.LadoBindingOff || len(global) != 2 ||
		overview.AdoptionItems[adopted.AdoptionID] != 2 || len(overview.Adoptions) != 1 {
		t.Fatalf("overview items %+v, adoption items %v", overview.Items, overview.AdoptionItems)
	}

	view, err := f.svc.Item(ctx, ids["CLAUDE.md"])
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Versions) != 2 || view.Versions[0].Content == nil || view.Versions[0].Content.Text != "# me\n" ||
		view.Versions[1].Content == nil || view.Versions[1].Content.Text != "# me, edited\n" || view.ManagedPath == "" || len(view.Warnings) != 0 {
		t.Fatalf("item view = %+v", view)
	}
	f.mustApply(Change{Op: OpDelete, ItemID: ids["CLAUDE.md"]}, false)
	if view, err = f.svc.Item(ctx, ids["CLAUDE.md"]); err != nil || view.Item.Live() || view.ManagedPath != "" || len(view.Versions) != 2 {
		t.Fatalf("deleted item view = %+v, %v; want its versions without a managed path", view, err)
	}
	if _, err := f.svc.Item(ctx, "no-such-item"); err == nil {
		t.Fatal("an unknown item has a view")
	} else {
		wantAPIError(t, err, CodeNotFound)
	}

	adoption, err := f.svc.Adoption(ctx, adopted.AdoptionID)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, be := range adoption.Backup {
		paths = append(paths, string(be.Root)+":"+be.Path+":"+be.Type)
	}
	if !slices.Equal(paths, []string{"claude_dir:CLAUDE.md:file", "claude_dir:settings.json#model:keys"}) || len(adoption.Items) != 2 || adoption.Report != nil {
		t.Fatalf("adoption view = %+v, backup %v", adoption, paths)
	}
	if _, err := f.restore(f.svc, adopted.AdoptionID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if adoption, err = f.svc.Adoption(ctx, adopted.AdoptionID); err != nil || adoption.Report == nil || len(adoption.Report.Entries) != 2 {
		t.Fatalf("restored adoption view = %+v, %v; want its report", adoption, err)
	}
	if overview, err = f.svc.Overview(ctx, true); err != nil || len(overview.Items) != 0 || overview.AdoptionItems[adopted.AdoptionID] != 2 {
		t.Fatalf("overview after the restore = %+v, %v", overview, err)
	}
}

// A scope view names Global, a project LADO knows, or a task, whose project
// applies; the conflicts compare the default mode with the policy's ceiling.
func TestScopeViewsCheckTheScope(t *testing.T) {
	ctx := context.Background()
	f := newChangeFixture(t)
	ids := f.adopt(claudeMove(domain.LadoSetupKindSetting, "model"))
	f.mustApply(Change{Op: OpBinding, ItemID: ids["model"], Binding: ChangeBinding{Scope: domain.LadoScopeProject, ScopeID: testProject, State: domain.LadoBindingForceOff}}, false)

	items, err := f.svc.ScopeItems(ctx, "", testTask)
	if err != nil {
		t.Fatal(err)
	}
	if want := (ladopolicy.Source{Scope: domain.LadoScopeProject, ScopeID: testProject, State: domain.LadoBindingForceOff}); len(items) != 1 ||
		items[0].On || items[0].Project != domain.LadoBindingForceOff || items[0].Task != domain.LadoBindingInherit || items[0].Source != want {
		t.Fatalf("task scope items = %+v, want the project's force_off", items)
	}
	_, err = f.svc.ScopeItems(ctx, "gone", "")
	wantAPIError(t, err, codeScopeNotFound)
	_, err = f.svc.ScopeConflicts(ctx, ScopeConflictsInput{ProjectID: "other", TaskID: testTask})
	wantAPIError(t, err, CodeScopeInvalid)

	permissions := json.RawMessage(`{"defaultMode":"bypassPermissions"}`)
	f.mustApply(Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: "permissions", Content: permissions}, true)
	aboveCeiling := func() bool {
		t.Helper()
		conflicts, err := f.svc.ScopeConflicts(ctx, ScopeConflictsInput{})
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(conflicts, func(c Conflict) bool { return c.Code == ConflictDefaultModeAboveCeiling })
	}
	if aboveCeiling() {
		t.Fatal("without a policy the default mode was compared with a ceiling")
	}
	f.svc.SetPolicy(&impactPolicy{})
	if !aboveCeiling() {
		t.Fatal("the default mode above the policy's auto ceiling is no conflict")
	}
}
