package pr

// LADO fork, #97 (docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md):
// before LADO applies a PR's pre-merge database SQL, it reads whether AO's
// merge could go through, so SQL never runs for a PR that cannot merge. The
// read is the one Merge makes, without the merge gate and the merger.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LadoMergeReadiness reads the PR at prURL from its host as Merge does and
// returns what would stop the merge (mergeBlockers), the host's
// mergeability and whether the PR merged already. It neither asks LADO's
// merge gate nor merges. number 0 takes the stored PR's number; another
// number than the stored one is ErrPRNotFound. A head that is not the open
// PR's head on the host is ErrPRHeadChanged; head "" reads whatever head
// the PR has.
func (s *ActionService) LadoMergeReadiness(ctx context.Context, prURL string, number int, head string) (domain.LadoMergeReadiness, error) {
	if strings.TrimSpace(prURL) == "" || number < 0 {
		return domain.LadoMergeReadiness{}, fmt.Errorf("%w: invalid pull request identity", ErrInvalidPR)
	}
	if s.store == nil || s.reader == nil {
		return domain.LadoMergeReadiness{}, errors.New("pr: merge readiness is not configured")
	}
	head = strings.ToLower(strings.TrimSpace(head))
	if head != "" && !gitSHAPattern.MatchString(head) {
		return domain.LadoMergeReadiness{}, fmt.Errorf("%w: invalid head", ErrInvalidPR)
	}
	tracked, ok, err := s.store.GetPR(ctx, prURL)
	if err != nil {
		return domain.LadoMergeReadiness{}, fmt.Errorf("load pull request: %w", err)
	}
	if !ok || (number != 0 && tracked.Number != number) || tracked.Number <= 0 {
		return domain.LadoMergeReadiness{}, ErrPRNotFound
	}
	repo, ok := scmRepoForPR(tracked)
	if !ok {
		return domain.LadoMergeReadiness{}, fmt.Errorf("%w: pull request repository is unknown", ErrPRPreconditions)
	}
	ref := ports.SCMPRRef{Repo: repo, Number: tracked.Number, URL: tracked.URL}
	fresh, review, err := s.fetchMergeReadiness(ctx, ref)
	if err != nil {
		return domain.LadoMergeReadiness{}, err
	}
	if head != "" && !fresh.PR.Merged && !strings.EqualFold(fresh.PR.HeadSHA, head) {
		return domain.LadoMergeReadiness{}, ErrPRHeadChanged
	}
	mergeability := domain.Mergeability(fresh.Mergeability.State)
	if fresh.Mergeability.Conflict && mergeability == "" {
		mergeability = domain.MergeConflicting
	}
	blockers := mergeBlockers(fresh, review)
	if blockers == nil {
		blockers = []string{}
	}
	return domain.LadoMergeReadiness{
		Blockers:     blockers,
		Mergeability: mergeability,
		Merged:       fresh.PR.Merged,
	}, nil
}
