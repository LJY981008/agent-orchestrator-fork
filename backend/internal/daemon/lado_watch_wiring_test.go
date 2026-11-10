package daemon

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoproject"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// ladoWatchSink stands for the session manager and keeps the setup hooks and
// the worker watch's.
type ladoWatchSink struct {
	ladoSetupSink
	values sessionmanager.LadoLaunchValues
	dirs   sessionmanager.LadoReadDirectories
}

func (s *ladoWatchSink) SetLadoLaunchValues(v sessionmanager.LadoLaunchValues) { s.values = v }

func (s *ladoWatchSink) SetLadoAdditionalDirectories(d sessionmanager.LadoReadDirectories) {
	s.dirs = d
}

// ladoCompactCodexAgent stands for the Codex adapter and keeps both of its
// LADO -c sources.
type ladoCompactCodexAgent struct {
	ladoCodexAgent
	compact func(ctx context.Context, sessionID string) ([]string, error)
}

func (a *ladoCompactCodexAgent) SetLadoCompactOverrides(fn func(ctx context.Context, sessionID string) ([]string, error)) {
	a.compact = fn
}

// recordingSender keeps the messages the daemon sends.
type recordingSender struct {
	sent []string
}

func (s *recordingSender) Send(_ context.Context, id domain.SessionID, message string, _ *ports.SpawnAttachment) error {
	s.sent = append(s.sent, string(id)+": "+message)
	return nil
}

// The worker watch's launch hooks go into the session manager, the Codex
// adapter and the reviewer launcher with the setup's, and give a project in
// LADO mode its compact window (plan step 19).
func TestWireLadoWatchInstallsTheLaunchHooks(t *testing.T) {
	ctx := context.Background()
	useLadoStoredTokens(t, nil)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	store := sqlitetest.MustOpen(t)
	at := time.Now().UTC().Truncate(time.Second)
	for _, id := range []domain.ProjectID{"mer", "plain"} {
		if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: string(id), Path: t.TempDir(), RegisteredAt: at}); err != nil {
			t.Fatalf("seed project: %v", err)
		}
	}
	on := true
	if _, err := ladoproject.New(ladoproject.Deps{Store: store, Projects: store}).UpdateSettings(ctx, "mer", ladoproject.UpdateInput{LadoMode: &on}); err != nil {
		t.Fatalf("turn LADO mode on: %v", err)
	}
	sessions, codex := &ladoWatchSink{}, &ladoCompactCodexAgent{}
	var reviewerEnv reviewcore.LadoReviewerEnv

	work := wireLadoWork(ctx, cfg, store, &recordingProjectCloner{}, ladoWorkDeps{setup: ladoSetupDeps{
		sessions:    sessions,
		agents:      ladoAgents{domain.HarnessCodex: codex},
		reviewers:   func(reviewcore.LadoSystemSection) {},
		reviewerEnv: func(env reviewcore.LadoReviewerEnv) { reviewerEnv = env },
	}}, slog.New(slog.DiscardHandler))

	if sessions.values == nil || sessions.dirs == nil || codex.compact == nil || reviewerEnv == nil {
		t.Fatalf("hooks: launch values %v, read folders %v, Codex compact %v, reviewer env %v",
			sessions.values != nil, sessions.dirs != nil, codex.compact != nil, reviewerEnv != nil)
	}
	if work.watch.launch == nil || work.watch.usage == nil || work.watch.watch == nil || work.watch.alerts == nil {
		t.Fatalf("watch services = %+v", work.watch)
	}
	worker, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	values, err := sessions.values.LaunchValues(ctx, worker, domain.HarnessClaudeCode, map[string]string{})
	if err != nil || values[domain.LadoClaudeCompactWindowEnv] != "200000" {
		t.Fatalf("launch values of a LADO project's Claude session = %v, %v", values, err)
	}
	plain, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "plain", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if values, err := sessions.values.LaunchValues(ctx, plain, domain.HarnessClaudeCode, map[string]string{}); err != nil || len(values) != 0 {
		t.Fatalf("launch values of a plain AO project's session = %v, %v; want none", values, err)
	}
	if env, err := reviewerEnv(ctx, "mer", domain.HarnessClaudeCode); err != nil || env[domain.LadoClaudeCompactWindowEnv] != "200000" {
		t.Fatalf("reviewer values = %v, %v", env, err)
	}
	if args, err := codex.compact(ctx, string(worker.ID)); err != nil || !slices.Equal(args, []string{"-c", domain.LadoCodexCompactLimitKey + "=200000"}) {
		t.Fatalf("Codex compact options = %q, %v", args, err)
	}
	// Only a LADO run's session reads a plan folder.
	if dirs, err := sessions.dirs(ctx, worker); err != nil || len(dirs) != 0 {
		t.Fatalf("read folders of a session outside a run = %v, %v", dirs, err)
	}
}

