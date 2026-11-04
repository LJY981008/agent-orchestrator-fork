package ladolaunch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeStore struct {
	sessions map[domain.SessionID]domain.SessionRecord
	lado     map[domain.ProjectID]bool
	global   domain.LadoWatchOverrides
	project  map[domain.ProjectID]domain.LadoWatchOverrides
	audits   []domain.LadoAuditEvent
	err      error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions: map[domain.SessionID]domain.SessionRecord{},
		lado:     map[domain.ProjectID]bool{},
		project:  map[domain.ProjectID]domain.LadoWatchOverrides{},
	}
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := f.sessions[id]
	return rec, ok, f.err
}

func (f *fakeStore) LadoProjectSettings(_ context.Context, id domain.ProjectID) (domain.LadoProjectSettings, error) {
	return domain.LadoProjectSettings{ProjectID: id, LadoMode: f.lado[id]}, f.err
}

func (f *fakeStore) LadoEffectiveWatchSettings(_ context.Context, id domain.ProjectID) (domain.LadoWatchSettings, error) {
	return domain.LadoWatchEffective(f.global, f.project[id]), f.err
}

func (f *fakeStore) AppendLadoAuditEvent(_ context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error) {
	f.audits = append(f.audits, ev)
	return ev, nil
}

type fakeProjects map[string]domain.ProjectRecord

func (f fakeProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	rec, ok := f[id]
	return rec, ok, nil
}

func intPtr(n int) *int { return &n }

type fixture struct {
	store    *fakeStore
	projects fakeProjects
	home     string
	dataDir  string
	repo     string
	env      map[string]string
	svc      *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		store:    newFakeStore(),
		projects: fakeProjects{},
		home:     t.TempDir(),
		dataDir:  t.TempDir(),
		repo:     t.TempDir(),
		env:      map[string]string{},
	}
	f.store.lado["lado"] = true
	f.projects["lado"] = domain.ProjectRecord{ID: "lado", Path: f.repo}
	f.projects["plain"] = domain.ProjectRecord{ID: "plain", Path: f.repo}
	f.svc = New(Options{
		DataDir: f.dataDir, Store: f.store, Projects: f.projects, Home: f.home,
		Getenv: func(k string) string { return f.env[k] },
		Now:    func() time.Time { return time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC) },
	})
	return f
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompactWindowGoesOnlyToLadoModeProjects(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if n, ok, err := f.svc.CompactWindow(ctx, "lado"); err != nil || !ok || n != domain.LadoDefaultCompactWindow {
		t.Fatalf("default = %d %v %v; want %d", n, ok, err, domain.LadoDefaultCompactWindow)
	}
	f.store.global.CompactWindow = intPtr(300_000)
	if n, _, _ := f.svc.CompactWindow(ctx, "lado"); n != 300_000 {
		t.Fatalf("global = %d, want 300000", n)
	}
	f.store.project["lado"] = domain.LadoWatchOverrides{CompactWindow: intPtr(150_000)}
	if n, _, _ := f.svc.CompactWindow(ctx, "lado"); n != 150_000 {
		t.Fatalf("project over global = %d, want 150000", n)
	}
	if n, ok, err := f.svc.CompactWindow(ctx, "plain"); err != nil || ok || n != 0 {
		t.Fatalf("LADO mode off = %d %v %v; want nothing", n, ok, err)
	}
	f.store.err = errors.New("db closed")
	if _, ok, err := f.svc.CompactWindow(ctx, "lado"); err == nil || ok {
		t.Fatal("a store error was not reported")
	}
}

