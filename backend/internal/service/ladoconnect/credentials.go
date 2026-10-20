// Package ladoconnect connects a project to its repository for LADO: one
// access token per host, an SSH check with an optional key file, and a link to
// the registered project or a clone (docs/LJY-PRD/TDD.md §5.1 and
// docs/LJY-PRD/adr/0006-issues-as-work-items.md).
package ladoconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// GitHubHost is the only GitHub host LADO supports: AO's GitHub API base is
// fixed to api.github.com.
const GitHubHost = "github.com"

// gitLabDotCom is the default GitLab host.
const gitLabDotCom = "gitlab.com"

// GitHubTokenStore is AO's own github.com token store (service/githubpat).
type GitHubTokenStore interface {
	StorePAT(ctx context.Context, token string) error
	HasPAT(ctx context.Context) bool
	DeletePAT(ctx context.Context) error
	GetToken(ctx context.Context) (string, error)
}

// Credentials keeps one access token per repository host (docs/LJY-PRD/TDD.md
// §4.5). The github.com token goes into AO's own store, so AO's GitHub
// settings and LADO share it. GitLab tokens go into a LADO file with the same
// protection as AO's: 0600 in a 0700 directory. Tokens are stored unencrypted,
// as AO stores its own.
type Credentials struct {
	github     GitHubTokenStore
	gitlabPath string
	now        func() time.Time
	mu         sync.Mutex
}

// NewCredentials returns the token store under AO's state directory.
func NewCredentials(github GitHubTokenStore, stateDir string) *Credentials {
	return &Credentials{
		github:     github,
		gitlabPath: filepath.Join(stateDir, "lado", "credentials", "gitlab-tokens.json"),
		now:        time.Now,
	}
}

// StoredCredential is a host with a stored token. It never carries the token.
type StoredCredential struct {
	Host     string
	Provider domain.TrackerProvider
}

// gitlabTokenFile is the GitLab token file.
type gitlabTokenFile struct {
	Version int                          `json:"version"`
	Hosts   map[string]gitlabStoredToken `json:"hosts"`
}

type gitlabStoredToken struct {
	Token    string    `json:"token"`
	StoredAt time.Time `json:"storedAt"`
}

// CredentialHost normalizes a repository host and names its provider.
// www.github.com and www.gitlab.com are the same hosts as github.com and
// gitlab.com. A host is a bare name: no scheme, path or port.
func CredentialHost(raw string) (string, domain.TrackerProvider, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	if host == "" || strings.ContainsAny(host, "/:@ \t\r\n?#") {
		return "", "", fmt.Errorf("%q is not a repository host name", raw)
	}
	switch host {
	case GitHubHost, "www.github.com":
		return GitHubHost, domain.TrackerProviderGitHub, nil
	case gitLabDotCom, "www.gitlab.com":
		return gitLabDotCom, domain.TrackerProviderGitLab, nil
	}
	if domain.RepositoryProvider(host) == "github" {
		return "", "", fmt.Errorf("%s: LADO supports GitHub on github.com only", host)
	}
	return host, domain.TrackerProviderGitLab, nil
}

