package ladoverify

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// #53: another PR merged into the remote base after the head left it. A
// change there to a file the head changed or the plan names fails the
// base_branch item as base_overlap; another change needs confirming as
// base_advanced.
func TestBaseMovedUnderTheHead(t *testing.T) {
	// setup makes a head that changes guide.md, then lets upstream change
	// path on main and fetches it into dir.
	setup := func(t *testing.T, path string) (*testRepo, string, string) {
		t.Helper()
		r := newTestRepo(t, t0.Add(-time.Hour))
		work := r.worktree(t0.Add(time.Minute), "lado/work")
		r.write(work, "guide.md", "How to run the app.\nInstall it first.\nStart it.\nStop it.\nUpdate it.\n")
		r.write(work, "src/login.go", "package login\n")
		head := r.commit(t0.Add(2*time.Minute), work, "fix: 안내를 고친다")
		r.write(r.upstream, path, "changed upstream\n")
		r.commit(t0.Add(3*time.Minute), r.upstream, "Change "+path)
		r.publish(t0.Add(3 * time.Minute))
		r.git(t0.Add(4*time.Minute), r.dir, "fetch", "-q", "origin")
		return r, head, r.git(t0, r.dir, "rev-parse", "refs/remotes/origin/main")
	}
	evaluate := func(t *testing.T, in BaseInput) domain.LadoVerifyItem {
		t.Helper()
		item, err := EvaluateBase(context.Background(), ExecGit{}, in)
		if err != nil {
			t.Fatalf("EvaluateBase = %v", err)
		}
		return item
	}
	expect := func(t *testing.T, got domain.LadoVerifyItem, outcome domain.LadoVerifyOutcome, code string, details domain.LadoBaseBranchDetails) {
		t.Helper()
		want, err := domain.NewLadoVerifyItem(domain.LadoVerifyBaseBranch, outcome, code, details)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("base_branch = %s %q %s\nwant %s %q %s", got.Outcome, got.Code, got.Details, want.Outcome, want.Code, want.Details)
		}
	}
	input := func(r *testRepo, head string) BaseInput {
		return BaseInput{Dir: r.dir, Branch: "main", Remote: "origin", HeadSHA: head, StartedAt: t0, Affected: []string{"src/**"}, Ignore: domain.LadoDefaultOverlapIgnore}
	}

	t.Run("a file the head changed too", func(t *testing.T) {
		r, head, tip := setup(t, "guide.md")
		expect(t, evaluate(t, input(r, head)), domain.LadoVerifyFail, domain.LadoCodeBaseOverlap, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin", Advanced: true, AdvancedTip: tip, Overlap: []string{"guide.md"}, OverlapCount: 1,
		})
	})

	t.Run("a file the plan names", func(t *testing.T) {
		r, head, tip := setup(t, "src/session.go")
		expect(t, evaluate(t, input(r, head)), domain.LadoVerifyFail, domain.LadoCodeBaseOverlap, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin", Advanced: true, AdvancedTip: tip, Overlap: []string{"src/session.go"}, OverlapCount: 1,
		})
	})

	t.Run("another file", func(t *testing.T) {
		r, head, tip := setup(t, "CHANGELOG.md")
		expect(t, evaluate(t, input(r, head)), domain.LadoVerifyConfirm, domain.LadoCodeBaseAdvanced, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin", Advanced: true, AdvancedTip: tip,
		})
	})

	t.Run("a file an ignore glob matches", func(t *testing.T) {
		r, head, tip := setup(t, "guide.md")
		in := input(r, head)
		in.Ignore = []string{"*.md"}
		expect(t, evaluate(t, in), domain.LadoVerifyConfirm, domain.LadoCodeBaseAdvanced, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin", Advanced: true, AdvancedTip: tip,
		})
	})

	t.Run("a workspace repository", func(t *testing.T) {
		r, head, tip := setup(t, "src/session.go")
		in := input(r, head)
		in.Prefix, in.Affected = "api", []string{"api/src/**"}
		expect(t, evaluate(t, in), domain.LadoVerifyFail, domain.LadoCodeBaseOverlap, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin", Advanced: true, AdvancedTip: tip, Overlap: []string{"api/src/session.go"}, OverlapCount: 1,
		})
		// The plan's paths of another repository name none of its files.
		in.Affected = []string{"src/**"}
		expect(t, evaluate(t, in), domain.LadoVerifyConfirm, domain.LadoCodeBaseAdvanced, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin", Advanced: true, AdvancedTip: tip,
		})
	})

	t.Run("the head took in the base", func(t *testing.T) {
		r, head, _ := setup(t, "guide.md")
		work := r.root + "/worktrees/lado-work"
		r.git(t0.Add(5*time.Minute), work, "fetch", "-q", "origin")
		r.git(t0.Add(5*time.Minute), work, "merge", "-q", "-X", "ours", "-m", "chore: base를 병합한다", "refs/remotes/origin/main")
		merged := r.git(t0, work, "rev-parse", "HEAD")
		if merged == head {
			t.Fatal("the merge made no commit")
		}
		expect(t, evaluate(t, input(r, merged)), domain.LadoVerifyPass, "", domain.LadoBaseBranchDetails{Branch: "main", Remote: "origin"})
	})

	t.Run("base_overlap ranks first", func(t *testing.T) {
		r, head, tip := setup(t, "guide.md")
		moved := t0.Add(5 * time.Minute)
		r.git(moved, r.root+"/worktrees/lado-work", "update-ref", "refs/heads/main", "HEAD")
		expect(t, evaluate(t, input(r, head)), domain.LadoVerifyFail, domain.LadoCodeBaseOverlap, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin", Advanced: true, AdvancedTip: tip, Overlap: []string{"guide.md"}, OverlapCount: 1,
			Moved: []domain.LadoReflogEntry{{Selector: fmt.Sprintf("main@{%d}", moved.Unix()), Commit: head}},
		})
		// Without the overlap, a move from the worktree ranks before the
		// base's advance.
		in := input(r, head)
		in.Ignore = []string{"*.md"}
		expect(t, evaluate(t, in), domain.LadoVerifyConfirm, domain.LadoCodeBaseMoved, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin", Advanced: true, AdvancedTip: tip,
			Moved: []domain.LadoReflogEntry{{Selector: fmt.Sprintf("main@{%d}", moved.Unix()), Commit: head}},
		})
	})

	t.Run("the PR's target", func(t *testing.T) {
		r, head, _ := setup(t, "guide.md")
		// The PR merges into release, which did not move.
		r.git(t0.Add(5*time.Minute), r.origin, "branch", "release", "main~1")
		r.git(t0.Add(5*time.Minute), r.dir, "fetch", "-q", "origin")
		in := input(r, head)
		in.Target = "release"
		expect(t, evaluate(t, in), domain.LadoVerifyPass, "", domain.LadoBaseBranchDetails{Branch: "main", Remote: "origin"})
	})

	t.Run("a page of overlapping files", func(t *testing.T) {
		r := newTestRepo(t, t0.Add(-time.Hour))
		work := r.worktree(t0.Add(time.Minute), "lado/work")
		r.write(work, "src/login.go", "package login\n")
		head := r.commit(t0.Add(2*time.Minute), work, "fix: 로그인을 고친다")
		var want []string
		for i := range outsidePlanMax + 5 {
			name := fmt.Sprintf("src/gen/file%03d.go", i)
			r.write(r.upstream, name, "package gen\n")
			if i < outsidePlanMax {
				want = append(want, name)
			}
		}
		r.commit(t0.Add(3*time.Minute), r.upstream, "Generate files")
		r.publish(t0.Add(3 * time.Minute))
		r.git(t0.Add(4*time.Minute), r.dir, "fetch", "-q", "origin")
		tip := r.git(t0, r.dir, "rev-parse", "refs/remotes/origin/main")
		expect(t, evaluate(t, input(r, head)), domain.LadoVerifyFail, domain.LadoCodeBaseOverlap, domain.LadoBaseBranchDetails{
			Branch: "main", Remote: "origin", Advanced: true, AdvancedTip: tip, Overlap: want, OverlapCount: outsidePlanMax + 5,
		})
	})
}

