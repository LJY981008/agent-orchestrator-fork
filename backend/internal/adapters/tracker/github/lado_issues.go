package github

// LADO fork: issue creation and token checks for LADO (docs/LJY-PRD/TDD.md
// §5.1). They hang off their own type, reached through Tracker.LadoIssues, so
// an upstream issue-write API (upstream issue #40) cannot collide with them.
// They make their own requests because roundTrip hides status codes and
// headers, and LADO needs both: a 4xx means GitHub created nothing, and
// X-OAuth-Scopes lists a classic token's scopes.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LadoIssues creates issues and checks tokens for LADO.
type LadoIssues struct {
	t *Tracker
}

// LadoIssues returns the tracker's LADO operations.
func (t *Tracker) LadoIssues() *LadoIssues {
	return &LadoIssues{t: t}
}

// errLadoNotSent marks a failure before a request left the daemon.
var errLadoNotSent = errors.New("github tracker: request not sent")

// CreateIssue creates an issue on repo. A 4xx answer, or a failure before
// sending, wraps ports.ErrLadoIssueNotCreated. A 5xx, a dropped connection or
// an unreadable success leaves the outcome unknown.
func (w *LadoIssues) CreateIssue(ctx context.Context, repo domain.TrackerRepo, title, body string) (domain.Issue, error) {
	owner, name, err := ladoRepoParts(repo)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("%w: %w", ports.ErrLadoIssueNotCreated, err)
	}
	answer, err := w.send(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues", owner, name), map[string]string{"title": title, "body": body})
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
	var raw ghIssue
	if err := json.Unmarshal(answer.body, &raw); err != nil {
		return domain.Issue{}, fmt.Errorf("github tracker: decode created issue: %w", err)
	}
	if raw.Number <= 0 {
		return domain.Issue{}, errors.New("github tracker: created issue has no number")
	}
	return issueFromGH(owner, name, raw), nil
}

// CheckAccess reports whose token this is and what it can do on repo. A
// classic token lists its scopes; a fine-grained token does not, so reads are
// probed and writes are reported as unverified.
func (w *LadoIssues) CheckAccess(ctx context.Context, repo domain.TrackerRepo) (ports.LadoRepoAccess, error) {
	owner, name, err := ladoRepoParts(repo)
	if err != nil {
		return ports.LadoRepoAccess{}, err
	}
	login, user, err := w.user(ctx)
	if err != nil {
		return ports.LadoRepoAccess{}, err
	}
	access := ports.LadoRepoAccess{Login: login}
	scopes, classic := ladoTokenScopes(user.header)

	repoPath := fmt.Sprintf("/repos/%s/%s", owner, name)
	readable, info, err := w.repository(ctx, repoPath)
	if err != nil {
		return access, err
	}
	if !readable {
		access.Permissions = []ports.LadoPermission{{
			Name: ports.LadoPermissionRepository, Status: ports.LadoPermissionMissing,
			Detail: fmt.Sprintf("The token cannot read %s/%s.", owner, name),
		}}
		return access, nil
	}
	access.DefaultBranch = info.DefaultBranch
	scopeAllows := func(public string) bool {
		return scopes["repo"] || (!info.Private && public != "" && scopes[public])
	}

	issues := ports.LadoPermission{Name: ports.LadoPermissionIssues, Status: ports.LadoPermissionOK}
	switch {
	case !info.HasIssues:
		issues.Status, issues.Detail = ports.LadoPermissionMissing, "Issues are turned off for this repository."
	case classic:
		if !scopeAllows("public_repo") {
			issues.Status, issues.Detail = ports.LadoPermissionMissing, ladoScopeDetail(info.Private, "public_repo")
		}
	default:
		readsIssues, err := w.probe(ctx, repoPath+"/issues?per_page=1")
		if err != nil {
			return access, err
		}
		if readsIssues {
			issues.Status, issues.Detail = ports.LadoPermissionUnverified, "GitHub does not say whether a fine-grained token can create issues. It needs Issues: Read and write."
		} else {
			issues.Status, issues.Detail = ports.LadoPermissionMissing, "The token needs Issues: Read and write."
		}
	}

	pulls := ports.LadoPermission{Name: ports.LadoPermissionPullRequests, Status: ports.LadoPermissionOK}
	switch {
	case classic:
		if info.Private && !scopes["repo"] {
			pulls.Status, pulls.Detail = ports.LadoPermissionMissing, ladoScopeDetail(true, "")
		}
	default:
		readsPulls, err := w.probe(ctx, repoPath+"/pulls?per_page=1")
		if err != nil {
			return access, err
		}
		if !readsPulls {
			pulls.Status, pulls.Detail = ports.LadoPermissionMissing, "The token needs Pull requests: Read."
		}
	}

	rules := ports.LadoPermission{Name: ports.LadoPermissionBranchRules, Status: ports.LadoPermissionOK}
	readsRules, err := w.probe(ctx, repoPath+"/rules/branches/"+(&url.URL{Path: info.DefaultBranch}).EscapedPath())
	if err != nil {
		return access, err
	}
	if !readsRules {
		rules.Status, rules.Detail = ports.LadoPermissionMissing, fmt.Sprintf("The token cannot read the branch rules of %s.", info.DefaultBranch)
	}

	statuses := ports.LadoPermission{Name: ports.LadoPermissionCommitStatuses, Status: ports.LadoPermissionOK}
	switch {
	case !info.Permissions.Push:
		statuses.Status, statuses.Detail = ports.LadoPermissionMissing, "Posting commit statuses needs write access to the repository."
	case classic:
		if !scopeAllows("public_repo") && !scopes["repo:status"] {
			statuses.Status, statuses.Detail = ports.LadoPermissionMissing, "The token needs the repo:status scope."
		}
	default:
		statuses.Status, statuses.Detail = ports.LadoPermissionUnverified, "GitHub does not say whether a fine-grained token can post commit statuses. It needs Commit statuses: Read and write."
	}

	access.Permissions = []ports.LadoPermission{
		{Name: ports.LadoPermissionRepository, Status: ports.LadoPermissionOK},
		issues, pulls, rules, statuses,
	}
	return access, nil
}

