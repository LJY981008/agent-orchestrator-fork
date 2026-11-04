package daemon

import (
	"context"
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// TestLadoStoredGitLabTokenServesSSHPortOrigin pins the daemon wiring for a
// self-managed GitLab host connected only through LADO: no AO_GITLAB_TOKEN,
// no glab, just the stored host token. The SCM provider built from that
// config must resolve an ssh:// origin on a non-default SSH port to the stored
// web host and report usable credentials, so the observer, claims and AO
// reviews reach the merge request.
func TestLadoStoredGitLabTokenServesSSHPortOrigin(t *testing.T) {
	ctx := context.Background()
	clearProviderTokenEnv(t)
	t.Setenv("PATH", t.TempDir()) // no glab on PATH
	creds, _ := newTestLadoCredentials(t)
	useLadoStoredTokens(t, creds)
	logger := slog.New(slog.DiscardHandler)

	empty, err := newGitLabSCMProvider(ladoGitLabConfig(ctx, config.GitLabConfig{}, creds, logger), logger)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := empty.SCMCredentialsAvailable(ctx); err != nil || ok {
		t.Fatalf("credentials before a stored token = %v, %v; want none", ok, err)
	}
	if _, ok := empty.ParseRepository("ssh://git@host:30022/a/b/c.git"); ok {
		t.Fatal("ssh origin parsed before the host was connected")
	}

	if _, err := creds.Store(ctx, "host", "glpat-stored"); err != nil {
		t.Fatal(err)
	}
	cfg := ladoGitLabConfig(ctx, config.GitLabConfig{}, creds, logger)
	provider, err := newGitLabSCMProvider(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	repo, ok := provider.ParseRepository("ssh://git@host:30022/a/b/c.git")
	if !ok {
		t.Fatalf("ssh origin on the stored host was not parsed (allowed hosts %v)", cfg.AllowedHosts)
	}
	if repo.Provider != "gitlab" || repo.Host != "host" || repo.Repo != "a/b/c" || repo.Owner != "a/b" || repo.Name != "c" {
		t.Fatalf("parsed repo = %+v, want gitlab host a/b/c", repo)
	}
	if ok, err := provider.SCMCredentialsAvailable(ctx); err != nil || !ok {
		t.Fatalf("SCMCredentialsAvailable = %v, %v; want the stored host token", ok, err)
	}

	multi := newMultiSCMProvider(cfg, logger)
	if multi == nil {
		t.Fatal("multi SCM provider missing with a stored GitLab token")
	}
	if got, ok := multi.ParseRepository("ssh://git@host:30022/a/b/c.git"); !ok || got != repo {
		t.Fatalf("multi ParseRepository = %+v, %v; want %+v", got, ok, repo)
	}
}
