package ladoqueue

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Gate 13: a daemon that stops after AO started a worker and before LADO
// linked it finds that worker when it starts again. The task has one
// session, linked as its execution, and AO was asked to start one worker.
func TestRecoveryLinksASpawnedSession(t *testing.T) {
	for _, c := range []struct {
		name string
		// restart is how long after the crash the daemon runs again.
		restart time.Duration
	}{
		{"while the claim still holds", 30 * time.Second},
		{"after the claim expired", 10 * time.Minute},
	} {
		t.Run(c.name, func(t *testing.T) {
			sf := newStoreFixture(t, nil)
			ctx := context.Background()

			crashed := sf.dispatcher(&crashBeforeLink{Store: sf.db}, sf.git)
			sf.setClock(t0)
			if err := crashed.Tick(ctx, t0); err == nil || !strings.Contains(err.Error(), "the daemon stopped") {
				t.Fatalf("Tick before the crash = %v, want the failed link", err)
			}
			worker := sf.session(t, 1)
			if !worker.ClientRequestCommitted {
				t.Fatalf("session %+v, want AO's committed session", worker)
			}
			if _, ok, err := sf.db.LadoExecution(ctx, worker.ID); err != nil || ok {
				t.Fatalf("execution before the restart = (%v, %v), want none", ok, err)
			}

			// The daemon starts again with a new dispatcher.
			restarted := t0.Add(c.restart)
			sf.setClock(restarted)
			if err := sf.dispatcher(sf.db, sf.git).Tick(ctx, restarted); err != nil {
				t.Fatalf("Tick after the restart = %v", err)
			}

			if n := len(sf.sessions.spawned()); n != 1 {
				t.Fatalf("AO was asked to start %d workers, want 1", n)
			}
			sessions, err := sf.db.ListSessions(ctx, "proj-1")
			if err != nil || len(sessions) != 1 || sessions[0].ID != worker.ID {
				t.Fatalf("sessions = %+v (%v), want only %s", sessions, err, worker.ID)
			}
			// The run started when AO created the session, and its deadline
			// runs from there.
			exec := sf.execution(t, worker.ID)
			if exec.QueueEntryID != storeEntry || exec.Attempt != 1 || exec.TaskID != "task-1" || exec.PlanRevisionID != "rev-2" ||
				!exec.StartedAt.Equal(worker.CreatedAt) || !exec.DeadlineAt.Equal(worker.CreatedAt.Add(8*time.Hour)) {
				t.Fatalf("execution = %+v, want attempt 1 of %s started at %s with an eight-hour deadline", exec, storeEntry, worker.CreatedAt)
			}
			task, _, err := sf.db.LadoTask(ctx, "task-1")
			if err != nil || task.StateAt(restarted) != domain.LadoTaskRunning || task.Queue.DispatchedSessionID != worker.ID || task.Queue.LeaseToken != "" {
				t.Fatalf("task %s with entry %+v (%v), want it running %s", task.StateAt(restarted), task.Queue, err, worker.ID)
			}
			if !strings.Contains(sf.logs.String(), "LADO linked the worker a stopped dispatch had started") {
				t.Fatalf("the logs do not say the worker was linked:\n%s", sf.logs.String())
			}

			// Later passes start nothing more.
			later := restarted.Add(time.Hour)
			sf.setClock(later)
			if err := sf.dispatcher(sf.db, sf.git).Tick(ctx, later); err != nil || len(sf.sessions.spawned()) != 1 {
				t.Fatalf("a later pass = %v after %d spawns, want nothing more", err, len(sf.sessions.spawned()))
			}
		})
	}
}

