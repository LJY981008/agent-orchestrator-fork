package controllers

// LADO fork: the worker watch settings and alerts (issue #42). The settings
// have a global row and a row per project; a null follows the row below it.

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoalert"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladolaunch"
)

// LadoWatchService serves the watch settings and the alerts
// (*ladoalert.Service).
type LadoWatchService interface {
	GlobalSettings(ctx context.Context) (ladoalert.SettingsView, error)
	UpdateGlobalSettings(ctx context.Context, values domain.LadoWatchOverrides) (ladoalert.SettingsView, error)
	ProjectSettings(ctx context.Context, project domain.ProjectID) (ladoalert.SettingsView, error)
	UpdateProjectSettings(ctx context.Context, project domain.ProjectID, values domain.LadoWatchOverrides) (ladoalert.SettingsView, error)
	ProjectAlerts(ctx context.Context, q ladoalert.AlertQuery) ([]domain.LadoAlert, error)
	MarkSeen(ctx context.Context, id domain.LadoAlertID) (domain.LadoAlert, error)
}

// LadoCompactService tells a project's compact window and the settings that
// turn auto-compact off (*ladolaunch.Service).
type LadoCompactService interface {
	CompactInfo(ctx context.Context, project domain.ProjectID) (ladolaunch.CompactInfo, error)
}

var (
	_ LadoWatchService   = (*ladoalert.Service)(nil)
	_ LadoCompactService = (*ladolaunch.Service)(nil)
)

const (
	ladoWatchSettingsPath        = "/api/v1/lado/watch-settings"
	ladoProjectWatchSettingsPath = "/api/v1/lado/projects/{projectId}/watch-settings"
	ladoProjectAlertsPath        = "/api/v1/lado/projects/{projectId}/alerts"
	ladoAlertSeenPath            = "/api/v1/lado/alerts/{alertId}/seen"
)

func (c *LadoController) registerWatch(r chi.Router) {
	r.Get("/lado/watch-settings", c.getWatchSettings)
	r.Put("/lado/watch-settings", c.putWatchSettings)
	r.Get("/lado/projects/{projectId}/watch-settings", c.getProjectWatchSettings)
	r.Put("/lado/projects/{projectId}/watch-settings", c.putProjectWatchSettings)
	r.Get("/lado/projects/{projectId}/alerts", c.listProjectAlerts)
	r.Post("/lado/alerts/{alertId}/seen", c.markAlertSeen)
}

func (c *LadoController) getWatchSettings(w http.ResponseWriter, r *http.Request) {
	if c.Watch == nil {
		apispec.NotImplemented(w, r, "GET", ladoWatchSettingsPath)
		return
	}
	view, err := c.Watch.GlobalSettings(r.Context())
	writeLadoWatchSettings(w, r, view, err)
}

func (c *LadoController) putWatchSettings(w http.ResponseWriter, r *http.Request) {
	if c.Watch == nil {
		apispec.NotImplemented(w, r, "PUT", ladoWatchSettingsPath)
		return
	}
	var req UpdateLadoWatchSettingsRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return
	}
	view, err := c.Watch.UpdateGlobalSettings(r.Context(), ladoWatchOverrides(req))
	writeLadoWatchSettings(w, r, view, err)
}

func (c *LadoController) getProjectWatchSettings(w http.ResponseWriter, r *http.Request) {
	if c.Watch == nil {
		apispec.NotImplemented(w, r, "GET", ladoProjectWatchSettingsPath)
		return
	}
	view, err := c.Watch.ProjectSettings(r.Context(), ladoProjectID(r))
	writeLadoWatchSettings(w, r, view, err)
}

func (c *LadoController) putProjectWatchSettings(w http.ResponseWriter, r *http.Request) {
	if c.Watch == nil {
		apispec.NotImplemented(w, r, "PUT", ladoProjectWatchSettingsPath)
		return
	}
	var req UpdateLadoWatchSettingsRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return
	}
	view, err := c.Watch.UpdateProjectSettings(r.Context(), ladoProjectID(r), ladoWatchOverrides(req))
	writeLadoWatchSettings(w, r, view, err)
}

