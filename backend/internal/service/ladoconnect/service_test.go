package ladoconnect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const secretToken = "tok-SECRET-123"

// fakeRepoClient answers as a host would.
type fakeRepoClient struct {
	ports.Tracker
	access    ports.LadoRepoAccess
	accessErr error
	login     string
	loginErr  error
}

func (c *fakeRepoClient) CreateIssue(context.Context, domain.TrackerRepo, string, string) (domain.Issue, error) {
	return domain.Issue{}, errors.New("not used")
}

func (c *fakeRepoClient) Login(context.Context) (string, error) { return c.login, c.loginErr }

func (c *fakeRepoClient) CheckAccess(context.Context, domain.TrackerRepo) (ports.LadoRepoAccess, error) {
	return c.access, c.accessErr
}

func (c *fakeRepoClient) IssueComments(context.Context, domain.TrackerID) ([]domain.LadoIssueComment, error) {
	return nil, nil
}

func (c *fakeRepoClient) BranchRules(context.Context, domain.TrackerRepo, string) (ports.LadoBranchRules, error) {
	return ports.LadoBranchRules{}, errors.New("not used")
}

func (c *fakeRepoClient) PostCommitStatus(context.Context, domain.TrackerRepo, string, ports.LadoCommitStatus) error {
	return errors.New("not used")
}

// fakeClients records which token each client was built with.
type fakeClients struct {
	client *fakeRepoClient
	err    error
	tokens []string
	repos  []domain.TrackerRepo
}

func (f *fakeClients) ForRepo(_ context.Context, repo domain.TrackerRepo, token string) (ports.LadoRepoClient, error) {
	f.tokens = append(f.tokens, token)
	f.repos = append(f.repos, repo)
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

type fakeGit struct {
	lsRemoteErr error
	lsRemote    []string // url + " | " + command
	sshCommands map[string]string
}

// hostKeyFailure is what ssh prints for a host key nobody accepted yet.
const hostKeyFailure = "Host key verification failed."

func (g *fakeGit) LsRemote(_ context.Context, url, command string) error {
	g.lsRemote = append(g.lsRemote, url+" | "+command)
	return g.lsRemoteErr
}

func (g *fakeGit) SetSSHCommand(_ context.Context, repoPath, command string) error {
	if g.sshCommands == nil {
		g.sshCommands = map[string]string{}
	}
	g.sshCommands[repoPath] = command
	return nil
}

type fakeProjects struct {
	projects []domain.ProjectRecord
	children map[string][]domain.WorkspaceRepoRecord
}

func (p fakeProjects) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	return p.projects, nil
}

func (p fakeProjects) ListWorkspaceRepos(_ context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error) {
	return p.children[projectID], nil
}

type fakeCloner struct {
	requests []CloneRequest
	err      error
}

func (c *fakeCloner) Clone(_ context.Context, in CloneRequest) (domain.ProjectID, error) {
	c.requests = append(c.requests, in)
	if c.err != nil {
		return "", c.err
	}
	return "demo", nil
}

type connectFixture struct {
	svc     *Service
	clients *fakeClients
	git     *fakeGit
	cloner  *fakeCloner
	creds   *Credentials
	home    string
	logs    *bytes.Buffer
}

func fullAccess() ports.LadoRepoAccess {
	return ports.LadoRepoAccess{
		Login:         "ljy",
		DefaultBranch: "main",
		Permissions: []ports.LadoPermission{
			{Name: ports.LadoPermissionRepository, Status: ports.LadoPermissionOK},
			{Name: ports.LadoPermissionIssues, Status: ports.LadoPermissionOK},
			{Name: ports.LadoPermissionPullRequests, Status: ports.LadoPermissionOK},
			{Name: ports.LadoPermissionBranchRules, Status: ports.LadoPermissionOK},
			{Name: ports.LadoPermissionCommitStatuses, Status: ports.LadoPermissionMissing, Detail: "Posting commit statuses needs write access to the repository."},
		},
	}
}

func newConnectFixture(t *testing.T, projects ...domain.ProjectRecord) *connectFixture {
	t.Helper()
	return newConnectFixtureWith(t, fakeProjects{projects: projects})
}

