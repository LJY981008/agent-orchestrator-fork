package ports

// LADO fork: the merge gate (docs/LJY-PRD/TDD.md §7.2 and
// docs/LJY-PRD/adr/0003-enforcement-points.md).

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LadoMergeGate authorizes AO's merge of a pull request. A nil gate keeps
// upstream behavior.
type LadoMergeGate interface {
	// AuthorizeMerge refuses to merge pr at headSHA unless LADO allows it:
	// a PR of a LADO execution, or of any session of a project in LADO
	// mode, merges only at a head whose integration the developer approved.
	// A refusal is an *apierr.Error, which the merge route renders as it is.
	AuthorizeMerge(ctx context.Context, pr domain.PullRequest, headSHA string) error
}
