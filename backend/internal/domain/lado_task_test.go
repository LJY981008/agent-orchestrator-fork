package domain_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/trackerintake"
)

func TestLadoIssueRefRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   domain.TrackerID
		ref  domain.IssueID
	}{
		{"github", domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "acme/demo#12"}, "github:acme/demo#12"},
		{"gitlab.com", domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project#7"}, "gitlab:group/project#7"},
		{"nested group", domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/sub/project#7"}, "gitlab:group/sub/project#7"},
		{"self-managed", domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project#7", Host: "gitlab.example.com"}, "gitlab:group/project#7@gitlab.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.LadoIssueRef(tc.id); got != tc.ref {
				t.Fatalf("LadoIssueRef = %q, want %q", got, tc.ref)
			}
			// AO stores this form in sessions.issue_id; LADO must not drift from it.
			if got := trackerintake.CanonicalIssueID(tc.id); got != tc.ref {
				t.Fatalf("AO's CanonicalIssueID = %q, want %q", got, tc.ref)
			}
			parsed, err := domain.ParseLadoIssueRef(tc.ref)
			if err != nil {
				t.Fatalf("ParseLadoIssueRef: %v", err)
			}
			if parsed != tc.id {
				t.Fatalf("ParseLadoIssueRef = %+v, want %+v", parsed, tc.id)
			}
		})
	}
}

func TestParseLadoIssueRefRejectsMalformedRefs(t *testing.T) {
	for _, ref := range []domain.IssueID{
		"",
		" github:acme/demo#12",
		"acme/demo#12",
		"github:",
		"github:acme/demo",
		"github:acme/demo#0",
		"github:acme/demo#x",
		"github:acme#12",
		"github:acme/demo/extra#12",
		"github:acme/demo#12@ghe.example.com",
		"gitlab:project#7",
		"gitlab:group/project#7@",
		"jira:ABC/DEF#2",
	} {
		if _, err := domain.ParseLadoIssueRef(ref); err == nil {
			t.Errorf("ParseLadoIssueRef(%q) = nil error, want an error", ref)
		}
	}
}

func TestLadoIssueNumber(t *testing.T) {
	if got := domain.LadoIssueNumber(domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/sub/project#41"}); got != 41 {
		t.Fatalf("LadoIssueNumber = %d, want 41", got)
	}
	if got := domain.LadoIssueNumber(domain.TrackerID{Native: "acme/demo"}); got != 0 {
		t.Fatalf("LadoIssueNumber without a number = %d, want 0", got)
	}
}

func TestLadoIssueSnapshotIsCanonicalJSON(t *testing.T) {
	snapshot := domain.LadoIssueSnapshot{
		ID:    "github:acme/demo#12",
		Title: "Add <b>bold</b> & more",
		Body:  "line 1\r\nline 2\n<!-- lado-task:lado-task-1 -->",
	}
	want := `{"id":"github:acme/demo#12","title":"Add <b>bold</b> & more","body":"line 1\r\nline 2\n<!-- lado-task:lado-task-1 -->"}`
	if got := string(snapshot.CanonicalJSON()); got != want {
		t.Fatalf("CanonicalJSON = %s\nwant %s", got, want)
	}
	sum := sha256.Sum256([]byte(want))
	if got := snapshot.SHA256(); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("SHA256 = %s, want the sha256 of the canonical JSON", got)
	}
	for name, edit := range map[string]func(*domain.LadoIssueSnapshot){
		"id":    func(s *domain.LadoIssueSnapshot) { s.ID = "github:acme/demo#13" },
		"title": func(s *domain.LadoIssueSnapshot) { s.Title += "!" },
		"body":  func(s *domain.LadoIssueSnapshot) { s.Body += " " },
	} {
		edited := snapshot
		edit(&edited)
		if edited.SHA256() == snapshot.SHA256() {
			t.Errorf("editing the %s kept the snapshot hash", name)
		}
	}
}

func TestSplitLadoTaskText(t *testing.T) {
	for _, tc := range []struct {
		name, text, title, body string
	}{
		{"title and body", "Add login\nDetails here\nMore", "Add login", "Details here\nMore"},
		{"crlf and blank lines", "  Add login  \r\n\r\n\r\nBody line\r\n  indented\r\n\r\n", "Add login", "Body line\n  indented"},
		{"leading blank lines", "\n\n  First real line\nrest", "First real line", "rest"},
		{"title only", "Only a title", "Only a title", ""},
		{"blank", "   \n \n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			title, body := domain.SplitLadoTaskText(tc.text)
			if title != tc.title || body != tc.body {
				t.Fatalf("SplitLadoTaskText = (%q, %q), want (%q, %q)", title, body, tc.title, tc.body)
			}
		})
	}
}

