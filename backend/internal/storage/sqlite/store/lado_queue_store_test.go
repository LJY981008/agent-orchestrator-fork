package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// openLadoQueueStore returns a store and a raw connection to the same file
// that enforces foreign keys as the store does, for the dispatcher's writes:
// claims, links and time-outs.
func openLadoQueueStore(t *testing.T) (*sqlite.Store, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db")+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open raw connection: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return s, raw
}

// ladoQueueEntryFor names the queue entry of approval "lado-approval-<n>"
// "lado-queue-<n>".
func ladoQueueEntryFor(approval domain.LadoPlanApprovalID) domain.LadoQueueEntryID {
	return domain.LadoQueueEntryID("lado-queue-" + strings.TrimPrefix(string(approval), "lado-approval-"))
}

// mustQueueLadoPlan approves rev at at with snapshot "lado-snapshot-<n>" and
// queue entry "lado-queue-<n>" for approval "lado-approval-<n>".
func mustQueueLadoPlan(t *testing.T, s *sqlite.Store, approval string, rev domain.LadoPlanRevision, at time.Time) domain.LadoPlanApproval {
	t.Helper()
	id := domain.LadoPlanApprovalID(approval)
	snapshot := ladoPolicySnapshot("lado-snapshot-"+strings.TrimPrefix(approval, "lado-approval-"), `{}`, false)
	stored, _, err := s.ApproveLadoPlan(context.Background(),
		domain.LadoPlanApproval{ID: id, RevisionID: rev.ID, TaskID: rev.TaskID, SHA256: rev.SHA256, ApprovedAt: at},
		ladoProof("nonce-"+approval, string(rev.ID)), ladoApprovalEvent(string(rev.ID)), snapshot,
		&domain.LadoQueueEntry{ID: ladoQueueEntryFor(id)})
	if err != nil {
		t.Fatalf("ApproveLadoPlan %s: %v", approval, err)
	}
	return stored
}

// ladoQueueRows reads every queue entry, by id.
func ladoQueueRows(t *testing.T, raw *sql.DB) map[domain.LadoQueueEntryID]gen.LadoQueueEntry {
	t.Helper()
	rows, err := gen.New(raw).ListLadoQueueEntries(context.Background(), sql.NullString{})
	if err != nil {
		t.Fatalf("list queue entries: %v", err)
	}
	byID := make(map[domain.LadoQueueEntryID]gen.LadoQueueEntry, len(rows))
	for _, row := range rows {
		byID[domain.LadoQueueEntryID(row.ID)] = row
	}
	return byID
}

// mustLadoSession creates a worker session of project, ended or not.
func mustLadoSession(t *testing.T, s *sqlite.Store, project string, terminated bool) domain.SessionRecord {
	t.Helper()
	ctx := context.Background()
	rec, err := s.CreateSession(ctx, sampleRecord(project))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if terminated {
		rec.IsTerminated = true
		if err := s.UpdateSession(ctx, rec); err != nil {
			t.Fatalf("terminate session: %v", err)
		}
	}
	return rec
}

func mustWriteLadoPR(t *testing.T, s *sqlite.Store, session domain.SessionID, number int, merged, closed bool, at time.Time) domain.LadoExecutionPR {
	t.Helper()
	pr := domain.LadoExecutionPR{URL: fmt.Sprintf("https://github.com/acme/demo/pull/%d", number), Number: number, Merged: merged, Closed: closed}
	if err := s.WritePR(context.Background(), domain.PullRequest{
		URL: pr.URL, SessionID: session, Number: number, Merged: merged, Closed: closed, UpdatedAt: at,
	}, nil, nil); err != nil {
		t.Fatalf("write PR %d: %v", number, err)
	}
	return pr
}

