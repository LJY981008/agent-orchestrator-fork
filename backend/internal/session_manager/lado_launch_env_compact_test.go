package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	claudeagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladolaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// compactStore is what ladolaunch reads: which projects are in LADO mode and
// their compact windows.
type compactStore struct {
	lado    map[domain.ProjectID]bool
	windows map[domain.ProjectID]int
}

func (s compactStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{}, false, nil
}

func (s compactStore) LadoProjectSettings(_ context.Context, id domain.ProjectID) (domain.LadoProjectSettings, error) {
	return domain.LadoProjectSettings{ProjectID: id, LadoMode: s.lado[id]}, nil
}

func (s compactStore) LadoEffectiveWatchSettings(_ context.Context, id domain.ProjectID) (domain.LadoWatchSettings, error) {
	var o domain.LadoWatchOverrides
	if n, ok := s.windows[id]; ok {
		o.CompactWindow = &n
	}
	return domain.LadoWatchEffective(domain.LadoWatchOverrides{}, o), nil
}

func (s compactStore) AppendLadoAuditEvent(_ context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error) {
	return ev, nil
}

func compactValues(t *testing.T, lado map[domain.ProjectID]bool, windows map[domain.ProjectID]int) *ladolaunch.Service {
	t.Helper()
	return ladolaunch.New(ladolaunch.Options{
		DataDir: t.TempDir(), Home: t.TempDir(), Getenv: func(string) string { return "" },
		Store: compactStore{lado: lado, windows: windows},
	})
}

// fixedReadDirs lets a LADO run's sessions read one folder.
func fixedReadDirs(dir string) LadoReadDirectories {
	return func(_ context.Context, rec domain.SessionRecord) ([]string, error) {
		if _, ok := domain.ParseLadoRunClientRequestID(rec.ClientRequestID); !ok {
			return nil, nil
		}
		return []string{dir}, nil
	}
}

// A Chat worker of a project in LADO mode starts with LADO's compact window,
// over the project's own variable, and may read its run's plan folder.
func TestChatSpawnGetsTheCompactWindowAndThePlanFolder(t *testing.T) {
	for _, tt := range []struct {
		name       string
		lado       bool
		clientID   string
		wantWindow string
		wantDir    bool
	}{
		{name: "LADO worker", lado: true, clientID: "lado:entry-1/1", wantWindow: "150000", wantDir: true},
		{name: "other session of a LADO project", lado: true, wantWindow: "150000"},
		{name: "project without LADO mode", clientID: "lado:entry-1/1", wantDir: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			launcher := &recordingLauncher{}
			m, st, _ := newChatManager(launcher)
			project := st.projects[string(chatTestProject)]
			project.Config.Env = map[string]string{domain.LadoClaudeCompactWindowEnv: "900000"}
			st.projects[string(chatTestProject)] = project
			m.SetLadoLaunchValues(compactValues(t,
				map[domain.ProjectID]bool{chatTestProject: tt.lado},
				map[domain.ProjectID]int{chatTestProject: 150_000}))
			runs := filepath.Join(t.TempDir(), "lado", "runs", "rev-1")
			m.SetLadoAdditionalDirectories(fixedReadDirs(runs))

			_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
				ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
				RequestedMode: domain.SessionModeChat, ClientRequestID: tt.clientID, ClientRequestHash: "hash",
			})
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			start := launcher.started[0]
			want := tt.wantWindow
			if want == "" {
				want = "900000" // the project's own value stays
			}
			if got := start.Env[domain.LadoClaudeCompactWindowEnv]; got != want {
				t.Fatalf("%s = %q, want %q", domain.LadoClaudeCompactWindowEnv, got, want)
			}
			if got := slices.Contains(start.AdditionalDirectories, runs); got != tt.wantDir {
				t.Fatalf("additional directories %q hold the runs folder: %v, want %v", start.AdditionalDirectories, got, tt.wantDir)
			}
		})
	}
}

// ladoResume resumes one Claude Code Chat session of a project with or
// without LADO mode, with no LADO config home. Its worktree asks for a larger
// window in .claude/settings.json.
func ladoResume(t *testing.T, ladoMode bool, clientRequestID string) ChatStart {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	store := sqlitetest.MustOpenAt(t, t.TempDir())
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "proj", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings, _ := json.Marshal(map[string]any{"autoCompactWindow": 900_000})
	if err := os.WriteFile(filepath.Join(workspace, ".claude", "settings.json"), settings, 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	created, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "proj", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		ClientRequestID: clientRequestID,
		Metadata: domain.SessionMetadata{
			WorkspacePath: workspace, Branch: "ao/session",
			ProviderConversationID: "92fadfcb-3d31-4dd6-93c1-097bf823cc19", ControllerGeneration: "chat-generation",
		},
		Activity:      domain.Activity{State: domain.ActivityExited, LastActivityAt: now},
		FirstSignalAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConversation(ctx, "conversation-1", domain.ConversationScopeSession, "proj", created.ID, now); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	manager := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: claudeagent.New()}, Workspace: &fakeWorkspace{},
		Store: store, Messenger: &fakeMessenger{}, Chat: launcher,
		Lifecycle: &sqliteTransitionLifecycle{store: store},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	manager.SetLadoLaunchValues(compactValues(t, map[domain.ProjectID]bool{"proj": ladoMode}, nil))
	manager.SetLadoAdditionalDirectories(fixedReadDirs("/data/lado/runs/rev-1"))
	if _, err := manager.ResumeAgentWithMode(ctx, created.ID); err != nil {
		t.Fatalf("ResumeAgentWithMode: %v", err)
	}
	if len(launcher.started) != 1 {
		t.Fatalf("started %d chat controllers, want 1", len(launcher.started))
	}
	return launcher.started[0]
}

