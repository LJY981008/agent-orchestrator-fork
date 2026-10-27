package ladoverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// PRD gate 10: approving an integration needs the desktop app's
// confirmation, and without one LADO reads and writes nothing. The dry run
// needs none and answers the record, its hash and the codes the confirmation
// shows.
func TestIntegrationApprovalNeedsPresence(t *testing.T) {
	refused := apierr.Forbidden("LADO_PRESENCE_INVALID", "The confirmation could not be verified.")
	refuse := func(string, string) (domain.LadoPresenceProof, error) { return domain.LadoPresenceProof{}, refused }

	t.Run("before anything is read", func(t *testing.T) {
		// Every read would reach a missing dependency.
		svc := New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
		in := ApproveInput{SessionID: "lado-1", PRURL: "https://github.com/acme/demo/pull/1", HeadSHA: verifyHead1, VerificationSHA256: strings.Repeat("0", 64)}
		_, err := svc.ApproveIntegration(context.Background(), in)
		requireAPIError(t, err, apierr.KindForbidden, "LADO_PRESENCE_REQUIRED")

		var asked []string
		in.HeadSHA = strings.ToUpper(verifyHead1)
		in.Presence = func(action, subject string) (domain.LadoPresenceProof, error) {
			asked = append(asked, action+" "+subject)
			return domain.LadoPresenceProof{}, refused
		}
		if _, err := svc.ApproveIntegration(context.Background(), in); !errors.Is(err, refused) {
			t.Fatalf("ApproveIntegration with a refused proof = %v, want the refusal", err)
		}
		if want := []string{"integration.approve lado-1@" + verifyHead1}; !slices.Equal(asked, want) {
			t.Errorf("presence asked for %q, want %q", asked, want)
		}
	})

	f := newApproveFixture(t)
	exec := f.add("1", verifyHead1)
	for _, presence := range []func(string, string) (domain.LadoPresenceProof, error){nil, refuse} {
		_, err := f.svc.ApproveIntegration(f.ctx, ApproveInput{
			SessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1,
			VerificationSHA256: strings.Repeat("0", 64), Acknowledge: []string{domain.LadoCodeNoTests}, Presence: presence,
		})
		if err == nil {
			t.Fatal("ApproveIntegration without a proof = nil error")
		}
	}
	if _, ok := f.store.recordOf(exec.SessionID, verifyHead1); ok {
		t.Error("a refused approval stored a record")
	}
	if n, dirs, rules := f.heads.readCount(), f.git.askedDirs(), f.host.branchReads(); n != 0 || len(dirs) != 0 || len(rules) != 0 {
		t.Errorf("a refused approval read %d live heads, git in %v and the rules of %v; want nothing", n, dirs, rules)
	}
	if approvals, audits := f.store.approvalCount(), f.store.auditEvents(); approvals != 0 || len(audits) != 0 {
		t.Errorf("a refused approval wrote %d approvals and %v", approvals, audits)
	}

	result, err := f.svc.ApproveIntegration(f.ctx, ApproveInput{
		SessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1, DryRun: true,
		Presence: func(string, string) (domain.LadoPresenceProof, error) {
			t.Error("the dry run asked for presence")
			return domain.LadoPresenceProof{}, refused
		},
	})
	if err != nil {
		t.Fatalf("dry run = %v", err)
	}
	stored := f.record(exec.SessionID, verifyHead1)
	if result.SHA256 != verifySHA(t, stored) || result.Verification.HeadSHA != verifyHead1 || len(result.Verification.Items) != len(domain.LadoVerifyKinds()) {
		t.Errorf("dry run = head %s, %d items, hash %s; want the stored record of %s with hash %s",
			result.Verification.HeadSHA, len(result.Verification.Items), result.SHA256, verifyHead1, verifySHA(t, stored))
	}
	if !slices.Equal(result.Confirm, []string{domain.LadoCodeNoTests}) || result.Approval != nil {
		t.Errorf("dry run confirm %v, approval %+v; want [no_tests] and none", result.Confirm, result.Approval)
	}
	if approvals, audits := f.store.approvalCount(), f.store.auditEvents(); approvals != 0 || len(audits) != 0 {
		t.Errorf("the dry run wrote %d approvals and %v", approvals, audits)
	}
}