// Token returns the stored token for host, and false when there is none.
func (c *Credentials) Token(ctx context.Context, host string) (string, bool, error) {
	host, provider, err := CredentialHost(host)
	if err != nil {
		return "", false, err
	}
	if provider == domain.TrackerProviderGitHub {
		if !c.github.HasPAT(ctx) {
			return "", false, nil
		}
		token, err := c.github.GetToken(ctx)
		if err != nil {
			return "", false, fmt.Errorf("read the stored github.com token: %w", err)
		}
		return token, token != "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	file, err := c.readGitLab()
	if err != nil {
		return "", false, err
	}
	stored, ok := file.Hosts[host]
	return stored.Token, ok && stored.Token != "", nil
}

// Store saves token for host, replacing any token stored before.
func (c *Credentials) Store(ctx context.Context, host, token string) (StoredCredential, error) {
	host, provider, err := CredentialHost(host)
	if err != nil {
		return StoredCredential{}, err
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return StoredCredential{}, errors.New("the token is empty or contains whitespace")
	}
	stored := StoredCredential{Host: host, Provider: provider}
	if provider == domain.TrackerProviderGitHub {
		if err := c.github.StorePAT(ctx, token); err != nil {
			return StoredCredential{}, fmt.Errorf("store the github.com token: %w", err)
		}
		return stored, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	file, err := c.readGitLab()
	if err != nil {
		return StoredCredential{}, err
	}
	file.Hosts[host] = gitlabStoredToken{Token: token, StoredAt: c.now().UTC()}
	if err := c.writeGitLab(file); err != nil {
		return StoredCredential{}, err
	}
	return stored, nil
}

// Delete removes the token stored for host. Deleting a missing token is not an
// error.
func (c *Credentials) Delete(ctx context.Context, host string) error {
	host, provider, err := CredentialHost(host)
	if err != nil {
		return err
	}
	if provider == domain.TrackerProviderGitHub {
		return c.github.DeletePAT(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	file, err := c.readGitLab()
	if err != nil {
		return err
	}
	if _, ok := file.Hosts[host]; !ok {
		return nil
	}
	delete(file.Hosts, host)
	return c.writeGitLab(file)
}

// List returns the hosts with a stored token, github.com first.
func (c *Credentials) List(ctx context.Context) ([]StoredCredential, error) {
	var listed []StoredCredential
	if c.github.HasPAT(ctx) {
		listed = append(listed, StoredCredential{Host: GitHubHost, Provider: domain.TrackerProviderGitHub})
	}
	c.mu.Lock()
	file, err := c.readGitLab()
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	hosts := make([]string, 0, len(file.Hosts))
	for host := range file.Hosts {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		listed = append(listed, StoredCredential{Host: host, Provider: domain.TrackerProviderGitLab})
	}
	return listed, nil
}

// GitLabHosts returns the self-managed GitLab hosts with a stored token.
func (c *Credentials) GitLabHosts(ctx context.Context) ([]string, error) {
	listed, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	var hosts []string
	for _, credential := range listed {
		if credential.Provider == domain.TrackerProviderGitLab && credential.Host != gitLabDotCom {
			hosts = append(hosts, credential.Host)
		}
	}
	return hosts, nil
}

// readGitLab reads the GitLab token file. A missing file holds no tokens. The
// caller holds c.mu.
func (c *Credentials) readGitLab() (gitlabTokenFile, error) {
	file := gitlabTokenFile{Version: 1, Hosts: map[string]gitlabStoredToken{}}
	data, err := os.ReadFile(c.gitlabPath)
	if errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return gitlabTokenFile{}, fmt.Errorf("read the GitLab token file: %w", err)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return gitlabTokenFile{}, fmt.Errorf("the GitLab token file %s is not valid JSON", c.gitlabPath)
	}
	if file.Hosts == nil {
		file.Hosts = map[string]gitlabStoredToken{}
	}
	return file, nil
}

// writeGitLab replaces the GitLab token file atomically. The caller holds
// c.mu.
func (c *Credentials) writeGitLab(file gitlabTokenFile) error {
	dir := filepath.Dir(c.gitlabPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the LADO credentials directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory needs its execute bit; 0700 keeps it to the user.
		return fmt.Errorf("protect the LADO credentials directory: %w", err)
	}
	data, err := json.MarshalIndent(file, "", "  ") //nolint:gosec // G117: the token file holds tokens by design; it is 0600 in a 0700 directory, like AO's github-pat.json.
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".gitlab-tokens-*.json")
	if err != nil {
		return fmt.Errorf("write the GitLab token file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect the GitLab token file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write the GitLab token file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write the GitLab token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write the GitLab token file: %w", err)
	}
	if err := os.Rename(tmpPath, c.gitlabPath); err != nil {
		return fmt.Errorf("replace the GitLab token file: %w", err)
	}
	return nil
}