func newConnectFixtureWith(t *testing.T, projects fakeProjects) *connectFixture {
	t.Helper()
	creds, _, _ := newTestCredentials(t)
	fx := &connectFixture{
		clients: &fakeClients{client: &fakeRepoClient{access: fullAccess(), login: "ljy"}},
		git:     &fakeGit{},
		cloner:  &fakeCloner{},
		creds:   creds,
		home:    t.TempDir(),
		logs:    &bytes.Buffer{},
	}
	fx.svc = New(Deps{
		Credentials: creds,
		Clients:     fx.clients,
		Projects:    projects,
		Cloner:      fx.cloner,
		Git:         fx.git,
		HomeDir:     func() (string, error) { return fx.home, nil },
		Log:         slog.New(slog.NewTextHandler(fx.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	return fx
}

func (fx *connectFixture) writeKey(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(fx.home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "id_lado"), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "id_lado")
}

func apiCode(err error) string {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func TestCheckRefusesURLsThatAreNotSSH(t *testing.T) {
	fx := newConnectFixture(t)
	for _, url := range []string{
		"", "https://github.com/acme/demo.git", "github.com/acme/demo", "file:///tmp/repo",
		"git@acme.ghe.io:acme/demo.git", "deploy@github.com:acme/demo.git",
	} {
		if _, err := fx.svc.Check(context.Background(), CheckInput{URL: url}); apiCode(err) != "LADO_REPOSITORY_URL_INVALID" {
			t.Errorf("Check(%q) err = %v, want LADO_REPOSITORY_URL_INVALID", url, err)
		}
	}
}

func TestCheckWithAnEnteredToken(t *testing.T) {
	fx := newConnectFixture(t)
	result, err := fx.svc.Check(context.Background(), CheckInput{URL: "git@github.com:acme/demo.git", Token: " " + secretToken + " "})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Host != "github.com" || result.Repository != (domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}) {
		t.Fatalf("result repository = %+v on %s", result.Repository, result.Host)
	}
	if !result.Token.Entered || result.Token.Stored || result.Token.Login != "ljy" || result.Token.Problem != "" {
		t.Fatalf("token check = %+v, want the entered token accepted for ljy", result.Token)
	}
	if fx.clients.tokens[0] != secretToken {
		t.Fatalf("client built with token %q, want the trimmed entered token", fx.clients.tokens[0])
	}
	if !result.SSH.OK || !result.Ready || result.Project != nil {
		t.Fatalf("result = %+v, want ready to clone", result)
	}
	if got := fx.git.lsRemote[0]; got != "git@github.com:acme/demo.git | ssh -o BatchMode=yes" {
		t.Fatalf("ls-remote = %q, want the URL with prompts off", got)
	}
}

func TestCheckUsesTheHostsExistingToken(t *testing.T) {
	fx := newConnectFixture(t)
	if _, err := fx.creds.Store(context.Background(), "gitlab.example.com", "glpat-existing"); err != nil {
		t.Fatal(err)
	}
	result, err := fx.svc.Check(context.Background(), CheckInput{URL: "ssh://git@gitlab.example.com:2222/group/project.git"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Host != "gitlab.example.com" || !result.Token.Stored || result.Token.Entered {
		t.Fatalf("result = %+v, want the stored gitlab.example.com token", result)
	}
	if fx.clients.tokens[0] != "" || fx.clients.repos[0].Host != "gitlab.example.com" {
		t.Fatalf("client built with token %q for %+v, want the host's chain", fx.clients.tokens[0], fx.clients.repos[0])
	}
}

func TestCheckReportsTokenProblems(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(*connectFixture)
		problem string
		code    string
	}{
		{"no token", func(fx *connectFixture) { fx.clients.err = errors.New("no token") }, "No access token for github.com", ProblemTokenMissing},
		{"rejected", func(fx *connectFixture) {
			fx.clients.client.accessErr = fmt.Errorf("%w: Bad credentials", ports.ErrLadoTokenRejected)
		}, "github.com rejected the token", ProblemTokenRejected},
		{"unreachable", func(fx *connectFixture) { fx.clients.client.accessErr = errors.New("dial tcp: no route") }, "could not check the token", ProblemTokenCheckFailed},
		{"unusable", func(fx *connectFixture) { fx.clients.err = errors.New("token has a line break") }, "could not use the token", ProblemTokenUnusable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newConnectFixture(t)
			tc.setup(fx)
			in := CheckInput{URL: "git@github.com:acme/demo.git"}
			if tc.code == ProblemTokenUnusable {
				in.Token = secretToken
			}
			result, err := fx.svc.Check(context.Background(), in)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			wantDetail := tc.code == ProblemTokenUnusable || tc.code == ProblemTokenCheckFailed
			if (result.Token.ProblemDetail != "") != wantDetail || strings.Contains(result.Token.ProblemDetail, "LADO") {
				t.Fatalf("problem detail = %q, want only the underlying error for %s", result.Token.ProblemDetail, tc.code)
			}
			if !strings.Contains(result.Token.Problem, tc.problem) || result.Token.ProblemCode != tc.code || result.Ready {
				t.Fatalf("token problem = %q (%s) ready %v, want %q (%s) and not ready", result.Token.Problem, result.Token.ProblemCode, result.Ready, tc.problem, tc.code)
			}
		})
	}
}

func TestCheckNeedsRepositoryAndIssueAccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		issues ports.LadoPermissionStatus
		ready  bool
	}{
		{"issues ok", ports.LadoPermissionOK, true},
		{"issues unverified", ports.LadoPermissionUnverified, true},
		{"issues missing", ports.LadoPermissionMissing, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newConnectFixture(t)
			fx.clients.client.access.Permissions[1].Status = tc.issues
			fx.clients.client.access.Permissions[1].Detail = "Issues are turned off for this repository."
			result, err := fx.svc.Check(context.Background(), CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken})
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if result.Ready != tc.ready {
				t.Fatalf("ready = %v, want %v", result.Ready, tc.ready)
			}
		})
	}
	fx := newConnectFixture(t)
	fx.clients.client.access = ports.LadoRepoAccess{Login: "ljy", Permissions: []ports.LadoPermission{{Name: ports.LadoPermissionRepository, Status: ports.LadoPermissionMissing, Detail: "The token cannot read acme/demo."}}}
	result, err := fx.svc.Check(context.Background(), CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken})
	if err != nil || result.Ready {
		t.Fatalf("Check without repository access = ready %v, err %v; want not ready", result.Ready, err)
	}
}