func TestIntegrationApprovalChecksHeadHashAndAcknowledgements(t *testing.T) {
	f := newApproveFixture(t)
	exec := f.add("1", verifyHead1)
	preview := f.preview(exec)

	t.Run("the head moved", func(t *testing.T) {
		f.heads.set(exec.PR.URL, verifyHead2)
		defer f.heads.set(exec.PR.URL, verifyHead1)
		_, err := f.approve(exec, preview.SHA256, preview.Confirm, "nonce-moved")
		requireDetails(t, err, apierr.KindConflict, "LADO_INTEGRATION_HEAD_CHANGED", map[string]any{"headSha": verifyHead1, "currentHeadSha": verifyHead2})
		_, err = f.svc.ApproveIntegration(f.ctx, ApproveInput{SessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1, DryRun: true})
		requireDetails(t, err, apierr.KindConflict, "LADO_INTEGRATION_HEAD_CHANGED", map[string]any{"headSha": verifyHead1, "currentHeadSha": verifyHead2})
	})

	t.Run("the dry run of a head AO has not observed", func(t *testing.T) {
		f.heads.set(exec.PR.URL, verifyHead3)
		defer f.heads.set(exec.PR.URL, verifyHead1)
		_, err := f.svc.ApproveIntegration(f.ctx, ApproveInput{SessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead3, DryRun: true})
		requireDetails(t, err, apierr.KindConflict, "LADO_INTEGRATION_HEAD_CHANGED", map[string]any{"headSha": verifyHead3, "currentHeadSha": verifyHead1})
	})

	t.Run("another pull request", func(t *testing.T) {
		_, err := f.svc.ApproveIntegration(f.ctx, ApproveInput{SessionID: exec.SessionID, PRURL: "https://github.com/acme/demo/pull/9", HeadSHA: verifyHead1, DryRun: true})
		requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_HEAD_CHANGED")
		_, err = f.svc.ApproveIntegration(f.ctx, ApproveInput{SessionID: "lado-9", PRURL: exec.PR.URL, HeadSHA: verifyHead1, DryRun: true})
		requireAPIError(t, err, apierr.KindNotFound, "LADO_EXECUTION_NOT_FOUND")
	})

	t.Run("another record", func(t *testing.T) {
		_, err := f.approve(exec, strings.Repeat("0", 64), preview.Confirm, "nonce-record")
		requireAPIError(t, err, apierr.KindConflict, "LADO_VERIFICATION_CHANGED")
	})

	t.Run("items that wait or fail", func(t *testing.T) {
		// PRD gate 9's approval half: an English commit fails, and the
		// approval is refused while it fails.
		failing := f.add("2", verifyHead2)
		f.git.setBranch(verifyHead2, []Commit{{SHA: strings.Repeat("cd", 20), Message: "Add the login form"}}, []string{"src/login.go"})
		f.store.setCommitChecks(failing.PR.URL, verifyHead2, domain.PullRequestCheck{Name: "build", CommitHash: verifyHead2, Status: domain.PRCheckInProgress})
		p := f.preview(failing)
		_, err := f.approve(failing, p.SHA256, p.Confirm, "nonce-failing")
		requireDetails(t, err, apierr.KindConflict, "LADO_INTEGRATION_NOT_READY",
			map[string]any{"reasons": []string{domain.LadoCodeChecksPending, domain.LadoCodeCommitMessagesFailed}})
	})

	t.Run("acknowledgements", func(t *testing.T) {
		_, err := f.approve(exec, preview.SHA256, nil, "nonce-ack-1")
		requireDetails(t, err, apierr.KindInvalid, "LADO_INTEGRATION_ACK_MISMATCH",
			map[string]any{"required": []string{domain.LadoCodeNoTests}, "given": []string{}})
		_, err = f.approve(exec, preview.SHA256, []string{domain.LadoCodeNoTests, domain.LadoCodeBaseMoved}, "nonce-ack-2")
		requireDetails(t, err, apierr.KindInvalid, "LADO_INTEGRATION_ACK_MISMATCH",
			map[string]any{"required": []string{domain.LadoCodeNoTests}, "given": []string{domain.LadoCodeBaseMoved, domain.LadoCodeNoTests}})
		if n := f.store.approvalCount(); n != 0 {
			t.Fatalf("refused approvals recorded %d approvals", n)
		}
		// The codes are a set.
		if _, err := f.approve(exec, preview.SHA256, []string{domain.LadoCodeNoTests, domain.LadoCodeNoTests}, "nonce-ack-3"); err != nil {
			t.Fatalf("approval = %v", err)
		}
	})

	t.Run("approved already", func(t *testing.T) {
		_, err := f.approve(exec, preview.SHA256, preview.Confirm, "nonce-again")
		requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_ALREADY_APPROVED")
		if n := f.store.approvalCount(); n != 1 {
			t.Errorf("approvals = %d, want 1", n)
		}
	})
}

