package ladoverify

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// checkBase evaluates the base branch of r's main from origin at started and
// compares the item with the outcome, code and details wanted.
func checkBase(t *testing.T, r *testRepo, remote, head string, started time.Time, outcome domain.LadoVerifyOutcome, code string, details domain.LadoBaseBranchDetails) {
	t.Helper()
	got, err := EvaluateBase(context.Background(), ExecGit{}, BaseInput{Dir: r.dir, Branch: "main", Remote: remote, HeadSHA: head, StartedAt: started})
	if err != nil {
		t.Fatalf("EvaluateBase = %v", err)
	}
	want, err := domain.NewLadoVerifyItem(domain.LadoVerifyBaseBranch, outcome, code, details)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("base_branch = %s %q %s\nwant %s %q %s", got.Outcome, got.Code, got.Details, want.Outcome, want.Code, want.Details)
	}
}

// PRD gate 8: an agent that moves the local base branch from its worktree
// after the dispatch leaves an entry the developer confirms, while a
// developer who only pulls origin moves nothing LADO asks about.
func TestBaseMovedFromAWorktreeNeedsConfirming(t *testing.T) {
	t.Run("update-ref from the worktree", func(t *testing.T) {
		r := newTestRepo(t, t0.Add(-time.Hour))
		work := r.worktree(t0.Add(time.Minute), "lado/work")
		r.write(work, "login.go", "package login\n")
		head := r.commit(t0.Add(2*time.Minute), work, "Fix the login timeout")
		moved := t0.Add(3 * time.Minute)
		r.git(moved, work, "update-ref", "refs/heads/main", "HEAD")

		checkBase(t, r, "origin", head, t0, domain.LadoVerifyConfirm, domain.LadoCodeBaseMoved, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin",
			Moved: []domain.LadoReflogEntry{{Selector: fmt.Sprintf("main@{%d}", moved.Unix()), Commit: head}},
		})
	})

	t.Run("a fast-forward pull of origin", func(t *testing.T) {
		r := newTestRepo(t, t0.Add(-time.Hour))
		work := r.worktree(t0.Add(time.Minute), "lado/work")
		r.write(work, "login.go", "package login\n")
		head := r.commit(t0.Add(2*time.Minute), work, "Fix the login timeout")
		r.write(r.upstream, "CHANGELOG.md", "- a fix\n")
		r.commit(t0.Add(3*time.Minute), r.upstream, "Record a fix")
		r.publish(t0.Add(3 * time.Minute))
		r.git(t0.Add(4*time.Minute), r.dir, "pull", "-q", "--ff-only")

		checkBase(t, r, "origin", head, t0, domain.LadoVerifyPass, "", domain.LadoBaseBranchDetails{Branch: "main", Remote: "origin"})
	})
}

func TestBaseItemCases(t *testing.T) {
	t.Run("no local branch", func(t *testing.T) {
		r := newTestRepo(t, t0.Add(-time.Hour))
		work := r.worktree(t0.Add(time.Minute), "lado/work")
		head := r.commit(t0.Add(2*time.Minute), work, "Fix the login timeout")
		r.git(t0.Add(3*time.Minute), r.dir, "switch", "-q", "--detach")
		r.git(t0.Add(3*time.Minute), r.dir, "branch", "-q", "-D", "main")

		checkBase(t, r, "origin", head, t0, domain.LadoVerifyPass, domain.LadoCodeNoLocalBranch, domain.LadoBaseBranchDetails{Branch: "main", Remote: "origin"})
	})

	t.Run("no reflog", func(t *testing.T) {
		r := newTestRepo(t, t0.Add(-time.Hour), "-c", "core.logAllRefUpdates=false")
		work := r.worktree(t0.Add(time.Minute), "lado/work")
		head := r.commit(t0.Add(2*time.Minute), work, "Fix the login timeout")

		checkBase(t, r, "origin", head, t0, domain.LadoVerifyConfirm, domain.LadoCodeReflogOff, domain.LadoBaseBranchDetails{Branch: "main", Remote: "origin"})
	})

	t.Run("no reflog and a moved tip", func(t *testing.T) {
		r := newTestRepo(t, t0.Add(-time.Hour), "-c", "core.logAllRefUpdates=false")
		work := r.worktree(t0.Add(time.Minute), "lado/work")
		head := r.commit(t0.Add(2*time.Minute), work, "Fix the login timeout")
		r.git(t0.Add(3*time.Minute), work, "update-ref", "refs/heads/main", "HEAD")

		checkBase(t, r, "origin", head, t0, domain.LadoVerifyConfirm, domain.LadoCodeBaseMoved, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin", Moved: []domain.LadoReflogEntry{{Selector: "main", Commit: head}},
		})
	})

	t.Run("the head already on origin", func(t *testing.T) {
		r := newTestRepo(t, t0.Add(-time.Hour))
		work := r.worktree(t0.Add(time.Minute), "lado/work")
		head := r.commit(t0.Add(2*time.Minute), work, "Fix the login timeout")
		// Someone merged the work outside LADO, and a fetch brought it.
		r.git(t0.Add(3*time.Minute), r.origin, "fetch", "-q", r.dir, "+refs/heads/lado/work:refs/heads/main")
		r.git(t0.Add(4*time.Minute), r.dir, "fetch", "-q", "origin")

		checkBase(t, r, "origin", head, t0, domain.LadoVerifyConfirm, domain.LadoCodeAlreadyOnBase, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin", AlreadyOnBase: true,
		})
	})

	t.Run("entries from before the dispatch", func(t *testing.T) {
		r := newTestRepo(t, t0.Add(-time.Hour))
		r.write(r.dir, "local.txt", "a local draft\n")
		draft := r.commit(t0.Add(-30*time.Minute), r.dir, "Draft locally")
		r.git(t0.Add(-20*time.Minute), r.dir, "reset", "-q", "--hard", "refs/remotes/origin/main")
		work := r.worktree(t0.Add(time.Minute), "lado/work")
		head := r.commit(t0.Add(2*time.Minute), work, "Fix the login timeout")

		checkBase(t, r, "origin", head, t0, domain.LadoVerifyPass, "", domain.LadoBaseBranchDetails{Branch: "main", Remote: "origin"})
		// Had the execution started before the draft, the draft would count.
		before := t0.Add(-40 * time.Minute)
		checkBase(t, r, "origin", head, before, domain.LadoVerifyConfirm, domain.LadoCodeBaseMoved, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin",
			Moved: []domain.LadoReflogEntry{{Selector: fmt.Sprintf("main@{%d}", t0.Add(-30*time.Minute).Unix()), Commit: draft, Subject: "commit: Draft locally"}},
		})
	})

	t.Run("no remote-tracking branch", func(t *testing.T) {
		r := newTestRepo(t, t0.Add(-time.Hour))
		work := r.worktree(t0.Add(time.Minute), "lado/work")
		head := r.commit(t0.Add(2*time.Minute), work, "Fix the login timeout")
		r.git(t0.Add(3*time.Minute), work, "update-ref", "refs/heads/main", "HEAD")

		// Nothing to compare with: the item passes.
		checkBase(t, r, "upstream", head, t0, domain.LadoVerifyPass, "", domain.LadoBaseBranchDetails{Branch: "main", Remote: "upstream"})
		checkBase(t, r, "", head, t0, domain.LadoVerifyPass, "", domain.LadoBaseBranchDetails{Branch: "main"})
	})
}
