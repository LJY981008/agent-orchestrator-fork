package ladoverify

// The loop's answers to the review of build step 8 (#15): each test plays
// the defect the review found and checks the fix.

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func passedRun(output string) CommandResult {
	return CommandResult{Status: domain.LadoCheckRunPassed, ExitCode: verifyExit(0), Output: []byte(output)}
}

// V1: a check command whose text changed runs again on the head; the pass of
// its old text no longer counts, and the record's hash changes at once.
func TestTickRunsAChangedCommandAgain(t *testing.T) {
	f := newVerifyFixture(t)
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./pkg/a"}}, 30)
	exec := f.addExecution("1", verifyHead1)

	f.tick()
	f.runner.waitCalls(t, 1)
	f.finish(passedRun("ok"))
	f.tick()
	before := f.record(exec.SessionID, verifyHead1)
	if item := verifyItemOf(t, before, domain.LadoVerifyCommands); item.Outcome != domain.LadoVerifyPass {
		t.Fatalf("commands item = %s %s, want pass", item.Outcome, item.Code)
	}

	// The developer widens the command through the desktop confirmation.
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}}, 30)
	f.tick()
	if calls := f.runner.waitCalls(t, 2); calls[1].Command != "go test ./..." {
		t.Fatalf("second run = %q, want the changed command", calls[1].Command)
	}
	after := f.record(exec.SessionID, verifyHead1)
	if item := verifyItemOf(t, after, domain.LadoVerifyCommands); item.Outcome != domain.LadoVerifyWait || item.Code != domain.LadoCodeCommandsRunning {
		t.Fatalf("commands item after the change = %s %s, want wait commands_running", item.Outcome, item.Code)
	}
	if verifySHA(t, after) == verifySHA(t, before) {
		t.Fatal("the record's hash did not change with the command")
	}
	f.finish(passedRun("ok"))
}

// V2: an edit the worker did not commit after a check command ended keeps
// the next command from running on it; the command runs once the worktree is
// as the last run left it.
func TestTickWaitsForAWorktreeChangedAfterARun(t *testing.T) {
	f := newVerifyFixture(t)
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}, {Name: "lint", Command: "golangci-lint run"}}, 30)
	exec := f.addExecution("1", verifyHead1)
	worktree := f.reads.worktree(exec.SessionID)

	f.tick()
	f.runner.waitCalls(t, 1)
	f.finish(passedRun("ok unit"))
	f.git.setStatus(worktree, " M src/login.go\n")
	f.tick()
	if n := f.store.runCount(); n != 1 {
		t.Fatalf("runs = %d, want lint not started on an edit", n)
	}
	record := f.record(exec.SessionID, verifyHead1)
	if item := verifyItemOf(t, record, domain.LadoVerifyCommands); item.Outcome != domain.LadoVerifyWait || item.Code != domain.LadoCodeWorktreeChanged {
		t.Fatalf("commands item = %s %s, want wait worktree_changed", item.Outcome, item.Code)
	}
	if _, reason := record.Overall(); reason != domain.LadoCodeWorktreeChanged {
		t.Fatalf("record reason = %q, want worktree_changed", reason)
	}

	// The worker undoes its edit: lint runs.
	f.git.setStatus(worktree, "")
	f.tick()
	if calls := f.runner.waitCalls(t, 2); calls[1].Command != "golangci-lint run" {
		t.Fatalf("second run = %q, want lint", calls[1].Command)
	}
	f.finish(passedRun("ok lint"))
}

// V3: an execution the loop cannot verify rests for five minutes, so that
// such executions never fill a pass; a pass lists as many more as rest.
func TestTickRestsAnExecutionItCannotVerify(t *testing.T) {
	f := newVerifyFixture(t)
	lost := f.addExecution("1", verifyHead1)
	f.reads.deleteTask(lost.TaskID)
	fine := f.addExecution("2", verifyHead3)

	if err := f.svc.Tick(f.ctx); err == nil || !strings.Contains(err.Error(), string(lost.SessionID)) {
		t.Fatalf("first pass = %v, want the lost task's error", err)
	}
	if limit := f.store.listLimit(); limit != verifyBatch {
		t.Fatalf("first pass listed up to %d, want %d", limit, verifyBatch)
	}
	f.clock.advance(time.Minute)
	if err := f.svc.Tick(f.ctx); err != nil {
		t.Fatalf("second pass = %v, want the resting execution left alone", err)
	}
	if limit := f.store.listLimit(); limit != verifyBatch+1 {
		t.Fatalf("second pass listed up to %d, want %d", limit, verifyBatch+1)
	}
	if record := f.record(fine.SessionID, verifyHead3); !record.ComputedAt.Equal(f.clock.Now()) {
		t.Fatalf("the other execution's record is from %s, want this pass", record.ComputedAt)
	}
	f.clock.advance(5 * time.Minute)
	if err := f.svc.Tick(f.ctx); err == nil || !strings.Contains(err.Error(), string(lost.SessionID)) {
		t.Fatalf("pass after the rest = %v, want the lost task tried again", err)
	}
}

