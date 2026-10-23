package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var ladoTaskCreatedAt = time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)

func pendingLadoTask(id string) domain.LadoTask {
	return domain.LadoTask{
		ID:        domain.LadoTaskID(id),
		ProjectID: "mer",
		Repo:      domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"},
		Agent:     "claude-code",
		Model:     "opus",
		CreatedAt: ladoTaskCreatedAt,
		UpdatedAt: ladoTaskCreatedAt,
	}
}

func observedLadoIssue(title string, state domain.NormalizedIssueState, at time.Time) domain.LadoObservedIssue {
	return domain.LadoObservedIssue{
		Title:          title,
		State:          state,
		URL:            "https://github.com/acme/demo/issues/12",
		SnapshotSHA256: "sha-" + title,
		ObservedAt:     at,
	}
}

func sameObservedLadoIssue(a, b domain.LadoObservedIssue) bool {
	return a.Title == b.Title && a.State == b.State && a.URL == b.URL &&
		a.SnapshotSHA256 == b.SnapshotSHA256 && a.ObservedAt.Equal(b.ObservedAt)
}

func importedLadoTask(id string, ref domain.IssueID) domain.LadoTask {
	task := pendingLadoTask(id)
	task.IssueRef = ref
	task.Issue = observedLadoIssue("Imported", domain.IssueOpen, ladoTaskCreatedAt)
	return task
}

func mustCreateLadoTask(t *testing.T, s *sqlite.Store, task domain.LadoTask) domain.LadoTask {
	t.Helper()
	created, err := s.CreateLadoTask(context.Background(), task)
	if err != nil {
		t.Fatalf("CreateLadoTask %s: %v", task.ID, err)
	}
	return created
}

