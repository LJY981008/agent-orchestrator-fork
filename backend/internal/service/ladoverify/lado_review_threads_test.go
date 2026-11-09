package ladoverify

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// commentStore is the fixture's store with the PR comments AO observed.
type commentStore struct {
	*verifyStore
	cmu      sync.Mutex
	comments map[string][]domain.PullRequestComment
	err      error
}

func (s *commentStore) ListPRComments(_ context.Context, prURL string) ([]domain.PullRequestComment, error) {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return slices.Clone(s.comments[prURL]), nil
}

func (s *commentStore) setComments(prURL string, comments ...domain.PullRequestComment) {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	s.comments[prURL] = comments
}

func (s *commentStore) failWith(err error) {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	s.err = err
}

// newReviewFixture is a verification fixture whose store lists PR comments.
func newReviewFixture(t *testing.T) (*verifyFixture, *commentStore) {
	t.Helper()
	var comments *commentStore
	f := newVerifyFixture(t, func(d *Deps) {
		comments = &commentStore{verifyStore: d.Store.(*verifyStore), comments: map[string][]domain.PullRequestComment{}}
		d.Store = comments
	})
	return f, comments
}

func humanComment(thread, id string) domain.PullRequestComment {
	return domain.PullRequestComment{ThreadID: thread, ID: id, Author: "reviewer", File: "src/login.go", Line: 3, Body: "Rename this"}
}

// resolveMessages are the review threads messages a session got.
func (f *verifyFixture) resolveMessages(id domain.SessionID) []string {
	var out []string
	for _, message := range f.sender.to(id) {
		if strings.Contains(message, "ao pr resolve-comments") {
			out = append(out, message)
		}
	}
	return out
}

func (s *Service) reviewSeenOf(key domain.SessionID) (reviewSeen, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen, ok := s.reviewSeen[key]
	return seen, ok
}

func TestUnresolvedReviewThreadsCountsThreadsThatBlockMerging(t *testing.T) {
	got := unresolvedReviewThreads([]domain.PullRequestComment{
		humanComment("T2", "c1"),
		humanComment("T2", "c2"),
		{ThreadID: "T1", ID: "c3", Body: "general note"},
		{ThreadID: "T4", ID: "c4", Resolved: true, Body: "done"},
		{ID: "c5", IsBot: true, Body: "CI passed"},
		{ThreadID: "T6", ID: "c6", IsBot: true, File: "src/a.go", Line: 9, Body: "nil check"},
		{ThreadID: "T7", ID: "c7", IsBot: true, File: "src/a.go", Body: "no line"},
		{ID: "c8", Body: "an old row without a thread id"},
	})
	want := []string{"T1", "T2", "T6", "comment:c8"}
	if !slices.Equal(got, want) {
		t.Fatalf("threads = %q, want %q", got, want)
	}
	if id := reviewThreadID("comment:c8"); id != "c8" {
		t.Fatalf("reviewThreadID = %q, want c8", id)
	}
}

