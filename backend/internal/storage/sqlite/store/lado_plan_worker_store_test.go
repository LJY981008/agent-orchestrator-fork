package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func mustLadoWorkerRequest(t *testing.T, s *sqlite.Store, id, task string, at time.Time) domain.LadoPlanRequest {
	t.Helper()
	req, err := s.CreateLadoPlanRequest(context.Background(), domain.LadoPlanRequest{
		ID:                  domain.LadoPlanRequestID(id),
		TaskID:              domain.LadoTaskID(task),
		Kind:                domain.LadoPlanRequestWorker,
		SessionID:           domain.LadoPlanRequestPlaceholder(domain.LadoPlanRequestID(id)),
		IssueSnapshotSHA256: ladoSHA('1'),
		RequestedAt:         at,
	})
	if err != nil {
		t.Fatalf("CreateLadoPlanRequest %s: %v", id, err)
	}
	return req
}

func ladoWorkerEvent(action, request string, at time.Time) *domain.LadoAuditEvent {
	return &domain.LadoAuditEvent{At: at, Actor: "lado", Action: action, SubjectKind: "plan_request", SubjectID: request, Payload: json.RawMessage(`{}`)}
}

func expectPhase(t *testing.T, s *sqlite.Store, id string, want domain.LadoPlanWorkerPhase) domain.LadoPlanRequest {
	t.Helper()
	req, ok, err := s.LadoPlanRequest(context.Background(), domain.LadoPlanRequestID(id))
	if err != nil || !ok {
		t.Fatalf("read %s = (%v, %v)", id, ok, err)
	}
	if got := req.WorkerPhase(); got != want {
		t.Fatalf("%s phase = %q, want %q (%+v)", id, got, want, req)
	}
	return req
}

