package gitlab

// LADO fork: issue creation and token checks for LADO (docs/LJY-PRD/TDD.md
// §5.1). They hang off their own type, reached through Tracker.LadoIssues, so
// an upstream issue-write API cannot collide with them. They make their own
// requests because roundTrip hides status codes: a 4xx means GitLab created
// nothing.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ladoDeveloperAccess is GitLab's Developer role, the lowest that may post
// commit statuses.
const ladoDeveloperAccess = 30

// LadoIssues creates issues and checks tokens for LADO.
type LadoIssues struct {
	t *Tracker
}

// LadoIssues returns the tracker's LADO operations.
func (t *Tracker) LadoIssues() *LadoIssues {
	return &LadoIssues{t: t}
}

// errLadoNotSent marks a failure before a request left the daemon.
var errLadoNotSent = errors.New("gitlab tracker: request not sent")

// CreateIssue creates an issue on repo. A 4xx answer, or a failure before
// sending, wraps ports.ErrLadoIssueNotCreated. A 5xx, a dropped connection or
// an unreadable success leaves the outcome unknown.
func (w *LadoIssues) CreateIssue(ctx context.Context, repo domain.TrackerRepo, title, body string) (domain.Issue, error) {
	projectPath, host, err := w.target(repo)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("%w: %w", ports.ErrLadoIssueNotCreated, err)
	}
	answer, err := w.send(ctx, host, http.MethodPost, ladoProjectPath(projectPath)+"/issues", map[string]string{"title": title, "description": body})
	if errors.Is(err, errLadoNotSent) {
		return domain.Issue{}, fmt.Errorf("%w: %w", ports.ErrLadoIssueNotCreated, err)
	}
	if err != nil {
		return domain.Issue{}, err
	}
	if answer.status >= 400 && answer.status < 500 {
		return domain.Issue{}, fmt.Errorf("%w: %w", ports.ErrLadoIssueNotCreated, answer.err())
	}
	if !answer.ok() {
		return domain.Issue{}, answer.err()
	}
	var raw glIssue
	if err := json.Unmarshal(answer.body, &raw); err != nil {
		return domain.Issue{}, fmt.Errorf("gitlab tracker: decode created issue: %w", err)
	}
	if raw.IID <= 0 {
		return domain.Issue{}, errors.New("gitlab tracker: created issue has no iid")
	}
	issue := issueFromGitLab(projectPath, raw)
	issue.ID.Host = repo.Host
	return issue, nil
}

// glLadoProject is the part of a project LADO reads.
type glLadoProject struct {
	DefaultBranch     string `json:"default_branch"`
	IssuesEnabled     *bool  `json:"issues_enabled"`
	IssuesAccessLevel string `json:"issues_access_level"`
	Permissions       struct {
		ProjectAccess *struct {
			AccessLevel int `json:"access_level"`
		} `json:"project_access"`
		GroupAccess *struct {
			AccessLevel int `json:"access_level"`
		} `json:"group_access"`
	} `json:"permissions"`
}

func (p glLadoProject) issuesOff() bool {
	if p.IssuesAccessLevel != "" {
		return p.IssuesAccessLevel == "disabled"
	}
	return p.IssuesEnabled != nil && !*p.IssuesEnabled
}

func (p glLadoProject) accessLevel() int {
	level := 0
	if p.Permissions.ProjectAccess != nil {
		level = p.Permissions.ProjectAccess.AccessLevel
	}
	if p.Permissions.GroupAccess != nil && p.Permissions.GroupAccess.AccessLevel > level {
		level = p.Permissions.GroupAccess.AccessLevel
	}
	return level
}

