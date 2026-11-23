package controllers

// LADO fork: the model and effort each LADO stage's workers run with (issue
// #103): the global defaults and each project's override. The service checks
// a value against its harness's model catalog before it keeps it and reports
// a refused one under a LADO_STAGE_* code.

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladostage"
)

// LadoStageAgentService reads and replaces the stage settings.
// *ladostage.Service implements it.
type LadoStageAgentService interface {
	Global(ctx context.Context) (domain.LadoStageAgents, error)
	SetGlobal(ctx context.Context, agents domain.LadoStageAgents) (domain.LadoStageAgents, error)
	Project(ctx context.Context, projectID domain.ProjectID) (ladostage.ProjectView, error)
	SetProject(ctx context.Context, projectID domain.ProjectID, agents domain.LadoStageAgents) (ladostage.ProjectView, error)
}

// LadoSessionRecordReader reads an AO session's record, whose metadata holds
// the model and effort the session resolved to at spawn. The session service
// implements it.
type LadoSessionRecordReader interface {
	GetRecord(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error)
}

const (
	ladoStageAgentsPath        = "/api/v1/lado/stage-agents"
	ladoProjectStageAgentsPath = "/api/v1/lado/projects/{projectId}/stage-agents"
)

func (c *LadoController) registerStageAgents(r chi.Router) {
	r.Get("/lado/stage-agents", c.getStageAgents)
	r.Put("/lado/stage-agents", c.putStageAgents)
	r.Get("/lado/projects/{projectId}/stage-agents", c.getProjectStageAgents)
	r.Put("/lado/projects/{projectId}/stage-agents", c.putProjectStageAgents)
}

func (c *LadoController) getStageAgents(w http.ResponseWriter, r *http.Request) {
	if c.StageAgents == nil {
		apispec.NotImplemented(w, r, "GET", ladoStageAgentsPath)
		return
	}
	agents, err := c.StageAgents.Global(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LadoStageAgentsResponse{Stages: ladoStageAgentsByStage(agents)})
}

func (c *LadoController) putStageAgents(w http.ResponseWriter, r *http.Request) {
	if c.StageAgents == nil {
		apispec.NotImplemented(w, r, "PUT", ladoStageAgentsPath)
		return
	}
	agents, ok := decodeLadoStageAgents(w, r)
	if !ok {
		return
	}
	kept, err := c.StageAgents.SetGlobal(r.Context(), agents)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LadoStageAgentsResponse{Stages: ladoStageAgentsByStage(kept)})
}

func (c *LadoController) getProjectStageAgents(w http.ResponseWriter, r *http.Request) {
	if c.StageAgents == nil {
		apispec.NotImplemented(w, r, "GET", ladoProjectStageAgentsPath)
		return
	}
	view, err := c.StageAgents.Project(r.Context(), ladoProjectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoProjectStageAgentsResponse(view))
}

func (c *LadoController) putProjectStageAgents(w http.ResponseWriter, r *http.Request) {
	if c.StageAgents == nil {
		apispec.NotImplemented(w, r, "PUT", ladoProjectStageAgentsPath)
		return
	}
	agents, ok := decodeLadoStageAgents(w, r)
	if !ok {
		return
	}
	view, err := c.StageAgents.SetProject(r.Context(), ladoProjectID(r), agents)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoProjectStageAgentsResponse(view))
}

// decodeLadoStageAgents reads an UpdateLadoStageAgentsRequest, which must
// name stages.
func decodeLadoStageAgents(w http.ResponseWriter, r *http.Request) (domain.LadoStageAgents, bool) {
	var req UpdateLadoStageAgentsRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return nil, false
	}
	if req.Stages == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			ladostage.CodeInvalid, "Send stages; {} clears the scope.", nil)
		return nil, false
	}
	return req.Stages.domainAgents(), true
}

// sessionAgent is the model and effort the session resolved to at spawn, as
// AO's metadata holds them; empty without a reader or for a session AO no
// longer has.
func (c *LadoController) sessionAgent(ctx context.Context, id domain.SessionID) (model, effort string) {
	if c.Sessions == nil || id == "" {
		return "", ""
	}
	rec, err := c.Sessions.GetRecord(ctx, id)
	if err != nil {
		return "", ""
	}
	return rec.Metadata.Model, rec.Metadata.Effort
}

// addSessionAgents fills the model and effort the task's execution sessions
// run with.
func (c *LadoController) addSessionAgents(ctx context.Context, resp *LadoTaskResponse) {
	for _, exec := range []*LadoTaskExecutionResponse{resp.Execution, resp.PreviousExecution} {
		if exec != nil {
			exec.SessionModel, exec.SessionEffort = c.sessionAgent(ctx, domain.SessionID(exec.SessionID))
		}
	}
}