// V4 and V6: a merge without an integration approval is audited for an
// execution dispatched under integration approval only, and a PR settles per
// state, so a closed PR that opens again and merges is audited when it
// merges.
func TestTickSettlesPerPRStateAndGate(t *testing.T) {
	f := newVerifyFixture(t)
	early := f.addExecution("1", verifyHead1)
	f.store.update(early.SessionID, func(e *domain.LadoExecution) { e.IntegrationGated, e.PR.Merged = false, true })
	reopened := f.addExecution("2", verifyHead3)
	f.store.update(reopened.SessionID, func(e *domain.LadoExecution) { e.PR.Closed = true })

	f.tick()
	if audits := f.store.auditEvents(); len(audits) != 0 {
		t.Fatalf("audit events = %+v, want none: one ran before the gate, the other closed", audits)
	}
	for _, exec := range []domain.LadoExecution{early, reopened} {
		if _, ok := f.store.settledAt(exec.SessionID); !ok {
			t.Fatalf("%s is not settled", exec.SessionID)
		}
	}

	// The closed PR opens again and is verified again.
	f.store.update(reopened.SessionID, func(e *domain.LadoExecution) { e.PR.Closed = false })
	f.tick()
	f.record(reopened.SessionID, verifyHead3)
	// It merges without an approval: that is audited once.
	f.store.update(reopened.SessionID, func(e *domain.LadoExecution) { e.PR.Merged = true })
	f.tick()
	f.tick()
	audits := f.store.auditEvents()
	if len(audits) != 1 || audits[0].Action != "execution.merged_without_approval" || audits[0].SubjectID != string(reopened.SessionID) {
		t.Fatalf("audit events = %+v, want the reopened PR's merge once", audits)
	}
}

// V5: a head made on the host, as by GitHub's Update branch, is not in the
// local repository until LADO fetches the PR's head ref. The loop fetches it
// at most every five minutes; while it cannot, the items git reads wait as
// head_unavailable.
func TestTickFetchesAHeadMadeOnTheHost(t *testing.T) {
	f := newVerifyFixture(t)
	exec := f.addExecution("7", verifyHead1)
	dir := f.reads.project("p1").Path
	f.git.lack(verifyHead1, "refs/pull/7/head")
	f.git.setFetchErr(errors.New("fatal: unable to access the host"))
	fetches := func() int {
		return len(slices.DeleteFunc(f.git.opsOf(dir), func(op string) bool { return op != "fetch origin refs/pull/7/head" }))
	}

	f.tick()
	record := f.record(exec.SessionID, verifyHead1)
	for _, kind := range []domain.LadoVerifyKind{domain.LadoVerifyCommitMessages, domain.LadoVerifyPaths, domain.LadoVerifyEvidence} {
		if item := verifyItemOf(t, record, kind); item.Outcome != domain.LadoVerifyWait || item.Code != domain.LadoCodeHeadUnavailable {
			t.Errorf("%s = %s %s, want wait head_unavailable", kind, item.Outcome, item.Code)
		}
	}
	if _, reason := record.Overall(); reason != domain.LadoCodeHeadUnavailable {
		t.Errorf("record reason = %q, want head_unavailable", reason)
	}
	if n := fetches(); n != 1 {
		t.Fatalf("fetches of the PR head = %d, want 1", n)
	}

	f.clock.advance(time.Minute)
	f.tick()
	if n := fetches(); n != 1 {
		t.Fatalf("fetches within five minutes = %d, want still 1", n)
	}

	f.git.setFetchErr(nil)
	f.clock.advance(5 * time.Minute)
	f.tick()
	if n := fetches(); n != 2 {
		t.Fatalf("fetches after five minutes = %d, want 2", n)
	}
	record = f.record(exec.SessionID, verifyHead1)
	for _, kind := range []domain.LadoVerifyKind{domain.LadoVerifyCommitMessages, domain.LadoVerifyPaths} {
		if item := verifyItemOf(t, record, kind); item.Outcome != domain.LadoVerifyPass {
			t.Errorf("%s once the head is fetched = %s %s, want pass", kind, item.Outcome, item.Code)
		}
	}
	// A head the repository holds is not fetched again.
	f.clock.advance(10 * time.Minute)
	f.tick()
	if n := fetches(); n != 2 {
		t.Fatalf("fetches of a head the repository holds = %d, want still 2", n)
	}
}

