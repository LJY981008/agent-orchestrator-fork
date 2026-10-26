package ladotask

// What LADO's dispatcher reads from a task before it starts the task's worker
// (docs/LJY-PRD/TDD.md §5.3): the issue as it reads now, and the plan and
// issue the developer approved, as LADO pinned them.

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// IssueCheck is a task's issue as it reads now.
type IssueCheck struct {
	// SHA256 hashes the issue's snapshot with its trusted comments, made as
	// a plan request makes it, so it equals the revision's
	// IssueSnapshotSHA256 while the issue and those comments are unchanged.
	SHA256 string
	State  domain.NormalizedIssueState
}

// IssueCheck reads the task's issue and its comments now and records what it
// saw on the task, so a closed issue shows on the task. A failed read is
// wrapped with the task; the clients keep their tokens to themselves, so it
// carries what the tracker answered and never a token.
func (s *Service) IssueCheck(ctx context.Context, task domain.LadoTask) (IssueCheck, error) {
	issue, comments, err := s.readIssueWithComments(ctx, task)
	if err != nil {
		return IssueCheck{}, fmt.Errorf("check the issue of task %s: %w", task.ID, err)
	}
	snapshot, _ := domain.NewLadoIssueSnapshot(task.IssueRef, issue.Title, issue.Body, comments)
	return IssueCheck{SHA256: snapshot.SHA256(), State: issue.State}, nil
}

// RunInput is what a worker starts from. The dispatcher writes the prompt.
type RunInput struct {
	// PlanText is the approved revision's pinned plan file, front matter and
	// body, byte for byte.
	PlanText string
	// IssueContext is the issue snapshot the revision was planned from,
	// rendered as the orchestrator's issue file: every text in its own fence.
	IssueContext string
	Revision     domain.LadoPlanRevision
	Approval     domain.LadoPlanApproval
	Snapshot     domain.LadoPolicySnapshot
}

// RunInput reads the inputs of the task's newest approval from what LADO
// pinned. It never reads the issue again: the worker starts from the issue
// the plan was made from, which a dispatch compares with IssueCheck first.
func (s *Service) RunInput(ctx context.Context, task domain.LadoTask) (RunInput, error) {
	approval := task.Plan.Approval
	if approval.ID == "" {
		return RunInput{}, fmt.Errorf("task %s has no approved plan", task.ID)
	}
	rev, ok, err := s.deps.Store.LadoPlanRevision(ctx, approval.RevisionID)
	if err != nil {
		return RunInput{}, fmt.Errorf("read plan revision %s: %w", approval.RevisionID, err)
	}
	if !ok || rev.SHA256 != approval.SHA256 {
		return RunInput{}, fmt.Errorf("approval %s names no pinned revision %s", approval.ID, approval.RevisionID)
	}
	plan, err := s.deps.Blobs.Get(rev.SHA256, domain.LadoPlanMaxBytes)
	if err != nil {
		return RunInput{}, fmt.Errorf("read the pinned plan of revision %s: %w", rev.ID, err)
	}
	// The revision keeps the hash of its request's issue snapshot.
	issue, err := s.pinnedIssue(rev.IssueSnapshotSHA256)
	if err != nil {
		return RunInput{}, fmt.Errorf("read the pinned issue of revision %s: %w", rev.ID, err)
	}
	snapshot, ok, err := s.deps.Store.LadoPolicySnapshot(ctx, approval.ID)
	if err != nil {
		return RunInput{}, fmt.Errorf("read the rule snapshot of approval %s: %w", approval.ID, err)
	}
	if !ok {
		return RunInput{}, fmt.Errorf("approval %s has no rule snapshot", approval.ID)
	}
	// The snapshot counts the trusted comments it left out, not the
	// untrusted ones.
	selection := domain.LadoCommentSelection{Omitted: issue.OmittedComments}
	return RunInput{
		PlanText:     string(plan),
		IssueContext: renderIssueFile(task, issue, rev.IssueSnapshotSHA256, selection),
		Revision:     rev,
		Approval:     approval,
		Snapshot:     snapshot,
	}, nil
}
