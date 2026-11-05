package ladoqueue

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// While the account a worker would start on is at its usage limit, its entry
// waits, without spending an attempt, until the reset; then it starts.
func TestAWorkerWaitsForItsAccountsLimitToReset(t *testing.T) {
	f := newFixture(t)
	limits := &fakeAccountLimits{resetAt: t0.Add(90 * time.Minute)}
	f.svc.deps.AccountLimits = limits
	f.store.add(testEntry("e1", t0.Add(-time.Minute)))

	f.tick(t, t0)
	e := f.store.entry("e1")
	if len(f.sessions.spawned()) != 0 {
		t.Fatalf("spawned %v during the limit", spawnIDs(f.sessions.spawned()))
	}
	// It checks again in a minute, not only at the reset.
	if e.WaitingReason != domain.LadoWaitingAccountLimited || e.AttemptCount != 0 || !e.NextAttemptAt.Equal(t0.Add(recheckAfter)) ||
		e.WaitingDetail != limits.resetAt.Format(time.RFC3339) {
		t.Fatalf("entry = %+v, want it waiting for the reset without an attempt", e)
	}
	for at := t0.Add(recheckAfter); at.Before(limits.resetAt); at = at.Add(30 * time.Minute) {
		f.tick(t, at)
	}
	if e := f.store.entry("e1"); len(f.sessions.spawned()) != 0 || e.AttemptCount != 0 {
		t.Fatalf("spawned %d, entry %+v before the reset", len(f.sessions.spawned()), e)
	}
	f.tick(t, limits.resetAt)
	spawned := f.sessions.spawned()
	if len(spawned) != 1 || spawned[0].ClientRequestID != domain.LadoClientRequestID("e1", 1) {
		t.Fatalf("spawned %v after the reset, want the entry's first attempt", spawnIDs(spawned))
	}
}

// A limit that lifts before its reset, as when a turn on the account
// completes or the owner picks another default account, lets the entry start
// on the next check, not at the reset it read.
func TestAWorkerStartsOnceItsAccountsLimitLiftsEarly(t *testing.T) {
	f := newFixture(t)
	limits := &fakeAccountLimits{resetAt: t0.Add(72 * time.Hour)}
	f.svc.deps.AccountLimits = limits
	f.store.add(testEntry("e1", t0.Add(-time.Minute)))

	f.tick(t, t0)
	if len(f.sessions.spawned()) != 0 {
		t.Fatalf("spawned %v during the limit", spawnIDs(f.sessions.spawned()))
	}
	limits.release()
	f.tick(t, t0.Add(recheckAfter))
	spawned := f.sessions.spawned()
	if len(spawned) != 1 || spawned[0].ClientRequestID != domain.LadoClientRequestID("e1", 1) {
		t.Fatalf("spawned %v a minute after the limit lifted, want the entry's first attempt", spawnIDs(spawned))
	}
}

// An agent that cannot start holds the entry a minute at a time without an
// attempt, and raises the task's alert once.
func TestAWorkerWaitsWhileItsAgentCannotStart(t *testing.T) {
	f := newFixture(t)
	preflight := &fakePreflight{err: fmt.Errorf("claude-code is not installed: %w", ports.ErrAgentBinaryNotFound)}
	f.svc.deps.Preflight = preflight
	f.store.add(testEntry("e1", t0.Add(-time.Minute)))

	f.tick(t, t0)
	e := f.store.entry("e1")
	if len(f.sessions.spawned()) != 0 || e.WaitingReason != domain.LadoWaitingAgentUnavailable || e.AttemptCount != 0 ||
		!e.NextAttemptAt.Equal(t0.Add(recheckAfter)) || e.WaitingDetail != string(domain.HarnessClaudeCode) {
		t.Fatalf("entry = %+v, spawned %d; want it waiting for the agent", e, len(f.sessions.spawned()))
	}
	f.tick(t, t0.Add(recheckAfter))
	if e := f.store.entry("e1"); e.AttemptCount != 0 || preflight.count() != 2 {
		t.Fatalf("entry = %+v after %d checks", e, preflight.count())
	}
	alerts := f.store.raised()
	if len(alerts) != 1 || alerts[0].Kind != domain.LadoAlertAgentUnavailable || alerts[0].TaskID != "task-1" || alerts[0].SessionID != "" {
		t.Fatalf("alerts = %+v, want one for the task", alerts)
	}

	preflight.set(nil)
	f.tick(t, t0.Add(2*recheckAfter))
	if spawned := f.sessions.spawned(); len(spawned) != 1 || spawned[0].ClientRequestID != domain.LadoClientRequestID("e1", 1) {
		t.Fatalf("spawned %v once the agent was back", spawnIDs(spawned))
	}
}

// The limit is read before the agent is checked: a limited account does not
// run the agent's check.
func TestTheLimitIsReadBeforeTheAgentIsChecked(t *testing.T) {
	f := newFixture(t)
	f.svc.deps.AccountLimits = &fakeAccountLimits{resetAt: t0.Add(time.Hour)}
	preflight := &fakePreflight{}
	f.svc.deps.Preflight = preflight
	f.store.add(testEntry("e1", t0.Add(-time.Minute)))
	f.tick(t, t0)
	if preflight.count() != 0 {
		t.Fatalf("checked the agent %d times during the limit", preflight.count())
	}
	f.tick(t, t0.Add(time.Hour))
	if preflight.count() != 1 || len(f.sessions.spawned()) != 1 {
		t.Fatalf("checks %d, spawned %d after the reset", preflight.count(), len(f.sessions.spawned()))
	}
}