// insertLadoExecution stores an execution of exec.QueueEntryID with the ids
// its entry and approval give, and returns it as task reads give it back,
// without the session's facts.
func insertLadoExecution(t *testing.T, raw *sql.DB, exec domain.LadoExecution) domain.LadoExecution {
	t.Helper()
	ctx := context.Background()
	q := gen.New(raw)
	entry, err := q.GetLadoQueueEntry(ctx, string(exec.QueueEntryID))
	if err != nil {
		t.Fatalf("read queue entry %s: %v", exec.QueueEntryID, err)
	}
	var revision string
	if err := raw.QueryRow(`SELECT revision_id FROM lado_plan_approvals WHERE id = ?`, entry.PlanApprovalID).Scan(&revision); err != nil {
		t.Fatalf("read approval %s: %v", entry.PlanApprovalID, err)
	}
	exec.TaskID = domain.LadoTaskID(entry.TaskID)
	exec.PlanRevisionID = domain.LadoPlanRevisionID(revision)
	exec.PlanApprovalID = domain.LadoPlanApprovalID(entry.PlanApprovalID)
	exec.PolicySnapshotID = domain.LadoPolicySnapshotID(entry.PolicySnapshotID)
	var ahead sql.NullInt64
	if exec.BaseCommitsAhead != nil {
		ahead = sql.NullInt64{Int64: int64(*exec.BaseCommitsAhead), Valid: true}
	}
	var deadline sql.NullTime
	if !exec.DeadlineAt.IsZero() {
		deadline = sql.NullTime{Time: exec.DeadlineAt, Valid: true}
	}
	if err := q.InsertLadoExecution(ctx, gen.InsertLadoExecutionParams{
		SessionID:        string(exec.SessionID),
		TaskID:           entry.TaskID,
		QueueEntryID:     entry.ID,
		PlanRevisionID:   revision,
		PlanApprovalID:   entry.PlanApprovalID,
		PolicySnapshotID: entry.PolicySnapshotID,
		Attempt:          int64(exec.Attempt),
		PlanBaseSha:      exec.PlanBaseSHA,
		DispatchBaseSha:  exec.DispatchBaseSHA,
		BaseCommitsAhead: ahead,
		StartedAt:        exec.StartedAt,
		DeadlineAt:       deadline,
	}); err != nil {
		t.Fatalf("insert execution of %s: %v", exec.QueueEntryID, err)
	}
	return exec
}

// dispatchLadoQueueEntry links an execution to its entry, as the dispatcher
// does: the execution row and the entry's session.
func dispatchLadoQueueEntry(t *testing.T, raw *sql.DB, exec domain.LadoExecution) domain.LadoExecution {
	t.Helper()
	exec = insertLadoExecution(t, raw, exec)
	if _, err := raw.Exec(`UPDATE lado_queue_entries SET dispatched_session_id = ? WHERE id = ?`, string(exec.SessionID), string(exec.QueueEntryID)); err != nil {
		t.Fatalf("dispatch %s: %v", exec.QueueEntryID, err)
	}
	return exec
}

