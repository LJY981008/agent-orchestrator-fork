// Package ladogate is LADO's worker gate (docs/LJY-PRD/TDD.md §6): in a project
// with LADO mode on, workers start only when LADO dispatches them, and only a
// person or AO itself messages a worker. An execution, the worker LADO
// dispatched, keeps the agent, interface and permission ceiling it was
// approved with, and one LADO stopped for good is not restored. Client
// request ids of the form lado:<entry>/<attempt> are the dispatcher's alone,
// and those of the form lado:plan/<request>/<role>/<attempt> LADO's plan
// workers', which run read-only (docs/LJY-PRD/TDD.md §5.1).
package ladogate

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Settings reads LADO's per-project settings.
type Settings interface {
	LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error)
}

// Sessions reads the session a message is addressed to.
type Sessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Grants reads whether the dispatcher holds a queue entry for an attempt.
type Grants interface {
	LadoDispatchGrant(ctx context.Context, entryID domain.LadoQueueEntryID, attempt int, now time.Time) (domain.LadoDispatchGrant, error)
	// LadoPartSpawnGrant is the grant of a parts run worker's client request
	// id: active only while the dispatcher starts that worker
	// (docs/LJY-PRD/TDD.md §5.4).
	LadoPartSpawnGrant(ctx context.Context, clientRequestID string, now time.Time) (domain.LadoDispatchGrant, error)
}

// Policies reads what an execution keeps from its rule snapshot, found
// through the client request id its session was started with.
type Policies interface {
	LadoEntryPolicyByClientRequest(ctx context.Context, clientRequestID string) (domain.LadoEntryPolicy, bool, error)
}

// Executions reads LADO's executions and their tasks.
type Executions interface {
	LadoExecution(ctx context.Context, sessionID domain.SessionID) (domain.LadoExecution, bool, error)
	LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error)
}

// PlanWorkers reads LADO's plan worker requests and the reviews of their
// drafts.
type PlanWorkers interface {
	LadoPlanRequest(ctx context.Context, id domain.LadoPlanRequestID) (domain.LadoPlanRequest, bool, error)
	LadoPlanCritique(ctx context.Context, id domain.LadoPlanRequestID, attempt int) (domain.LadoPlanCritique, bool, error)
}

// Gate implements ports.WorkGate. Every read error refuses the work.
type Gate struct {
	settings   Settings
	sessions   Sessions
	grants     Grants
	policies   Policies
	executions Executions
	plans      PlanWorkers
}

var _ ports.WorkGate = (*Gate)(nil)

// New returns a gate over LADO's settings, AO's sessions, the dispatch
// grants and rule snapshots of LADO's queue, and LADO's executions.
func New(settings Settings, sessions Sessions, grants Grants, policies Policies, executions Executions) *Gate {
	return &Gate{settings: settings, sessions: sessions, grants: grants, policies: policies, executions: executions}
}

// SetPlanWorkers installs the reads of plan worker requests. The daemon calls
// it with New; without it no plan worker starts.
func (g *Gate) SetPlanWorkers(plans PlanWorkers) {
	g.plans = plans
}

// AuthorizeSpawn refuses automations and every new worker in a LADO project
// but the one LADO's dispatcher starts for the queue entry it holds and the
// plan workers LADO starts; orchestrators still start there. In every
// project, and outside them, a LADO client request id needs LADO's grant: the
// dispatcher takes the session that holds one for its queue entry's worker,
// and a plan worker's id is granted only while LADO starts that session.
func (g *Gate) AuthorizeSpawn(ctx context.Context, cfg ports.SpawnConfig) error {
	if planWorker, granted, err := g.planWorkerGranted(ctx, cfg); err != nil {
		return err
	} else if planWorker {
		if !granted {
			return apierr.Conflict("LADO_PLAN_WORKER_REQUIRED",
				"Client request ids that start with lado:plan/ are LADO's: only its plan loop starts plan workers with them.",
				map[string]any{"projectId": string(cfg.ProjectID), "clientRequestId": cfg.ClientRequestID})
		}
		return nil
	}
	granted, err := g.granted(ctx, cfg)
	if err != nil {
		return err
	}
	if _, dispatch := domain.ParseLadoRunClientRequestID(cfg.ClientRequestID); dispatch && !granted {
		return apierr.Conflict("LADO_TASK_REQUIRED",
			"Client request ids that start with lado: are LADO's: only its dispatcher starts sessions with them.",
			map[string]any{"projectId": string(cfg.ProjectID), "clientRequestId": cfg.ClientRequestID})
	}
	if cfg.ProjectID == "" {
		return nil
	}
	on, err := g.ladoMode(ctx, cfg.ProjectID)
	if err != nil || !on {
		return err
	}
	switch {
	case cfg.AutomationRunID != nil:
		return apierr.Conflict("LADO_AUTOMATION_REFUSED",
			"This project is in LADO mode: automations cannot start sessions in it.",
			map[string]any{"projectId": string(cfg.ProjectID)})
	case cfg.Kind == domain.KindOrchestrator, granted:
		return nil
	}
	return apierr.Conflict("LADO_TASK_REQUIRED",
		"This project is in LADO mode: workers start only from approved tasks. Create a task instead.",
		map[string]any{"projectId": string(cfg.ProjectID)})
}

