package ladoverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// The daemon hands the loop LADO's store for these reads as it is.
var (
	_ Store     = (*store.Store)(nil)
	_ Tasks     = (*store.Store)(nil)
	_ Snapshots = (*store.Store)(nil)
	_ Projects  = (*store.Store)(nil)
	_ Sessions  = (*store.Store)(nil)
)

var (
	verifyHead1 = strings.Repeat("1a2b3c4d", 5)
	verifyHead2 = strings.Repeat("5e6f7a8b", 5)
	verifyHead3 = strings.Repeat("9c0d1e2f", 5)
	verifyHead4 = strings.Repeat("3a4b5c6d", 5)
	verifyBase  = strings.Repeat("0f", 20)
	// verifyKoreanCommit is the commit a head has unless a test gives it
	// others.
	verifyKoreanCommit = Commit{SHA: strings.Repeat("ab", 20), Message: "fix: 로그인 오류를 고친다"}
)

func TestTickBuildsARecordForTheHead(t *testing.T) {
	f := newVerifyFixture(t)
	exec := f.addExecution("1", verifyHead1)
	ws, child := f.addWorkspaceExecution("2", verifyHead1)

	f.tick()
	record := f.record(exec.SessionID, verifyHead1)
	want := []struct {
		kind    domain.LadoVerifyKind
		outcome domain.LadoVerifyOutcome
		code    string
	}{
		{domain.LadoVerifyRequiredChecks, domain.LadoVerifyPass, domain.LadoCodeNoneReported},
		{domain.LadoVerifyCommands, domain.LadoVerifyConfirm, domain.LadoCodeNoTests},
		{domain.LadoVerifyCommitMessages, domain.LadoVerifyPass, ""},
		{domain.LadoVerifyPaths, domain.LadoVerifyPass, ""},
		{domain.LadoVerifyBaseBranch, domain.LadoVerifyPass, domain.LadoCodeNoLocalBranch},
		{domain.LadoVerifyProtection, domain.LadoVerifyPass, ""},
		{domain.LadoVerifyReview, domain.LadoVerifyInfo, domain.LadoCodeNotRequired},
		{domain.LadoVerifyEvidence, domain.LadoVerifyInfo, ""},
	}
	if len(record.Items) != len(want) {
		t.Fatalf("record has %d items, want %d: %+v", len(record.Items), len(want), record.Items)
	}
	for i, w := range want {
		if got := record.Items[i]; got.Kind != w.kind || got.Outcome != w.outcome || got.Code != w.code {
			t.Errorf("item %d = %s %s %q, want %s %s %q", i, got.Kind, got.Outcome, got.Code, w.kind, w.outcome, w.code)
		}
	}
	if outcome, reason := record.Overall(); outcome != domain.LadoVerifyConfirm || reason != "" {
		t.Errorf("Overall = %s %q, want confirm", outcome, reason)
	}
	if record.PRURL != exec.PR.URL || !record.ComputedAt.Equal(f.clock.Now()) || len(record.Notified) != 0 {
		t.Errorf("record = %s at %s notified %v, want %s at %s, none notified", record.PRURL, record.ComputedAt, record.Notified, exec.PR.URL, f.clock.Now())
	}
	// The workspace project's changed paths start with the child's folder,
	// as its plan's do, and git reads the child repository.
	if paths := verifyItemOf(t, f.record(ws.SessionID, verifyHead1), domain.LadoVerifyPaths); paths.Outcome != domain.LadoVerifyPass {
		t.Errorf("workspace paths item = %s %s %s, want pass", paths.Outcome, paths.Code, paths.Details)
	}
	if !f.git.asked(child) {
		t.Errorf("git read %v, want the child repository %s", f.git.askedDirs(), child)
	}

	first := verifySHA(t, record)
	f.clock.advance(time.Minute)
	f.tick()
	again := f.record(exec.SessionID, verifyHead1)
	if sum := verifySHA(t, again); sum != first {
		t.Errorf("second pass hash = %s, want %s", sum, first)
	}
	if got := f.store.changedOf(exec.SessionID, verifyHead1); !slices.Equal(got, []bool{true, false}) {
		t.Errorf("upserts changed = %v, want [true false]", got)
	}
	if !again.ComputedAt.Equal(f.clock.Now()) {
		t.Errorf("second pass computed at %s, want %s", again.ComputedAt, f.clock.Now())
	}
	if writes := f.store.linkWritesOf(exec.SessionID, verifyHead1); writes != 1 {
		t.Errorf("evidence links written %d times, want once while the evidence stays the same", writes)
	}

	// A new head gets its own record; the old head's stays.
	f.push(exec, verifyHead2)
	f.tick()
	if got := f.store.changedOf(exec.SessionID, verifyHead2); !slices.Equal(got, []bool{true}) {
		t.Errorf("new head upserts changed = %v, want [true]", got)
	}
	if fresh := f.record(exec.SessionID, verifyHead2); fresh.HeadSHA != verifyHead2 || verifySHA(t, fresh) == first {
		t.Errorf("new head record = %s with hash %s, want its own", fresh.HeadSHA, verifySHA(t, fresh))
	}
	if old := f.record(exec.SessionID, verifyHead1); verifySHA(t, old) != first {
		t.Error("the old head's record changed")
	}
}