// V5: the dry run and the approval fetch a head the repository lacks every
// time, so the developer never waits for the loop's next fetch; while the
// fetch fails, the approval is not ready.
func TestIntegrationApprovalFetchesTheHead(t *testing.T) {
	f := newApproveFixture(t)
	exec := f.add("3", verifyHead1)
	dir := f.reads.project("p1").Path
	f.git.lack(verifyHead1, "refs/pull/3/head")
	f.git.setFetchErr(errors.New("fatal: could not read from the remote"))

	preview := f.preview(exec)
	if outcome, reason := preview.Verification.Overall(); outcome != domain.LadoVerifyWait || reason != domain.LadoCodeHeadUnavailable {
		t.Fatalf("dry run = %s %q, want wait head_unavailable", outcome, reason)
	}
	_, err := f.approve(exec, preview.SHA256, preview.Confirm, "nonce-head")
	var refused *apierr.Error
	if !errors.As(err, &refused) || refused.Code != "LADO_INTEGRATION_NOT_READY" {
		t.Fatalf("approval = %v, want LADO_INTEGRATION_NOT_READY", err)
	}
	if reasons, _ := refused.Details["reasons"].([]string); !slices.Contains(reasons, domain.LadoCodeHeadUnavailable) {
		t.Fatalf("reasons = %v, want head_unavailable", refused.Details["reasons"])
	}

	f.git.setFetchErr(nil)
	preview = f.preview(exec)
	if outcome, _ := preview.Verification.Overall(); outcome == domain.LadoVerifyWait || outcome == domain.LadoVerifyFail {
		t.Fatalf("dry run once the head is fetched = %s, want it ready", outcome)
	}
	fetched := slices.DeleteFunc(f.git.opsOf(dir), func(op string) bool { return op != "fetch origin refs/pull/3/head" })
	if len(fetched) != 3 {
		t.Fatalf("fetches of the PR head = %d, want one per request", len(fetched))
	}
}

// V5: a worker whose worktree is behind a head made on the host is asked
// once to pull its branch; the check commands wait for its worktree.
func TestTickAsksTheWorkerToPullAHeadMadeOnTheHost(t *testing.T) {
	f := newVerifyFixture(t)
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}}, 30)
	exec := f.addExecution("1", verifyHead1)
	// Update branch made verifyHead2 on top of the worker's verifyHead1.
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.PR.HeadSHA = verifyHead2 })
	f.git.setAncestor(verifyHead1, verifyHead2)

	f.tick()
	f.tick()
	messages := f.sender.to(exec.SessionID)
	if len(messages) != 1 || !strings.Contains(messages[0], "Pull your branch") || !strings.Contains(messages[0], shortCommit(verifyHead2)) ||
		!strings.Contains(messages[0], shortCommit(verifyHead1)) {
		t.Fatalf("messages = %q, want one asking to pull %s", messages, shortCommit(verifyHead2))
	}
	record := f.record(exec.SessionID, verifyHead2)
	if item := verifyItemOf(t, record, domain.LadoVerifyCommands); item.Code != domain.LadoCodeWorktreeNotAtHead {
		t.Fatalf("commands item = %s %s, want worktree_not_at_head", item.Outcome, item.Code)
	}
	if !slices.Contains(record.Notified, domain.LadoNotifiedWorktreeBehind) {
		t.Fatalf("notified = %v, want the pull request noted", record.Notified)
	}

	// A worktree that went its own way is not behind: no message.
	other := f.addExecution("2", verifyHead3)
	f.store.update(other.SessionID, func(e *domain.LadoExecution) { e.PR.HeadSHA = verifyHead4 })
	f.tick()
	if got := f.sender.to(other.SessionID); len(got) != 0 {
		t.Fatalf("messages to a worktree that is not behind = %q, want none", got)
	}
}

