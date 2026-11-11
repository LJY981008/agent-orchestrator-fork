package ladodbchange

// The hold of lado/integration: an approved head whose pre-merge SQL is not
// applied must not get its success status, or the host would let anyone
// merge it with the database behind the code. LADO records the hold, so the
// verification loop leaves the approval out of its status retries, and
// releases it once the files are applied (job.go) or no longer wait.

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// HoldIntegrationStatus reports whether the approval's lado/integration
// status must wait: true when the approved record has pre-merge SQL files
// that are not applied, after recording the hold. A record without a
// db_changes item, or whose files are all applied (or whose settings were
// turned off), is not held, and a hold left from before is released.
func (s *Service) HoldIntegrationStatus(ctx context.Context, approval domain.LadoIntegrationApproval, record domain.LadoVerification) (bool, error) {
	details, ok, err := record.DBChanges()
	if err != nil || !ok {
		return false, err
	}
	project, err := s.projectOf(ctx, approval.ExecutionSessionID)
	if err != nil {
		return false, err
	}
	pending, err := s.PendingPreMerge(ctx, project, details)
	if err != nil {
		return false, err
	}
	held, err := s.deps.Store.LadoIntegrationStatusHeld(ctx, approval.ID)
	if err != nil {
		return false, fmt.Errorf("read the hold of approval %s: %w", approval.ID, err)
	}
	if len(pending) == 0 {
		if held {
			if err := s.deps.Store.ReleaseLadoIntegrationStatus(ctx, approval.ID); err != nil {
				return false, fmt.Errorf("release the hold of approval %s: %w", approval.ID, err)
			}
		}
		return false, nil
	}
	if !held {
		if err := s.deps.Store.HoldLadoIntegrationStatus(ctx, approval.ID, s.now()); err != nil {
			return false, fmt.Errorf("hold the status of approval %s: %w", approval.ID, err)
		}
	}
	return true, nil
}

// projectOf returns the project of an execution, through its task.
func (s *Service) projectOf(ctx context.Context, session domain.SessionID) (domain.ProjectID, error) {
	exec, ok, err := s.deps.Store.LadoExecution(ctx, session)
	switch {
	case err != nil:
		return "", fmt.Errorf("read execution %s: %w", session, err)
	case !ok:
		return "", fmt.Errorf("execution %s not found", session)
	}
	task, ok, err := s.deps.Store.LadoTask(ctx, exec.TaskID)
	switch {
	case err != nil:
		return "", fmt.Errorf("read task %s: %w", exec.TaskID, err)
	case !ok:
		return "", fmt.Errorf("task %s not found", exec.TaskID)
	}
	return task.ProjectID, nil
}
