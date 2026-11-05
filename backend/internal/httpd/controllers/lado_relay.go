package controllers

// LADO fork: the routes an orchestrator relays a developer's instruction to a
// running worker through, and the task window reads the relays from (issue
// #49, docs/LJY-PRD/adr/0011-worker-relay.md). Like the rest of /api/v1/lado,
// they serve the loopback listener only. The daemon's ladorelay service does
// every check; the worker gate still refuses an orchestrator's own ao send.

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladorelay"
)

// LadoRelayService relays developer instructions to workers.
// *ladorelay.Service implements it.
type LadoRelayService interface {
	Relay(ctx context.Context, req ladorelay.Request) (domain.LadoRelay, error)
	List(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoRelay, error)
}

const (
	ladoRelaysPath     = "/api/v1/lado/relays"
	ladoTaskRelaysPath = "/api/v1/lado/tasks/{taskId}/relays"
	// maxLadoRelayBody fits an instruction and evidence at their limits,
	// every character escaped in JSON.
	maxLadoRelayBody = 128 << 10
)

func (c *LadoController) registerRelay(r chi.Router) {
	r.Post("/lado/relays", c.createRelay)
	r.Get("/lado/tasks/{taskId}/relays", c.listTaskRelays)
}

func (c *LadoController) createRelay(w http.ResponseWriter, r *http.Request) {
	if c.Relay == nil {
		apispec.NotImplemented(w, r, "POST", ladoRelaysPath)
		return
	}
	var req CreateLadoRelayRequest
	if !decodeLadoBody(w, r, maxLadoRelayBody, &req) {
		return
	}
	relay, err := c.Relay.Relay(r.Context(), ladorelay.Request{
		SessionID:       domain.SessionID(req.SessionID),
		Task:            req.Task,
		WorkerSessionID: domain.SessionID(req.WorkerSessionID),
		Message:         req.Message,
		Evidence:        req.Evidence,
		OutsidePlan:     req.OutsidePlan,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ladoRelayResponse(relay))
}

func (c *LadoController) listTaskRelays(w http.ResponseWriter, r *http.Request) {
	if c.Relay == nil {
		apispec.NotImplemented(w, r, "GET", ladoTaskRelaysPath)
		return
	}
	relays, err := c.Relay.List(r.Context(), ladoTaskID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoRelayListResponse{Relays: make([]LadoRelayResponse, 0, len(relays))}
	for _, relay := range relays {
		resp.Relays = append(resp.Relays, ladoRelayResponse(relay))
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func ladoRelayResponse(r domain.LadoRelay) LadoRelayResponse {
	reasons := r.OutsidePlanReasons
	if reasons == nil {
		reasons = []string{}
	}
	return LadoRelayResponse{
		ID:                    string(r.ID),
		TaskID:                string(r.TaskID),
		ProjectID:             string(r.ProjectID),
		OrchestratorSessionID: string(r.OrchestratorSessionID),
		WorkerSessionID:       string(r.WorkerSessionID),
		WorkerRole:            r.WorkerRole,
		PartID:                r.PartID,
		Instruction:           r.Instruction,
		Evidence:              r.Evidence,
		EvidenceMessageID:     r.EvidenceMessageID,
		EvidenceAt:            r.EvidenceAt.UTC(),
		BeyondEvidence:        r.BeyondEvidence,
		OutsidePlan:           r.OutsidePlan,
		OutsidePlanReasons:    reasons,
		State:                 string(r.State),
		Failure:               r.Failure,
		CreatedAt:             r.CreatedAt.UTC(),
		DeliveredAt:           ladoOptionalUTC(r.DeliveredAt),
	}
}