func ladoTaskIDs(tasks []domain.LadoTask) []domain.LadoTaskID {
	ids := make([]domain.LadoTaskID, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func sameLadoTaskIDs(got []domain.LadoTask, want ...domain.LadoTaskID) bool {
	ids := ladoTaskIDs(got)
	if len(ids) != len(want) {
		return false
	}
	for i := range ids {
		if ids[i] != want[i] {
			return false
		}
	}
	return true
}

func TestCreateLadoTaskKeepsAPendingTaskOutOfTheProjectList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created := mustCreateLadoTask(t, s, pendingLadoTask("lado-task-1"))
	if created.IssueRef != "" || created.Agent != "claude-code" || created.Model != "opus" || !created.CreatedAt.Equal(ladoTaskCreatedAt) ||
		created.Repo != (domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}) {
		t.Fatalf("created = %+v, want a pending task with its preferences", created)
	}

	listed, err := s.ListLadoTasks(ctx, "mer", true)
	if err != nil {
		t.Fatalf("ListLadoTasks: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("project tasks = %v, want none while the issue is being created", ladoTaskIDs(listed))
	}
	pending, err := s.ListPendingLadoTasks(ctx)
	if err != nil {
		t.Fatalf("ListPendingLadoTasks: %v", err)
	}
	if !sameLadoTaskIDs(pending, "lado-task-1") {
		t.Fatalf("pending tasks = %v, want [lado-task-1]", ladoTaskIDs(pending))
	}
	got, ok, err := s.LadoTask(ctx, "lado-task-1")
	if err != nil || !ok {
		t.Fatalf("LadoTask = ok %v, err %v; want the pending task", ok, err)
	}
	if got.IssueRef != "" {
		t.Fatalf("pending task issue ref = %q, want none", got.IssueRef)
	}
	if _, ok, err := s.LadoTask(ctx, "missing"); err != nil || ok {
		t.Fatalf("LadoTask(missing) = ok %v, err %v; want not found", ok, err)
	}
}

func TestLinkLadoTaskIssueShowsTheTask(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustCreateLadoTask(t, s, pendingLadoTask("lado-task-1"))

	linkedAt := ladoTaskCreatedAt.Add(time.Minute)
	issue := observedLadoIssue("Add login", domain.IssueOpen, linkedAt)
	linked, err := s.LinkLadoTaskIssue(ctx, "lado-task-1", "github:acme/demo#12", issue, linkedAt)
	if err != nil {
		t.Fatalf("LinkLadoTaskIssue: %v", err)
	}
	if linked.IssueRef != "github:acme/demo#12" || !sameObservedLadoIssue(linked.Issue, issue) || !linked.UpdatedAt.Equal(linkedAt) {
		t.Fatalf("linked = %+v, want the issue ref and observed issue %+v", linked, issue)
	}
	listed, err := s.ListLadoTasks(ctx, "mer", false)
	if err != nil {
		t.Fatalf("ListLadoTasks: %v", err)
	}
	if !sameLadoTaskIDs(listed, "lado-task-1") {
		t.Fatalf("project tasks = %v, want [lado-task-1]", ladoTaskIDs(listed))
	}
	pending, err := s.ListPendingLadoTasks(ctx)
	if err != nil {
		t.Fatalf("ListPendingLadoTasks: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending tasks = %v, want none after linking", ladoTaskIDs(pending))
	}
	if _, err := s.LinkLadoTaskIssue(ctx, "lado-task-1", "github:acme/demo#13", issue, linkedAt); err == nil {
		t.Fatal("linking a task that already has an issue succeeded")
	}
}

func TestLadoIssueHasOneLiveTask(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const ref domain.IssueID = "github:acme/demo#12"
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-a", ref))

	if _, err := s.CreateLadoTask(ctx, importedLadoTask("lado-task-b", ref)); !errors.Is(err, domain.ErrLadoIssueHasTask) {
		t.Fatalf("second task for the issue: err = %v, want ErrLadoIssueHasTask", err)
	}
	mustCreateLadoTask(t, s, pendingLadoTask("lado-task-c"))
	if _, err := s.LinkLadoTaskIssue(ctx, "lado-task-c", ref, observedLadoIssue("x", domain.IssueOpen, ladoTaskCreatedAt), ladoTaskCreatedAt); !errors.Is(err, domain.ErrLadoIssueHasTask) {
		t.Fatalf("linking a second task to the issue: err = %v, want ErrLadoIssueHasTask", err)
	}

	if _, ok, err := s.CancelLadoTask(ctx, "lado-task-a", ladoTaskCreatedAt.Add(time.Hour)); err != nil || !ok {
		t.Fatalf("CancelLadoTask = ok %v, err %v", ok, err)
	}
	later := importedLadoTask("lado-task-d", ref)
	later.CreatedAt = ladoTaskCreatedAt.Add(2 * time.Hour)
	later.UpdatedAt = later.CreatedAt
	mustCreateLadoTask(t, s, later)

	live, err := s.ListLadoTasks(ctx, "mer", false)
	if err != nil {
		t.Fatalf("ListLadoTasks: %v", err)
	}
	if !sameLadoTaskIDs(live, "lado-task-d") {
		t.Fatalf("live tasks = %v, want [lado-task-d]", ladoTaskIDs(live))
	}
	all, err := s.ListLadoTasks(ctx, "mer", true)
	if err != nil {
		t.Fatalf("ListLadoTasks with cancelled: %v", err)
	}
	if !sameLadoTaskIDs(all, "lado-task-a", "lado-task-d") {
		t.Fatalf("all tasks = %v, want [lado-task-a lado-task-d]", ladoTaskIDs(all))
	}
	refs, err := s.LiveLadoTaskIssueRefs(ctx)
	if err != nil {
		t.Fatalf("LiveLadoTaskIssueRefs: %v", err)
	}
	if len(refs) != 1 || !refs[ref] {
		t.Fatalf("live issue refs = %v, want only %s", refs, ref)
	}
}

func TestObserveLadoTaskIssueRecordsTheLatestFacts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-a", "github:acme/demo#1"))
	second := importedLadoTask("lado-task-b", "github:acme/demo#2")
	second.Issue.ObservedAt = ladoTaskCreatedAt.Add(-time.Hour)
	mustCreateLadoTask(t, s, second)
	cancelled := importedLadoTask("lado-task-c", "github:acme/demo#3")
	mustCreateLadoTask(t, s, cancelled)
	if _, _, err := s.CancelLadoTask(ctx, "lado-task-c", ladoTaskCreatedAt); err != nil {
		t.Fatalf("CancelLadoTask: %v", err)
	}

	toObserve, err := s.ListLadoTasksToObserve(ctx)
	if err != nil {
		t.Fatalf("ListLadoTasksToObserve: %v", err)
	}
	if !sameLadoTaskIDs(toObserve, "lado-task-b", "lado-task-a") {
		t.Fatalf("tasks to observe = %v, want the oldest observation first and no cancelled task", ladoTaskIDs(toObserve))
	}

	observedAt := ladoTaskCreatedAt.Add(time.Hour)
	closed := observedLadoIssue("Renamed", domain.IssueDone, observedAt)
	if err := s.ObserveLadoTaskIssue(ctx, "lado-task-a", closed, observedAt); err != nil {
		t.Fatalf("ObserveLadoTaskIssue: %v", err)
	}
	got, _, err := s.LadoTask(ctx, "lado-task-a")
	if err != nil {
		t.Fatalf("LadoTask: %v", err)
	}
	if !sameObservedLadoIssue(got.Issue, closed) || got.State() != domain.LadoTaskClosedUnmerged {
		t.Fatalf("observed task = %+v, want issue %+v and state closed_unmerged", got, closed)
	}
}

