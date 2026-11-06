package daemon

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	trackergitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/gitlab"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoconnect"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// logRecords is a slog handler that keeps every record.
type logRecords struct {
	mu      sync.Mutex
	records []slog.Record
}

func (l *logRecords) Enabled(context.Context, slog.Level) bool { return true }

func (l *logRecords) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, r.Clone())
	return nil
}

func (l *logRecords) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logRecords) WithGroup(string) slog.Handler      { return l }

// at returns the messages logged at level or above.
func (l *logRecords) at(level slog.Level) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var messages []string
	for _, r := range l.records {
		if r.Level >= level {
			messages = append(messages, r.Message)
		}
	}
	return messages
}

// lastTracker is the GitLab tracker the hub registered last.
func lastTracker(t *testing.T, hub *ladoGitLabHosts) *trackergitlab.Tracker {
	t.Helper()
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if len(hub.trackers) == 0 {
		t.Fatal("no GitLab tracker registered with the hub")
	}
	return hub.trackers[len(hub.trackers)-1]
}

// TestLadoConnectionReachesAOGitLabAdaptersWithoutRestart pins issue #19: AO's
// GitLab SCM providers and tracker, built before any host token was stored,
// take a host token saved through LADO's connection service at once, and
// drop the host again when the token is deleted.
func TestLadoConnectionReachesAOGitLabAdaptersWithoutRestart(t *testing.T) {
	ctx := context.Background()
	clearProviderTokenEnv(t)
	t.Setenv("PATH", t.TempDir()) // no glab on PATH
	useLadoStoredTokens(t, nil)
	logger := slog.New(slog.DiscardHandler)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}

	work := wireLadoWork(ctx, cfg, sqlitetest.MustOpen(t), &recordingProjectCloner{}, ladoWorkDeps{}, logger)
	hub := ladoGitLabHub
	if hub == nil || work.clients == nil {
		t.Fatalf("wireLadoWork left hub %v, clients %v", hub, work.clients)
	}
	work.clients.gitlabBaseURL = (&authRecorder{}).server(t).URL

	provider, err := newGitLabSCMProvider(cfg.GitLab, logger)
	if err != nil {
		t.Fatal(err)
	}
	multi := newMultiSCMProvider(cfg.GitLab, logger)
	if newMultiTracker(cfg.GitLab, logger) == nil {
		t.Fatal("multi tracker missing")
	}
	tracker := lastTracker(t, hub)
	const origin = "ssh://git@host:30022/a/b/c.git"
	if _, ok := provider.ParseRepository(origin); ok {
		t.Fatal("ssh origin parsed before the host was connected")
	}
	if err := tracker.ConfigForHost("host"); !errors.Is(err, trackergitlab.ErrHostNotAllowed) {
		t.Fatalf("tracker before the host was connected: %v, want ErrHostNotAllowed", err)
	}

	if _, _, err := work.connections.StoreCredential(ctx, "host", "glpat-stored"); err != nil {
		t.Fatalf("StoreCredential: %v", err)
	}
	for name, p := range map[string]interface {
		ParseRepository(string) (ports.SCMRepo, bool)
	}{"gitlab": provider, "multi": multi} {
		repo, ok := p.ParseRepository(origin)
		if !ok || repo.Provider != "gitlab" || repo.Host != "host" || repo.Repo != "a/b/c" {
			t.Fatalf("%s ParseRepository after the token was stored = %+v, %v; want gitlab host a/b/c", name, repo, ok)
		}
	}
	if ok, err := provider.SCMCredentialsAvailable(ctx); err != nil || !ok {
		t.Fatalf("SCMCredentialsAvailable = %v, %v; want the stored host token", ok, err)
	}
	if err := tracker.ConfigForHost("host"); err != nil {
		t.Fatalf("tracker after the token was stored: %v", err)
	}

	if err := work.connections.DeleteCredential(ctx, "host"); err != nil {
		t.Fatalf("DeleteCredential: %v", err)
	}
	if _, ok := provider.ParseRepository(origin); ok {
		t.Fatal("ssh origin still parsed after the token was deleted")
	}
	if ok, err := provider.SCMCredentialsAvailable(ctx); err != nil || ok {
		t.Fatalf("SCMCredentialsAvailable after delete = %v, %v; want none", ok, err)
	}
	if err := tracker.ConfigForHost("host"); !errors.Is(err, trackergitlab.ErrHostNotAllowed) {
		t.Fatalf("tracker after the token was deleted: %v, want ErrHostNotAllowed", err)
	}
}

