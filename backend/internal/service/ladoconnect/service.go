package ladoconnect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Projects reads AO's registered projects.
type Projects interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
}

// Placement says where a connection clones a repository that no registered
// project has.
type Placement string

// Clone placements.
const (
	// PlaceStandalone clones the repository as a project of its own.
	PlaceStandalone Placement = "standalone"
	// PlaceNewWorkspace creates a workspace folder and clones the repository
	// into it as the workspace's first repository.
	PlaceNewWorkspace Placement = "new_workspace"
	// PlaceWorkspace clones the repository into a registered workspace
	// project, beside its other repositories.
	PlaceWorkspace Placement = "workspace"
)

// CloneRequest asks AO's clone flow for a checkout.
type CloneRequest struct {
	RemoteURL string
	Placement Placement
	// DestinationParent is the folder to clone into, or for a new workspace
	// the folder to create the workspace folder in.
	DestinationParent string
	// WorkspaceName names a new workspace's folder.
	WorkspaceName string
	// WorkspaceID is the registered workspace project to clone into.
	WorkspaceID domain.ProjectID
	// GitConfig holds key=value pairs for the new repository's local config,
	// set before the first fetch.
	GitConfig []string
}

// Cloner clones a repository through AO's clone flow, which also registers
// the project or the workspace's new repository.
type Cloner interface {
	Clone(ctx context.Context, in CloneRequest) (domain.ProjectID, error)
}

// Deps wires a Service.
type Deps struct {
	Credentials *Credentials
	Clients     ports.LadoRepoClients
	Projects    Projects
	Cloner      Cloner
	// Git runs git; nil uses the system git.
	Git Git
	// HomeDir expands "~" in key paths; nil uses os.UserHomeDir.
	HomeDir func() (string, error)
	Log     *slog.Logger
}

