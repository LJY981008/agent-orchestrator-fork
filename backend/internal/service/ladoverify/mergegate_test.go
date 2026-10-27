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

func (s failingGateStore) GetLadoIntegrationApproval(ctx context.Context, session domain.SessionID, head string) (domain.LadoIntegrationApproval, bool, error) {
	if s.fail == "approval" {
		return domain.LadoIntegrationApproval{}, false, errGateRead
	}
	return s.verifyStore.GetLadoIntegrationApproval(ctx, session, head)
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
