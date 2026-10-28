package ladoverify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// skipCommandsOnWindows skips a test whose commands are sh syntax.
func skipCommandsOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the check command tests use sh")
	}
}

// requireCommandEnd fails the test unless the run ended with status and
// exit code (nil: none), with an error exactly when no exit code says why.
func requireCommandEnd(t *testing.T, name string, got CommandResult, status domain.LadoCheckRunStatus, exitCode *int) {
	t.Helper()
	if got.Status != status || (got.ExitCode == nil) != (exitCode == nil) || (exitCode != nil && *got.ExitCode != *exitCode) {
		code := "none"
		if got.ExitCode != nil {
			code = strconv.Itoa(*got.ExitCode)
		}
		t.Fatalf("%s: status %s, exit code %s, err %v; want %s", name, got.Status, code, got.Err, status)
	}
	if (status == domain.LadoCheckRunPassed || exitCode != nil) != (got.Err == nil) {
		t.Fatalf("%s: err = %v, want one only when no exit code says why", name, got.Err)
	}
}

func exitedWith(code int) *int { return &code }

func TestRunCommandPassesFailsAndTimesOut(t *testing.T) {
	skipCommandsOnWindows(t)
	ctx := context.Background()
	dir := t.TempDir()

	passed := RunCommand(ctx, CommandSpec{Dir: dir, Command: "pwd; exit 0", Timeout: time.Minute})
	requireCommandEnd(t, "exit 0", passed, domain.LadoCheckRunPassed, exitedWith(0))
	want, _ := filepath.EvalSymlinks(dir)
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(passed.Output))); got != want {
		t.Fatalf("the command ran in %q, want %q", passed.Output, want)
	}

	failed := RunCommand(ctx, CommandSpec{Dir: dir, Command: "echo broken >&2; exit 3", Timeout: time.Minute})
	requireCommandEnd(t, "exit 3", failed, domain.LadoCheckRunFailed, exitedWith(3))
	if string(failed.Output) != "broken\n" {
		t.Fatalf("exit 3 output = %q, want its stderr", failed.Output)
	}

	start := time.Now()
	timedOut := RunCommand(ctx, CommandSpec{Dir: dir, Command: "sleep 30", Timeout: time.Second})
	requireCommandEnd(t, "sleep 30 with 1s", timedOut, domain.LadoCheckRunTimedOut, nil)
	if elapsed := time.Since(start); elapsed > 10*time.Second || timedOut.Duration < time.Second {
		t.Fatalf("the timed out run took %s (duration %s), want about the 1s limit", elapsed, timedOut.Duration)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(200*time.Millisecond, cancel)
	defer timer.Stop()
	start = time.Now()
	cancelled := RunCommand(cancelCtx, CommandSpec{Dir: dir, Command: "sleep 30", Timeout: time.Minute})
	requireCommandEnd(t, "cancelled sleep 30", cancelled, domain.LadoCheckRunCancelled, nil)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the cancelled run took %s, want it stopped at the cancel", elapsed)
	}
	early := RunCommand(cancelCtx, CommandSpec{Dir: dir, Command: "exit 0", Timeout: time.Minute})
	requireCommandEnd(t, "cancelled before the start", early, domain.LadoCheckRunCancelled, nil)

	missing := RunCommand(ctx, CommandSpec{Dir: filepath.Join(dir, "missing"), Command: "exit 0", Timeout: time.Minute})
	requireCommandEnd(t, "missing folder", missing, domain.LadoCheckRunError, nil)
	for name, spec := range map[string]CommandSpec{
		"no folder":     {Command: "exit 0", Timeout: time.Minute},
		"no command":    {Dir: dir, Command: "  ", Timeout: time.Minute},
		"no time limit": {Dir: dir, Command: "exit 0"},
	} {
		requireCommandEnd(t, name, RunCommand(ctx, spec), domain.LadoCheckRunError, nil)
	}
}

