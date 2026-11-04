package multi

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type postingProvider struct {
	fakeProvider
	posts []ports.SCMReviewPost
}

func (p *postingProvider) PostReview(_ context.Context, post ports.SCMReviewPost) error {
	p.posts = append(p.posts, post)
	return nil
}

func reviewPostFor(provider string) ports.SCMReviewPost {
	return ports.SCMReviewPost{
		PR:        ports.SCMPRRef{Repo: ports.SCMRepo{Provider: provider, Owner: "o", Name: "r"}, Number: 1},
		TargetSHA: "sha1",
		Comments:  []ports.SCMReviewComment{{Path: "a.go", Line: 1, Body: "fix"}},
	}
}

func TestPostReview_RoutesGitLabToGitLabProvider(t *testing.T) {
	gl := &postingProvider{fakeProvider: fakeProvider{key: "gitlab"}}
	gh := &postingProvider{fakeProvider: fakeProvider{key: "github"}}
	m := New(NamedProvider{Key: "github", Provider: gh}, NamedProvider{Key: "gitlab", Provider: gl})

	if err := m.PostReview(context.Background(), reviewPostFor("gitlab")); err != nil {
		t.Fatalf("PostReview: %v", err)
	}
	if len(gl.posts) != 1 || gl.posts[0].TargetSHA != "sha1" || len(gl.posts[0].Comments) != 1 {
		t.Fatalf("gitlab posts = %+v, want the review", gl.posts)
	}
	if len(gh.posts) != 0 {
		t.Fatalf("github posts = %+v, want none", gh.posts)
	}
}

func TestPostReview_GitHubIsUnsupported(t *testing.T) {
	gh := &postingProvider{fakeProvider: fakeProvider{key: "github"}}
	m := New(NamedProvider{Key: "github", Provider: gh})

	err := m.PostReview(context.Background(), reviewPostFor("github"))
	if !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("err = %v, want ErrSCMUnsupported", err)
	}
	if len(gh.posts) != 0 {
		t.Fatalf("github posts = %+v, want none", gh.posts)
	}
}

func TestPostReview_UnsupportedWithoutGitLabProvider(t *testing.T) {
	m := New(NamedProvider{Key: "github", Provider: &fakeProvider{key: "github"}})
	if err := m.PostReview(context.Background(), reviewPostFor("gitlab")); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("err = %v, want ErrSCMUnsupported", err)
	}
	var nilProvider *Provider
	if err := nilProvider.PostReview(context.Background(), reviewPostFor("gitlab")); !errors.Is(err, ports.ErrSCMUnsupported) {
		t.Fatalf("nil provider err = %v, want ErrSCMUnsupported", err)
	}
}
