package review

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// gitlabPostTimeout bounds posting one review on a GitLab merge request. The
// post runs detached from the submit request, so a reviewer CLI that gives up
// cannot cut a half-posted review short.
const gitlabPostTimeout = 60 * time.Second

// isGitLabMergeRequest reports whether a review run's PR URL is a GitLab merge
// request.
func isGitLabMergeRequest(prURL string) bool {
	return strings.Contains(prURL, "/-/merge_requests/")
}

// postGitLabReview posts a completed review on its GitLab merge request with
// the daemon's GitLab credentials (LADO fork). The reviewer has no GitLab CLI
// or token, so it hands its summary and required changes to `ao review
// submit` and AO posts them. The comments become unresolved discussions,
// which the SCM observer forwards to the worker like any other review.
//
// Posting is best effort: a failure is logged and reported as not posted, but
// never fails the submit, because the verdict is already recorded.
func (s *Service) postGitLabReview(ctx context.Context, run domain.ReviewRun, review SubmittedReview) bool {
	logger := slog.Default()
	if s.poster == nil {
		logger.WarnContext(ctx, "review: GitLab review not posted: no review poster is configured", "session", run.SessionID, "run", run.ID)
		return false
	}
	ref, err := s.gitlabReviewRef(ctx, run)
	if err != nil {
		logger.WarnContext(ctx, "review: GitLab review not posted", "session", run.SessionID, "run", run.ID, "err", err)
		return false
	}
	postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gitlabPostTimeout)
	defer cancel()
	err = s.poster.PostReview(postCtx, ports.SCMReviewPost{
		PR:        ref,
		TargetSHA: run.TargetSHA,
		Summary:   gitlabReviewSummary(review.Verdict, review.Body),
		Comments:  review.Comments,
	})
	if err != nil {
		logger.WarnContext(ctx, "review: posting GitLab review failed", "session", run.SessionID, "run", run.ID, "comments", len(review.Comments), "err", err)
		return false
	}
	return true
}

// gitlabReviewRef resolves the run's merge request, preferring the PR row AO
// tracks for the worker and falling back to the MR URL itself.
func (s *Service) gitlabReviewRef(ctx context.Context, run domain.ReviewRun) (ports.SCMPRRef, error) {
	prs, err := s.store.ListPRsBySession(ctx, run.SessionID)
	if err != nil {
		return ports.SCMPRRef{}, err
	}
	for _, pr := range prs {
		if pr.URL != run.PRURL && pr.HTMLURL != run.PRURL {
			continue
		}
		if pr.Provider == "" {
			pr.Provider = "gitlab"
		}
		if pr.Repo == "" || pr.Host == "" {
			break
		}
		return reviewRequestRef(pr)
	}
	return gitlabRefFromURL(run.PRURL)
}

// gitlabRefFromURL parses https://host/group/sub/repo/-/merge_requests/7.
func gitlabRefFromURL(raw string) (ports.SCMPRRef, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ports.SCMPRRef{}, fmt.Errorf("invalid merge request URL")
	}
	project, rest, ok := strings.Cut(strings.Trim(u.Path, "/"), "/-/merge_requests/")
	if !ok {
		return ports.SCMPRRef{}, fmt.Errorf("not a merge request URL")
	}
	number, err := strconv.Atoi(strings.SplitN(rest, "/", 2)[0])
	if err != nil || number <= 0 {
		return ports.SCMPRRef{}, fmt.Errorf("invalid merge request number")
	}
	owner, name, ok := strings.Cut(project, "/")
	if !ok || owner == "" || name == "" {
		return ports.SCMPRRef{}, fmt.Errorf("invalid merge request project")
	}
	return ports.SCMPRRef{
		Repo:   ports.SCMRepo{Provider: "gitlab", Host: u.Host, Owner: owner, Name: name, Repo: project},
		Number: number,
		URL:    raw,
	}, nil
}

// gitlabReviewSummary heads the reviewer's summary with the verdict, since a
// GitLab note has no review state of its own.
func gitlabReviewSummary(verdict domain.ReviewVerdict, body string) string {
	head := "**AO review: approved**"
	if verdict == domain.VerdictChangesRequested {
		head = "**AO review: changes requested**"
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return head
	}
	return head + "\n\n" + body
}
