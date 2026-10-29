package controllers

// LADO fork: how long a task's stages took and what its sessions spent on
// investigation, per task and as a project's baseline (issue #34).

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// LadoTimingsService reads tasks' timings. *ladotask.Service implements it;
// the daemon asserts that at compile time.
type LadoTimingsService interface {
	TaskTimings(ctx context.Context, id domain.LadoTaskID) (domain.LadoTaskTimings, error)
	ProjectTimings(ctx context.Context, projectID domain.ProjectID, limit int) (domain.LadoTimingsSummary, error)
}

const (
	ladoTaskTimingsPath = "/api/v1/lado/tasks/{taskId}/timings"
	ladoTimingsPath     = "/api/v1/lado/timings"
	// defaultLadoTimingsLimit and maxLadoTimingsLimit bound how many tasks a
	// project's baseline sums up.
	defaultLadoTimingsLimit = 20
	maxLadoTimingsLimit     = 100
)

func (c *LadoController) timingsService() (LadoTimingsService, bool) {
	if c.Tasks == nil {
		return nil, false
	}
	timings, ok := c.Tasks.(LadoTimingsService)
	return timings, ok
}

func (c *LadoController) getTaskTimings(w http.ResponseWriter, r *http.Request) {
	timings, ok := c.timingsService()
	if !ok {
		apispec.NotImplemented(w, r, "GET", ladoTaskTimingsPath)
		return
	}
	task, err := timings.TaskTimings(r.Context(), ladoTaskID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoTaskTimingsResponse(task))
}

func (c *LadoController) getTimings(w http.ResponseWriter, r *http.Request) {
	timings, ok := c.timingsService()
	if !ok {
		apispec.NotImplemented(w, r, "GET", ladoTimingsPath)
		return
	}
	query := r.URL.Query()
	projectID := query.Get("projectId")
	if projectID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "projectId is required", nil)
		return
	}
	limit := defaultLadoTimingsLimit
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > maxLadoTimingsLimit {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "limit must be a whole number from 1 to 100", nil)
			return
		}
		limit = value
	}
	summary, err := timings.ProjectTimings(r.Context(), domain.ProjectID(projectID), limit)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoTimingsSummaryResponse(summary))
}

func ladoTimingsSummaryResponse(summary domain.LadoTimingsSummary) LadoTimingsSummaryResponse {
	resp := LadoTimingsSummaryResponse{
		ProjectID:     string(summary.ProjectID),
		Tasks:         make([]LadoTaskTimingsResponse, 0, len(summary.Tasks)),
		Gaps:          make([]LadoTimingGapResponse, 0, len(summary.Gaps)),
		Investigation: make([]LadoInvestigationSummaryResponse, 0, len(summary.Investigation)),
	}
	for _, task := range summary.Tasks {
		resp.Tasks = append(resp.Tasks, ladoTaskTimingsResponse(task))
	}
	for _, gap := range summary.Gaps {
		resp.Gaps = append(resp.Gaps, LadoTimingGapResponse{
			From: string(gap.From), To: string(gap.To), Count: gap.Count,
			MedianMs: gap.Median.Milliseconds(), MaxMs: gap.Max.Milliseconds(),
		})
	}
	for _, role := range summary.Investigation {
		resp.Investigation = append(resp.Investigation, LadoInvestigationSummaryResponse{
			Role: string(role.Role), Sessions: role.Sessions, MedianCalls: role.MedianCalls,
			MedianInvestigationCalls: role.MedianInvestigationCalls, MedianInvestigateMs: role.MedianInvestigate.Milliseconds(),
		})
	}
	return resp
}

func ladoTaskTimingsResponse(task domain.LadoTaskTimings) LadoTaskTimingsResponse {
	timeline := task.Timeline
	resp := LadoTaskTimingsResponse{
		TaskID: string(task.TaskID), IssueRef: string(task.IssueRef), Title: task.Title,
		Timeline: LadoTaskTimelineResponse{
			Points:  make([]LadoTimingPointResponse, 0, len(timeline.Points)),
			TotalMs: timeline.Total.Milliseconds(), PlanRequests: timeline.PlanRequests,
			Revisions: timeline.Revisions, Approvals: timeline.Approvals, Runs: timeline.Runs,
		},
		Sessions: make([]LadoSessionMetricsResponse, 0, len(task.Sessions)),
	}
	for _, p := range timeline.Points {
		resp.Timeline.Points = append(resp.Timeline.Points, LadoTimingPointResponse{
			Stage: string(p.Stage), At: p.At.UTC(), SincePrevMs: p.SincePrev.Milliseconds(), Estimated: p.Estimated,
		})
	}
	for _, s := range task.Sessions {
		resp.Sessions = append(resp.Sessions, LadoSessionMetricsResponse{
			SessionID: string(s.SessionID), Role: string(s.Role),
			ToolCalls: s.ToolCalls, ReadCalls: s.ReadCalls, SearchCalls: s.SearchCalls,
			CommandCalls: s.CommandCalls, EditCalls: s.EditCalls, InvestigationCalls: s.InvestigationCalls(),
			FirstActivityAt: ladoOptionalUTC(s.FirstActivityAt), FirstEditAt: ladoOptionalUTC(s.FirstEditAt),
			LastActivityAt: ladoOptionalUTC(s.LastActivityAt), InvestigateMs: s.Investigate.Milliseconds(),
			Stored: s.Stored, UpdatedAt: ladoOptionalUTC(s.UpdatedAt),
		})
	}
	return resp
}

func ladoOptionalUTC(at time.Time) *time.Time {
	return ladoOptionalTime(at.UTC())
}
