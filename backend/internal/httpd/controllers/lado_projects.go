package controllers

// LADO fork: LADO's project settings routes (docs/LJY-PRD/TDD.md §13).

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoproject"
)

// LadoProjectService is the controller-facing LADO project settings contract.
type LadoProjectService interface {
	Settings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error)
	UpdateSettings(ctx context.Context, projectID domain.ProjectID, in ladoproject.UpdateInput) (domain.LadoProjectSettings, error)
}

// LadoController owns the /api/v1/lado routes. A nil service answers 501, so
// the routes stay mounted for the route/spec parity test.
type LadoController struct {
	Projects LadoProjectService
}

const (
	ladoProjectSettingsPath  = "/api/v1/lado/projects/{projectId}/settings"
	maxLadoSettingsBodyBytes = 4 << 10
)

// Register mounts the LADO routes.
func (c *LadoController) Register(r chi.Router) {
	r.Get("/lado/projects/{projectId}/settings", c.getProjectSettings)
	r.Put("/lado/projects/{projectId}/settings", c.putProjectSettings)
}

func (c *LadoController) getProjectSettings(w http.ResponseWriter, r *http.Request) {
	if c.Projects == nil {
		apispec.NotImplemented(w, r, "GET", ladoProjectSettingsPath)
		return
	}
	settings, err := c.Projects.Settings(r.Context(), ladoProjectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoProjectSettingsResponse(settings))
}

func (c *LadoController) putProjectSettings(w http.ResponseWriter, r *http.Request) {
	if c.Projects == nil {
		apispec.NotImplemented(w, r, "PUT", ladoProjectSettingsPath)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLadoSettingsBodyBytes)
	var req UpdateLadoProjectSettingsRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	settings, err := c.Projects.UpdateSettings(r.Context(), ladoProjectID(r), ladoproject.UpdateInput{LadoMode: req.LadoMode})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoProjectSettingsResponse(settings))
}

func ladoProjectID(r *http.Request) domain.ProjectID {
	return domain.ProjectID(chi.URLParam(r, "projectId"))
}

func ladoProjectSettingsResponse(settings domain.LadoProjectSettings) LadoProjectSettingsResponse {
	return LadoProjectSettingsResponse{ProjectID: string(settings.ProjectID), LadoMode: settings.LadoMode}
}