func TestRecoveryReleasesAnExpiredClaim(t *testing.T) {
	expired, holds := t0.Add(-time.Minute), t0.Add(time.Minute)
	claimed := func(attempt int, leaseEnds time.Time) domain.LadoQueueEntry {
		e := testEntry("entry-1", t0.Add(-time.Hour))
		e.LeaseToken, e.ClaimedAt, e.LeaseExpiresAt, e.AttemptCount = "old-lease", leaseEnds.Add(-2*time.Minute), leaseEnds, attempt
		return e
	}
	session := func(committed, ended bool) *domain.SessionRecord {
		return &domain.SessionRecord{
			ID: "proj-1-7", ProjectID: "proj-1", Kind: domain.KindWorker, ClientRequestID: "lado:entry-1/1", ClientRequestHash: testDispatchHash(),
			ClientRequestCommitted: committed, IsTerminated: ended, CreatedAt: t0.Add(-3 * time.Minute),
		}
	}
	released := func(t *testing.T, e domain.LadoQueueEntry) {
		t.Helper()
		if e.LeaseToken != "" || e.WaitingReason != domain.LadoWaitingRetry || !e.NextAttemptAt.Equal(t0) || e.AttemptCount != 1 || e.State(t0) != domain.LadoQueueWaiting {
			t.Fatalf("entry = %+v, want it due again now, waiting to retry after its first attempt", e)
		}
	}
	untouched := func(t *testing.T, e domain.LadoQueueEntry) {
		t.Helper()
		if want := claimed(1, holds); !reflect.DeepEqual(e, want) {
			t.Fatalf("entry = %+v, want it left as it was: %+v", e, want)
		}
	}
	for _, c := range []struct {
		name    string
		entry   domain.LadoQueueEntry
		session *domain.SessionRecord
		check   func(*testing.T, domain.LadoQueueEntry)
		// kills are the sessions recovery stops.
		kills []domain.SessionID
	}{
		{"an expired claim without a session", claimed(1, expired), nil, released, nil},
		// AO's startup checks would clean it up as well; stopping it keeps the
		// next attempt's worker the task's only one.
		{"an expired claim whose session AO never finished starting", claimed(1, expired), session(false, false), released, []domain.SessionID{"proj-1-7"}},
		{"an expired claim whose session AO rolled back", claimed(1, expired), session(false, true), released, nil},
		{"an expired last attempt", claimed(3, expired), nil, func(t *testing.T, e domain.LadoQueueEntry) {
			t.Helper()
			if e.State(t0) != domain.LadoQueueFailed || e.ErrorMessage != abandonedAttempt || !e.FailedAt.Equal(t0) || e.LeaseToken != "" {
				t.Fatalf("entry = %+v, want it failed now: its three attempts are spent", e)
			}
		}, nil},
		// Another dispatch may still be starting the worker.
		{"a claim that still holds", claimed(1, holds), nil, untouched, nil},
		{"a claim that still holds while AO starts its worker", claimed(1, holds), session(false, false), untouched, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.add(c.entry)
			if c.session != nil {
				f.store.addSession(*c.session)
			}

			if err := f.svc.recover(context.Background(), t0, &f.store.settings); err != nil {
				t.Fatalf("recover = %v", err)
			}

			c.check(t, f.store.entry("entry-1"))
			if kills := f.sessions.killed(); !reflect.DeepEqual(kills, c.kills) {
				t.Fatalf("stopped %v, want %v", kills, c.kills)
			}
			if links := f.store.linked(); len(links) != 0 {
				t.Fatalf("linked %+v, want nothing", links)
			}
		})
	}

	t.Run("the next attempt follows in the same pass", func(t *testing.T) {
		f := newFixture(t)
		f.store.add(claimed(1, expired))

		f.tick(t, t0)

		if spawned := f.sessions.spawned(); len(spawned) != 1 || spawned[0].ClientRequestID != "lado:entry-1/2" {
			t.Fatalf("spawned %+v, want the second attempt's worker", spawned)
		}
	})

	// A claim that could not be settled may hide a worker, so the pass claims
	// nothing.
	t.Run("a claim recovery cannot settle", func(t *testing.T) {
		f := newFixture(t)
		f.store.add(claimed(1, expired), testEntry("entry-2", t0.Add(-30*time.Minute)))
		f.store.sessionErr = errors.New("database is locked")

		if err := f.svc.Tick(context.Background(), t0); err == nil || !strings.Contains(err.Error(), "database is locked") {
			t.Fatalf("Tick = %v, want the failed read", err)
		}
		if limits, spawned := f.store.claimLimits(), f.sessions.spawned(); len(limits) != 0 || len(spawned) != 0 {
			t.Fatalf("claimed with %v and spawned %d workers, want neither", limits, len(spawned))
		}
		if e := f.store.entry("entry-1"); e.LeaseToken != "old-lease" {
			t.Fatalf("entry = %+v, want its claim kept", e)
		}
	})
}