func TestLadoTaskMarker(t *testing.T) {
	if got := domain.LadoTaskMarker("lado-task-1"); got != "<!-- lado-task:lado-task-1 -->" {
		t.Fatalf("LadoTaskMarker = %q", got)
	}
}

func TestLadoTaskStatePrecedence(t *testing.T) {
	cancelledAt := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	task := domain.LadoTask{IssueRef: "github:acme/demo#1", Issue: domain.LadoObservedIssue{State: domain.IssueOpen}}
	for _, tc := range []struct {
		name      string
		issue     domain.NormalizedIssueState
		cancelled bool
		want      domain.LadoTaskState
	}{
		{"open issue", domain.IssueOpen, false, domain.LadoTaskDraft},
		{"issue labelled in progress", domain.IssueInProgress, false, domain.LadoTaskDraft},
		{"issue closed", domain.IssueDone, false, domain.LadoTaskClosedUnmerged},
		{"issue closed as not planned", domain.IssueCancelled, false, domain.LadoTaskClosedUnmerged},
		{"cancelled task with a closed issue", domain.IssueDone, true, domain.LadoTaskCancelled},
		{"cancelled task with an open issue", domain.IssueOpen, true, domain.LadoTaskCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := task
			got.Issue.State = tc.issue
			if tc.cancelled {
				got.CancelledAt = cancelledAt
			}
			if state := got.State(); state != tc.want {
				t.Fatalf("State = %q, want %q", state, tc.want)
			}
		})
	}
}

func TestLadoTaskStateFollowsRuleSnapshots(t *testing.T) {
	rev1 := domain.LadoPlanRevision{ID: "lado-plan-1", RequestID: "req-1", SHA256: strings.Repeat("a", 64)}
	rev2 := domain.LadoPlanRevision{ID: "lado-plan-2", RequestID: "req-1", SHA256: strings.Repeat("b", 64)}
	approval := domain.LadoPlanApproval{ID: "lado-approval-1", RevisionID: rev1.ID, SHA256: rev1.SHA256}
	rulesThen, rulesNow := strings.Repeat("c", 64), strings.Repeat("d", 64)
	snapshot := domain.LadoPolicySnapshotRef{ID: "lado-snapshot-1", SHA256: rulesThen}
	blocking := domain.LadoPolicySnapshotRef{ID: "lado-snapshot-1", SHA256: rulesThen, Blocking: true}
	approved := func(snapshot domain.LadoPolicySnapshotRef, current string) domain.LadoTaskPlan {
		return domain.LadoTaskPlan{Latest: rev1, Approval: approval, Snapshot: snapshot, PolicySHA256: current}
	}
	// An approval with a rule snapshot has its queue entry. One given before
	// LADO had rules has neither.
	entry := domain.LadoQueueEntry{ID: "lado-queue-1", TaskID: "lado-task-1", ProjectID: "demo", PlanApprovalID: approval.ID, PolicySnapshotID: snapshot.ID}
	for _, tc := range []struct {
		name      string
		plan      domain.LadoTaskPlan
		issue     domain.NormalizedIssueState
		cancelled bool
		want      domain.LadoTaskState
	}{
		{"rules as approved", approved(snapshot, rulesThen), domain.IssueOpen, false, domain.LadoTaskQueued},
		{"rules changed since the approval", approved(snapshot, rulesNow), domain.IssueOpen, false, domain.LadoTaskReviewRequired},
		{"approved before rules existed", approved(domain.LadoPolicySnapshotRef{}, rulesNow), domain.IssueOpen, false, domain.LadoTaskReviewRequired},
		{"approved with a blocking problem", approved(blocking, rulesThen), domain.IssueOpen, false, domain.LadoTaskBlocked},
		{"rules changed after a blocked approval", approved(blocking, rulesNow), domain.IssueOpen, false, domain.LadoTaskReviewRequired},
		// The task service leaves the current hash empty when it could not
		// compute it; the stored snapshot alone decides then.
		{"current rules not computed", approved(snapshot, ""), domain.IssueOpen, false, domain.LadoTaskQueued},
		{"current rules not computed, blocking snapshot", approved(blocking, ""), domain.IssueOpen, false, domain.LadoTaskBlocked},
		// Without a snapshot the approval has no queue entry either.
		{"approved before rules existed, current rules not computed", approved(domain.LadoPolicySnapshotRef{}, ""), domain.IssueOpen, false, domain.LadoTaskReviewRequired},
		// Rule facts count only for a valid approval.
		{"edited after the approval", domain.LadoTaskPlan{Latest: rev2, Approval: approval, Snapshot: blocking, PolicySHA256: rulesNow}, domain.IssueOpen, false, domain.LadoTaskPlanned},
		{"cancelled task whose rules changed", approved(snapshot, rulesNow), domain.IssueOpen, true, domain.LadoTaskCancelled},
		{"cancelled task approved with a blocking problem", approved(blocking, rulesThen), domain.IssueOpen, true, domain.LadoTaskCancelled},
		{"closed issue whose rules changed", approved(snapshot, rulesNow), domain.IssueDone, false, domain.LadoTaskClosedUnmerged},
		{"closed issue approved with a blocking problem", approved(blocking, rulesThen), domain.IssueCancelled, false, domain.LadoTaskClosedUnmerged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := domain.LadoTask{IssueRef: "github:acme/demo#1", Issue: domain.LadoObservedIssue{State: tc.issue}, Plan: tc.plan}
			if tc.plan.Snapshot.ID != "" {
				task.Queue = entry
			}
			if tc.cancelled {
				task.CancelledAt = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
			}
			if got := task.State(); got != tc.want {
				t.Fatalf("State = %q, want %q", got, tc.want)
			}
		})
	}
}

