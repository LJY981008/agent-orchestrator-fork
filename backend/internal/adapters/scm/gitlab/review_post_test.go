package gitlab

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	reviewPostMRPath = "/api/v4/projects/myorg%2Fsub%2Fmyrepo/merge_requests/42"
	reviewedSHA      = "headsha2"
)

func validReviewPost() ports.SCMReviewPost {
	return ports.SCMReviewPost{
		PR: ports.SCMPRRef{
			Repo:   ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "sub/myrepo", Repo: "myorg/sub/myrepo"},
			Number: 42,
			URL:    "https://gitlab.com/myorg/sub/myrepo/-/merge_requests/42",
		},
		TargetSHA: reviewedSHA,
		Summary:   "AO review: changes requested",
		Comments: []ports.SCMReviewComment{
			{Path: "pkg/a.go", Line: 12, Body: "handle the error"},
		},
	}
}

type postedDiscussion struct {
	Body     string              `json:"body"`
	Position *discussionPosition `json:"position"`
}

type reviewPostServer struct {
	mu          sync.Mutex
	versions    []map[string]any
	rejectPos   bool
	notes       []string
	discussions []postedDiscussion
}

func (s *reviewPostServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == reviewPostMRPath+"/versions":
			_ = json.NewEncoder(w).Encode(s.versions)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == reviewPostMRPath+"/notes":
			var body noteRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode note: %v", err)
			}
			s.notes = append(s.notes, body.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
		case r.Method == http.MethodPost && r.URL.EscapedPath() == reviewPostMRPath+"/discussions":
			var body postedDiscussion
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode discussion: %v", err)
			}
			s.discussions = append(s.discussions, body)
			if body.Position != nil && s.rejectPos {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"400 Bad request - Note {:line_code=>[\"can't be blank\"]}"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"d1"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestPostReview_AnchorsCommentsOnReviewedVersion(t *testing.T) {
	srv := &reviewPostServer{versions: []map[string]any{
		{"id": 3, "head_commit_sha": "headsha3", "base_commit_sha": "base3", "start_commit_sha": "start3"},
		{"id": 2, "head_commit_sha": reviewedSHA, "base_commit_sha": "base2", "start_commit_sha": "start2"},
	}}
	_, p := testServer(t, srv.handler(t))

	if err := p.PostReview(ctx(), validReviewPost()); err != nil {
		t.Fatalf("PostReview: %v", err)
	}
	if len(srv.notes) != 1 || srv.notes[0] != "AO review: changes requested" {
		t.Fatalf("notes = %q, want the summary once", srv.notes)
	}
	if len(srv.discussions) != 1 {
		t.Fatalf("discussions = %+v, want one", srv.discussions)
	}
	got := srv.discussions[0]
	if got.Body != "handle the error" || got.Position == nil {
		t.Fatalf("discussion = %+v, want positioned comment body", got)
	}
	want := discussionPosition{PositionType: "text", BaseSHA: "base2", StartSHA: "start2", HeadSHA: reviewedSHA, OldPath: "pkg/a.go", NewPath: "pkg/a.go", NewLine: 12}
	if *got.Position != want {
		t.Fatalf("position = %+v, want %+v", *got.Position, want)
	}
}

func TestPostReview_PostsWithoutPositionWhenNoVersionMatches(t *testing.T) {
	srv := &reviewPostServer{versions: []map[string]any{
		{"id": 3, "head_commit_sha": "headsha3", "base_commit_sha": "base3", "start_commit_sha": "start3"},
	}}
	_, p := testServer(t, srv.handler(t))

	if err := p.PostReview(ctx(), validReviewPost()); err != nil {
		t.Fatalf("PostReview: %v", err)
	}
	if len(srv.discussions) != 1 {
		t.Fatalf("discussions = %+v, want one", srv.discussions)
	}
	got := srv.discussions[0]
	if got.Position != nil {
		t.Fatalf("position = %+v, want none when the reviewed commit is not a version", got.Position)
	}
	if !strings.HasPrefix(got.Body, "`pkg/a.go:12`") || !strings.Contains(got.Body, "handle the error") {
		t.Fatalf("body = %q, want path:line prefix and the comment", got.Body)
	}
}

func TestPostReview_RepostsWithoutPositionWhenGitLabRejectsIt(t *testing.T) {
	srv := &reviewPostServer{
		versions: []map[string]any{
			{"id": 2, "head_commit_sha": reviewedSHA, "base_commit_sha": "base2", "start_commit_sha": "start2"},
		},
		rejectPos: true,
	}
	_, p := testServer(t, srv.handler(t))

	if err := p.PostReview(ctx(), validReviewPost()); err != nil {
		t.Fatalf("PostReview: %v", err)
	}
	if len(srv.discussions) != 2 {
		t.Fatalf("discussions = %+v, want a positioned try and an unpositioned repost", srv.discussions)
	}
	if srv.discussions[0].Position == nil {
		t.Fatalf("first try = %+v, want a position", srv.discussions[0])
	}
	repost := srv.discussions[1]
	if repost.Position != nil || !strings.HasPrefix(repost.Body, "`pkg/a.go:12`") {
		t.Fatalf("repost = %+v, want unpositioned body with path:line", repost)
	}
}

func TestPostReview_RejectsInvalidReference(t *testing.T) {
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
	}))
	post := validReviewPost()
	post.PR.Number = 0
	if err := p.PostReview(ctx(), post); err == nil {
		t.Fatal("PostReview accepted an invalid merge request reference")
	}
}

func TestUnpositionedBody(t *testing.T) {
	for _, tc := range []struct {
		path string
		line int
		want string
	}{
		{"", 0, "x"},
		{"a.go", 0, "`a.go`\n\nx"},
		{"a.go", 3, "`a.go:3`\n\nx"},
	} {
		if got := unpositionedBody(tc.path, tc.line, "x"); got != tc.want {
			t.Fatalf("unpositionedBody(%q,%d) = %q, want %q", tc.path, tc.line, got, tc.want)
		}
	}
}
