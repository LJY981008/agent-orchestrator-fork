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