// AuthorizeSend refuses messages from other sessions and from automation to a
// worker of a LADO project. People, AO's report relay and AO's own nudges still
// reach it, and messages to orchestrators are never refused.
func (g *Gate) AuthorizeSend(ctx context.Context, target domain.SessionID, origin ports.SendOrigin) error {
	switch origin {
	case ports.SendOriginHuman, ports.SendOriginCoordination, ports.SendOriginDaemon:
		return nil
	}
	rec, found, err := g.sessions.GetSession(ctx, target)
	if err != nil {
		return fmt.Errorf("LADO gate: read session %s: %w", target, err)
	}
	if !found || rec.ProjectID == "" || rec.Kind == domain.KindOrchestrator {
		return nil
	}
	on, err := g.ladoMode(ctx, rec.ProjectID)
	if err != nil || !on {
		return err
	}
	return apierr.Conflict("LADO_SEND_REFUSED",
		"This project is in LADO mode: only a person or AO itself can message its workers.",
		map[string]any{"sessionId": string(target), "origin": string(origin)})
}

// granted reports whether the spawn is the dispatcher's: its client request id
// names a queue entry and an attempt, a lease holds the entry for that
// attempt right now (or, for a parts run worker, the entry's spawn request is
// that id right now), and the entry is of the spawn's project and has the
// spawn's dispatch hash.
func (g *Gate) granted(ctx context.Context, cfg ports.SpawnConfig) (bool, error) {
	run, ok := domain.ParseLadoRunClientRequestID(cfg.ClientRequestID)
	if !ok {
		return false, nil
	}
	var grant domain.LadoDispatchGrant
	var err error
	if run.Role == domain.LadoRoleSingle {
		grant, err = g.grants.LadoDispatchGrant(ctx, run.Entry, run.Attempt, time.Now())
	} else {
		grant, err = g.grants.LadoPartSpawnGrant(ctx, cfg.ClientRequestID, time.Now())
	}
	if err != nil {
		return false, fmt.Errorf("LADO gate: %w", err)
	}
	return grant.Active && grant.ProjectID == cfg.ProjectID && grant.Hash == cfg.ClientRequestHash, nil
}

// planWorkerGranted reports whether the spawn is a plan worker's, and whether
// LADO starts it now: its hash fits its client request id, it is a worker of
// the project of an open plan worker request, and the request is starting
// that attempt of its designer, or that attempt of a critic whose session is
// not recorded yet.
func (g *Gate) planWorkerGranted(ctx context.Context, cfg ports.SpawnConfig) (bool, bool, error) {
	id, role, attempt, ok := domain.ParseLadoPlanWorkerClientRequestID(cfg.ClientRequestID)
	if !ok {
		return false, false, nil
	}
	if g.plans == nil || cfg.Kind != domain.KindWorker || cfg.ClientRequestHash != domain.LadoPlanWorkerHash(cfg.ClientRequestID) {
		return true, false, nil
	}
	req, found, err := g.plans.LadoPlanRequest(ctx, id)
	if err != nil {
		return true, false, fmt.Errorf("LADO gate: read plan request %s: %w", id, err)
	}
	if !found || !req.Open() {
		return true, false, nil
	}
	task, found, err := g.executions.LadoTask(ctx, req.TaskID)
	if err != nil {
		return true, false, fmt.Errorf("LADO gate: read task %s: %w", req.TaskID, err)
	}
	if !found || task.ProjectID != cfg.ProjectID {
		return true, false, nil
	}
	if role == domain.LadoPlanRoleDesigner {
		return true, req.WorkerStartedAt.IsZero() && req.WorkerAttempts == attempt, nil
	}
	critique, found, err := g.plans.LadoPlanCritique(ctx, id, attempt)
	if err != nil {
		return true, false, fmt.Errorf("LADO gate: read plan critique %s/%d: %w", id, attempt, err)
	}
	return true, found && critique.State == domain.LadoCritiqueRunning && critique.SessionID == "", nil
}

// ReadOnlySession reports whether a session must run read-only: a plan
// worker. The Chat service then asks the provider for a read-only sandbox,
// which Codex enforces. A session that cannot be read is not one.
func (g *Gate) ReadOnlySession(ctx context.Context, target domain.SessionID) bool {
	rec, found, err := g.sessions.GetSession(ctx, target)
	return err == nil && found && domain.IsLadoPlanWorkerClientRequestID(rec.ClientRequestID)
}

// ClampTurnSettings lowers the approval of an execution's next turns to its
// ceiling when it asks for more. An empty approval keeps the conversation's
// starting mode, which is the snapshot's own.
func (g *Gate) ClampTurnSettings(ctx context.Context, target domain.SessionID, s ports.ChatTurnSettings) (ports.ChatTurnSettings, error) {
	policy, execution, err := g.execution(ctx, target)
	if err != nil {
		return ports.ChatTurnSettings{}, err
	}
	if execution && s.Approval != "" && aboveCeiling(s.Approval, policy.Ceiling) {
		s.Approval = policy.Ceiling
	}
	return s, nil
}

