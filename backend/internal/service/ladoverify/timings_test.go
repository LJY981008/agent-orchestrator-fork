package ladoverify

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// verifyTimings is the stage times and session metrics part of verifyStore.
// It keeps the store's rules: the first PR sighting and the first
// verification stay. err fails every write and read of it.
type verifyTimings struct {
	prSeen         map[domain.SessionID]time.Time
	prSeenWrites   int
	verified       map[domain.SessionID]time.Time
	verifiedWrites int
	// others are executions of queue entries the loop does not verify, such
	// as part workers; activities are the sessions' activities.
	others     []domain.LadoExecution
	activities map[domain.SessionID][]domain.LadoActivityFact
	metrics    map[domain.SessionID]domain.LadoSessionMetrics
	err        error
}

func (s *verifyStore) MarkLadoExecutionPRSeen(_ context.Context, session domain.SessionID, prURL string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timings.err != nil {
		return s.timings.err
	}
	if prURL == "" || at.IsZero() {
		return errors.New("a PR and a time are required")
	}
	s.timings.prSeenWrites++
	if s.timings.prSeen == nil {
		s.timings.prSeen = map[domain.SessionID]time.Time{}
	}
	if _, ok := s.timings.prSeen[session]; !ok {
		s.timings.prSeen[session] = at
	}
	return nil
}

func (s *verifyStore) MarkLadoExecutionVerified(_ context.Context, session domain.SessionID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timings.err != nil {
		return s.timings.err
	}
	s.timings.verifiedWrites++
	if s.timings.verified == nil {
		s.timings.verified = map[domain.SessionID]time.Time{}
	}
	if _, ok := s.timings.verified[session]; !ok {
		s.timings.verified[session] = at
	}
	return nil
}

func (s *verifyStore) LadoExecutionsForEntry(_ context.Context, entryID domain.LadoQueueEntryID) ([]domain.LadoExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timings.err != nil {
		return nil, s.timings.err
	}
	var out []domain.LadoExecution
	for _, exec := range append(slices.Clone(s.executions), s.timings.others...) {
		if exec.QueueEntryID == entryID {
			out = append(out, exec)
		}
	}
	return out, nil
}

func (s *verifyStore) LadoSessionActivityFacts(_ context.Context, session domain.SessionID) ([]domain.LadoActivityFact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timings.err != nil {
		return nil, s.timings.err
	}
	return slices.Clone(s.timings.activities[session]), nil
}

func (s *verifyStore) UpsertLadoSessionMetrics(_ context.Context, m domain.LadoSessionMetrics) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timings.err != nil {
		return s.timings.err
	}
	if m.SessionID == "" || m.TaskID == "" || !m.Role.Valid() {
		return errors.New("a session, a task and a known role are required")
	}
	if s.timings.metrics == nil {
		s.timings.metrics = map[domain.SessionID]domain.LadoSessionMetrics{}
	}
	s.timings.metrics[m.SessionID] = m
	return nil
}

func (s *verifyStore) timingsOf(session domain.SessionID) (prSeen, verified time.Time, prSeenWrites, verifiedWrites int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timings.prSeen[session], s.timings.verified[session], s.timings.prSeenWrites, s.timings.verifiedWrites
}

func (s *verifyStore) metricsOf(session domain.SessionID) (domain.LadoSessionMetrics, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.timings.metrics[session]
	return m, ok
}

// The loop records when it first saw an execution's PR, and when a head of
// it first passed or asked for confirmation; a wait or a fail records
// nothing, and later passes keep the first times.
func TestTickRecordsWhenThePRWasSeenAndFirstVerified(t *testing.T) {
	f := newVerifyFixture(t)
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}}, 7)
	exec := f.addExecution("1", verifyHead1)
	seen := f.clock.Now()

	f.tick() // unit runs: the head waits
	if outcome, _ := f.record(exec.SessionID, verifyHead1).Overall(); outcome != domain.LadoVerifyWait {
		t.Fatalf("outcome while unit runs = %s, want wait", outcome)
	}
	if prSeen, verified, _, _ := f.store.timingsOf(exec.SessionID); !prSeen.Equal(seen) || !verified.IsZero() {
		t.Fatalf("after a wait: PR seen %s, verified %s; want seen now and not verified", prSeen, verified)
	}

	f.finish(CommandResult{Status: domain.LadoCheckRunFailed, ExitCode: verifyExit(1), Output: []byte("--- FAIL")})
	f.clock.advance(time.Minute)
	f.tick()
	if outcome, _ := f.record(exec.SessionID, verifyHead1).Overall(); outcome != domain.LadoVerifyFail {
		t.Fatalf("outcome after unit failed = %s, want fail", outcome)
	}
	if _, verified, _, _ := f.store.timingsOf(exec.SessionID); !verified.IsZero() {
		t.Fatalf("a failing head was recorded as verified at %s", verified)
	}

	f.push(exec, verifyHead2)
	f.clock.advance(time.Minute)
	f.tick() // unit runs on the new head
	f.finish(CommandResult{Status: domain.LadoCheckRunPassed, ExitCode: verifyExit(0), Output: []byte("ok")})
	f.clock.advance(time.Minute)
	passed := f.clock.Now()
	f.tick()
	if outcome, _ := f.record(exec.SessionID, verifyHead2).Overall(); outcome != domain.LadoVerifyPass {
		t.Fatalf("outcome after unit passed = %s, want pass", outcome)
	}

	f.clock.advance(time.Minute)
	f.tick()
	f.clock.advance(time.Minute)
	f.tick()
	prSeen, verified, prSeenWrites, verifiedWrites := f.store.timingsOf(exec.SessionID)
	if !prSeen.Equal(seen) || !verified.Equal(passed) {
		t.Errorf("PR seen %s, verified %s; want %s and %s, the first times", prSeen, verified, seen, passed)
	}
	if prSeenWrites != 1 || verifiedWrites != 1 {
		t.Errorf("writes: PR seen %d, verified %d; want one each over every pass", prSeenWrites, verifiedWrites)
	}

	// A head that asks for confirmation verifies too.
	confirm := f.addExecution("2", verifyHead3)
	f.store.setChecks(nil, 7)
	f.tick()
	if outcome, _ := f.record(confirm.SessionID, verifyHead3).Overall(); outcome != domain.LadoVerifyConfirm {
		t.Fatalf("outcome without commands = %s, want confirm", outcome)
	}
	if _, verified, _, _ := f.store.timingsOf(confirm.SessionID); !verified.Equal(f.clock.Now()) {
		t.Errorf("a confirming head verified at %s, want now", verified)
	}
}

