package ladoverify

// LADO fork, #21: the missing pipeline waiver (docs/LJY-PRD/TDD.md §7.2).
// A GitLab MR whose head no CI pipeline runs for reads CI unknown, and AO's
// merge refuses it. When LADO approved the integration of that head, the
// developer has decided: AO's merge then goes on. The waiver is narrow. AO
// asks it only when ci_no_pipeline is the one blocker, the merge gate has
// already let the merge through, and it holds only for a GitLab PR of LADO
// with an approval of that head into the branch it merges into now that LADO
// has not revoked.

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.LadoCIWaiver = (*Service)(nil)

// WaivesMissingPipeline implements ports.LadoCIWaiver. A read that fails
// returns the merge gate's failure, and AO does not merge.
func (s *Service) WaivesMissingPipeline(ctx context.Context, pr domain.PullRequest, headSHA string) (bool, error) {
	if !strings.EqualFold(strings.TrimSpace(pr.Provider), "gitlab") {
		return false, nil
	}
	head := strings.ToLower(strings.TrimSpace(headSHA))
	if head == "" {
		return false, nil
	}
	isLado, approvals, err := s.ladoPRApprovals(ctx, pr, head)
	switch {
	case err != nil:
		return false, s.mergeGateFailed(pr, head, err)
	case !isLado:
		return false, nil
	}
	_, ok := domain.LadoApprovalFor(approvals, pr.TargetBranch)
	return ok, nil
}
