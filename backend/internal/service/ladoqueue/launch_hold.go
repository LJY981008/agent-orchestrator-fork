package ladoqueue

// Holding a new worker (issue #42, plan step 15 and decision 4). A worker
// started on an account at its usage limit stops at once and spends an
// attempt; one started while the agent's executable is missing fails its
// spawn and burns its client request id. So before it starts a single, part,
// merge or integration worker, the dispatcher asks whether the account a new
// session would run on is at its limit, and then checks the agent itself.
// Either holds the worker without spending an attempt: the queue entry or
// the part waits with LadoWaitingAccountLimited, checked again every minute
// until the limit lifts, or with LadoWaitingAgentUnavailable for a minute. The integration worker has no
// waiting reason of its own; it waits as one does for a worker slot.

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// AccountLimits tells whether the account a new worker of an agent would
// start on is at its usage limit. *ladowatch.Service implements it.
type AccountLimits interface {
	LaunchAccountLimit(ctx context.Context, harness domain.AgentHarness, now time.Time) (domain.LadoAccountLimit, bool, error)
}

// Preflight checks that an agent can start a session now; an agent whose
// executable is missing or does not run fails. *agent.Service implements it.
type Preflight interface {
	PreflightLaunch(ctx context.Context, harness domain.AgentHarness) error
}

// launchHold is why a new worker waits, if it does.
type launchHold struct {
	// reason is LadoWaitingAccountLimited, LadoWaitingAgentUnavailable or ""
	// when the worker may start; detail is the reset time or the agent.
	reason string
	detail string
	until  time.Time
}

// launchHolds caches one pass's holds by agent, so a pass asks once.
type launchHolds map[domain.AgentHarness]launchHold

// launchHold tells whether a new worker of task with harness waits at now.
// A limit that cannot be read holds nothing; an agent that fails its check
// raises the task's alert, once. A check stopped by the pass's end holds
// nothing either: the caller hands the work back.
func (s *Service) launchHold(ctx context.Context, task domain.LadoTaskID, harness domain.AgentHarness, now time.Time, cache launchHolds) launchHold {
	hold, ok := cache[harness]
	if !ok {
		hold = s.checkLaunch(ctx, task, harness, now)
		if cache != nil && ctx.Err() == nil {
			cache[harness] = hold
		}
	}
	if hold.reason == domain.LadoWaitingAgentUnavailable {
		if _, _, err := s.deps.Store.AddLadoAlert(context.WithoutCancel(ctx), domain.LadoAlert{
			TaskID: task, Kind: domain.LadoAlertAgentUnavailable, Level: domain.LadoAlertLevelCrossed, CreatedAt: now,
		}); err != nil {
			s.deps.Log.Warn("LADO could not raise an alert", "task", task, "kind", domain.LadoAlertAgentUnavailable, "err", err)
		}
	}
	return hold
}

func (s *Service) checkLaunch(ctx context.Context, task domain.LadoTaskID, harness domain.AgentHarness, now time.Time) launchHold {
	if s.deps.AccountLimits != nil {
		limit, ok, err := s.deps.AccountLimits.LaunchAccountLimit(ctx, harness, now)
		switch {
		case err != nil:
			s.deps.Log.Warn("LADO could not read the usage limits of a new worker's account", "task", task, "agent", harness, "err", err)
		case ok:
			// The limit may lift before its reset (a turn that completed on
			// the account, another default account), so the entry checks
			// again soon; the reset is only the detail.
			until := now.Add(recheckAfter)
			if limit.ResetAt.Before(until) {
				until = limit.ResetAt
			}
			return launchHold{reason: domain.LadoWaitingAccountLimited, detail: limit.ResetAt.UTC().Format(time.RFC3339), until: until}
		}
	}
	if s.deps.Preflight == nil {
		return launchHold{}
	}
	if err := s.deps.Preflight.PreflightLaunch(ctx, harness); err != nil {
		if ctx.Err() != nil {
			return launchHold{}
		}
		s.deps.Log.Warn("LADO holds a new worker: its agent cannot start", "task", task, "agent", harness, "err", err)
		return launchHold{reason: domain.LadoWaitingAgentUnavailable, detail: string(harness), until: now.Add(recheckAfter)}
	}
	return launchHold{}
}
