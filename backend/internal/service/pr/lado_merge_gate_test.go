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

// fakeLadoCIWaiver is a merge gate that allows and also waives a missing
// pipeline as told.
type fakeLadoCIWaiver struct {
	fakeLadoMergeGate
	waives    bool
	waiveErr  error
	waiveAsks []string
}

func (w *fakeLadoCIWaiver) WaivesMissingPipeline(_ context.Context, pr domain.PullRequest, headSHA string) (bool, error) {
	w.waiveAsks = append(w.waiveAsks, pr.URL+"@"+headSHA+"->"+pr.TargetBranch)
	return w.waives, w.waiveErr
}

// #21: a GitLab head no pipeline runs for reads CI unknown. AO merges it
// only when that is the one blocker and the installed gate waives the
// missing pipeline; otherwise the refusal names the blockers.
func TestMergeWaivesAMissingPipelineOnlyThroughTheGate(t *testing.T) {
	setup := func(mutate func(*fakeSCMAction)) (domain.PullRequest, *fakeSCMAction, *ActionService) {
		pr, scm := mergeableActionFixture()
		pr.URL, pr.Provider, pr.Host, pr.TargetBranch = "https://gitlab.example.com/acme/widgets/-/merge_requests/42", "gitlab", "gitlab.example.com", "main"
		scm.observation.PR.URL, scm.observation.PR.TargetBranch = pr.URL, "main"
		scm.observation.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: pr.HeadSHA, NoPipeline: true}
		if mutate != nil {
			mutate(scm)
		}
		return pr, scm, NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: scm, Merger: scm})
	}
	merge := func(svc *ActionService, pr domain.PullRequest) error {
		_, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA})
		return err
	}
	blockedBy := func(t *testing.T, err error, want ...string) {
		t.Helper()
		var blocked *MergeBlockedError
		if !errors.Is(err, ErrPRPreconditions) || !errors.As(err, &blocked) {
			t.Fatalf("merge = %v, want a MergeBlockedError", err)
		}
		if strings.Join(blocked.Blockers, ",") != strings.Join(want, ",") {
			t.Errorf("blockers = %v, want %v", blocked.Blockers, want)
		}
	}

	t.Run("a waiver that waives", func(t *testing.T) {
		pr, scm, svc := setup(func(scm *fakeSCMAction) { scm.observation.PR.TargetBranch = "release" })
		gate := &fakeLadoCIWaiver{waives: true}
		svc.SetMergeGate(gate)
		if err := merge(svc, pr); err != nil || scm.mergeCalls != 1 {
			t.Fatalf("merge = %v with %d merges, want one merge", err, scm.mergeCalls)
		}
		// The waiver is asked about the branch the PR merges into on the host.
		if want := pr.URL + "@" + pr.HeadSHA + "->release"; len(gate.waiveAsks) != 1 || gate.waiveAsks[0] != want {
			t.Errorf("waiver asked %v, want %s", gate.waiveAsks, want)
		}
	})

	t.Run("a waiver that does not waive", func(t *testing.T) {
		pr, scm, svc := setup(nil)
		gate := &fakeLadoCIWaiver{}
		svc.SetMergeGate(gate)
		blockedBy(t, merge(svc, pr), domain.MergeBlockerCINoPipeline)
		if scm.mergeCalls != 0 || len(gate.waiveAsks) != 1 {
			t.Errorf("%d merges after %d waiver questions, want none after one", scm.mergeCalls, len(gate.waiveAsks))
		}
	})

	t.Run("no gate", func(t *testing.T) {
		pr, scm, svc := setup(nil)
		blockedBy(t, merge(svc, pr), domain.MergeBlockerCINoPipeline)
		if scm.mergeCalls != 0 {
			t.Errorf("%d merges, want none", scm.mergeCalls)
		}
	})

	t.Run("a gate that is no waiver", func(t *testing.T) {
		pr, scm, svc := setup(nil)
		svc.SetMergeGate(&fakeLadoMergeGate{})
		blockedBy(t, merge(svc, pr), domain.MergeBlockerCINoPipeline)
		if scm.mergeCalls != 0 {
			t.Errorf("%d merges, want none", scm.mergeCalls)
		}
	})

	t.Run("a waiver that cannot read", func(t *testing.T) {
		pr, scm, svc := setup(nil)
		failed := apierr.Internal("LADO_MERGE_GATE_FAILED", "LADO could not check.")
		svc.SetMergeGate(&fakeLadoCIWaiver{waives: true, waiveErr: failed})
		if err := merge(svc, pr); !errors.Is(err, failed) || scm.mergeCalls != 0 {
			t.Fatalf("merge = %v with %d merges, want the waiver's error and none", err, scm.mergeCalls)
		}
	})

	t.Run("another blocker as well", func(t *testing.T) {
		pr, scm, svc := setup(func(scm *fakeSCMAction) {
			scm.observation.Mergeability = ports.SCMMergeabilityObservation{State: string(domain.MergeBlocked)}
		})
		gate := &fakeLadoCIWaiver{waives: true}
		svc.SetMergeGate(gate)
		blockedBy(t, merge(svc, pr), domain.MergeBlockerCINoPipeline, domain.MergeBlockerNotMergeable)
		if scm.mergeCalls != 0 || len(gate.waiveAsks) != 0 {
			t.Errorf("%d merges after %d waiver questions, want neither", scm.mergeCalls, len(gate.waiveAsks))
		}
	})

	t.Run("CI passing", func(t *testing.T) {
		pr, scm, svc := setup(func(scm *fakeSCMAction) {
			scm.observation.CI.Summary, scm.observation.CI.NoPipeline = string(domain.CIPassing), false
		})
		gate := &fakeLadoCIWaiver{}
		svc.SetMergeGate(gate)
		if err := merge(svc, pr); err != nil || scm.mergeCalls != 1 || len(gate.waiveAsks) != 0 {
			t.Fatalf("merge = %v with %d merges after %d waiver questions, want one merge and none", err, scm.mergeCalls, len(gate.waiveAsks))
		}
	})

	t.Run("CI unknown with a pipeline", func(t *testing.T) {
		pr, scm, svc := setup(func(scm *fakeSCMAction) { scm.observation.CI.NoPipeline = false })
		gate := &fakeLadoCIWaiver{waives: true}
		svc.SetMergeGate(gate)
		blockedBy(t, merge(svc, pr), domain.MergeBlockerCIUnknown)
		if scm.mergeCalls != 0 || len(gate.waiveAsks) != 0 {
			t.Errorf("%d merges after %d waiver questions, want neither", scm.mergeCalls, len(gate.waiveAsks))
		}
	})
}

