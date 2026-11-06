package ladoverify

// LADO #85: a worker that addressed review comments and pushed often leaves
// their threads open, and an unresolved thread keeps AO from calling the PR
// ready to merge. When review threads outlive a push and the execution's own
// worker rests (idle, waiting for input, or reported done) for
// reviewThreadsNotifyAfter, the worker is told once per head how to resolve
// the threads it addressed with `ao pr resolve-comments`. Nothing is
// resolved for it: a thread it did not address stays open. The execution
// view shows how many threads are unresolved (HeadView); the count stays
// out of the record and its hash.

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// reviewThreadsNotifyAfter is how long the conditions of the review threads
// message must hold on a head before the worker is told. A worker that
// resolved its threads on the host needs AO's next observation to see it.
const reviewThreadsNotifyAfter = 2 * time.Minute

// reviewThreadsListed is how many thread ids the message names; it counts
// the others.
const reviewThreadsListed = 5

// commentKeyPrefix keys an unresolved comment without a thread id, by its
// comment id, which `ao pr resolve-comments` takes too.
const commentKeyPrefix = "comment:"

// prCommentLister reads a PR's review comments as AO observed them. The
// daemon's store (*store.Store) has it; the Store interface does not ask
// for it, so a store without it shows no threads and sends no message.
type prCommentLister interface {
	ListPRComments(ctx context.Context, prURL string) ([]domain.PullRequestComment, error)
}

// reviewSeen is what this daemon saw of an execution's (passKey) review
// threads, in memory: first holds, by thread key, the head a thread was
// first seen unresolved at, and since when the message's conditions hold on
// head (zero while they do not). A daemon that restarts sees every thread
// first at the head of the time, which only delays the message to the next
// push.
type reviewSeen struct {
	head  string
	first map[string]string
	since time.Time
}