// S8: after its attempts ran out, the approved head's lado/integration
// status can be posted again, attempts started over, without presence.
func TestRepostIntegrationStatusStartsOver(t *testing.T) {
	f := newVerifyFixture(t)
	exec := f.addExecution("1", verifyHead1)
	if _, err := f.svc.RepostIntegrationStatus(f.ctx, exec.SessionID); !isAPIError(err, "LADO_INTEGRATION_NOT_APPROVED") {
		t.Fatalf("repost without an approval = %v, want LADO_INTEGRATION_NOT_APPROVED", err)
	}
	f.store.addApproval(domain.LadoIntegrationApproval{
		ID: "approval-1", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1, TargetBranch: "main",
		StatusState: domain.LadoIntegrationStatusFailed, StatusAttempts: domain.LadoIntegrationStatusMaxAttempts, StatusError: "the host answered 403",
	})
	// The loop has given up on it.
	f.tick()
	if posts := f.host.postsMade(); len(posts) != 0 {
		t.Fatalf("the loop posted %d statuses after the attempts ran out, want none", len(posts))
	}

	approval, err := f.svc.RepostIntegrationStatus(f.ctx, exec.SessionID)
	if err != nil {
		t.Fatalf("repost = %v", err)
	}
	if approval.StatusState != domain.LadoIntegrationStatusPosted || approval.StatusAttempts != 1 || approval.StatusError != "" {
		t.Fatalf("approval after the repost = %+v, want posted after one attempt", approval)
	}
	if stored := f.store.approval("approval-1"); stored.StatusState != domain.LadoIntegrationStatusPosted || stored.StatusAttempts != 1 {
		t.Fatalf("stored approval = %+v, want posted after one attempt", stored)
	}
	if posts := f.host.postsMade(); len(posts) != 1 || posts[0].sha != verifyHead1 || posts[0].status.Context != domain.LadoIntegrationStatusContext {
		t.Fatalf("posts = %+v, want the lado/integration status on the head", posts)
	}

	// A post that fails again is the approval's status, and the loop takes
	// it up from there.
	f.store.addApproval(domain.LadoIntegrationApproval{
		ID: "approval-3", ExecutionSessionID: "lado-3", PRURL: "https://github.com/acme/demo/pull/3", HeadSHA: verifyHead3, TargetBranch: "main",
		StatusState: domain.LadoIntegrationStatusFailed, StatusAttempts: domain.LadoIntegrationStatusMaxAttempts,
	})
	f.addExecution("3", verifyHead3)
	f.host.setPostErr(errors.New("the host answered 403: Resource not accessible by integration"))
	approval, err = f.svc.RepostIntegrationStatus(f.ctx, "lado-3")
	if err != nil || approval.StatusState != domain.LadoIntegrationStatusFailed || approval.StatusAttempts != 1 {
		t.Fatalf("repost that fails = %+v, %v; want failed after one attempt", approval, err)
	}
}

// S6: an approval holds for its PR: the same head opened again as a new PR
// is not approved, and can be approved for that PR.
func TestApprovalHoldsForItsPR(t *testing.T) {
	f := newApproveFixture(t)
	exec := f.add("1", verifyHead1)
	preview := f.preview(exec)
	if _, err := f.approve(exec, preview.SHA256, preview.Confirm, "nonce-first"); err != nil {
		t.Fatalf("first approval = %v", err)
	}
	// The PR closed and the worker opened another one at the same head.
	reopened := exec
	reopened.PR.URL, reopened.PR.Number = "https://github.com/acme/demo/pull/11", 11
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.PR = reopened.PR })
	f.heads.set(reopened.PR.URL, verifyHead1)
	pr := domain.PullRequest{URL: reopened.PR.URL, SessionID: exec.SessionID, Number: 11, HeadSHA: verifyHead1, TargetBranch: "main"}
	if err := f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1); !isAPIError(err, "LADO_INTEGRATION_APPROVAL_REQUIRED") {
		t.Fatalf("merge of the new PR on the old PR's approval = %v, want LADO_INTEGRATION_APPROVAL_REQUIRED", err)
	}
	preview = f.preview(reopened)
	if _, err := f.approve(reopened, preview.SHA256, preview.Confirm, "nonce-second"); err != nil {
		t.Fatalf("approval of the new PR = %v, want it recorded", err)
	}
	if err := f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1); err != nil {
		t.Fatalf("merge of the new PR once approved = %v", err)
	}
	if _, err := f.approve(reopened, preview.SHA256, preview.Confirm, "nonce-third"); !isAPIError(err, "LADO_INTEGRATION_ALREADY_APPROVED") {
		t.Fatalf("a second approval of the new PR = %v, want LADO_INTEGRATION_ALREADY_APPROVED", err)
	}
}

