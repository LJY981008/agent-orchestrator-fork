package ladoverify

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// #59 with real git: a workspace project holds back (acme/demo, the issue's
// repository) and front (acme/web), each cloned from its own origin. The
// worker's workspace has a worktree of each, and only its front worktree
// has the head the PR in front points at. LADO verifies the PR in front: its
// check command runs in the front worktree, the dry run and the approval
// pass, and the lado/integration status goes to front.
func TestCrossRepoApprovalWithRealGit(t *testing.T) {
	back := newTestRepo(t, t0.Add(-time.Hour))
	front := newTestRepo(t, t0.Add(-time.Hour))
	root, worker := t.TempDir(), t.TempDir()
	clone := func(r *testRepo, name string) string {
		dir := filepath.Join(root, name)
		r.git(t0.Add(-time.Hour), root, "clone", "-q", r.origin, dir)
		r.configure(dir)
		tree := filepath.Join(worker, name)
		r.git(t0.Add(time.Minute), dir, "worktree", "add", "-q", "-b", "lado/work", tree, "refs/remotes/origin/main")
		return tree
	}
	backTree, frontTree := clone(back, "back"), clone(front, "front")
	front.write(frontTree, "src/login.go", "package login\n")
	head := front.commit(t0.Add(2*time.Minute), frontTree, "fix: 로그인 화면 오류를 고친다")

	f := newApproveFixture(t, func(d *Deps) { d.Git = ExecGit{} })
	f.reads.setProject(domain.ProjectRecord{ID: "ws", Path: root, Kind: domain.ProjectKindWorkspace}, []domain.WorkspaceRepoRecord{
		{ProjectID: "ws", Name: "back", RelativePath: "back", RepoOriginURL: "https://github.com/acme/demo.git"},
		{ProjectID: "ws", Name: "front", RelativePath: "front", RepoOriginURL: "https://github.com/acme/web.git"},
	})
	exec := f.add("1", head)
	exec.PR.URL = "https://github.com/acme/web/pull/1"
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.PR.URL = exec.PR.URL })
	f.heads.set(exec.PR.URL, head)
	f.reads.updateTask(exec.TaskID, func(task *domain.LadoTask) { task.ProjectID = "ws" })
	f.reads.updatePlan(exec.PlanRevisionID, func(plan *domain.LadoPlan) { plan.AffectedPaths = []string{"front/src/**"} })
	f.reads.mu.Lock()
	session := f.reads.sessions[exec.SessionID]
	session.Metadata.WorkspacePath = worker
	f.reads.sessions[exec.SessionID] = session
	f.reads.mu.Unlock()
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}}, 30)

	f.tick()
	if specs := f.runner.waitCalls(t, 1); specs[0].Dir != frontTree {
		t.Fatalf("check command ran in %s, want the front worktree %s (back is %s)", specs[0].Dir, frontTree, backTree)
	}
	f.finish(CommandResult{Status: domain.LadoCheckRunPassed, ExitCode: verifyExit(0), Output: []byte("ok")})

	preview := f.preview(exec)
	for _, item := range preview.Verification.Items {
		if item.Outcome == domain.LadoVerifyWait || item.Outcome == domain.LadoVerifyFail {
			t.Errorf("%s item = %s %q %s, want it settled", item.Kind, item.Outcome, item.Code, item.Details)
		}
	}
	if paths := verifyItemOf(t, preview.Verification, domain.LadoVerifyPaths); paths.Outcome != domain.LadoVerifyPass {
		t.Errorf("paths item = %s %q %s, want pass for front/src/login.go", paths.Outcome, paths.Code, paths.Details)
	}
	if got := f.host.branchReads(); len(got) == 0 || slices.ContainsFunc(got, func(r string) bool { return r != "acme/web@main" }) {
		t.Errorf("rules read = %v, want acme/web@main only", got)
	}
	result, err := f.approve(exec, preview.SHA256, preview.Confirm, "nonce-cross-repo")
	if err != nil {
		t.Fatalf("approval = %v (confirm %v)", err, preview.Confirm)
	}
	if want := front.git(t0, front.origin, "rev-parse", "main"); result.Approval.BaseTipSHA != want {
		t.Errorf("approval against %s, want front's main %s", result.Approval.BaseTipSHA, want)
	}
	if posts := f.host.postsMade(); len(posts) != 1 || posts[0].repo.Native != "acme/web" || posts[0].sha != head {
		t.Errorf("statuses posted = %+v, want one on acme/web at the head", posts)
	}
}
