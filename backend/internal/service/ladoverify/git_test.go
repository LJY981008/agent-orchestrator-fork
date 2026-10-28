package ladoverify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestGitHelpersReadCommitsPathsAndReflog(t *testing.T) {
	ctx := context.Background()
	git := ExecGit{}
	r := newTestRepo(t, t0.Add(-time.Hour))
	base := r.git(t0, r.dir, "rev-parse", "HEAD")

	// The worker renames the guide into a folder with a space in its name and
	// edits the readme, then merges a side branch that adds notes.
	work := r.worktree(t0.Add(time.Minute), "lado/work")
	r.git(t0, work, "rm", "-q", "guide.md")
	r.write(work, "docs/the guide.md", "How to run the app.\nInstall it.\nStart it.\nStop it.\nUpdate it.\n")
	r.write(work, "README.md", "# app\n\nSee docs.\n")
	message := "Move the guide into docs\n\nThe guide moves under docs.\nIts text stays as it was."
	first := r.commit(t0.Add(2*time.Minute), work, message)
	r.git(t0, work, "switch", "-q", "-c", "lado/side")
	r.write(work, "notes.txt", "notes\n")
	side := r.commit(t0.Add(3*time.Minute), work, "Add notes")
	r.git(t0, work, "switch", "-q", "lado/work")
	r.git(t0.Add(4*time.Minute), work, "merge", "-q", "--no-ff", "--no-verify", "-m", "Merge the notes", "lado/side")
	head := r.git(t0, work, "rev-parse", "HEAD")

	if got, err := git.MergeBase(ctx, r.dir, "refs/remotes/origin/main", head); err != nil || got != base {
		t.Fatalf("MergeBase = (%q, %v), want %s", got, err, base)
	}
	// The merge is left out; the message keeps its lines.
	commits, err := git.Commits(ctx, r.dir, base, head)
	if want := []Commit{{SHA: first, Message: message}, {SHA: side, Message: "Add notes"}}; err != nil || !reflect.DeepEqual(commits, want) {
		t.Fatalf("Commits = (%q, %v), want %q", commits, err, want)
	}
	if _, err := git.Commits(ctx, r.dir, "refs/remotes/origin/main", head); err == nil {
		t.Fatal("Commits took a ref for a commit id")
	}
	// Both sides of the rename, each path once.
	paths, err := git.ChangedPaths(ctx, r.dir, base, head)
	slices.Sort(paths)
	if want := []string{"README.md", "docs/the guide.md", "guide.md", "notes.txt"}; err != nil || !slices.Equal(paths, want) {
		t.Fatalf("ChangedPaths = (%q, %v), want %q", paths, err, want)
	}
	if paths, err := git.ChangedPaths(ctx, r.dir, head, head); err != nil || len(paths) != 0 {
		t.Fatalf("ChangedPaths of one commit = (%q, %v), want none", paths, err)
	}

	// The reflog of the local main, newest first, each entry's time read
	// from its selector.
	local := r.commit(t0.Add(5*time.Minute), r.dir, "Note the release")
	reflog, err := git.Reflog(ctx, r.dir, "refs/heads/main")
	if err != nil || len(reflog) != 2 {
		t.Fatalf("Reflog = (%+v, %v), want two entries", reflog, err)
	}
	at := t0.Add(5 * time.Minute)
	if want := (ReflogEntry{Commit: local, Selector: fmt.Sprintf("main@{%d}", at.Unix()), Subject: "commit: Note the release", At: at}); reflog[0] != want {
		t.Fatalf("newest reflog entry = %+v, want %+v", reflog[0], want)
	}
	if clone := reflog[1]; clone.Commit != base || !clone.At.Equal(t0.Add(-time.Hour)) || !strings.HasPrefix(clone.Subject, "clone: from ") {
		t.Fatalf("oldest reflog entry = %+v, want the clone of %s an hour before", clone, base)
	}
	if _, err := git.Reflog(ctx, r.dir, "refs/heads/develop"); err == nil {
		t.Fatal("Reflog of a missing branch succeeded")
	}

	for _, c := range []struct {
		ancestor, descendant string
		want                 bool
	}{
		{base, head, true},
		{head, head, true},
		{head, base, false},
		{local, "refs/remotes/origin/main", false},
	} {
		if got, err := git.IsAncestor(ctx, r.dir, c.ancestor, c.descendant); err != nil || got != c.want {
			t.Fatalf("IsAncestor(%s, %s) = (%v, %v), want %v", c.ancestor, c.descendant, got, err, c.want)
		}
	}
	if _, err := git.IsAncestor(ctx, r.dir, strings.Repeat("ab", 20), head); err == nil {
		t.Fatal("IsAncestor of a commit the repository lacks succeeded")
	}

	if sha, err := git.RevParse(ctx, r.dir, "refs/heads/main"); err != nil || sha != local {
		t.Fatalf("RevParse(main) = (%q, %v), want %s", sha, err, local)
	}
	for _, ref := range []string{"refs/heads/develop", strings.Repeat("ab", 20), "--all"} {
		if _, err := git.RevParse(ctx, r.dir, ref); !errors.Is(err, ErrRefNotFound) {
			t.Fatalf("RevParse(%q) = %v, want ErrRefNotFound", ref, err)
		}
	}
	if _, err := git.RevParse(ctx, filepath.Join(r.root, "missing"), "HEAD"); err == nil || errors.Is(err, ErrRefNotFound) {
		t.Fatalf("RevParse in a missing folder = %v, want another error", err)
	}

	if status, err := git.Status(ctx, work); err != nil || status != "" {
		t.Fatalf("Status of a committed work tree = (%q, %v), want none", status, err)
	}
	r.write(work, "scratch.txt", "draft\n")
	if status, err := git.Status(ctx, work); err != nil || status != "?? scratch.txt\n" {
		t.Fatalf("Status with an untracked file = (%q, %v), want the file", status, err)
	}

	// Another developer's commit reaches the remote-tracking branch only
	// through a fetch.
	published := r.commit(t0.Add(6*time.Minute), r.upstream, "Publish a fix")
	r.publish(t0.Add(6 * time.Minute))
	if sha, _ := git.RevParse(ctx, r.dir, "refs/remotes/origin/main"); sha != base {
		t.Fatalf("origin/main before the fetch = %s, want %s", sha, base)
	}
	if err := git.Fetch(ctx, r.dir, "origin", "main"); err != nil {
		t.Fatalf("Fetch = %v", err)
	}
	if sha, err := git.RevParse(ctx, r.dir, "refs/remotes/origin/main"); err != nil || sha != published {
		t.Fatalf("origin/main after the fetch = (%q, %v), want %s", sha, err, published)
	}
	for _, c := range [][2]string{{"origin", "main:refs/heads/main"}, {"origin", "-main"}, {"--upload-pack=x", "main"}, {"origin", "ma*n"}} {
		if err := git.Fetch(ctx, r.dir, c[0], c[1]); err == nil {
			t.Fatalf("Fetch(%q, %q) succeeded", c[0], c[1])
		}
	}
	if err := git.Fetch(ctx, r.dir, "origin", "develop"); err == nil {
		t.Fatal("Fetch of a branch the remote lacks succeeded")
	}
}