// Login returns the account the token belongs to.
func (w *LadoIssues) Login(ctx context.Context) (string, error) {
	login, _, err := w.user(ctx)
	return login, err
}

// user reads the token's account. A 401 wraps ports.ErrLadoTokenRejected.
func (w *LadoIssues) user(ctx context.Context) (string, ladoAnswer, error) {
	answer, err := w.send(ctx, http.MethodGet, "/user", nil)
	if err != nil {
		return "", ladoAnswer{}, err
	}
	if answer.status == http.StatusUnauthorized {
		return "", ladoAnswer{}, fmt.Errorf("%w: %w", ports.ErrLadoTokenRejected, answer.err())
	}
	if !answer.ok() {
		return "", ladoAnswer{}, answer.err()
	}
	var who struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(answer.body, &who); err != nil {
		return "", ladoAnswer{}, fmt.Errorf("github tracker: decode user: %w", err)
	}
	return who.Login, answer, nil
}

// ghLadoRepository is the part of a repository LADO reads.
type ghLadoRepository struct {
	Private       bool   `json:"private"`
	HasIssues     bool   `json:"has_issues"`
	DefaultBranch string `json:"default_branch"`
	Permissions   struct {
		Push bool `json:"push"`
	} `json:"permissions"`
}

// repository reads a repository, reporting false when the token cannot see it.
func (w *LadoIssues) repository(ctx context.Context, path string) (bool, ghLadoRepository, error) {
	answer, err := w.send(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, ghLadoRepository{}, err
	}
	if refused, err := answer.refused(); refused || err != nil {
		return false, ghLadoRepository{}, err
	}
	var info ghLadoRepository
	if err := json.Unmarshal(answer.body, &info); err != nil {
		return false, ghLadoRepository{}, fmt.Errorf("github tracker: decode repository: %w", err)
	}
	return true, info, nil
}

// probe reports whether a read is allowed.
func (w *LadoIssues) probe(ctx context.Context, path string) (bool, error) {
	answer, err := w.send(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}
	refused, err := answer.refused()
	return !refused && err == nil, err
}

func ladoRepoParts(repo domain.TrackerRepo) (string, string, error) {
	if repo.Provider != domain.TrackerProviderGitHub {
		return "", "", fmt.Errorf("%w: provider=%q", ErrWrongProvider, repo.Provider)
	}
	return parseGitHubRepo(repo.Native)
}

// ladoTokenScopes returns a classic token's scopes. GitHub sends
// X-OAuth-Scopes, possibly empty, for classic tokens only.
func ladoTokenScopes(header http.Header) (map[string]bool, bool) {
	values, classic := header[http.CanonicalHeaderKey("X-OAuth-Scopes")]
	scopes := map[string]bool{}
	for _, value := range values {
		for _, scope := range strings.Split(value, ",") {
			if scope = strings.TrimSpace(scope); scope != "" {
				scopes[scope] = true
			}
		}
	}
	return scopes, classic
}

func ladoScopeDetail(private bool, public string) string {
	if private || public == "" {
		return "The token needs the repo scope."
	}
	return "The token needs the " + public + " scope."
}

// ladoAnswer is one response's status, headers and body, and the token sent,
// so an error can leave it out.
type ladoAnswer struct {
	status int
	header http.Header
	body   []byte
	token  string
}

func (a ladoAnswer) ok() bool {
	return a.status >= 200 && a.status < 300
}

// err classifies a failed answer the way the tracker's own calls do.
func (a ladoAnswer) err() error {
	return classifyError(&http.Response{StatusCode: a.status, Header: a.header}, a.body)
}

// refused reports a 403 or 404 that is not a rate limit: the token may not
// read this. Other failures are errors.
func (a ladoAnswer) refused() (bool, error) {
	if a.ok() {
		return false, nil
	}
	err := a.err()
	if (a.status == http.StatusForbidden || a.status == http.StatusNotFound) && !errors.Is(err, ErrRateLimited) {
		return true, nil
	}
	return false, err
}

// send makes one request with the tracker's headers and token.
func (w *LadoIssues) send(ctx context.Context, method, path string, body any) (ladoAnswer, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return ladoAnswer{}, fmt.Errorf("%w: encode body: %w", errLadoNotSent, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, w.t.baseURL+path, rdr)
	if err != nil {
		return ladoAnswer{}, fmt.Errorf("%w: build request: %w", errLadoNotSent, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", w.t.userAgent)
	tok, err := w.t.tokens.Token(ctx)
	if err != nil {
		return ladoAnswer{}, fmt.Errorf("%w: %w", errLadoNotSent, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := w.t.http.Do(req)
	if err != nil {
		return ladoAnswer{}, fmt.Errorf("github tracker: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ladoAnswer{}, fmt.Errorf("github tracker: read response body: %w", err)
	}
	return ladoAnswer{status: resp.StatusCode, header: resp.Header, body: respBody, token: tok}, nil
}
