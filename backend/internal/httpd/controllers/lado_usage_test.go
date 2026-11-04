package controllers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladousage"
)

type fakeLadoUsageService struct {
	task       ladousage.TaskUsage
	project    ladousage.ProjectUsage
	err        error
	gotTask    domain.LadoTaskID
	gotProject domain.ProjectID
}

func (f *fakeLadoUsageService) TaskUsage(_ context.Context, id domain.LadoTaskID) (ladousage.TaskUsage, error) {
	f.gotTask = id
	return f.task, f.err
}

func (f *fakeLadoUsageService) ProjectUsage(_ context.Context, id domain.ProjectID) (ladousage.ProjectUsage, error) {
	f.gotProject = id
	return f.project, f.err
}

func sampleLadoTaskUsage() ladousage.TaskUsage {
	return ladousage.TaskUsage{
		TaskID: "lado-task-1", ProjectID: "demo", IssueRef: "github:acme/demo#12", Title: "Add login",
		Totals: ladousage.Totals{
			CostNanos: 12_340_000_000, InputTokens: 1000, CachedInputTokens: 800, OutputTokens: 50,
			SubagentCostNanos: 2_000_000_000, SubagentInputTokens: 300, SubagentOutputTokens: 10,
		},
		Compactions: 3, StartedAt: ladoTaskTime, EndedAt: ladoTaskTime.Add(90 * time.Minute), Elapsed: 90 * time.Minute,
		EndReason: ladousage.EndInProgress, UsageMissing: 1,
		Sessions: []ladousage.SessionUsage{
			{SessionID: "demo-plan-1", Role: domain.LadoSessionDesigner, Attempt: 2, Totals: ladousage.Totals{CostNanos: 10_340_000_000}},
			{SessionID: "demo-7", Role: domain.LadoSessionPart, PartID: "store", Attempt: 1, ContextUsed: 150_000, ContextWindow: 200_000, Compactions: 3},
			{SessionID: "demo-6", Role: domain.LadoSessionPart, PartID: "store", UsageMissing: true},
		},
	}
}

func TestLadoAPI_TaskUsage(t *testing.T) {
	svc := &fakeLadoUsageService{task: sampleLadoTaskUsage()}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoUsage: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1/usage", "")
	if status != http.StatusOK || svc.gotTask != "lado-task-1" {
		t.Fatalf("GET = %d %s, asked for %q", status, body, svc.gotTask)
	}
	got := decodeLadoJSON[controllers.LadoTaskUsageResponse](t, body)
	task := got.Task
	if task.TaskID != "lado-task-1" || task.Totals.CostNanos != 12_340_000_000 || task.Totals.SubagentCostNanos != 2_000_000_000 ||
		task.Compactions != 3 || task.ElapsedMs != 5_400_000 || task.EndReason != "in_progress" || !task.InProgress ||
		task.SessionsMissing != 1 || task.StartedAt == nil || !task.EndedAt.Equal(ladoTaskTime.Add(90*time.Minute)) {
		t.Fatalf("task = %+v", task)
	}
	if len(got.Sessions) != 3 {
		t.Fatalf("sessions = %+v", got.Sessions)
	}
	part := got.Sessions[1]
	if part.Role != "part" || part.PartID != "store" || part.Attempt != 1 || part.ContextUsed != 150_000 || part.Compactions != 3 {
		t.Errorf("part session = %+v", part)
	}
	if !got.Sessions[2].UsageMissing || got.Sessions[2].Totals.CostNanos != 0 {
		t.Errorf("deleted session = %+v", got.Sessions[2])
	}
	if !strings.Contains(string(body), `"role":"designer"`) || !strings.Contains(string(body), `"costNanos":12340000000`) {
		t.Errorf("body = %s", body)
	}

	merged := sampleLadoTaskUsage()
	merged.EndReason = ladousage.EndMerged
	svc.task = merged
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1/usage", "")
	if got := decodeLadoJSON[controllers.LadoTaskUsageResponse](t, body); status != http.StatusOK || got.Task.EndReason != "merged" || got.Task.InProgress {
		t.Fatalf("merged = %d %s", status, body)
	}

	svc.err = apierr.NotFound("LADO_TASK_NOT_FOUND", "Unknown task")
	if body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/nope/usage", ""); status != http.StatusNotFound {
		t.Fatalf("unknown task = %d %s", status, body)
	}
}

func TestLadoAPI_ProjectUsage(t *testing.T) {
	task := sampleLadoTaskUsage()
	task.Sessions = nil
	svc := &fakeLadoUsageService{project: ladousage.ProjectUsage{ProjectID: "demo", Tasks: []ladousage.TaskUsage{task}}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoUsage: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/demo/usage", "")
	if status != http.StatusOK || svc.gotProject != "demo" {
		t.Fatalf("GET = %d %s, asked for %q", status, body, svc.gotProject)
	}
	got := decodeLadoJSON[controllers.LadoProjectUsageResponse](t, body)
	if got.ProjectID != "demo" || len(got.Tasks) != 1 || got.Tasks[0].Totals.CostNanos != 12_340_000_000 || strings.Contains(string(body), `"sessions"`) {
		t.Fatalf("project = %s", body)
	}

	svc.err = apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	if body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/missing/usage", ""); status != http.StatusNotFound {
		t.Fatalf("unknown project = %d %s", status, body)
	}
}

func TestLadoAPI_UsageWithoutService(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, path := range []string{"/api/v1/lado/tasks/t/usage", "/api/v1/lado/projects/p/usage"} {
		if body, status, _ := doRequest(t, srv, "GET", path, ""); status != http.StatusNotImplemented {
			t.Errorf("%s = %d %s, want 501", path, status, body)
		}
	}
}
