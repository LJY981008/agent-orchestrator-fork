package ladoverify

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// checkPaths evaluates in and compares the item with the outcome, code and
// details wanted.
func checkPaths(t *testing.T, in PathsInput, outcome domain.LadoVerifyOutcome, code string, details domain.LadoPathsDetails) {
	t.Helper()
	got, err := EvaluatePaths(in)
	if err != nil {
		t.Fatalf("EvaluatePaths = %v", err)
	}
	want, err := domain.NewLadoVerifyItem(domain.LadoVerifyPaths, outcome, code, details)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %s %q %s\nwant %s %q %s", got.Outcome, got.Code, got.Details, want.Outcome, want.Code, want.Details)
	}
}

// Owner decision 2: a protected path changes only when the approved plan
// names it, which a catch-all entry does not.
func TestPathsProtectedNeedThePlanToNameThem(t *testing.T) {
	changed := []string{"src/app.go", ".github/workflows/ci.yml"}
	protected := domain.DefaultLadoProtectedPaths()
	for _, tc := range []struct {
		name     string
		affected []string
		on       bool
		outcome  domain.LadoVerifyOutcome
		code     string
		details  domain.LadoPathsDetails
	}{
		{"a catch-all plan", []string{"**"}, true, domain.LadoVerifyFail, domain.LadoCodeProtectedPath,
			domain.LadoPathsDetails{Protected: []string{".github/workflows/ci.yml"}}},
		{"a plan entry that is no more precise than the glob", []string{"src/**", "**/ci.yml"}, true, domain.LadoVerifyFail, domain.LadoCodeProtectedPath,
			domain.LadoPathsDetails{Protected: []string{".github/workflows/ci.yml"}}},
		{"the plan names the file", []string{"src/**", ".github/workflows/ci.yml"}, true, domain.LadoVerifyPass, "", domain.LadoPathsDetails{}},
		{"the plan names its folder", []string{"src/**", ".github/workflows/*"}, true, domain.LadoVerifyPass, "", domain.LadoPathsDetails{}},
		{"the plan names the protected folder", []string{"src", ".github"}, true, domain.LadoVerifyPass, "", domain.LadoPathsDetails{}},
		{"a snapshot without the gate", []string{"**"}, false, domain.LadoVerifyPass, "", domain.LadoPathsDetails{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkPaths(t, PathsInput{Changed: changed, Affected: tc.affected, Protected: protected, ProtectedOn: tc.on}, tc.outcome, tc.code, tc.details)
		})
	}

	t.Run("a glob without a fixed part needs an entry inside it", func(t *testing.T) {
		// Any entry that matches docs/CLAUDE.md has a fixed part as long as
		// that of **/CLAUDE.md, so the entry itself must be a CLAUDE.md.
		for _, tc := range []struct {
			affected []string
			named    bool
		}{
			{[]string{"**"}, false},
			{[]string{"docs/**"}, false},
			{[]string{"docs/*.md"}, false},
			{[]string{"docs/CLAUDE.md"}, true},
			{[]string{"**/CLAUDE.md"}, true},
			{[]string{"docs/**/CLAUDE.md"}, true},
		} {
			in := PathsInput{Changed: []string{"docs/CLAUDE.md"}, Affected: tc.affected, Protected: protected, ProtectedOn: true}
			if tc.named {
				checkPaths(t, in, domain.LadoVerifyPass, "", domain.LadoPathsDetails{})
			} else {
				checkPaths(t, in, domain.LadoVerifyFail, domain.LadoCodeProtectedPath, domain.LadoPathsDetails{Protected: []string{"docs/CLAUDE.md"}})
			}
		}
	})

	t.Run("every protected glob that matches needs naming", func(t *testing.T) {
		// .claude/CLAUDE.md is under .claude/** and is a CLAUDE.md: the plan
		// names both, or the file itself.
		for _, tc := range []struct {
			affected []string
			named    bool
		}{
			{[]string{"**/CLAUDE.md"}, false},
			{[]string{".claude/**"}, false},
			{[]string{".claude/**", "**/CLAUDE.md"}, true},
			{[]string{".claude/CLAUDE.md"}, true},
		} {
			in := PathsInput{Changed: []string{".claude/CLAUDE.md"}, Affected: tc.affected, Protected: protected, ProtectedOn: true}
			if tc.named {
				checkPaths(t, in, domain.LadoVerifyPass, "", domain.LadoPathsDetails{})
			} else {
				checkPaths(t, in, domain.LadoVerifyFail, domain.LadoCodeProtectedPath, domain.LadoPathsDetails{Protected: []string{".claude/CLAUDE.md"}})
			}
		}
	})
}

// Owner decision 2: a change outside the plan's affected_paths does not
// block; the developer confirms it, from a list of the first 50.
func TestPathsOutsidePlanNeedConfirming(t *testing.T) {
	// git gives paths relative to the repository, which is the folder back
	// of a workspace project; the plan's paths are relative to the project.
	changed := []string{"api/login.go"}
	for i := 59; i >= 0; i-- {
		changed = append(changed, fmt.Sprintf("web/page%02d.ts", i))
	}
	changed = append(changed, "web/page07.ts")
	in := PathsInput{
		Changed:     PrefixPaths("back/", changed),
		Affected:    []string{"back/api/**"},
		Protected:   domain.DefaultLadoProtectedPaths(),
		ProtectedOn: true,
	}
	var outside []string
	for i := range 50 {
		outside = append(outside, fmt.Sprintf("back/web/page%02d.ts", i))
	}
	checkPaths(t, in, domain.LadoVerifyConfirm, domain.LadoCodeOutsidePlan, domain.LadoPathsDetails{OutsidePlan: outside, OutsideCount: 60})

	in.Affected = append(in.Affected, "back/web/*.ts")
	checkPaths(t, in, domain.LadoVerifyPass, "", domain.LadoPathsDetails{})

	if got := PrefixPaths("", []string{"web/a.ts"}); !reflect.DeepEqual(got, []string{"web/a.ts"}) {
		t.Fatalf("PrefixPaths without a prefix = %q", got)
	}
}

// A file moved out of a protected folder is a change to the protected path
// too: the old side of the rename counts.
func TestPathsRenameCountsBothSides(t *testing.T) {
	r := newTestRepo(t, t0.Add(-time.Hour))
	workflow := "name: ci\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n"
	r.write(r.dir, ".github/workflows/ci.yml", workflow)
	base := r.commit(t0, r.dir, "ci: 검사 추가")
	r.git(t0, r.dir, "rm", "-q", ".github/workflows/ci.yml")
	r.write(r.dir, "ci/old.yml", workflow)
	head := r.commit(t0.Add(time.Minute), r.dir, "chore: 옛 검사 옮김")
	if status := r.git(t0, r.dir, "diff-tree", "-r", "-M", "--no-commit-id", "--name-status", base, head); !strings.HasPrefix(status, "R100") {
		t.Fatalf("git saw no rename: %q", status)
	}

	changed, err := ExecGit{}.ChangedPaths(context.Background(), r.dir, base, head)
	if err != nil {
		t.Fatalf("ChangedPaths = %v", err)
	}
	in := PathsInput{Changed: changed, Affected: []string{"ci/**"}, Protected: domain.DefaultLadoProtectedPaths(), ProtectedOn: true}
	checkPaths(t, in, domain.LadoVerifyFail, domain.LadoCodeProtectedPath, domain.LadoPathsDetails{
		Protected:    []string{".github/workflows/ci.yml"},
		OutsidePlan:  []string{".github/workflows/ci.yml"},
		OutsideCount: 1,
	})
}
