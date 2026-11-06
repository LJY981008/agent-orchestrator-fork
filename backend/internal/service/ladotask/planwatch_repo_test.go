package ladotask

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// recordingGit resolves every ref to one commit and records the folders it
// was asked in.
type recordingGit struct {
	mu   sync.Mutex
	dirs []string
}

func (g *recordingGit) RevParse(_ context.Context, dir, _ string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.dirs = append(g.dirs, dir)
	return strings.Repeat("c", 40), nil
}

// workspacePlanFixture turns the plan fixture's project into a workspace
// whose back child holds the task's issue and whose front child is another
// repository.
func workspacePlanFixture(t *testing.T) (*planFixture, *recordingGit, string) {
	t.Helper()
	pf := newPlanFixture(t)
	ctx := context.Background()
	project, _, err := pf.store.GetProject(ctx, "demo")
	if err != nil {
		t.Fatalf("read project: %v", err)
	}
	project.Kind = domain.ProjectKindWorkspace
	project.RepoOriginURL = ""
	children := []domain.WorkspaceRepoRecord{
		{Name: "back", RelativePath: "back", RepoOriginURL: "git@github.com:acme/demo.git", RegisteredAt: taskEpoch},
		{Name: "front", RelativePath: "front", RepoOriginURL: "git@github.com:acme/front.git", RegisteredAt: taskEpoch},
	}
	if err := pf.store.UpsertWorkspaceProject(ctx, project, children); err != nil {
		t.Fatalf("make the project a workspace: %v", err)
	}
	git := &recordingGit{}
	pf.svc.deps.Git = git
	return pf, git, project.Path
}

// partsPlanFile is a valid plan with two parts: the plan's paths and part
// b's are given, part a keeps to back/internal/**.
func (pf *planFixture) partsPlanFile(top, partB []string) string {
	list := func(globs []string, indent string) string {
		var b strings.Builder
		for _, g := range globs {
			fmt.Fprintf(&b, "%s- %q\n", indent, g)
		}
		return b.String()
	}
	return fmt.Sprintf(`---
task: %s
issue: %s
base_ref: main
steps:
  - Add the login route
  - Add the login form
  - Open the pull request
acceptance_criteria:
  - A user can sign in
affected_paths:
%stest_commands: []
risk: low
permission_ceiling: auto
review:
  required: false
plan_source: issue
parts:
  - id: a
    title: Route
    steps: [1]
    affected_paths: ["back/internal/**"]
    depends_on: []
  - id: b
    title: Form
    steps: [2]
    affected_paths:
%s    depends_on: []
---

Plan.
`, pf.task.ID, pf.task.IssueRef, list(top, "  "), list(partB, "      "))
}

// pinLegacy has the orchestrator write content for a request made before plan
// workers and returns the revision LADO pinned.
func (pf *planFixture) pinLegacy(t *testing.T, content string) domain.LadoPlanRevision {
	t.Helper()
	pf.legacyRequest(t, "", "", true)
	pf.now = pf.now.Add(time.Minute)
	pf.writePlan(content, domain.ArtifactVersionSourceAgent)
	pf.svc.WatchPlans(context.Background())
	return pf.latest(t).Plan.Latest
}

// repoCodes keeps the problems of the plan's repositories: its repos
// list and its paths against the workspace's children.
func repoCodes(problems []domain.LadoPlanProblem) []domain.LadoPlanProblem {
	var out []domain.LadoPlanProblem
	for _, p := range problems {
		switch p.Code {
		case domain.LadoPlanPartRepoOutside, domain.LadoPlanReposMissing, domain.LadoPlanRepoUnknown, domain.LadoPlanReposInvalid:
			out = append(out, p)
		}
	}
	return out
}

// withRepos gives a plan file a repos list.
func withRepos(content string, repos ...string) string {
	var b strings.Builder
	b.WriteString("repos:\n")
	for _, repo := range repos {
		fmt.Fprintf(&b, "  - %q\n", repo)
	}
	return strings.Replace(content, "test_commands:", b.String()+"test_commands:", 1)
}

