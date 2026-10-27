package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// ladoVerifyAt is when the verification tests start their executions.
var ladoVerifyAt = ladoQueueAt.Add(2 * time.Hour)

// ladoHead is a 40-character commit id of one repeated hex digit.
func ladoHead(c byte) string {
	return strings.Repeat(string(c), 40)
}

// openLadoVerifyStore returns a store whose project mer has LADO mode on.
func openLadoVerifyStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	mustLadoMode(t, s, "mer", true)
	return s
}

// mustLadoExecution queues task "lado-task-<key>" in project mer and links a
// new worker session to its entry, started at started, as the dispatcher
// does; the session ended when terminated. It returns the execution as the
// execution view reads it.
func mustLadoExecution(t *testing.T, s *sqlite.Store, key string, started time.Time, terminated bool) domain.LadoExecution {
	t.Helper()
	ctx := context.Background()
	entry := mustQueuedLadoTask(t, s, key, "mer", ladoQueueAt)
	claimed := mustClaimLadoQueue(t, s, started, 1, ladoTokens("verify-"+key), entry)[0]
	session := mustLadoSession(t, s, "mer", terminated).ID
	if err := s.LinkLadoExecution(ctx, entry, claimed.LeaseToken, domain.LadoExecution{SessionID: session, StartedAt: started}); err != nil {
		t.Fatalf("link the execution of %s: %v", entry, err)
	}
	exec, ok, err := s.LadoExecution(ctx, session)
	if err != nil || !ok {
		t.Fatalf("read the execution of %s = (%v, %v)", entry, ok, err)
	}
	return exec
}

// mustWriteLadoHeadPR writes PR #number of session as AO's SCM observer does:
// in state, with head, target branch main and title "Change <number>", last
// updated at at, with checks. It returns the PR as execution reads carry it.
func mustWriteLadoHeadPR(t *testing.T, s *sqlite.Store, session domain.SessionID, number int, state domain.PRState, head string, at time.Time, checks ...domain.PullRequestCheck) domain.LadoExecutionPR {
	t.Helper()
	pr := domain.LadoExecutionPR{
		URL:          fmt.Sprintf("https://github.com/acme/demo/pull/%d", number),
		Number:       number,
		Merged:       state == domain.PRStateMerged,
		Closed:       state == domain.PRStateClosed,
		HeadSHA:      head,
		TargetBranch: "main",
		Title:        fmt.Sprintf("Change %d", number),
	}
	if err := s.WriteSCMObservation(context.Background(), domain.PullRequest{
		URL: pr.URL, SessionID: session, Number: number, Draft: state == domain.PRStateDraft, Merged: pr.Merged, Closed: pr.Closed,
		HeadSHA: head, TargetBranch: pr.TargetBranch, Title: pr.Title, UpdatedAt: at,
	}, checks, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write PR %d: %v", number, err)
	}
	return pr
}

func mustLadoVerifyItem(t *testing.T, kind domain.LadoVerifyKind, outcome domain.LadoVerifyOutcome, code string, details any) domain.LadoVerifyItem {
	t.Helper()
	item, err := domain.NewLadoVerifyItem(kind, outcome, code, details)
	if err != nil {
		t.Fatalf("NewLadoVerifyItem %s: %v", kind, err)
	}
	return item
}

// ladoVerification is the record of session's head on prURL computed at at.
// Its items pass, evidence is info, and the items given replace the ones of
// their kinds.
func ladoVerification(t *testing.T, session domain.SessionID, prURL, head string, at time.Time, items ...domain.LadoVerifyItem) domain.LadoVerification {
	t.Helper()
	v := domain.LadoVerification{ExecutionSessionID: session, HeadSHA: head, PRURL: prURL, ComputedAt: at}
	for _, kind := range domain.LadoVerifyKinds() {
		outcome := domain.LadoVerifyPass
		if kind == domain.LadoVerifyEvidence {
			outcome = domain.LadoVerifyInfo
		}
		item := mustLadoVerifyItem(t, kind, outcome, "", nil)
		for _, given := range items {
			if given.Kind == kind {
				item = given
			}
		}
		v.Items = append(v.Items, item)
	}
	return v
}

func mustUpsertLadoVerification(t *testing.T, s *sqlite.Store, v domain.LadoVerification) bool {
	t.Helper()
	changed, err := s.UpsertLadoVerification(context.Background(), v)
	if err != nil {
		t.Fatalf("UpsertLadoVerification %s: %v", v.HeadSHA, err)
	}
	return changed
}

func ladoVerificationSHA256(t *testing.T, v domain.LadoVerification) string {
	t.Helper()
	sum, err := v.SHA256()
	if err != nil {
		t.Fatalf("SHA256: %v", err)
	}
	return sum
}

// ladoIntegrationApproval approves v, acknowledging its confirm codes, at at
// with status state.
func ladoIntegrationApproval(t *testing.T, id string, v domain.LadoVerification, state domain.LadoIntegrationStatusState, at time.Time) domain.LadoIntegrationApproval {
	t.Helper()
	return domain.LadoIntegrationApproval{
		ID: id, ExecutionSessionID: v.ExecutionSessionID, PRURL: v.PRURL, HeadSHA: v.HeadSHA,
		VerificationSHA256: ladoVerificationSHA256(t, v), Acknowledged: v.ConfirmCodes(),
		Actor: "human", ApprovedAt: at, StatusState: state,
	}
}

// ladoIntegrationProof is an unused presence proof for approving a head.
func ladoIntegrationProof(nonce string, a domain.LadoIntegrationApproval) domain.LadoPresenceProof {
	return domain.LadoPresenceProof{
		Nonce: nonce, Action: domain.LadoPresenceIntegrationApprove, SubjectID: string(a.ExecutionSessionID) + "@" + a.HeadSHA,
		ExpiresAt: a.ApprovedAt.Add(time.Minute), UsedAt: a.ApprovedAt,
	}
}

func ladoIntegrationEvent(a domain.LadoIntegrationApproval) domain.LadoAuditEvent {
	return domain.LadoAuditEvent{
		At: a.ApprovedAt, Actor: a.Actor, Action: "integration.approved", SubjectKind: "execution", SubjectID: string(a.ExecutionSessionID),
		Payload: json.RawMessage(fmt.Sprintf(`{"head":%q}`, a.HeadSHA)),
	}
}

// mustApproveLadoIntegration approves a with the proof "nonce-<id>".
func mustApproveLadoIntegration(t *testing.T, s *sqlite.Store, a domain.LadoIntegrationApproval) {
	t.Helper()
	if err := s.ApproveLadoIntegration(context.Background(), a, ladoIntegrationProof("nonce-"+a.ID, a), ladoIntegrationEvent(a)); err != nil {
		t.Fatalf("ApproveLadoIntegration %s: %v", a.ID, err)
	}
}

