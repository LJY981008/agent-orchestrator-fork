package daemon

import (
	"context"
	"errors"
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
		case "POST /repos/acme/demo/statuses/" + sha:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	clients := newLadoRepoClients(config.GitLabConfig{})
	clients.githubBaseURL = srv.URL
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
	defer mu.Unlock()
	if !slices.Equal(got, want) {
		t.Fatalf("requests = %q, want %q", got, want)
	}

	gitlabRepo := domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/project"}
	gitlab, err := clients.ForRepo(ctx, gitlabRepo, "tok-entered")
	if err != nil {
		t.Fatalf("ForRepo for GitLab: %v", err)
	}
	if _, err := gitlab.BranchRules(ctx, gitlabRepo, "main"); !errors.Is(err, ports.ErrLadoUnsupported) {
		t.Fatalf("GitLab BranchRules = %v, want ErrLadoUnsupported", err)
	}
	if err := gitlab.PostCommitStatus(ctx, gitlabRepo, sha, status); !errors.Is(err, ports.ErrLadoUnsupported) {
		t.Fatalf("GitLab PostCommitStatus = %v, want ErrLadoUnsupported", err)
	}
}