// Service checks and makes repository connections, and keeps host tokens.
type Service struct {
	deps Deps
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Git == nil {
		deps.Git = execGit{}
	}
	if deps.HomeDir == nil {
		deps.HomeDir = os.UserHomeDir
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Service{deps: deps}
}

// CheckInput names a repository and the credentials to reach it.
type CheckInput struct {
	// URL is the repository's SSH URL.
	URL string
	// Token is a new access token for the repository's host. Empty uses the
	// host's existing token.
	Token string
	// SSHKeyPath is an optional private key file for the repository.
	SSHKeyPath string
}

// Problem codes say why a check failed in a form the app translates. The
// matching Problem message is English and carries the details.
const (
	ProblemTokenMissing     = "token_missing"
	ProblemTokenUnusable    = "token_unusable"
	ProblemTokenRejected    = "token_rejected"
	ProblemTokenCheckFailed = "token_check_failed"
	ProblemKeyHomeUnknown   = "key_home_unknown"
	ProblemKeyPathRelative  = "key_path_relative"
	ProblemKeyMissing       = "key_missing"
	ProblemKeyNotAFile      = "key_not_a_file"
	ProblemHostKeyUntrusted = "host_key_untrusted"
	ProblemKeyRefused       = "key_refused"
	ProblemSSHFailed        = "ssh_failed"
)

// TokenCheck is what the host said about the token.
type TokenCheck struct {
	// Entered is true when the check used a newly entered token.
	Entered bool
	// Stored is true when LADO already keeps a token for the host.
	Stored      bool
	Login       string
	Permissions []ports.LadoPermission
	// Problem says why the token cannot be used. Empty when it can.
	Problem string
	// ProblemCode is one of the token Problem codes, or empty.
	ProblemCode string
	// ProblemDetail is the underlying error, when there is one to show.
	ProblemDetail string
}

// SSHCheck is the result of reaching the repository over SSH.
type SSHCheck struct {
	OK      bool
	Problem string
	// ProblemCode is one of the key or SSH Problem codes, or empty.
	ProblemCode string
	// ProblemDetail is what git or ssh printed, when there is one to show.
	ProblemDetail string
}

// LinkedProject is the registered project a connection links to.
type LinkedProject struct {
	ID   domain.ProjectID
	Name string
	// Path is the project's folder.
	Path string
	// Repository is the workspace repository that has the connection's
	// repository. It is empty for a single-repo project.
	Repository string
	// RepoPath is the repository's checkout: the project's folder, or the
	// workspace repository's folder.
	RepoPath string
}

// CheckResult is everything a connection needs, checked without writing.
type CheckResult struct {
	Repository domain.TrackerRepo
	// Host is the host the token belongs to.
	Host  string
	Token TokenCheck
	SSH   SSHCheck
	// Project is the registered project with the same repository, if any.
	// Without one, connecting clones the repository.
	Project *LinkedProject
	// Ready is true when connecting can go ahead: the token reads the
	// repository and is not missing issue access, and SSH works.
	Ready bool
}

// Check checks a token, repository access and SSH, and finds the project to
// link. It writes nothing.
func (s *Service) Check(ctx context.Context, in CheckInput) (CheckResult, error) {
	repo, host, err := connectionRepo(in.URL)
	if err != nil {
		return CheckResult{}, err
	}
	result := CheckResult{Repository: repo, Host: host}
	result.Token = s.checkToken(ctx, repo, host, in.Token)
	result.SSH = s.checkSSH(ctx, in.URL, host, in.SSHKeyPath)
	project, err := s.findProject(ctx, repo)
	if err != nil {
		return CheckResult{}, err
	}
	result.Project = project
	result.Ready = result.Token.Problem == "" && tokenCanWorkOn(result.Token.Permissions) && result.SSH.OK
	return result, nil
}

// ConnectOutcome says how a connection reached its project.
type ConnectOutcome string

// Connection outcomes.
const (
	ConnectLinked ConnectOutcome = "linked"
	ConnectCloned ConnectOutcome = "cloned"
)

// ConnectInput is a CheckInput plus where to clone when no registered project
// has the repository.
type ConnectInput struct {
	CheckInput
	// Placement is where the clone goes; empty clones a project of its own.
	Placement Placement
	// DestinationParent is the folder to clone into, or for a new workspace
	// the folder to create the workspace folder in.
	DestinationParent string
	// WorkspaceName names a new workspace's folder.
	WorkspaceName string
	// WorkspaceID is the registered workspace project to clone into.
	WorkspaceID domain.ProjectID
}

// ConnectResult is a made connection.
type ConnectResult struct {
	ProjectID domain.ProjectID
	Outcome   ConnectOutcome
}

// Connect checks the connection again, stores an entered token, then links the
// registered project with the same repository or clones it where the
// placement says. With a key file, the repository's core.sshCommand uses that
// key, for every git command in it and its worktrees.
func (s *Service) Connect(ctx context.Context, in ConnectInput) (ConnectResult, error) {
	check, err := s.Check(ctx, in.CheckInput)
	if err != nil {
		return ConnectResult{}, err
	}
	if !check.Ready {
		return ConnectResult{}, apierr.Conflict("LADO_CONNECTION_NOT_READY", notReadyMessage(check), nil)
	}
	var request CloneRequest
	if check.Project == nil {
		if request, err = cloneRequest(in); err != nil {
			return ConnectResult{}, err
		}
	}
	if strings.TrimSpace(in.Token) != "" {
		if _, err := s.deps.Credentials.Store(ctx, check.Host, in.Token); err != nil {
			s.deps.Log.Warn("LADO could not store a host token", "host", check.Host, "err", err)
			return ConnectResult{}, apierr.Internal("LADO_CREDENTIAL_STORE_FAILED", "Failed to store the access token")
		}
	}
	keyPath, problem := s.keyPath(in.SSHKeyPath)
	if problem.code != "" {
		return ConnectResult{}, apierr.Invalid("LADO_SSH_KEY_INVALID", problem.message, map[string]any{"problem": problem.code})
	}
	if check.Project != nil {
		if keyPath != "" {
			if err := s.deps.Git.SetSSHCommand(ctx, check.Project.RepoPath, sshCommand(keyPath, false)); err != nil {
				return ConnectResult{}, apierr.Internal("LADO_SSH_KEY_SETUP_FAILED", "Failed to set the repository's SSH key: "+err.Error())
			}
		}
		return ConnectResult{ProjectID: check.Project.ID, Outcome: ConnectLinked}, nil
	}
	if keyPath != "" {
		request.GitConfig = []string{"core.sshCommand=" + sshCommand(keyPath, false)}
	}
	projectID, err := s.deps.Cloner.Clone(ctx, request)
	if err != nil {
		return ConnectResult{}, err
	}
	return ConnectResult{ProjectID: projectID, Outcome: ConnectCloned}, nil
}

// cloneRequest checks that a connection names everything its placement
// needs.
func cloneRequest(in ConnectInput) (CloneRequest, error) {
	request := CloneRequest{
		RemoteURL:         strings.TrimSpace(in.URL),
		Placement:         in.Placement,
		DestinationParent: strings.TrimSpace(in.DestinationParent),
		WorkspaceName:     strings.TrimSpace(in.WorkspaceName),
		WorkspaceID:       domain.ProjectID(strings.TrimSpace(string(in.WorkspaceID))),
	}
	if request.Placement == "" {
		request.Placement = PlaceStandalone
	}
	switch request.Placement {
	case PlaceStandalone, PlaceNewWorkspace:
		if request.DestinationParent == "" {
			return CloneRequest{}, apierr.Invalid("LADO_DESTINATION_REQUIRED", "Choose where to clone the repository.", nil)
		}
		if request.Placement == PlaceNewWorkspace && request.WorkspaceName == "" {
			return CloneRequest{}, apierr.Invalid("LADO_WORKSPACE_NAME_REQUIRED", "Name the workspace folder.", nil)
		}
	case PlaceWorkspace:
		if request.WorkspaceID == "" {
			return CloneRequest{}, apierr.Invalid("LADO_WORKSPACE_REQUIRED", "Choose the workspace to clone into.", nil)
		}
	default:
		return CloneRequest{}, apierr.Invalid("LADO_PLACEMENT_INVALID", fmt.Sprintf("Unknown clone placement %q.", in.Placement), nil)
	}
	return request, nil
}

// Credentials lists the hosts with a stored token.
func (s *Service) Credentials(ctx context.Context) ([]StoredCredential, error) {
	listed, err := s.deps.Credentials.List(ctx)
	if err != nil {
		return nil, apierr.Internal("LADO_CREDENTIALS_LOAD_FAILED", "Failed to read the stored access tokens")
	}
	return listed, nil
}

// StoreCredential checks token with the host, then stores it. It returns the
// stored host and the account the token belongs to.
func (s *Service) StoreCredential(ctx context.Context, host, token string) (StoredCredential, string, error) {
	host, provider, err := CredentialHost(host)
	if err != nil {
		return StoredCredential{}, "", apierr.Invalid("LADO_CREDENTIAL_HOST_INVALID", err.Error(), nil)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return StoredCredential{}, "", apierr.Invalid("LADO_TOKEN_REQUIRED", "Enter an access token.", nil)
	}
	repo := domain.TrackerRepo{Provider: provider}
	if provider == domain.TrackerProviderGitLab && host != gitLabDotCom {
		repo.Host = host
	}
	client, err := s.deps.Clients.ForRepo(ctx, repo, token)
	if err != nil {
		return StoredCredential{}, "", apierr.Invalid("LADO_TOKEN_UNUSABLE", fmt.Sprintf("LADO could not use the token for %s: %v", host, err), nil)
	}
	login, err := client.Login(ctx)
	if errors.Is(err, ports.ErrLadoTokenRejected) {
		return StoredCredential{}, "", apierr.Invalid("LADO_TOKEN_REJECTED", host+" rejected the token.", nil)
	}
	if err != nil {
		return StoredCredential{}, "", apierr.Unavailable("LADO_TOKEN_CHECK_FAILED", fmt.Sprintf("LADO could not check the token with %s: %v", host, err))
	}
	stored, err := s.deps.Credentials.Store(ctx, host, token)
	if err != nil {
		return StoredCredential{}, "", apierr.Invalid("LADO_TOKEN_INVALID", err.Error(), nil)
	}
	return stored, login, nil
}

// DeleteCredential removes the token stored for host.
func (s *Service) DeleteCredential(ctx context.Context, host string) error {
	if _, _, err := CredentialHost(host); err != nil {
		return apierr.Invalid("LADO_CREDENTIAL_HOST_INVALID", err.Error(), nil)
	}
	if err := s.deps.Credentials.Delete(ctx, host); err != nil {
		return apierr.Internal("LADO_CREDENTIAL_DELETE_FAILED", "Failed to delete the access token")
	}
	return nil
}

// connectionRepo parses a repository SSH URL into its tracker repository and
// token host.
func connectionRepo(raw string) (domain.TrackerRepo, string, error) {
	url := strings.TrimSpace(raw)
	if !isSSHURL(url) {
		return domain.TrackerRepo{}, "", apierr.Invalid("LADO_REPOSITORY_URL_INVALID",
			"Use the repository's SSH URL, such as git@github.com:owner/repo.git.", nil)
	}
	repo, err := domain.LadoTrackerRepoFromOrigin(url)
	if err != nil {
		return domain.TrackerRepo{}, "", apierr.Invalid("LADO_REPOSITORY_URL_INVALID", err.Error(), nil)
	}
	host := repo.Host
	switch {
	case repo.Provider == domain.TrackerProviderGitHub:
		host = GitHubHost
	case host == "":
		host = gitLabDotCom
	}
	return repo, host, nil
}

// isSSHURL accepts ssh:// URLs and scp-like git@host:path remotes.
func isSSHURL(url string) bool {
	if strings.HasPrefix(url, "ssh://") {
		return true
	}
	if strings.Contains(url, "://") {
		return false
	}
	at, colon := strings.IndexByte(url, '@'), strings.IndexByte(url, ':')
	return at > 0 && colon > at+1 && !strings.Contains(url[:colon], "/")
}

// checkToken checks the entered token, or the host's existing one.
func (s *Service) checkToken(ctx context.Context, repo domain.TrackerRepo, host, entered string) TokenCheck {
	check := TokenCheck{Entered: strings.TrimSpace(entered) != ""}
	if _, stored, err := s.deps.Credentials.Token(ctx, host); err == nil {
		check.Stored = stored
	}
	client, err := s.deps.Clients.ForRepo(ctx, repo, strings.TrimSpace(entered))
	if err != nil {
		if check.Entered {
			check.ProblemCode, check.Problem = ProblemTokenUnusable, fmt.Sprintf("LADO could not use the token: %v", err)
			check.ProblemDetail = err.Error()
		} else {
			check.ProblemCode, check.Problem = ProblemTokenMissing, fmt.Sprintf("No access token for %s. Enter one.", host)
		}
		return check
	}
	access, err := client.CheckAccess(ctx, repo)
	switch {
	case errors.Is(err, ports.ErrLadoTokenRejected):
		check.ProblemCode, check.Problem = ProblemTokenRejected, host+" rejected the token."
	case err != nil:
		check.ProblemCode, check.Problem = ProblemTokenCheckFailed, fmt.Sprintf("LADO could not check the token with %s: %v", host, err)
		check.ProblemDetail = err.Error()
	default:
		check.Login = access.Login
		check.Permissions = access.Permissions
	}
	return check
}

// tokenCanWorkOn reports whether a token can read the repository and is not
// known to lack issue access. Permissions for later build steps are reported
// but do not block.
func tokenCanWorkOn(permissions []ports.LadoPermission) bool {
	var repository, issues bool
	for _, p := range permissions {
		switch p.Name {
		case ports.LadoPermissionRepository:
			repository = p.Status == ports.LadoPermissionOK
		case ports.LadoPermissionIssues:
			issues = p.Status != ports.LadoPermissionMissing
		}
	}
	return repository && issues
}

// checkSSH reaches the repository over SSH with prompts off.
func (s *Service) checkSSH(ctx context.Context, url, host, rawKeyPath string) SSHCheck {
	keyPath, problem := s.keyPath(rawKeyPath)
	if problem.code != "" {
		return SSHCheck{Problem: problem.message, ProblemCode: problem.code}
	}
	if err := s.deps.Git.LsRemote(ctx, strings.TrimSpace(url), sshCommand(keyPath, true)); err != nil {
		problem := sshProblem(host, err)
		check := SSHCheck{Problem: problem.message, ProblemCode: problem.code}
		if problem.code == ProblemSSHFailed {
			check.ProblemDetail = problem.message
		}
		return check
	}
	return SSHCheck{OK: true}
}

// checkProblem is a failed check: one of the Problem codes and an English
// message with the details.
type checkProblem struct {
	code    string
	message string
}

// keyPath expands and checks a private key path. Empty means no key file. A
// path that cannot be used comes back as a problem to show the developer.
func (s *Service) keyPath(raw string) (string, checkProblem) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", checkProblem{}
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := s.deps.HomeDir()
		if err != nil {
			return "", checkProblem{ProblemKeyHomeUnknown, fmt.Sprintf("LADO could not find the home directory for %s.", raw)}
		}
		path = filepath.Join(home, path[1:])
	}
	if !filepath.IsAbs(path) {
		return "", checkProblem{ProblemKeyPathRelative, fmt.Sprintf("The key file path must be absolute: %s", raw)}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", checkProblem{ProblemKeyMissing, fmt.Sprintf("The key file %s does not exist or cannot be read.", raw)}
	}
	if !info.Mode().IsRegular() {
		return "", checkProblem{ProblemKeyNotAFile, fmt.Sprintf("The key file %s is not a file.", raw)}
	}
	return filepath.Clean(path), checkProblem{}
}