// A failing git command carries one line of its stderr, without the
// credentials of a URL.
func TestGitErrorsCarryOneLineWithoutCredentials(t *testing.T) {
	stderr := "Cloning...\nfatal: unable to access 'https://x-access-token:ghp_secret@github.com/acme/app.git/': The requested URL returned error: 403\nhint: check the token\n"
	if got, want := failureLine(stderr), "fatal: unable to access 'https://github.com/acme/app.git/': The requested URL returned error: 403"; got != want {
		t.Fatalf("failureLine = %q, want %q", got, want)
	}
	if got := failureLine("warning: something\n\n"); got != "warning: something" {
		t.Fatalf("failureLine without a fatal line = %q, want the first line", got)
	}
	if got := failureLine(strings.Repeat("é", 400)); len(got) > maxGitErrorBytes || !strings.HasPrefix(got, "é") || strings.ToValidUTF8(got, "") != got {
		t.Fatalf("failureLine of a long line = %d bytes, want at most %d whole characters", len(got), maxGitErrorBytes)
	}
}

// V5 of #15: a head made on the host is in no branch the developer's clone
// fetches. FetchPullHead brings it by the ref the host keeps for the pull
// request, without writing a ref or FETCH_HEAD in the developer's repository.
func TestFetchPullHeadBringsAHeadMadeOnTheHost(t *testing.T) {
	ctx := context.Background()
	git := ExecGit{}
	r := newTestRepo(t, t0)
	// The host made a commit on the pull request's branch, as Update branch
	// does, and keeps it as refs/pull/7/head.
	r.write(r.upstream, "merge.txt", "merged main into the branch\n")
	head := r.commit(t0.Add(time.Minute), r.upstream, "Merge main into the branch")
	r.git(t0.Add(time.Minute), r.upstream, "push", "-q", "origin", "HEAD:refs/pull/7/head")

	if has, err := git.HasCommit(ctx, r.dir, head); err != nil || has {
		t.Fatalf("HasCommit before the fetch = (%v, %v), want false", has, err)
	}
	if err := git.FetchPullHead(ctx, r.dir, "origin", PullHeadRef(domain.TrackerProviderGitHub, 7)); err != nil {
		t.Fatalf("FetchPullHead = %v", err)
	}
	if has, err := git.HasCommit(ctx, r.dir, head); err != nil || !has {
		t.Fatalf("HasCommit after the fetch = (%v, %v), want true", has, err)
	}
	if refs := r.git(t0, r.dir, "for-each-ref", "--format=%(refname)"); strings.Contains(refs, "pull") {
		t.Fatalf("refs after the fetch = %q, want none written", refs)
	}
	if _, err := os.Stat(filepath.Join(r.dir, ".git", "FETCH_HEAD")); err == nil {
		t.Fatal("the fetch wrote FETCH_HEAD")
	}

	// A ref the host does not keep fails; only pull request head refs and
	// remotes git can fetch are asked for.
	if err := git.FetchPullHead(ctx, r.dir, "origin", "refs/pull/8/head"); err == nil {
		t.Fatal("fetching a pull request the host does not have worked")
	}
	for _, tc := range []struct{ remote, ref string }{
		{"origin", "refs/heads/main"},
		{"origin", "refs/pull/0/head"},
		{"origin", "refs/pull/7/head:refs/heads/main"},
		{"--upload-pack=touch", "refs/pull/7/head"},
	} {
		if err := git.FetchPullHead(ctx, r.dir, tc.remote, tc.ref); err == nil {
			t.Fatalf("FetchPullHead(%q, %q) was run", tc.remote, tc.ref)
		}
	}
	if _, err := git.HasCommit(ctx, r.dir, "HEAD"); err == nil {
		t.Fatal("HasCommit of a ref name was run")
	}

	for _, tc := range []struct {
		provider domain.TrackerProvider
		number   int
		want     string
	}{
		{domain.TrackerProviderGitHub, 7, "refs/pull/7/head"},
		{domain.TrackerProviderGitLab, 12, "refs/merge-requests/12/head"},
		{domain.TrackerProviderGitHub, 0, ""},
		{"linear", 3, ""},
	} {
		if got := PullHeadRef(tc.provider, tc.number); got != tc.want {
			t.Errorf("PullHeadRef(%s, %d) = %q, want %q", tc.provider, tc.number, got, tc.want)
		}
	}
}
