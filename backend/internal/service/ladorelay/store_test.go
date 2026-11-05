package ladorelay

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// realStore is AO's store, with the sessions and the task's executions of
// the fixture: seeding a dispatched execution takes a whole approval.
type realStore struct {
	*store.Store
	fake *fakeStore
}

func (s realStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	return s.fake.GetSession(ctx, id)
}

func (s realStore) LadoRelayExecutions(ctx context.Context, id domain.LadoTaskID) ([]domain.LadoRelayExecution, error) {
	return s.fake.LadoRelayExecutions(ctx, id)
}

func TestRelayRecordsTheRelayInTheStore(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	db := sqlitetest.MustOpen(t)
	at := t0.Add(-3 * time.Hour)
	if err := db.UpsertProject(ctx, domain.ProjectRecord{ID: string(testProject), Path: "/work/app", RegisteredAt: at}); err != nil {
		t.Fatalf("register the project: %v", err)
	}
	if _, err := db.SetLadoMode(ctx, testProject, true, at); err != nil {
		t.Fatalf("turn LADO mode on: %v", err)
	}
	task := f.store.tasks[0]
	task.Agent, task.CreatedAt, task.UpdatedAt = "claude-code", at, at
	if _, err := db.CreateLadoTask(ctx, task); err != nil {
		t.Fatalf("create the task: %v", err)
	}
	svc := New(Deps{
		Store: realStore{Store: db, fake: f.store}, Conversations: f.convs, Plans: f.plans, Messenger: f.messenger,
		Clock: func() time.Time { return t0 }, NewID: func() string { return "1" },
		Log: slog.New(slog.NewTextHandler(&f.logs, nil)),
	})

	req := request()
	req.Message = "frontend/src/App.tsx 도 고치세요"
	if _, err := svc.Relay(ctx, req); err != nil {
		t.Fatalf("Relay: %v", err)
	}
	relays, err := svc.List(ctx, testTask)
	if err != nil || len(relays) != 1 {
		t.Fatalf("List = (%d relays, %v), want one", len(relays), err)
	}
	got := relays[0]
	want := domain.LadoRelay{
		ID: "lado-relay-1", TaskID: testTask, ProjectID: testProject,
		OrchestratorSessionID: testOrchestrator, WorkerSessionID: testWorker, WorkerRole: domain.LadoRoleSingle,
		Instruction: req.Message, Evidence: testEvidence, EvidenceMessageID: "m-1", EvidenceAt: t0.Add(-time.Hour),
		BeyondEvidence: true, OutsidePlan: true,
		OutsidePlanReasons: []string{domain.LadoRelayPathReason("frontend/src/App.tsx")},
		State:              domain.LadoRelayDelivered, CreatedAt: t0, DeliveredAt: t0,
	}
	if !relayEqual(got, want) {
		t.Errorf("stored relay = %+v,\nwant %+v", got, want)
	}
	if strings.Contains(f.logs.String(), "could not") {
		t.Errorf("the relay logged a failure:\n%s", f.logs.String())
	}
}
