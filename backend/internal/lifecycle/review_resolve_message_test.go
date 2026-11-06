package lifecycle

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// reviewResolvePhrases are the parts of the thread-closing procedure every
// review-comment nudge carries (#85).
var reviewResolvePhrases = []string{
	"you do not need glab, gh or a token",
	"list only the thread IDs you addressed",
	"Do not resolve a thread you decided not to address; report why with `ao report --checkpoint --note",
	"do not try another tool to resolve the thread",
	"`ao report --needs-input --note \"<PR URL> <thread id...>: <error>\"`",
}

func TestReviewCommentsMessage_NamesResolveCommandWithPRNumber(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.comments["pr42"] = []domain.PullRequestComment{
		{ID: "1", ThreadID: "T1", Author: "alice", File: "foo.go", Line: 12, Body: "fix this", URL: "https://github.com/o/r/pull/42#discussion_r1", AutoInjectReview: true},
	}
	o := ports.PRObservation{Fetched: true, URL: "pr42", Number: 42, Review: domain.ReviewChangesRequest}
	if err := m.ApplyPRObservation(context.Background(), "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one review nudge, got %v", msg.msgs)
	}
	got := msg.msgs[0]
	want := append([]string{
		"Thread ID: T1",
		"`ao pr resolve-comments 42 <thread id...>`",
	}, reviewResolvePhrases...)
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("review nudge missing %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, "<PR number>") {
		t.Fatalf("review nudge used the placeholder although the PR number is known:\n%s", got)
	}
	if strings.Contains(got, "resolve each thread directly after pushing when available") {
		t.Fatalf("review nudge kept the old command-less wording:\n%s", got)
	}
}

func TestReviewCommentsMessage_PlaceholderWithoutPRNumber(t *testing.T) {
	got := formatReviewCommentsMessage(0, []ports.PRCommentObservation{
		{ID: "1", ThreadID: "T1", Author: "alice", Body: "fix this"},
	})
	for _, w := range append([]string{
		"Thread ID: T1",
		"`ao pr resolve-comments <PR number> <thread id...>`",
	}, reviewResolvePhrases...) {
		if !strings.Contains(got, w) {
			t.Fatalf("message missing %q:\n%s", w, got)
		}
	}
}

func TestReviewCommentsMessage_NoCommentsBranchNamesResolveCommand(t *testing.T) {
	for _, tc := range []struct {
		number int
		want   string
	}{
		{7, "`ao pr resolve-comments 7 <thread id...>`"},
		{0, "`ao pr resolve-comments <PR number> <thread id...>`"},
	} {
		got := formatReviewCommentsMessage(tc.number, nil)
		if !strings.HasPrefix(got, "A reviewer left feedback on your PR.") {
			t.Fatalf("no-comment message lost its lead:\n%s", got)
		}
		for _, w := range append([]string{tc.want}, reviewResolvePhrases...) {
			if !strings.Contains(got, w) {
				t.Fatalf("no-comment message (number %d) missing %q:\n%s", tc.number, w, got)
			}
		}
	}
}

func TestReviewCommentsSignature_IgnoresMessageWording(t *testing.T) {
	// The dedup signature is built from comment and thread IDs only, so the
	// new wording does not resend comments already delivered.
	comments := []ports.PRCommentObservation{{ID: "1", ThreadID: "T1", Body: "fix this"}}
	if got, want := reviewCommentsSignature(comments), "T1\x001"; got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
}
