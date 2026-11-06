package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLadoRepoClientsPassBranchRulesAndStatus(t *testing.T) {
	ctx := context.Background()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/acme/demo/rules/branches/main":
			_, _ = w.Write([]byte(`[{"type":"deletion"}]`))
		case "POST /repos/acme/demo/statuses/" + sha, "POST /projects/group/project/statuses/" + sha:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case "GET /projects/group/project":
			_, _ = w.Write([]byte(`{"only_allow_merge_if_pipeline_succeeds":true,"permissions":{"project_access":{"access_level":30}}}`))
		case "GET /personal_access_tokens/self":
			_, _ = w.Write([]byte(`{"scopes":["api"]}`))
		case "GET /projects/group/project/protected_branches":
			_, _ = w.Write([]byte(`[{"name":"main","push_access_levels":[{"access_level":0}],"allow_force_push":false}]`))
		case "GET /projects/group/project/merge_requests/7":
			_, _ = w.Write([]byte(`{"iid":7,"head_pipeline":{"id":77,"sha":"` + sha + `"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	clients := newLadoRepoClients(config.GitLabConfig{})
	clients.githubBaseURL = srv.URL
	clients.gitlabBaseURL = srv.URL
	status := ports.LadoCommitStatus{State: "success", Context: domain.LadoIntegrationStatusContext, Description: "Approved in LADO"}

	repo := domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}
	client, err := clients.ForRepo(ctx, repo, "tok-entered")
	if err != nil {
		t.Fatalf("ForRepo: %v", err)
	}
	rules, err := client.BranchRules(ctx, repo, "main")
	if err != nil || len(rules.Rules) != 1 || rules.Rules[0].Type != ports.LadoBranchRuleDeletion {
		t.Fatalf("BranchRules = %+v, %v; want the deletion rule", rules, err)
	}
	if err := client.PostCommitStatus(ctx, repo, sha, status); err != nil {
		t.Fatalf("PostCommitStatus: %v", err)
	}
	want := []string{
		"GET /repos/acme/demo/rules/branches/main Bearer tok-entered",
		"POST /repos/acme/demo/statuses/" + sha + " Bearer tok-entered",
	}
	mu.Lock()
	if !slices.Equal(got, want) {
		mu.Unlock()
		t.Fatalf("requests = %q, want %q", got, want)
	}
	mu.Unlock()

	gitlabRepo := domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/project"}
	gitlab, err := clients.ForRepo(ctx, gitlabRepo, "tok-entered")
	if err != nil {
		t.Fatalf("ForRepo for GitLab: %v", err)
	}
	gitlabRules, err := gitlab.BranchRules(ctx, gitlabRepo, "main")
	if err != nil || gitlabRules.GitLab == nil || !gitlabRules.GitLab.Protected {
		t.Fatalf("GitLab BranchRules = %+v, %v; want main protected", gitlabRules, err)
	}
	status.MergeRequest = 7
	if err := gitlab.PostCommitStatus(ctx, gitlabRepo, sha, status); err != nil {
		t.Fatalf("GitLab PostCommitStatus: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if last := got[len(got)-1]; last != "POST /projects/group/project/statuses/"+sha+" Bearer tok-entered" {
		t.Fatalf("last request = %q, want the GitLab status with the entered token", last)
	}
}