func TestIntegrationApprovalRecordsAndPostsStatus(t *testing.T) {
	f := newApproveFixture(t)
	gh := f.add("1", verifyHead1)
	dir := f.reads.project("p1").Path
	preview := f.preview(gh)
	approvedOps := len(f.git.opsOf(dir))

	result, err := f.approve(gh, preview.SHA256, preview.Confirm, "nonce-github")
	if err != nil {
		t.Fatalf("approval = %v", err)
	}
	now := f.clock.Now()
	if result.Approval == nil || !strings.HasPrefix(result.Approval.ID, "lado-integration-") {
		t.Fatalf("approval = %+v, want one", result.Approval)
	}
	want := domain.LadoIntegrationApproval{
		ID: result.Approval.ID, ExecutionSessionID: gh.SessionID, PRURL: gh.PR.URL, HeadSHA: verifyHead1,
		VerificationSHA256: preview.SHA256, Acknowledged: []string{domain.LadoCodeNoTests}, ProofNonce: "nonce-github", Actor: "human",
		ApprovedAt: now, StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 1, StatusPostedAt: now,
	}
	if !reflect.DeepEqual(*result.Approval, want) {
		t.Errorf("approval = %+v\nwant %+v", *result.Approval, want)
	}
	if stored := f.store.approval(want.ID); !reflect.DeepEqual(stored, want) {
		t.Errorf("stored approval = %+v\nwant %+v", stored, want)
	}
	if result.SHA256 != preview.SHA256 || !slices.Equal(result.Confirm, preview.Confirm) {
		t.Errorf("result hash %s confirm %v, want the dry run's %s %v", result.SHA256, result.Confirm, preview.SHA256, preview.Confirm)
	}
	posted := verifyPost{repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}, sha: verifyHead1, status: ports.LadoCommitStatus{State: "success", Context: "lado/integration", Description: "Approved in LADO"}}
	if posts := f.host.postsMade(); len(posts) != 1 || posts[0] != posted {
		t.Errorf("posts = %+v, want %+v", posts, posted)
	}
	audits := f.store.auditEvents()
	if len(audits) != 1 {
		t.Fatalf("audit events = %+v, want one", audits)
	}
	if a := audits[0]; a.Actor != "human" || a.Action != "integration.approved" || a.SubjectKind != "execution" || a.SubjectID != string(gh.SessionID) || !a.At.Equal(now) {
		t.Errorf("audit = %+v", a)
	}
	var payload map[string]any
	if err := json.Unmarshal(audits[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	wantPayload := map[string]any{
		"task": "task-1", "session": "lado-1", "pr": gh.PR.URL, "head": verifyHead1, "verification": preview.SHA256,
		"acknowledged": []any{domain.LadoCodeNoTests}, "proof": "nonce-github", "dev": false,
	}
	if !reflect.DeepEqual(payload, wantPayload) {
		t.Errorf("audit payload = %v\nwant %v", payload, wantPayload)
	}
	select {
	case <-f.svc.kick:
	default:
		t.Error("the approval kicked no pass")
	}
	// The dry run and the approval each fetch the base branch before git
	// reads anything, and read the branch's rules past the cache.
	ops := f.git.opsOf(dir)
	if len(ops) <= approvedOps || ops[0] != "fetch origin main" || ops[approvedOps] != "fetch origin main" {
		t.Errorf("git operations = %v, want each request to start with the fetch of origin main", ops)
	}
	if reads := f.host.branchReads(); len(reads) != 2 {
		t.Errorf("rule reads = %v, want one per request", reads)
	}

	t.Run("a post that fails", func(t *testing.T) {
		exec := f.add("2", verifyHead3)
		f.host.setPostErr(errors.New("github tracker: post the lado/integration status: the host answered 403: Resource not accessible by integration\nretry later"))
		defer f.host.setPostErr(nil)
		p := f.preview(exec)
		result, err := f.approve(exec, p.SHA256, p.Confirm, "nonce-failing")
		if err != nil {
			t.Fatalf("approval with a failing post = %v, want the approval", err)
		}
		a := result.Approval
		if a.StatusState != domain.LadoIntegrationStatusFailed || a.StatusAttempts != 1 || !a.StatusPostedAt.IsZero() || !strings.Contains(a.StatusError, "403") || strings.Contains(a.StatusError, "\n") {
			t.Errorf("approval = %s after %d attempts (%q), want failed after one with one line of the error", a.StatusState, a.StatusAttempts, a.StatusError)
		}
		if stored := f.store.approval(a.ID); stored.StatusState != domain.LadoIntegrationStatusFailed || stored.HeadSHA != verifyHead3 {
			t.Errorf("stored approval = %+v, want it kept as failed", stored)
		}
	})

	t.Run("GitLab", func(t *testing.T) {
		exec := f.add("3", verifyHead4)
		f.reads.updateTask(exec.TaskID, func(task *domain.LadoTask) {
			task.Repo = domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "acme/demo"}
		})
		before := len(f.host.postsMade())
		p := f.preview(exec)
		if !slices.Contains(p.Confirm, domain.LadoCodeUnprotectedGitLab) {
			t.Errorf("GitLab confirm codes = %v, want unprotected_gitlab among them", p.Confirm)
		}
		result, err := f.svc.ApproveIntegration(f.ctx, ApproveInput{
			SessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead4, VerificationSHA256: p.SHA256,
			Acknowledge: p.Confirm, Presence: presenceProof("nonce-gitlab", true),
		})
		if err != nil {
			t.Fatalf("approval = %v", err)
		}
		if a := result.Approval; a.StatusState != domain.LadoIntegrationStatusNotApplicable || a.StatusAttempts != 0 || a.Actor != "human:dev-presence" {
			t.Errorf("GitLab approval = %s after %d attempts by %s, want not_applicable by human:dev-presence", a.StatusState, a.StatusAttempts, a.Actor)
		}
		if n := len(f.host.postsMade()); n != before {
			t.Errorf("posts = %d, want still %d", n, before)
		}
	})
}

