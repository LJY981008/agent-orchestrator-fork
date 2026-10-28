package gitlab

// LADO fork: GitLab has no branch rules or lado/integration status in LADO
// (docs/LJY-PRD/TDD.md §7.1). Protected branches with external status checks
// are a later step, so a GitLab project stays "unprotected: detection only"
// and these answer ports.ErrLadoUnsupported without a request.

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// BranchRules answers ports.ErrLadoUnsupported.
func (w *LadoIssues) BranchRules(context.Context, domain.TrackerRepo, string) (ports.LadoBranchRules, error) {
	return ports.LadoBranchRules{}, ports.ErrLadoUnsupported
}

// PostCommitStatus answers ports.ErrLadoUnsupported.
func (w *LadoIssues) PostCommitStatus(context.Context, domain.TrackerRepo, string, ports.LadoCommitStatus) error {
	return ports.ErrLadoUnsupported
}
