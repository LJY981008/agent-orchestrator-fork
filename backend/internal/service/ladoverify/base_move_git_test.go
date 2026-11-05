package ladoverify

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// overlapRepo is a repository where two workers changed guide.md on their
// own branches, each a line apart, so git merges either into the other
// without a conflict; the loop's fixture reads it with real git.
type overlapRepo struct {
	*testRepo
	f            *approveFixture
	workA, workB string
	headA, headB string
}

func newOverlapRepo(t *testing.T) *overlapRepo {
	t.Helper()
	r := &overlapRepo{testRepo: newTestRepo(t, t0.Add(-time.Hour))}
	r.workA = r.worktree(t0.Add(time.Minute), "lado/a")
	r.write(r.workA, "guide.md", "How to run the app.\nInstall it with make.\nStart it.\nStop it.\nUpdate it.\n")
	r.headA = r.commit(t0.Add(2*time.Minute), r.workA, "docs: 설치 방법을 적는다")
	r.workB = r.worktree(t0.Add(time.Minute), "lado/b")
	r.write(r.workB, "guide.md", "How to run the app.\nInstall it.\nStart it.\nStop it.\nUpdate it with git pull.\n")
	r.headB = r.commit(t0.Add(3*time.Minute), r.workB, "docs: 갱신 방법을 적는다")

	r.f = newApproveFixture(t, func(d *Deps) { d.Git = ExecGit{} })
	r.f.reads.setProject(domain.ProjectRecord{ID: "p1", Path: r.dir}, nil)
	return r
}

// add adds the execution of a worker whose session's worktree is work.
func (r *overlapRepo) add(key, head, work string) domain.LadoExecution {
	r.t.Helper()
	exec := r.f.add(key, head)
	r.f.reads.mu.Lock()
	session := r.f.reads.sessions[exec.SessionID]
	session.Metadata.WorkspacePath = work
	r.f.reads.sessions[exec.SessionID] = session
	r.f.reads.mu.Unlock()
	return exec
}

// mergeA merges A's branch into origin's main, as the host's merge of its
// PR would, and marks A's PR merged.
func (r *overlapRepo) mergeA(exec domain.LadoExecution) {
	r.t.Helper()
	r.git(t0.Add(4*time.Minute), r.workA, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.PR.Merged = true })
}

// approve approves exec's head as the developer does after the dry run.
func (r *overlapRepo) approve(exec domain.LadoExecution, nonce string) (ApproveResult, error) {
	r.t.Helper()
	preview := r.f.preview(exec)
	return r.f.approve(exec, preview.SHA256, preview.Confirm, nonce)
}