func TestTickRunsCommandsOncePerHead(t *testing.T) {
	f := newVerifyFixture(t)
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}, {Name: "lint", Command: "golangci-lint run"}}, 7)
	f.reads.setProjectEnv("p1", map[string]string{"PROJECT_VAR": "1", "AO_PROJECT": "x"})
	one := f.addExecution("1", verifyHead1)
	two := f.addExecution("2", verifyHead3)

	f.tick()
	spec := f.runner.waitCalls(t, 1)[0]
	if spec.Dir != f.reads.worktree(one.SessionID) || spec.Command != "go test ./..." || spec.Timeout != 7*time.Minute {
		t.Errorf("spec = %+v, want unit in %s for 7m", spec, f.reads.worktree(one.SessionID))
	}
	for _, entry := range []string{"PATH=/usr/bin", "PROJECT_VAR=1"} {
		if !slices.Contains(spec.Env, entry) {
			t.Errorf("env %v lacks %s", spec.Env, entry)
		}
	}
	for _, entry := range spec.Env {
		if strings.HasPrefix(entry, "AO_") || strings.HasPrefix(entry, "GITHUB_TOKEN=") {
			t.Errorf("env holds %s", entry)
		}
	}
	commands := verifyItemOf(t, f.record(one.SessionID, verifyHead1), domain.LadoVerifyCommands)
	if commands.Outcome != domain.LadoVerifyWait || commands.Code != domain.LadoCodeCommandsRunning {
		t.Errorf("commands item while unit runs = %s %s, want wait commands_running", commands.Outcome, commands.Code)
	}

	// One slot for the daemon: nothing else starts while unit runs.
	f.tick()
	if n := f.store.runCount(); n != 1 {
		t.Fatalf("runs started while one runs = %d, want 1", n)
	}

	passed := func(output string) CommandResult {
		return CommandResult{Status: domain.LadoCheckRunPassed, ExitCode: verifyExit(0), Output: []byte(output)}
	}
	order := []struct {
		ended   CommandResult
		session domain.SessionID
		command string
	}{
		{passed("ok unit"), one.SessionID, "golangci-lint run"},
		{passed("ok lint"), two.SessionID, "go test ./..."},
		{CommandResult{Status: domain.LadoCheckRunFailed, ExitCode: verifyExit(1), Output: []byte("--- FAIL: TestLogin")}, two.SessionID, "golangci-lint run"},
	}
	for i, next := range order {
		f.finish(next.ended)
		if i == 0 {
			// unit left build output in the worktree: lint still runs.
			f.git.setDirty(f.reads.worktree(one.SessionID), true)
		}
		f.tick()
		calls := f.runner.waitCalls(t, i+2)
		if got := calls[i+1]; got.Dir != f.reads.worktree(next.session) || got.Command != next.command {
			t.Errorf("run %d = %s in %s, want %s in %s", i+2, got.Command, got.Dir, next.command, f.reads.worktree(next.session))
		}
	}
	// The worker hears about the failed unit tests once lint ended too.
	if got := f.sender.to(two.SessionID); len(got) != 0 {
		t.Errorf("fix requests while lint runs = %q, want none yet", got)
	}
	f.finish(passed("ok lint"))

	// Every command ran once on each head: more passes start nothing.
	f.tick()
	f.tick()
	if n := f.store.runCount(); n != 4 {
		t.Fatalf("runs started after all ran = %d, want 4", n)
	}
	for _, exec := range []domain.LadoExecution{one, two} {
		runs := f.store.runsOf(exec.SessionID, exec.PR.HeadSHA)
		if len(runs) != 2 || runs[0].Name != "unit" || runs[1].Name != "lint" {
			t.Fatalf("runs of %s = %+v, want unit then lint", exec.SessionID, runs)
		}
		for _, run := range runs {
			if run.FinishedAt.IsZero() || run.ExitCode == nil {
				t.Errorf("run %s of %s = %s exit %v, want it ended", run.Name, exec.SessionID, run.Status, run.ExitCode)
			}
			if log, err := f.blobs.Get(run.LogSHA256, MaxCommandOutput); err != nil || len(log) == 0 {
				t.Errorf("log of %s = %q, %v; want the output", run.Name, log, err)
			}
		}
	}
	if item := verifyItemOf(t, f.record(one.SessionID, verifyHead1), domain.LadoVerifyCommands); item.Outcome != domain.LadoVerifyPass {
		t.Errorf("commands item of %s = %s %s, want pass", one.SessionID, item.Outcome, item.Code)
	}
	if item := verifyItemOf(t, f.record(two.SessionID, verifyHead3), domain.LadoVerifyCommands); item.Outcome != domain.LadoVerifyFail || item.Code != domain.LadoCodeCommandsFailed {
		t.Errorf("commands item of %s = %s %s, want fail commands_failed", two.SessionID, item.Outcome, item.Code)
	}
	if got := f.sender.to(two.SessionID); len(got) != 1 || !strings.Contains(got[0], "\n\nunit exited with 1. The end of its output:\n--- FAIL: TestLogin\n\n") || strings.Contains(got[0], "lint") {
		t.Errorf("fix requests = %q, want one naming the unit tests only", got)
	}
	if most := f.runner.mostAtOnce(); most != 1 {
		t.Errorf("commands running at once = %d, want 1", most)
	}

	// A new head runs every command again, from a clean worktree.
	f.push(one, verifyHead2)
	f.tick()
	if n := f.store.runCount(); n != 4 {
		t.Fatalf("runs started in a worktree with changes = %d, want still 4", n)
	}
	if item := verifyItemOf(t, f.record(one.SessionID, verifyHead2), domain.LadoVerifyCommands); item.Outcome != domain.LadoVerifyWait || item.Code != domain.LadoCodeWorktreeNotAtHead {
		t.Errorf("commands item in a worktree with changes = %s %s, want wait worktree_not_at_head", item.Outcome, item.Code)
	}
	f.git.setDirty(f.reads.worktree(one.SessionID), false)
	f.tick()
	calls := f.runner.waitCalls(t, 5)
	if len(calls) != 5 || calls[4].Command != "go test ./..." || calls[4].Dir != f.reads.worktree(one.SessionID) {
		t.Fatalf("runs after the push = %+v, want unit again", calls)
	}
	f.finish(passed("ok"))
	if runs := f.store.runsOf(one.SessionID, verifyHead2); len(runs) != 1 || runs[0].Status != domain.LadoCheckRunPassed {
		t.Errorf("runs of the new head = %+v, want unit passed", runs)
	}
}

func TestTickSendsOneFixRequestPerHeadAndItem(t *testing.T) {
	f := newVerifyFixture(t)
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}}, 30)
	one := f.addExecution("1", verifyHead1)
	f.reads.updatePlan(one.PlanRevisionID, func(plan *domain.LadoPlan) { plan.ReviewRequired = true })
	// The unit tests failed on the head, after a long output.
	output := strings.Repeat("ok\tacme/demo/pkg\t0.01s\n", 200) + "--- FAIL: TestLogin\n"
	logSHA, err := f.blobs.Put([]byte(output))
	if err != nil {
		t.Fatal(err)
	}
	f.store.addRun(domain.LadoCheckRun{
		ID: "run-unit", ExecutionSessionID: one.SessionID, HeadSHA: verifyHead1, Name: "unit", Command: "go test ./...",
		Status: domain.LadoCheckRunFailed, ExitCode: verifyExit(2), LogSHA256: logSHA, StartedAt: t0, FinishedAt: t0.Add(time.Minute),
	})
	english := Commit{SHA: strings.Repeat("cd", 20), Message: "Add the login page for users"}
	f.git.setBranch(verifyHead1, []Commit{english}, []string{".github/workflows/ci.yml", "src/login.go"})
	review := strings.Repeat("Rename the handler to loginHandler. ", 120)
	f.reviews.setRuns(one.SessionID, domain.ReviewRun{
		ID: "review-9", SessionID: one.SessionID, Harness: domain.ReviewerCodex, PRURL: one.PR.URL, TargetSHA: verifyHead1,
		Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested, Body: review, CreatedAt: t0,
	})
	// Another worker's session ended; a third cannot take a message yet.
	ended := f.addExecution("2", verifyHead3)
	f.reads.endSession(ended.SessionID)
	f.git.setBranch(verifyHead3, nil, []string{".github/workflows/ci.yml"})
	busy := f.addExecution("3", verifyHead4)
	f.git.setBranch(verifyHead4, nil, []string{".claude/settings.json"})
	f.sender.failNext(busy.SessionID, 1)

	f.tick()
	messages := f.sender.to(one.SessionID)
	kinds := []domain.LadoVerifyKind{domain.LadoVerifyCommands, domain.LadoVerifyCommitMessages, domain.LadoVerifyPaths, domain.LadoVerifyReview}
	if len(messages) != len(kinds) {
		t.Fatalf("messages = %d, want one per failing item:\n%s", len(messages), strings.Join(messages, "\n----\n"))
	}
	for i, kind := range kinds {
		message := messages[i]
		header := "LADO checked head 1a2b3c4 of your pull request, and the " + string(kind) + " item failed: "
		if !strings.HasPrefix(message, header) || !strings.HasSuffix(message, "\n\n"+fixFooter) {
			t.Errorf("message %d does not start with %q and end with the footer:\n%s", i, header, message)
		}
		if len(message) > fixMessageMax {
			t.Errorf("message %d has %d bytes, more than %d", i, len(message), fixMessageMax)
		}
	}
	tail := between(messages[0], "unit exited with 2. The end of its output:\n", "\n\n"+fixFooter)
	if len(tail) > fixLogTailMax || !strings.HasSuffix(tail, "--- FAIL: TestLogin") || !strings.HasPrefix(tail, cutMark) {
		t.Errorf("log tail has %d bytes (at most %d), ending with the failure and cut at its start: %q", len(tail), fixLogTailMax, tail)
	}
	if !strings.Contains(messages[1], "\ncdcdcdc Add the login page for users") {
		t.Errorf("commit message request names no English commit:\n%s", messages[1])
	}
	if !strings.Contains(messages[2], "\n.github/workflows/ci.yml\n") || strings.Contains(messages[2], "src/login.go") {
		t.Errorf("paths request does not name only the protected path:\n%s", messages[2])
	}
	body := between(messages[3], "asked for changes.\n\n", "\n\n"+fixFooter)
	if len(body) > fixReviewBodyMax || !strings.HasPrefix(body, "Rename the handler") || !strings.HasSuffix(body, cutMark) {
		t.Errorf("review body has %d bytes (at most %d), from its start and cut: %q", len(body), fixReviewBodyMax, body)
	}
	if notified := f.record(one.SessionID, verifyHead1).Notified; !slices.Equal(notified, []string{"commands", "commit_messages", "paths", "review"}) {
		t.Errorf("notified = %v, want the four items", notified)
	}
	if got := f.sender.to(ended.SessionID); len(got) != 0 {
		t.Errorf("an ended session got %d messages", len(got))
	}
	if got, notified := f.sender.to(busy.SessionID), f.record(busy.SessionID, verifyHead4).Notified; len(got) != 0 || len(notified) != 0 {
		t.Errorf("a failed send left %d messages and notified %v, want none", len(got), notified)
	}

	f.tick()
	if got := f.sender.to(one.SessionID); len(got) != len(kinds) {
		t.Errorf("messages after another pass = %d, want still %d", len(got), len(kinds))
	}
	if got := f.sender.to(busy.SessionID); len(got) != 1 || !strings.Contains(got[0], ".claude/settings.json") {
		t.Errorf("the send that failed was not retried: %q", got)
	}
	if notified := f.record(busy.SessionID, verifyHead4).Notified; !slices.Equal(notified, []string{"paths"}) {
		t.Errorf("notified after the retry = %v, want [paths]", notified)
	}

	// A new head is told again about what fails on it.
	f.git.setBranch(verifyHead2, []Commit{english}, []string{".github/workflows/ci.yml"})
	f.push(one, verifyHead2)
	f.tick()
	got := f.sender.to(one.SessionID)[len(kinds):]
	if len(got) != 2 || !strings.Contains(got[0], "head 5e6f7a8") || !strings.Contains(got[0], "commit_messages item") || !strings.Contains(got[1], "paths item") {
		t.Errorf("messages for the new head = %q, want commit_messages and paths", got)
	}
}

