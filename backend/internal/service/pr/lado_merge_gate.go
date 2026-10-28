package pr

// LADO fork: AO's merge asks LADO's merge gate before it reads the PR from
// its host, and again when the host's target branch differs from the stored
// one (docs/LJY-PRD/TDD.md §7.2), so the desktop's and the mobile app's Merge
// buttons merge a LADO execution's PR only at a head the developer approved,
// into the branch it was approved into.

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ladoMergeGate holds the gate the daemon installs once it built the service.
type ladoMergeGate struct {
	gate atomic.Pointer[ports.LadoMergeGate]
}

// SetMergeGate installs LADO's merge gate. The daemon calls it before serving
// requests; nil keeps upstream behavior.
func (s *ActionService) SetMergeGate(gate ports.LadoMergeGate) {
	if gate == nil {
		s.ladoMerge.gate.Store(nil)
		return
	}
	s.ladoMerge.gate.Store(&gate)
}

// authorize asks the gate, when one is installed, whether pr may merge at
// headSHA. Its refusal is returned as it is.
func (m *ladoMergeGate) authorize(ctx context.Context, pr domain.PullRequest, headSHA string) error {
	gate := m.gate.Load()
	if gate == nil {
		return nil
	}
	return (*gate).AuthorizeMerge(ctx, pr, headSHA)
}

// authorizeFresh asks the gate again once the PR was read from its host,
// when its target branch there differs from the stored one: an approval
// holds for the branch it was approved into, and the host's base can change
// before AO's poller sees it (review of #31).
func (m *ladoMergeGate) authorizeFresh(ctx context.Context, tracked domain.PullRequest, fresh ports.SCMPRObservation, headSHA string) error {
	target := strings.TrimSpace(fresh.TargetBranch)
	if target == "" || target == tracked.TargetBranch {
		return nil
	}
	tracked.TargetBranch = target
	return m.authorize(ctx, tracked, headSHA)
}

// LadoSCMRef is the reference AO's merge reads pr through, for LADO's read of
// a PR's head from its host. It reports false when pr's repository or number
// is unknown.
func LadoSCMRef(pr domain.PullRequest) (ports.SCMPRRef, bool) {
	repo, ok := scmRepoForPR(pr)
	if !ok || pr.Number <= 0 {
		return ports.SCMPRRef{}, false
	}
	return ports.SCMPRRef{Repo: repo, Number: pr.Number, URL: pr.URL}, true
}