func TestIntegrationApprovalRefusesReplayedProof(t *testing.T) {
	f := newApproveFixture(t)
	one := f.add("1", verifyHead1)
	two := f.add("2", verifyHead3)
	dir := f.reads.project("p1").Path
	p1, p2 := f.preview(one), f.preview(two)
	if _, err := f.approve(one, p1.SHA256, p1.Confirm, "nonce-once"); err != nil {
		t.Fatalf("first approval = %v", err)
	}
	upserts, reads, ops := len(f.store.changedOf(two.SessionID, verifyHead3)), f.heads.readCount(), len(f.git.opsOf(dir))

	_, err := f.approve(two, p2.SHA256, p2.Confirm, "nonce-once")
	requireAPIError(t, err, apierr.KindForbidden, "LADO_PRESENCE_REPLAYED")
	if n := f.store.approvalCount(); n != 1 {
		t.Errorf("approvals = %d, want the first only", n)
	}
	if n := len(f.store.auditEvents()); n != 1 {
		t.Errorf("audit events = %d, want the first only", n)
	}
	if got := len(f.store.changedOf(two.SessionID, verifyHead3)); got != upserts {
		t.Errorf("the replayed proof stored the record again (%d upserts, want %d)", got, upserts)
	}
	if got, gotOps := f.heads.readCount(), len(f.git.opsOf(dir)); got != reads || gotOps != ops {
		t.Errorf("the replayed proof read %d live heads and ran %d git operations, want none", got-reads, gotOps-ops)
	}

	t.Run("stored in the meantime", func(t *testing.T) {
		// Another request stored the proof after this one checked it.
		g := newApproveFixture(t, func(d *Deps) { d.Store = proofBlindStore{d.Store.(*verifyStore)} })
		exec := g.add("1", verifyHead1)
		g.store.mu.Lock()
		g.store.proofs = append(g.store.proofs, domain.LadoPresenceProof{Nonce: "nonce-raced"})
		g.store.mu.Unlock()
		p := g.preview(exec)
		_, err := g.approve(exec, p.SHA256, p.Confirm, "nonce-raced")
		requireAPIError(t, err, apierr.KindForbidden, "LADO_PRESENCE_REPLAYED")
		if n, audits := g.store.approvalCount(), g.store.auditEvents(); n != 0 || len(audits) != 0 {
			t.Errorf("a replayed proof wrote %d approvals and %v", n, audits)
		}
	})
}

