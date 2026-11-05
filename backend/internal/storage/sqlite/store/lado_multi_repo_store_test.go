package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// openLadoMultiRepoStore returns a store whose project mer has LADO mode on,
// with a raw connection to read what the store methods do not return.
func openLadoMultiRepoStore(t *testing.T) (*sqlite.Store, *sql.DB) {
	t.Helper()
	s, raw := openLadoQueueStore(t)
	seedProject(t, s, "mer")
	mustLadoMode(t, s, "mer", true)
	return s, raw
}

// mustWriteLadoRepoPR writes PR #number of session in repository
// acme/<repo> as AO's SCM observer does: in state, with head and
// mergeability, from branch <repo>-<number> into main, last updated at at.
func mustWriteLadoRepoPR(t *testing.T, s *sqlite.Store, session domain.SessionID, repo string, number int, state domain.PRState, head string, mergeability domain.Mergeability, at time.Time) string {
	t.Helper()
	url := fmt.Sprintf("https://github.com/acme/%s/pull/%d", repo, number)
	if err := s.WriteSCMObservation(context.Background(), domain.PullRequest{
		URL: url, SessionID: session, Number: number, Draft: state == domain.PRStateDraft,
		Merged: state == domain.PRStateMerged, Closed: state == domain.PRStateClosed,
		HeadSHA: head, SourceBranch: fmt.Sprintf("%s-%d", repo, number), TargetBranch: "main",
		Title: fmt.Sprintf("Change %d", number), Mergeability: mergeability, UpdatedAt: at,
	}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write PR %s#%d: %v", repo, number, err)
	}
	return url
}

func mustTrackLadoPR(t *testing.T, s *sqlite.Store, session domain.SessionID, url, repo string, position int, want bool) {
	t.Helper()
	tracked, err := s.TrackLadoExecutionPR(context.Background(), session, url, repo, position, ladoVerifyAt)
	if err != nil || tracked != want {
		t.Fatalf("TrackLadoExecutionPR %s = (%v, %v), want %v", url, tracked, err, want)
	}
}

func mustReadLadoExecution(t *testing.T, s *sqlite.Store, session domain.SessionID) domain.LadoExecution {
	t.Helper()
	exec, ok, err := s.LadoExecution(context.Background(), session)
	if err != nil || !ok {
		t.Fatalf("LadoExecution %s = (%v, %v)", session, ok, err)
	}
	return exec
}

func ladoTrackedURLs(exec domain.LadoExecution) []string {
	var urls []string
	for _, pr := range exec.PRs {
		urls = append(urls, pr.PR.URL)
	}
	return urls
}

func ladoPRURLs(prs []domain.LadoExecutionPR) []string {
	var urls []string
	for _, pr := range prs {
		urls = append(urls, pr.URL)
	}
	return urls
}

// An execution tracks one PR per repository: the tracked PRs stay its PRs in
// merge order whatever PR AO updated last, a second live PR of a repository
// is not tracked and shows as untracked, each tracked PR carries the
// verification of its own head, and the verification list gives one row per
// tracked PR.
func TestLadoExecutionTracksOnePRPerRepository(t *testing.T) {
	s, _ := openLadoMultiRepoStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	back := mustWriteLadoRepoPR(t, s, exec.SessionID, "back", 1, domain.PRStateOpen, ladoHead('a'), domain.MergeMergeable, at)
	front := mustWriteLadoRepoPR(t, s, exec.SessionID, "front", 2, domain.PRStateOpen, ladoHead('b'), domain.MergeMergeable, at.Add(time.Minute))

	// Before LADO tracks them, the newest PR is the execution's.
	candidates, err := s.ListLadoExecutionPRsToTrack(ctx, 10)
	if err != nil || len(candidates) != 1 || candidates[0].PR.URL != front ||
		!slices.Equal(ladoPRURLs(candidates[0].UntrackedPRs), []string{front, back}) {
		t.Fatalf("candidates = %+v (%v), want the execution with both PRs untracked", candidates, err)
	}

	mustTrackLadoPR(t, s, exec.SessionID, back, "back", 0, true)
	mustTrackLadoPR(t, s, exec.SessionID, front, "/front/", 1, true)
	mustTrackLadoPR(t, s, exec.SessionID, front, "front", 1, false)
	second := mustWriteLadoRepoPR(t, s, exec.SessionID, "back", 3, domain.PRStateOpen, ladoHead('c'), domain.MergeMergeable, at.Add(2*time.Minute))
	mustTrackLadoPR(t, s, exec.SessionID, second, "back", 0, false)
	if _, err := s.TrackLadoExecutionPR(ctx, "no-such-session", back, "back", 0, at); err == nil {
		t.Fatal("tracking a PR of no execution was accepted")
	}

	// AO updates the front PR last; the tracked PRs stay.
	mustWriteLadoRepoPR(t, s, exec.SessionID, "front", 2, domain.PRStateOpen, ladoHead('d'), domain.MergeMergeable, at.Add(time.Hour))
	read := mustReadLadoExecution(t, s, exec.SessionID)
	if read.PR.URL != back || !slices.Equal(ladoTrackedURLs(read), []string{back, front}) ||
		read.PRs[1].Repo != "front" || read.PRs[1].Position != 1 || read.PRs[1].PR.HeadSHA != ladoHead('d') ||
		!slices.Equal(ladoPRURLs(read.UntrackedPRs), []string{second}) {
		t.Fatalf("execution PR %s, PRs %+v, untracked %v; want back then front, the second back PR untracked", read.PR.URL, read.PRs, read.UntrackedPRs)
	}
	candidates, err = s.ListLadoExecutionPRsToTrack(ctx, 10)
	if err != nil || len(candidates) != 1 || !slices.Equal(ladoPRURLs(candidates[0].UntrackedPRs), []string{second}) {
		t.Fatalf("candidates = %+v (%v), want the second back PR left", candidates, err)
	}

	// Each tracked PR reads its own head's record.
	backRecord := ladoVerification(t, exec.SessionID, back, ladoHead('a'), at)
	frontRecord := ladoVerification(t, exec.SessionID, front, ladoHead('d'), at.Add(time.Minute),
		mustLadoVerifyItem(t, domain.LadoVerifyRequiredChecks, domain.LadoVerifyWait, domain.LadoCodeChecksPending, nil))
	mustUpsertLadoVerification(t, s, backRecord)
	mustUpsertLadoVerification(t, s, frontRecord)
	read = mustReadLadoExecution(t, s, exec.SessionID)
	if read.PRs[0].Verification.Outcome != domain.LadoVerifyPass || read.PRs[1].Verification.Outcome != domain.LadoVerifyWait ||
		read.Verification.Outcome != domain.LadoVerifyPass {
		t.Fatalf("verification = %+v, PRs %+v", read.Verification, read.PRs)
	}

	toVerify, err := s.ListLadoExecutionsToVerify(ctx, 10)
	if err != nil {
		t.Fatalf("ListLadoExecutionsToVerify: %v", err)
	}
	var verified []string
	for _, e := range toVerify {
		if e.SessionID == exec.SessionID {
			verified = append(verified, e.PR.URL)
			if len(e.PRs) != 2 {
				t.Fatalf("verify row of %s carries PRs %+v", e.PR.URL, e.PRs)
			}
			if e.PR.URL == front && e.Verification.Outcome != domain.LadoVerifyWait {
				t.Fatalf("front verify row = %+v", e.Verification)
			}
		}
	}
	// The oldest record first: back's.
	if !slices.Equal(verified, []string{back, front}) {
		t.Fatalf("verify rows = %v, want back then front", verified)
	}
	if got, _ := s.ListLadoExecutionsToVerify(ctx, 1); len(got) != 1 || got[0].PR.URL != back {
		t.Fatalf("one verify row = %+v, want back", got)
	}
}

