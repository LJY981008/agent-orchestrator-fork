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
)

// fakeLadoTimingsService is a task service that reads timings too.
type fakeLadoTimingsService struct {
	fakeLadoTaskService
	timings    domain.LadoTaskTimings
	summary    domain.LadoTimingsSummary
	timingsErr error
	gotLimit   int
}

func (f *fakeLadoTimingsService) TaskTimings(_ context.Context, id domain.LadoTaskID) (domain.LadoTaskTimings, error) {
	f.calls++
	f.gotID = id
	return f.timings, f.timingsErr
}

func (f *fakeLadoTimingsService) ProjectTimings(_ context.Context, projectID domain.ProjectID, limit int) (domain.LadoTimingsSummary, error) {
	f.calls++
	f.gotProject, f.gotLimit = projectID, limit
	return f.summary, f.timingsErr
}

func sampleLadoTaskTimings() domain.LadoTaskTimings {
	at := ladoTaskTime
	return domain.LadoTaskTimings{
		TaskID: "lado-task-1", IssueRef: "github:acme/demo#12", Title: "Add login",
		Timeline: domain.LadoTaskTimeline{
			Points: []domain.LadoTimingPoint{
				{Stage: domain.LadoTimingReceived, At: at},
				{Stage: domain.LadoTimingPlanRequested, At: at.Add(90 * time.Second), SincePrev: 90 * time.Second},
				{Stage: domain.LadoTimingPROpened, At: at.Add(time.Hour), SincePrev: time.Hour - 90*time.Second, Estimated: true},
				{Stage: domain.LadoTimingMerged, At: at.Add(2 * time.Hour), SincePrev: time.Hour},
			},
			Total: 2 * time.Hour, PlanRequests: 2, Revisions: 3, Approvals: 1, Runs: 1,
		},
		Sessions: []domain.LadoSessionMetrics{
			{
				SessionID: "demo-plan-1", TaskID: "lado-task-1", Role: domain.LadoSessionDesigner,
				ToolCalls: 12, ReadCalls: 7, SearchCalls: 4, CommandCalls: 1,
				FirstActivityAt: at, LastActivityAt: at.Add(10 * time.Minute), Investigate: 10 * time.Minute,
				Stored: true, UpdatedAt: at.Add(11 * time.Minute),
			},
			{
				SessionID: "demo-7", TaskID: "lado-task-1", Role: domain.LadoSessionWorker,
				ToolCalls: 20, ReadCalls: 5, SearchCalls: 3, CommandCalls: 8, EditCalls: 4,
				FirstActivityAt: at.Add(time.Hour), FirstEditAt: at.Add(70 * time.Minute), Investigate: 9500 * time.Millisecond,
			},
		},
	}
}

func TestLadoAPI_TaskTimings(t *testing.T) {
	svc := &fakeLadoTimingsService{timings: sampleLadoTaskTimings()}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1/timings", "")
	if status != http.StatusOK || svc.gotID != "lado-task-1" {
		t.Fatalf("GET = %d %s, asked for %q", status, body, svc.gotID)
	}
	got := decodeLadoJSON[controllers.LadoTaskTimingsResponse](t, body)
	if got.TaskID != "lado-task-1" || got.IssueRef != "github:acme/demo#12" || got.Title != "Add login" {
		t.Fatalf("task = %+v", got)
	}
	tl := got.Timeline
	if tl.TotalMs != 7_200_000 || tl.PlanRequests != 2 || tl.Revisions != 3 || tl.Approvals != 1 || tl.Runs != 1 || len(tl.Points) != 4 {
		t.Fatalf("timeline = %+v", tl)
	}
	if p := tl.Points[1]; p.Stage != "plan_requested" || p.SincePrevMs != 90_000 || p.Estimated || !p.At.Equal(ladoTaskTime.Add(90*time.Second)) {
		t.Fatalf("plan requested = %+v", p)
	}
	if p := tl.Points[2]; p.Stage != "pr_opened" || !p.Estimated {
		t.Fatalf("pr opened = %+v, want estimated", p)
	}
	if len(got.Sessions) != 2 {
		t.Fatalf("sessions = %+v", got.Sessions)
	}
	designer, worker := got.Sessions[0], got.Sessions[1]
	if designer.Role != "designer" || !designer.Stored || designer.UpdatedAt == nil || designer.FirstEditAt != nil ||
		designer.InvestigationCalls != 11 || designer.InvestigateMs != 600_000 {
		t.Fatalf("designer = %+v", designer)
	}
	if worker.Role != "worker" || worker.Stored || worker.UpdatedAt != nil || worker.FirstEditAt == nil ||
		worker.EditCalls != 4 || worker.InvestigateMs != 9500 || worker.LastActivityAt != nil {
		t.Fatalf("computed worker = %+v", worker)
	}
}