func TestCheckSSHWithAKeyFile(t *testing.T) {
	fx := newConnectFixture(t)
	key := fx.writeKey(t)
	result, err := fx.svc.Check(context.Background(), CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken, SSHKeyPath: "~/.ssh/id_lado"})
	if err != nil || !result.SSH.OK {
		t.Fatalf("Check = %+v, %v; want SSH ok", result.SSH, err)
	}
	want := "git@github.com:acme/demo.git | ssh -i '" + filepath.ToSlash(key) + "' -o IdentitiesOnly=yes -o BatchMode=yes"
	if fx.git.lsRemote[0] != want {
		t.Fatalf("ls-remote = %q\nwant %q", fx.git.lsRemote[0], want)
	}

	missing, err := fx.svc.Check(context.Background(), CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken, SSHKeyPath: "~/.ssh/id_missing"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if missing.SSH.OK || !strings.Contains(missing.SSH.Problem, "~/.ssh/id_missing") || missing.SSH.ProblemCode != ProblemKeyMissing || missing.Ready {
		t.Fatalf("missing key = %+v, want a problem naming the path", missing.SSH)
	}
	if len(fx.git.lsRemote) != 1 {
		t.Fatalf("ls-remote ran %d times, want no run for a missing key", len(fx.git.lsRemote))
	}
	relative, _ := fx.svc.Check(context.Background(), CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken, SSHKeyPath: "id_lado"})
	if relative.SSH.OK || relative.SSH.ProblemCode != ProblemKeyPathRelative {
		t.Fatalf("relative key path = %+v, want key_path_relative", relative.SSH)
	}
}

