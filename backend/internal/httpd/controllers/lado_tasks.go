package controllers

// LADO fork: issue-backed task routes (docs/LJY-PRD/TDD.md §13).

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

// LadoTaskService is the controller-facing task contract.
type LadoTaskService interface {
	List(ctx context.Context, projectID domain.ProjectID, includeCancelled bool) ([]domain.LadoTask, error)
	Create(ctx context.Context, in ladotask.CreateInput) (domain.LadoTask, error)
	Get(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, error)
	UpdatePreferences(ctx context.Context, id domain.LadoTaskID, agent, model *string) (domain.LadoTask, error)
	Cancel(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, error)
	Import(ctx context.Context, in ladotask.ImportInput) (ladotask.ImportResult, error)
	ImportableIssues(ctx context.Context, projectID domain.ProjectID) ([]ladotask.ImportableIssue, []ladotask.RepoFailure, error)
	Repositories(ctx context.Context, projectID domain.ProjectID) ([]ladotask.ProjectRepo, error)
	View(ctx context.Context, id domain.LadoTaskID) (ladotask.TaskView, error)
	RequestPlan(ctx context.Context, in ladotask.PlanRequestInput) (domain.LadoPlanRequest, error)
	Plans(ctx context.Context, id domain.LadoTaskID) (ladotask.PlanHistory, error)
	Plan(ctx context.Context, revisionID domain.LadoPlanRevisionID) (ladotask.PlanView, error)
	ApprovePlan(ctx context.Context, revisionID domain.LadoPlanRevisionID, sha256, policySHA256 string, proof domain.LadoPresenceProof) (domain.LadoPlanApproval, error)
}

const (
	ladoTasksPath         = "/api/v1/lado/tasks"
	ladoTaskImportsPath   = "/api/v1/lado/tasks/imports"
	ladoTaskPath          = "/api/v1/lado/tasks/{taskId}"
	ladoTaskCancelPath    = "/api/v1/lado/tasks/{taskId}/cancel"
	ladoProjectIssuesPath = "/api/v1/lado/projects/{projectId}/issues"
	ladoProjectReposPath  = "/api/v1/lado/projects/{projectId}/repositories"
	// maxLadoTaskBodyBytes fits the largest issue body LADO accepts.
	maxLadoTaskBodyBytes = 512 << 10
	maxLadoSmallBody     = 64 << 10
)

func (c *LadoController) listTasks(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "GET", ladoTasksPath)
		return
	}
	query := r.URL.Query()
	projectID := query.Get("projectId")
	if projectID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "projectId is required", nil)
		return
	}
	includeCancelled := false
	if raw := query.Get("includeCancelled"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "includeCancelled must be true or false", nil)
			return
		}
		includeCancelled = value
	}
	tasks, err := c.Tasks.List(r.Context(), domain.ProjectID(projectID), includeCancelled)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LadoTaskListResponse{Tasks: ladoTaskResponses(tasks)})
}

func (c *LadoController) createTask(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "POST", ladoTasksPath)
		return
	}
	var req CreateLadoTaskRequest
	if !decodeLadoBody(w, r, maxLadoTaskBodyBytes, &req) {
		return
	}
	task, err := c.Tasks.Create(r.Context(), ladotask.CreateInput{
		ProjectID:  domain.ProjectID(req.ProjectID),
		Repository: req.Repository,
		Text:       req.Text,
		Agent:      req.Agent,
		Model:      req.Model,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ladoTaskResponse(task))
}

func (c *LadoController) importTasks(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "POST", ladoTaskImportsPath)
		return
	}
	var req ImportLadoTasksRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return
	}
	refs := make([]domain.IssueID, 0, len(req.IssueRefs))
	for _, ref := range req.IssueRefs {
		refs = append(refs, domain.IssueID(ref))
	}
	result, err := c.Tasks.Import(r.Context(), ladotask.ImportInput{ProjectID: domain.ProjectID(req.ProjectID), IssueRefs: refs})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := ImportLadoTasksResponse{Tasks: ladoTaskResponses(result.Tasks), Skipped: make([]string, 0, len(result.Skipped))}
	for _, ref := range result.Skipped {
		resp.Skipped = append(resp.Skipped, string(ref))
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) getTask(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "GET", ladoTaskPath)
		return
	}
	view, err := c.getTaskView(r.Context(), ladoTaskID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, view)
}