// AO can fail a spawn after it started the session; the attempt's session is
// the execution then, and no second attempt starts another worker.
func TestDispatchLinksTheSessionOfAFailedSpawn(t *testing.T) {
	lost := apierr.Internal("SPAWN_INTERNAL", "read the started session: context deadline exceeded")
	for _, c := range []struct {
		name      string
		committed bool
	}{
		{"AO finished starting it", true},
		{"AO never finished starting it", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.add(testEntry("entry-1", t0.Add(-10*time.Minute)))
			f.sessions.errs = []error{lost}
			f.sessions.onSpawn = func(cfg ports.SpawnConfig) {
				f.store.addSession(domain.SessionRecord{
					ID: "proj-1-4", ProjectID: "proj-1", Kind: cfg.Kind, ClientRequestID: cfg.ClientRequestID,
					ClientRequestHash: cfg.ClientRequestHash, ClientRequestCommitted: c.committed,
				})
			}

			f.tick(t, t0)

			e, links := f.store.entry("entry-1"), f.store.linked()
			if !c.committed {
				if len(links) != 0 || e.WaitingReason != domain.LadoWaitingRetry || e.AttemptCount != 1 {
					t.Fatalf("entry %+v with links %+v, want the attempt failed and retried", e, links)
				}
				return
			}
			if len(links) != 1 || links[0].SessionID != "proj-1-4" || e.DispatchedSessionID != "proj-1-4" {
				t.Fatalf("entry %+v with links %+v, want proj-1-4 linked", e, links)
			}
			if !strings.Contains(f.logs.String(), "SPAWN_INTERNAL") {
				t.Fatalf("the logs do not name the spawn's error:\n%s", f.logs.String())
			}
		})
	}
}

