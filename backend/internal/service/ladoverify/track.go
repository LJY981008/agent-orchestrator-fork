package ladoverify

// LADO fork (#64): an execution tracks one PR per repository it changes,
// pinned in lado_execution_prs rather than read again as the session's
// newest PR, so a later update of another PR never moves what LADO
// verifies and approves. Each pass starts by pinning the PRs the executions'
// sessions opened since: the PR's repository is the workspace's child
// repository whose origin is the PR's (domain.LadoMatchWorkspaceRepo), ""
// in a single-repository project, and its place in the merge order is the
// plan's repos order. A repository keeps the PR pinned first: a second PR
// opened there while that one is open stays untracked, also in a
// single-repository project. A pinned PR the worker closed without merging
// gives way to a newer PR of the same repository.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// trackBatch caps the executions one pass pins PRs for. The store keeps
	// listing an execution while its session has a PR LADO leaves
	// untracked on purpose, such as a second open PR of a repository, so the
	// cap is well above the executions a pass verifies; such an execution
	// costs no write.
	trackBatch = 4 * verifyBatch
	// ladoAuditPRReplaced is the audit action of a tracked PR that gave way
	// to a newer one of its repository.
	ladoAuditPRReplaced = "execution.pr_replaced"
)

// trackPRs pins the PRs of the executions with PRs LADO does not track yet
// (trackExecution). What fails for one execution is returned, joined with
// the others, after the rest ran, and that execution waits failedRetryAfter
// before it is tried again.
func (s *Service) trackPRs(ctx context.Context) error {
	executions, err := s.deps.Store.ListLadoExecutionPRsToTrack(ctx, trackBatch)
	if err != nil {
		return fmt.Errorf("list the executions with pull requests to track: %w", err)
	}
	now := s.now()
	resting := map[domain.SessionID]bool{}
	s.mu.Lock()
	for session, at := range s.trackFailedAt {
		if now.Sub(at) < failedRetryAfter {
			resting[session] = true
		} else {
			delete(s.trackFailedAt, session)
		}
	}
	s.mu.Unlock()
	matchers := map[domain.LadoTaskID]prRepoMatcher{}
	var problems []error
	for _, exec := range executions {
		if ctx.Err() != nil {
			break
		}
		if resting[exec.SessionID] {
			continue
		}
		err := s.trackExecution(ctx, exec, matchers)
		if ctx.Err() != nil {
			break
		}
		s.mu.Lock()
		if err != nil {
			s.trackFailedAt[exec.SessionID] = now
		} else {
			delete(s.trackFailedAt, exec.SessionID)
		}
		s.mu.Unlock()
		if err != nil {
			problems = append(problems, fmt.Errorf("track the pull requests of execution %s: %w", exec.SessionID, err))
		}
	}
	return errors.Join(problems...)
}

