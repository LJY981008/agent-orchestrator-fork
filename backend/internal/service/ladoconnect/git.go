package ladoconnect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// lsRemoteTimeout bounds the SSH check.
const lsRemoteTimeout = 20 * time.Second

// Git runs the git commands a connection needs.
type Git interface {
	// LsRemote checks that url answers over SSH with every prompt off.
	LsRemote(ctx context.Context, url, sshCommand string) error
	// SetSSHCommand sets core.sshCommand in a repository's local config.
	SetSSHCommand(ctx context.Context, repoPath, sshCommand string) error
}

// sshCommand is the ssh command git runs for the repository. With a key file,
// only that key is offered. batch turns every prompt off, for checks the
// daemon runs; the command stored in a repository leaves prompts to the
// person or agent running git.
func sshCommand(keyPath string, batch bool) string {
	parts := []string{"ssh"}
	if keyPath != "" {
		parts = append(parts, "-i", shellQuote(filepath.ToSlash(keyPath)), "-o", "IdentitiesOnly=yes")
	}
	if batch {
		parts = append(parts, "-o", "BatchMode=yes")
	}
	return strings.Join(parts, " ")
}

// shellQuote quotes s for the shell git runs core.sshCommand and
// GIT_SSH_COMMAND through, on every platform.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// execGit runs the system git.
type execGit struct{}

func (execGit) LsRemote(ctx context.Context, url, sshCommand string) error {
	ctx, cancel := context.WithTimeout(ctx, lsRemoteTimeout)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, "git", "ls-remote", "--", url, "HEAD")
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = time.Second
	// GIT_SSH_COMMAND wins over any core.sshCommand, so the check uses exactly
	// the key the connection names.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "GIT_SSH_COMMAND="+sshCommand)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("git ls-remote gave no answer within %s", lsRemoteTimeout)
	}
	if err != nil {
		if detail := failureLine(output.String()); detail != "" {
			return errors.New(detail)
		}
		return fmt.Errorf("git ls-remote: %w", err)
	}
	return nil
}

func (execGit) SetSSHCommand(ctx context.Context, repoPath, sshCommand string) error {
	cmd := aoprocess.CommandContext(ctx, "git", "-C", repoPath, "config", "--local", "core.sshCommand", sshCommand)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if detail := failureLine(string(output)); detail != "" {
			return fmt.Errorf("set core.sshCommand: %s", detail)
		}
		return fmt.Errorf("set core.sshCommand: %w", err)
	}
	return nil
}

// failureLine returns the line of git's output that names the failure: the
// first one that is not an ssh warning. Later lines are git's generic advice.
func failureLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "Warning:") {
			return line
		}
	}
	return ""
}