// ladoAuditProbe appends a probe event and returns its id: two probes n apart
// have n-1 events between them.
func ladoAuditProbe(t *testing.T, s *sqlite.Store) int64 {
	t.Helper()
	event, err := s.AppendLadoAuditEvent(context.Background(), domain.LadoAuditEvent{
		At: ladoVerifyAt, Actor: "test", Action: "test.probe", SubjectKind: "test", SubjectID: "probe",
	})
	if err != nil {
		t.Fatalf("append a probe event: %v", err)
	}
	return event.ID
}

// ladoExecutionEvents counts the lado_execution change events of session
// after *seq and moves *seq past every event read.
func ladoExecutionEvents(t *testing.T, s *sqlite.Store, seq *int64, session domain.SessionID) int {
	t.Helper()
	events, err := s.EventsAfter(context.Background(), *seq, 1000)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	n := 0
	for _, ev := range events {
		*seq = ev.Seq
		var payload struct {
			Kind      string `json:"kind"`
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(ev.Payload, &payload)
		if payload.Kind == "lado_execution" && payload.SessionID == string(session) {
			n++
		}
	}
	return n
}

func TestLadoCheckRunsLifecycle(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	head, other := ladoHead('a'), ladoHead('b')
	start := func(id, head, name string, started time.Time) domain.LadoCheckRun {
		t.Helper()
		run := domain.LadoCheckRun{ID: id, ExecutionSessionID: exec.SessionID, HeadSHA: head, Name: name, Command: "make " + name, StartedAt: started}
		if err := s.StartLadoCheckRun(ctx, run); err != nil {
			t.Fatalf("StartLadoCheckRun %s: %v", id, err)
		}
		run.Status = domain.LadoCheckRunRunning
		return run
	}
	test := start("run-2", head, "test", at.Add(2*time.Minute))
	lint := start("run-1", head, "lint", at.Add(time.Minute))
	elsewhere := start("run-3", other, "lint", at.Add(3*time.Minute))
	if err := s.StartLadoCheckRun(ctx, domain.LadoCheckRun{
		ID: "run-4", ExecutionSessionID: exec.SessionID, HeadSHA: head, Name: "lint", Command: "make lint", Status: domain.LadoCheckRunPassed, StartedAt: at,
	}); err == nil {
		t.Fatal("starting a run that already passed was accepted")
	}

	// A run finishes once: its first outcome stays.
	exit := 0
	if err := s.FinishLadoCheckRun(ctx, lint.ID, domain.LadoCheckRunPassed, &exit, ladoSHA('1'), "", at.Add(4*time.Minute)); err != nil {
		t.Fatalf("FinishLadoCheckRun: %v", err)
	}
	lint.Status, lint.ExitCode, lint.LogSHA256, lint.FinishedAt = domain.LadoCheckRunPassed, &exit, ladoSHA('1'), at.Add(4*time.Minute)
	failed := 1
	if err := s.FinishLadoCheckRun(ctx, lint.ID, domain.LadoCheckRunFailed, &failed, ladoSHA('2'), "again", at.Add(5*time.Minute)); !errors.Is(err, domain.ErrLadoCheckRunFinished) {
		t.Fatalf("finishing a finished run = %v, want ErrLadoCheckRunFinished", err)
	}
	if err := s.FinishLadoCheckRun(ctx, "run-missing", domain.LadoCheckRunFailed, nil, "", "", at); err == nil || errors.Is(err, domain.ErrLadoCheckRunFinished) {
		t.Fatalf("finishing an unknown run = %v, want an error of its own", err)
	}
	if err := s.FinishLadoCheckRun(ctx, test.ID, domain.LadoCheckRunRunning, nil, "", "", at); err == nil {
		t.Fatal("finishing a run as running was accepted")
	}
	if got, ok, err := s.GetLadoCheckRun(ctx, lint.ID); err != nil || !ok || !reflect.DeepEqual(got, lint) {
		t.Fatalf("GetLadoCheckRun = %+v (%v, %v), want %+v", got, ok, err, lint)
	}
	if _, ok, err := s.GetLadoCheckRun(ctx, "run-missing"); err != nil || ok {
		t.Fatalf("GetLadoCheckRun of an unknown run = (%v, %v), want none", ok, err)
	}

	// At daemon start, the runs a stopped daemon left running are cancelled,
	// of every head; they cannot finish after that.
	cancelled := at.Add(10 * time.Minute)
	if n, err := s.CancelRunningLadoCheckRuns(ctx, cancelled); err != nil || n != 2 {
		t.Fatalf("CancelRunningLadoCheckRuns = (%d, %v), want 2", n, err)
	}
	if n, err := s.CancelRunningLadoCheckRuns(ctx, cancelled.Add(time.Minute)); err != nil || n != 0 {
		t.Fatalf("CancelRunningLadoCheckRuns again = (%d, %v), want 0", n, err)
	}
	if err := s.FinishLadoCheckRun(ctx, test.ID, domain.LadoCheckRunPassed, &exit, "", "", cancelled.Add(time.Minute)); !errors.Is(err, domain.ErrLadoCheckRunFinished) {
		t.Fatalf("finishing a cancelled run = %v, want ErrLadoCheckRunFinished", err)
	}
	test.Status, test.FinishedAt = domain.LadoCheckRunCancelled, cancelled
	elsewhere.Status, elsewhere.FinishedAt = domain.LadoCheckRunCancelled, cancelled

	// Each head lists its own runs by start time.
	for h, want := range map[string][]domain.LadoCheckRun{head: {lint, test}, other: {elsewhere}, ladoHead('c'): {}} {
		got, err := s.ListLadoCheckRuns(ctx, exec.SessionID, h)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("ListLadoCheckRuns %s = %+v (%v), want %+v", h[:1], got, err, want)
		}
	}
}