// B3: a session under an attempt's client request id that the dispatcher did
// not start for that entry, of another project, with another dispatch hash,
// or not a worker, is not the entry's execution. It is neither linked nor
// stopped, and the attempt counts as failed, after a failed spawn and in
// recovery alike.
func TestForeignSessionIsNotAdopted(t *testing.T) {
	foreign := map[string]func(*domain.SessionRecord){
		"of another project":      func(rec *domain.SessionRecord) { rec.ProjectID = "proj-9" },
		"with another hash":       func(rec *domain.SessionRecord) { rec.ClientRequestHash = "0f0f" },
		"an orchestrator":         func(rec *domain.SessionRecord) { rec.Kind = domain.KindOrchestrator },
		"without a dispatch hash": func(rec *domain.SessionRecord) { rec.ClientRequestHash = "" },
	}
	session := func(id string, change func(*domain.SessionRecord)) domain.SessionRecord {
		rec := domain.SessionRecord{
			ID: domain.SessionID("proj-9-" + id), ProjectID: "proj-1", Kind: domain.KindWorker, ClientRequestID: "lado:entry-1/1",
			ClientRequestHash: testDispatchHash(), ClientRequestCommitted: true, CreatedAt: t0.Add(-3 * time.Minute),
		}
		change(&rec)
		return rec
	}
	retried := func(t *testing.T, f *fixture) {
		t.Helper()
		if links := f.store.linked(); len(links) != 0 {
			t.Fatalf("linked %+v, want nothing", links)
		}
		if kills := f.sessions.killed(); len(kills) != 0 {
			t.Fatalf("stopped %v, want the other session left alone", kills)
		}
		e := f.store.entry("entry-1")
		if e.AttemptCount != 1 || e.WaitingReason != domain.LadoWaitingRetry || e.LeaseToken != "" || e.DispatchedSessionID != "" ||
			!e.NextAttemptAt.Equal(t0.Add(30*time.Second)) {
			t.Fatalf("entry = %+v, want its first attempt failed and the next one in 30 seconds", e)
		}
	}
	for name, change := range foreign {
		t.Run("after a failed spawn, a session "+name, func(t *testing.T) {
			f := newFixture(t)
			f.store.add(testEntry("entry-1", t0.Add(-10*time.Minute)))
			f.sessions.errs = []error{apierr.Conflict("CLIENT_REQUEST_CONFLICT", "The client request id was used with another request.", nil)}
			f.sessions.onSpawn = func(ports.SpawnConfig) { f.store.addSession(session("4", change)) }

			f.tick(t, t0)
			retried(t, f)
		})
		t.Run("in recovery, a session "+name, func(t *testing.T) {
			f := newFixture(t)
			e := testEntry("entry-1", t0.Add(-time.Hour))
			e.LeaseToken, e.ClaimedAt, e.LeaseExpiresAt, e.AttemptCount = "old-lease", t0.Add(-3*time.Minute), t0.Add(-time.Minute), 1
			f.store.add(e)
			f.store.addSession(session("7", change))

			if err := f.svc.recover(context.Background(), t0, &f.store.settings); err != nil {
				t.Fatalf("recover = %v, want the attempt failed without an error", err)
			}
			retried(t, f)
		})
	}
}

// B5: an attempt's session that never finished starting and could not be
// stopped keeps the entry's claim, so no next attempt starts a second worker
// beside it; the next pass tries again.
func TestRecoveryKeepsTheLeaseWhenTheStopFails(t *testing.T) {
	f := newFixture(t)
	e := testEntry("entry-1", t0.Add(-time.Hour))
	e.LeaseToken, e.ClaimedAt, e.LeaseExpiresAt, e.AttemptCount = "old-lease", t0.Add(-3*time.Minute), t0.Add(-time.Minute), 1
	f.store.add(e, testEntry("entry-2", t0.Add(-30*time.Minute)))
	f.store.addSession(domain.SessionRecord{
		ID: "proj-1-7", ProjectID: "proj-1", Kind: domain.KindWorker, ClientRequestID: "lado:entry-1/1",
		ClientRequestHash: testDispatchHash(), CreatedAt: t0.Add(-3 * time.Minute),
	})
	f.sessions.setKillErr("proj-1-7", apierr.Internal("SESSION_KILL_FAILED", "The runtime did not stop"))

	if err := f.svc.Tick(context.Background(), t0); err == nil || !strings.Contains(err.Error(), "SESSION_KILL_FAILED") {
		t.Fatalf("Tick = %v, want the failed stop", err)
	}
	if got := f.store.entry("entry-1"); got.LeaseToken != "old-lease" || got.AttemptCount != 1 {
		t.Fatalf("entry = %+v, want its claim kept", got)
	}
	if limits := f.store.claimLimits(); len(limits) != 0 {
		t.Fatalf("claimed with %v, want no claim while a worker may run", limits)
	}

	// The stop works on the next pass: the entry is due again and its next
	// attempt starts.
	f.sessions.setKillErr("proj-1-7", nil)
	f.tick(t, t0.Add(15*time.Second))
	if kills := f.sessions.killed(); !reflect.DeepEqual(kills, []domain.SessionID{"proj-1-7", "proj-1-7"}) {
		t.Fatalf("stopped %v, want proj-1-7 twice", kills)
	}
	if spawned := f.sessions.spawned(); len(spawned) != 1 || spawned[0].ClientRequestID != "lado:entry-1/2" {
		t.Fatalf("spawned %+v, want entry-1's second attempt", spawned)
	}
}