// LADO #85: a thread first seen at head 1 that is still unresolved at head
// 2, with the worker idle for two minutes, gets the worker one message per
// head with the PR number, the thread ids and what to do when the command
// fails.
func TestTickAsksARestingWorkerToResolveReviewThreads(t *testing.T) {
	f, comments := newReviewFixture(t)
	exec := f.addExecution("42", verifyHead1)
	f.reads.setActivity(exec.SessionID, domain.ActivityIdle)
	comments.setComments(exec.PR.URL,
		humanComment("PRRT_a", "c1"), humanComment("PRRT_a", "c2"),
		domain.PullRequestComment{ThreadID: "PRRT_b", ID: "c3", Resolved: true},
		domain.PullRequestComment{ID: "c4", IsBot: true, Body: "Coverage 80%"})

	// The thread is seen at head 1: no push after it yet.
	f.tick()
	f.clock.advance(reviewThreadsNotifyAfter + time.Minute)
	f.tick()
	if got := f.resolveMessages(exec.SessionID); len(got) != 0 {
		t.Fatalf("messages before a push = %q, want none", got)
	}
	if seen, ok := f.svc.reviewSeenOf(exec.SessionID); !ok || seen.first["PRRT_a"] != verifyHead1 || !seen.since.IsZero() {
		t.Fatalf("seen = %+v, %v, want PRRT_a first at head 1 and no time", seen, ok)
	}

	f.push(exec, verifyHead2)
	f.tick()
	f.clock.advance(reviewThreadsNotifyAfter - time.Second)
	f.tick()
	if got := f.resolveMessages(exec.SessionID); len(got) != 0 {
		t.Fatalf("messages before two minutes = %q, want none", got)
	}
	f.clock.advance(time.Second)
	f.tick()
	got := f.resolveMessages(exec.SessionID)
	if len(got) != 1 {
		t.Fatalf("messages = %q, want one", got)
	}
	message := got[0]
	for _, want := range []string{
		shortCommit(verifyHead2), "#42", exec.PR.URL, "1 review thread is still unresolved", "PRRT_a",
		"`ao pr resolve-comments 42 <thread id...>`", "no glab, gh or token", "without ids the command resolves every unresolved thread",
		"Do not resolve a thread you did not address", "ao report --checkpoint", "ao report --needs-input --note \"" + exec.PR.URL,
	} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q lacks %q", message, want)
		}
	}
	if strings.Contains(message, "PRRT_b") || strings.Contains(message, "c4") {
		t.Errorf("message %q names a resolved thread or a bot's chatter", message)
	}
	if record := f.record(exec.SessionID, verifyHead2); !slices.Contains(record.Notified, domain.LadoNotifiedReviewThreads) {
		t.Fatalf("notified = %v, want review_threads", record.Notified)
	}
	if record := f.record(exec.SessionID, verifyHead1); slices.Contains(record.Notified, domain.LadoNotifiedReviewThreads) {
		t.Fatalf("head 1 notified = %v, want no review_threads", record.Notified)
	}

	// The same head is not told again.
	f.clock.advance(reviewThreadsNotifyAfter)
	f.tick()
	if got := f.resolveMessages(exec.SessionID); len(got) != 1 {
		t.Fatalf("messages on the same head = %d, want still 1", len(got))
	}

	// Another head is told again, two minutes after it.
	f.push(exec, verifyHead3)
	f.tick()
	if got := f.resolveMessages(exec.SessionID); len(got) != 1 {
		t.Fatalf("messages right after a new head = %d, want still 1", len(got))
	}
	f.clock.advance(reviewThreadsNotifyAfter)
	f.tick()
	if got := f.resolveMessages(exec.SessionID); len(got) != 2 || !strings.Contains(got[1], shortCommit(verifyHead3)) {
		t.Fatalf("messages after head 3 = %q, want a second for head 3", got)
	}

	// Resolved threads forget what the daemon saw.
	comments.setComments(exec.PR.URL, domain.PullRequestComment{ThreadID: "PRRT_a", ID: "c1", Resolved: true})
	f.tick()
	if _, ok := f.svc.reviewSeenOf(exec.SessionID); ok {
		t.Fatal("the review threads stay after they were resolved")
	}
}

// A worker that reported done rests, whatever activity AO reads; the
// message names five threads and counts the others.
func TestTickTellsAWorkerThatReportedDone(t *testing.T) {
	f, comments := newReviewFixture(t)
	exec := f.addExecution("7", verifyHead1)
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.WorkerDone = true })
	var threads []domain.PullRequestComment
	for _, id := range []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7"} {
		threads = append(threads, humanComment(id, "c-"+id))
	}
	comments.setComments(exec.PR.URL, threads...)
	f.tick()
	f.push(exec, verifyHead2)
	f.tick()
	f.clock.advance(reviewThreadsNotifyAfter)
	f.tick()
	got := f.resolveMessages(exec.SessionID)
	if len(got) != 1 {
		t.Fatalf("messages = %q, want one", got)
	}
	if !strings.Contains(got[0], "7 review threads are still unresolved") || !strings.Contains(got[0], "t1, t2, t3, t4, t5 and 2 more") ||
		strings.Contains(got[0], "t6") {
		t.Fatalf("message %q, want five ids and 2 more", got[0])
	}
}

// A PR without a number names the placeholder.
func TestResolveMessageWithoutANumber(t *testing.T) {
	message := resolveMessage(verifyHead1, domain.LadoExecutionPR{URL: "https://gitlab.example/g/p/-/merge_requests/3"}, []string{"comment:99"})
	for _, want := range []string{"`ao pr resolve-comments <PR number> <thread id...>`", ": 99.", "https://gitlab.example/g/p/-/merge_requests/3"} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q lacks %q", message, want)
		}
	}
	if strings.Contains(message, "comment:") {
		t.Errorf("message %q names the key prefix", message)
	}
}

