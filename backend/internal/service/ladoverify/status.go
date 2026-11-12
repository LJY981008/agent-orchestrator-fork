package ladoverify

// The lado/integration commit status (decision m of the step 8 plan;
// docs/LJY-PRD/TDD.md §7.1). Once the developer approved a head, LADO posts a
// success status on it, which a ruleset that requires the check turns into
// the permission to merge. A post that failed is tried again by the loop.
// Once LADO revoked the approval (#53), it posts a pending status instead.
// GitLab (#21) gets the same statuses on the merge request's pipeline, and a
// pending one on each head before its approval (gitlab_pending.go): with
// "Pipelines must succeed" on, GitLab merges only once LADO's status passes.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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
// not_applicable for a host whose client posts no status
// (ErrLadoUnsupported). On GitLab (#21) the status names the merge request's
// source branch and number, which pick the pipeline it joins; a PR AO knows
// neither of fails as source branch unknown. The post holds the lock of the
// repository and head, which the waiting status of an unapproved GitLab head
// takes too (gitlab_pending.go), and the host's answer becomes the
// repository's last post (ProtectionRead.StatusPostFailed). It returns the
// approval as recorded; an error means the approval's repository or PR could
// not be read, the outcome could not be recorded, or ctx ended. The
// integration approval calls it right after recording an approval; the loop
// calls it again for those still pending or failed.
//
// #97: the status of a live approval whose record still has pre-merge SQL
// to apply is held (DBChanges.HoldIntegrationStatus): nothing is posted or
// recorded, no attempt counts, and the approval is returned as it was. A
// hold that cannot be checked posts nothing either and returns the error.
func (s *Service) postIntegrationStatus(ctx context.Context, approval domain.LadoIntegrationApproval) (domain.LadoIntegrationApproval, error) {
	s.mu.Lock()
	s.statusTried[approval.ID] = s.now()
	s.mu.Unlock()
	repo, err := s.approvalRepo(ctx, approval)
	if err != nil {
		return approval, err
	}
	unlock := s.lockHead(repo, approval.HeadSHA)
	defer unlock()
	return s.postIntegrationStatusLocked(ctx, repo, approval)
}

// postIntegrationStatusLocked is postIntegrationStatus on repo, with the
// lock of repo and the approval's head held.
func (s *Service) postIntegrationStatusLocked(ctx context.Context, repo domain.TrackerRepo, approval domain.LadoIntegrationApproval) (domain.LadoIntegrationApproval, error) {
	// #97: an approved head whose pre-merge SQL is not applied yet waits;
	// its hold keeps it out of the retries until the SQL is applied and the
	// database change service posts it (RepostIntegrationStatusOf). The
	// GitLab waiting status's success over itself waits the same way.
	held, err := s.dbChangesHeld(ctx, approval)
	if err != nil || held {
		s.mu.Lock()
		delete(s.statusTried, approval.ID)
		s.mu.Unlock()
		if err != nil {
			return approval, fmt.Errorf("check the approval's database changes: %w", err)
		}
		return approval, nil
	}
	state, attempts, postedAt, message := domain.LadoIntegrationStatusNotApplicable, approval.StatusAttempts, time.Time{}, ""
	status := integrationStatusOf(approval)
	if repo.Provider == domain.TrackerProviderGitLab {
		status.Ref, status.MergeRequest, err = s.mergeRequestRef(ctx, approval.PRURL)
	}
	if err == nil {
		err = s.postCommitStatus(ctx, repo, approval.HeadSHA, status)
		if ctx.Err() != nil {
			// The caller gave up; the answer says nothing about the host.
			return approval, ctx.Err()
		}
		if !errors.Is(err, ports.ErrLadoUnsupported) {
			s.protection.notePost(repo, err != nil)
		}
	} else if !errors.Is(err, errSourceBranchUnknown) {
		return approval, err
	}
	switch {
	case err == nil:
		state, attempts, postedAt = domain.LadoIntegrationStatusPosted, attempts+1, s.now()
	case errors.Is(err, ports.ErrLadoUnsupported):
	default:
		state, attempts, message = domain.LadoIntegrationStatusFailed, attempts+1, redactedErrorLine(err)
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

// integrationStatusOf is the status of the approval: success, or pending
// once the approval was revoked.
func integrationStatusOf(approval domain.LadoIntegrationApproval) ports.LadoCommitStatus {
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
	return status
}

// postCommitStatus posts status on sha through the host's client.
func (s *Service) postCommitStatus(ctx context.Context, repo domain.TrackerRepo, sha string, status ports.LadoCommitStatus) error {
	client, err := s.deps.Clients.ForRepo(ctx, repo)
	if err != nil {
		return err
	}
	return client.PostCommitStatus(ctx, repo, sha, status)
}

// errSourceBranchUnknown means AO knows neither the source branch nor the
// number of a GitLab merge request, so LADO cannot tell the pipeline its
// status joins.
var errSourceBranchUnknown = errors.New("source branch unknown")

// mergeRequestRef is the source branch and number of the PR at url, as AO
// observed it, for a GitLab status (ports.LadoCommitStatus.Ref and
// MergeRequest). A PR AO has not stored or stored without either is
// errSourceBranchUnknown.
func (s *Service) mergeRequestRef(ctx context.Context, url string) (string, int, error) {
	pr, ok, err := s.deps.Store.GetPR(ctx, url)
	switch {
	case err != nil:
		return "", 0, fmt.Errorf("read pull request %s: %w", url, err)
	case !ok || (pr.SourceBranch == "" && pr.Number <= 0):
		return "", 0, errSourceBranchUnknown
	}
	return pr.SourceBranch, max(pr.Number, 0), nil
}

// lockHead takes the lock of one head of one repository, which every
// lado/integration post on it holds (#21), and returns its release. Locks
// are dropped once no one holds or waits for them.
func (s *Service) lockHead(repo domain.TrackerRepo, head string) func() {
	key := statusRepoKey(repo) + "\x00" + strings.ToLower(head)
	s.mu.Lock()
	lock, ok := s.headLocks[key]
	if !ok {
		lock = &headLock{}
		s.headLocks[key] = lock
	}
	lock.users++
	s.mu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.mu.Lock()
		lock.users--
		if lock.users == 0 {
			delete(s.headLocks, key)
		}
		s.mu.Unlock()
	}
}

// headLock is the lock of one head (lockHead); users counts those who hold
// or wait for it.
type headLock struct {
	mu    sync.Mutex
	users int
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
