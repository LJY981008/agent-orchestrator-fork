package store_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// openLadoTimingsStore returns a store whose project mer has LADO mode on,
// and a raw connection to the same file for the activities a chat driver
// writes.
func openLadoTimingsStore(t *testing.T) (*sqlite.Store, *sql.DB) {
	t.Helper()
	s, raw := openLadoQueueStore(t)
	seedProject(t, s, "mer")
	mustLadoMode(t, s, "mer", true)
	return s, raw
}

func TestLadoExecutionTimingsKeepTheFirstTimes(t *testing.T) {
	s, _ := openLadoTimingsStore(t)
	ctx := context.Background()
	at := func(minutes int) time.Time { return ladoVerifyAt.Add(time.Duration(minutes) * time.Minute) }
	exec := mustLadoExecution(t, s, "1", at(0), false)
	execution := func() domain.LadoTimingExecution {
		t.Helper()
		facts, ok, err := s.LadoTaskTimingFacts(ctx, exec.TaskID)
		if err != nil || !ok || len(facts.Executions) != 1 {
			t.Fatalf("LadoTaskTimingFacts = %+v, %v, %v; want one execution", facts.Executions, ok, err)
		}
		return facts.Executions[0]
	}
	if e := execution(); !e.PRSeenAt.IsZero() || !e.VerifiedAt.IsZero() {
		t.Fatalf("execution before any record = %+v, want no times", e)
	}

	// The head can verify before the loop records the PR: both keep their
	// first time.
	if err := s.MarkLadoExecutionVerified(ctx, exec.SessionID, at(30)); err != nil {
		t.Fatalf("MarkLadoExecutionVerified: %v", err)
	}
	if err := s.MarkLadoExecutionPRSeen(ctx, exec.SessionID, "https://github.com/acme/demo/pull/7", at(10)); err != nil {
		t.Fatalf("MarkLadoExecutionPRSeen: %v", err)
	}
	if err := s.MarkLadoExecutionPRSeen(ctx, exec.SessionID, "https://github.com/acme/demo/pull/8", at(20)); err != nil {
		t.Fatalf("MarkLadoExecutionPRSeen again: %v", err)
	}
	if err := s.MarkLadoExecutionVerified(ctx, exec.SessionID, at(40)); err != nil {
		t.Fatalf("MarkLadoExecutionVerified again: %v", err)
	}
	e := execution()
	if !e.PRSeenAt.Equal(at(10)) || !e.VerifiedAt.Equal(at(30)) || e.PRSeenEstimated || e.VerifiedEstimated {
		t.Fatalf("execution = %+v, want the PR seen at %v and verified at %v, not estimated", e, at(10), at(30))
	}

	if err := s.MarkLadoExecutionPRSeen(ctx, "mer-missing", "https://github.com/acme/demo/pull/9", at(1)); err == nil {
		t.Fatal("a PR of no execution was recorded")
	}
	if err := s.MarkLadoExecutionPRSeen(ctx, exec.SessionID, "", at(1)); err == nil {
		t.Fatal("a PR without a URL was recorded")
	}
	if err := s.MarkLadoExecutionVerified(ctx, exec.SessionID, time.Time{}); err == nil {
		t.Fatal("a verification without a time was recorded")
	}
}

