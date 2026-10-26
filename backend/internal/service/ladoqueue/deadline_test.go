package ladoqueue

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestOverdueRunsAreStopped(t *testing.T) {
	f := newFixture(t)
	run := func(session domain.SessionID, task domain.LadoTaskID, deadline time.Time) domain.LadoExecution {
		return domain.LadoExecution{SessionID: session, TaskID: task, Attempt: 1, StartedAt: deadline.Add(-8 * time.Hour), DeadlineAt: deadline}
	}
	stuck := run("proj-1-4", "task-2", t0.Add(-time.Hour))
	overdue := run("proj-1-3", "task-1", t0.Add(-time.Minute))
	later := run("proj-1-5", "task-3", t0.Add(time.Hour))
	f.store.overdue = []domain.LadoExecution{stuck, overdue, later}
	f.sessions.killErrs = map[domain.SessionID]error{"proj-1-4": apierr.Internal("SESSION_KILL_FAILED", "The runtime did not stop")}
	timedOut := func(id domain.SessionID) time.Time {
		t.Helper()
		runs, _ := f.store.timedOut()
		for _, exec := range runs {
			if exec.SessionID == id {
				return exec.TimedOutAt
			}
		}
		t.Fatalf("no run %s", id)
		return time.Time{}
	}

	// Both overdue workers are asked to stop; the one that stopped is
	// timed out, with its audit event, and the other is not.
	f.tick(t, t0)
	if kills := f.sessions.killed(); !reflect.DeepEqual(kills, []domain.SessionID{"proj-1-4", "proj-1-3"}) {
		t.Fatalf("stopped %v, want the two overdue workers", kills)
	}
	if !timedOut("proj-1-3").Equal(t0) || !timedOut("proj-1-4").IsZero() || !timedOut("proj-1-5").IsZero() {
		t.Fatalf("time-outs: proj-1-3 %s, proj-1-4 %s, proj-1-5 %s; want only proj-1-3, now",
			timedOut("proj-1-3"), timedOut("proj-1-4"), timedOut("proj-1-5"))
	}
	_, events := f.store.timedOut()
	if len(events) != 1 {
		t.Fatalf("audit events %+v, want one", events)
	}
	ev := events[0]
	if ev.At != t0 || ev.Actor != "lado" || ev.Action != "execution.timed_out" || ev.SubjectKind != "execution" || ev.SubjectID != "proj-1-3" {
		t.Fatalf("audit event %+v, want LADO's execution.timed_out of proj-1-3 now", ev)
	}
	var payload map[string]string
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatalf("payload %s: %v", ev.Payload, err)
	}
	if want := map[string]string{"task": "task-1", "session": "proj-1-3", "deadline": overdue.DeadlineAt.Format(time.RFC3339Nano)}; !reflect.DeepEqual(payload, want) {
		t.Fatalf("payload = %v, want %v", payload, want)
	}
	if logs := f.logs.String(); !strings.Contains(logs, "could not stop a worker past its deadline") || !strings.Contains(logs, "SESSION_KILL_FAILED") {
		t.Fatalf("the logs do not say proj-1-4 could not be stopped:\n%s", logs)
	}

	// The next pass stops the stuck worker again, and only it.
	f.sessions.killErrs = nil
	next := t0.Add(15 * time.Second)
	f.tick(t, next)
	if kills := f.sessions.killed(); !reflect.DeepEqual(kills, []domain.SessionID{"proj-1-4", "proj-1-3", "proj-1-4"}) {
		t.Fatalf("stopped %v, want proj-1-4 once more", kills)
	}
	if _, events := f.store.timedOut(); !timedOut("proj-1-4").Equal(next) || len(events) != 2 {
		t.Fatalf("proj-1-4 timed out at %s with %d audit events, want now and two", timedOut("proj-1-4"), len(events))
	}
}

// Stop is the task service's Killer: a cancelled task's worker stops through
// AO.
func TestStopStopsTheSession(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Stop(context.Background(), "proj-1-3"); err != nil {
		t.Fatalf("Stop = %v", err)
	}
	refused := errors.New("the session is gone")
	f.sessions.killErrs = map[domain.SessionID]error{"proj-1-4": refused}
	if err := f.svc.Stop(context.Background(), "proj-1-4"); !errors.Is(err, refused) {
		t.Fatalf("Stop = %v, want AO's error", err)
	}
	if kills := f.sessions.killed(); !reflect.DeepEqual(kills, []domain.SessionID{"proj-1-3", "proj-1-4"}) {
		t.Fatalf("stopped %v, want both sessions", kills)
	}
}

// B6: once a run has a PR, whatever its state, its maximum run time no
// longer applies: what is left is the PR's review. A run past its deadline
// without one is stopped.
func TestDeadlineStopsOnlyBeforeAPR(t *testing.T) {
	f := newFixture(t)
	deadline := t0.Add(-time.Minute)
	run := func(session domain.SessionID, pr domain.LadoExecutionPR) domain.LadoExecution {
		return domain.LadoExecution{SessionID: session, TaskID: "task-1", Attempt: 1, StartedAt: deadline.Add(-8 * time.Hour), DeadlineAt: deadline, PR: pr}
	}
	f.store.overdue = []domain.LadoExecution{
		run("proj-1-3", domain.LadoExecutionPR{URL: "https://github.com/acme/app/pull/7", Number: 7}),
		run("proj-1-4", domain.LadoExecutionPR{URL: "https://github.com/acme/app/pull/8", Number: 8, Closed: true}),
		run("proj-1-5", domain.LadoExecutionPR{}),
	}

	f.tick(t, t0)

	if kills := f.sessions.killed(); !reflect.DeepEqual(kills, []domain.SessionID{"proj-1-5"}) {
		t.Fatalf("stopped %v, want only the run without a PR", kills)
	}
	runs, events := f.store.timedOut()
	if !runs[0].TimedOutAt.IsZero() || !runs[1].TimedOutAt.IsZero() || !runs[2].TimedOutAt.Equal(t0) || len(events) != 1 {
		t.Fatalf("time-outs %+v with %d events, want only proj-1-5 timed out now", runs, len(events))
	}
}

// Recovery and deadlines do not wait for the dispatch settings; claims do.
func TestTickStopsOverdueRunsWithoutSettings(t *testing.T) {
	f := newFixture(t)
	f.store.settingsErr = errors.New("database is locked")
	f.store.overdue = []domain.LadoExecution{{SessionID: "proj-1-3", TaskID: "task-1", Attempt: 1, StartedAt: t0.Add(-9 * time.Hour), DeadlineAt: t0.Add(-time.Hour)}}
	f.store.add(testEntry("entry-1", t0.Add(-10*time.Minute)))

	if err := f.svc.Tick(context.Background(), t0); err == nil || !strings.Contains(err.Error(), "database is locked") {
		t.Fatalf("Tick = %v, want the failed settings read", err)
	}
	if kills := f.sessions.killed(); !reflect.DeepEqual(kills, []domain.SessionID{"proj-1-3"}) {
		t.Fatalf("stopped %v, want the overdue run", kills)
	}
	if limits := f.store.claimLimits(); len(limits) != 0 {
		t.Fatalf("claimed with %v, want no claim without the settings", limits)
	}
}
