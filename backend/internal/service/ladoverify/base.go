package ladoverify

// The base_branch item (decision h of the step 8 plan; docs/LJY-PRD/TDD.md
// §7.3): after its worker started, the local base branch only follows the
// remote one. An agent that moved it from its worktree, as with git
// update-ref refs/heads/main HEAD, leaves reflog entries the remote branch
// does not hold, and the developer confirms them before the integration.
//
// LADO fork (#53): the item also tells when the remote base moved past the
// head's merge base, as when another PR merged into it. A change there to a
// file the head changed or the plan names fails as base_overlap: the head
// must take in the base and be verified again. Another move needs
// confirming as base_advanced.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// BaseInput is what the base_branch item reads: the repository verified (in
// a workspace project, the child repository of the PR), the base branch
// and remote the dispatcher resolved, the pull request's head, when the
// execution started and the local branch's commit the dispatcher recorded
// (empty when the branch did not exist or LADO did not record it).
//
// Target is the branch the PR merges into, whose remote-tracking branch the
// base move compares with (empty: Branch). Prefix is the repository's folder
// in a workspace project, slash-separated; Affected are the plan's
// affected_paths and Ignore the project's overlap ignore globs, both from
// the project root.
//
// #59: DefaultBranch is the default branch of the PR's repository when the
// PR is in another repository than its issue and LADO resolved it, else
// empty; DispatchBranch is the base branch the dispatcher started the worker
// from, in the issue's repository.
type BaseInput struct {
	Dir, Branch, Remote, HeadSHA  string
	StartedAt                     time.Time
	DispatchLocalSHA              string
	Target, Prefix                string
	Affected, Ignore              []string
	DefaultBranch, DispatchBranch string
}

// EvaluateBase computes the base_branch item. It does not fetch: it compares
// with the remote-tracking branch as it is, which the integration approval
// fetches first.
//
// Each reflog entry of the local branch since the execution started, and the
// branch's tip when it moved since dispatch, must be a commit the
// remote-tracking branch holds; the others are confirmed as base_moved, so
// unpushed commits the developer had before the dispatch do not count. A
// local branch without a reflog is confirmed as reflog_off, a head the
// remote branch already holds as already_on_base, and a local branch that
// existed at dispatch and is gone, with its reflog, as base_deleted. A
// repository without the local branch otherwise passes as no_local_branch.
// Without the remote-tracking branch there is nothing to compare with, and
// the item passes unless the branch was deleted.
//
// When the remote-tracking target branch is past the head's merge base with
// it, the files changed on it since that the head changed too or the plan
// names, except those an ignore glob matches, fail the item as
// base_overlap; without such a file the move needs confirming as
// base_advanced.
//
// With DefaultBranch, a target or a dispatcher's base branch other than it
// (case aside) needs confirming as base_not_default, with both branches in
// the details. Codes rank base_overlap, already_on_base, base_deleted,
// base_moved, reflog_off, base_not_default, then base_advanced.
func EvaluateBase(ctx context.Context, git Git, in BaseInput) (domain.LadoVerifyItem, error) {
	if in.Dir == "" || in.Branch == "" {
		return domain.LadoVerifyItem{}, errors.New("evaluate the base branch: no repository or branch")
	}
	details := domain.LadoBaseBranchDetails{Branch: in.Branch, Remote: in.Remote, DispatchCommit: in.DispatchLocalSHA}
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
	details.Deleted = local == "" && in.DispatchLocalSHA != ""
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
	if err := baseAdvance(ctx, git, in, &details); err != nil {
		return domain.LadoVerifyItem{}, fmt.Errorf("evaluate the base branch: %w", err)
	}
	notDefault := notDefaultBase(in)
	if notDefault {
		details.DefaultBranch, details.DispatchBranch = in.DefaultBranch, in.DispatchBranch
	}
	outcome, code := domain.LadoVerifyPass, ""
	switch {
	case details.OverlapCount > 0:
		outcome, code = domain.LadoVerifyFail, domain.LadoCodeBaseOverlap
	case details.AlreadyOnBase:
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeAlreadyOnBase
	case details.Deleted:
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeBaseDeleted
	case len(details.Moved) > 0:
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeBaseMoved
	case reflogOff:
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeReflogOff
	case notDefault:
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeBaseNotDefault
	case details.Advanced:
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeBaseAdvanced
	case local == "":
		code = domain.LadoCodeNoLocalBranch
	}
	return domain.NewLadoVerifyItem(domain.LadoVerifyBaseBranch, outcome, code, details)
}