func TestLadoTaskTimingFactsReadsTheTask(t *testing.T) {
	s, _ := openLadoTimingsStore(t)
	ctx := context.Background()
	at := func(minutes int) time.Time { return ladoVerifyAt.Add(time.Duration(minutes) * time.Minute) }
	exec := mustLadoExecution(t, s, "2", at(0), false)

	// A worker request's designer and critic are plan sessions; the
	// orchestrator of the task's first request and a worker request whose
	// designer never started are not.
	mustLadoWorkerRequest(t, s, "req-w", string(exec.TaskID), ladoPlanAt.Add(time.Minute))
	if _, ok, err := s.BeginLadoPlanWorkerAttempt(ctx, "req-w"); err != nil || !ok {
		t.Fatalf("BeginLadoPlanWorkerAttempt = %v, %v", ok, err)
	}
	if ok, err := s.StartLadoPlanWorker(ctx, "req-w", "designer-1", ladoPlanAt.Add(2*time.Minute), nil); err != nil || !ok {
		t.Fatalf("StartLadoPlanWorker = %v, %v", ok, err)
	}
	if err := s.CreateLadoPlanCritique(ctx, domain.LadoPlanCritique{
		ID: "crit-1", RequestID: "req-w", Attempt: 1, State: domain.LadoCritiqueRunning, StartedAt: ladoPlanAt.Add(3 * time.Minute),
	}, nil); err != nil {
		t.Fatalf("CreateLadoPlanCritique: %v", err)
	}
	if ok, err := s.SetLadoPlanCritiqueSession(ctx, "req-w", 1, "critic-1", nil); err != nil || !ok {
		t.Fatalf("SetLadoPlanCritiqueSession = %v, %v", ok, err)
	}
	mustLadoWorkerRequest(t, s, "req-p", string(exec.TaskID), ladoPlanAt.Add(4*time.Minute))

	pr := mustWriteLadoHeadPR(t, s, exec.SessionID, 7, domain.PRStateOpen, ladoHead('a'), at(5))
	if err := s.MarkLadoExecutionPRSeen(ctx, exec.SessionID, pr.URL, at(6)); err != nil {
		t.Fatalf("MarkLadoExecutionPRSeen: %v", err)
	}
	record := ladoVerification(t, exec.SessionID, pr.URL, ladoHead('a'), at(10))
	mustUpsertLadoVerification(t, s, record)
	mustApproveLadoIntegration(t, s, ladoIntegrationApproval(t, "approval-int", record, domain.LadoIntegrationStatusPosted, at(20)))

	facts, ok, err := s.LadoTaskTimingFacts(ctx, exec.TaskID)
	if err != nil || !ok {
		t.Fatalf("LadoTaskTimingFacts = %v, %v", ok, err)
	}
	if facts.TaskID != exec.TaskID || facts.ProjectID != "mer" || facts.IssueRef != "github:acme/demo#2" || facts.Title != "Task 2" ||
		!facts.CreatedAt.Equal(ladoTaskCreatedAt) {
		t.Fatalf("task = %s %s %s %q %v", facts.TaskID, facts.ProjectID, facts.IssueRef, facts.Title, facts.CreatedAt)
	}
	if !slices.EqualFunc(facts.PlanRequestedAt, []time.Time{ladoPlanAt, ladoPlanAt.Add(time.Minute), ladoPlanAt.Add(4 * time.Minute)}, time.Time.Equal) {
		t.Fatalf("plan requests = %v", facts.PlanRequestedAt)
	}
	if len(facts.RevisionCreatedAt) != 1 || facts.RevisionCreatedAt[0].IsZero() {
		t.Fatalf("revisions = %v, want one", facts.RevisionCreatedAt)
	}
	if want := []domain.LadoTimingApproval{{ID: "lado-approval-2", At: ladoQueueAt}}; !slices.EqualFunc(facts.Approvals, want, func(a, b domain.LadoTimingApproval) bool {
		return a.ID == b.ID && a.At.Equal(b.At)
	}) {
		t.Fatalf("approvals = %+v, want %+v", facts.Approvals, want)
	}
	if len(facts.QueueEntries) != 1 || facts.QueueEntries[0].ID != "lado-queue-2" || facts.QueueEntries[0].ApprovalID != "lado-approval-2" ||
		!facts.QueueEntries[0].PartsStartedAt.IsZero() {
		t.Fatalf("queue entries = %+v", facts.QueueEntries)
	}
	if len(facts.Executions) != 1 {
		t.Fatalf("executions = %+v, want one", facts.Executions)
	}
	if e := facts.Executions[0]; e.SessionID != exec.SessionID || e.Role != domain.LadoRoleSingle || e.ApprovalID != "lado-approval-2" ||
		e.QueueEntryID != "lado-queue-2" || !e.StartedAt.Equal(at(0)) || !e.PRSeenAt.Equal(at(6)) || !e.VerifiedAt.IsZero() ||
		!e.SettledAt.IsZero() || e.SettledState != "" {
		t.Fatalf("execution = %+v", e)
	}
	if len(facts.IntegrationApprovals) != 1 || facts.IntegrationApprovals[0].SessionID != exec.SessionID || !facts.IntegrationApprovals[0].At.Equal(at(20)) {
		t.Fatalf("integration approvals = %+v", facts.IntegrationApprovals)
	}
	wantSessions := []domain.LadoTimingSession{{SessionID: "designer-1", Role: domain.LadoSessionDesigner}, {SessionID: "critic-1", Role: domain.LadoSessionCritic}}
	if !slices.Equal(facts.PlanSessions, wantSessions) {
		t.Fatalf("plan sessions = %+v, want %+v", facts.PlanSessions, wantSessions)
	}
	stages := []domain.LadoTimingStage{}
	for _, p := range domain.LadoTaskTimelineOf(facts).Points {
		stages = append(stages, p.Stage)
	}
	if want := []domain.LadoTimingStage{
		domain.LadoTimingReceived, domain.LadoTimingPlanRequested, domain.LadoTimingRevision, domain.LadoTimingApproved,
		domain.LadoTimingRunStarted, domain.LadoTimingPROpened, domain.LadoTimingIntegrationApproved,
	}; !slices.Equal(stages, want) {
		t.Fatalf("timeline stages = %v, want %v", stages, want)
	}

	if _, ok, err := s.LadoTaskTimingFacts(ctx, "lado-task-missing"); err != nil || ok {
		t.Fatalf("facts of a missing task = %v, %v; want not found", ok, err)
	}
}

