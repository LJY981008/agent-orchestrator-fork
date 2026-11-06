package ladoverify

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// #21: LADO waives a missing pipeline only for a GitLab PR of LADO whose
// integration the developer approved at that head into the branch it merges
// into now, and has not revoked.
func TestWaivesMissingPipeline(t *testing.T) {
	f := newVerifyFixture(t)
	exec := f.addExecution("1", verifyHead1)
	pr := domain.PullRequest{URL: exec.PR.URL, SessionID: exec.SessionID, Number: 1, HeadSHA: verifyHead1, TargetBranch: "main", Provider: "gitlab"}
	waives := func(t *testing.T, pr domain.PullRequest, head string, want bool) {
		t.Helper()
		got, err := f.svc.WaivesMissingPipeline(f.ctx, pr, head)
		if err != nil || got != want {
			t.Fatalf("WaivesMissingPipeline(%s, %s into %s) = %v, %v; want %v", pr.Provider, head, pr.TargetBranch, got, err, want)
		}
	}

	// No approval yet; one of another head does not count.
	waives(t, pr, verifyHead1, false)
	f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-head2", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead2, TargetBranch: "main"})
	waives(t, pr, verifyHead1, false)

	f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-head1", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1, TargetBranch: "main"})
	waives(t, pr, verifyHead1, true)
	waives(t, pr, strings.ToUpper(verifyHead1), true)
	gitlab := pr
	gitlab.Provider = "GitLab"
	waives(t, gitlab, verifyHead1, true)

	// Another head, another target branch, a GitHub PR and a PR LADO does
	// not know are not waived.
	waives(t, pr, verifyHead3, false)
	release := pr
	release.TargetBranch = "release"
	waives(t, release, verifyHead1, false)
	github := pr
	github.Provider = "github"
	waives(t, github, verifyHead1, false)
	github.Provider = ""
	waives(t, github, verifyHead1, false)
	other := pr
	other.URL, other.SessionID = "https://gitlab.example.com/acme/demo/-/merge_requests/9", "outside-1"
	waives(t, other, verifyHead1, false)

	// A revoked approval holds for none.
	exec2 := f.addExecution("2", verifyHead2)
	pr2 := domain.PullRequest{URL: exec2.PR.URL, SessionID: exec2.SessionID, Number: 2, HeadSHA: verifyHead2, TargetBranch: "main", Provider: "gitlab"}
	f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-revoked", ExecutionSessionID: exec2.SessionID, PRURL: exec2.PR.URL, HeadSHA: verifyHead2, TargetBranch: "main", RevokedAt: t0, RevokedReason: domain.LadoCodeBaseOverlap})
	waives(t, pr2, verifyHead2, false)

	// A read that fails refuses the merge with the gate's failure.
	for _, fail := range []string{"execution", "naming", "approval"} {
		svc := New(Deps{Store: failingGateStore{verifyStore: f.store, fail: fail}, Sessions: f.reads, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
		got, err := svc.WaivesMissingPipeline(context.Background(), pr, verifyHead1)
		if got {
			t.Errorf("%s: a failed read waives", fail)
		}
		requireAPIError(t, err, apierr.KindInternal, "LADO_MERGE_GATE_FAILED")
	}
}