func TestTickTriggersReviewWhenReady(t *testing.T) {
	f := newVerifyFixture(t)
	one := f.addExecution("1", verifyHead1)
	f.reads.updatePlan(one.PlanRevisionID, func(plan *domain.LadoPlan) { plan.ReviewRequired, plan.Reviewer = true, "Codex" })
	// Another plan requires a review too, but a CI check failed on its head.
	failing := f.addExecution("2", verifyHead3)
	f.reads.updatePlan(failing.PlanRevisionID, func(plan *domain.LadoPlan) { plan.ReviewRequired = true })
	f.store.setCommitChecks(failing.PR.URL, verifyHead3, domain.PullRequestCheck{Name: "build", CommitHash: verifyHead3, Status: domain.PRCheckFailed})

	f.tick()
	triggers := f.reviews.triggered()
	if len(triggers) != 1 || triggers[0] != (verifyTrigger{one.SessionID, one.PR.URL, domain.ReviewerCodex}) {
		t.Fatalf("triggers = %+v, want one for %s with codex", triggers, one.SessionID)
	}
	rounds := f.store.roundsOf(one.SessionID)
	if len(rounds) != 1 || rounds[0].ReviewRunID != "review-run-1" || rounds[0].Round != 1 || rounds[0].HeadSHA != verifyHead1 || !rounds[0].CreatedAt.Equal(f.clock.Now()) {
		t.Fatalf("rounds = %+v, want review-run-1 as round 1 on the head", rounds)
	}
	if item := verifyItemOf(t, f.record(one.SessionID, verifyHead1), domain.LadoVerifyReview); item.Outcome != domain.LadoVerifyWait || item.Code != domain.LadoCodeReviewPending {
		t.Errorf("review item = %s %s, want wait review_pending", item.Outcome, item.Code)
	}

	// While the run goes on nothing starts; once it ran too long LADO
	// cancels it.
	f.reviews.setRuns(one.SessionID, domain.ReviewRun{ID: "review-run-1", SessionID: one.SessionID, PRURL: one.PR.URL, TargetSHA: verifyHead1, Status: domain.ReviewRunRunning, CreatedAt: f.clock.Now()})
	f.tick()
	if n, cancels := len(f.reviews.triggered()), f.reviews.cancelled(); n != 1 || len(cancels) != 0 {
		t.Fatalf("while the review runs: %d triggers and cancels %v, want 1 and none", n, cancels)
	}
	f.clock.advance(domain.LadoReviewStallAfter + time.Minute)
	f.tick()
	if cancels := f.reviews.cancelled(); !slices.Equal(cancels, []domain.SessionID{one.SessionID}) {
		t.Fatalf("cancels = %v, want %s", cancels, one.SessionID)
	}
	if rounds := f.store.roundsOf(one.SessionID); !rounds[0].CancelledAt.Equal(f.clock.Now()) {
		t.Errorf("round 1 cancelled at %s, want %s", rounds[0].CancelledAt, f.clock.Now())
	}

	// The cancelled round counts; the next pass starts round 2.
	f.reviews.setRuns(one.SessionID, domain.ReviewRun{ID: "review-run-1", SessionID: one.SessionID, PRURL: one.PR.URL, TargetSHA: verifyHead1, Status: domain.ReviewRunCancelled, CreatedAt: t0})
	f.tick()
	if rounds := f.store.roundsOf(one.SessionID); len(rounds) != 2 || rounds[1].Round != 2 || rounds[1].ReviewRunID != "review-run-2" {
		t.Errorf("rounds after the cancel = %+v, want round 2", rounds)
	}
	for _, trigger := range f.reviews.triggered() {
		if trigger.worker == failing.SessionID {
			t.Errorf("a review started for %s while a CI check fails", failing.SessionID)
		}
	}
}

func TestTickRetriesStatusPosting(t *testing.T) {
	f := newVerifyFixture(t)
	one := f.addExecution("1", verifyHead1)
	lab := f.addExecution("2", verifyHead3)
	f.reads.updateTask(lab.TaskID, func(task *domain.LadoTask) {
		task.Repo = domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "acme/demo"}
	})
	f.host.setPostErr(errors.New("github tracker: post the lado/integration status: the host answered 500: token ghp_" + strings.Repeat("a", 36) + " refused\nretry later"))
	f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-gh", ExecutionSessionID: one.SessionID, PRURL: one.PR.URL, HeadSHA: verifyHead1, StatusState: domain.LadoIntegrationStatusPending})
	f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-gl", ExecutionSessionID: lab.SessionID, PRURL: lab.PR.URL, HeadSHA: verifyHead3, StatusState: domain.LadoIntegrationStatusPending})

	f.tick()
	posts := f.host.postsMade()
	want := verifyPost{repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}, sha: verifyHead1, status: ports.LadoCommitStatus{State: "success", Context: "lado/integration", Description: "Approved in LADO"}}
	if len(posts) != 1 || posts[0] != want {
		t.Fatalf("posts = %+v, want %+v", posts, want)
	}
	gh := f.store.approval("approval-gh")
	if gh.StatusState != domain.LadoIntegrationStatusFailed || gh.StatusAttempts != 1 || !gh.StatusPostedAt.IsZero() {
		t.Errorf("GitHub approval = %s after %d attempts posted at %s, want failed after 1", gh.StatusState, gh.StatusAttempts, gh.StatusPostedAt)
	}
	if strings.Contains(gh.StatusError, "ghp_") || strings.Contains(gh.StatusError, "\n") || !strings.Contains(gh.StatusError, "[redacted]") {
		t.Errorf("status error = %q, want one line without the token", gh.StatusError)
	}
	if gl := f.store.approval("approval-gl"); gl.StatusState != domain.LadoIntegrationStatusNotApplicable || gl.StatusAttempts != 0 {
		t.Errorf("GitLab approval = %s after %d attempts, want not_applicable without one", gl.StatusState, gl.StatusAttempts)
	}

	f.clock.advance(time.Minute)
	f.tick()
	if n := len(f.host.postsMade()); n != 1 {
		t.Fatalf("posts a minute later = %d, want still 1", n)
	}
	f.clock.advance(statusRetryAfter - time.Minute)
	f.tick()
	if n, attempts := len(f.host.postsMade()), f.store.approval("approval-gh").StatusAttempts; n != 2 || attempts != 2 {
		t.Fatalf("five minutes later: %d posts, %d attempts; want 2 and 2", n, attempts)
	}
	for range domain.LadoIntegrationStatusMaxAttempts {
		f.clock.advance(statusRetryAfter)
		f.tick()
	}
	if n, attempts := len(f.host.postsMade()), f.store.approval("approval-gh").StatusAttempts; n != domain.LadoIntegrationStatusMaxAttempts || attempts != domain.LadoIntegrationStatusMaxAttempts {
		t.Errorf("after every retry: %d posts, %d attempts; want %d", n, attempts, domain.LadoIntegrationStatusMaxAttempts)
	}

	// A post that works records the time.
	f.host.setPostErr(nil)
	f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-new", ExecutionSessionID: one.SessionID, PRURL: one.PR.URL, HeadSHA: verifyHead2, StatusState: domain.LadoIntegrationStatusPending})
	f.tick()
	posted := f.store.approval("approval-new")
	if posted.StatusState != domain.LadoIntegrationStatusPosted || posted.StatusAttempts != 1 || !posted.StatusPostedAt.Equal(f.clock.Now()) || posted.StatusError != "" {
		t.Errorf("approval = %s after %d attempts at %s (%q), want posted now", posted.StatusState, posted.StatusAttempts, posted.StatusPostedAt, posted.StatusError)
	}
	if n := len(f.host.postsMade()); n != domain.LadoIntegrationStatusMaxAttempts+1 {
		t.Errorf("posts = %d, want the new approval's only", n)
	}
}

