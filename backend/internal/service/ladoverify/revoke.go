package ladoverify

// LADO fork (#53): an integration approval holds for the base its head was
// verified against. Once another PR's merge into that base changed the same
// paths as the head (base_overlap), LADO withdraws the approval: on GitHub a
// pending lado/integration status replaces the success it posted, so a
// ruleset that requires it stops the merge, and AO's merge gate refuses.
// The head has to take in the base; its new head is verified and approved
// on its own.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// revokedStatusDescription is what the pending status of a revoked approval
// says.
const revokedStatusDescription = "Base moved in LADO; verify a new head"

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
// fails is posted again by the loop. It returns the approvals it revoked.
func (s *Service) revokeOverlapped(ctx context.Context, exec domain.LadoExecution, record domain.LadoVerification) ([]domain.LadoIntegrationApproval, error) {
	if !baseOverlapped(record) {
		return nil, nil
	}
	approvals, err := s.deps.Store.ListLadoIntegrationApprovals(ctx, record.ExecutionSessionID, record.PRURL, record.HeadSHA)
	if err != nil {
		return nil, fmt.Errorf("read the integration approvals of head %s: %w", record.HeadSHA, err)
	}
	var revoked []domain.LadoIntegrationApproval
	for _, approval := range approvals {
		if approval.Revoked() {
			continue
		}
		now := s.now()
		payload, err := json.Marshal(map[string]any{
			"task":    exec.TaskID,
			"session": approval.ExecutionSessionID,
			"pr":      approval.PRURL,
			"head":    approval.HeadSHA,
			"target":  approval.TargetBranch,
			"reason":  domain.LadoCodeBaseOverlap,
		})
		if err != nil {
			return revoked, err
		}
		ok, err := s.deps.Store.RevokeLadoIntegrationApproval(ctx, approval.ID, domain.LadoCodeBaseOverlap, now, domain.LadoAuditEvent{
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
		s.deps.Log.Info("LADO revoked an integration approval: the base moved with a change to the same paths",
			"session", approval.ExecutionSessionID, "pr", approval.PRURL, "head", approval.HeadSHA, "target", approval.TargetBranch)
		approval.RevokedAt, approval.RevokedReason = now, domain.LadoCodeBaseOverlap
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
