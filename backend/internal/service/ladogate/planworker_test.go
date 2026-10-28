package ladogate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladogate"
)

// fakePlans holds plan worker requests and their reviews.
type fakePlans struct {
	requests  map[domain.LadoPlanRequestID]domain.LadoPlanRequest
	critiques map[domain.LadoPlanRequestID]domain.LadoPlanCritique
	err       error
}

func (f fakePlans) LadoPlanRequest(_ context.Context, id domain.LadoPlanRequestID) (domain.LadoPlanRequest, bool, error) {
	if f.err != nil {
		return domain.LadoPlanRequest{}, false, f.err
	}
	req, ok := f.requests[id]
	return req, ok, nil
}

func (f fakePlans) LadoPlanCritique(_ context.Context, id domain.LadoPlanRequestID, attempt int) (domain.LadoPlanCritique, bool, error) {
	c, ok := f.critiques[id]
	if !ok || c.Attempt != attempt {
		return domain.LadoPlanCritique{}, false, nil
	}
	return c, true, nil
}

func planWorkerSpawn(project domain.ProjectID, role domain.LadoPlanRole, attempt int) ports.SpawnConfig {
	id := domain.LadoPlanWorkerClientRequestID("req-1", role, attempt)
	return ports.SpawnConfig{ProjectID: project, Kind: domain.KindWorker, ClientRequestID: id, ClientRequestHash: domain.LadoPlanWorkerHash(id)}
}

// A plan worker's client request id is LADO's: it starts a worker in a LADO
// project only while LADO's plan loop starts that very session, and never
// otherwise, in any project.
func TestAuthorizeSpawnGrantsPlanWorkersOnlyToLADO(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	open := domain.LadoPlanRequest{ID: "req-1", TaskID: "task-1", Kind: domain.LadoPlanRequestWorker, WorkerAttempts: 1}
	tasks := fakeExecutions{tasks: map[domain.LadoTaskID]domain.LadoTask{"task-1": {ID: "task-1", ProjectID: "lado"}}}
	gate := func(req domain.LadoPlanRequest, critique domain.LadoPlanCritique) *ladogate.Gate {
		g := ladogate.New(ladoProject(), fakeSessions{}, fakeGrants{}, fakePolicies{}, tasks)
		g.SetPlanWorkers(fakePlans{
			requests:  map[domain.LadoPlanRequestID]domain.LadoPlanRequest{"req-1": req},
			critiques: map[domain.LadoPlanRequestID]domain.LadoPlanCritique{"req-1": critique},
		})
		return g
	}
	running := domain.LadoPlanCritique{RequestID: "req-1", Attempt: 1, State: domain.LadoCritiqueRunning}

	if err := gate(open, running).AuthorizeSpawn(ctx, planWorkerSpawn("lado", domain.LadoPlanRoleDesigner, 1)); err != nil {
		t.Fatalf("the designer LADO starts = %v", err)
	}
	if err := gate(open, running).AuthorizeSpawn(ctx, planWorkerSpawn("lado", domain.LadoPlanRoleCritic, 1)); err != nil {
		t.Fatalf("the critic LADO starts = %v", err)
	}
	started := open
	started.WorkerStartedAt = at
	withSession := running
	withSession.SessionID = "lado-7"
	failed := open
	failed.FailedAt = at
	wrongHash := planWorkerSpawn("lado", domain.LadoPlanRoleDesigner, 1)
	wrongHash.ClientRequestHash = "x"
	orchestrator := planWorkerSpawn("lado", domain.LadoPlanRoleDesigner, 1)
	orchestrator.Kind = domain.KindOrchestrator
	refused := map[string]struct {
		gate *ladogate.Gate
		cfg  ports.SpawnConfig
	}{
		"no plan reads":         {ladogate.New(ladoProject(), fakeSessions{}, fakeGrants{}, fakePolicies{}, tasks), planWorkerSpawn("lado", domain.LadoPlanRoleDesigner, 1)},
		"designer started":      {gate(started, running), planWorkerSpawn("lado", domain.LadoPlanRoleDesigner, 1)},
		"another attempt":       {gate(open, running), planWorkerSpawn("lado", domain.LadoPlanRoleDesigner, 2)},
		"critic started":        {gate(open, withSession), planWorkerSpawn("lado", domain.LadoPlanRoleCritic, 1)},
		"no such critique":      {gate(open, running), planWorkerSpawn("lado", domain.LadoPlanRoleCritic, 2)},
		"request failed":        {gate(failed, running), planWorkerSpawn("lado", domain.LadoPlanRoleDesigner, 1)},
		"another project":       {gate(open, running), planWorkerSpawn("other", domain.LadoPlanRoleDesigner, 1)},
		"wrong hash":            {gate(open, running), wrongHash},
		"orchestrator kind":     {gate(open, running), orchestrator},
		"another request":       {gate(open, running), ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker, ClientRequestID: "lado:plan/req-2/design/1", ClientRequestHash: domain.LadoPlanWorkerHash("lado:plan/req-2/design/1")}},
		"not in LADO mode, too": {gate(open, running), planWorkerSpawn("plain", domain.LadoPlanRoleDesigner, 1)},
	}
	for name, tc := range refused {
		var apiErr *apierr.Error
		if err := tc.gate.AuthorizeSpawn(ctx, tc.cfg); !errors.As(err, &apiErr) || apiErr.Code != "LADO_PLAN_WORKER_REQUIRED" {
			t.Errorf("%s = %v, want LADO_PLAN_WORKER_REQUIRED", name, err)
		}
	}
	readErr := errors.New("disk")
	g := ladogate.New(ladoProject(), fakeSessions{}, fakeGrants{}, fakePolicies{}, tasks)
	g.SetPlanWorkers(fakePlans{err: readErr})
	if err := g.AuthorizeSpawn(ctx, planWorkerSpawn("lado", domain.LadoPlanRoleDesigner, 1)); !errors.Is(err, readErr) {
		t.Fatalf("a failed read = %v, want it refused with the error", err)
	}
}

