package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

var ladoTaskTime = time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)

func sampleLadoTask() domain.LadoTask {
	return domain.LadoTask{
		ID:        "lado-task-1",
		ProjectID: "mer",
		Repo:      domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"},
		IssueRef:  "github:acme/demo#12",
		Agent:     "claude-code",
		Issue: domain.LadoObservedIssue{
			Title: "Add login", State: domain.IssueOpen, URL: "https://github.com/acme/demo/issues/12",
			SnapshotSHA256: "abc", ObservedAt: ladoTaskTime,
		},
		CreatedAt: ladoTaskTime,
		UpdatedAt: ladoTaskTime,
	}
}

type fakeLadoTaskService struct {
	task            domain.LadoTask
	issues          []ladotask.ImportableIssue
	failures        []ladotask.RepoFailure
	repos           []ladotask.ProjectRepo
	err             error
	gotProject      domain.ProjectID
	gotCancelledToo bool
	gotCreate       ladotask.CreateInput
	gotImport       ladotask.ImportInput
	gotID           domain.LadoTaskID
	gotAgent        *string
	gotModel        *string
	calls           int
	// Plan routes.
	view        ladotask.TaskView
	planView    ladotask.PlanView
	history     ladotask.PlanHistory
	request     domain.LadoPlanRequest
	approval    domain.LadoPlanApproval
	gotPlanReq  ladotask.PlanRequestInput
	gotRevision domain.LadoPlanRevisionID
	gotSHA      string
	gotProof    domain.LadoPresenceProof
}

func (f *fakeLadoTaskService) View(_ context.Context, id domain.LadoTaskID) (ladotask.TaskView, error) {
	f.calls++
	f.gotID = id
	view := f.view
	if view.Task.ID == "" {
		view.Task = f.task
	}
	return view, f.err
}

func (f *fakeLadoTaskService) RequestPlan(_ context.Context, in ladotask.PlanRequestInput) (domain.LadoPlanRequest, error) {
	f.calls++
	f.gotPlanReq = in
	return f.request, f.err
}

func (f *fakeLadoTaskService) Plans(_ context.Context, id domain.LadoTaskID) (ladotask.PlanHistory, error) {
	f.calls++
	f.gotID = id
	return f.history, f.err
}

func (f *fakeLadoTaskService) Plan(_ context.Context, id domain.LadoPlanRevisionID) (ladotask.PlanView, error) {
	f.calls++
	f.gotRevision = id
	return f.planView, f.err
}

func (f *fakeLadoTaskService) ApprovePlan(_ context.Context, id domain.LadoPlanRevisionID, sha256 string, proof domain.LadoPresenceProof) (domain.LadoPlanApproval, error) {
	f.calls++
	f.gotRevision, f.gotSHA, f.gotProof = id, sha256, proof
	return f.approval, f.err
}

func (f *fakeLadoTaskService) List(_ context.Context, projectID domain.ProjectID, includeCancelled bool) ([]domain.LadoTask, error) {
	f.calls++
	f.gotProject, f.gotCancelledToo = projectID, includeCancelled
	return []domain.LadoTask{f.task}, f.err
}

func (f *fakeLadoTaskService) Create(_ context.Context, in ladotask.CreateInput) (domain.LadoTask, error) {
	f.calls++
	f.gotCreate = in
	return f.task, f.err
}

func (f *fakeLadoTaskService) Get(_ context.Context, id domain.LadoTaskID) (domain.LadoTask, error) {
	f.calls++
	f.gotID = id
	return f.task, f.err
}

func (f *fakeLadoTaskService) UpdatePreferences(_ context.Context, id domain.LadoTaskID, agent, model *string) (domain.LadoTask, error) {
	f.calls++
	f.gotID, f.gotAgent, f.gotModel = id, agent, model
	return f.task, f.err
}

func (f *fakeLadoTaskService) Cancel(_ context.Context, id domain.LadoTaskID) (domain.LadoTask, error) {
	f.calls++
	f.gotID = id
	task := f.task
	task.CancelledAt = ladoTaskTime.Add(time.Hour)
	return task, f.err
}

func (f *fakeLadoTaskService) Import(_ context.Context, in ladotask.ImportInput) (ladotask.ImportResult, error) {
	f.calls++
	f.gotImport = in
	return ladotask.ImportResult{Tasks: []domain.LadoTask{f.task}, Skipped: []domain.IssueID{"github:acme/demo#3"}}, f.err
}

func (f *fakeLadoTaskService) ImportableIssues(_ context.Context, projectID domain.ProjectID) ([]ladotask.ImportableIssue, []ladotask.RepoFailure, error) {
	f.calls++
	f.gotProject = projectID
	return f.issues, f.failures, f.err
}

