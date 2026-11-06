package ladoverify

// LADO fork (#53): an integration approval holds for the base its head was
// verified against. Once another PR's merge into that base changed the same
// paths as the head (base_overlap), LADO withdraws the approval: on GitHub a
// pending lado/integration status replaces the success it posted, so a
// ruleset that requires it stops the merge, and AO's merge gate refuses.
// The head has to take in the base; its new head is verified and approved
// on its own.
//
// #64: a task's PRs are approved together, so once one loses its approval
// for its base, the approvals of the execution's other PRs that have not
// merged are withdrawn too (domain.LadoIntegrationSiblingRevoked). Those
// heads can be approved again as they are, with the new head of the PR
// whose base moved.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// revokedStatusDescription is what the pending status of a revoked approval
// says, and siblingRevokedStatusDescription that of an approval withdrawn
// for a sibling PR's base.
const (
	revokedStatusDescription        = "Base moved in LADO; verify a new head"
	siblingRevokedStatusDescription = "Another PR of the task lost its approval in LADO; approve again"
)

// baseOverlapped reports whether the record's base_branch item is
// base_overlap.
func baseOverlapped(record domain.LadoVerification) bool {
	for _, item := range record.Items {
		if item.Kind == domain.LadoVerifyBaseBranch {
			return item.Code == domain.LadoCodeBaseOverlap
		}
	}
	return false
}

// revokeOverlapped revokes the integration approvals of the record's head
// that still hold when the record is base_overlap, each with its audit
// event, and posts the pending status of each on GitHub once; a post that
// fails is posted again by the loop. Once the head has an approval revoked
// for its base, now or before, the approvals of the current heads of the
// execution's other tracked PRs that have not merged are revoked as
// sibling_revoked the same way (#64). It returns the approvals it revoked.
func (s *Service) revokeOverlapped(ctx context.Context, exec domain.LadoExecution, record domain.LadoVerification) ([]domain.LadoIntegrationApproval, error) {
	if !baseOverlapped(record) {
		return nil, nil
	}
	approvals, err := s.deps.Store.ListLadoIntegrationApprovals(ctx, record.ExecutionSessionID, record.PRURL, record.HeadSHA)
	if err != nil {
		return nil, fmt.Errorf("read the integration approvals of head %s: %w", record.HeadSHA, err)
	}
	revoked, err := s.revokeApprovals(ctx, exec, approvals, domain.LadoCodeBaseOverlap, "")
	if err != nil {
		return revoked, err
	}
	overlapped := slices.ContainsFunc(approvals, func(a domain.LadoIntegrationApproval) bool {
		return a.RevokedReason == domain.LadoCodeBaseOverlap
	})
	if len(revoked) == 0 && !overlapped {
		return revoked, nil
	}
	for _, sibling := range exec.TrackedPRs() {
		if sibling.PR.URL == record.PRURL || sibling.PR.Merged || sibling.PR.HeadSHA == "" {
			continue
		}
		approvals, err := s.deps.Store.ListLadoIntegrationApprovals(ctx, record.ExecutionSessionID, sibling.PR.URL, sibling.PR.HeadSHA)
		if err != nil {
			return revoked, fmt.Errorf("read the integration approvals of head %s: %w", sibling.PR.HeadSHA, err)
		}
		more, err := s.revokeApprovals(ctx, exec, approvals, domain.LadoIntegrationSiblingRevoked, record.PRURL)
		revoked = append(revoked, more...)
		if err != nil {
			return revoked, err
		}
	}
	return revoked, nil
}

// revokeApprovals revokes those of approvals that still hold for reason,
// each with its audit event (cause names the PR whose base moved, for a
// sibling's), and posts the pending status of each on GitHub once. It
// returns the approvals it revoked.
func (s *Service) revokeApprovals(ctx context.Context, exec domain.LadoExecution, approvals []domain.LadoIntegrationApproval, reason, cause string) ([]domain.LadoIntegrationApproval, error) {
	var revoked []domain.LadoIntegrationApproval
	for _, approval := range approvals {
		if approval.Revoked() {
			continue
		}
		now := s.now()
		fields := map[string]any{
			"task":    exec.TaskID,
			"session": approval.ExecutionSessionID,
			"pr":      approval.PRURL,
			"head":    approval.HeadSHA,
			"target":  approval.TargetBranch,
			"reason":  reason,
		}
		if cause != "" {
			fields["cause"] = cause
		}
		payload, err := json.Marshal(fields)
		if err != nil {
			return revoked, err
		}
		ok, err := s.deps.Store.RevokeLadoIntegrationApproval(ctx, approval.ID, reason, now, domain.LadoAuditEvent{
			At:          now,
			Actor:       "lado",
			Action:      domain.LadoAuditIntegrationRevoked,
			SubjectKind: "execution",
			SubjectID:   string(approval.ExecutionSessionID),
			Payload:     payload,
		})
		if err != nil {
			return revoked, fmt.Errorf("revoke integration approval %s: %w", approval.ID, err)
		}
		if !ok {
			// Revoked in the meantime; that revocation stands.
			continue
		}
		if cause == "" {
			s.deps.Log.Info("LADO revoked an integration approval: the base moved with a change to the same paths",
				"session", approval.ExecutionSessionID, "pr", approval.PRURL, "head", approval.HeadSHA, "target", approval.TargetBranch)
		} else {
			s.deps.Log.Info("LADO revoked an integration approval: another pull request of the task lost its approval for its base",
				"session", approval.ExecutionSessionID, "pr", approval.PRURL, "head", approval.HeadSHA, "target", approval.TargetBranch, "cause", cause)
		}
		approval.RevokedAt, approval.RevokedReason = now, reason
		if approval.StatusState != domain.LadoIntegrationStatusNotApplicable {
			approval.StatusState, approval.StatusAttempts, approval.StatusError = domain.LadoIntegrationStatusPending, 0, ""
			if posted, err := s.postIntegrationStatus(ctx, approval); err != nil {
				s.deps.Log.Warn("LADO revoked an integration approval but could not post its pending status; the loop posts it again",
					"approval", approval.ID, "err", err)
			} else {
				approval = posted
			}
		}
		revoked = append(revoked, approval)
	}
	return revoked, nil
}

// integrationRevoked refuses an approval of a head whose approval into the
// same target LADO revoked: the head has to take in the base first.
func integrationRevoked() error {
	return apierr.Conflict("LADO_INTEGRATION_REVOKED",
		"LADO revoked this head's integration approval: its base branch took a merge that changed the same paths. "+
			"Take the base into the branch, then approve the new head.", nil)
}