// Settling a PR stores the investigation metrics of every session of its
// queue entry and of no other, and a PR merged before the loop saw it open
// is seen at the settle.
func TestSettleStoresTheEntrySessionMetrics(t *testing.T) {
	f := newVerifyFixture(t)
	integration := f.addExecution("1", verifyHead1)
	f.store.update(integration.SessionID, func(e *domain.LadoExecution) {
		e.QueueEntryID, e.Role, e.PR.Merged = "entry-1", domain.LadoRoleIntegration, true
	})
	other := f.addExecution("2", verifyHead3)
	f.store.update(other.SessionID, func(e *domain.LadoExecution) { e.QueueEntryID = "entry-2" })
	part := domain.LadoExecution{SessionID: "lado-1-part", TaskID: integration.TaskID, QueueEntryID: "entry-1", Role: domain.LadoRolePart, PartID: "store"}
	at := func(seconds int) time.Time { return t0.Add(time.Duration(seconds) * time.Second) }
	f.store.mu.Lock()
	f.store.timings.others = []domain.LadoExecution{part}
	f.store.timings.activities = map[domain.SessionID][]domain.LadoActivityFact{
		part.SessionID: {
			{Kind: domain.ActivityKindCommand, Command: "rg Settle", CreatedAt: at(10), UpdatedAt: at(11)},
			{Kind: domain.ActivityKindMCPTool, ToolKind: "read", ToolName: "Read", CreatedAt: at(20), UpdatedAt: at(21)},
			{Kind: domain.ActivityKindFileChange, CreatedAt: at(70), UpdatedAt: at(71)},
		},
	}
	f.store.mu.Unlock()

	f.tick()
	if _, ok := f.store.settledAt(integration.SessionID); !ok {
		t.Fatal("the merged PR was not settled")
	}
	m, ok := f.store.metricsOf(part.SessionID)
	if !ok || m.TaskID != integration.TaskID || m.Role != domain.LadoSessionPart || m.ToolCalls != 3 || m.SearchCalls != 1 ||
		m.ReadCalls != 1 || m.EditCalls != 1 || m.Investigate != time.Minute || !m.UpdatedAt.Equal(f.clock.Now()) {
		t.Errorf("part metrics = %+v, %v", m, ok)
	}
	if m, ok := f.store.metricsOf(integration.SessionID); !ok || m.Role != domain.LadoSessionIntegration || m.ToolCalls != 0 {
		t.Errorf("integration metrics = %+v, %v; want a row without calls", m, ok)
	}
	if _, ok := f.store.metricsOf(other.SessionID); ok {
		t.Error("an open PR of another entry got metrics")
	}
	if prSeen, _, _, _ := f.store.timingsOf(integration.SessionID); !prSeen.Equal(f.clock.Now()) {
		t.Errorf("a PR merged before the loop saw it was seen at %s, want the settle", prSeen)
	}
}

// A timing record the store cannot keep is logged: the pass verifies and
// settles as before.
func TestTimingFailuresDoNotStopVerification(t *testing.T) {
	f := newVerifyFixture(t)
	f.store.timings.err = errors.New("disk full")
	open := f.addExecution("1", verifyHead1)
	merged := f.addExecution("2", verifyHead3)
	f.store.update(merged.SessionID, func(e *domain.LadoExecution) { e.QueueEntryID, e.PR.Merged = "entry-2", true })

	if err := f.svc.verifyExecution(f.ctx, f.store.executions[0]); err != nil {
		t.Fatalf("verifyExecution = %v, want the record stored", err)
	}
	if outcome, _ := f.record(open.SessionID, verifyHead1).Overall(); outcome != domain.LadoVerifyConfirm {
		t.Errorf("outcome = %s, want confirm", outcome)
	}
	if err := f.svc.settle(f.ctx, f.store.executions[1]); err != nil {
		t.Fatalf("settle = %v, want the PR settled", err)
	}
	if _, ok := f.store.settledAt(merged.SessionID); !ok {
		t.Error("the merged PR was not settled")
	}
	f.tick()

	// Once the store keeps them again, the next pass records them.
	f.store.mu.Lock()
	f.store.timings.err = nil
	f.store.mu.Unlock()
	f.clock.advance(time.Minute)
	f.tick()
	if prSeen, verified, _, _ := f.store.timingsOf(open.SessionID); !prSeen.Equal(f.clock.Now()) || !verified.Equal(f.clock.Now()) {
		t.Errorf("after the store recovered: PR seen %s, verified %s; want now", prSeen, verified)
	}
}
