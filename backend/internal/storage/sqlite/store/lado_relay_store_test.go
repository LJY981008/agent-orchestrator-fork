package store_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func ladoRelayFixture(id domain.LadoRelayID, task domain.LadoTaskID, worker domain.SessionID, at time.Time) domain.LadoRelay {
	return domain.LadoRelay{
		ID: id, TaskID: task, ProjectID: "mer", OrchestratorSessionID: "orchestrator-1", WorkerSessionID: worker,
		Instruction: "후속 2건을 이어서 하세요", Evidence: "그 워커에게 후속 2건 해",
		EvidenceMessageID: "msg-1", EvidenceAt: at.Add(-time.Minute), CreatedAt: at,
	}
}

// A relay is recorded as sending, read back as written and moves once to
// delivered or failed.
func TestLadoRelaysInTheStore(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	exec := mustLadoExecution(t, s, "1", ladoVerifyAt, false)
	at := ladoVerifyAt.Add(time.Hour)

	first := ladoRelayFixture("lado-relay-1", exec.TaskID, exec.SessionID, at)
	if err := s.InsertLadoRelay(ctx, first); err != nil {
		t.Fatalf("InsertLadoRelay: %v", err)
	}
	second := ladoRelayFixture("lado-relay-2", exec.TaskID, exec.SessionID, at.Add(time.Minute))
	second.WorkerRole, second.PartID = domain.LadoRoleMerge, "backend"
	second.BeyondEvidence, second.OutsidePlan = true, true
	second.OutsidePlanReasons = []string{domain.LadoRelayFlaggedReason, domain.LadoRelayPathReason("docs/x.md")}
	if err := s.InsertLadoRelay(ctx, second); err != nil {
		t.Fatalf("InsertLadoRelay second: %v", err)
	}

	got, ok, err := s.LadoRelay(ctx, second.ID)
	if err != nil || !ok {
		t.Fatalf("LadoRelay = (%v, %v)", ok, err)
	}
	want := second
	want.State = domain.LadoRelaySending
	want.EvidenceAt, want.CreatedAt = want.EvidenceAt.UTC(), want.CreatedAt.UTC()
	if !ladoRelaysEqual(got, want) {
		t.Fatalf("relay = %+v\nwant %+v", got, want)
	}
	if _, ok, err := s.LadoRelay(ctx, "lado-relay-missing"); ok || err != nil {
		t.Fatalf("unknown relay = (%v, %v), want none", ok, err)
	}

	delivered := at.Add(2 * time.Minute)
	if ok, err := s.MarkLadoRelayDelivered(ctx, first.ID, delivered); !ok || err != nil {
		t.Fatalf("MarkLadoRelayDelivered = (%v, %v)", ok, err)
	}
	if ok, err := s.MarkLadoRelayDelivered(ctx, first.ID, delivered.Add(time.Minute)); ok || err != nil {
		t.Fatalf("delivering twice = (%v, %v), want nothing written", ok, err)
	}
	if ok, err := s.MarkLadoRelayFailed(ctx, first.ID, "late"); ok || err != nil {
		t.Fatalf("failing a delivered relay = (%v, %v), want nothing written", ok, err)
	}
	if ok, err := s.MarkLadoRelayFailed(ctx, second.ID, "agent exited"); !ok || err != nil {
		t.Fatalf("MarkLadoRelayFailed = (%v, %v)", ok, err)
	}
	if ok, err := s.MarkLadoRelayDelivered(ctx, second.ID, delivered); ok || err != nil {
		t.Fatalf("delivering a failed relay = (%v, %v), want nothing written", ok, err)
	}
	if ok, err := s.MarkLadoRelayFailed(ctx, "lado-relay-missing", "x"); ok || err != nil {
		t.Fatalf("failing an unknown relay = (%v, %v)", ok, err)
	}

	relays, err := s.LadoRelaysByTask(ctx, exec.TaskID)
	if err != nil || len(relays) != 2 {
		t.Fatalf("LadoRelaysByTask = %+v, %v", relays, err)
	}
	if relays[0].ID != second.ID || relays[1].ID != first.ID {
		t.Fatalf("relays = %s, %s; want newest first", relays[0].ID, relays[1].ID)
	}
	if r := relays[0]; r.State != domain.LadoRelayFailed || r.Failure != "agent exited" || !r.DeliveredAt.IsZero() {
		t.Fatalf("failed relay = %+v", r)
	}
	if r := relays[1]; r.State != domain.LadoRelayDelivered || !r.DeliveredAt.Equal(delivered) || r.Failure != "" ||
		r.OutsidePlanReasons == nil || len(r.OutsidePlanReasons) != 0 {
		t.Fatalf("delivered relay = %+v", r)
	}
	if other, err := s.LadoRelaysByTask(ctx, "lado-task-other"); err != nil || len(other) != 0 {
		t.Fatalf("relays of another task = %+v, %v", other, err)
	}

	// The table refuses a relay of no task, an unknown role and a missing
	// field; the store refuses one without its times.
	for name, r := range map[string]domain.LadoRelay{
		"no task": ladoRelayFixture("lado-relay-3", "lado-task-missing", exec.SessionID, at),
		"unknown role": func() domain.LadoRelay {
			r := ladoRelayFixture("lado-relay-4", exec.TaskID, exec.SessionID, at)
			r.WorkerRole = "plan"
			return r
		}(),
		"no evidence": func() domain.LadoRelay {
			r := ladoRelayFixture("lado-relay-5", exec.TaskID, exec.SessionID, at)
			r.Evidence = ""
			return r
		}(),
		"no worker": ladoRelayFixture("lado-relay-6", exec.TaskID, "", at),
		"same id":   ladoRelayFixture(first.ID, exec.TaskID, exec.SessionID, at),
		"no created at": func() domain.LadoRelay {
			r := ladoRelayFixture("lado-relay-7", exec.TaskID, exec.SessionID, at)
			r.CreatedAt = time.Time{}
			return r
		}(),
		"no evidence at": func() domain.LadoRelay {
			r := ladoRelayFixture("lado-relay-8", exec.TaskID, exec.SessionID, at)
			r.EvidenceAt = time.Time{}
			return r
		}(),
	} {
		if err := s.InsertLadoRelay(ctx, r); err == nil {
			t.Fatalf("a relay with %s was recorded", name)
		}
	}
}