func TestLaunchValuesPerHarness(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rec := domain.SessionRecord{ID: "s-1", ProjectID: "lado"}
	claude, err := f.svc.LaunchValues(ctx, rec, domain.HarnessClaudeCode, map[string]string{})
	if err != nil || !reflect.DeepEqual(claude, map[string]string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "200000"}) {
		t.Fatalf("claude = %v, %v", claude, err)
	}
	codex, err := f.svc.LaunchValues(ctx, rec, domain.HarnessCodex, nil)
	if err != nil || !reflect.DeepEqual(codex, map[string]string{domain.LadoCodexCompactLimitEnv: "200000"}) {
		t.Fatalf("codex = %v, %v", codex, err)
	}
	if other, err := f.svc.LaunchValues(ctx, rec, domain.HarnessGemini, nil); err != nil || other != nil {
		t.Fatalf("gemini = %v, %v; want none", other, err)
	}
	rec.ProjectID = "plain"
	if plain, err := f.svc.LaunchValues(ctx, rec, domain.HarnessClaudeCode, nil); err != nil || plain != nil {
		t.Fatalf("LADO mode off = %v, %v; want none", plain, err)
	}
	if reviewer, err := f.svc.ReviewerValues(ctx, "lado", domain.HarnessClaudeCode); err != nil || reviewer["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "200000" {
		t.Fatalf("reviewer = %v, %v", reviewer, err)
	}
	if reviewer, err := f.svc.ReviewerValues(ctx, "plain", domain.HarnessClaudeCode); err != nil || reviewer != nil {
		t.Fatalf("reviewer of a plain project = %v, %v; want none", reviewer, err)
	}
}

func TestCodexOverridesNameTheSessionsProject(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.store.sessions["s-1"] = domain.SessionRecord{ID: "s-1", ProjectID: "lado"}
	f.store.sessions["s-2"] = domain.SessionRecord{ID: "s-2", ProjectID: "plain"}
	f.store.project["lado"] = domain.LadoWatchOverrides{CompactWindow: intPtr(120_000)}
	got, err := f.svc.CodexOverrides(ctx, "s-1")
	if err != nil || !reflect.DeepEqual(got, []string{"-c", "model_auto_compact_token_limit=120000"}) {
		t.Fatalf("overrides = %q, %v", got, err)
	}
	for _, id := range []string{"s-2", "missing", ""} {
		if got, err := f.svc.CodexOverrides(ctx, id); err != nil || got != nil {
			t.Fatalf("%q: %q, %v; want none", id, got, err)
		}
	}
}

func TestCompactInfoFindsTheSettingsThatTurnAutoCompactOff(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	info, err := f.svc.CompactInfo(ctx, "lado")
	if err != nil || !info.Applies || info.Window != 200_000 || len(info.Warnings) != 0 {
		t.Fatalf("clean = %+v, %v", info, err)
	}

	projectSettings := filepath.Join(f.repo, ".claude", "settings.json")
	// A larger autoCompactWindow is no warning: the variable wins over it.
	writeJSON(t, projectSettings, map[string]any{"autoCompactWindow": 900_000, "autoCompactEnabled": false})
	localSettings := filepath.Join(f.repo, ".claude", "settings.local.json")
	writeJSON(t, localSettings, map[string]any{"env": map[string]any{"DISABLE_AUTO_COMPACT": "1", "DISABLE_COMPACT": "0"}})
	userSettings := filepath.Join(f.home, ".claude", "settings.json")
	writeJSON(t, userSettings, map[string]any{"env": map[string]any{"DISABLE_COMPACT": true}})
	userConfig := filepath.Join(f.home, ".claude.json")
	writeJSON(t, userConfig, map[string]any{"autoCompactEnabled": false, "projects": map[string]any{}})
	ladoSettings := filepath.Join(f.dataDir, "lado", "harness", "claude", "settings.json")
	writeJSON(t, ladoSettings, map[string]any{"autoCompactEnabled": true, "env": map[string]any{"DISABLE_AUTO_COMPACT": "true"}})
	f.projects["lado"] = domain.ProjectRecord{ID: "lado", Path: f.repo, Config: domain.ProjectConfig{Env: map[string]string{"DISABLE_AUTO_COMPACT": "yes"}}}
	f.env["DISABLE_COMPACT"] = "on"

	info, err = f.svc.CompactInfo(ctx, "lado")
	if err != nil {
		t.Fatal(err)
	}
	want := []CompactWarning{
		{Source: SourceProjectSettings, Setting: SettingAutoCompactEnabled, Path: projectSettings},
		{Source: SourceProjectLocalSettings, Setting: SettingDisableAutoCompact, Path: localSettings},
		{Source: SourceUserSettings, Setting: SettingDisableCompact, Path: userSettings},
		{Source: SourceUserConfig, Setting: SettingAutoCompactEnabled, Path: userConfig},
		{Source: SourceLadoSettings, Setting: SettingDisableAutoCompact, Path: ladoSettings},
		{Source: SourceProjectEnv, Setting: SettingDisableAutoCompact},
		{Source: SourceDaemonEnv, Setting: SettingDisableCompact},
	}
	if !reflect.DeepEqual(info.Warnings, want) {
		t.Fatalf("warnings =\n%+v\nwant\n%+v", info.Warnings, want)
	}

	// A project with LADO mode off still shows its setting and warnings.
	plain, err := f.svc.CompactInfo(ctx, "plain")
	if err != nil || plain.Applies || plain.Window != 200_000 || len(plain.Warnings) == 0 {
		t.Fatalf("plain = %+v, %v", plain, err)
	}
}

