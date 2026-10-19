package daemon

// LADO fork: LADO's stored host tokens join AO's GitHub and GitLab token
// chains after the AO_GITHUB_TOKEN and AO_GITLAB_TOKEN overrides
// (docs/LJY-PRD/TDD.md §1.2 and §5.1), so AO's PR observer and tracker use a
// token stored by connecting a repository too.

import (
	"context"
	"log/slog"
	"strings"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	trackergithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/github"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/githubpat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoconnect"
)

// ladoStoredTokens is LADO's token store. installLadoCredentials sets it once,
// before any token chain is built; nil leaves the chains as upstream builds
// them.
var ladoStoredTokens *ladoconnect.Credentials

// installLadoCredentials opens LADO's token store and returns AO's GitLab
// config with the stored self-managed hosts added. AO reads its GitLab config
// once at startup, so a host connected later reaches AO's own observers on
// the next daemon start; LADO's own requests use it at once.
func installLadoCredentials(cfg config.Config, log *slog.Logger) config.GitLabConfig {
	ladoStoredTokens = ladoconnect.NewCredentials(githubpat.New(cfg.DataDir), cfg.StateDir)
	return ladoGitLabConfig(context.Background(), cfg.GitLab, ladoStoredTokens, log)
}

// ladoGitLabConfig adds the stored self-managed GitLab hosts to AO's
// allowlist, with their tokens. A host token from AO_GITLAB_HOST_TOKENS wins.
func ladoGitLabConfig(ctx context.Context, base config.GitLabConfig, creds *ladoconnect.Credentials, log *slog.Logger) config.GitLabConfig {
	hosts, err := creds.GitLabHosts(ctx)
	if err != nil {
		log.Warn("LADO could not read its stored GitLab hosts", "err", err)
		return base
	}
	merged := config.GitLabConfig{
		AllowedHosts: append([]string(nil), base.AllowedHosts...),
		HostTokens:   make(map[string]string, len(base.HostTokens)+len(hosts)),
	}
	for host, token := range base.HostTokens {
		merged.HostTokens[host] = token
	}
	for _, host := range hosts {
		if !ladoHasHost(merged.AllowedHosts, host) {
			merged.AllowedHosts = append(merged.AllowedHosts, host)
		}
		if ladoHasHostToken(merged.HostTokens, host) {
			continue
		}
		if token, ok, err := creds.Token(ctx, host); err == nil && ok {
			merged.HostTokens[host] = token
		}
	}
	return merged
}

func ladoHasHost(hosts []string, host string) bool {
	for _, h := range hosts {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}
	return false
}

func ladoHasHostToken(tokens map[string]string, host string) bool {
	for h := range tokens {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}
	return false
}

// ladoStoredToken returns the token LADO stores for host. A store that cannot
// be read counts as no token, so the chain moves on to the provider's CLI.
func ladoStoredToken(ctx context.Context, host string) (string, bool) {
	if ladoStoredTokens == nil {
		return "", false
	}
	token, ok, err := ladoStoredTokens.Token(ctx, host)
	if err != nil || !ok {
		return "", false
	}
	return token, true
}

// ladoSCMGitHubToken is the stored github.com token in the SCM chain.
type ladoSCMGitHubToken struct{}

func (ladoSCMGitHubToken) Token(ctx context.Context) (string, error) {
	if token, ok := ladoStoredToken(ctx, ladoconnect.GitHubHost); ok {
		return token, nil
	}
	return "", scmgithub.ErrNoToken
}

// ladoTrackerGitHubToken is the stored github.com token in the tracker chain.
type ladoTrackerGitHubToken struct{}

func (ladoTrackerGitHubToken) Token(ctx context.Context) (string, error) {
	if token, ok := ladoStoredToken(ctx, ladoconnect.GitHubHost); ok {
		return token, nil
	}
	return "", trackergithub.ErrNoToken
}

// ladoGitLabToken is a stored GitLab token in the SCM and tracker chains.
type ladoGitLabToken struct {
	host string
}

func (s ladoGitLabToken) Token(ctx context.Context) (string, error) {
	if token, ok := ladoStoredToken(ctx, s.host); ok {
		return token, nil
	}
	return "", scmgitlab.ErrNoToken
}

// ladoGitLabDefaultTokens is the GitLab tracker's default chain,
// trackergitlab.DefaultTokenSource, with the stored gitlab.com token after
// the env overrides.
func ladoGitLabDefaultTokens() scmgitlab.TokenSource {
	return scmgitlab.FallbackTokenSource{
		&scmgitlab.EnvTokenSource{EnvVars: []string{"AO_GITLAB_TOKEN"}},
		ladoGitLabToken{host: "gitlab.com"},
		&scmgitlab.GLabTokenSource{},
	}
}
