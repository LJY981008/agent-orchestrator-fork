package pr

// LADO fork: AO's merge asks LADO's merge gate before it reads the PR from
// its host (docs/LJY-PRD/TDD.md §7.2), so the desktop's and the mobile app's
// Merge buttons merge a LADO execution's PR only at a head the developer
// approved.

import (
	"context"
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