// sshProblem turns an SSH failure into what the developer can do about it.
// LADO never accepts host keys itself.
func sshProblem(host string, err error) checkProblem {
	message := err.Error()
	switch {
	case strings.Contains(message, "Host key verification failed"):
		return checkProblem{ProblemHostKeyUntrusted, fmt.Sprintf("The SSH host key of %s is not trusted yet. Connect once from a terminal with `ssh -T git@%s`, check the fingerprint, then try again.", host, host)}
	case strings.Contains(message, "Permission denied"):
		return checkProblem{ProblemKeyRefused, fmt.Sprintf("%s refused the SSH key. Add the key to your account, or choose the key file %s accepts.", host, host)}
	default:
		return checkProblem{ProblemSSHFailed, message}
	}
}

// findProject returns the first registered project whose origin names repo,
// or that has it as a workspace repository.
func (s *Service) findProject(ctx context.Context, repo domain.TrackerRepo) (*LinkedProject, error) {
	projects, err := s.deps.Projects.ListProjects(ctx)
	if err != nil {
		return nil, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load projects")
	}
	for _, project := range projects {
		if !project.ArchivedAt.IsZero() {
			continue
		}
		name := project.DisplayName
		if name == "" {
			name = project.ID
		}
		if project.Kind == domain.ProjectKindWorkspace {
			children, err := s.deps.Projects.ListWorkspaceRepos(ctx, project.ID)
			if err != nil {
				return nil, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load the workspace's repositories")
			}
			for _, child := range children {
				if sameOrigin(child.RepoOriginURL, repo) {
					return &LinkedProject{
						ID: domain.ProjectID(project.ID), Name: name, Path: project.Path,
						Repository: child.Name, RepoPath: filepath.Join(project.Path, filepath.FromSlash(child.RelativePath)),
					}, nil
				}
			}
			continue
		}
		if sameOrigin(project.RepoOriginURL, repo) {
			return &LinkedProject{ID: domain.ProjectID(project.ID), Name: name, Path: project.Path, RepoPath: project.Path}, nil
		}
	}
	return nil, nil
}

// sameOrigin reports whether an origin remote names repo.
func sameOrigin(origin string, repo domain.TrackerRepo) bool {
	if origin == "" {
		return false
	}
	parsed, err := domain.LadoTrackerRepoFromOrigin(origin)
	return err == nil && domain.LadoSameRepo(parsed, repo)
}

// notReadyMessage names the first thing that blocks a connection.
func notReadyMessage(check CheckResult) string {
	switch {
	case check.Token.Problem != "":
		return check.Token.Problem
	case !tokenCanWorkOn(check.Token.Permissions):
		for _, p := range check.Token.Permissions {
			if p.Status == ports.LadoPermissionMissing && p.Detail != "" {
				return p.Detail
			}
		}
		return "The token cannot work on this repository."
	case !check.SSH.OK:
		return check.SSH.Problem
	}
	return "The connection is not ready."
}
