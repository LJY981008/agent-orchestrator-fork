package ladonotify

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var t0 = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

// fakeStore is the outbox and the facts, in memory.
type fakeStore struct {
	mu        sync.Mutex
	adds      int
	since     time.Time
	notices   []domain.LadoOrchestratorNotice
	requests  []domain.LadoPlanRequestNoticeFact
	revisions []domain.LadoPlanRevisionNoticeFact
	tasks     map[domain.ProjectID][]domain.LadoTask
}

func (f *fakeStore) LadoNoticeSince(context.Context) (time.Time, error) { return f.since, nil }

func (f *fakeStore) AddLadoOrchestratorNotice(_ context.Context, n domain.LadoOrchestratorNotice) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adds++
	for _, have := range f.notices {
		if have.Key == n.Key {
			return false, nil
		}
	}
	n.ID = int64(len(f.notices) + 1)
	f.notices = append(f.notices, n)
	return true, nil
}

func (f *fakeStore) ListWaitingLadoOrchestratorNotices(context.Context) ([]domain.LadoOrchestratorNotice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LadoOrchestratorNotice
	for _, n := range f.notices {
		if n.SentAt.IsZero() && n.DroppedAt.IsZero() {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeStore) mark(ids []int64, edit func(*domain.LadoOrchestratorNotice)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.notices {
		if slices.Contains(ids, f.notices[i].ID) {
			edit(&f.notices[i])
		}
	}
}

func (f *fakeStore) MarkLadoOrchestratorNoticesSent(_ context.Context, ids []int64, to domain.SessionID, at time.Time) error {
	f.mark(ids, func(n *domain.LadoOrchestratorNotice) { n.SentAt, n.SentTo = at, to })
	return nil
}

func (f *fakeStore) DropLadoOrchestratorNotices(_ context.Context, ids []int64, at time.Time) error {
	f.mark(ids, func(n *domain.LadoOrchestratorNotice) { n.DroppedAt = at })
	return nil
}

func (f *fakeStore) ListLadoPlanRequestNoticeFacts(context.Context, time.Time) ([]domain.LadoPlanRequestNoticeFact, error) {
	return f.requests, nil
}

func (f *fakeStore) ListLadoPlanRevisionNoticeFacts(context.Context, time.Time) ([]domain.LadoPlanRevisionNoticeFact, error) {
	return f.revisions, nil
}

func (f *fakeStore) ListLadoProjectsWithRunsSince(context.Context, time.Time) ([]domain.ProjectID, error) {
	var out []domain.ProjectID
	for id := range f.tasks {
		out = append(out, id)
	}
	slices.Sort(out)
	return out, nil
}

func (f *fakeStore) ListLadoTasks(_ context.Context, id domain.ProjectID, _ bool) ([]domain.LadoTask, error) {
	return f.tasks[id], nil
}

func (f *fakeStore) texts(sent bool) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, n := range f.notices {
		if !n.SentAt.IsZero() == sent && n.DroppedAt.IsZero() {
			out = append(out, n.Text)
		}
	}
	return out
}

// fakeOrchestrators is one project's orchestrator, or none.
type fakeOrchestrators struct {
	mu       sync.Mutex
	live     bool
	activity domain.ActivityState
	sent     []string
	asked    int
}

func (f *fakeOrchestrators) Orchestrator(context.Context, domain.ProjectID) (domain.SessionID, domain.ActivityState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	return "demo-orchestrator-1", f.activity, f.live, nil
}

func (f *fakeOrchestrators) Send(_ context.Context, _ domain.SessionID, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, message)
	return nil
}

type fixture struct {
	store *fakeStore
	orch  *fakeOrchestrators
	svc   *Service
	now   time.Time
}

