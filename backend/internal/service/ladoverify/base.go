package ladoverify

// The base_branch item (decision h of the step 8 plan; docs/LJY-PRD/TDD.md
// §7.3): after its worker started, the local base branch only follows the
// remote one. An agent that moved it from its worktree, as with git
// update-ref refs/heads/main HEAD, leaves reflog entries the remote branch
// does not hold, and the developer confirms them before the integration.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// BaseInput is what the base_branch item reads: the repository that holds
// the issue (in a workspace project, its child repository), the base branch
// and remote the dispatcher resolved, the pull request's head and when the
// execution started.
type BaseInput struct {
	Dir, Branch, Remote, HeadSHA string
	StartedAt                    time.Time
}

// EvaluateBase computes the base_branch item. It does not fetch: it compares
// with the remote-tracking branch as it is, which the integration approval
// fetches first.
//
// Each reflog entry of the local branch since the execution started, and the
// branch's tip, must be a commit the remote-tracking branch holds; the
// others are confirmed as base_moved. A local branch without a reflog is
// confirmed as reflog_off, a head the remote branch already holds as
// already_on_base, and a repository without the local branch passes as
// no_local_branch. Without the remote-tracking branch there is nothing to
// compare with, and the item passes. Codes rank already_on_base, then
// base_moved, then reflog_off.
func EvaluateBase(ctx context.Context, git Git, in BaseInput) (domain.LadoVerifyItem, error) {
	if in.Dir == "" || in.Branch == "" {
		return domain.LadoVerifyItem{}, errors.New("evaluate the base branch: no repository or branch")
	}
	details := domain.LadoBaseBranchDetails{Branch: in.Branch, Remote: in.Remote}
	local, err := commitOf(ctx, git, in.Dir, "refs/heads/"+in.Branch)
	if err != nil {
		return domain.LadoVerifyItem{}, fmt.Errorf("evaluate the base branch: %w", err)
	}
	remote := ""
	if in.Remote != "" {
		if remote, err = commitOf(ctx, git, in.Dir, "refs/remotes/"+in.Remote+"/"+in.Branch); err != nil {
			return domain.LadoVerifyItem{}, fmt.Errorf("evaluate the base branch: %w", err)
		}
	}
	reflogOff := false
	if remote != "" {
		onRemote := reachability(ctx, git, in.Dir, remote)
		if details.AlreadyOnBase, err = headOnRemote(ctx, git, in, onRemote); err != nil {
			return domain.LadoVerifyItem{}, fmt.Errorf("evaluate the base branch: %w", err)
		}
		if local != "" {
			if details.Moved, reflogOff, err = movedSince(ctx, git, in, local, onRemote); err != nil {
				return domain.LadoVerifyItem{}, fmt.Errorf("evaluate the base branch: %w", err)
			}
		}
	}
	outcome, code := domain.LadoVerifyPass, ""
	switch {
	case details.AlreadyOnBase:
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeAlreadyOnBase
	case len(details.Moved) > 0:
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeBaseMoved
	case reflogOff:
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeReflogOff
	case local == "":
		code = domain.LadoCodeNoLocalBranch
	}
	return domain.NewLadoVerifyItem(domain.LadoVerifyBaseBranch, outcome, code, details)
}

// commitOf returns the commit ref names, or "" when it names none.
func commitOf(ctx context.Context, git Git, dir, ref string) (string, error) {
	sha, err := git.RevParse(ctx, dir, ref)
	if errors.Is(err, ErrRefNotFound) {
		return "", nil
	}
	return sha, err
}

// reachability returns a check of whether the remote base commit holds a
// commit, which asks git once per commit.
func reachability(ctx context.Context, git Git, dir, remote string) func(string) (bool, error) {
	known := map[string]bool{}
	return func(commit string) (bool, error) {
		if held, ok := known[commit]; ok {
			return held, nil
		}
		held, err := git.IsAncestor(ctx, dir, commit, remote)
		if err != nil {
			return false, err
		}
		known[commit] = held
		return held, nil
	}
}

// headOnRemote reports whether the remote base already holds the pull
// request's head. A head this repository lacks is not on the remote-tracking
// branch either.
func headOnRemote(ctx context.Context, git Git, in BaseInput, onRemote func(string) (bool, error)) (bool, error) {
	if in.HeadSHA == "" {
		return false, nil
	}
	head, err := commitOf(ctx, git, in.Dir, in.HeadSHA)
	if err != nil || head == "" {
		return false, err
	}
	return onRemote(head)
}

// movedSince lists the reflog entries of the local branch since the
// execution started whose commit the remote base does not hold, newest
// first, and then the branch's tip, local, when it is such a commit and no
// listed entry names it. It reports whether the branch has no reflog.
// Reflog times are whole seconds, so the entries of the second the
// execution started in count too.
func movedSince(ctx context.Context, git Git, in BaseInput, local string, onRemote func(string) (bool, error)) ([]domain.LadoReflogEntry, bool, error) {
	entries, err := git.Reflog(ctx, in.Dir, "refs/heads/"+in.Branch)
	if err != nil {
		return nil, false, err
	}
	since := in.StartedAt.Truncate(time.Second)
	var moved []domain.LadoReflogEntry
	for _, entry := range entries {
		if entry.At.Before(since) {
			continue
		}
		held, err := onRemote(entry.Commit)
		if err != nil {
			return nil, false, err
		}
		if !held {
			moved = append(moved, domain.LadoReflogEntry{Selector: entry.Selector, Commit: entry.Commit, Subject: redact(entry.Subject)})
		}
	}
	held, err := onRemote(local)
	if err != nil {
		return nil, false, err
	}
	named := slices.ContainsFunc(moved, func(e domain.LadoReflogEntry) bool { return e.Commit == local })
	if !held && !named {
		tip := domain.LadoReflogEntry{Selector: in.Branch, Commit: local}
		if len(entries) > 0 && entries[0].Commit == local {
			tip.Selector, tip.Subject = entries[0].Selector, redact(entries[0].Subject)
		}
		moved = append(moved, tip)
	}
	return moved, len(entries) == 0, nil
}
