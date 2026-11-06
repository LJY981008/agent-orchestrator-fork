package ladoverify

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// verifyTip is the commit another PR's merge moved origin's main to.
var verifyTip = strings.Repeat("7d", 20)

// #53: a head whose base took a merge of the same paths (base_overlap)
// loses its integration approval in the loop: a pending lado/integration
// status replaces the success, on GitLab too (#21), where the waiting status
// of an unapproved head follows, the worker is told once, and the head
// cannot be approved again.
func TestTickRevokesAnApprovalOnceTheBaseOverlaps(t *testing.T) {
	f := newApproveFixture(t)
	hub := f.add("1", verifyHead1)
	lab := f.add("2", verifyHead3)
	f.reads.updateTask(lab.TaskID, func(task *domain.LadoTask) {
		task.Repo = domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "acme/demo"}
	})
	f.store.addApproval(domain.LadoIntegrationApproval{
		ID: "approval-gh", ExecutionSessionID: hub.SessionID, PRURL: hub.PR.URL, HeadSHA: verifyHead1, TargetBranch: "main",
		StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 1, StatusPostedAt: t0, BaseTipSHA: verifyBase,
	})
	f.store.addApproval(domain.LadoIntegrationApproval{
		ID: "approval-gl", ExecutionSessionID: lab.SessionID, PRURL: lab.PR.URL, HeadSHA: verifyHead3, TargetBranch: "main",
		StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 1, StatusPostedAt: t0, BaseTipSHA: verifyBase,
	})
	// Another PR's merge into main changed src/login.go, which both heads
	// change too.
	f.git.moveBase(verifyTip, []string{"src/login.go", "README.md"}, verifyHead1, verifyHead3)

	f.tick()
	record := f.record(hub.SessionID, verifyHead1)
	item := verifyItemOf(t, record, domain.LadoVerifyBaseBranch)
	if item.Outcome != domain.LadoVerifyFail || item.Code != domain.LadoCodeBaseOverlap {
		t.Fatalf("base_branch item = %s %q, want fail %s", item.Outcome, item.Code, domain.LadoCodeBaseOverlap)
	}
	var details domain.LadoBaseBranchDetails
	if err := json.Unmarshal(item.Details, &details); err != nil {
		t.Fatal(err)
	}
	if !details.Advanced || details.AdvancedTip != verifyTip || !slices.Equal(details.Overlap, []string{"src/login.go"}) || details.OverlapCount != 1 {
		t.Errorf("details = %+v, want advanced to the tip with src/login.go overlapping", details)
	}

	gh := f.store.approval("approval-gh")
	if !gh.Revoked() || gh.RevokedReason != domain.LadoCodeBaseOverlap || !gh.RevokedAt.Equal(f.clock.Now()) {
		t.Errorf("GitHub approval revoked at %s for %q, want now for base_overlap", gh.RevokedAt, gh.RevokedReason)
	}
	// Its status started over and was posted once: pending.
	if gh.StatusState != domain.LadoIntegrationStatusPosted || gh.StatusAttempts != 1 || !gh.StatusPostedAt.Equal(f.clock.Now()) {
		t.Errorf("GitHub approval status = %s after %d attempts at %s, want posted now after 1", gh.StatusState, gh.StatusAttempts, gh.StatusPostedAt)
	}
	want := verifyPost{repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}, sha: verifyHead1,
		status: ports.LadoCommitStatus{State: "pending", Context: "lado/integration", Description: "Base moved in LADO; verify a new head"}}
	if posts := f.host.postsTo(domain.TrackerProviderGitHub); len(posts) != 1 || posts[0] != want {
		t.Fatalf("posts = %+v, want %+v", posts, want)
	}
	gl := f.store.approval("approval-gl")
	if !gl.Revoked() || gl.StatusState != domain.LadoIntegrationStatusPosted || gl.StatusAttempts != 1 {
		t.Errorf("GitLab approval = revoked %v, %s after %d attempts; want revoked, posted after 1", gl.Revoked(), gl.StatusState, gl.StatusAttempts)
	}
	// The merge request's revoked approval posts its pending status, then
	// the pass posts the waiting status of the head, approved no more.
	labRepo := domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "acme/demo"}
	wantLab := []verifyPost{
		{repo: labRepo, sha: verifyHead3, status: ports.LadoCommitStatus{State: "pending", Context: "lado/integration", Description: "Base moved in LADO; verify a new head", MergeRequest: lab.PR.Number}},
		{repo: labRepo, sha: verifyHead3, status: ports.LadoCommitStatus{State: "pending", Context: "lado/integration", Description: "Waiting for approval in LADO", MergeRequest: lab.PR.Number}},
	}
	if posts := f.host.postsTo(domain.TrackerProviderGitLab); !slices.Equal(posts, wantLab) {
		t.Fatalf("GitLab posts = %+v, want %+v", posts, wantLab)
	}

	audits := f.store.auditEvents()
	if len(audits) != 2 {
		t.Fatalf("audit events = %+v, want one per approval", audits)
	}
	for i, exec := range []domain.LadoExecution{hub, lab} {
		audit := audits[i]
		if audit.Action != domain.LadoAuditIntegrationRevoked || audit.Actor != "lado" || audit.SubjectID != string(exec.SessionID) {
			t.Errorf("audit %d = %+v", i, audit)
		}
		var payload map[string]string
		if err := json.Unmarshal(audit.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if want := map[string]string{"task": string(exec.TaskID), "session": string(exec.SessionID), "pr": exec.PR.URL, "head": exec.PR.HeadSHA, "target": "main", "reason": "base_overlap"}; !mapsEqual(payload, want) {
			t.Errorf("audit %d payload = %v, want %v", i, payload, want)
		}
	}

	// The worker is told once to take in the base.
	messages := f.sender.to(hub.SessionID)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want one:\n%s", len(messages), strings.Join(messages, "\n----\n"))
	}
	for _, part := range []string{
		"LADO checked head 1a2b3c4 of your pull request, and the base_branch item failed: ",
		"the base branch main took a merge that changed the same paths as your branch",
		"Merge the base into your branch (or rebase onto it and push with --force-with-lease), run the project's check commands again and push.",
		"\n\nsrc/login.go",
	} {
		if !strings.Contains(messages[0], part) {
			t.Errorf("fix request lacks %q:\n%s", part, messages[0])
		}
	}
	if strings.Contains(messages[0], "README.md") {
		t.Errorf("fix request names a file only the base changed:\n%s", messages[0])
	}

	// The same head cannot be approved again.
	preview := f.preview(hub)
	_, err := f.approve(hub, preview.SHA256, preview.Confirm, "nonce-revoked")
	requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_REVOKED")

	// The task waits for the head to take in the base.
	exec, _, _ := f.store.LadoExecution(f.ctx, hub.SessionID)
	exec.Verification = domain.LadoExecutionVerification{HeadSHA: record.HeadSHA, BaseOverlap: baseOverlapped(record)}
	if state, code := exec.State(); state != domain.LadoTaskValidating || code != domain.LadoCodeBaseOverlap {
		t.Errorf("task state = %s (%s), want %s (%s)", state, code, domain.LadoTaskValidating, domain.LadoCodeBaseOverlap)
	}

	// Another pass revokes, posts and tells nothing again.
	f.clock.advance(statusRetryAfter)
	f.tick()
	if n := len(f.host.postsTo(domain.TrackerProviderGitHub)); n != 1 {
		t.Errorf("posts after another pass = %d, want 1", n)
	}
	if n := len(f.store.auditEvents()); n != 2 {
		t.Errorf("audit events after another pass = %d, want 2", n)
	}
	if n := len(f.sender.to(hub.SessionID)); n != 1 {
		t.Errorf("messages after another pass = %d, want 1", n)
	}
}