// #53 with real git: two executions change the same file, one merges into
// the remote base without a text conflict. The other's head fails with
// base_overlap, its worker is told once, and it cannot be approved; the
// head that took in the base is verified on its own and can be approved,
// while the old head is refused as changed.
func TestBaseOverlapAfterAnotherMerge(t *testing.T) {
	r := newOverlapRepo(t)
	f := r.f
	a := r.add("1", r.headA, r.workA)
	b := r.add("2", r.headB, r.workB)
	r.mergeA(a)

	f.tick()
	item := verifyItemOf(t, f.record(b.SessionID, r.headB), domain.LadoVerifyBaseBranch)
	if item.Outcome != domain.LadoVerifyFail || item.Code != domain.LadoCodeBaseOverlap {
		t.Fatalf("base_branch item = %s %q, want fail %s", item.Outcome, item.Code, domain.LadoCodeBaseOverlap)
	}
	var details domain.LadoBaseBranchDetails
	if err := json.Unmarshal(item.Details, &details); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(details.Overlap, []string{"guide.md"}) || details.AdvancedTip != r.headA {
		t.Errorf("details = %+v, want guide.md overlapping at A's head", details)
	}
	messages := f.sender.to(b.SessionID)
	if len(messages) != 1 || !strings.Contains(messages[0], "base_branch item failed") || !strings.Contains(messages[0], "\n\nguide.md") {
		t.Fatalf("messages = %q, want one base_branch request naming guide.md", messages)
	}
	_, err := r.approve(b, "nonce-overlap")
	requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_NOT_READY")

	// The worker merges the base into its branch and pushes.
	r.git(t0.Add(5*time.Minute), r.workB, "merge", "-q", "--no-edit", "-m", "merge: 기준 브랜치를 받아들인다", "refs/remotes/origin/main")
	merged := r.git(t0.Add(5*time.Minute), r.workB, "rev-parse", "HEAD")
	r.git(t0.Add(5*time.Minute), r.workB, "push", "-q", "origin", "HEAD:refs/heads/lado/b")
	f.store.update(b.SessionID, func(e *domain.LadoExecution) { e.PR.HeadSHA = merged })
	f.heads.set(b.PR.URL, merged)

	f.clock.advance(time.Minute)
	f.tick()
	item = verifyItemOf(t, f.record(b.SessionID, merged), domain.LadoVerifyBaseBranch)
	if item.Outcome != domain.LadoVerifyPass {
		t.Fatalf("base_branch item of the new head = %s %q, want pass", item.Outcome, item.Code)
	}
	if n := len(f.sender.to(b.SessionID)); n != 1 {
		t.Errorf("messages after the new head = %d, want still 1", n)
	}
	now, _, _ := f.store.LadoExecution(f.ctx, b.SessionID)
	result, err := r.approve(now, "nonce-new-head")
	if err != nil {
		t.Fatalf("approval of the head that took in the base = %v", err)
	}
	if result.Approval.HeadSHA != merged || result.Approval.BaseTipSHA != r.headA {
		t.Errorf("approval of %s against %s, want %s against %s", result.Approval.HeadSHA, result.Approval.BaseTipSHA, merged, r.headA)
	}

	// The old head is no longer the PR's.
	_, err = f.approve(b, strings.Repeat("0", 64), []string{domain.LadoCodeNoTests}, "nonce-old-head")
	requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_HEAD_CHANGED")
}

// #53 with real git: A and B are both approved and A merges. Before any
// pass of the loop, AO's merge of B is refused and B's approval revoked.
func TestMergeGateRevokesBeforeTheLoopSeesTheMerge(t *testing.T) {
	r := newOverlapRepo(t)
	f := r.f
	a := r.add("1", r.headA, r.workA)
	b := r.add("2", r.headB, r.workB)
	approved := map[domain.SessionID]ApproveResult{}
	for i, exec := range []domain.LadoExecution{a, b} {
		result, err := r.approve(exec, "nonce-"+string(rune('a'+i)))
		if err != nil {
			t.Fatalf("approval of %s = %v", exec.SessionID, err)
		}
		approved[exec.SessionID] = result
	}
	pr := func(exec domain.LadoExecution) domain.PullRequest {
		return domain.PullRequest{URL: exec.PR.URL, SessionID: exec.SessionID, Number: exec.PR.Number, HeadSHA: exec.PR.HeadSHA, TargetBranch: "main"}
	}
	if err := f.svc.AuthorizeMerge(f.ctx, pr(a), r.headA); err != nil {
		t.Fatalf("AuthorizeMerge of A = %v", err)
	}
	r.mergeA(a)

	requireAPIError(t, f.svc.AuthorizeMerge(f.ctx, pr(b), r.headB), apierr.KindConflict, "LADO_INTEGRATION_REVOKED")
	revoked := f.store.approval(approved[b.SessionID].Approval.ID)
	if !revoked.Revoked() || revoked.RevokedReason != domain.LadoCodeBaseOverlap {
		t.Errorf("B's approval = revoked %v for %q, want revoked for base_overlap", revoked.Revoked(), revoked.RevokedReason)
	}
	if a := f.store.approval(approved[a.SessionID].Approval.ID); a.Revoked() {
		t.Error("A's approval was revoked")
	}
	_, err := r.approve(b, "nonce-again")
	requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_REVOKED")
}
