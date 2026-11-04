package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const gitlabMRURL = "https://gitlab.example.com/a/b/c/-/merge_requests/7"

type fakeReviewPoster struct {
	posts []ports.SCMReviewPost
	err   error
}

func (f *fakeReviewPoster) PostReview(_ context.Context, post ports.SCMReviewPost) error {
	f.posts = append(f.posts, post)
	return f.err
}

func gitlabRunStore() *fakeStore {
	return &fakeStore{
		ok: true,
		run: domain.ReviewRun{
			ID: "run-1", SessionID: "mer-1", PRURL: gitlabMRURL, TargetSHA: "sha-reviewed",
			Status: domain.ReviewRunRunning, Harness: "claude-code",
		},
		prs: []domain.PullRequest{{
			URL: gitlabMRURL, Number: 7, HeadSHA: "sha-newer",
			Provider: "gitlab", Host: "gitlab.example.com", Repo: "a/b/c",
		}},
	}
}

func TestSubmitPostsGitLabReviewWithCommentsAndReviewedSHA(t *testing.T) {
	store := gitlabRunStore()
	poster := &fakeReviewPoster{}
	sink := &recordingSink{}
	svc := New(nil, store, WithReviewPoster(poster), WithTelemetry(sink))

	comments := []ports.SCMReviewComment{{Path: "pkg/a.go", Line: 12, Body: "handle the error"}}
	runs, err := svc.SubmitMany(context.Background(), "mer-1", []SubmittedReview{{
		RunID: "run-1", Verdict: domain.VerdictChangesRequested, Body: "two problems",
		GithubReviewID: "should-be-dropped", Comments: comments,
	}})
	if err != nil {
		t.Fatalf("SubmitMany: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != domain.ReviewRunComplete {
		t.Fatalf("runs = %+v, want one complete run", runs)
	}
	if runs[0].GithubReviewID != "" || store.run.GithubReviewID != "" {
		t.Fatalf("githubReviewId = %q / %q, want empty for a GitLab run", runs[0].GithubReviewID, store.run.GithubReviewID)
	}
	if len(poster.posts) != 1 {
		t.Fatalf("posts = %+v, want one", poster.posts)
	}
	post := poster.posts[0]
	if post.TargetSHA != "sha-reviewed" {
		t.Fatalf("TargetSHA = %q, want the reviewed commit", post.TargetSHA)
	}
	if len(post.Comments) != 1 || post.Comments[0] != comments[0] {
		t.Fatalf("comments = %+v, want %+v", post.Comments, comments)
	}
	if post.PR.Number != 7 || post.PR.Repo.Provider != "gitlab" || post.PR.Repo.Host != "gitlab.example.com" || post.PR.Repo.Repo != "a/b/c" {
		t.Fatalf("PR ref = %+v", post.PR)
	}
	if !strings.Contains(post.Summary, "changes requested") || !strings.Contains(post.Summary, "two problems") {
		t.Fatalf("summary = %q, want verdict and body", post.Summary)
	}
	got := sink.named("ao.review.submitted")
	if len(got) != 1 || got[0].Payload["posted_to_provider"] != true {
		t.Fatalf("submitted events = %+v, want posted_to_provider=true", got)
	}

	// A retry of the same completed result does not post again.
	if _, err := svc.SubmitMany(context.Background(), "mer-1", []SubmittedReview{{
		RunID: "run-1", Verdict: domain.VerdictChangesRequested, Body: "two problems", Comments: comments,
	}}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(poster.posts) != 1 {
		t.Fatalf("posts after retry = %d, want 1", len(poster.posts))
	}
}

func TestSubmitGitLabPostFailureStillCompletesRun(t *testing.T) {
	store := gitlabRunStore()
	poster := &fakeReviewPoster{err: errors.New("gitlab down")}
	sink := &recordingSink{}
	svc := New(nil, store, WithReviewPoster(poster), WithTelemetry(sink))

	run, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictApproved, "fine", "")
	if err != nil {
		t.Fatalf("Submit must not fail when posting fails: %v", err)
	}
	if run.Status != domain.ReviewRunComplete {
		t.Fatalf("run = %+v, want complete", run)
	}
	if len(poster.posts) != 1 {
		t.Fatalf("posts = %d, want one attempt", len(poster.posts))
	}
	got := sink.named("ao.review.submitted")
	if len(got) != 1 || got[0].Payload["posted_to_provider"] != false {
		t.Fatalf("submitted events = %+v, want posted_to_provider=false", got)
	}
}

func TestSubmitGitHubRunDoesNotCallPoster(t *testing.T) {
	store := &fakeStore{
		ok:  true,
		run: domain.ReviewRun{ID: "run-1", SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
		prs: []domain.PullRequest{{URL: "https://github.com/o/r/pull/1", Number: 1, Provider: "github"}},
	}
	poster := &fakeReviewPoster{}
	svc := New(nil, store, WithReviewPoster(poster))

	run, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictApproved, "fine", "987")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if run.GithubReviewID != "987" {
		t.Fatalf("githubReviewId = %q, want 987", run.GithubReviewID)
	}
	if len(poster.posts) != 0 {
		t.Fatalf("posts = %+v, want none for a GitHub run", poster.posts)
	}
}

func TestSubmitGitLabWithoutPosterIsNotPosted(t *testing.T) {
	store := gitlabRunStore()
	sink := &recordingSink{}
	svc := New(nil, store, WithTelemetry(sink))

	if _, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictApproved, "", ""); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got := sink.named("ao.review.submitted")
	if len(got) != 1 || got[0].Payload["posted_to_provider"] != false {
		t.Fatalf("submitted events = %+v, want posted_to_provider=false", got)
	}
}

func TestGitLabReviewRefFallsBackToMergeRequestURL(t *testing.T) {
	store := gitlabRunStore()
	store.prs = nil
	svc := New(nil, store)

	ref, err := svc.gitlabReviewRef(context.Background(), store.run)
	if err != nil {
		t.Fatalf("gitlabReviewRef: %v", err)
	}
	if ref.Number != 7 || ref.Repo.Host != "gitlab.example.com" || ref.Repo.Owner != "a" || ref.Repo.Name != "b/c" || ref.Repo.Repo != "a/b/c" {
		t.Fatalf("ref = %+v", ref)
	}
	for _, bad := range []string{"https://gitlab.example.com/a/-/merge_requests/x", "https://gitlab.example.com/a/b/pull/1", "not a url"} {
		if _, err := gitlabRefFromURL(bad); err == nil {
			t.Fatalf("gitlabRefFromURL(%q) succeeded", bad)
		}
	}
}