func TestTickSettlesMergedWithoutApproval(t *testing.T) {
	f := newVerifyFixture(t)
	merged := f.addExecution("1", verifyHead1)
	f.store.update(merged.SessionID, func(exec *domain.LadoExecution) { exec.PR.Merged = true })
	approved := f.addExecution("2", verifyHead3)
	f.store.update(approved.SessionID, func(exec *domain.LadoExecution) {
		exec.PR.Merged, exec.Integration.MergedHeadApproved = true, true
	})
	closed := f.addExecution("3", verifyHead4)
	f.store.update(closed.SessionID, func(exec *domain.LadoExecution) { exec.PR.Closed = true })

	f.tick()
	audits := f.store.auditEvents()
	if len(audits) != 1 {
		t.Fatalf("audit events = %+v, want one", audits)
	}
	audit := audits[0]
	if audit.Actor != "lado" || audit.Action != "execution.merged_without_approval" || audit.SubjectKind != "execution" || audit.SubjectID != string(merged.SessionID) || !audit.At.Equal(f.clock.Now()) {
		t.Errorf("audit = %+v", audit)
	}
	var payload map[string]string
	if err := json.Unmarshal(audit.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"task": "task-1", "session": "lado-1", "pr": merged.PR.URL, "head": verifyHead1}; !mapsEqual(payload, want) {
		t.Errorf("payload = %v, want %v", payload, want)
	}
	for _, exec := range []domain.LadoExecution{merged, approved, closed} {
		if at, ok := f.store.settledAt(exec.SessionID); !ok || !at.Equal(f.clock.Now()) {
			t.Errorf("%s settled = %s, %v; want now", exec.SessionID, at, ok)
		}
		if _, ok := f.store.recordOf(exec.SessionID, exec.PR.HeadSHA); ok {
			t.Errorf("%s got a verification record", exec.SessionID)
		}
	}

	f.clock.advance(time.Minute)
	f.tick()
	if n := len(f.store.auditEvents()); n != 1 {
		t.Errorf("audit events after another pass = %d, want 1", n)
	}
	if listed := f.store.lastListed(); len(listed) != 0 {
		t.Errorf("settled executions are still listed: %v", listed)
	}
}

func TestTickGoesOnAfterABrokenExecution(t *testing.T) {
	f := newVerifyFixture(t)
	lost := f.addExecution("1", verifyHead1)
	f.reads.deleteTask(lost.TaskID)
	gitless := f.addExecution("2", verifyHead3)
	f.reads.setProject(domain.ProjectRecord{ID: "p2", Path: t.TempDir()}, nil)
	f.reads.updateTask(gitless.TaskID, func(task *domain.LadoTask) { task.ProjectID = "p2" })
	f.git.breakRepo(f.reads.project("p2").Path)
	fine := f.addExecution("3", verifyHead4)

	err := f.svc.Tick(f.ctx)
	if err == nil || !strings.Contains(err.Error(), string(lost.SessionID)) || !strings.Contains(err.Error(), "task-1 not found") {
		t.Fatalf("Tick = %v, want the lost task's error", err)
	}
	if _, ok := f.store.recordOf(lost.SessionID, verifyHead1); ok {
		t.Error("an execution without its task got a record")
	}
	record := f.record(gitless.SessionID, verifyHead3)
	if len(record.Items) != len(domain.LadoVerifyKinds()) {
		t.Fatalf("record of a repository git cannot read has %d items, want every one", len(record.Items))
	}
	for _, kind := range []domain.LadoVerifyKind{domain.LadoVerifyCommitMessages, domain.LadoVerifyPaths, domain.LadoVerifyBaseBranch, domain.LadoVerifyEvidence} {
		if item := verifyItemOf(t, record, kind); item.Outcome != domain.LadoVerifyWait || item.Code != domain.LadoValidatingVerifying {
			t.Errorf("%s = %s %s, want wait verifying", kind, item.Outcome, item.Code)
		}
	}
	if outcome, reason := record.Overall(); outcome != domain.LadoVerifyWait || reason != domain.LadoValidatingVerifying {
		t.Errorf("Overall = %s %q, want wait verifying", outcome, reason)
	}
	if outcome, _ := f.record(fine.SessionID, verifyHead4).Overall(); outcome != domain.LadoVerifyConfirm {
		t.Errorf("the execution after the broken ones = %s, want confirm", outcome)
	}
}

func TestStartCancelsLeftoverCheckRuns(t *testing.T) {
	f := newVerifyFixture(t)
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}}, 30)
	one := f.addExecution("1", verifyHead1)
	f.store.addRun(domain.LadoCheckRun{ID: "run-left", ExecutionSessionID: one.SessionID, HeadSHA: verifyHead1, Name: "unit", Command: "go test ./...", Status: domain.LadoCheckRunRunning, StartedAt: t0})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.svc.Start(ctx)
	waitUntil(t, "the command runs again", func() bool { return len(f.runner.calls()) == 1 })
	runs := f.store.runsOf(one.SessionID, verifyHead1)
	if len(runs) != 2 || runs[0].ID != "run-left" || runs[0].Status != domain.LadoCheckRunCancelled || !runs[0].FinishedAt.Equal(f.clock.Now()) {
		t.Fatalf("runs = %+v, want the leftover cancelled now", runs)
	}
	if runs[1].Status != domain.LadoCheckRunRunning || runs[1].Name != "unit" {
		t.Fatalf("new run = %+v, want unit running", runs[1])
	}

	// Later passes leave the daemon's own run alone.
	f.svc.Kick()
	waitUntil(t, "a second pass", func() bool { return f.store.listCount() >= 2 })
	if runs := f.store.runsOf(one.SessionID, verifyHead1); runs[1].Status != domain.LadoCheckRunRunning || f.store.cancelCount() != 1 {
		t.Fatalf("after a second pass: run %s, leftovers cancelled %d times; want running, once", runs[1].Status, f.store.cancelCount())
	}

	cancel()
	f.svc.Wait()
	runs = f.store.runsOf(one.SessionID, verifyHead1)
	if runs[1].Status != domain.LadoCheckRunCancelled || runs[1].FinishedAt.IsZero() || !strings.Contains(runs[1].ErrorMessage, "cancelled") {
		t.Errorf("run after the stop = %+v, want cancelled with its end", runs[1])
	}
	if n := f.store.runCount(); n != 2 {
		t.Errorf("runs = %d, want the leftover and its rerun", n)
	}
}

