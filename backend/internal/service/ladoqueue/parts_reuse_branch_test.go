package ladoqueue

// Parts runs in workspace projects (#60): every worker of a parts run, and a
// resolve worker, asks AO to work on the branch LADO made rather than on a
// new name; a part done without a commit stops the run unless its plan
// allows it; and a parts run merges in the issue's repository only.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A part whose worker reports done with no commit on its branch, in a plan
// that does not mark it allow_empty, is not merged: the run fails and names
// the part and why, the integration branch stays where it was and no
// integration worker starts.
func TestAPartDoneWithoutACommitFailsTheRun(t *testing.T) {
	pf := newPartsFixture(t)
	strict := strings.Replace(partsPlanText, "    allow_empty: true\n", "", 1)
	if strict == partsPlanText {
		t.Fatal("partsPlanText no longer marks c allow_empty")
	}
	pf.tasks.input.PlanText = strict
	ctx := context.Background()
	integration := domain.LadoIntegrationBranch("task-1", storeEntry)

	pf.tick(t, t0)
	parts := pf.parts(t, storeEntry)
	pf.commitParts(t, "a", "b")
	pf.reportDone(t, parts["a"].SessionID, "added the service")
	pf.reportDone(t, parts["b"].SessionID, "added the page")
	pf.tick(t, t0.Add(time.Minute))
	parts = pf.parts(t, storeEntry)
	if parts["a"].State != domain.LadoPartMerged || parts["b"].State != domain.LadoPartMerged || parts["c"].State != domain.LadoPartRunning {
		t.Fatalf("parts = %+v, want a and b merged and c running", parts)
	}
	head := pf.branch(t, integration)

	pf.reportDone(t, parts["c"].SessionID, "nothing to change")
	pf.tick(t, t0.Add(2*time.Minute))
	c := pf.parts(t, storeEntry)["c"]
	if c.State != domain.LadoPartFailed || c.MergedSHA != "" || !strings.Contains(c.Error, "no commit after its start") {
		t.Fatalf("part c = %+v, want it failed for its missing commit", c)
	}
	task, _, _ := pf.db.LadoTask(ctx, "task-1")
	if state := task.StateAt(t0.Add(2 * time.Minute)); state != domain.LadoTaskFailed || task.Queue.FailedAt.IsZero() ||
		!strings.HasPrefix(task.Queue.ErrorMessage, "part c: ") || !strings.Contains(task.Queue.ErrorMessage, "no commit after its start") {
		t.Fatalf("task = %s, entry %+v; want failed with part c and the reason", state, task.Queue)
	}
	if got := pf.branch(t, integration); got != head {
		t.Fatalf("integration branch moved to %s, want it left at %s", got, head)
	}
	pf.tick(t, t0.Add(3*time.Minute))
	for _, cfg := range pf.sessions.spawned() {
		if strings.Contains(cfg.ClientRequestID, "/integration/") {
			t.Fatalf("an integration worker started after the run failed: %s", cfg.ClientRequestID)
		}
	}
	pf.requireUntouched(t)
}

