package ladoqueue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// gitTimeout bounds one git call.
const gitTimeout = 10 * time.Second

// Git reads where a worker's base stands and how far it moved since the plan
// was made.
type Git interface {
	// RevParse returns the commit ref names in the repository at dir.
	RevParse(ctx context.Context, dir, ref string) (string, error)
	// CountCommits returns how many commits to has that from does not, both
	// commit ids.
	CountCommits(ctx context.Context, dir, from, to string) (int, error)
}

// commitID matches a full SHA-1 or SHA-256 commit id.
var commitID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// execGit runs the system git with every prompt off.
type execGit struct{}

func (execGit) RevParse(ctx context.Context, dir, ref string) (string, error) {
	sha, err := runGit(ctx, dir, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err == nil && sha == "" {
		err = errors.New("no output")
	}
	if err != nil {
		return "", fmt.Errorf("%s names no commit in %s: %w", ref, dir, err)
	}
	return sha, nil
}

func (execGit) CountCommits(ctx context.Context, dir, from, to string) (int, error) {
	if !commitID.MatchString(from) || !commitID.MatchString(to) {
		return 0, fmt.Errorf("count the commits from %q to %q: not commit ids", from, to)
	}
	out, err := runGit(ctx, dir, "rev-list", "--count", "--end-of-options", from+".."+to, "--")
	if err != nil {
		return 0, fmt.Errorf("count the commits from %s to %s in %s: %w", from, to, dir, err)
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0, fmt.Errorf("count the commits from %s to %s in %s: %w", from, to, dir, err)
	}
	return n, nil
}

// runGit runs git in dir and returns its output without the trailing line
// break.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	out, err := cmd.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("git %s gave no answer within %s", args[0], gitTimeout)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
