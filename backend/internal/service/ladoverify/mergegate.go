package ladoverify

// LADO fork: the merge gate (decision n of the step 8 plan;
// docs/LJY-PRD/TDD.md §7.2). AO's merge route, behind the desktop's and the
// mobile app's Merge buttons, asks it before it reads the PR from its host.
// The PR of a LADO execution merges only at a head whose integration the
// developer approved, into the branch it was approved into; the PR of
// another session of a project in LADO mode has no such approval and does
// not merge through AO. Other PRs merge as before.

import (
	"context"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.LadoMergeGate = (*Service)(nil)

// AuthorizeMerge implements ports.LadoMergeGate. A PR is LADO's when its
// session is an execution or when any LADO record, a verification or an
// integration approval, names its URL: a PR claimed into another session
// stays LADO's (S7 of #15). Such a PR merges only with an approval of that
// PR at headSHA into the branch it merges into now: an approval into another
// branch refuses with LADO_INTEGRATION_TARGET_CHANGED (S5). Without any LADO
// record, a PR of a session of a project in LADO mode refuses too. A read
// that fails refuses the merge.
func (s *Service) AuthorizeMerge(ctx context.Context, pr domain.PullRequest, headSHA string) error {
	head := strings.ToLower(strings.TrimSpace(headSHA))
	_, isExecution, err := s.deps.Store.LadoExecution(ctx, pr.SessionID)
	if err != nil {
		return s.mergeGateFailed(pr, head, err)
	}
	naming, err := s.deps.Store.LadoSessionsNamingPR(ctx, pr.URL)
	if err != nil {
		return s.mergeGateFailed(pr, head, err)
	}
	if !isExecution && len(naming) == 0 {
		inLado, err := s.inLadoProject(ctx, pr.SessionID)
		switch {
		case err != nil:
			return s.mergeGateFailed(pr, head, err)
		case inLado:
			return approvalRequired(pr, head)
		}
		return nil
	}
	approvals, err := s.deps.Store.ListLadoIntegrationApprovalsForPR(ctx, pr.URL, head)
	if err != nil {
		return s.mergeGateFailed(pr, head, err)
	}
	// Only the approvals of the executions the PR belongs to count.
	approvals = slices.DeleteFunc(approvals, func(a domain.LadoIntegrationApproval) bool {
		return a.ExecutionSessionID != pr.SessionID && !slices.Contains(naming, a.ExecutionSessionID)
	})
	if _, ok := domain.LadoApprovalFor(approvals, pr.TargetBranch); ok {
		return nil
	}
	if len(approvals) > 0 {
		return targetChanged(pr, head, approvals[len(approvals)-1].TargetBranch)
	}
	return approvalRequired(pr, head)
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

// targetChanged refuses a PR that merges into another branch than approved,
// the branch an approval holds for or, before the approval, the branch the
// record was verified for, as after its base was changed on the host.
func targetChanged(pr domain.PullRequest, head, approved string) error {
	return apierr.Conflict("LADO_INTEGRATION_TARGET_CHANGED",
		"The pull request now merges into another branch. Review it again in the AO desktop app, then approve its integration.",
		map[string]any{"prUrl": pr.URL, "headSha": head, "sessionId": string(pr.SessionID), "approvedTarget": approved, "currentTarget": pr.TargetBranch})
}

func (s *Service) mergeGateFailed(pr domain.PullRequest, head string, err error) error {
	s.deps.Log.Error("LADO could not check a merge against its integration approvals; it refuses the merge", "pr", pr.URL, "head", head, "err", err)
	return apierr.Internal("LADO_MERGE_GATE_FAILED", "LADO could not check this pull request's integration approval, so it did not merge.")
}