// Back merges first: it settles on its own row while the session stays
// observed, the paths stay held and the front PR is still verified; once
// front settles too, the execution's own settled_* take front's values in
// the same write, observing ends and the paths are free.
func TestSettleLadoExecutionPRSettlesTheExecutionLast(t *testing.T) {
	s, raw := openLadoMultiRepoStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	back := mustWriteLadoRepoPR(t, s, exec.SessionID, "back", 1, domain.PRStateOpen, ladoHead('a'), domain.MergeMergeable, at)
	front := mustWriteLadoRepoPR(t, s, exec.SessionID, "front", 2, domain.PRStateOpen, ladoHead('b'), domain.MergeMergeable, at)
	mustTrackLadoPR(t, s, exec.SessionID, back, "back", 0, true)
	mustTrackLadoPR(t, s, exec.SessionID, front, "front", 1, true)
	observed := func() bool {
		t.Helper()
		sessions, err := s.LadoExecutionSessionsToObserve(ctx)
		if err != nil {
			t.Fatalf("LadoExecutionSessionsToObserve: %v", err)
		}
		return slices.Contains(sessions, exec.SessionID)
	}
	holds := func() bool {
		t.Helper()
		tasks, err := s.LadoOverlapHolders(ctx, at)
		if err != nil {
			t.Fatalf("LadoOverlapHolders: %v", err)
		}
		return slices.ContainsFunc(tasks, func(task domain.LadoLiveRunTask) bool { return task.TaskID == exec.TaskID })
	}
	settled := func() (string, string) {
		t.Helper()
		var url, state string
		if err := raw.QueryRow(`SELECT settled_pr_url, settled_pr_state FROM lado_executions WHERE session_id = ?`, string(exec.SessionID)).Scan(&url, &state); err != nil {
			t.Fatalf("read settled: %v", err)
		}
		return url, state
	}
	verifying := func() []string {
		t.Helper()
		rows, err := s.ListLadoExecutionsToVerify(ctx, 10)
		if err != nil {
			t.Fatalf("ListLadoExecutionsToVerify: %v", err)
		}
		var urls []string
		for _, e := range rows {
			urls = append(urls, e.PR.URL)
		}
		return urls
	}
	audit := func(pr string) *domain.LadoAuditEvent {
		return &domain.LadoAuditEvent{At: at, Actor: "lado", Action: "execution.merged_without_approval", SubjectKind: "execution", SubjectID: string(exec.SessionID), Payload: []byte(fmt.Sprintf(`{"pr":%q}`, pr))}
	}

	mustWriteLadoRepoPR(t, s, exec.SessionID, "back", 1, domain.PRStateMerged, ladoHead('a'), domain.MergeMergeable, at.Add(time.Minute))
	if got := verifying(); !slices.Equal(got, []string{back, front}) {
		t.Fatalf("verify rows = %v, want the merged back to settle and front", got)
	}
	before := ladoAuditProbe(t, s)
	for range 2 {
		if err := s.SettleLadoExecutionPR(ctx, exec.SessionID, back, domain.PRStateMerged, at.Add(2*time.Minute), audit(back)); err != nil {
			t.Fatalf("settle back: %v", err)
		}
	}
	if after := ladoAuditProbe(t, s); after != before+2 {
		t.Fatalf("settling back twice appended %d audit events, want 1", after-before-1)
	}
	if url, _ := settled(); url != "" {
		t.Fatalf("the execution settled with back while front is open: %q", url)
	}
	read := mustReadLadoExecution(t, s, exec.SessionID)
	if !read.SettledAt.IsZero() || !read.PRs[0].Settled || read.PRs[1].Settled {
		t.Fatalf("execution after back settled = %v, PRs %+v", read.SettledAt, read.PRs)
	}
	if !observed() || !holds() {
		t.Fatalf("after back merged: observed %v, holds %v; want both", observed(), holds())
	}
	if got := verifying(); !slices.Equal(got, []string{front}) {
		t.Fatalf("verify rows = %v, want front only", got)
	}

	// Front gets a new head and merges.
	mustWriteLadoRepoPR(t, s, exec.SessionID, "front", 2, domain.PRStateOpen, ladoHead('c'), domain.MergeMergeable, at.Add(3*time.Minute))
	if got := mustReadLadoExecution(t, s, exec.SessionID); got.PRs[1].PR.HeadSHA != ladoHead('c') {
		t.Fatalf("front head = %s", got.PRs[1].PR.HeadSHA)
	}
	mustWriteLadoRepoPR(t, s, exec.SessionID, "front", 2, domain.PRStateMerged, ladoHead('c'), domain.MergeMergeable, at.Add(4*time.Minute))
	if err := s.SettleLadoExecutionPR(ctx, exec.SessionID, front, domain.PRStateMerged, at.Add(5*time.Minute), nil); err != nil {
		t.Fatalf("settle front: %v", err)
	}
	if url, state := settled(); url != front || state != string(domain.PRStateMerged) {
		t.Fatalf("execution settled = %q %q, want front merged", url, state)
	}
	if read := mustReadLadoExecution(t, s, exec.SessionID); !read.SettledAt.Equal(at.Add(5 * time.Minute)) {
		t.Fatalf("execution settled at %v", read.SettledAt)
	}
	if observed() || holds() {
		t.Fatalf("after front merged: observed %v, holds %v; want neither", observed(), holds())
	}
	if got := verifying(); len(got) != 0 {
		t.Fatalf("verify rows = %v, want none", got)
	}

	// An execution without tracked PRs settles as before.
	old := mustLadoExecution(t, s, "2", at, true)
	oldPR := mustWriteLadoRepoPR(t, s, old.SessionID, "back", 9, domain.PRStateClosed, ladoHead('e'), domain.MergeMergeable, at)
	if err := s.SettleLadoExecutionPR(ctx, old.SessionID, oldPR, domain.PRStateClosed, at, nil); err != nil {
		t.Fatalf("settle an untracked execution: %v", err)
	}
	if read := mustReadLadoExecution(t, s, old.SessionID); read.SettledAt.IsZero() || len(read.PRs) != 0 {
		t.Fatalf("untracked execution = %+v", read)
	}
}

