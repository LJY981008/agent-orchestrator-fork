package gitlab

// LADO fork: a GitLab base branch's protection and the lado/integration
// commit status (docs/LJY-PRD/TDD.md §7.1). GitLab's free tier has no
// external status checks, so LADO guards a branch with a protected branch
// that no one pushes to, "Pipelines must succeed", and a lado/integration
// status on the pipeline the merge request looks at. Refusals keep their
// HTTP status as a ports.LadoHTTPError.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/httpkit"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// ladoProtectedPageSize is the largest page GitLab serves.
	ladoProtectedPageSize = 100
	// ladoProtectedMaxPages caps the pages of protected branches one read
	// follows.
	ladoProtectedMaxPages = 10
	// ladoPipelinePageSize is how many of a commit's newest pipelines a
	// status post looks through for one that is not external.
	ladoPipelinePageSize = 20
	// ladoExternalSource is the source of a pipeline that commit statuses
	// alone made.
	ladoExternalSource = "external"
)

// ladoCommitSHA is a full commit id, SHA-1 or SHA-256, in lower case.
var ladoCommitSHA = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// glLadoAccessLevel is one push or merge access entry of a protected branch.
type glLadoAccessLevel struct {
	AccessLevel int    `json:"access_level"`
	UserID      *int64 `json:"user_id"`
	GroupID     *int64 `json:"group_id"`
	DeployKeyID *int64 `json:"deploy_key_id"`
}

func (a glLadoAccessLevel) access() ports.LadoGitLabAccess {
	return ports.LadoGitLabAccess{
		AccessLevel: a.AccessLevel,
		User:        a.UserID != nil && *a.UserID > 0,
		Group:       a.GroupID != nil && *a.GroupID > 0,
		DeployKey:   a.DeployKeyID != nil && *a.DeployKeyID > 0,
	}
}

// glLadoProtectedBranch is the part of a protected branch entry LADO reads.
type glLadoProtectedBranch struct {
	Name                      string              `json:"name"`
	PushAccessLevels          []glLadoAccessLevel `json:"push_access_levels"`
	MergeAccessLevels         []glLadoAccessLevel `json:"merge_access_levels"`
	AllowForcePush            *bool               `json:"allow_force_push"`
	CodeOwnerApprovalRequired bool                `json:"code_owner_approval_required"`
}

// BranchRules reads how GitLab guards branch on repo: the project's merge
// settings and the token's access on it, the token's scopes, and every
// protected branch entry that matches the branch, following the pages up to
// ladoProtectedMaxPages. The answer has no Rules, only GitLab.
func (w *LadoIssues) BranchRules(ctx context.Context, repo domain.TrackerRepo, branch string) (ports.LadoBranchRules, error) {
	projectPath, host, err := w.target(repo)
	if err != nil {
		return ports.LadoBranchRules{}, err
	}
	if strings.TrimSpace(branch) == "" {
		return ports.LadoBranchRules{}, errors.New("gitlab tracker: no branch to read the protection of")
	}
	project, answer, err := w.readProject(ctx, host, projectPath)
	if err != nil {
		return ports.LadoBranchRules{}, err
	}
	if !answer.ok() {
		return ports.LadoBranchRules{}, fmt.Errorf("gitlab tracker: read project %s: %w", projectPath, answer.httpError())
	}
	scopes, scopesKnown, err := w.tokenScopes(ctx, host)
	if err != nil {
		return ports.LadoBranchRules{}, err
	}
	entries, err := w.protectedBranches(ctx, host, projectPath)
	if err != nil {
		return ports.LadoBranchRules{}, err
	}
	protection := &ports.LadoGitLabProtection{
		PipelineMustSucceed:    project.PipelineMustSucceed,
		SkippedPipelineAllowed: project.SkippedPipelineAllowed,
		AccessLevel:            project.accessLevel(),
		TokenScopes:            scopes,
		ScopesKnown:            scopesKnown,
	}
	for _, entry := range entries {
		if !ladoRefMatches(entry.Name, branch) {
			continue
		}
		protection.Protected = true
		protection.MatchedNames = append(protection.MatchedNames, entry.Name)
		protection.PushAccess = appendLadoAccess(protection.PushAccess, entry.PushAccessLevels)
		protection.MergeAccess = appendLadoAccess(protection.MergeAccess, entry.MergeAccessLevels)
		// GitLab versions before allow_force_push say nothing; LADO reads
		// that as allowed rather than guess the branch is guarded.
		if entry.AllowForcePush == nil || *entry.AllowForcePush {
			protection.AllowForcePush = true
		}
		if entry.CodeOwnerApprovalRequired {
			protection.CodeOwnerApprovalRequired = true
		}
	}
	return ports.LadoBranchRules{GitLab: protection}, nil
}