// CheckAccess reports whose token this is and what it can do on repo. Personal
// access tokens report their scopes; when GitLab does not, writes are
// reported as unverified.
func (w *LadoIssues) CheckAccess(ctx context.Context, repo domain.TrackerRepo) (ports.LadoRepoAccess, error) {
	projectPath, host, err := w.target(repo)
	if err != nil {
		return ports.LadoRepoAccess{}, err
	}
	login, err := w.user(ctx, host)
	if err != nil {
		return ports.LadoRepoAccess{}, err
	}
	access := ports.LadoRepoAccess{Login: login}

	scopes, scopesKnown, err := w.tokenScopes(ctx, host)
	if err != nil {
		return access, err
	}
	projectAPI := ladoProjectPath(projectPath)
	answer, err := w.send(ctx, host, http.MethodGet, projectAPI, nil)
	if err != nil {
		return access, err
	}
	if refused, err := answer.refused(); refused || err != nil {
		if err != nil {
			return access, err
		}
		access.Permissions = []ports.LadoPermission{{
			Name: ports.LadoPermissionRepository, Status: ports.LadoPermissionMissing,
			Detail: fmt.Sprintf("The token cannot read %s.", projectPath),
		}}
		return access, nil
	}
	var project glLadoProject
	if err := json.Unmarshal(answer.body, &project); err != nil {
		return access, fmt.Errorf("gitlab tracker: decode project: %w", err)
	}
	access.DefaultBranch = project.DefaultBranch
	hasAPI := slices.Contains(scopes, "api")
	const unreportedScopes = "GitLab did not report the token's scopes. "

	issues := ports.LadoPermission{Name: ports.LadoPermissionIssues, Status: ports.LadoPermissionOK}
	switch {
	case project.issuesOff():
		issues.Status, issues.Detail = ports.LadoPermissionMissing, "Issues are turned off for this project."
	case !scopesKnown:
		issues.Status, issues.Detail = ports.LadoPermissionUnverified, unreportedScopes+"Creating issues needs the api scope."
	case !hasAPI:
		issues.Status, issues.Detail = ports.LadoPermissionMissing, "The token needs the api scope."
	}

	pulls := ports.LadoPermission{Name: ports.LadoPermissionPullRequests, Status: ports.LadoPermissionOK}
	readsMRs, err := w.probe(ctx, host, projectAPI+"/merge_requests?per_page=1")
	if err != nil {
		return access, err
	}
	if !readsMRs {
		pulls.Status, pulls.Detail = ports.LadoPermissionMissing, "The token cannot read merge requests."
	}

	rules := ports.LadoPermission{Name: ports.LadoPermissionBranchRules, Status: ports.LadoPermissionOK}
	readsRules, err := w.probe(ctx, host, projectAPI+"/protected_branches?per_page=1")
	if err != nil {
		return access, err
	}
	if !readsRules {
		rules.Status, rules.Detail = ports.LadoPermissionMissing, "The token cannot read protected branches."
	}

	statuses := ports.LadoPermission{Name: ports.LadoPermissionCommitStatuses, Status: ports.LadoPermissionOK}
	switch {
	case project.accessLevel() < ladoDeveloperAccess:
		statuses.Status, statuses.Detail = ports.LadoPermissionMissing, "Posting commit statuses needs the Developer role or higher."
	case !scopesKnown:
		statuses.Status, statuses.Detail = ports.LadoPermissionUnverified, unreportedScopes+"Posting commit statuses needs the api scope."
	case !hasAPI:
		statuses.Status, statuses.Detail = ports.LadoPermissionMissing, "The token needs the api scope."
	}

	access.Permissions = []ports.LadoPermission{
		{Name: ports.LadoPermissionRepository, Status: ports.LadoPermissionOK},
		issues, pulls, rules, statuses,
	}
	return access, nil
}

// Login returns the account the default host's token belongs to.
func (w *LadoIssues) Login(ctx context.Context) (string, error) {
	return w.user(ctx, w.t.defaultHost)
}

