package lifecycle

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The review comments of one PR observation go to the worker as one message
// (#89), while each comment keeps its own dedup key, signature and budget.

func batchComment(n int) domain.PullRequestComment {
	return domain.PullRequestComment{
		ID: fmt.Sprintf("c%02d", n), ThreadID: fmt.Sprintf("THREAD-%02d", n),
		Author: "alice", File: "foo.go", Line: n,
		Body:             fmt.Sprintf("finding-%02d body", n),
		URL:              fmt.Sprintf("https://github.com/o/r/pull/1#discussion_r%02d", n),
		AutoInjectReview: true,
	}
}

func prCommentObservation(c domain.PullRequestComment) ports.PRCommentObservation {
	return prCommentObservations([]domain.PullRequestComment{c})[0]
}

func batchComments(from, to int) []domain.PullRequestComment {
	out := make([]domain.PullRequestComment, 0, to-from+1)
	for n := from; n <= to; n++ {
		out = append(out, batchComment(n))
	}
	return out
}

func newBatchManager(t *testing.T) (*Manager, *fakeStore, *fakeMessenger) {
	t.Helper()
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	return m, st, msg
}

func observeBatch(t *testing.T, m *Manager, o ports.PRObservation) {
	t.Helper()
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
}

func batchObservation() ports.PRObservation {
	return ports.PRObservation{Fetched: true, URL: "pr1", Number: 1}
}

func assertContainsAll(t *testing.T, msg string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Fatalf("message missing %q:\n%s", w, msg)
		}
	}
}

func assertContainsNone(t *testing.T, msg string, notWant ...string) {
	t.Helper()
	for _, w := range notWant {
		if strings.Contains(msg, w) {
			t.Fatalf("message includes %q:\n%s", w, msg)
		}
	}
}

func bodiesOf(comments []domain.PullRequestComment) []string {
	out := make([]string, 0, len(comments))
	for _, c := range comments {
		out = append(out, c.Body)
	}
	return out
}

// (a)–(c): seven comments → one message; a new comment → a message with only
// that one; nothing new → no message.
func TestReviewCommentBatch_OneMessageThenOnlyNewComments(t *testing.T) {
	m, st, msg := newBatchManager(t)
	st.comments["pr1"] = batchComments(1, 7)
	observeBatch(t, m, batchObservation())

	if len(msg.msgs) != 1 {
		t.Fatalf("first pass sent %d messages, want 1: %q", len(msg.msgs), msg.msgs)
	}
	got := msg.msgs[0]
	assertContainsAll(t, got, "The following 7 unresolved review comment(s)")
	for n := 1; n <= 7; n++ {
		c := batchComment(n)
		assertContainsAll(t, got, c.Body, "Thread ID: "+c.ThreadID, fmt.Sprintf("\n%d. foo.go:%d (@alice):", n, n))
	}
	if count := strings.Count(got, "ao pr resolve-comments"); count != 1 {
		t.Fatalf("resolve instructions appear %d times, want 1:\n%s", count, got)
	}
	assertContainsNone(t, got, "did not fit")

	st.comments["pr1"] = batchComments(1, 8)
	observeBatch(t, m, batchObservation())
	if len(msg.msgs) != 2 {
		t.Fatalf("second pass sent %d messages, want 1 more: %q", len(msg.msgs)-1, msg.msgs[1:])
	}
	second := msg.msgs[1]
	assertContainsAll(t, second, "The following 1 unresolved review comment(s)", batchComment(8).Body, "Thread ID: THREAD-08")
	assertContainsNone(t, second, bodiesOf(batchComments(1, 7))...)

	observeBatch(t, m, batchObservation())
	if len(msg.msgs) != 2 {
		t.Fatalf("unchanged third pass sent %d messages: %q", len(msg.msgs)-2, msg.msgs[2:])
	}
}