// queuedLadoTask is an open task whose latest revision has a valid approval,
// the rules still giving its snapshot, and the approval's queue entry.
func queuedLadoTask(enqueuedAt time.Time) domain.LadoTask {
	rev := domain.LadoPlanRevision{ID: "lado-plan-1", RequestID: "lado-plan-request-1", Number: 1, SHA256: strings.Repeat("a", 64)}
	approval := domain.LadoPlanApproval{ID: "lado-approval-1", RevisionID: rev.ID, TaskID: "lado-task-1", SHA256: rev.SHA256}
	snapshot := domain.LadoPolicySnapshotRef{ID: "lado-snapshot-1", SHA256: strings.Repeat("c", 64)}
	return domain.LadoTask{
		ID:        "lado-task-1",
		ProjectID: "demo",
		IssueRef:  "github:acme/demo#1",
		Issue:     domain.LadoObservedIssue{State: domain.IssueOpen},
		Plan:      domain.LadoTaskPlan{Latest: rev, Approval: approval, Snapshot: snapshot, PolicySHA256: snapshot.SHA256},
		Queue: domain.LadoQueueEntry{
			ID:               "lado-queue-1",
			TaskID:           "lado-task-1",
			ProjectID:        "demo",
			PlanApprovalID:   approval.ID,
			PolicySnapshotID: snapshot.ID,
			EnqueuedAt:       enqueuedAt,
		},
	}
}

// dispatchedLadoTask is queuedLadoTask once the first attempt of its entry
// spawned session demo-7, which still runs.
func dispatchedLadoTask(startedAt time.Time) domain.LadoTask {
	task := queuedLadoTask(startedAt.Add(-time.Minute))
	task.Queue.AttemptCount, task.Queue.DispatchedSessionID = 1, "demo-7"
	task.Execution = domain.LadoExecution{
		SessionID:        "demo-7",
		TaskID:           task.ID,
		QueueEntryID:     task.Queue.ID,
		PlanRevisionID:   task.Plan.Latest.ID,
		PlanApprovalID:   task.Plan.Approval.ID,
		PolicySnapshotID: task.Plan.Snapshot.ID,
		Attempt:          1,
		StartedAt:        startedAt,
		DeadlineAt:       startedAt.Add(8 * time.Hour),
	}
	return task
}

// approveLadoTaskAgain gives the task a newer approval of its latest revision
// and that approval's queue entry, as approving a failed task again does.
func approveLadoTaskAgain(task *domain.LadoTask, at time.Time) {
	task.Plan.Approval.ID, task.Plan.Approval.ApprovedAt = "lado-approval-2", at
	task.Plan.Snapshot.ID = "lado-snapshot-2"
	task.Queue = domain.LadoQueueEntry{
		ID:               "lado-queue-2",
		TaskID:           task.ID,
		ProjectID:        task.ProjectID,
		PlanApprovalID:   task.Plan.Approval.ID,
		PolicySnapshotID: task.Plan.Snapshot.ID,
		EnqueuedAt:       at,
	}
}

