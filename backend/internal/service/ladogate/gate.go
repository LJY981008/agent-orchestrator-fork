// Package ladogate is LADO's worker gate (docs/LJY-PRD/TDD.md §6): in a project
// with LADO mode on, workers start only when LADO dispatches them, and only a
// person or AO itself messages a worker. An execution, the worker LADO
// dispatched, keeps the agent, interface and permission ceiling it was
// approved with.
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
}

// Policies reads what an execution keeps from its rule snapshot, found
// through the client request id its session was started with.
type Policies interface {
	LadoEntryPolicyByClientRequest(ctx context.Context, clientRequestID string) (domain.LadoEntryPolicy, bool, error)
}

// Gate implements ports.WorkGate. Every read error refuses the work.
type Gate struct {
	settings Settings
	sessions Sessions
	grants   Grants
	policies Policies
}

var _ ports.WorkGate = (*Gate)(nil)

// New returns a gate over LADO's settings, AO's sessions, and the dispatch
// grants and rule snapshots of LADO's queue.
func New(settings Settings, sessions Sessions, grants Grants, policies Policies) *Gate {
	return &Gate{settings: settings, sessions: sessions, grants: grants, policies: policies}
}

// AuthorizeSpawn refuses automations and every new worker in a LADO project
// but the one LADO's dispatcher starts for the queue entry it holds;
// orchestrators still start there.
func (g *Gate) AuthorizeSpawn(ctx context.Context, cfg ports.SpawnConfig) error {
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
	case cfg.Kind == domain.KindOrchestrator:
		return nil
	}
	granted, err := g.granted(ctx, cfg)
	if err != nil || granted {
		return err
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
// attempt right now, and the entry is of the spawn's project and has the
// spawn's dispatch hash.
func (g *Gate) granted(ctx context.Context, cfg ports.SpawnConfig) (bool, error) {
	entryID, attempt, ok := domain.ParseLadoClientRequestID(cfg.ClientRequestID)
	if !ok {
		return false, nil
	}
	grant, err := g.grants.LadoDispatchGrant(ctx, entryID, attempt, time.Now())
	if err != nil {
		return false, fmt.Errorf("LADO gate: %w", err)
	}
	return grant.Active && grant.ProjectID == cfg.ProjectID && grant.Hash == cfg.ClientRequestHash, nil
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

// execution reads whether target is an execution, a session started for a
// queue entry, and what it keeps from the entry's rule snapshot. The session's
// client request id finds the entry, so this holds from the spawn on, before
// the dispatcher links the execution.
func (g *Gate) execution(ctx context.Context, target domain.SessionID) (domain.LadoEntryPolicy, bool, error) {
	rec, found, err := g.sessions.GetSession(ctx, target)
	if err != nil {
		return domain.LadoEntryPolicy{}, false, fmt.Errorf("LADO gate: read session %s: %w", target, err)
	}
	if !found {
		return domain.LadoEntryPolicy{}, false, nil
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
