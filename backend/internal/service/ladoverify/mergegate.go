package ladoverify

// LADO fork: the merge gate (decision n of the step 8 plan;
// docs/LJY-PRD/TDD.md §7.2). AO's merge route, behind the desktop's and the
// mobile app's Merge buttons, asks it before it reads the PR from its host.
// The PR of a LADO execution merges only at a head whose integration the
// developer approved, into the branch it was approved into; the PR of
// another session of a project in LADO mode has no such approval and does
// not merge through AO. Other PRs merge as before.

import (
	"cmp"
	"context"
	"fmt"
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
//
// #53: before it lets an approved head merge, the gate fetches the target
// and, when the remote target moved since the approval, computes the head's
// record again (no check command starts). A record that turned base_overlap
// revokes the approval and refuses with LADO_INTEGRATION_REVOKED; a fetch or
// computation that fails refuses with LADO_MERGE_GATE_FAILED.
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
	if approval, ok := domain.LadoApprovalFor(approvals, pr.TargetBranch); ok {
		return s.approvedBaseHolds(ctx, pr, head, approval)
	}
	// A revoked approval does not count; one into this target says why.
	revoked := slices.ContainsFunc(approvals, func(a domain.LadoIntegrationApproval) bool {
		return a.Revoked() && (a.TargetBranch == "" || strings.EqualFold(a.TargetBranch, pr.TargetBranch))
	})
	approvals = slices.DeleteFunc(approvals, domain.LadoIntegrationApproval.Revoked)
	switch {
	case revoked:
		return integrationRevoked()
	case len(approvals) > 0:
		return targetChanged(pr, head, approvals[len(approvals)-1].TargetBranch)
	}
	return approvalRequired(pr, head)
}

// approvedBaseHolds checks that the approval still holds for the remote
// target as it is now: it fetches the target, and when its commit is not the
// one the approval was given against, computes the head's record again and
// revokes the approval if the record turned base_overlap.
func (s *Service) approvedBaseHolds(ctx context.Context, pr domain.PullRequest, head string, approval domain.LadoIntegrationApproval) error {
	exec, ok, err := s.deps.Store.LadoExecution(ctx, approval.ExecutionSessionID)
	switch {
	case err != nil:
		return s.mergeGateFailed(pr, head, err)
	case !ok:
		return s.mergeGateFailed(pr, head, fmt.Errorf("execution %s not found", approval.ExecutionSessionID))
	}
	// The record is of the PR and head the approval holds for, whatever PR
	// the execution's session has now.
	exec.PR.URL, exec.PR.HeadSHA, exec.PR.Merged, exec.PR.Closed = approval.PRURL, head, false, false
	exec.PR.TargetBranch = cmp.Or(approval.TargetBranch, pr.TargetBranch, exec.PR.TargetBranch)
	if pr.Number != 0 {
		exec.PR.Number = pr.Number
	}
	in, err := s.headInputs(ctx, exec)
	if err != nil {
		return s.mergeGateFailed(pr, head, err)
	}
	if err := s.fetchBranch(ctx, in.dir, in.baseRemote, in.target); err != nil {
		return s.mergeGateFailed(pr, head, fmt.Errorf("fetch the target branch: %s", redactedErrorLine(err)))
	}
	s.fetchDue(fetchKey(in.dir, in.baseRemote, in.target), true)
	tip, err := s.remoteTip(ctx, in)
	switch {
	case err != nil:
		return s.mergeGateFailed(pr, head, err)
	case strings.EqualFold(tip, approval.BaseTipSHA):
		return nil
	}
	record, err := s.verifyHead(ctx, exec, verifyOptions{})
	if err != nil {
		return s.mergeGateFailed(pr, head, err)
	}
	revoked, err := s.revokeOverlapped(ctx, exec, record)
	if err != nil {
		return s.mergeGateFailed(pr, head, err)
	}
	if baseOverlapped(record) || slices.ContainsFunc(revoked, func(a domain.LadoIntegrationApproval) bool { return a.ID == approval.ID }) {
		return integrationRevoked()
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
