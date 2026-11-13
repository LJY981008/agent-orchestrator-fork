package ladoqueue

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// newOverlapFixture is the store fixture whose tasks all name backend/**:
// task-1, approved first, and the task test queues after it.
func newOverlapFixture(t *testing.T) *storeFixture {
	t.Helper()
	sf := newStoreFixture(t, nil)
	sf.tasks.input.PlanText = pathsPlan("backend/**")
	sf.tasks.revisionPaths = map[domain.LadoPlanRevisionID][]string{"rev-2": {"backend/**"}}
	// Both tasks may run at once: only their paths hold one back.
	if err := sf.db.SetLadoProjectMaxConcurrent(context.Background(), "proj-1", 2, domain.LadoAuditEvent{
		At: t0, Actor: "test", Action: "project.cap_changed", SubjectKind: "project", SubjectID: "proj-1",
	}); err != nil {
		t.Fatalf("raise the project's cap: %v", err)
	}
	// A new project has the overlap wait off (#101); these tests turn it on.
	if err := sf.db.SetLadoProjectOverlap(context.Background(), "proj-1", true, nil, domain.LadoAuditEvent{
		At: t0, Actor: "test", Action: "project.overlap_changed", SubjectKind: "project", SubjectID: "proj-1",
	}); err != nil {
		t.Fatalf("turn the overlap wait on: %v", err)
	}
	return sf
}

// entry returns the queue entry of task-<key> as its task reads it.
func (sf *storeFixture) entry(t *testing.T, key string) domain.LadoQueueEntry {
	t.Helper()
	task, ok, err := sf.db.LadoTask(context.Background(), domain.LadoTaskID("task-"+key))
	if err != nil || !ok {
		t.Fatalf("read task-%s = (%v, %v)", key, ok, err)
	}
	return task.Queue
}

// requireWaitsForTask1 checks that the entry of task-<key> waits for task-1's paths without a
// worker.
func (sf *storeFixture) requireWaitsForTask1(t *testing.T, key string) {
	t.Helper()
	entry := sf.entry(t, key)
	if entry.WaitingReason != domain.LadoWaitingPathsOverlap || entry.WaitingDetail != "github:acme/app#12" || entry.DispatchedSessionID != "" {
		t.Fatalf("entry = %+v, want it waiting for #12's paths", entry)
	}
}

// A run holds its paths while its PR is open, but no longer once the PR
// merged or closed, the run timed out or the task was cancelled, even while
// its session still runs. The run's slot still counts.
func TestASettledRunReleasesItsPathsWhileItsSessionRuns(t *testing.T) {
	settle := map[string]func(t *testing.T, sf *storeFixture, run domain.SessionID){
		"open": func(*testing.T, *storeFixture, domain.SessionID) {},
		"merged": func(t *testing.T, sf *storeFixture, run domain.SessionID) {
			writeOverlapPR(t, sf, run, domain.PRStateMerged)
		},
		"closed": func(t *testing.T, sf *storeFixture, run domain.SessionID) {
			writeOverlapPR(t, sf, run, domain.PRStateClosed)
		},
		"timed out": func(t *testing.T, sf *storeFixture, run domain.SessionID) {
			if _, err := sf.db.MarkLadoExecutionTimedOut(context.Background(), run, domain.LadoAuditEvent{
				At: sf.clock(), Actor: "lado", Action: "execution.timed_out", SubjectKind: "execution", SubjectID: string(run),
			}); err != nil {
				t.Fatalf("time the run out: %v", err)
			}
		},
		"cancelled": func(t *testing.T, sf *storeFixture, _ domain.SessionID) {
			// The task's sweep stops the session later; it still runs here.
			sf.sessions.setKillErr(sf.session(t, 1).ID, context.DeadlineExceeded)
			if _, _, err := sf.db.CancelLadoTask(context.Background(), "task-1", sf.clock()); err != nil {
				t.Fatalf("cancel task-1: %v", err)
			}
		},
	}
	for name, settleRun := range settle {
		t.Run(name, func(t *testing.T) {
			sf := newOverlapFixture(t)
			sf.tick(t, t0)
			run := sf.session(t, 1).ID
			writeOverlapPR(t, sf, run, domain.PRStateOpen)
			settleRun(t, sf, run)
			sf.queueTask(t, "9", "proj-1")
			sf.tick(t, t0.Add(time.Minute))
			if name == "open" {
				sf.requireWaitsForTask1(t, "9")
				return
			}
			if entry := sf.entry(t, "9"); entry.DispatchedSessionID == "" {
				t.Fatalf("entry = %+v, want it dispatched", entry)
			}
			if rec, _, _ := sf.db.GetSession(context.Background(), run); rec.IsTerminated {
				t.Fatal("the settled run's session ended; the test wants it running")
			}
		})
	}
}

