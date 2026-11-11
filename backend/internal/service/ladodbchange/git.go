package ladodbchange

// The git reads the database change work needs. Every call runs the system
// git in one repository with every prompt off, a time limit and
// --end-of-options before the revisions, and reads -z output, so a path with
// a line break or a quote stays one path. Nothing here writes a ref but
// Fetch, which updates one remote-tracking branch.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	// gitTimeout bounds one git read, fetchTimeout one fetch.
	gitTimeout   = 15 * time.Second
	fetchTimeout = 60 * time.Second
	// maxGitErrorBytes caps the line of git's stderr an error carries.
	maxGitErrorBytes = 512
	// BlobReadMax caps the bytes a detection reads of a SQL file, enough for
	// its phase marker and far more than a migration needs.
	BlobReadMax = 1 << 20
)

// ErrRefNotFound is the error of a ref that names no commit.
var ErrRefNotFound = errors.New("the ref names no commit")

// Change is one path a diff lists: its status (A, M, D or T), its mode and
// blob on each side, empty on the side where it does not exist.
type Change struct {
	Status           byte
	Path             string
	OldMode, NewMode string
	OldBlob, NewBlob string
}

// TreeEntry is one file of a tree: its mode, blob and path.
type TreeEntry struct {
	Mode, Blob, Path string
}

// Git reads the repositories (ExecGit).
type Git interface {
	// MergeBase returns the best common ancestor of a and b.
	MergeBase(ctx context.Context, dir, a, b string) (string, error)
	// Diff returns the paths that differ from one commit to another, with
	// renames read as a deletion and an addition, in git's order.
	Diff(ctx context.Context, dir, from, to string) ([]Change, error)
	// Tree returns every file of commit's tree, or of the paths given.
	Tree(ctx context.Context, dir, commit string, paths ...string) ([]TreeEntry, error)
	// Blob returns at most limit bytes of a blob and whether it holds more.
	Blob(ctx context.Context, dir, blob string, limit int) ([]byte, bool, error)
	// RemoteHead returns the branch remote's HEAD names
	// (refs/remotes/<remote>/HEAD), ErrRefNotFound without one.
	RemoteHead(ctx context.Context, dir, remote string) (string, error)
	// Fetch updates refs/remotes/<remote>/<branch> from the remote.
	Fetch(ctx context.Context, dir, remote, branch string) error
	// RevParse returns the commit ref names, ErrRefNotFound for none.
	RevParse(ctx context.Context, dir, ref string) (string, error)
}

// ExecGit runs the system git.
type ExecGit struct{}

var _ Git = ExecGit{}

// objectID matches a full SHA-1 or SHA-256 object id.
var objectID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// zeroID matches the id git prints for a side that does not exist.
var zeroID = regexp.MustCompile(`^0+$`)

// IsObjectID reports whether s is a full object id.
func IsObjectID(s string) bool { return objectID.MatchString(s) }

// MergeBase runs git merge-base.
func (ExecGit) MergeBase(ctx context.Context, dir, a, b string) (string, error) {
	if a == "" || b == "" {
		return "", fmt.Errorf("find the merge base of %q and %q: a revision is empty", a, b)
	}
	out, err := runGit(ctx, dir, gitTimeout, "merge-base", "--end-of-options", a, b)
	var gitErr *gitError
	switch {
	case errors.As(err, &gitErr) && gitErr.code == 1 && gitErr.line == "":
		return "", fmt.Errorf("%s and %s share no commit in %s", a, b, dir)
	case err != nil:
		return "", fmt.Errorf("find the merge base of %s and %s in %s: %w", a, b, dir, err)
	}
	return oneObject(out, "merge-base")
}

// Diff runs git diff-tree -r --no-renames --raw -z, which names each side's
// mode and blob, without the developer's diff settings.
func (ExecGit) Diff(ctx context.Context, dir, from, to string) ([]Change, error) {
	if !objectID.MatchString(from) || !objectID.MatchString(to) {
		return nil, fmt.Errorf("diff %q and %q: not commit ids", from, to)
	}
	out, err := runGit(ctx, dir, gitTimeout, "diff-tree", "-r", "--no-commit-id", "--no-renames", "--raw", "-z",
		"--end-of-options", from, to, "--")
	if err != nil {
		return nil, fmt.Errorf("diff %s and %s in %s: %w", from, to, dir, err)
	}
	changes, err := parseRawDiff(string(out))
	if err != nil {
		return nil, fmt.Errorf("diff %s and %s in %s: %w", from, to, dir, err)
	}
	return changes, nil
}