func TestApproveLadoPlanWritesQueueEntry(t *testing.T) {
	s, raw := openLadoQueueStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	first := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))

	// The entry takes the ids the store wrote, whatever it carried.
	at := first.CreatedAt.Add(time.Minute)
	approval := domain.LadoPlanApproval{ID: "lado-approval-1", RevisionID: first.ID, TaskID: first.TaskID, SHA256: first.SHA256, ApprovedAt: at}
	if _, _, err := s.ApproveLadoPlan(ctx, approval, ladoProof("nonce-1", string(first.ID)), ladoApprovalEvent(string(first.ID)),
		ladoPolicySnapshot("lado-snapshot-1", `{}`, false),
		&domain.LadoQueueEntry{ID: "lado-queue-1", TaskID: "lado-task-9", ProjectID: "zed", PlanApprovalID: "lado-approval-9", PolicySnapshotID: "lado-snapshot-9", AttemptCount: 3}); err != nil {
		t.Fatalf("ApproveLadoPlan: %v", err)
	}
	task, _, err := s.LadoTask(ctx, "lado-task-1")
	if err != nil {
		t.Fatal(err)
	}
	want := domain.LadoQueueEntry{ID: "lado-queue-1", TaskID: "lado-task-1", ProjectID: "mer", PlanApprovalID: "lado-approval-1", PolicySnapshotID: "lado-snapshot-1", EnqueuedAt: at}
	if !reflect.DeepEqual(task.Queue, want) || task.State() != domain.LadoTaskQueued {
		t.Fatalf("queue entry %+v state %q, want %+v and queued", task.Queue, task.State(), want)
	}
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("VerifyLadoAuditChain = (%d, %v)", broken, err)
	}

	// A refused approval keeps no entry and supersedes nothing: neither one
	// of a superseded revision nor one whose entry cannot be stored.
	second := mustPin(t, s, ladoRevision("lado-plan-2", "lado-task-1", "req-1", 2, ladoSHA('b')))
	stale := domain.LadoPlanApproval{ID: "lado-approval-stale", RevisionID: first.ID, TaskID: first.TaskID, SHA256: first.SHA256, ApprovedAt: at.Add(time.Hour)}
	if _, _, err := s.ApproveLadoPlan(ctx, stale, ladoProof("nonce-stale", string(first.ID)), ladoApprovalEvent(string(first.ID)),
		ladoPolicySnapshot("lado-snapshot-stale", `{}`, false), &domain.LadoQueueEntry{ID: "lado-queue-stale"}); !errors.Is(err, domain.ErrLadoPlanSuperseded) {
		t.Fatalf("approving a superseded revision = %v, want ErrLadoPlanSuperseded", err)
	}
	reused := domain.LadoPlanApproval{ID: "lado-approval-reused", RevisionID: second.ID, TaskID: second.TaskID, SHA256: second.SHA256, ApprovedAt: at.Add(time.Hour)}
	if _, _, err := s.ApproveLadoPlan(ctx, reused, ladoProof("nonce-reused", string(second.ID)), ladoApprovalEvent(string(second.ID)),
		ladoPolicySnapshot("lado-snapshot-reused", `{}`, false), &domain.LadoQueueEntry{ID: "lado-queue-1"}); err == nil {
		t.Fatal("approving with an entry id in use succeeded")
	}
	rows := ladoQueueRows(t, raw)
	if len(rows) != 1 || rows["lado-queue-1"].SupersededAt.Valid {
		t.Fatalf("entries after refused approvals = %+v, want only lado-queue-1, still open", rows)
	}
	if _, ok, err := s.LadoPolicySnapshot(ctx, "lado-approval-reused"); err != nil || ok {
		t.Fatalf("snapshot of the refused approval = (%v, %v), want none", ok, err)
	}
	if approvals, _ := s.ListLadoPlanApprovals(ctx, "lado-task-1"); len(approvals) != 1 {
		t.Fatalf("approvals = %d, want only the first", len(approvals))
	}

	// A newer approval supersedes the open entry and queues its own.
	secondAt := at.Add(2 * time.Hour)
	mustQueueLadoPlan(t, s, "lado-approval-2", second, secondAt)
	rows = ladoQueueRows(t, raw)
	if old := rows["lado-queue-1"]; !old.SupersededAt.Valid || !old.SupersededAt.Time.Equal(secondAt) {
		t.Fatalf("first entry = %+v, want superseded at %s", old, secondAt)
	}
	task, _, _ = s.LadoTask(ctx, "lado-task-1")
	if task.Queue.ID != "lado-queue-2" || task.Queue.PlanApprovalID != "lado-approval-2" || task.Queue.PolicySnapshotID != "lado-snapshot-2" || task.State() != domain.LadoTaskQueued {
		t.Fatalf("task queue %+v state %q, want lado-queue-2 queued", task.Queue, task.State())
	}

	// Without a snapshot, or without an entry, an approval queues nothing,
	// yet it still supersedes the entries of older approvals.
	third := mustPin(t, s, ladoRevision("lado-plan-3", "lado-task-1", "req-1", 3, ladoSHA('c')))
	if _, _, err := s.ApproveLadoPlan(ctx, domain.LadoPlanApproval{ID: "lado-approval-3", RevisionID: third.ID, TaskID: third.TaskID, SHA256: third.SHA256, ApprovedAt: at.Add(3 * time.Hour)},
		ladoProof("nonce-3", string(third.ID)), ladoApprovalEvent(string(third.ID)), nil, &domain.LadoQueueEntry{ID: "lado-queue-3"}); err != nil {
		t.Fatalf("ApproveLadoPlan without a snapshot: %v", err)
	}
	if _, _, err := s.ApproveLadoPlan(ctx, domain.LadoPlanApproval{ID: "lado-approval-4", RevisionID: third.ID, TaskID: third.TaskID, SHA256: third.SHA256, ApprovedAt: at.Add(4 * time.Hour)},
		ladoProof("nonce-4", string(third.ID)), ladoApprovalEvent(string(third.ID)), ladoPolicySnapshot("lado-snapshot-4", `{}`, false), nil); err != nil {
		t.Fatalf("ApproveLadoPlan without an entry: %v", err)
	}
	rows = ladoQueueRows(t, raw)
	if len(rows) != 2 || !rows["lado-queue-2"].SupersededAt.Valid || !rows["lado-queue-2"].SupersededAt.Time.Equal(at.Add(3*time.Hour)) {
		t.Fatalf("entries = %+v, want the two queued ones, the second superseded by the approval without a snapshot", rows)
	}
	task, _, _ = s.LadoTask(ctx, "lado-task-1")
	if task.Queue != (domain.LadoQueueEntry{}) || task.State() != domain.LadoTaskReviewRequired || task.StateReason() != domain.LadoReviewApprovedBeforeQueue {
		t.Fatalf("task queue %+v state %q (%s), want no entry and review_required", task.Queue, task.State(), task.StateReason())
	}
}

