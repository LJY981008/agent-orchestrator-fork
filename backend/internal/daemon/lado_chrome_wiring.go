package daemon

// LADO fork: Claude in Chrome (docs/LJY-PRD/TDD.md). The setting lives in
// <DataDir>/lado/claude-chrome.json; the session manager asks it for the
// CLAUDE_CODE_ENABLE_CFC value of every Claude Code controller it starts.

import (
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladochrome"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// ladoChromeSessions is the session manager's Claude in Chrome hook. Test
// doubles of the session manager need not have it.
type ladoChromeSessions interface {
	SetLadoClaudeChrome(sessionmanager.LadoClaudeChrome)
}

// wireLadoClaudeChrome builds the Claude in Chrome service and, when sessions
// is the session manager, installs it as the launch value source before
// anything spawns or restores.
func wireLadoClaudeChrome(cfg config.Config, store *sqlite.Store, sessions any, log *slog.Logger) *ladochrome.Service {
	chrome := ladochrome.New(ladochrome.Options{DataDir: cfg.DataDir, Store: store, Log: log})
	if hook, ok := sessions.(ladoChromeSessions); ok {
		hook.SetLadoClaudeChrome(chrome)
	}
	return chrome
}