func TestLadoTaskStateAfterDispatch(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	openPR := domain.LadoExecutionPR{URL: "https://github.com/acme/demo/pull/8", Number: 8}
	mergedPR, closedPR := openPR, openPR
	mergedPR.Merged, closedPR.Closed = true, true
	ended := func(task *domain.LadoTask) { task.Execution.SessionTerminated = true }
	for _, tc := range []struct {
		name string
		edit func(*domain.LadoTask)
		want domain.LadoTaskState
	}{
		{"session lives", func(*domain.LadoTask) {}, domain.LadoTaskRunning},
		{"session lives with its PR open", func(task *domain.LadoTask) { task.Execution.PR = openPR }, domain.LadoTaskRunning},
		{"session ended with its PR open", func(task *domain.LadoTask) { ended(task); task.Execution.PR = openPR }, domain.LadoTaskRunning},
		{"PR merged", func(task *domain.LadoTask) { task.Execution.PR = mergedPR }, domain.LadoTaskCompleted},
		{"PR merged after the session ended", func(task *domain.LadoTask) { ended(task); task.Execution.PR = mergedPR }, domain.LadoTaskCompleted},
		{"PR both merged and closed", func(task *domain.LadoTask) {
			task.Execution.PR = mergedPR
			task.Execution.PR.Closed = true
		}, domain.LadoTaskCompleted},
		{"PR closed unmerged", func(task *domain.LadoTask) { ended(task); task.Execution.PR = closedPR }, domain.LadoTaskClosedUnmerged},
		{"timed out", func(task *domain.LadoTask) { task.Execution.TimedOutAt = now }, domain.LadoTaskFailed},
		{"timed out and ended", func(task *domain.LadoTask) { ended(task); task.Execution.TimedOutAt = now }, domain.LadoTaskFailed},
		{"timed out with its PR open", func(task *domain.LadoTask) {
			task.Execution.TimedOutAt, task.Execution.PR = now, openPR
		}, domain.LadoTaskFailed},
		{"timed out, then the PR merged", func(task *domain.LadoTask) {
			task.Execution.TimedOutAt, task.Execution.PR = now, mergedPR
		}, domain.LadoTaskCompleted},
		{"timed out, then the PR closed", func(task *domain.LadoTask) {
			task.Execution.TimedOutAt, task.Execution.PR = now, closedPR
		}, domain.LadoTaskClosedUnmerged},
		{"session ended without a PR", ended, domain.LadoTaskFailed},
		// The execution decides before the issue does, but a failed run
		// leaves a closed issue's task closed: a merged PR usually closes the
		// issue.
		{"issue closed by the merged PR", func(task *domain.LadoTask) {
			task.Execution.PR, task.Issue.State = mergedPR, domain.IssueDone
		}, domain.LadoTaskCompleted},
		{"issue closed while the session runs", func(task *domain.LadoTask) { task.Issue.State = domain.IssueDone }, domain.LadoTaskRunning},
		{"issue closed while the PR is open", func(task *domain.LadoTask) {
			ended(task)
			task.Execution.PR, task.Issue.State = openPR, domain.IssueDone
		}, domain.LadoTaskRunning},
		{"issue closed after the session ended without a PR", func(task *domain.LadoTask) {
			ended(task)
			task.Issue.State = domain.IssueDone
		}, domain.LadoTaskClosedUnmerged},
		{"issue closed after a timeout", func(task *domain.LadoTask) {
			task.Execution.TimedOutAt, task.Issue.State = now, domain.IssueCancelled
		}, domain.LadoTaskClosedUnmerged},
		{"issue closed as not planned after the PR closed", func(task *domain.LadoTask) {
			task.Execution.PR, task.Issue.State = closedPR, domain.IssueCancelled
		}, domain.LadoTaskClosedUnmerged},
		// The execution keeps the rules and the plan it was dispatched with.
		{"rules changed after dispatch", func(task *domain.LadoTask) { task.Plan.PolicySHA256 = strings.Repeat("d", 64) }, domain.LadoTaskRunning},
		{"plan edited after dispatch", func(task *domain.LadoTask) {
			task.Plan.Latest = domain.LadoPlanRevision{ID: "lado-plan-2", RequestID: "lado-plan-request-2", Number: 2, SHA256: strings.Repeat("b", 64)}
		}, domain.LadoTaskRunning},
		{"task cancelled while the session runs", func(task *domain.LadoTask) { task.CancelledAt = now }, domain.LadoTaskCancelled},
		// Approving a failed task again starts over: the run that ended is
		// history, and the new approval's entry decides.
		{"approved again after the session ended without a PR", func(task *domain.LadoTask) {
			ended(task)
			approveLadoTaskAgain(task, now)
		}, domain.LadoTaskQueued},
		{"approved again after a timeout", func(task *domain.LadoTask) {
			ended(task)
			task.Execution.TimedOutAt = now.Add(-time.Minute)
			approveLadoTaskAgain(task, now)
		}, domain.LadoTaskQueued},
		{"newer approval after the PR closed", func(task *domain.LadoTask) {
			ended(task)
			task.Execution.PR = closedPR
			approveLadoTaskAgain(task, now)
		}, domain.LadoTaskQueued},
		{"issue closed after a failed run approved again", func(task *domain.LadoTask) {
			ended(task)
			approveLadoTaskAgain(task, now)
			task.Issue.State = domain.IssueDone
		}, domain.LadoTaskClosedUnmerged},
		// A run of an older approval still counts while it runs, and once its
		// PR merged.
		{"newer approval while the session runs", func(task *domain.LadoTask) { approveLadoTaskAgain(task, now) }, domain.LadoTaskRunning},
		{"newer approval while the PR is open", func(task *domain.LadoTask) {
			ended(task)
			task.Execution.PR = openPR
			approveLadoTaskAgain(task, now)
		}, domain.LadoTaskRunning},
		{"approved again after a timeout, then the old PR merged", func(task *domain.LadoTask) {
			ended(task)
			task.Execution.TimedOutAt, task.Execution.PR = now.Add(-time.Minute), mergedPR
			approveLadoTaskAgain(task, now)
		}, domain.LadoTaskCompleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := dispatchedLadoTask(now.Add(-time.Hour))
			tc.edit(&task)
			if got := task.StateAt(now); got != tc.want {
				t.Fatalf("StateAt = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLadoTaskStateBeforeDispatch(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Minute), now.Add(time.Minute)
	rulesNow := strings.Repeat("d", 64)
	rev2 := domain.LadoPlanRevision{ID: "lado-plan-2", RequestID: "lado-plan-request-2", Number: 2, SHA256: strings.Repeat("b", 64)}
	lease := func(task *domain.LadoTask, until time.Time) {
		task.Queue.AttemptCount = 1
		task.Queue.LeaseToken, task.Queue.ClaimedAt, task.Queue.LeaseExpiresAt = "token-1", until.Add(-2*time.Minute), until
	}
	failed := func(task *domain.LadoTask) {
		task.Queue.AttemptCount, task.Queue.FailedAt, task.Queue.ErrorMessage = 3, past, "spawn failed"
	}
	for _, tc := range []struct {
		name string
		edit func(*domain.LadoTask)
		want domain.LadoTaskState
	}{
		{"task cancelled", func(task *domain.LadoTask) { task.CancelledAt = past }, domain.LadoTaskCancelled},
		{"cancelled task with a closed issue", func(task *domain.LadoTask) {
			task.CancelledAt, task.Issue.State = past, domain.IssueDone
		}, domain.LadoTaskCancelled},
		{"issue closed", func(task *domain.LadoTask) { task.Issue.State = domain.IssueDone }, domain.LadoTaskClosedUnmerged},
		{"issue closed while dispatching", func(task *domain.LadoTask) {
			lease(task, future)
			task.Issue.State = domain.IssueCancelled
		}, domain.LadoTaskClosedUnmerged},
		{"issue closed after the rules changed", func(task *domain.LadoTask) {
			task.Plan.PolicySHA256, task.Issue.State = rulesNow, domain.IssueDone
		}, domain.LadoTaskClosedUnmerged},
		// Review required wins over Blocked, and both over the entry's other
		// facts.
		{"rules changed", func(task *domain.LadoTask) { task.Plan.PolicySHA256 = rulesNow }, domain.LadoTaskReviewRequired},
		{"rules changed over a blocking snapshot", func(task *domain.LadoTask) {
			task.Plan.PolicySHA256, task.Plan.Snapshot.Blocking = rulesNow, true
		}, domain.LadoTaskReviewRequired},
		{"rules changed over a blocked entry", func(task *domain.LadoTask) {
			task.Plan.PolicySHA256, task.Queue.BlockedReason = rulesNow, domain.LadoBlockedBaseRefMismatch
		}, domain.LadoTaskReviewRequired},
		{"rules changed while dispatching", func(task *domain.LadoTask) {
			lease(task, future)
			task.Plan.PolicySHA256 = rulesNow
		}, domain.LadoTaskReviewRequired},
		{"entry needs review", func(task *domain.LadoTask) { task.Queue.ReviewRequiredReason = domain.LadoReviewIssueEdited }, domain.LadoTaskReviewRequired},
		{"entry review over a blocking snapshot", func(task *domain.LadoTask) {
			task.Queue.ReviewRequiredReason, task.Plan.Snapshot.Blocking = domain.LadoReviewIssueEdited, true
		}, domain.LadoTaskReviewRequired},
		{"blocking snapshot", func(task *domain.LadoTask) { task.Plan.Snapshot.Blocking = true }, domain.LadoTaskBlocked},
		{"blocking snapshot over attempts exhausted", func(task *domain.LadoTask) {
			failed(task)
			task.Plan.Snapshot.Blocking = true
		}, domain.LadoTaskBlocked},
		{"entry blocked", func(task *domain.LadoTask) { task.Queue.BlockedReason = domain.LadoBlockedBaseRefMismatch }, domain.LadoTaskBlocked},
		{"attempts exhausted", failed, domain.LadoTaskFailed},
		// A valid approval without its entry asks to be approved again.
		{"approved before the queue", func(task *domain.LadoTask) { task.Queue = domain.LadoQueueEntry{} }, domain.LadoTaskReviewRequired},
		{"entry of an older approval", func(task *domain.LadoTask) {
			failed(task)
			task.Queue.PlanApprovalID = "lado-approval-0"
		}, domain.LadoTaskReviewRequired},
		{"entry cancelled while the issue was closed", func(task *domain.LadoTask) { task.Queue.CancelledAt = past }, domain.LadoTaskReviewRequired},
		{"dispatching", func(task *domain.LadoTask) { lease(task, future) }, domain.LadoTaskDispatching},
		{"lease expired", func(task *domain.LadoTask) { lease(task, past) }, domain.LadoTaskQueued},
		{"queued", func(*domain.LadoTask) {}, domain.LadoTaskQueued},
		{"waiting for a free slot", func(task *domain.LadoTask) { task.Queue.WaitingReason = domain.LadoWaitingSlotGlobal }, domain.LadoTaskQueued},
		{"waiting while memory is tight", func(task *domain.LadoTask) { task.Queue.WaitingReason = domain.LadoWaitingMemoryTight }, domain.LadoTaskQueued},
		{"waiting to retry", func(task *domain.LadoTask) {
			task.Queue.AttemptCount, task.Queue.WaitingReason, task.Queue.NextAttemptAt = 1, domain.LadoWaitingRetry, future
		}, domain.LadoTaskQueued},
		{"next attempt not due yet", func(task *domain.LadoTask) { task.Queue.NextAttemptAt = future }, domain.LadoTaskQueued},
		// The read always carries the execution of a dispatched entry; without
		// it the entry still says a session runs.
		{"dispatched entry without its execution", func(task *domain.LadoTask) {
			task.Queue.AttemptCount, task.Queue.DispatchedSessionID = 1, "demo-7"
		}, domain.LadoTaskRunning},
		// Queue facts count only while the approval is valid: a newer revision
		// waits for its own approval.
		{"plan edited after the approval", func(task *domain.LadoTask) { task.Plan.Latest = rev2 }, domain.LadoTaskPlanned},
		{"plan edited after the entry was blocked", func(task *domain.LadoTask) {
			task.Plan.Latest, task.Queue.BlockedReason = rev2, domain.LadoBlockedBaseRefMismatch
		}, domain.LadoTaskPlanned},
		{"plan edited after the attempts ran out", func(task *domain.LadoTask) {
			failed(task)
			task.Plan.Latest = rev2
		}, domain.LadoTaskPlanned},
		{"plan edited while dispatching", func(task *domain.LadoTask) {
			lease(task, future)
			task.Plan.Latest = rev2
		}, domain.LadoTaskPlanned},
		{"not approved yet", func(task *domain.LadoTask) {
			task.Plan.Approval, task.Plan.Snapshot, task.Queue = domain.LadoPlanApproval{}, domain.LadoPolicySnapshotRef{}, domain.LadoQueueEntry{}
		}, domain.LadoTaskPlanned},
		{"no plan yet", func(task *domain.LadoTask) { task.Plan, task.Queue = domain.LadoTaskPlan{}, domain.LadoQueueEntry{} }, domain.LadoTaskDraft},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := queuedLadoTask(now.Add(-time.Hour))
			tc.edit(&task)
			if got := task.StateAt(now); got != tc.want {
				t.Fatalf("StateAt = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLadoTaskStateReason(t *testing.T) {
	// State and StateReason read the clock, so leases and retries are set
	// around it.
	now := time.Now()
	future := now.Add(time.Hour)
	rev2 := domain.LadoPlanRevision{ID: "lado-plan-2", RequestID: "lado-plan-request-2", Number: 2, SHA256: strings.Repeat("b", 64)}
	waiting := func(reason string, retryAt time.Time) func(*domain.LadoTask) {
		return func(task *domain.LadoTask) { task.Queue.WaitingReason, task.Queue.NextAttemptAt = reason, retryAt }
	}
	for _, tc := range []struct {
		name       string
		dispatched bool
		edit       func(*domain.LadoTask)
		state      domain.LadoTaskState
		reason     string
	}{
		{"draft", false, func(task *domain.LadoTask) { task.Plan, task.Queue = domain.LadoTaskPlan{}, domain.LadoQueueEntry{} }, domain.LadoTaskDraft, ""},
		{"planned", false, func(task *domain.LadoTask) { task.Plan.Latest = rev2 }, domain.LadoTaskPlanned, ""},
		{"cancelled", false, func(task *domain.LadoTask) { task.CancelledAt = now }, domain.LadoTaskCancelled, ""},
		{"issue closed", false, func(task *domain.LadoTask) { task.Issue.State = domain.IssueDone }, domain.LadoTaskClosedUnmerged, ""},
		{"rules changed", false, func(task *domain.LadoTask) { task.Plan.PolicySHA256 = strings.Repeat("d", 64) }, domain.LadoTaskReviewRequired, domain.LadoReviewRulesChanged},
		{"rules changed at dispatch", false, func(task *domain.LadoTask) {
			task.Plan.PolicySHA256, task.Queue.ReviewRequiredReason = "", domain.LadoReviewRulesChanged
		}, domain.LadoTaskReviewRequired, domain.LadoReviewRulesChanged},
		{"issue edited", false, func(task *domain.LadoTask) { task.Queue.ReviewRequiredReason = domain.LadoReviewIssueEdited }, domain.LadoTaskReviewRequired, domain.LadoReviewIssueEdited},
		{"approved before the queue", false, func(task *domain.LadoTask) { task.Queue = domain.LadoQueueEntry{} }, domain.LadoTaskReviewRequired, domain.LadoReviewApprovedBeforeQueue},
		{"blocking snapshot", false, func(task *domain.LadoTask) { task.Plan.Snapshot.Blocking = true }, domain.LadoTaskBlocked, domain.LadoBlockedSnapshot},
		{"base ref mismatch", false, func(task *domain.LadoTask) { task.Queue.BlockedReason = domain.LadoBlockedBaseRefMismatch }, domain.LadoTaskBlocked, domain.LadoBlockedBaseRefMismatch},
		{"agent unsupported", false, func(task *domain.LadoTask) { task.Queue.BlockedReason = domain.LadoBlockedAgentUnsupported }, domain.LadoTaskBlocked, domain.LadoBlockedAgentUnsupported},
		{"attempts exhausted", false, func(task *domain.LadoTask) {
			task.Queue.AttemptCount, task.Queue.FailedAt, task.Queue.ErrorMessage = 3, now, "spawn failed"
		}, domain.LadoTaskFailed, domain.LadoFailedAttemptsExhausted},
		{"queued", false, func(*domain.LadoTask) {}, domain.LadoTaskQueued, ""},
		{"no global slot", false, waiting(domain.LadoWaitingSlotGlobal, time.Time{}), domain.LadoTaskQueued, domain.LadoWaitingSlotGlobal},
		{"no project slot", false, waiting(domain.LadoWaitingSlotProject, time.Time{}), domain.LadoTaskQueued, domain.LadoWaitingSlotProject},
		{"memory tight", false, waiting(domain.LadoWaitingMemoryTight, time.Time{}), domain.LadoTaskQueued, domain.LadoWaitingMemoryTight},
		{"issue unreadable", false, waiting(domain.LadoWaitingIssueUnreadable, future), domain.LadoTaskQueued, domain.LadoWaitingIssueUnreadable},
		{"retry", false, waiting(domain.LadoWaitingRetry, future), domain.LadoTaskQueued, domain.LadoWaitingRetry},
		{"LADO mode off", false, waiting(domain.LadoWaitingModeOff, time.Time{}), domain.LadoTaskQueued, domain.LadoWaitingModeOff},
		{"next attempt without a reason", false, waiting("", future), domain.LadoTaskQueued, ""},
		{"dispatching", false, func(task *domain.LadoTask) {
			task.Queue.AttemptCount, task.Queue.LeaseToken, task.Queue.ClaimedAt, task.Queue.LeaseExpiresAt = 1, "token-1", now, future
		}, domain.LadoTaskDispatching, ""},
		{"running", true, func(*domain.LadoTask) {}, domain.LadoTaskRunning, ""},
		{"timed out", true, func(task *domain.LadoTask) { task.Execution.TimedOutAt = now }, domain.LadoTaskFailed, domain.LadoFailedTimedOut},
		{"ended without a PR", true, func(task *domain.LadoTask) { task.Execution.SessionTerminated = true }, domain.LadoTaskFailed, domain.LadoFailedEndedWithoutPR},
		{"failed run of a closed issue", true, func(task *domain.LadoTask) {
			task.Execution.SessionTerminated, task.Issue.State = true, domain.IssueDone
		}, domain.LadoTaskClosedUnmerged, ""},
		{"completed", true, func(task *domain.LadoTask) {
			task.Execution.PR = domain.LadoExecutionPR{URL: "https://github.com/acme/demo/pull/8", Number: 8, Merged: true}
		}, domain.LadoTaskCompleted, ""},
		{"PR closed", true, func(task *domain.LadoTask) {
			task.Execution.PR = domain.LadoExecutionPR{URL: "https://github.com/acme/demo/pull/8", Number: 8, Closed: true}
		}, domain.LadoTaskClosedUnmerged, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := queuedLadoTask(now.Add(-time.Hour))
			if tc.dispatched {
				task = dispatchedLadoTask(now.Add(-time.Hour))
			}
			tc.edit(&task)
			if got := task.State(); got != tc.state {
				t.Fatalf("State = %q, want %q", got, tc.state)
			}
			if got := task.StateAt(now); got != tc.state {
				t.Fatalf("StateAt = %q, want %q like State", got, tc.state)
			}
			if got := task.StateReason(); got != tc.reason {
				t.Fatalf("StateReason = %q, want %q", got, tc.reason)
			}
		})
	}
}

func TestLadoTrackerRepoFromOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, origin string
		want         domain.TrackerRepo
	}{
		{"github scp", "git@github.com:Acme/Demo.git", domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "Acme/Demo"}},
		{"github ssh url", "ssh://git@github.com/acme/demo.git", domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}},
		{"github https", "https://github.com/acme/demo", domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}},
		{"gitlab.com nested", "git@gitlab.com:group/sub/project.git", domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/sub/project"}},
		{"self-managed ssh port", "ssh://git@GitLab.Example.com:2222/group/project.git", domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/project", Host: "gitlab.example.com"}},
		{"self-managed https", "https://gitlab.example.com/group/project", domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/project", Host: "gitlab.example.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.LadoTrackerRepoFromOrigin(tc.origin)
			if err != nil {
				t.Fatalf("LadoTrackerRepoFromOrigin: %v", err)
			}
			if got != tc.want {
				t.Fatalf("LadoTrackerRepoFromOrigin = %+v, want %+v", got, tc.want)
			}
		})
	}
	for _, origin := range []string{"", "not a url", "git@acme.ghe.io:acme/demo.git", "https://api.github.com/acme/demo"} {
		if got, err := domain.LadoTrackerRepoFromOrigin(origin); err == nil {
			t.Errorf("LadoTrackerRepoFromOrigin(%q) = %+v, want an error", origin, got)
		}
	}
}

func TestLadoSameRepo(t *testing.T) {
	a := domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "Acme/Demo"}
	if !domain.LadoSameRepo(a, domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}) {
		t.Fatal("repositories differing only in case are the same repository")
	}
	if domain.LadoSameRepo(a, domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/other"}) {
		t.Fatal("different repositories matched")
	}
	gitlab := domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "acme/demo"}
	if domain.LadoSameRepo(a, gitlab) {
		t.Fatal("different providers matched")
	}
	if domain.LadoSameRepo(gitlab, domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "acme/demo", Host: "gitlab.example.com"}) {
		t.Fatal("different GitLab hosts matched")
	}
}

func TestLadoRepoRefRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		repo domain.TrackerRepo
		ref  string
	}{
		{domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/back"}, "github:acme/back"},
		{domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/sub/front"}, "gitlab:group/sub/front"},
		{domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/back", Host: "gitlab.example.com"}, "gitlab:group/back@gitlab.example.com"},
	} {
		if got := domain.LadoRepoRef(tc.repo); got != tc.ref {
			t.Fatalf("LadoRepoRef(%+v) = %q, want %q", tc.repo, got, tc.ref)
		}
		parsed, err := domain.ParseLadoRepoRef(tc.ref)
		if err != nil || parsed != tc.repo {
			t.Fatalf("ParseLadoRepoRef(%q) = %+v, %v; want %+v", tc.ref, parsed, err, tc.repo)
		}
	}
	for _, ref := range []string{"", "acme/back", "github:acme/back#12", "github:acme", "jira:a/b", "github:acme/back@ghe.example.com"} {
		if _, err := domain.ParseLadoRepoRef(ref); err == nil {
			t.Errorf("ParseLadoRepoRef(%q) = nil error, want an error", ref)
		}
	}
}

func TestLadoIssueRepo(t *testing.T) {
	id := domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/back#7", Host: "gitlab.example.com"}
	want := domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/back", Host: "gitlab.example.com"}
	if got := domain.LadoIssueRepo(id); got != want {
		t.Fatalf("LadoIssueRepo = %+v, want %+v", got, want)
	}
}