// processGone reports whether pid no longer runs: it does not exist, or it
// is a zombie its new parent has not reaped yet.
func processGone(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil || process.Signal(syscall.Signal(0)) != nil {
		return true
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// The state follows the command name, which ends at the last ')'.
	fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
	return len(fields) > 0 && fields[0] == "Z"
}

// requireProcessGone waits up to 10s for the process whose id the command
// wrote to file in dir to stop.
func requireProcessGone(t *testing.T, dir, file string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatalf("read the child's pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		t.Fatalf("pid file holds %q", raw)
	}
	for deadline := time.Now().Add(10 * time.Second); !processGone(pid); {
		if time.Now().After(deadline) {
			t.Fatalf("process %d still runs after the run ended", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunCommandStopsTheWholeTree(t *testing.T) {
	skipCommandsOnWindows(t)
	ctx := context.Background()
	dir := t.TempDir()

	start := time.Now()
	result := RunCommand(ctx, CommandSpec{Dir: dir, Command: "sleep 60 & echo $! > pid; wait", Timeout: time.Second})
	requireCommandEnd(t, "the shell waiting for its child", result, domain.LadoCheckRunTimedOut, nil)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the run took %s, want it stopped at the 1s limit", elapsed)
	}
	requireProcessGone(t, dir, "pid")

	// A child the shell leaves running when it exits stops with the run too.
	result = RunCommand(ctx, CommandSpec{Dir: dir, Command: "sleep 60 >/dev/null 2>&1 & echo $! > left; exit 0", Timeout: time.Minute})
	requireCommandEnd(t, "the shell leaving its child", result, domain.LadoCheckRunPassed, exitedWith(0))
	requireProcessGone(t, dir, "left")
}

// S4 of #15: a check command runs code an agent wrote, as the developer. It
// gets none of the known ways to the developer's access: tokens, the SSH
// agent, askpass helpers, Git Credential Manager and GitHub CLI settings,
// git credential helpers at any config level, and git over SSH. The project's
// variables come after and may set any of them on purpose.
func TestRunCommandEnvWithholdsCredentials(t *testing.T) {
	skipCommandsOnWindows(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, name := range []string{
		"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GL_TOKEN", "AO_GITHUB_TOKEN", "AO_GITLAB_HOST_TOKENS",
		"SSH_AUTH_SOCK", "GIT_ASKPASS", "SSH_ASKPASS", "SUDO_ASKPASS", "GCM_INTERACTIVE", "GH_HOST", "GITHUB_API_URL", "GITLAB_HOST",
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "NPM_TOKEN", "Claude_Code_OAuth_Token", "GIT_SSH", "GIT_CONFIG_PARAMETERS",
	} {
		t.Setenv(name, "secret-"+strings.ToLower(name))
	}
	t.Setenv("AO_DATA_DIR", "/home/dev/.ao")
	t.Setenv("LADO_SHARED", "daemon")
	t.Setenv("LADO_DAEMON_ONLY", "kept")
	// The developer's git keeps a password for example.com in a store helper,
	// and an URL-specific helper besides, as gh auth setup-git writes one.
	gitconfig := "[user]\n\tname = LADO Dev\n\temail = dev@example.com\n[credential]\n\thelper = store\n" +
		"[credential \"https://example.com\"]\n\thelper = !printf 'username=lado\\npassword=secret-url-helper\\n'\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(gitconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".git-credentials"), []byte("https://lado:secret-stored@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := map[string]string{"LADO_PROJECT": "project value", "LADO_SHARED": "project", "AO_SESSION": "from the project"}

	run := func(env []string, command string) string {
		t.Helper()
		result := RunCommand(context.Background(), CommandSpec{Dir: t.TempDir(), Command: command, Env: env, Timeout: time.Minute})
		requireCommandEnd(t, command, result, domain.LadoCheckRunPassed, exitedWith(0))
		return string(result.Output)
	}
	ours := func(line string) bool {
		return slices.Contains(gitCredentialBlock, line) || strings.HasPrefix(line, "GH_CONFIG_DIR=")
	}
	check := func(name string, lines []string, want ...string) {
		t.Helper()
		for _, line := range lines {
			if (withheldVariable(envName(line)) && !ours(line)) || strings.Contains(line, "secret-") {
				t.Fatalf("%s: the command saw %q", name, line)
			}
		}
		for _, line := range want {
			if !slices.Contains(lines, line) {
				t.Fatalf("%s: the command did not see %q in\n%s", name, line, strings.Join(lines, "\n"))
			}
		}
	}
	env := CommandEnv(os.Environ(), project)
	lines := strings.Split(run(env, "env"), "\n")
	check("project env", lines, append([]string{"LADO_PROJECT=project value", "LADO_SHARED=project", "LADO_DAEMON_ONLY=kept"}, gitCredentialBlock...)...)
	if slices.Contains(lines, "LADO_SHARED=daemon") {
		t.Fatal("the daemon's LADO_SHARED stayed beside the project's")
	}
	// No Env is the daemon's environment without the withheld variables,
	// never the daemon's environment as it is.
	check("no env", strings.Split(run(nil, "env"), "\n"), "LADO_SHARED=daemon", "LADO_DAEMON_ONLY=kept")

	// git finds no credential: no helper runs, and nothing prompts. The
	// developer's own settings stay.
	out := run(env, `printf 'protocol=https\nhost=example.com\n\n' | git credential fill 2>&1; echo "fill=$?"; git config --get user.name`)
	if strings.Contains(out, "secret-") || !strings.Contains(out, "fill=128") || !strings.Contains(out, "LADO Dev") {
		t.Fatalf("git credential fill in a check command printed %q, want no password, a failure and the user's name", out)
	}
	// git over SSH fails without reaching a host.
	out = run(env, `git ls-remote git@example.invalid:lado/app.git 2>&1; echo "ls-remote=$?"`)
	if !strings.Contains(out, "LADO check commands cannot use git over SSH") || strings.Contains(out, "ls-remote=0") {
		t.Fatalf("git ls-remote over SSH printed %q, want LADO's refusal", out)
	}
	// The GitHub CLI's folder is empty and goes with the run.
	out = run(env, `echo "dir=$GH_CONFIG_DIR"; ls -A "$GH_CONFIG_DIR" | wc -l`)
	dir := strings.TrimPrefix(strings.SplitN(out, "\n", 2)[0], "dir=")
	if !strings.Contains(dir, "lado-check-gh-") || strings.TrimSpace(strings.SplitN(out, "\n", 3)[1]) != "0" {
		t.Fatalf("GH_CONFIG_DIR in a check command: %q, want an empty folder of the run", out)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the GitHub CLI's folder %s outlived the run (%v)", dir, err)
	}

	// The project may set any of them on purpose.
	got := CommandEnv([]string{"=C:=C:\\work", "github_token=lower", "Ao_Home=x", "PATH=/bin", "KEEP=1=2", "git_ssh_command=daemon"},
		map[string]string{"": "empty", "PATH": "/opt/bin", "GIT_SSH_COMMAND": "my-transport"})
	want := []string{"=C:=C:\\work", "KEEP=1=2"}
	for _, entry := range gitCredentialBlock {
		if envName(entry) != "GIT_SSH_COMMAND" {
			want = append(want, entry)
		}
	}
	want = append(want, "GIT_SSH_COMMAND=my-transport", "PATH=/opt/bin")
	if !slices.Equal(got, want) {
		t.Fatalf("CommandEnv = %q, want %q", got, want)
	}
}

func TestRunCommandKeepsTheOutputTail(t *testing.T) {
	skipCommandsOnWindows(t)
	dir := t.TempDir()
	var content bytes.Buffer
	for i := 0; content.Len() < 3*MaxCommandOutput; i++ {
		fmt.Fprintf(&content, "output line %07d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "out.txt"), content.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	result := RunCommand(context.Background(), CommandSpec{Dir: dir, Command: "cat out.txt; echo done >&2", Timeout: time.Minute})
	requireCommandEnd(t, "3 MiB of output", result, domain.LadoCheckRunPassed, exitedWith(0))
	want := append(content.Bytes(), "done\n"...)
	want = want[len(want)-MaxCommandOutput:]
	if !bytes.Equal(result.Output, want) {
		t.Fatalf("output = %d bytes ending %q, want the last %d bytes ending %q", len(result.Output), result.Output[max(0, len(result.Output)-40):], len(want), want[len(want)-40:])
	}

	// The ring itself, at a size the cases can see.
	tail := newOutputTail(8)
	for _, step := range []struct{ write, want string }{
		{"", ""},
		{"abc", "abc"},
		{"defgh", "abcdefgh"},
		{"ij", "cdefghij"},
		{"klmnopq", "jklmnopq"},
		{"0123456789abcdefghij", "cdefghij"},
		{"Z", "defghijZ"},
	} {
		if n, err := tail.Write([]byte(step.write)); n != len(step.write) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", step.write, n, err)
		}
		if got := string(tail.Bytes()); got != step.want {
			t.Fatalf("after %q the tail is %q, want %q", step.write, got, step.want)
		}
	}
}

func TestWorktreeReady(t *testing.T) {
	ctx := context.Background()
	git := ExecGit{}
	r := newTestRepo(t, t0)
	base := r.git(t0, r.dir, "rev-parse", "refs/remotes/origin/main")
	wt := r.worktree(t0, "lado/checks")
	r.write(wt, "app.txt", "one\n")
	head := r.commit(t0, wt, "Add the app")

	// Before the head's first run, the worktree must have no change.
	for _, tc := range []struct {
		name, dir, head, want string
		prepare, undo         func()
	}{
		{name: "clean at the head", dir: wt, head: head},
		{name: "head in capitals", dir: wt, head: strings.ToUpper(head)},
		{name: "another head", dir: wt, head: base, want: domain.LadoCodeWorktreeNotAtHead},
		{name: "untracked file", dir: wt, head: head, want: domain.LadoCodeWorktreeNotAtHead,
			prepare: func() { r.write(wt, "notes.txt", "draft\n") }, undo: func() { _ = os.Remove(filepath.Join(wt, "notes.txt")) }},
		{name: "changed file", dir: wt, head: head, want: domain.LadoCodeWorktreeNotAtHead,
			prepare: func() { r.write(wt, "app.txt", "two\n") }, undo: func() { r.write(wt, "app.txt", "one\n") }},
		{name: "not a repository", dir: t.TempDir(), head: head, want: domain.LadoCodeWorktreeNotAtHead},
		{name: "missing folder", dir: filepath.Join(r.root, "gone"), head: head, want: domain.LadoCodeWorktreeMissing},
		{name: "a file", dir: filepath.Join(wt, "app.txt"), head: head, want: domain.LadoCodeWorktreeMissing},
		{name: "no folder", head: head, want: domain.LadoCodeWorktreeMissing},
	} {
		if tc.prepare != nil {
			tc.prepare()
		}
		if got := WorktreeReady(ctx, git, tc.dir, tc.head, nil); got != tc.want {
			t.Fatalf("%s: WorktreeReady = %q, want %q", tc.name, got, tc.want)
		}
		if tc.undo != nil {
			tc.undo()
		}
	}
	if got := WorktreeReady(ctx, git, wt, head, nil); got != "" {
		t.Fatalf("after the changes were undone: %q, want ready", got)
	}

	// V2 of #15: after a run, the worktree may hold what the run left, and
	// nothing else. A first command builds into dist/; the next one runs
	// with that output, but not on an edit the worker did not commit.
	r.write(wt, "dist/app.js", "built\n")
	status, err := git.Status(ctx, wt)
	if err != nil || status == "" {
		t.Fatalf("Status after a build = (%q, %v), want the build output", status, err)
	}
	built := domain.LadoCheckRun{
		Name: "build", Status: domain.LadoCheckRunPassed, StartedAt: t0, FinishedAt: t0.Add(time.Minute),
		WorktreeSHA256: domain.LadoWorktreeStatusSHA256(status),
	}
	// A run LADO marked to run again, or one a stopped daemon left, has no
	// status of its own: the last run that left one counts.
	marker := domain.LadoCheckRun{Name: "build", Status: domain.LadoCheckRunCancelled, StartedAt: t0.Add(2 * time.Minute), FinishedAt: t0.Add(2 * time.Minute)}
	runs := []domain.LadoCheckRun{built, marker}
	if got := WorktreeReady(ctx, git, wt, head, runs); got != "" {
		t.Fatalf("with the run's own output: %q, want ready", got)
	}
	r.write(wt, "app.txt", "edited\n")
	if got := WorktreeReady(ctx, git, wt, head, runs); got != domain.LadoCodeWorktreeChanged {
		t.Fatalf("with an edit after the run: %q, want worktree_changed", got)
	}
	r.write(wt, "app.txt", "one\n")
	r.write(wt, "notes.txt", "draft\n")
	if got := WorktreeReady(ctx, git, wt, head, runs); got != domain.LadoCodeWorktreeChanged {
		t.Fatalf("with a new file after the run: %q, want worktree_changed", got)
	}
	_ = os.Remove(filepath.Join(wt, "notes.txt"))
	if got := WorktreeReady(ctx, git, wt, head, runs); got != "" {
		t.Fatalf("after the edits were undone: %q, want ready", got)
	}
	// A run whose status LADO could not read leaves the one before it.
	unread := domain.LadoCheckRun{Name: "lint", Status: domain.LadoCheckRunPassed, StartedAt: t0.Add(3 * time.Minute), FinishedAt: t0.Add(4 * time.Minute)}
	if got := WorktreeReady(ctx, git, wt, head, append(runs, unread)); got != "" {
		t.Fatalf("after a run without a status: %q, want ready", got)
	}
}

// TestRunCommandHelperProcess is no test of its own: the quoted-arguments
// test runs this test binary as a check command's program, and this prints
// the arguments after "--", one per line in brackets.
func TestRunCommandHelperProcess(t *testing.T) {
	if os.Getenv("LADO_RUNNER_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	for _, arg := range args {
		fmt.Printf("[%s]\n", arg)
	}
	os.Exit(0)
}

// S1 of #15: quoted arguments, with spaces and pipes in them, reach a check
// command's program as written: through cmd /d /s /c on Windows, where Go's
// own quoting gave go test -run "A|B" its quotes and so no test to run, and
// through sh -c elsewhere. The Windows job of CI runs this test.
func TestRunCommandPassesQuotedArguments(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := `"` + exe + `" -test.run=TestRunCommandHelperProcess -- "TestLogin|TestLogout" "two words" plain`
	env := append(CommandEnv(os.Environ(), nil), "LADO_RUNNER_HELPER=1")
	result := RunCommand(context.Background(), CommandSpec{Dir: t.TempDir(), Command: command, Env: env, Timeout: time.Minute})
	requireCommandEnd(t, "the helper", result, domain.LadoCheckRunPassed, exitedWith(0))
	got := strings.ReplaceAll(string(result.Output), "\r\n", "\n")
	if want := "[TestLogin|TestLogout]\n[two words]\n[plain]\n"; got != want {
		t.Fatalf("the program got\n%s\nwant\n%s", got, want)
	}
}