func newFixture() *fixture {
	f := &fixture{store: &fakeStore{since: t0, tasks: map[domain.ProjectID][]domain.LadoTask{}}, orch: &fakeOrchestrators{live: true, activity: domain.ActivityIdle}, now: t0.Add(time.Hour)}
	f.svc = New(Deps{Store: f.store, Orchestrators: f.orch, Clock: func() time.Time { return f.now }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return f
}

func (f *fixture) tick(t *testing.T) {
	t.Helper()
	if err := f.svc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
}

// Each kind of event gives one line, once, and none tells of an event from
// before notices started, of a request to an orchestrator, or of a request
// a newer one replaced.
func TestTickQueuesOneLinePerEvent(t *testing.T) {
	f := newFixture()
	ref := domain.IssueID("github:acme/demo#27")
	f.store.requests = []domain.LadoPlanRequestNoticeFact{
		{ID: "r-1", ProjectID: "demo", IssueRef: ref, Kind: domain.LadoPlanRequestWorker, RequestedAt: t0.Add(time.Minute)},
		{ID: "r-2", ProjectID: "demo", IssueRef: ref, Kind: domain.LadoPlanRequestWorker, RequestedAt: t0.Add(2 * time.Minute), FailedAt: t0.Add(3 * time.Minute), FailureCode: domain.LadoPlanWorkerTimedOut},
		{ID: "r-3", ProjectID: "demo", IssueRef: ref, Kind: domain.LadoPlanRequestWorker, RequestedAt: t0.Add(-time.Hour), FailedAt: t0.Add(4 * time.Minute), FailureCode: domain.LadoPlanRequestReplaced},
		{ID: "r-4", ProjectID: "demo", IssueRef: ref, Kind: domain.LadoPlanRequestOrchestrator, RequestedAt: t0.Add(5 * time.Minute)},
	}
	f.store.revisions = []domain.LadoPlanRevisionNoticeFact{{TaskID: "lado-task-1", ProjectID: "demo", IssueRef: ref, Number: 2, CreatedAt: t0.Add(6 * time.Minute)}}
	f.store.tasks["demo"] = []domain.LadoTask{
		{ID: "lado-task-1", ProjectID: "demo", IssueRef: ref, Execution: domain.LadoExecution{
			SessionID: "demo-9", StartedAt: t0.Add(7 * time.Minute), PR: domain.LadoExecutionPR{URL: "https://github.com/acme/demo/pull/41", Merged: true},
		}},
		{ID: "lado-task-2", ProjectID: "demo", IssueRef: "github:acme/demo#28", Execution: domain.LadoExecution{
			SessionID: "demo-10", StartedAt: t0.Add(8 * time.Minute), SessionTerminated: true,
		}},
		{ID: "lado-task-3", ProjectID: "demo", IssueRef: "github:acme/demo#29", Queue: domain.LadoQueueEntry{ID: "q-1", FailedAt: t0.Add(9 * time.Minute)}},
		{ID: "lado-task-4", ProjectID: "demo", IssueRef: "github:acme/demo#30", Execution: domain.LadoExecution{SessionID: "demo-old", StartedAt: t0.Add(-time.Hour)}},
	}
	f.orch.live = false
	f.tick(t)
	adds := f.store.adds
	// The next tick reads the same facts and writes nothing.
	f.tick(t)
	if f.store.adds != adds {
		t.Fatalf("the second tick wrote %d notices again", f.store.adds-adds)
	}
	got := f.store.texts(false)
	want := []string{
		"LADO 알림: #27 계획 요청 접수 — 계획 워커가 씁니다",
		"LADO 알림: #27 계획 요청 접수 — 계획 워커가 씁니다",
		"LADO 알림: #27 계획 실패: 시간 초과",
		"LADO 알림: #27 계획 리비전 2 — 칸반에서 확인",
		"LADO 알림: #27 실행 시작",
		"LADO 알림: #27 PR #41 열림",
		"LADO 알림: #27 완료",
		"LADO 알림: #28 실행 시작",
		"LADO 알림: #28 실행 실패: PR 없이 끝남",
		"LADO 알림: #29 실행 실패: 워커를 띄우지 못함",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("notices =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Without a live orchestrator nothing goes, and none is started.
	f.now = f.now.Add(quietFor)
	f.tick(t)
	if f.orch.asked != 1 || len(f.orch.sent) != 0 || len(f.store.texts(false)) != len(want) {
		t.Fatalf("asked %d, sent %d; want the orchestrator looked up once and the notices kept", f.orch.asked, len(f.orch.sent))
	}
}

// Notices wait while the orchestrator works and go together, in one message
// of their lines only, once it is idle and no new one came for a while.
func TestNoticesWaitForAnIdleOrchestratorAndGoTogether(t *testing.T) {
	f := newFixture()
	ref := domain.IssueID("github:acme/demo#27")
	f.store.requests = []domain.LadoPlanRequestNoticeFact{{ID: "r-1", ProjectID: "demo", IssueRef: ref, Kind: domain.LadoPlanRequestWorker, RequestedAt: t0.Add(time.Minute)}}
	f.store.revisions = []domain.LadoPlanRevisionNoticeFact{{TaskID: "lado-task-1", ProjectID: "demo", IssueRef: ref, Number: 1, CreatedAt: t0.Add(2 * time.Minute)}}
	f.orch.activity = domain.ActivityActive
	f.tick(t)
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if len(f.orch.sent) != 0 {
		t.Fatalf("sent %q while the orchestrator works", f.orch.sent)
	}
	f.orch.activity = domain.ActivityIdle
	f.tick(t)
	if len(f.orch.sent) != 1 || f.orch.sent[0] != "LADO 알림: #27 계획 요청 접수 — 계획 워커가 씁니다\nLADO 알림: #27 계획 리비전 1 — 칸반에서 확인" {
		t.Fatalf("sent = %q", f.orch.sent)
	}
	if len(f.store.texts(true)) != 2 {
		t.Fatal("the sent notices are not marked")
	}
	f.tick(t)
	if len(f.orch.sent) != 1 {
		t.Fatal("notices went twice")
	}

	// A fresh notice waits for a quiet moment, so a burst goes together.
	f.store.revisions = append(f.store.revisions, domain.LadoPlanRevisionNoticeFact{TaskID: "lado-task-1", ProjectID: "demo", IssueRef: ref, Number: 2, CreatedAt: f.now})
	f.tick(t)
	if len(f.orch.sent) != 1 {
		t.Fatal("a fresh notice went at once")
	}
	f.now = f.now.Add(quietFor)
	f.tick(t)
	if len(f.orch.sent) != 2 || f.orch.sent[1] != "LADO 알림: #27 계획 리비전 2 — 칸반에서 확인" {
		t.Fatalf("sent = %q", f.orch.sent)
	}
}

// Without an orchestrator, notices wait a day at most, and only the newest
// twenty of a project.
func TestNoticesNobodyTakesAreDropped(t *testing.T) {
	f := newFixture()
	f.orch.live = false
	for i := range 25 {
		f.store.requests = append(f.store.requests, domain.LadoPlanRequestNoticeFact{
			ID: domain.LadoPlanRequestID("r-" + string(rune('a'+i))), ProjectID: "demo", IssueRef: domain.IssueID("github:acme/demo#" + string(rune('1'+i%9))),
			Kind: domain.LadoPlanRequestWorker, RequestedAt: t0.Add(time.Duration(i) * time.Minute),
		})
	}
	f.tick(t)
	if waiting := f.store.texts(false); len(waiting) != domain.LadoNoticeMaxPending {
		t.Fatalf("%d notices wait, want %d", len(waiting), domain.LadoNoticeMaxPending)
	}
	f.now = f.now.Add(domain.LadoNoticeKeep + time.Minute)
	f.tick(t)
	if waiting := f.store.texts(false); len(waiting) != 0 {
		t.Fatalf("%d notices still wait after a day", len(waiting))
	}
	f.orch.live = true
	f.tick(t)
	if len(f.orch.sent) != 0 {
		t.Fatalf("dropped notices were sent: %q", f.orch.sent)
	}
}