// The pending status of a revoked approval that could not be posted is
// posted again by the loop, pending as well.
func TestTickRetriesThePendingStatusOfARevokedApproval(t *testing.T) {
	f := newApproveFixture(t)
	exec := f.add("1", verifyHead1)
	f.store.addApproval(domain.LadoIntegrationApproval{
		ID: "approval-gh", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1, TargetBranch: "main",
		StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 3, StatusPostedAt: t0,
	})
	f.git.moveBase(verifyTip, []string{"src/login.go"}, verifyHead1)
	f.host.setPostErr(errors.New("the host answered 502"))

	f.tick()
	revoked := f.store.approval("approval-gh")
	if !revoked.Revoked() || revoked.StatusState != domain.LadoIntegrationStatusFailed || revoked.StatusAttempts != 1 {
		t.Fatalf("approval = revoked %v, %s after %d attempts; want revoked, failed after 1", revoked.Revoked(), revoked.StatusState, revoked.StatusAttempts)
	}
	f.host.setPostErr(nil)
	f.clock.advance(statusRetryAfter)
	f.tick()
	// The post that failed and the one that worked, both pending.
	posts := f.host.postsMade()
	if len(posts) != 2 {
		t.Fatalf("posts = %+v, want two", posts)
	}
	for _, post := range posts {
		if post.status.State != "pending" || post.status.Description != revokedStatusDescription {
			t.Errorf("post = %+v, want pending", post)
		}
	}
	if a := f.store.approval("approval-gh"); a.StatusState != domain.LadoIntegrationStatusPosted || a.StatusAttempts != 2 {
		t.Errorf("approval = %s after %d attempts, want posted after 2", a.StatusState, a.StatusAttempts)
	}
}