// unresolvedReviewThreads returns the sorted keys of the threads of the
// comments that keep a PR from being ready to merge
// (domain.IsActionableReviewComment): unresolved human comments, and bot
// comments anchored to a file and line. A comment without a thread id is
// keyed by commentKeyPrefix and its id.
func unresolvedReviewThreads(comments []domain.PullRequestComment) []string {
	var keys []string
	for _, comment := range comments {
		if !domain.IsActionableReviewComment(comment.Resolved, comment.IsBot, comment.File, comment.Line) {
			continue
		}
		key := strings.TrimSpace(comment.ThreadID)
		if key == "" {
			if comment.ID == "" {
				continue
			}
			key = commentKeyPrefix + comment.ID
		}
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// reviewThreadID is the id `ao pr resolve-comments` takes for a thread key.
func reviewThreadID(key string) string {
	return strings.TrimPrefix(key, commentKeyPrefix)
}

// readReviewThreads reads the unresolved review threads of a PR; ok is
// false when the store cannot list comments or the read failed.
func (s *Service) readReviewThreads(ctx context.Context, prURL string) ([]string, bool) {
	lister, ok := s.deps.Store.(prCommentLister)
	if !ok || prURL == "" {
		return nil, false
	}
	comments, err := lister.ListPRComments(ctx, prURL)
	if err != nil {
		s.deps.Log.Warn("LADO could not read the review comments of a pull request", "pr", prURL, "err", err)
		return nil, false
	}
	return unresolvedReviewThreads(comments), true
}

// unresolvedReviewThreadCount is the number of unresolved review threads of
// a PR for the execution view; a read that fails counts none.
func (s *Service) unresolvedReviewThreadCount(ctx context.Context, prURL string) int {
	threads, _ := s.readReviewThreads(ctx, prURL)
	return len(threads)
}

// workerRests reports whether the execution's worker rests: it is not busy,
// waiting on an approval prompt or gone, and it is idle, waits for input or
// reported done.
func workerRests(activity domain.ActivityState, done bool) bool {
	switch activity {
	case domain.ActivityBlocked, domain.ActivityActive, domain.ActivityExited:
		return false
	case domain.ActivityIdle, domain.ActivityWaitingInput:
		return true
	}
	return done
}

// requestResolve keeps what this daemon saw of the head's review threads
// and, once threads first seen at an earlier head are still unresolved and
// the execution's own live worker rested for reviewThreadsNotifyAfter, asks
// it once per head to resolve the threads it addressed, returning the
// notified list with domain.LadoNotifiedReviewThreads. The time starts over
// whenever a condition stops holding or the head moves; a new thread does
// not start it over. A message that could not be sent goes out on a later
// pass. A resolve worker is never told: it does not own the PR.
func (s *Service) requestResolve(ctx context.Context, in headInputs, notified []string) []string {
	key := passKey(in.exec)
	threads, ok := s.readReviewThreads(ctx, in.exec.PR.URL)
	if !ok {
		return notified
	}
	if len(threads) == 0 {
		s.forgetReviewSeen(key)
		return notified
	}
	now := s.now()
	s.mu.Lock()
	seen, found := s.reviewSeen[key]
	moved := !found || !strings.EqualFold(seen.head, in.head)
	first := make(map[string]string, len(threads))
	pushed := false
	for _, thread := range threads {
		at := cmp.Or(seen.first[thread], in.head)
		first[thread] = at
		if !strings.EqualFold(at, in.head) {
			pushed = true
		}
	}
	holds := in.live && in.owner == in.exec.SessionID && pushed &&
		workerRests(in.ownerActivity, in.exec.WorkerDone)
	since := seen.since
	if moved || !holds {
		since = time.Time{}
	}
	if holds && since.IsZero() {
		since = now
	}
	s.reviewSeen[key] = reviewSeen{head: in.head, first: first, since: since}
	s.mu.Unlock()
	if !holds || s.deps.Sender == nil || now.Sub(since) < reviewThreadsNotifyAfter ||
		slices.Contains(notified, domain.LadoNotifiedReviewThreads) {
		return notified
	}
	if err := s.deps.Sender.Send(ctx, in.owner, resolveMessage(in.head, in.exec.PR, threads)); err != nil {
		s.deps.Log.Warn("LADO could not ask a worker to resolve its review threads; it tries again", "session", in.owner, "head", in.head, "err", err)
		return notified
	}
	return append(slices.Clone(notified), domain.LadoNotifiedReviewThreads)
}

// resolveMessage asks the worker to resolve the review threads it addressed
// on the PR at head, naming the first reviewThreadsListed of them and how
// many more there are, and to report the threads it leaves open.
func resolveMessage(head string, pr domain.LadoExecutionPR, threads []string) string {
	number, ref := "<PR number>", pr.URL
	if pr.Number > 0 {
		number = strconv.Itoa(pr.Number)
		ref = fmt.Sprintf("#%d (%s)", pr.Number, pr.URL)
	}
	ids := make([]string, 0, min(len(threads), reviewThreadsListed))
	for _, thread := range threads[:min(len(threads), reviewThreadsListed)] {
		ids = append(ids, reviewThreadID(thread))
	}
	list := strings.Join(ids, ", ")
	if more := len(threads) - len(ids); more > 0 {
		list += fmt.Sprintf(" and %d more", more)
	}
	noun := "threads are"
	if len(threads) == 1 {
		noun = "thread is"
	}
	return fmt.Sprintf("LADO checks head %s of your pull request %s. %d review %s still unresolved there "+
		"after you pushed: %s. An unresolved thread keeps the pull request from being ready to merge.\n\n"+
		"Resolve each thread you addressed with `ao pr resolve-comments %s <thread id...>`. The AO daemon resolves it on "+
		"GitHub and GitLab alike: you need no glab, gh or token. Name only the threads you addressed: without ids the "+
		"command resolves every unresolved thread. Do not resolve a thread you did not address; report why with "+
		"`ao report --checkpoint --note \"<thread id>: <reason>\"`. If the command fails, do not try another tool: report it "+
		"with `ao report --needs-input --note \"%s <thread id...>: <error>\"`.",
		shortCommit(head), ref, len(threads), noun, list, number, pr.URL)
}

// forgetReviewSeen forgets what this daemon saw of an execution's review
// threads.
func (s *Service) forgetReviewSeen(key domain.SessionID) {
	s.mu.Lock()
	delete(s.reviewSeen, key)
	s.mu.Unlock()
}

// pruneReviewSeen forgets the review threads of the executions a complete
// listing of those to verify left out: they settled or are gone.
func (s *Service) pruneReviewSeen(listed []domain.LadoExecution) {
	keep := make(map[domain.SessionID]bool, len(listed))
	for _, exec := range listed {
		keep[passKey(exec)] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.reviewSeen {
		if !keep[key] {
			delete(s.reviewSeen, key)
		}
	}
}