// parseRawDiff reads diff-tree --raw -z output: for each path
// ":<old mode> <new mode> <old blob> <new blob> <status>" NUL "<path>" NUL.
func parseRawDiff(out string) ([]Change, error) {
	if out == "" {
		return nil, nil
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	if len(fields)%2 != 0 {
		return nil, errors.New("git diff-tree printed an unexpected record")
	}
	changes := make([]Change, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		if !strings.HasPrefix(fields[i], ":") || len(meta) != 5 || meta[4] == "" || fields[i+1] == "" ||
			!objectID.MatchString(meta[2]) || !objectID.MatchString(meta[3]) {
			return nil, errors.New("git diff-tree printed an unexpected record")
		}
		c := Change{Status: meta[4][0], Path: fields[i+1], OldMode: meta[0], NewMode: meta[1], OldBlob: meta[2], NewBlob: meta[3]}
		if zeroID.MatchString(c.OldBlob) {
			c.OldMode, c.OldBlob = "", ""
		}
		if zeroID.MatchString(c.NewBlob) {
			c.NewMode, c.NewBlob = "", ""
		}
		changes = append(changes, c)
	}
	return changes, nil
}

// Tree runs git ls-tree -r -z --full-tree, of the whole tree or of the paths
// given, and keeps the blobs.
func (ExecGit) Tree(ctx context.Context, dir, commit string, paths ...string) ([]TreeEntry, error) {
	if !objectID.MatchString(commit) {
		return nil, fmt.Errorf("list the tree of %q: not a commit id", commit)
	}
	// ls-tree reads "--" as a path: --end-of-options keeps the paths
	// from being read as options.
	args := append([]string{"ls-tree", "-r", "-z", "--full-tree", "--end-of-options", commit}, paths...)
	out, err := runGit(ctx, dir, gitTimeout, args...)
	if err != nil {
		return nil, fmt.Errorf("list the tree of %s in %s: %w", commit, dir, err)
	}
	var entries []TreeEntry
	for record := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if record == "" {
			continue
		}
		meta, path, ok := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || path == "" {
			return nil, fmt.Errorf("list the tree of %s in %s: git printed an unexpected entry", commit, dir)
		}
		if fields[1] != "blob" {
			continue
		}
		entries = append(entries, TreeEntry{Mode: fields[0], Blob: fields[2], Path: path})
	}
	return entries, nil
}

// Blob runs git cat-file blob and keeps at most limit bytes of its output.
func (ExecGit) Blob(ctx context.Context, dir, blob string, limit int) ([]byte, bool, error) {
	if !objectID.MatchString(blob) {
		return nil, false, fmt.Errorf("read blob %q: not an object id", blob)
	}
	capped := &capWriter{limit: limit}
	if err := runGitTo(ctx, dir, gitTimeout, capped, "cat-file", "blob", blob); err != nil {
		return nil, false, fmt.Errorf("read blob %s in %s: %w", blob, dir, err)
	}
	return capped.buf.Bytes(), capped.over, nil
}

// RemoteHead runs git symbolic-ref on refs/remotes/<remote>/HEAD.
func (ExecGit) RemoteHead(ctx context.Context, dir, remote string) (string, error) {
	if !refName(remote) {
		return "", fmt.Errorf("read the HEAD of remote %q: not a remote name", remote)
	}
	out, err := runGit(ctx, dir, gitTimeout, "symbolic-ref", "--quiet", "--end-of-options", "refs/remotes/"+remote+"/HEAD")
	var gitErr *gitError
	switch {
	case errors.As(err, &gitErr) && gitErr.code == 1:
		return "", fmt.Errorf("refs/remotes/%s/HEAD in %s: %w", remote, dir, ErrRefNotFound)
	case err != nil:
		return "", fmt.Errorf("read refs/remotes/%s/HEAD in %s: %w", remote, dir, err)
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "refs/remotes/"+remote+"/")
	if !ok || !refName(branch) {
		return "", fmt.Errorf("refs/remotes/%s/HEAD in %s names no branch of the remote", remote, dir)
	}
	return branch, nil
}