func TestTickDoesNotAskToResolve(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *verifyFixture, comments *commentStore, exec domain.LadoExecution)
		// wait is how long after the push the test waits.
		wait   time.Duration
		noPush bool
	}{
		{name: "threads resolved", setup: func(f *verifyFixture, c *commentStore, exec domain.LadoExecution) {
			c.setComments(exec.PR.URL, domain.PullRequestComment{ThreadID: "T1", ID: "c1", Resolved: true})
		}},
		{name: "no push after the thread", noPush: true},
		{name: "worker active", setup: func(f *verifyFixture, _ *commentStore, exec domain.LadoExecution) {
			f.reads.setActivity(exec.SessionID, domain.ActivityActive)
			f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.WorkerDone = true })
		}},
		{name: "worker blocked", setup: func(f *verifyFixture, _ *commentStore, exec domain.LadoExecution) {
			f.reads.setActivity(exec.SessionID, domain.ActivityBlocked)
			f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.WorkerDone = true })
		}},
		{name: "worker activity unknown and not done", setup: func(f *verifyFixture, _ *commentStore, exec domain.LadoExecution) {
			f.reads.setActivity(exec.SessionID, "")
		}},
		{name: "owner is the resolve worker", setup: func(f *verifyFixture, _ *commentStore, exec domain.LadoExecution) {
			f.reads.endSession(exec.SessionID)
			f.addSession("lado-fix-1")
			f.reads.setActivity("lado-fix-1", domain.ActivityIdle)
			f.store.update(exec.SessionID, func(e *domain.LadoExecution) {
				e.Resolve = domain.LadoExecutionResolve{State: domain.LadoResolveResolving, SessionID: "lado-fix-1"}
			})
		}},
		{name: "no owner", setup: func(f *verifyFixture, _ *commentStore, exec domain.LadoExecution) {
			f.reads.endSession(exec.SessionID)
		}},
		{name: "before two minutes", wait: reviewThreadsNotifyAfter - time.Second},
		{name: "only a bot's chatter", setup: func(f *verifyFixture, c *commentStore, exec domain.LadoExecution) {
			c.setComments(exec.PR.URL, domain.PullRequestComment{ThreadID: "T9", ID: "c9", IsBot: true, Body: "Deploy preview ready"})
		}},
		{name: "a store without comments", setup: func(f *verifyFixture, c *commentStore, _ domain.LadoExecution) {
			c.failWith(errors.New("database is locked"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, comments := newReviewFixture(t)
			exec := f.addExecution("1", verifyHead1)
			f.reads.setActivity(exec.SessionID, domain.ActivityIdle)
			comments.setComments(exec.PR.URL, humanComment("T1", "c1"))
			if tc.setup != nil {
				tc.setup(f, comments, exec)
			}
			f.tick()
			head := verifyHead1
			if !tc.noPush {
				f.push(exec, verifyHead2)
				head = verifyHead2
				f.tick()
			}
			f.clock.advance(orDuration(tc.wait, reviewThreadsNotifyAfter+time.Minute))
			f.tick()
			for _, id := range []domain.SessionID{exec.SessionID, "lado-fix-1"} {
				if got := f.resolveMessages(id); len(got) != 0 {
					t.Fatalf("messages to %s = %q, want none", id, got)
				}
			}
			if record := f.record(exec.SessionID, head); slices.Contains(record.Notified, domain.LadoNotifiedReviewThreads) {
				t.Fatalf("notified = %v, want no review_threads", record.Notified)
			}
		})
	}
}

func orDuration(d, fallback time.Duration) time.Duration {
	if d != 0 {
		return d
	}
	return fallback
}

// The two minutes start over when the worker works again: idle for a
// minute, active, then idle again waits two minutes from then.
func TestResolveWaitStartsOverWhenTheWorkerWorks(t *testing.T) {
	f, comments := newReviewFixture(t)
	exec := f.addExecution("1", verifyHead1)
	f.reads.setActivity(exec.SessionID, domain.ActivityIdle)
	comments.setComments(exec.PR.URL, humanComment("T1", "c1"))
	f.tick()
	f.push(exec, verifyHead2)
	f.tick()
	f.clock.advance(time.Minute)
	f.tick()

	f.reads.setActivity(exec.SessionID, domain.ActivityActive)
	f.tick()
	if seen, _ := f.svc.reviewSeenOf(exec.SessionID); !seen.since.IsZero() {
		t.Fatalf("since while active = %s, want none", seen.since)
	}
	f.reads.setActivity(exec.SessionID, domain.ActivityWaitingInput)
	f.tick()
	back := f.svc.now()

	// A new thread does not start the time over.
	comments.setComments(exec.PR.URL, humanComment("T1", "c1"), humanComment("T2", "c2"))
	f.clock.advance(90 * time.Second)
	f.tick()
	if got := f.resolveMessages(exec.SessionID); len(got) != 0 {
		t.Fatalf("messages 90s after the worker rested again = %q, want none", got)
	}
	if seen, _ := f.svc.reviewSeenOf(exec.SessionID); !seen.since.Equal(back) || seen.first["T2"] != verifyHead2 {
		t.Fatalf("seen = %+v, want since %s and T2 first at head 2", seen, back)
	}
	f.clock.advance(30 * time.Second)
	f.tick()
	got := f.resolveMessages(exec.SessionID)
	if len(got) != 1 || !strings.Contains(got[0], "2 review threads") || !strings.Contains(got[0], "T1, T2") {
		t.Fatalf("messages two minutes after the worker rested again = %q, want one naming T1 and T2", got)
	}
}

