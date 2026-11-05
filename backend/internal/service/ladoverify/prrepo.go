package ladoverify

// LADO fork (#59): in a workspace project the PR may be in another child
// repository than the task's issue, as when the issue is in the back end's
// repository and the work only in the front end's. LADO verifies the PR in
// the repository it is open in: the child repository whose origin is the
// PR's, its worktree, its head, its rules and its paths.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// prRepoNotInWorkspaceError says that the PR is in a repository none of the
// workspace project's child repositories has as its origin. The record says
// so (domain.LadoCodePRRepoNotInWorkspace) rather than wait in silence.
type prRepoNotInWorkspaceError struct {
	repo domain.TrackerRepo
}

func (e *prRepoNotInWorkspaceError) Error() string {
	return fmt.Sprintf("no workspace repository has the origin %s of the pull request", domain.LadoRepoRef(e.repo))
}

// verifyPlace is the repository LADO verifies an execution's PR in.
type verifyPlace struct {
	project domain.ProjectRecord
	// repo is the repository verified: the PR's in a workspace project when
	// its URL can be read, else the task's.
	repo domain.TrackerRepo
	// dir is its folder and relative that folder under the project's,
	// slash-separated (empty for a single repository).
	dir, relative string
	// configured is the base branch the workspace configures for the child
	// repository, if any.
	configured string
	// crossRepo says that repo is another repository than the task's.
	crossRepo bool
}

// verifyRepo finds where the PR at prURL of the task's execution session is
// verified. A single-repository project verifies in its folder, the task's
// repository. A workspace project verifies in the child repository whose
// origin is the PR's (domain.LadoMatchWorkspaceRepo: the same repository or,
// for a GitLab instance under a relative URL root, the one child whose path
// ends the PR's, #64) or, when there is no PR or its URL cannot be read
// (logged once per session and URL), the task's issue's, as the dispatcher
// chose. A child matched by its path's end is verified as its origin names
// it: its rules are read and its status posted there. A PR in a repository
// that is no child repository fails with a *prRepoNotInWorkspaceError.
func (s *Service) verifyRepo(ctx context.Context, task domain.LadoTask, session domain.SessionID, prURL string) (verifyPlace, error) {
	project, ok, err := s.deps.Projects.GetProject(ctx, string(task.ProjectID))
	switch {
	case err != nil:
		return verifyPlace{}, fmt.Errorf("read project %s: %w", task.ProjectID, err)
	case !ok:
		return verifyPlace{}, fmt.Errorf("project %s not found", task.ProjectID)
	}
	place := verifyPlace{project: project, repo: task.Repo, dir: project.Path}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return place, nil
	}
	fromPR := false
	if prURL != "" {
		if repo, err := domain.LadoPRRepo(prURL); err == nil {
			place.repo, fromPR = repo, true
			place.crossRepo = !domain.LadoSameRepo(repo, task.Repo)
		} else {
			s.warnUnreadablePR(session, prURL, err)
		}
	}
	children, err := s.deps.Projects.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return verifyPlace{}, fmt.Errorf("read the workspace repositories of project %s: %w", project.ID, err)
	}
	if child, ok := domain.LadoMatchWorkspaceRepo(place.repo, children); ok {
		if repo, err := domain.LadoTrackerRepoFromOrigin(child.RepoOriginURL); err == nil && !domain.LadoSameRepo(repo, place.repo) {
			place.repo = repo
			// The issue's repository may be named under the same root.
			issueChild, found := domain.LadoMatchWorkspaceRepo(task.Repo, children)
			place.crossRepo = fromPR && (!found || issueChild.RelativePath != child.RelativePath)
		}
		place.relative, place.configured = filepath.ToSlash(child.RelativePath), child.DefaultBranch
		place.dir = filepath.Join(project.Path, filepath.FromSlash(child.RelativePath))
		return place, nil
	}
	if fromPR {
		return place, &prRepoNotInWorkspaceError{repo: place.repo}
	}
	return place, fmt.Errorf("no workspace repository has the origin %s", domain.LadoRepoRef(place.repo))
}

// warnUnreadablePR logs, once per session and URL, that LADO could not read
// the repository of a PR's URL and verifies it in the issue's repository.
func (s *Service) warnUnreadablePR(session domain.SessionID, prURL string, err error) {
	key := string(session) + "\x00" + prURL
	s.mu.Lock()
	warned := s.prURLWarned[key]
	s.prURLWarned[key] = true
	s.mu.Unlock()
	if !warned {
		s.deps.Log.Warn("LADO could not read the repository of a pull request's URL; it verifies it in the issue's repository",
			"session", session, "pr", prURL, "err", err)
	}
}

// resolvedBase is a repository's default branch and remote as LADO last
// resolved them, or why it could not.
type resolvedBase struct {
	branch, remote string
	err            error
	at             time.Time
}

// resolveBase resolves the default branch and the remote of the repository
// at dir (Deps.Branches), configured being the branch the workspace names
// for it. The result, a failure too, is kept for baseFetchEvery: the loop
// resolves a repository again only once it is that old, an integration
// approval and its dry run (force) every time. Each resolution is bounded by
// baseResolveTimeout. Without Branches, or when the resolution fails, the
// branch is unknown ("") and the remote is origin.
func (s *Service) resolveBase(ctx context.Context, dir, configured string, force bool) (branch, remote string) {
	if s.deps.Branches == nil {
		return "", "origin"
	}
	now := s.now()
	s.mu.Lock()
	cached, ok := s.baseResolved[dir]
	for other, entry := range s.baseResolved {
		if now.Sub(entry.at) >= baseFetchEvery {
			delete(s.baseResolved, other)
		}
	}
	s.mu.Unlock()
	if !ok || force || now.Sub(cached.at) >= baseFetchEvery {
		cached = s.resolveBaseNow(ctx, dir, configured)
		cached.at = now
		// A stopping daemon's failure says nothing about the repository.
		if ctx.Err() == nil {
			s.mu.Lock()
			s.baseResolved[dir] = cached
			s.mu.Unlock()
		}
		if cached.err != nil && ctx.Err() == nil {
			s.deps.Log.Warn("LADO could not resolve the default branch of a pull request's repository; it does not compare with it",
				"dir", dir, "err", redactedErrorLine(cached.err))
		}
	}
	if cached.err != nil {
		return "", "origin"
	}
	if cached.remote == "" {
		cached.remote = "origin"
	}
	return cached.branch, cached.remote
}

// resolveBaseNow asks Deps.Branches within baseResolveTimeout.
func (s *Service) resolveBaseNow(ctx context.Context, dir, configured string) resolvedBase {
	ctx, cancel := context.WithTimeout(ctx, baseResolveTimeout)
	defer cancel()
	base, err := s.deps.Branches.ResolveDefaultBranch(ctx, dir, configured)
	switch {
	case err != nil:
		return resolvedBase{err: err}
	case base.Branch == "":
		return resolvedBase{err: errors.New("no branch")}
	}
	return resolvedBase{branch: base.Branch, remote: base.Remote}
}
