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
	notices  []domain.LadoOrchestratorNotice
	// ladoProjects are the LADO mode projects; waiting is what
	// CountLadoAccountLimitedWaits answers, and waitCounts how often it was
	// asked. records counts the account limit records.
	ladoProjects []domain.ProjectID
	waiting      int
	waitCounts   int
	records      int
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

// RecordLadoAccountLimit keeps the real upsert's rules: a record over a live
// limit keeps its detection time and its first known identity; one over a
// released or expired limit starts it again.
func (f *fakeStore) RecordLadoAccountLimit(_ context.Context, l domain.LadoAccountLimit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records++
	if old, ok := f.limits[l.AccountKey]; ok && old.ReleasedAt.IsZero() && old.ResetAt.After(l.DetectedAt) {
		l.DetectedAt = old.DetectedAt
		if old.Identity != "" {
			l.Identity = old.Identity
		}
	}
	l.ReleasedAt, l.ReleaseReason, l.ReleasedWaiting = time.Time{}, domain.LadoLimitReleaseNone, 0
	f.limits[l.AccountKey] = l
	return nil
}

func (f *fakeStore) MarkLadoAccountLimitReleased(_ context.Context, key string, reason domain.LadoLimitReleaseReason, at time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.limits[key]
	if !ok || !l.ReleasedAt.IsZero() {
		return false, nil
	}
	l.ReleasedAt, l.ReleaseReason, l.ReleasedWaiting, l.UpdatedAt = at, reason, 0, at
	f.limits[key] = l
	return true, nil
}

func (f *fakeStore) SetLadoAccountLimitReleasedWaiting(_ context.Context, key string, waiting int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l, ok := f.limits[key]; ok && !l.ReleasedAt.IsZero() {
		l.ReleasedWaiting = waiting
		f.limits[key] = l
	}
	return nil
}

func (f *fakeStore) CountLadoAccountLimitedWaits(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waitCounts++
	return f.waiting, nil
}

func (f *fakeStore) ListLadoModeProjectIDs(context.Context) ([]domain.ProjectID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.ProjectID(nil), f.ladoProjects...), nil
}

func (f *fakeStore) AddLadoOrchestratorNotice(_ context.Context, n domain.LadoOrchestratorNotice) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, old := range f.notices {
		if old.Key == n.Key {
			return false, nil
		}
	}
	f.notices = append(f.notices, n)
	return true, nil
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

// limit reads an account's limit.
func (f *fakeStore) limit(key string) (domain.LadoAccountLimit, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.limits[key]
	return l, ok
}

// noticeTexts lists the orchestrator notices' texts.
func (f *fakeStore) noticeTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, n := range f.notices {
		out = append(out, string(n.ProjectID)+" "+n.Text)
	}
	return out
}

// audited returns the audit events of action.
func (f *fakeStore) audited(action string) []domain.LadoAuditEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LadoAuditEvent
	for _, ev := range f.audits {
		if ev.Action == action {
			out = append(out, ev)
		}
	}
	return out
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

// fakeIdentities answers each account's login fingerprint, or fails.
type fakeIdentities struct {
	mu    sync.Mutex
	fps   map[string]string
	fail  error
	calls int
	// block, when set, holds every read until it is closed.
	block chan struct{}
	// entered gets a value as each read starts, when set.
	entered chan struct{}
}

func (f *fakeIdentities) AccountFingerprint(ctx context.Context, accountID string) (string, error) {
	f.mu.Lock()
	f.calls++
	block, entered := f.block, f.entered
	f.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return "", f.fail
	}
	return f.fps[accountID], nil
}

func (f *fakeIdentities) set(accountID, fp string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fps[accountID] = fp
}

func (f *fakeIdentities) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
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
func (f *fixture) rest(at time.Time) { f.restOf(worker1, at) }

// restOf shows session id idle since at, in its project and with the time it
// was made kept.
func (f *fixture) restOf(id domain.SessionID, at time.Time) {
	rec := f.record(id)
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: at}
	f.sessions.set(rec)
}

// record is session id's record, or a new Chat worker of proj1.
func (f *fixture) record(id domain.SessionID) domain.SessionRecord {
	rec, ok, _ := f.sessions.Session(context.Background(), id)
	if !ok {
		rec = domain.SessionRecord{ID: id, ProjectID: proj1, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat}
	}
	return rec
}

// work shows the worker active at at, with a turn running.
func (f *fixture) work(at time.Time) {
	rec := f.record(worker1)
	rec.Activity = domain.Activity{State: domain.ActivityActive, LastActivityAt: at}
	f.sessions.set(rec)
	started := at
	f.convs.set(worker1, domain.ConversationTurn{ID: "turn-run", State: domain.TurnStateRunning, RequestedAt: at, StartedAt: &started})
}

// endTurn makes the worker's newest turn one that ended at at.
func (f *fixture) endTurn(state domain.TurnState, errText string, at time.Time) {
	f.endTurnOf(worker1, state, errText, at)
}

// endTurnOf makes session id's newest turn one that ended at at.
func (f *fixture) endTurnOf(id domain.SessionID, state domain.TurnState, errText string, at time.Time) {
	started, done := at.Add(-time.Minute), at
	f.convs.set(id, domain.ConversationTurn{
		ID: "turn-" + at.Format(time.RFC3339Nano), State: state, ErrorMessage: errText,
		RequestedAt: started, StartedAt: &started, CompletedAt: &done,
	})
}

// limitAt makes the worker stop at its session limit at at, resting.
func (f *fixture) limitAt(at time.Time, text string) { f.limitOf(worker1, at, text) }

// limitOf makes session id stop at its limit at at, resting.
func (f *fixture) limitOf(id domain.SessionID, at time.Time, text string) {
	f.endTurnOf(id, domain.TurnStateFailed, text, at)
	f.restOf(id, at)
}

// addWorker runs another part worker, session id of task in project, made at
// created and resting since then.
func (f *fixture) addWorker(id domain.SessionID, task domain.LadoTaskID, project domain.ProjectID, created time.Time) {
	f.store.mu.Lock()
	f.store.tasks[task] = domain.LadoTask{ID: task, ProjectID: project, IssueRef: "github:o/r#8"}
	f.store.execs = append(f.store.execs, domain.LadoExecution{SessionID: id, TaskID: task, Role: domain.LadoRolePart, PartID: "b"})
	f.store.mu.Unlock()
	f.sessions.set(domain.SessionRecord{
		ID: id, ProjectID: project, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		CreatedAt: created, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: created},
	})
	f.endTurnOf(id, domain.TurnStateCompleted, "", created)
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
