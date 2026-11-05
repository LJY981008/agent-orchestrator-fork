package ladoverify

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// #59: in a workspace project the PR may be in another child repository
// than the task's issue. These tests use the loop's fakes; crossrepo_git_test.go
// approves such a PR with real git.

// addCrossRepoExecution adds a workspace execution whose task's issue is in
// the child repository api (acme/demo) and whose PR is in the child
// repository web (acme/web), which the workspace configures with main. The
// plan names web/src/**, and the worker's worktree of web is at head while
// its worktree of api is elsewhere. It returns the execution, the web child
// repository and the worker's worktree of it.
func (f *verifyFixture) addCrossRepoExecution(key, head string) (domain.LadoExecution, string, string) {
	f.t.Helper()
	exec, api := f.addWorkspaceExecution(key, head)
	f.reads.setProject(f.reads.project("ws"), []domain.WorkspaceRepoRecord{
		{ProjectID: "ws", Name: "web", RelativePath: "web", RepoOriginURL: "https://github.com/acme/web.git", DefaultBranch: "main"},
		{ProjectID: "ws", Name: "api", RelativePath: "api", RepoOriginURL: "https://github.com/acme/demo.git"},
	})
	exec.PR.URL = "https://github.com/acme/web/pull/" + key
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.PR.URL = exec.PR.URL })
	f.reads.updatePlan(exec.PlanRevisionID, func(plan *domain.LadoPlan) { plan.AffectedPaths = []string{"web/src/**"} })
	worker := f.reads.worktree(exec.SessionID)
	f.git.setHead(filepath.Join(worker, "api"), verifyHead4)
	tree := filepath.Join(worker, "web")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.git.setHead(tree, head)
	return exec, filepath.Join(filepath.Dir(api), "web"), tree
}

// addCrossRepoApproval is addCrossRepoExecution whose PR the host has at
// head.
func (f *approveFixture) addCrossRepoApproval(key, head string) (domain.LadoExecution, string) {
	f.t.Helper()
	exec, web, _ := f.addCrossRepoExecution(key, head)
	f.heads.set(exec.PR.URL, head)
	return exec, web
}

func baseDetailsOf(t *testing.T, record domain.LadoVerification) (domain.LadoVerifyItem, domain.LadoBaseBranchDetails) {
	t.Helper()
	item := verifyItemOf(t, record, domain.LadoVerifyBaseBranch)
	var details domain.LadoBaseBranchDetails
	if err := json.Unmarshal(item.Details, &details); err != nil {
		t.Fatal(err)
	}
	return item, details
}

// (a) The PR in web is verified in web: its folder, the worker's worktree of
// it, its pull request ref, its rules and its paths. The dispatcher's local
// base commit, the issue repository's, does not count.
func TestCrossRepoVerifiesInThePRRepository(t *testing.T) {
	f := newVerifyFixture(t)
	exec, web, tree := f.addCrossRepoExecution("2", verifyHead1)
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.DispatchLocalBaseSHA = verifyHead3 })
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}}, 30)
	f.git.lack(verifyHead1, "refs/pull/2/head")
	api := filepath.Join(filepath.Dir(web), "api")

	f.tick()
	record := f.record(exec.SessionID, verifyHead1)
	if paths := verifyItemOf(t, record, domain.LadoVerifyPaths); paths.Outcome != domain.LadoVerifyPass {
		t.Errorf("paths item = %s %s %s, want pass for web/src/login.go under web/src/**", paths.Outcome, paths.Code, paths.Details)
	}
	if !f.git.asked(web) || f.git.asked(api) {
		t.Errorf("git read %v, want web %s and not api", f.git.askedDirs(), web)
	}
	if ops := f.git.opsOf(web); !slices.Contains(ops, "fetch origin refs/pull/2/head") {
		t.Errorf("web ops = %v, want the PR's head fetched from its ref", ops)
	}
	if got := f.host.branchReads(); !slices.Equal(got, []string{"acme/web@main"}) {
		t.Errorf("rules read = %v, want acme/web@main", got)
	}
	protection := verifyItemOf(t, record, domain.LadoVerifyProtection)
	if !strings.Contains(string(protection.Details), `"repo":"acme/web"`) {
		t.Errorf("protection details = %s, want acme/web", protection.Details)
	}
	if specs := f.runner.waitCalls(t, 1); specs[0].Dir != tree {
		t.Errorf("check command ran in %s, want the worker's worktree of web %s", specs[0].Dir, tree)
	}
	item, details := baseDetailsOf(t, record)
	if item.Outcome != domain.LadoVerifyPass || item.Code != domain.LadoCodeNoLocalBranch || details.DispatchCommit != "" || details.Deleted {
		t.Errorf("base_branch item = %s %q %+v, want no_local_branch without the dispatcher's commit", item.Outcome, item.Code, details)
	}
}

