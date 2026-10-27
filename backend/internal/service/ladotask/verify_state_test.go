package ladotask

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// verifyRecord is the record of head on prURL: every item passes, evidence
// is info, and the items given replace those of their kinds.
func verifyRecord(t *testing.T, session domain.SessionID, prURL, head string, at time.Time, items ...domain.LadoVerifyItem) domain.LadoVerification {
	t.Helper()
	v := domain.LadoVerification{ExecutionSessionID: session, HeadSHA: head, PRURL: prURL, ComputedAt: at}
	for _, kind := range domain.LadoVerifyKinds() {
		outcome := domain.LadoVerifyPass
		if kind == domain.LadoVerifyEvidence {
			outcome = domain.LadoVerifyInfo
		}
		item := verifyItem(t, kind, outcome, "")
		for _, given := range items {
			if given.Kind == kind {
				item = given
			}
		}
		v.Items = append(v.Items, item)
	}
	return v
}

func verifyItem(t *testing.T, kind domain.LadoVerifyKind, outcome domain.LadoVerifyOutcome, code string) domain.LadoVerifyItem {
	t.Helper()
	item, err := domain.NewLadoVerifyItem(kind, outcome, code, nil)
	if err != nil {
		t.Fatalf("NewLadoVerifyItem %s: %v", kind, err)
	}
	return item
}

// stateReasons returns the task's state and reason as the task list, the
// task, the task view and the execution view, which reads the task from the
// store, show them.
func (pf *planFixture) stateReasons(t *testing.T) map[string]string {
	t.Helper()
	ctx := context.Background()
	tasks, err := pf.svc.List(ctx, "demo", false)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("List = (%d tasks, %v), want the task", len(tasks), err)
	}
	task, err := pf.svc.Get(ctx, pf.task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	view, err := pf.svc.View(ctx, pf.task.ID)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	execution, ok, err := pf.store.LadoTask(ctx, pf.task.ID)
	if err != nil || !ok {
		t.Fatalf("LadoTask = %v, %v", ok, err)
	}
	show := func(task domain.LadoTask) string { return string(task.State()) + "/" + task.StateReason() }
	return map[string]string{"List": show(tasks[0]), "Get": show(task), "View": show(view.Task), "Execution": show(execution)}
}

func (pf *planFixture) wantStateReason(t *testing.T, state domain.LadoTaskState, reason string) {
	t.Helper()
	want := string(state) + "/" + reason
	for read, got := range pf.stateReasons(t) {
		if got != want {
			t.Fatalf("%s shows the task as %q, want %q", read, got, want)
		}
	}
}

// writeHeadPR writes PR number of session at head as AO's SCM observer does.
func (pf *planFixture) writeHeadPR(t *testing.T, session domain.SessionID, number int, head string, merged bool) string {
	t.Helper()
	url := fmt.Sprintf("https://github.com/acme/demo/pull/%d", number)
	if err := pf.store.WriteSCMObservation(context.Background(), domain.PullRequest{
		URL: url, SessionID: session, Number: number, Merged: merged, HeadSHA: head, TargetBranch: "main", Title: "로그인을 더한다", UpdatedAt: pf.now,
	}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write PR %d: %v", number, err)
	}
	return url
}

// A dispatched task's state follows the record LADO stores for its PR's
// current head through every read: Validating while the record waits (with
// its reason) or is missing, Integration pending once it passes,
// Integrating once the head is approved, and Completed once the PR merges,
// which a merge of a head nobody approved marks.
func TestTaskStateFollowsVerificationThroughReads(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	rev := pf.planned(t)
	if _, err := pf.svc.ApprovePlan(ctx, rev.ID, rev.SHA256, policyA, pf.proof(rev.ID)); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	session := pf.run(t, pf.claim(t))
	head := strings.Repeat("a", 40)
	prURL := pf.writeHeadPR(t, session, 8, head, false)
	pf.wantStateReason(t, domain.LadoTaskValidating, domain.LadoValidatingVerifying)

	upsert := func(v domain.LadoVerification) {
		t.Helper()
		if _, err := pf.store.UpsertLadoVerification(ctx, v); err != nil {
			t.Fatalf("UpsertLadoVerification: %v", err)
		}
	}
	upsert(verifyRecord(t, session, prURL, head, pf.now,
		verifyItem(t, domain.LadoVerifyRequiredChecks, domain.LadoVerifyWait, domain.LadoCodeChecksPending)))
	pf.wantStateReason(t, domain.LadoTaskValidating, domain.LadoCodeChecksPending)
	exec, ok, err := pf.store.LadoExecution(ctx, session)
	if err != nil || !ok || exec.Verification.HeadSHA != head || exec.Verification.Outcome != domain.LadoVerifyWait || exec.Verification.Reason != domain.LadoCodeChecksPending {
		t.Fatalf("execution read = %+v, %v, %v; want the wait on %s", exec.Verification, ok, err, head)
	}

	pf.now = pf.now.Add(time.Minute)
	passed := verifyRecord(t, session, prURL, head, pf.now)
	upsert(passed)
	pf.wantStateReason(t, domain.LadoTaskIntegrationPending, "")

	sum, err := passed.SHA256()
	if err != nil {
		t.Fatalf("SHA256: %v", err)
	}
	approval := domain.LadoIntegrationApproval{
		ID: "integration-1", ExecutionSessionID: session, PRURL: prURL, HeadSHA: head, VerificationSHA256: sum, Acknowledged: []string{},
		Actor: "human", ApprovedAt: pf.now, StatusState: domain.LadoIntegrationStatusPending,
	}
	proof := domain.LadoPresenceProof{
		Nonce: "nonce-integration-1", Action: domain.LadoPresenceIntegrationApprove, SubjectID: string(session) + "@" + head,
		ExpiresAt: pf.now.Add(time.Minute), UsedAt: pf.now,
	}
	event := domain.LadoAuditEvent{At: pf.now, Actor: "human", Action: "integration.approved", SubjectKind: "execution", SubjectID: string(session), Payload: json.RawMessage(`{}`)}
	if err := pf.store.ApproveLadoIntegration(ctx, approval, proof, event); err != nil {
		t.Fatalf("ApproveLadoIntegration: %v", err)
	}
	pf.wantStateReason(t, domain.LadoTaskIntegrating, "")
	if err := pf.store.SetLadoIntegrationStatus(ctx, approval.ID, domain.LadoIntegrationStatusFailed, 1, time.Time{}, "the host answered 403"); err != nil {
		t.Fatalf("SetLadoIntegrationStatus: %v", err)
	}
	pf.wantStateReason(t, domain.LadoTaskIntegrating, domain.LadoIntegratingStatusNotPosted)

	pf.writeHeadPR(t, session, 8, head, true)
	pf.wantStateReason(t, domain.LadoTaskCompleted, "")

	// A later PR of the session merged at a head nobody approved.
	pf.now = pf.now.Add(time.Minute)
	pf.writeHeadPR(t, session, 9, strings.Repeat("b", 40), true)
	pf.wantStateReason(t, domain.LadoTaskCompleted, domain.LadoCompletedWithoutApproval)
}