// AuthorizeConfigMode refuses a config option that would switch an execution
// to an approval mode above its ceiling.
func (g *Gate) AuthorizeConfigMode(ctx context.Context, target domain.SessionID, mode domain.PermissionMode) error {
	policy, execution, err := g.execution(ctx, target)
	if err != nil || !execution || !aboveCeiling(mode, policy.Ceiling) {
		return err
	}
	return apierr.Conflict("LADO_APPROVAL_ABOVE_CEILING",
		"This session runs an approved LADO task: its approval mode cannot go above the task's permission ceiling.",
		map[string]any{"sessionId": string(target), "mode": string(mode), "ceiling": string(policy.Ceiling)})
}

// AuthorizeExecutionChange refuses an agent switch or an interface transition
// of an execution.
func (g *Gate) AuthorizeExecutionChange(ctx context.Context, target domain.SessionID) error {
	_, execution, err := g.execution(ctx, target)
	if err != nil || !execution {
		return err
	}
	return apierr.Conflict("LADO_EXECUTION_FIXED",
		"This session runs an approved LADO task: its agent and interface stay as approved.",
		map[string]any{"sessionId": string(target)})
}

// AuthorizeRestore refuses to restore or resume an execution LADO stopped for
// good: one that ran past its maximum run time, or whose task was cancelled.
// Other executions, and sessions that are not executions, restore as AO
// restores them.
func (g *Gate) AuthorizeRestore(ctx context.Context, target domain.SessionID) error {
	exec, ok, err := g.executions.LadoExecution(ctx, target)
	if err != nil {
		return fmt.Errorf("LADO gate: read execution %s: %w", target, err)
	}
	if !ok {
		return nil
	}
	reason := ""
	if !exec.TimedOutAt.IsZero() {
		reason = domain.LadoFailedTimedOut
	} else {
		task, found, err := g.executions.LadoTask(ctx, exec.TaskID)
		if err != nil {
			return fmt.Errorf("LADO gate: read task %s: %w", exec.TaskID, err)
		}
		if found && !task.CancelledAt.IsZero() {
			reason = "task_cancelled"
		}
	}
	if reason == "" {
		return nil
	}
	return apierr.Conflict("LADO_EXECUTION_FIXED",
		"This session ran an approved LADO task that LADO stopped, past its maximum run time or for a cancelled task: it cannot be restored.",
		map[string]any{"sessionId": string(target), "reason": reason})
}

// execution reads whether target is an execution, a session started for a
// queue entry, and what it keeps from the entry's rule snapshot. The session's
// client request id finds the entry, so this holds from the spawn on, before
// the dispatcher links the execution. A plan worker counts as one, with the
// ceiling auto.
func (g *Gate) execution(ctx context.Context, target domain.SessionID) (domain.LadoEntryPolicy, bool, error) {
	rec, found, err := g.sessions.GetSession(ctx, target)
	if err != nil {
		return domain.LadoEntryPolicy{}, false, fmt.Errorf("LADO gate: read session %s: %w", target, err)
	}
	if !found {
		return domain.LadoEntryPolicy{}, false, nil
	}
	if domain.IsLadoPlanWorkerClientRequestID(rec.ClientRequestID) {
		// A plan worker runs in auto at most, never bypass, and keeps its
		// agent and interface, as an execution does.
		return domain.LadoEntryPolicy{Ceiling: domain.PermissionModeAuto}, true, nil
	}
	policy, execution, err := g.policies.LadoEntryPolicyByClientRequest(ctx, rec.ClientRequestID)
	if err != nil {
		return domain.LadoEntryPolicy{}, false, fmt.Errorf("LADO gate: session %s: %w", target, err)
	}
	if execution && domain.LadoPermissionRank(policy.Ceiling) == 0 {
		// Dispatch blocks a snapshot without a ceiling; refuse rather than
		// guess one.
		return domain.LadoEntryPolicy{}, false, fmt.Errorf("LADO gate: session %s: rule snapshot has no permission ceiling", target)
	}
	return policy, execution, nil
}

// aboveCeiling reports whether mode allows an execution more than its
// ceiling. A mode that ranks below every ceiling, such as default, counts as
// above: with it each agent decides what it allows, and Codex runs it as
// bypass.
func aboveCeiling(mode, ceiling domain.PermissionMode) bool {
	rank := domain.LadoPermissionRank(mode)
	return rank == 0 || rank > domain.LadoPermissionRank(ceiling)
}

func (g *Gate) ladoMode(ctx context.Context, projectID domain.ProjectID) (bool, error) {
	settings, err := g.settings.LadoProjectSettings(ctx, projectID)
	if err != nil {
		return false, fmt.Errorf("LADO gate: %w", err)
	}
	return settings.LadoMode, nil
}
