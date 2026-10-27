package ladoverify

// LADO fork: a pull request's head as its host has it now (decision m of the
// step 8 plan). AO keeps the head it last observed, which a push moves before
// AO observes the PR again, so an integration approval reads the head from
// the host, as AO's merge does before it merges.

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LiveHeads reads pull requests from their hosts: AO's SCM reader, which
// AO's merge reads too.
type LiveHeads interface {
	// LiveHead returns pr's head commit as its host has it now.
	LiveHead(ctx context.Context, pr domain.PullRequest) (string, error)
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