// #53, decision b: the new details fields are left out while empty, so the
// record of a head whose base did not move keeps the hash it had before
// them.
func TestBaseRecordHashUnchangedWithoutAMove(t *testing.T) {
	r := newTestRepo(t, t0.Add(-time.Hour))
	work := r.worktree(t0.Add(time.Minute), "lado/work")
	r.write(work, "src/login.go", "package login\n")
	head := r.commit(t0.Add(2*time.Minute), work, "fix: 로그인을 고친다")
	item, err := EvaluateBase(context.Background(), ExecGit{}, BaseInput{
		Dir: r.dir, Branch: "main", Remote: "origin", HeadSHA: head, StartedAt: t0,
		Affected: []string{"src/**"}, Ignore: domain.LadoDefaultOverlapIgnore,
	})
	if err != nil {
		t.Fatal(err)
	}
	const details = `{"branch":"main","remote":"origin","moved":[],"alreadyOnBase":false,"dispatchCommit":"","deleted":false}`
	if string(item.Details) != details {
		t.Errorf("details = %s\nwant %s", item.Details, details)
	}
	record := domain.LadoVerification{PRURL: "https://github.com/acme/demo/pull/1", HeadSHA: verifyHead1, Items: []domain.LadoVerifyItem{item}}
	// The hash of the same record before #53.
	const want = "fa8f6286c361c3482cb81be1a213f53d274eeff05a287ae79d64bc6f745c873e"
	if got := verifySHA(t, record); got != want {
		t.Errorf("record hash = %s\nwant %s", got, want)
	}
}