// A plan worker runs in auto at most, as an execution under an auto
// ceiling: its turns and config cannot ask for bypass, it keeps its agent and
// interface, and it runs read-only.
func TestPlanWorkersStayBelowBypassAndReadOnly(t *testing.T) {
	ctx := context.Background()
	sessions := fakeSessions{records: map[domain.SessionID]domain.SessionRecord{
		"plan-1":   {ID: "plan-1", ProjectID: "lado", Kind: domain.KindWorker, ClientRequestID: domain.LadoPlanWorkerClientRequestID("req-1", domain.LadoPlanRoleDesigner, 1)},
		"worker-1": {ID: "worker-1", ProjectID: "lado", Kind: domain.KindWorker},
	}}
	g := ladogate.New(ladoProject(), sessions, fakeGrants{}, fakePolicies{}, fakeExecutions{})
	turn, err := g.ClampTurnSettings(ctx, "plan-1", ports.ChatTurnSettings{Approval: domain.PermissionModeBypassPermissions})
	if err != nil || turn.Approval != domain.PermissionModeAuto {
		t.Fatalf("a plan worker's bypass turn = %+v, %v; want auto", turn, err)
	}
	var apiErr *apierr.Error
	if err := g.AuthorizeConfigMode(ctx, "plan-1", domain.PermissionModeBypassPermissions); !errors.As(err, &apiErr) || apiErr.Code != "LADO_APPROVAL_ABOVE_CEILING" {
		t.Fatalf("switching a plan worker to bypass = %v", err)
	}
	if err := g.AuthorizeExecutionChange(ctx, "plan-1"); !errors.As(err, &apiErr) || apiErr.Code != "LADO_EXECUTION_FIXED" {
		t.Fatalf("switching a plan worker's agent = %v", err)
	}
	if !g.ReadOnlySession(ctx, "plan-1") || g.ReadOnlySession(ctx, "worker-1") || g.ReadOnlySession(ctx, "gone") {
		t.Fatal("ReadOnlySession marks the wrong sessions")
	}
	if turn, err := g.ClampTurnSettings(ctx, "worker-1", ports.ChatTurnSettings{Approval: domain.PermissionModeBypassPermissions}); err != nil || turn.Approval != domain.PermissionModeBypassPermissions {
		t.Fatalf("another worker's turn = %+v, %v; want it unchanged", turn, err)
	}
}
