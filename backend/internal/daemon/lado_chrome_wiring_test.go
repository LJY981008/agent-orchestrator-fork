package daemon

import (
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type chromeHookSessions struct {
	chrome sessionmanager.LadoClaudeChrome
}

func (s *chromeHookSessions) SetLadoClaudeChrome(chrome sessionmanager.LadoClaudeChrome) {
	s.chrome = chrome
}

func TestWireLadoClaudeChromeInstallsTheLaunchSource(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir()}
	store := sqlitetest.MustOpenAt(t, t.TempDir())
	sessions := &chromeHookSessions{}
	chrome := wireLadoClaudeChrome(cfg, store, sessions, slog.New(slog.DiscardHandler))
	if sessions.chrome == nil || sessions.chrome != chrome {
		t.Fatalf("session manager hook = %v, want the Chrome service", sessions.chrome)
	}
	rec := domain.SessionRecord{ID: "s-1", Mode: domain.SessionModeChat}
	if value, ok := sessions.chrome.LaunchEnv(rec, domain.HarnessClaudeCode); value != "1" || !ok {
		t.Fatalf("default launch value = %q, %v; want 1", value, ok)
	}

	// A session double without the hook still wires.
	if wireLadoClaudeChrome(cfg, store, struct{}{}, slog.New(slog.DiscardHandler)) == nil {
		t.Fatal("wireLadoClaudeChrome returned nil without the hook")
	}
}