func TestLadoPlanWorkerRequestLifecycle(t *testing.T) {
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	ctx := context.Background()
	seedProject(t, s, "mer")
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-1", "github:acme/demo#12"))
	mustLadoWorkerRequest(t, s, "req-1", "lado-task-1", ladoPlanAt)
	at := func(minutes int) time.Time { return ladoPlanAt.Add(time.Duration(minutes) * time.Minute) }

	req := expectPhase(t, s, "req-1", domain.LadoPlanPhaseWaiting)
	if req.Kind != domain.LadoPlanRequestWorker || req.WorkerSession() != "" || !req.Open() {
		t.Fatalf("new request = %+v", req)
	}
	if err := s.SetLadoPlanRequestWaiting(ctx, "req-1", domain.LadoPlanWaitingCapacity); err != nil {
		t.Fatal(err)
	}
	if req = expectPhase(t, s, "req-1", domain.LadoPlanPhaseWaiting); req.WaitingReason != domain.LadoPlanWaitingCapacity {
		t.Fatalf("waiting reason = %q", req.WaitingReason)
	}
	if n, err := s.CountRunningLadoPlanWorkers(ctx); err != nil || n != 0 {
		t.Fatalf("running before the start = %d, %v", n, err)
	}
	attempt, ok, err := s.BeginLadoPlanWorkerAttempt(ctx, "req-1")
	if err != nil || !ok || attempt != 1 {
		t.Fatalf("first attempt = %d %v %v", attempt, ok, err)
	}
	if ok, err := s.StartLadoPlanWorker(ctx, "req-1", "mer-3", at(1), ladoWorkerEvent("plan_worker.started", "req-1", at(1))); err != nil || !ok {
		t.Fatalf("start = %v, %v", ok, err)
	}
	if ok, _ := s.StartLadoPlanWorker(ctx, "req-1", "mer-4", at(2), nil); ok {
		t.Fatal("a second start was recorded")
	}
	if _, ok, _ := s.BeginLadoPlanWorkerAttempt(ctx, "req-1"); ok {
		t.Fatal("an attempt was counted after the start")
	}
	req = expectPhase(t, s, "req-1", domain.LadoPlanPhaseDesigning)
	if req.WorkerSession() != "mer-3" || req.WaitingReason != "" || req.WorkerAttempts != 1 {
		t.Fatalf("started request = %+v", req)
	}
	if n, _ := s.CountRunningLadoPlanWorkers(ctx); n != 1 {
		t.Fatalf("running = %d, want 1", n)
	}
	if ok, err := s.RecordLadoPlanFinal(ctx, "req-1", ladoSHA('f'), at(3), nil); err != nil || ok {
		t.Fatalf("a final plan before the draft's review = %v, %v; want refused", ok, err)
	}
	if ok, err := s.RecordLadoPlanDraft(ctx, "req-1", ladoSHA('d'), at(5), nil); err != nil || !ok {
		t.Fatalf("draft = %v, %v", ok, err)
	}
	if ok, _ := s.RecordLadoPlanDraft(ctx, "req-1", ladoSHA('e'), at(6), nil); ok {
		t.Fatal("a second draft was recorded")
	}
	expectPhase(t, s, "req-1", domain.LadoPlanPhaseCritiquing)

	if err := s.CreateLadoPlanCritique(ctx, domain.LadoPlanCritique{
		ID: "crit-1", RequestID: "req-1", Attempt: 1, State: domain.LadoCritiqueRunning, StartedAt: at(6),
	}, ladoWorkerEvent("plan_worker.critique_started", "req-1", at(6))); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetLadoPlanCritiqueSession(ctx, "req-1", 1, "mer-5", nil); err != nil || !ok {
		t.Fatalf("critic session = %v, %v", ok, err)
	}
	if ok, _ := s.SetLadoPlanCritiqueSession(ctx, "req-1", 1, "mer-6", nil); ok {
		t.Fatal("a second critic session was recorded")
	}
	findings := []domain.LadoPlanFinding{{ID: "C1", Severity: domain.LadoFindingP1, Title: "order"}, {ID: "C2", Severity: domain.LadoFindingMinor, Title: "typo"}}
	if ok, err := s.FinishLadoPlanCritique(ctx, "req-1", 1, domain.LadoCritiqueDone, findings, "", at(20), nil); err != nil || !ok {
		t.Fatalf("finish = %v, %v", ok, err)
	}
	if ok, _ := s.FinishLadoPlanCritique(ctx, "req-1", 1, domain.LadoCritiqueFailed, nil, "late", at(21), nil); ok {
		t.Fatal("a finished critique finished again")
	}
	critique, ok, err := s.LadoPlanCritique(ctx, "req-1", 1)
	if err != nil || !ok || critique.State != domain.LadoCritiqueDone || critique.SessionID != "mer-5" ||
		!slices.Equal(critique.Findings, findings) || !critique.FinishedAt.Equal(at(20)) {
		t.Fatalf("critique = %+v, %v, %v", critique, ok, err)
	}
	task, _, _ := s.LadoTask(ctx, "lado-task-1")
	if task.Plan.Critique.ID != "crit-1" {
		t.Fatalf("task read critique = %+v", task.Plan.Critique)
	}
	tasks, _ := s.ListLadoTasks(ctx, "mer", false)
	if len(tasks) != 1 || tasks[0].Plan.Critique.ID != "crit-1" || tasks[0].Plan.Request.ID != "req-1" {
		t.Fatalf("listed task plan = %+v", tasks[0].Plan)
	}

	if ok, err := s.RecordLadoPlanFeedback(ctx, "req-1", at(21), nil); err != nil || !ok {
		t.Fatalf("feedback = %v, %v", ok, err)
	}
	expectPhase(t, s, "req-1", domain.LadoPlanPhaseRevising)
	if ok, err := s.RecordLadoPlanFinal(ctx, "req-1", ladoSHA('f'), at(30), ladoWorkerEvent("plan_worker.submitted", "req-1", at(30))); err != nil || !ok {
		t.Fatalf("final = %v, %v", ok, err)
	}
	if ok, err := s.RecordLadoPlanFinal(ctx, "req-1", ladoSHA('g'), at(31), nil); err != nil || !ok {
		t.Fatalf("a newer final = %v, %v; the last submission counts", ok, err)
	}
	req = expectPhase(t, s, "req-1", domain.LadoPlanPhaseSubmitted)
	if req.FinalSHA256 != ladoSHA('g') || !req.SubmittedAt.Equal(at(31)) {
		t.Fatalf("final = %s at %s", req.FinalSHA256, req.SubmittedAt)
	}
	if ok, _ := s.RecordLadoPlanFixRequested(ctx, "req-1", at(32)); !ok {
		t.Fatal("the fix request was not recorded")
	}
	if ok, _ := s.RecordLadoPlanFixRequested(ctx, "req-1", at(33)); ok {
		t.Fatal("a second fix request was recorded")
	}
	expectPhase(t, s, "req-1", domain.LadoPlanPhaseFixing)
	if err := s.RecordLadoPlanReminder(ctx, "req-1", at(34)); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CleanLadoPlanRequest(ctx, "req-1", at(40), ladoWorkerEvent("plan_worker.cleaned", "req-1", at(40))); err != nil || !ok {
		t.Fatalf("clean = %v, %v", ok, err)
	}
	req = expectPhase(t, s, "req-1", domain.LadoPlanPhaseDone)
	if req.Open() || !req.RemindedAt.Equal(at(34)) {
		t.Fatalf("cleaned request = %+v", req)
	}
	if ok, _ := s.FailLadoPlanWorkerRequest(ctx, "req-1", at(41), domain.LadoPlanWorkerTimedOut, "late", nil); ok {
		t.Fatal("a cleaned request failed")
	}
	if n, _ := s.CountRunningLadoPlanWorkers(ctx); n != 0 {
		t.Fatalf("running after the cleanup = %d", n)
	}
	open, err := s.ListOpenLadoPlanWorkerRequests(ctx)
	if err != nil || len(open) != 0 {
		t.Fatalf("open requests = %+v, %v", open, err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	rows, err := raw.Query(`SELECT action FROM lado_audit_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var actions []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"plan_worker.started", "plan_worker.critique_started", "plan_worker.submitted", "plan_worker.cleaned"}; !slices.Equal(actions, want) {
		t.Fatalf("audit actions = %v, want %v", actions, want)
	}
}

// The delivery writes of a request to an orchestrator never touch a plan
// worker request, and a failed one takes no more steps.
func TestLadoPlanWorkerRequestFailsAndIgnoresDeliveries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-1", "github:acme/demo#12"))
	mustLadoWorkerRequest(t, s, "req-1", "lado-task-1", ladoPlanAt)
	if err := s.MarkLadoPlanRequestDelivered(ctx, "req-1", ladoPlanAt); err != nil {
		t.Fatal(err)
	}
	if err := s.FailLadoPlanRequest(ctx, "req-1", ladoPlanAt, "orchestrator gone"); err != nil {
		t.Fatal(err)
	}
	req := expectPhase(t, s, "req-1", domain.LadoPlanPhaseWaiting)
	if !req.DeliveredAt.IsZero() || !req.FailedAt.IsZero() {
		t.Fatalf("an orchestrator delivery touched a plan worker request: %+v", req)
	}
	if ok, err := s.FailLadoPlanWorkerRequest(ctx, "req-1", ladoPlanAt.Add(time.Minute), domain.LadoPlanWorkerSpawnFailed, "no agent", nil); err != nil || !ok {
		t.Fatalf("fail = %v, %v", ok, err)
	}
	req = expectPhase(t, s, "req-1", domain.LadoPlanPhaseFailed)
	if req.FailureCode != domain.LadoPlanWorkerSpawnFailed || req.DeliveryError != "no agent" {
		t.Fatalf("failed request = %+v", req)
	}
	if _, ok, _ := s.BeginLadoPlanWorkerAttempt(ctx, "req-1"); ok {
		t.Fatal("a failed request counted an attempt")
	}
	if ok, _ := s.FailLadoPlanWorkerRequest(ctx, "req-1", ladoPlanAt.Add(2*time.Minute), domain.LadoPlanRequestReplaced, "", nil); ok {
		t.Fatal("a failed request failed again")
	}
	if task, _, _ := s.LadoTask(ctx, "lado-task-1"); task.Plan.RequestPending() {
		t.Fatal("a failed plan worker request still reads as pending")
	}
}

func TestLadoPlanRevisionMetricsAndUsage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	rev := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	cost := int64(1500)
	metrics := domain.LadoPlanRevisionMetrics{
		RevisionID: rev.ID, RequestID: "req-1", Design: 21 * time.Minute, Critique: 15 * time.Minute, Revise: 4 * time.Minute,
		Review: domain.LadoPlanReviewDone, P1: 2, Major: 1, Minor: 3, Addressed: 2, NotAddressed: 1,
		Sessions: []domain.LadoPlanSessionUsage{
			{SessionID: "mer-3", Role: domain.LadoPlanRoleDesigner, InputTokens: 1200000, OutputTokens: 30000, CacheReadTokens: 900000, CacheWriteTokens: 50000, CostNanos: &cost},
			{SessionID: "mer-5", Role: domain.LadoPlanRoleCritic, InputTokens: 600000},
		},
		ChangeKinds: []string{domain.LadoChangeDBSchema},
		UpdatedAt:   ladoPlanAt,
	}
	if err := s.SetLadoPlanRevisionMetrics(ctx, metrics); err != nil {
		t.Fatal(err)
	}
	metrics.Sessions[1].OutputTokens = 9000
	if err := s.SetLadoPlanRevisionMetrics(ctx, metrics); err != nil {
		t.Fatalf("replace metrics: %v", err)
	}
	got, ok, err := s.LadoPlanRevisionMetrics(ctx, rev.ID)
	if err != nil || !ok {
		t.Fatalf("read metrics = %v, %v", ok, err)
	}
	if got.Design != metrics.Design || got.Review != domain.LadoPlanReviewDone || got.P1 != 2 || got.NotAddressed != 1 ||
		len(got.Sessions) != 2 || got.Sessions[1].OutputTokens != 9000 || *got.Sessions[0].CostNanos != cost ||
		!slices.Equal(got.ChangeKinds, metrics.ChangeKinds) {
		t.Fatalf("metrics = %+v", got)
	}
	byRevision, err := s.ListLadoPlanRevisionMetrics(ctx, "lado-task-1")
	if err != nil || len(byRevision) != 1 || byRevision[rev.ID].Critique != metrics.Critique {
		t.Fatalf("listed metrics = %+v, %v", byRevision, err)
	}

	t0 := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	session := seedUsageSession(t, s, domain.HarnessClaudeCode)
	empty, err := s.LadoSessionUsage(ctx, session.ID)
	if err != nil || empty.InputTokens != 0 || empty.CostNanos != nil {
		t.Fatalf("usage without events = %+v, %v", empty, err)
	}
	source := seedUsageSource(t, s, session, t0)
	mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{
		ByteOffset: 100, State: domain.UsageSourceActive, ParserStateJSON: `{}`, UpdatedAt: t0,
	}, []domain.ModelUsageEvent{anthropicUsageEvent("one", 10, 40, 100, 5), anthropicUsageEvent("two", 20, 0, 50, 7)}))
	usage, err := s.LadoSessionUsage(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.InputTokens != 220 || usage.OutputTokens != 12 || usage.CacheReadTokens != 150 || usage.CacheWriteTokens != 40 {
		t.Fatalf("usage = %+v, want 220 in, 12 out, 150 cache reads, 40 cache writes", usage)
	}
}

func TestLadoOrchestratorNotices(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	since, err := s.LadoNoticeSince(ctx)
	if err != nil || since.IsZero() {
		t.Fatalf("notice start = %v, %v", since, err)
	}
	notice := func(key string, minute int) domain.LadoOrchestratorNotice {
		at := ladoPlanAt.Add(time.Duration(minute) * time.Minute)
		return domain.LadoOrchestratorNotice{ProjectID: "mer", Key: key, Text: "LADO 알림: " + key, OccurredAt: at, CreatedAt: at}
	}
	for _, n := range []domain.LadoOrchestratorNotice{notice("b", 2), notice("a", 1), notice("c", 3)} {
		if added, err := s.AddLadoOrchestratorNotice(ctx, n); err != nil || !added {
			t.Fatalf("add %s = %v, %v", n.Key, added, err)
		}
	}
	if added, err := s.AddLadoOrchestratorNotice(ctx, notice("a", 9)); err != nil || added {
		t.Fatalf("a repeated key = %v, %v; want ignored", added, err)
	}
	waiting, err := s.ListWaitingLadoOrchestratorNotices(ctx)
	if err != nil || len(waiting) != 3 || waiting[0].Key != "a" || waiting[2].Key != "c" {
		t.Fatalf("waiting = %+v, %v", waiting, err)
	}
	if err := s.MarkLadoOrchestratorNoticesSent(ctx, []int64{waiting[0].ID, waiting[1].ID}, "mer-orchestrator-1", ladoPlanAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.DropLadoOrchestratorNotices(ctx, []int64{waiting[2].ID}, ladoPlanAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if waiting, _ := s.ListWaitingLadoOrchestratorNotices(ctx); len(waiting) != 0 {
		t.Fatalf("still waiting: %+v", waiting)
	}
	all, err := s.ListLadoOrchestratorNotices(ctx, "mer")
	if err != nil || len(all) != 3 || all[0].SentTo != "mer-orchestrator-1" || all[2].DroppedAt.IsZero() {
		t.Fatalf("all notices = %+v, %v", all, err)
	}
}

func TestLadoNoticeFacts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-1", "github:acme/demo#12"))
	old := ladoPlanAt
	recent := ladoPlanAt.Add(48 * time.Hour)
	mustLadoPlanRequest(t, s, "req-old", "lado-task-1", old)
	mustLadoWorkerRequest(t, s, "req-new", "lado-task-1", recent)
	if _, err := s.FailLadoPlanWorkerRequest(ctx, "req-new", recent.Add(time.Minute), domain.LadoPlanWorkerNoPlan, "", nil); err != nil {
		t.Fatal(err)
	}
	rev := ladoRevision("lado-plan-1", "lado-task-1", "req-old", 1, ladoSHA('a'))
	rev.CreatedAt = recent.Add(2 * time.Minute)
	mustPin(t, s, rev)
	since := recent.Add(-time.Hour)
	requests, err := s.ListLadoPlanRequestNoticeFacts(ctx, since)
	if err != nil || len(requests) != 1 || requests[0].ID != "req-new" || requests[0].IssueRef != "github:acme/demo#12" ||
		requests[0].FailureCode != domain.LadoPlanWorkerNoPlan || requests[0].ProjectID != "mer" {
		t.Fatalf("request facts = %+v, %v", requests, err)
	}
	revisions, err := s.ListLadoPlanRevisionNoticeFacts(ctx, since)
	if err != nil || len(revisions) != 1 || revisions[0].Number != 1 || revisions[0].TaskID != "lado-task-1" {
		t.Fatalf("revision facts = %+v, %v", revisions, err)
	}
	if projects, err := s.ListLadoProjectsWithRunsSince(ctx, since); err != nil || len(projects) != 0 {
		t.Fatalf("projects with runs = %v, %v", projects, err)
	}
}

func TestLadoDispatchSettingsCarryPlanWorkerSettings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	defaults, err := s.LadoDispatchSettings(ctx)
	if err != nil || defaults.MaxPlanWorkers != 2 || !defaults.PlanReview.Skip || defaults.PlanReview.MaxPaths != 3 ||
		len(defaults.PlanReview.Patterns[domain.LadoChangeDBSchema]) == 0 {
		t.Fatalf("defaults = %+v, %v", defaults, err)
	}
	event := domain.LadoAuditEvent{At: ladoPlanAt, Actor: "api", Action: "dispatch.settings_changed", SubjectKind: "settings", SubjectID: "dispatch", Payload: json.RawMessage(`{}`)}
	custom := defaults
	custom.MaxPlanWorkers = 1
	custom.PlanReview = domain.LadoPlanReviewSettings{Skip: false, MaxPaths: 5, Patterns: map[string][]string{domain.LadoChangeAuth: {"**/login/**"}}}
	stored, err := s.SetLadoDispatchSettings(ctx, custom, event)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MaxPlanWorkers != 1 || stored.PlanReview.Skip || stored.PlanReview.MaxPaths != 5 ||
		!slices.Equal(stored.PlanReview.Patterns[domain.LadoChangeAuth], []string{"**/login/**"}) || len(stored.PlanReview.Patterns) != 1 {
		t.Fatalf("stored = %+v", stored)
	}
	custom.PlanReview = domain.DefaultLadoPlanReviewSettings()
	if stored, err = s.SetLadoDispatchSettings(ctx, custom, event); err != nil || len(stored.PlanReview.Patterns) != len(domain.LadoChangeKinds) {
		t.Fatalf("back to the default patterns = %+v, %v", stored.PlanReview, err)
	}
}

// Each step of a plan worker request publishes its task's change, except a
// reminder and a spawn attempt; critiques and metrics publish it too.
func TestLadoPlanWorkerChangesPublishTaskChangeEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-1", "github:acme/demo#12"))
	last := func() int64 {
		events, err := s.EventsAfter(ctx, 0, 10000)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 0 {
			return 0
		}
		return events[len(events)-1].Seq
	}
	seq := last()
	expect := func(step string, want int) {
		t.Helper()
		events, err := s.EventsAfter(ctx, seq, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != want {
			t.Fatalf("%s: %d change events, want %d", step, len(events), want)
		}
		seq = last()
	}
	mustLadoWorkerRequest(t, s, "req-1", "lado-task-1", ladoPlanAt)
	expect("request", 1)
	if _, _, err := s.BeginLadoPlanWorkerAttempt(ctx, "req-1"); err != nil {
		t.Fatal(err)
	}
	expect("attempt", 0)
	if _, err := s.StartLadoPlanWorker(ctx, "req-1", "mer-3", ladoPlanAt, nil); err != nil {
		t.Fatal(err)
	}
	expect("start", 1)
	if err := s.RecordLadoPlanReminder(ctx, "req-1", ladoPlanAt); err != nil {
		t.Fatal(err)
	}
	expect("reminder", 0)
	if _, err := s.RecordLadoPlanDraft(ctx, "req-1", ladoSHA('d'), ladoPlanAt, nil); err != nil {
		t.Fatal(err)
	}
	expect("draft", 1)
	if err := s.CreateLadoPlanCritique(ctx, domain.LadoPlanCritique{ID: "c", RequestID: "req-1", Attempt: 1, State: domain.LadoCritiqueRunning, StartedAt: ladoPlanAt}, nil); err != nil {
		t.Fatal(err)
	}
	expect("critique", 1)
	if _, err := s.SetLadoPlanCritiqueSession(ctx, "req-1", 1, "mer-5", nil); err != nil {
		t.Fatal(err)
	}
	expect("critic", 1)
}