// (b) A PR in a repository that is no child repository fails base_branch
// as pr_repo_not_in_workspace, the other items waiting; the worker is not
// told and the head cannot be approved.
func TestCrossRepoPRNotInWorkspaceFails(t *testing.T) {
	f := newApproveFixture(t)
	exec, _ := f.addCrossRepoApproval("2", verifyHead1)
	exec.PR.URL = "https://github.com/acme/other/pull/2"
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.PR.URL = exec.PR.URL })
	f.heads.set(exec.PR.URL, verifyHead1)

	f.tick()
	record := f.record(exec.SessionID, verifyHead1)
	item, details := baseDetailsOf(t, record)
	if item.Outcome != domain.LadoVerifyFail || item.Code != domain.LadoCodePRRepoNotInWorkspace {
		t.Fatalf("base_branch item = %s %q, want fail %s", item.Outcome, item.Code, domain.LadoCodePRRepoNotInWorkspace)
	}
	if details.PRRepo != "github:acme/other" || details.Branch != "main" {
		t.Errorf("details = %+v, want prRepo github:acme/other and branch main", details)
	}
	for _, other := range record.Items {
		if other.Kind != domain.LadoVerifyBaseBranch && (other.Outcome != domain.LadoVerifyWait || other.Code != domain.LadoValidatingVerifying) {
			t.Errorf("%s item = %s %q, want verifying", other.Kind, other.Outcome, other.Code)
		}
	}
	if outcome, reason := record.Overall(); outcome != domain.LadoVerifyFail || reason != domain.LadoCodePRRepoNotInWorkspace {
		t.Errorf("Overall = %s %q, want fail %s", outcome, reason, domain.LadoCodePRRepoNotInWorkspace)
	}
	if got := f.sender.to(exec.SessionID); len(got) != 0 {
		t.Errorf("worker told %q, want nothing", got)
	}
	if got := f.git.askedDirs(); len(got) != 0 {
		t.Errorf("git read %v, want nothing", got)
	}

	preview := f.preview(exec)
	_, err := f.approve(exec, preview.SHA256, preview.Confirm, "nonce-other")
	requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_NOT_READY")

	// An approval given before the workspace lost the repository does not
	// let the PR merge.
	f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-old", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL,
		HeadSHA: verifyHead1, TargetBranch: "main", StatusState: domain.LadoIntegrationStatusPosted})
	pr := domain.PullRequest{URL: exec.PR.URL, SessionID: exec.SessionID, Number: 2, HeadSHA: verifyHead1, TargetBranch: "main"}
	requireAPIError(t, f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1), apierr.KindInternal, "LADO_MERGE_GATE_FAILED")
}

