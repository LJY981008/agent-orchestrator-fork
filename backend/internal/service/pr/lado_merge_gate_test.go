package pr

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeLadoMergeGate records what it is asked and answers err.
type fakeLadoMergeGate struct {
	err   error
	asked []string
	// targets are the target branches it was asked about; it answers
	// targetErr for refuseTarget.
	targets      []string
	refuseTarget string
	targetErr    error
}

func (g *fakeLadoMergeGate) AuthorizeMerge(_ context.Context, pr domain.PullRequest, headSHA string) error {
	g.asked = append(g.asked, pr.URL+"@"+headSHA)
	g.targets = append(g.targets, pr.TargetBranch)
	if g.refuseTarget != "" && pr.TargetBranch == g.refuseTarget {
		return g.targetErr
	}
	return g.err
}

// countingSCMAction counts the reads of the live PR.
type countingSCMAction struct {
	*fakeSCMAction
	fetches int
}

func (c *countingSCMAction) FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	c.fetches++
	return c.fakeSCMAction.FetchPullRequests(ctx, refs)
}

// PRD gate 7: AO's merge asks LADO's merge gate after it checked the stored
// head and before it reads the PR from its host; a refusal stops it there.
func TestMergeAsksTheGate(t *testing.T) {
	setup := func() (domain.PullRequest, *countingSCMAction, *ActionService) {
		pr, scm := mergeableActionFixture()
		reader := &countingSCMAction{fakeSCMAction: scm}
		return pr, reader, NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: reader, Merger: scm})
	}
	merge := func(svc *ActionService, pr domain.PullRequest, head string) error {
		_, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: head})
		return err
	}

	t.Run("no gate", func(t *testing.T) {
		pr, reader, svc := setup()
		if err := merge(svc, pr, pr.HeadSHA); err != nil || reader.fetches != 1 || reader.mergeCalls != 1 {
			t.Fatalf("merge without a gate = %v after %d reads and %d merges, want one of each", err, reader.fetches, reader.mergeCalls)
		}
		svc.SetMergeGate(&fakeLadoMergeGate{err: errors.New("refused")})
		svc.SetMergeGate(nil)
		if err := merge(svc, pr, pr.HeadSHA); err != nil || reader.mergeCalls != 2 {
			t.Fatalf("merge after the gate was removed = %v, %d merges; want a second merge", err, reader.mergeCalls)
		}
	})

	t.Run("a gate that allows", func(t *testing.T) {
		pr, reader, svc := setup()
		gate := &fakeLadoMergeGate{}
		svc.SetMergeGate(gate)
		if err := merge(svc, pr, strings.ToUpper(pr.HeadSHA)); err != nil || reader.mergeCalls != 1 {
			t.Fatalf("merge = %v, %d merges; want one", err, reader.mergeCalls)
		}
		if want := pr.URL + "@" + pr.HeadSHA; len(gate.asked) != 1 || gate.asked[0] != want {
			t.Errorf("gate asked %v, want %s", gate.asked, want)
		}
	})

	t.Run("a gate that refuses", func(t *testing.T) {
		pr, reader, svc := setup()
		refusal := apierr.Conflict("LADO_INTEGRATION_APPROVAL_REQUIRED", "Approve the integration first.", map[string]any{"prUrl": pr.URL})
		gate := &fakeLadoMergeGate{err: refusal}
		svc.SetMergeGate(gate)
		if err := merge(svc, pr, pr.HeadSHA); !errors.Is(err, refusal) {
			t.Fatalf("merge = %v, want the gate's refusal", err)
		}
		if reader.fetches != 0 || reader.mergeCalls != 0 {
			t.Errorf("a refused merge read the PR %d times and merged %d times, want neither", reader.fetches, reader.mergeCalls)
		}
		// A stale head is refused before the gate is asked.
		if err := merge(svc, pr, strings.Repeat("b", 40)); !errors.Is(err, ErrPRHeadChanged) || len(gate.asked) != 1 {
			t.Errorf("stale head merge = %v after %d gate questions, want ErrPRHeadChanged without asking", err, len(gate.asked))
		}
	})
	// Review of #31: the host's base can change before AO's poller sees it.
	// The gate is asked again with the branch the PR merges into on the host,
	// and its refusal stops the merge before the SCM merger.
	t.Run("a target branch changed on the host", func(t *testing.T) {
		pr, reader, _ := setup()
		pr.TargetBranch = "main"
		svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: reader, Merger: reader.fakeSCMAction})
		reader.observation.PR.TargetBranch = "release"
		refusal := apierr.Conflict("LADO_INTEGRATION_TARGET_CHANGED", "Review it again.", map[string]any{"currentTarget": "release"})
		gate := &fakeLadoMergeGate{refuseTarget: "release", targetErr: refusal}
		svc.SetMergeGate(gate)
		if err := merge(svc, pr, pr.HeadSHA); !errors.Is(err, refusal) {
			t.Fatalf("merge = %v, want the gate's refusal for release", err)
		}
		if want := []string{"main", "release"}; strings.Join(gate.targets, ",") != strings.Join(want, ",") || reader.mergeCalls != 0 {
			t.Errorf("gate asked about %v and %d merges ran, want %v and none", gate.targets, reader.mergeCalls, want)
		}

		// The same branch on the host asks once.
		reader.observation.PR.TargetBranch = "main"
		gate.targets = nil
		if err := merge(svc, pr, pr.HeadSHA); err != nil || len(gate.targets) != 1 || reader.mergeCalls != 1 {
			t.Errorf("merge into the stored branch = %v after asking about %v with %d merges, want one question and one merge", err, gate.targets, reader.mergeCalls)
		}
	})
}