// trackExecution pins the untracked PRs of one execution. A tracked PR that
// closed without merging is replaced first by the newest untracked PR of its
// repository with a higher number, with an audit event. Then each untracked
// PR, the session's newest first, is pinned in its repository unless that
// repository has a live tracked PR already: the newest PR of a repository
// wins, and the others stay untracked. A PR whose URL LADO cannot read in a
// workspace project is left untracked (logged once); one in a repository
// that is none of the workspace's is tracked under that repository's
// reference, so its record says so.
func (s *Service) trackExecution(ctx context.Context, exec domain.LadoExecution, matchers map[domain.LadoTaskID]prRepoMatcher) error {
	matcher, ok := matchers[exec.TaskID]
	if !ok {
		var err error
		if matcher, err = s.prRepoMatcher(ctx, exec.TaskID); err != nil {
			return err
		}
		matchers[exec.TaskID] = matcher
	}
	type candidate struct {
		pr   domain.LadoExecutionPR
		repo string
	}
	var candidates []candidate
	for _, pr := range exec.UntrackedPRs {
		repo, ok := matcher.key(pr.URL)
		if !ok {
			s.warnUnreadablePR(exec.SessionID, pr.URL, errors.New("its repository cannot be read from its URL"))
			continue
		}
		candidates = append(candidates, candidate{pr: pr, repo: repo})
	}
	live := map[string]bool{}
	maxPosition := -1
	for _, tracked := range exec.PRs {
		live[ladoRepoKey(tracked.Repo)] = true
		maxPosition = max(maxPosition, tracked.Position)
	}
	used := map[string]bool{}
	now := s.now()

	for _, tracked := range exec.PRs {
		if !tracked.PR.Closed || tracked.PR.Merged {
			continue
		}
		newer := -1
		for i, c := range candidates {
			if c.repo == ladoRepoKey(tracked.Repo) && !used[c.pr.URL] && c.pr.Number > tracked.PR.Number &&
				(newer < 0 || c.pr.Number > candidates[newer].pr.Number) {
				newer = i
			}
		}
		if newer < 0 {
			continue
		}
		next := candidates[newer].pr
		payload, err := json.Marshal(map[string]any{
			"task": exec.TaskID, "session": exec.SessionID, "repo": tracked.Repo, "old": tracked.PR.URL, "new": next.URL,
		})
		if err != nil {
			return err
		}
		replaced, err := s.deps.Store.ReplaceLadoExecutionPR(ctx, exec.SessionID, tracked.PR.URL, next.URL, now, &domain.LadoAuditEvent{
			At: now, Actor: "lado", Action: ladoAuditPRReplaced, SubjectKind: "execution", SubjectID: string(exec.SessionID), Payload: payload,
		})
		if err != nil {
			return fmt.Errorf("replace pull request %s with %s: %w", tracked.PR.URL, next.URL, err)
		}
		used[next.URL] = true
		if replaced {
			s.deps.Log.Info("LADO tracks a newer pull request of a repository whose tracked one closed",
				"session", exec.SessionID, "repo", tracked.Repo, "old", tracked.PR.URL, "new", next.URL)
		}
	}

	// A repository the plan orders takes its place in that order; the
	// others come after them, in the order they are tracked.
	position := func(repo string) int {
		if i := slices.IndexFunc(exec.PRRepos, func(r string) bool { return ladoRepoKey(r) == repo }); i >= 0 {
			maxPosition = max(maxPosition, i)
			return i
		}
		maxPosition = max(maxPosition+1, len(exec.PRRepos))
		return maxPosition
	}
	for _, c := range candidates {
		if used[c.pr.URL] || live[c.repo] {
			continue
		}
		tracked, err := s.deps.Store.TrackLadoExecutionPR(ctx, exec.SessionID, c.pr.URL, c.repo, position(c.repo), now)
		if err != nil {
			return fmt.Errorf("track pull request %s: %w", c.pr.URL, err)
		}
		live[c.repo] = true
		if tracked {
			s.deps.Log.Info("LADO tracks a pull request of an execution", "session", exec.SessionID, "pr", c.pr.URL, "repo", c.repo)
		}
	}
	return nil
}

// prRepoMatcher tells the repository key of a task's PRs: "" in a
// single-repository project, the child repository's folder in a workspace
// project.
type prRepoMatcher struct {
	workspace bool
	children  []domain.WorkspaceRepoRecord
}

// prRepoMatcher reads the task's project and, for a workspace project, its
// child repositories.
func (s *Service) prRepoMatcher(ctx context.Context, taskID domain.LadoTaskID) (prRepoMatcher, error) {
	task, ok, err := s.deps.Tasks.LadoTask(ctx, taskID)
	switch {
	case err != nil:
		return prRepoMatcher{}, fmt.Errorf("read task %s: %w", taskID, err)
	case !ok:
		return prRepoMatcher{}, fmt.Errorf("task %s not found", taskID)
	}
	project, ok, err := s.deps.Projects.GetProject(ctx, string(task.ProjectID))
	switch {
	case err != nil:
		return prRepoMatcher{}, fmt.Errorf("read project %s: %w", task.ProjectID, err)
	case !ok:
		return prRepoMatcher{}, fmt.Errorf("project %s not found", task.ProjectID)
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return prRepoMatcher{}, nil
	}
	children, err := s.deps.Projects.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return prRepoMatcher{}, fmt.Errorf("read the workspace repositories of project %s: %w", project.ID, err)
	}
	return prRepoMatcher{workspace: true, children: children}, nil
}

// key returns the repository key of the PR at prURL: "" in a
// single-repository project; in a workspace project the folder of the child
// repository the PR is open in, or, for a repository that is none of the
// children, its lower-case reference (which holds a ":", so no folder
// matches it). ok is false when the URL cannot be read in a workspace
// project.
func (m prRepoMatcher) key(prURL string) (string, bool) {
	if !m.workspace {
		return "", true
	}
	repo, err := domain.LadoPRRepo(prURL)
	if err != nil {
		return "", false
	}
	if child, ok := domain.LadoMatchWorkspaceRepo(repo, m.children); ok {
		return ladoRepoKey(filepath.ToSlash(child.RelativePath)), true
	}
	return strings.ToLower(domain.LadoRepoRef(repo)), true
}

// ladoRepoKey is a repository folder as lado_execution_prs keeps it,
// without a leading or trailing "/".
func ladoRepoKey(relative string) string {
	return strings.Trim(relative, "/")
}