func TestCheckExplainsSSHFailures(t *testing.T) {
	for _, tc := range []struct {
		failure, want, code string
	}{
		{hostKeyFailure, "ssh -T git@github.com", ProblemHostKeyUntrusted},
		{"git@github.com: Permission denied (publickey).", "github.com refused the SSH key", ProblemKeyRefused},
		{"ssh: Could not resolve hostname github.com", "Could not resolve hostname", ProblemSSHFailed},
	} {
		fx := newConnectFixture(t)
		fx.git.lsRemoteErr = errors.New(tc.failure)
		result, err := fx.svc.Check(context.Background(), CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if (result.SSH.ProblemDetail == tc.failure) != (tc.code == ProblemSSHFailed) {
			t.Fatalf("SSH problem detail = %q for %s", result.SSH.ProblemDetail, tc.code)
		}
		if result.SSH.OK || !strings.Contains(result.SSH.Problem, tc.want) || result.SSH.ProblemCode != tc.code {
			t.Fatalf("SSH problem for %q = %q (%s), want it to mention %q (%s)", tc.failure, result.SSH.Problem, result.SSH.ProblemCode, tc.want, tc.code)
		}
	}
}

func TestCheckFindsTheRegisteredProject(t *testing.T) {
	fx := newConnectFixture(t,
		domain.ProjectRecord{ID: "other", Path: "/src/other", RepoOriginURL: "git@github.com:acme/other.git"},
		domain.ProjectRecord{ID: "no-origin", Path: "/src/no-origin"},
		domain.ProjectRecord{ID: "demo", Path: "/src/demo", DisplayName: "Demo", RepoOriginURL: "https://github.com/Acme/Demo"},
		domain.ProjectRecord{ID: "demo-copy", Path: "/src/demo-copy", RepoOriginURL: "git@github.com:acme/demo.git"},
	)
	result, err := fx.svc.Check(context.Background(), CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Project == nil || *result.Project != (LinkedProject{ID: "demo", Name: "Demo", Path: "/src/demo", RepoPath: "/src/demo"}) {
		t.Fatalf("project = %+v, want the first project with the same repository", result.Project)
	}
}

func TestConnectLinksTheRegisteredProject(t *testing.T) {
	fx := newConnectFixture(t, domain.ProjectRecord{ID: "demo", Path: "/src/demo", RepoOriginURL: "git@github.com:acme/demo.git"})
	key := fx.writeKey(t)
	result, err := fx.svc.Connect(context.Background(), ConnectInput{CheckInput: CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken, SSHKeyPath: key}})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if result != (ConnectResult{ProjectID: "demo", Outcome: ConnectLinked}) {
		t.Fatalf("result = %+v, want demo linked", result)
	}
	if token, ok, _ := fx.creds.Token(context.Background(), "github.com"); !ok || token != secretToken {
		t.Fatalf("stored token = %q, %v; want the entered token", token, ok)
	}
	want := "ssh -i '" + filepath.ToSlash(key) + "' -o IdentitiesOnly=yes"
	if got := fx.git.sshCommands["/src/demo"]; got != want {
		t.Fatalf("core.sshCommand = %q, want %q (no BatchMode)", got, want)
	}
	if len(fx.cloner.requests) != 0 {
		t.Fatal("linking cloned the repository")
	}
}

func TestConnectWithoutAKeyLeavesTheRepositoryConfigAlone(t *testing.T) {
	fx := newConnectFixture(t, domain.ProjectRecord{ID: "demo", Path: "/src/demo", RepoOriginURL: "git@github.com:acme/demo.git"})
	if _, err := fx.svc.Connect(context.Background(), ConnectInput{CheckInput: CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken}}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if len(fx.git.sshCommands) != 0 {
		t.Fatalf("core.sshCommand set to %v without a key file", fx.git.sshCommands)
	}
}

func TestConnectClonesWhenNoProjectHasTheRepository(t *testing.T) {
	fx := newConnectFixture(t)
	key := fx.writeKey(t)
	in := ConnectInput{CheckInput: CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken, SSHKeyPath: key}}
	if _, err := fx.svc.Connect(context.Background(), in); apiCode(err) != "LADO_DESTINATION_REQUIRED" {
		t.Fatalf("Connect without a destination err = %v, want LADO_DESTINATION_REQUIRED", err)
	}
	in.DestinationParent = "~/ao/projects"
	result, err := fx.svc.Connect(context.Background(), in)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if result != (ConnectResult{ProjectID: "demo", Outcome: ConnectCloned}) {
		t.Fatalf("result = %+v, want demo cloned", result)
	}
	want := CloneRequest{
		RemoteURL:         "git@github.com:acme/demo.git",
		Placement:         PlaceStandalone,
		DestinationParent: "~/ao/projects",
		GitConfig:         []string{"core.sshCommand=ssh -i '" + filepath.ToSlash(key) + "' -o IdentitiesOnly=yes"},
	}
	got := fx.cloner.requests[0]
	if got.RemoteURL != want.RemoteURL || got.Placement != want.Placement || got.DestinationParent != want.DestinationParent || strings.Join(got.GitConfig, ";") != strings.Join(want.GitConfig, ";") {
		t.Fatalf("clone request = %+v, want %+v", got, want)
	}
}

