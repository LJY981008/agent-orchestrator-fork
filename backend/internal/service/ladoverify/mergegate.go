package ladoverify

// LADO fork: the merge gate (decision n of the step 8 plan;
// docs/LJY-PRD/TDD.md §7.2). AO's merge route, behind the desktop's and the
// mobile app's Merge buttons, asks it before it reads the PR from its host.
// The PR of a LADO execution merges only at a head whose integration the
// developer approved; the PR of another session of a project in LADO mode has
// no such approval and does not merge through AO. Other PRs merge as before.

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.LadoMergeGate = (*Service)(nil)

// AuthorizeMerge implements ports.LadoMergeGate: the execution's approval
// must be of this PR at headSHA. A read that fails refuses the merge.
func (s *Service) AuthorizeMerge(ctx context.Context, pr domain.PullRequest, headSHA string) error {
	head := strings.ToLower(strings.TrimSpace(headSHA))
	exec, isExecution, err := s.deps.Store.LadoExecution(ctx, pr.SessionID)
	if err != nil {
		return s.mergeGateFailed(pr, head, err)
	}
	if !isExecution {
		inLado, err := s.inLadoProject(ctx, pr.SessionID)
		switch {
		case err != nil:
			return s.mergeGateFailed(pr, head, err)
		case inLado:
			return approvalRequired(pr, head)
		}
		return nil
	}
	approval, approved, err := s.deps.Store.GetLadoIntegrationApproval(ctx, exec.SessionID, head)
	switch {
	case err != nil:
		return s.mergeGateFailed(pr, head, err)
	case !approved || approval.PRURL != pr.URL:
		return approvalRequired(pr, head)
	}
	return nil
}

// inLadoProject reports whether the session belongs to a project in LADO
// mode. A session AO keeps no record of belongs to no project.
func (s *Service) inLadoProject(ctx context.Context, id domain.SessionID) (bool, error) {
	session, ok, err := s.deps.Sessions.GetSession(ctx, id)
	if err != nil || !ok || session.ProjectID == "" {
		return false, err
	}
	settings, err := s.deps.Store.LadoProjectSettings(ctx, session.ProjectID)
	if err != nil {
		return false, err
	}
	return settings.LadoMode, nil
}

func approvalRequired(pr domain.PullRequest, head string) error {
	return apierr.Conflict("LADO_INTEGRATION_APPROVAL_REQUIRED",
		"This pull request is in a LADO project: it merges only at a head whose integration you approved in the AO desktop app.",
		map[string]any{"prUrl": pr.URL, "headSha": head, "sessionId": string(pr.SessionID)})
}

func (s *Service) mergeGateFailed(pr domain.PullRequest, head string, err error) error {
	s.deps.Log.Error("LADO could not check a merge against its integration approvals; it refuses the merge", "pr", pr.URL, "head", head, "err", err)
	return apierr.Internal("LADO_MERGE_GATE_FAILED", "LADO could not check this pull request's integration approval, so it did not merge.")
}