func TestLadoTaskReadsCarryQueueAndExecution(t *testing.T) {
	s, raw := openLadoQueueStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	seedProject(t, s, "zed")
	at := ladoPlanAt.Add(time.Hour)
	newTask := func(id, project string, issue domain.IssueID, sha byte) domain.LadoPlanRevision {
		t.Helper()
		task := importedLadoTask(id, issue)
		task.ProjectID = domain.ProjectID(project)
		mustCreateLadoTask(t, s, task)
		mustLadoPlanRequest(t, s, "req-"+id, id, ladoPlanAt)
		return mustPin(t, s, ladoRevision("lado-plan-"+id, id, "req-"+id, 1, ladoSHA(sha)))
	}

	// lado-task-1 was approved twice; only the newest approval's entry counts.
	rev := newTask("lado-task-1", "mer", "github:acme/demo#1", 'a')
	mustQueueLadoPlan(t, s, "lado-approval-1", rev, at)
	mustQueueLadoPlan(t, s, "lado-approval-2", rev, at.Add(time.Minute))

	// lado-task-2's first run ended without a PR. Approved again, its second
	// run merged the newer of its two PRs.
	rev = newTask("lado-task-2", "mer", "github:acme/demo#2", 'b')
	mustQueueLadoPlan(t, s, "lado-approval-3", rev, at)
	dispatchLadoQueueEntry(t, raw, domain.LadoExecution{SessionID: mustLadoSession(t, s, "mer", true).ID, QueueEntryID: "lado-queue-3", Attempt: 1, StartedAt: at.Add(time.Minute)})
	mustQueueLadoPlan(t, s, "lado-approval-4", rev, at.Add(2*time.Hour))
	ahead := 2
	merged := dispatchLadoQueueEntry(t, raw, domain.LadoExecution{
		SessionID: mustLadoSession(t, s, "mer", false).ID, QueueEntryID: "lado-queue-4", Attempt: 2,
		PlanBaseSHA: strings.Repeat("c", 40), DispatchBaseSHA: strings.Repeat("d", 40), BaseCommitsAhead: &ahead,
		StartedAt: at.Add(3 * time.Hour), DeadlineAt: at.Add(11 * time.Hour),
	})
	mustWriteLadoPR(t, s, merged.SessionID, 6, false, true, at.Add(4*time.Hour))
	merged.PR = mustWriteLadoPR(t, s, merged.SessionID, 7, true, false, at.Add(5*time.Hour))

	// lado-task-3, in another project, ended with its PR closed unmerged;
	// lado-task-4's session ended without a PR.
	rev = newTask("lado-task-3", "zed", "github:acme/demo#3", 'c')
	mustQueueLadoPlan(t, s, "lado-approval-5", rev, at)
	closed := dispatchLadoQueueEntry(t, raw, domain.LadoExecution{SessionID: mustLadoSession(t, s, "zed", true).ID, QueueEntryID: "lado-queue-5", Attempt: 1, StartedAt: at.Add(time.Minute)})
	closed.SessionTerminated = true
	closed.PR = mustWriteLadoPR(t, s, closed.SessionID, 8, false, true, at.Add(time.Hour))
	rev = newTask("lado-task-4", "mer", "github:acme/demo#4", 'd')
	mustQueueLadoPlan(t, s, "lado-approval-6", rev, at)
	ended := dispatchLadoQueueEntry(t, raw, domain.LadoExecution{SessionID: mustLadoSession(t, s, "mer", true).ID, QueueEntryID: "lado-queue-6", Attempt: 3, StartedAt: at.Add(time.Minute)})
	ended.SessionTerminated = true

	type facts struct {
		queue     domain.LadoQueueEntryID
		session   domain.SessionID
		execution domain.LadoExecution
		state     domain.LadoTaskState
		reason    string
	}
	wants := map[domain.LadoTaskID]facts{
		"lado-task-1": {queue: "lado-queue-2", state: domain.LadoTaskQueued},
		"lado-task-2": {queue: "lado-queue-4", session: merged.SessionID, execution: merged, state: domain.LadoTaskCompleted},
		"lado-task-3": {queue: "lado-queue-5", session: closed.SessionID, execution: closed, state: domain.LadoTaskClosedUnmerged},
		"lado-task-4": {queue: "lado-queue-6", session: ended.SessionID, execution: ended, state: domain.LadoTaskFailed, reason: domain.LadoFailedEndedWithoutPR},
	}
	singles := map[domain.LadoTaskID]domain.LadoTask{}
	for id, want := range wants {
		task, ok, err := s.LadoTask(ctx, id)
		if err != nil || !ok {
			t.Fatalf("LadoTask %s: %v %v", id, ok, err)
		}
		if task.Queue.ID != want.queue || task.Queue.PlanApprovalID != task.Plan.Approval.ID || task.Queue.DispatchedSessionID != want.session {
			t.Fatalf("%s queue = %+v, want %s of approval %s dispatched to %q", id, task.Queue, want.queue, task.Plan.Approval.ID, want.session)
		}
		if !reflect.DeepEqual(task.Execution, want.execution) {
			t.Fatalf("%s execution = %+v, want %+v", id, task.Execution, want.execution)
		}
		if task.State() != want.state || task.StateReason() != want.reason {
			t.Fatalf("%s state = %q (%s), want %q (%s)", id, task.State(), task.StateReason(), want.state, want.reason)
		}
		singles[id] = task
	}

	// The list reads carry the same facts.
	var listed []domain.LadoTask
	for _, project := range []domain.ProjectID{"mer", "zed"} {
		tasks, err := s.ListLadoTasks(ctx, project, false)
		if err != nil {
			t.Fatalf("ListLadoTasks %s: %v", project, err)
		}
		listed = append(listed, tasks...)
	}
	live, err := s.ListLiveLadoTasks(ctx)
	if err != nil {
		t.Fatalf("ListLiveLadoTasks: %v", err)
	}
	for name, tasks := range map[string][]domain.LadoTask{"ListLadoTasks": listed, "ListLiveLadoTasks": live} {
		if len(tasks) != len(wants) {
			t.Fatalf("%s returned %d tasks, want %d", name, len(tasks), len(wants))
		}
		for _, task := range tasks {
			single := singles[task.ID]
			if !reflect.DeepEqual(task.Queue, single.Queue) || !reflect.DeepEqual(task.Execution, single.Execution) {
				t.Fatalf("%s %s = queue %+v execution %+v, want what LadoTask reads: %+v %+v", name, task.ID, task.Queue, task.Execution, single.Queue, single.Execution)
			}
		}
	}
	updated, _, err := s.SetLadoTaskPreferences(ctx, "lado-task-2", "codex", "", at.Add(6*time.Hour))
	if err != nil || !reflect.DeepEqual(updated.Execution, merged) || updated.State() != domain.LadoTaskCompleted {
		t.Fatalf("SetLadoTaskPreferences = execution %+v state %q (%v), want the merged run", updated.Execution, updated.State(), err)
	}
}

