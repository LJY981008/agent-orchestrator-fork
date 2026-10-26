package ladoqueue

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// B4: each pass stops the running workers their task no longer wants, a
// cancelled task's and one whose approval a newer one replaced, and audits
// each stop. A worker that could not be stopped is stopped on the next pass.
func TestSweepStopsUnwantedRuns(t *testing.T) {
	f := newFixture(t)
	cancelled := testTask()
	cancelled.ID, cancelled.CancelledAt = "task-2", t0.Add(-time.Minute)
	f.store.setTask(cancelled)
	replaced := testTask()
	replaced.ID = "task-3"
	f.store.setTask(replaced)
	run := func(session domain.SessionID, task domain.LadoTaskID, approval domain.LadoPlanApprovalID) domain.LadoExecution {
		return domain.LadoExecution{SessionID: session, TaskID: task, PlanApprovalID: approval, Attempt: 1, StartedAt: t0.Add(-time.Hour)}
	}
	f.store.live = []domain.LadoExecution{
		run("proj-1-1", "task-1", "approval-1"),
		run("proj-1-2", "task-2", "approval-1"),
		// task-3's newest approval is approval-1.
		run("proj-1-3", "task-3", "approval-0"),
	}
	f.sessions.setKillErr("proj-1-3", apierr.Internal("SESSION_KILL_FAILED", "The runtime did not stop"))
	stopped := func(t *testing.T, ev domain.LadoAuditEvent, at time.Time, task, session, reason string) {
		t.Helper()
		if ev.At != at || ev.Actor != "lado" || ev.Action != "execution.stopped" || ev.SubjectKind != "execution" || ev.SubjectID != session {
			t.Fatalf("audit event %+v, want LADO's execution.stopped of %s at %s", ev, session, at)
		}
		var payload map[string]string
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatalf("payload %s: %v", ev.Payload, err)
		}
		if want := map[string]string{"task": task, "session": session, "reason": reason}; !reflect.DeepEqual(payload, want) {
			t.Fatalf("payload = %v, want %v", payload, want)
		}
	}

	f.tick(t, t0)
	if kills := f.sessions.killed(); !reflect.DeepEqual(kills, []domain.SessionID{"proj-1-2", "proj-1-3"}) {
		t.Fatalf("stopped %v, want the cancelled task's and the replaced approval's workers", kills)
	}
	events := f.store.audited()
	if len(events) != 1 {
		t.Fatalf("audit events %+v, want the one stop that worked", events)
	}
	stopped(t, events[0], t0, "task-2", "proj-1-2", "task_cancelled")

	// The worker that did not stop is stopped on the next pass, and the
	// stopped ones are left alone.
	f.sessions.setKillErr("proj-1-3", nil)
	next := t0.Add(15 * time.Second)
	f.tick(t, next)
	f.tick(t, next.Add(15*time.Second))
	if kills := f.sessions.killed(); !reflect.DeepEqual(kills, []domain.SessionID{"proj-1-2", "proj-1-3", "proj-1-3"}) {
		t.Fatalf("stopped %v, want proj-1-3 once more", kills)
	}
	if events = f.store.audited(); len(events) != 2 {
		t.Fatalf("audit events %+v, want two", events)
	}
	stopped(t, events[1], next, "task-3", "proj-1-3", "approval_superseded")
}