// user reads the token's account on host. A 401 wraps
// ports.ErrLadoTokenRejected.
func (w *LadoIssues) user(ctx context.Context, host hostEntry) (string, error) {
	answer, err := w.send(ctx, host, http.MethodGet, "/user", nil)
	if err != nil {
		return "", err
	}
	if answer.status == http.StatusUnauthorized {
		return "", fmt.Errorf("%w: %w", ports.ErrLadoTokenRejected, answer.err())
	}
	if !answer.ok() {
		return "", answer.err()
	}
	var who struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(answer.body, &who); err != nil {
		return "", fmt.Errorf("gitlab tracker: decode user: %w", err)
	}
	return who.Username, nil
}

// tokenScopes reads a personal access token's scopes. Older GitLab versions
// and other token kinds do not report them.
func (w *LadoIssues) tokenScopes(ctx context.Context, host hostEntry) ([]string, bool, error) {
	answer, err := w.send(ctx, host, http.MethodGet, "/personal_access_tokens/self", nil)
	if err != nil {
		return nil, false, err
	}
	if !answer.ok() {
		return nil, false, nil
	}
	var token struct {
		Scopes []string `json:"scopes"`
	}
	// An answer LADO cannot read leaves the scopes unknown, as a refusal does.
	readable := json.Unmarshal(answer.body, &token) == nil
	return token.Scopes, readable, nil
}

// probe reports whether a read is allowed.
func (w *LadoIssues) probe(ctx context.Context, host hostEntry, path string) (bool, error) {
	answer, err := w.send(ctx, host, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}
	refused, err := answer.refused()
	return !refused && err == nil, err
}

func (w *LadoIssues) target(repo domain.TrackerRepo) (string, hostEntry, error) {
	if repo.Provider != domain.TrackerProviderGitLab {
		return "", hostEntry{}, fmt.Errorf("%w: provider=%q", ErrWrongProvider, repo.Provider)
	}
	projectPath, err := parseGitLabRepo(repo.Native)
	if err != nil {
		return "", hostEntry{}, err
	}
	host, err := w.t.configForHost(repo.Host)
	if err != nil {
		return "", hostEntry{}, err
	}
	return projectPath, host, nil
}

func ladoProjectPath(projectPath string) string {
	return "/projects/" + url.PathEscape(projectPath)
}

// ladoAnswer is one response's status and body.
type ladoAnswer struct {
	status int
	header http.Header
	body   []byte
}

func (a ladoAnswer) ok() bool {
	return a.status >= 200 && a.status < 300
}

// err classifies a failed answer the way the tracker's own calls do.
func (a ladoAnswer) err() error {
	return classifyError(&http.Response{StatusCode: a.status, Header: a.header}, a.body)
}

// refused reports a 403 or 404: the token may not read this. Other failures
// are errors.
func (a ladoAnswer) refused() (bool, error) {
	if a.ok() {
		return false, nil
	}
	if a.status == http.StatusForbidden || a.status == http.StatusNotFound {
		return true, nil
	}
	return false, a.err()
}

// send makes one request to host with the tracker's headers and the host's
// token.
func (w *LadoIssues) send(ctx context.Context, host hostEntry, method, path string, body any) (ladoAnswer, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return ladoAnswer{}, fmt.Errorf("%w: encode body: %w", errLadoNotSent, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, host.baseURL+path, rdr)
	if err != nil {
		return ladoAnswer{}, fmt.Errorf("%w: build request: %w", errLadoNotSent, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", w.t.userAgent)
	tok, err := host.tokens.Token(ctx)
	if err != nil {
		return ladoAnswer{}, fmt.Errorf("%w: %w", errLadoNotSent, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := w.t.http.Do(req)
	if err != nil {
		return ladoAnswer{}, fmt.Errorf("gitlab tracker: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ladoAnswer{}, fmt.Errorf("gitlab tracker: read response body: %w", err)
	}
	return ladoAnswer{status: resp.StatusCode, header: resp.Header, body: respBody}, nil
}