// Turning LADO mode on reads the rules of the base branch of each of the
// project's repositories again, past the cache, in the background of the
// running loop: in a workspace, each repository with an origin, at the
// branch the dispatcher would start its workers from. Before Start and after
// the stop nothing is read; a branch that cannot be resolved has a read with
// only its error.
func TestRefreshProtectionReadsEachBaseBranchAgain(t *testing.T) {
	f := newVerifyFixture(t)
	root := t.TempDir()
	f.reads.setProject(domain.ProjectRecord{ID: "ws", Path: root, Kind: domain.ProjectKindWorkspace}, []domain.WorkspaceRepoRecord{
		{ProjectID: "ws", Name: "web", RelativePath: "web", RepoOriginURL: "https://github.com/acme/web.git", DefaultBranch: "develop"},
		{ProjectID: "ws", Name: "docs", RelativePath: "docs"},
		{ProjectID: "ws", Name: "api", RelativePath: "api", RepoOriginURL: "https://github.com/acme/api.git"},
	})
	branches := &verifyBranches{byDir: map[string]string{filepath.Join(root, "web"): "develop", filepath.Join(root, "api"): "main"}}
	svc := New(Deps{Store: f.store, Projects: f.reads, Clients: f.host, Branches: branches, Clock: f.clock.Now,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Interval: time.Hour})
	want := []string{"acme/web@develop", "acme/api@main"}

	svc.RefreshProtection("ws")
	reads, err := svc.ProjectProtection(context.Background(), "ws", false)
	if err != nil || len(reads) != 2 || reads[0].Branch != "develop" || reads[1].Branch != "main" || reads[0].Err != nil || reads[1].Err != nil {
		t.Fatalf("ProjectProtection = %+v, %v; want web at develop and api at main", reads, err)
	}
	if got := f.host.branchReads(); !slices.Equal(got, want) {
		t.Fatalf("host reads = %v before Start, want only ProjectProtection's %v", got, want)
	}
	if got := branches.configuredSeen(); !slices.Equal(got, []string{"develop", ""}) {
		t.Fatalf("configured branches passed = %q, want the workspace's", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)
	svc.RefreshProtection("ws")
	waitUntil(t, "the refresh reads both branches again", func() bool { return len(f.host.branchReads()) == 4 })
	cancel()
	svc.Wait()
	svc.RefreshProtection("ws")
	time.Sleep(50 * time.Millisecond)
	if got := f.host.branchReads(); !slices.Equal(got, append(want, want...)) {
		t.Fatalf("host reads = %v, want two rounds and none after the stop", got)
	}

	branches.setErr(errors.New("no origin/HEAD"))
	reads, err = svc.ProjectProtection(context.Background(), "ws", true)
	if err != nil || len(reads) != 2 || reads[0].Err == nil || reads[0].Branch != "" || reads[0].Repo.Native != "acme/web" {
		t.Fatalf("ProjectProtection with no base branch = %+v, %v; want each repository with its error", reads, err)
	}
}

// verifyBranches resolves a repository folder's base branch.
type verifyBranches struct {
	mu         sync.Mutex
	byDir      map[string]string
	err        error
	configured []string
}

func (b *verifyBranches) ResolveDefaultBranch(_ context.Context, dir, configured string) (ports.WorkspaceDefaultBranch, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.configured = append(b.configured, configured)
	if b.err != nil {
		return ports.WorkspaceDefaultBranch{}, b.err
	}
	return ports.WorkspaceDefaultBranch{Remote: "origin", Branch: b.byDir[dir]}, nil
}

func (b *verifyBranches) setErr(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.err = err
}

func (b *verifyBranches) configuredSeen() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.configured)
}

// verifyFixture is the loop over fakes: project p1, a single repository,
// whose tasks' executions the tests add, and the hosts and services around
// it. Its passes run under a context its cleanup ends, so check commands
// still running end as cancelled.
type verifyFixture struct {
	t       *testing.T
	ctx     context.Context
	store   *verifyStore
	reads   *verifyReads
	git     *verifyGit
	host    *verifyHost
	reviews *verifyReviews
	sender  *verifySender
	runner  *verifyRunner
	blobs   *ladoblob.Store
	clock   *protectionClock
	svc     *Service
}

func newVerifyFixture(t *testing.T) *verifyFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f := &verifyFixture{
		t:       t,
		ctx:     ctx,
		store:   &verifyStore{timeout: domain.LadoCheckTimeoutDefault},
		reads:   &verifyReads{},
		git:     &verifyGit{},
		host:    &verifyHost{rules: protectionRules(protectionAllTypes, domain.LadoIntegrationStatusContext)},
		reviews: &verifyReviews{},
		sender:  &verifySender{},
		runner:  &verifyRunner{results: make(chan CommandResult)},
		blobs:   ladoblob.New(t.TempDir()),
		clock:   &protectionClock{now: t0.Add(time.Hour)},
	}
	f.reads.setProject(domain.ProjectRecord{ID: "p1", Path: t.TempDir()}, nil)
	var ids atomic.Int64
	f.svc = New(Deps{
		Store:     f.store,
		Tasks:     f.reads,
		Plans:     f.reads,
		Snapshots: f.reads,
		Projects:  f.reads,
		Sessions:  f.reads,
		Git:       f.git,
		Clients:   f.host,
		Reviews:   f.reviews,
		Artifacts: &evidenceArtifacts{},
		Blobs:     f.blobs,
		Sender:    f.sender,
		Run:       f.runner.Run,
		Env:       func() []string { return []string{"PATH=/usr/bin", "AO_SESSION=ao-1", "GITHUB_TOKEN=gh-secret"} },
		Clock:     f.clock.Now,
		NewID:     func() string { return strconv.FormatInt(ids.Add(1), 10) },
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Interval:  time.Hour,
	})
	t.Cleanup(func() {
		cancel()
		f.svc.Wait()
	})
	return f
}

// addExecution adds an open execution at head of a new task in project p1,
// with its approved plan, a rule snapshot holding the Korean commit message
// and protected paths validators, and a live worker session whose worktree
// is at head.
func (f *verifyFixture) addExecution(key, head string) domain.LadoExecution {
	f.t.Helper()
	number, err := strconv.Atoi(key)
	if err != nil {
		f.t.Fatal(err)
	}
	exec := domain.LadoExecution{
		SessionID:        domain.SessionID("lado-" + key),
		TaskID:           domain.LadoTaskID("task-" + key),
		PlanRevisionID:   domain.LadoPlanRevisionID("rev-" + key),
		PlanApprovalID:   domain.LadoPlanApprovalID("approval-" + key),
		PolicySnapshotID: domain.LadoPolicySnapshotID("snapshot-" + key),
		Attempt:          1,
		BaseBranch:       "main",
		BaseRemote:       "origin",
		StartedAt:        t0,
		PR: domain.LadoExecutionPR{
			URL: "https://github.com/acme/demo/pull/" + key, Number: number,
			HeadSHA: head, TargetBranch: "main", Title: "로그인 오류를 고친다",
		},
	}
	worktree := f.t.TempDir()
	f.reads.mu.Lock()
	f.reads.tasks[exec.TaskID] = domain.LadoTask{ID: exec.TaskID, ProjectID: "p1", Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}}
	f.reads.plans[exec.PlanRevisionID] = domain.LadoPlan{AcceptanceCriteria: []string{"The login works"}, AffectedPaths: []string{"src/**"}}
	f.reads.snapshots[exec.PlanApprovalID] = domain.LadoPolicySnapshot{ID: exec.PolicySnapshotID, ApprovalID: exec.PlanApprovalID, Content: verifySnapshotContent(f.t)}
	f.reads.sessions[exec.SessionID] = domain.SessionRecord{ID: exec.SessionID, ProjectID: "p1", Metadata: domain.SessionMetadata{WorkspacePath: worktree}}
	f.reads.mu.Unlock()
	f.git.setHead(worktree, head)
	f.store.addExecution(exec)
	return exec
}

// addWorkspaceExecution adds an execution whose task's issue is in the
// child repository api of workspace project ws; its plan names paths from
// the workspace root. It returns the execution and the child repository.
func (f *verifyFixture) addWorkspaceExecution(key, head string) (domain.LadoExecution, string) {
	f.t.Helper()
	exec := f.addExecution(key, head)
	root := f.t.TempDir()
	f.reads.setProject(domain.ProjectRecord{ID: "ws", Path: root, Kind: domain.ProjectKindWorkspace}, []domain.WorkspaceRepoRecord{
		{ProjectID: "ws", Name: "web", RelativePath: "web", RepoOriginURL: "https://github.com/acme/web.git"},
		{ProjectID: "ws", Name: "api", RelativePath: "api", RepoOriginURL: "https://github.com/acme/demo.git"},
	})
	f.reads.updateTask(exec.TaskID, func(task *domain.LadoTask) { task.ProjectID = "ws" })
	f.reads.updatePlan(exec.PlanRevisionID, func(plan *domain.LadoPlan) { plan.AffectedPaths = []string{"api/src/**"} })
	f.git.setHead(filepath.Join(f.reads.worktree(exec.SessionID), "api"), head)
	return exec, filepath.Join(root, "api")
}