// The watch loop counts the compactions of an open task's sessions on its
// own: nobody opens the Usage tab, and the count still reaches the session's
// watch row (plan step 19, decision 10).
func TestLadoWatchLoopCountsCompactionsWithoutTheUsageTab(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	useLadoStoredTokens(t, nil)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	dir := t.TempDir()
	store := sqlitetest.MustOpenAt(t, dir)
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db")+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open raw connection: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	session := seedLadoPlanWorkerSession(t, store)
	transcript := filepath.Join(t.TempDir(), "main.jsonl")
	compact := `{"type":"system","subtype":"compact_boundary","isSidechain":false,"content":"Conversation compacted","compactMetadata":{"trigger":"auto","preTokens":101234}}`
	lines := []string{`{"type":"user","message":{"role":"user","content":"hi"}}`, compact, `{"type":"assistant","message":{"role":"assistant","content":"ok"}}`, compact}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := raw.Exec(`INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at)
		VALUES (?, 'claude-code', 'root', 'active', CURRENT_TIMESTAMP)`, string(session))
	if err != nil {
		t.Fatalf("seed usage binding: %v", err)
	}
	binding, _ := res.LastInsertId()
	if _, err := raw.Exec(`INSERT INTO usage_sources (binding_id, kind, native_session_id, subagent_id, artifact_path, state, updated_at)
		VALUES (?, 'claude_main', 'native', '', ?, 'active', CURRENT_TIMESTAMP)`, binding, transcript); err != nil {
		t.Fatalf("seed usage source: %v", err)
	}
	sender := &recordingSender{}
	work := wireLadoWork(ctx, cfg, store, &recordingProjectCloner{}, ladoWorkDeps{sender: sender}, slog.New(slog.DiscardHandler))

	done := startLadoWatch(ctx, work.watch, 10*time.Millisecond, slog.New(slog.DiscardHandler))
	deadline := time.Now().Add(30 * time.Second)
	for {
		watch, ok, err := store.LadoSessionWatch(ctx, session)
		if err != nil {
			t.Fatalf("read the session watch: %v", err)
		}
		if ok && watch.Compactions == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session watch after the loop ran = %+v (row %v); want 2 compactions", watch, ok)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the watch loop did not stop with its context")
	}
	// Two more passes read nothing new and count nothing twice.
	runLadoWatchPass(context.Background(), work.watch, slog.New(slog.DiscardHandler))
	runLadoWatchPass(context.Background(), work.watch, slog.New(slog.DiscardHandler))
	if watch, _, _ := store.LadoSessionWatch(context.Background(), session); watch.Compactions != 2 {
		t.Fatalf("compactions after passes that read nothing new = %d, want 2", watch.Compactions)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("the watch sent %q to a plan worker; it watches only a run's workers", sender.sent)
	}
}

// seedLadoPlanWorkerSession makes an open task with a plan worker request and
// the session of its first designer.
func seedLadoPlanWorkerSession(t *testing.T, store *sqlite.Store) domain.SessionID {
	t.Helper()
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: at}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	task, err := store.CreateLadoTask(ctx, domain.LadoTask{
		ID: "lado-task-1", ProjectID: "mer", IssueRef: "github:acme/demo#42",
		Repo:  domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"},
		Agent: "claude-code", Model: "opus", CreatedAt: at, UpdatedAt: at,
		Issue: domain.LadoObservedIssue{Title: "Watch", State: domain.IssueOpen, URL: "https://github.com/acme/demo/issues/42", SnapshotSHA256: strings.Repeat("1", 64), ObservedAt: at},
	})
	if err != nil {
		t.Fatalf("CreateLadoTask: %v", err)
	}
	req, err := store.CreateLadoPlanRequest(ctx, domain.LadoPlanRequest{
		ID: "req-1", TaskID: task.ID, Kind: domain.LadoPlanRequestWorker,
		SessionID:           domain.LadoPlanRequestPlaceholder("req-1"),
		IssueSnapshotSHA256: strings.Repeat("1", 64), RequestedAt: at,
	})
	if err != nil {
		t.Fatalf("CreateLadoPlanRequest: %v", err)
	}
	rec, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		ClientRequestID:   domain.LadoPlanWorkerClientRequestID(req.ID, domain.LadoPlanRoleDesigner, 1),
		ClientRequestHash: "v1:x", CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return rec.ID
}

// onlySessionAccounts tells a session's account but not which login it runs
// on.
type onlySessionAccounts struct{}

func (onlySessionAccounts) SessionAccountID(context.Context, domain.SessionID, domain.AgentHarness) (string, error) {
	return "", nil
}

// sessionAndLoginAccounts also reads which login an account runs on.
type sessionAndLoginAccounts struct{ onlySessionAccounts }

func (sessionAndLoginAccounts) AccountFingerprint(context.Context, string) (string, error) {
	return "sha256:a", nil
}

// The watch reads which login a limited key runs on only through an accounts
// service that can tell it (issue #92); without one it records no login.
func TestLadoWatchIdentitiesComeFromTheAccounts(t *testing.T) {
	if got := ladoWatchIdentities(nil); got != nil {
		t.Fatalf("identities without accounts = %v, want nil", got)
	}
	if got := ladoWatchIdentities(onlySessionAccounts{}); got != nil {
		t.Fatalf("identities of accounts that cannot read a login = %v, want nil", got)
	}
	if got := ladoWatchIdentities(sessionAndLoginAccounts{}); got == nil {
		t.Fatal("identities of accounts that read a login = nil")
	}
}
