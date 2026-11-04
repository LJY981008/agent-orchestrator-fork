package controllers

// LADO fork: the orchestrator's project memo routes (issue #50). The memo is
// the handoff note an orchestrator writes before /clear; a person reads, edits
// and restores it here.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/projectmemo"
)

// ProjectMemoService reads and writes a project's orchestrator memo. Errors are
// apierr values: PROJECT_NOT_FOUND, PROJECT_MEMO_NOT_FOUND,
// PROJECT_MEMO_TOO_LONG, PROJECT_MEMO_INVALID and PROJECT_MEMO_CONFLICT.
type ProjectMemoService interface {
	// Current returns the memo in force (ok false when there is none yet) and
	// the kept earlier revisions, newest first.
	Current(ctx context.Context, project domain.ProjectID) (memo projectmemo.Memo, ok bool, revisions []projectmemo.Memo, err error)
	// Save makes body the current memo as a person's edit. baseRevision is the
	// revision the editor read; "" means it read no memo.
	Save(ctx context.Context, project domain.ProjectID, body, baseRevision string) (projectmemo.Memo, error)
	// Revision returns one revision, current or kept.
	Revision(ctx context.Context, project domain.ProjectID, revision string) (projectmemo.Memo, error)
}

// ProjectMemoController owns /projects/{id}/orchestrator-memo. A nil Svc keeps
// the routes registered and answers 501.
type ProjectMemoController struct {
	Svc ProjectMemoService
	// Now is the clock for the stale flag; nil uses time.Now.
	Now func() time.Time
}

// projectMemoBodyLimit bounds a PUT body. A full memo is 4,000 runes, at most
// 16 KiB of UTF-8 before JSON escaping.
const projectMemoBodyLimit = 256 << 10

// Register mounts the project memo routes.
func (c *ProjectMemoController) Register(r chi.Router) {
	r.Get("/projects/{id}/orchestrator-memo", c.get)
	r.Put("/projects/{id}/orchestrator-memo", c.save)
	r.Get("/projects/{id}/orchestrator-memo/revisions/{revisionId}", c.revision)
}

func (c *ProjectMemoController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{id}/orchestrator-memo")
		return
	}
	memo, ok, revisions, err := c.Svc.Current(r.Context(), projectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, c.response(memo, ok, revisions))
}

func (c *ProjectMemoController) save(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/projects/{id}/orchestrator-memo")
		return
	}
	var in SaveProjectMemoRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, projectMemoBodyLimit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROJECT_MEMO_TOO_LONG",
				"The memo is too long", nil)
			return
		}
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	project := projectID(r)
	if _, err := c.Svc.Save(r.Context(), project, in.Body, in.BaseRevision); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	// Answer with the whole view, so the editor sees the new revision and the
	// history entry the old memo became.
	memo, ok, revisions, err := c.Svc.Current(r.Context(), project)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, c.response(memo, ok, revisions))
}

func (c *ProjectMemoController) revision(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{id}/orchestrator-memo/revisions/{revisionId}")
		return
	}
	memo, err := c.Svc.Revision(r.Context(), projectID(r), chi.URLParam(r, "revisionId"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ProjectMemoRevisionResponse{Memo: c.payload(memo)})
}

func (c *ProjectMemoController) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *ProjectMemoController) response(memo projectmemo.Memo, ok bool, revisions []projectmemo.Memo) ProjectMemoResponse {
	out := ProjectMemoResponse{
		Revisions:      make([]ProjectMemoRevisionSummary, 0, len(revisions)),
		MaxChars:       projectmemo.MaxRunes,
		StaleAfterDays: int(projectmemo.StaleAfter / (24 * time.Hour)),
	}
	if ok {
		p := c.payload(memo)
		out.Memo = &p
	}
	for _, rev := range revisions {
		out.Revisions = append(out.Revisions, ProjectMemoRevisionSummary{
			Revision:  rev.Revision,
			Source:    string(rev.Source),
			UpdatedAt: rev.UpdatedAt,
			Chars:     rev.Runes(),
		})
	}
	return out
}

func (c *ProjectMemoController) payload(m projectmemo.Memo) ProjectMemoPayload {
	return ProjectMemoPayload{
		Revision:  m.Revision,
		Source:    string(m.Source),
		UpdatedAt: m.UpdatedAt,
		Body:      m.Body,
		Chars:     m.Runes(),
		Stale:     m.Stale(c.now()),
	}
}

// ProjectMemoRevisionIDParam is the {revisionId} path parameter of a memo
// revision route.
type ProjectMemoRevisionIDParam struct {
	RevisionID string `path:"revisionId" description:"Memo revision id, as listed in the memo's revisions."`
}

// ProjectMemoPayload is one memo revision with its body.
type ProjectMemoPayload struct {
	Revision string `json:"revision"`
	// Source says who wrote it: the orchestrator before /clear, or a person.
	Source    string    `json:"source" enum:"clear,user"`
	UpdatedAt time.Time `json:"updatedAt"`
	Body      string    `json:"body"`
	// Chars is the body length in characters, the unit maxChars counts.
	Chars int `json:"chars"`
	// Stale is true when the memo is older than staleAfterDays.
	Stale bool `json:"stale"`
}

// ProjectMemoRevisionSummary is a kept earlier revision without its body.
type ProjectMemoRevisionSummary struct {
	Revision  string    `json:"revision"`
	Source    string    `json:"source" enum:"clear,user"`
	UpdatedAt time.Time `json:"updatedAt"`
	Chars     int       `json:"chars"`
}

// ProjectMemoResponse is a project's orchestrator memo view.
type ProjectMemoResponse struct {
	// Memo is the memo in force, or null when the project has none yet.
	Memo *ProjectMemoPayload `json:"memo"`
	// Revisions are the kept earlier revisions, newest first.
	Revisions      []ProjectMemoRevisionSummary `json:"revisions"`
	MaxChars       int                          `json:"maxChars"`
	StaleAfterDays int                          `json:"staleAfterDays"`
}

// ProjectMemoRevisionResponse is one memo revision.
type ProjectMemoRevisionResponse struct {
	Memo ProjectMemoPayload `json:"memo"`
}

// SaveProjectMemoRequest replaces a project's memo. To restore an earlier
// revision, read it and save its body.
type SaveProjectMemoRequest struct {
	Body string `json:"body" description:"New memo text, at most maxChars characters. Known secret patterns are masked before saving."`
	// BaseRevision is the revision the editor read, or "" when there was no
	// memo. A different current revision answers 409 PROJECT_MEMO_CONFLICT.
	BaseRevision string `json:"baseRevision"`
}