// A base that moved without a change to the head's paths only asks the
// developer to acknowledge it (base_advanced); the approval holds.
func TestTickKeepsTheApprovalWhenTheBaseAdvances(t *testing.T) {
	f := newApproveFixture(t)
	exec := f.add("1", verifyHead1)
	f.store.addApproval(domain.LadoIntegrationApproval{
		ID: "approval-gh", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1, TargetBranch: "main",
		StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 1,
	})
	f.git.moveBase(verifyTip, []string{"docs/guide.md"}, verifyHead1)

	f.tick()
	item := verifyItemOf(t, f.record(exec.SessionID, verifyHead1), domain.LadoVerifyBaseBranch)
	if item.Outcome != domain.LadoVerifyConfirm || item.Code != domain.LadoCodeBaseAdvanced {
		t.Fatalf("base_branch item = %s %q, want confirm %s", item.Outcome, item.Code, domain.LadoCodeBaseAdvanced)
	}
	if a := f.store.approval("approval-gh"); a.Revoked() {
		t.Errorf("approval revoked for %q", a.RevokedReason)
	}
	if n := len(f.host.postsMade()) + len(f.sender.to(exec.SessionID)); n != 0 {
		t.Errorf("posts and messages = %d, want none", n)
	}

	// A new head must acknowledge it.
	f.add("2", verifyHead2)
	f.git.setRef(verifyHead2, verifyHead2)
	other, _, _ := f.store.LadoExecution(f.ctx, "lado-2")
	preview := f.preview(other)
	if !slices.Contains(preview.Confirm, domain.LadoCodeBaseAdvanced) {
		t.Fatalf("confirm codes = %v, want base_advanced among them", preview.Confirm)
	}
	without := slices.DeleteFunc(slices.Clone(preview.Confirm), func(code string) bool { return code == domain.LadoCodeBaseAdvanced })
	_, err := f.approve(other, preview.SHA256, without, "nonce-advanced-1")
	requireAPIError(t, err, apierr.KindInvalid, "LADO_INTEGRATION_ACK_MISMATCH")
	result, err := f.approve(other, preview.SHA256, preview.Confirm, "nonce-advanced-2")
	if err != nil {
		t.Fatalf("approval acknowledging base_advanced = %v", err)
	}
	if result.Approval.BaseTipSHA != verifyTip {
		t.Errorf("approval's base tip = %q, want %q", result.Approval.BaseTipSHA, verifyTip)
	}
}

