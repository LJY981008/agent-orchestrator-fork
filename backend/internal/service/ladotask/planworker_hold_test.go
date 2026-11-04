package ladotask

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladowatch"
)

// The watch tells the accounts at their limit, and AO's agent service checks
// an agent before a launch.
var (
	_ AccountLimits = (*ladowatch.Service)(nil)
	_ Preflight     = (*agentsvc.Service)(nil)
)

// fakeLimits holds every account until resetAt.
type fakeLimits struct {
	mu      sync.Mutex
	resetAt time.Time
}

func (f *fakeLimits) LaunchAccountLimit(_ context.Context, harness domain.AgentHarness, now time.Time) (domain.LadoAccountLimit, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !now.Before(f.resetAt) {
		return domain.LadoAccountLimit{}, false, nil
	}
	key, _ := domain.LadoAccountKey(harness, "")
	return domain.LadoAccountLimit{AccountKey: key, ResetAt: f.resetAt}, true, nil
}

type fakePreflight struct {
	mu  sync.Mutex
	err error
}

func (f *fakePreflight) PreflightLaunch(context.Context, domain.AgentHarness) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// A designer waits while its account is at its limit, spends neither of its
// starts, and starts after the reset.
func TestAPlanWorkerWaitsForItsAccountsLimit(t *testing.T) {
	pf := newPlanFixture(t)
	limits := &fakeLimits{resetAt: pf.now.Add(time.Hour)}
	pf.svc.deps.AccountLimits = limits
	req := pf.requestAndStart(t, PlanRequestInput{TaskID: pf.task.ID})
	if req.WorkerSession() != "" || req.WaitingReason != domain.LadoWaitingAccountLimited || len(pf.sessions.spawns()) != 0 {
		t.Fatalf("request = %+v, spawns %d; want it waiting for the reset", req, len(pf.sessions.spawns()))
	}
	pf.now = pf.now.Add(30 * time.Minute)
	pf.svc.DrivePlanWorkers(context.Background())
	if len(pf.sessions.spawns()) != 0 {
		t.Fatal("a plan worker started during the limit")
	}
	pf.now = limits.resetAt
	pf.svc.DrivePlanWorkers(context.Background())
	got := pf.request(t, req.ID)
	spawns := pf.sessions.spawns()
	if got.WorkerSession() == "" || got.WaitingReason != "" || len(spawns) != 1 ||
		spawns[0].ClientRequestID != domain.LadoPlanWorkerClientRequestID(req.ID, domain.LadoPlanRoleDesigner, 1) {
		t.Fatalf("request = %+v, spawns %d; want its first designer started", got, len(spawns))
	}
}

// A designer whose agent cannot start waits and raises the task's alert.
func TestAPlanWorkerWaitsWhileItsAgentCannotStart(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	preflight := &fakePreflight{err: errors.New("claude: native binary not found")}
	pf.svc.deps.Preflight = preflight
	req := pf.requestAndStart(t, PlanRequestInput{TaskID: pf.task.ID})
	if req.WorkerSession() != "" || req.WaitingReason != domain.LadoWaitingAgentUnavailable || len(pf.sessions.spawns()) != 0 {
		t.Fatalf("request = %+v; want it waiting for the agent", req)
	}
	pf.svc.DrivePlanWorkers(ctx)
	alerts, err := pf.store.ListLadoAlertsByTask(ctx, pf.task.ID)
	if err != nil || len(alerts) != 1 || alerts[0].Kind != domain.LadoAlertAgentUnavailable {
		t.Fatalf("alerts = %+v, %v; want one", alerts, err)
	}
	preflight.mu.Lock()
	preflight.err = nil
	preflight.mu.Unlock()
	pf.svc.DrivePlanWorkers(ctx)
	if got := pf.request(t, req.ID); got.WorkerSession() == "" || got.WaitingReason != "" {
		t.Fatalf("request = %+v; want started", got)
	}
}

// A critic is not started, and its review not begun, while its account is at
// its limit; after the reset the review begins.
func TestACriticWaitsForItsAccountsLimit(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	req := pf.requestAndStart(t, PlanRequestInput{TaskID: pf.task.ID})
	designer := pf.designer(t, req.ID)
	pf.now = pf.now.Add(20 * time.Minute)
	pf.submit(t, req.ID, designer, pf.workerPlan("issue", "", "backend/a/**", "backend/b/**", "frontend/**", "docs/**"), true)
	limits := &fakeLimits{resetAt: pf.now.Add(time.Hour)}
	pf.svc.deps.AccountLimits = limits
	pf.svc.DrivePlanWorkers(ctx)
	if len(pf.sessions.spawns()) != 1 {
		t.Fatalf("%d spawns during the limit, want the designer only", len(pf.sessions.spawns()))
	}
	if critiques, _ := pf.store.ListLadoPlanCritiques(ctx, req.ID); len(critiques) != 0 {
		t.Fatalf("critiques = %+v; want none begun during the limit", critiques)
	}
	pf.now = limits.resetAt
	pf.svc.DrivePlanWorkers(ctx)
	running, ok, _ := pf.store.LadoPlanCritique(ctx, req.ID, 1)
	if len(pf.sessions.spawns()) != 2 || !ok || running.State != domain.LadoCritiqueRunning || !running.StartedAt.Equal(limits.resetAt) {
		t.Fatalf("spawns %d, critique %+v; want the critic started at the reset", len(pf.sessions.spawns()), running)
	}
}
