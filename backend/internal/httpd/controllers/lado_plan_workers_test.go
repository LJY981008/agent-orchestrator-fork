package controllers_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

func TestLadoAPI_SubmitPlanAndCritique(t *testing.T) {
	started := ladoTaskTime.Add(time.Minute)
	req := domain.LadoPlanRequest{
		ID: "req-1", TaskID: "lado-task-1", Kind: domain.LadoPlanRequestWorker, SessionID: "mer-3",
		RequestedAt: ladoTaskTime, WorkerStartedAt: started, DraftSubmittedAt: started,
	}
	svc := &fakeLadoTaskService{task: sampleLadoTask(), request: req, critique: domain.LadoPlanCritique{
		RequestID: "req-1", Attempt: 1, SessionID: "mer-5", State: domain.LadoCritiqueDone, StartedAt: started,
		Findings: []domain.LadoPlanFinding{{ID: "C1", Severity: domain.LadoFindingP1, Title: "Order"}},
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/plan-requests/req-1/submit", `{"sessionId":"mer-3","content":"---\n---\nPlan","draft":true}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoPlanRequestResponse](t, body)
	if got.Kind != "worker" || got.Phase != "critiquing" || got.SessionID != "mer-3" {
		t.Fatalf("response = %+v", got)
	}
	if in := svc.gotSubmit; in.RequestID != "req-1" || in.SessionID != "mer-3" || string(in.Content) != "---\n---\nPlan" || !in.Draft {
		t.Fatalf("service got %+v", in)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/plan-requests/req-1/critique",
		`{"sessionId":"mer-5","findings":[{"id":"C1","severity":"P1","title":"Order","failure_scenario":"fresh installs fail"}]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	critique := decodeLadoJSON[controllers.LadoPlanCritiqueResponse](t, body)
	if critique.State != "done" || len(critique.Findings) != 1 || critique.Findings[0].Severity != "P1" {
		t.Fatalf("critique = %+v", critique)
	}
	if in := svc.gotCritique; in.SessionID != "mer-5" || len(in.Findings) != 1 || in.Findings[0].FailureScenario != "fresh installs fail" {
		t.Fatalf("service got %+v", in)
	}
}

// A task shows where its plan worker is, and a plan revision what its review
// found, how the plan answers each finding and what making it took.
func TestLadoAPI_TaskAndPlanShowThePlanWorker(t *testing.T) {
	task := sampleLadoTask()
	task.Plan.Request = domain.LadoPlanRequest{
		ID: "req-1", TaskID: task.ID, Kind: domain.LadoPlanRequestWorker, SessionID: "mer-3", RequestedAt: ladoTaskTime,
		WorkerStartedAt: ladoTaskTime, DraftSubmittedAt: ladoTaskTime,
	}
	task.Plan.Critique = domain.LadoPlanCritique{RequestID: "req-1", Attempt: 1, SessionID: "mer-5", State: domain.LadoCritiqueRunning}
	waiting := sampleLadoTask()
	waiting.Plan.Request = domain.LadoPlanRequest{ID: "req-2", Kind: domain.LadoPlanRequestWorker, SessionID: domain.LadoPlanRequestPlaceholder("req-2"),
		RequestedAt: ladoTaskTime, WaitingReason: domain.LadoPlanWaitingCapacity}
	rev := domain.LadoPlanRevision{ID: "lado-plan-1", TaskID: task.ID, RequestID: "req-1", Number: 1, Source: "agent", SHA256: strings.Repeat("a", 64), CreatedAt: ladoTaskTime}
	metrics := domain.LadoPlanRevisionMetrics{RevisionID: rev.ID, Design: 21 * time.Minute, Critique: 15 * time.Minute, Review: domain.LadoPlanReviewDone, P1: 1, Addressed: 1,
		Sessions: []domain.LadoPlanSessionUsage{{SessionID: "mer-3", Role: domain.LadoPlanRoleDesigner, InputTokens: 1200}}}
	critique := domain.LadoPlanCritique{RequestID: "req-1", Attempt: 1, SessionID: "mer-5", State: domain.LadoCritiqueDone, StartedAt: ladoTaskTime,
		Findings: []domain.LadoPlanFinding{{ID: "C1", Severity: domain.LadoFindingP1, Title: "Order"}, {ID: "C2", Severity: domain.LadoFindingMinor, Title: "Typo"}}}
	svc := &fakeLadoTaskService{
		task: task,
		planView: ladotask.PlanView{Task: task, Revision: rev, Plan: domain.LadoPlan{Body: "Plan.\n\n## 계획 검수\n\n- C1: 반영 — 순서를 고침", ChangeKinds: []string{domain.LadoChangeDBSchema}},
			Critique: critique, Metrics: &metrics},
		history: ladotask.PlanHistory{Revisions: []domain.LadoPlanRevision{rev}, Critiques: []domain.LadoPlanCritique{critique},
			Metrics: map[domain.LadoPlanRevisionID]domain.LadoPlanRevisionMetrics{rev.ID: metrics}},
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1", "")
	view := decodeLadoJSON[controllers.LadoTaskViewResponse](t, body)
	worker := view.Task.Plan.Worker
	if status != http.StatusOK || worker == nil || worker.Phase != "critiquing" || worker.DesignerSessionID != "mer-3" ||
		worker.CriticSessionID != "mer-5" || worker.Review != "running" || worker.StartedAt == nil {
		t.Fatalf("worker = %+v (status %d)", worker, status)
	}
	svc.task, svc.view.Task = waiting, waiting
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1", "")
	if worker := decodeLadoJSON[controllers.LadoTaskViewResponse](t, body).Task.Plan.Worker; worker == nil || worker.Phase != "waiting" ||
		worker.WaitingReason != "plan_worker_capacity" || worker.DesignerSessionID != "" {
		t.Fatalf("waiting worker = %+v", worker)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/plans/lado-plan-1", "")
	plan := decodeLadoJSON[controllers.LadoPlanResponse](t, body)
	if status != http.StatusOK || plan.Critique == nil || len(plan.Critique.Findings) != 2 || plan.Critique.Findings[0].Answer != "addressed" ||
		plan.Critique.Findings[1].Answer != "" || plan.Revision.Metrics == nil || plan.Revision.Metrics.DesignMs != (21*time.Minute).Milliseconds() ||
		len(plan.Plan.ChangeKinds) != 1 || plan.Revision.Metrics.Sessions[0].Role != "design" {
		t.Fatalf("plan = %+v", plan)
	}
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1/plans", "")
	history := decodeLadoJSON[controllers.LadoPlanHistoryResponse](t, body)
	if len(history.Critiques) != 1 || history.Revisions[0].Metrics == nil || history.Revisions[0].Metrics.Review != "done" {
		t.Fatalf("history = %+v", history)
	}
}