// Every worker of a parts run starts on a branch LADO made, or on the
// integration branch, and asks AO to keep that branch as it is; so does a
// resolve worker. A single worker's branch is left to AO's naming.
func TestRunWorkersAskToReuseExistingBranch(t *testing.T) {
	t.Run("part, merge and integration workers", func(t *testing.T) {
		pf := newPartsFixture(t)
		pf.tick(t, t0)
		parts := pf.parts(t, storeEntry)
		commitOn(t, pf.repo, parts["a"].Branch, map[string]string{"shared.txt": "start\na\n"})
		bTip := commitOn(t, pf.repo, parts["b"].Branch, map[string]string{"shared.txt": "start\nb\n"})
		pf.reportDone(t, parts["a"].SessionID, "a")
		pf.reportDone(t, parts["b"].SessionID, "b")
		pf.tick(t, t0.Add(time.Minute))
		parts = pf.parts(t, storeEntry)
		if parts["b"].State != domain.LadoPartConflict || parts["b"].MergeSessionID == "" {
			t.Fatalf("part b = %+v, want a merge worker on its conflict", parts["b"])
		}
		head := pf.branch(t, domain.LadoIntegrationBranch("task-1", storeEntry))
		commitOn(t, pf.repo, parts["b"].MergeBranch, map[string]string{"shared.txt": "start\na\nb\n"}, head, bTip)
		pf.reportDone(t, parts["b"].MergeSessionID, "kept both")
		pf.reportDone(t, parts["c"].SessionID, "nothing to change")
		pf.tick(t, t0.Add(2*time.Minute))

		roles := map[string]bool{}
		for _, cfg := range pf.sessions.spawned() {
			req, ok := domain.ParseLadoRunClientRequestID(cfg.ClientRequestID)
			if !ok {
				t.Fatalf("spawn %s is not a parts run worker's", cfg.ClientRequestID)
			}
			roles[req.Role] = true
			if !cfg.ReuseExistingBranch || cfg.Branch == "" {
				t.Fatalf("spawn %s on %q does not ask to reuse its branch", cfg.ClientRequestID, cfg.Branch)
			}
		}
		for _, role := range []string{domain.LadoRolePart, domain.LadoRoleMerge, domain.LadoRoleIntegration} {
			if !roles[role] {
				t.Fatalf("no %s worker started: %v", role, spawnIDs(pf.sessions.spawned()))
			}
		}
	})

	t.Run("a resolve worker, and not a single worker", func(t *testing.T) {
		rf := newResolveFixture(t)
		rf.exitRun(t)
		rf.tick(t, t0.Add(3*time.Minute))
		spawns := rf.sessions.spawned()
		if len(spawns) != 2 {
			t.Fatalf("spawned %v, want the single worker and one resolve worker", spawnIDs(spawns))
		}
		if single := spawns[0]; single.ReuseExistingBranch || single.Branch != "" {
			t.Fatalf("single worker spawn asks for branch %q, reuse %v; want AO's naming", single.Branch, single.ReuseExistingBranch)
		}
		if resolve := rf.resolveSpawns(); len(resolve) != 1 || !resolve[0].ReuseExistingBranch {
			t.Fatalf("resolve spawns = %+v, want one that reuses its branch", resolve)
		}
	})
}

// workspacePlanText is a plan of task-1 in a workspace project whose paths
// all lie in back/, the folder of the issue's repository.
const workspacePlanText = `---
task: task-1
issue: "github:acme/app#12"
base_ref: main
steps:
  - Add the service
  - Add the job
  - Check everything together
acceptance_criteria:
  - works
affected_paths:
  - "back/svc/**"
  - "back/jobs/**"
test_commands: []
risk: low
permission_ceiling: auto
review:
  required: false
plan_source: issue
parts:
  - id: a
    title: Service
    steps: [1]
    affected_paths: ["back/svc/**"]
  - id: b
    title: Job
    steps: [2]
    affected_paths: ["back/jobs/**"]
---

# Plan
`

// workspaceFixture is the parts fixture in a workspace project: a root
// folder holding the back repository, whose origin is the issue's, and the
// front repository of another origin.
type workspaceFixture struct {
	*partsFixture
	root, back, front string
}

func newWorkspaceFixture(t *testing.T, plan string) *workspaceFixture {
	t.Helper()
	pf := newPartsFixture(t)
	root := t.TempDir()
	back, front := filepath.Join(root, "back"), filepath.Join(root, "front")
	for _, dir := range []string{back, front} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, nil, "init", "-q", "-b", "main")
		if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("start\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, nil, "add", "shared.txt")
		gitIn(t, dir, nil, "commit", "-q", "-m", "init")
		gitIn(t, dir, nil, "branch", "feature")
	}
	gitIn(t, back, nil, "remote", "add", "origin", "https://github.com/acme/app.git")
	gitIn(t, front, nil, "remote", "add", "origin", "https://github.com/acme/web.git")
	pf.projects.project.Path, pf.projects.project.Kind = root, domain.ProjectKindWorkspace
	pf.projects.repos = []domain.WorkspaceRepoRecord{
		{ProjectID: "proj-1", Name: "front", RelativePath: "front", RepoOriginURL: "https://github.com/acme/web.git", DefaultBranch: "main"},
		{ProjectID: "proj-1", Name: "back", RelativePath: "back", RepoOriginURL: "https://github.com/acme/app.git", DefaultBranch: "main"},
	}
	pf.tasks.input.PlanText = plan
	// The parts fixture's own repository is back from here on.
	pf.repo, pf.main = back, gitIn(t, back, nil, "rev-parse", "main")
	pf.git.sha = pf.main
	return &workspaceFixture{partsFixture: pf, root: root, back: back, front: front}
}

// ladoBranches lists the LADO branches of the repository at dir.
func ladoBranches(t *testing.T, dir string) string {
	t.Helper()
	return gitIn(t, dir, nil, "for-each-ref", "--format=%(refname)", "refs/heads/lado/")
}

