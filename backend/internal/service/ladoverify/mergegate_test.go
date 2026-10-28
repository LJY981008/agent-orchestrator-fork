package ladoverify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// PRD gate 7: AO's merge of a LADO project's PR needs the developer's
// integration approval of the head it merges.
func TestMergeGateNeedsAnApprovalForTheHead(t *testing.T) {
	f := newVerifyFixture(t)
	exec := f.addExecution("1", verifyHead1)
	pr := domain.PullRequest{URL: exec.PR.URL, SessionID: exec.SessionID, Number: 1, HeadSHA: verifyHead1}
	refused := func(t *testing.T, err error, pr domain.PullRequest, head string) {
		t.Helper()
		requireDetails(t, err, apierr.KindConflict, "LADO_INTEGRATION_APPROVAL_REQUIRED",
			map[string]any{"prUrl": pr.URL, "headSha": head, "sessionId": string(pr.SessionID)})
	}

	t.Run("a LADO execution's PR", func(t *testing.T) {
		refused(t, f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1), pr, verifyHead1)
		f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-head2", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead2})
		refused(t, f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1), pr, verifyHead1)

		f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-head1", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1})
		if err := f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1); err != nil {
			t.Fatalf("AuthorizeMerge of the approved head = %v", err)
		}
		if err := f.svc.AuthorizeMerge(f.ctx, pr, strings.ToUpper(verifyHead1)); err != nil {
			t.Errorf("AuthorizeMerge of the approved head in capitals = %v", err)
		}
		// The approval is of one PR: another PR of the session at the same
		// head does not merge on it.
		other := pr
		other.URL = "https://github.com/acme/demo/pull/7"
		refused(t, f.svc.AuthorizeMerge(f.ctx, other, verifyHead1), other, verifyHead1)
	})

	// S5 of #15: an approval holds for the branch the PR merged into when
	// the head was approved. A PR whose base was changed on the host since
	// is refused until its head is approved into the new branch.
	t.Run("another target branch", func(t *testing.T) {
		exec := f.addExecution("4", verifyHead4)
		f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-main", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead4, TargetBranch: "main"})
		onMain := domain.PullRequest{URL: exec.PR.URL, SessionID: exec.SessionID, Number: 4, HeadSHA: verifyHead4, TargetBranch: "Main"}
		if err := f.svc.AuthorizeMerge(f.ctx, onMain, verifyHead4); err != nil {
			t.Fatalf("AuthorizeMerge into the approved branch = %v", err)
		}
		onRelease := onMain
		onRelease.TargetBranch = "release"
		requireDetails(t, f.svc.AuthorizeMerge(f.ctx, onRelease, verifyHead4), apierr.KindConflict, "LADO_INTEGRATION_TARGET_CHANGED",
			map[string]any{"prUrl": exec.PR.URL, "headSha": verifyHead4, "sessionId": string(exec.SessionID), "approvedTarget": "main", "currentTarget": "release"})
		f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-release", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead4, TargetBranch: "release"})
		if err := f.svc.AuthorizeMerge(f.ctx, onRelease, verifyHead4); err != nil {
			t.Fatalf("AuthorizeMerge once approved into the new branch = %v", err)
		}
	})

	// S7 of #15: a PR claimed into another session stays LADO's while any
	// LADO record names it, so it merges only with an approval.
	t.Run("a PR claimed into another session", func(t *testing.T) {
		exec := f.addExecution("5", verifyHead2)
		f.tick()
		f.record(exec.SessionID, verifyHead2)
		f.store.setLadoMode("p1", false)
		claimed := domain.PullRequest{URL: exec.PR.URL, SessionID: "outside-1", Number: 5, HeadSHA: verifyHead2, TargetBranch: "main"}
		refused(t, f.svc.AuthorizeMerge(f.ctx, claimed, verifyHead2), claimed, verifyHead2)
		f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-claimed", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead2, TargetBranch: "main"})
		if err := f.svc.AuthorizeMerge(f.ctx, claimed, verifyHead2); err != nil {
			t.Fatalf("AuthorizeMerge of the claimed PR's approved head = %v", err)
		}
		// Another head of it still needs its own approval.
		refused(t, f.svc.AuthorizeMerge(f.ctx, claimed, verifyHead3), claimed, verifyHead3)
	})

	t.Run("another session of a project in LADO mode", func(t *testing.T) {
		f.reads.addSession(domain.SessionRecord{ID: "orchestrator-1", ProjectID: "p1"})
		other := domain.PullRequest{URL: "https://github.com/acme/demo/pull/8", SessionID: "orchestrator-1", Number: 8, HeadSHA: verifyHead3}
		f.store.setLadoMode("p1", true)
		refused(t, f.svc.AuthorizeMerge(f.ctx, other, verifyHead3), other, verifyHead3)

		f.store.setLadoMode("p1", false)
		if err := f.svc.AuthorizeMerge(f.ctx, other, verifyHead3); err != nil {
			t.Errorf("AuthorizeMerge in a project without LADO mode = %v", err)
		}
		gone := domain.PullRequest{URL: "https://github.com/acme/demo/pull/9", SessionID: "gone", Number: 9, HeadSHA: verifyHead3}
		if err := f.svc.AuthorizeMerge(f.ctx, gone, verifyHead3); err != nil {
			t.Errorf("AuthorizeMerge of a session AO has no record of = %v", err)
		}
	})

	t.Run("reads that fail", func(t *testing.T) {
		f.store.setLadoMode("p1", true)
		other := domain.PullRequest{URL: "https://github.com/acme/demo/pull/8", SessionID: "orchestrator-1", Number: 8, HeadSHA: verifyHead3}
		cases := []struct {
			fail string
			pr   domain.PullRequest
		}{
			{"execution", pr},
			{"naming", pr},
			{"approval", pr},
			{"session", other},
			{"settings", other},
		}
		for _, c := range cases {
			var sessions Sessions = f.reads
			if c.fail == "session" {
				sessions = failingSessions{}
			}
			svc := New(Deps{Store: failingGateStore{verifyStore: f.store, fail: c.fail}, Sessions: sessions, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
			err := svc.AuthorizeMerge(context.Background(), c.pr, c.pr.HeadSHA)
			if e := requireAPIError(t, err, apierr.KindInternal, "LADO_MERGE_GATE_FAILED"); strings.Contains(e.Message, "database") {
				t.Errorf("%s: the refusal tells the read error: %q", c.fail, e.Message)
			}
		}
	})
}

// failingGateStore fails the merge gate's read its fail names.
type failingGateStore struct {
	*verifyStore
	fail string
}

var errGateRead = errors.New("database is locked")

func (s failingGateStore) LadoExecution(ctx context.Context, id domain.SessionID) (domain.LadoExecution, bool, error) {
	if s.fail == "execution" {
		return domain.LadoExecution{}, false, errGateRead
	}
	return s.verifyStore.LadoExecution(ctx, id)
}

func (s failingGateStore) ListLadoIntegrationApprovalsForPR(ctx context.Context, prURL, head string) ([]domain.LadoIntegrationApproval, error) {
	if s.fail == "approval" {
		return nil, errGateRead
	}
	return s.verifyStore.ListLadoIntegrationApprovalsForPR(ctx, prURL, head)
}

func (s failingGateStore) LadoSessionsNamingPR(ctx context.Context, prURL string) ([]domain.SessionID, error) {
	if s.fail == "naming" {
		return nil, errGateRead
	}
	return s.verifyStore.LadoSessionsNamingPR(ctx, prURL)
}

func (s failingGateStore) LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error) {
	if s.fail == "settings" {
		return domain.LadoProjectSettings{}, errGateRead
	}
	return s.verifyStore.LadoProjectSettings(ctx, projectID)
}

// failingSessions cannot read any session.
type failingSessions struct{}

func (failingSessions) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{}, false, errGateRead
}
