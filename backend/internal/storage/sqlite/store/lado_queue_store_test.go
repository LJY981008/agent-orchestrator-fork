package store_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
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
	ctx := context.Background()
	rows, err := raw.QueryContext(ctx, `SELECT id FROM lado_queue_entries`)
	if err != nil {
		t.Fatalf("list queue entries: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("list queue entries: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list queue entries: %v", err)
	}
	byID := make(map[domain.LadoQueueEntryID]gen.LadoQueueEntry, len(ids))
	for _, id := range ids {
		row, err := gen.New(raw).GetLadoQueueEntry(ctx, id)
		if err != nil {
			t.Fatalf("read queue entry %s: %v", id, err)
		}
		byID[domain.LadoQueueEntryID(id)] = row
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
	// The dispatcher's insert gates every execution's integration.
	exec.IntegrationGated = true
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
		// The PR merged a head without an integration approval.
		"lado-task-2": {queue: "lado-queue-4", session: merged.SessionID, execution: merged, state: domain.LadoTaskCompleted, reason: domain.LadoCompletedWithoutApproval},
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
	expect("dispatch settings and project caps", nil...)
}

// ladoQueueAt is when the dispatch tests approve their first plan.
var ladoQueueAt = ladoPlanAt.Add(time.Hour)

// mustQueuedLadoTask creates task "lado-task-<key>" in project for issue
// #<key>, titled "Task <key>", pins a plan for it and approves the plan at at,
// which queues entry "lado-queue-<key>".
func mustQueuedLadoTask(t *testing.T, s *sqlite.Store, key, project string, at time.Time) domain.LadoQueueEntryID {
	t.Helper()
	id := "lado-task-" + key
	task := importedLadoTask(id, domain.IssueID("github:acme/demo#"+key))
	task.ProjectID = domain.ProjectID(project)
	task.Issue.Title = "Task " + key
	mustCreateLadoTask(t, s, task)
	mustLadoPlanRequest(t, s, "req-"+key, id, ladoPlanAt)
	sum := sha256.Sum256([]byte(id))
	rev := mustPin(t, s, ladoRevision("lado-plan-"+key, id, "req-"+key, 1, hex.EncodeToString(sum[:])))
	mustQueueLadoPlan(t, s, "lado-approval-"+key, rev, at)
	return domain.LadoQueueEntryID("lado-queue-" + key)
}

func mustLadoMode(t *testing.T, s *sqlite.Store, project string, on bool) {
	t.Helper()
	if _, err := s.SetLadoMode(context.Background(), domain.ProjectID(project), on, ladoQueueAt); err != nil {
		t.Fatalf("SetLadoMode %s %v: %v", project, on, err)
	}
}

// ladoTokens hands out the lease tokens "<prefix>-1", "<prefix>-2", ...
func ladoTokens(prefix string) func() string {
	n := 0
	return func() string {
		n++
		return prefix + "-" + strconv.Itoa(n)
	}
}

// mustClaimLadoQueue claims up to limit entries at now for two minutes and
// checks that it got want, in that order.
func mustClaimLadoQueue(t *testing.T, s *sqlite.Store, now time.Time, limit int, tokens func() string, want ...domain.LadoQueueEntryID) []domain.LadoQueueEntry {
	t.Helper()
	claimed, err := s.ClaimLadoQueueEntries(context.Background(), now, now.Add(2*time.Minute), limit, nil, tokens)
	if err != nil {
		t.Fatalf("ClaimLadoQueueEntries at %s: %v", now, err)
	}
	got := make([]domain.LadoQueueEntryID, 0, len(claimed))
	for _, entry := range claimed {
		got = append(got, entry.ID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("claimed %v at %s, want %v", got, now, want)
	}
	return claimed
}

// storedLadoQueueEntry reads entry again, as its task carries it.
func storedLadoQueueEntry(t *testing.T, s *sqlite.Store, entry domain.LadoQueueEntry) domain.LadoQueueEntry {
	t.Helper()
	task, ok, err := s.LadoTask(context.Background(), entry.TaskID)
	if err != nil || !ok {
		t.Fatalf("LadoTask %s = (%v, %v)", entry.TaskID, ok, err)
	}
	if task.Queue.ID != entry.ID {
		t.Fatalf("task %s carries entry %q, want %s", entry.TaskID, task.Queue.ID, entry.ID)
	}
	return task.Queue
}

// unleased is entry without its lease.
func unleased(entry domain.LadoQueueEntry) domain.LadoQueueEntry {
	entry.LeaseToken, entry.ClaimedAt, entry.LeaseExpiresAt = "", time.Time{}, time.Time{}
	return entry
}

func countLadoExecutions(t *testing.T, raw *sql.DB) int {
	t.Helper()
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM lado_executions`).Scan(&n); err != nil {
		t.Fatalf("count executions: %v", err)
	}
	return n
}

func TestClaimLadoQueueEntriesLeasesOldestOpen(t *testing.T) {
	s, _ := openLadoQueueStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	seedProject(t, s, "zed")
	mustLadoMode(t, s, "mer", true)
	at := ladoQueueAt
	// zed's entry is the oldest, but zed has LADO mode off. A cancelled
	// task's entry is not open.
	zed := mustQueuedLadoTask(t, s, "1", "zed", at)
	mustQueuedLadoTask(t, s, "2", "mer", at.Add(time.Minute))
	if _, _, err := s.CancelLadoTask(ctx, "lado-task-2", at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	first := mustQueuedLadoTask(t, s, "3", "mer", at.Add(2*time.Minute))
	second := mustQueuedLadoTask(t, s, "4", "mer", at.Add(3*time.Minute))
	third := mustQueuedLadoTask(t, s, "5", "mer", at.Add(4*time.Minute))
	fourth := mustQueuedLadoTask(t, s, "6", "mer", at.Add(5*time.Minute))
	now := at.Add(time.Hour)
	tokens := ladoTokens("token")

	// Up to the limit, oldest first, each with its own token, counting an
	// attempt; what the claim returns is what it stored.
	claimed := mustClaimLadoQueue(t, s, now, 2, tokens, first, second)
	for i, entry := range claimed {
		if entry.LeaseToken != fmt.Sprintf("token-%d", i+1) || entry.AttemptCount != 1 || !entry.ClaimedAt.Equal(now) ||
			!entry.LeaseExpiresAt.Equal(now.Add(2*time.Minute)) || entry.State(now) != domain.LadoQueueDispatching {
			t.Fatalf("claimed entry %+v, want token-%d and attempt 1, leased at %s for two minutes", entry, i+1, now)
		}
		if stored := storedLadoQueueEntry(t, s, entry); !reflect.DeepEqual(stored, entry) {
			t.Fatalf("stored entry %+v, want what the claim returned: %+v", stored, entry)
		}
	}
	// A leased entry is not claimed twice.
	claimed = append(claimed, mustClaimLadoQueue(t, s, now, 10, tokens, third, fourth)...)
	mustClaimLadoQueue(t, s, now.Add(time.Minute), 10, tokens)

	// An entry waits until its next attempt is due. One handed back without a
	// wait is claimed again at once, and so is one whose lease expired, each
	// with a new token and attempt.
	if ok, err := s.ReleaseLadoQueueEntry(ctx, third, claimed[2].LeaseToken, now.Add(10*time.Minute), domain.LadoWaitingRetry); err != nil || !ok {
		t.Fatalf("release %s = (%v, %v)", third, ok, err)
	}
	if ok, err := s.ReleaseLadoQueueEntry(ctx, fourth, claimed[3].LeaseToken, time.Time{}, ""); err != nil || !ok {
		t.Fatalf("release %s = (%v, %v)", fourth, ok, err)
	}
	again := mustClaimLadoQueue(t, s, now.Add(time.Minute), 10, tokens, fourth)
	if again[0].AttemptCount != 2 || again[0].LeaseToken != "token-5" {
		t.Fatalf("claimed again as %+v, want attempt 2 with token-5", again[0])
	}
	// Recovery reads the leased entries, expired or not, by claim time.
	leased, err := s.LeasedLadoQueueEntries(ctx)
	if want := []domain.LadoQueueEntry{claimed[0], claimed[1], again[0]}; err != nil || !reflect.DeepEqual(leased, want) {
		t.Fatalf("LeasedLadoQueueEntries = %+v (%v), want %+v", leased, err, want)
	}
	for _, entry := range mustClaimLadoQueue(t, s, now.Add(2*time.Minute), 10, tokens, first, second) {
		if entry.AttemptCount != 2 || !entry.ClaimedAt.Equal(now.Add(2*time.Minute)) || entry.LeaseToken == claimed[0].LeaseToken || entry.LeaseToken == claimed[1].LeaseToken {
			t.Fatalf("expired lease claimed again as %+v, want attempt 2 with a new token at %s", entry, now.Add(2*time.Minute))
		}
	}

	// With LADO mode on, zed's entry is claimed. Once every lease has ended
	// and the wait is over, all open entries come in order, and the claim
	// clears the waiting reason.
	mustLadoMode(t, s, "zed", true)
	mustClaimLadoQueue(t, s, now.Add(2*time.Minute), 10, tokens, zed)
	later := now.Add(10 * time.Minute)
	attempts := map[domain.LadoQueueEntryID]int{zed: 2, first: 3, second: 3, third: 2, fourth: 3}
	for _, entry := range mustClaimLadoQueue(t, s, later, 10, tokens, zed, first, second, third, fourth) {
		if entry.AttemptCount != attempts[entry.ID] || entry.WaitingReason != "" {
			t.Fatalf("claimed %+v, want attempt %d without a waiting reason", entry, attempts[entry.ID])
		}
	}

	// A claim with no room takes nothing, and a lease must end after it
	// starts.
	if got, err := s.ClaimLadoQueueEntries(ctx, later.Add(time.Hour), later.Add(2*time.Hour), 0, nil, tokens); err != nil || len(got) != 0 {
		t.Fatalf("claim with limit 0 = (%v, %v), want nothing", got, err)
	}
	if _, err := s.ClaimLadoQueueEntries(ctx, later.Add(time.Hour), later.Add(time.Hour), 10, nil, tokens); err == nil {
		t.Fatal("a lease that ends as it starts was accepted")
	}
	// The entries of a project whose run slots are all taken are left alone.
	full, err := s.ClaimLadoQueueEntries(ctx, later.Add(time.Hour), later.Add(time.Hour+2*time.Minute), 10, []domain.ProjectID{"mer"}, tokens)
	if err != nil || len(full) != 1 || full[0].ID != zed {
		t.Fatalf("claim with mer full = %+v (%v), want zed's entry alone", full, err)
	}
	mustClaimLadoQueue(t, s, later.Add(time.Hour), 10, tokens, first, second, third, fourth)
}

func TestSetLadoQueueWaiting(t *testing.T) {
	s, raw := openLadoQueueStore(t)
	ctx := context.Background()
	for _, project := range []string{"mer", "zed", "kim"} {
		seedProject(t, s, project)
	}
	mustLadoMode(t, s, "mer", true)
	mustLadoMode(t, s, "zed", false)
	// kim has no LADO settings, so its mode is off too.
	at := ladoQueueAt
	leased := mustQueuedLadoTask(t, s, "1", "mer", at)
	review := mustQueuedLadoTask(t, s, "2", "mer", at.Add(time.Minute))
	retry := mustQueuedLadoTask(t, s, "3", "mer", at.Add(2*time.Minute))
	now := at.Add(time.Hour)
	claimed := mustClaimLadoQueue(t, s, now, 3, ladoTokens("token"), leased, review, retry)
	if ok, err := s.MarkLadoQueueEntryReviewRequired(ctx, review, claimed[1].LeaseToken, domain.LadoReviewIssueEdited); err != nil || !ok {
		t.Fatalf("mark %s review required = (%v, %v)", review, ok, err)
	}
	next := now.Add(10 * time.Minute)
	if ok, err := s.ReleaseLadoQueueEntry(ctx, retry, claimed[2].LeaseToken, next, domain.LadoWaitingRetry); err != nil || !ok {
		t.Fatalf("release %s = (%v, %v)", retry, ok, err)
	}
	open := mustQueuedLadoTask(t, s, "4", "mer", at.Add(3*time.Minute))
	zed := mustQueuedLadoTask(t, s, "5", "zed", at.Add(4*time.Minute))
	kim := mustQueuedLadoTask(t, s, "6", "kim", at.Add(5*time.Minute))
	set := func(reason string, project *domain.ProjectID, want int64) {
		t.Helper()
		if n, err := s.SetLadoQueueWaiting(ctx, reason, project); err != nil || n != want {
			t.Fatalf("SetLadoQueueWaiting(%q, %v) = (%d, %v), want %d", reason, project, n, err, want)
		}
	}
	waiting := func(want map[domain.LadoQueueEntryID]string) {
		t.Helper()
		rows := ladoQueueRows(t, raw)
		for id, reason := range want {
			if got := rows[id].WaitingReason; got != reason {
				t.Fatalf("entry %s waits for %q, want %q", id, got, reason)
			}
		}
	}

	// A reason marks the open entries no lease holds in projects with LADO
	// mode on, a waiting one too; its attempt and next attempt stay. The same
	// reason again writes nothing.
	set(domain.LadoWaitingMemoryTight, nil, 2)
	set(domain.LadoWaitingMemoryTight, nil, 0)
	waiting(map[domain.LadoQueueEntryID]string{
		open: domain.LadoWaitingMemoryTight, retry: domain.LadoWaitingMemoryTight,
		leased: "", review: "", zed: "", kim: "",
	})
	if row := ladoQueueRows(t, raw)[retry]; !row.NextAttemptAt.Valid || !row.NextAttemptAt.Time.Equal(next) || row.AttemptCount != 1 {
		t.Fatalf("waiting entry %+v, want its next attempt at %s and its attempt kept", row, next)
	}

	// The entries of projects with LADO mode off wait for that alone; the
	// other reasons leave them.
	set(domain.LadoWaitingModeOff, nil, 2)
	set(domain.LadoWaitingModeOff, nil, 0)
	set(domain.LadoWaitingSlotGlobal, nil, 2)
	waiting(map[domain.LadoQueueEntryID]string{
		open: domain.LadoWaitingSlotGlobal, retry: domain.LadoWaitingSlotGlobal,
		zed: domain.LadoWaitingModeOff, kim: domain.LadoWaitingModeOff, leased: "", review: "",
	})

	// One project's entries.
	mer, zedProject := domain.ProjectID("mer"), domain.ProjectID("zed")
	set(domain.LadoWaitingMemoryTight, &zedProject, 0)
	set(domain.LadoWaitingModeOff, &mer, 0)
	set(domain.LadoWaitingMemoryTight, &mer, 2)

	// Once zed turns LADO mode on, its entry waits for what the others do,
	// and a claim clears the reason.
	mustLadoMode(t, s, "zed", true)
	set(domain.LadoWaitingMemoryTight, nil, 1)
	waiting(map[domain.LadoQueueEntryID]string{zed: domain.LadoWaitingMemoryTight, kim: domain.LadoWaitingModeOff})
	for _, entry := range mustClaimLadoQueue(t, s, now, 10, ladoTokens("again"), open, zed) {
		if entry.WaitingReason != "" {
			t.Fatalf("claimed %+v, want no waiting reason", entry)
		}
	}
	if _, err := s.SetLadoQueueWaiting(ctx, "", nil); err == nil {
		t.Fatal("a waiting reason of nothing was accepted")
	}
}

func TestLadoQueueReleaseHoldAndMarks(t *testing.T) {
	s, _ := openLadoQueueStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	mustLadoMode(t, s, "mer", true)
	ids := make([]domain.LadoQueueEntryID, 0, 6)
	for i := 1; i <= 6; i++ {
		ids = append(ids, mustQueuedLadoTask(t, s, strconv.Itoa(i), "mer", ladoQueueAt.Add(time.Duration(i)*time.Minute)))
	}
	now := ladoQueueAt.Add(time.Hour)
	claimed := mustClaimLadoQueue(t, s, now, 6, ladoTokens("token"), ids...)
	released, held, review, blocked, failed, cancelled := claimed[0], claimed[1], claimed[2], claimed[3], claimed[4], claimed[5]
	writes := map[string]func(entry domain.LadoQueueEntry, token string) (bool, error){
		"release": func(e domain.LadoQueueEntry, token string) (bool, error) {
			return s.ReleaseLadoQueueEntry(ctx, e.ID, token, now.Add(30*time.Second), domain.LadoWaitingRetry)
		},
		"hold": func(e domain.LadoQueueEntry, token string) (bool, error) {
			return s.HoldLadoQueueEntry(ctx, e.ID, token, domain.LadoWaitingSlotProject, now.Add(15*time.Second))
		},
		"review": func(e domain.LadoQueueEntry, token string) (bool, error) {
			return s.MarkLadoQueueEntryReviewRequired(ctx, e.ID, token, domain.LadoReviewIssueEdited)
		},
		"blocked": func(e domain.LadoQueueEntry, token string) (bool, error) {
			return s.MarkLadoQueueEntryBlocked(ctx, e.ID, token, domain.LadoBlockedBaseRefMismatch)
		},
		"fail": func(e domain.LadoQueueEntry, token string) (bool, error) {
			return s.FailLadoQueueEntry(ctx, e.ID, token, "spawn failed: the agent did not start", now)
		},
		"cancel": func(e domain.LadoQueueEntry, token string) (bool, error) {
			return s.CancelLadoQueueEntry(ctx, e.ID, token, now)
		},
	}

	// A token that does not hold the entry changes nothing: none, an unknown
	// one, and another entry's.
	for name, write := range writes {
		for _, token := range []string{"", "token-9", held.LeaseToken} {
			if ok, err := write(released, token); err != nil || ok {
				t.Fatalf("%s with token %q = (%v, %v), want false", name, token, ok, err)
			}
		}
	}
	if got := storedLadoQueueEntry(t, s, released); !reflect.DeepEqual(got, released) {
		t.Fatalf("entry after writes with the wrong token = %+v, want it as claimed: %+v", got, released)
	}

	// With its token, each write clears the lease. A release keeps the attempt
	// the claim counted; a hold gives it back.
	wantReleased := unleased(released)
	wantReleased.NextAttemptAt, wantReleased.WaitingReason = now.Add(30*time.Second), domain.LadoWaitingRetry
	wantHeld := unleased(held)
	wantHeld.AttemptCount, wantHeld.NextAttemptAt, wantHeld.WaitingReason = 0, now.Add(15*time.Second), domain.LadoWaitingSlotProject
	wantReview := unleased(review)
	wantReview.ReviewRequiredReason = domain.LadoReviewIssueEdited
	wantBlocked := unleased(blocked)
	wantBlocked.BlockedReason = domain.LadoBlockedBaseRefMismatch
	wantFailed := unleased(failed)
	wantFailed.FailedAt, wantFailed.ErrorMessage = now, "spawn failed: the agent did not start"
	wantCancelled := unleased(cancelled)
	wantCancelled.CancelledAt = now
	for _, c := range []struct {
		write       string
		entry, want domain.LadoQueueEntry
		state       domain.LadoQueueState
	}{
		{"release", released, wantReleased, domain.LadoQueueWaiting},
		{"hold", held, wantHeld, domain.LadoQueueWaiting},
		{"review", review, wantReview, domain.LadoQueueReviewRequired},
		{"blocked", blocked, wantBlocked, domain.LadoQueueBlocked},
		{"fail", failed, wantFailed, domain.LadoQueueFailed},
		{"cancel", cancelled, wantCancelled, domain.LadoQueueCancelled},
	} {
		if ok, err := writes[c.write](c.entry, c.entry.LeaseToken); err != nil || !ok {
			t.Fatalf("%s = (%v, %v), want true", c.write, ok, err)
		}
		got := storedLadoQueueEntry(t, s, c.entry)
		if !reflect.DeepEqual(got, c.want) || got.State(now) != c.state {
			t.Fatalf("%s left %+v (%s), want %+v (%s)", c.write, got, got.State(now), c.want, c.state)
		}
		// The token is spent.
		for name, write := range writes {
			if ok, err := write(c.entry, c.entry.LeaseToken); err != nil || ok {
				t.Fatalf("%s after %s = (%v, %v), want false", name, c.write, ok, err)
			}
		}
		if got := storedLadoQueueEntry(t, s, c.entry); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("writes with the spent token after %s changed the entry to %+v", c.write, got)
		}
	}

	// A mark needs its reason, and a failure and a cancel their time.
	if _, err := s.MarkLadoQueueEntryReviewRequired(ctx, released.ID, "token-9", ""); err == nil {
		t.Fatal("a review mark without a reason was accepted")
	}
	if _, err := s.MarkLadoQueueEntryBlocked(ctx, released.ID, "token-9", ""); err == nil {
		t.Fatal("a blocked mark without a reason was accepted")
	}
	if _, err := s.FailLadoQueueEntry(ctx, released.ID, "token-9", "spawn failed", time.Time{}); err == nil {
		t.Fatal("a failure without a time was accepted")
	}
	if _, err := s.CancelLadoQueueEntry(ctx, released.ID, "token-9", time.Time{}); err == nil {
		t.Fatal("a cancel without a time was accepted")
	}

	// Once their waits are over, the released and the held entry are claimed
	// again; the stopped ones are not.
	again := mustClaimLadoQueue(t, s, now.Add(time.Minute), 10, ladoTokens("again"), released.ID, held.ID)
	if again[0].AttemptCount != 2 || again[1].AttemptCount != 1 {
		t.Fatalf("claimed again with attempts %d and %d, want 2 after the release and 1 after the hold", again[0].AttemptCount, again[1].AttemptCount)
	}
}

func TestLinkLadoExecutionAtomically(t *testing.T) {
	s, raw := openLadoQueueStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	mustLadoMode(t, s, "mer", true)
	first := mustQueuedLadoTask(t, s, "1", "mer", ladoQueueAt)
	now := ladoQueueAt.Add(time.Hour)
	entry := mustClaimLadoQueue(t, s, now, 1, ladoTokens("token"), first)[0]
	session := mustLadoSession(t, s, "mer", false).ID
	ahead := 3
	exec := domain.LadoExecution{
		SessionID: session, DispatchBaseSHA: strings.Repeat("d", 40), BaseCommitsAhead: &ahead,
		StartedAt: now.Add(time.Second), DeadlineAt: now.Add(8 * time.Hour),
		// The entry gives these, not the caller.
		TaskID: "lado-task-9", QueueEntryID: "lado-queue-9", PlanRevisionID: "lado-plan-9", PlanApprovalID: "lado-approval-9",
		PolicySnapshotID: "lado-snapshot-9", Attempt: 9, PlanBaseSHA: strings.Repeat("9", 40),
	}

	// Without the token that holds the entry, nothing is written.
	for _, c := range []struct {
		entry domain.LadoQueueEntryID
		token string
	}{{first, ""}, {first, "token-9"}, {"lado-queue-missing", entry.LeaseToken}} {
		if err := s.LinkLadoExecution(ctx, c.entry, c.token, exec); !errors.Is(err, domain.ErrLadoLeaseLost) {
			t.Fatalf("link %s with token %q = %v, want ErrLadoLeaseLost", c.entry, c.token, err)
		}
	}
	if n, stored := countLadoExecutions(t, raw), storedLadoQueueEntry(t, s, entry); n != 0 || !reflect.DeepEqual(stored, entry) {
		t.Fatalf("after refused links: %d executions and entry %+v, want none and the entry as claimed", n, stored)
	}

	// The execution, the entry's session and the cleared lease commit
	// together.
	if err := s.LinkLadoExecution(ctx, first, entry.LeaseToken, exec); err != nil {
		t.Fatalf("LinkLadoExecution: %v", err)
	}
	want := domain.LadoExecution{
		SessionID: session, TaskID: "lado-task-1", QueueEntryID: first, PlanRevisionID: "lado-plan-1",
		PlanApprovalID: "lado-approval-1", PolicySnapshotID: "lado-snapshot-1", Attempt: 1,
		PlanBaseSHA: strings.Repeat("c", 40), DispatchBaseSHA: exec.DispatchBaseSHA, BaseCommitsAhead: &ahead,
		StartedAt: exec.StartedAt, DeadlineAt: exec.DeadlineAt, IntegrationGated: true,
	}
	dispatched := unleased(entry)
	dispatched.DispatchedSessionID = session
	checkLinked := func(step string) {
		t.Helper()
		got, ok, err := s.LadoExecution(ctx, session)
		if err != nil || !ok || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: execution = %+v (%v, %v), want %+v", step, got, ok, err, want)
		}
		task, _, err := s.LadoTask(ctx, "lado-task-1")
		if err != nil || !reflect.DeepEqual(task.Queue, dispatched) || !reflect.DeepEqual(task.Execution, want) || task.State() != domain.LadoTaskRunning {
			t.Fatalf("%s: task queue %+v execution %+v state %q, want %+v, %+v and running", step, task.Queue, task.Execution, task.State(), dispatched, want)
		}
		if n := countLadoExecutions(t, raw); n != 1 {
			t.Fatalf("%s: %d executions, want 1", step, n)
		}
	}
	checkLinked("link")

	// Linking the session again, as recovery may, keeps the execution as it
	// is. Another session cannot take the entry.
	again := exec
	again.StartedAt, again.DispatchBaseSHA = now.Add(time.Hour), strings.Repeat("e", 40)
	if err := s.LinkLadoExecution(ctx, first, entry.LeaseToken, again); err != nil {
		t.Fatalf("linking the session again: %v", err)
	}
	other := exec
	other.SessionID = mustLadoSession(t, s, "mer", false).ID
	if err := s.LinkLadoExecution(ctx, first, entry.LeaseToken, other); !errors.Is(err, domain.ErrLadoLeaseLost) {
		t.Fatalf("linking another session = %v, want ErrLadoLeaseLost", err)
	}
	checkLinked("link again")

	// After a lease expired and another claim took the entry, only the new
	// token links it.
	second := mustQueuedLadoTask(t, s, "2", "mer", ladoQueueAt.Add(time.Minute))
	stale := mustClaimLadoQueue(t, s, now, 1, ladoTokens("stale"), second)[0]
	fresh := mustClaimLadoQueue(t, s, now.Add(2*time.Minute), 1, ladoTokens("fresh"), second)[0]
	if err := s.LinkLadoExecution(ctx, second, stale.LeaseToken, other); !errors.Is(err, domain.ErrLadoLeaseLost) {
		t.Fatalf("link with the expired claim's token = %v, want ErrLadoLeaseLost", err)
	}
	// When marking the entry fails, the execution is not kept either.
	if _, err := raw.Exec(`CREATE TRIGGER test_refuse_dispatch BEFORE UPDATE OF dispatched_session_id ON lado_queue_entries
		BEGIN SELECT RAISE(ABORT, 'dispatch refused'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkLadoExecution(ctx, second, fresh.LeaseToken, other); err == nil || errors.Is(err, domain.ErrLadoLeaseLost) {
		t.Fatalf("link with a refused dispatch = %v, want the refusal", err)
	}
	if _, ok, err := s.LadoExecution(ctx, other.SessionID); err != nil || ok || !reflect.DeepEqual(storedLadoQueueEntry(t, s, fresh), fresh) {
		t.Fatalf("after a refused dispatch: execution kept %v (%v) and entry %+v, want no execution and the entry leased", ok, err, storedLadoQueueEntry(t, s, fresh))
	}
	if _, err := raw.Exec(`DROP TRIGGER test_refuse_dispatch`); err != nil {
		t.Fatal(err)
	}
	// A session that runs another entry cannot run this one too.
	if err := s.LinkLadoExecution(ctx, second, fresh.LeaseToken, exec); err == nil || errors.Is(err, domain.ErrLadoLeaseLost) {
		t.Fatalf("linking a session that runs another entry = %v, want a refusal", err)
	}
	if err := s.LinkLadoExecution(ctx, second, fresh.LeaseToken, other); err != nil {
		t.Fatalf("link with the new token: %v", err)
	}
	if got, _, err := s.LadoExecution(ctx, other.SessionID); err != nil || got.QueueEntryID != second || got.Attempt != 2 {
		t.Fatalf("execution %+v (%v), want entry %s at attempt 2", got, err, second)
	}

	// An entry a newer approval superseded while leased still links: its
	// session exists.
	third := mustQueuedLadoTask(t, s, "3", "mer", ladoQueueAt.Add(2*time.Minute))
	leased := mustClaimLadoQueue(t, s, now, 1, ladoTokens("leased"), third)[0]
	rev, _, err := s.LadoPlanRevision(ctx, "lado-plan-3")
	if err != nil {
		t.Fatal(err)
	}
	mustQueueLadoPlan(t, s, "lado-approval-3b", rev, ladoQueueAt.Add(3*time.Hour))
	late := exec
	late.SessionID = mustLadoSession(t, s, "mer", false).ID
	if err := s.LinkLadoExecution(ctx, third, leased.LeaseToken, late); err != nil {
		t.Fatalf("link of a superseded entry: %v", err)
	}
	if row := ladoQueueRows(t, raw)[third]; !row.SupersededAt.Valid || row.DispatchedSessionID.String != string(late.SessionID) {
		t.Fatalf("superseded entry after its link = %+v, want it superseded and dispatched", row)
	}
	if n := countLadoExecutions(t, raw); n != 3 {
		t.Fatalf("%d executions, want 3", n)
	}
}

func TestLadoExecutionReadsTheSessionFacts(t *testing.T) {
	s, _ := openLadoQueueStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	mustLadoMode(t, s, "mer", true)
	id := mustQueuedLadoTask(t, s, "1", "mer", ladoQueueAt)
	running := mustQueuedLadoTask(t, s, "2", "mer", ladoQueueAt.Add(time.Minute))
	now := ladoQueueAt.Add(time.Hour)
	// The first attempt's spawn failed; the second attempt's session ran and
	// merged its PR.
	first := mustClaimLadoQueue(t, s, now, 1, ladoTokens("token"), id)[0]
	if ok, err := s.ReleaseLadoQueueEntry(ctx, id, first.LeaseToken, time.Time{}, domain.LadoWaitingRetry); err != nil || !ok {
		t.Fatalf("release = (%v, %v)", ok, err)
	}
	second := mustClaimLadoQueue(t, s, now, 1, ladoTokens("again"), id)[0]
	session := mustLadoSession(t, s, "mer", true).ID
	if err := s.LinkLadoExecution(ctx, id, second.LeaseToken, domain.LadoExecution{SessionID: session, StartedAt: now}); err != nil {
		t.Fatalf("link: %v", err)
	}
	pr := mustWriteLadoPR(t, s, session, 4, true, false, now.Add(time.Hour))
	got, ok, err := s.LadoExecution(ctx, session)
	if err != nil || !ok || got.Attempt != 2 || !got.SessionTerminated || got.PR != pr {
		t.Fatalf("LadoExecution = %+v (%v, %v), want attempt 2 of an ended session with PR %+v", got, ok, err, pr)
	}
	if got, ok, err := s.LadoExecution(ctx, "mer-unknown"); err != nil || ok {
		t.Fatalf("LadoExecution of a session that is not an execution = %+v (%v, %v), want none", got, ok, err)
	}

	// Only the execution whose session runs is live.
	claim := mustClaimLadoQueue(t, s, now, 1, ladoTokens("live"), running)[0]
	live := mustLadoSession(t, s, "mer", false).ID
	if err := s.LinkLadoExecution(ctx, running, claim.LeaseToken, domain.LadoExecution{SessionID: live, StartedAt: now}); err != nil {
		t.Fatalf("link: %v", err)
	}
	executions, err := s.LiveLadoExecutions(ctx)
	if err != nil || len(executions) != 1 || executions[0].SessionID != live || executions[0].QueueEntryID != running || executions[0].TaskID != "lado-task-2" {
		t.Fatalf("LiveLadoExecutions = %+v (%v), want the execution of %s alone", executions, err, live)
	}
}

func TestCountLiveLadoExecutions(t *testing.T) {
	s, _ := openLadoQueueStore(t)
	ctx := context.Background()
	for _, project := range []string{"mer", "zed"} {
		seedProject(t, s, project)
		mustLadoMode(t, s, project, true)
	}
	now := ladoQueueAt.Add(time.Hour)
	tokens := ladoTokens("token")
	n := 0
	// queue approves the next task, in project.
	queue := func(project string) domain.LadoQueueEntryID {
		n++
		return mustQueuedLadoTask(t, s, strconv.Itoa(n), project, ladoQueueAt.Add(time.Duration(n)*time.Minute))
	}
	// run dispatches the next task in project to session.
	run := func(project string, session domain.SessionID) {
		id := queue(project)
		entry := mustClaimLadoQueue(t, s, now, 1, tokens, id)[0]
		if err := s.LinkLadoExecution(ctx, id, entry.LeaseToken, domain.LadoExecution{SessionID: session, StartedAt: now}); err != nil {
			t.Fatalf("link %s: %v", id, err)
		}
	}
	live := mustLadoSession(t, s, "mer", false)
	run("mer", live.ID)
	run("mer", mustLadoSession(t, s, "mer", true).ID)
	// A session AO no longer has does not run.
	run("mer", "mer-gone")
	run("zed", mustLadoSession(t, s, "zed", false).ID)
	// An entry leased now takes a slot. One whose lease expired does not, nor
	// does one waiting in the queue.
	mustClaimLadoQueue(t, s, now, 1, tokens, queue("mer"))
	expired := queue("zed")
	if claimed, err := s.ClaimLadoQueueEntries(ctx, now.Add(-10*time.Minute), now.Add(-8*time.Minute), 1, nil, tokens); err != nil || len(claimed) != 1 || claimed[0].ID != expired {
		t.Fatalf("claim of %s = (%+v, %v)", expired, claimed, err)
	}
	queue("zed")

	check := func(step string, at time.Time, total int, want map[domain.ProjectID]int) {
		t.Helper()
		gotTotal, got, err := s.CountLiveLadoExecutions(ctx, at)
		if err != nil || gotTotal != total || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: CountLiveLadoExecutions = (%d, %v, %v), want (%d, %v)", step, gotTotal, got, err, total, want)
		}
	}
	check("now", now, 3, map[domain.ProjectID]int{"mer": 2, "zed": 1})
	live.IsTerminated = true
	if err := s.UpdateSession(ctx, live); err != nil {
		t.Fatalf("end session: %v", err)
	}
	check("after mer's live session ended", now, 2, map[domain.ProjectID]int{"mer": 1, "zed": 1})
	check("when mer's lease ends", now.Add(2*time.Minute), 1, map[domain.ProjectID]int{"zed": 1})
}

func TestLadoDispatchGrant(t *testing.T) {
	s, _ := openLadoQueueStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	mustLadoMode(t, s, "mer", true)
	approved := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	snapshot := ladoPolicySnapshot("lado-snapshot-1", `{"agent":"codex"}`, false)
	if _, _, err := s.ApproveLadoPlan(ctx,
		domain.LadoPlanApproval{ID: "lado-approval-1", RevisionID: approved.ID, TaskID: approved.TaskID, SHA256: approved.SHA256, ApprovedAt: ladoQueueAt},
		ladoProof("nonce-1", string(approved.ID)), ladoApprovalEvent(string(approved.ID)), snapshot, &domain.LadoQueueEntry{ID: "lado-queue-1"}); err != nil {
		t.Fatalf("ApproveLadoPlan: %v", err)
	}
	// A revision pinned since does not change the hash, which names what the
	// approval approved.
	newer := mustPin(t, s, ladoRevision("lado-plan-2", "lado-task-1", "req-1", 2, ladoSHA('b')))
	now := ladoQueueAt.Add(time.Hour)
	entry := mustClaimLadoQueue(t, s, now, 1, ladoTokens("token"), "lado-queue-1")[0]
	grant := func(id domain.LadoQueueEntryID, attempt int, at time.Time) domain.LadoDispatchGrant {
		t.Helper()
		got, err := s.LadoDispatchGrant(ctx, id, attempt, at)
		if err != nil {
			t.Fatalf("LadoDispatchGrant %s/%d: %v", id, attempt, err)
		}
		return got
	}
	hash := domain.LadoDispatchHash(approved.SHA256, snapshot.SHA256)
	active := domain.LadoDispatchGrant{ProjectID: "mer", Hash: hash, Active: true}
	inactive := domain.LadoDispatchGrant{ProjectID: "mer", Hash: hash}
	for _, c := range []struct {
		name    string
		attempt int
		at      time.Time
		want    domain.LadoDispatchGrant
	}{
		{"the leased attempt", 1, now, active},
		{"before the lease ends", 1, now.Add(time.Minute), active},
		{"another attempt", 2, now, inactive},
		{"when the lease ends", 1, entry.LeaseExpiresAt, inactive},
		{"after the lease ended", 1, now.Add(time.Hour), inactive},
	} {
		if got := grant("lado-queue-1", c.attempt, c.at); got != c.want {
			t.Fatalf("%s: grant = %+v, want %+v", c.name, got, c.want)
		}
	}
	if got := grant("lado-queue-missing", 1, now); got != (domain.LadoDispatchGrant{}) {
		t.Fatalf("grant of an unknown entry = %+v, want none", got)
	}

	// A released entry has no grant until a claim takes it again, for the
	// next attempt; a dispatched one has none.
	if ok, err := s.ReleaseLadoQueueEntry(ctx, entry.ID, entry.LeaseToken, time.Time{}, domain.LadoWaitingRetry); err != nil || !ok {
		t.Fatalf("release = (%v, %v)", ok, err)
	}
	if got := grant("lado-queue-1", 1, now); got != inactive {
		t.Fatalf("grant of a released entry = %+v, want %+v", got, inactive)
	}
	entry = mustClaimLadoQueue(t, s, now, 1, ladoTokens("again"), "lado-queue-1")[0]
	if got, old := grant("lado-queue-1", 2, now), grant("lado-queue-1", 1, now); got != active || old != inactive {
		t.Fatalf("grants after the second claim = %+v for attempt 2 and %+v for attempt 1, want %+v and %+v", got, old, active, inactive)
	}
	if err := s.LinkLadoExecution(ctx, entry.ID, entry.LeaseToken, domain.LadoExecution{SessionID: mustLadoSession(t, s, "mer", false).ID, StartedAt: now}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if got := grant("lado-queue-1", 2, now); got != inactive {
		t.Fatalf("grant of a dispatched entry = %+v, want %+v", got, inactive)
	}

	// Nor has a leased entry that a newer approval superseded or the task's
	// cancellation cancelled.
	mustQueueLadoPlan(t, s, "lado-approval-2", newer, ladoQueueAt.Add(2*time.Hour))
	mustClaimLadoQueue(t, s, now, 1, ladoTokens("newer"), "lado-queue-2")
	if got := grant("lado-queue-2", 1, now); !got.Active {
		t.Fatalf("grant of the newer entry = %+v, want an active one", got)
	}
	mustQueueLadoPlan(t, s, "lado-approval-3", newer, ladoQueueAt.Add(3*time.Hour))
	if got := grant("lado-queue-2", 1, now); got.Active {
		t.Fatalf("grant of a superseded entry = %+v, want an inactive one", got)
	}
	mustClaimLadoQueue(t, s, now, 1, ladoTokens("last"), "lado-queue-3")
	if _, _, err := s.CancelLadoTask(ctx, "lado-task-1", now); err != nil {
		t.Fatal(err)
	}
	if got := grant("lado-queue-3", 1, now); got.Active {
		t.Fatalf("grant of a cancelled entry = %+v, want an inactive one", got)
	}
}

// TestLadoEntryPolicyByClientRequest reads the ceiling and the rules text an
// execution keeps from the entry its client request id names, before any
// execution row exists.
func TestLadoEntryPolicyByClientRequest(t *testing.T) {
	s, _ := openLadoQueueStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	approve := func(approval, content string, rev domain.LadoPlanRevision, at time.Time) {
		t.Helper()
		id := domain.LadoPlanApprovalID(approval)
		snapshot := ladoPolicySnapshot("lado-snapshot-"+strings.TrimPrefix(approval, "lado-approval-"), content, false)
		if _, _, err := s.ApproveLadoPlan(ctx,
			domain.LadoPlanApproval{ID: id, RevisionID: rev.ID, TaskID: rev.TaskID, SHA256: rev.SHA256, ApprovedAt: at},
			ladoProof("nonce-"+approval, string(rev.ID)), ladoApprovalEvent(string(rev.ID)), snapshot,
			&domain.LadoQueueEntry{ID: ladoQueueEntryFor(id)}); err != nil {
			t.Fatalf("ApproveLadoPlan %s: %v", approval, err)
		}
	}
	rev := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	approve("lado-approval-1", `{"agent":{"harness":"codex","model":"","permissionMode":"accept-edits"},`+
		`"permissionCeiling":"auto","prompt":"Write commit messages in Korean."}`, rev, ladoQueueAt)
	// A newer approval supersedes the first entry, which still names its
	// snapshot: a worker it started runs under it.
	approve("lado-approval-2", `{"permissionCeiling":"accept-edits","prompt":""}`, rev, ladoQueueAt.Add(time.Hour))
	// A snapshot whose content is not an object cannot be read.
	approve("lado-approval-3", `[]`, rev, ladoQueueAt.Add(2*time.Hour))

	withRules := domain.LadoEntryPolicy{Ceiling: domain.PermissionModeAuto, Prompt: "Write commit messages in Korean."}
	for _, c := range []struct {
		id   string
		want domain.LadoEntryPolicy
	}{
		{domain.LadoClientRequestID("lado-queue-1", 1), withRules},
		{domain.LadoClientRequestID("lado-queue-1", 3), withRules},
		{domain.LadoClientRequestID("lado-queue-2", 1), domain.LadoEntryPolicy{Ceiling: domain.PermissionModeAcceptEdits}},
	} {
		got, found, err := s.LadoEntryPolicyByClientRequest(ctx, c.id)
		if err != nil || !found || got != c.want {
			t.Fatalf("policy of %q = (%+v, %v, %v), want %+v", c.id, got, found, err, c.want)
		}
	}
	for _, id := range []string{"", "lado-queue-1", "lado:lado-queue-1", "lado:lado-queue-1/0", "task-1", domain.LadoClientRequestID("lado-queue-missing", 1)} {
		got, found, err := s.LadoEntryPolicyByClientRequest(ctx, id)
		if err != nil || found || got != (domain.LadoEntryPolicy{}) {
			t.Fatalf("policy of %q = (%+v, %v, %v), want not found", id, got, found, err)
		}
	}
	if _, found, err := s.LadoEntryPolicyByClientRequest(ctx, domain.LadoClientRequestID("lado-queue-3", 1)); err == nil || found {
		t.Fatalf("policy of an unreadable snapshot = (found %v, %v), want an error", found, err)
	}
}

// ladoTimedOutEvent is the audit event of a run LADO stopped at its deadline.
func ladoTimedOutEvent(session domain.SessionID, at time.Time) domain.LadoAuditEvent {
	return domain.LadoAuditEvent{
		At: at, Actor: "lado", Action: "execution.timed_out", SubjectKind: "execution", SubjectID: string(session),
		Payload: json.RawMessage(`{"session":"` + string(session) + `"}`),
	}
}

func TestLadoExecutionsPastDeadline(t *testing.T) {
	s, raw := openLadoQueueStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	mustLadoMode(t, s, "mer", true)
	now := ladoQueueAt.Add(10 * time.Hour)
	started := now.Add(-8 * time.Hour)
	tokens := ladoTokens("token")
	n := 0
	// start dispatches the next task to a session, which can have ended, with
	// deadline.
	start := func(deadline time.Time, ended bool) domain.LadoExecution {
		t.Helper()
		n++
		id := mustQueuedLadoTask(t, s, strconv.Itoa(n), "mer", ladoQueueAt.Add(time.Duration(n)*time.Minute))
		entry := mustClaimLadoQueue(t, s, started, 1, tokens, id)[0]
		session := mustLadoSession(t, s, "mer", ended).ID
		if err := s.LinkLadoExecution(ctx, id, entry.LeaseToken, domain.LadoExecution{SessionID: session, StartedAt: started, DeadlineAt: deadline}); err != nil {
			t.Fatalf("link %s: %v", id, err)
		}
		execution, ok, err := s.LadoExecution(ctx, session)
		if err != nil || !ok {
			t.Fatalf("LadoExecution %s = (%v, %v)", session, ok, err)
		}
		return execution
	}
	overdue := start(now.Add(-time.Minute), false)
	due := start(now, false)
	start(now.Add(time.Minute), false)
	start(now.Add(-time.Minute), true)
	start(time.Time{}, false)
	marked := start(now.Add(-time.Hour), false)
	if ok, err := s.MarkLadoExecutionTimedOut(ctx, marked.SessionID, ladoTimedOutEvent(marked.SessionID, now.Add(-time.Hour))); err != nil || !ok {
		t.Fatalf("MarkLadoExecutionTimedOut = (%v, %v)", ok, err)
	}
	overdue.PR = mustWriteLadoPR(t, s, overdue.SessionID, 5, false, false, now.Add(-time.Hour))

	// Only runs whose session lives, that have a deadline at or before now
	// and were not stopped yet, by deadline, with their PR.
	got, err := s.LadoExecutionsPastDeadline(ctx, now)
	if err != nil || !reflect.DeepEqual(got, []domain.LadoExecution{overdue, due}) {
		t.Fatalf("LadoExecutionsPastDeadline = %+v (%v), want %+v", got, err, []domain.LadoExecution{overdue, due})
	}

	// A time-out keeps its first time, and the run leaves the list.
	for i, want := range []bool{true, false} {
		if ok, err := s.MarkLadoExecutionTimedOut(ctx, overdue.SessionID, ladoTimedOutEvent(overdue.SessionID, now.Add(time.Duration(i)*time.Minute))); err != nil || ok != want {
			t.Fatalf("time-out %d = (%v, %v), want %v", i+1, ok, err, want)
		}
	}
	if ok, err := s.MarkLadoExecutionTimedOut(ctx, "mer-unknown", ladoTimedOutEvent("mer-unknown", now)); err != nil || ok {
		t.Fatalf("time-out of a session that is not an execution = (%v, %v), want false", ok, err)
	}
	if _, err := s.MarkLadoExecutionTimedOut(ctx, due.SessionID, ladoTimedOutEvent(due.SessionID, time.Time{})); err == nil {
		t.Fatal("a time-out without a time was accepted")
	}
	if got, err := s.LadoExecutionsPastDeadline(ctx, now); err != nil || !reflect.DeepEqual(got, []domain.LadoExecution{due}) {
		t.Fatalf("after the time-out: %+v (%v), want %+v", got, err, []domain.LadoExecution{due})
	}
	stopped, _, err := s.LadoExecution(ctx, overdue.SessionID)
	if err != nil || !stopped.TimedOutAt.Equal(now) {
		t.Fatalf("timed-out execution = %+v (%v), want timed out at %s", stopped, err, now)
	}
	// Each time-out wrote its audit event with it, and only the time-outs did.
	rows, err := raw.Query(`SELECT subject_id, at FROM lado_audit_events WHERE action = 'execution.timed_out' ORDER BY id`)
	if err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var audited []string
	for rows.Next() {
		var subject string
		var at time.Time
		if err := rows.Scan(&subject, &at); err != nil {
			t.Fatalf("scan audit event: %v", err)
		}
		audited = append(audited, subject+"@"+at.UTC().Format(time.TimeOnly))
	}
	if want := []string{
		string(marked.SessionID) + "@" + now.Add(-time.Hour).Format(time.TimeOnly),
		string(overdue.SessionID) + "@" + now.Format(time.TimeOnly),
	}; !slices.Equal(audited, want) || rows.Err() != nil {
		t.Fatalf("time-out audit events = %v (%v), want %v", audited, rows.Err(), want)
	}
	// A run stopped at its deadline fails without a PR; one whose PR opened
	// meanwhile shows its PR's state (decision l, as changed for #15).
	task, _, err := s.LadoTask(ctx, marked.TaskID)
	if err != nil || task.State() != domain.LadoTaskFailed || task.StateReason() != domain.LadoFailedTimedOut {
		t.Fatalf("task state %q (%s) %v, want failed (timed_out)", task.State(), task.StateReason(), err)
	}
	task, _, err = s.LadoTask(ctx, overdue.TaskID)
	if err != nil || task.State() != domain.LadoTaskValidating || task.StateReason() != domain.LadoValidatingVerifying {
		t.Fatalf("task state with an open PR %q (%s) %v, want validating (verifying)", task.State(), task.StateReason(), err)
	}
}

func TestLadoDispatchSettingsDefaultsAndUpdate(t *testing.T) {
	s, raw := openLadoQueueStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	if got, err := s.LadoDispatchSettings(ctx); err != nil || got != domain.DefaultLadoDispatchSettings() {
		t.Fatalf("LadoDispatchSettings without a row = (%+v, %v), want the defaults", got, err)
	}
	countEvents := func() int {
		t.Helper()
		return countLadoRuleRows(t, raw).events
	}
	event := func(action string, at time.Time) domain.LadoAuditEvent {
		return domain.LadoAuditEvent{At: at, Actor: "api", Action: action, SubjectKind: "dispatch_settings", SubjectID: "global", Payload: json.RawMessage(`{}`)}
	}
	at := ladoQueueAt

	// Each change stores the settings and its audit event together and
	// replaces the one before.
	for i, settings := range []domain.LadoDispatchSettings{
		{MaxConcurrent: 3, MaxRunMinutes: 60, HoldOnMemoryTight: false},
		{MaxConcurrent: 1, MaxRunMinutes: 0, HoldOnMemoryTight: true},
	} {
		changedAt := at.Add(time.Duration(i) * time.Hour)
		stored, err := s.SetLadoDispatchSettings(ctx, settings, event("dispatch.settings_changed", changedAt))
		want := settings
		want.UpdatedAt = changedAt
		if err != nil || stored != want {
			t.Fatalf("SetLadoDispatchSettings %d = (%+v, %v), want %+v", i+1, stored, err, want)
		}
		if got, err := s.LadoDispatchSettings(ctx); err != nil || got != want {
			t.Fatalf("LadoDispatchSettings after change %d = (%+v, %v), want %+v", i+1, got, err, want)
		}
		if n, action := countEvents(), latestLadoAuditAction(t, raw); n != i+1 || action != "dispatch.settings_changed" {
			t.Fatalf("after change %d: %d audit events, the latest %q", i+1, n, action)
		}
	}
	current, _ := s.LadoDispatchSettings(ctx)

	// A change the table refuses, or one whose event has no time, keeps
	// neither the settings nor the event.
	for name, change := range map[string]struct {
		settings domain.LadoDispatchSettings
		at       time.Time
	}{
		"cap 9":         {domain.LadoDispatchSettings{MaxConcurrent: 9, MaxRunMinutes: 60}, at.Add(3 * time.Hour)},
		"event no time": {domain.LadoDispatchSettings{MaxConcurrent: 4, MaxRunMinutes: 60}, time.Time{}},
	} {
		if _, err := s.SetLadoDispatchSettings(ctx, change.settings, event("dispatch.settings_changed", change.at)); err == nil {
			t.Fatalf("%s: SetLadoDispatchSettings succeeded", name)
		}
		if got, _ := s.LadoDispatchSettings(ctx); got != current || countEvents() != 2 {
			t.Fatalf("%s: settings %+v with %d audit events, want %+v with 2", name, got, countEvents(), current)
		}
	}
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("VerifyLadoAuditChain = (%d, %v)", broken, err)
	}

	// A project's cap is 1 until it sets one. Setting it gives a project
	// without settings a row with LADO mode off, and the cap and the mode
	// change apart.
	capOf := func(project domain.ProjectID) int {
		t.Helper()
		n, err := s.LadoProjectMaxConcurrent(ctx, project)
		if err != nil {
			t.Fatalf("LadoProjectMaxConcurrent %s: %v", project, err)
		}
		return n
	}
	if got := capOf("mer"); got != 1 {
		t.Fatalf("cap without settings = %d, want 1", got)
	}
	capEvent := event("project.max_concurrent_changed", at.Add(4*time.Hour))
	capEvent.SubjectKind, capEvent.SubjectID = "project", "mer"
	if err := s.SetLadoProjectMaxConcurrent(ctx, "mer", 3, capEvent); err != nil {
		t.Fatalf("SetLadoProjectMaxConcurrent: %v", err)
	}
	settings, err := s.LadoProjectSettings(ctx, "mer")
	if err != nil || capOf("mer") != 3 || settings.LadoMode || !settings.UpdatedAt.Equal(capEvent.At) {
		t.Fatalf("after setting the cap: cap %d, settings %+v (%v), want 3 with LADO mode off at %s", capOf("mer"), settings, err, capEvent.At)
	}
	if n, action := countEvents(), latestLadoAuditAction(t, raw); n != 3 || action != "project.max_concurrent_changed" {
		t.Fatalf("after setting the cap: %d audit events, the latest %q", n, action)
	}
	mustLadoMode(t, s, "mer", true)
	if got := capOf("mer"); got != 3 {
		t.Fatalf("cap after turning LADO mode on = %d, want 3", got)
	}
	capEvent.At = at.Add(5 * time.Hour)
	if err := s.SetLadoProjectMaxConcurrent(ctx, "mer", 2, capEvent); err != nil {
		t.Fatalf("SetLadoProjectMaxConcurrent again: %v", err)
	}
	if settings, err := s.LadoProjectSettings(ctx, "mer"); err != nil || capOf("mer") != 2 || !settings.LadoMode || settings.MaxConcurrent != 2 {
		t.Fatalf("after changing the cap: cap %d, settings %+v (%v), want 2 with LADO mode on", capOf("mer"), settings, err)
	}
	// The table refuses a cap outside 1 to 8, and the event goes with it.
	if err := s.SetLadoProjectMaxConcurrent(ctx, "mer", 0, capEvent); err == nil {
		t.Fatal("cap 0 was accepted")
	}
	if got, n := capOf("mer"), countEvents(); got != 2 || n != 4 {
		t.Fatalf("after a refused cap: cap %d with %d audit events, want 2 with 4", got, n)
	}
	if got := capOf("zed"); got != 1 {
		t.Fatalf("another project's cap = %d, want 1", got)
	}
}

func TestListLadoQueue(t *testing.T) {
	s, _ := openLadoQueueStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	seedProject(t, s, "zed")
	mustLadoMode(t, s, "mer", true)
	at := ladoQueueAt
	now := at.Add(time.Hour)
	in := func(minutes int) time.Time { return at.Add(time.Duration(minutes) * time.Minute) }

	// In mer: 2 stays leased, 3 is dispatched, 4 failed, 5 waits for a retry
	// and 6 needs review; 1 is queued after them but enqueued first.
	for i := 2; i <= 6; i++ {
		mustQueuedLadoTask(t, s, strconv.Itoa(i), "mer", in(i))
	}
	claimed := mustClaimLadoQueue(t, s, now, 5, ladoTokens("token"), "lado-queue-2", "lado-queue-3", "lado-queue-4", "lado-queue-5", "lado-queue-6")
	if err := s.LinkLadoExecution(ctx, claimed[1].ID, claimed[1].LeaseToken, domain.LadoExecution{SessionID: mustLadoSession(t, s, "mer", false).ID, StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, write := range []func() (bool, error){
		func() (bool, error) {
			return s.FailLadoQueueEntry(ctx, claimed[2].ID, claimed[2].LeaseToken, "spawn failed", now)
		},
		func() (bool, error) {
			return s.ReleaseLadoQueueEntry(ctx, claimed[3].ID, claimed[3].LeaseToken, now.Add(time.Minute), domain.LadoWaitingRetry)
		},
		func() (bool, error) {
			return s.MarkLadoQueueEntryReviewRequired(ctx, claimed[4].ID, claimed[4].LeaseToken, domain.LadoReviewRulesChanged)
		},
	} {
		if ok, err := write(); err != nil || !ok {
			t.Fatalf("write = (%v, %v)", ok, err)
		}
	}
	mustQueuedLadoTask(t, s, "1", "mer", in(1))
	// In zed, where LADO mode is off: 7 is queued, 8 was approved again and 9
	// was cancelled.
	mustQueuedLadoTask(t, s, "7", "zed", in(7))
	mustQueuedLadoTask(t, s, "8", "zed", in(8))
	rev, _, err := s.LadoPlanRevision(ctx, "lado-plan-8")
	if err != nil {
		t.Fatal(err)
	}
	mustQueueLadoPlan(t, s, "lado-approval-8b", rev, in(10))
	mustQueuedLadoTask(t, s, "9", "zed", in(9))
	if _, _, err := s.CancelLadoTask(ctx, "lado-task-9", now); err != nil {
		t.Fatal(err)
	}

	// item is entry key's row, read back through its task.
	item := func(key, task string) domain.LadoQueueItem {
		t.Helper()
		got, _, err := s.LadoTask(ctx, domain.LadoTaskID("lado-task-"+task))
		if err != nil || got.Queue.ID != domain.LadoQueueEntryID("lado-queue-"+key) {
			t.Fatalf("task %s carries entry %q (%v), want lado-queue-%s", task, got.Queue.ID, err, key)
		}
		return domain.LadoQueueItem{Entry: got.Queue, IssueRef: domain.IssueID("github:acme/demo#" + task), Title: "Task " + task}
	}
	mer := []domain.LadoQueueItem{item("1", "1"), item("2", "2"), item("5", "5")}
	zed := []domain.LadoQueueItem{item("7", "7"), item("8b", "8")}
	states := map[domain.LadoQueueEntryID]domain.LadoQueueState{
		"lado-queue-1": domain.LadoQueueQueued, "lado-queue-2": domain.LadoQueueDispatching, "lado-queue-5": domain.LadoQueueWaiting,
		"lado-queue-7": domain.LadoQueueQueued, "lado-queue-8b": domain.LadoQueueQueued,
	}
	merID, zedID, otherID := domain.ProjectID("mer"), domain.ProjectID("zed"), domain.ProjectID("other")
	for _, c := range []struct {
		name    string
		project *domain.ProjectID
		want    []domain.LadoQueueItem
	}{
		{"every project", nil, append(slices.Clone(mer), zed...)},
		{"mer", &merID, mer},
		{"zed", &zedID, zed},
		{"a project without entries", &otherID, []domain.LadoQueueItem{}},
	} {
		got, err := s.ListLadoQueue(ctx, c.project)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Fatalf("ListLadoQueue %s = %+v (%v), want %+v", c.name, got, err, c.want)
		}
		for _, item := range got {
			if state := item.Entry.State(now); state != states[item.Entry.ID] {
				t.Fatalf("ListLadoQueue %s: %s is %s, want %s", c.name, item.Entry.ID, state, states[item.Entry.ID])
			}
		}
	}
}