// PRD gate 8, its second half: once an agent moved the local base branch
// from its worktree, the approval must acknowledge base_moved.
func TestApprovalNeedsTheBaseMoveAcknowledged(t *testing.T) {
	r := newTestRepo(t, t0.Add(-time.Hour))
	work := r.worktree(t0.Add(time.Minute), "lado/work")
	r.write(work, "src/login.go", "package login\n")
	head := r.commit(t0.Add(2*time.Minute), work, "fix: 로그인 시간 초과를 고친다")
	moved := t0.Add(3 * time.Minute)
	r.git(moved, work, "update-ref", "refs/heads/main", "HEAD")

	f := newApproveFixture(t, func(d *Deps) { d.Git = ExecGit{} })
	f.reads.setProject(domain.ProjectRecord{ID: "p1", Path: r.dir}, nil)
	exec := f.add("1", head)

	preview := f.preview(exec)
	if want := []string{domain.LadoCodeBaseMoved, domain.LadoCodeNoTests}; !slices.Equal(preview.Confirm, want) {
		t.Fatalf("confirm codes = %v, want %v (items %+v)", preview.Confirm, want, preview.Verification.Items)
	}
	var base domain.LadoBaseBranchDetails
	if err := json.Unmarshal(verifyItemOf(t, preview.Verification, domain.LadoVerifyBaseBranch).Details, &base); err != nil {
		t.Fatal(err)
	}
	if want := []domain.LadoReflogEntry{{Selector: fmt.Sprintf("main@{%d}", moved.Unix()), Commit: head}}; !reflect.DeepEqual(base.Moved, want) {
		t.Errorf("moved = %+v, want %+v", base.Moved, want)
	}

	_, err := f.approve(exec, preview.SHA256, []string{domain.LadoCodeNoTests}, "nonce-base-1")
	requireDetails(t, err, apierr.KindInvalid, "LADO_INTEGRATION_ACK_MISMATCH", map[string]any{
		"required": []string{domain.LadoCodeBaseMoved, domain.LadoCodeNoTests}, "given": []string{domain.LadoCodeNoTests},
	})
	if n := f.store.approvalCount(); n != 0 {
		t.Fatalf("approvals = %d, want none", n)
	}
	result, err := f.approve(exec, preview.SHA256, []string{domain.LadoCodeNoTests, domain.LadoCodeBaseMoved}, "nonce-base-2")
	if err != nil {
		t.Fatalf("approval acknowledging base_moved = %v", err)
	}
	if got := result.Approval.Acknowledged; !slices.Equal(got, preview.Confirm) {
		t.Errorf("acknowledged = %v, want %v", got, preview.Confirm)
	}
}

// approveFixture is the loop's fixture whose PRs' host answers the live
// heads integration approvals read.
type approveFixture struct {
	*verifyFixture
	heads *approveHeads
}

func newApproveFixture(t *testing.T, adjust ...func(*Deps)) *approveFixture {
	t.Helper()
	f := &approveFixture{verifyFixture: newVerifyFixture(t, adjust...), heads: &approveHeads{}}
	f.svc.SetLiveHeads(f.heads)
	return f
}

// add adds an execution at head, which its PR has on the host too.
func (f *approveFixture) add(key, head string) domain.LadoExecution {
	f.t.Helper()
	exec := f.addExecution(key, head)
	f.heads.set(exec.PR.URL, head)
	return exec
}

// preview is the dry run of approving exec's head.
func (f *approveFixture) preview(exec domain.LadoExecution) ApproveResult {
	f.t.Helper()
	result, err := f.svc.ApproveIntegration(f.ctx, ApproveInput{SessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: exec.PR.HeadSHA, DryRun: true})
	if err != nil {
		f.t.Fatalf("dry run of %s = %v", exec.SessionID, err)
	}
	return result
}

// approve approves exec's head under a proof with nonce.
func (f *approveFixture) approve(exec domain.LadoExecution, sum string, acknowledge []string, nonce string) (ApproveResult, error) {
	return f.svc.ApproveIntegration(f.ctx, ApproveInput{
		SessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: exec.PR.HeadSHA,
		VerificationSHA256: sum, Acknowledge: acknowledge, Presence: presenceProof(nonce, false),
	})
}

