package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/gitlfs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// LADO #22: checks around `git worktree add`.
//
// The Git LFS check and the hooks folder guard run git directly instead of
// through w.run, like enableWorktreeConfig. Unit tests script w.run and fail
// on any command they do not expect; on their fake repository paths these
// direct commands fail, and a failure here never blocks a session start, it
// is only logged.

const ladoGitLFSFix = "install Git LFS (https://git-lfs.com), run git lfs install, then start the session again"

// ladoRequireGitLFS fails before a new worktree is created when ref's
// .gitattributes use the LFS filter but git-lfs is not installed. A check
// that cannot answer (no ref, not a repository) does not block.
func (w *Workspace) ladoRequireGitLFS(ctx context.Context, repo, ref string) error {
	if !hasGitMetadata(repo) {
		return nil
	}
	uses, err := gitlfs.UsesLFS(ctx, w.binary, repo, ref)
	if err != nil {
		w.logf("gitworktree: git-lfs check skipped for %s: %v", repo, err)
		return nil
	}
	if !uses || w.gitLFSInstalled(ctx) {
		return nil
	}
	name := filepath.Base(repo)
	return &ports.WorkspaceSetupError{
		Repo:    name,
		Summary: fmt.Sprintf("repository %s uses Git LFS (.gitattributes has filter=lfs), but git-lfs is not installed on this computer", name),
		Fix:     ladoGitLFSFix,
		Err:     fmt.Errorf("gitworktree: repository %q: %w", repo, ports.ErrGitLFSMissing),
	}
}

func (w *Workspace) gitLFSInstalled(ctx context.Context) bool {
	if w.lfsInstalled != nil {
		return w.lfsInstalled(ctx)
	}
	return gitlfs.Installed(ctx, w.binary)
}

// ladoWrapWorktreeAddError gives a failed `git worktree add` a short summary
// ending with the last line git printed. Error() and errors.Is are unchanged.
func ladoWrapWorktreeAddError(repo string, err error) error {
	if err == nil {
		return nil
	}
	name := filepath.Base(repo)
	output := err.Error()
	var cmdErr commandError
	if errors.As(err, &cmdErr) && strings.TrimSpace(cmdErr.output) != "" {
		output = cmdErr.output
	}
	summary := "could not create the worktree of repository " + name
	if line := lastNonEmptyLine(output); line != "" {
		summary += ": " + line
	}
	return &ports.WorkspaceSetupError{Repo: name, Summary: summary, Err: err}
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// ladoGuardHooksDir keeps hooks written into a worktree out of `git status`.
// Husky sets core.hooksPath to an untracked folder inside the checkout
// (.husky/_) and puts a `*` .gitignore there; a new worktree has no such
// folder, so hooks git-lfs writes there show up as untracked files. When the
// effective core.hooksPath is an untracked path inside this worktree, create
// the folder and its `*` .gitignore. The source repository's config and
// info/exclude are left alone (info/exclude is shared by every worktree).
func (w *Workspace) ladoGuardHooksDir(ctx context.Context, worktree string) {
	if err := w.guardHooksDir(ctx, worktree); err != nil {
		w.logf("gitworktree: hooks folder guard skipped for %s: %v", worktree, err)
	}
}

func (w *Workspace) guardHooksDir(ctx context.Context, worktree string) error {
	if !hasGitMetadata(worktree) {
		return nil
	}
	out, err := aoprocess.CommandContext(ctx, w.binary, "-C", worktree, "config", "--type=path", "--get", "core.hooksPath").Output()
	if err != nil {
		// Exit 1 means the key is unset; anything else is worth a log line.
		var exitErr interface{ ExitCode() int }
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil
		}
		return fmt.Errorf("read core.hooksPath: %w", err)
	}
	hooksPath := strings.TrimSpace(string(out))
	if hooksPath == "" {
		return nil
	}
	root, err := physicalAbs(worktree)
	if err != nil {
		return err
	}
	dir := hooksPath
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	dir, err = physicalAbs(dir)
	if err != nil {
		return err
	}
	if !assetPathWithin(root, dir) {
		return nil
	}
	rel := filepath.ToSlash(relativeOrEmpty(root, dir))
	if rel == "" || rel == "." {
		return nil
	}
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return nil
	}
	tracked, err := aoprocess.CommandContext(ctx, w.binary, "-C", root, "ls-files", "--", rel).Output()
	if err != nil {
		return fmt.Errorf("list tracked files under %s: %w", rel, err)
	}
	if strings.TrimSpace(string(tracked)) != "" {
		return nil
	}
	if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
		return nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	ignore, err := os.OpenFile(filepath.Join(dir, ".gitignore"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := ignore.WriteString("*\n")
	closeErr := ignore.Close()
	return errors.Join(writeErr, closeErr)
}

// hasGitMetadata reports whether dir has a .git entry (a folder in a
// checkout, a file in a linked worktree). Fake test paths have none.
func hasGitMetadata(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}