func (c *LadoController) updateTask(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "PATCH", ladoTaskPath)
		return
	}
	var req UpdateLadoTaskRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return
	}
	task, err := c.Tasks.UpdatePreferences(r.Context(), ladoTaskID(r), req.Agent, req.Model)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoTaskResponse(task))
}

func (c *LadoController) cancelTask(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "POST", ladoTaskCancelPath)
		return
	}
	task, err := c.Tasks.Cancel(r.Context(), ladoTaskID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoTaskResponse(task))
}

func (c *LadoController) listProjectIssues(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "GET", ladoProjectIssuesPath)
		return
	}
	issues, failures, err := c.Tasks.ImportableIssues(r.Context(), ladoProjectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoProjectIssuesResponse{
		Issues:   make([]LadoIssueResponse, 0, len(issues)),
		Failures: make([]LadoRepositoryFailureResponse, 0, len(failures)),
	}
	for _, item := range issues {
		labels := item.Issue.Labels
		if labels == nil {
			labels = []string{}
		}
		resp.Issues = append(resp.Issues, LadoIssueResponse{
			Repository: ladoRepositoryResponse(item.Repo),
			Ref:        string(domain.LadoIssueRef(item.Issue.ID)),
			Number:     domain.LadoIssueNumber(item.Issue.ID),
			Title:      item.Issue.Title,
			URL:        item.Issue.URL,
			Labels:     labels,
		})
	}
	for _, failure := range failures {
		resp.Failures = append(resp.Failures, LadoRepositoryFailureResponse{Repository: ladoRepositoryResponse(failure.Repo), Problem: failure.Problem})
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) listProjectRepositories(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "GET", ladoProjectReposPath)
		return
	}
	repos, err := c.Tasks.Repositories(r.Context(), ladoProjectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoProjectRepositoriesResponse{Repositories: make([]LadoRepositoryResponse, 0, len(repos))}
	for _, repo := range repos {
		resp.Repositories = append(resp.Repositories, ladoRepositoryResponse(repo))
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func ladoRepositoryResponse(repo ladotask.ProjectRepo) LadoRepositoryResponse {
	host := repo.Repo.Host
	if host == "" {
		host = "github.com"
		if repo.Repo.Provider == domain.TrackerProviderGitLab {
			host = "gitlab.com"
		}
	}
	return LadoRepositoryResponse{
		Ref:      domain.LadoRepoRef(repo.Repo),
		Name:     repo.Name,
		Provider: string(repo.Repo.Provider),
		Host:     host,
		Path:     repo.Repo.Native,
	}
}

func ladoTaskID(r *http.Request) domain.LadoTaskID {
	return domain.LadoTaskID(chi.URLParam(r, "taskId"))
}

func ladoTaskResponses(tasks []domain.LadoTask) []LadoTaskResponse {
	out := make([]LadoTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, ladoTaskResponse(task))
	}
	return out
}

func ladoTaskResponse(task domain.LadoTask) LadoTaskResponse {
	resp := LadoTaskResponse{
		ID:          string(task.ID),
		ProjectID:   string(task.ProjectID),
		Repository:  domain.LadoRepoRef(task.Repo),
		State:       string(task.State()),
		StateReason: task.StateReason(),
		Issue: LadoTaskIssueResponse{
			Ref:        string(task.IssueRef),
			Title:      task.Issue.Title,
			State:      string(task.Issue.State),
			URL:        task.Issue.URL,
			ObservedAt: task.Issue.ObservedAt,
		},
		Agent:     task.Agent,
		Model:     task.Model,
		Plan:      ladoTaskPlanResponse(task.Plan),
		Queue:     ladoTaskQueueResponse(task.Queue, time.Now()),
		Execution: ladoTaskExecutionResponse(task),
		CreatedAt: task.CreatedAt,
	}
	if id, err := domain.ParseLadoIssueRef(task.IssueRef); err == nil {
		resp.Issue.Number = domain.LadoIssueNumber(id)
	}
	resp.CancelledAt = ladoOptionalTime(task.CancelledAt)
	return resp
}

func ladoOptionalTime(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	return &at
}
