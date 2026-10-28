package ladoverify

// This file runs a project's check commands (docs/LJY-PRD/TDD.md §8.2): one
// command at a time in the execution's worktree, through the shell of the
// OS, with a time limit that ends its whole process tree, an environment
// without AO's variables or tracker tokens, and the end of its output. When
// a command runs and which slot it takes is the loop's.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	// MaxCommandOutput is how much output a check command run keeps: the
	// last 1 MiB of its stdout and stderr together.
	MaxCommandOutput = 1 << 20
	// commandWaitDelay bounds the wait for output pipes a detached child
	// still holds after the shell exited or was stopped.
	commandWaitDelay = time.Second
)

// A check command runs the code of the pull request's head, which an agent
// wrote, as the developer, outside any sandbox and with the network (S4 of
// #15). LADO keeps the known ways to the developer's access out of its
// environment: AO's variables, tracker and LLM tokens, the SSH agent,
// askpass helpers and the settings of Git Credential Manager and the GitHub
// CLI. Names compare without case. What the command can still read as the
// same OS user (LADO's token file, ~/.ssh keys, the GitHub CLI's hosts.yml or
// keyring, ~/.git-credentials) stays open until OS isolation (T2,
// docs/LJY-PRD/TDD.md §8.2).
var (
	withheldPrefixes = []string{"AO_", "GCM_", "GH_", "GITHUB_", "GITLAB_", "GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"}
	withheldSuffixes = []string{"ASKPASS", "_TOKEN", "_API_KEY"}
	withheldNames    = []string{"SSH_AUTH_SOCK", "GIT_SSH", "GIT_SSH_COMMAND", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_TERMINAL_PROMPT"}
)

// gitCredentialBlock turns the developer's git credentials off for a check
// command: an empty credential.helper, read last as a command-line setting,
// clears the helpers of every config file, URL-specific ones too; an empty
// core.askPass leaves no askpass program; terminal prompts are off; and git
// over SSH fails, since LADO's own SSH connection rides on the repository's
// core.sshCommand, which GIT_SSH_COMMAND overrides. The developer's other
// settings, such as user.name and user.email, stay. A project that needs one
// of these for its checks sets it in its env, which comes after.
var gitCredentialBlock = []string{
	"GIT_TERMINAL_PROMPT=0",
	"GIT_CONFIG_COUNT=2",
	"GIT_CONFIG_KEY_0=credential.helper",
	"GIT_CONFIG_VALUE_0=",
	"GIT_CONFIG_KEY_1=core.askPass",
	"GIT_CONFIG_VALUE_1=",
	`GIT_SSH_COMMAND=echo "LADO check commands cannot use git over SSH" >&2; exit 1`,
}

// CommandSpec is one check command to run: the folder it runs in, the line
// the shell reads, its environment and its time limit. A nil Env is
// CommandEnv of the daemon's own environment, never that environment as it
// is.
type CommandSpec struct {
	Dir, Command string
	Env          []string
	Timeout      time.Duration
}

// CommandResult is how a check command run ended. ExitCode is set for a
// command that exited by itself (passed, or failed with a code); Output is
// the end of its combined stdout and stderr (MaxCommandOutput); Err says why
// a run that did not pass ended, when an exit code does not.
type CommandResult struct {
	Status   domain.LadoCheckRunStatus
	ExitCode *int
	Output   []byte
	Duration time.Duration
	Err      error
}

// RunCommand runs spec.Command in spec.Dir with sh -c (cmd /c on Windows)
// and waits for it. The time limit or the end of ctx stops the command's
// whole process tree (aoprocess.ConfigureTreeCancellation), and the run is
// timed_out or cancelled; a command that cannot start is error, exit code 0
// passed and any other end failed. On Unix, what the shell leaves running in
// its process group when it exits is stopped as well, so no process of the
// run keeps changing the worktree after it.
func RunCommand(ctx context.Context, spec CommandSpec) CommandResult {
	switch {
	case spec.Dir == "":
		return CommandResult{Status: domain.LadoCheckRunError, Err: errors.New("run a check command: the folder is empty")}
	case strings.TrimSpace(spec.Command) == "":
		return CommandResult{Status: domain.LadoCheckRunError, Err: errors.New("run a check command: the command is empty")}
	case spec.Timeout <= 0:
		return CommandResult{Status: domain.LadoCheckRunError, Err: fmt.Errorf("run a check command: the time limit %s is not positive", spec.Timeout)}
	}
	env := spec.Env
	if env == nil {
		env = CommandEnv(os.Environ(), nil)
	}
	// The GitHub CLI reads its logins from an empty folder of the run's own,
	// unless the project names one.
	if !slices.ContainsFunc(env, func(entry string) bool { return strings.EqualFold(envName(entry), "GH_CONFIG_DIR") }) {
		ghConfig, err := os.MkdirTemp("", "lado-check-gh-")
		if err != nil {
			return CommandResult{Status: domain.LadoCheckRunError, Err: fmt.Errorf("run a check command: make the GitHub CLI's folder: %w", err)}
		}
		defer func() { _ = os.RemoveAll(ghConfig) }()
		env = append(slices.Clip(env), "GH_CONFIG_DIR="+ghConfig)
	}
	// The run's time counts from its time limit's start, so a run that
	// timed out never reads as shorter than the limit.
	started := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	cmd := shellCommand(runCtx, spec.Command)
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.Dir, cmd.Env, cmd.WaitDelay = spec.Dir, env, commandWaitDelay
	// exec gives Stdout and Stderr one pipe when they are the same writer,
	// so their lines keep their order and one goroutine writes the tail.
	output := newOutputTail(MaxCommandOutput)
	cmd.Stdout, cmd.Stderr = output, output

	if err := cmd.Start(); err != nil {
		result := CommandResult{Status: domain.LadoCheckRunError, Duration: time.Since(started), Err: fmt.Errorf("start the check command: %w", err)}
		if ctx.Err() != nil {
			result.Status, result.Err = domain.LadoCheckRunCancelled, fmt.Errorf("the check command was cancelled before it started: %w", ctx.Err())
		}
		return result
	}
	waitErr := cmd.Wait()
	if runtime.GOOS != "windows" {
		// The shell's process group outlives it while a child it left runs.
		// On Windows the shell's process id may name another process once
		// it was waited for, so its tree is left alone there.
		_ = cmd.Cancel()
	}
	result := CommandResult{Output: output.Bytes(), Duration: time.Since(started)}
	result.Status, result.ExitCode, result.Err = commandEnd(ctx, runCtx, cmd.ProcessState, waitErr, spec.Timeout)
	return result
}

// commandEnd reads how a started command ended. A shell that exited by
// itself ran to its end, whatever happened to the contexts while its output
// drained; one a signal stopped was stopped by the time limit, by the caller
// or by someone else. Windows has no signals and a stopped tree exits with
// 1, so the contexts come first there.
func commandEnd(ctx, runCtx context.Context, state *os.ProcessState, waitErr error, limit time.Duration) (domain.LadoCheckRunStatus, *int, error) {
	exited := state != nil && state.Exited()
	if !exited || runtime.GOOS == "windows" {
		switch {
		case ctx.Err() != nil:
			return domain.LadoCheckRunCancelled, nil, fmt.Errorf("the check command was cancelled: %w", ctx.Err())
		case runCtx.Err() != nil:
			return domain.LadoCheckRunTimedOut, nil, fmt.Errorf("the check command ran longer than its time limit of %s", limit)
		}
	}
	if !exited {
		if waitErr == nil {
			waitErr = errors.New("the check command ended without an exit code")
		}
		return domain.LadoCheckRunFailed, nil, fmt.Errorf("the check command was stopped: %w", waitErr)
	}
	code := state.ExitCode()
	if code == 0 {
		return domain.LadoCheckRunPassed, &code, nil
	}
	return domain.LadoCheckRunFailed, &code, nil
}

// CommandEnv is a check command's environment: base, usually the daemon's
// own (os.Environ), without the variables LADO withholds (withheldPrefixes,
// withheldSuffixes, withheldNames), then gitCredentialBlock, then the
// project's variables over both, each replacing an entry of the same name.
// The project's AO_* variables are left out, as for an agent
// (agentlaunch.MergeEnv). Withheld names compare without case; other names
// compare without case on Windows only, as the system does.
func CommandEnv(base []string, project map[string]string) []string {
	names := slices.Sorted(maps.Keys(project))
	overridden := func(name string) bool {
		return slices.ContainsFunc(names, func(other string) bool { return sameVariable(name, other) })
	}
	env := make([]string, 0, len(base)+len(gitCredentialBlock)+len(project))
	for _, entry := range base {
		if name := envName(entry); !withheldVariable(name) && !overridden(name) {
			env = append(env, entry)
		}
	}
	for _, entry := range gitCredentialBlock {
		if !overridden(envName(entry)) {
			env = append(env, entry)
		}
	}
	for _, name := range names {
		if name == "" || aoVariable(name) {
			continue
		}
		env = append(env, name+"="+project[name])
	}
	return env
}

// envName is the name of an environment entry "NAME=value". Windows keeps
// entries such as "=C:=C:\dir", whose name starts with "=".
func envName(entry string) string {
	if entry == "" {
		return ""
	}
	if i := strings.IndexByte(entry[1:], '='); i >= 0 {
		return entry[:i+1]
	}
	return entry
}

func aoVariable(name string) bool {
	return strings.HasPrefix(strings.ToUpper(name), "AO_")
}

func withheldVariable(name string) bool {
	upper := strings.ToUpper(name)
	return slices.ContainsFunc(withheldPrefixes, func(prefix string) bool { return strings.HasPrefix(upper, prefix) }) ||
		slices.ContainsFunc(withheldSuffixes, func(suffix string) bool { return strings.HasSuffix(upper, suffix) }) ||
		slices.Contains(withheldNames, upper)
}

func sameVariable(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// WorktreeReady tells whether the next check command can run on head in the
// worktree at dir, so that the commands test exactly the pushed commit: ""
// when it can, domain.LadoCodeWorktreeMissing when the folder is gone, and
// domain.LadoCodeWorktreeNotAtHead while its HEAD is another commit or git
// cannot read it. Before the head's first run that left a status, the
// worktree must have no change, untracked files included
// (worktree_not_at_head otherwise). After one, its status must be the one
// LADO read when the head's last run ended, which may hold that run's build
// output: any other change, such as an edit the worker did not commit, waits
// as domain.LadoCodeWorktreeChanged.
func WorktreeReady(ctx context.Context, git Git, dir, head string, runs []domain.LadoCheckRun) string {
	if dir == "" {
		return domain.LadoCodeWorktreeMissing
	}
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist) || (err == nil && !info.IsDir()):
		return domain.LadoCodeWorktreeMissing
	case err != nil:
		return domain.LadoCodeWorktreeNotAtHead
	}
	current, err := git.RevParse(ctx, dir, "HEAD")
	if err != nil || !strings.EqualFold(current, head) {
		return domain.LadoCodeWorktreeNotAtHead
	}
	status, err := git.Status(ctx, dir)
	if err != nil {
		return domain.LadoCodeWorktreeNotAtHead
	}
	after, ran := lastWorktreeStatus(runs)
	switch {
	case !ran && strings.TrimSpace(status) != "":
		return domain.LadoCodeWorktreeNotAtHead
	case ran && domain.LadoWorktreeStatusSHA256(status) != after:
		return domain.LadoCodeWorktreeChanged
	}
	return ""
}