// A PR the execution settled before LADO tracked PRs is tracked as settled,
// and a closed PR replaced by a newer one in its repository keeps its row
// while the new one takes its place in the merge order.
func TestTrackAndReplaceLadoExecutionPR(t *testing.T) {
	s, _ := openLadoMultiRepoStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	merged := mustWriteLadoRepoPR(t, s, exec.SessionID, "back", 1, domain.PRStateMerged, ladoHead('a'), domain.MergeMergeable, at)
	if err := s.SettleLadoExecutionPR(ctx, exec.SessionID, merged, domain.PRStateMerged, at, nil); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if candidates, _ := s.ListLadoExecutionPRsToTrack(ctx, 10); len(candidates) != 0 {
		t.Fatalf("a settled execution with only merged PRs is a candidate: %+v", candidates)
	}
	mustTrackLadoPR(t, s, exec.SessionID, merged, "back", 0, true)
	if read := mustReadLadoExecution(t, s, exec.SessionID); len(read.PRs) != 1 || !read.PRs[0].Settled {
		t.Fatalf("seeded PR = %+v, want it settled", read.PRs)
	}

	closed := mustWriteLadoRepoPR(t, s, exec.SessionID, "front", 2, domain.PRStateOpen, ladoHead('b'), domain.MergeMergeable, at)
	mustTrackLadoPR(t, s, exec.SessionID, closed, "front", 1, true)
	newer := mustWriteLadoRepoPR(t, s, exec.SessionID, "front", 3, domain.PRStateOpen, ladoHead('c'), domain.MergeMergeable, at.Add(time.Minute))
	audit := &domain.LadoAuditEvent{At: at, Actor: "lado", Action: "execution.pr_replaced", SubjectKind: "execution", SubjectID: string(exec.SessionID)}
	if ok, err := s.ReplaceLadoExecutionPR(ctx, exec.SessionID, closed, newer, at, audit); err != nil || ok {
		t.Fatalf("replacing an open PR = (%v, %v), want refused", ok, err)
	}
	mustWriteLadoRepoPR(t, s, exec.SessionID, "front", 2, domain.PRStateClosed, ladoHead('b'), domain.MergeMergeable, at.Add(2*time.Minute))
	if ok, err := s.ReplaceLadoExecutionPR(ctx, exec.SessionID, merged, newer, at, audit); err != nil || ok {
		t.Fatalf("replacing a merged PR = (%v, %v), want refused", ok, err)
	}
	before := ladoAuditProbe(t, s)
	if ok, err := s.ReplaceLadoExecutionPR(ctx, exec.SessionID, closed, newer, at.Add(3*time.Minute), audit); err != nil || !ok {
		t.Fatalf("ReplaceLadoExecutionPR = (%v, %v)", ok, err)
	}
	if after := ladoAuditProbe(t, s); after != before+2 {
		t.Fatalf("replacing appended %d audit events, want 1", after-before-1)
	}
	if ok, err := s.ReplaceLadoExecutionPR(ctx, exec.SessionID, closed, newer, at, audit); err != nil || ok {
		t.Fatalf("replacing again = (%v, %v)", ok, err)
	}
	read := mustReadLadoExecution(t, s, exec.SessionID)
	if !slices.Equal(ladoTrackedURLs(read), []string{merged, newer}) || read.PRs[1].Repo != "front" || read.PRs[1].Position != 1 ||
		len(read.UntrackedPRs) != 0 {
		t.Fatalf("PRs after the replacement = %+v, untracked %v", read.PRs, read.UntrackedPRs)
	}
}

