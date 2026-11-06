package store_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// mustObserveLadoPR writes PR #number of session at head as AO's SCM observer
// does, updated at at, open or merged; a merged PR carries the host's merge
// time mergedAt when it is not zero. It returns the PR as execution reads
// carry it.
func mustObserveLadoPR(t *testing.T, s *sqlite.Store, session domain.SessionID, number int, merged bool, head string, at, mergedAt time.Time) domain.LadoExecutionPR {
	t.Helper()
	pr := domain.LadoExecutionPR{
		URL: fmt.Sprintf("https://github.com/acme/demo/pull/%d", number), Number: number, Merged: merged,
		HeadSHA: head, TargetBranch: "main", Title: fmt.Sprintf("Change %d", number), Mergeability: domain.MergeUnknown,
	}
	observed := domain.PullRequest{
		URL: pr.URL, SessionID: session, Number: number, Merged: merged,
		HeadSHA: head, TargetBranch: pr.TargetBranch, Title: pr.Title, UpdatedAt: at,
	}
	if merged {
		observed.MergedAtProvider = mergedAt
	}
	if err := s.WriteSCMObservation(context.Background(), observed, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write PR %d: %v", number, err)
	}
	return pr
}

// A revoked approval counts as the merged head's once the PR merged at that
// head and LADO revoked it for a base overlap no earlier than the host's
// merge time (#75): LADO saw the PR's own merge as a base move. Settling
// audits execution.merged_without_approval by MergedHeadApproved, and the
// task reads completed without a reason. A revocation before the merge, a
// merge without the host's merge time and an open PR's revocation still do
// not count.
func TestLadoApprovalRevokedAfterTheMergeCountsForTheMergedHead(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	mergedAt := at.Add(2 * time.Hour)
	type selfMergeCase struct {
		name   string
		task   domain.LadoTaskID
		exec   domain.LadoExecution
		state  domain.LadoTaskState
		reason string
	}
	overlapItem := mustLadoVerifyItem(t, domain.LadoVerifyBaseBranch, domain.LadoVerifyFail, domain.LadoCodeBaseOverlap, domain.LadoBaseBranchDetails{
		Branch: "main", Remote: "origin", Advanced: true, AdvancedTip: ladoHead('f'), Overlap: []string{"src/login.go"}, OverlapCount: 1,
	})
	overlapped := func(head byte) domain.LadoExecutionVerification {
		return domain.LadoExecutionVerification{HeadSHA: ladoHead(head), Outcome: domain.LadoVerifyFail, Reason: domain.LadoCodeBaseOverlap, BaseOverlap: true}
	}
	// run approves PR #number of a new execution at head, revokes the
	// approval for reason at revokedAt with the head's record turned
	// base_overlap, as the loop does, and then observes the PR, merged at
	// the host's merge time hostMergedAt when merged.
	run := func(name, key string, number int, head byte, reason string, revokedAt time.Time, merged bool, hostMergedAt time.Time) *selfMergeCase {
		t.Helper()
		exec := mustLadoExecution(t, s, key, at.Add(time.Duration(number)*time.Minute), merged)
		exec.SessionTerminated = merged
		pr := mustObserveLadoPR(t, s, exec.SessionID, number, false, ladoHead(head), at.Add(time.Hour), time.Time{})
		record := ladoVerification(t, exec.SessionID, pr.URL, ladoHead(head), at.Add(time.Hour))
		mustUpsertLadoVerification(t, s, record)
		approval := ladoIntegrationApproval(t, "approval-"+key, record, domain.LadoIntegrationStatusPosted, at.Add(time.Hour+time.Minute))
		mustApproveLadoIntegration(t, s, approval)
		mustUpsertLadoVerification(t, s, ladoVerification(t, exec.SessionID, pr.URL, ladoHead(head), revokedAt, overlapItem))
		event := domain.LadoAuditEvent{At: revokedAt, Actor: "lado", Action: domain.LadoAuditIntegrationRevoked, SubjectKind: "execution", SubjectID: string(exec.SessionID)}
		if revoked, err := s.RevokeLadoIntegrationApproval(ctx, approval.ID, reason, revokedAt, event); err != nil || !revoked {
			t.Fatalf("%s: revoke = (%v, %v)", name, revoked, err)
		}
		if merged {
			pr = mustObserveLadoPR(t, s, exec.SessionID, number, true, ladoHead(head), at.Add(3*time.Hour), hostMergedAt)
		}
		exec.PR = pr
		exec.Verification = overlapped(head)
		return &selfMergeCase{name: name, task: domain.LadoTaskID("lado-task-" + key), exec: exec}
	}

	// LADO revoked the approval 15 seconds after the host merged the PR.
	afterMerge := run("revoked after the merge", "1", 51, 'a', domain.LadoCodeBaseOverlap, mergedAt.Add(15*time.Second), true, mergedAt)
	afterMerge.exec.Integration = domain.LadoExecutionIntegration{HeadApproved: true, StatusState: domain.LadoIntegrationStatusPending, MergedHeadApproved: true}
	afterMerge.state = domain.LadoTaskCompleted
	// A sibling's base overlap revoked it at the very merge time.
	siblingAtMerge := run("sibling revoked at the merge", "2", 52, 'b', domain.LadoIntegrationSiblingRevoked, mergedAt, true, mergedAt)
	siblingAtMerge.exec.Integration = domain.LadoExecutionIntegration{HeadApproved: true, StatusState: domain.LadoIntegrationStatusPending, MergedHeadApproved: true}
	siblingAtMerge.state = domain.LadoTaskCompleted
	// LADO revoked it a minute before the host merged the PR.
	beforeMerge := run("revoked before the merge", "3", 53, 'c', domain.LadoCodeBaseOverlap, mergedAt.Add(-time.Minute), true, mergedAt)
	beforeMerge.state, beforeMerge.reason = domain.LadoTaskCompleted, domain.LadoCompletedWithoutApproval
	// AO has no host merge time for the PR.
	noMergeTime := run("no host merge time", "4", 54, 'd', domain.LadoCodeBaseOverlap, mergedAt.Add(15*time.Second), true, time.Time{})
	noMergeTime.state, noMergeTime.reason = domain.LadoTaskCompleted, domain.LadoCompletedWithoutApproval
	// The PR is still open: its revoked approval does not hold.
	open := run("open PR", "5", 55, 'e', domain.LadoCodeBaseOverlap, mergedAt.Add(15*time.Second), false, time.Time{})
	open.state, open.reason = domain.LadoTaskValidating, domain.LadoCodeBaseOverlap
	cases := []*selfMergeCase{afterMerge, siblingAtMerge, beforeMerge, noMergeTime, open}

	listed, err := s.ListLadoExecutionsToVerify(ctx, 50)
	if err != nil {
		t.Fatalf("ListLadoExecutionsToVerify: %v", err)
	}
	for _, c := range cases {
		var got *domain.LadoExecution
		for i := range listed {
			if listed[i].SessionID == c.exec.SessionID {
				got = &listed[i]
			}
		}
		if got == nil {
			t.Fatalf("%s: ListLadoExecutionsToVerify left out %s", c.name, c.exec.SessionID)
		}
		if !reflect.DeepEqual(*got, c.exec) {
			t.Fatalf("%s: listed execution = %+v, want %+v", c.name, *got, c.exec)
		}
		task, ok, err := s.LadoTask(ctx, c.task)
		if err != nil || !ok {
			t.Fatalf("%s: LadoTask %s = (%v, %v)", c.name, c.task, ok, err)
		}
		if !reflect.DeepEqual(task.Execution, c.exec) {
			t.Fatalf("%s: task execution = %+v, want %+v", c.name, task.Execution, c.exec)
		}
		if task.State() != c.state || task.StateReason() != c.reason {
			t.Fatalf("%s: state = %q (%s), want %q (%s)", c.name, task.State(), task.StateReason(), c.state, c.reason)
		}
	}

	// The rule only reads: the approval rows stay revoked.
	for _, c := range cases {
		if _, ok := ladoApprovalOf(t, s, c.exec.SessionID, c.exec.PR.URL, c.exec.PR.HeadSHA); ok {
			t.Fatalf("%s: the revoked approval holds again", c.name)
		}
	}
}