// A part, merge or integration worker waits for its account's limit too; the
// part records why, spends no attempt, and starts after the reset.
func TestPartsWaitForTheirAccountsLimit(t *testing.T) {
	pf := newPartsFixture(t)
	limits := &fakeAccountLimits{}
	pf.svc.deps.AccountLimits = limits
	pf.tick(t, t0)
	parts := pf.parts(t, storeEntry)
	if parts["a"].State != domain.LadoPartRunning || parts["b"].State != domain.LadoPartRunning {
		t.Fatalf("parts = %+v, want a and b running", parts)
	}

	// a reports done while its account hits its limit: c, which waits for
	// a, waits for the reset once a merges.
	commitOn(t, pf.repo, "lado/task-1/1/a", map[string]string{"svc/a.go": "package svc\n"})
	pf.reportDone(t, parts["a"].SessionID, "added the service")
	limits.resetAt = t0.Add(3 * time.Hour)
	spawns := len(pf.sessions.spawned())
	pf.tick(t, t0.Add(time.Minute))
	c := pf.parts(t, storeEntry)["c"]
	if c.State != domain.LadoPartWaiting || c.WaitingReason != domain.LadoWaitingAccountLimited || c.Attempts != 0 ||
		c.WaitingDetail != limits.resetAt.Format(time.RFC3339) || len(pf.sessions.spawned()) != spawns {
		t.Fatalf("part c = %+v, spawned %d; want it waiting for the reset", c, len(pf.sessions.spawned())-spawns)
	}

	pf.tick(t, limits.resetAt)
	c = pf.parts(t, storeEntry)["c"]
	if c.State != domain.LadoPartRunning || c.Attempts != 1 || c.WaitingReason != "" || len(pf.sessions.spawned()) != spawns+1 {
		t.Fatalf("part c = %+v after the reset, want its first worker running", c)
	}
}

// An agent that cannot start holds a part without spending its attempt, and
// one pass checks the agent once for all of its starts.
func TestPartsWaitWhileTheirAgentCannotStart(t *testing.T) {
	pf := newPartsFixture(t)
	// The entry's own check passes; the parts' check, later in the pass,
	// fails.
	preflight := &fakePreflight{next: []error{nil}, err: errors.New("the agent does not run")}
	pf.svc.deps.Preflight = preflight
	pf.tick(t, t0)
	parts := pf.parts(t, storeEntry)
	for _, id := range []string{"a", "b"} {
		if p := parts[id]; p.State != domain.LadoPartWaiting || p.WaitingReason != domain.LadoWaitingAgentUnavailable || p.Attempts != 0 {
			t.Fatalf("part %s = %+v, want it waiting for the agent", id, p)
		}
	}
	if len(pf.sessions.spawned()) != 0 {
		t.Fatalf("spawned %v", spawnIDs(pf.sessions.spawned()))
	}
	if got := preflight.count(); got != 2 {
		t.Fatalf("checked the agent %d times, want once for the entry and once for the parts pass", got)
	}
	preflight.set(nil)
	pf.tick(t, t0.Add(time.Minute))
	parts = pf.parts(t, storeEntry)
	if parts["a"].State != domain.LadoPartRunning || parts["a"].Attempts != 1 || parts["b"].State != domain.LadoPartRunning {
		t.Fatalf("parts = %+v once the agent was back", parts)
	}
}

// The integration worker waits for its account's limit too, without
// spending one of its starts.
func TestTheIntegrationWorkerWaitsForItsAccountsLimit(t *testing.T) {
	pf := newPartsFixture(t)
	limits := &fakeAccountLimits{}
	pf.svc.deps.AccountLimits = limits
	pf.tick(t, t0)
	parts := pf.parts(t, storeEntry)
	pf.commitParts(t, "a", "b")
	pf.reportDone(t, parts["a"].SessionID, "nothing")
	pf.reportDone(t, parts["b"].SessionID, "nothing")
	pf.tick(t, t0.Add(time.Minute))
	pf.commitParts(t, "c")
	pf.reportDone(t, pf.parts(t, storeEntry)["c"].SessionID, "nothing")
	limits.resetAt = t0.Add(time.Hour)
	pf.tick(t, t0.Add(2*time.Minute))
	if spawns := pf.sessions.spawned(); len(spawns) != 3 {
		t.Fatalf("spawned %v during the limit, want no integration worker", spawnIDs(spawns))
	}
	run, _, _ := pf.db.LadoPartRun(context.Background(), storeEntry)
	if run.IntegrationAttempts != 0 {
		t.Fatalf("the integration worker spent %d starts during the limit", run.IntegrationAttempts)
	}
	pf.tick(t, limits.resetAt)
	if spawns := pf.sessions.spawned(); len(spawns) != 4 || spawns[3].ClientRequestID != "lado:lado-queue-1/integration/1" {
		t.Fatalf("spawned %v after the reset, want the integration worker's first start", spawnIDs(spawns))
	}
}