// (d): a comment whose attempts reached reviewMaxNudge stays out.
func TestReviewCommentBatch_SpentBudgetCommentLeftOut(t *testing.T) {
	m, st, msg := newBatchManager(t)
	spent := batchComment(1)
	key := commentNudgeKey("pr1", prCommentObservation(spent))
	raw, err := json.Marshal(reactionPayload{
		Seen:     map[string]string{key: "an older signature"},
		Attempts: map[string]int{key: reviewMaxNudge},
	})
	if err != nil {
		t.Fatal(err)
	}
	st.signatures["pr1"] = string(raw)
	st.comments["pr1"] = batchComments(1, 3)
	observeBatch(t, m, batchObservation())

	if len(msg.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1: %q", len(msg.msgs), msg.msgs)
	}
	assertContainsAll(t, msg.msgs[0], "The following 2 unresolved review comment(s)", batchComment(2).Body, batchComment(3).Body)
	assertContainsNone(t, msg.msgs[0], spent.Body)
}

// (e): a payload persisted before #89 (per-comment seen keys) still dedups.
func TestReviewCommentBatch_PayloadFromBeforeBatchingStillDedups(t *testing.T) {
	m, st, msg := newBatchManager(t)
	// Written as the pre-#89 code persisted it: one "comment:<url>:<id>" key per
	// comment, the signature being "<thread id>\x00<comment id>".
	st.signatures["pr1"] = `{"seen":{"comment:pr1:c01":"THREAD-01\u0000c01","comment:pr1:c02":"THREAD-02\u0000c02"},` +
		`"attempts":{"comment:pr1:c01":1,"comment:pr1:c02":1}}`
	st.comments["pr1"] = batchComments(1, 3)
	observeBatch(t, m, batchObservation())

	if len(msg.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1: %q", len(msg.msgs), msg.msgs)
	}
	assertContainsAll(t, msg.msgs[0], "The following 1 unresolved review comment(s)", batchComment(3).Body)
	assertContainsNone(t, msg.msgs[0], batchComment(1).Body, batchComment(2).Body)

	var p reactionPayload
	if err := json.Unmarshal([]byte(st.signatures["pr1"]), &p); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c01", "c02", "c03"} {
		if p.Attempts["comment:pr1:"+id] != 1 {
			t.Fatalf("persisted attempts for %s = %d, want 1: %+v", id, p.Attempts["comment:pr1:"+id], p)
		}
	}
}

// (f): past the comment-count limit, the rest are named without bodies, the
// worker is told not to wait, and their bodies go on the next PR change.
func TestReviewCommentBatch_CountLimitNamesTheRestAndSendsThemLater(t *testing.T) {
	m, st, msg := newBatchManager(t)
	total := reviewCommentBatchMaxComments + 3
	st.comments["pr1"] = batchComments(1, total)
	observeBatch(t, m, batchObservation())

	if len(msg.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(msg.msgs))
	}
	got := msg.msgs[0]
	assertContainsAll(t, got,
		fmt.Sprintf("The following %d unresolved review comment(s)", reviewCommentBatchMaxComments),
		"3 more unresolved review comment(s) did not fit in this message.",
		"address them too",
		"Do not wait for AO",
	)
	assertContainsAll(t, got, bodiesOf(batchComments(1, reviewCommentBatchMaxComments))...)
	omitted := batchComments(reviewCommentBatchMaxComments+1, total)
	for _, c := range omitted {
		assertContainsAll(t, got, fmt.Sprintf("- foo.go:%d (@alice) Thread ID: %s %s", c.Line, c.ThreadID, c.URL))
	}
	assertContainsNone(t, got, bodiesOf(omitted)...)
	assertContainsNone(t, got, "AO sends them in a later message", "wait for the next message")
	if strings.Index(got, "did not fit") > strings.Index(got, "ao pr resolve-comments") {
		t.Fatalf("omitted list must come before the resolve instructions:\n%s", got)
	}

	// In production lifecycle runs again only after the PR changes (a push, CI
	// or review change); this second call stands for that next observation.
	observeBatch(t, m, batchObservation())
	if len(msg.msgs) != 2 {
		t.Fatalf("next pass sent %d messages, want 1 more", len(msg.msgs)-1)
	}
	second := msg.msgs[1]
	assertContainsAll(t, second, "The following 3 unresolved review comment(s)")
	assertContainsAll(t, second, bodiesOf(omitted)...)
	assertContainsNone(t, second, bodiesOf(batchComments(1, reviewCommentBatchMaxComments))...)
	assertContainsNone(t, second, "did not fit")
}