// (c) A single repository and a workspace PR in its issue's repository read
// the issue repository's rules, compare with no default branch and never
// resolve one.
func TestSameRepoKeepsTheIssueRepository(t *testing.T) {
	branches := &verifyBranches{byDir: map[string]string{}}
	f := newVerifyFixture(t, func(d *Deps) { d.Branches = branches })
	single := f.addExecution("1", verifyHead1)
	ws, child := f.addWorkspaceExecution("2", verifyHead1)

	f.tick()
	if got := f.host.branchReads(); len(got) == 0 || slices.ContainsFunc(got, func(r string) bool { return r != "acme/demo@main" }) {
		t.Errorf("rules read = %v, want acme/demo@main only", got)
	}
	if got := branches.configuredSeen(); len(got) != 0 {
		t.Errorf("default branch resolved %d times, want never", len(got))
	}
	for _, session := range []domain.SessionID{single.SessionID, ws.SessionID} {
		item, details := baseDetailsOf(t, f.record(session, verifyHead1))
		if item.Code != domain.LadoCodeNoLocalBranch || details.DefaultBranch != "" || details.PRRepo != "" {
			t.Errorf("%s base_branch = %s %q %+v, want no_local_branch as before", session, item.Outcome, item.Code, details)
		}
	}
	if !f.git.asked(child) {
		t.Errorf("git read %v, want the issue's child repository %s", f.git.askedDirs(), child)
	}
}

// (d) The PR's repository's default branch is develop while the PR merges
// into main: base_branch needs confirming as base_not_default, and the
// approval acknowledges it. The base is fetched from the remote resolved
// for web, and the status goes to web.
func TestCrossRepoBaseNotDefaultNeedsConfirming(t *testing.T) {
	branches := &verifyBranches{byDir: map[string]string{}, remote: "upstream"}
	f := newApproveFixture(t, func(d *Deps) { d.Branches = branches })
	exec, web := f.addCrossRepoApproval("2", verifyHead1)
	branches.byDir[web] = "develop"

	preview := f.preview(exec)
	item, details := baseDetailsOf(t, preview.Verification)
	if item.Outcome != domain.LadoVerifyConfirm || item.Code != domain.LadoCodeBaseNotDefault {
		t.Fatalf("base_branch item = %s %q, want confirm %s", item.Outcome, item.Code, domain.LadoCodeBaseNotDefault)
	}
	if details.DefaultBranch != "develop" || details.DispatchBranch != "main" || details.Branch != "main" || details.Remote != "upstream" {
		t.Errorf("details = %+v, want main on upstream, default develop, dispatched from main", details)
	}
	if ops := f.git.opsOf(web); !slices.Contains(ops, "fetch upstream main") {
		t.Errorf("web ops = %v, want main fetched from upstream", ops)
	}
	want := []string{domain.LadoCodeBaseNotDefault, domain.LadoCodeNoTests}
	if !slices.Equal(preview.Confirm, want) {
		t.Fatalf("confirm codes = %v, want %v", preview.Confirm, want)
	}
	_, err := f.approve(exec, preview.SHA256, []string{domain.LadoCodeNoTests}, "nonce-default-1")
	requireAPIError(t, err, apierr.KindInvalid, "LADO_INTEGRATION_ACK_MISMATCH")
	result, err := f.approve(exec, preview.SHA256, want, "nonce-default-2")
	if err != nil {
		t.Fatalf("approval acknowledging base_not_default = %v", err)
	}
	if result.Approval.StatusState != domain.LadoIntegrationStatusPosted {
		t.Errorf("status = %s, want posted", result.Approval.StatusState)
	}
	if posts := f.host.postsMade(); len(posts) != 1 || posts[0].repo.Native != "acme/web" || posts[0].sha != verifyHead1 {
		t.Errorf("statuses posted = %+v, want one on acme/web", posts)
	}

	// The default branch alone does not count: a PR into it passes.
	branches.byDir[web] = "Main"
	if item, _ := baseDetailsOf(t, f.preview(exec).Verification); item.Code == domain.LadoCodeBaseNotDefault {
		t.Errorf("base_branch item = %s %q with main the default, want no base_not_default", item.Outcome, item.Code)
	}
}

