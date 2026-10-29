package ladotask

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ladoActivity is one activity a test gives a session's conversation.
type ladoActivity struct {
	kind            domain.ActivityKind
	detail          string
	created, closed time.Duration // after the base time
}

// activities gives session a conversation holding activities, times after
// base. The raw connection does not enforce foreign keys, so a plan worker
// session the fake session service started needs no session row.
func (pf *planFixture) activities(t *testing.T, session domain.SessionID, base time.Time, activities ...ladoActivity) {
	t.Helper()
	conversation := "conv-" + string(session)
	if _, err := pf.raw.Exec(`INSERT INTO conversations (id, scope, project_id, session_id, latest_sequence, created_at, updated_at)
		VALUES (?, 'session', 'demo', ?, ?, ?, ?)`, conversation, string(session), len(activities), base, base); err != nil {
		t.Fatalf("insert the conversation of %s: %v", session, err)
	}
	for i, a := range activities {
		if _, err := pf.raw.Exec(`INSERT INTO conversation_activities (id, conversation_id, sequence, kind, status, detail_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'completed', ?, ?, ?)`, fmt.Sprintf("%s-%d", conversation, i), conversation, i+1, string(a.kind), a.detail,
			base.Add(a.created).UTC(), base.Add(a.closed).UTC()); err != nil {
			t.Fatalf("insert activity %d of %s: %v", i, session, err)
		}
	}
}

func command(cmd string, created, closed time.Duration) ladoActivity {
	return ladoActivity{domain.ActivityKindCommand, fmt.Sprintf(`{"command":%q}`, cmd), created, closed}
}

func storedMetrics(t *testing.T, pf *planFixture, task domain.LadoTaskID) map[domain.SessionID]domain.LadoSessionMetrics {
	t.Helper()
	rows, err := pf.store.ListLadoSessionMetrics(context.Background(), task)
	if err != nil {
		t.Fatalf("ListLadoSessionMetrics: %v", err)
	}
	out := map[domain.SessionID]domain.LadoSessionMetrics{}
	for _, m := range rows {
		out[m.SessionID] = m
	}
	return out
}

// Stopping a plan request's sessions stores the designer's and the critic's
// investigation metrics, also before the request has a revision.
func TestRefreshMetricsStoresThePlanSessionsWithoutARevision(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	req := pf.requestAndStart(t, PlanRequestInput{TaskID: pf.task.ID})
	designer := pf.designer(t, req.ID)
	pf.submit(t, req.ID, designer, pf.workerPlan("issue", "", "backend/a/**", "backend/b/**", "frontend/**", "docs/**"), true)
	pf.svc.DrivePlanWorkers(ctx)
	critique, _, _ := pf.store.LadoPlanCritique(ctx, req.ID, 1)
	if critique.SessionID == "" {
		t.Fatal("no critic started")
	}
	base := pf.now
	pf.activities(t, designer, base,
		command("rg Login", time.Minute, time.Minute),
		ladoActivity{domain.ActivityKindMCPTool, `{"toolKind":"read","providerToolName":"Read"}`, 2 * time.Minute, 2 * time.Minute},
		ladoActivity{domain.ActivityKindReasoning, `{}`, 3 * time.Minute, 4 * time.Minute})
	pf.activities(t, critique.SessionID, base,
		ladoActivity{domain.ActivityKindMCPTool, `{"toolKind":"search","providerToolName":"Grep"}`, 10 * time.Minute, 12 * time.Minute})
	if revisions, _ := pf.store.ListLadoPlanRevisions(ctx, pf.task.ID); len(revisions) != 0 {
		t.Fatalf("revisions = %d, want none", len(revisions))
	}

	pf.svc.refreshMetrics(ctx, pf.request(t, req.ID))
	stored := storedMetrics(t, pf, pf.task.ID)
	if m := stored[designer]; !m.Stored || m.Role != domain.LadoSessionDesigner || m.ToolCalls != 2 || m.SearchCalls != 1 || m.ReadCalls != 1 ||
		m.Investigate != 3*time.Minute || m.EditCalls != 0 {
		t.Errorf("designer metrics = %+v", m)
	}
	if m := stored[critique.SessionID]; !m.Stored || m.Role != domain.LadoSessionCritic || m.ToolCalls != 1 || m.SearchCalls != 1 || m.Investigate != 2*time.Minute {
		t.Errorf("critic metrics = %+v", m)
	}
}

