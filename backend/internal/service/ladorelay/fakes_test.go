package ladorelay

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

var t0 = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

const (
	testProject      domain.ProjectID  = "proj-1"
	testOrchestrator domain.SessionID  = "proj-1-orchestrator-1"
	testTask         domain.LadoTaskID = "lado-task-1"
	testWorker       domain.SessionID  = "proj-1-worker-1"
	testRevision                       = domain.LadoPlanRevisionID("rev-1")
	// testSaid is what the developer typed in the orchestrator's chat.
	testSaid = "그 워커에게 이어서 해: backend/internal/service/ladorelay/service.go 의 테스트를 고쳐 줘"
	// testEvidence and testInstruction are inside testSaid.
	testEvidence    = "그 워커에게 이어서 해"
	testInstruction = "backend/internal/service/ladorelay/service.go 의 테스트를 고쳐 줘"
)

// fakeStore keeps what a relay reads and writes in memory.
type fakeStore struct {
	mu         sync.Mutex
	sessions   map[domain.SessionID]domain.SessionRecord
	ladoMode   map[domain.ProjectID]bool
	tasks      []domain.LadoTask
	executions map[domain.LadoTaskID][]domain.LadoRelayExecution
	relays     []domain.LadoRelay
	audits     []domain.LadoAuditEvent
}

func (s *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	return rec, ok, nil
}

func (s *fakeStore) LadoProjectSettings(_ context.Context, id domain.ProjectID) (domain.LadoProjectSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return domain.LadoProjectSettings{ProjectID: id, LadoMode: s.ladoMode[id]}, nil
}

func (s *fakeStore) LadoTask(_ context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.tasks {
		if task.ID == id {
			return task, true, nil
		}
	}
	return domain.LadoTask{}, false, nil
}

func (s *fakeStore) ListLadoTasks(_ context.Context, project domain.ProjectID, includeCancelled bool) ([]domain.LadoTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.LadoTask
	for _, task := range s.tasks {
		if task.ProjectID == project && (includeCancelled || task.CancelledAt.IsZero()) {
			out = append(out, task)
		}
	}
	return out, nil
}

func (s *fakeStore) LadoRelayExecutions(_ context.Context, id domain.LadoTaskID) ([]domain.LadoRelayExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.executions[id]), nil
}

func (s *fakeStore) InsertLadoRelay(_ context.Context, r domain.LadoRelay) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.State, r.Failure, r.DeliveredAt = domain.LadoRelaySending, "", time.Time{}
	s.relays = append(s.relays, r)
	return nil
}

func (s *fakeStore) mark(id domain.LadoRelayID, change func(*domain.LadoRelay)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.relays {
		if s.relays[i].ID == id && s.relays[i].State == domain.LadoRelaySending {
			change(&s.relays[i])
			return true
		}
	}
	return false
}

func (s *fakeStore) MarkLadoRelayDelivered(_ context.Context, id domain.LadoRelayID, at time.Time) (bool, error) {
	return s.mark(id, func(r *domain.LadoRelay) { r.State, r.DeliveredAt = domain.LadoRelayDelivered, at }), nil
}

func (s *fakeStore) MarkLadoRelayFailed(_ context.Context, id domain.LadoRelayID, failure string) (bool, error) {
	return s.mark(id, func(r *domain.LadoRelay) { r.State, r.Failure = domain.LadoRelayFailed, failure }), nil
}

func (s *fakeStore) LadoRelay(_ context.Context, id domain.LadoRelayID) (domain.LadoRelay, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.relays {
		if r.ID == id {
			return r, true, nil
		}
	}
	return domain.LadoRelay{}, false, nil
}

func (s *fakeStore) LadoRelaysByTask(_ context.Context, id domain.LadoTaskID) ([]domain.LadoRelay, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.LadoRelay
	for i := len(s.relays) - 1; i >= 0; i-- {
		if s.relays[i].TaskID == id {
			out = append(out, s.relays[i])
		}
	}
	return out, nil
}

func (s *fakeStore) AppendLadoAuditEvent(_ context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, ev)
	return ev, nil
}

// fakeConversations holds each project's orchestrator conversation.
type fakeConversations map[domain.ProjectID][]domain.ConversationMessage

func (c fakeConversations) ProjectMessages(_ context.Context, project domain.ProjectID) ([]domain.ConversationMessage, error) {
	return c[project], nil
}