// (g): the byte limit counts sanitized numbered entries; the first comment goes
// in even when it alone is over the limit.
func TestReviewCommentBatch_ByteLimit(t *testing.T) {
	big := func(n, size int) domain.PullRequestComment {
		c := batchComment(n)
		c.Body = fmt.Sprintf("BODY-%02d ", n) + strings.Repeat("x", size)
		return c
	}
	entrySize := func(n int, c domain.PullRequestComment) int {
		return len(formatReviewCommentEntry(n, prCommentObservation(c)))
	}

	t.Run("stops before the entry that would cross the limit", func(t *testing.T) {
		m, st, msg := newBatchManager(t)
		size := reviewCommentBatchMaxBytes / 3
		comments := []domain.PullRequestComment{big(1, size), big(2, size), big(3, size), big(4, 10)}
		st.comments["pr1"] = comments
		observeBatch(t, m, batchObservation())

		sum := entrySize(1, comments[0]) + entrySize(2, comments[1])
		if sum > reviewCommentBatchMaxBytes || sum+entrySize(3, comments[2]) <= reviewCommentBatchMaxBytes {
			t.Fatalf("fixture does not straddle the limit: two entries %d bytes, three %d", sum, sum+entrySize(3, comments[2]))
		}
		if len(msg.msgs) != 1 {
			t.Fatalf("sent %d messages, want 1", len(msg.msgs))
		}
		got := msg.msgs[0]
		assertContainsAll(t, got, "The following 2 unresolved review comment(s)", "BODY-01", "BODY-02",
			"2 more unresolved review comment(s) did not fit", "Thread ID: THREAD-03", "Thread ID: THREAD-04")
		// The fourth is small but comes after one that did not fit: the batch
		// is a prefix of the due comments, in the order the store lists them.
		assertContainsNone(t, got, "BODY-03", "BODY-04")
	})

	t.Run("first comment over the limit still goes in whole", func(t *testing.T) {
		m, st, msg := newBatchManager(t)
		comments := []domain.PullRequestComment{big(1, reviewCommentBatchMaxBytes+100), big(2, 10)}
		st.comments["pr1"] = comments
		observeBatch(t, m, batchObservation())

		if len(msg.msgs) != 1 {
			t.Fatalf("sent %d messages, want 1", len(msg.msgs))
		}
		got := msg.msgs[0]
		assertContainsAll(t, got, "The following 1 unresolved review comment(s)", comments[0].Body,
			"1 more unresolved review comment(s) did not fit", "Thread ID: THREAD-02")
		assertContainsNone(t, got, "BODY-02")
	})
}

// The body-less list of comments that did not fit stops at
// reviewCommentOmittedMaxLines lines.
func TestReviewCommentBatch_OmittedListCapped(t *testing.T) {
	omitted := make([]ports.PRCommentObservation, 0, reviewCommentOmittedMaxLines+3)
	for n := 1; n <= reviewCommentOmittedMaxLines+3; n++ {
		omitted = append(omitted, prCommentObservation(batchComment(n)))
	}
	got := formatReviewCommentsMessage(1, omitted[:1], omitted)
	if lines := strings.Count(got, "\n- foo.go:"); lines != reviewCommentOmittedMaxLines {
		t.Fatalf("listed %d omitted comments, want %d:\n%s", lines, reviewCommentOmittedMaxLines, got)
	}
	assertContainsAll(t, got, fmt.Sprintf("%d more unresolved review comment(s) did not fit", len(omitted)), "and 3 more on the PR")
}

