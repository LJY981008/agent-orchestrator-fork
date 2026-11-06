// Package gitlfs checks whether a repository uses Git LFS and whether git-lfs
// is installed (LADO #22). Both checks read only; they never run LFS filters.
package gitlfs

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const defaultGitBinary = "git"

// lfsAttributesPathspec matches .gitattributes at the root and at any depth.
const lfsAttributesPathspec = ":(glob)**/.gitattributes"

// UsesLFS reports whether any .gitattributes in ref's tree (at any depth)
// mentions filter=lfs. ref defaults to HEAD. It returns an error when git
// cannot answer, for example when ref does not exist.
func UsesLFS(ctx context.Context, gitBinary, repo, ref string) (bool, error) {
	if strings.TrimSpace(ref) == "" {
		ref = "HEAD"
	}
	cmd := aoprocess.CommandContext(ctx, binary(gitBinary),
		"-C", repo, "grep", "-q", "-e", "filter=lfs", ref, "--", lfsAttributesPathspec)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return false, fmt.Errorf("gitlfs: git grep %s in %s: %w: %s", ref, repo, err, msg)
	}
	return false, fmt.Errorf("gitlfs: git grep %s in %s: %w", ref, repo, err)
}

// Installed reports whether `git lfs version` succeeds.
func Installed(ctx context.Context, gitBinary string) bool {
	return aoprocess.CommandContext(ctx, binary(gitBinary), "lfs", "version").Run() == nil
}

func binary(gitBinary string) string {
	if strings.TrimSpace(gitBinary) == "" {
		return defaultGitBinary
	}
	return gitBinary
}
