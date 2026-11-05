package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLadoExecutionConflictReasonsComeBeforeIntegrating(t *testing.T) {
	head, older := strings.Repeat("a", 40), strings.Repeat("b", 40)
	openPR := domain.LadoExecutionPR{URL: "https://github.com/acme/demo/pull/8", Number: 8, HeadSHA: head, TargetBranch: "main", Mergeability: domain.MergeMergeable}
	conflicting := openPR
	conflicting.Mergeability = domain.MergeConflicting
	passed := domain.LadoExecutionVerification{HeadSHA: head, Outcome: domain.LadoVerifyPass}
	overlap := domain.LadoExecutionVerification{HeadSHA: head, Outcome: domain.LadoVerifyFail, Reason: domain.LadoCodeBaseOverlap, BaseOverlap: true}
	// Another item fails first: the record's reason is that item's, and the
	// base still overlaps.
	overlapBehind := domain.LadoExecutionVerification{HeadSHA: head, Outcome: domain.LadoVerifyFail, Reason: domain.LadoCodeCommandsFailed, BaseOverlap: true}
	overlapOlder := overlap
	overlapOlder.HeadSHA = older
	approved := domain.LadoExecutionIntegration{HeadApproved: true, StatusState: domain.LadoIntegrationStatusPosted}
	resolving := domain.LadoExecutionResolve{State: domain.LadoResolveResolving, Attempts: 1, SessionID: "demo-9"}
	needsHuman := domain.LadoExecutionResolve{State: domain.LadoResolveNeedsHuman, Attempts: domain.LadoMaxResolveAttempts}
	for _, tc := range []struct {
		name         string
		pr           domain.LadoExecutionPR
		verification domain.LadoExecutionVerification
		integration  domain.LadoExecutionIntegration
		resolve      domain.LadoExecutionResolve
		state        domain.LadoTaskState
		reason       string
	}{
		{"conflict", conflicting, passed, domain.LadoExecutionIntegration{}, domain.LadoExecutionResolve{}, domain.LadoTaskValidating, domain.LadoValidatingMergeConflict},
		{"conflict on an approved head", conflicting, passed, approved, domain.LadoExecutionResolve{}, domain.LadoTaskValidating, domain.LadoValidatingMergeConflict},
		{"base overlap", openPR, overlap, domain.LadoExecutionIntegration{}, domain.LadoExecutionResolve{}, domain.LadoTaskValidating, domain.LadoCodeBaseOverlap},
		{"base overlap on an approved head", openPR, overlap, approved, domain.LadoExecutionResolve{}, domain.LadoTaskValidating, domain.LadoCodeBaseOverlap},
		{"base overlap behind another failure", openPR, overlapBehind, domain.LadoExecutionIntegration{}, domain.LadoExecutionResolve{}, domain.LadoTaskValidating, domain.LadoCodeBaseOverlap},
		{"base overlap of an older head", openPR, overlapOlder, approved, domain.LadoExecutionResolve{}, domain.LadoTaskIntegrating, ""},
		{"resolving", conflicting, passed, approved, resolving, domain.LadoTaskValidating, domain.LadoValidatingResolving},
		{"resolving pushed, the PR merges again", openPR, passed, domain.LadoExecutionIntegration{}, resolving, domain.LadoTaskValidating, domain.LadoValidatingResolving},
		{"resolving a base overlap", openPR, overlap, approved, resolving, domain.LadoTaskValidating, domain.LadoValidatingResolving},
		{"unresolved conflict", conflicting, passed, approved, needsHuman, domain.LadoTaskValidating, domain.LadoValidatingConflictUnresolved},
		{"unresolved base overlap", openPR, overlap, domain.LadoExecutionIntegration{}, needsHuman, domain.LadoTaskValidating, domain.LadoValidatingConflictUnresolved},
		// A person resolved it: the head decides again.
		{"unresolved, then resolved by hand", openPR, passed, domain.LadoExecutionIntegration{}, needsHuman, domain.LadoTaskIntegrationPending, ""},
		{"mergeable approved head", openPR, passed, approved, domain.LadoExecutionResolve{}, domain.LadoTaskIntegrating, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := domain.LadoExecution{SessionID: "demo-7", PR: tc.pr, Verification: tc.verification, Integration: tc.integration, Resolve: tc.resolve}
			if state, reason := e.State(); state != tc.state || reason != tc.reason {
				t.Fatalf("State = %q, %q; want %q, %q", state, reason, tc.state, tc.reason)
			}
		})
	}
	// A merged or closed PR is settled whatever its mergeability or resolve.
	merged := conflicting
	merged.Merged = true
	e := domain.LadoExecution{SessionID: "demo-7", PR: merged, Resolve: needsHuman, Integration: domain.LadoExecutionIntegration{MergedHeadApproved: true}}
	if state, _ := e.State(); state != domain.LadoTaskCompleted {
		t.Fatalf("merged PR state = %q", state)
	}
}

