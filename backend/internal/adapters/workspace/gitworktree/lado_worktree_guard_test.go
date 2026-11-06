package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/gitdefault"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LADO #22: git-lfs check before `git worktree add`, the worktree add error
// summary, and the hooks folder guard.

// commitLFSAttributes commits only a .gitattributes with the LFS filter, so a
// git-lfs install on this computer cannot turn any file into a pointer.
func commitLFSAttributes(t *testing.T, git, repo, rel string) {
	t.Helper()
	p := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("*.bin filter=lfs diff=lfs merge=lfs -text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, repo, "add", rel)
	runGit(t, git, repo, "commit", "-m", "track bin with lfs")
	runGit(t, git, repo, "push", "origin", "main")
}

func lfsWorkspace(t *testing.T, git, managed string, repos StaticRepoResolver, installed *bool) *Workspace {
	t.Helper()
	ws, err := New(Options{
		Binary: git, ManagedRoot: managed, RepoResolver: repos,
		GitLFSInstalled: func(context.Context) bool { return *installed },
	})
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestLadoGitLFSMissingFailsBeforeWorktreeAdd(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	commitLFSAttributes(t, git, repo, "assets/.gitattributes") // nested
	installed := false
	ws := lfsWorkspace(t, git, filepath.Join(tmp, "managed"), StaticRepoResolver{"proj": repo}, &installed)

	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/lfs"}
	_, err := ws.Create(context.Background(), cfg)
	if !errors.Is(err, ports.ErrGitLFSMissing) {
		t.Fatalf("create = %v, want ErrGitLFSMissing", err)
	}
	summary, ok := ports.WorkspaceSetupSummary(err)
	if !ok || !strings.HasPrefix(summary, "repository repo uses Git LFS") || !strings.Contains(summary, "install Git LFS (https://git-lfs.com)") {
		t.Fatalf("summary = %q, %v", summary, ok)
	}
	path, err := ws.managedPath(cfg)
	if err != nil {
		t.Fatal(err)
	}
	requireAbsent(t, path)
	if out := gitOutput(t, git, repo, "branch", "--list", "feature/lfs"); out != "" {
		t.Fatalf("branch was created: %q", out)
	}

	installed = true
	info, err := ws.Create(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create with git-lfs installed: %v", err)
	}

	// An existing session restores even without git-lfs.
	installed = false
	if err := ws.Destroy(context.Background(), info); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := ws.Restore(context.Background(), cfg); err != nil {
		t.Fatalf("restore without git-lfs: %v", err)
	}
}

func TestLadoGitLFSNotUsedCreatesWithoutGitLFS(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	installed := false
	ws := lfsWorkspace(t, git, filepath.Join(tmp, "managed"), StaticRepoResolver{"proj": repo}, &installed)
	if _, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/plain"}); err != nil {
		t.Fatalf("create: %v", err)
	}
}

func TestLadoGitLFSMissingInWorkspaceChildRollsBack(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	childRepo := setupOriginClone(t, git, filepath.Join(rootRepo, "front"))
	commitLFSAttributes(t, git, childRepo, ".gitattributes")
	installed := false
	managed := filepath.Join(tmp, "managed")
	ws := lfsWorkspace(t, git, managed, StaticRepoResolver{"proj": rootRepo}, &installed)

	cfg := ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "sess", Kind: "worker", Branch: "ao/lfs",
		RootRepoPath: rootRepo,
		Repos:        []ports.WorkspaceProjectRepoConfig{{Name: "front", RelativePath: "front", RepoPath: childRepo}},
	}
	_, err := ws.CreateWorkspaceProject(context.Background(), cfg)
	if !errors.Is(err, ports.ErrGitLFSMissing) {
		t.Fatalf("create workspace project = %v, want ErrGitLFSMissing", err)
	}
	requireAbsent(t, filepath.Join(ws.managedRoot, "proj", "worker", "sess"))

	installed = true
	if _, err := ws.CreateWorkspaceProject(context.Background(), cfg); err != nil {
		t.Fatalf("create workspace project with git-lfs: %v", err)
	}
}

func TestLadoWrapWorktreeAddErrorKeepsTextAndSentinels(t *testing.T) {
	cause := fmt.Errorf("gitworktree: worktree add branch %q: %w", "b", commandError{
		args:   []string{"git", "-C", "/very/long/path", "worktree", "add"},
		output: "Preparing worktree (new branch 'b')\ngit-lfs filter-process: git-lfs: command not found\nfatal: the remote end hung up\n\n",
		err:    ErrBranchNotFetched,
	})
	err := ladoWrapWorktreeAddError("/src/front", cause)
	if err.Error() != cause.Error() {
		t.Fatalf("Error() = %q, want %q", err.Error(), cause.Error())
	}
	if !errors.Is(err, ErrBranchNotFetched) {
		t.Fatal("sentinel lost")
	}
	summary, ok := ports.WorkspaceSetupSummary(err)
	if !ok || summary != "could not create the worktree of repository front: fatal: the remote end hung up." {
		t.Fatalf("summary = %q, %v", summary, ok)
	}
}

