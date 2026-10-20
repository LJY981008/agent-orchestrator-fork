package daemon

// LADO fork: LADO's clients for a repository's tracker (docs/LJY-PRD/TDD.md
// §5.1). They are AO's tracker adapters built per host, so LADO reaches a host
// connected after startup, including a self-managed GitLab that AO's own
// allowlist only learns on the next daemon start.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	trackergithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/github"
	trackergitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/gitlab"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoconnect"
)

// ladoIssueOps is what each tracker adapter's LadoIssues offers.
type ladoIssueOps interface {
	CreateIssue(ctx context.Context, repo domain.TrackerRepo, title, body string) (domain.Issue, error)
	Login(ctx context.Context) (string, error)
	CheckAccess(ctx context.Context, repo domain.TrackerRepo) (ports.LadoRepoAccess, error)
	IssueComments(ctx context.Context, id domain.TrackerID) ([]domain.LadoIssueComment, error)
}

// ladoRepoClient joins an adapter's tracker reads and LADO operations.
type ladoRepoClient struct {
	ports.Tracker
	issues ladoIssueOps
}

func (c ladoRepoClient) CreateIssue(ctx context.Context, repo domain.TrackerRepo, title, body string) (domain.Issue, error) {
	return c.issues.CreateIssue(ctx, repo, title, body)
}

func (c ladoRepoClient) Login(ctx context.Context) (string, error) {
	return c.issues.Login(ctx)
}

func (c ladoRepoClient) CheckAccess(ctx context.Context, repo domain.TrackerRepo) (ports.LadoRepoAccess, error) {
	return c.issues.CheckAccess(ctx, repo)
}

func (c ladoRepoClient) IssueComments(ctx context.Context, id domain.TrackerID) ([]domain.LadoIssueComment, error) {
	return c.issues.IssueComments(ctx, id)
}

// ladoRepoClients builds repository clients. A client on a host's token
// chain is kept per host, so the adapters' caches survive between calls; a
// client for an entered token is built for that one check.
type ladoRepoClients struct {
	// gitlabHostTokens are AO_GITLAB_HOST_TOKENS, which win over stored
	// tokens.
	gitlabHostTokens map[string]string
	httpClient       *http.Client
	// githubBaseURL and gitlabBaseURL point the clients elsewhere in tests.
	githubBaseURL string
	gitlabBaseURL string

	mu     sync.Mutex
	cached map[string]ports.LadoRepoClient
}

func newLadoRepoClients(gitlab config.GitLabConfig) *ladoRepoClients {
	return &ladoRepoClients{gitlabHostTokens: gitlab.HostTokens, cached: map[string]ports.LadoRepoClient{}}
}

// ForRepo returns a client for repo's host.
func (c *ladoRepoClients) ForRepo(_ context.Context, repo domain.TrackerRepo, token string) (ports.LadoRepoClient, error) {
	host, err := ladoClientHost(repo)
	if err != nil {
		return nil, err
	}
	token = strings.TrimSpace(token)
	if token != "" {
		return c.build(repo.Provider, host, token)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.cached[host]; ok {
		return client, nil
	}
	client, err := c.build(repo.Provider, host, "")
	if err != nil {
		return nil, err
	}
	c.cached[host] = client
	return client, nil
}

// ladoClientHost is the host a repository's tokens belong to.
func ladoClientHost(repo domain.TrackerRepo) (string, error) {
	switch repo.Provider {
	case domain.TrackerProviderGitHub:
		return ladoconnect.GitHubHost, nil
	case domain.TrackerProviderGitLab:
		if host := scmgitlab.NormalizeHost(repo.Host); !scmgitlab.IsGitLabDotCom(host) {
			return host, nil
		}
		return "gitlab.com", nil
	}
	return "", fmt.Errorf("LADO does not support the %q tracker", repo.Provider)
}

func (c *ladoRepoClients) build(provider domain.TrackerProvider, host, token string) (ports.LadoRepoClient, error) {
	if provider == domain.TrackerProviderGitHub {
		var tokens trackergithub.TokenSource = &ghTokenSource{}
		if token != "" {
			tokens = trackergithub.StaticTokenSource(token)
		}
		tracker, err := trackergithub.New(trackergithub.Options{Token: tokens, HTTPClient: c.httpClient, BaseURL: c.githubBaseURL})
		if err != nil {
			return nil, err
		}
		return ladoRepoClient{Tracker: tracker, issues: tracker.LadoIssues()}, nil
	}
	tokens := c.gitlabTokens(host, token)
	opts := trackergitlab.Options{Token: tokens, HTTPClient: c.httpClient, BaseURL: c.gitlabBaseURL}
	if host != "gitlab.com" {
		// The host is both the default and the only allowed host, so the
		// token check and every issue call go to it.
		if opts.BaseURL == "" {
			opts.BaseURL = "https://" + host + "/api/v4"
		}
		opts.AllowedHosts = []string{host}
		opts.HostTokens = map[string]scmgitlab.TokenSource{host: tokens}
	}
	tracker, err := trackergitlab.New(opts)
	if err != nil {
		return nil, err
	}
	return ladoRepoClient{Tracker: tracker, issues: tracker.LadoIssues()}, nil
}

// gitlabTokens is a GitLab host's token source: an entered token, or the
// host's chain.
func (c *ladoRepoClients) gitlabTokens(host, token string) scmgitlab.TokenSource {
	switch {
	case token != "":
		return scmgitlab.StaticTokenSource(token)
	case host == "gitlab.com":
		return ladoGitLabDefaultTokens()
	}
	var chain scmgitlab.FallbackTokenSource
	for h, envToken := range c.gitlabHostTokens {
		if strings.EqualFold(strings.TrimSpace(h), host) && strings.TrimSpace(envToken) != "" {
			chain = append(chain, scmgitlab.StaticTokenSource(envToken))
		}
	}
	return append(chain, ladoGitLabToken{host: host})
}