func (f *verifyFixture) tick() {
	f.t.Helper()
	if err := f.svc.Tick(f.ctx); err != nil {
		f.t.Fatalf("Tick = %v", err)
	}
}

// finish ends the check command that runs with result and waits for the
// kick that follows once its end is recorded.
func (f *verifyFixture) finish(result CommandResult) {
	f.t.Helper()
	select {
	case f.runner.results <- result:
	case <-time.After(10 * time.Second):
		f.t.Fatal("no check command runs")
	}
	select {
	case <-f.svc.kick:
	case <-time.After(10 * time.Second):
		f.t.Fatal("no pass was kicked after the command ended")
	}
}

// push moves the execution's PR head and its worktree to head.
func (f *verifyFixture) push(exec domain.LadoExecution, head string) {
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.PR.HeadSHA = head })
	f.git.setHead(f.reads.worktree(exec.SessionID), head)
}

func (f *verifyFixture) record(session domain.SessionID, head string) domain.LadoVerification {
	f.t.Helper()
	record, ok := f.store.recordOf(session, head)
	if !ok {
		f.t.Fatalf("no record of %s at %s", session, shortCommit(head))
	}
	return record
}

func verifyItemOf(t *testing.T, record domain.LadoVerification, kind domain.LadoVerifyKind) domain.LadoVerifyItem {
	t.Helper()
	for _, item := range record.Items {
		if item.Kind == kind {
			return item
		}
	}
	t.Fatalf("record has no %s item", kind)
	return domain.LadoVerifyItem{}
}

func verifySHA(t *testing.T, record domain.LadoVerification) string {
	t.Helper()
	sum, err := record.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

// verifySnapshotContent is a lado-policy/3 snapshot's content with the
// validators of the Korean commit message rule and the protected paths, at
// their defaults.
func verifySnapshotContent(t *testing.T) []byte {
	t.Helper()
	locale, err := json.Marshal(domain.DefaultLadoCommitLocaleConfig())
	if err != nil {
		t.Fatal(err)
	}
	paths, err := json.Marshal(domain.LadoProtectedPathsConfig{Globs: domain.DefaultLadoProtectedPaths()})
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(struct {
		Compiler   string                         `json:"compiler"`
		Validators []ladopolicy.SnapshotValidator `json:"validators"`
	}{ladopolicy.Compiler, []ladopolicy.SnapshotValidator{
		{RuleID: "git.commit.message.korean", Kind: ladopolicy.ValidatorCommitMessageLocale, Config: locale},
		{RuleID: domain.LadoRuleProtectedPaths, Kind: ladopolicy.ValidatorProtectedPaths, Config: paths},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func verifyExit(code int) *int { return &code }

// between returns the text of s after start and before the last end.
func between(s, start, end string) string {
	i := strings.Index(s, start)
	j := strings.LastIndex(s, end)
	if i < 0 || j < i+len(start) {
		return ""
	}
	return s[i+len(start) : j]
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// waitUntil waits up to ten seconds for done.
func waitUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// verifyStore is LADO's store as the loop uses it, holding what S03's store
// holds and keeping its rules: a record keeps its notified list when given
// none, rounds come in order, an execution settles once, and the status of
// an approval is retried while pending or failed and below the attempts cap.
type verifyStore struct {
	mu         sync.Mutex
	executions []domain.LadoExecution
	commands   []domain.LadoCheckCommand
	timeout    int
	checks     map[string][]domain.PullRequestCheck
	runs       []domain.LadoCheckRun
	records    map[string]domain.LadoVerification
	changed    map[string][]bool
	links      map[string]int
	reviews    []domain.LadoExecutionReview
	settled    map[domain.SessionID]time.Time
	audits     []domain.LadoAuditEvent
	approvals  []domain.LadoIntegrationApproval
	lists      int
	listed     []domain.SessionID
	cancels    int
}

func verifyKey(session domain.SessionID, head string) string { return string(session) + "@" + head }

func (s *verifyStore) addExecution(exec domain.LadoExecution) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executions = append(s.executions, exec)
}

func (s *verifyStore) update(session domain.SessionID, change func(*domain.LadoExecution)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.executions {
		if s.executions[i].SessionID == session {
			change(&s.executions[i])
		}
	}
}

func (s *verifyStore) setChecks(commands []domain.LadoCheckCommand, minutes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands, s.timeout = commands, minutes
}

func (s *verifyStore) setCommitChecks(prURL, commit string, checks ...domain.PullRequestCheck) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checks == nil {
		s.checks = map[string][]domain.PullRequestCheck{}
	}
	s.checks[prURL+" "+commit] = checks
}

func (s *verifyStore) addRun(run domain.LadoCheckRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, run)
}

func (s *verifyStore) addApproval(a domain.LadoIntegrationApproval) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvals = append(s.approvals, a)
}

func (s *verifyStore) ListLadoExecutionsToVerify(_ context.Context, limit int) ([]domain.LadoExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists++
	s.listed = nil
	var out []domain.LadoExecution
	for _, exec := range s.executions {
		if _, settled := s.settled[exec.SessionID]; !settled && len(out) < limit {
			out = append(out, exec)
			s.listed = append(s.listed, exec.SessionID)
		}
	}
	return out, nil
}

func (s *verifyStore) LadoExecution(_ context.Context, session domain.SessionID) (domain.LadoExecution, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, exec := range s.executions {
		if exec.SessionID == session {
			return exec, true, nil
		}
	}
	return domain.LadoExecution{}, false, nil
}

func (s *verifyStore) LadoProjectChecks(context.Context, domain.ProjectID) ([]domain.LadoCheckCommand, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.commands), s.timeout, nil
}

func (s *verifyStore) GetPR(context.Context, string) (domain.PullRequest, bool, error) {
	return domain.PullRequest{}, false, nil
}

func (s *verifyStore) ListLadoCommitChecks(_ context.Context, prURL, commit string) ([]domain.PullRequestCheck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.checks[prURL+" "+commit]), nil
}

func (s *verifyStore) StartLadoCheckRun(_ context.Context, run domain.LadoCheckRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run.Status = domain.LadoCheckRunRunning
	s.runs = append(s.runs, run)
	return nil
}

func (s *verifyStore) FinishLadoCheckRun(_ context.Context, id string, status domain.LadoCheckRunStatus, exitCode *int, logSHA256, errorMessage string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.runs {
		if s.runs[i].ID != id {
			continue
		}
		if s.runs[i].Status != domain.LadoCheckRunRunning {
			return domain.ErrLadoCheckRunFinished
		}
		s.runs[i].Status, s.runs[i].ExitCode, s.runs[i].LogSHA256, s.runs[i].ErrorMessage, s.runs[i].FinishedAt = status, exitCode, logSHA256, errorMessage, at
		return nil
	}
	return fmt.Errorf("no check run %s", id)
}

func (s *verifyStore) CancelRunningLadoCheckRuns(_ context.Context, at time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels++
	n := 0
	for i := range s.runs {
		if s.runs[i].Status == domain.LadoCheckRunRunning {
			s.runs[i].Status, s.runs[i].FinishedAt = domain.LadoCheckRunCancelled, at
			n++
		}
	}
	return n, nil
}

func (s *verifyStore) ListLadoCheckRuns(_ context.Context, session domain.SessionID, head string) ([]domain.LadoCheckRun, error) {
	return s.runsOf(session, head), nil
}

func (s *verifyStore) runsOf(session domain.SessionID, head string) []domain.LadoCheckRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	var runs []domain.LadoCheckRun
	for _, run := range s.runs {
		if run.ExecutionSessionID == session && run.HeadSHA == head {
			runs = append(runs, run)
		}
	}
	return runs
}

func (s *verifyStore) runCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runs)
}

func (s *verifyStore) GetLadoVerification(_ context.Context, session domain.SessionID, head string) (domain.LadoVerification, bool, error) {
	record, ok := s.recordOf(session, head)
	return record, ok, nil
}

func (s *verifyStore) recordOf(session domain.SessionID, head string) (domain.LadoVerification, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[verifyKey(session, head)]
	record.Items, record.Notified = slices.Clone(record.Items), slices.Clone(record.Notified)
	return record, ok
}

