package ladoqueue

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// gitRepo makes a repository on main with one empty commit per message and
// returns it with the commits, oldest first.
func gitRepo(t *testing.T, messages ...string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	commits := make([]string, 0, len(messages))
	for _, message := range messages {
		git("-c", "user.name=LADO", "-c", "user.email=lado@example.com", "-c", "commit.gpgsign=false",
			"commit", "-q", "--no-verify", "--allow-empty", "-m", message)
		commits = append(commits, git("rev-parse", "HEAD"))
	}
	return dir, commits
}

// Decision e of the step 6 plan: the execution records the plan's base SHA,
// the commit the base names at dispatch and how many commits lie between,
// and what git cannot tell holds nothing up.
func TestDispatchRecordsTheBaseMove(t *testing.T) {
	repo, commits := gitRepo(t, "the base the plan was made on", "moved once", "moved twice")
	planned, head := commits[0], commits[2]
	two, none := 2, 0
	for _, c := range []struct {
		name string
		// dir is the checkout the worker starts in, and planBase the
		// revision's base SHA.
		dir      string
		planBase string
		sha      string
		ahead    *int
		// warning is what LADO logs it could not tell; empty when it could
		// tell all there was.
		warning string
	}{
		{"a base that moved", repo, planned, head, &two, ""},
		{"a base that stayed", repo, head, head, &none, ""},
		{"a plan base git does not know", repo, strings.Repeat("ab", 20), head, nil, "could not count how far"},
		{"a plan without a base SHA", repo, "", head, nil, ""},
		{"a checkout git cannot read", filepath.Join(t.TempDir(), "missing"), planned, "", nil, "could not read the commit"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sf := newStoreFixture(t, func(rev *domain.LadoPlanRevision) { rev.BaseSHA = c.planBase })
			sf.svc = sf.dispatcher(sf.db, nil)
			sf.projects.project.Path = c.dir
			sf.branches.resolved = ports.WorkspaceDefaultBranch{Remote: "origin", Branch: "main", BaseRef: "refs/heads/main"}

			sf.tick(t, t0)

			// The worker starts either way.
			exec := sf.execution(t, sf.session(t, 1).ID)
			if exec.PlanBaseSHA != c.planBase || exec.DispatchBaseSHA != c.sha {
				t.Fatalf("execution base %q at dispatch %q, want %q and %q", exec.PlanBaseSHA, exec.DispatchBaseSHA, c.planBase, c.sha)
			}
			switch {
			case c.ahead == nil && exec.BaseCommitsAhead != nil:
				t.Fatalf("commits ahead = %d, want unknown", *exec.BaseCommitsAhead)
			case c.ahead != nil && (exec.BaseCommitsAhead == nil || *exec.BaseCommitsAhead != *c.ahead):
				t.Fatalf("commits ahead = %v, want %d", exec.BaseCommitsAhead, *c.ahead)
			}
			for _, warning := range []string{"could not count how far", "could not read the commit"} {
				if logged := strings.Contains(sf.logs.String(), warning); logged != (warning == c.warning) {
					t.Fatalf("logged %q: %v, want %v:\n%s", warning, logged, warning == c.warning, sf.logs.String())
				}
			}
		})
	}
}

func TestGitCountsOnlyBetweenCommitIDs(t *testing.T) {
	repo, commits := gitRepo(t, "first", "second")
	ctx := context.Background()
	if n, err := (execGit{}).CountCommits(ctx, repo, commits[0], commits[1]); err != nil || n != 1 {
		t.Fatalf("CountCommits = (%d, %v), want 1", n, err)
	}
	// Refs and anything git would read as an option are not counted.
	for _, from := range []string{"main", "--all", "HEAD~1", strings.ToUpper(commits[0])} {
		if _, err := (execGit{}).CountCommits(ctx, repo, from, commits[1]); err == nil {
			t.Fatalf("CountCommits from %q was accepted", from)
		}
	}
	if sha, err := (execGit{}).RevParse(ctx, repo, "refs/heads/main"); err != nil || sha != commits[1] {
		t.Fatalf("RevParse = (%q, %v), want %s", sha, err, commits[1])
	}
	if _, err := (execGit{}).RevParse(ctx, repo, "refs/heads/develop"); err == nil {
		t.Fatal("RevParse of a missing branch succeeded")
	}
}
