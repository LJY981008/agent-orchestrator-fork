package ladoverify

// LADO fork: a pull request as its host has it now (decision m of the step 8
// plan). AO keeps the head, target branch and state it last observed, which a
// push, a retarget or a merge on the host change before AO observes the PR
// again, so an integration approval reads them from the host, as AO's merge
// does before it merges (review of #31).

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LivePR is a pull request as its host has it now.
type LivePR struct {
	HeadSHA string
	// TargetBranch is the branch it merges into; empty when the host did
	// not say.
	TargetBranch string
	// Merged and Closed tell a pull request that is no longer open.
	Merged, Closed bool
}

// LiveHeads reads pull requests from their hosts: AO's SCM reader, which
// AO's merge reads too.
type LiveHeads interface {
	// LivePR returns pr as its host has it now.
	LivePR(ctx context.Context, pr domain.PullRequest) (LivePR, error)
}

// SetLiveHeads gives the service its reader of PR heads, which the daemon
// builds after the service. Without one no integration can be approved.
func (s *Service) SetLiveHeads(heads LiveHeads) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.liveHeads = heads
}

func (s *Service) liveHeadReader() LiveHeads {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.liveHeads
}