func (s *verifyStore) UpsertLadoVerification(_ context.Context, v domain.LadoVerification) (bool, error) {
	sum, err := v.SHA256()
	if err != nil {
		return false, err
	}
	outcome, reason := v.Overall()
	s.mu.Lock()
	defer s.mu.Unlock()
	key := verifyKey(v.ExecutionSessionID, v.HeadSHA)
	old, found := s.records[key]
	changed := !found
	if found {
		oldSum, _ := old.SHA256()
		oldOutcome, oldReason := old.Overall()
		changed = oldSum != sum || oldOutcome != outcome || oldReason != reason
	}
	switch {
	case v.Notified != nil:
		v.Notified = slices.Clone(v.Notified)
	case found:
		v.Notified = old.Notified
	default:
		v.Notified = []string{}
	}
	v.Items = slices.Clone(v.Items)
	if s.records == nil {
		s.records, s.changed = map[string]domain.LadoVerification{}, map[string][]bool{}
	}
	s.records[key] = v
	s.changed[key] = append(s.changed[key], changed)
	return changed, nil
}

func (s *verifyStore) changedOf(session domain.SessionID, head string) []bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.changed[verifyKey(session, head)])
}

func (s *verifyStore) ListLadoExecutionReviews(_ context.Context, session domain.SessionID) ([]domain.LadoExecutionReview, error) {
	return s.roundsOf(session), nil
}

func (s *verifyStore) roundsOf(session domain.SessionID) []domain.LadoExecutionReview {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rounds []domain.LadoExecutionReview
	for _, round := range s.reviews {
		if round.ExecutionSessionID == session {
			rounds = append(rounds, round)
		}
	}
	return rounds
}

func (s *verifyStore) AddLadoExecutionReview(_ context.Context, r domain.LadoExecutionReview) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := 1
	for _, round := range s.reviews {
		if round.ExecutionSessionID == r.ExecutionSessionID {
			next++
		}
	}
	if r.Round != next || r.Round > domain.LadoReviewRoundCap {
		return fmt.Errorf("round %d is not the next one (%d)", r.Round, next)
	}
	s.reviews = append(s.reviews, r)
	return nil
}

func (s *verifyStore) MarkLadoExecutionReviewCancelled(_ context.Context, runID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.reviews {
		if s.reviews[i].ReviewRunID == runID {
			s.reviews[i].CancelledAt = at
			return nil
		}
	}
	return fmt.Errorf("no review round of run %s", runID)
}

func (s *verifyStore) ReplaceLadoEvidenceLinks(_ context.Context, session domain.SessionID, head string, links []domain.LadoEvidenceLink) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, link := range links {
		if link.ID == "" || link.CreatedAt.IsZero() {
			return errors.New("an evidence link needs an id and a time")
		}
	}
	if s.links == nil {
		s.links = map[string]int{}
	}
	s.links[verifyKey(session, head)]++
	return nil
}

func (s *verifyStore) linkWritesOf(session domain.SessionID, head string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.links[verifyKey(session, head)]
}

func (s *verifyStore) SettleLadoExecution(_ context.Context, session domain.SessionID, at time.Time, audit *domain.LadoAuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, done := s.settled[session]; done {
		return nil
	}
	if s.settled == nil {
		s.settled = map[domain.SessionID]time.Time{}
	}
	s.settled[session] = at
	if audit != nil {
		s.audits = append(s.audits, *audit)
	}
	return nil
}

func (s *verifyStore) settledAt(session domain.SessionID) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.settled[session]
	return at, ok
}

func (s *verifyStore) auditEvents() []domain.LadoAuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.audits)
}

func (s *verifyStore) ListLadoIntegrationStatusRetries(_ context.Context, limit int) ([]domain.LadoIntegrationApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.LadoIntegrationApproval
	for _, a := range s.approvals {
		retry := a.StatusState == domain.LadoIntegrationStatusPending || a.StatusState == domain.LadoIntegrationStatusFailed
		if retry && a.StatusAttempts < domain.LadoIntegrationStatusMaxAttempts && len(out) < limit {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *verifyStore) SetLadoIntegrationStatus(_ context.Context, id string, state domain.LadoIntegrationStatusState, attempts int, postedAt time.Time, errorMessage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.approvals {
		if s.approvals[i].ID == id {
			a := &s.approvals[i]
			a.StatusState, a.StatusAttempts, a.StatusPostedAt, a.StatusError = state, attempts, postedAt, errorMessage
			return nil
		}
	}
	return fmt.Errorf("no integration approval %s", id)
}

func (s *verifyStore) approval(id string) domain.LadoIntegrationApproval {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.approvals {
		if a.ID == id {
			return a
		}
	}
	return domain.LadoIntegrationApproval{}
}

func (s *verifyStore) listCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists
}

func (s *verifyStore) lastListed() []domain.SessionID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.listed)
}

func (s *verifyStore) cancelCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancels
}

// verifyReads holds the tasks, plans, rule snapshots, projects and sessions
// the loop reads.
type verifyReads struct {
	mu        sync.Mutex
	tasks     map[domain.LadoTaskID]domain.LadoTask
	plans     map[domain.LadoPlanRevisionID]domain.LadoPlan
	snapshots map[domain.LadoPlanApprovalID]domain.LadoPolicySnapshot
	projects  map[string]domain.ProjectRecord
	repos     map[string][]domain.WorkspaceRepoRecord
	sessions  map[domain.SessionID]domain.SessionRecord
}

func (r *verifyReads) setProject(project domain.ProjectRecord, repos []domain.WorkspaceRepoRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.projects == nil {
		r.tasks = map[domain.LadoTaskID]domain.LadoTask{}
		r.plans = map[domain.LadoPlanRevisionID]domain.LadoPlan{}
		r.snapshots = map[domain.LadoPlanApprovalID]domain.LadoPolicySnapshot{}
		r.projects = map[string]domain.ProjectRecord{}
		r.repos = map[string][]domain.WorkspaceRepoRecord{}
		r.sessions = map[domain.SessionID]domain.SessionRecord{}
	}
	r.projects[project.ID], r.repos[project.ID] = project, repos
}

func (r *verifyReads) project(id string) domain.ProjectRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.projects[id]
}

func (r *verifyReads) setProjectEnv(id string, env map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	project := r.projects[id]
	project.Config.Env = env
	r.projects[id] = project
}

func (r *verifyReads) updateTask(id domain.LadoTaskID, change func(*domain.LadoTask)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task := r.tasks[id]
	change(&task)
	r.tasks[id] = task
}

func (r *verifyReads) deleteTask(id domain.LadoTaskID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tasks, id)
}

func (r *verifyReads) updatePlan(id domain.LadoPlanRevisionID, change func(*domain.LadoPlan)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	plan := r.plans[id]
	change(&plan)
	r.plans[id] = plan
}

func (r *verifyReads) endSession(id domain.SessionID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.sessions[id]
	session.IsTerminated = true
	r.sessions[id] = session
}

func (r *verifyReads) worktree(id domain.SessionID) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[id].Metadata.WorkspacePath
}

func (r *verifyReads) LadoTask(_ context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	return task, ok, nil
}

func (r *verifyReads) ApprovedPlan(_ context.Context, id domain.LadoPlanRevisionID) (domain.LadoPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	plan, ok := r.plans[id]
	if !ok {
		return domain.LadoPlan{}, fmt.Errorf("no plan revision %s", id)
	}
	return plan, nil
}

func (r *verifyReads) LadoPolicySnapshot(_ context.Context, id domain.LadoPlanApprovalID) (domain.LadoPolicySnapshot, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot, ok := r.snapshots[id]
	return snapshot, ok, nil
}

func (r *verifyReads) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	project, ok := r.projects[id]
	return project, ok, nil
}

func (r *verifyReads) ListWorkspaceRepos(_ context.Context, id string) ([]domain.WorkspaceRepoRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.repos[id]), nil
}

func (r *verifyReads) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[id]
	return session, ok, nil
}

