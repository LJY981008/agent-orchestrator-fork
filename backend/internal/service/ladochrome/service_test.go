package ladochrome_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladochrome"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// chromeStore keeps proofs and audit events in a real store and sessions in
// a map, so a test needs no project rows.
type chromeStore struct {
	*sqlite.Store
	mu       sync.Mutex
	sessions map[domain.SessionID]domain.SessionRecord
	events   []domain.LadoAuditEvent
}

func (s *chromeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	return rec, ok, nil
}

func (s *chromeStore) RecordLadoClaudeChromeChange(ctx context.Context, proof *domain.LadoPresenceProof, event domain.LadoAuditEvent) error {
	if err := s.Store.RecordLadoClaudeChromeChange(ctx, proof, event); err != nil {
		return err
	}
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	return nil
}

func (s *chromeStore) auditCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func (s *chromeStore) put(rec domain.SessionRecord) {
	s.mu.Lock()
	s.sessions[rec.ID] = rec
	s.mu.Unlock()
}

var chromeNow = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func newChromeService(t *testing.T) (*ladochrome.Service, *chromeStore, string) {
	t.Helper()
	dataDir := t.TempDir()
	st := &chromeStore{
		Store:    sqlitetest.MustOpenAt(t, t.TempDir()),
		sessions: map[domain.SessionID]domain.SessionRecord{},
	}
	svc := ladochrome.New(ladochrome.Options{
		DataDir: dataDir, Store: st, Now: func() time.Time { return chromeNow },
	})
	return svc, st, dataDir
}

func proofFor(nonce string) ladochrome.Presence {
	return func(action, subject string) (domain.LadoPresenceProof, error) {
		return domain.LadoPresenceProof{
			Nonce: nonce, Action: action, SubjectID: subject,
			ExpiresAt: chromeNow.Add(time.Minute), UsedAt: chromeNow,
		}, nil
	}
}

func refusePresence(called *int) ladochrome.Presence {
	return func(string, string) (domain.LadoPresenceProof, error) {
		*called++
		return domain.LadoPresenceProof{}, apierr.Forbidden("LADO_PRESENCE_REQUIRED", "confirm in the app")
	}
}

func mustSettings(t *testing.T, svc *ladochrome.Service) domain.LadoClaudeChromeSettings {
	t.Helper()
	got, err := svc.Settings(context.Background())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	return got
}

func TestSettingsDefaultWithoutAFile(t *testing.T) {
	svc, _, _ := newChromeService(t)
	if got := mustSettings(t, svc); got != (domain.LadoClaudeChromeSettings{ChatSessions: true, LadoWorkers: false}) {
		t.Fatalf("default settings = %+v", got)
	}
}

func TestSetSettingsPersistsPrivatelyAndReloads(t *testing.T) {
	svc, st, dataDir := newChromeService(t)
	ctx := context.Background()
	next := domain.LadoClaudeChromeSettings{ChatSessions: false, LadoWorkers: false}
	if _, err := svc.SetSettings(ctx, next, nil); err != nil {
		t.Fatalf("turn off: %v", err)
	}
	path := filepath.Join(dataDir, "lado", "claude-chrome.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat settings file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings file mode = %v, want 0600", info.Mode().Perm())
	}
	if dir, err := os.Stat(filepath.Dir(path)); err != nil || dir.Mode().Perm() != 0o700 {
		t.Fatalf("settings folder = %v, %v; want 0700", dir, err)
	}
	reloaded := ladochrome.New(ladochrome.Options{DataDir: dataDir, Store: st})
	if got := mustSettings(t, reloaded); got != next {
		t.Fatalf("reloaded settings = %+v, want %+v", got, next)
	}
}

func TestSettingsDefaultWhenTheFileIsDamaged(t *testing.T) {
	svc, _, dataDir := newChromeService(t)
	path := filepath.Join(dataDir, "lado", "claude-chrome.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"{not json", `{"chatSessions":false}`} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := mustSettings(t, svc); got != domain.DefaultLadoClaudeChromeSettings() {
			t.Fatalf("damaged %q settings = %+v, want defaults", body, got)
		}
	}
}

