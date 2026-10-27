package ports

// LADO fork: the config home that carries the setup LADO manages into a
// Claude Code session (docs/LJY-PRD/HARNESS.md §9).

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LadoSetupHome builds the config homes of Claude Code sessions. A nil home
// keeps upstream behavior.
type LadoSetupHome interface {
	// PrepareClaudeHome builds or reconciles a session's config home and
	// returns the launch variables that point Claude Code at it. The caller
	// sets them over the launch environment; an empty map changes nothing.
	PrepareClaudeHome(ctx context.Context, in LadoSetupHomeInput) (map[string]string, error)
	// OwnsHome reports whether configDir is a config home LADO built.
	OwnsHome(configDir string) bool
}

// LadoSetupHomeInput is a Claude Code session about to launch.
type LadoSetupHomeInput struct {
	SessionID domain.SessionID
	ProjectID domain.ProjectID
	// ClientRequestID is the session's client request id, which names the
	// LADO execution it runs, if any.
	ClientRequestID string
	// Env is the launch environment so far. It is read, never changed.
	Env map[string]string
}