func (f *fakeLadoTaskService) Repositories(_ context.Context, projectID domain.ProjectID) ([]ladotask.ProjectRepo, error) {
	f.calls++
	f.gotProject = projectID
	return f.repos, f.err
}

func decodeLadoJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

func TestLadoAPI_ListTasks(t *testing.T) {
	svc := &fakeLadoTaskService{task: sampleLadoTask()}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks?projectId=mer&includeCancelled=true", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	resp := decodeLadoJSON[controllers.LadoTaskListResponse](t, body)
	if len(resp.Tasks) != 1 {
		t.Fatalf("tasks = %+v", resp.Tasks)
	}
	got := resp.Tasks[0]
	want := controllers.LadoTaskIssueResponse{Ref: "github:acme/demo#12", Number: 12, Title: "Add login", State: "open", URL: "https://github.com/acme/demo/issues/12", ObservedAt: ladoTaskTime}
	if got.ID != "lado-task-1" || got.ProjectID != "mer" || got.Repository != "github:acme/demo" || got.State != "draft" || got.Agent != "claude-code" || got.Issue != want || got.CancelledAt != nil {
		t.Fatalf("task = %+v", got)
	}
	if svc.gotProject != "mer" || !svc.gotCancelledToo {
		t.Fatalf("service got %q, cancelled %v", svc.gotProject, svc.gotCancelledToo)
	}

	for _, query := range []string{"", "?projectId=mer&includeCancelled=maybe"} {
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks"+query, "")
		if status != http.StatusBadRequest {
			t.Fatalf("query %q status = %d, body %s", query, status, body)
		}
	}
}

func TestLadoAPI_CreateTask(t *testing.T) {
	svc := &fakeLadoTaskService{task: sampleLadoTask()}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/tasks", `{"projectId":"mer","repository":"github:acme/back","text":"Add login\nDetails","agent":"claude-code","model":"opus"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if got := decodeLadoJSON[controllers.LadoTaskResponse](t, body); got.ID != "lado-task-1" {
		t.Fatalf("task = %+v", got)
	}
	want := ladotask.CreateInput{ProjectID: "mer", Repository: "github:acme/back", Text: "Add login\nDetails", Agent: "claude-code", Model: "opus"}
	if svc.gotCreate != want {
		t.Fatalf("service got %+v, want %+v", svc.gotCreate, want)
	}

	calls := svc.calls
	for _, bad := range []string{`not json`, `{"projectId":"mer","text":"x","priority":1}`} {
		if body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/tasks", bad); status != http.StatusBadRequest {
			t.Fatalf("body %q status = %d, response %s", bad, status, body)
		}
	}
	if svc.calls != calls {
		t.Fatal("a malformed body reached the service")
	}
}

func TestLadoAPI_TaskErrorsKeepTheirEnvelope(t *testing.T) {
	svc := &fakeLadoTaskService{err: apierr.Conflict("LADO_ISSUE_NOT_CREATED", "The tracker did not create the issue: 410 Issues are disabled", nil)}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/tasks", `{"projectId":"mer","text":"Add login"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if apiErr := decodeLadoJSON[envelope.APIError](t, body); apiErr.Code != "LADO_ISSUE_NOT_CREATED" {
		t.Fatalf("envelope = %s", body)
	}
}

func TestLadoAPI_TaskByID(t *testing.T) {
	svc := &fakeLadoTaskService{task: sampleLadoTask()}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	if body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1", ""); status != http.StatusOK || svc.gotID != "lado-task-1" {
		t.Fatalf("get status = %d, id %q, body %s", status, svc.gotID, body)
	}

	body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/lado/tasks/lado-task-1", `{"agent":"codex"}`)
	if status != http.StatusOK {
		t.Fatalf("patch status = %d, body %s", status, body)
	}
	if svc.gotAgent == nil || *svc.gotAgent != "codex" || svc.gotModel != nil {
		t.Fatalf("service got agent %v model %v, want only the agent", svc.gotAgent, svc.gotModel)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/tasks/lado-task-1/cancel", "")
	if status != http.StatusOK {
		t.Fatalf("cancel status = %d, body %s", status, body)
	}
	cancelled := decodeLadoJSON[controllers.LadoTaskResponse](t, body)
	if cancelled.State != "cancelled" || cancelled.CancelledAt == nil {
		t.Fatalf("cancelled task = %+v", cancelled)
	}
}