// The owner of an execution's requests is its session while that lives,
// else its live resolve worker; check commands run in the worktree at the
// head.
func TestTickSendsTheBaseOverlapToTheOwner(t *testing.T) {
	for _, tc := range []struct {
		name string
		// end ends the original session.
		end   func(*verifyFixture, domain.LadoExecution)
		owner domain.SessionID
		// resolverAtHead puts the resolve worker's worktree at the head and
		// the original's elsewhere.
		resolverAtHead bool
	}{
		{name: "the original session lives", owner: "lado-1"},
		{name: "the original lives, the resolver's worktree is at the head", owner: "lado-1", resolverAtHead: true},
		{name: "the original session ended", end: func(f *verifyFixture, exec domain.LadoExecution) { f.reads.endSession(exec.SessionID) }, owner: "lado-fix-1", resolverAtHead: true},
		{name: "the original session exited", end: func(f *verifyFixture, exec domain.LadoExecution) {
			f.reads.setActivity(exec.SessionID, domain.ActivityExited)
		}, owner: "lado-fix-1", resolverAtHead: true},
		{name: "the execution's session terminated", end: func(f *verifyFixture, exec domain.LadoExecution) {
			f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.SessionTerminated = true })
		}, owner: "lado-fix-1", resolverAtHead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVerifyFixture(t)
			f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}}, 30)
			exec := f.addExecution("1", verifyHead1)
			resolver := f.addSession("lado-fix-1")
			f.store.update(exec.SessionID, func(e *domain.LadoExecution) {
				e.Resolve = domain.LadoExecutionResolve{State: domain.LadoResolveResolving, SessionID: "lado-fix-1"}
			})
			if tc.resolverAtHead {
				f.git.setHead(resolver, verifyHead1)
				f.git.setHead(f.reads.worktree(exec.SessionID), verifyHead2)
			}
			if tc.end != nil {
				tc.end(f, exec)
			}
			f.git.moveBase(verifyTip, []string{"src/login.go"}, verifyHead1)

			f.tick()
			for _, id := range []domain.SessionID{"lado-1", "lado-fix-1"} {
				messages := f.sender.to(id)
				want := 0
				if id == tc.owner {
					want = 1
				}
				if len(messages) != want {
					t.Errorf("%s got %d messages, want %d", id, len(messages), want)
				}
				if want == 1 && !strings.Contains(messages[0], "base_branch item failed") {
					t.Errorf("%s got %q, want the base_branch request", id, messages[0])
				}
			}
			dir := f.reads.worktree(exec.SessionID)
			if tc.resolverAtHead {
				dir = resolver
			}
			if spec := f.runner.waitCalls(t, 1)[0]; spec.Dir != dir {
				t.Errorf("check command ran in %s, want %s", spec.Dir, dir)
			}
		})
	}

	t.Run("no session lives", func(t *testing.T) {
		f := newVerifyFixture(t)
		exec := f.addExecution("1", verifyHead1)
		f.addSession("lado-fix-1")
		f.reads.endSession("lado-fix-1")
		f.store.update(exec.SessionID, func(e *domain.LadoExecution) {
			e.Resolve = domain.LadoExecutionResolve{State: domain.LadoResolveNeedsHuman, SessionID: "lado-fix-1"}
		})
		f.reads.endSession(exec.SessionID)
		f.git.moveBase(verifyTip, []string{"src/login.go"}, verifyHead1)

		f.tick()
		if n := len(f.sender.to("lado-1")) + len(f.sender.to("lado-fix-1")); n != 0 {
			t.Errorf("messages = %d, want none", n)
		}
		if notified := f.record(exec.SessionID, verifyHead1).Notified; len(notified) != 0 {
			t.Errorf("notified = %v, want none", notified)
		}
	})
}