// verifyGit answers git reads by head: the merge-base is verifyBase, each
// head has its commits and changed paths (verifyKoreanCommit and
// src/login.go unless set), worktrees have their HEAD and are clean unless
// set dirty, and no repository has a local base branch. A broken repository answers every read with an
// error.
type verifyGit struct {
	mu      sync.Mutex
	commits map[string][]Commit
	changed map[string][]string
	heads   map[string]string
	dirty   map[string]bool
	broken  map[string]bool
	dirs    map[string]bool
}

func (g *verifyGit) setBranch(head string, commits []Commit, changed []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.commits == nil {
		g.commits, g.changed = map[string][]Commit{}, map[string][]string{}
	}
	if commits == nil {
		commits = []Commit{verifyKoreanCommit}
	}
	g.commits[head], g.changed[head] = commits, changed
}

func (g *verifyGit) setHead(worktree, head string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.heads == nil {
		g.heads = map[string]string{}
	}
	g.heads[worktree] = head
}

func (g *verifyGit) setDirty(worktree string, dirty bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.dirty == nil {
		g.dirty = map[string]bool{}
	}
	g.dirty[worktree] = dirty
}

func (g *verifyGit) breakRepo(dir string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.broken == nil {
		g.broken = map[string]bool{}
	}
	g.broken[dir] = true
}

func (g *verifyGit) asked(dir string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.dirs[dir]
}

func (g *verifyGit) askedDirs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var dirs []string
	for dir := range g.dirs {
		dirs = append(dirs, dir)
	}
	return dirs
}

// read notes the repository and fails for a broken one.
func (g *verifyGit) read(dir, command string) error {
	if g.dirs == nil {
		g.dirs = map[string]bool{}
	}
	g.dirs[dir] = true
	if g.broken[dir] {
		return fmt.Errorf("git %s: fatal: not a git repository: %s", command, dir)
	}
	return nil
}

func (g *verifyGit) MergeBase(_ context.Context, dir, _, _ string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.read(dir, "merge-base"); err != nil {
		return "", err
	}
	return verifyBase, nil
}

func (g *verifyGit) Commits(_ context.Context, dir, _, to string) ([]Commit, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.read(dir, "rev-list"); err != nil {
		return nil, err
	}
	if commits, ok := g.commits[to]; ok {
		return commits, nil
	}
	return []Commit{verifyKoreanCommit}, nil
}

func (g *verifyGit) ChangedPaths(_ context.Context, dir, _, to string) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.read(dir, "diff-tree"); err != nil {
		return nil, err
	}
	if changed, ok := g.changed[to]; ok {
		return changed, nil
	}
	return []string{"src/login.go"}, nil
}

func (g *verifyGit) Reflog(_ context.Context, dir, _ string) ([]ReflogEntry, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return nil, g.read(dir, "reflog")
}

func (g *verifyGit) IsAncestor(_ context.Context, dir, _, _ string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return false, g.read(dir, "merge-base")
}

func (g *verifyGit) RevParse(_ context.Context, dir, ref string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if head, ok := g.heads[dir]; ok && ref == "HEAD" {
		return head, nil
	}
	if err := g.read(dir, "rev-parse"); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s in %s: %w", ref, dir, ErrRefNotFound)
}

func (g *verifyGit) StatusClean(_ context.Context, dir string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.dirty[dir], nil
}

func (g *verifyGit) Fetch(context.Context, string, string, string) error { return nil }

// verifyHost is the repositories' host: it answers branch rules and records
// the commit statuses posted, refusing them while postErr is set.
type verifyHost struct {
	mu      sync.Mutex
	rules   ports.LadoBranchRules
	postErr error
	posts   []verifyPost
	// ruleReads are the branches whose rules were read, as repo@branch.
	ruleReads []string
}

type verifyPost struct {
	repo   domain.TrackerRepo
	sha    string
	status ports.LadoCommitStatus
}

func (h *verifyHost) ForRepo(context.Context, domain.TrackerRepo) (ports.LadoRepoClient, error) {
	return verifyClient{host: h}, nil
}

func (h *verifyHost) setPostErr(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.postErr = err
}

func (h *verifyHost) postsMade() []verifyPost {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.posts)
}

type verifyClient struct {
	ports.LadoRepoClient
	host *verifyHost
}

func (c verifyClient) BranchRules(_ context.Context, repo domain.TrackerRepo, branch string) (ports.LadoBranchRules, error) {
	c.host.mu.Lock()
	defer c.host.mu.Unlock()
	c.host.ruleReads = append(c.host.ruleReads, repo.Native+"@"+branch)
	return c.host.rules, nil
}

func (h *verifyHost) branchReads() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.ruleReads)
}

func (c verifyClient) PostCommitStatus(_ context.Context, repo domain.TrackerRepo, sha string, status ports.LadoCommitStatus) error {
	c.host.mu.Lock()
	defer c.host.mu.Unlock()
	c.host.posts = append(c.host.posts, verifyPost{repo: repo, sha: sha, status: status})
	return c.host.postErr
}

// verifyReviews is AO's review service: each trigger creates the run
// review-run-<n>.
type verifyReviews struct {
	mu       sync.Mutex
	runs     map[domain.SessionID][]domain.ReviewRun
	triggers []verifyTrigger
	cancels  []domain.SessionID
}

type verifyTrigger struct {
	worker  domain.SessionID
	prURL   string
	harness domain.ReviewerHarness
}

func (r *verifyReviews) setRuns(worker domain.SessionID, runs ...domain.ReviewRun) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs == nil {
		r.runs = map[domain.SessionID][]domain.ReviewRun{}
	}
	r.runs[worker] = runs
}

func (r *verifyReviews) triggered() []verifyTrigger {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.triggers)
}

func (r *verifyReviews) cancelled() []domain.SessionID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.cancels)
}

func (r *verifyReviews) Trigger(_ context.Context, worker domain.SessionID, prURL string, harness domain.ReviewerHarness) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.triggers = append(r.triggers, verifyTrigger{worker: worker, prURL: prURL, harness: harness})
	return []string{fmt.Sprintf("review-run-%d", len(r.triggers))}, nil
}

func (r *verifyReviews) Runs(_ context.Context, worker domain.SessionID) ([]domain.ReviewRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.runs[worker]), nil
}

func (r *verifyReviews) Cancel(_ context.Context, worker domain.SessionID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancels = append(r.cancels, worker)
	return nil
}

// verifySender is AO's session manager: it records the messages each worker
// got, after failing the number of sends a test asks for.
type verifySender struct {
	mu       sync.Mutex
	sent     map[domain.SessionID][]string
	failures map[domain.SessionID]int
}

func (s *verifySender) failNext(id domain.SessionID, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures == nil {
		s.failures = map[domain.SessionID]int{}
	}
	s.failures[id] = n
}

func (s *verifySender) to(id domain.SessionID) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent[id])
}

func (s *verifySender) Send(_ context.Context, id domain.SessionID, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures[id] > 0 {
		s.failures[id]--
		return errors.New("the session takes no message yet")
	}
	if s.sent == nil {
		s.sent = map[domain.SessionID][]string{}
	}
	s.sent[id] = append(s.sent[id], message)
	return nil
}

// verifyRunner runs check commands that end when the test hands them a
// result, or as cancelled when their context ends.
type verifyRunner struct {
	mu            sync.Mutex
	specs         []CommandSpec
	running, most int
	results       chan CommandResult
}

func (r *verifyRunner) Run(ctx context.Context, spec CommandSpec) CommandResult {
	r.mu.Lock()
	r.specs = append(r.specs, spec)
	r.running++
	r.most = max(r.most, r.running)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running--
		r.mu.Unlock()
	}()
	select {
	case result := <-r.results:
		return result
	case <-ctx.Done():
		return CommandResult{Status: domain.LadoCheckRunCancelled, Err: fmt.Errorf("the check command was cancelled: %w", ctx.Err())}
	}
}

func (r *verifyRunner) calls() []CommandSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.specs)
}

// waitCalls waits until n commands started and returns them.
func (r *verifyRunner) waitCalls(t *testing.T, n int) []CommandSpec {
	t.Helper()
	waitUntil(t, fmt.Sprintf("%d check commands to start", n), func() bool { return len(r.calls()) >= n })
	return r.calls()
}

func (r *verifyRunner) mostAtOnce() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.most
}