func TestTurningOnFailsWithoutPresenceAndWritesNothing(t *testing.T) {
	svc, st, dataDir := newChromeService(t)
	ctx := context.Background()
	called := 0
	_, err := svc.SetSettings(ctx, domain.LadoClaudeChromeSettings{ChatSessions: true, LadoWorkers: true}, refusePresence(&called))
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "LADO_PRESENCE_REQUIRED" || called != 1 {
		t.Fatalf("turn on without presence = %v (called %d)", err, called)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "lado", "claude-chrome.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("settings file after refused change: %v", err)
	}
	if st.auditCount() != 0 {
		t.Fatalf("audit events = %d, want 0", st.auditCount())
	}
	if _, err := svc.SetSettings(ctx, domain.LadoClaudeChromeSettings{ChatSessions: true, LadoWorkers: true}, nil); err == nil {
		t.Fatal("turn on with no presence check succeeded")
	}
}

func TestTurningOffNeedsNoPresenceAndIsAudited(t *testing.T) {
	svc, st, _ := newChromeService(t)
	called := 0
	got, err := svc.SetSettings(context.Background(), domain.LadoClaudeChromeSettings{}, refusePresence(&called))
	if err != nil || got != (domain.LadoClaudeChromeSettings{}) {
		t.Fatalf("turn off = %+v, %v", got, err)
	}
	if called != 0 {
		t.Fatalf("turning off asked for presence %d times", called)
	}
	if st.auditCount() != 1 || st.events[0].Action != ladochrome.AuditAction || st.events[0].Actor != "api" {
		t.Fatalf("audit events = %+v", st.events)
	}
}

func TestTurningOnStoresProofAndRefusesAReplay(t *testing.T) {
	svc, st, _ := newChromeService(t)
	ctx := context.Background()
	off := domain.LadoClaudeChromeSettings{}
	if _, err := svc.SetSettings(ctx, off, nil); err != nil {
		t.Fatalf("turn off: %v", err)
	}
	var subject string
	presence := func(action, s string) (domain.LadoPresenceProof, error) {
		subject = s
		return proofFor("chrome-nonce-0001")(action, s)
	}
	on := domain.LadoClaudeChromeSettings{ChatSessions: true, LadoWorkers: true}
	if got, err := svc.SetSettings(ctx, on, presence); err != nil || got != on {
		t.Fatalf("turn on = %+v, %v", got, err)
	}
	if subject != "chatSessions,ladoWorkers" {
		t.Fatalf("presence subject = %q", subject)
	}
	if used, err := st.LadoPresenceProofUsed(ctx, "chrome-nonce-0001"); err != nil || !used {
		t.Fatalf("proof stored = %v, %v", used, err)
	}
	if st.auditCount() != 2 || st.events[1].Actor != "human" {
		t.Fatalf("audit events = %+v", st.events)
	}

	if _, err := svc.SetSettings(ctx, off, nil); err != nil {
		t.Fatalf("turn off again: %v", err)
	}
	_, err := svc.SetSettings(ctx, on, proofFor("chrome-nonce-0001"))
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "LADO_PRESENCE_REPLAYED" {
		t.Fatalf("replayed proof = %v, want LADO_PRESENCE_REPLAYED", err)
	}
	if got := mustSettings(t, svc); got != off {
		t.Fatalf("settings after replay = %+v, want %+v", got, off)
	}
	if st.auditCount() != 3 {
		t.Fatalf("audit events after replay = %d, want 3", st.auditCount())
	}
}

func chatSession(id domain.SessionID, clientRequestID string, harness domain.AgentHarness) domain.SessionRecord {
	return domain.SessionRecord{
		ID: id, Harness: harness, Mode: domain.SessionModeChat, ClientRequestID: clientRequestID,
	}
}

