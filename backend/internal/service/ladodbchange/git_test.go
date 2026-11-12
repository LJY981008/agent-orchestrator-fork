package ladodbchange

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExecGitReads(t *testing.T) {
	repo := newTestRepo(t, map[string]string{"db/a b.sql": "SELECT 1;\n", "keep.txt": "x\n"})
	g := ExecGit{}
	ctx := context.Background()
	base := repo.git(repo.dir, "rev-parse", "HEAD")
	head := repo.commit(repo.dir, map[string]string{"db/new\nline.sql": "SELECT 2;\n", "keep.txt": "y\n"}, []string{"db/a b.sql"}, "Change")

	if mb, err := g.MergeBase(ctx, repo.dir, base, head); err != nil || mb != base {
		t.Fatalf("MergeBase = %q, %v", mb, err)
	}
	changes, err := g.Diff(ctx, repo.dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Change{}
	for _, c := range changes {
		got[c.Path] = c
	}
	if c := got["db/a b.sql"]; c.Status != 'D' || c.NewBlob != "" || c.OldBlob != repo.blob(base, "db/a b.sql") {
		t.Fatalf("deleted = %+v", c)
	}
	if c := got["db/new\nline.sql"]; c.Status != 'A' || c.OldBlob != "" || c.NewMode != "100644" || !IsObjectID(c.NewBlob) {
		t.Fatalf("added = %+v", c)
	}
	if c := got["keep.txt"]; c.Status != 'M' {
		t.Fatalf("modified = %+v", c)
	}

	tree, err := g.Tree(ctx, repo.dir, head)
	if err != nil || len(tree) != 2 {
		t.Fatalf("Tree = %+v, %v", tree, err)
	}
	some, err := g.Tree(ctx, repo.dir, head, "keep.txt")
	if err != nil || len(some) != 1 || some[0].Path != "keep.txt" {
		t.Fatalf("Tree of a path = %+v, %v", some, err)
	}

	blob := repo.blob(head, "keep.txt")
	if data, more, err := g.Blob(ctx, repo.dir, blob, 100); err != nil || string(data) != "y\n" || more {
		t.Fatalf("Blob = %q %v %v", data, more, err)
	}
	if data, more, err := g.Blob(ctx, repo.dir, blob, 1); err != nil || string(data) != "y" || !more {
		t.Fatalf("capped Blob = %q %v %v", data, more, err)
	}
	if _, _, err := g.Blob(ctx, repo.dir, "--output=x", 1); err == nil {
		t.Fatal("Blob of an option")
	}

	if branch, err := g.RemoteHead(ctx, repo.dir, "origin"); err != nil || branch != "main" {
		t.Fatalf("RemoteHead = %q, %v", branch, err)
	}
	repo.git(repo.dir, "remote", "set-head", "origin", "--delete")
	if _, err := g.RemoteHead(ctx, repo.dir, "origin"); !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("RemoteHead without HEAD = %v", err)
	}

	tip := repo.push(map[string]string{"z.sql": "SELECT 3;\n"}, "Tip")
	if err := g.Fetch(ctx, repo.dir, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	if got, err := g.RevParse(ctx, repo.dir, "refs/remotes/origin/main"); err != nil || got != tip {
		t.Fatalf("RevParse = %q, %v", got, err)
	}
	if _, err := g.RevParse(ctx, repo.dir, "refs/remotes/origin/nope"); !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("RevParse of nothing = %v", err)
	}
	if err := g.Fetch(ctx, repo.dir, "origin", "--upload-pack=x"); err == nil {
		t.Fatal("Fetch of an option")
	}
	if err := g.Fetch(ctx, repo.dir, "origin", "nope"); err == nil {
		t.Fatal("Fetch of a missing branch")
	}
	if _, err := g.Diff(ctx, repo.dir, "HEAD~1", "HEAD"); err == nil {
		t.Fatal("Diff of names")
	}
}

func TestParseRawDiffRefusesOddOutput(t *testing.T) {
	for _, out := range []string{
		":100644 100644 abc def M\x00a\x00",
		"100644 100644 " + strings.Repeat("a", 40) + " " + strings.Repeat("b", 40) + " M\x00a\x00",
		":100644 100644 " + strings.Repeat("a", 40) + " " + strings.Repeat("b", 40) + " M\x00",
	} {
		if _, err := parseRawDiff(out); err == nil {
			t.Fatalf("parsed %q", out)
		}
	}
}

func TestFailureLineCutsCredentials(t *testing.T) {
	line := failureLine("warning: x\nfatal: unable to access 'https://user:tok3n@host/repo.git/': failed\n")
	if strings.Contains(line, "tok3n") || !strings.HasPrefix(line, "fatal:") {
		t.Fatalf("line = %q", line)
	}
}
