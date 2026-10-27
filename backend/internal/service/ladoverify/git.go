// Package ladoverify verifies the head of a LADO execution's pull request
// before the developer approves its integration (docs/LJY-PRD/TDD.md §8):
// each item of the verification record is computed from git, the tracker
// and LADO's own records.
//
// This file holds the git reads the items share. Every call runs the system
// git in one repository with every prompt off and a time limit, and its
// output is split at NUL or ASCII separator characters, so the line breaks
// of a commit message stay in the message.
package ladoverify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	// gitTimeout bounds one git read.
	gitTimeout = 10 * time.Second
	// fetchTimeout bounds one fetch of a branch.
	fetchTimeout = 30 * time.Second
	// maxGitErrorBytes caps the line of git's stderr an error carries.
	maxGitErrorBytes = 512
)

// ErrRefNotFound is the error of a ref that names no commit in the
// repository.
var ErrRefNotFound = errors.New("the ref names no commit")

// Commit is one commit and its full message.
type Commit struct{ SHA, Message string }

// ReflogEntry is one entry of a ref's reflog: the commit the ref named after
// the update, the entry's selector with its time (as "main@{1793090100}"),
// the update's subject (as "commit: Fix the login"; empty for an update
// without a message) and the time.
type ReflogEntry struct {
	Commit, Selector, Subject string
	At                        time.Time
}

// Git reads a repository for verification. Commits and ChangedPaths take
// commit ids; the other reads take refs or commit ids.
type Git interface {
	// MergeBase returns the best common ancestor of a and b.
	MergeBase(ctx context.Context, dir, a, b string) (string, error)
	// Commits returns the commits to has that from does not, merges left
	// out, oldest first, each with its full message.
	Commits(ctx context.Context, dir, from, to string) ([]Commit, error)
	// ChangedPaths returns the paths that differ between from and to,
	// relative to the repository root, each once: both sides of a rename
	// or a copy.
	ChangedPaths(ctx context.Context, dir, from, to string) ([]string, error)
	// Reflog returns the reflog of ref, newest first; it is empty when git
	// keeps none for the ref.
	Reflog(ctx context.Context, dir, ref string) ([]ReflogEntry, error)
	// IsAncestor reports whether ancestor is descendant or one of its
	// ancestors.
	IsAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error)
	// RevParse returns the commit ref names, or ErrRefNotFound when it
	// names none.
	RevParse(ctx context.Context, dir, ref string) (string, error)
	// StatusClean reports whether the work tree at dir has no change,
	// untracked files included.
	StatusClean(ctx context.Context, dir string) (bool, error)
	// Fetch updates refs/remotes/<remote>/<branch> from the remote's branch.
	Fetch(ctx context.Context, dir, remote, branch string) error
}

// ExecGit runs the system git.
type ExecGit struct{}

var _ Git = ExecGit{}

// commitID matches a full SHA-1 or SHA-256 commit id.
var commitID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

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
	return oneCommit(out, "merge-base")
}

// Commits runs git rev-list --no-merges --reverse with each full message.
func (ExecGit) Commits(ctx context.Context, dir, from, to string) ([]Commit, error) {
	if !commitID.MatchString(from) || !commitID.MatchString(to) {
		return nil, fmt.Errorf("list the commits from %q to %q: not commit ids", from, to)
	}
	out, err := runGit(ctx, dir, gitTimeout, "rev-list", "--no-merges", "--reverse", "--format=%H%x1f%B%x1e", "--end-of-options", from+".."+to, "--")
	if err != nil {
		return nil, fmt.Errorf("list the commits from %s to %s in %s: %w", from, to, dir, err)
	}
	commits, err := parseCommits(string(out))
	if err != nil {
		return nil, fmt.Errorf("list the commits from %s to %s in %s: %w", from, to, dir, err)
	}
	return commits, nil
}