func TestLadoVerificationUpsertReportsChanges(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	head := ladoHead('a')
	prURL := "https://github.com/acme/demo/pull/7"
	seq := int64(0)
	ladoExecutionEvents(t, s, &seq, exec.SessionID)
	check := func(step string, v domain.LadoVerification, wantChanged bool, wantNotified []string) {
		t.Helper()
		changed, err := s.UpsertLadoVerification(ctx, v)
		if err != nil || changed != wantChanged {
			t.Fatalf("%s: UpsertLadoVerification = (%v, %v), want changed %v", step, changed, err, wantChanged)
		}
		// A change event comes exactly with a reported change.
		if n, want := ladoExecutionEvents(t, s, &seq, exec.SessionID), map[bool]int{true: 1, false: 0}[wantChanged]; n != want {
			t.Fatalf("%s: %d change events, want %d", step, n, want)
		}
		got, ok, err := s.GetLadoVerification(ctx, exec.SessionID, head)
		if err != nil || !ok {
			t.Fatalf("%s: GetLadoVerification = (%v, %v)", step, ok, err)
		}
		want := v
		want.Notified = wantNotified
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: stored %+v, want %+v", step, got, want)
		}
		// The stored content hashes as the record did.
		if ladoVerificationSHA256(t, got) != ladoVerificationSHA256(t, v) {
			t.Fatalf("%s: the stored record hashes differently", step)
		}
	}

	waiting := ladoVerification(t, exec.SessionID, prURL, head, at,
		mustLadoVerifyItem(t, domain.LadoVerifyRequiredChecks, domain.LadoVerifyWait, domain.LadoCodeChecksPending, domain.LadoRequiredChecksDetails{Required: []string{"ci"}}),
		mustLadoVerifyItem(t, domain.LadoVerifyCommands, domain.LadoVerifyFail, domain.LadoCodeCommandsFailed, nil))
	check("new record", waiting, true, []string{})

	// The same items later, after the worker was told about the failure: the
	// list is stored, nothing changed.
	told := waiting
	told.ComputedAt, told.Notified = at.Add(15*time.Second), []string{string(domain.LadoVerifyCommands)}
	check("same record, notified", told, false, []string{"commands"})
	// A record without a list keeps the stored one.
	quiet := waiting
	quiet.ComputedAt = at.Add(30 * time.Second)
	check("same record, no list", quiet, false, []string{"commands"})

	// A new outcome is a change; the list stays.
	passing := ladoVerification(t, exec.SessionID, prURL, head, at.Add(time.Minute))
	check("outcome changed", passing, true, []string{"commands"})
	// So is new content under the same outcome: the hash differs.
	detailed := ladoVerification(t, exec.SessionID, prURL, head, at.Add(2*time.Minute),
		mustLadoVerifyItem(t, domain.LadoVerifyRequiredChecks, domain.LadoVerifyPass, "", domain.LadoRequiredChecksDetails{Checks: []domain.LadoVerifyCheck{{Name: "ci", Status: "passed", Required: true}}}))
	check("content changed", detailed, true, []string{"commands"})

	// Another head has its own record; this one stays.
	if changed := mustUpsertLadoVerification(t, s, ladoVerification(t, exec.SessionID, prURL, ladoHead('b'), at)); !changed {
		t.Fatal("the first record of another head reported no change")
	}
	if got, ok, err := s.GetLadoVerification(ctx, exec.SessionID, head); err != nil || !ok || ladoVerificationSHA256(t, got) != ladoVerificationSHA256(t, detailed) {
		t.Fatalf("after another head's record: %+v (%v, %v), want the detailed record", got, ok, err)
	}
	if _, ok, err := s.GetLadoVerification(ctx, exec.SessionID, ladoHead('c')); err != nil || ok {
		t.Fatalf("GetLadoVerification of a head without a record = (%v, %v), want none", ok, err)
	}
}

func TestLadoExecutionReviewsCountRounds(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	review := func(run string, round int, head byte, created time.Time) domain.LadoExecutionReview {
		return domain.LadoExecutionReview{ReviewRunID: run, ExecutionSessionID: exec.SessionID, HeadSHA: ladoHead(head), Round: round, CreatedAt: created}
	}
	first := review("review-1", 1, 'a', at.Add(time.Minute))
	second := review("review-2", 2, 'b', at.Add(time.Hour))
	third := review("review-3", 3, 'b', at.Add(2*time.Hour))
	if err := s.AddLadoExecutionReview(ctx, first); err != nil {
		t.Fatalf("AddLadoExecutionReview round 1: %v", err)
	}
	// Rounds come one after another, up to the cap.
	for _, wrong := range []domain.LadoExecutionReview{review("review-x", 1, 'b', at), review("review-x", 3, 'b', at), review("review-x", 0, 'b', at)} {
		if err := s.AddLadoExecutionReview(ctx, wrong); err == nil {
			t.Fatalf("round %d after round 1 was accepted", wrong.Round)
		}
	}
	for _, r := range []domain.LadoExecutionReview{second, third} {
		if err := s.AddLadoExecutionReview(ctx, r); err != nil {
			t.Fatalf("AddLadoExecutionReview round %d: %v", r.Round, err)
		}
	}
	if err := s.AddLadoExecutionReview(ctx, review("review-4", 4, 'c', at.Add(3*time.Hour))); err == nil {
		t.Fatal("a fourth round was accepted")
	}

	// A stalled run is marked once; it still counts as a round.
	cancelled := at.Add(90 * time.Minute)
	if err := s.MarkLadoExecutionReviewCancelled(ctx, second.ReviewRunID, cancelled); err != nil {
		t.Fatalf("MarkLadoExecutionReviewCancelled: %v", err)
	}
	if err := s.MarkLadoExecutionReviewCancelled(ctx, second.ReviewRunID, cancelled.Add(time.Hour)); err != nil {
		t.Fatalf("marking a cancelled run again: %v", err)
	}
	if err := s.MarkLadoExecutionReviewCancelled(ctx, "review-missing", cancelled); err == nil {
		t.Fatal("marking an unknown run was accepted")
	}
	second.CancelledAt = cancelled
	got, err := s.ListLadoExecutionReviews(ctx, exec.SessionID)
	if err != nil || !reflect.DeepEqual(got, []domain.LadoExecutionReview{first, second, third}) {
		t.Fatalf("ListLadoExecutionReviews = %+v (%v), want rounds 1 to 3 with round 2 cancelled", got, err)
	}

	// Another execution counts its own rounds.
	other := mustLadoExecution(t, s, "2", at.Add(time.Minute), false)
	if err := s.AddLadoExecutionReview(ctx, domain.LadoExecutionReview{ReviewRunID: "review-5", ExecutionSessionID: other.SessionID, HeadSHA: ladoHead('d'), Round: 1, CreatedAt: at}); err != nil {
		t.Fatalf("round 1 of another execution: %v", err)
	}
	if got, err := s.ListLadoExecutionReviews(ctx, other.SessionID); err != nil || len(got) != 1 || got[0].ReviewRunID != "review-5" {
		t.Fatalf("the other execution's rounds = %+v (%v), want review-5 alone", got, err)
	}
}

