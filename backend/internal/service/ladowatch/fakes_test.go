package ladowatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// The real store has every read and write the watch needs, and AO's
// conversation store is its Conversations.
var (
	_ Store         = (*store.Store)(nil)
	_ Conversations = (*store.Store)(nil)
)

// t0 is the fake clock's start: 2026-10-29 16:33 in Seoul, when a worker hit
// "You've hit your session limit · resets 8:20pm (Asia/Seoul)".
var t0 = time.Date(2026, 10, 29, 7, 33, 0, 0, time.UTC)

type fakeStore struct {
	mu       sync.Mutex
	execs    []domain.LadoExecution
	prs      map[domain.SessionID]domain.LadoExecutionPR
	tasks    map[domain.LadoTaskID]domain.LadoTask
	reports  map[domain.SessionID]domain.ReportState
	settings map[domain.ProjectID]domain.LadoWatchSettings
	watches  map[domain.SessionID]domain.LadoSessionWatch
	limits   map[string]domain.LadoAccountLimit
	defaults map[domain.AgentAccountProvider]string
	alerts   []domain.LadoAlert
	audits   []domain.LadoAuditEvent
	// failLimitWrite fails the next limit watch writes.
	failLimitWrite int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		prs:      map[domain.SessionID]domain.LadoExecutionPR{},
		tasks:    map[domain.LadoTaskID]domain.LadoTask{},
		reports:  map[domain.SessionID]domain.ReportState{},
		settings: map[domain.ProjectID]domain.LadoWatchSettings{},
		watches:  map[domain.SessionID]domain.LadoSessionWatch{},
		limits:   map[string]domain.LadoAccountLimit{},
		defaults: map[domain.AgentAccountProvider]string{},
	}
}

func (f *fakeStore) LiveLadoExecutions(context.Context) ([]domain.LadoExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.LadoExecution(nil), f.execs...), nil
}

func (f *fakeStore) LadoExecution(_ context.Context, id domain.SessionID) (domain.LadoExecution, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.execs {
		if e.SessionID == id {
			e.PR = f.prs[id]
			return e, true, nil
		}
	}
	return domain.LadoExecution{}, false, nil
}

func (f *fakeStore) LadoTask(_ context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	return t, ok, nil
}

func (f *fakeStore) LatestSessionReport(_ context.Context, id domain.SessionID) (domain.ReportState, string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.reports[id]
	return r, "", ok, nil
}

func (f *fakeStore) LadoEffectiveWatchSettings(_ context.Context, p domain.ProjectID) (domain.LadoWatchSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.settings[p]; ok {
		return s, nil
	}
	return domain.LadoWatchDefaults(), nil
}

func (f *fakeStore) EnsureLadoSessionWatch(_ context.Context, s domain.SessionID, task domain.LadoTaskID, at time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.watches[s]; ok {
		return false, nil
	}
	f.watches[s] = domain.LadoSessionWatch{SessionID: s, TaskID: task, CreatedAt: at, UpdatedAt: at}
	return true, nil
}

func (f *fakeStore) LadoSessionWatch(_ context.Context, s domain.SessionID) (domain.LadoSessionWatch, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.watches[s]
	return w, ok, nil
}

func (f *fakeStore) SetLadoSessionWatchLimit(_ context.Context, s domain.SessionID, l domain.LadoWatchLimit, at time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failLimitWrite > 0 {
		f.failLimitWrite--
		return false, errors.New("write failed")
	}
	w, ok := f.watches[s]
	if !ok {
		return false, nil
	}
	w.LadoWatchLimit, w.UpdatedAt = l, at
	f.watches[s] = w
	return true, nil
}

func (f *fakeStore) SetLadoSessionWatchStall(_ context.Context, s domain.SessionID, st domain.LadoWatchStall, at time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.watches[s]
	if !ok {
		return false, nil
	}
	w.LadoWatchStall, w.UpdatedAt = st, at
	f.watches[s] = w
	return true, nil
}

