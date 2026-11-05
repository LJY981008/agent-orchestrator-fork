package ladotask

import (
	"context"
	"fmt"
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

func repoOutside(problems []domain.LadoPlanProblem) []domain.LadoPlanProblem {
	var out []domain.LadoPlanProblem
	for _, p := range problems {
		if p.Code == domain.LadoPlanPartRepoOutside {
			out = append(out, p)
		}
	}
	return out
}

// In a workspace, a plan with parts that touches another repository than
// the issue's cannot be approved: LADO merges the parts in the issue's
// repository only (#60).
func TestWorkspacePartsPlanOutsideTheIssueRepositoryIsAProblem(t *testing.T) {
	cases := []struct {
		name       string
		top, partB []string
		want       []domain.LadoPlanProblem
	}{
		{
			name: "a part in front", top: []string{"back/**", "front/**"}, partB: []string{"front/**"},
			want: []domain.LadoPlanProblem{
				{Code: domain.LadoPlanPartRepoOutside, Field: "affected_paths", Detail: "front/**"},
				{Code: domain.LadoPlanPartRepoOutside, Field: "parts", Detail: "b: front/**"},
			},
		},
		{
			name: "the plan's paths in front", top: []string{"back/**", "front/src/**"}, partB: []string{"back/web/**"},
			want: []domain.LadoPlanProblem{{Code: domain.LadoPlanPartRepoOutside, Field: "affected_paths", Detail: "front/src/**"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pf, _, _ := workspacePlanFixture(t)
			rev := pf.pinLegacy(t, pf.partsPlanFile(tc.top, tc.partB))
			if rev.Valid() || !slices.Equal(repoOutside(rev.Problems), tc.want) {
				t.Fatalf("problems = %+v, want %+v", rev.Problems, tc.want)
			}
			if _, err := pf.svc.ApprovePlan(context.Background(), rev.ID, rev.SHA256, policyA, pf.proof(rev.ID)); apiCode(err) != "LADO_PLAN_INVALID" {
				t.Fatalf("approving = %v, want LADO_PLAN_INVALID", err)
			}
		})
	}
}

// A workspace plan with parts inside the issue's repository, or one without
// parts, has no such problem, and the base resolves in that repository.
func TestWorkspacePlanInsideTheIssueRepositoryIsValid(t *testing.T) {
	pf, git, root := workspacePlanFixture(t)
	rev := pf.pinLegacy(t, pf.partsPlanFile([]string{"back/**"}, []string{"back/web/**"}))
	if !rev.Valid() || rev.BaseSHA != strings.Repeat("c", 40) {
		t.Fatalf("revision = %+v, want a valid plan", rev)
	}
	if want := filepath.Join(root, "back"); len(git.dirs) == 0 || git.dirs[len(git.dirs)-1] != want {
		t.Fatalf("base resolved in %v, want %s", git.dirs, want)
	}

	// One worker runs a plan without parts in the whole workspace.
	pf.now = pf.now.Add(time.Minute)
	unsplit := strings.Replace(pf.planFile("issue"), "  - backend/**\n", "  - back/**\n  - front/**\n", 1)
	if rev := pf.pinLegacy(t, unsplit); !rev.Valid() {
		t.Fatalf("plan without parts: problems %+v", rev.Problems)
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