func TestCancelLadoTaskCancelsOpenEntries(t *testing.T) {
	s, raw := openLadoQueueStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	rev := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	at := rev.CreatedAt.Add(time.Minute)
	mustQueueLadoPlan(t, s, "lado-approval-1", rev, at)
	run := dispatchLadoQueueEntry(t, raw, domain.LadoExecution{SessionID: mustLadoSession(t, s, "mer", false).ID, QueueEntryID: "lado-queue-1", Attempt: 1, StartedAt: at.Add(time.Minute)})
	// Approved again after the run: the dispatched entry is not open.
	mustQueueLadoPlan(t, s, "lado-approval-2", rev, at.Add(time.Hour))
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-2", "github:acme/demo#13"))
	mustLadoPlanRequest(t, s, "req-2", "lado-task-2", ladoPlanAt)
	mustQueueLadoPlan(t, s, "lado-approval-3", mustPin(t, s, ladoRevision("lado-plan-2", "lado-task-2", "req-2", 1, ladoSHA('b'))), at)

	cancelAt := at.Add(2 * time.Hour)
	task, ok, err := s.CancelLadoTask(ctx, "lado-task-1", cancelAt)
	if err != nil || !ok {
		t.Fatalf("CancelLadoTask = ok %v, err %v", ok, err)
	}
	if task.State() != domain.LadoTaskCancelled || task.Queue.ID != "lado-queue-2" || !task.Queue.CancelledAt.Equal(cancelAt) || task.Execution.SessionID != run.SessionID {
		t.Fatalf("cancelled task = queue %+v execution %+v state %q, want its open entry cancelled and its run kept", task.Queue, task.Execution, task.State())
	}
	rows := ladoQueueRows(t, raw)
	if dispatched := rows["lado-queue-1"]; dispatched.CancelledAt.Valid || dispatched.DispatchedSessionID.String != string(run.SessionID) {
		t.Fatalf("dispatched entry = %+v, want it as it was", dispatched)
	}
	if other := rows["lado-queue-3"]; other.CancelledAt.Valid {
		t.Fatalf("another task's entry = %+v, want it open", other)
	}

	// Cancelling again keeps the first cancellation of the entries too.
	if _, _, err := s.CancelLadoTask(ctx, "lado-task-1", cancelAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if open := ladoQueueRows(t, raw)["lado-queue-2"]; !open.CancelledAt.Time.Equal(cancelAt) {
		t.Fatalf("entry after a second cancel = %+v, want cancelled at %s", open, cancelAt)
	}
}

func TestLadoQueueChangesPublishEvents(t *testing.T) {
	s, raw := openLadoQueueStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	rev := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	session := mustLadoSession(t, s, "mer", false)
	at := rev.CreatedAt.Add(time.Minute)

	baseline, err := s.EventsAfter(ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	if len(baseline) > 0 {
		seq = baseline[len(baseline)-1].Seq
	}
	// expect checks the change events since the last call, each written as
	// "<kind> <payload sessionId>/<change_log session id>".
	expect := func(step string, want ...string) {
		t.Helper()
		events, err := s.EventsAfter(ctx, seq, 1000)
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		var got []string
		for _, ev := range events {
			seq = ev.Seq
			var payload struct {
				Kind      string `json:"kind"`
				SessionID string `json:"sessionId"`
				TaskID    string `json:"taskId"`
				ProjectID string `json:"projectId"`
			}
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatalf("%s: payload %s: %v", step, ev.Payload, err)
			}
			if ev.Type != "session_updated" || ev.ProjectID != "mer" || payload.TaskID != "lado-task-1" || payload.ProjectID != "mer" {
				t.Fatalf("%s: event %+v payload %s, want a session_updated event of lado-task-1 in mer", step, ev, ev.Payload)
			}
			got = append(got, payload.Kind+" "+payload.SessionID+"/"+ev.SessionID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s: events %q, want %q", step, got, want)
		}
	}
	const taskChange = "lado_task /"

	mustQueueLadoPlan(t, s, "lado-approval-1", rev, at)
	expect("approval and its entry", taskChange, taskChange)

	// Each fact a task's state or reason reads publishes; the times of the
	// lease and the next attempt and the error text do not.
	set := func(column string, value any) {
		t.Helper()
		if _, err := raw.Exec(`UPDATE lado_queue_entries SET `+column+` = ? WHERE id = 'lado-queue-1'`, value); err != nil {
			t.Fatalf("set %s: %v", column, err)
		}
	}
	for _, change := range []struct {
		column string
		value  any
	}{
		{"lease_token", "token-1"},
		{"attempt_count", 1},
		{"waiting_reason", domain.LadoWaitingSlotGlobal},
		{"review_required_reason", domain.LadoReviewRulesChanged},
		{"blocked_reason", domain.LadoBlockedSnapshot},
		{"dispatched_session_id", string(session.ID)},
		{"failed_at", at},
		{"superseded_at", at},
		{"cancelled_at", at},
	} {
		set(change.column, change.value)
		expect(change.column, taskChange)
	}
	for _, change := range []struct {
		column string
		value  any
	}{
		{"next_attempt_at", at},
		{"claimed_at", at},
		{"lease_expires_at", at},
		{"error_message", "spawn failed"},
		{"waiting_reason", domain.LadoWaitingSlotGlobal},
	} {
		set(change.column, change.value)
		expect(change.column+" alone", nil...)
	}

	// An execution publishes on its session when it starts and times out.
	runChange := "lado_execution " + string(session.ID) + "/" + string(session.ID)
	insertLadoExecution(t, raw, domain.LadoExecution{SessionID: session.ID, QueueEntryID: "lado-queue-1", Attempt: 1, StartedAt: at})
	expect("execution", runChange)
	if _, err := raw.Exec(`UPDATE lado_executions SET deadline_at = ?, dispatch_base_sha = 'abc'`, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	expect("deadline and base", nil...)
	q := gen.New(raw)
	for i, want := range [][]string{{runChange}, nil} {
		if _, err := q.MarkLadoExecutionTimedOut(ctx, gen.MarkLadoExecutionTimedOutParams{TimedOutAt: sql.NullTime{Time: at.Add(time.Hour), Valid: true}, SessionID: string(session.ID)}); err != nil {
			t.Fatal(err)
		}
		expect(fmt.Sprintf("time-out %d", i+1), want...)
	}
	// change_log names only sessions that exist.
	mustQueueLadoPlan(t, s, "lado-approval-2", rev, at.Add(time.Hour))
	expect("second approval and its entry", taskChange, taskChange)
	insertLadoExecution(t, raw, domain.LadoExecution{SessionID: "mer-gone", QueueEntryID: "lado-queue-2", Attempt: 1, StartedAt: at.Add(time.Hour)})
	expect("execution of a session AO no longer has", "lado_execution mer-gone/")

	// Dispatch settings and project caps publish nothing.
	if err := q.UpsertLadoDispatchSettings(ctx, gen.UpsertLadoDispatchSettingsParams{MaxConcurrent: 3, MaxRunMinutes: 60, HoldOnMemoryTight: 0, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertLadoProjectMaxConcurrent(ctx, gen.UpsertLadoProjectMaxConcurrentParams{ProjectID: "mer", MaxConcurrent: 2, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.SetLadoProjectMaxConcurrent(ctx, gen.SetLadoProjectMaxConcurrentParams{MaxConcurrent: 4, UpdatedAt: at, ProjectID: "mer"}); err != nil {
		t.Fatal(err)
	}
	expect("dispatch settings and project caps", nil...)
}