func TestLadoEvidenceLinksReplaceForHead(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	head, other := ladoHead('a'), ladoHead('b')
	link := func(id, head string, criterion int, state domain.LadoEvidenceState, kind domain.LadoEvidenceRefKind, key string) domain.LadoEvidenceLink {
		return domain.LadoEvidenceLink{ID: id, ExecutionSessionID: exec.SessionID, HeadSHA: head, Criterion: criterion, State: state, RefKind: kind, RefKey: key, CreatedAt: at}
	}
	pinned := link("link-1", head, 0, domain.LadoEvidenceFile, domain.LadoEvidenceRefArtifactVersion, string(exec.SessionID)+"/lado/evidence.md@2")
	pinned.SHA = ladoSHA('e')
	firstSet := []domain.LadoEvidenceLink{
		pinned,
		link("link-2", head, 1, domain.LadoEvidenceLinked, domain.LadoEvidenceRefPRCheck, "ci"),
		link("link-3", head, 2, domain.LadoEvidenceClaimed, domain.LadoEvidenceRefClaim, "it works"),
	}
	elsewhere := []domain.LadoEvidenceLink{link("link-4", other, 1, domain.LadoEvidenceStale, domain.LadoEvidenceRefNone, "")}
	expect := func(step, h string, want []domain.LadoEvidenceLink) {
		t.Helper()
		got, err := s.ListLadoEvidenceLinks(ctx, exec.SessionID, h)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: links of %s = %+v (%v), want %+v", step, h[:1], got, err, want)
		}
	}
	replace := func(h string, links []domain.LadoEvidenceLink) {
		t.Helper()
		if err := s.ReplaceLadoEvidenceLinks(ctx, exec.SessionID, h, links); err != nil {
			t.Fatalf("ReplaceLadoEvidenceLinks %s: %v", h[:1], err)
		}
	}
	replace(head, firstSet)
	replace(other, elsewhere)
	expect("first", head, firstSet)
	expect("first", other, elsewhere)

	// The head's links are replaced as a whole; a link may leave its session
	// and head to the call.
	commit := link("link-5", "", 1, domain.LadoEvidenceLinked, domain.LadoEvidenceRefCommit, ladoHead('c'))
	commit.ExecutionSessionID = ""
	replace(head, []domain.LadoEvidenceLink{commit})
	commit.ExecutionSessionID, commit.HeadSHA = exec.SessionID, head
	expect("replaced", head, []domain.LadoEvidenceLink{commit})
	expect("replaced", other, elsewhere)

	// A link of another head is refused, and nothing changes.
	if err := s.ReplaceLadoEvidenceLinks(ctx, exec.SessionID, head, []domain.LadoEvidenceLink{link("link-6", other, 1, domain.LadoEvidenceLinked, domain.LadoEvidenceRefCommit, "x")}); err == nil {
		t.Fatal("a link of another head was accepted")
	}
	expect("refused", head, []domain.LadoEvidenceLink{commit})

	replace(head, nil)
	expect("emptied", head, []domain.LadoEvidenceLink{})
	expect("emptied", other, elsewhere)
}

func TestApproveLadoIntegrationRecordsProofAndAuditTogether(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	prURL := "https://github.com/acme/demo/pull/7"
	unprotected := mustLadoVerifyItem(t, domain.LadoVerifyProtection, domain.LadoVerifyConfirm, domain.LadoCodeUnprotected, nil)
	record := ladoVerification(t, exec.SessionID, prURL, ladoHead('a'), at, unprotected)
	mustUpsertLadoVerification(t, s, record)
	approval := ladoIntegrationApproval(t, "lado-integration-1", record, domain.LadoIntegrationStatusPending, at.Add(time.Minute))
	proof := ladoIntegrationProof("nonce-integration-1", approval)

	before := ladoAuditProbe(t, s)
	if err := s.ApproveLadoIntegration(ctx, approval, proof, ladoIntegrationEvent(approval)); err != nil {
		t.Fatalf("ApproveLadoIntegration: %v", err)
	}
	if after := ladoAuditProbe(t, s); after != before+2 {
		t.Fatalf("the approval appended %d audit events, want 1", after-before-1)
	}
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("VerifyLadoAuditChain = (%d, %v)", broken, err)
	}
	stored := approval
	stored.ProofNonce = proof.Nonce
	if got, ok, err := s.GetLadoIntegrationApproval(ctx, exec.SessionID, record.HeadSHA); err != nil || !ok || !reflect.DeepEqual(got, stored) {
		t.Fatalf("GetLadoIntegrationApproval = %+v (%v, %v), want %+v", got, ok, err, stored)
	}
	if used, err := s.LadoPresenceProofUsed(ctx, proof.Nonce); err != nil || !used {
		t.Fatalf("the approval's proof used = (%v, %v), want true", used, err)
	}

	// Refused approvals keep nothing: not their proofs, approvals or audit
	// events.
	next := ladoVerification(t, exec.SessionID, prURL, ladoHead('b'), at.Add(time.Hour))
	mustUpsertLadoVerification(t, s, next)
	nextApproval := ladoIntegrationApproval(t, "lado-integration-2", next, domain.LadoIntegrationStatusPending, at.Add(2*time.Hour))
	again := approval
	again.ID, again.ApprovedAt = "lado-integration-3", at.Add(2*time.Hour)
	stale := nextApproval
	stale.VerificationSHA256 = ladoVerificationSHA256(t, record)
	unrecorded := ladoIntegrationApproval(t, "lado-integration-4", ladoVerification(t, exec.SessionID, prURL, ladoHead('c'), at), domain.LadoIntegrationStatusPending, at.Add(2*time.Hour))
	refusals := []struct {
		name     string
		approval domain.LadoIntegrationApproval
		nonce    string
		want     error
	}{
		{"a replayed proof", nextApproval, proof.Nonce, domain.ErrLadoPresenceReplayed},
		{"a plan approval's proof", nextApproval, "nonce-lado-approval-1", domain.ErrLadoPresenceReplayed},
		{"a second approval of the head", again, "nonce-integration-2", domain.ErrLadoIntegrationAlreadyApproved},
		{"a hash the head's record does not have", stale, "nonce-integration-3", domain.ErrLadoVerificationChanged},
		{"a head without a record", unrecorded, "nonce-integration-4", domain.ErrLadoVerificationChanged},
	}
	before = ladoAuditProbe(t, s)
	for _, r := range refusals {
		if err := s.ApproveLadoIntegration(ctx, r.approval, ladoIntegrationProof(r.nonce, r.approval), ladoIntegrationEvent(r.approval)); !errors.Is(err, r.want) {
			t.Fatalf("%s = %v, want %v", r.name, err, r.want)
		}
		if r.nonce != proof.Nonce && r.nonce != "nonce-lado-approval-1" {
			if used, err := s.LadoPresenceProofUsed(ctx, r.nonce); err != nil || used {
				t.Fatalf("%s kept its proof (%v, %v)", r.name, used, err)
			}
		}
	}
	if after := ladoAuditProbe(t, s); after != before+1 {
		t.Fatalf("refused approvals appended %d audit events, want none", after-before-1)
	}
	if got, _, err := s.GetLadoIntegrationApproval(ctx, exec.SessionID, record.HeadSHA); err != nil || !reflect.DeepEqual(got, stored) {
		t.Fatalf("after refusals the head's approval = %+v (%v), want the first", got, err)
	}
	for _, h := range []string{ladoHead('b'), ladoHead('c')} {
		if _, ok, err := s.GetLadoIntegrationApproval(ctx, exec.SessionID, h); err != nil || ok {
			t.Fatalf("refused approvals left one for %s (%v)", h[:1], err)
		}
	}

	// A proof a refused approval carried is still unused.
	if err := s.ApproveLadoIntegration(ctx, nextApproval, ladoIntegrationProof("nonce-integration-3", nextApproval), ladoIntegrationEvent(nextApproval)); err != nil {
		t.Fatalf("approving the next head with a proof a refused call carried: %v", err)
	}
}

