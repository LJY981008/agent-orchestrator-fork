package pr

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// #97: LADO reads whether AO's merge could go through before it applies a
// PR's pre-merge SQL: the blockers Merge reads, the mergeability and
// whether the PR merged, without the gate or the merger.
func TestLadoMergeReadiness(t *testing.T) {
	setup := func(change func(*fakeSCMAction)) (domain.PullRequest, *countingSCMAction, *fakeLadoMergeGate, *ActionService) {
		pr, scm := mergeableActionFixture()
		if change != nil {
			change(scm)
		}
		reader := &countingSCMAction{fakeSCMAction: scm}
		svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: reader, Merger: scm})
		gate := &fakeLadoMergeGate{err: errors.New("the gate is not asked")}
		svc.SetMergeGate(gate)
		return pr, reader, gate, svc
	}
	ctx := context.Background()

	t.Run("mergeable", func(t *testing.T) {
		pr, reader, gate, svc := setup(nil)
		got, err := svc.LadoMergeReadiness(ctx, pr.URL, pr.Number, strings.ToUpper(pr.HeadSHA))
		if err != nil {
			t.Fatalf("readiness = %v", err)
		}
		if len(got.Blockers) != 0 || got.Mergeability != domain.MergeMergeable || got.Merged {
			t.Errorf("readiness = %+v, want mergeable without blockers", got)
		}
		if reader.fetches != 1 || reader.mergeCalls != 0 || len(gate.asked) != 0 {
			t.Errorf("%d reads, %d merges, gate asked %v; want one read only", reader.fetches, reader.mergeCalls, gate.asked)
		}
		if got := domain.LadoDBChangeMergeStoppers(got); len(got) != 0 {
			t.Errorf("stoppers = %v, want none", got)
		}
	})

	t.Run("number 0 takes the stored one, head empty any", func(t *testing.T) {
		pr, _, _, svc := setup(nil)
		if _, err := svc.LadoMergeReadiness(ctx, pr.URL, 0, ""); err != nil {
			t.Errorf("readiness = %v", err)
		}
	})

	t.Run("unresolved comments and a conflict", func(t *testing.T) {
		pr, _, _, svc := setup(func(scm *fakeSCMAction) {
			scm.observation.Mergeability = ports.SCMMergeabilityObservation{State: string(domain.MergeConflicting), Conflict: true}
			scm.review = ports.SCMReviewObservation{Threads: []ports.SCMReviewThreadObservation{{
				ID: "thread-1", Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Body: "fix this"}},
			}}}
		})
		got, err := svc.LadoMergeReadiness(ctx, pr.URL, pr.Number, pr.HeadSHA)
		if err != nil {
			t.Fatalf("readiness = %v", err)
		}
		if got.Mergeability != domain.MergeConflicting || !slices.Contains(got.Blockers, domain.MergeBlockerUnresolvedComments) ||
			!slices.Contains(got.Blockers, domain.MergeBlockerNotMergeable) {
			t.Errorf("readiness = %+v, want conflicting with unresolved comments", got)
		}
		stoppers := domain.LadoDBChangeMergeStoppers(got)
		if !slices.Contains(stoppers, domain.MergeBlockerUnresolvedComments) || !slices.Contains(stoppers, domain.MergeBlockerNotMergeable) {
			t.Errorf("stoppers = %v, want both", stoppers)
		}
	})

	t.Run("blocked only by the missing status", func(t *testing.T) {
		pr, _, _, svc := setup(func(scm *fakeSCMAction) {
			scm.observation.Mergeability = ports.SCMMergeabilityObservation{State: string(domain.MergeBlocked)}
			scm.observation.CI.Summary = string(domain.CIPending)
		})
		got, err := svc.LadoMergeReadiness(ctx, pr.URL, pr.Number, pr.HeadSHA)
		if err != nil {
			t.Fatalf("readiness = %v", err)
		}
		if len(got.Blockers) == 0 || got.Mergeability != domain.MergeBlocked {
			t.Errorf("readiness = %+v, want blocked", got)
		}
		if stoppers := domain.LadoDBChangeMergeStoppers(got); len(stoppers) != 0 {
			t.Errorf("stoppers = %v, want none: lado/integration is what blocks", stoppers)
		}
	})

	t.Run("merged", func(t *testing.T) {
		pr, _, _, svc := setup(func(scm *fakeSCMAction) { scm.observation.PR.Merged = true })
		got, err := svc.LadoMergeReadiness(ctx, pr.URL, pr.Number, pr.HeadSHA)
		if err != nil || !got.Merged || !slices.Contains(got.Blockers, domain.MergeBlockerMerged) {
			t.Errorf("readiness = %+v, %v; want merged", got, err)
		}
	})

	t.Run("another head", func(t *testing.T) {
		pr, _, _, svc := setup(func(scm *fakeSCMAction) {
			scm.observation.PR.HeadSHA = strings.Repeat("b", 40)
			scm.observation.CI.HeadSHA = strings.Repeat("b", 40)
		})
		if _, err := svc.LadoMergeReadiness(ctx, pr.URL, pr.Number, pr.HeadSHA); !errors.Is(err, ErrPRHeadChanged) {
			t.Errorf("readiness of an old head = %v, want ErrPRHeadChanged", err)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		pr, _, _, svc := setup(nil)
		if _, err := svc.LadoMergeReadiness(ctx, pr.URL, pr.Number+1, pr.HeadSHA); !errors.Is(err, ErrPRNotFound) {
			t.Errorf("another number = %v, want ErrPRNotFound", err)
		}
		if _, err := svc.LadoMergeReadiness(ctx, "", pr.Number, pr.HeadSHA); !errors.Is(err, ErrInvalidPR) {
			t.Errorf("no URL = %v, want ErrInvalidPR", err)
		}
		if _, err := svc.LadoMergeReadiness(ctx, pr.URL, pr.Number, "main"); !errors.Is(err, ErrInvalidPR) {
			t.Errorf("a head that is no SHA = %v, want ErrInvalidPR", err)
		}
		missing := NewActionService(ActionDeps{Store: &fakeActionStore{}, Reader: &fakeSCMAction{}})
		if _, err := missing.LadoMergeReadiness(ctx, pr.URL, pr.Number, pr.HeadSHA); !errors.Is(err, ErrPRNotFound) {
			t.Errorf("an unknown PR = %v, want ErrPRNotFound", err)
		}
	})
}
