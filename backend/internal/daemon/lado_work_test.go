package daemon

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	trackergithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/github"
	trackergitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/gitlab"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoconnect"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// memGitHubTokens stands in for AO's githubpat store.
type memGitHubTokens struct {
	token string
}

func (m *memGitHubTokens) StorePAT(_ context.Context, token string) error {
	m.token = token
	return nil
}

func (m *memGitHubTokens) HasPAT(context.Context) bool { return m.token != "" }

func (m *memGitHubTokens) DeletePAT(context.Context) error {
	m.token = ""
	return nil
}

func (m *memGitHubTokens) GetToken(context.Context) (string, error) { return m.token, nil }

// useLadoStoredTokens points the token chains at creds for one test.
func useLadoStoredTokens(t *testing.T, creds *ladoconnect.Credentials) {
	t.Helper()
	previous := ladoStoredTokens
	ladoStoredTokens = creds
	t.Cleanup(func() { ladoStoredTokens = previous })
}

func newTestLadoCredentials(t *testing.T) (*ladoconnect.Credentials, *memGitHubTokens) {
	t.Helper()
	github := &memGitHubTokens{}
	return ladoconnect.NewCredentials(github, t.TempDir()), github
}

func clearProviderTokenEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"AO_GITHUB_TOKEN", "GITHUB_TOKEN", "AO_GITLAB_TOKEN", "GITLAB_TOKEN"} {
		t.Setenv(name, "")
	}
}

func TestLadoTokenSourcesReadTheStore(t *testing.T) {
	ctx := context.Background()
	useLadoStoredTokens(t, nil)
	if _, err := (ladoSCMGitHubToken{}).Token(ctx); !errors.Is(err, scmgithub.ErrNoToken) {
		t.Fatalf("SCM GitHub token without a store: %v, want ErrNoToken", err)
	}
	if _, err := (ladoTrackerGitHubToken{}).Token(ctx); !errors.Is(err, trackergithub.ErrNoToken) {
		t.Fatalf("tracker GitHub token without a store: %v, want ErrNoToken", err)
	}
	if _, err := (ladoGitLabToken{host: "gitlab.com"}).Token(ctx); !errors.Is(err, scmgitlab.ErrNoToken) {
		t.Fatalf("GitLab token without a store: %v, want ErrNoToken", err)
	}

	creds, github := newTestLadoCredentials(t)
	useLadoStoredTokens(t, creds)
	if _, err := (ladoGitLabToken{host: "gitlab.example.com"}).Token(ctx); !errors.Is(err, scmgitlab.ErrNoToken) {
		t.Fatalf("GitLab token before storing: %v, want ErrNoToken", err)
	}
	github.token = "ghp_stored"
	if _, err := creds.Store(ctx, "gitlab.example.com", "glpat-stored"); err != nil {
		t.Fatal(err)
	}
	if token, err := (ladoSCMGitHubToken{}).Token(ctx); err != nil || token != "ghp_stored" {
		t.Fatalf("SCM GitHub token = %q, %v", token, err)
	}
	if token, err := (ladoTrackerGitHubToken{}).Token(ctx); err != nil || token != "ghp_stored" {
		t.Fatalf("tracker GitHub token = %q, %v", token, err)
	}
	if token, err := (ladoGitLabToken{host: "gitlab.example.com"}).Token(ctx); err != nil || token != "glpat-stored" {
		t.Fatalf("GitLab token = %q, %v", token, err)
	}
}

