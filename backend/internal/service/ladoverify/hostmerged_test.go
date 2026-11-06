package ladoverify

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// countedHeads is the fixture's host that counts its reads per PR and can
// fail them.
type countedHeads struct {
	*approveHeads
	mu    sync.Mutex
	byURL map[string]int
	err   error
}

// countHeads puts a countedHeads in front of the fixture's host.
func countHeads(f *approveFixture) *countedHeads {
	h := &countedHeads{approveHeads: f.heads, byURL: map[string]int{}}
	f.svc.SetLiveHeads(h)
	return h
}

func (h *countedHeads) LivePR(ctx context.Context, pr domain.PullRequest) (LivePR, error) {
	h.mu.Lock()
	h.byURL[pr.URL]++
	err := h.err
	h.mu.Unlock()
	if err != nil {
		return LivePR{}, err
	}
	return h.approveHeads.LivePR(ctx, pr)
}

func (h *countedHeads) readsOf(url string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.byURL[url]
}

func (h *countedHeads) fail(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.err = err
}

// revokedAudits counts the integration.revoked audit events.
func revokedAudits(f *approveFixture) int {
	n := 0
	for _, audit := range f.store.auditEvents() {
		if audit.Action == domain.LadoAuditIntegrationRevoked {
			n++
		}
	}
	return n
}

// overlappedApproval is an execution at verifyHead1 with an approval that
// holds, whose base took a merge of src/login.go, a path the head changes
// too.
func overlappedApproval(f *approveFixture) domain.LadoExecution {
	f.t.Helper()
	exec := f.add("1", verifyHead1)
	f.store.addApproval(domain.LadoIntegrationApproval{
		ID: "approval-gh", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1, TargetBranch: "main",
		StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 1, StatusPostedAt: t0, BaseTipSHA: verifyBase,
	})
	f.git.moveBase(verifyTip, []string{"src/login.go"}, verifyHead1)
	return exec
}

// #75: the host merged the PR a moment ago, and AO still has it open. The
// base the loop sees moved by that merge: the approval holds, nothing is
// posted and the worker is not told to take in the base. The pass reads the
// host once for the fix request and the revocation; once the host says the
// PR is open, both happen as before.
func TestTickKeepsTheApprovalOfAPRTheHostMerged(t *testing.T) {
	f := newApproveFixture(t)
	heads := countHeads(f)
	exec := overlappedApproval(f)
	f.heads.setPR(exec.PR.URL, LivePR{HeadSHA: verifyHead1, TargetBranch: "main", Merged: true})

	f.tick()
	record := f.record(exec.SessionID, verifyHead1)
	if !baseOverlapped(record) {
		t.Fatalf("record = %+v, want base_overlap", record.Items)
	}
	if slices.Contains(record.Notified, string(domain.LadoVerifyBaseBranch)) {
		t.Errorf("notified = %v, want base_branch left out", record.Notified)
	}
	if a := f.store.approval("approval-gh"); a.Revoked() {
		t.Errorf("approval revoked for %q, want it to hold", a.RevokedReason)
	}
	if n := revokedAudits(f); n != 0 {
		t.Errorf("integration.revoked audits = %d, want none", n)
	}
	if posts := f.host.postsMade(); len(posts) != 0 {
		t.Errorf("posts = %+v, want none", posts)
	}
	if messages := f.sender.to(exec.SessionID); len(messages) != 0 {
		t.Errorf("messages = %q, want none", messages)
	}
	if n := heads.readsOf(exec.PR.URL); n != 1 {
		t.Errorf("host reads in the pass = %d, want 1", n)
	}

	// Each pass reads the host once until AO sees the merge.
	f.clock.advance(time.Minute)
	f.tick()
	if n := heads.readsOf(exec.PR.URL); n != 2 {
		t.Errorf("host reads after two passes = %d, want 2", n)
	}
	if a := f.store.approval("approval-gh"); a.Revoked() {
		t.Errorf("approval revoked on the second pass for %q", a.RevokedReason)
	}

	// The host has the PR open: the loop revokes and tells as before.
	f.heads.forget(exec.PR.URL)
	f.clock.advance(time.Minute)
	f.tick()
	if a := f.store.approval("approval-gh"); !a.Revoked() || a.RevokedReason != domain.LadoCodeBaseOverlap {
		t.Errorf("approval = revoked %v for %q, want revoked for base_overlap", a.Revoked(), a.RevokedReason)
	}
	if n := revokedAudits(f); n != 1 {
		t.Errorf("integration.revoked audits = %d, want 1", n)
	}
	if messages := f.sender.to(exec.SessionID); len(messages) != 1 || !strings.Contains(messages[0], "base_branch item failed") {
		t.Errorf("messages = %q, want the base_branch request", messages)
	}
}

