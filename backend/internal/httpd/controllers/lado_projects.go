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
	Projects    LadoProjectService
	Connections LadoConnectionService
	Tasks       LadoTaskService
	Rules       LadoRuleService
	Queue       LadoQueueService
	Setup       LadoSetupService
	Verify      LadoVerifyService
	// Presence checks the human-presence proofs approval routes need. Nil
	// refuses every such route.
	Presence LadoPresence
}

const (
	ladoProjectSettingsPath = "/api/v1/lado/projects/{projectId}/settings"
	// maxLadoSettingsBody fits ten check commands of 2000 characters each,
	// escaped.
	maxLadoSettingsBody = 128 << 10
)

// Register mounts the LADO routes.
func (c *LadoController) Register(r chi.Router) {
	r.Get("/lado/projects/{projectId}/settings", c.getProjectSettings)
	r.Put("/lado/projects/{projectId}/settings", c.putProjectSettings)
	r.Get("/lado/projects/{projectId}/issues", c.listProjectIssues)
	r.Get("/lado/projects/{projectId}/repositories", c.listProjectRepositories)
	r.Post("/lado/connections/check", c.checkConnection)
	r.Post("/lado/connections", c.createConnection)
	r.Get("/lado/credentials", c.listCredentials)
	r.Put("/lado/credentials/{host}", c.storeCredential)
	r.Delete("/lado/credentials/{host}", c.deleteCredential)
	r.Get("/lado/tasks", c.listTasks)
	r.Post("/lado/tasks", c.createTask)
	r.Post("/lado/tasks/imports", c.importTasks)
	r.Get("/lado/tasks/{taskId}", c.getTask)
	r.Patch("/lado/tasks/{taskId}", c.updateTask)
	r.Post("/lado/tasks/{taskId}/cancel", c.cancelTask)
	r.Post("/lado/tasks/{taskId}/plan-requests", c.requestPlan)
	r.Get("/lado/tasks/{taskId}/plans", c.listPlans)
	r.Get("/lado/plans/{revisionId}", c.getPlan)
	r.Post("/lado/plans/{revisionId}/approve", c.approvePlan)
	r.Get("/lado/rules", c.listRules)
	r.Post("/lado/rules", c.createRule)
	r.Get("/lado/rules/{ruleId}", c.getRule)
	r.Patch("/lado/rules/{ruleId}", c.updateRule)
	r.Delete("/lado/rules/{ruleId}", c.deleteRule)
	r.Put("/lado/bindings", c.setBinding)
	r.Get("/lado/effective", c.getEffective)
	r.Get("/lado/queue", c.listQueue)
	r.Get("/lado/executions/{sessionId}", c.getExecution)
	r.Get("/lado/dispatch-settings", c.getDispatchSettings)
	r.Put("/lado/dispatch-settings", c.putDispatchSettings)
	c.registerSetup(r)
	c.registerVerify(r)
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
	// Turning LADO mode off and changing the check commands need a presence
	// proof over these exact bytes.
	body, ok := readLadoPresenceBody(w, r, maxLadoSettingsBody)
	if !ok {
		return
	}
	var req UpdateLadoProjectSettingsRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	in := ladoproject.UpdateInput{
		LadoMode:            req.LadoMode,
		MaxConcurrent:       req.MaxConcurrent,
		CheckTimeoutMinutes: req.CheckTimeoutMinutes,
		CheckCommandsBase:   req.CheckCommandsBase,
		Presence:            c.presenceCheck(r, body),
	}
	if req.CheckCommands != nil {
		commands := make([]domain.LadoCheckCommand, 0, len(*req.CheckCommands))
		for _, command := range *req.CheckCommands {
			commands = append(commands, domain.LadoCheckCommand{Name: command.Name, Command: command.Command})
		}
		in.CheckCommands = &commands
	}
	settings, err := c.Projects.UpdateSettings(r.Context(), ladoProjectID(r), in)
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
	commands := make([]LadoCheckCommandBody, 0, len(settings.CheckCommands))
	for _, command := range settings.CheckCommands {
		commands = append(commands, LadoCheckCommandBody{Name: command.Name, Command: command.Command})
	}
	return LadoProjectSettingsResponse{
		ProjectID:           string(settings.ProjectID),
		LadoMode:            settings.LadoMode,
		MaxConcurrent:       settings.MaxConcurrent,
		CheckCommands:       commands,
		CheckCommandsSHA256: domain.LadoCheckCommandsSHA256(settings.CheckCommands),
		CheckTimeoutMinutes: settings.CheckTimeoutMinutes,
	}
}