// #21: the live read's blockers add a CI result of another head and a
// partial review listing to the shared readiness rule, and name a missing
// pipeline only while CI is unknown.
func TestMergeBlockers(t *testing.T) {
	_, ready := mergeableActionFixture()
	cases := []struct {
		name   string
		mutate func(*ports.SCMObservation, *ports.SCMReviewObservation)
		want   []string
	}{
		{"ready", func(*ports.SCMObservation, *ports.SCMReviewObservation) {}, nil},
		{"CI of another head", func(o *ports.SCMObservation, _ *ports.SCMReviewObservation) { o.CI.HeadSHA = strings.Repeat("b", 40) }, []string{domain.MergeBlockerHeadMismatch}},
		{"no head", func(o *ports.SCMObservation, _ *ports.SCMReviewObservation) { o.PR.HeadSHA, o.CI.HeadSHA = "", "" }, []string{domain.MergeBlockerHeadMismatch}},
		{"partial review", func(_ *ports.SCMObservation, r *ports.SCMReviewObservation) { r.Partial = true }, []string{domain.MergeBlockerReviewPartial}},
		{"no pipeline", func(o *ports.SCMObservation, _ *ports.SCMReviewObservation) { o.CI.Summary, o.CI.NoPipeline = "", true }, []string{domain.MergeBlockerCINoPipeline}},
		{"no pipeline flag with CI pending", func(o *ports.SCMObservation, _ *ports.SCMReviewObservation) {
			o.CI.Summary, o.CI.NoPipeline = string(domain.CIPending), true
		}, []string{domain.MergeBlockerCIPending}},
		{"changes requested and unresolved", func(_ *ports.SCMObservation, r *ports.SCMReviewObservation) {
			r.Decision = string(domain.ReviewChangesRequest)
			r.Threads = []ports.SCMReviewThreadObservation{{ID: "t1", Comments: []ports.SCMReviewCommentObservation{{ID: "c1"}}}}
		}, []string{domain.MergeBlockerChangesRequested, domain.MergeBlockerUnresolvedComments}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, review := ready.observation, ready.review
			c.mutate(&o, &review)
			if got := mergeBlockers(o, review); strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("mergeBlockers = %v, want %v", got, c.want)
			}
		})
	}
}