// The loop fetches a base branch at most every two minutes, and at once
// after LADO settled a merge into it; that pass lists more executions so
// the target's open ones come first.
func TestTickFetchesTheBaseEveryTwoMinutesAndAfterAMerge(t *testing.T) {
	f := newVerifyFixture(t)
	open := f.addExecution("1", verifyHead1)
	merging := f.addExecution("2", verifyHead3)

	f.tick()
	if n := f.git.fetchCount("origin", "main"); n != 1 {
		t.Fatalf("fetches = %d, want 1 for both executions", n)
	}
	f.clock.advance(time.Minute)
	f.tick()
	if n := f.git.fetchCount("origin", "main"); n != 1 {
		t.Fatalf("fetches a minute later = %d, want still 1", n)
	}
	f.clock.advance(time.Minute)
	f.tick()
	if n := f.git.fetchCount("origin", "main"); n != 2 {
		t.Fatalf("fetches two minutes later = %d, want 2", n)
	}

	f.store.update(merging.SessionID, func(e *domain.LadoExecution) { e.PR.Merged = true })
	f.tick()
	if n := f.git.fetchCount("origin", "main"); n != 2 {
		t.Fatalf("fetches in the pass that settled the merge = %d, want still 2", n)
	}
	select {
	case <-f.svc.kick:
	default:
		t.Fatal("the settled merge kicked no pass")
	}
	f.tick()
	if n := f.git.fetchCount("origin", "main"); n != 3 {
		t.Fatalf("fetches after the merge = %d, want 3", n)
	}
	if got, want := f.store.listLimit(), urgentListMax; got != want {
		t.Errorf("list limit after the merge = %d, want %d", got, want)
	}
	if _, ok := f.store.recordOf(open.SessionID, verifyHead1); !ok {
		t.Error("the open execution has no record")
	}
	f.tick()
	if n, limit := f.git.fetchCount("origin", "main"), f.store.listLimit(); n != 3 || limit != verifyBatch {
		t.Errorf("the next pass fetched %d times and listed %d, want 3 and %d", n, limit, verifyBatch)
	}
}

// After a merge, the open executions into its target come first, those
// with an approved head before the others; the rest keep the store's order.
func TestUrgentFirstOrdersTheMovedTarget(t *testing.T) {
	f := newVerifyFixture(t)
	release := f.addExecution("1", verifyHead1)
	release.PR.TargetBranch = "release"
	open := f.addExecution("2", verifyHead2)
	approved := f.addExecution("3", verifyHead3)
	approved.Integration.HeadApproved = true
	merged := f.addExecution("4", verifyHead4)
	merged.PR.Merged = true
	unknown := f.addExecution("5", verifyHead4)
	f.reads.deleteTask(unknown.TaskID)
	task, _, _ := f.reads.LadoTask(f.ctx, open.TaskID)
	urgent := map[string]bool{targetKey(task, open): true}

	got, keys := f.svc.urgentFirst(f.ctx, []domain.LadoExecution{release, open, merged, unknown, approved}, urgent)
	var order []domain.SessionID
	for _, exec := range got {
		order = append(order, exec.SessionID)
	}
	if want := []domain.SessionID{"lado-3", "lado-2", "lado-1", "lado-4", "lado-5"}; !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	if _, ok := keys[unknown.SessionID]; ok || len(keys) != 4 {
		t.Errorf("keys = %v, want the four executions whose task was read", keys)
	}
}

