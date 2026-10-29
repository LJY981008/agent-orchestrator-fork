package controllers

// LADO fork: the parts run of a task (docs/LJY-PRD/TDD.md §5.4).

import (
	"context"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoqueue"
)

// LadoPartsService reads a task's parts run. *ladoqueue.Service implements it.
type LadoPartsService interface {
	TaskParts(ctx context.Context, id domain.LadoTaskID) (ladoqueue.PartsView, error)
}

const ladoTaskPartsPath = "/api/v1/lado/tasks/{taskId}/parts"

func (c *LadoController) getTaskParts(w http.ResponseWriter, r *http.Request) {
	parts, ok := c.Queue.(LadoPartsService)
	if c.Queue == nil || !ok {
		apispec.NotImplemented(w, r, "GET", ladoTaskPartsPath)
		return
	}
	view, err := parts.TaskParts(r.Context(), ladoTaskID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoTaskPartsResponse(view))
}

func ladoTaskPartsResponse(view ladoqueue.PartsView) LadoTaskPartsResponse {
	resp := LadoTaskPartsResponse{Parts: make([]LadoTaskPartResponse, 0, len(view.Parts))}
	if view.Run.Branch != "" {
		resp.Branch, resp.BaseSHA, resp.HeadSHA = view.Run.Branch, view.Run.BaseSHA, view.Run.HeadSHA
		resp.StartedAt = ladoOptionalTime(view.Run.StartedAt)
	}
	for _, part := range view.Parts {
		resp.Parts = append(resp.Parts, LadoTaskPartResponse{
			ID: part.PartID, Title: part.Title, Phase: part.Phase, Steps: orEmptyInts(part.Steps),
			AffectedPaths: orEmptyStrings(part.AffectedPaths), DependsOn: orEmptyStrings(part.DependsOn),
			State: string(part.State), WaitingReason: ladoPartWaiting(part), WaitingDetail: ladoPartWaitingDetail(part),
			Attempts: part.Attempts, SessionID: string(part.SessionID), Branch: part.Branch, StartSHA: part.StartSHA,
			MergedSHA: part.MergedSHA, MergeSessionID: string(part.MergeSessionID), MergeAttempts: part.MergeAttempts,
			Summary: part.Summary, Error: part.Error, UpdatedAt: part.UpdatedAt,
		})
	}
	return resp
}

// ladoPartWaiting is the reason a part waits, only while it waits to start.
func ladoPartWaiting(part domain.LadoTaskPart) string {
	if part.State == domain.LadoPartWaiting || (part.State == domain.LadoPartConflict && part.MergeSessionID == "") {
		return part.WaitingReason
	}
	return ""
}

func ladoPartWaitingDetail(part domain.LadoTaskPart) string {
	if ladoPartWaiting(part) == "" {
		return ""
	}
	return part.WaitingDetail
}