// notDefaultBase reports whether the PR's target or the dispatcher's base
// branch is not the known default branch of the PR's repository. Without a
// default branch there is nothing to compare: what git cannot tell does not
// stop the integration.
func notDefaultBase(in BaseInput) bool {
	if in.DefaultBranch == "" {
		return false
	}
	target := cmp.Or(in.Target, in.Branch)
	return !strings.EqualFold(target, in.DefaultBranch) ||
		(in.DispatchBranch != "" && !strings.EqualFold(in.DispatchBranch, in.DefaultBranch))
}

// baseAdvance sets details.Advanced, AdvancedTip, Overlap and OverlapCount
// when the remote-tracking target branch is past the head's merge base with
// it. A repository without that branch or the head has nothing to compare,
// and a head the branch holds nothing to take in.
func baseAdvance(ctx context.Context, git Git, in BaseInput, details *domain.LadoBaseBranchDetails) error {
	if in.Remote == "" || in.HeadSHA == "" {
		return nil
	}
	ref := "refs/remotes/" + in.Remote + "/" + cmp.Or(in.Target, in.Branch)
	tip, err := commitOf(ctx, git, in.Dir, ref)
	if err != nil || tip == "" {
		return err
	}
	head, err := commitOf(ctx, git, in.Dir, in.HeadSHA)
	if err != nil || head == "" {
		return err
	}
	// A head the target holds already has nothing to take in.
	base, err := git.MergeBase(ctx, in.Dir, tip, head)
	if err != nil || base == tip || base == head {
		return err
	}
	details.Advanced, details.AdvancedTip = true, tip
	onBase, err := git.ChangedPaths(ctx, in.Dir, base, tip)
	if err != nil {
		return err
	}
	onHead, err := git.ChangedPaths(ctx, in.Dir, base, head)
	if err != nil {
		return err
	}
	headPaths := PrefixPaths(in.Prefix, onHead)
	var overlap []string
	for _, p := range PrefixPaths(in.Prefix, onBase) {
		if anyGlob(in.Ignore, p) || slices.Contains(overlap, p) {
			continue
		}
		if slices.Contains(headPaths, p) || anyGlob(in.Affected, p) {
			overlap = append(overlap, p)
		}
	}
	slices.Sort(overlap)
	details.OverlapCount = len(overlap)
	details.Overlap = overlap[:min(len(overlap), outsidePlanMax)]
	return nil
}

// anyGlob reports whether one of globs matches the path p.
func anyGlob(globs []string, p string) bool {
	return slices.ContainsFunc(globs, func(glob string) bool { return MatchPath(glob, p) })
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
// first, and then the branch's tip, local, when it is such a commit, no
// listed entry names it and it is not the commit the dispatcher recorded:
// a tip the branch had at dispatch was the developer's, not the run's. It
// reports whether the branch has no reflog.
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
	if !held && !named && local != in.DispatchLocalSHA {
		tip := domain.LadoReflogEntry{Selector: in.Branch, Commit: local}
		if len(entries) > 0 && entries[0].Commit == local {
			tip.Selector, tip.Subject = entries[0].Selector, redact(entries[0].Subject)
		}
		moved = append(moved, tip)
	}
	return moved, len(entries) == 0, nil
}