// A host that cannot be read leaves the loop as it was: the approval is
// revoked, its pending status posted and the worker told.
func TestTickRevokesWhenTheHostCannotBeRead(t *testing.T) {
	f := newApproveFixture(t)
	heads := countHeads(f)
	exec := overlappedApproval(f)
	heads.fail(errors.New("the host answered 502"))

	f.tick()
	if a := f.store.approval("approval-gh"); !a.Revoked() || a.RevokedReason != domain.LadoCodeBaseOverlap {
		t.Errorf("approval = revoked %v for %q, want revoked for base_overlap", a.Revoked(), a.RevokedReason)
	}
	if n := revokedAudits(f); n != 1 {
		t.Errorf("integration.revoked audits = %d, want 1", n)
	}
	if posts := f.host.postsMade(); len(posts) != 1 || posts[0].status.State != "pending" {
		t.Errorf("posts = %+v, want one pending", posts)
	}
	if messages := f.sender.to(exec.SessionID); len(messages) != 1 {
		t.Errorf("messages = %d, want 1", len(messages))
	}
	if n := heads.readsOf(exec.PR.URL); n != 1 {
		t.Errorf("host reads in the pass = %d, want 1", n)
	}
}

// A sibling PR the host merged keeps its approval when another PR of the
// task loses its own for its base (#64, #75).
func TestTickKeepsTheApprovalOfASiblingTheHostMerged(t *testing.T) {
	f := newApproveFixture(t)
	heads := countHeads(f)
	exec, back, front := f.addMultiRepoApproval("7", verifyHead1, verifyHead2)
	f.approvedTask(exec, back, front)
	f.heads.setPR(front.URL, LivePR{HeadSHA: verifyHead2, TargetBranch: "main", Merged: true})
	// The approval read the host too.
	before := heads.readsOf(front.URL)

	f.git.moveBase(verifyTip, []string{"src/login.go"}, verifyHead1)
	f.clock.advance(3 * time.Minute)
	f.tick()
	if got := f.approvalOf(exec.SessionID, back, verifyHead1); got.RevokedReason != domain.LadoCodeBaseOverlap {
		t.Fatalf("back approval revoked for %q, want base_overlap", got.RevokedReason)
	}
	if got := f.approvalOf(exec.SessionID, front, verifyHead2); got.Revoked() {
		t.Errorf("front approval revoked for %q, want it to hold", got.RevokedReason)
	}
	if n := revokedAudits(f); n != 1 {
		t.Errorf("integration.revoked audits = %d, want back's only", n)
	}
	if n := heads.readsOf(front.URL) - before; n != 1 {
		t.Errorf("host reads of front in the pass = %d, want 1", n)
	}
}

// The merge gate merges an open PR now: it revokes an overlapped approval
// without asking the host whether the PR merged.
func TestMergeGateDoesNotAskTheHostWhetherThePRMerged(t *testing.T) {
	f := newApproveFixture(t)
	heads := countHeads(f)
	exec := f.add("1", verifyHead1)
	f.store.addApproval(domain.LadoIntegrationApproval{
		ID: "approval-gh", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1, TargetBranch: "main",
		StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 1, BaseTipSHA: verifyBase,
	})
	f.git.setRef(verifyHead1, verifyHead1)
	f.git.moveBase(verifyTip, []string{"src/login.go"})
	f.heads.setPR(exec.PR.URL, LivePR{HeadSHA: verifyHead1, TargetBranch: "main", Merged: true})

	pr := domain.PullRequest{URL: exec.PR.URL, SessionID: exec.SessionID, Number: 1, HeadSHA: verifyHead1, TargetBranch: "main"}
	requireAPIError(t, f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1), apierr.KindConflict, "LADO_INTEGRATION_REVOKED")
	if a := f.store.approval("approval-gh"); !a.Revoked() || a.RevokedReason != domain.LadoCodeBaseOverlap {
		t.Errorf("approval = revoked %v for %q, want revoked for base_overlap", a.Revoked(), a.RevokedReason)
	}
	if n := heads.readsOf(exec.PR.URL); n != 0 {
		t.Errorf("host reads = %d, want none", n)
	}
}
