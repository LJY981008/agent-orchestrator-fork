package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// reviewRunStore adds the optional ReviewRunLister to the reducer fake.
type reviewRunStore struct {
	*fakeStore
	runs      []domain.ReviewRun
	runsErr   error
	runsCalls int
}

func (s *reviewRunStore) ListReviewRunsBySession(_ context.Context, _ domain.SessionID) ([]domain.ReviewRun, error) {
	s.runsCalls++
	if s.runsErr != nil {
		return nil, s.runsErr
	}
	return append([]domain.ReviewRun(nil), s.runs...), nil
}

func newReviewRunManager(runs ...domain.ReviewRun) (*Manager, *reviewRunStore, *fakeMessenger) {
	st := &reviewRunStore{fakeStore: newFakeStore(), runs: runs}
	msg := &fakeMessenger{}
	st.sessions["mer-1"] = working("mer-1")
	return New(st, msg), st, msg
}

func completeRun(prURL, githubReviewID string) domain.ReviewRun {
	return domain.ReviewRun{SessionID: "mer-1", PRURL: prURL, Status: domain.ReviewRunComplete, GithubReviewID: githubReviewID}
}

func reviewComment(id, reviewID, author, body string) domain.PullRequestComment {
	return domain.PullRequestComment{
		ID: id, ThreadID: "T1", ReviewID: reviewID, Author: author,
		File: "foo.go", Line: 12, Body: body, AutoInjectReview: true,
	}
}

func applyPR1(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1"}); err != nil {
		t.Fatal(err)
	}
}

// assertNudged checks that wantMsgs nudges were sent, that every wanted body is
// in them and that none of the unwanted bodies are. The review comments of one
// observation share one nudge (#89), so the count is of messages, not bodies.
func assertNudged(t *testing.T, msgs []string, wantMsgs int, want []string, notWant []string) {
	t.Helper()
	if len(msgs) != wantMsgs {
		t.Fatalf("got %d nudges, want %d: %q", len(msgs), wantMsgs, msgs)
	}
	all := strings.Join(msgs, "\n---\n")
	for _, body := range want {
		if !strings.Contains(all, body) {
			t.Fatalf("nudges missing %q:\n%s", body, all)
		}
	}
	for _, body := range notWant {
		if strings.Contains(all, body) {
			t.Fatalf("nudges include %q:\n%s", body, all)
		}
	}
}

func TestReviewReplyFilter_WorkerReplyNotInjected(t *testing.T) {
	m, st, msg := newReviewRunManager(completeRun("pr1", "R1"))
	st.comments["pr1"] = []domain.PullRequestComment{
		reviewComment("c1", "R1", "W", "review finding"),
		reviewComment("c2", "R2", "W", "fixed in abc123"),
	}
	applyPR1(t, m)
	assertNudged(t, msg.msgs, 1, []string{"review finding"}, []string{"fixed in abc123"})
}

func TestReviewReplyFilter_NewRunCommentInSameThreadInjected(t *testing.T) {
	m, st, msg := newReviewRunManager(completeRun("pr1", "R1"), completeRun("pr1", "R3"))
	st.comments["pr1"] = []domain.PullRequestComment{
		reviewComment("c1", "R1", "W", "review finding"),
		reviewComment("c2", "R2", "W", "fixed in abc123"),
		reviewComment("c3", "R3", "W", "still broken here"),
	}
	applyPR1(t, m)
	assertNudged(t, msg.msgs, 1, []string{"review finding", "still broken here"}, []string{"fixed in abc123"})
}

func TestReviewReplyFilter_OtherAccountsAndBotsInjected(t *testing.T) {
	m, st, msg := newReviewRunManager(completeRun("pr1", "R1"))
	bot := reviewComment("b1", "B1", "review-bot[bot]", "bot finding")
	bot.IsBot = true
	st.comments["pr1"] = []domain.PullRequestComment{
		reviewComment("c1", "R1", "W", "review finding"),
		reviewComment("a1", "A1", "alice", "human finding"),
		bot,
	}
	applyPR1(t, m)
	assertNudged(t, msg.msgs, 1, []string{"review finding", "human finding", "bot finding"}, nil)
}

func TestReviewReplyFilter_NoRunsInjectsEverything(t *testing.T) {
	m, st, msg := newReviewRunManager()
	st.comments["pr1"] = []domain.PullRequestComment{
		reviewComment("c2", "R2", "W", "fixed in abc123"),
	}
	applyPR1(t, m)
	assertNudged(t, msg.msgs, 1, []string{"fixed in abc123"}, nil)
}

func TestReviewReplyFilter_EmptyReviewIDInjected(t *testing.T) {
	m, st, msg := newReviewRunManager(completeRun("pr1", "R1"))
	st.comments["pr1"] = []domain.PullRequestComment{
		reviewComment("c1", "R1", "W", "review finding"),
		reviewComment("n1", "", "W", "note without review"),
	}
	applyPR1(t, m)
	assertNudged(t, msg.msgs, 1, []string{"review finding", "note without review"}, nil)
}