// A message that could not be sent goes out on a later pass; a settled PR
// forgets what the daemon saw.
func TestResolveMessageRetriesAndASettledPRForgets(t *testing.T) {
	f, comments := newReviewFixture(t)
	exec := f.addExecution("1", verifyHead1)
	f.reads.setActivity(exec.SessionID, domain.ActivityIdle)
	comments.setComments(exec.PR.URL, humanComment("T1", "c1"))
	f.tick()
	f.push(exec, verifyHead2)
	f.tick()
	f.clock.advance(reviewThreadsNotifyAfter)
	// The fix requests of the pass may take a send first; fail them all.
	f.sender.failNext(exec.SessionID, 10)
	f.tick()
	if record := f.record(exec.SessionID, verifyHead2); slices.Contains(record.Notified, domain.LadoNotifiedReviewThreads) {
		t.Fatalf("notified after a failed send = %v, want no review_threads", record.Notified)
	}
	f.sender.failNext(exec.SessionID, 0)
	f.tick()
	if got := f.resolveMessages(exec.SessionID); len(got) != 1 {
		t.Fatalf("messages = %q, want one", got)
	}

	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.PR.Merged = true })
	f.tick()
	if _, ok := f.svc.reviewSeenOf(exec.SessionID); ok {
		t.Fatal("the review threads stay after the PR merged")
	}
}

func TestPruneReviewSeenForgetsExecutionsNotListed(t *testing.T) {
	s := New(Deps{})
	s.reviewSeen["lado-1"] = reviewSeen{head: verifyHead1}
	s.reviewSeen["lado-2"] = reviewSeen{head: verifyHead2}
	s.pruneReviewSeen([]domain.LadoExecution{{SessionID: "lado-2"}})
	if _, ok := s.reviewSeenOf("lado-1"); ok {
		t.Fatal("lado-1 kept its review threads")
	}
	if _, ok := s.reviewSeenOf("lado-2"); !ok {
		t.Fatal("lado-2 lost its review threads")
	}
}

// The execution view counts unresolved threads, one per thread, without
// resolved ones and a bot's chatter but with a bot's anchored comment; the
// record and its hash stay the same.
func TestHeadCountsUnresolvedReviewThreads(t *testing.T) {
	f, comments := newReviewFixture(t)
	exec := f.addExecution("1", verifyHead1)
	f.tick()
	before, err := f.svc.Head(f.ctx, exec.SessionID, exec.PR.URL, verifyHead1, "main")
	if err != nil {
		t.Fatal(err)
	}
	if before.UnresolvedReviewThreads != 0 || before.Verification == nil {
		t.Fatalf("view without comments = %+v, want a record and no threads", before)
	}

	comments.setComments(exec.PR.URL,
		humanComment("T1", "c1"), humanComment("T1", "c2"), humanComment("T1", "c3"),
		domain.PullRequestComment{ThreadID: "T2", ID: "c4", Resolved: true},
		domain.PullRequestComment{ID: "c5", IsBot: true, Body: "Build passed"},
		domain.PullRequestComment{ThreadID: "T3", ID: "c6", IsBot: true, File: "src/a.go", Line: 4, Body: "possible nil"})
	f.tick()
	view, err := f.svc.Head(f.ctx, exec.SessionID, exec.PR.URL, verifyHead1, "main")
	if err != nil {
		t.Fatal(err)
	}
	if view.UnresolvedReviewThreads != 2 {
		t.Fatalf("threads = %d, want 2 (T1 and the bot's anchored T3)", view.UnresolvedReviewThreads)
	}
	if view.SHA256 != before.SHA256 || view.SHA256 != verifySHA(t, f.record(exec.SessionID, verifyHead1)) {
		t.Fatalf("record hash = %s, want %s as without threads", view.SHA256, before.SHA256)
	}

	// A failed read counts none and does not fail the view.
	comments.failWith(errors.New("database is locked"))
	view, err = f.svc.Head(f.ctx, exec.SessionID, exec.PR.URL, verifyHead1, "main")
	if err != nil || view.UnresolvedReviewThreads != 0 || view.SHA256 != before.SHA256 {
		t.Fatalf("view after a failed read = %+v, %v, want the record and no threads", view, err)
	}
}

// A store that does not list comments shows no threads.
func TestHeadWithoutACommentListerCountsNone(t *testing.T) {
	f := newVerifyFixture(t)
	exec := f.addExecution("1", verifyHead1)
	f.tick()
	view, err := f.svc.Head(f.ctx, exec.SessionID, exec.PR.URL, verifyHead1, "main")
	if err != nil || view.UnresolvedReviewThreads != 0 || view.Verification == nil {
		t.Fatalf("view = %+v, %v, want the record and no threads", view, err)
	}
}
