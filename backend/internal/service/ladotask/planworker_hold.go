package ladotask

// Holding a new plan worker (issue #42, plan step 15). A designer or critic
// started on an account at its usage limit stops at once, and one started
// while its agent's executable is missing fails its spawn and spends one of
// the request's two starts. So before either starts, LADO asks whether the
// account a new session would run on is at its limit, and then checks the
// agent. A designer waits with the request's waiting reason
// (LadoWaitingAccountLimited or LadoWaitingAgentUnavailable) and starts on a
// later pass; a critic is not started, nor its review recorded, until then.

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// AccountLimits tells whether the account a new session of an agent would
// start on is at its usage limit. *ladowatch.Service implements it.
type AccountLimits interface {
	LaunchAccountLimit(ctx context.Context, harness domain.AgentHarness, now time.Time) (domain.LadoAccountLimit, bool, error)
}

// Preflight checks that an agent can start a session now. *agent.Service
// implements it.
type Preflight interface {
	PreflightLaunch(ctx context.Context, harness domain.AgentHarness) error
}

// planWorkerHold returns why a new plan worker of task waits at now, "" when
// it may start. An agent LADO cannot resolve or keep read-only holds
// nothing: its start fails on that. A limit that cannot be read holds
// nothing either. An agent that cannot start raises the task's alert, once.
func (s *Service) planWorkerHold(ctx context.Context, task domain.LadoTask, now time.Time) string {
	if s.deps.AccountLimits == nil && s.deps.Preflight == nil {
		return ""
	}
	harness, _, err := s.planAgent(ctx, task)
	if err != nil || !planWorkerAgentSupported(harness) {
		return ""
	}
	if s.deps.AccountLimits != nil {
		_, limited, err := s.deps.AccountLimits.LaunchAccountLimit(ctx, harness, now)
		switch {
		case err != nil:
			s.deps.Log.Warn("LADO could not read the usage limits of a plan worker's account", "task", task.ID, "agent", harness, "err", err)
		case limited:
			return domain.LadoWaitingAccountLimited
		}
	}
	if s.deps.Preflight == nil {
		return ""
	}
	if err := s.deps.Preflight.PreflightLaunch(ctx, harness); err != nil {
		if ctx.Err() != nil {
			return ""
		}
		s.deps.Log.Warn("LADO holds a plan worker: its agent cannot start", "task", task.ID, "agent", harness, "err", err)
		if _, _, err := s.deps.Store.AddLadoAlert(context.WithoutCancel(ctx), domain.LadoAlert{
			TaskID: task.ID, Kind: domain.LadoAlertAgentUnavailable, Level: domain.LadoAlertLevelCrossed, CreatedAt: now,
		}); err != nil {
			s.deps.Log.Warn("LADO could not raise an alert", "task", task.ID, "kind", domain.LadoAlertAgentUnavailable, "err", err)
		}
		return domain.LadoWaitingAgentUnavailable
	}
	return ""
}