// (e) The loop resolves the PR repository's default branch at most every
// baseFetchEvery, an approval's dry run every time.
func TestCrossRepoResolvesTheDefaultBranchOncePerInterval(t *testing.T) {
	branches := &verifyBranches{byDir: map[string]string{}}
	f := newApproveFixture(t, func(d *Deps) { d.Branches = branches })
	exec, web := f.addCrossRepoApproval("2", verifyHead1)
	branches.byDir[web] = "main"

	f.tick()
	f.clock.advance(time.Minute)
	f.tick()
	if got := branches.configuredSeen(); !slices.Equal(got, []string{"main"}) {
		t.Fatalf("resolutions after two passes = %q, want one with the workspace's main", got)
	}
	f.preview(exec)
	if got := len(branches.configuredSeen()); got != 2 {
		t.Fatalf("resolutions after the dry run = %d, want 2", got)
	}
	f.tick()
	if got := len(branches.configuredSeen()); got != 2 {
		t.Fatalf("resolutions after the next pass = %d, want still 2", got)
	}
	f.clock.advance(baseFetchEvery)
	f.tick()
	if got := len(branches.configuredSeen()); got != 3 {
		t.Fatalf("resolutions once the last is baseFetchEvery old = %d, want 3", got)
	}
}

// (f) A PR URL LADO cannot read is verified in the issue's repository, as
// before, and the warning is logged once.
func TestUnreadablePRURLWarnsOnce(t *testing.T) {
	logs := &lockedBuffer{}
	f := newVerifyFixture(t, func(d *Deps) { d.Log = slog.New(slog.NewTextHandler(logs, nil)) })
	exec, api := f.addWorkspaceExecution("2", verifyHead1)
	exec.PR.URL = "https://example.com/acme/demo/pull/2"
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.PR.URL = exec.PR.URL })

	f.tick()
	f.clock.advance(time.Minute)
	f.tick()
	if paths := verifyItemOf(t, f.record(exec.SessionID, verifyHead1), domain.LadoVerifyPaths); paths.Outcome != domain.LadoVerifyPass {
		t.Errorf("paths item = %s %s %s, want pass in api", paths.Outcome, paths.Code, paths.Details)
	}
	if !f.git.asked(api) {
		t.Errorf("git read %v, want the issue's repository %s", f.git.askedDirs(), api)
	}
	if n := strings.Count(logs.String(), "could not read the repository of a pull request's URL"); n != 1 {
		t.Errorf("warnings = %d, want 1:\n%s", n, logs.String())
	}
}

// (g) The target key names the PR's repository, without case and without
// the remote.
func TestTargetKeyNamesThePRRepository(t *testing.T) {
	task := domain.LadoTask{ProjectID: "ws", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}}
	cross := domain.LadoExecution{BaseBranch: "main", BaseRemote: "origin", PR: domain.LadoExecutionPR{URL: "https://github.com/acme/web/pull/2", TargetBranch: "main"}}
	key := targetKey(task, cross)
	if !strings.Contains(key, "acme/web") || strings.Contains(key, "acme/demo") {
		t.Errorf("key = %q, want the PR's repository acme/web", key)
	}
	// Another execution of web, known by its task's repository only, with
	// other case and another remote, names the same target.
	webTask := domain.LadoTask{ProjectID: "ws", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "Acme/Web"}}
	other := domain.LadoExecution{BaseBranch: "MAIN", BaseRemote: "upstream"}
	if got := targetKey(webTask, other); got != key {
		t.Errorf("key = %q, want %q", got, key)
	}
	// The issue's repository's main is another target.
	if got := targetKey(task, domain.LadoExecution{BaseBranch: "main"}); got == key {
		t.Errorf("key of acme/demo = %q, want another than web's", got)
	}
	gitlab := domain.LadoTask{ProjectID: "ws", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "Team/Front", Host: "GitLab.Example.com"}}
	mr := domain.LadoExecution{PR: domain.LadoExecutionPR{URL: "https://gitlab.example.com/team/front/-/merge_requests/9", TargetBranch: "master"}}
	if a, b := targetKey(gitlab, mr), targetKey(gitlab, domain.LadoExecution{BaseBranch: "Master"}); a != b {
		t.Errorf("GitLab keys %q and %q differ", a, b)
	}
}

// lockedBuffer is a log sink the loop and its commands write to at once.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

var _ io.Writer = (*lockedBuffer)(nil)

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