func TestLadoAPI_ImportTasks(t *testing.T) {
	svc := &fakeLadoTaskService{task: sampleLadoTask()}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/tasks/imports", `{"projectId":"mer","issueRefs":["github:acme/demo#12","github:acme/demo#3"]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	resp := decodeLadoJSON[controllers.ImportLadoTasksResponse](t, body)
	if len(resp.Tasks) != 1 || len(resp.Skipped) != 1 || resp.Skipped[0] != "github:acme/demo#3" {
		t.Fatalf("response = %+v", resp)
	}
	if svc.gotImport.ProjectID != "mer" || len(svc.gotImport.IssueRefs) != 2 || svc.gotImport.IssueRefs[1] != "github:acme/demo#3" {
		t.Fatalf("service got %+v", svc.gotImport)
	}
}

func TestLadoAPI_ProjectIssues(t *testing.T) {
	gitlab := ladotask.ProjectRepo{Name: "back", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/project", Host: "gitlab.example.com"}}
	front := ladotask.ProjectRepo{Name: "front", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/front"}}
	svc := &fakeLadoTaskService{
		issues: []ladotask.ImportableIssue{{Repo: gitlab, Issue: domain.Issue{
			ID:    domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project#7", Host: "gitlab.example.com"},
			Title: "Fix build", URL: "https://gitlab.example.com/group/project/-/issues/7",
		}}},
		failures: []ladotask.RepoFailure{{Repo: front, Problem: "403 Resource not accessible"}},
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/issues", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	resp := decodeLadoJSON[controllers.LadoProjectIssuesResponse](t, body)
	wantRepo := controllers.LadoRepositoryResponse{Ref: "gitlab:group/project@gitlab.example.com", Name: "back", Provider: "gitlab", Host: "gitlab.example.com", Path: "group/project"}
	if len(resp.Issues) != 1 || resp.Issues[0].Ref != "gitlab:group/project#7@gitlab.example.com" || resp.Issues[0].Number != 7 ||
		resp.Issues[0].Labels == nil || resp.Issues[0].Repository != wantRepo {
		t.Fatalf("issues = %+v", resp.Issues)
	}
	wantFailure := controllers.LadoRepositoryFailureResponse{
		Repository: controllers.LadoRepositoryResponse{Ref: "github:acme/front", Name: "front", Provider: "github", Host: "github.com", Path: "acme/front"},
		Problem:    "403 Resource not accessible",
	}
	if len(resp.Failures) != 1 || resp.Failures[0] != wantFailure {
		t.Fatalf("failures = %+v, want [%+v]", resp.Failures, wantFailure)
	}
	if svc.gotProject != "mer" {
		t.Fatalf("service got project %q", svc.gotProject)
	}

	svc.issues, svc.failures = nil, nil
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/issues", "")
	if empty := decodeLadoJSON[controllers.LadoProjectIssuesResponse](t, body); empty.Issues == nil || empty.Failures == nil {
		t.Fatalf("empty response = %s, want empty lists", body)
	}
}

func TestLadoAPI_ProjectRepositories(t *testing.T) {
	svc := &fakeLadoTaskService{repos: []ladotask.ProjectRepo{
		{Name: "back", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/back"}},
		{Name: "front", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/front"}},
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/repositories", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	resp := decodeLadoJSON[controllers.LadoProjectRepositoriesResponse](t, body)
	if len(resp.Repositories) != 2 || resp.Repositories[0].Ref != "github:acme/back" || resp.Repositories[1].Name != "front" {
		t.Fatalf("repositories = %+v", resp.Repositories)
	}
	if svc.gotProject != "mer" {
		t.Fatalf("service got project %q", svc.gotProject)
	}
}

func TestLadoAPI_TaskRoutesWithoutAServiceAnswerNotImplemented(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/api/v1/lado/tasks?projectId=mer", ""},
		{"POST", "/api/v1/lado/tasks", `{"projectId":"mer","text":"x"}`},
		{"POST", "/api/v1/lado/tasks/imports", `{"projectId":"mer","issueRefs":["github:a/b#1"]}`},
		{"GET", "/api/v1/lado/tasks/t1", ""},
		{"PATCH", "/api/v1/lado/tasks/t1", `{"agent":"codex"}`},
		{"POST", "/api/v1/lado/tasks/t1/cancel", ""},
		{"GET", "/api/v1/lado/projects/mer/issues", ""},
		{"GET", "/api/v1/lado/projects/mer/repositories", ""},
		{"POST", "/api/v1/lado/tasks/t1/plan-requests", `{}`},
		{"GET", "/api/v1/lado/tasks/t1/plans", ""},
		{"GET", "/api/v1/lado/plans/p1", ""},
		{"POST", "/api/v1/lado/plans/p1/approve", `{"sha256":"x"}`},
	} {
		if body, status, _ := doRequest(t, srv, route.method, route.path, route.body); status != http.StatusNotImplemented {
			t.Fatalf("%s %s status = %d, body %s", route.method, route.path, status, body)
		}
	}
}