func TestLadoIntegrationStatusRetries(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	approve := func(id string, head byte, state domain.LadoIntegrationStatusState, attempts int, approved time.Time) domain.LadoIntegrationApproval {
		t.Helper()
		record := ladoVerification(t, exec.SessionID, "https://github.com/acme/demo/pull/7", ladoHead(head), at)
		mustUpsertLadoVerification(t, s, record)
		a := ladoIntegrationApproval(t, id, record, state, approved)
		a.StatusAttempts = attempts
		if state == domain.LadoIntegrationStatusPosted {
			a.StatusPostedAt = approved.Add(time.Second)
		}
		mustApproveLadoIntegration(t, s, a)
		a.ProofNonce = "nonce-" + id
		return a
	}
	failed := approve("approval-2", '2', domain.LadoIntegrationStatusFailed, 3, at.Add(2*time.Minute))
	pending := approve("approval-1", '1', domain.LadoIntegrationStatusPending, 0, at.Add(time.Minute))
	approve("approval-3", '3', domain.LadoIntegrationStatusPosted, 1, at.Add(3*time.Minute))
	approve("approval-4", '4', domain.LadoIntegrationStatusNotApplicable, 0, at.Add(4*time.Minute))
	approve("approval-5", '5', domain.LadoIntegrationStatusFailed, domain.LadoIntegrationStatusMaxAttempts, at.Add(5*time.Minute))
	expect := func(step string, limit int, want ...domain.LadoIntegrationApproval) {
		t.Helper()
		got, err := s.ListLadoIntegrationStatusRetries(ctx, limit)
		if err != nil || len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
			t.Fatalf("%s: ListLadoIntegrationStatusRetries(%d) = %+v (%v), want %+v", step, limit, got, err, want)
		}
	}
	// Pending and failed with attempts left, oldest first.
	expect("start", 10, pending, failed)
	expect("start", 1, pending)

	posted := at.Add(10 * time.Minute)
	if err := s.SetLadoIntegrationStatus(ctx, pending.ID, domain.LadoIntegrationStatusPosted, 1, posted, ""); err != nil {
		t.Fatalf("SetLadoIntegrationStatus posted: %v", err)
	}
	pending.StatusState, pending.StatusAttempts, pending.StatusPostedAt = domain.LadoIntegrationStatusPosted, 1, posted
	if got, ok, err := s.GetLadoIntegrationApproval(ctx, exec.SessionID, pending.HeadSHA); err != nil || !ok || !reflect.DeepEqual(got, pending) {
		t.Fatalf("after posting: %+v (%v, %v), want %+v", got, ok, err, pending)
	}
	expect("posted", 10, failed)

	// Another failure counts; the twelfth leaves the list.
	if err := s.SetLadoIntegrationStatus(ctx, failed.ID, domain.LadoIntegrationStatusFailed, 4, time.Time{}, "HTTP 502"); err != nil {
		t.Fatalf("SetLadoIntegrationStatus failed: %v", err)
	}
	failed.StatusAttempts, failed.StatusError = 4, "HTTP 502"
	expect("failed again", 10, failed)
	if err := s.SetLadoIntegrationStatus(ctx, failed.ID, domain.LadoIntegrationStatusFailed, domain.LadoIntegrationStatusMaxAttempts, time.Time{}, "HTTP 502"); err != nil {
		t.Fatalf("SetLadoIntegrationStatus out of attempts: %v", err)
	}
	expect("out of attempts", 10)
	if err := s.SetLadoIntegrationStatus(ctx, "approval-missing", domain.LadoIntegrationStatusPosted, 1, posted, ""); err == nil {
		t.Fatal("setting the status of an unknown approval was accepted")
	}
}