func TestChatResumeGetsTheCompactWindowWithoutASetupHome(t *testing.T) {
	start := ladoResume(t, true, "lado:entry-1/integration/1")
	if got := start.Env[domain.LadoClaudeCompactWindowEnv]; got != "200000" {
		t.Fatalf("%s = %q, want the default 200000", domain.LadoClaudeCompactWindowEnv, got)
	}
	if !slices.Contains(start.AdditionalDirectories, "/data/lado/runs/rev-1") {
		t.Fatalf("additional directories = %q, want the run's plan folder", start.AdditionalDirectories)
	}
}

func TestChatResumeOfAProjectWithoutLadoModeGetsNoCompactWindow(t *testing.T) {
	start := ladoResume(t, false, "")
	if got, ok := start.Env[domain.LadoClaudeCompactWindowEnv]; ok {
		t.Fatalf("%s = %q, want none", domain.LadoClaudeCompactWindowEnv, got)
	}
	if slices.Contains(start.AdditionalDirectories, "/data/lado/runs/rev-1") {
		t.Fatalf("additional directories = %q, want no plan folder for a session that is not a LADO run's", start.AdditionalDirectories)
	}
}

// prepareWorkspace starts every terminal launch, the orchestrator's included,
// with applyLadoSetupHome: the compact window goes on for Claude Code, and the
// value the Codex Chat driver moves into the thread config for Codex.
func TestApplyLadoSetupHomePutsTheCompactWindowOnEveryAgentStart(t *testing.T) {
	ctx := context.Background()
	claude := claudeagent.New()
	st := newFakeStore()
	st.sessions["orch"] = domain.SessionRecord{ID: "orch", ProjectID: "lado", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode}
	st.sessions["plain"] = domain.SessionRecord{ID: "plain", ProjectID: "other", Harness: domain.HarnessClaudeCode}
	st.sessions["codex"] = domain.SessionRecord{ID: "codex", ProjectID: "lado", Harness: domain.HarnessCodex}
	m := New(Deps{Store: st, Agents: switchTestAgents{domain.HarnessClaudeCode: claude, domain.HarnessCodex: fakeAgent{}}})
	m.SetLadoLaunchValues(compactValues(t, map[domain.ProjectID]bool{"lado": true}, nil))

	env := map[string]string{"PATH": "/bin", domain.LadoClaudeCompactWindowEnv: "900000"}
	if err := m.applyLadoSetupHome(ctx, claude, "orch", env); err != nil {
		t.Fatal(err)
	}
	if env[domain.LadoClaudeCompactWindowEnv] != "200000" {
		t.Fatalf("orchestrator env = %v, want LADO's window over the project's", env)
	}

	plain := map[string]string{"PATH": "/bin"}
	if err := m.applyLadoSetupHome(ctx, claude, "plain", plain); err != nil {
		t.Fatal(err)
	}
	if _, ok := plain[domain.LadoClaudeCompactWindowEnv]; ok {
		t.Fatalf("a project without LADO mode got %v", plain)
	}

	codexEnv := map[string]string{"PATH": "/bin"}
	if err := m.applyLadoSetupHome(ctx, fakeAgent{}, "codex", codexEnv); err != nil {
		t.Fatal(err)
	}
	if codexEnv[domain.LadoCodexCompactLimitEnv] != "200000" {
		t.Fatalf("codex env = %v, want %s=200000", codexEnv, domain.LadoCodexCompactLimitEnv)
	}
	if _, ok := codexEnv[domain.LadoClaudeCompactWindowEnv]; ok {
		t.Fatalf("codex env = %v holds Claude's variable", codexEnv)
	}

	// A missing record never fails a launch without a config home.
	if err := m.applyLadoSetupHome(ctx, claude, "missing", map[string]string{}); err != nil {
		t.Fatalf("missing record = %v, want nil", err)
	}
}

func TestLadoChatDirectoriesKeepsTheCallersSlice(t *testing.T) {
	m := New(Deps{Store: newFakeStore()})
	m.SetLadoAdditionalDirectories(func(context.Context, domain.SessionRecord) ([]string, error) {
		return []string{"/runs/rev-1", "relative", "/runs/rev-1"}, nil
	})
	base := make([]string, 1, 4)
	base[0] = "/repo"
	got := m.ladoChatDirectories(context.Background(), domain.SessionRecord{}, base)
	// base has room to grow; the added folder must not land in its array.
	if !slices.Equal(got, []string{"/repo", "/runs/rev-1"}) || base[:2][1] != "" {
		t.Fatalf("dirs = %q, base %q; want the plan folder once, absolute only, the caller's slice untouched", got, base[:2])
	}
}