func TestListLadoTaskIDsForTimings(t *testing.T) {
	s, _ := openLadoTimingsStore(t)
	ctx := context.Background()
	seedProject(t, s, "other")
	for i, id := range []string{"lado-task-a", "lado-task-b", "lado-task-c"} {
		task := importedLadoTask(id, domain.IssueID("github:acme/demo#"+id))
		task.CreatedAt = ladoTaskCreatedAt.Add(time.Duration(i) * time.Hour)
		mustCreateLadoTask(t, s, task)
	}
	mustCreateLadoTask(t, s, pendingLadoTask("lado-task-pending"))
	elsewhere := importedLadoTask("lado-task-other", "github:acme/demo#other")
	elsewhere.ProjectID = "other"
	mustCreateLadoTask(t, s, elsewhere)

	ids, err := s.ListLadoTaskIDsForTimings(ctx, "mer", 2)
	if err != nil || !slices.Equal(ids, []domain.LadoTaskID{"lado-task-c", "lado-task-b"}) {
		t.Fatalf("ListLadoTaskIDsForTimings = %v, %v; want the newest two tasks with an issue", ids, err)
	}
	if ids, err := s.ListLadoTaskIDsForTimings(ctx, "mer", 100); err != nil || len(ids) != 3 {
		t.Fatalf("ListLadoTaskIDsForTimings of all = %v, %v; want the three tasks with an issue", ids, err)
	}
	if _, err := s.ListLadoTaskIDsForTimings(ctx, "mer", 0); err == nil {
		t.Fatal("a zero limit was accepted")
	}
}