func TestLadoProjectCheckSettings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	at := ladoVerifyAt
	expect := func(step string, project domain.ProjectID, commands []domain.LadoCheckCommand, minutes int) {
		t.Helper()
		gotCommands, gotMinutes, err := s.LadoProjectChecks(ctx, project)
		if err != nil || !reflect.DeepEqual(gotCommands, commands) || gotMinutes != minutes {
			t.Fatalf("%s: LadoProjectChecks %s = (%+v, %d, %v), want (%+v, %d)", step, project, gotCommands, gotMinutes, err, commands, minutes)
		}
		settings, err := s.LadoProjectSettings(ctx, project)
		if err != nil || !reflect.DeepEqual(settings.CheckCommands, commands) || settings.CheckTimeoutMinutes != minutes {
			t.Fatalf("%s: LadoProjectSettings %s = %+v (%v), want commands %+v and %d minutes", step, project, settings, err, commands, minutes)
		}
	}
	event := func(action string, at time.Time) domain.LadoAuditEvent {
		return domain.LadoAuditEvent{At: at, Actor: "human", Action: action, SubjectKind: "project", SubjectID: "mer"}
	}
	expect("defaults", "mer", []domain.LadoCheckCommand{}, domain.LadoCheckTimeoutDefault)

	// Commands are stored with the proof that allowed them and their audit
	// event; a project without settings gets them with LADO mode off.
	commands := []domain.LadoCheckCommand{{Name: "test", Command: "go test ./..."}, {Name: "lint", Command: `npm run lint && echo "<done>"`}}
	proof := domain.LadoPresenceProof{Nonce: "nonce-checks-1", Action: domain.LadoPresenceCheckCommands, SubjectID: "mer", ExpiresAt: at.Add(time.Minute), UsedAt: at}
	before := ladoAuditProbe(t, s)
	if err := s.SetLadoProjectCheckCommands(ctx, "mer", commands, &proof, event("project.check_commands_changed", at)); err != nil {
		t.Fatalf("SetLadoProjectCheckCommands: %v", err)
	}
	expect("commands", "mer", commands, domain.LadoCheckTimeoutDefault)
	if settings, err := s.LadoProjectSettings(ctx, "mer"); err != nil || settings.LadoMode || !settings.UpdatedAt.Equal(at) {
		t.Fatalf("settings after the commands = %+v (%v), want LADO mode off, updated at %s", settings, err, at)
	}
	if used, err := s.LadoPresenceProofUsed(ctx, proof.Nonce); err != nil || !used {
		t.Fatalf("the commands' proof used = (%v, %v), want true", used, err)
	}
	if err := s.SetLadoProjectCheckCommands(ctx, "mer", nil, &proof, event("project.check_commands_changed", at.Add(time.Minute))); !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("a replayed proof = %v, want ErrLadoPresenceReplayed", err)
	}
	expect("replayed", "mer", commands, domain.LadoCheckTimeoutDefault)

	// The time limit needs no proof; the table keeps it in range.
	if err := s.SetLadoProjectCheckTimeout(ctx, "mer", 60, event("project.check_timeout_changed", at.Add(2*time.Minute))); err != nil {
		t.Fatalf("SetLadoProjectCheckTimeout: %v", err)
	}
	expect("timeout", "mer", commands, 60)
	for _, minutes := range []int{domain.LadoCheckTimeoutMin - 1, domain.LadoCheckTimeoutMax + 1} {
		if err := s.SetLadoProjectCheckTimeout(ctx, "mer", minutes, event("project.check_timeout_changed", at.Add(3*time.Minute))); err == nil {
			t.Fatalf("a time limit of %d minutes was accepted", minutes)
		}
	}
	expect("out of range", "mer", commands, 60)

	// Without a proof the commands store as well; none are an empty list.
	if err := s.SetLadoProjectCheckCommands(ctx, "mer", nil, nil, event("project.check_commands_changed", at.Add(4*time.Minute))); err != nil {
		t.Fatalf("SetLadoProjectCheckCommands without a proof: %v", err)
	}
	expect("cleared", "mer", []domain.LadoCheckCommand{}, 60)
	if after := ladoAuditProbe(t, s); after != before+4 {
		t.Fatalf("the changes appended %d audit events, want 3", after-before-1)
	}

	// Turning LADO mode on keeps the checks; another project has the
	// defaults.
	if _, err := s.SetLadoMode(ctx, "mer", true, at.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	expect("mode on", "mer", []domain.LadoCheckCommand{}, 60)
	expect("other project", "zed", []domain.LadoCheckCommand{}, domain.LadoCheckTimeoutDefault)
}

func TestLadoExecutionReadsCarryVerificationFacts(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	type readCase struct {
		task   domain.LadoTaskID
		exec   domain.LadoExecution
		state  domain.LadoTaskState
		reason string
	}
	var cases []*readCase
	add := func(key string, terminated bool, number int, state domain.PRState, head byte) *readCase {
		t.Helper()
		exec := mustLadoExecution(t, s, key, at.Add(time.Duration(number)*time.Minute), terminated)
		exec.PR = mustWriteLadoHeadPR(t, s, exec.SessionID, number, state, ladoHead(head), at.Add(time.Hour))
		c := &readCase{task: domain.LadoTaskID("lado-task-" + key), exec: exec}
		cases = append(cases, c)
		return c
	}
	verify := func(c *readCase, head byte, items ...domain.LadoVerifyItem) domain.LadoVerification {
		t.Helper()
		v := ladoVerification(t, c.exec.SessionID, c.exec.PR.URL, ladoHead(head), at.Add(2*time.Hour), items...)
		mustUpsertLadoVerification(t, s, v)
		return v
	}

	// lado-task-1's PR has a new head without a record; the old head's
	// record says nothing about it.
	unverified := add("1", false, 31, domain.PRStateOpen, 'b')
	verify(unverified, 'a')
	unverified.state, unverified.reason = domain.LadoTaskValidating, domain.LadoValidatingVerifying

	// lado-task-2's head failed its check commands.
	failing := add("2", false, 32, domain.PRStateOpen, 'c')
	verify(failing, 'c', mustLadoVerifyItem(t, domain.LadoVerifyCommands, domain.LadoVerifyFail, domain.LadoCodeCommandsFailed, nil))
	failing.exec.Verification = domain.LadoExecutionVerification{HeadSHA: ladoHead('c'), Outcome: domain.LadoVerifyFail, Reason: domain.LadoCodeCommandsFailed}
	failing.state, failing.reason = domain.LadoTaskValidating, domain.LadoCodeCommandsFailed

	// lado-task-3's head needs confirming; the approval of its older head
	// does not carry over.
	pending := add("3", false, 33, domain.PRStateOpen, 'e')
	mustApproveLadoIntegration(t, s, ladoIntegrationApproval(t, "approval-3-old", verify(pending, 'd'), domain.LadoIntegrationStatusPosted, at.Add(2*time.Hour)))
	verify(pending, 'e', mustLadoVerifyItem(t, domain.LadoVerifyProtection, domain.LadoVerifyConfirm, domain.LadoCodeUnprotected, nil))
	pending.exec.Verification = domain.LadoExecutionVerification{HeadSHA: ladoHead('e'), Outcome: domain.LadoVerifyConfirm}
	pending.state = domain.LadoTaskIntegrationPending

	// lado-task-4's head is approved; its status is not posted yet.
	integrating := add("4", false, 34, domain.PRStateOpen, 'f')
	integratingApproval := ladoIntegrationApproval(t, "approval-4", verify(integrating, 'f'), domain.LadoIntegrationStatusPending, at.Add(3*time.Hour))
	mustApproveLadoIntegration(t, s, integratingApproval)
	integrating.exec.Verification = domain.LadoExecutionVerification{HeadSHA: ladoHead('f'), Outcome: domain.LadoVerifyPass}
	integrating.exec.Integration = domain.LadoExecutionIntegration{HeadApproved: true, StatusState: domain.LadoIntegrationStatusPending}
	integrating.state = domain.LadoTaskIntegrating

	// lado-task-5 merged its approved head; lado-task-6 merged a head without
	// an approval.
	completed := add("5", true, 35, domain.PRStateMerged, '1')
	mustApproveLadoIntegration(t, s, ladoIntegrationApproval(t, "approval-5", verify(completed, '1'), domain.LadoIntegrationStatusPosted, at.Add(3*time.Hour)))
	completed.exec.SessionTerminated = true
	completed.exec.Verification = domain.LadoExecutionVerification{HeadSHA: ladoHead('1'), Outcome: domain.LadoVerifyPass}
	completed.exec.Integration = domain.LadoExecutionIntegration{HeadApproved: true, StatusState: domain.LadoIntegrationStatusPosted, MergedHeadApproved: true}
	completed.state = domain.LadoTaskCompleted
	bypassed := add("6", true, 36, domain.PRStateMerged, '2')
	verify(bypassed, '2')
	bypassed.exec.SessionTerminated = true
	bypassed.exec.Verification = domain.LadoExecutionVerification{HeadSHA: ladoHead('2'), Outcome: domain.LadoVerifyPass}
	bypassed.state, bypassed.reason = domain.LadoTaskCompleted, domain.LadoCompletedWithoutApproval

	// Task lists, one task and the execution view carry the same facts and
	// give the same state.
	checkReads := func(step string) {
		t.Helper()
		listed, err := s.ListLadoTasks(ctx, "mer", false)
		if err != nil {
			t.Fatalf("%s: ListLadoTasks: %v", step, err)
		}
		live, err := s.ListLiveLadoTasks(ctx)
		if err != nil {
			t.Fatalf("%s: ListLiveLadoTasks: %v", step, err)
		}
		for _, c := range cases {
			single, ok, err := s.LadoTask(ctx, c.task)
			if err != nil || !ok {
				t.Fatalf("%s: LadoTask %s = (%v, %v)", step, c.task, ok, err)
			}
			reads := map[string]domain.LadoTask{"LadoTask": single}
			for name, tasks := range map[string][]domain.LadoTask{"ListLadoTasks": listed, "ListLiveLadoTasks": live} {
				i := slices.IndexFunc(tasks, func(task domain.LadoTask) bool { return task.ID == c.task })
				if i < 0 {
					t.Fatalf("%s: %s left out %s", step, name, c.task)
				}
				reads[name] = tasks[i]
			}
			for name, task := range reads {
				if !reflect.DeepEqual(task.Execution, c.exec) {
					t.Fatalf("%s: %s %s execution = %+v, want %+v", step, name, c.task, task.Execution, c.exec)
				}
				if task.State() != c.state || task.StateReason() != c.reason {
					t.Fatalf("%s: %s %s state = %q (%s), want %q (%s)", step, name, c.task, task.State(), task.StateReason(), c.state, c.reason)
				}
			}
			view, ok, err := s.LadoExecution(ctx, c.exec.SessionID)
			if err != nil || !ok || !reflect.DeepEqual(view, c.exec) {
				t.Fatalf("%s: LadoExecution %s = %+v (%v, %v), want %+v", step, c.exec.SessionID, view, ok, err, c.exec)
			}
		}
	}
	checkReads("start")

	// Once posting the status failed, the approved head says so.
	if err := s.SetLadoIntegrationStatus(ctx, integratingApproval.ID, domain.LadoIntegrationStatusFailed, 1, time.Time{}, "HTTP 502"); err != nil {
		t.Fatal(err)
	}
	integrating.exec.Integration.StatusState = domain.LadoIntegrationStatusFailed
	integrating.reason = domain.LadoIntegratingStatusNotPosted
	// A record for lado-task-1's head arrives and waits on its checks.
	verify(unverified, 'b', mustLadoVerifyItem(t, domain.LadoVerifyRequiredChecks, domain.LadoVerifyWait, domain.LadoCodeChecksPending, nil))
	unverified.exec.Verification = domain.LadoExecutionVerification{HeadSHA: ladoHead('b'), Outcome: domain.LadoVerifyWait, Reason: domain.LadoCodeChecksPending}
	unverified.reason = domain.LadoCodeChecksPending
	checkReads("later")
}