// PRD gate 7 with #53: the merge gate fetches the target of an approved
// head; once the target moved past the commit the approval was given
// against, it computes the head's record again (no check command starts)
// and refuses a head whose base overlaps it, revoking the approval.
func TestMergeGateChecksTheBaseOfAnApprovedHead(t *testing.T) {
	f := newApproveFixture(t)
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}}, 30)
	exec := f.add("1", verifyHead1)
	f.store.addApproval(domain.LadoIntegrationApproval{
		ID: "approval-gh", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1, TargetBranch: "main",
		StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 1, BaseTipSHA: verifyBase,
	})
	f.git.setRef("refs/remotes/origin/main", verifyBase)
	f.git.setRef(verifyHead1, verifyHead1)
	pr := domain.PullRequest{URL: exec.PR.URL, SessionID: exec.SessionID, Number: 1, HeadSHA: verifyHead1, TargetBranch: "main"}

	t.Run("the target has not moved", func(t *testing.T) {
		if err := f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1); err != nil {
			t.Fatalf("AuthorizeMerge = %v", err)
		}
		if n := f.git.fetchCount("origin", "main"); n != 1 {
			t.Errorf("fetches = %d, want 1", n)
		}
		if _, ok := f.store.recordOf(exec.SessionID, verifyHead1); ok {
			t.Error("the gate computed a record of a head whose base did not move")
		}
	})

	t.Run("the fetch fails", func(t *testing.T) {
		f.git.failFetch("origin", "main", errors.New("could not read from remote repository"))
		defer f.git.failFetch("origin", "main", nil)
		requireAPIError(t, f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1), apierr.KindInternal, "LADO_MERGE_GATE_FAILED")
		if f.store.approval("approval-gh").Revoked() {
			t.Error("a failed fetch revoked the approval")
		}
	})

	t.Run("the target moved without an overlap", func(t *testing.T) {
		f.git.moveBase(verifyTip, []string{"docs/guide.md"})
		if err := f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1); err != nil {
			t.Fatalf("AuthorizeMerge = %v", err)
		}
		item := verifyItemOf(t, f.record(exec.SessionID, verifyHead1), domain.LadoVerifyBaseBranch)
		if item.Code != domain.LadoCodeBaseAdvanced {
			t.Errorf("base_branch code = %q, want %s", item.Code, domain.LadoCodeBaseAdvanced)
		}
	})

	t.Run("the target moved with an overlap", func(t *testing.T) {
		other := strings.Repeat("3e", 20)
		f.git.moveBase(other, []string{"src/login.go"})
		requireAPIError(t, f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1), apierr.KindConflict, "LADO_INTEGRATION_REVOKED")
		a := f.store.approval("approval-gh")
		if !a.Revoked() || a.RevokedReason != domain.LadoCodeBaseOverlap || a.StatusState != domain.LadoIntegrationStatusPosted || a.StatusAttempts != 1 {
			t.Errorf("approval = revoked %v (%q), %s after %d attempts; want revoked for base_overlap, posted after 1", a.Revoked(), a.RevokedReason, a.StatusState, a.StatusAttempts)
		}
		if posts := f.host.postsMade(); len(posts) != 1 || posts[0].status.State != "pending" {
			t.Errorf("posts = %+v, want one pending", posts)
		}
		// A revoked approval no longer counts, whatever the target does.
		f.git.setRef("refs/remotes/origin/main", verifyBase)
		requireAPIError(t, f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1), apierr.KindConflict, "LADO_INTEGRATION_REVOKED")
		onRelease := pr
		onRelease.TargetBranch = "release"
		requireAPIError(t, f.svc.AuthorizeMerge(f.ctx, onRelease, verifyHead1), apierr.KindConflict, "LADO_INTEGRATION_APPROVAL_REQUIRED")
	})

	if n := len(f.runner.calls()); n != 0 {
		t.Errorf("the gate started %d check commands", n)
	}
}

// setActivity sets the activity state of a session.
func (r *verifyReads) setActivity(id domain.SessionID, state domain.ActivityState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.sessions[id]
	session.Activity.State = state
	r.sessions[id] = session
}

// addSession adds a live session of project p1 with a worktree of its own,
// as a resolve worker has, and returns the worktree.
func (f *verifyFixture) addSession(id domain.SessionID) string {
	worktree := f.t.TempDir()
	f.reads.mu.Lock()
	defer f.reads.mu.Unlock()
	f.reads.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "p1", Metadata: domain.SessionMetadata{WorkspacePath: worktree}}
	return worktree
}