func (f *fakeStore) RecordLadoAccountLimit(_ context.Context, l domain.LadoAccountLimit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if old, ok := f.limits[l.AccountKey]; ok {
		l.DetectedAt = old.DetectedAt
	}
	f.limits[l.AccountKey] = l
	return nil
}

func (f *fakeStore) ReleaseLadoAccountLimit(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.limits[key]
	delete(f.limits, key)
	return ok, nil
}

func (f *fakeStore) LadoAccountLimit(_ context.Context, key string) (domain.LadoAccountLimit, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.limits[key]
	return l, ok, nil
}

func (f *fakeStore) ListLadoAccountLimits(context.Context) ([]domain.LadoAccountLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LadoAccountLimit
	for _, l := range f.limits {
		out = append(out, l)
	}
	return out, nil
}

func (f *fakeStore) GetAgentAccountDefault(_ context.Context, p domain.AgentAccountProvider) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.defaults[p]
	return id, ok, nil
}

func (f *fakeStore) AddLadoAlert(_ context.Context, a domain.LadoAlert) (domain.LadoAlertID, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, old := range f.alerts {
		if old.TaskID == a.TaskID && old.SessionID == a.SessionID && old.Kind == a.Kind && old.Level == a.Level {
			return 0, false, nil
		}
	}
	a.ID = domain.LadoAlertID(len(f.alerts) + 1)
	f.alerts = append(f.alerts, a)
	return a.ID, true, nil
}

func (f *fakeStore) AppendLadoAuditEvent(_ context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, ev)
	return ev, nil
}

func (f *fakeStore) alertKinds() []domain.LadoAlertKind {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kinds []domain.LadoAlertKind
	for _, a := range f.alerts {
		kinds = append(kinds, a.Kind)
	}
	return kinds
}

func (f *fakeStore) auditCount(action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, ev := range f.audits {
		if ev.Action == action {
			n++
		}
	}
	return n
}

type fakeSessions struct {
	mu       sync.Mutex
	sessions map[domain.SessionID]domain.SessionRecord
}

func (f *fakeSessions) Session(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	return s, ok, nil
}

func (f *fakeSessions) set(rec domain.SessionRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[rec.ID] = rec
}

type fakeConversations struct {
	mu    sync.Mutex
	turns map[domain.SessionID]domain.ConversationTurn
}

func (f *fakeConversations) ConversationForSession(_ context.Context, s domain.SessionID) (domain.ConversationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.turns[s]; !ok {
		return domain.ConversationRecord{}, store.ErrConversationNotFound
	}
	return domain.ConversationRecord{ID: "conv-" + string(s)}, nil
}

func (f *fakeConversations) LatestConversationTurn(_ context.Context, conv string) (domain.ConversationTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.turns[domain.SessionID(conv[len("conv-"):])]
	if !ok {
		return domain.ConversationTurn{}, domain.ErrNoConversationTurn
	}
	return t, nil
}

func (f *fakeConversations) set(s domain.SessionID, t domain.ConversationTurn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turns[s] = t
}

type fakeAccounts struct{ ids map[domain.SessionID]string }

func (f fakeAccounts) SessionAccountID(_ context.Context, id domain.SessionID, _ domain.AgentHarness) (string, error) {
	return f.ids[id], nil
}

type sent struct {
	to   domain.SessionID
	text string
}

type fakeMessenger struct {
	mu   sync.Mutex
	sent []sent
	fail error
	// trace records the calls of the tick, shared with the counter.
	trace *[]string
}

func (f *fakeMessenger) Send(_ context.Context, id domain.SessionID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.trace != nil {
		*f.trace = append(*f.trace, "send")
	}
	if f.fail != nil {
		return f.fail
	}
	f.sent = append(f.sent, sent{id, text})
	return nil
}

