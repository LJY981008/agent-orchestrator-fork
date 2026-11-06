package ladoverify

// LADO fork (#75): a PR the host merged a moment ago still looks open to
// LADO until AO observes it again. When its own squash or rebase merge moved
// the base, the loop could take the move for another PR's merge of the same
// paths (base_overlap), revoke the approval the merge just used and ask the
// worker to take in the base. Before it revokes an approval that still
// holds, or sends a base_overlap fix request, the loop reads the PR from its
// host again: a merged PR has no merge to stop and nothing for its worker to
// do. A read that fails leaves the loop as it was: the revocation stops the
// merge of an overlapped head, so it does not give way to a failed read.

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// mergedOnHost reports whether the host says the PR at prURL merged. It is
// false when LADO has no reader of the hosts, AO has no row of the PR or the
// host could not be read.
func (s *Service) mergedOnHost(ctx context.Context, prURL string) bool {
	reader := s.liveHeadReader()
	if reader == nil {
		s.deps.Log.Warn("LADO cannot read pull requests from their host, so it takes the PR as open", "pr", prURL)
		return false
	}
	pr, ok, err := s.deps.Store.GetPR(ctx, prURL)
	switch {
	case err != nil:
		s.deps.Log.Warn("LADO could not read a PR to ask its host whether it merged; it takes the PR as open", "pr", prURL, "err", redactedErrorLine(err))
		return false
	case !ok:
		s.deps.Log.Warn("LADO found no PR to ask its host whether it merged; it takes the PR as open", "pr", prURL)
		return false
	}
	live, err := reader.LivePR(ctx, pr)
	if err != nil {
		s.deps.Log.Warn("LADO could not read a PR from its host to see whether it merged; it takes the PR as open", "pr", prURL, "err", redactedErrorLine(err))
		return false
	}
	return live.Merged
}

// hostMerges keeps, for one pass, what the hosts said of the PRs the pass
// asked about, so that a fix request and a revocation of the same PR read
// it once. A nil *hostMerges does not ask the host: every PR counts as open,
// as for the merge gate, which merges an open PR now.
type hostMerges struct {
	s      *Service
	merged map[string]bool
}

// newHostMerges is an empty memo of one pass.
func (s *Service) newHostMerges() *hostMerges {
	return &hostMerges{s: s, merged: map[string]bool{}}
}

// mergedOnHost is Service.mergedOnHost, read at most once per PR in the
// pass; false on a nil memo.
func (m *hostMerges) mergedOnHost(ctx context.Context, prURL string) bool {
	if m == nil || prURL == "" {
		return false
	}
	if merged, ok := m.merged[prURL]; ok {
		return merged
	}
	merged := m.s.mergedOnHost(ctx, prURL)
	m.merged[prURL] = merged
	return merged
}

// holding reports whether any of approvals still holds, the only ones a
// revocation would withdraw.
func holding(approvals []domain.LadoIntegrationApproval) bool {
	for _, approval := range approvals {
		if !approval.Revoked() {
			return true
		}
	}
	return false
}