func TestReviewReplyFilter_AuthorNormalized(t *testing.T) {
	m, st, msg := newReviewRunManager(completeRun("pr1", "R1"))
	st.comments["pr1"] = []domain.PullRequestComment{
		reviewComment("c1", "R1", "W", "review finding"),
		reviewComment("c2", "R2", " @w ", "fixed in abc123"),
	}
	applyPR1(t, m)
	assertNudged(t, msg.msgs, 1, []string{"review finding"}, []string{"fixed in abc123"})
}

func TestReviewReplyFilter_ClaimedPROriginalAuthorInjected(t *testing.T) {
	m, st, msg := newReviewRunManager(completeRun("pr1", "R1"))
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Author: "alice"}}
	st.comments["pr1"] = []domain.PullRequestComment{
		reviewComment("c1", "R1", "W", "review finding"),
		reviewComment("a1", "A1", "alice", "author note"),
		reviewComment("c2", "R2", "W", "fixed in abc123"),
	}
	applyPR1(t, m)
	assertNudged(t, msg.msgs, 1, []string{"review finding", "author note"}, []string{"fixed in abc123"})
}

func TestReviewReplyFilter_RunListErrorSkipsReviewCommentsOnly(t *testing.T) {
	m, st, msg := newReviewRunManager(completeRun("pr1", "R1"))
	st.runsErr = errors.New("db down")
	st.comments["pr1"] = []domain.PullRequestComment{
		reviewComment("c1", "R1", "W", "review finding"),
	}
	o := ports.PRObservation{Fetched: true, URL: "pr1", CI: domain.CIFailing, Checks: []ports.PRCheckObservation{
		{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"},
	}}
	err := m.ApplyPRObservation(ctx, "mer-1", o)
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("want run list error, got %v", err)
	}
	assertNudged(t, msg.msgs, 1, []string{"CI is failing"}, []string{"review finding"})
}

func TestReviewReplyFilter_StoreWithoutListerInjectsEverything(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.comments["pr1"] = []domain.PullRequestComment{
		reviewComment("c1", "R1", "W", "review finding"),
		reviewComment("c2", "R2", "W", "fixed in abc123"),
	}
	applyPR1(t, m)
	assertNudged(t, msg.msgs, 1, []string{"review finding", "fixed in abc123"}, nil)
}

func TestReviewReplyFilter_FinishedRunWithoutIDOnThisPRDisablesFilter(t *testing.T) {
	comments := []domain.PullRequestComment{
		reviewComment("c1", "R1", "W", "review finding"),
		reviewComment("c9", "R9", "W", "unrecorded run finding"),
	}

	m, st, msg := newReviewRunManager(completeRun("pr1", "R1"), completeRun("pr1", ""))
	st.comments["pr1"] = comments
	applyPR1(t, m)
	assertNudged(t, msg.msgs, 1, []string{"review finding", "unrecorded run finding"}, nil)

	// The same kind of run on another PR does not switch this PR's filter off.
	m, st, msg = newReviewRunManager(completeRun("pr1", "R1"), completeRun("pr2", ""))
	st.comments["pr1"] = comments
	applyPR1(t, m)
	assertNudged(t, msg.msgs, 1, []string{"review finding"}, []string{"unrecorded run finding"})
}

func TestReviewReplyFilter_RunningRunCommentInjectedOnceIDRecorded(t *testing.T) {
	running := domain.ReviewRun{SessionID: "mer-1", PRURL: "pr1", Status: domain.ReviewRunRunning}
	m, st, msg := newReviewRunManager(completeRun("pr1", "R1"), running)
	st.comments["pr1"] = []domain.PullRequestComment{
		reviewComment("c1", "R1", "W", "review finding"),
		reviewComment("c9", "R9", "W", "second pass finding"),
	}
	applyPR1(t, m)
	assertNudged(t, msg.msgs, 1, []string{"review finding"}, []string{"second pass finding"})

	// The pass submits and records its review id; the next poll injects it.
	st.runs[1] = completeRun("pr1", "R9")
	msg.msgs = nil
	applyPR1(t, m)
	assertNudged(t, msg.msgs, 1, []string{"second pass finding"}, []string{"review finding"})
}

func TestReviewReplyFilter_RunsReadOnlyWhenCommentsWouldBeInjected(t *testing.T) {
	m, st, msg := newReviewRunManager()
	st.runsErr = errors.New("db down")
	applyPR1(t, m)

	off := reviewComment("c1", "R1", "W", "review finding")
	off.AutoInjectReview = false
	resolved := reviewComment("c2", "R2", "W", "resolved finding")
	resolved.Resolved = true
	st.comments["pr1"] = []domain.PullRequestComment{off, resolved}
	applyPR1(t, m)

	if st.runsCalls != 0 {
		t.Fatalf("review runs read %d times, want 0", st.runsCalls)
	}
	assertNudged(t, msg.msgs, 0, nil, nil)
}