// fakePlans holds the approved plans by revision.
type fakePlans map[domain.LadoPlanRevisionID]domain.LadoPlan

func (p fakePlans) ApprovedPlan(_ context.Context, id domain.LadoPlanRevisionID) (domain.LadoPlan, error) {
	plan, ok := p[id]
	if !ok {
		return domain.LadoPlan{}, errors.New("no such revision")
	}
	return plan, nil
}

// sent is one message the messenger delivered.
type sent struct {
	to   domain.SessionID
	text string
}

type fakeMessenger struct {
	mu   sync.Mutex
	sent []sent
	err  error
}

func (m *fakeMessenger) Relay(_ context.Context, to domain.SessionID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, sent{to: to, text: text})
	return nil
}

// fixture is a Service over fakes holding one LADO project with a live Chat
// orchestrator, a task for issue #49 and its one live single worker, and a
// developer message that gives the instruction an hour ago.
type fixture struct {
	store     *fakeStore
	convs     fakeConversations
	plans     fakePlans
	messenger *fakeMessenger
	now       time.Time
	ids       int
	logs      bytes.Buffer
	svc       *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		store: &fakeStore{
			sessions: map[domain.SessionID]domain.SessionRecord{
				testOrchestrator: {
					ID: testOrchestrator, ProjectID: testProject, Kind: domain.KindOrchestrator,
					Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle},
				},
				testWorker: {ID: testWorker, ProjectID: testProject, Kind: domain.KindWorker, Mode: domain.SessionModeChat},
			},
			ladoMode: map[domain.ProjectID]bool{testProject: true},
			tasks: []domain.LadoTask{{
				ID: testTask, ProjectID: testProject, IssueRef: "github:acme/app#49",
				Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/app"},
			}},
			executions: map[domain.LadoTaskID][]domain.LadoRelayExecution{
				testTask: {liveExecution(testWorker, domain.LadoRoleSingle, "")},
			},
		},
		convs: fakeConversations{testProject: {
			message("m-1", domain.MessageRoleUser, domain.MessageOriginHuman, testSaid, t0.Add(-time.Hour)),
			message("m-2", domain.MessageRoleAssistant, domain.MessageOriginProvider, "알겠습니다. LADO 에 전달을 요청합니다.", t0.Add(-59*time.Minute)),
		}},
		plans: fakePlans{testRevision: {
			AffectedPaths: []string{"backend/internal/service/ladorelay/**", "docs/LJY-PRD/**"},
			Parts: []domain.LadoPlanPart{
				{ID: "service", AffectedPaths: []string{"backend/internal/service/ladorelay/**"}},
				{ID: "docs", AffectedPaths: []string{"docs/LJY-PRD/**"}},
			},
		}},
		messenger: &fakeMessenger{},
		now:       t0,
	}
	f.svc = New(Deps{
		Store: f.store, Conversations: f.convs, Plans: f.plans, Messenger: f.messenger,
		Clock: func() time.Time { return f.now },
		NewID: func() string { f.ids++; return strconv.Itoa(f.ids) },
		Log:   slog.New(slog.NewTextHandler(&f.logs, nil)),
	})
	return f
}

func liveExecution(session domain.SessionID, role, part string) domain.LadoRelayExecution {
	return domain.LadoRelayExecution{
		Execution: domain.LadoExecution{
			SessionID: session, TaskID: testTask, PlanRevisionID: testRevision,
			Role: role, PartID: part, StartedAt: t0.Add(-2 * time.Hour),
		},
		SessionActivity: domain.ActivityIdle,
	}
}

func message(id string, role domain.MessageRole, origin domain.MessageOrigin, text string, at time.Time) domain.ConversationMessage {
	return domain.ConversationMessage{ID: id, ConversationID: "conv-1", Role: role, Origin: origin, Text: text, CreatedAt: at}
}

// request is the relay request the fixture's orchestrator makes.
func request() Request {
	return Request{SessionID: testOrchestrator, Task: "#49", Message: testInstruction, Evidence: testEvidence}
}

// setExecutions replaces the task's executions, newest first.
func (f *fixture) setExecutions(executions ...domain.LadoRelayExecution) {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	f.store.executions[testTask] = executions
}

// apiError returns err's API error, failing the test without one.
func apiError(t *testing.T, err error) *apierr.Error {
	t.Helper()
	var e *apierr.Error
	if !errors.As(err, &e) {
		t.Fatalf("error = %v, want an *apierr.Error", err)
	}
	return e
}