// The repositories an execution expects PRs in and whether that is a fact
// round-trip through a link, and the worker's newest report says whether it
// is done.
func TestLadoExecutionCarriesExpectedReposAndWorkerDone(t *testing.T) {
	s, _ := openLadoMultiRepoStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	entry := mustQueuedLadoTask(t, s, "1", "mer", ladoQueueAt)
	claimed := mustClaimLadoQueue(t, s, at, 1, ladoTokens("repos"), entry)[0]
	session := mustLadoSession(t, s, "mer", false)
	if err := s.LinkLadoExecution(ctx, entry, claimed.LeaseToken, domain.LadoExecution{
		SessionID: session.ID, StartedAt: at, PRRepos: []string{"back/", "front"},
	}); err != nil {
		t.Fatalf("LinkLadoExecution: %v", err)
	}
	read := mustReadLadoExecution(t, s, session.ID)
	if !slices.Equal(read.PRRepos, []string{"back", "front"}) || read.PRReposFirm || read.WorkerDone {
		t.Fatalf("execution = repos %v firm %v done %v", read.PRRepos, read.PRReposFirm, read.WorkerDone)
	}
	if got := read.MissingPRRepos(); !slices.Equal(got, []string{"back", "front"}) {
		t.Fatalf("missing = %v", got)
	}
	for i, state := range []domain.ReportState{domain.ReportCheckpoint, domain.ReportDone} {
		report := domain.ReportRecord{
			ID: fmt.Sprintf("report-%d", i), SessionID: session.ID, ProjectID: session.ProjectID, State: state, Note: "progress",
			CreatedAt: at.Add(time.Duration(i) * time.Minute), DeliveryState: domain.ReportPending, AvailableAt: at, RepeatCount: 1,
		}
		if state == domain.ReportDone {
			report.SettlementDeadline = at.Add(time.Hour)
		}
		if _, err := s.CreateReport(ctx, report); err != nil {
			t.Fatalf("CreateReport: %v", err)
		}
	}
	read = mustReadLadoExecution(t, s, session.ID)
	if !read.WorkerDone || len(read.MissingPRRepos()) != 0 {
		t.Fatalf("after done: done %v, missing %v", read.WorkerDone, read.MissingPRRepos())
	}
}

// One presence proof approves two PRs in one transaction: the first approval
// takes the proof's nonce and the second a derived one with its own proof
// row; the proof cannot be used again, and a failing approval keeps nothing.
func TestApproveLadoIntegrationsOneProofTwoApprovals(t *testing.T) {
	s, raw := openLadoMultiRepoStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	back := mustWriteLadoRepoPR(t, s, exec.SessionID, "back", 1, domain.PRStateOpen, ladoHead('a'), domain.MergeMergeable, at)
	front := mustWriteLadoRepoPR(t, s, exec.SessionID, "front", 2, domain.PRStateOpen, ladoHead('b'), domain.MergeMergeable, at)
	mustTrackLadoPR(t, s, exec.SessionID, back, "back", 0, true)
	mustTrackLadoPR(t, s, exec.SessionID, front, "front", 1, true)
	backRecord := ladoVerification(t, exec.SessionID, back, ladoHead('a'), at)
	frontRecord := ladoVerification(t, exec.SessionID, front, ladoHead('b'), at)
	mustUpsertLadoVerification(t, s, backRecord)
	mustUpsertLadoVerification(t, s, frontRecord)
	approvals := []domain.LadoIntegrationApproval{
		ladoIntegrationApproval(t, "approval-back", backRecord, domain.LadoIntegrationStatusPending, at.Add(time.Minute)),
		ladoIntegrationApproval(t, "approval-front", frontRecord, domain.LadoIntegrationStatusPending, at.Add(time.Minute)),
	}
	proof := domain.LadoPresenceProof{
		Nonce: "nonce", Action: domain.LadoPresenceIntegrationApprove,
		SubjectID: string(exec.SessionID) + "@" + ladoHead('a') + "," + ladoHead('b'),
		ExpiresAt: at.Add(2 * time.Minute), UsedAt: at.Add(time.Minute),
	}
	event := ladoIntegrationEvent(approvals[0])

	// A stale record for front fails the whole request.
	stale := slices.Clone(approvals)
	stale[1].VerificationSHA256 = ladoVerificationSHA256(t, backRecord)
	if err := s.ApproveLadoIntegrations(ctx, stale, proof, event); !errors.Is(err, domain.ErrLadoVerificationChanged) {
		t.Fatalf("approving a stale record = %v, want ErrLadoVerificationChanged", err)
	}
	if used, _ := s.LadoPresenceProofUsed(ctx, "nonce"); used {
		t.Fatal("a failed approval kept its proof")
	}
	if _, ok := ladoApprovalOf(t, s, exec.SessionID, back, ladoHead('a')); ok {
		t.Fatal("a failed approval kept the back approval")
	}

	before := ladoAuditProbe(t, s)
	if err := s.ApproveLadoIntegrations(ctx, approvals, proof, event); err != nil {
		t.Fatalf("ApproveLadoIntegrations: %v", err)
	}
	if after := ladoAuditProbe(t, s); after != before+2 {
		t.Fatalf("approving appended %d audit events, want 1", after-before-1)
	}
	gotBack, okBack := ladoApprovalOf(t, s, exec.SessionID, back, ladoHead('a'))
	gotFront, okFront := ladoApprovalOf(t, s, exec.SessionID, front, ladoHead('b'))
	if !okBack || !okFront || gotBack.ProofNonce != "nonce" || gotFront.ProofNonce != store.LadoDerivedPresenceNonce("nonce", 1) || gotFront.ProofNonce != "nonce#1" {
		t.Fatalf("approvals = %+v / %+v", gotBack, gotFront)
	}
	var action, subject string
	if err := raw.QueryRow(`SELECT action, subject_id FROM lado_presence_proofs WHERE nonce = 'nonce#1'`).Scan(&action, &subject); err != nil ||
		action != proof.Action || subject != proof.SubjectID {
		t.Fatalf("derived proof = %q %q, %v", action, subject, err)
	}
	read := mustReadLadoExecution(t, s, exec.SessionID)
	if !read.PRs[0].Integration.HeadApproved || !read.PRs[1].Integration.HeadApproved {
		t.Fatalf("PRs after the approval = %+v", read.PRs)
	}

	again := []domain.LadoIntegrationApproval{approvals[0]}
	again[0].ID = "approval-again"
	if err := s.ApproveLadoIntegrations(ctx, again, proof, event); !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("reusing the proof = %v, want ErrLadoPresenceReplayed", err)
	}
	other := proof
	other.Nonce = "other"
	if err := s.ApproveLadoIntegrations(ctx, again, other, event); !errors.Is(err, domain.ErrLadoIntegrationAlreadyApproved) {
		t.Fatalf("approving back again = %v, want ErrLadoIntegrationAlreadyApproved", err)
	}
}

