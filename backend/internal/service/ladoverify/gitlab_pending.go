package ladoverify

// LADO fork (#21): GitLab merges a merge request once its pipeline succeeds
// ("Pipelines must succeed"), and a project whose CI runs on merge request
// branches would let a passing head merge before LADO's approval. So each
// pass posts a pending lado/integration status on every open GitLab head
// still without an approval, which keeps the head's pipeline from
// succeeding until the approval's success replaces it. The status goes up
// again every waitingRepostEvery, so a new pipeline of the head (a retry)
// takes it too. What remains: a pipeline that starts and passes inside that
// window can be merged on GitLab; LADO then records merged_without_approval.
//
// The post and the approval's success race: the loop may read no approval,
// the approval may then be recorded and post its success, and the loop's
// pending would bury it. Both posts hold the lock of the repository and
// head; under it the loop reads the approvals before it posts and again
// after, and posts the success of an approval recorded in between at once.
// The approval is recorded outside the lock, in its transaction, so the
// second read is needed.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// waitingRepostEvery is how often LADO posts the waiting status of one
	// unapproved head: from when this daemon last posted it, so at once
	// after a restart.
	waitingRepostEvery = 2 * time.Minute
	// waitingStatusDescription is what the waiting status says.
	waitingStatusDescription = "Waiting for approval in LADO"
)

// postWaitingStatuses posts the waiting status on the current head of each
// open GitLab PR among executions that has no integration approval holding
// for its target, at most every waitingRepostEvery per head. A merged,
// closed or approved head, and any head on another host, is left alone.
// What fails is logged and becomes the repository's last post
// (ProtectionRead.StatusPostFailed); it is kept out of the records.
func (s *Service) postWaitingStatuses(ctx context.Context, executions []domain.LadoExecution) {
	s.forgetWaitingPosts()
	tasks := map[domain.LadoTaskID]*domain.LadoTask{}
	for _, exec := range executions {
		if ctx.Err() != nil {
			return
		}
		if exec.PR.URL == "" || exec.PR.Merged || exec.PR.Closed || exec.PR.HeadSHA == "" {
			continue
		}
		key := waitingKey(exec)
		s.mu.Lock()
		_, posted := s.waitingPosted[key]
		s.mu.Unlock()
		if posted {
			continue
		}
		task, read := tasks[exec.TaskID]
		if !read {
			found, ok, err := s.deps.Tasks.LadoTask(ctx, exec.TaskID)
			if err == nil && ok {
				task = &found
			}
			tasks[exec.TaskID] = task
		}
		if task == nil {
			// The pass reports it when it verifies the execution.
			continue
		}
		place, err := s.verifyRepo(ctx, *task, exec.SessionID, exec.PR.URL)
		if err != nil || place.repo.Provider != domain.TrackerProviderGitLab {
			continue
		}
		s.mu.Lock()
		s.waitingPosted[key] = s.now()
		s.mu.Unlock()
		if err := s.postWaitingStatus(ctx, exec, place.repo); err != nil && ctx.Err() == nil {
			s.deps.Log.Warn("LADO could not post the waiting status of an unapproved GitLab head",
				"session", exec.SessionID, "pr", exec.PR.URL, "head", exec.PR.HeadSHA, "err", redactedErrorLine(err))
		}
	}
}

// postWaitingStatus posts the waiting status on the execution's head in
// repo unless an approval holds for its target, with the lock of the head
// held. Once it posted, it reads the approvals again and posts the success
// of one recorded in the meantime.
func (s *Service) postWaitingStatus(ctx context.Context, exec domain.LadoExecution, repo domain.TrackerRepo) error {
	head, target := exec.PR.HeadSHA, approvalTarget(exec)
	unlock := s.lockHead(repo, head)
	defer unlock()
	if _, approved, err := s.headApproval(ctx, exec, target); err != nil || approved {
		return err
	}
	status := ports.LadoCommitStatus{
		State:       "pending",
		Context:     domain.LadoIntegrationStatusContext,
		Description: waitingStatusDescription,
	}
	var err error
	if status.Ref, status.MergeRequest, err = s.mergeRequestRef(ctx, exec.PR.URL); err != nil {
		return err
	}
	err = s.postCommitStatus(ctx, repo, head, status)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, ports.ErrLadoUnsupported):
		return nil
	}
	s.protection.notePost(repo, err != nil)
	if err != nil {
		return fmt.Errorf("post the waiting status: %w", err)
	}
	approval, approved, err := s.headApproval(ctx, exec, target)
	if err != nil || !approved || approval.StatusState == domain.LadoIntegrationStatusNotApplicable {
		return err
	}
	// The approval came while the waiting status went up: its success goes
	// up again over it.
	s.mu.Lock()
	s.statusTried[approval.ID] = s.now()
	s.mu.Unlock()
	_, err = s.postIntegrationStatusLocked(ctx, repo, approval)
	return err
}

// headApproval returns the integration approval of the execution's head that
// holds for target, if any.
func (s *Service) headApproval(ctx context.Context, exec domain.LadoExecution, target string) (domain.LadoIntegrationApproval, bool, error) {
	approvals, err := s.deps.Store.ListLadoIntegrationApprovals(ctx, exec.SessionID, exec.PR.URL, exec.PR.HeadSHA)
	if err != nil {
		return domain.LadoIntegrationApproval{}, false, fmt.Errorf("read the integration approvals of head %s: %w", exec.PR.HeadSHA, err)
	}
	approval, ok := domain.LadoApprovalFor(approvals, target)
	return approval, ok, nil
}

// waitingKey names one head of one PR for the waiting status.
func waitingKey(exec domain.LadoExecution) string {
	return exec.PR.URL + "\x00" + exec.PR.HeadSHA
}

// forgetWaitingPosts forgets the waiting statuses posted waitingRepostEvery
// ago or more, whose heads take theirs again.
func (s *Service) forgetWaitingPosts() {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, at := range s.waitingPosted {
		if now.Sub(at) >= waitingRepostEvery {
			delete(s.waitingPosted, key)
		}
	}
}
