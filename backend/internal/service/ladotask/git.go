package ladotask

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// revParseTimeout bounds one base ref lookup.
const revParseTimeout = 10 * time.Second

// listFilesTimeout bounds one listing of a commit's files (#69): a large
// workspace repository may hold hundreds of thousands of them.
const listFilesTimeout = 10 * time.Second

// Git resolves a plan's base ref to the commit it names and lists the files
// of a commit.
type Git interface {
	// RevParse returns the commit ref names in the repository at dir.
	RevParse(ctx context.Context, dir, ref string) (string, error)
	// ListFiles returns the slash-separated paths of every file in the tree
	// of rev in the repository at dir, relative to its root.
	ListFiles(ctx context.Context, dir, rev string) ([]string, error)
}

// execGit runs the system git with every prompt off.
type execGit struct{}

// RevParse tries ref as given, then as origin/<ref>, since a plan names a
// branch the way people do and the checkout may not have it locally.
func (execGit) RevParse(ctx context.Context, dir, ref string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, revParseTimeout)
	defer cancel()
	var lastErr error
	for _, candidate := range []string{ref, "origin/" + ref} {
		cmd := aoprocess.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--verify", "--quiet", "--end-of-options", candidate+"^{commit}")
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
		out, err := cmd.Output()
		if err == nil {
			if sha := strings.TrimSpace(string(out)); sha != "" {
				return sha, nil
			}
		}
		lastErr = err
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("git rev-parse gave no answer within %s", revParseTimeout)
		}
	}
	return "", fmt.Errorf("%s names no commit in %s: %w", ref, dir, lastErr)
}

// ListFiles lists rev's tree with git ls-tree, NUL-separated so that no path
// is quoted.
func (execGit) ListFiles(ctx context.Context, dir, rev string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, listFilesTimeout)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, "git", "-C", dir, "ls-tree", "-r", "--name-only", "-z", "--end-of-options", rev)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	out, err := cmd.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("git ls-tree gave no answer within %s", listFilesTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("list the files of %s in %s: %w", rev, dir, err)
	}
	var files []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	return files, nil
}