// Fetch runs git fetch of one branch into its remote-tracking ref, tags and
// submodules left out.
func (ExecGit) Fetch(ctx context.Context, dir, remote, branch string) error {
	if !refName(remote) || !refName(branch) {
		return fmt.Errorf("fetch %q from %q: not a branch and remote git can fetch", branch, remote)
	}
	refspec := "+refs/heads/" + branch + ":refs/remotes/" + remote + "/" + branch
	if _, err := runGit(ctx, dir, fetchTimeout, "fetch", "--quiet", "--no-tags", "--no-recurse-submodules",
		"--end-of-options", remote, refspec); err != nil {
		return fmt.Errorf("fetch %s from %s in %s: %w", branch, remote, dir, err)
	}
	return nil
}

// RevParse runs git rev-parse --verify --quiet on ref's commit.
func (ExecGit) RevParse(ctx context.Context, dir, ref string) (string, error) {
	if ref == "" || strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("read ref %q: not a ref", ref)
	}
	out, err := runGit(ctx, dir, gitTimeout, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	var gitErr *gitError
	switch {
	case errors.As(err, &gitErr) && gitErr.code == 1:
		return "", fmt.Errorf("%s in %s: %w", ref, dir, ErrRefNotFound)
	case err != nil:
		return "", fmt.Errorf("read %s in %s: %w", ref, dir, err)
	}
	return oneObject(out, "rev-parse")
}

// refName reports whether name can stand in a refspec as a branch or a
// remote: none of the characters a refspec or a revision reads specially.
func refName(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") ||
		strings.Contains(name, "..") || strings.Contains(name, "//") || strings.Contains(name, "@{") {
		return false
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(`:~^?*[\`, r) {
			return false
		}
	}
	return true
}

// oneObject reads the object id a command printed on its own line.
func oneObject(out []byte, command string) (string, error) {
	sha := strings.TrimSpace(string(out))
	if !objectID.MatchString(sha) {
		return "", fmt.Errorf("git %s printed no object id", command)
	}
	return sha, nil
}

// capWriter keeps the first limit bytes written to it and notes more.
type capWriter struct {
	buf   bytes.Buffer
	limit int
	over  bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.limit - w.buf.Len(); room > 0 {
		if len(p) > room {
			w.buf.Write(p[:room])
			w.over = true
		} else {
			w.buf.Write(p)
		}
	} else if len(p) > 0 {
		w.over = true
	}
	return len(p), nil
}

// gitError is a git command that exited with a failure: its exit code and
// the line of its stderr that says why.
type gitError struct {
	command string
	code    int
	line    string
}

func (e *gitError) Error() string {
	if e.line == "" {
		return fmt.Sprintf("git %s exited with %d", e.command, e.code)
	}
	return fmt.Sprintf("git %s: %s", e.command, e.line)
}

// runGit runs git in dir and returns what it printed.
func runGit(ctx context.Context, dir string, timeout time.Duration, args ...string) ([]byte, error) {
	var out bytes.Buffer
	if err := runGitTo(ctx, dir, timeout, &out, args...); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// runGitTo runs git in dir with every prompt off, writing what it prints to
// stdout. A command that exits with a failure returns a *gitError.
func runGitTo(ctx context.Context, dir string, timeout time.Duration, stdout io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = time.Second
	// GIT_OPTIONAL_LOCKS=0 keeps git from refreshing the index of a work tree
	// an agent may be using.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("git %s gave no answer within %s", args[0], timeout)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("git %s: %w", args[0], ctx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &gitError{command: args[0], code: exitErr.ExitCode(), line: failureLine(stderr.String())}
	}
	return fmt.Errorf("run git %s: %w", args[0], err)
}

// failureLine is the line of git's stderr that says why it failed: the first
// fatal or error line, else the first line with text, without URL
// credentials and capped.
func failureLine(stderr string) string {
	line := ""
	for candidate := range strings.SplitSeq(stderr, "\n") {
		candidate = strings.TrimSpace(candidate)
		if strings.HasPrefix(candidate, "fatal:") || strings.HasPrefix(candidate, "error:") {
			line = candidate
			break
		}
		if line == "" {
			line = candidate
		}
	}
	return cutBytes(redactURLCredentials(line), maxGitErrorBytes)
}