// In a workspace project the parts run works in the repository that holds
// the issue: its integration branch and the parts' branches are back's, the
// commits of the parts' workers merge into back's integration branch, and
// the integration worker starts there. Every worker asks to keep its branch.
func TestAWorkspacePartsRunMergesInTheIssuesRepository(t *testing.T) {
	wf := newWorkspaceFixture(t, workspacePlanText)
	ctx := context.Background()
	integration := domain.LadoIntegrationBranch("task-1", storeEntry)

	wf.tick(t, t0)
	run, ok, err := wf.db.LadoPartRun(ctx, storeEntry)
	if err != nil || !ok || run.RepoDir != wf.back {
		t.Fatalf("parts run = %+v (%v, %v), want it in %s", run, ok, err, wf.back)
	}
	if got := wf.branch(t, integration); got != wf.main {
		t.Fatalf("back's integration branch = %q, want it at back's main %s", got, wf.main)
	}
	parts := wf.parts(t, storeEntry)
	aTip := commitOn(t, wf.back, parts["a"].Branch, map[string]string{"svc/a.go": "package svc\n"})
	bTip := commitOn(t, wf.back, parts["b"].Branch, map[string]string{"jobs/b.go": "package jobs\n"})
	wf.reportDone(t, parts["a"].SessionID, "added the service")
	wf.reportDone(t, parts["b"].SessionID, "added the job")
	wf.tick(t, t0.Add(time.Minute))
	parts = wf.parts(t, storeEntry)
	head := wf.branch(t, integration)
	for id, tip := range map[string]string{"a": aTip, "b": bTip} {
		if parts[id].State != domain.LadoPartMerged || parts[id].MergedSHA == parts[id].StartSHA {
			t.Fatalf("part %s = %+v, want it merged with its commit", id, parts[id])
		}
		if ok, err := (execPartGit{}).IsAncestor(ctx, wf.back, tip, head); err != nil || !ok {
			t.Fatalf("part %s's commit %s is not on back's integration branch %s (%v)", id, tip, head, err)
		}
	}
	if fileAt(t, wf.back, integration, "svc/a.go") != "package svc" || fileAt(t, wf.back, integration, "jobs/b.go") != "package jobs" {
		t.Fatal("back's integration branch lacks the parts' files")
	}
	spawns := wf.sessions.spawned()
	if len(spawns) != 3 || spawns[2].ClientRequestID != "lado:lado-queue-1/integration/1" || spawns[2].Branch != integration {
		t.Fatalf("spawned %v, want both parts and the integration worker on %s", spawnIDs(spawns), integration)
	}
	for _, cfg := range spawns {
		if !cfg.ReuseExistingBranch {
			t.Fatalf("spawn %s does not ask to reuse its branch", cfg.ClientRequestID)
		}
	}
	if got := ladoBranches(t, wf.front); got != "" {
		t.Fatalf("front has LADO branches %q, want none", got)
	}
	wf.requireUntouched(t)
}

// A plan with parts whose paths leave the issue's repository, pinned before
// plans were checked for it, fails at dispatch: its parts run would merge in
// back only and lose front's work. No integration branch is made and no
// worker starts.
func TestAWorkspacePartsPlanOutsideTheRepositoryFails(t *testing.T) {
	plan := strings.Replace(workspacePlanText, `  - "back/jobs/**"
`, `  - "back/jobs/**"
  - "front/**"
`, 1)
	plan = strings.Replace(plan, `affected_paths: ["back/jobs/**"]`, `affected_paths: ["back/jobs/**", "front/**"]`, 1)
	wf := newWorkspaceFixture(t, plan)
	ctx := context.Background()

	wf.tick(t, t0)
	task, _, _ := wf.db.LadoTask(ctx, "task-1")
	if task.Queue.FailedAt.IsZero() || !strings.HasPrefix(task.Queue.ErrorMessage, "LADO_PARTS_OUTSIDE_REPO: ") ||
		!strings.Contains(task.Queue.ErrorMessage, "front/**") || !strings.Contains(task.Queue.ErrorMessage, "back/") {
		t.Fatalf("entry = %+v, want it failed for the paths outside back", task.Queue)
	}
	if _, ok, err := wf.db.LadoPartRun(ctx, storeEntry); err != nil || ok {
		t.Fatalf("a parts run was started (%v, %v)", ok, err)
	}
	for _, dir := range []string{wf.back, wf.front} {
		if got := ladoBranches(t, dir); got != "" {
			t.Fatalf("%s has LADO branches %q, want none", dir, got)
		}
	}
	if spawns := wf.sessions.spawned(); len(spawns) != 0 {
		t.Fatalf("spawned %v, want no worker", spawnIDs(spawns))
	}
	wf.tick(t, t0.Add(time.Minute))
	if spawns := wf.sessions.spawned(); len(spawns) != 0 {
		t.Fatalf("spawned %v after the failure, want no worker", spawnIDs(spawns))
	}
}
