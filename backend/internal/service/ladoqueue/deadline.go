package ladoqueue

// The maximum run time (decision g of the step 6 plan, as the owner revised
// it on 2026-10-26): a worker past its deadline that has not opened a PR is
// stopped through AO, its worktree kept as it is, and the run is marked timed
// out, so the task shows Failed. Once a run has a PR, in whatever state, the
// limit no longer applies: what is left is the PR's review.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// stopOverdue stops each execution without a PR whose session still runs
// past its deadline, and then marks it timed out with its audit event. A worker that
// could not be stopped stays unmarked and is stopped again on the next pass:
// a task shows Failed only once its worker no longer runs. It returns the
// reads and writes that failed, joined.
func (s *Service) stopOverdue(ctx context.Context, now time.Time) error {
	overdue, err := s.deps.Store.LadoExecutionsPastDeadline(ctx, now)
	if err != nil {
		return fmt.Errorf("list the executions past their deadline: %w", err)
	}
	var problems []error
	for _, exec := range overdue {
		if ctx.Err() != nil {
			break
		}
		if exec.PR.URL != "" {
			continue
		}
		if _, err := s.deps.Sessions.RequestKill(ctx, exec.SessionID); err != nil {
			s.deps.Log.Warn("LADO could not stop a worker past its deadline", "task", exec.TaskID, "session", exec.SessionID,
				"deadline", exec.DeadlineAt, "problem", problem(err, "the session could not be stopped"))
			continue
		}
		if err := s.markTimedOut(context.WithoutCancel(ctx), exec); err != nil {
			problems = append(problems, fmt.Errorf("execution %s: %w", exec.SessionID, err))
		}
	}
	return errors.Join(problems...)
}

// markTimedOut records the stopped run as timed out now, with its audit event.
func (s *Service) markTimedOut(ctx context.Context, exec domain.LadoExecution) error {
	payload, err := json.Marshal(map[string]any{"task": exec.TaskID, "session": exec.SessionID, "deadline": exec.DeadlineAt})
	if err != nil {
		return err
	}
	marked, err := s.deps.Store.MarkLadoExecutionTimedOut(ctx, exec.SessionID, domain.LadoAuditEvent{
		At:          s.deps.Clock().UTC(),
		Actor:       "lado",
		Action:      "execution.timed_out",
		SubjectKind: "execution",
		SubjectID:   string(exec.SessionID),
		Payload:     payload,
	})
	if err != nil {
		return fmt.Errorf("mark timed out: %w", err)
	}
	if marked {
		s.deps.Log.Info("LADO stopped a worker at its deadline", "task", exec.TaskID, "session", exec.SessionID, "deadline", exec.DeadlineAt)
	}
	return nil
}