func TestConnectRefusesAConnectionThatIsNotReady(t *testing.T) {
	fx := newConnectFixture(t)
	fx.git.lsRemoteErr = errors.New(hostKeyFailure)
	_, err := fx.svc.Connect(context.Background(), ConnectInput{
		CheckInput:        CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken},
		DestinationParent: "/src",
	})
	if apiCode(err) != "LADO_CONNECTION_NOT_READY" || !strings.Contains(err.Error(), "not trusted yet") {
		t.Fatalf("err = %v, want LADO_CONNECTION_NOT_READY naming the SSH problem", err)
	}
	if _, ok, _ := fx.creds.Token(context.Background(), "github.com"); ok {
		t.Fatal("a refused connection stored the token")
	}
	if len(fx.cloner.requests) != 0 {
		t.Fatal("a refused connection cloned the repository")
	}
}

func TestStoreCredentialChecksTheTokenFirst(t *testing.T) {
	fx := newConnectFixture(t)
	stored, login, err := fx.svc.StoreCredential(context.Background(), "GitLab.com", secretToken)
	if err != nil {
		t.Fatalf("StoreCredential: %v", err)
	}
	if stored.Host != "gitlab.com" || login != "ljy" || fx.clients.tokens[0] != secretToken || fx.clients.repos[0].Host != "" {
		t.Fatalf("stored %+v login %q client %v %+v", stored, login, fx.clients.tokens, fx.clients.repos)
	}

	fx.clients.client.loginErr = fmt.Errorf("%w: 401 Unauthorized", ports.ErrLadoTokenRejected)
	if _, _, err := fx.svc.StoreCredential(context.Background(), "gitlab.example.com", secretToken); apiCode(err) != "LADO_TOKEN_REJECTED" {
		t.Fatalf("rejected token err = %v, want LADO_TOKEN_REJECTED", err)
	}
	if _, ok, _ := fx.creds.Token(context.Background(), "gitlab.example.com"); ok {
		t.Fatal("a rejected token was stored")
	}
	if fx.clients.repos[1].Host != "gitlab.example.com" {
		t.Fatalf("self-managed check went to %+v", fx.clients.repos[1])
	}
	if _, _, err := fx.svc.StoreCredential(context.Background(), "https://gitlab.com", secretToken); apiCode(err) != "LADO_CREDENTIAL_HOST_INVALID" {
		t.Fatalf("bad host err = %v, want LADO_CREDENTIAL_HOST_INVALID", err)
	}
}