// An approval revoked because a sibling PR's base moved is renewed in place
// when its head is approved again, together with the sibling's new head; one
// revoked for its own base overlap is not.
func TestApproveLadoIntegrationsRenewsSiblingRevoked(t *testing.T) {
	s, _ := openLadoMultiRepoStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	back := mustWriteLadoRepoPR(t, s, exec.SessionID, "back", 1, domain.PRStateOpen, ladoHead('a'), domain.MergeMergeable, at)
	front := mustWriteLadoRepoPR(t, s, exec.SessionID, "front", 2, domain.PRStateOpen, ladoHead('b'), domain.MergeMergeable, at)
	mustTrackLadoPR(t, s, exec.SessionID, back, "back", 0, true)
	mustTrackLadoPR(t, s, exec.SessionID, front, "front", 1, true)
	backRecord := ladoVerification(t, exec.SessionID, back, ladoHead('a'), at)
	frontRecord := ladoVerification(t, exec.SessionID, front, ladoHead('b'), at)
	mustUpsertLadoVerification(t, s, backRecord)
	mustUpsertLadoVerification(t, s, frontRecord)
	first := []domain.LadoIntegrationApproval{
		ladoIntegrationApproval(t, "approval-back", backRecord, domain.LadoIntegrationStatusPosted, at.Add(time.Minute)),
		ladoIntegrationApproval(t, "approval-front", frontRecord, domain.LadoIntegrationStatusPosted, at.Add(time.Minute)),
	}
	if err := s.ApproveLadoIntegrations(ctx, first, ladoIntegrationProof("nonce-1", first[0]), ladoIntegrationEvent(first[0])); err != nil {
		t.Fatalf("approve: %v", err)
	}
	event := domain.LadoAuditEvent{At: at.Add(time.Hour), Actor: "lado", Action: domain.LadoAuditIntegrationRevoked, SubjectKind: "execution", SubjectID: string(exec.SessionID)}
	for id, reason := range map[string]string{"approval-back": domain.LadoCodeBaseOverlap, "approval-front": domain.LadoIntegrationSiblingRevoked} {
		if ok, err := s.RevokeLadoIntegrationApproval(ctx, id, reason, at.Add(time.Hour), event); err != nil || !ok {
			t.Fatalf("revoke %s = (%v, %v)", id, ok, err)
		}
	}
	if read := mustReadLadoExecution(t, s, exec.SessionID); read.PRs[0].Integration.HeadApproved || read.PRs[1].Integration.HeadApproved {
		t.Fatalf("revoked approvals still count: %+v", read.PRs)
	}

	// The same back head stays refused.
	sameBack := ladoIntegrationApproval(t, "approval-back-2", backRecord, domain.LadoIntegrationStatusPending, at.Add(2*time.Hour))
	sameFront := ladoIntegrationApproval(t, "approval-front-2", frontRecord, domain.LadoIntegrationStatusPending, at.Add(2*time.Hour))
	if err := s.ApproveLadoIntegrations(ctx, []domain.LadoIntegrationApproval{sameBack, sameFront}, ladoIntegrationProof("nonce-2", sameBack), ladoIntegrationEvent(sameBack)); !errors.Is(err, domain.ErrLadoIntegrationRevoked) {
		t.Fatalf("approving the overlapped back head again = %v, want ErrLadoIntegrationRevoked", err)
	}

	// A new back head and the same front head approve together.
	mustWriteLadoRepoPR(t, s, exec.SessionID, "back", 1, domain.PRStateOpen, ladoHead('c'), domain.MergeMergeable, at.Add(time.Hour))
	newBackRecord := ladoVerification(t, exec.SessionID, back, ladoHead('c'), at.Add(time.Hour))
	mustUpsertLadoVerification(t, s, newBackRecord)
	newBack := ladoIntegrationApproval(t, "approval-back-3", newBackRecord, domain.LadoIntegrationStatusPending, at.Add(3*time.Hour))
	renewFront := ladoIntegrationApproval(t, "approval-front-3", frontRecord, domain.LadoIntegrationStatusPending, at.Add(3*time.Hour))
	renewFront.BaseTipSHA = ladoHead('f')
	if err := s.ApproveLadoIntegrations(ctx, []domain.LadoIntegrationApproval{newBack, renewFront}, ladoIntegrationProof("nonce-3", newBack), ladoIntegrationEvent(newBack)); err != nil {
		t.Fatalf("approving again: %v", err)
	}
	approvals, err := s.ListLadoIntegrationApprovals(ctx, exec.SessionID, front, ladoHead('b'))
	if err != nil || len(approvals) != 1 {
		t.Fatalf("front approvals = %+v (%v), want the one row renewed", approvals, err)
	}
	renewed := approvals[0]
	if renewed.ID != "approval-front" || renewed.Revoked() || renewed.RevokedReason != "" || renewed.ProofNonce != "nonce-3#1" ||
		!renewed.ApprovedAt.Equal(at.Add(3*time.Hour)) || renewed.BaseTipSHA != ladoHead('f') || renewed.StatusState != domain.LadoIntegrationStatusPending {
		t.Fatalf("renewed front approval = %+v", renewed)
	}
	read := mustReadLadoExecution(t, s, exec.SessionID)
	if !read.PRs[0].Integration.HeadApproved || !read.PRs[1].Integration.HeadApproved {
		t.Fatalf("PRs after approving again = %+v", read.PRs)
	}
}