func TestLinkLadoExecutionRecordsBaseBranch(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	now := ladoVerifyAt
	link := func(key string, exec domain.LadoExecution) domain.LadoExecution {
		t.Helper()
		entry := mustQueuedLadoTask(t, s, key, "mer", ladoQueueAt)
		claimed := mustClaimLadoQueue(t, s, now, 1, ladoTokens("base-"+key), entry)[0]
		exec.SessionID, exec.StartedAt = mustLadoSession(t, s, "mer", false).ID, now
		if err := s.LinkLadoExecution(ctx, entry, claimed.LeaseToken, exec); err != nil {
			t.Fatalf("LinkLadoExecution %s: %v", entry, err)
		}
		// Linking again, as recovery may, keeps what the first link recorded.
		again := exec
		again.BaseBranch, again.BaseRemote, again.DispatchLocalBaseSHA = "other", "elsewhere", ladoHead('9')
		if err := s.LinkLadoExecution(ctx, entry, claimed.LeaseToken, again); err != nil {
			t.Fatalf("LinkLadoExecution %s again: %v", entry, err)
		}
		return exec
	}
	expect := func(want domain.LadoExecution) {
		t.Helper()
		got, ok, err := s.LadoExecution(ctx, want.SessionID)
		if err != nil || !ok || got.BaseBranch != want.BaseBranch || got.BaseRemote != want.BaseRemote ||
			got.DispatchLocalBaseSHA != want.DispatchLocalBaseSHA || got.DispatchBaseSHA != want.DispatchBaseSHA {
			t.Fatalf("LadoExecution %s = %+v (%v, %v), want base %s/%s, local %q, dispatch %q", want.SessionID, got, ok, err,
				want.BaseRemote, want.BaseBranch, want.DispatchLocalBaseSHA, want.DispatchBaseSHA)
		}
		task, _, err := s.LadoTask(ctx, got.TaskID)
		if err != nil || !reflect.DeepEqual(task.Execution, got) {
			t.Fatalf("task %s execution = %+v (%v), want %+v", got.TaskID, task.Execution, err, got)
		}
	}

	resolved := link("1", domain.LadoExecution{DispatchBaseSHA: ladoHead('d'), BaseBranch: "release/1.2", BaseRemote: "upstream", DispatchLocalBaseSHA: ladoHead('e')})
	expect(resolved)
	// Without a local branch, the local commit stays empty.
	remoteOnly := link("2", domain.LadoExecution{DispatchBaseSHA: ladoHead('d'), BaseBranch: "main", BaseRemote: "origin"})
	expect(remoteOnly)

	live, err := s.LiveLadoExecutions(ctx)
	if err != nil || len(live) != 2 || live[0].BaseBranch != "release/1.2" || live[0].DispatchLocalBaseSHA != ladoHead('e') || live[1].BaseRemote != "origin" {
		t.Fatalf("LiveLadoExecutions = %+v (%v), want both executions with their base facts", live, err)
	}
}

