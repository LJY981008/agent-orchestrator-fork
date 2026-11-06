package daemon

// LADO fork: LADO's stored host tokens join AO's GitHub and GitLab token
// chains after the AO_GITHUB_TOKEN and AO_GITLAB_TOKEN overrides
// (docs/LJY-PRD/TDD.md §1.2 and §5.1), so AO's PR observer and tracker use a
// token stored by connecting a repository too. The GitLab host hub below
// gives AO's GitLab adapters a self-managed host saved or deleted while the
// daemon runs (issue #19).

import (
	"context"
	"log/slog"
	"sync"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	trackergithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/github"
	trackergitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/gitlab"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/githubpat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoconnect"
)

// ladoStoredTokens is LADO's token store. installLadoCredentials sets it once,
// before any token chain is built; nil leaves the chains as upstream builds
// them.
var ladoStoredTokens *ladoconnect.Credentials

// ladoGitLabHub keeps AO's GitLab SCM providers and trackers in step with the
// stored self-managed hosts. installLadoCredentials sets it with the store;
// nil builds the adapters from the config they are given, as upstream does.
var ladoGitLabHub *ladoGitLabHosts

// installLadoCredentials opens LADO's token store and the GitLab host hub.
// AO's GitLab adapters built from here on take the stored self-managed hosts
// from the hub, and a token saved or deleted later reaches them through
// ladoGitLabHosts.Reload, without a daemon restart (issue #19). cfg.GitLab
// stays the environment's config (AO_GITLAB_HOSTS and AO_GITLAB_HOST_TOKENS).
func installLadoCredentials(cfg config.Config, log *slog.Logger) {
	ladoStoredTokens = ladoconnect.NewCredentials(githubpat.New(cfg.DataDir), cfg.StateDir)
	ladoGitLabHub = newLadoGitLabHosts(cfg.GitLab, ladoStoredTokens, log)
}

// ladoGitLabHosts is the daemon's GitLab host hub: the environment's GitLab
// config, LADO's token store, and the GitLab adapters built from both.
type ladoGitLabHosts struct {
	// mu serializes building and registering an adapter with Reload, so an
	// adapter never misses a reload and an older snapshot is never applied
	// after a newer one.
	mu        sync.Mutex
	base      config.GitLabConfig
	creds     *ladoconnect.Credentials
	log       *slog.Logger
	providers []*scmgitlab.Provider
	trackers  []*trackergitlab.Tracker
}

func newLadoGitLabHosts(base config.GitLabConfig, creds *ladoconnect.Credentials, log *slog.Logger) *ladoGitLabHosts {
	if log == nil {
		log = slog.Default()
	}
	return &ladoGitLabHosts{base: base, creds: creds, log: log}
}

// ladoGitLabHostSet is the allowlist and host tokens the hub gives AO's
// GitLab adapters.
type ladoGitLabHostSet struct {
	allowed []string
	tokens  map[string]scmgitlab.TokenSource
}

// snapshot builds the adapters' hosts. The environment's hosts stay allowed
// and its host tokens are static. A stored self-managed host is allowed only
// when its token can be read now; its token source reads the store on every
// call and fails with ErrNoToken once the token is gone, so a deleted host
// never falls back to the default (gitlab.com) token. A host token from
// AO_GITLAB_HOST_TOKENS wins over the stored one. A store whose host list
// cannot be read is an error.
func (h *ladoGitLabHosts) snapshot(ctx context.Context) (ladoGitLabHostSet, error) {
	set := h.baseHosts()
	if h.creds == nil {
		return set, nil
	}
	hosts, err := h.creds.GitLabHosts(ctx)
	if err != nil {
		return ladoGitLabHostSet{}, err
	}
	for _, host := range hosts {
		if _, ok, err := h.creds.Token(ctx, host); err != nil || !ok {
			continue
		}
		if !ladoHasHost(set.allowed, host) {
			set.allowed = append(set.allowed, host)
		}
		if !ladoHasHostToken(h.base.HostTokens, host) {
			set.tokens[host] = ladoGitLabToken{host: host}
		}
	}
	return set, nil
}

// baseHosts is the environment's hosts, with static host tokens.
func (h *ladoGitLabHosts) baseHosts() ladoGitLabHostSet {
	set := ladoGitLabHostSet{
		allowed: append([]string(nil), h.base.AllowedHosts...),
		tokens:  make(map[string]scmgitlab.TokenSource, len(h.base.HostTokens)),
	}
	for host, token := range h.base.HostTokens {
		set.tokens[host] = scmgitlab.StaticTokenSource(token)
	}
	return set
}

// initialHosts is the snapshot a new adapter starts with. The caller holds
// h.mu. A store that cannot be read gives the environment's hosts only; the
// next Reload adds the stored ones.
func (h *ladoGitLabHosts) initialHosts(ctx context.Context) ladoGitLabHostSet {
	set, err := h.snapshot(ctx)
	if err != nil {
		h.log.Warn("LADO could not read its stored GitLab hosts", "err", err)
		return h.baseHosts()
	}
	return set
}

// newSCMProvider builds a GitLab SCM provider with the hub's hosts and
// registers it for Reload.
func (h *ladoGitLabHosts) newSCMProvider(opts scmgitlab.ProviderOptions) (*scmgitlab.Provider, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.initialHosts(context.Background())
	opts.AllowedHosts, opts.HostTokens = set.allowed, set.tokens
	provider, err := scmgitlab.NewProvider(opts)
	if err != nil {
		return nil, err
	}
	h.providers = append(h.providers, provider)
	return provider, nil
}

// newTracker builds a GitLab tracker with the hub's hosts and registers it
// for Reload.
func (h *ladoGitLabHosts) newTracker(opts trackergitlab.Options) (*trackergitlab.Tracker, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.initialHosts(context.Background())
	opts.AllowedHosts, opts.HostTokens = set.allowed, set.tokens
	tracker, err := trackergitlab.New(opts)
	if err != nil {
		return nil, err
	}
	h.trackers = append(h.trackers, tracker)
	return tracker, nil
}

// hostTokenCount is how many self-managed hosts have their own token now.
func (h *ladoGitLabHosts) hostTokenCount(ctx context.Context) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.initialHosts(ctx).tokens)
}

// Reload gives every registered adapter the hosts of a new snapshot, as one
// step under the hub's lock. ladoconnect calls it after a token was stored
// or deleted. A store that cannot be read keeps the adapters' current hosts.
func (h *ladoGitLabHosts) Reload(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, err := h.snapshot(ctx)
	if err != nil {
		h.log.Warn("LADO could not read its stored GitLab hosts; AO's GitLab hosts stay as they are", "err", err)
		return
	}
	for _, provider := range h.providers {
		provider.SetHosts(set.allowed, set.tokens)
	}
	for _, tracker := range h.trackers {
		tracker.SetHosts(set.allowed, set.tokens)
	}
}

// ladoGitLabBase is the GitLab config from the environment only: the hub's
// base, or cfg.GitLab without a hub.
func ladoGitLabBase(cfg config.Config) config.GitLabConfig {
	if hub := ladoGitLabHub; hub != nil {
		return hub.base
	}
	return cfg.GitLab
}

func ladoHasHost(hosts []string, host string) bool {
	for _, h := range hosts {
		if scmgitlab.NormalizeHost(h) == scmgitlab.NormalizeHost(host) {
			return true
		}
	}
	return false
}

func ladoHasHostToken(tokens map[string]string, host string) bool {
	for h := range tokens {
		if scmgitlab.NormalizeHost(h) == scmgitlab.NormalizeHost(host) {
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