// Review rounds count per PR; rounds recorded before they named a PR count
// for every PR of the execution.
func TestLadoExecutionPRReviewsCountPerPR(t *testing.T) {
	s, _ := openLadoMultiRepoStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	review := func(id string, round int) domain.LadoExecutionReview {
		return domain.LadoExecutionReview{ReviewRunID: id, ExecutionSessionID: exec.SessionID, HeadSHA: ladoHead('a'), Round: round, CreatedAt: at}
	}
	if err := s.AddLadoExecutionReview(ctx, review("legacy", 1)); err != nil {
		t.Fatalf("AddLadoExecutionReview: %v", err)
	}
	for _, tc := range []struct {
		id, pr string
		round  int
		ok     bool
	}{
		{"back-1", "back", 1, false},
		{"back-2", "back", 2, true},
		{"front-2", "front", 2, true},
		{"back-2b", "back", 2, false},
		{"back-3", "back", 3, true},
		{"back-4", "back", 4, false},
	} {
		err := s.AddLadoExecutionPRReview(ctx, review(tc.id, tc.round), tc.pr)
		if (err == nil) != tc.ok {
			t.Fatalf("round %d of %s = %v, want ok %v", tc.round, tc.pr, err, tc.ok)
		}
	}
	got, err := s.ListLadoExecutionPRReviews(ctx, exec.SessionID, "back")
	if err != nil || len(got) != 3 || got[0].ReviewRunID != "legacy" || got[2].ReviewRunID != "back-3" {
		t.Fatalf("back rounds = %+v (%v)", got, err)
	}
	if got, _ := s.ListLadoExecutionPRReviews(ctx, exec.SessionID, "front"); len(got) != 2 {
		t.Fatalf("front rounds = %+v", got)
	}
}

// The resolver works on the first tracked PR in merge order that conflicts,
// with its source branch, and its row records that PR.
func TestListLadoExecutionsToResolvePicksTheTrackedPR(t *testing.T) {
	s, _ := openLadoMultiRepoStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, true)
	back := mustWriteLadoRepoPR(t, s, exec.SessionID, "back", 1, domain.PRStateOpen, ladoHead('a'), domain.MergeMergeable, at)
	front := mustWriteLadoRepoPR(t, s, exec.SessionID, "front", 2, domain.PRStateOpen, ladoHead('b'), domain.MergeConflicting, at)
	// The newest PR is mergeable, but a tracked one conflicts.
	newest := mustWriteLadoRepoPR(t, s, exec.SessionID, "back", 3, domain.PRStateOpen, ladoHead('c'), domain.MergeMergeable, at.Add(time.Hour))
	mustTrackLadoPR(t, s, exec.SessionID, back, "back", 0, true)
	if got := ladoResolveCandidates(t, s); len(got) != 0 {
		t.Fatalf("candidates with only back tracked = %+v", got)
	}
	mustTrackLadoPR(t, s, exec.SessionID, front, "front", 1, true)
	candidate, ok := ladoResolveCandidates(t, s)[exec.SessionID]
	if !ok || candidate.Execution.PR.URL != front || candidate.PRSourceBranch != "front-2" || !candidate.Conflicting() || len(candidate.Execution.PRs) != 2 {
		t.Fatalf("candidate = %+v (%v), want front from front-2", candidate, ok)
	}
	if ok, err := s.UpsertLadoPRResolve(ctx, domain.LadoPRResolve{
		ExecutionSessionID: exec.SessionID, PRURL: front, State: domain.LadoResolveResolving, UpdatedAt: at,
	}); err != nil || !ok {
		t.Fatalf("UpsertLadoPRResolve = (%v, %v)", ok, err)
	}
	if r := mustLadoPRResolve(t, s, exec.SessionID); r.PRURL != front {
		t.Fatalf("resolve PR = %q", r.PRURL)
	}
	// Back conflicts too, but the resolve row stays on front.
	mustWriteLadoRepoPR(t, s, exec.SessionID, "back", 1, domain.PRStateOpen, ladoHead('a'), domain.MergeConflicting, at.Add(2*time.Hour))
	candidate = ladoResolveCandidates(t, s)[exec.SessionID]
	if candidate.Execution.PR.URL != front || candidate.Execution.Resolve.PRURL != front {
		t.Fatalf("candidate with a resolve of front = %+v", candidate.Execution)
	}
	_ = newest
}

