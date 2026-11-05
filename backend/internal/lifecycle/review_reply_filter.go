package lifecycle

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ReviewRunLister lists a session's AO review passes. It stays optional on the
// broad lifecycle Store so focused reducer fakes do not need it; production
// SQLite implements it. Without it, unresolved review comments are injected
// unfiltered.
type ReviewRunLister interface {
	ListReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
}

// reviewReplyFilter tells the worker's own replies in a review thread apart
// from review feedback (#48). AO's reviewer and the worker post from the same
// account, so the author alone cannot decide it: a comment is a worker reply
// when it belongs to a GitHub review that no AO review pass recorded, and its
// author is the account those passes posted from.
type reviewReplyFilter struct {
	enabled bool
	// runReviewIDs holds the GitHub review ids AO review passes recorded.
	runReviewIDs map[string]struct{}
	// sessionAuthors holds the normalized authors of comments that belong to
	// those reviews: the account the reviewer and the worker both post from.
	sessionAuthors map[string]struct{}
}

// loadReviewReplyFilter builds the filter for one PR from the session's review
// runs and that PR's persisted comments. It is disabled (inject everything, the
// old behaviour) when no run recorded a review id, when no comment proves which
// account the reviewer posted from, or when a finished pass on this PR left no
// review id: that pass may still have posted inline comments that would look
// exactly like worker replies. A running pass is not counted, because its
// comments are only skipped until the run records its id on the next poll.
func loadReviewReplyFilter(runs []domain.ReviewRun, prURL string, comments []domain.PullRequestComment) reviewReplyFilter {
	f := reviewReplyFilter{
		runReviewIDs:   map[string]struct{}{},
		sessionAuthors: map[string]struct{}{},
	}
	for _, run := range runs {
		id := strings.TrimSpace(run.GithubReviewID)
		if id == "" {
			if run.PRURL == prURL && run.Status != domain.ReviewRunRunning {
				return reviewReplyFilter{}
			}
			continue
		}
		f.runReviewIDs[id] = struct{}{}
	}
	if len(f.runReviewIDs) == 0 {
		return reviewReplyFilter{}
	}
	for _, c := range comments {
		if _, ok := f.runReviewIDs[strings.TrimSpace(c.ReviewID)]; !ok {
			continue
		}
		if author := normalizeReviewAuthor(c.Author); author != "" {
			f.sessionAuthors[author] = struct{}{}
		}
	}
	if len(f.sessionAuthors) == 0 {
		return reviewReplyFilter{}
	}
	f.enabled = true
	return f
}

// isWorkerReviewReply reports whether comment is the worker's own reply and so
// must not be injected back into the worker.
func (f reviewReplyFilter) isWorkerReviewReply(comment ports.PRCommentObservation) bool {
	if !f.enabled {
		return false
	}
	reviewID := strings.TrimSpace(comment.ReviewID)
	if reviewID == "" {
		return false
	}
	if _, ok := f.runReviewIDs[reviewID]; ok {
		return false
	}
	author := normalizeReviewAuthor(comment.Author)
	if author == "" {
		return false
	}
	_, ok := f.sessionAuthors[author]
	return ok
}

func normalizeReviewAuthor(author string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(author), "@"))
}