func TestLadoAPI_TaskTimingsOfAnUnknownTask(t *testing.T) {
	svc := &fakeLadoTimingsService{timingsErr: apierr.NotFound("LADO_TASK_NOT_FOUND", "Unknown task")}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-9/timings", "")
	if status != http.StatusNotFound || ladoErrorCode(t, body) != "LADO_TASK_NOT_FOUND" {
		t.Fatalf("GET = %d %s, want 404", status, body)
	}
}

func TestLadoAPI_ProjectTimings(t *testing.T) {
	svc := &fakeLadoTimingsService{summary: domain.LadoTimingsSummary{
		ProjectID: "mer",
		Tasks:     []domain.LadoTaskTimings{sampleLadoTaskTimings()},
		Gaps: []domain.LadoTimingGap{
			{From: domain.LadoTimingReceived, To: domain.LadoTimingPlanRequested, Count: 3, Median: 2 * time.Minute, Max: time.Hour},
		},
		Investigation: []domain.LadoInvestigationSummary{
			{Role: domain.LadoSessionCritic, Sessions: 4, MedianCalls: 9, MedianInvestigationCalls: 8, MedianInvestigate: 5 * time.Minute},
		},
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/timings?projectId=mer", "")
	if status != http.StatusOK || svc.gotProject != "mer" || svc.gotLimit != 20 {
		t.Fatalf("GET = %d %s, asked for %q limit %d, want the default 20", status, body, svc.gotProject, svc.gotLimit)
	}
	got := decodeLadoJSON[controllers.LadoTimingsSummaryResponse](t, body)
	if got.ProjectID != "mer" || len(got.Tasks) != 1 || got.Tasks[0].Timeline.TotalMs != 7_200_000 {
		t.Fatalf("summary = %+v", got)
	}
	if len(got.Gaps) != 1 || got.Gaps[0] != (controllers.LadoTimingGapResponse{From: "received", To: "plan_requested", Count: 3, MedianMs: 120_000, MaxMs: 3_600_000}) {
		t.Fatalf("gaps = %+v", got.Gaps)
	}
	if len(got.Investigation) != 1 || got.Investigation[0] != (controllers.LadoInvestigationSummaryResponse{Role: "critic", Sessions: 4, MedianCalls: 9, MedianInvestigationCalls: 8, MedianInvestigateMs: 300_000}) {
		t.Fatalf("investigation = %+v", got.Investigation)
	}

	if _, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/timings?projectId=mer&limit=100", ""); status != http.StatusOK || svc.gotLimit != 100 {
		t.Fatalf("limit 100 = %d, asked for %d", status, svc.gotLimit)
	}
}

func TestLadoAPI_ProjectTimingsEmptyListsAreArrays(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: &fakeLadoTimingsService{summary: domain.LadoTimingsSummary{ProjectID: "mer"}}})
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/timings?projectId=mer", "")
	if status != http.StatusOK {
		t.Fatalf("GET = %d %s", status, body)
	}
	for _, field := range []string{`"tasks":[]`, `"gaps":[]`, `"investigation":[]`} {
		if !strings.Contains(string(body), field) {
			t.Fatalf("body %s has no %s", body, field)
		}
	}
}

func TestLadoAPI_ProjectTimingsBadQuery(t *testing.T) {
	svc := &fakeLadoTimingsService{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})
	for _, query := range []string{"", "?limit=5", "?projectId=mer&limit=0", "?projectId=mer&limit=101", "?projectId=mer&limit=-1", "?projectId=mer&limit=ten"} {
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/timings"+query, "")
		if status != http.StatusBadRequest || ladoErrorCode(t, body) != "INVALID_QUERY" {
			t.Fatalf("%q = %d %s, want 400", query, status, body)
		}
	}
	if svc.calls != 0 {
		t.Fatalf("the service was called %d times for bad queries", svc.calls)
	}
}

// Without a task service, or with one that reads no timings, both routes
// answer 501.
func TestLadoAPI_TimingsNotImplemented(t *testing.T) {
	for name, deps := range map[string]httpd.APIDeps{
		"no service":          {},
		"no timings in tasks": {LadoTasks: &fakeLadoTaskService{}},
	} {
		srv := newLadoTestServer(t, deps)
		for _, path := range []string{"/api/v1/lado/tasks/lado-task-1/timings", "/api/v1/lado/timings?projectId=mer"} {
			if body, status, _ := doRequest(t, srv, "GET", path, ""); status != http.StatusNotImplemented {
				t.Fatalf("%s: %s = %d %s, want 501", name, path, status, body)
			}
		}
	}
}