// A parts run in two repositories: the first repository's values are the
// run's own, each part has a row per repository, the part's own SHA fields
// follow its first repository, a merge in one repository moves that
// repository's head only from the head it read and, in the first, the run's
// head with it; a conflicted repository round-trips. A run started with one
// repository reads as that one.
func TestLadoPartRunReposInTheStore(t *testing.T) {
	s, _ := openLadoMultiRepoStore(t)
	ctx := context.Background()
	now := ladoQueueAt.Add(time.Hour)
	id := mustQueuedLadoTask(t, s, "1", "mer", ladoQueueAt)
	entry := mustClaimLadoQueue(t, s, now, 1, ladoTokens("t"), id)[0]
	backBase, frontBase := ladoHead('a'), ladoHead('b')
	run := domain.LadoPartRun{
		QueueEntryID: id, TaskID: "lado-task-1", ProjectID: "mer", PlanRevisionID: "lado-plan-1", RepoDir: "/ignored",
		Branch: "lado/task-1/integration", BaseSHA: "ignored", HeadSHA: "ignored", StartedAt: now,
	}
	ahead := 2
	repos := []domain.LadoPartRunRepo{
		{Relative: "front", Position: 1, Dir: "/w/front", BaseSHA: frontBase, HeadSHA: frontBase, BaseBranch: "main", BaseRemote: "origin"},
		{Relative: "back/", Position: 0, Dir: "/w/back", BaseSHA: backBase, HeadSHA: backBase, BaseBranch: "develop", BaseRemote: "origin", DispatchLocalBaseSHA: backBase, BaseCommitsAhead: &ahead},
	}
	parts := []domain.LadoTaskPart{
		{PartID: "a", Title: "A", Phase: 1, Steps: []int{1}, AffectedPaths: []string{"back/**"}},
		{PartID: "b", Title: "B", Position: 1, Phase: 1, Steps: []int{2}, AffectedPaths: []string{"front/**"}},
	}
	for name, bad := range map[string][]domain.LadoPartRunRepo{
		"no repository":       nil,
		"no position 0":       {{Relative: "back", Position: 1, Dir: "/w/back", BaseSHA: backBase, HeadSHA: backBase}},
		"a repeated position": {repos[1], {Relative: "front", Position: 0, Dir: "/w/front", BaseSHA: frontBase, HeadSHA: frontBase}},
		"a repeated folder":   {repos[1], {Relative: "back", Position: 1, Dir: "/w/back", BaseSHA: backBase, HeadSHA: backBase}},
	} {
		if err := s.StartLadoPartRunRepos(ctx, entry.LeaseToken, run, bad, parts, ladoPartsEvent("parts.started")); err == nil {
			t.Fatalf("starting with %s was accepted", name)
		}
	}
	if err := s.StartLadoPartRunRepos(ctx, "another-token", run, repos, parts, ladoPartsEvent("parts.started")); !errors.Is(err, domain.ErrLadoLeaseLost) {
		t.Fatalf("StartLadoPartRunRepos with another token = %v, want ErrLadoLeaseLost", err)
	}
	if err := s.StartLadoPartRunRepos(ctx, entry.LeaseToken, run, repos, parts, ladoPartsEvent("parts.started")); err != nil {
		t.Fatalf("StartLadoPartRunRepos: %v", err)
	}
	stored, ok, err := s.LadoPartRun(ctx, id)
	if err != nil || !ok || stored.RepoDir != "/w/back" || stored.BaseSHA != backBase || stored.HeadSHA != backBase || stored.BaseBranch != "develop" ||
		stored.DispatchLocalBaseSHA != backBase || stored.BaseCommitsAhead == nil || *stored.BaseCommitsAhead != 2 {
		t.Fatalf("run = %+v (%v, %v), want the back repository's values", stored, ok, err)
	}
	runRepos, err := s.LadoPartRunRepos(ctx, id)
	if err != nil || len(runRepos) != 2 || runRepos[0].Relative != "back" || runRepos[1].Relative != "front" || runRepos[1].Dir != "/w/front" || runRepos[1].Changed() {
		t.Fatalf("run repositories = %+v (%v)", runRepos, err)
	}
	partRepos, err := s.LadoTaskPartRepos(ctx, id)
	if err != nil || len(partRepos) != 2 || len(partRepos["a"]) != 2 || partRepos["a"][0].Relative != "back" || partRepos["b"][1].Relative != "front" {
		t.Fatalf("part repositories = %+v (%v)", partRepos, err)
	}

	stored2, err := s.LadoTaskParts(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	partA := stored2[0]
	partA.State, partA.Attempts, partA.Branch = domain.LadoPartRunning, 1, "lado/task-1/a"
	starts := []domain.LadoTaskPartRepo{{Relative: "back", StartSHA: backBase}, {Relative: "front", StartSHA: frontBase}}
	if ok, err := s.SetLadoTaskPartRepos(ctx, partA, domain.LadoPartWaiting, []domain.LadoTaskPartRepo{{Relative: "elsewhere"}}); err == nil || ok {
		t.Fatalf("setting a repository of no run's = (%v, %v)", ok, err)
	}
	if ok, err := s.SetLadoTaskPartRepos(ctx, partA, domain.LadoPartWaiting, starts); err != nil || !ok {
		t.Fatalf("SetLadoTaskPartRepos = (%v, %v)", ok, err)
	}
	if ok, err := s.SetLadoTaskPartRepos(ctx, partA, domain.LadoPartWaiting, starts); err != nil || ok {
		t.Fatalf("SetLadoTaskPartRepos from a stale state = (%v, %v)", ok, err)
	}
	stored2, _ = s.LadoTaskParts(ctx, id)
	if stored2[0].StartSHA != backBase || stored2[0].State != domain.LadoPartRunning {
		t.Fatalf("part a = %+v, want back's start", stored2[0])
	}

	// Part a merges into front (not the first repository), then into back.
	partA = stored2[0]
	partA.State = domain.LadoPartMerging
	frontMerged, backMerged := ladoHead('c'), ladoHead('d')
	front := domain.LadoTaskPartRepo{Relative: "front", StartSHA: frontBase, MergeFromSHA: frontBase, MergeTipSHA: ladoHead('e'), MergedSHA: frontMerged}
	event := ladoPartsEvent("part.merged")
	if ok, err := s.MergeLadoTaskPartRepo(ctx, partA, domain.LadoPartRunning, front, backBase, &event); err != nil || ok {
		t.Fatalf("merging front from another head = (%v, %v)", ok, err)
	}
	if ok, err := s.MergeLadoTaskPartRepo(ctx, partA, domain.LadoPartRunning, front, frontBase, &event); err != nil || !ok {
		t.Fatalf("merge front = (%v, %v)", ok, err)
	}
	if stored, _, _ := s.LadoPartRun(ctx, id); stored.HeadSHA != backBase {
		t.Fatalf("a front merge moved the run's head to %s", stored.HeadSHA)
	}
	backRepo := domain.LadoTaskPartRepo{Relative: "back", StartSHA: backBase, MergeFromSHA: backBase, MergeTipSHA: ladoHead('f'), MergedSHA: backMerged}
	partA.State = domain.LadoPartMerged
	if ok, err := s.MergeLadoTaskPartRepo(ctx, partA, domain.LadoPartMerging, backRepo, backBase, nil); err != nil || !ok {
		t.Fatalf("merge back = (%v, %v)", ok, err)
	}
	stored, _, _ = s.LadoPartRun(ctx, id)
	runRepos, _ = s.LadoPartRunRepos(ctx, id)
	if stored.HeadSHA != backMerged || runRepos[0].HeadSHA != backMerged || runRepos[1].HeadSHA != frontMerged || !runRepos[1].Changed() {
		t.Fatalf("heads after the merges = run %s, repos %+v", stored.HeadSHA, runRepos)
	}
	partRepos, _ = s.LadoTaskPartRepos(ctx, id)
	if partRepos["a"][0] != backRepo || partRepos["a"][1] != front {
		t.Fatalf("part a repositories = %+v", partRepos["a"])
	}
	stored2, _ = s.LadoTaskParts(ctx, id)
	if stored2[0].State != domain.LadoPartMerged || stored2[0].MergedSHA != backMerged || stored2[0].MergeTipSHA != ladoHead('f') {
		t.Fatalf("part a = %+v", stored2[0])
	}

	// Part b conflicts in front.
	partB := stored2[1]
	partB.State, partB.MergeRepo = domain.LadoPartConflict, "front"
	if ok, err := s.UpdateLadoTaskPart(ctx, partB, domain.LadoPartWaiting); err != nil || !ok {
		t.Fatalf("UpdateLadoTaskPart = (%v, %v)", ok, err)
	}
	if stored2, _ = s.LadoTaskParts(ctx, id); stored2[1].MergeRepo != "front" {
		t.Fatalf("part b merge repo = %q", stored2[1].MergeRepo)
	}

	// The single-repository merge keeps the first repository's row the same.
	partB.State, partB.MergedSHA, partB.MergeTipSHA = domain.LadoPartMerged, ladoHead('9'), ladoHead('8')
	if ok, err := s.MergeLadoTaskPart(ctx, partB, domain.LadoPartConflict, backMerged, ladoPartsEvent("part.merged")); err != nil || !ok {
		t.Fatalf("MergeLadoTaskPart = (%v, %v)", ok, err)
	}
	runRepos, _ = s.LadoPartRunRepos(ctx, id)
	partRepos, _ = s.LadoTaskPartRepos(ctx, id)
	if runRepos[0].HeadSHA != ladoHead('9') || partRepos["b"][0].MergedSHA != ladoHead('9') || partRepos["b"][1].MergedSHA != "" {
		t.Fatalf("after a single-repository merge: repos %+v, part b %+v", runRepos, partRepos["b"])
	}
}

// A run started before runs had repositories reads as one repository with an
// empty folder, from the run's and the parts' own columns.
func TestLadoPartRunReposReadAnOldRunAsOne(t *testing.T) {
	s, _ := openLadoMultiRepoStore(t)
	ctx := context.Background()
	now := ladoQueueAt.Add(time.Hour)
	id := mustQueuedLadoTask(t, s, "1", "mer", ladoQueueAt)
	entry := mustClaimLadoQueue(t, s, now, 1, ladoTokens("t"), id)[0]
	base := ladoHead('a')
	run := domain.LadoPartRun{
		QueueEntryID: id, TaskID: "lado-task-1", ProjectID: "mer", PlanRevisionID: "lado-plan-1", RepoDir: "/repo",
		Branch: "lado/task-1/integration", BaseSHA: base, HeadSHA: base, BaseBranch: "main", StartedAt: now,
	}
	if err := s.StartLadoPartRun(ctx, entry.LeaseToken, run, []domain.LadoTaskPart{{PartID: "a", Title: "A"}}, ladoPartsEvent("parts.started")); err != nil {
		t.Fatalf("StartLadoPartRun: %v", err)
	}
	parts, _ := s.LadoTaskParts(ctx, id)
	part := parts[0]
	part.State, part.StartSHA = domain.LadoPartRunning, base
	if ok, err := s.UpdateLadoTaskPart(ctx, part, domain.LadoPartWaiting); err != nil || !ok {
		t.Fatalf("UpdateLadoTaskPart = (%v, %v)", ok, err)
	}
	repos, err := s.LadoPartRunRepos(ctx, id)
	if err != nil || len(repos) != 1 || repos[0].Relative != "" || repos[0].Dir != "/repo" || repos[0].BaseSHA != base || repos[0].BaseBranch != "main" {
		t.Fatalf("old run repositories = %+v (%v)", repos, err)
	}
	partRepos, err := s.LadoTaskPartRepos(ctx, id)
	if err != nil || len(partRepos["a"]) != 1 || partRepos["a"][0].StartSHA != base {
		t.Fatalf("old run part repositories = %+v (%v)", partRepos, err)
	}
	if repos, err := s.LadoPartRunRepos(ctx, "no-such-entry"); err != nil || len(repos) != 0 {
		t.Fatalf("repositories of no run = %+v (%v)", repos, err)
	}
}