// TestLadoGitLabHubNeverGivesAStoredHostTheDefaultToken: a stored host's
// token is read on every call, so a token deleted before the hub reloads
// fails with ErrNoToken instead of falling back to AO_GITLAB_TOKEN, and a
// listed host whose token cannot be read is not allowed at all.
func TestLadoGitLabHubNeverGivesAStoredHostTheDefaultToken(t *testing.T) {
	ctx := context.Background()
	clearProviderTokenEnv(t)
	t.Setenv("AO_GITLAB_TOKEN", "default-token")
	stateDir := t.TempDir()
	creds := ladoconnect.NewCredentials(&memGitHubTokens{}, stateDir)
	if _, err := creds.Store(ctx, "gitlab.example.com", "stored-tok"); err != nil {
		t.Fatal(err)
	}
	hub := useLadoGitLabHub(t, config.GitLabConfig{}, creds)
	if _, err := newGitLabTracker(config.GitLabConfig{}); err != nil {
		t.Fatal(err)
	}

	set, err := hub.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source := set.tokens["gitlab.example.com"]
	if source == nil {
		t.Fatalf("snapshot tokens = %v, want the stored host", set.tokens)
	}
	if token, err := source.Token(ctx); err != nil || token != "stored-tok" {
		t.Fatalf("stored host token = %q, %v", token, err)
	}
	if err := creds.Delete(ctx, "gitlab.example.com"); err != nil {
		t.Fatal(err)
	}
	if token, err := source.Token(ctx); !errors.Is(err, scmgitlab.ErrNoToken) {
		t.Fatalf("deleted host token = %q, %v; want ErrNoToken, not the default token", token, err)
	}

	// A host listed in the file with no usable token.
	path := filepath.Join(stateDir, "lado", "credentials", "gitlab-tokens.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"hosts":{"gitlab.empty.com":{"token":""}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if hosts, err := creds.GitLabHosts(ctx); err != nil || len(hosts) != 1 {
		t.Fatalf("GitLabHosts = %v, %v; want the listed host", hosts, err)
	}
	set, err = hub.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.allowed) != 0 || len(set.tokens) != 0 {
		t.Fatalf("snapshot = %+v, want a host without a readable token left out", set)
	}
	hub.Reload(ctx)
	if err := lastTracker(t, hub).ConfigForHost("gitlab.empty.com"); !errors.Is(err, trackergitlab.ErrHostNotAllowed) {
		t.Fatalf("tracker allowed a host without a readable token: %v", err)
	}
}

