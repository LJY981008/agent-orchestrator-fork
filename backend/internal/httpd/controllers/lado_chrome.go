package controllers

// LADO fork: Claude in Chrome for Claude Code Chat sessions (issue #40).

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// LadoClaudeChromeService is the Claude in Chrome setting and the sessions'
// state. *ladochrome.Service implements it.
type LadoClaudeChromeService interface {
	Settings(ctx context.Context) (domain.LadoClaudeChromeSettings, error)
	SetSettings(ctx context.Context, next domain.LadoClaudeChromeSettings, presence func(action, subject string) (domain.LadoPresenceProof, error)) (domain.LadoClaudeChromeSettings, error)
	SessionChrome(ctx context.Context, id domain.SessionID) (domain.LadoSessionChrome, error)
}

const (
	ladoClaudeChromePath        = "/api/v1/lado/claude-chrome"
	ladoSessionClaudeChromePath = "/api/v1/lado/sessions/{sessionId}/claude-chrome"
)

func (c *LadoController) getClaudeChrome(w http.ResponseWriter, r *http.Request) {
	if c.Chrome == nil {
		apispec.NotImplemented(w, r, "GET", ladoClaudeChromePath)
		return
	}
	settings, err := c.Chrome.Settings(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoClaudeChromeSettingsResponse(settings))
}

func (c *LadoController) putClaudeChrome(w http.ResponseWriter, r *http.Request) {
	if c.Chrome == nil {
		apispec.NotImplemented(w, r, "PUT", ladoClaudeChromePath)
		return
	}
	// Turning anything on needs a presence proof over these exact bytes.
	body, ok := readLadoPresenceBody(w, r, maxLadoSmallBody)
	if !ok {
		return
	}
	var req UpdateLadoClaudeChromeRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	if req.ChatSessions == nil || req.LadoWorkers == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"LADO_CLAUDE_CHROME_INVALID", "Send chatSessions and ladoWorkers.", nil)
		return
	}
	next := domain.LadoClaudeChromeSettings{ChatSessions: *req.ChatSessions, LadoWorkers: *req.LadoWorkers}
	settings, err := c.Chrome.SetSettings(r.Context(), next, c.presenceCheck(r, body))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoClaudeChromeSettingsResponse(settings))
}

func (c *LadoController) getSessionClaudeChrome(w http.ResponseWriter, r *http.Request) {
	if c.Chrome == nil {
		apispec.NotImplemented(w, r, "GET", ladoSessionClaudeChromePath)
		return
	}
	id := domain.SessionID(chi.URLParam(r, "sessionId"))
	state, err := c.Chrome.SessionChrome(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoSessionClaudeChromeResponse(id, state))
}