// protectedBranches lists the project's protected branch entries. A list
// past ladoProtectedMaxPages is no answer, since a partial list could miss
// the entry that guards the branch.
func (w *LadoIssues) protectedBranches(ctx context.Context, host hostEntry, projectPath string) ([]glLadoProtectedBranch, error) {
	first := fmt.Sprintf("%s/protected_branches?per_page=%d", ladoProjectPath(projectPath), ladoProtectedPageSize)
	var entries []glLadoProtectedBranch
	path := first
	for page := 1; ; page++ {
		answer, err := w.send(ctx, host, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		if !answer.ok() {
			return nil, fmt.Errorf("gitlab tracker: read the protected branches of %s: %w", projectPath, answer.httpError())
		}
		var raw []glLadoProtectedBranch
		if err := json.Unmarshal(answer.body, &raw); err != nil {
			return nil, fmt.Errorf("gitlab tracker: decode protected branches: %w", err)
		}
		entries = append(entries, raw...)
		next := ladoNextPage(answer.header, host.baseURL, first)
		if next == "" {
			return entries, nil
		}
		if page == ladoProtectedMaxPages {
			return nil, fmt.Errorf("gitlab tracker: the protected branches of %s run past %d pages", projectPath, ladoProtectedMaxPages)
		}
		path = next
	}
}

// ladoRefMatches reports whether a protected branch name matches branch as
// GitLab's RefMatcher does: the name itself, or a wildcard where each *
// stands for any text, slashes included.
func ladoRefMatches(name, branch string) bool {
	if !strings.Contains(name, "*") {
		return name == branch
	}
	pattern := strings.ReplaceAll(regexp.QuoteMeta(name), `\*`, `.*`)
	matched, err := regexp.MatchString(`\A`+pattern+`\z`, branch)
	return err == nil && matched
}

// appendLadoAccess adds the entries not already in list.
func appendLadoAccess(list []ports.LadoGitLabAccess, entries []glLadoAccessLevel) []ports.LadoGitLabAccess {
	for _, entry := range entries {
		access := entry.access()
		known := false
		for _, have := range list {
			if have == access {
				known = true
				break
			}
		}
		if !known {
			list = append(list, access)
		}
	}
	return list
}

// glLadoPipeline is the part of a pipeline LADO reads.
type glLadoPipeline struct {
	ID     int64  `json:"id"`
	SHA    string `json:"sha"`
	Source string `json:"source"`
}

// glLadoStatus is the body of a commit status. GitLab takes either the
// pipeline to join or the ref whose newest pipeline for the commit it joins,
// making an external pipeline when there is none.
type glLadoStatus struct {
	State       string `json:"state"`
	Name        string `json:"name"`
	Description string `json:"description"`
	TargetURL   string `json:"target_url,omitempty"`
	PipelineID  int64  `json:"pipeline_id,omitempty"`
	Ref         string `json:"ref,omitempty"`
}

// PostCommitStatus posts status on the commit sha, on the pipeline the merge
// request looks at. With status.MergeRequest it is the merge request's head
// pipeline when that is for sha; else the commit's newest pipeline that is
// not external, else its newest pipeline. A merge request event pipeline's
// ref is refs/merge-requests/<iid>/head, so a status sent with the source
// branch as ref would miss it and start an external pipeline of its own;
// LADO names the pipeline instead and sends no ref. Only a commit with no
// pipeline gets ref = status.Ref, which makes an external pipeline. A post
// GitLab refuses because the status is already in that state counts as
// success.
func (w *LadoIssues) PostCommitStatus(ctx context.Context, repo domain.TrackerRepo, sha string, status ports.LadoCommitStatus) error {
	projectPath, host, err := w.target(repo)
	if err != nil {
		return err
	}
	if !ladoCommitSHA.MatchString(sha) {
		return fmt.Errorf("gitlab tracker: %q is not a commit SHA", sha)
	}
	if status.Ref == "" && status.MergeRequest <= 0 {
		return errors.New("gitlab tracker: a commit status needs the merge request or its source branch")
	}
	pipelineID, err := w.statusPipeline(ctx, host, projectPath, sha, status.MergeRequest)
	if err != nil {
		return err
	}
	body := glLadoStatus{
		State:       ladoGitLabState(status.State),
		Name:        status.Context,
		Description: status.Description,
		TargetURL:   status.TargetURL,
		PipelineID:  pipelineID,
	}
	if pipelineID == 0 {
		if status.Ref == "" {
			return fmt.Errorf("gitlab tracker: commit %s has no pipeline and the source branch is unknown", sha)
		}
		body.Ref = status.Ref
	}
	answer, err := w.send(ctx, host, http.MethodPost, fmt.Sprintf("%s/statuses/%s", ladoProjectPath(projectPath), sha), body)
	if err != nil {
		return err
	}
	switch {
	case answer.status == http.StatusOK || answer.status == http.StatusCreated:
		return nil
	case answer.status == http.StatusBadRequest && answer.alreadyInState():
		return nil
	}
	return fmt.Errorf("gitlab tracker: post the %s status: %w", status.Context, answer.httpError())
}

// statusPipeline picks the pipeline a status on sha joins, or 0 when the
// commit has none.
func (w *LadoIssues) statusPipeline(ctx context.Context, host hostEntry, projectPath, sha string, mergeRequest int) (int64, error) {
	if mergeRequest > 0 {
		answer, err := w.send(ctx, host, http.MethodGet, fmt.Sprintf("%s/merge_requests/%d", ladoProjectPath(projectPath), mergeRequest), nil)
		if err != nil {
			return 0, err
		}
		if !answer.ok() {
			return 0, fmt.Errorf("gitlab tracker: read merge request !%d: %w", mergeRequest, answer.httpError())
		}
		var mr struct {
			HeadPipeline *glLadoPipeline `json:"head_pipeline"`
		}
		if err := json.Unmarshal(answer.body, &mr); err != nil {
			return 0, fmt.Errorf("gitlab tracker: decode merge request: %w", err)
		}
		if mr.HeadPipeline != nil && mr.HeadPipeline.ID > 0 && strings.EqualFold(mr.HeadPipeline.SHA, sha) {
			return mr.HeadPipeline.ID, nil
		}
	}
	query := url.Values{}
	query.Set("sha", sha)
	query.Set("order_by", "id")
	query.Set("sort", "desc")
	query.Set("per_page", fmt.Sprint(ladoPipelinePageSize))
	answer, err := w.send(ctx, host, http.MethodGet, ladoProjectPath(projectPath)+"/pipelines?"+query.Encode(), nil)
	if err != nil {
		return 0, err
	}
	if !answer.ok() {
		return 0, fmt.Errorf("gitlab tracker: read the pipelines of %s: %w", sha, answer.httpError())
	}
	var pipelines []glLadoPipeline
	if err := json.Unmarshal(answer.body, &pipelines); err != nil {
		return 0, fmt.Errorf("gitlab tracker: decode pipelines: %w", err)
	}
	for _, pipeline := range pipelines {
		if pipeline.ID > 0 && pipeline.Source != ladoExternalSource {
			return pipeline.ID, nil
		}
	}
	// Only external pipelines: join the newest, which LADO's own earlier
	// status may have made, rather than start another.
	for _, pipeline := range pipelines {
		if pipeline.ID > 0 {
			return pipeline.ID, nil
		}
	}
	return 0, nil
}

// ladoGitLabState is GitLab's name for a commit status state; GitLab calls
// GitHub's failure and error failed.
func ladoGitLabState(state string) string {
	switch state {
	case "failure", "error":
		return "failed"
	}
	return state
}

// alreadyInState reports GitLab's refusal of a status that is already in the
// state posted, such as "Cannot transition status via :enqueue from
// :pending".
func (a ladoAnswer) alreadyInState() bool {
	const refusal = "Cannot transition status"
	return strings.Contains(httpkit.Message(a.body), refusal) || strings.Contains(string(a.body), refusal)
}

// httpError is the answer as a ports.LadoHTTPError, with the token cut out of
// GitLab's message in case something on the way echoed it.
func (a ladoAnswer) httpError() *ports.LadoHTTPError {
	message := httpkit.Message(a.body)
	if a.token != "" {
		message = strings.ReplaceAll(message, a.token, "[redacted]")
	}
	return &ports.LadoHTTPError{Status: a.status, Message: message}
}