func writeOverlapPR(t *testing.T, sf *storeFixture, run domain.SessionID, state domain.PRState) {
	t.Helper()
	if err := sf.db.WriteSCMObservation(context.Background(), domain.PullRequest{
		URL: "https://github.com/acme/app/pull/12", SessionID: run, Number: 12,
		Merged: state == domain.PRStateMerged, Closed: state == domain.PRStateClosed,
		HeadSHA: "1111111111111111111111111111111111111111", SourceBranch: "feat-12", TargetBranch: "main",
		Title: "Change 12", Mergeability: domain.MergeMergeable, UpdatedAt: sf.clock(),
	}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write the PR: %v", err)
	}
}

// An entry approved after another task of the project that still waits in
// the queue with overlapping paths waits for it; an earlier one that is
// blocked or needs a review does not hold it, so no two entries wait for
// each other.
func TestALaterEntryWaitsForAnEarlierQueuedOverlap(t *testing.T) {
	cases := []struct {
		name  string
		hold  func(t *testing.T, sf *storeFixture, token string)
		waits bool
	}{
		{"waiting", func(t *testing.T, sf *storeFixture, token string) {
			if ok, err := sf.db.ReleaseLadoQueueEntry(context.Background(), storeEntry, token, t0.Add(time.Hour), domain.LadoWaitingSlotWorkers); err != nil || !ok {
				t.Fatalf("release task-1 = (%v, %v)", ok, err)
			}
		}, true},
		{"blocked", func(t *testing.T, sf *storeFixture, token string) {
			if ok, err := sf.db.MarkLadoQueueEntryBlocked(context.Background(), storeEntry, token, "a rule blocks it"); err != nil || !ok {
				t.Fatalf("block task-1 = (%v, %v)", ok, err)
			}
		}, false},
		{"review required", func(t *testing.T, sf *storeFixture, token string) {
			if ok, err := sf.db.MarkLadoQueueEntryReviewRequired(context.Background(), storeEntry, token, "the rules changed"); err != nil || !ok {
				t.Fatalf("mark task-1 for review = (%v, %v)", ok, err)
			}
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sf := newOverlapFixture(t)
			claimed, err := sf.db.ClaimLadoQueueEntries(context.Background(), t0, t0.Add(time.Minute), 1, nil, func() string { return "hold-1" })
			if err != nil || len(claimed) != 1 || claimed[0].ID != storeEntry {
				t.Fatalf("claim task-1 = (%+v, %v)", claimed, err)
			}
			tc.hold(t, sf, claimed[0].LeaseToken)
			sf.queueTask(t, "9", "proj-1")
			sf.tick(t, t0.Add(time.Minute))
			if tc.waits {
				sf.requireWaitsForTask1(t, "9")
				if got := len(sf.sessions.spawned()); got != 0 {
					t.Fatalf("spawns = %d, want none", got)
				}
				return
			}
			if entry := sf.entry(t, "9"); entry.DispatchedSessionID == "" {
				t.Fatalf("entry = %+v, want it dispatched", entry)
			}
		})
	}
}