// isAPIError reports whether err is an *apierr.Error with code.
func isAPIError(err error, code string) bool {
	var apiErr *apierr.Error
	return errors.As(err, &apiErr) && apiErr.Code == code
}

// Review of #31: the loop never fetches, so the remote-tracking target can be
// older than the host's. After the host's Update branch merged the target's
// newer commits into the head, the merge-base with the base tip AO observed
// keeps those commits, another developer's, out of the worker's.
func TestMergeBaseTakesTheNewerBase(t *testing.T) {
	r := newTestRepo(t, t0.Add(-2*time.Hour))
	wt := r.worktree(t0, "ao/feature")
	r.write(wt, "src/login.go", "package login\n")
	r.commit(t0.Add(time.Minute), wt, "feat: 로그인 화면을 더한다")

	// Another developer's change reaches main; the developer's clone does
	// not fetch it, and the host merges main into the worker's branch.
	r.write(r.upstream, ".github/workflows/ci.yml", "name: ci\n")
	newBase := r.commit(t0.Add(2*time.Minute), r.upstream, "Add CI")
	r.publish(t0.Add(2 * time.Minute))
	r.git(t0.Add(3*time.Minute), wt, "fetch", "-q", r.origin, "main")
	r.git(t0.Add(3*time.Minute), wt, "merge", "-q", "--no-edit", "--no-verify", "FETCH_HEAD")
	head := r.git(t0, wt, "rev-parse", "HEAD")
	stale := r.git(t0, r.dir, "rev-parse", "refs/remotes/origin/main")

	f := newVerifyFixture(t)
	exec := f.addExecution("1", head)
	f.svc.deps.Git = ExecGit{}
	in := headInputs{exec: exec, head: head, dir: r.dir, target: "main", baseRemote: "origin"}

	// Without a base tip from the host, the stale remote-tracking branch
	// counts, and with it another developer's commit.
	if base, err := f.svc.mergeBase(f.ctx, in); err != nil || base != stale {
		t.Fatalf("mergeBase without an observed base = %s, %v; want the stale %s", base, err, stale)
	}
	f.store.mu.Lock()
	f.store.baseSHAs = map[string]string{exec.PR.URL: newBase}
	f.store.mu.Unlock()
	base, err := f.svc.mergeBase(f.ctx, in)
	if err != nil || base != newBase {
		t.Fatalf("mergeBase = %s, %v; want the host's base %s", base, err, newBase)
	}
	commits, err := ExecGit{}.Commits(f.ctx, r.dir, base, head)
	if err != nil || len(commits) != 1 || !strings.Contains(commits[0].Message, "로그인") {
		t.Errorf("commits from the base = %+v, %v; want only the worker's", commits, err)
	}
	changed, err := ExecGit{}.ChangedPaths(f.ctx, r.dir, base, head)
	if err != nil || !slices.Equal(changed, []string{"src/login.go"}) {
		t.Errorf("changed paths from the base = %v, %v; want only the worker's", changed, err)
	}

	// A base tip the repository lacks leaves the remote-tracking branch.
	f.store.mu.Lock()
	f.store.baseSHAs = map[string]string{exec.PR.URL: strings.Repeat("e", 40)}
	f.store.mu.Unlock()
	if base, err := f.svc.mergeBase(f.ctx, in); err != nil || base != stale {
		t.Errorf("mergeBase with an unknown observed base = %s, %v; want %s", base, err, stale)
	}
}