// parseCommits reads the records of rev-list --format=%H%x1f%B%x1e: each a
// "commit <sha>" header line, the sha, \x1f, the full message and \x1e.
func parseCommits(out string) ([]Commit, error) {
	var commits []Commit
	for record := range strings.SplitSeq(out, "\x1e") {
		record = strings.TrimLeft(record, "\n")
		if record == "" {
			continue
		}
		head, message, ok := strings.Cut(record, "\x1f")
		sha := head[strings.LastIndexByte(head, '\n')+1:]
		if !ok || !commitID.MatchString(sha) {
			return nil, errors.New("git rev-list printed an unexpected record")
		}
		commits = append(commits, Commit{SHA: sha, Message: strings.TrimRight(message, "\n")})
	}
	return commits, nil
}

// ChangedPaths runs git diff-tree -r --name-status -M -z, which prints what
// git diff does with those options without the settings of the developer's
// git diff (colour, external tools).
func (ExecGit) ChangedPaths(ctx context.Context, dir, from, to string) ([]string, error) {
	if !commitID.MatchString(from) || !commitID.MatchString(to) {
		return nil, fmt.Errorf("list the paths changed from %q to %q: not commit ids", from, to)
	}
	out, err := runGit(ctx, dir, gitTimeout, "diff-tree", "-r", "--no-commit-id", "--name-status", "-M", "-z", "--end-of-options", from, to, "--")
	if err != nil {
		return nil, fmt.Errorf("list the paths changed from %s to %s in %s: %w", from, to, dir, err)
	}
	paths, err := parseNameStatus(string(out))
	if err != nil {
		return nil, fmt.Errorf("list the paths changed from %s to %s in %s: %w", from, to, dir, err)
	}
	return paths, nil
}

// parseNameStatus reads --name-status -z output: a status, then its path,
// or the two paths of a rename or a copy (R or C with a score). Each path
// is listed once, in the order git gives.
func parseNameStatus(out string) ([]string, error) {
	if out == "" {
		return nil, nil
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	var paths []string
	seen := map[string]bool{}
	for i := 0; i < len(fields); i++ {
		status, n := fields[i], 1
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			n = 2
		}
		if status == "" || i+n >= len(fields) {
			return nil, errors.New("git diff-tree printed an unexpected status")
		}
		for _, path := range fields[i+1 : i+1+n] {
			if !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
		i += n
	}
	return paths, nil
}

// Reflog runs git reflog show with --date=unix, which puts each entry's
// time in its selector.
func (ExecGit) Reflog(ctx context.Context, dir, ref string) ([]ReflogEntry, error) {
	if ref == "" {
		return nil, errors.New("read a reflog: the ref is empty")
	}
	out, err := runGit(ctx, dir, gitTimeout, "reflog", "show", "--no-show-signature", "--date=unix",
		"--format=%H%x1f%gd%x1f%gs%x1e", "--end-of-options", ref, "--")
	if err != nil {
		return nil, fmt.Errorf("read the reflog of %s in %s: %w", ref, dir, err)
	}
	var entries []ReflogEntry
	for record := range strings.SplitSeq(string(out), "\x1e") {
		record = strings.TrimLeft(record, "\n")
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\x1f", 3)
		if len(fields) != 3 || !commitID.MatchString(fields[0]) {
			return nil, fmt.Errorf("read the reflog of %s in %s: git printed an unexpected entry", ref, dir)
		}
		at, err := reflogTime(fields[1])
		if err != nil {
			return nil, fmt.Errorf("read the reflog of %s in %s: %w", ref, dir, err)
		}
		entries = append(entries, ReflogEntry{Commit: fields[0], Selector: fields[1], Subject: fields[2], At: at})
	}
	return entries, nil
}

// reflogTime reads the time of a reflog selector printed with --date=unix,
// as "main@{1793090100}".
func reflogTime(selector string) (time.Time, error) {
	open := strings.LastIndex(selector, "@{")
	if open < 0 || !strings.HasSuffix(selector, "}") {
		return time.Time{}, fmt.Errorf("the reflog selector %q has no time", selector)
	}
	seconds, err := strconv.ParseInt(selector[open+2:len(selector)-1], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("the reflog selector %q has no time", selector)
	}
	return time.Unix(seconds, 0).UTC(), nil
}

// IsAncestor runs git merge-base --is-ancestor.
func (ExecGit) IsAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	if ancestor == "" || descendant == "" {
		return false, fmt.Errorf("tell whether %q is an ancestor of %q: a revision is empty", ancestor, descendant)
	}
	_, err := runGit(ctx, dir, gitTimeout, "merge-base", "--is-ancestor", "--end-of-options", ancestor, descendant)
	var gitErr *gitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &gitErr) && gitErr.code == 1:
		return false, nil
	default:
		return false, fmt.Errorf("tell whether %s is an ancestor of %s in %s: %w", ancestor, descendant, dir, err)
	}
}