// A task's timings hold its stages and its sessions' metrics: the stored
// ones, and those of a plan or an execution session without a stored row
// counted at read time and not stored. A project's timings give the gaps
// between the stages and each role's median investigation.
func TestTaskAndProjectTimings(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	req := pf.requestAndStart(t, PlanRequestInput{TaskID: pf.task.ID})
	designer := pf.designer(t, req.ID)
	base := pf.now
	pf.activities(t, designer, base,
		command("rg Login", time.Minute, time.Minute),
		ladoActivity{domain.ActivityKindMCPTool, `{"toolKind":"read","providerToolName":"Read"}`, 2 * time.Minute, 2 * time.Minute},
		command("go test ./...", 3*time.Minute, 4*time.Minute))
	pf.now = pf.now.Add(5 * time.Minute)
	pf.submit(t, req.ID, designer, pf.workerPlan("issue", "", "backend/**"), true)
	pf.svc.WatchPlans(ctx)
	pf.svc.DrivePlanWorkers(ctx)
	pf.now = pf.now.Add(2 * time.Minute)
	pf.submit(t, req.ID, designer, pf.workerPlan("issue", "검수를 건너뜀: 영향 경로 1개", "backend/**"), false)
	pf.pin(t, designer) // the cleanup stores the designer's metrics
	rev := pf.latest(t).Plan.Latest
	if !rev.Valid() {
		t.Fatalf("revision = %+v", rev)
	}
	if m := storedMetrics(t, pf, pf.task.ID)[designer]; !m.Stored || m.ToolCalls != 3 || m.CommandCalls != 1 || m.Investigate != 3*time.Minute {
		t.Fatalf("designer metrics after the cleanup = %+v", m)
	}
	pf.now = pf.now.Add(time.Minute)
	if _, err := pf.svc.ApprovePlan(ctx, rev.ID, rev.SHA256, policyA, pf.proof(rev.ID)); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	pf.now = pf.now.Add(time.Minute)
	worker := pf.run(t, pf.claim(t))
	pf.activities(t, worker, pf.now,
		command("cd backend && cat main.go", time.Minute, time.Minute),
		ladoActivity{domain.ActivityKindFileChange, ``, 6 * time.Minute, 6 * time.Minute})

	timings, err := pf.svc.TaskTimings(ctx, pf.task.ID)
	if err != nil {
		t.Fatalf("TaskTimings: %v", err)
	}
	var stages []domain.LadoTimingStage
	for _, p := range timings.Timeline.Points {
		stages = append(stages, p.Stage)
	}
	want := []domain.LadoTimingStage{domain.LadoTimingReceived, domain.LadoTimingPlanRequested, domain.LadoTimingRevision, domain.LadoTimingApproved, domain.LadoTimingRunStarted}
	if fmt.Sprint(stages) != fmt.Sprint(want) || timings.TaskID != pf.task.ID || timings.IssueRef != pf.task.IssueRef || timings.Timeline.Runs != 1 {
		t.Fatalf("timings = %+v, stages %v; want %v", timings, stages, want)
	}
	if len(timings.Sessions) != 2 {
		t.Fatalf("sessions = %+v, want the designer and the worker", timings.Sessions)
	}
	if d := timings.Sessions[0]; d.SessionID != designer || !d.Stored || d.Role != domain.LadoSessionDesigner {
		t.Errorf("designer = %+v, want the stored row", d)
	}
	if w := timings.Sessions[1]; w.SessionID != worker || w.Stored || w.Role != domain.LadoSessionWorker || w.ReadCalls != 1 || w.EditCalls != 1 ||
		w.Investigate != 5*time.Minute || w.TaskID != pf.task.ID {
		t.Errorf("worker = %+v, want counted at read time", w)
	}
	if n := pf.count(t, "lado_session_metrics"); n != 1 {
		t.Errorf("stored metrics after a read = %d, want only the designer's", n)
	}

	// Without its stored row, the designer is counted at read time too.
	if _, err := pf.raw.Exec(`DELETE FROM lado_session_metrics`); err != nil {
		t.Fatal(err)
	}
	timings, err = pf.svc.TaskTimings(ctx, pf.task.ID)
	if err != nil || len(timings.Sessions) != 2 {
		t.Fatalf("TaskTimings = %+v, %v", timings.Sessions, err)
	}
	if d := timings.Sessions[0]; d.Stored || d.Role != domain.LadoSessionDesigner || d.ToolCalls != 3 || d.Investigate != 3*time.Minute {
		t.Errorf("designer without a row = %+v", d)
	}
	if n := pf.count(t, "lado_session_metrics"); n != 0 {
		t.Errorf("stored metrics after a read = %d, want none", n)
	}
	if _, err := pf.svc.TaskTimings(ctx, "lado-task-missing"); apiCode(err) != "LADO_TASK_NOT_FOUND" {
		t.Errorf("an unknown task = %v, want LADO_TASK_NOT_FOUND", err)
	}

	// A second task, newer, with a stored designer row of its own.
	pf.now = pf.now.Add(time.Hour)
	second, err := pf.svc.Create(ctx, CreateInput{ProjectID: "demo", Text: "Add logout"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := pf.store.UpsertLadoSessionMetrics(ctx, domain.LadoSessionMetrics{
		SessionID: "demo-plan-other", TaskID: second.ID, Role: domain.LadoSessionDesigner,
		ToolCalls: 11, ReadCalls: 4, SearchCalls: 3, Investigate: 9 * time.Minute, UpdatedAt: pf.now,
	}); err != nil {
		t.Fatalf("UpsertLadoSessionMetrics: %v", err)
	}
	summary, err := pf.svc.ProjectTimings(ctx, "demo", 0)
	if err != nil {
		t.Fatalf("ProjectTimings: %v", err)
	}
	if summary.ProjectID != "demo" || len(summary.Tasks) != 2 || summary.Tasks[0].TaskID != second.ID {
		t.Fatalf("summary tasks = %+v", summary.Tasks)
	}
	first := summary.Tasks[1].Timeline.Points
	if len(summary.Gaps) != len(first)-1 {
		t.Fatalf("gaps = %+v, want one per step of the first task", summary.Gaps)
	}
	for i, gap := range summary.Gaps {
		if gap.From != first[i].Stage || gap.To != first[i+1].Stage || gap.Count != 1 || gap.Median != first[i+1].SincePrev || gap.Max != gap.Median {
			t.Errorf("gap %d = %+v, want %s→%s once in %s", i, gap, first[i].Stage, first[i+1].Stage, first[i+1].SincePrev)
		}
	}
	roles := map[domain.LadoSessionRole]domain.LadoInvestigationSummary{}
	for _, r := range summary.Investigation {
		roles[r.Role] = r
	}
	if d := roles[domain.LadoSessionDesigner]; d.Sessions != 2 || d.MedianCalls != 7 || d.MedianInvestigationCalls != 4 || d.MedianInvestigate != 6*time.Minute {
		t.Errorf("designer summary = %+v, want the medians of (3, 11) calls, (2, 7) investigation calls and (3m, 9m)", d)
	}
	if w := roles[domain.LadoSessionWorker]; w.Sessions != 1 || w.MedianCalls != 2 || w.MedianInvestigate != 5*time.Minute {
		t.Errorf("worker summary = %+v", w)
	}

	if one, err := pf.svc.ProjectTimings(ctx, "demo", 1); err != nil || len(one.Tasks) != 1 || one.Tasks[0].TaskID != second.ID {
		t.Errorf("ProjectTimings of one = %+v, %v; want the newest task", one.Tasks, err)
	}
	for _, c := range []struct {
		project domain.ProjectID
		limit   int
		code    string
	}{
		{"", 20, "LADO_TIMINGS_PROJECT_REQUIRED"},
		{"demo", -1, "LADO_TIMINGS_LIMIT_INVALID"},
		{"demo", TimingsMaxLimit + 1, "LADO_TIMINGS_LIMIT_INVALID"},
		{"nope", 20, "PROJECT_NOT_FOUND"},
	} {
		if _, err := pf.svc.ProjectTimings(ctx, c.project, c.limit); apiCode(err) != c.code {
			t.Errorf("ProjectTimings(%q, %d) = %v, want %s", c.project, c.limit, err, c.code)
		}
	}
}