// In a workspace, a plan whose paths lie in more than one child repository
// needs a repos list that says the order of their pull requests (#64), with
// or without parts, and a plan's paths lie inside the repositories it runs
// in. Such a plan cannot be approved.
func TestWorkspacePlanAcrossRepositoriesNeedsRepos(t *testing.T) {
	missing := []domain.LadoPlanProblem{{Code: domain.LadoPlanReposMissing, Field: "repos", Detail: "back, front"}}
	cases := []struct {
		name    string
		content func(pf *planFixture) string
		want    []domain.LadoPlanProblem
	}{
		{
			name: "a part in front",
			content: func(pf *planFixture) string {
				return pf.partsPlanFile([]string{"back/**", "front/**"}, []string{"front/**"})
			},
			want: missing,
		},
		{
			name: "the plan's paths in front",
			content: func(pf *planFixture) string {
				return pf.partsPlanFile([]string{"back/**", "front/src/**"}, []string{"back/web/**"})
			},
			want: missing,
		},
		{
			name: "a plan without parts in back and front",
			content: func(pf *planFixture) string {
				return strings.Replace(pf.planFile("issue"), "  - backend/**\n", "  - back/**\n  - front/**\n", 1)
			},
			want: missing,
		},
		{
			name: "repos [back] with a part in front",
			content: func(pf *planFixture) string {
				return withRepos(pf.partsPlanFile([]string{"back/**", "front/**"}, []string{"front/**"}), "back")
			},
			want: []domain.LadoPlanProblem{
				{Code: domain.LadoPlanPartRepoOutside, Field: "affected_paths", Detail: "front/**"},
				{Code: domain.LadoPlanPartRepoOutside, Field: "parts", Detail: "b: front/**"},
			},
		},
		{
			name: "repos [back] for a plan without parts in front",
			content: func(pf *planFixture) string {
				return withRepos(strings.Replace(pf.planFile("issue"), "  - backend/**\n", "  - back/**\n  - front/src/**\n", 1), "back")
			},
			want: []domain.LadoPlanProblem{{Code: domain.LadoPlanPartRepoOutside, Field: "affected_paths", Detail: "front/src/**"}},
		},
		{
			name: "a repos entry that is no child",
			content: func(pf *planFixture) string {
				return withRepos(pf.partsPlanFile([]string{"back/**", "side/**"}, []string{"side/**"}), "back", "side")
			},
			want: []domain.LadoPlanProblem{{Code: domain.LadoPlanRepoUnknown, Field: "repos", Detail: "side"}},
		},
		{
			// The dispatch, the PR tracking and the merge order compare
			// folders exactly, so the plan must name the child as it is.
			name: "a repos entry in another case than its child",
			content: func(pf *planFixture) string {
				return withRepos(pf.partsPlanFile([]string{"Back/**", "front/**"}, []string{"front/**"}), "Back", "front")
			},
			want: []domain.LadoPlanProblem{{Code: domain.LadoPlanRepoUnknown, Field: "repos", Detail: "Back"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pf, _, _ := workspacePlanFixture(t)
			rev := pf.pinLegacy(t, tc.content(pf))
			if rev.Valid() || !slices.Equal(repoCodes(rev.Problems), tc.want) {
				t.Fatalf("problems = %+v, want %+v", rev.Problems, tc.want)
			}
			if _, err := pf.svc.ApprovePlan(context.Background(), rev.ID, rev.SHA256, policyA, pf.proof(rev.ID)); apiCode(err) != "LADO_PLAN_INVALID" {
				t.Fatalf("approving = %v, want LADO_PLAN_INVALID", err)
			}
		})
	}
}

// A workspace plan with parts inside the issue's repository, one without
// parts in one repository, and one across repositories with a repos list
// that holds its paths are valid, and the base resolves in the issue's
// repository.
func TestWorkspacePlanInsideItsRepositoriesIsValid(t *testing.T) {
	pf, git, root := workspacePlanFixture(t)
	rev := pf.pinLegacy(t, pf.partsPlanFile([]string{"back/**"}, []string{"back/web/**"}))
	if !rev.Valid() || rev.BaseSHA != strings.Repeat("c", 40) {
		t.Fatalf("revision = %+v, want a valid plan", rev)
	}
	if want := filepath.Join(root, "back"); len(git.dirs) == 0 || git.dirs[len(git.dirs)-1] != want {
		t.Fatalf("base resolved in %v, want %s", git.dirs, want)
	}

	valid := map[string]string{
		"a plan without parts in back":     strings.Replace(pf.planFile("issue"), "  - backend/**\n", "  - back/**\n", 1),
		"a plan without parts in no child": pf.planFile("issue"),
		"parts in back and front with repos": withRepos(
			pf.partsPlanFile([]string{"back/**", "front/**"}, []string{"front/**"}), "back", "front"),
		"a plan without parts in back and front with repos": withRepos(
			strings.Replace(pf.planFile("issue"), "  - backend/**\n", "  - back/**\n  - front/**\n", 1), "back", "front"),
	}
	for _, name := range slices.Sorted(maps.Keys(valid)) {
		pf.now = pf.now.Add(time.Minute)
		if rev := pf.pinLegacy(t, valid[name]); !rev.Valid() {
			t.Fatalf("%s: problems %+v", name, rev.Problems)
		}
	}
}

// A single repository takes no repos list.
func TestSingleRepositoryPlanWithReposIsAProblem(t *testing.T) {
	pf := newPlanFixture(t)
	rev := pf.pinLegacy(t, withRepos(pf.planFile("issue"), "backend"))
	want := []domain.LadoPlanProblem{{Code: domain.LadoPlanRepoUnknown, Field: "repos", Detail: "backend"}}
	if rev.Valid() || !slices.Equal(repoCodes(rev.Problems), want) {
		t.Fatalf("problems = %+v, want %+v", rev.Problems, want)
	}
}

// A single repository's plan with parts is not checked against a folder.
func TestSingleRepositoryPartsPlanIsNotCheckedAgainstAFolder(t *testing.T) {
	pf := newPlanFixture(t)
	rev := pf.pinLegacy(t, pf.partsPlanFile([]string{"back/**", "frontend/**"}, []string{"frontend/**"}))
	if !rev.Valid() {
		t.Fatalf("problems = %+v, want none", rev.Problems)
	}
}