// RevParse runs git rev-parse --verify --quiet on the commit of ref.
func (ExecGit) RevParse(ctx context.Context, dir, ref string) (string, error) {
	if ref == "" {
		return "", errors.New("read a ref: the ref is empty")
	}
	out, err := runGit(ctx, dir, gitTimeout, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	var gitErr *gitError
	switch {
	case errors.As(err, &gitErr) && gitErr.code == 1:
		// --quiet turns a ref that names no commit into a silent exit 1.
		return "", fmt.Errorf("%s in %s: %w", ref, dir, ErrRefNotFound)
	case err != nil:
		return "", fmt.Errorf("read %s in %s: %w", ref, dir, err)
	}
	return oneCommit(out, "rev-parse")
}

// StatusClean runs git status --porcelain, which lists the untracked files
// whatever status.showUntrackedFiles says.
func (ExecGit) StatusClean(ctx context.Context, dir string) (bool, error) {
	out, err := runGit(ctx, dir, gitTimeout, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return false, fmt.Errorf("read the status of %s: %w", dir, err)
	}
	return len(bytes.TrimSpace(out)) == 0, nil
}

// Fetch runs git fetch of the one branch into its remote-tracking ref, with
// tags and submodules left out.
func (ExecGit) Fetch(ctx context.Context, dir, remote, branch string) error {
	if !refName(remote) || !refName(branch) {
		return fmt.Errorf("fetch %q from %q: not a branch and remote git can fetch", branch, remote)
	}
	refspec := "+refs/heads/" + branch + ":refs/remotes/" + remote + "/" + branch
	if _, err := runGit(ctx, dir, fetchTimeout, "fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--end-of-options", remote, refspec); err != nil {
		return fmt.Errorf("fetch %s from %s in %s: %w", branch, remote, dir, err)
	}
	return nil
}

// refName reports whether name can stand in a refspec as a branch or a
// remote: none of the characters a refspec or a revision reads specially.
// git refuses the other names a ref cannot have.
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

// oneCommit reads the commit id a command printed on its own line.
func oneCommit(out []byte, command string) (string, error) {
	sha := strings.TrimSpace(string(out))
	if !commitID.MatchString(sha) {
		return "", fmt.Errorf("git %s printed no commit id", command)
	}
	return sha, nil
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

// runGit runs git in dir with every prompt off and returns what it printed.
// A command that exits with a failure returns a *gitError.
func runGit(ctx context.Context, dir string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = time.Second
	// GIT_OPTIONAL_LOCKS=0 keeps status from refreshing the index of a work
	// tree an agent may be using.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("git %s gave no answer within %s", args[0], timeout)
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], ctx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil, &gitError{command: args[0], code: exitErr.ExitCode(), line: failureLine(stderr.String())}
	}
	return nil, fmt.Errorf("run git %s: %w", args[0], err)
}

// credentialInURL matches the user information of a URL, where a remote can
// carry a token.
var credentialInURL = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s@]+@`)

// failureLine is the line of git's stderr that says why it failed: the
// first fatal or error line, else the first line with text. URL credentials
// are cut, and the line is capped.
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
	return redact(line)
}

// redact cuts URL credentials from text git printed and caps it.
func redact(text string) string {
	text = credentialInURL.ReplaceAllString(text, "$1")
	if len(text) > maxGitErrorBytes {
		cut := maxGitErrorBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	return text
}
