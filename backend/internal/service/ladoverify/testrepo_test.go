package ladoverify

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// t0 is when the execution of a test started.
var t0 = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

// testRepo is a bare repository that plays origin, with two clones of it:
// the developer's own (dir), where LADO reads the base branch, and one that
// plays another developer, whose main origin takes as it is (upstream).
// Every git command runs at a time the test gives, so its commits and reflog
// entries carry that time. The gate runs these tests with HOME in a temp
// dir, and each repository sets its own identity and turns signing off.
type testRepo struct {
	t        *testing.T
	root     string
	origin   string
	dir      string
	upstream string
}

// newTestRepo makes origin with one commit, made by upstream at at, and
// clones it into dir with cloneArgs.
func newTestRepo(t *testing.T, at time.Time, cloneArgs ...string) *testRepo {
	t.Helper()
	root := t.TempDir()
	r := &testRepo{t: t, root: root, origin: filepath.Join(root, "origin.git"), dir: filepath.Join(root, "app"), upstream: filepath.Join(root, "upstream")}
	r.git(at, root, "init", "-q", "--bare", "-b", "main", r.origin)
	r.configure(r.origin)
	r.git(at, root, "clone", "-q", r.origin, r.upstream)
	r.configure(r.upstream)
	r.write(r.upstream, "README.md", "# app\n")
	r.write(r.upstream, "guide.md", "How to run the app.\nInstall it.\nStart it.\nStop it.\nUpdate it.\n")
	r.commit(at, r.upstream, "Start the app")
	r.publish(at)
	r.git(at, root, append(append([]string{"clone", "-q"}, cloneArgs...), r.origin, r.dir)...)
	r.configure(r.dir)
	return r
}

// configure gives the repository at dir its own identity and no signing.
func (r *testRepo) configure(dir string) {
	r.t.Helper()
	r.git(t0, dir, "config", "user.name", "LADO")
	r.git(t0, dir, "config", "user.email", "lado@example.com")
	r.git(t0, dir, "config", "commit.gpgsign", "false")
}

// git runs git in dir at at and returns its output without the trailing
// line break.
func (r *testRepo) git(at time.Time, dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	date := fmt.Sprintf("%d +0000", at.Unix())
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date, "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// write writes a file of the work tree at dir.
func (r *testRepo) write(dir, path, content string) {
	r.t.Helper()
	file := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// commit commits every change of the work tree at dir at at, hooks off, and
// returns the commit.
func (r *testRepo) commit(at time.Time, dir, message string) string {
	r.t.Helper()
	r.git(at, dir, "add", "-A")
	r.git(at, dir, "commit", "-q", "--no-verify", "--allow-empty", "-m", message)
	return r.git(at, dir, "rev-parse", "HEAD")
}

// publish makes origin's main upstream's main, as another developer's merge
// would.
func (r *testRepo) publish(at time.Time) {
	r.t.Helper()
	r.git(at, r.origin, "fetch", "-q", r.upstream, "+refs/heads/main:refs/heads/main")
}

// worktree adds a work tree linked to dir on a new branch from origin's
// main, as AO gives a worker, and returns its folder.
func (r *testRepo) worktree(at time.Time, branch string) string {
	r.t.Helper()
	path := filepath.Join(r.root, "worktrees", strings.ReplaceAll(branch, "/", "-"))
	r.git(at, r.dir, "worktree", "add", "-q", "-b", branch, path, "refs/remotes/origin/main")
	return path
}