// A nil omitted list keeps the message exactly as before batching.
func TestReviewCommentBatch_NoOmittedKeepsMessage(t *testing.T) {
	comments := []ports.PRCommentObservation{prCommentObservation(batchComment(1))}
	got := formatReviewCommentsMessage(1, comments, nil)
	want := "The following 1 unresolved review comment(s) are on your PR as of just now. You should not need to re-fetch this data unless you need additional context.\n" +
		"\n1. foo.go:1 (@alice):\nfinding-01 body\n   https://github.com/o/r/pull/1#discussion_r01\n   Thread ID: THREAD-01\n" +
		"\nAddress each comment and push fixes. You should not need to re-fetch review data unless you need additional context beyond what is provided here.\n\n" +
		reviewThreadResolveInstructions(1)
	if got != want {
		t.Fatalf("message changed:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// (h): comments with neither ID nor thread ID share one key; only the first
// goes in and its attempts rise by one.
func TestReviewCommentBatch_SharedEmptyKeyOnce(t *testing.T) {
	m, st, msg := newBatchManager(t)
	st.comments["pr1"] = []domain.PullRequestComment{
		{Author: "alice", Body: "first anonymous", AutoInjectReview: true},
		{Author: "bob", Body: "second anonymous", AutoInjectReview: true},
	}
	// The signature of an ID-less comment falls back to the review decision;
	// no changes-requested review is stored, so no review nudge is sent.
	o := batchObservation()
	o.Review = domain.ReviewChangesRequest
	observeBatch(t, m, o)

	if len(msg.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1: %q", len(msg.msgs), msg.msgs)
	}
	assertContainsAll(t, msg.msgs[0], "The following 1 unresolved review comment(s)", "first anonymous")
	assertContainsNone(t, msg.msgs[0], "second anonymous")
	if got := m.react.attempts["comment:pr1:"]; got != 1 {
		t.Fatalf("attempts for the shared key = %d, want 1", got)
	}
}

// (i): a suppressed send records nothing, so the whole batch goes later.
func TestReviewCommentBatch_SuppressedRecordsNothing(t *testing.T) {
	m, st, msg := newBatchManager(t)
	m.SetSessionInputLease(fixedLifecycleInputLease(false))
	st.comments["pr1"] = batchComments(1, 4)
	observeBatch(t, m, batchObservation())

	if len(msg.msgs) != 0 {
		t.Fatalf("suppressed pass sent %q", msg.msgs)
	}
	if st.signatureWrites != 0 || st.signatures["pr1"] != "" {
		t.Fatalf("suppressed pass persisted %d times: %q", st.signatureWrites, st.signatures["pr1"])
	}
	for _, c := range batchComments(1, 4) {
		if key := commentNudgeKey("pr1", prCommentObservation(c)); m.react.attempts[key] != 0 || m.react.seen[key] != "" {
			t.Fatalf("suppressed pass recorded %s", key)
		}
	}

	m.SetSessionInputLease(fixedLifecycleInputLease(true))
	observeBatch(t, m, batchObservation())
	if len(msg.msgs) != 1 {
		t.Fatalf("sent %d messages after the lease opened, want 1", len(msg.msgs))
	}
	assertContainsAll(t, msg.msgs[0], "The following 4 unresolved review comment(s)")
	assertContainsAll(t, msg.msgs[0], bodiesOf(batchComments(1, 4))...)
}

// (j): CI failure, review comments and a changes-requested review give three
// messages in that order.
func TestReviewCommentBatch_AlongsideCIAndChangesRequested(t *testing.T) {
	m, st, msg := newBatchManager(t)
	st.comments["pr1"] = batchComments(1, 5)
	st.reviews["pr1"] = []domain.PullRequestReview{
		{ID: "R1", State: domain.ReviewChangesRequest, Author: "bob", Body: "please change", AutoInjectReview: true},
	}
	o := batchObservation()
	o.CI = domain.CIFailing
	o.Checks = []ports.PRCheckObservation{{Name: "build", CommitHash: "h1", Status: domain.PRCheckFailed, LogTail: "boom"}}
	o.Review = domain.ReviewChangesRequest
	observeBatch(t, m, o)

	if len(msg.msgs) != 3 {
		t.Fatalf("sent %d messages, want CI + comments + review: %q", len(msg.msgs), msg.msgs)
	}
	assertContainsAll(t, msg.msgs[0], "CI is failing")
	assertContainsAll(t, msg.msgs[1], "The following 5 unresolved review comment(s)")
	assertContainsAll(t, msg.msgs[1], bodiesOf(batchComments(1, 5))...)
	assertContainsAll(t, msg.msgs[2], "A changes-requested review from @bob", "please change")

	observeBatch(t, m, o)
	if len(msg.msgs) != 3 {
		t.Fatalf("unchanged pass sent %d more messages: %q", len(msg.msgs)-3, msg.msgs[3:])
	}
}
