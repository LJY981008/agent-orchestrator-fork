package ladoqueue

// One worker per task (docs/LJY-PRD/TDD.md §5.3): a run its task no longer
// wants, because the task was cancelled or a newer approval replaced the
// run's, is stopped through AO on every pass until it no longer runs.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Why LADO stopped a run, in its execution.stopped audit event.
const (
	stopTaskCancelled      = "task_cancelled"
	stopApprovalSuperseded = "approval_superseded"
)

// stopUnwanted stops each running execution its task no longer wants. A
// worker that could not be stopped still runs, so the next pass stops it
// again. It returns the reads and writes that failed, joined.
func (s *Service) stopUnwanted(ctx context.Context) error {
	running, err := s.deps.Store.LiveLadoExecutions(ctx)
	if err != nil {
		return fmt.Errorf("list the running executions: %w", err)
	}
	var problems []error
	for _, exec := range running {
		if ctx.Err() != nil {
			break
		}
		task, ok, err := s.deps.Store.LadoTask(ctx, exec.TaskID)
		if err != nil {
			problems = append(problems, fmt.Errorf("execution %s: read task %s: %w", exec.SessionID, exec.TaskID, err))
			continue
		}
		if !ok {
			continue
		}
		if reason := unwanted(task, exec.PlanApprovalID); reason != "" {
			if err := s.stopRun(ctx, exec.TaskID, exec.SessionID, reason); err != nil {
				problems = append(problems, err)
			}
		}
	}
	return errors.Join(problems...)
}

// unwanted says why the task no longer wants a run of approval, or "" while
// it does.
func unwanted(task domain.LadoTask, approval domain.LadoPlanApprovalID) string {
	switch {
	case !task.CancelledAt.IsZero():
		return stopTaskCancelled
	case task.Plan.Approval.ID != approval:
		return stopApprovalSuperseded
	}
	return ""
}

// stopRun stops a worker its task no longer wants and audits why. A stop that
// failed is logged, not returned: the worker runs on, and the next pass
// stops it again.
func (s *Service) stopRun(ctx context.Context, taskID domain.LadoTaskID, sessionID domain.SessionID, reason string) error {
	if _, err := s.deps.Sessions.RequestKill(ctx, sessionID); err != nil {
		s.deps.Log.Warn("LADO could not stop a worker its task no longer wants", "task", taskID, "session", sessionID,
			"reason", reason, "problem", problem(err, "the session could not be stopped"))
		return nil
	}
	s.deps.Log.Info("LADO stopped a worker its task no longer wants", "task", taskID, "session", sessionID, "reason", reason)
	payload, err := json.Marshal(map[string]any{"task": taskID, "session": sessionID, "reason": reason})
	if err != nil {
		return err
	}
	if _, err := s.deps.Store.AppendLadoAuditEvent(context.WithoutCancel(ctx), domain.LadoAuditEvent{
		At:          s.deps.Clock().UTC(),
		Actor:       "lado",
		Action:      "execution.stopped",
		SubjectKind: "execution",
		SubjectID:   string(sessionID),
		Payload:     payload,
	}); err != nil {
		return fmt.Errorf("execution %s: audit the stop: %w", sessionID, err)
	}
	return nil
}
