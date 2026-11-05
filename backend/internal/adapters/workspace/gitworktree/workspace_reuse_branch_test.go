package gitworktree

// LADO #60: a workspace-project session that reuses the branches LADO made in
// the issue's child repo. Every test runs real git on a root + back + front
// workspace.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	reuseIntegrationBranch = "lado/t/e/integration"
	reusePartBranch        = "lado/t/e/a"
)

type reuseFixture struct {
	git                 string
	tmp                 string
	root, back, front   string
	ws                  *Workspace
	startSHA            string // X: where LADO made the integration and part branches in back
	rootBase, frontBase string
}

// newReuseFixture builds a root repo with back and front child repos and makes
// the integration and part branches in back at a commit X off back's main, the
// way ladoqueue makes them before it spawns a part worker.
func newReuseFixture(t *testing.T) *reuseFixture {
	t.Helper()
	git := requireGit(t)
	tmp := t.TempDir()
	f := &reuseFixture{
		git:   git,
		tmp:   tmp,
		root:  setupOriginClone(t, git, filepath.Join(tmp, "root")),
		back:  setupOriginClone(t, git, filepath.Join(tmp, "back")),
		front: setupOriginClone(t, git, filepath.Join(tmp, "front")),
	}
	f.startSHA = gitOutput(t, git, f.back, "commit-tree", "HEAD^{tree}", "-p", "HEAD", "-m", "integration start")
	runGit(t, git, f.back, "branch", reuseIntegrationBranch, f.startSHA)
	runGit(t, git, f.back, "branch", reusePartBranch, f.startSHA)
	f.rootBase = gitOutput(t, git, f.root, "rev-parse", "origin/main")
	f.frontBase = gitOutput(t, git, f.front, "rev-parse", "origin/main")
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": f.root}})
	if err != nil {
		t.Fatal(err)
	}
	f.ws = ws
	return f
}

func (f *reuseFixture) cfg(session, branch string, reuse bool) ports.WorkspaceProjectConfig {
	return ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: domain.SessionID(session), Kind: domain.KindWorker,
		Branch: branch, ReuseExistingBranch: reuse, RootRepoPath: f.root, BaseBranch: "main",
		Repos: []ports.WorkspaceProjectRepoConfig{
			{Name: "back", RelativePath: "back", RepoPath: f.back, BaseBranch: "main"},
			{Name: "front", RelativePath: "front", RepoPath: f.front, BaseBranch: "main"},
		},
	}
}

func (f *reuseFixture) sessionRoot(session string) string {
	return filepath.Join(f.ws.managedRoot, "proj", session)
}

func (f *reuseFixture) branchSHA(t *testing.T, repo, branch string) string {
	t.Helper()
	return gitOutput(t, f.git, repo, "rev-parse", "refs/heads/"+branch)
}

func worktreeByName(t *testing.T, info ports.WorkspaceProjectInfo, name string) ports.WorkspaceRepoInfo {
	t.Helper()
	for _, row := range info.Worktrees {
		if row.RepoName == name {
			return row
		}
	}
	t.Fatalf("no %q worktree in %+v", name, info.Worktrees)
	return ports.WorkspaceRepoInfo{}
}