// presenceProof stands in for a verified proof of the desktop app with nonce.
func presenceProof(nonce string, dev bool) func(action, subject string) (domain.LadoPresenceProof, error) {
	return func(action, subject string) (domain.LadoPresenceProof, error) {
		return domain.LadoPresenceProof{Nonce: nonce, Action: action, SubjectID: subject, Dev: dev, ExpiresAt: t0.Add(2 * time.Hour), UsedAt: t0.Add(time.Hour)}, nil
	}
}

// requireAPIError fails unless err is an *apierr.Error of kind and code.
func requireAPIError(t *testing.T, err error, kind apierr.Kind, code string) *apierr.Error {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != kind || apiErr.Code != code {
		t.Fatalf("error = %v (%#v), want %s of kind %d", err, apiErr, code, kind)
	}
	return apiErr
}

// requireDetails is requireAPIError that compares the details too.
func requireDetails(t *testing.T, err error, kind apierr.Kind, code string, details map[string]any) {
	t.Helper()
	if got := requireAPIError(t, err, kind, code).Details; !reflect.DeepEqual(got, details) {
		t.Errorf("%s details = %#v, want %#v", code, got, details)
	}
}

// approveHeads is the PRs' host as integration approvals read it.
type approveHeads struct {
	mu    sync.Mutex
	heads map[string]string
	reads int
}

func (h *approveHeads) set(prURL, head string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.heads == nil {
		h.heads = map[string]string{}
	}
	h.heads[prURL] = head
}

func (h *approveHeads) readCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reads
}

func (h *approveHeads) LiveHead(_ context.Context, pr domain.PullRequest) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reads++
	head, ok := h.heads[pr.URL]
	if !ok {
		return "", fmt.Errorf("no pull request %s on the host", pr.URL)
	}
	return head, nil
}

// proofBlindStore misses the proofs stored, as when another request stores
// the same proof after this one checked it.
type proofBlindStore struct {
	*verifyStore
}

func (proofBlindStore) LadoPresenceProofUsed(context.Context, string) (bool, error) {
	return false, nil
}

// The integration approval and merge gate parts of verifyStore keep the
// store's rules: an approval stores its proof once, one approval per head,
// and only of the head's stored record.

func (s *verifyStore) LadoPresenceProofUsed(_ context.Context, nonce string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.proofs, func(p domain.LadoPresenceProof) bool { return p.Nonce == nonce }), nil
}

func (s *verifyStore) ApproveLadoIntegration(_ context.Context, a domain.LadoIntegrationApproval, proof domain.LadoPresenceProof, audit domain.LadoAuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.ContainsFunc(s.proofs, func(p domain.LadoPresenceProof) bool { return p.Nonce == proof.Nonce }) {
		return domain.ErrLadoPresenceReplayed
	}
	if slices.ContainsFunc(s.approvals, func(x domain.LadoIntegrationApproval) bool {
		return x.ExecutionSessionID == a.ExecutionSessionID && x.HeadSHA == a.HeadSHA
	}) {
		return domain.ErrLadoIntegrationAlreadyApproved
	}
	record, ok := s.records[verifyKey(a.ExecutionSessionID, a.HeadSHA)]
	if !ok {
		return domain.ErrLadoVerificationChanged
	}
	if sum, err := record.SHA256(); err != nil || sum != a.VerificationSHA256 {
		return domain.ErrLadoVerificationChanged
	}
	a.ProofNonce, a.Acknowledged = proof.Nonce, slices.Clone(a.Acknowledged)
	s.proofs = append(s.proofs, proof)
	s.approvals = append(s.approvals, a)
	s.audits = append(s.audits, audit)
	return nil
}

func (s *verifyStore) GetLadoIntegrationApproval(_ context.Context, session domain.SessionID, head string) (domain.LadoIntegrationApproval, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.approvals {
		if a.ExecutionSessionID == session && a.HeadSHA == head {
			return a, true, nil
		}
	}
	return domain.LadoIntegrationApproval{}, false, nil
}

func (s *verifyStore) LadoProjectSettings(_ context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return domain.LadoProjectSettings{ProjectID: projectID, LadoMode: s.ladoModes[projectID]}, nil
}

func (s *verifyStore) setLadoMode(projectID domain.ProjectID, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ladoModes == nil {
		s.ladoModes = map[domain.ProjectID]bool{}
	}
	s.ladoModes[projectID] = on
}

func (s *verifyStore) approvalCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.approvals)
}

func (r *verifyReads) addSession(session domain.SessionRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[session.ID] = session
}