func TestLadoResolveClientRequestID(t *testing.T) {
	id := domain.LadoResolveClientRequestID("lado-queue-1", 1)
	if id != "lado:lado-queue-1/_resolve/1" {
		t.Fatalf("resolve id = %s", id)
	}
	r, ok := domain.ParseLadoRunClientRequestID(id)
	want := domain.LadoRunRequest{Entry: "lado-queue-1", Role: domain.LadoRoleMerge, Attempt: 1}
	if !ok || r != want || !r.Resolve() {
		t.Fatalf("parsed %+v, %v; want %+v", r, ok, want)
	}
	if domain.LadoRunClientRequestID(r) != id {
		t.Fatal("the id does not round-trip")
	}
	// A part's merge worker is not a resolve worker.
	if (domain.LadoRunRequest{Entry: "e", Role: domain.LadoRoleMerge, Part: "api", Attempt: 1}).Resolve() {
		t.Fatal("a part's merge worker is a resolve worker")
	}
	for _, bad := range []string{"lado:e/_resolve/0", "lado:e/_resolve/x", "lado:e/_Resolve/1", "lado:e/merge-/1", "lado:e/_resolve-api/1"} {
		if _, ok := domain.ParseLadoRunClientRequestID(bad); ok {
			t.Errorf("%s parsed", bad)
		}
	}
	if domain.ValidLadoPartID("_resolve") {
		t.Fatal("_resolve is a part id")
	}
}

func TestLadoResolveBranch(t *testing.T) {
	task := domain.LadoTaskID("lado-task-c14c6be2-d106-48d5")
	entry := domain.LadoQueueEntryID("lado-queue-500878f7-5c3b")
	if got := domain.LadoResolveBranch(task, entry, 1); got != "lado/c14c6be2/500878f7/_resolve.1" {
		t.Fatalf("resolve branch = %s", got)
	}
	if got := domain.LadoResolveBranch(task, entry, 2); got != "lado/c14c6be2/500878f7/_resolve.2" {
		t.Fatalf("second resolve branch = %s", got)
	}
	if domain.LadoResolveBranch(task, entry, 1) == domain.LadoPartBranch(task, entry, "resolve", 1) {
		t.Fatal("the resolve branch is part resolve's")
	}
}

func TestLadoSessionLive(t *testing.T) {
	for _, tc := range []struct {
		terminated bool
		activity   domain.ActivityState
		want       bool
	}{
		{false, domain.ActivityActive, true},
		{false, domain.ActivityIdle, true},
		{false, domain.ActivityWaitingInput, true},
		{false, "", true},
		{false, domain.ActivityExited, false},
		{true, domain.ActivityIdle, false},
		{true, domain.ActivityExited, false},
	} {
		s := domain.SessionRecord{IsTerminated: tc.terminated, Activity: domain.Activity{State: tc.activity}}
		if got := domain.LadoSessionLive(s); got != tc.want {
			t.Errorf("terminated=%v activity=%q: live = %v, want %v", tc.terminated, tc.activity, got, tc.want)
		}
	}
}

func TestLadoApprovalForSkipsRevoked(t *testing.T) {
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	revoked := domain.LadoIntegrationApproval{ID: "a1", TargetBranch: "main", RevokedAt: now, RevokedReason: domain.LadoCodeBaseOverlap}
	if !revoked.Revoked() {
		t.Fatal("Revoked = false")
	}
	if _, ok := domain.LadoApprovalFor([]domain.LadoIntegrationApproval{revoked}, "main"); ok {
		t.Fatal("a revoked approval holds")
	}
	held := domain.LadoIntegrationApproval{ID: "a2", TargetBranch: "release"}
	got, ok := domain.LadoApprovalFor([]domain.LadoIntegrationApproval{revoked, held}, "release")
	if !ok || got.ID != "a2" {
		t.Fatalf("LadoApprovalFor = %+v, %v", got, ok)
	}
	anyTarget := domain.LadoIntegrationApproval{ID: "a3", RevokedAt: now}
	if _, ok := domain.LadoApprovalFor([]domain.LadoIntegrationApproval{anyTarget}, "main"); ok {
		t.Fatal("a revoked approval without a target holds")
	}
}