func TestWorkspaceReuseExistingBranchKeepsLadoBranchAndMergesBack(t *testing.T) {
	f := newReuseFixture(t)
	ctx := context.Background()
	info, err := f.ws.CreateWorkspaceProject(ctx, f.cfg("part-a", reusePartBranch, true))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = f.ws.DestroyWorkspaceProject(context.Background(), info) }()
	if info.Root.Branch != reusePartBranch {
		t.Fatalf("root branch = %q, want %q", info.Root.Branch, reusePartBranch)
	}
	if len(info.Worktrees) != 3 {
		t.Fatalf("worktrees = %+v, want root, back and front", info.Worktrees)
	}
	for _, row := range info.Worktrees {
		if row.Branch != reusePartBranch {
			t.Fatalf("%s branch = %q, want %q without a suffix", row.RepoName, row.Branch, reusePartBranch)
		}
		if got := gitOutput(t, f.git, row.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != reusePartBranch {
			t.Fatalf("%s worktree is on %q, want %q", row.RepoName, got, reusePartBranch)
		}
	}
	back := worktreeByName(t, info, "back")
	if got := gitOutput(t, f.git, back.Path, "rev-parse", "HEAD"); got != f.startSHA {
		t.Fatalf("back worktree HEAD = %s, want LADO's start %s", got, f.startSHA)
	}
	if got := gitOutput(t, f.git, worktreeByName(t, info, domain.RootWorkspaceRepoName).Path, "rev-parse", "HEAD"); got != f.rootBase {
		t.Fatalf("root worktree HEAD = %s, want its base %s", got, f.rootBase)
	}
	if got := gitOutput(t, f.git, worktreeByName(t, info, "front").Path, "rev-parse", "HEAD"); got != f.frontBase {
		t.Fatalf("front worktree HEAD = %s, want its base %s", got, f.frontBase)
	}

	// The worker commits Y in back; LADO reads the part branch it recorded.
	if err := os.WriteFile(filepath.Join(back.Path, "part.txt"), []byte("part a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.git, back.Path, "add", "part.txt")
	runGit(t, f.git, back.Path, "commit", "-m", "part a work")
	partTip := gitOutput(t, f.git, back.Path, "rev-parse", "HEAD")
	if got := f.branchSHA(t, f.back, reusePartBranch); got != partTip {
		t.Fatalf("back %s = %s, want the worker's commit %s", reusePartBranch, got, partTip)
	}

	// The same commands ladoqueue's execPartGit.Merge and the branch move run.
	head := f.branchSHA(t, f.back, reuseIntegrationBranch)
	tree, _, _ := strings.Cut(gitOutput(t, f.git, f.back, "merge-tree", "--write-tree", "--no-messages", "--name-only", "--end-of-options", head, partTip), "\n")
	merged := gitOutput(t, f.git, f.back, "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", head, "-p", partTip, "-m", "LADO: merge part a")
	runGit(t, f.git, f.back, "update-ref", "refs/heads/"+reuseIntegrationBranch, merged, head)
	runGit(t, f.git, f.back, "merge-base", "--is-ancestor", partTip, reuseIntegrationBranch)
	if merged == f.startSHA {
		t.Fatal("the merge left the integration branch at its start")
	}
}

func TestWorkspaceReuseExistingBranchOffKeepsSuffixedBranch(t *testing.T) {
	f := newReuseFixture(t)
	info, err := f.ws.CreateWorkspaceProject(context.Background(), f.cfg("part-a", reusePartBranch, false))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = f.ws.DestroyWorkspaceProject(context.Background(), info) }()
	want := reusePartBranch + "-2"
	for _, row := range info.Worktrees {
		if row.Branch != want {
			t.Fatalf("%s branch = %q, want AO's default suffixed %q", row.RepoName, row.Branch, want)
		}
	}
	if got := gitOutput(t, f.git, worktreeByName(t, info, "back").Path, "rev-parse", "HEAD"); got == f.startSHA {
		t.Fatalf("back worktree started at LADO's commit %s without ReuseExistingBranch", got)
	}
	if got := f.branchSHA(t, f.back, reusePartBranch); got != f.startSHA {
		t.Fatalf("back %s = %s, want it untouched at %s", reusePartBranch, got, f.startSHA)
	}
}

func TestWorkspaceReuseExistingBranchRejectsUnsafeRequests(t *testing.T) {
	f := newReuseFixture(t)
	ctx := context.Background()

	fresh := f.cfg("fresh", reusePartBranch, true)
	fresh.FreshBranch = true
	if _, err := f.ws.CreateWorkspaceProject(ctx, fresh); err == nil || !strings.Contains(err.Error(), "FreshBranch") {
		t.Fatalf("FreshBranch with ReuseExistingBranch: err = %v, want a refusal", err)
	}
	if _, err := os.Stat(f.sessionRoot("fresh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused FreshBranch request left a workspace: %v", err)
	}

	if _, err := f.ws.CreateWorkspaceProject(ctx, f.cfg("missing", "lado/t/e/missing", true)); err == nil || !strings.Contains(err.Error(), "no workspace repository has this local branch") {
		t.Fatalf("missing branch: err = %v, want a refusal", err)
	}
	if _, err := os.Stat(f.sessionRoot("missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused missing branch left a workspace: %v", err)
	}

	other := filepath.Join(f.tmp, "other-checkout")
	runGit(t, f.git, f.back, "worktree", "add", other, reusePartBranch)
	if resolved, err := filepath.EvalSymlinks(other); err == nil {
		other = resolved
	}
	_, err := f.ws.CreateWorkspaceProject(ctx, f.cfg("held", reusePartBranch, true))
	if !errors.Is(err, ErrBranchCheckedOutElsewhere) || !strings.Contains(err.Error(), other) {
		t.Fatalf("branch held elsewhere: err = %v, want ErrBranchCheckedOutElsewhere naming %q", err, other)
	}
	if _, err := os.Stat(f.sessionRoot("held")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused held branch left a workspace: %v", err)
	}
}

func TestWorkspaceReuseExistingBranchFailureRemovesWorktreesAndKeepsBranch(t *testing.T) {
	for _, failRepo := range []string{"back", "front"} {
		t.Run(failRepo, func(t *testing.T) {
			f := newReuseFixture(t)
			failPath := map[string]string{"back": f.back, "front": f.front}[failRepo]
			if resolved, err := filepath.EvalSymlinks(failPath); err == nil {
				failPath = resolved
			}
			injected := errors.New("injected worktree add failure")
			actualRun := f.ws.run
			failing := true
			f.ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
				if failing && len(args) > 3 && args[0] == "-C" && filepath.Clean(args[1]) == failPath && args[2] == "worktree" && args[3] == "add" {
					return nil, injected
				}
				return actualRun(ctx, binary, args...)
			}
			ctx := context.Background()
			cfg := f.cfg("part-a", reusePartBranch, true)
			info, err := f.ws.CreateWorkspaceProject(ctx, cfg)
			if !errors.Is(err, injected) {
				t.Fatalf("create err = %v, want the injected failure", err)
			}
			if len(info.Worktrees) != 0 {
				t.Fatalf("failed create returned worktrees: %+v", info.Worktrees)
			}
			if _, err := os.Stat(f.sessionRoot("part-a")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed create left the session workspace: %v", err)
			}
			for _, repo := range []string{f.root, f.back, f.front} {
				list := gitOutput(t, f.git, repo, "worktree", "list", "--porcelain")
				if strings.Contains(list, "refs/heads/"+reusePartBranch+"\n") || strings.HasSuffix(list, "refs/heads/"+reusePartBranch) {
					t.Fatalf("%s still has %s checked out after rollback:\n%s", repo, reusePartBranch, list)
				}
			}
			if got := f.branchSHA(t, f.back, reusePartBranch); got != f.startSHA {
				t.Fatalf("back %s = %s after rollback, want LADO's %s", reusePartBranch, got, f.startSHA)
			}
			if got := f.branchSHA(t, f.back, reuseIntegrationBranch); got != f.startSHA {
				t.Fatalf("back %s = %s after rollback, want LADO's %s", reuseIntegrationBranch, got, f.startSHA)
			}

			// A retry with the same branch, like the integration worker's, works.
			failing = false
			retry, err := f.ws.CreateWorkspaceProject(ctx, cfg)
			if err != nil {
				t.Fatalf("retry after rollback: %v", err)
			}
			defer func() { _ = f.ws.DestroyWorkspaceProject(context.Background(), retry) }()
			if got := gitOutput(t, f.git, worktreeByName(t, retry, "back").Path, "rev-parse", "HEAD"); got != f.startSHA {
				t.Fatalf("retried back worktree HEAD = %s, want %s", got, f.startSHA)
			}
			for _, row := range retry.Worktrees {
				if row.Branch != reusePartBranch {
					t.Fatalf("retried %s branch = %q, want %q", row.RepoName, row.Branch, reusePartBranch)
				}
			}
		})
	}
}
