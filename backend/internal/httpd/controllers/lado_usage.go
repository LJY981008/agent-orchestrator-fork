package controllers

// LADO fork: what a task's sessions spent, per task and as a project's board
// totals (issue #42). The usage view reads; only the watch loop stores the
// compaction counts it shows.

import (
	"context"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladousage"
)

// LadoUsageService adds up tasks' usage (*ladousage.Service).
type LadoUsageService interface {
	TaskUsage(ctx context.Context, id domain.LadoTaskID) (ladousage.TaskUsage, error)
	ProjectUsage(ctx context.Context, projectID domain.ProjectID) (ladousage.ProjectUsage, error)
}

var _ LadoUsageService = (*ladousage.Service)(nil)

const (
	ladoTaskUsagePath    = "/api/v1/lado/tasks/{taskId}/usage"
	ladoProjectUsagePath = "/api/v1/lado/projects/{projectId}/usage"
)

func (c *LadoController) getTaskUsage(w http.ResponseWriter, r *http.Request) {
	if c.Usage == nil {
		apispec.NotImplemented(w, r, "GET", ladoTaskUsagePath)
		return
	}
	usage, err := c.Usage.TaskUsage(r.Context(), ladoTaskID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoTaskUsageResponse{
		Task:     ladoTaskUsageSummaryResponse(usage),
		Sessions: make([]LadoSessionUsageResponse, 0, len(usage.Sessions)),
	}
	for _, s := range usage.Sessions {
		resp.Sessions = append(resp.Sessions, LadoSessionUsageResponse{
			SessionID: string(s.SessionID), Role: string(s.Role), PartID: s.PartID, Attempt: s.Attempt,
			Totals:      ladoUsageTotalsResponse(s.Totals),
			ContextUsed: s.ContextUsed, ContextWindow: s.ContextWindow, Compactions: s.Compactions,
			UsageMissing: s.UsageMissing, Incomplete: s.Incomplete,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) getProjectUsage(w http.ResponseWriter, r *http.Request) {
	if c.Usage == nil {
		apispec.NotImplemented(w, r, "GET", ladoProjectUsagePath)
		return
	}
	usage, err := c.Usage.ProjectUsage(r.Context(), ladoProjectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoProjectUsageResponse{
		ProjectID: string(usage.ProjectID),
		Tasks:     make([]LadoTaskUsageSummaryResponse, 0, len(usage.Tasks)),
	}
	for _, task := range usage.Tasks {
		resp.Tasks = append(resp.Tasks, ladoTaskUsageSummaryResponse(task))
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func ladoTaskUsageSummaryResponse(u ladousage.TaskUsage) LadoTaskUsageSummaryResponse {
	reason := string(u.EndReason)
	if u.InProgress() {
		reason = "in_progress"
	}
	return LadoTaskUsageSummaryResponse{
		TaskID: string(u.TaskID), IssueRef: string(u.IssueRef), Title: u.Title,
		Totals: ladoUsageTotalsResponse(u.Totals), Compactions: u.Compactions,
		StartedAt: ladoOptionalUTC(u.StartedAt), EndedAt: u.EndedAt.UTC(),
		ElapsedMs: u.Elapsed.Milliseconds(), EndReason: reason, InProgress: u.InProgress(),
		SessionsMissing: u.UsageMissing, Incomplete: u.Incomplete,
	}
}

func ladoUsageTotalsResponse(t ladousage.Totals) LadoUsageTotalsResponse {
	return LadoUsageTotalsResponse{
		CostNanos: t.CostNanos, InputTokens: t.InputTokens, CachedInputTokens: t.CachedInputTokens,
		OutputTokens: t.OutputTokens, SubagentCostNanos: t.SubagentCostNanos,
		SubagentInputTokens: t.SubagentInputTokens, SubagentOutputTokens: t.SubagentOutputTokens,
	}
}
