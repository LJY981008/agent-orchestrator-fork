package ladoqueue

// The git writes of a parts run (docs/LJY-PRD/TDD.md §5.4). LADO writes only
// branches under refs/heads/lado/<task>/ and commits it merges there. It
// merges without a worktree or a checkout: git merge-tree computes the merged
// tree in the object store and git commit-tree makes the merge commit, so no
// branch is ever checked out by LADO and no hook of the repository runs. The
// base branch and every other branch stay as they are.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// partGitTimeout bounds one git call of a parts run; a merge of a large tree
// takes longer than a read.
const partGitTimeout = 2 * time.Minute

// ladoRefPrefix is the only ref namespace LADO writes.
const ladoRefPrefix = "refs/heads/lado/"

// ladoMergeAuthor is who LADO's merge commits name.
const (
	ladoMergeName  = "LADO"
	ladoMergeEmail = "lado@localhost"
)

// errMergeConflict means the part's branch does not merge into the
// integration branch without a conflict.
var errMergeConflict = errors.New("the merge has conflicts")

// PartGit writes the branches of parts runs.
type PartGit interface {
	// CreateBranch makes branch name point at sha. A branch that already
	// points there is fine; one that points elsewhere is an error.
	CreateBranch(ctx context.Context, dir, name, sha string) error
	// MoveBranch moves branch name from old to new, only while it still
	// points at old.
	MoveBranch(ctx context.Context, dir, name, newSHA, oldSHA string) error
	// Merge makes a merge commit of tip into head with message, without
	// moving any branch, and returns it; errMergeConflict when the two
	// conflict.
	Merge(ctx context.Context, dir, head, tip, message string) (string, error)
	// IsAncestor reports whether ancestor is an ancestor of, or the same
	// commit as, descendant.
	IsAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error)
	// Parents returns the parents of a commit, first parent first.
	Parents(ctx context.Context, dir, sha string) ([]string, error)
	// BranchSHA returns the commit a branch points at, "" when it does not
	// exist.
	BranchSHA(ctx context.Context, dir, name string) (string, error)
}

// execPartGit runs the system git with every prompt off.
type execPartGit struct{}

func ladoRef(name string) (string, error) {
	ref := "refs/heads/" + name
	if !strings.HasPrefix(ref, ladoRefPrefix) || strings.Contains(name, "..") || strings.ContainsAny(name, " ~^:?*[\\") {
		return "", fmt.Errorf("LADO writes only branches under lado/, not %q", name)
	}
	return ref, nil
}

func (execPartGit) BranchSHA(ctx context.Context, dir, name string) (string, error) {
	ref, err := ladoRef(name)
	if err != nil {
		return "", err
	}
	out, err := runPartGit(ctx, dir, nil, "for-each-ref", "--format=%(objectname)", "--end-of-options", ref)
	if err != nil {
		return "", fmt.Errorf("read branch %s: %w", name, err)
	}
	return out, nil
}

func (g execPartGit) CreateBranch(ctx context.Context, dir, name, sha string) error {
	ref, err := ladoRef(name)
	if err != nil {
		return err
	}
	if !commitID.MatchString(sha) {
		return fmt.Errorf("create branch %s: %q is not a commit id", name, sha)
	}
	current, err := g.BranchSHA(ctx, dir, name)
	if err != nil {
		return err
	}
	switch current {
	case sha:
		return nil
	case "":
	default:
		return fmt.Errorf("branch %s already points at %s, not %s", name, current, sha)
	}
	// The empty old value creates the ref only while it does not exist.
	if _, err := runPartGit(ctx, dir, nil, "update-ref", "-m", "LADO: start "+name, "--end-of-options", ref, sha, ""); err != nil {
		return fmt.Errorf("create branch %s: %w", name, err)
	}
	return nil
}

func (execPartGit) MoveBranch(ctx context.Context, dir, name, newSHA, oldSHA string) error {
	ref, err := ladoRef(name)
	if err != nil {
		return err
	}
	if !commitID.MatchString(newSHA) || !commitID.MatchString(oldSHA) {
		return fmt.Errorf("move branch %s: not commit ids", name)
	}
	if _, err := runPartGit(ctx, dir, nil, "update-ref", "-m", "LADO: integrate", "--end-of-options", ref, newSHA, oldSHA); err != nil {
		return fmt.Errorf("move branch %s from %s to %s: %w", name, oldSHA, newSHA, err)
	}
	return nil
}

func (execPartGit) Merge(ctx context.Context, dir, head, tip, message string) (string, error) {
	if !commitID.MatchString(head) || !commitID.MatchString(tip) {
		return "", errors.New("merge: not commit ids")
	}
	tree, err := runPartGit(ctx, dir, nil, "merge-tree", "--write-tree", "--no-messages", "--name-only", "--end-of-options", head, tip)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", errMergeConflict
	}
	if err != nil {
		return "", fmt.Errorf("merge %s into %s: %w", tip, head, err)
	}
	tree, _, _ = strings.Cut(tree, "\n")
	if !commitID.MatchString(tree) {
		return "", fmt.Errorf("merge %s into %s: git gave no tree", tip, head)
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + ladoMergeName, "GIT_AUTHOR_EMAIL=" + ladoMergeEmail,
		"GIT_COMMITTER_NAME=" + ladoMergeName, "GIT_COMMITTER_EMAIL=" + ladoMergeEmail,
	}
	sha, err := runPartGit(ctx, dir, env, "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", head, "-p", tip, "-m", message)
	if err != nil {
		return "", fmt.Errorf("commit the merge of %s into %s: %w", tip, head, err)
	}
	if !commitID.MatchString(sha) {
		return "", fmt.Errorf("commit the merge of %s into %s: git gave no commit", tip, head)
	}
	return sha, nil
}

func (execPartGit) IsAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	if !commitID.MatchString(ancestor) || !commitID.MatchString(descendant) {
		return false, errors.New("is-ancestor: not commit ids")
	}
	_, err := runPartGit(ctx, dir, nil, "merge-base", "--is-ancestor", "--end-of-options", ancestor, descendant)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("is %s an ancestor of %s: %w", ancestor, descendant, err)
	}
	return true, nil
}

func (execPartGit) Parents(ctx context.Context, dir, sha string) ([]string, error) {
	if !commitID.MatchString(sha) {
		return nil, errors.New("parents: not a commit id")
	}
	out, err := runPartGit(ctx, dir, nil, "rev-list", "--parents", "-n", "1", "--end-of-options", sha, "--")
	if err != nil {
		return nil, fmt.Errorf("read the parents of %s: %w", sha, err)
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return nil, fmt.Errorf("read the parents of %s: no output", sha)
	}
	return fields[1:], nil
}

// runPartGit runs git in dir with prompts off and env added, and returns its
// output without the trailing line break.
func runPartGit(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, partGitTimeout)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never"), env...)
	out, err := cmd.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("git %s gave no answer within %s", args[0], partGitTimeout)
	}
	if err != nil {
		return strings.TrimSpace(string(out)), err
	}
	return strings.TrimSpace(string(out)), nil
}