func TestLadoStoredTokensComeAfterTheEnvOverrides(t *testing.T) {
	ctx := context.Background()
	clearProviderTokenEnv(t)
	creds, github := newTestLadoCredentials(t)
	useLadoStoredTokens(t, creds)
	github.token = "ghp_stored"
	if _, err := creds.Store(ctx, "gitlab.com", "glpat-stored"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AO_GITHUB_TOKEN", "env-github")
	if token, err := (&ghTokenSource{}).Token(ctx); err != nil || token != "env-github" {
		t.Fatalf("tracker chain with AO_GITHUB_TOKEN = %q, %v; want the env token", token, err)
	}
	t.Setenv("AO_GITHUB_TOKEN", "")
	if token, err := (&ghTokenSource{}).Token(ctx); err != nil || token != "ghp_stored" {
		t.Fatalf("tracker chain without env = %q, %v; want the stored token before gh", token, err)
	}

	t.Setenv("AO_GITLAB_TOKEN", "env-gitlab")
	if token, err := ladoGitLabDefaultTokens().Token(ctx); err != nil || token != "env-gitlab" {
		t.Fatalf("GitLab chain with AO_GITLAB_TOKEN = %q, %v; want the env token", token, err)
	}
	t.Setenv("AO_GITLAB_TOKEN", "")
	if token, err := ladoGitLabDefaultTokens().Token(ctx); err != nil || token != "glpat-stored" {
		t.Fatalf("GitLab chain without env = %q, %v; want the stored token before glab", token, err)
	}
}

func TestLadoGitLabConfigAddsStoredSelfManagedHosts(t *testing.T) {
	ctx := context.Background()
	creds, _ := newTestLadoCredentials(t)
	for host, token := range map[string]string{"gitlab.example.com": "stored-a", "gitlab.other.com": "stored-b", "gitlab.com": "stored-com"} {
		if _, err := creds.Store(ctx, host, token); err != nil {
			t.Fatal(err)
		}
	}
	base := config.GitLabConfig{
		AllowedHosts: []string{"GitLab.Example.com"},
		HostTokens:   map[string]string{"gitlab.example.com": "env-a"},
	}

	merged := ladoGitLabConfig(ctx, base, creds, slog.New(slog.DiscardHandler))

	if strings.Join(merged.AllowedHosts, ",") != "GitLab.Example.com,gitlab.other.com" {
		t.Fatalf("allowed hosts = %v, want the stored self-managed host added once", merged.AllowedHosts)
	}
	if len(merged.HostTokens) != 2 || merged.HostTokens["gitlab.example.com"] != "env-a" || merged.HostTokens["gitlab.other.com"] != "stored-b" {
		t.Fatalf("host tokens = %v, want the env token kept and the stored one added", merged.HostTokens)
	}
	if len(base.AllowedHosts) != 1 || len(base.HostTokens) != 1 {
		t.Fatalf("base config changed: %+v", base)
	}
}

// authRecorder is a GitHub API that records the token of each request.
type authRecorder struct {
	mu    sync.Mutex
	auths []string
}

func (a *authRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.auths = append(a.auths, r.Header.Get("Authorization"))
		a.mu.Unlock()
		if r.URL.Path != "/user" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"login":"ljy","username":"ljy"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (a *authRecorder) last() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.auths[len(a.auths)-1]
}

func TestLadoRepoClientsUseTheEnteredOrTheStoredToken(t *testing.T) {
	ctx := context.Background()
	clearProviderTokenEnv(t)
	creds, github := newTestLadoCredentials(t)
	useLadoStoredTokens(t, creds)
	github.token = "ghp_stored"
	recorder := &authRecorder{}
	clients := newLadoRepoClients(config.GitLabConfig{})
	clients.githubBaseURL = recorder.server(t).URL
	repo := domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}

	entered, err := clients.ForRepo(ctx, repo, " tok-entered ")
	if err != nil {
		t.Fatalf("ForRepo with a token: %v", err)
	}
	if _, err := entered.Login(ctx); err != nil || recorder.last() != "Bearer tok-entered" {
		t.Fatalf("entered-token client sent %q (%v), want the entered token", recorder.last(), err)
	}
	chained, err := clients.ForRepo(ctx, repo, "")
	if err != nil {
		t.Fatalf("ForRepo on the chain: %v", err)
	}
	if _, err := chained.Login(ctx); err != nil || recorder.last() != "Bearer ghp_stored" {
		t.Fatalf("chain client sent %q (%v), want the stored token", recorder.last(), err)
	}
	again, err := clients.ForRepo(ctx, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.(ladoRepoClient).Tracker != chained.(ladoRepoClient).Tracker {
		t.Fatal("the chain client was rebuilt instead of reused")
	}
}

func TestLadoRepoClientsForASelfManagedGitLab(t *testing.T) {
	ctx := context.Background()
	creds, _ := newTestLadoCredentials(t)
	useLadoStoredTokens(t, creds)
	if _, err := creds.Store(ctx, "gitlab.example.com", "stored-tok"); err != nil {
		t.Fatal(err)
	}
	clients := newLadoRepoClients(config.GitLabConfig{HostTokens: map[string]string{"GitLab.Example.com": "env-tok"}})
	if token, err := clients.gitlabTokens("gitlab.example.com", "").Token(ctx); err != nil || token != "env-tok" {
		t.Fatalf("self-managed chain = %q, %v; want AO_GITLAB_HOST_TOKENS first", token, err)
	}
	clients.gitlabHostTokens = nil
	if token, err := clients.gitlabTokens("gitlab.example.com", "").Token(ctx); err != nil || token != "stored-tok" {
		t.Fatalf("self-managed chain without env = %q, %v; want the stored token", token, err)
	}

	client, err := clients.ForRepo(ctx, domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/project", Host: "GitLab.Example.com"}, "")
	if err != nil {
		t.Fatalf("ForRepo: %v", err)
	}
	tracker, ok := client.(ladoRepoClient).Tracker.(*trackergitlab.Tracker)
	if !ok || tracker.ConfigForHost("gitlab.example.com") != nil {
		t.Fatalf("client %T does not route gitlab.example.com", client.(ladoRepoClient).Tracker)
	}
	if _, err := clients.ForRepo(ctx, domain.TrackerRepo{Provider: "jira", Native: "x"}, ""); err == nil {
		t.Fatal("ForRepo accepted an unknown tracker")
	}
}

type recordingProjectCloner struct {
	got          projectsvc.CloneInput
	gotWorkspace projectsvc.LadoWorkspaceCloneInput
	gotParent    string
	gotName      string
	gotID        domain.ProjectID
}

func (r *recordingProjectCloner) Clone(_ context.Context, in projectsvc.CloneInput) (projectsvc.Project, error) {
	r.got = in
	return projectsvc.Project{ID: "demo"}, nil
}

func (r *recordingProjectCloner) CloneIntoNewWorkspace(_ context.Context, parent, name string, in projectsvc.LadoWorkspaceCloneInput) (projectsvc.Project, error) {
	r.gotParent, r.gotName, r.gotWorkspace = parent, name, in
	return projectsvc.Project{ID: "product"}, nil
}

func (r *recordingProjectCloner) CloneIntoWorkspace(_ context.Context, id domain.ProjectID, in projectsvc.LadoWorkspaceCloneInput) (projectsvc.Project, error) {
	r.gotID, r.gotWorkspace = id, in
	return projectsvc.Project{ID: id}, nil
}

func TestWireLadoWorkBuildsConnectionsAndTasks(t *testing.T) {
	ctx := context.Background()
	useLadoStoredTokens(t, nil)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	cloner := &recordingProjectCloner{}

	connections, tasks, _ := wireLadoWork(ctx, cfg, sqlitetest.MustOpen(t), cloner, ladoWorkDeps{}, slog.New(slog.DiscardHandler))

	if connections == nil || tasks == nil || ladoStoredTokens == nil {
		t.Fatalf("wireLadoWork = %v, %v with store %v", connections, tasks, ladoStoredTokens)
	}
	if listed, err := connections.Credentials(ctx); err != nil || len(listed) != 0 {
		t.Fatalf("Credentials = %v, %v; want an empty store", listed, err)
	}
	id, err := (ladoCloner{projects: cloner}).Clone(ctx, ladoconnect.CloneRequest{RemoteURL: "git@github.com:acme/demo.git", DestinationParent: "/src", GitConfig: []string{"core.sshCommand=x"}})
	if err != nil || id != "demo" {
		t.Fatalf("Clone = %q, %v", id, err)
	}
	if cloner.got.RemoteURL != "git@github.com:acme/demo.git" || cloner.got.DestinationParent != "/src" || strings.Join(cloner.got.GitConfig, ",") != "core.sshCommand=x" {
		t.Fatalf("clone input = %+v", cloner.got)
	}

	id, err = (ladoCloner{projects: cloner}).Clone(ctx, ladoconnect.CloneRequest{
		RemoteURL: "git@github.com:acme/back.git", Placement: ladoconnect.PlaceNewWorkspace,
		DestinationParent: "/src", WorkspaceName: "product", GitConfig: []string{"core.sshCommand=x"},
	})
	if err != nil || id != "product" || cloner.gotParent != "/src" || cloner.gotName != "product" ||
		cloner.gotWorkspace.RemoteURL != "git@github.com:acme/back.git" || strings.Join(cloner.gotWorkspace.GitConfig, ",") != "core.sshCommand=x" {
		t.Fatalf("new workspace clone = %q, %v with %+v", id, err, cloner)
	}
	id, err = (ladoCloner{projects: cloner}).Clone(ctx, ladoconnect.CloneRequest{
		RemoteURL: "git@github.com:acme/front.git", Placement: ladoconnect.PlaceWorkspace, WorkspaceID: "product",
	})
	if err != nil || id != "product" || cloner.gotID != "product" || cloner.gotWorkspace.RemoteURL != "git@github.com:acme/front.git" {
		t.Fatalf("workspace clone = %q, %v with %+v", id, err, cloner)
	}
}