// TestLadoGitLabHubKeepsHostsWhenTheStoreCannotBeRead: a reload that cannot
// read the stored hosts leaves the adapters' hosts as they are.
func TestLadoGitLabHubKeepsHostsWhenTheStoreCannotBeRead(t *testing.T) {
	ctx := context.Background()
	clearProviderTokenEnv(t)
	t.Setenv("PATH", t.TempDir())
	stateDir := t.TempDir()
	creds := ladoconnect.NewCredentials(&memGitHubTokens{}, stateDir)
	if _, err := creds.Store(ctx, "host", "stored-tok"); err != nil {
		t.Fatal(err)
	}
	hub := useLadoGitLabHub(t, config.GitLabConfig{}, creds)
	provider, err := newGitLabSCMProvider(config.GitLabConfig{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.ParseRepository("git@host:g/p.git"); !ok {
		t.Fatal("stored host not allowed at build")
	}

	path := filepath.Join(stateDir, "lado", "credentials", "gitlab-tokens.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.snapshot(ctx); err == nil {
		t.Fatal("snapshot of an unreadable store succeeded")
	}
	hub.Reload(ctx)
	if _, ok := provider.ParseRepository("git@host:g/p.git"); !ok {
		t.Fatal("a failed reload dropped the host")
	}
}

// TestLadoRepoClientsUseAReplacedStoredToken pins the startup bug issue #19
// fixed: LADO's own clients got the stored tokens of startup as static
// tokens, so a replaced token was not used until a restart. A token from
// AO_GITLAB_HOST_TOKENS still wins over the stored one.
func TestLadoRepoClientsUseAReplacedStoredToken(t *testing.T) {
	ctx := context.Background()
	clearProviderTokenEnv(t)
	useLadoStoredTokens(t, nil)
	cfg := config.Config{
		DataDir:  t.TempDir(),
		StateDir: t.TempDir(),
		GitLab:   config.GitLabConfig{HostTokens: map[string]string{"gitlab.env.com": "env-tok"}},
	}
	seed := ladoconnect.NewCredentials(&memGitHubTokens{}, cfg.StateDir)
	for host, token := range map[string]string{"gitlab.example.com": "old-tok", "gitlab.env.com": "stored-env"} {
		if _, err := seed.Store(ctx, host, token); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.DiscardHandler)

	// As the daemon does: the token store first, then LADO's work.
	installLadoCredentials(cfg, logger)
	work := wireLadoWork(ctx, cfg, sqlitetest.MustOpen(t), &recordingProjectCloner{}, ladoWorkDeps{}, logger)
	recorder := &authRecorder{}
	work.clients.gitlabBaseURL = recorder.server(t).URL

	if _, err := ladoStoredTokens.Store(ctx, "gitlab.example.com", "new-tok"); err != nil {
		t.Fatal(err)
	}
	client, err := work.clients.ForRepo(ctx, domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "g/p", Host: "gitlab.example.com"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Login(ctx); err != nil || recorder.last() != "Bearer new-tok" {
		t.Fatalf("LADO client sent %q (%v), want the replaced token", recorder.last(), err)
	}
	client, err = work.clients.ForRepo(ctx, domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "g/p", Host: "gitlab.env.com"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Login(ctx); err != nil || recorder.last() != "Bearer env-tok" {
		t.Fatalf("LADO client sent %q (%v), want AO_GITLAB_HOST_TOKENS first", recorder.last(), err)
	}
}

// TestNewMultiTrackerWithOnlyHostTokensDoesNotWarn pins issue #19's log
// change: without a gitlab.com token the GitLab tracker is still registered,
// and the daemon notes it at Info instead of warning "tracker disabled".
func TestNewMultiTrackerWithOnlyHostTokensDoesNotWarn(t *testing.T) {
	ctx := context.Background()
	clearProviderTokenEnv(t)
	t.Setenv("PATH", t.TempDir()) // no glab, no gh

	for _, tc := range []struct {
		name   string
		env    config.GitLabConfig
		stored bool
		want   string
	}{
		{name: "env host token", env: config.GitLabConfig{AllowedHosts: []string{"gitlab.internal.example"}, HostTokens: map[string]string{"gitlab.internal.example": "tok"}}, want: "self-managed"},
		{name: "stored host token", stored: true, want: "self-managed"},
		{name: "no token", want: "no GitLab token yet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			creds, _ := newTestLadoCredentials(t)
			if tc.stored {
				if _, err := creds.Store(ctx, "gitlab.internal.example", "tok"); err != nil {
					t.Fatal(err)
				}
			}
			useLadoGitLabHub(t, tc.env, creds)
			records := &logRecords{}

			tracker := newMultiTracker(tc.env, slog.New(records))

			if tracker == nil {
				t.Fatal("multi tracker missing")
			}
			if warned := records.at(slog.LevelWarn); len(warned) != 0 {
				t.Fatalf("warnings = %q, want none", warned)
			}
			_, err := tracker.Get(ctx, domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "g/p#1", Host: "gitlab.attacker.example"})
			if !errors.Is(err, trackergitlab.ErrHostNotAllowed) {
				t.Fatalf("Get on an unknown host = %v, want ErrHostNotAllowed from a registered GitLab tracker", err)
			}
			if tc.want != "no GitLab token yet" {
				if err := lastTracker(t, ladoGitLabHub).ConfigForHost("gitlab.internal.example"); err != nil {
					t.Fatalf("host with a token not allowed: %v", err)
				}
			}
			infos := strings.Join(records.at(slog.LevelInfo), "\n")
			if !strings.Contains(infos, tc.want) {
				t.Fatalf("info logs = %q, want one with %q", infos, tc.want)
			}
		})
	}
}
