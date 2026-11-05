package gitworktree

// LADO (#60): a workspace-project session that reuses the branch its caller
// already created. LADO makes each part, merge, integration and resolve branch
// in the issue's child repo before it spawns the worker. The default workspace
// branch choice would see that branch and pick "<branch>-2" instead, so the
// worker committed somewhere LADO never merged. Kept in its own file so an
// upstream sync only has to keep the short hook in CreateWorkspaceProject.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ladoReuseWorkspaceBranch returns branch unchanged after checking that every
// repo can use it: it is a valid name everywhere, at least one repo holds the
// local branch, and no repo has it checked out at another path. Repos that hold
// the branch check out its commit; the others create it from their base, as an
// ordinary workspace session would.
func (w *Workspace) ladoReuseWorkspaceBranch(ctx context.Context, repos []workspaceProjectRepo, requested string, fresh bool) (string, error) {
	if fresh {
		return "", errors.New("gitworktree: ReuseExistingBranch cannot be combined with FreshBranch")
	}
	branch := strings.TrimSpace(requested)
	if branch == "" {
		return "", errors.New("gitworktree: branch is required")
	}
	found := false
	for _, repo := range repos {
		if err := w.validateBranch(ctx, repo.repoPath, branch); err != nil {
			return "", err
		}
		exists, err := w.refExists(ctx, repo.repoPath, "refs/heads/"+branch)
		if err != nil {
			return "", err
		}
		found = found || exists
		records, err := w.listRecords(ctx, repo.repoPath)
		if err != nil {
			return "", err
		}
		if conflict, ok := findWorktreeByBranch(records, branch); ok && filepath.Clean(conflict.Path) != filepath.Clean(repo.outputPath) {
			return "", fmt.Errorf("%w: %q is checked out at %q in workspace repo %q", ErrBranchCheckedOutElsewhere, branch, conflict.Path, repo.name)
		}
	}
	if !found {
		return "", fmt.Errorf("gitworktree: reuse branch %q: no workspace repository has this local branch", branch)
	}
	return branch, nil
}
