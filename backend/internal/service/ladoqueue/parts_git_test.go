package ladoqueue

import (
	"context"
	"errors"
	"testing"
)

// LADO writes only branches under lado/: a branch elsewhere is refused, a
// move from a commit the branch no longer points at fails, and a merge that
// conflicts makes no commit and moves nothing.
func TestPartGitWritesOnlyItsOwnBranches(t *testing.T) {
	ctx := context.Background()
	repo, main := newPartsRepo(t)
	g := execPartGit{}
	for _, name := range []string{"main", "feature", "lado", "lado/../main", "x/lado/y"} {
		if err := g.CreateBranch(ctx, repo, name, main); err == nil {
			t.Fatalf("CreateBranch(%q) was allowed", name)
		}
	}
	if err := g.MoveBranch(ctx, repo, "main", main, main); err == nil {
		t.Fatal("MoveBranch moved main")
	}
	if err := g.CreateBranch(ctx, repo, "lado/t/integration", main); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := g.CreateBranch(ctx, repo, "lado/t/integration", main); err != nil {
		t.Fatalf("CreateBranch again at the same commit: %v", err)
	}
	one := commitOn(t, repo, "lado/t/integration", map[string]string{"shared.txt": "one\n"})
	two := commitOn(t, repo, "feature", map[string]string{"shared.txt": "two\n"}, main)
	if err := g.CreateBranch(ctx, repo, "lado/t/integration", main); err == nil {
		t.Fatal("CreateBranch moved an existing branch")
	}
	if err := g.MoveBranch(ctx, repo, "lado/t/integration", two, main); err == nil {
		t.Fatal("MoveBranch moved a branch that no longer points at the old commit")
	}
	if _, err := g.Merge(ctx, repo, one, two, "chore(lado): 병합"); !errors.Is(err, errMergeConflict) {
		t.Fatalf("Merge of a conflict = %v, want errMergeConflict", err)
	}
	if got, _ := g.BranchSHA(ctx, repo, "lado/t/integration"); got != one {
		t.Fatalf("integration branch = %s after a conflict, want %s", got, one)
	}
	clean := commitOn(t, repo, "lado/t/other", map[string]string{"other.txt": "x\n"}, main)
	merged, err := g.Merge(ctx, repo, one, clean, "chore(lado): 병합")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if parents, _ := g.Parents(ctx, repo, merged); len(parents) != 2 || parents[0] != one || parents[1] != clean {
		t.Fatalf("merge parents = %v", parents)
	}
	if ok, _ := g.IsAncestor(ctx, repo, clean, merged); !ok {
		t.Fatal("the part is not in the merge")
	}
	if got, _ := g.BranchSHA(ctx, repo, "lado/t/integration"); got != one {
		t.Fatal("Merge moved a branch")
	}
}