// ladoRelaysEqual compares relays with times by instant.
func ladoRelaysEqual(a, b domain.LadoRelay) bool {
	if !a.EvidenceAt.Equal(b.EvidenceAt) || !a.CreatedAt.Equal(b.CreatedAt) || !a.DeliveredAt.Equal(b.DeliveredAt) {
		return false
	}
	a.EvidenceAt, a.CreatedAt, a.DeliveredAt = b.EvidenceAt, b.CreatedAt, b.DeliveredAt
	return reflect.DeepEqual(a, b)
}

// setLadoSessionActivity sets a session's activity as AO's activity
// observer does.
func setLadoSessionActivity(t *testing.T, s *sqlite.Store, id domain.SessionID, state domain.ActivityState) {
	t.Helper()
	ctx := context.Background()
	rec, ok, err := s.GetSession(ctx, id)
	if err != nil || !ok {
		t.Fatalf("read session %s = (%v, %v)", id, ok, err)
	}
	rec.Activity.State = state
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("set the activity of %s: %v", id, err)
	}
}

// A relay reads every execution of a task, of every role, newest first,
// with whether each session ended and its activity; another task's do not
// show.
func TestLadoRelayExecutionsReadEveryRole(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	now := ladoQueueAt.Add(time.Hour)
	id := mustQueuedLadoTask(t, s, "1", "mer", ladoQueueAt)
	entry := mustClaimLadoQueue(t, s, now, 1, ladoTokens("t"), id)[0]
	base := strings.Repeat("a", 40)
	run := domain.LadoPartRun{
		QueueEntryID: id, TaskID: "lado-task-1", ProjectID: "mer", PlanRevisionID: "lado-plan-1", RepoDir: "/repo",
		Branch: "lado/task-1/integration", BaseSHA: base, HeadSHA: base, StartedAt: now,
	}
	parts := []domain.LadoTaskPart{
		{PartID: "a", Title: "A", Phase: 1, Steps: []int{1}, AffectedPaths: []string{"svc/**"}},
		{PartID: "b", Title: "B", Position: 1, Phase: 1, Steps: []int{2}, AffectedPaths: []string{"web/**"}},
	}
	if err := s.StartLadoPartRun(ctx, entry.LeaseToken, run, parts, ladoPartsEvent("parts.started")); err != nil {
		t.Fatalf("StartLadoPartRun: %v", err)
	}
	stored, err := s.LadoTaskParts(ctx, id)
	if err != nil || len(stored) != 2 {
		t.Fatalf("parts = %+v, %v", stored, err)
	}
	link := func(request string, exec domain.LadoExecution, part *domain.LadoTaskPart) {
		t.Helper()
		if ok, err := s.SetLadoPartSpawnRequest(ctx, id, request, now.Add(time.Hour)); !ok || err != nil {
			t.Fatalf("SetLadoPartSpawnRequest %s = %v, %v", request, ok, err)
		}
		var old domain.LadoPartState
		if part != nil {
			old = part.State
			part.State, part.Attempts, part.SessionID = domain.LadoPartRunning, 1, exec.SessionID
		}
		if err := s.LinkLadoPartExecution(ctx, request, exec, part, old); err != nil {
			t.Fatalf("link %s: %v", request, err)
		}
	}
	partA := mustLadoSession(t, s, "mer", false)
	link("lado:lado-queue-1/a/1", domain.LadoExecution{SessionID: partA.ID, QueueEntryID: id, Attempt: 1, Role: domain.LadoRolePart,
		PartID: "a", Branch: "lado/task-1/a", StartSHA: base, StartedAt: now}, &stored[0])
	partB := mustLadoSession(t, s, "mer", true)
	link("lado:lado-queue-1/b/1", domain.LadoExecution{SessionID: partB.ID, QueueEntryID: id, Attempt: 1, Role: domain.LadoRolePart,
		PartID: "b", Branch: "lado/task-1/b", StartSHA: base, StartedAt: now.Add(time.Minute)}, &stored[1])
	integration := mustLadoSession(t, s, "mer", false)
	setLadoSessionActivity(t, s, integration.ID, domain.ActivityExited)
	link("lado:lado-queue-1/integration/1", domain.LadoExecution{SessionID: integration.ID, QueueEntryID: id, Attempt: 1,
		Role: domain.LadoRoleIntegration, Branch: run.Branch, StartSHA: base, StartedAt: now.Add(2 * time.Minute)}, nil)
	other := mustLadoExecution(t, s, "2", now, false)

	got, err := s.LadoRelayExecutions(ctx, "lado-task-1")
	if err != nil {
		t.Fatalf("LadoRelayExecutions: %v", err)
	}
	type view struct {
		session    domain.SessionID
		role, part string
		terminated bool
		activity   domain.ActivityState
	}
	var views []view
	for _, e := range got {
		if e.Execution.TaskID != "lado-task-1" {
			t.Fatalf("execution %s of task %s read for lado-task-1", e.Execution.SessionID, e.Execution.TaskID)
		}
		views = append(views, view{e.Execution.SessionID, e.Execution.Role, e.Execution.PartID, e.Execution.SessionTerminated, e.SessionActivity})
	}
	want := []view{
		{integration.ID, domain.LadoRoleIntegration, "", false, domain.ActivityExited},
		{partB.ID, domain.LadoRolePart, "b", true, domain.ActivityActive},
		{partA.ID, domain.LadoRolePart, "a", false, domain.ActivityActive},
	}
	if !slices.Equal(views, want) {
		t.Fatalf("executions = %+v\nwant %+v", views, want)
	}

	single, err := s.LadoRelayExecutions(ctx, other.TaskID)
	if err != nil || len(single) != 1 || single[0].Execution.SessionID != other.SessionID || single[0].Execution.Role != domain.LadoRoleSingle {
		t.Fatalf("executions of %s = %+v, %v", other.TaskID, single, err)
	}
	if none, err := s.LadoRelayExecutions(ctx, "lado-task-missing"); err != nil || len(none) != 0 {
		t.Fatalf("executions of an unknown task = %+v, %v", none, err)
	}
}