func TestSetLadoTaskPreferences(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-a", "github:acme/demo#1"))

	at := ladoTaskCreatedAt.Add(time.Minute)
	updated, ok, err := s.SetLadoTaskPreferences(ctx, "lado-task-a", "codex", "gpt-5.5", at)
	if err != nil || !ok {
		t.Fatalf("SetLadoTaskPreferences = ok %v, err %v", ok, err)
	}
	if updated.Agent != "codex" || updated.Model != "gpt-5.5" || !updated.UpdatedAt.Equal(at) {
		t.Fatalf("updated = %+v, want codex / gpt-5.5", updated)
	}
	if _, ok, err := s.SetLadoTaskPreferences(ctx, "missing", "codex", "", at); err != nil || ok {
		t.Fatalf("SetLadoTaskPreferences(missing) = ok %v, err %v; want not found", ok, err)
	}
	mustCreateLadoTask(t, s, pendingLadoTask("lado-task-pending"))
	if _, ok, err := s.SetLadoTaskPreferences(ctx, "lado-task-pending", "codex", "", at); err != nil || ok {
		t.Fatalf("SetLadoTaskPreferences(pending) = ok %v, err %v; want not found", ok, err)
	}
}

func TestCancelLadoTaskKeepsTheFirstCancellation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-a", "github:acme/demo#1"))

	first := ladoTaskCreatedAt.Add(time.Hour)
	cancelled, ok, err := s.CancelLadoTask(ctx, "lado-task-a", first)
	if err != nil || !ok {
		t.Fatalf("CancelLadoTask = ok %v, err %v", ok, err)
	}
	if !cancelled.CancelledAt.Equal(first) || cancelled.State() != domain.LadoTaskCancelled {
		t.Fatalf("cancelled = %+v, want cancelled at %s", cancelled, first)
	}
	again, ok, err := s.CancelLadoTask(ctx, "lado-task-a", first.Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("second CancelLadoTask = ok %v, err %v", ok, err)
	}
	if !again.CancelledAt.Equal(first) {
		t.Fatalf("second cancel moved cancelled_at to %s, want %s", again.CancelledAt, first)
	}
	if _, ok, err := s.CancelLadoTask(ctx, "missing", first); err != nil || ok {
		t.Fatalf("CancelLadoTask(missing) = ok %v, err %v; want not found", ok, err)
	}
	mustCreateLadoTask(t, s, pendingLadoTask("lado-task-pending"))
	if _, ok, err := s.CancelLadoTask(ctx, "lado-task-pending", first); err != nil || ok {
		t.Fatalf("CancelLadoTask(pending) = ok %v, err %v; want not found", ok, err)
	}
}

func TestDeletePendingLadoTaskLeavesLinkedTasks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustCreateLadoTask(t, s, pendingLadoTask("lado-task-pending"))
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-linked", "github:acme/demo#1"))

	for _, id := range []domain.LadoTaskID{"lado-task-pending", "lado-task-linked", "missing"} {
		if err := s.DeletePendingLadoTask(ctx, id); err != nil {
			t.Fatalf("DeletePendingLadoTask(%s): %v", id, err)
		}
	}
	if _, ok, _ := s.LadoTask(ctx, "lado-task-pending"); ok {
		t.Fatal("the pending task survived deletion")
	}
	if _, ok, _ := s.LadoTask(ctx, "lado-task-linked"); !ok {
		t.Fatal("deleting pending tasks removed a linked task")
	}
}

func TestLadoTaskChangesPublishChangeEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	baseline, err := s.EventsAfter(ctx, 0, 1000)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	var seq int64
	if len(baseline) > 0 {
		seq = baseline[len(baseline)-1].Seq
	}
	expectEvents := func(step string, want int) {
		t.Helper()
		events, err := s.EventsAfter(ctx, seq, 1000)
		if err != nil {
			t.Fatalf("%s: EventsAfter: %v", step, err)
		}
		if len(events) != want {
			t.Fatalf("%s: %d change events, want %d", step, len(events), want)
		}
		for _, ev := range events {
			seq = ev.Seq
			var payload struct {
				Kind      string `json:"kind"`
				TaskID    string `json:"taskId"`
				ProjectID string `json:"projectId"`
			}
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatalf("%s: payload %s: %v", step, ev.Payload, err)
			}
			if ev.Type != "session_updated" || ev.ProjectID != "mer" || ev.SessionID != "" ||
				payload.Kind != "lado_task" || payload.TaskID != "lado-task-1" || payload.ProjectID != "mer" {
				t.Fatalf("%s: event %+v payload %+v, want a lado_task session_updated event for mer", step, ev, payload)
			}
		}
	}

	mustCreateLadoTask(t, s, pendingLadoTask("lado-task-1"))
	expectEvents("create pending", 0)

	at := ladoTaskCreatedAt.Add(time.Minute)
	issue := observedLadoIssue("Add login", domain.IssueOpen, at)
	if _, err := s.LinkLadoTaskIssue(ctx, "lado-task-1", "github:acme/demo#12", issue, at); err != nil {
		t.Fatalf("LinkLadoTaskIssue: %v", err)
	}
	expectEvents("link", 1)

	unchanged := issue
	unchanged.ObservedAt = at.Add(5 * time.Minute)
	if err := s.ObserveLadoTaskIssue(ctx, "lado-task-1", unchanged, unchanged.ObservedAt); err != nil {
		t.Fatalf("ObserveLadoTaskIssue unchanged: %v", err)
	}
	expectEvents("observe without changes", 0)

	renamed := unchanged
	renamed.Title = "Add login with SSO"
	renamed.SnapshotSHA256 = "sha-renamed"
	if err := s.ObserveLadoTaskIssue(ctx, "lado-task-1", renamed, renamed.ObservedAt); err != nil {
		t.Fatalf("ObserveLadoTaskIssue renamed: %v", err)
	}
	expectEvents("observe a renamed issue", 1)

	if _, _, err := s.SetLadoTaskPreferences(ctx, "lado-task-1", "codex", "", at); err != nil {
		t.Fatalf("SetLadoTaskPreferences: %v", err)
	}
	expectEvents("preferences", 1)

	if _, _, err := s.CancelLadoTask(ctx, "lado-task-1", at); err != nil {
		t.Fatalf("CancelLadoTask: %v", err)
	}
	expectEvents("cancel", 1)

	ghost := importedLadoTask("lado-task-ghost", "github:acme/demo#99")
	ghost.ProjectID = "ghost"
	mustCreateLadoTask(t, s, ghost)
	expectEvents("task of a project that is not registered", 0)
}

func TestListLiveLadoTasksCarriesPlanFacts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	seedProject(t, s, "zed")
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-1", "github:acme/demo#12"))
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-2", "github:acme/demo#13"))
	other := importedLadoTask("lado-task-3", "github:acme/other#1")
	// Older than mer's tasks, yet listed after them: tasks come by project.
	other.ProjectID, other.CreatedAt = "zed", ladoTaskCreatedAt.Add(-time.Hour)
	mustCreateLadoTask(t, s, other)
	mustCreateLadoTask(t, s, pendingLadoTask("lado-task-4"))
	for _, id := range []string{"lado-task-1", "lado-task-2", "lado-task-3"} {
		mustLadoPlanRequest(t, s, "req-"+id, id, ladoPlanAt)
	}
	snapshot := ladoPolicySnapshot("lado-snapshot-1", `{}`, true)
	mustApproveLadoPlan(t, s, "lado-approval-1", mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-lado-task-1", 1, ladoSHA('a'))), "nonce-1", snapshot)
	mustPin(t, s, ladoRevision("lado-plan-2", "lado-task-2", "req-lado-task-2", 1, ladoSHA('b')))
	mustPin(t, s, ladoRevision("lado-plan-3", "lado-task-3", "req-lado-task-3", 1, ladoSHA('c')))
	if _, _, err := s.CancelLadoTask(ctx, "lado-task-2", ladoPlanAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	live, err := s.ListLiveLadoTasks(ctx)
	if err != nil {
		t.Fatalf("ListLiveLadoTasks: %v", err)
	}
	if got := ladoTaskIDs(live); !reflect.DeepEqual(got, []domain.LadoTaskID{"lado-task-1", "lado-task-3"}) {
		t.Fatalf("ListLiveLadoTasks = %v, want the live tasks with an issue, by project", got)
	}
	for _, task := range live {
		single, ok, err := s.LadoTask(ctx, task.ID)
		if err != nil || !ok {
			t.Fatalf("LadoTask %s: %v %v", task.ID, ok, err)
		}
		if !reflect.DeepEqual(task.Plan, single.Plan) {
			t.Fatalf("%s plan facts = %+v, want what LadoTask reads: %+v", task.ID, task.Plan, single.Plan)
		}
	}
	approved := live[0]
	if approved.Plan.Latest.ID != "lado-plan-1" || approved.Plan.Approval.ID != "lado-approval-1" || approved.State() != domain.LadoTaskBlocked ||
		approved.Plan.Snapshot != (domain.LadoPolicySnapshotRef{ID: snapshot.ID, SHA256: snapshot.SHA256, Blocking: true}) {
		t.Fatalf("approved task plan = %+v state %q, want its revision, approval and blocking snapshot", approved.Plan, approved.State())
	}
	planned := live[1]
	if planned.ProjectID != "zed" || planned.Plan.Latest.ID != "lado-plan-3" || planned.Plan.Approval.ID != "" ||
		planned.Plan.Snapshot != (domain.LadoPolicySnapshotRef{}) || planned.State() != domain.LadoTaskPlanned {
		t.Fatalf("planned task = %+v state %q, want zed's revision without an approval", planned, planned.State())
	}
}