func TestCompactInfoReadsTheConfigFolderTheLaunchNames(t *testing.T) {
	f := newFixture(t)
	account := t.TempDir()
	writeJSON(t, filepath.Join(account, ".claude.json"), map[string]any{"autoCompactEnabled": false})
	// The home's own config is not the one the session runs on.
	writeJSON(t, filepath.Join(f.home, ".claude.json"), map[string]any{"autoCompactEnabled": false})
	got := f.svc.compactWarnings(context.Background(), compactInput{project: "lado", env: map[string]string{"CLAUDE_CONFIG_DIR": account}})
	want := []CompactWarning{{Source: SourceUserConfig, Setting: SettingAutoCompactEnabled, Path: filepath.Join(account, ".claude.json")}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("warnings = %+v, want %+v", got, want)
	}
}

func TestLaunchRecordsCompactBlockedOncePerSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	workspace := t.TempDir()
	rec := domain.SessionRecord{ID: "s-1", ProjectID: "lado", Metadata: domain.SessionMetadata{WorkspacePath: workspace}}

	if _, err := f.svc.LaunchValues(ctx, rec, domain.HarnessClaudeCode, map[string]string{}); err != nil || len(f.store.audits) != 0 {
		t.Fatalf("a clean start recorded %d audits (%v)", len(f.store.audits), err)
	}

	writeJSON(t, filepath.Join(workspace, ".claude", "settings.json"), map[string]any{"autoCompactEnabled": false})
	env := map[string]string{"DISABLE_AUTO_COMPACT": "1"}
	for range 2 {
		if _, err := f.svc.LaunchValues(ctx, rec, domain.HarnessClaudeCode, env); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.store.audits) != 1 {
		t.Fatalf("audits = %d, want one per session", len(f.store.audits))
	}
	ev := f.store.audits[0]
	if ev.Action != AuditCompactBlocked || ev.SubjectKind != "session" || ev.SubjectID != "s-1" {
		t.Fatalf("audit = %+v", ev)
	}
	var payload struct {
		ProjectID string           `json:"projectId"`
		Warnings  []CompactWarning `json:"warnings"`
	}
	if err := json.Unmarshal(ev.Payload, &payload); err != nil || payload.ProjectID != "lado" || len(payload.Warnings) != 2 {
		t.Fatalf("payload = %s (%v)", ev.Payload, err)
	}

	// Codex and a project with LADO mode off record nothing.
	if _, err := f.svc.LaunchValues(ctx, domain.SessionRecord{ID: "s-2", ProjectID: "lado", Metadata: rec.Metadata}, domain.HarnessCodex, env); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.LaunchValues(ctx, domain.SessionRecord{ID: "s-3", ProjectID: "plain", Metadata: rec.Metadata}, domain.HarnessClaudeCode, env); err != nil {
		t.Fatal(err)
	}
	if len(f.store.audits) != 1 {
		t.Fatalf("audits = %d after Codex and a plain project, want still one", len(f.store.audits))
	}
}