func (f *fakeMessenger) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.sent {
		out = append(out, s.text)
	}
	return out
}

type fakeCounter struct {
	calls int
	trace *[]string
}

func (f *fakeCounter) CountCompactions(context.Context) error {
	f.calls++
	if f.trace != nil {
		*f.trace = append(*f.trace, "count")
	}
	return nil
}

// fixture is a watch over fakes with one running worker, "s-1", of task
// "task-1" in project "proj-1", resting since its last turn ended at t0.
type fixture struct {
	t        *testing.T
	now      time.Time
	store    *fakeStore
	sessions *fakeSessions
	convs    *fakeConversations
	msgs     *fakeMessenger
	counter  *fakeCounter
	svc      *Service
	trace    []string
}

const (
	worker1 domain.SessionID  = "s-1"
	task1   domain.LadoTaskID = "task-1"
	proj1   domain.ProjectID  = "proj-1"
)

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		t:        t,
		now:      t0,
		store:    newFakeStore(),
		sessions: &fakeSessions{sessions: map[domain.SessionID]domain.SessionRecord{}},
		convs:    &fakeConversations{turns: map[domain.SessionID]domain.ConversationTurn{}},
	}
	f.msgs = &fakeMessenger{trace: &f.trace}
	f.counter = &fakeCounter{trace: &f.trace}
	f.store.tasks[task1] = domain.LadoTask{ID: task1, ProjectID: proj1, IssueRef: "github:o/r#7"}
	f.store.execs = []domain.LadoExecution{{SessionID: worker1, TaskID: task1, Role: domain.LadoRolePart, PartID: "a"}}
	f.rest(t0)
	f.endTurn(domain.TurnStateCompleted, "", t0)
	f.svc = New(Deps{
		Store: f.store, Sessions: f.sessions, Conversations: f.convs, Messenger: f.msgs, CompactCounter: f.counter,
		Accounts: fakeAccounts{ids: map[domain.SessionID]string{}},
		Clock:    func() time.Time { return f.now },
		Local:    seoul(t),
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return f
}

func seoul(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatalf("load Asia/Seoul: %v", err)
	}
	return loc
}

// rest shows the worker idle since at.
func (f *fixture) rest(at time.Time) {
	f.sessions.set(domain.SessionRecord{
		ID: worker1, ProjectID: proj1, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: at},
	})
}

// work shows the worker active at at, with a turn running.
func (f *fixture) work(at time.Time) {
	f.sessions.set(domain.SessionRecord{
		ID: worker1, ProjectID: proj1, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: at},
	})
	started := at
	f.convs.set(worker1, domain.ConversationTurn{ID: "turn-run", State: domain.TurnStateRunning, RequestedAt: at, StartedAt: &started})
}

// endTurn makes the worker's newest turn one that ended at at.
func (f *fixture) endTurn(state domain.TurnState, errText string, at time.Time) {
	started, done := at.Add(-time.Minute), at
	f.convs.set(worker1, domain.ConversationTurn{
		ID: "turn-" + at.Format(time.RFC3339Nano), State: state, ErrorMessage: errText,
		RequestedAt: started, StartedAt: &started, CompletedAt: &done,
	})
}

// limitAt makes the worker stop at its session limit at at, resting.
func (f *fixture) limitAt(at time.Time, text string) {
	f.endTurn(domain.TurnStateFailed, text, at)
	f.rest(at)
}

func (f *fixture) tick(at time.Time) {
	f.t.Helper()
	f.now = at
	if err := f.svc.Tick(context.Background()); err != nil {
		f.t.Fatalf("tick at %s: %v", at, err)
	}
}

func (f *fixture) watch() domain.LadoSessionWatch {
	f.t.Helper()
	w, ok, _ := f.store.LadoSessionWatch(context.Background(), worker1)
	if !ok {
		f.t.Fatalf("no watch row for %s", worker1)
	}
	return w
}
