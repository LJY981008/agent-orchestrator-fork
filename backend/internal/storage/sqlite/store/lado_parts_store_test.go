package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func ladoPartsEvent(action string) domain.LadoAuditEvent {
	return domain.LadoAuditEvent{At: ladoQueueAt, Actor: "lado", Action: action, SubjectKind: "queue_entry", SubjectID: "lado-queue-1"}
}

// A parts run in the store: starting it takes the claimed entry out of the
// queue; a part worker's start is granted and linked only under its spawn
// request; part executions take worker slots but never become the task's
// pull request worker nor reach verification; a merge moves the recorded
// head only from the head it read; and the integration worker dispatches
// the entry and is the task's one pull request worker.
func TestLadoPartRunsInTheStore(t *testing.T) {
	s, raw := openLadoQueueStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	mustLadoMode(t, s, "mer", true)
	now := ladoQueueAt.Add(time.Hour)
	id := mustQueuedLadoTask(t, s, "1", "mer", ladoQueueAt)
	entry := mustClaimLadoQueue(t, s, now, 1, ladoTokens("t"), id)[0]
	base, merged := strings.Repeat("a", 40), strings.Repeat("b", 40)
	run := domain.LadoPartRun{
		QueueEntryID: id, TaskID: "lado-task-1", ProjectID: "mer", PlanRevisionID: "lado-plan-1", RepoDir: "/repo",
		Branch: "lado/task-1/integration", BaseSHA: base, HeadSHA: base, StartedAt: now,
	}
	parts := []domain.LadoTaskPart{
		{PartID: "a", Title: "A", Phase: 1, Steps: []int{1}, AffectedPaths: []string{"svc/**"}},
		{PartID: "b", Title: "B", Position: 1, Phase: 2, Steps: []int{2}, AffectedPaths: []string{"web/**"}, DependsOn: []string{"a"}, WaitingReason: domain.LadoWaitingPartDependency},
	}
	if err := s.StartLadoPartRun(ctx, "another-token", run, parts, ladoPartsEvent("parts.started")); !errors.Is(err, domain.ErrLadoLeaseLost) {
		t.Fatalf("StartLadoPartRun with another token = %v, want ErrLadoLeaseLost", err)
	}
	if err := s.StartLadoPartRun(ctx, entry.LeaseToken, run, parts, ladoPartsEvent("parts.started")); err != nil {
		t.Fatalf("StartLadoPartRun: %v", err)
	}
	task, _, _ := s.LadoTask(ctx, "lado-task-1")
	if task.Queue.PartsStartedAt.IsZero() || task.Queue.LeaseToken != "" || task.StateAt(now) != domain.LadoTaskRunning ||
		task.Parts != (domain.LadoTaskPartsSummary{Total: 2}) {
		t.Fatalf("task = %s, entry %+v, parts %+v; want running 0/2", task.StateAt(now), task.Queue, task.Parts)
	}
	mustClaimLadoQueue(t, s, now.Add(time.Hour), 1, ladoTokens("u"))
	stored, err := s.LadoTaskParts(ctx, id)
	if err != nil || len(stored) != 2 || stored[1].DependsOn[0] != "a" || stored[1].WaitingReason != domain.LadoWaitingPartDependency {
		t.Fatalf("parts = %+v, %v", stored, err)
	}

	request := "lado:lado-queue-1/a/1"
	if ok, err := s.SetLadoPartSpawnRequest(ctx, id, request, now.Add(2*time.Minute)); !ok || err != nil {
		t.Fatalf("SetLadoPartSpawnRequest = %v, %v", ok, err)
	}
	if ok, _ := s.SetLadoPartSpawnRequest(ctx, id, "lado:lado-queue-1/b/1", now.Add(2*time.Minute)); ok {
		t.Fatal("a second start took the entry while another was in progress")
	}
	for _, tc := range []struct {
		id     string
		at     time.Time
		active bool
	}{{request, now, true}, {"lado:lado-queue-1/b/1", now, false}, {request, now.Add(3 * time.Minute), false}, {"lado:lado-queue-1/1", now, false}} {
		grant, err := s.LadoPartSpawnGrant(ctx, tc.id, tc.at)
		if err != nil || grant.Active != tc.active || (tc.active && grant.ProjectID != "mer") {
			t.Fatalf("grant of %s at %s = %+v, %v; want active %v", tc.id, tc.at, grant, err, tc.active)
		}
	}
	session := mustLadoSession(t, s, "mer", false)
	partA := stored[0]
	partA.State, partA.Attempts, partA.SessionID, partA.Branch, partA.StartSHA = domain.LadoPartRunning, 1, session.ID, "lado/task-1/a", base
	exec := domain.LadoExecution{SessionID: session.ID, QueueEntryID: id, Attempt: 1, Role: domain.LadoRolePart, PartID: "a", Branch: "lado/task-1/a", StartSHA: base, StartedAt: now}
	if err := s.LinkLadoPartExecution(ctx, "lado:lado-queue-1/b/1", exec, &partA, domain.LadoPartWaiting); !errors.Is(err, domain.ErrLadoLeaseLost) {
		t.Fatalf("link under another request = %v, want ErrLadoLeaseLost", err)
	}
	if err := s.LinkLadoPartExecution(ctx, request, exec, &partA, domain.LadoPartWaiting); err != nil {
		t.Fatalf("LinkLadoPartExecution: %v", err)
	}
	if err := s.LinkLadoPartExecution(ctx, request, exec, &partA, domain.LadoPartWaiting); err != nil {
		t.Fatalf("linking again = %v, want nil", err)
	}
	if requests, _ := s.LadoPartSpawnRequests(ctx); len(requests) != 0 {
		t.Fatalf("spawn requests after the link = %+v, want none", requests)
	}
	load, err := s.LadoRunLoad(ctx, now)
	if err != nil || load.TaskCount() != 1 || load.Workers != 1 || !load.Runs("lado-task-1") {
		t.Fatalf("load = %+v, %v; want one task with one worker", load, err)
	}
	task, _, _ = s.LadoTask(ctx, "lado-task-1")
	if task.Execution.SessionID != "" {
		t.Fatalf("task execution = %+v, want none: a part worker opens no pull request", task.Execution)
	}
	if err := s.WritePR(ctx, domain.PullRequest{URL: "https://github.com/acme/demo/pull/9", SessionID: session.ID, Number: 9, HeadSHA: merged, UpdatedAt: now}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if toVerify, err := s.ListLadoExecutionsToVerify(ctx, 10); err != nil || len(toVerify) != 0 {
		t.Fatalf("executions to verify = %+v, %v; want a part worker's PR ignored", toVerify, err)
	}

	partA.State, partA.MergedSHA = domain.LadoPartMerged, merged
	if ok, err := s.MergeLadoTaskPart(ctx, partA, domain.LadoPartRunning, merged, ladoPartsEvent("part.merged")); ok || err != nil {
		t.Fatalf("merge from a head the run no longer has = %v, %v; want nothing written", ok, err)
	}
	if ok, err := s.MergeLadoTaskPart(ctx, partA, domain.LadoPartRunning, base, ladoPartsEvent("part.merged")); !ok || err != nil {
		t.Fatalf("MergeLadoTaskPart = %v, %v", ok, err)
	}
	if got, _, _ := s.LadoPartRun(ctx, id); got.HeadSHA != merged {
		t.Fatalf("run head = %s, want %s", got.HeadSHA, merged)
	}

	integration := "lado:lado-queue-1/integration/1"
	if ok, _ := s.SetLadoPartSpawnRequest(ctx, id, integration, now.Add(2*time.Minute)); !ok {
		t.Fatal("the integration start was refused")
	}
	worker := mustLadoSession(t, s, "mer", false)
	if err := s.LinkLadoPartExecution(ctx, integration, domain.LadoExecution{SessionID: worker.ID, QueueEntryID: id, Attempt: 1, Role: domain.LadoRoleIntegration,
		Branch: run.Branch, StartSHA: merged, DispatchBaseSHA: base, StartedAt: now.Add(time.Minute)}, nil, ""); err != nil {
		t.Fatalf("link the integration worker: %v", err)
	}
	task, _, _ = s.LadoTask(ctx, "lado-task-1")
	if task.Execution.SessionID != worker.ID || task.Queue.DispatchedSessionID != worker.ID || task.Execution.DispatchBaseSHA != base {
		t.Fatalf("task execution %+v entry %+v, want the integration worker", task.Execution, task.Queue)
	}
	if runs, _ := s.ActiveLadoPartRuns(ctx); len(runs) != 0 {
		t.Fatalf("active runs = %+v, want none once the integration worker started", runs)
	}
	if failed, _ := s.FailLadoPartRun(ctx, id, "late", ladoPartsEvent("part.failed")); failed {
		t.Fatal("a dispatched run was failed")
	}
	if _, err := raw.Exec(`INSERT INTO lado_executions (session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id, attempt, started_at, role)
		SELECT 'second', task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id, 2, started_at, '' FROM lado_executions WHERE session_id = ?`, string(worker.ID)); err == nil {
		t.Fatal("the entry took a second pull request worker")
	}
}

// A run fails once, and a run its task no longer wants ends as superseded or
// cancelled; neither takes a task slot after.
func TestLadoPartRunsFailAndEnd(t *testing.T) {
	s, _ := openLadoQueueStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	mustLadoMode(t, s, "mer", true)
	now := ladoQueueAt.Add(time.Hour)
	tokens := ladoTokens("t")
	start := func(key string) domain.LadoQueueEntryID {
		id := mustQueuedLadoTask(t, s, key, "mer", ladoQueueAt)
		entry := mustClaimLadoQueue(t, s, now, 1, tokens, id)[0]
		sha := strings.Repeat("a", 40)
		if err := s.StartLadoPartRun(ctx, entry.LeaseToken, domain.LadoPartRun{
			QueueEntryID: id, TaskID: entry.TaskID, ProjectID: "mer", PlanRevisionID: domain.LadoPlanRevisionID("lado-plan-" + key), RepoDir: "/repo",
			Branch: "lado/x/integration", BaseSHA: sha, HeadSHA: sha, StartedAt: now,
		}, []domain.LadoTaskPart{{PartID: "a"}, {PartID: "b", Position: 1}}, ladoPartsEvent("parts.started")); err != nil {
			t.Fatalf("start %s: %v", key, err)
		}
		return id
	}
	failing, superseded, cancelled := start("1"), start("2"), start("3")
	if load, _ := s.LadoRunLoad(ctx, now); load.TaskCount() != 3 || load.Workers != 0 {
		t.Fatalf("load = %+v, want three tasks without workers", load)
	}
	if failed, err := s.FailLadoPartRun(ctx, failing, "part a: gave up", ladoPartsEvent("part.failed")); !failed || err != nil {
		t.Fatalf("FailLadoPartRun = %v, %v", failed, err)
	}
	if failed, _ := s.FailLadoPartRun(ctx, failing, "again", ladoPartsEvent("part.failed")); failed {
		t.Fatal("a run failed twice")
	}
	if ended, err := s.EndLadoPartRun(ctx, superseded, true, now); !ended || err != nil {
		t.Fatalf("supersede = %v, %v", ended, err)
	}
	if ended, err := s.EndLadoPartRun(ctx, cancelled, false, now); !ended || err != nil {
		t.Fatalf("cancel = %v, %v", ended, err)
	}
	if load, _ := s.LadoRunLoad(ctx, now); load.TaskCount() != 0 {
		t.Fatalf("load = %+v, want no task left running", load)
	}
	task, _, _ := s.LadoTask(ctx, "lado-task-1")
	if task.StateAt(now) != domain.LadoTaskFailed || task.Queue.ErrorMessage != "part a: gave up" {
		t.Fatalf("failed task = %s, %q", task.StateAt(now), task.Queue.ErrorMessage)
	}
}