// lastWorktreeStatus returns the status hash LADO read after the head's run
// that ended last among those that left one; false when none did, as before
// the first run or after runs a stopped daemon left.
func lastWorktreeStatus(runs []domain.LadoCheckRun) (string, bool) {
	var last domain.LadoCheckRun
	found := false
	for _, run := range runs {
		if run.WorktreeSHA256 == "" || run.FinishedAt.IsZero() {
			continue
		}
		if !found || !run.FinishedAt.Before(last.FinishedAt) {
			last, found = run, true
		}
	}
	return last.WorktreeSHA256, found
}

// outputTail keeps the last bytes written to it in a ring of fixed size, so
// a run's memory does not grow with its output.
type outputTail struct {
	ring []byte
	// next is where the next byte goes; size counts the bytes kept, which
	// fill the ring from its start until it wraps.
	next, size int
}

func newOutputTail(limit int) *outputTail {
	return &outputTail{ring: make([]byte, limit)}
}

func (o *outputTail) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > len(o.ring) {
		p = p[len(p)-len(o.ring):]
	}
	for len(p) > 0 {
		k := copy(o.ring[o.next:], p)
		p = p[k:]
		o.next = (o.next + k) % len(o.ring)
		o.size = min(o.size+k, len(o.ring))
	}
	return n, nil
}

// Bytes returns a copy of the kept bytes, oldest first.
func (o *outputTail) Bytes() []byte {
	if o.size < len(o.ring) {
		return slices.Clone(o.ring[:o.size])
	}
	return append(slices.Clone(o.ring[o.next:]), o.ring[:o.next]...)
}
