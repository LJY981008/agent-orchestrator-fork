package controllers

// LADO fork: the routes plan workers submit through (docs/LJY-PRD/TDD.md §5.1
// and §13), and what the task and plan routes show of plan workers. Like the
// rest of /api/v1/lado, they serve the loopback listener only.

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

const (
	ladoPlanSubmitPath   = "/api/v1/lado/plan-requests/{requestId}/submit"
	ladoPlanCritiquePath = "/api/v1/lado/plan-requests/{requestId}/critique"
	// maxLadoPlanSubmitBody fits a plan at its limit, escaped in JSON.
	maxLadoPlanSubmitBody = 3 << 20
)

func ladoPlanRequestID(r *http.Request) domain.LadoPlanRequestID {
	return domain.LadoPlanRequestID(chi.URLParam(r, "requestId"))
}

func (c *LadoController) submitPlan(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "POST", ladoPlanSubmitPath)
		return
	}
	var req SubmitLadoPlanRequest
	if !decodeLadoBody(w, r, maxLadoPlanSubmitBody, &req) {
		return
	}
	updated, err := c.Tasks.SubmitPlan(r.Context(), ladotask.SubmitPlanInput{
		RequestID: ladoPlanRequestID(r), SessionID: domain.SessionID(req.SessionID), Content: []byte(req.Content), Draft: req.Draft,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoPlanRequestResponse(updated))
}

func (c *LadoController) submitCritique(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "POST", ladoPlanCritiquePath)
		return
	}
	var req SubmitLadoCritiqueRequest
	if !decodeLadoBody(w, r, maxLadoPlanSubmitBody, &req) {
		return
	}
	findings := make([]domain.LadoPlanFinding, 0, len(req.Findings))
	for _, f := range req.Findings {
		findings = append(findings, domain.LadoPlanFinding{
			ID: f.ID, Severity: domain.LadoPlanFindingSeverity(f.Severity), Title: f.Title,
			FailureScenario: f.FailureScenario, Where: f.Where, Suggestion: f.Suggestion,
		})
	}
	critique, err := c.Tasks.SubmitCritique(r.Context(), ladotask.SubmitCritiqueInput{
		RequestID: ladoPlanRequestID(r), SessionID: domain.SessionID(req.SessionID), Findings: findings,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoPlanCritiqueResponse(critique, ""))
}

// ladoPlanWorkerResponse is where a plan worker request is; nil for a request
// to an orchestrator.
func ladoPlanWorkerResponse(req domain.LadoPlanRequest, critique domain.LadoPlanCritique) *LadoPlanWorkerResponse {
	if !req.IsWorker() {
		return nil
	}
	resp := &LadoPlanWorkerResponse{
		RequestID:         string(req.ID),
		Phase:             string(req.WorkerPhase()),
		FailureCode:       req.FailureCode,
		DesignerSessionID: string(req.WorkerSession()),
		StartedAt:         ladoOptionalTime(req.WorkerStartedAt),
	}
	if req.WorkerPhase() == domain.LadoPlanPhaseWaiting {
		resp.WaitingReason = req.WaitingReason
	}
	if critique.RequestID == req.ID {
		resp.CriticSessionID = string(critique.SessionID)
		resp.Review = string(critique.State)
	}
	return resp
}

// ladoPlanCritiqueResponse is a review; body is the plan whose review section
// answers its findings, "" for none.
func ladoPlanCritiqueResponse(c domain.LadoPlanCritique, body string) LadoPlanCritiqueResponse {
	section, _ := domain.LadoPlanReviewSection(body)
	findings := make([]LadoPlanFindingResponse, 0, len(c.Findings))
	for _, f := range c.Findings {
		resp := LadoPlanFindingResponse{
			ID: f.ID, Severity: string(f.Severity), Title: f.Title,
			FailureScenario: f.FailureScenario, Where: f.Where, Suggestion: f.Suggestion,
		}
		if section != "" {
			resp.Answer = string(domain.LadoPlanReviewAnswerOf(section, f.ID))
		}
		findings = append(findings, resp)
	}
	return LadoPlanCritiqueResponse{
		RequestID: string(c.RequestID), Attempt: c.Attempt, SessionID: string(c.SessionID), State: string(c.State),
		SkipReason: c.SkipReason, FailureReason: c.FailureReason, Findings: findings,
		StartedAt: c.StartedAt, FinishedAt: ladoOptionalTime(c.FinishedAt),
	}
}

// ladoPlanMetricsResponse is what making a revision took; nil when LADO did
// not record it.
func ladoPlanMetricsResponse(m *domain.LadoPlanRevisionMetrics) *LadoPlanMetricsResponse {
	if m == nil {
		return nil
	}
	sessions := make([]LadoPlanSessionUsageResponse, 0, len(m.Sessions))
	for _, s := range m.Sessions {
		sessions = append(sessions, LadoPlanSessionUsageResponse{
			SessionID: string(s.SessionID), Role: string(s.Role), InputTokens: s.InputTokens, OutputTokens: s.OutputTokens,
			CacheReadTokens: s.CacheReadTokens, CacheWriteTokens: s.CacheWriteTokens, CostNanos: s.CostNanos,
		})
	}
	kinds := m.ChangeKinds
	if kinds == nil {
		kinds = []string{}
	}
	return &LadoPlanMetricsResponse{
		DesignMs: m.Design.Milliseconds(), CritiqueMs: m.Critique.Milliseconds(), ReviseMs: m.Revise.Milliseconds(),
		Review: string(m.Review), P1: m.P1, Major: m.Major, Minor: m.Minor, Addressed: m.Addressed, NotAddressed: m.NotAddressed,
		Sessions: sessions, ChangeKinds: kinds, UpdatedAt: m.UpdatedAt,
	}
}
