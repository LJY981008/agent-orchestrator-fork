package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A GitLab reviewer hands its required changes to AO in "comments"; the CLI
// must forward them to the daemon unchanged so AO can post them on the MR.
func TestReviewSubmitBatchForwardsGitLabComments(t *testing.T) {
	cfg := setReviewEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, `{"reviews":[{"id":"run-1","verdict":"changes_requested"}]}`)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`{ "reviews": [ { "runId": "run-1", "verdict": "changes_requested", "body": "fix it", "comments": [ { "path": "pkg/a.go", "line": 12, "body": "handle the error" }, { "path": "pkg/b.go", "line": 3, "body": "add a test" } ] } ] }`)
	out, errOut, err := executeCLI(t, deps, "review", "submit", "--session", "mer-1", "--reviews", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "recorded 1 review(s) for mer-1") {
		t.Fatalf("stdout = %q", out)
	}
	var req struct {
		Reviews []struct {
			RunID          string `json:"runId"`
			GithubReviewID string `json:"githubReviewId"`
			Comments       []struct {
				Path string `json:"path"`
				Line int    `json:"line"`
				Body string `json:"body"`
			} `json:"comments"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(req.Reviews) != 1 || req.Reviews[0].RunID != "run-1" || req.Reviews[0].GithubReviewID != "" {
		t.Fatalf("request = %s", capture.body)
	}
	comments := req.Reviews[0].Comments
	if len(comments) != 2 || comments[0].Path != "pkg/a.go" || comments[0].Line != 12 || comments[0].Body != "handle the error" || comments[1].Path != "pkg/b.go" {
		t.Fatalf("comments = %+v, want both forwarded", comments)
	}
	if strings.Contains(capture.body, `"comments":null`) {
		t.Fatalf("request carries a null comments field: %s", capture.body)
	}
}

// A GitHub submit without comments keeps its old wire shape.
func TestReviewSubmitBatchOmitsEmptyComments(t *testing.T) {
	cfg := setReviewEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, `{"reviews":[{"id":"run-1","verdict":"approved"}]}`)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`{"reviews":[{"runId":"run-1","verdict":"approved","githubReviewId":"101"}]}`)
	if _, errOut, err := executeCLI(t, deps, "review", "submit", "mer-1", "--reviews", "-"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if strings.Contains(capture.body, `"comments"`) {
		t.Fatalf("request = %s, want no comments field", capture.body)
	}
}