func TestLadoBaseBranchAdvanceDetails(t *testing.T) {
	// Empty, the new fields are left out: the hash of a record whose base
	// did not move stays (TestLadoVerificationContentIsTheFormat).
	item, err := domain.NewLadoVerifyItem(domain.LadoVerifyBaseBranch, domain.LadoVerifyPass, domain.LadoCodeNoLocalBranch, domain.LadoBaseBranchDetails{Branch: "main", Remote: "origin"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"branch":"main","remote":"origin","moved":[],"alreadyOnBase":false,"dispatchCommit":"","deleted":false}`; string(item.Details) != want {
		t.Fatalf("details = %s, want %s", item.Details, want)
	}
	tip := strings.Repeat("c", 40)
	item, err = domain.NewLadoVerifyItem(domain.LadoVerifyBaseBranch, domain.LadoVerifyFail, domain.LadoCodeBaseOverlap, domain.LadoBaseBranchDetails{
		Branch: "main", Remote: "origin", Advanced: true, AdvancedTip: tip, Overlap: []string{"api/login.go"}, OverlapCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"branch":"main","remote":"origin","moved":[],"alreadyOnBase":false,"dispatchCommit":"","deleted":false,"advanced":true,"advancedTip":"` + tip + `","overlap":["api/login.go"],"overlapCount":1}`
	if string(item.Details) != want {
		t.Fatalf("details = %s, want %s", item.Details, want)
	}
	var back domain.LadoBaseBranchDetails
	if err := json.Unmarshal(item.Details, &back); err != nil || !back.Advanced || back.AdvancedTip != tip || back.OverlapCount != 1 {
		t.Fatalf("details read back %+v, %v", back, err)
	}
	// base_overlap fails the record; base_advanced only needs confirming.
	v := domain.LadoVerification{Items: []domain.LadoVerifyItem{item}}
	if outcome, reason := v.Overall(); outcome != domain.LadoVerifyFail || reason != domain.LadoCodeBaseOverlap {
		t.Fatalf("Overall = %s, %s", outcome, reason)
	}
	advanced, err := domain.NewLadoVerifyItem(domain.LadoVerifyBaseBranch, domain.LadoVerifyConfirm, domain.LadoCodeBaseAdvanced, domain.LadoBaseBranchDetails{Branch: "main", Advanced: true, AdvancedTip: tip})
	if err != nil {
		t.Fatal(err)
	}
	v = domain.LadoVerification{Items: []domain.LadoVerifyItem{advanced}}
	if outcome, _ := v.Overall(); outcome != domain.LadoVerifyConfirm {
		t.Fatalf("base_advanced Overall = %s", outcome)
	}
}

func TestLadoResolveConstants(t *testing.T) {
	if domain.LadoMaxResolveAttempts != 2 || domain.LadoResolveDoneGrace != 10*time.Minute {
		t.Fatalf("resolve limits = %d, %s", domain.LadoMaxResolveAttempts, domain.LadoResolveDoneGrace)
	}
	r := domain.LadoPRResolve{State: domain.LadoResolveNeedsHuman, Attempts: 2, SessionID: "demo-9", Error: "push refused"}
	if got := r.Execution(); got != (domain.LadoExecutionResolve{State: domain.LadoResolveNeedsHuman, Attempts: 2, SessionID: "demo-9", Error: "push refused"}) {
		t.Fatalf("Execution = %+v", got)
	}
	alert := domain.LadoAlert{Kind: domain.LadoAlertConflictUnresolved, SessionID: "demo-7", Level: 1, Value: 2}
	if !alert.Kind.Valid() {
		t.Fatal("conflict_unresolved is not a valid alert kind")
	}
	if got := alert.Line(53); !strings.Contains(got, "#53") || !strings.Contains(got, "demo-7") || !strings.HasPrefix(got, domain.LadoNoticePrefix) {
		t.Fatalf("alert line = %q", got)
	}
}