func TestListLadoExecutionsToVerify(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	started := func(n int) time.Time { return at.Add(time.Duration(n) * time.Minute) }
	settle := func(exec *domain.LadoExecution, settled time.Time, audit *domain.LadoAuditEvent) {
		t.Helper()
		if err := s.SettleLadoExecution(ctx, exec.SessionID, settled, audit); err != nil {
			t.Fatalf("SettleLadoExecution %s: %v", exec.SessionID, err)
		}
		if exec.SettledAt.IsZero() {
			exec.SettledAt = settled
		}
	}

	// lado-task-1's PR is open, with a record of its head.
	open := mustLadoExecution(t, s, "1", started(1), false)
	open.PR = mustWriteLadoHeadPR(t, s, open.SessionID, 41, domain.PRStateOpen, ladoHead('1'), at.Add(time.Hour))
	mustUpsertLadoVerification(t, s, ladoVerification(t, open.SessionID, open.PR.URL, ladoHead('1'), at.Add(time.Hour),
		mustLadoVerifyItem(t, domain.LadoVerifyRequiredChecks, domain.LadoVerifyWait, domain.LadoCodeChecksPending, nil)))
	open.Verification = domain.LadoExecutionVerification{HeadSHA: ladoHead('1'), Outcome: domain.LadoVerifyWait, Reason: domain.LadoCodeChecksPending}
	// lado-task-2 has no PR.
	mustLadoExecution(t, s, "2", started(2), false)
	// lado-task-3's PR merged and lado-task-5's closed; neither is settled.
	merged := mustLadoExecution(t, s, "3", started(3), true)
	merged.PR = mustWriteLadoHeadPR(t, s, merged.SessionID, 43, domain.PRStateMerged, ladoHead('3'), at.Add(time.Hour))
	// lado-task-4's closed PR is settled.
	settledClosed := mustLadoExecution(t, s, "4", started(4), true)
	mustWriteLadoHeadPR(t, s, settledClosed.SessionID, 44, domain.PRStateClosed, ladoHead('4'), at.Add(time.Hour))
	settle(&settledClosed, at.Add(2*time.Hour), nil)
	closed := mustLadoExecution(t, s, "5", started(5), true)
	closed.PR = mustWriteLadoHeadPR(t, s, closed.SessionID, 45, domain.PRStateClosed, ladoHead('5'), at.Add(time.Hour))
	// lado-task-6's newest PR merged and is settled: its older open PR does
	// not count.
	replaced := mustLadoExecution(t, s, "6", started(6), false)
	mustWriteLadoHeadPR(t, s, replaced.SessionID, 46, domain.PRStateOpen, ladoHead('6'), at.Add(time.Hour))
	mustWriteLadoHeadPR(t, s, replaced.SessionID, 47, domain.PRStateMerged, ladoHead('7'), at.Add(2*time.Hour))
	settle(&replaced, at.Add(3*time.Hour), nil)
	// lado-task-7's newest PR, a draft, is open after an older one merged;
	// a settled time does not stop an open PR's verification.
	reopened := mustLadoExecution(t, s, "7", started(7), false)
	mustWriteLadoHeadPR(t, s, reopened.SessionID, 48, domain.PRStateMerged, ladoHead('8'), at.Add(time.Hour))
	reopened.PR = mustWriteLadoHeadPR(t, s, reopened.SessionID, 49, domain.PRStateDraft, ladoHead('9'), at.Add(2*time.Hour))
	settle(&reopened, at.Add(3*time.Hour), nil)

	expect := func(step string, limit int, want ...domain.LadoExecution) {
		t.Helper()
		got, err := s.ListLadoExecutionsToVerify(ctx, limit)
		if err != nil {
			t.Fatalf("%s: ListLadoExecutionsToVerify(%d): %v", step, limit, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: ListLadoExecutionsToVerify(%d) = %+v, want %+v", step, limit, got, want)
		}
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Fatalf("%s: execution %d = %+v, want %+v", step, i, got[i], want[i])
			}
			// The loop sees each execution as the execution view reads it.
			view, _, err := s.LadoExecution(ctx, got[i].SessionID)
			if err != nil || !reflect.DeepEqual(view, got[i]) {
				t.Fatalf("%s: LadoExecution %s = %+v (%v), want %+v", step, got[i].SessionID, view, err, got[i])
			}
		}
	}
	expect("start", 50, open, merged, closed, reopened)
	expect("limit", 2, open, merged)
	expect("no limit", 0)

	// Settling a merged PR with its audit event takes it off the list; the
	// event is appended once, and the first settled time stays.
	audit := domain.LadoAuditEvent{At: at.Add(4 * time.Hour), Actor: "lado", Action: "execution.merged_without_approval", SubjectKind: "execution", SubjectID: string(merged.SessionID)}
	before := ladoAuditProbe(t, s)
	settle(&merged, at.Add(4*time.Hour), &audit)
	settle(&merged, at.Add(5*time.Hour), &audit)
	if after := ladoAuditProbe(t, s); after != before+2 {
		t.Fatalf("settling twice appended %d audit events, want 1", after-before-1)
	}
	if got, _, err := s.LadoExecution(ctx, merged.SessionID); err != nil || !got.SettledAt.Equal(at.Add(4*time.Hour)) {
		t.Fatalf("settled at %s (%v), want the first settling time", got.SettledAt, err)
	}
	settle(&closed, at.Add(4*time.Hour), nil)
	expect("settled", 50, open, reopened)
}

func TestListLadoCommitChecksReadsOneCommit(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	check := func(name, commit string, status domain.PRCheckStatus) domain.PullRequestCheck {
		return domain.PullRequestCheck{Name: name, CommitHash: commit, Status: status, Conclusion: "", CreatedAt: at}
	}
	head, old := ladoHead('b'), ladoHead('a')
	pr := mustWriteLadoHeadPR(t, s, exec.SessionID, 51, domain.PRStateOpen, head, at,
		check("test", old, domain.PRCheckFailed), check("test", head, domain.PRCheckPassed), check("build", head, domain.PRCheckInProgress))
	got, err := s.ListLadoCommitChecks(ctx, pr.URL, head)
	if err != nil || len(got) != 2 || got[0].Name != "build" || got[0].Status != domain.PRCheckInProgress || got[1].Name != "test" || got[1].Status != domain.PRCheckPassed {
		t.Fatalf("ListLadoCommitChecks of the head = %+v (%v), want build in progress and test passed", got, err)
	}
	if got, err := s.ListLadoCommitChecks(ctx, pr.URL, ladoHead('c')); err != nil || len(got) != 0 {
		t.Fatalf("ListLadoCommitChecks of a commit without checks = %+v (%v), want none", got, err)
	}
}