// hookRepo makes a clone whose shared config points core.hooksPath at
// hooksPath. extensions.worktreeConfig (written by enableWorktreeConfig) and
// branch.autoSetupMerge=false (git itself records upstream tracking for a new
// branch otherwise) are set up front so the bytes of .git/config show only
// what the hooks guard does.
func hookRepo(t *testing.T, git, dir, hooksPath string) string {
	t.Helper()
	repo := setupOriginClone(t, git, dir)
	runGit(t, git, repo, "config", "extensions.worktreeConfig", "true")
	runGit(t, git, repo, "config", "branch.autoSetupMerge", "false")
	runGit(t, git, repo, "config", "core.hooksPath", hooksPath)
	return repo
}

type repoConfigSnapshot struct {
	config, exclude []byte
}

func snapshotRepoConfig(t *testing.T, repo string) repoConfigSnapshot {
	t.Helper()
	config, err := os.ReadFile(filepath.Join(repo, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	exclude, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return repoConfigSnapshot{config: config, exclude: exclude}
}

func (s repoConfigSnapshot) requireSame(t *testing.T, repo string) {
	t.Helper()
	now := snapshotRepoConfig(t, repo)
	if !bytes.Equal(now.config, s.config) {
		t.Fatalf("%s/.git/config changed:\n%s\nwant:\n%s", repo, now.config, s.config)
	}
	if !bytes.Equal(now.exclude, s.exclude) {
		t.Fatalf("%s/.git/info/exclude changed:\n%s\nwant:\n%s", repo, now.exclude, s.exclude)
	}
}

func requireHooksIgnored(t *testing.T, git, worktree string) {
	t.Helper()
	hooks := filepath.Join(worktree, ".husky", "_")
	got, err := os.ReadFile(filepath.Join(hooks, ".gitignore"))
	if err != nil || string(got) != "*\n" {
		t.Fatalf("hooks .gitignore = %q, %v; want \"*\\n\"", got, err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte("#!/bin/sh\ngit lfs post-checkout \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Limited to .husky: a workspace root also lists its child worktree.
	if status := gitOutput(t, git, worktree, "status", "--porcelain", "--", ".husky"); status != "" {
		t.Fatalf("git status after writing a hook = %q, want clean", status)
	}
}

func TestLadoHooksDirGuardSingleRepo(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := hookRepo(t, git, tmp, ".husky/_")
	before := snapshotRepoConfig(t, repo)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/hooks"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	requireHooksIgnored(t, git, info.Path)
	before.requireSame(t, repo)
}

func TestLadoHooksDirGuardWorkspaceProject(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := hookRepo(t, git, filepath.Join(tmp, "root"), ".husky/_")
	childRepo := hookRepo(t, git, filepath.Join(rootRepo, "front"), ".husky/_")
	// The workspace root records its default branch on first use; do it now.
	runGit(t, git, rootRepo, "config", "--local", gitdefault.ManagedDefaultConfigKey, "main")
	rootBefore := snapshotRepoConfig(t, rootRepo)
	childBefore := snapshotRepoConfig(t, childRepo)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "sess", Kind: "worker", Branch: "ao/hooks",
		RootRepoPath: rootRepo,
		Repos:        []ports.WorkspaceProjectRepoConfig{{Name: "front", RelativePath: "front", RepoPath: childRepo}},
	})
	if err != nil {
		t.Fatalf("create workspace project: %v", err)
	}
	if len(info.Worktrees) != 2 {
		t.Fatalf("worktrees = %#v", info.Worktrees)
	}
	requireHooksIgnored(t, git, info.Worktrees[1].Path)
	requireHooksIgnored(t, git, info.Root.Path)
	rootBefore.requireSame(t, rootRepo)
	childBefore.requireSame(t, childRepo)
}

func TestLadoHooksDirGuardLeavesTrackedHooksAlone(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := hookRepo(t, git, tmp, ".husky/_")
	hook := filepath.Join(repo, ".husky", "_", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, repo, "add", ".husky/_/pre-commit")
	runGit(t, git, repo, "-c", "core.hooksPath=/dev/null", "commit", "-m", "tracked hooks")
	runGit(t, git, repo, "push", "origin", "main")
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/tracked"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	requirePresent(t, filepath.Join(info.Path, ".husky", "_", "pre-commit"))
	requireAbsent(t, filepath.Join(info.Path, ".husky", "_", ".gitignore"))
}

func TestLadoHooksDirGuardIgnoresPathsOutsideTheWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	outside := filepath.Join(tmp, "shared-hooks")
	repo := hookRepo(t, git, filepath.Join(tmp, "src"), outside)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/outside"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	requireAbsent(t, outside)
	if status := gitOutput(t, git, info.Path, "status", "--porcelain"); status != "" {
		t.Fatalf("git status = %q, want clean", status)
	}
}