func TestConnectionErrorsAndLogsNeverCarryTheToken(t *testing.T) {
	fx := newConnectFixture(t)
	fx.clients.client.accessErr = errors.New("dial tcp: connection refused")
	result, err := fx.svc.Check(context.Background(), CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	_, connectErr := fx.svc.Connect(context.Background(), ConnectInput{CheckInput: CheckInput{URL: "git@github.com:acme/demo.git", Token: secretToken}, DestinationParent: "/src"})
	fx.clients.client.loginErr = fmt.Errorf("%w: Bad credentials", ports.ErrLadoTokenRejected)
	_, _, storeErr := fx.svc.StoreCredential(context.Background(), "github.com", secretToken)
	for _, text := range []string{result.Token.Problem, fmt.Sprint(connectErr), fmt.Sprint(storeErr), fx.logs.String()} {
		if strings.Contains(text, secretToken) {
			t.Fatalf("%q carries the token", text)
		}
	}
}

func workspaceProjects() fakeProjects {
	return fakeProjects{
		projects: []domain.ProjectRecord{{ID: "product", Kind: domain.ProjectKindWorkspace, Path: "/src/product", DisplayName: "Product"}},
		children: map[string][]domain.WorkspaceRepoRecord{"product": {
			{Name: "back", RelativePath: "back", RepoOriginURL: "git@github.com:acme/back.git"},
			{Name: "docs", RelativePath: "docs"},
			{Name: "front", RelativePath: "front", RepoOriginURL: "https://github.com/acme/front.git"},
		}},
	}
}

func TestCheckFindsAWorkspaceRepository(t *testing.T) {
	fx := newConnectFixtureWith(t, workspaceProjects())
	key := fx.writeKey(t)
	result, err := fx.svc.Check(context.Background(), CheckInput{URL: "git@github.com:acme/front.git", Token: secretToken})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	want := LinkedProject{ID: "product", Name: "Product", Path: "/src/product", Repository: "front", RepoPath: filepath.Join("/src/product", "front")}
	if result.Project == nil || *result.Project != want {
		t.Fatalf("project = %+v, want %+v", result.Project, want)
	}

	connected, err := fx.svc.Connect(context.Background(), ConnectInput{CheckInput: CheckInput{URL: "git@github.com:acme/front.git", Token: secretToken, SSHKeyPath: key}})
	if err != nil || connected != (ConnectResult{ProjectID: "product", Outcome: ConnectLinked}) {
		t.Fatalf("Connect = %+v, %v; want the workspace linked", connected, err)
	}
	if _, ok := fx.git.sshCommands[want.RepoPath]; !ok || len(fx.git.sshCommands) != 1 {
		t.Fatalf("core.sshCommand set on %v, want only the front repository", fx.git.sshCommands)
	}
	if len(fx.cloner.requests) != 0 {
		t.Fatal("linking a workspace repository cloned it")
	}
}

func TestConnectClonesWherePlacementSays(t *testing.T) {
	ctx := context.Background()
	base := CheckInput{URL: "git@github.com:acme/back.git", Token: secretToken}
	for _, tc := range []struct {
		in   ConnectInput
		code string
	}{
		{ConnectInput{CheckInput: base, Placement: PlaceNewWorkspace, WorkspaceName: "product"}, "LADO_DESTINATION_REQUIRED"},
		{ConnectInput{CheckInput: base, Placement: PlaceNewWorkspace, DestinationParent: "/src", WorkspaceName: " "}, "LADO_WORKSPACE_NAME_REQUIRED"},
		{ConnectInput{CheckInput: base, Placement: PlaceWorkspace, DestinationParent: "/src"}, "LADO_WORKSPACE_REQUIRED"},
		{ConnectInput{CheckInput: base, Placement: "beside", DestinationParent: "/src"}, "LADO_PLACEMENT_INVALID"},
	} {
		fx := newConnectFixture(t)
		if _, err := fx.svc.Connect(ctx, tc.in); apiCode(err) != tc.code {
			t.Errorf("Connect(%+v) err = %v, want %s", tc.in, err, tc.code)
		}
		if _, ok, _ := fx.creds.Token(ctx, "github.com"); ok || len(fx.cloner.requests) != 0 {
			t.Errorf("Connect(%+v) stored the token or cloned although the placement was refused", tc.in)
		}
	}

	fx := newConnectFixture(t)
	if _, err := fx.svc.Connect(ctx, ConnectInput{CheckInput: base, Placement: PlaceNewWorkspace, DestinationParent: " /src ", WorkspaceName: " product "}); err != nil {
		t.Fatalf("Connect into a new workspace: %v", err)
	}
	if got := fx.cloner.requests[0]; got.Placement != PlaceNewWorkspace || got.DestinationParent != "/src" || got.WorkspaceName != "product" {
		t.Fatalf("clone request = %+v, want a trimmed new workspace", got)
	}
	if _, err := fx.svc.Connect(ctx, ConnectInput{CheckInput: CheckInput{URL: "git@github.com:acme/front.git"}, Placement: PlaceWorkspace, WorkspaceID: "product"}); err != nil {
		t.Fatalf("Connect into a workspace: %v", err)
	}
	if got := fx.cloner.requests[1]; got.Placement != PlaceWorkspace || got.WorkspaceID != "product" || got.RemoteURL != "git@github.com:acme/front.git" {
		t.Fatalf("clone request = %+v, want the front repository in product", got)
	}
}