func (c *LadoController) listProjectAlerts(w http.ResponseWriter, r *http.Request) {
	if c.Watch == nil {
		apispec.NotImplemented(w, r, "GET", ladoProjectAlertsPath)
		return
	}
	query := r.URL.Query()
	q := ladoalert.AlertQuery{Project: ladoProjectID(r), Task: domain.LadoTaskID(query.Get("taskId"))}
	if raw := query.Get("unseen"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "unseen must be true or false", nil)
			return
		}
		q.UnseenOnly = value
	}
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > ladoalert.MaxAlertLimit {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "limit must be 1 to 500", nil)
			return
		}
		q.Limit = value
	}
	alerts, err := c.Watch.ProjectAlerts(r.Context(), q)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoAlertListResponse{Alerts: make([]LadoAlertResponse, 0, len(alerts))}
	for _, a := range alerts {
		resp.Alerts = append(resp.Alerts, ladoAlertResponse(a))
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) markAlertSeen(w http.ResponseWriter, r *http.Request) {
	if c.Watch == nil {
		apispec.NotImplemented(w, r, "POST", ladoAlertSeenPath)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "alertId"), 10, 64)
	if err != nil || id < 1 {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "LADO_ALERT_NOT_FOUND", "Unknown alert", nil)
		return
	}
	alert, err := c.Watch.MarkSeen(r.Context(), domain.LadoAlertID(id))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoAlertResponse(alert))
}

func writeLadoWatchSettings(w http.ResponseWriter, r *http.Request, view ladoalert.SettingsView, err error) {
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LadoWatchSettingsResponse{
		ProjectID: string(view.Scope),
		Values: LadoWatchSettingsValues{
			CompactWindow:      view.Values.CompactWindow,
			TaskCostCents:      view.Values.TaskCostCents,
			SessionCostCents:   view.Values.SessionCostCents,
			SessionCompactions: view.Values.SessionCompactions,
			StallMinutes:       view.Values.StallMinutes,
			WakeEnabled:        view.Values.WakeEnabled,
		},
		Inherited: ladoWatchResolved(view.Inherited),
		Effective: ladoWatchResolved(view.Effective),
		Defaults:  ladoWatchResolved(domain.LadoWatchDefaults()),
		UpdatedAt: ladoOptionalUTC(view.Values.UpdatedAt),
	})
}

func ladoWatchOverrides(req UpdateLadoWatchSettingsRequest) domain.LadoWatchOverrides {
	return domain.LadoWatchOverrides{
		CompactWindow:      req.CompactWindow,
		TaskCostCents:      req.TaskCostCents,
		SessionCostCents:   req.SessionCostCents,
		SessionCompactions: req.SessionCompactions,
		StallMinutes:       req.StallMinutes,
		WakeEnabled:        req.WakeEnabled,
	}
}

func ladoWatchResolved(s domain.LadoWatchSettings) LadoWatchSettingsResolved {
	return LadoWatchSettingsResolved{
		CompactWindow:      s.CompactWindow,
		TaskCostCents:      s.TaskCostCents,
		SessionCostCents:   s.SessionCostCents,
		SessionCompactions: s.SessionCompactions,
		StallMinutes:       s.StallMinutes,
		WakeEnabled:        s.WakeEnabled,
	}
}

func ladoAlertResponse(a domain.LadoAlert) LadoAlertResponse {
	return LadoAlertResponse{
		ID:             int64(a.ID),
		TaskID:         string(a.TaskID),
		SessionID:      string(a.SessionID),
		ProjectID:      string(a.ProjectID),
		IssueRef:       string(a.IssueRef),
		Kind:           string(a.Kind),
		Level:          a.Level,
		Value:          a.Value,
		Threshold:      a.Threshold,
		Line:           ladoalert.Line(a),
		NotificationID: a.NotificationID(),
		CreatedAt:      a.CreatedAt.UTC(),
		SeenAt:         ladoOptionalUTC(a.SeenAt),
	}
}

// ladoCompactWarnings lists the settings that turn auto-compact off, never
// nil.
func ladoCompactWarnings(warnings []ladolaunch.CompactWarning) []LadoCompactWarningResponse {
	out := make([]LadoCompactWarningResponse, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, LadoCompactWarningResponse{Source: w.Source, Setting: w.Setting, Path: w.Path})
	}
	return out
}
