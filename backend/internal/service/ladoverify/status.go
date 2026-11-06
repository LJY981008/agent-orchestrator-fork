package ladoverify

// The lado/integration commit status (decision m of the step 8 plan;
// docs/LJY-PRD/TDD.md §7.1). Once the developer approved a head, LADO posts a
// success status on it, which a ruleset that requires the check turns into
// the permission to merge. A post that failed is tried again by the loop.
// Once LADO revoked the approval (#53), it posts a pending status instead.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// statusRetryAfter is how long LADO waits before it posts an approval's
	// status again.
	statusRetryAfter = 5 * time.Minute
	// integrationStatusState and integrationStatusDescription are what the
	// status says.
	integrationStatusState       = "success"
	integrationStatusDescription = "Approved in LADO"
)

// retryStatuses posts again the statuses of the approvals still pending or
// failed, each at most every statusRetryAfter: from when this daemon last
// tried, so at once after a restart. The store stops listing an approval
// after its last attempt (domain.LadoIntegrationStatusMaxAttempts).
func (s *Service) retryStatuses(ctx context.Context) error {
	approvals, err := s.deps.Store.ListLadoIntegrationStatusRetries(ctx, statusBatch)
	if err != nil {
		return fmt.Errorf("list the integration statuses to post: %w", err)
	}
	var problems []error
	for _, approval := range approvals {
		if ctx.Err() != nil {
			break
		}
		s.mu.Lock()
		last, tried := s.statusTried[approval.ID]
		s.mu.Unlock()
		if tried && s.now().Sub(last) < statusRetryAfter {
			continue
		}
		if _, err := s.postIntegrationStatus(ctx, approval); err != nil {
			problems = append(problems, fmt.Errorf("integration approval %s: %w", approval.ID, err))
		}
	}
	return errors.Join(problems...)
}

// postIntegrationStatus posts the lado/integration success status, or
// pending for a revoked approval, on the approval's head and records how it
// went: posted, failed with one line of the error and one more attempt, or
// not_applicable for a host LADO posts no status to (GitLab). It returns the approval as recorded; an error means the
// approval's repository could not be read, the outcome could not be
// recorded, or ctx ended. The integration approval calls it right after
// recording an approval; the loop calls it again for those still pending or
// failed.
func (s *Service) postIntegrationStatus(ctx context.Context, approval domain.LadoIntegrationApproval) (domain.LadoIntegrationApproval, error) {
	s.mu.Lock()
	s.statusTried[approval.ID] = s.now()
	s.mu.Unlock()
	repo, err := s.approvalRepo(ctx, approval)
	if err != nil {
		return approval, err
	}
	state, attempts, postedAt, message := domain.LadoIntegrationStatusNotApplicable, approval.StatusAttempts, time.Time{}, ""
	if repo.Provider != domain.TrackerProviderGitLab {
		err := s.postStatus(ctx, repo, approval)
		if ctx.Err() != nil {
			// The caller gave up; the answer says nothing about the host.
			return approval, ctx.Err()
		}
		switch {
		case err == nil:
			state, attempts, postedAt = domain.LadoIntegrationStatusPosted, attempts+1, s.now()
		case errors.Is(err, ports.ErrLadoUnsupported):
		default:
			state, attempts, message = domain.LadoIntegrationStatusFailed, attempts+1, redactedErrorLine(err)
		}
	}
	if err := s.deps.Store.SetLadoIntegrationStatus(ctx, approval.ID, state, attempts, postedAt, message); err != nil {
		return approval, fmt.Errorf("record the integration status: %w", err)
	}
	if state != domain.LadoIntegrationStatusFailed || attempts >= domain.LadoIntegrationStatusMaxAttempts {
		s.mu.Lock()
		delete(s.statusTried, approval.ID)
		s.mu.Unlock()
	}
	if state == domain.LadoIntegrationStatusFailed {
		s.deps.Log.Warn("LADO could not post the integration status", "approval", approval.ID, "head", approval.HeadSHA, "attempts", attempts, "err", message)
	}
	approval.StatusState, approval.StatusAttempts, approval.StatusPostedAt, approval.StatusError = state, attempts, postedAt, message
	return approval, nil
}

// postStatus posts the approval's status on its head through the host's
// client: success, or pending once the approval was revoked.
func (s *Service) postStatus(ctx context.Context, repo domain.TrackerRepo, approval domain.LadoIntegrationApproval) error {
	client, err := s.deps.Clients.ForRepo(ctx, repo)
	if err != nil {
		return err
	}
	status := ports.LadoCommitStatus{
		State:       integrationStatusState,
		Context:     domain.LadoIntegrationStatusContext,
		Description: integrationStatusDescription,
	}
	if approval.Revoked() {
		status.State, status.Description = "pending", revokedStatusDescription
		if approval.RevokedReason == domain.LadoIntegrationSiblingRevoked {
			status.Description = siblingRevokedStatusDescription
		}
	}
	return client.PostCommitStatus(ctx, repo, approval.HeadSHA, status)
}

// approvalRepo is the repository of the approval's PR, as its record was
// verified in (verifyRepo): in a workspace project the PR's own, which may
// be another than the task's issue's (#59), else the task's.
func (s *Service) approvalRepo(ctx context.Context, approval domain.LadoIntegrationApproval) (domain.TrackerRepo, error) {
	exec, ok, err := s.deps.Store.LadoExecution(ctx, approval.ExecutionSessionID)
	switch {
	case err != nil:
		return domain.TrackerRepo{}, fmt.Errorf("read execution %s: %w", approval.ExecutionSessionID, err)
	case !ok:
		return domain.TrackerRepo{}, fmt.Errorf("execution %s not found", approval.ExecutionSessionID)
	}
	task, ok, err := s.deps.Tasks.LadoTask(ctx, exec.TaskID)
	switch {
	case err != nil:
		return domain.TrackerRepo{}, fmt.Errorf("read task %s: %w", exec.TaskID, err)
	case !ok:
		return domain.TrackerRepo{}, fmt.Errorf("task %s not found", exec.TaskID)
	}
	place, err := s.verifyRepo(ctx, task, exec.SessionID, cmp.Or(approval.PRURL, exec.PR.URL))
	if err != nil {
		return domain.TrackerRepo{}, err
	}
	return place.repo, nil
}
