package controllers

// LADO fork: project knowledge routes (issue #24,
// docs/LJY-PRD/adr/0010-project-knowledge-store.md).

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoknowledge"
)

// LadoKnowledgeService is a project's knowledge store.
// *ladoknowledge.Service implements it.
type LadoKnowledgeService interface {
	Index(ctx context.Context, projectID domain.ProjectID) (ladoknowledge.Index, error)
	Importable(ctx context.Context, projectID domain.ProjectID) ([]ladoknowledge.Importable, error)
	Claim(ctx context.Context, projectID domain.ProjectID) error
	Write(ctx context.Context, in ladoknowledge.WriteInput) (domain.LadoKnowledgeEntry, error)
	ImportClaudeMemory(ctx context.Context, projectID domain.ProjectID, path string) (ladoknowledge.ImportResult, error)
	ImportProject(ctx context.Context, projectID, fromID domain.ProjectID) (ladoknowledge.ImportResult, error)
}

const (
	ladoKnowledgePath        = "/api/v1/lado/projects/{projectId}/knowledge"
	ladoKnowledgeImportsPath = "/api/v1/lado/projects/{projectId}/knowledge/imports"
	ladoKnowledgeClaimPath   = "/api/v1/lado/projects/{projectId}/knowledge/claim"
	ladoKnowledgeNotePath    = "/api/v1/lado/knowledge/notes/{slug}"
	// maxLadoKnowledgeNoteBody fits a note of domain.LadoKnowledgeNoteMaxBytes
	// (64 KiB) even when JSON escapes every byte as \u00XX.
	maxLadoKnowledgeNoteBody = 512 << 10
	// maxLadoKnowledgeImportBody fits a path and a project id.
	maxLadoKnowledgeImportBody = 16 << 10
)

func (c *LadoController) registerKnowledge(r chi.Router) {
	r.Get("/lado/projects/{projectId}/knowledge", c.getKnowledge)
	r.Post("/lado/projects/{projectId}/knowledge/imports", c.importKnowledge)
	r.Post("/lado/projects/{projectId}/knowledge/claim", c.claimKnowledge)
	r.Put("/lado/knowledge/notes/{slug}", c.putKnowledgeNote)
}

func (c *LadoController) getKnowledge(w http.ResponseWriter, r *http.Request) {
	if c.Knowledge == nil {
		apispec.NotImplemented(w, r, "GET", ladoKnowledgePath)
		return
	}
	resp, err := c.knowledgeView(r.Context(), ladoProjectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) claimKnowledge(w http.ResponseWriter, r *http.Request) {
	if c.Knowledge == nil {
		apispec.NotImplemented(w, r, "POST", ladoKnowledgeClaimPath)
		return
	}
	id := ladoProjectID(r)
	if err := c.Knowledge.Claim(r.Context(), id); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp, err := c.knowledgeView(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) knowledgeView(ctx context.Context, id domain.ProjectID) (LadoKnowledgeResponse, error) {
	idx, err := c.Knowledge.Index(ctx, id)
	if err != nil {
		return LadoKnowledgeResponse{}, err
	}
	importable, err := c.Knowledge.Importable(ctx, id)
	if err != nil {
		return LadoKnowledgeResponse{}, err
	}
	return ladoKnowledgeResponse(idx, importable), nil
}

func (c *LadoController) importKnowledge(w http.ResponseWriter, r *http.Request) {
	if c.Knowledge == nil {
		apispec.NotImplemented(w, r, "POST", ladoKnowledgeImportsPath)
		return
	}
	var req ImportLadoKnowledgeRequest
	if !decodeLadoBody(w, r, maxLadoKnowledgeImportBody, &req) {
		return
	}
	id := ladoProjectID(r)
	var (
		result ladoknowledge.ImportResult
		err    error
	)
	switch req.Source {
	case ladoknowledge.SourceClaudeMemory:
		if req.FromProjectID != "" {
			writeLadoKnowledgeImportInvalid(w, r, "fromProjectId", "fromProjectId is only for source project.")
			return
		}
		result, err = c.Knowledge.ImportClaudeMemory(r.Context(), id, req.Path)
	case ladoknowledge.SourceProject:
		if req.Path != "" {
			writeLadoKnowledgeImportInvalid(w, r, "path", "path is only for source claude_memory.")
			return
		}
		if strings.TrimSpace(req.FromProjectID) == "" {
			writeLadoKnowledgeImportInvalid(w, r, "fromProjectId", "Send fromProjectId, the knowledge folder to import from.")
			return
		}
		result, err = c.Knowledge.ImportProject(r.Context(), id, domain.ProjectID(req.FromProjectID))
	default:
		writeLadoKnowledgeImportInvalid(w, r, "source", "source is claude_memory or project.")
		return
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoKnowledgeImportResponse(result))
}

func writeLadoKnowledgeImportInvalid(w http.ResponseWriter, r *http.Request, field, message string) {
	envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
		ladoknowledge.CodeInvalidImport, message, map[string]any{"field": field})
}

func (c *LadoController) putKnowledgeNote(w http.ResponseWriter, r *http.Request) {
	if c.Knowledge == nil {
		apispec.NotImplemented(w, r, "PUT", ladoKnowledgeNotePath)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLadoKnowledgeNoteBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			// The note is far past its own cap: say so, as for one just over it.
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
				ladoknowledge.CodeInvalidNote, "A note is at most 64 KiB.", map[string]any{"field": "content"})
			return
		}
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Unreadable request body", nil)
		return
	}
	var req WriteLadoKnowledgeNoteRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			ladoknowledge.CodeInvalidNote, "Send sessionId, the session that writes the note.", map[string]any{"field": "sessionId"})
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			ladoknowledge.CodeInvalidNote, "Send content, the whole note with its front matter.", map[string]any{"field": "content"})
		return
	}
	entry, err := c.Knowledge.Write(r.Context(), ladoknowledge.WriteInput{
		SessionID:    domain.SessionID(req.SessionID),
		Slug:         chi.URLParam(r, "slug"),
		BaseRevision: req.BaseRevision,
		Content:      []byte(req.Content),
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoKnowledgeNoteResponse(entry))
}