func TestLaunchEnvFollowsSessionKindAndHarness(t *testing.T) {
	svc, _, _ := newChromeService(t)
	ctx := context.Background()
	human := chatSession("human-1", "", domain.HarnessClaudeCode)
	worker := chatSession("worker-1", "lado:entry-1/1", domain.HarnessClaudeCode)
	tui := human
	tui.ID, tui.Mode = "tui-1", domain.SessionModeTUI

	cases := []struct {
		name    string
		rec     domain.SessionRecord
		harness domain.AgentHarness
		value   string
		ok      bool
	}{
		{"human chat", human, domain.HarnessClaudeCode, "1", true},
		{"lado worker default off", worker, domain.HarnessClaudeCode, "0", true},
		{"terminal session", tui, domain.HarnessClaudeCode, "", false},
		{"codex", human, domain.HarnessCodex, "", false},
		// The stored harness still names the old agent during a switch.
		{"switching from codex", chatSession("switch-1", "", domain.HarnessCodex), domain.HarnessClaudeCode, "1", true},
	}
	for _, tc := range cases {
		if value, ok := svc.LaunchEnv(tc.rec, tc.harness); value != tc.value || ok != tc.ok {
			t.Errorf("%s: LaunchEnv = %q, %v; want %q, %v", tc.name, value, ok, tc.value, tc.ok)
		}
	}

	if _, err := svc.SetSettings(ctx, domain.LadoClaudeChromeSettings{ChatSessions: true, LadoWorkers: true}, proofFor("chrome-nonce-0002")); err != nil {
		t.Fatalf("turn workers on: %v", err)
	}
	if value, ok := svc.LaunchEnv(worker, domain.HarnessClaudeCode); value != "1" || !ok {
		t.Fatalf("worker with LADO workers on = %q, %v", value, ok)
	}
	if _, err := svc.SetSettings(ctx, domain.LadoClaudeChromeSettings{ChatSessions: false, LadoWorkers: true}, nil); err != nil {
		t.Fatalf("turn chat off: %v", err)
	}
	if value, ok := svc.LaunchEnv(human, domain.HarnessClaudeCode); value != "0" || !ok {
		t.Fatalf("human chat with Chat sessions off = %q, %v", value, ok)
	}
}

func TestSessionChromeReportsLaunchValueThenSetting(t *testing.T) {
	svc, st, _ := newChromeService(t)
	ctx := context.Background()
	human := chatSession("human-1", "", domain.HarnessClaudeCode)
	st.put(human)

	// No start recorded yet: the next start gets the setting.
	got, err := svc.SessionChrome(ctx, human.ID)
	if err != nil {
		t.Fatalf("SessionChrome: %v", err)
	}
	if !got.Supported || !got.Enabled || got.Basis != domain.LadoChromeBasisSetting ||
		got.Kind != domain.LadoChromeKindChat || !got.SettingOn {
		t.Fatalf("before launch = %+v", got)
	}

	svc.LaunchEnv(human, domain.HarnessClaudeCode)
	if _, err := svc.SetSettings(ctx, domain.LadoClaudeChromeSettings{}, nil); err != nil {
		t.Fatalf("turn off: %v", err)
	}
	got, _ = svc.SessionChrome(ctx, human.ID)
	if !got.Enabled || got.Basis != domain.LadoChromeBasisLaunch || got.SettingOn {
		t.Fatalf("running session after setting change = %+v, want launch value on, setting off", got)
	}

	hibernatedAt := chromeNow
	hibernated := human
	hibernated.HibernatedAt = &hibernatedAt
	st.put(hibernated)
	got, _ = svc.SessionChrome(ctx, human.ID)
	if got.Enabled || got.Basis != domain.LadoChromeBasisSetting {
		t.Fatalf("hibernated session = %+v, want the current setting", got)
	}

	terminated := human
	terminated.IsTerminated = true
	st.put(terminated)
	if got, _ = svc.SessionChrome(ctx, human.ID); got.Basis != domain.LadoChromeBasisSetting {
		t.Fatalf("terminated session = %+v, want the current setting", got)
	}

	worker := chatSession("worker-1", "lado:plan/req-1/author/1", domain.HarnessClaudeCode)
	st.put(worker)
	if got, _ = svc.SessionChrome(ctx, worker.ID); got.Kind != domain.LadoChromeKindLadoWorker || got.SettingOn {
		t.Fatalf("LADO worker = %+v", got)
	}

	codex := chatSession("codex-1", "", domain.HarnessCodex)
	st.put(codex)
	if got, _ = svc.SessionChrome(ctx, codex.ID); got.Supported {
		t.Fatalf("codex session = %+v, want unsupported", got)
	}

	_, err = svc.SessionChrome(ctx, "missing")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindNotFound {
		t.Fatalf("missing session = %v, want not found", err)
	}
}
