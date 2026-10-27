package ladosetup

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// effectiveFixture is a service over a fake home with a real store and blob
// store, where tests create managed items directly.
type effectiveFixture struct {
	*fakeHome
	store *sqlite.Store
	blobs *ladoblob.Store
	svc   *Service
	items int
}

func newEffectiveFixture(t *testing.T, projects fakeProjects) *effectiveFixture {
	t.Helper()
	h := newFakeHome(t)
	blobs := ladoblob.New(h.state)
	t.Cleanup(func() { _ = blobs.Close() })
	store := sqlitetest.MustOpen(t)
	deps := h.deps()
	deps.Store, deps.Blobs, deps.Projects = store, blobs, projects
	return &effectiveFixture{fakeHome: h, store: store, blobs: blobs, svc: New(deps)}
}

// item stores a live item made in LADO whose version 1 holds content.
func (f *effectiveFixture) item(tool domain.LadoSetupTool, kind domain.LadoSetupKind, name, content string) domain.LadoSetupItemID {
	f.t.Helper()
	f.items++
	id := domain.LadoSetupItemID(fmt.Sprintf("setup-%d", f.items))
	sum, err := f.blobs.Put([]byte(content))
	if err != nil {
		f.t.Fatal(err)
	}
	contentKind := domain.LadoSetupContentFile
	if isKeyKind(kind) {
		contentKind = domain.LadoSetupContentJSON
	}
	err = f.store.CreateLadoSetupItem(context.Background(),
		domain.LadoSetupItem{ID: id, Tool: tool, Kind: kind, Name: name, CreatedAt: scanAt},
		domain.LadoSetupItemVersion{ID: domain.LadoSetupItemVersionID(string(id) + "-v1"), ContentKind: contentKind, SHA256: sum, Size: int64(len(content)), Actor: "api", CreatedAt: scanAt},
		domain.LadoAuditEvent{At: scanAt, Actor: "api", Action: "setup.item_created", SubjectKind: "setup_item", SubjectID: string(id), Payload: json.RawMessage(`{}`)},
		nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *effectiveFixture) effective(scope ladopolicy.SetupScope) ladopolicy.SetupResolution {
	f.t.Helper()
	res, err := f.svc.EffectiveSetup(context.Background(), scope)
	if err != nil {
		f.t.Fatalf("EffectiveSetup: %v", err)
	}
	return res
}

func TestEffectiveSetupHashesInstalledPlugins(t *testing.T) {
	f := newEffectiveFixture(t, nil)
	plugins := f.item(domain.LadoSetupToolClaudeCode, domain.LadoSetupKindSetting, enabledPluginsKey, `{"lint@market":false,"review@market":true}`)
	f.item(domain.LadoSetupToolClaudeCode, domain.LadoSetupKindSetting, "model", `"opus"`)
	installed := filepath.Join(f.claude, "plugins", "installed_plugins.json")
	install := func(review, lint string) {
		f.write(installed, fmt.Sprintf(`{"version":2,"plugins":{
			"review@market":[{"scope":"project","installPath":"/elsewhere","version":"9"},{"scope":"user","installPath":%q,"version":%q,"gitCommitSha":"abc","lastUpdated":"%s"}],
			"lint@market":[{"scope":"user","installPath":"/lint","version":%q}]}}`,
			filepath.Join(f.claude, "plugins", "cache", "review"), review, review, lint))
	}
	scope := ladopolicy.SetupScope{ProjectID: "demo", Harness: domain.HarnessClaudeCode}
	sums := func() map[domain.LadoSetupItemID]string {
		t.Helper()
		out := map[domain.LadoSetupItemID]string{}
		for _, item := range f.effective(scope).Items {
			out[item.ItemID] = item.InstalledSHA256
		}
		return out
	}

	install("1.0.0", "1.0.0")
	first := sums()
	if len(first[plugins]) != 64 || len(first) != 2 || slices.ContainsFunc(f.effective(scope).Items, func(item ladopolicy.SetupItemState) bool {
		return item.ItemID != plugins && item.InstalledSHA256 != ""
	}) {
		t.Fatalf("installed sums %v, want one for the enabledPlugins setting only", first)
	}
	// Only what the setting turns on counts, and only its user install.
	install("1.0.0", "2.0.0")
	if got := sums(); got[plugins] != first[plugins] {
		t.Fatalf("a disabled plugin's update changed the sum: %s, was %s", got[plugins], first[plugins])
	}
	install("1.1.0", "2.0.0")
	updated := sums()
	if updated[plugins] == first[plugins] {
		t.Fatalf("an enabled plugin's update kept the sum %s", first[plugins])
	}
	if err := os.Remove(installed); err != nil {
		t.Fatal(err)
	}
	if got := sums(); got[plugins] == updated[plugins] || got[plugins] == first[plugins] || len(got[plugins]) != 64 {
		t.Fatalf("no plugins file: sum %q, want another one", got[plugins])
	}
	f.write(installed, "{not json")
	if got := sums(); got[plugins] == "" || got[plugins] == updated[plugins] {
		t.Fatalf("an unreadable plugins file: sum %q, want one of its own", got[plugins])
	}
	// Turned off, the setting is not delivered and nothing is hashed.
	for _, item := range f.effective(ladopolicy.SetupScope{ProjectID: "demo", Harness: domain.HarnessClaudeCode, Change: &ladopolicy.SetupChange{
		Binding: &domain.LadoSetupBinding{ItemID: plugins, Scope: domain.LadoScopeProject, ScopeID: "demo", State: domain.LadoBindingForceOff},
	}}).Items {
		if item.ItemID == plugins && (item.On || item.InstalledSHA256 != "") {
			t.Fatalf("an off enabledPlugins setting = %+v, want off without a sum", item)
		}
	}
}

func TestScopeConflictsShowsWhatSnapshotsLeaveOut(t *testing.T) {
	h := newFakeHome(t)
	repo := h.mkdir(filepath.Join(h.root, "repo"))
	h.write(filepath.Join(repo, ".claude", "settings.json"), `{"model":"sonnet"}`)
	f := newEffectiveFixture(t, fakeProjects{{ID: "demo", Path: repo}})
	model := f.item(domain.LadoSetupToolClaudeCode, domain.LadoSetupKindSetting, "model", `"opus"`)
	ao := f.item(domain.LadoSetupToolCodex, domain.LadoSetupKindMCPServer, "ao", `{"command":"ao"}`)
	codes := func(conflicts []Conflict) map[string]domain.LadoSetupItemID {
		out := map[string]domain.LadoSetupItemID{}
		for _, c := range conflicts {
			out[c.Code] = c.ItemID
		}
		return out
	}

	// The app's list holds both tools' items and the project's repository
	// settings.
	got, err := f.svc.ScopeConflicts(context.Background(), ScopeConflictsInput{ProjectID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]domain.LadoSetupItemID{ConflictMCPNameTaken: ao, ConflictProjectOverrides: model}; !maps.Equal(codes(got), want) {
		t.Fatalf("ScopeConflicts = %+v, want %v", got, want)
	}
	global, err := f.svc.ScopeConflicts(context.Background(), ScopeConflictsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]domain.LadoSetupItemID{ConflictMCPNameTaken: ao}; !maps.Equal(codes(global), want) {
		t.Fatalf("Global ScopeConflicts = %+v, want %v", global, want)
	}
	// A snapshot takes the agent tool's items, without the repository file.
	claude := f.effective(ladopolicy.SetupScope{ProjectID: "demo", Harness: domain.HarnessClaudeCode})
	codex := f.effective(ladopolicy.SetupScope{ProjectID: "demo", Harness: domain.HarnessCodex})
	if len(claude.Problems) != 0 || len(codex.Problems) != 1 || codex.Problems[0].Code != ConflictMCPNameTaken || codex.Problems[0].Details["item"] != string(ao) {
		t.Fatalf("Claude Code problems %+v, Codex problems %+v; want only Codex's ao server", claude.Problems, codex.Problems)
	}
}