func TestLadoSessionMetricsAndActivityFacts(t *testing.T) {
	s, raw := openLadoTimingsStore(t)
	ctx := context.Background()
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-1", "github:acme/demo#1"))
	at := func(seconds int) time.Time { return ladoVerifyAt.Add(time.Duration(seconds) * time.Second) }
	session := mustLadoSession(t, s, "mer", false).ID
	if _, err := s.CreateConversation(ctx, "conv-1", domain.ConversationScopeSession, "mer", session, at(0)); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	activity := func(sequence int, kind domain.ActivityKind, detail string, created, updated int) {
		t.Helper()
		if _, err := raw.Exec(`INSERT INTO conversation_activities (id, conversation_id, sequence, kind, status, detail_json, created_at, updated_at)
			VALUES (?, 'conv-1', ?, ?, 'completed', ?, ?, ?)`, "act-"+string(rune('a'+sequence)), sequence, string(kind), detail, at(created), at(updated)); err != nil {
			t.Fatalf("insert activity %d: %v", sequence, err)
		}
	}
	activity(1, domain.ActivityKindReasoning, `{}`, 0, 1)
	activity(2, domain.ActivityKindCommand, `{"command":"rg foo","rawCommand":"bash -lc 'rg foo'"}`, 10, 12)
	activity(3, domain.ActivityKindCommand, `{"rawCommand":"cat a.go"}`, 20, 21)
	activity(4, domain.ActivityKindMCPTool, `{"protocol":"acp","toolKind":"read","providerToolName":"Read"}`, 30, 31)
	activity(5, domain.ActivityKindFileChange, ``, 100, 101)
	activity(6, domain.ActivityKindCommand, `not json`, 110, 200)

	facts, err := s.LadoSessionActivityFacts(ctx, session)
	if err != nil {
		t.Fatalf("LadoSessionActivityFacts: %v", err)
	}
	type fact struct {
		kind                    domain.ActivityKind
		toolKind, tool, command string
	}
	var got []fact
	for _, f := range facts {
		got = append(got, fact{f.Kind, f.ToolKind, f.ToolName, f.Command})
	}
	want := []fact{
		{domain.ActivityKindReasoning, "", "", ""},
		{domain.ActivityKindCommand, "", "", "rg foo"},
		{domain.ActivityKindCommand, "", "", "cat a.go"},
		{domain.ActivityKindMCPTool, "read", "Read", ""},
		{domain.ActivityKindFileChange, "", "", ""},
		{domain.ActivityKindCommand, "", "", ""},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("facts = %+v\nwant  %+v", got, want)
	}
	if !facts[1].CreatedAt.Equal(at(10)) || !facts[5].UpdatedAt.Equal(at(200)) {
		t.Fatalf("fact times = %v, %v", facts[1].CreatedAt, facts[5].UpdatedAt)
	}
	if none, err := s.LadoSessionActivityFacts(ctx, "mer-none"); err != nil || len(none) != 0 {
		t.Fatalf("facts of a session without a conversation = %v, %v; want none", none, err)
	}

	metrics := domain.SummarizeLadoActivities(facts)
	metrics.SessionID, metrics.TaskID, metrics.Role, metrics.UpdatedAt = session, "lado-task-1", domain.LadoSessionWorker, at(300)
	if metrics.ToolCalls != 5 || metrics.ReadCalls != 2 || metrics.SearchCalls != 1 || metrics.EditCalls != 1 || metrics.CommandCalls != 1 ||
		metrics.Investigate != 90*time.Second {
		t.Fatalf("summarized metrics = %+v", metrics)
	}
	if err := s.UpsertLadoSessionMetrics(ctx, metrics); err != nil {
		t.Fatalf("UpsertLadoSessionMetrics: %v", err)
	}
	critic := domain.LadoSessionMetrics{
		SessionID: "critic-1", TaskID: "lado-task-1", Role: domain.LadoSessionCritic, ToolCalls: 3, SearchCalls: 3,
		FirstActivityAt: at(-50), LastActivityAt: at(-10), Investigate: 40 * time.Second, UpdatedAt: at(300),
	}
	if err := s.UpsertLadoSessionMetrics(ctx, critic); err != nil {
		t.Fatalf("UpsertLadoSessionMetrics critic: %v", err)
	}
	stored, err := s.ListLadoSessionMetrics(ctx, "lado-task-1")
	if err != nil || len(stored) != 2 {
		t.Fatalf("ListLadoSessionMetrics = %+v, %v; want two", stored, err)
	}
	metrics.Stored, critic.Stored = true, true
	for i, want := range []domain.LadoSessionMetrics{critic, metrics} {
		if !equalLadoSessionMetrics(stored[i], want) {
			t.Fatalf("stored metrics %d = %+v\nwant %+v", i, stored[i], want)
		}
	}

	// A second store replaces the first.
	metrics.EditCalls, metrics.FirstEditAt, metrics.Investigate = 2, time.Time{}, 3*time.Minute
	if err := s.UpsertLadoSessionMetrics(ctx, metrics); err != nil {
		t.Fatalf("UpsertLadoSessionMetrics again: %v", err)
	}
	if stored, _ := s.ListLadoSessionMetrics(ctx, "lado-task-1"); len(stored) != 2 || !equalLadoSessionMetrics(stored[1], metrics) {
		t.Fatalf("replaced metrics = %+v, want %+v", stored, metrics)
	}
	if others, err := s.ListLadoSessionMetrics(ctx, "lado-task-2"); err != nil || len(others) != 0 {
		t.Fatalf("metrics of another task = %v, %v", others, err)
	}

	for name, bad := range map[string]domain.LadoSessionMetrics{
		"no session":       {TaskID: "lado-task-1", Role: domain.LadoSessionWorker},
		"no task":          {SessionID: "s", Role: domain.LadoSessionWorker},
		"an unknown role":  {SessionID: "s", TaskID: "lado-task-1", Role: "orchestrator"},
		"a negative count": {SessionID: "s", TaskID: "lado-task-1", Role: domain.LadoSessionWorker, ReadCalls: -1},
		"a missing task":   {SessionID: "s", TaskID: "lado-task-missing", Role: domain.LadoSessionWorker},
	} {
		if err := s.UpsertLadoSessionMetrics(ctx, bad); err == nil {
			t.Fatalf("metrics with %s were stored", name)
		}
	}
}

func equalLadoSessionMetrics(a, b domain.LadoSessionMetrics) bool {
	return a.SessionID == b.SessionID && a.TaskID == b.TaskID && a.Role == b.Role && a.ToolCalls == b.ToolCalls &&
		a.ReadCalls == b.ReadCalls && a.SearchCalls == b.SearchCalls && a.CommandCalls == b.CommandCalls &&
		a.EditCalls == b.EditCalls && a.FirstActivityAt.Equal(b.FirstActivityAt) && a.FirstEditAt.Equal(b.FirstEditAt) &&
		a.LastActivityAt.Equal(b.LastActivityAt) && a.Investigate == b.Investigate && a.Stored == b.Stored &&
		a.UpdatedAt.Equal(b.UpdatedAt)
}
