package store_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// mustWriteLadoMergeablePR writes PR #number of session as AO's SCM observer
// does, open with head, from branch feat-<number> into main, with
// mergeability, last updated at at.
func mustWriteLadoMergeablePR(t *testing.T, s *sqlite.Store, session domain.SessionID, number int, state domain.PRState, head string, mergeability domain.Mergeability, at time.Time) string {
	t.Helper()
	url := fmt.Sprintf("https://github.com/acme/demo/pull/%d", number)
	if err := s.WriteSCMObservation(context.Background(), domain.PullRequest{
		URL: url, SessionID: session, Number: number, Draft: state == domain.PRStateDraft,
		Merged: state == domain.PRStateMerged, Closed: state == domain.PRStateClosed,
		HeadSHA: head, SourceBranch: fmt.Sprintf("feat-%d", number), TargetBranch: "main",
		Title: fmt.Sprintf("Change %d", number), Mergeability: mergeability, UpdatedAt: at,
	}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write PR %d: %v", number, err)
	}
	return url
}

// ladoResolveCandidates lists the executions to resolve by session.
func ladoResolveCandidates(t *testing.T, s *sqlite.Store) map[domain.SessionID]domain.LadoResolveCandidate {
	t.Helper()
	got, err := s.ListLadoExecutionsToResolve(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListLadoExecutionsToResolve: %v", err)
	}
	bySession := make(map[domain.SessionID]domain.LadoResolveCandidate, len(got))
	for _, c := range got {
		bySession[c.Execution.SessionID] = c
	}
	return bySession
}

func mustLadoPRResolve(t *testing.T, s *sqlite.Store, session domain.SessionID) domain.LadoPRResolve {
	t.Helper()
	r, ok, err := s.LadoPRResolve(context.Background(), session)
	if err != nil || !ok {
		t.Fatalf("LadoPRResolve %s = (%v, %v)", session, ok, err)
	}
	return r
}

func ladoWorkers(t *testing.T, s *sqlite.Store, now time.Time) int {
	t.Helper()
	load, err := s.LadoRunLoad(context.Background(), now)
	if err != nil {
		t.Fatalf("LadoRunLoad: %v", err)
	}
	return load.Workers
}

// One resolve end to end in the store: the conflicting PR of an ended
// execution is a candidate, its row is created, a worker spawn is requested,
// granted and linked as a merge execution of the entry, and the row writes
// over the version it read.
func TestLadoPRResolveLifecycle(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, true)
	entry := exec.QueueEntryID
	if _, ok, err := s.LadoPRResolve(ctx, exec.SessionID); err != nil || ok {
		t.Fatalf("resolve before any = (%v, %v), want none", ok, err)
	}
	if got := ladoResolveCandidates(t, s); len(got) != 0 {
		t.Fatalf("candidates without a PR = %v", got)
	}

	// A mergeable PR is no candidate; a conflicting one is.
	mustWriteLadoMergeablePR(t, s, exec.SessionID, 7, domain.PRStateOpen, ladoHead('a'), domain.MergeMergeable, at)
	if got := ladoResolveCandidates(t, s); len(got) != 0 {
		t.Fatalf("candidates with a mergeable PR = %v", got)
	}
	prURL := mustWriteLadoMergeablePR(t, s, exec.SessionID, 7, domain.PRStateOpen, ladoHead('a'), domain.MergeConflicting, at.Add(time.Minute))
	c, ok := ladoResolveCandidates(t, s)[exec.SessionID]
	if !ok {
		t.Fatal("the conflicting PR's execution is no candidate")
	}
	if !c.Conflicting() || c.SessionLive || c.ResolveSessionLive || c.ProjectID != "mer" || c.PRSourceBranch != "feat-7" ||
		c.Resolve.Version != 0 || c.Execution.PR.URL != prURL || c.Execution.PR.Mergeability != domain.MergeConflicting {
		t.Fatalf("candidate = %+v", c)
	}

	// The row is created once.
	start := domain.LadoPRResolve{ExecutionSessionID: exec.SessionID, StartHeadSHA: ladoHead('a'), BaseTipSHA: ladoHead('e'), UpdatedAt: at.Add(2 * time.Minute)}
	for i, want := range []bool{true, false} {
		if written, err := s.UpsertLadoPRResolve(ctx, start); err != nil || written != want {
			t.Fatalf("create #%d = (%v, %v), want %v", i+1, written, err, want)
		}
	}
	if _, err := s.UpsertLadoPRResolve(ctx, domain.LadoPRResolve{ExecutionSessionID: "nobody", UpdatedAt: at}); err == nil {
		t.Fatal("a resolve of no execution was accepted")
	}
	if _, err := s.UpsertLadoPRResolve(ctx, domain.LadoPRResolve{ExecutionSessionID: exec.SessionID, State: "merged", UpdatedAt: at}); err == nil {
		t.Fatal("an unknown state was accepted")
	}
	row := mustLadoPRResolve(t, s, exec.SessionID)
	if row.Version != 1 || row.TaskID != exec.TaskID || row.QueueEntryID != entry || row.State != domain.LadoResolveNone ||
		row.BaseTipSHA != ladoHead('e') || !row.CreatedAt.Equal(start.UpdatedAt) {
		t.Fatalf("created resolve = %+v", row)
	}

	// A spawn request numbers the worker and holds the row.
	workers := ladoWorkers(t, s, at)
	now := at.Add(3 * time.Minute)
	request, ok, err := s.SetLadoResolveSpawnRequest(ctx, exec.SessionID, now, now.Add(2*time.Minute))
	if err != nil || !ok || request != domain.LadoResolveClientRequestID(entry, 1) {
		t.Fatalf("SetLadoResolveSpawnRequest = (%q, %v, %v)", request, ok, err)
	}
	if again, ok, err := s.SetLadoResolveSpawnRequest(ctx, exec.SessionID, now, now.Add(2*time.Minute)); err != nil || ok || again != "" {
		t.Fatalf("a second request while one holds = (%q, %v, %v)", again, ok, err)
	}
	if _, ok, err := s.SetLadoResolveSpawnRequest(ctx, "nobody", now, now.Add(time.Minute)); err != nil || ok {
		t.Fatalf("a request without a row = (%v, %v)", ok, err)
	}
	if got := ladoWorkers(t, s, now); got != workers+1 {
		t.Fatalf("workers while the spawn holds = %d, want %d", got, workers+1)
	}
	if got := ladoWorkers(t, s, now.Add(2*time.Minute)); got != workers {
		t.Fatalf("workers once the spawn expired = %d, want %d", got, workers)
	}

	// The worker gate grants the request until it expires.
	grant, err := s.LadoPartSpawnGrant(ctx, request, now)
	if err != nil || !grant.Active || grant.ProjectID != "mer" || grant.Hash == "" {
		t.Fatalf("grant of the request = (%+v, %v)", grant, err)
	}
	for name, probe := range map[string]struct {
		id  string
		now time.Time
	}{
		"an expired request": {request, now.Add(2 * time.Minute)},
		"another number":     {domain.LadoResolveClientRequestID(entry, 2), now},
		"another entry":      {domain.LadoResolveClientRequestID("lado-queue-9", 1), now},
	} {
		if grant, err := s.LadoPartSpawnGrant(ctx, probe.id, probe.now); err != nil || grant.Active {
			t.Fatalf("grant of %s = (%+v, %v), want inactive", name, grant, err)
		}
	}

	// Linking takes the request: a wrong one loses, the right one stores a
	// merge execution of the entry and makes the row resolving.
	worker := mustLadoSession(t, s, "mer", false).ID
	link := domain.LadoExecution{SessionID: worker, StartedAt: now.Add(time.Minute), Branch: domain.LadoResolveBranch(exec.TaskID, entry, 1), StartSHA: ladoHead('a')}
	if err := s.LinkLadoResolveExecution(ctx, domain.LadoResolveClientRequestID(entry, 2), link); !errors.Is(err, domain.ErrLadoLeaseLost) {
		t.Fatalf("link with another request = %v, want ErrLadoLeaseLost", err)
	}
	if _, ok, err := s.LadoExecution(ctx, worker); err != nil || ok {
		t.Fatalf("a lost link stored the execution (%v, %v)", ok, err)
	}
	for i := range 2 {
		if err := s.LinkLadoResolveExecution(ctx, request, link); err != nil {
			t.Fatalf("link #%d: %v", i+1, err)
		}
	}
	merge, ok, err := s.LadoExecution(ctx, worker)
	if err != nil || !ok || merge.Role != domain.LadoRoleMerge || merge.PartID != "" || merge.QueueEntryID != entry ||
		merge.TaskID != exec.TaskID || merge.Branch != link.Branch || merge.StartSHA != ladoHead('a') {
		t.Fatalf("resolve execution = %+v (%v, %v)", merge, ok, err)
	}
	row = mustLadoPRResolve(t, s, exec.SessionID)
	if row.State != domain.LadoResolveResolving || row.SessionID != worker || row.SpawnRequest != "" || !row.SpawnExpiresAt.IsZero() ||
		row.Spawns != 1 || row.Version != 2 {
		t.Fatalf("linked resolve = %+v", row)
	}
	if grant, err := s.LadoPartSpawnGrant(ctx, request, now); err != nil || grant.Active {
		t.Fatalf("grant after the link = (%+v, %v), want inactive", grant, err)
	}
	if err := s.LinkLadoResolveExecution(ctx, request, domain.LadoExecution{SessionID: mustLadoSession(t, s, "mer", false).ID, StartedAt: now}); !errors.Is(err, domain.ErrLadoLeaseLost) {
		t.Fatalf("a second worker for a used request = %v, want ErrLadoLeaseLost", err)
	}

	// Execution reads carry the resolve, and the candidate its live worker.
	read, ok, err := s.LadoExecution(ctx, exec.SessionID)
	if err != nil || !ok || read.Resolve != (domain.LadoExecutionResolve{State: domain.LadoResolveResolving, SessionID: worker}) ||
		read.PR.Mergeability != domain.MergeConflicting {
		t.Fatalf("execution read = %+v (%v, %v)", read, ok, err)
	}
	c = ladoResolveCandidates(t, s)[exec.SessionID]
	if !c.ResolveSessionLive || c.Resolve.Version != 2 {
		t.Fatalf("candidate with a worker = %+v", c)
	}

	// Writes go over the version read only.
	stale := row
	stale.Version = 1
	stale.UpdatedAt = now.Add(5 * time.Minute)
	if written, err := s.UpsertLadoPRResolve(ctx, stale); err != nil || written {
		t.Fatalf("a write over an old version = (%v, %v)", written, err)
	}
	failed := row
	failed.Attempts, failed.Error, failed.DoneSeenAt, failed.UpdatedAt = 1, "push rejected", now.Add(4*time.Minute), now.Add(5*time.Minute)
	if written, err := s.UpsertLadoPRResolve(ctx, failed); err != nil || !written {
		t.Fatalf("a write over the version read = (%v, %v)", written, err)
	}
	row = mustLadoPRResolve(t, s, exec.SessionID)
	if row.Attempts != 1 || row.Error != "push rejected" || !row.DoneSeenAt.Equal(failed.DoneSeenAt) || row.Version != 3 || row.Spawns != 1 {
		t.Fatalf("written resolve = %+v", row)
	}

	// The next worker takes the next number; an expired request is listed
	// until it is cleared.
	later := now.Add(10 * time.Minute)
	next, ok, err := s.SetLadoResolveSpawnRequest(ctx, exec.SessionID, later, later.Add(time.Minute))
	if err != nil || !ok || next != domain.LadoResolveClientRequestID(entry, 2) {
		t.Fatalf("the next request = (%q, %v, %v)", next, ok, err)
	}
	expired := func(at time.Time) []domain.SessionID {
		t.Helper()
		rows, err := s.ListExpiredLadoResolveSpawns(ctx, at)
		if err != nil {
			t.Fatalf("ListExpiredLadoResolveSpawns: %v", err)
		}
		var got []domain.SessionID
		for _, r := range rows {
			got = append(got, r.ExecutionSessionID)
		}
		return got
	}
	if got := expired(later); len(got) != 0 {
		t.Fatalf("expired before the expiry = %v", got)
	}
	if got := expired(later.Add(time.Minute)); !slices.Equal(got, []domain.SessionID{exec.SessionID}) {
		t.Fatalf("expired at the expiry = %v", got)
	}
	if cleared, err := s.ClearLadoResolveSpawnRequest(ctx, exec.SessionID, request); err != nil || cleared {
		t.Fatalf("clearing an old request = (%v, %v)", cleared, err)
	}
	if cleared, err := s.ClearLadoResolveSpawnRequest(ctx, exec.SessionID, next); err != nil || !cleared {
		t.Fatalf("clearing the request = (%v, %v)", cleared, err)
	}
	if got := expired(later.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("expired after clearing = %v", got)
	}

	// A row with a state stays a candidate after the PR merged, and leaves
	// once its state is cleared.
	mustWriteLadoMergeablePR(t, s, exec.SessionID, 7, domain.PRStateMerged, ladoHead('a'), domain.MergeConflicting, at.Add(time.Hour))
	if _, ok := ladoResolveCandidates(t, s)[exec.SessionID]; !ok {
		t.Fatal("a resolving row left the candidates when its PR merged")
	}
	row = mustLadoPRResolve(t, s, exec.SessionID)
	row.State, row.UpdatedAt = domain.LadoResolveNone, later
	if written, err := s.UpsertLadoPRResolve(ctx, row); err != nil || !written {
		t.Fatalf("clear the state = (%v, %v)", written, err)
	}
	if got := ladoResolveCandidates(t, s); len(got) != 0 {
		t.Fatalf("candidates after the merge = %v", got)
	}
}

// An open PR whose current head's record failed with base_overlap is a
// candidate; a record of an older head is not.
func TestListLadoExecutionsToResolveReadsBaseOverlap(t *testing.T) {
	s := openLadoVerifyStore(t)
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	prURL := mustWriteLadoMergeablePR(t, s, exec.SessionID, 7, domain.PRStateOpen, ladoHead('a'), domain.MergeMergeable, at)
	overlap := mustLadoVerifyItem(t, domain.LadoVerifyBaseBranch, domain.LadoVerifyFail, domain.LadoCodeBaseOverlap, nil)
	mustUpsertLadoVerification(t, s, ladoVerification(t, exec.SessionID, prURL, ladoHead('a'), at, overlap))

	c, ok := ladoResolveCandidates(t, s)[exec.SessionID]
	if !ok || !c.Conflicting() || !c.SessionLive || !c.Execution.Verification.BaseOverlap {
		t.Fatalf("candidate = %+v (%v)", c, ok)
	}
	read, _, err := s.LadoExecution(context.Background(), exec.SessionID)
	if err != nil || !read.Verification.BaseOverlap || read.PR.Mergeability != domain.MergeMergeable {
		t.Fatalf("execution read = %+v (%v)", read, err)
	}

	mustWriteLadoMergeablePR(t, s, exec.SessionID, 7, domain.PRStateOpen, ladoHead('b'), domain.MergeMergeable, at.Add(time.Minute))
	if got := ladoResolveCandidates(t, s); len(got) != 0 {
		t.Fatalf("candidates after a new head = %v", got)
	}
}

// AO keeps observing the open and draft PRs of LADO executions, ended or
// not; a merged PR and an execution without a PR are not observed.
func TestLadoExecutionSessionsToObserve(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	open := mustLadoExecution(t, s, "1", at, true)
	merged := mustLadoExecution(t, s, "2", at, true)
	mustLadoExecution(t, s, "3", at, false)
	draft := mustLadoExecution(t, s, "4", at, false)
	mustWriteLadoMergeablePR(t, s, open.SessionID, 1, domain.PRStateOpen, ladoHead('a'), domain.MergeMergeable, at)
	mustWriteLadoMergeablePR(t, s, merged.SessionID, 2, domain.PRStateMerged, ladoHead('b'), domain.MergeMergeable, at)
	mustWriteLadoMergeablePR(t, s, draft.SessionID, 4, domain.PRStateDraft, ladoHead('d'), domain.MergeMergeable, at)
	got, err := s.LadoExecutionSessionsToObserve(ctx)
	want := []domain.SessionID{open.SessionID, draft.SessionID}
	slices.Sort(want)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("LadoExecutionSessionsToObserve = %v (%v), want %v", got, err, want)
	}
}

// What holds paths: a live execution until it timed out, its task was
// cancelled or its PR merged; a lease and a parts run as for the run load.
func TestLadoOverlapHolders(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	holders := func() []domain.LadoTaskID {
		t.Helper()
		tasks, err := s.LadoOverlapHolders(ctx, at)
		if err != nil {
			t.Fatalf("LadoOverlapHolders: %v", err)
		}
		var got []domain.LadoTaskID
		for _, task := range tasks {
			got = append(got, task.TaskID)
		}
		return got
	}
	live := mustLadoExecution(t, s, "1", at, false)
	merged := mustLadoExecution(t, s, "2", at, false)
	timedOut := mustLadoExecution(t, s, "3", at, false)
	cancelled := mustLadoExecution(t, s, "4", at, false)
	mustLadoExecution(t, s, "5", at, true)
	if got, want := holders(), []domain.LadoTaskID{live.TaskID, merged.TaskID, timedOut.TaskID, cancelled.TaskID}; !slices.Equal(got, want) {
		t.Fatalf("holders = %v, want %v", got, want)
	}

	mustWriteLadoMergeablePR(t, s, merged.SessionID, 2, domain.PRStateMerged, ladoHead('b'), domain.MergeMergeable, at)
	if _, err := s.MarkLadoExecutionTimedOut(ctx, timedOut.SessionID, domain.LadoAuditEvent{
		At: at, Actor: "lado", Action: "execution.timed_out", SubjectKind: "execution", SubjectID: string(timedOut.SessionID),
	}); err != nil {
		t.Fatalf("MarkLadoExecutionTimedOut: %v", err)
	}
	if _, _, err := s.CancelLadoTask(ctx, cancelled.TaskID, at); err != nil {
		t.Fatalf("CancelLadoTask: %v", err)
	}
	if got, want := holders(), []domain.LadoTaskID{live.TaskID}; !slices.Equal(got, want) {
		t.Fatalf("holders = %v, want %v", got, want)
	}
	// The run load still counts them: their sessions run.
	load, err := s.LadoRunLoad(ctx, at)
	if err != nil || len(load.Tasks) < 2 {
		t.Fatalf("run load = %+v (%v), want the running sessions", load, err)
	}
}

// The queued entries of other tasks of the project approved before an
// entry, still waiting for a worker.
func TestListLadoQueuedOverlapCandidates(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	seedProject(t, s, "other")
	first := mustQueuedLadoTask(t, s, "1", "mer", ladoQueueAt)
	second := mustQueuedLadoTask(t, s, "2", "mer", ladoQueueAt.Add(time.Minute))
	third := mustQueuedLadoTask(t, s, "3", "mer", ladoQueueAt.Add(2*time.Minute))
	mustQueuedLadoTask(t, s, "4", "other", ladoQueueAt)
	candidates := func(entry domain.LadoQueueEntryID) []domain.LadoTaskID {
		t.Helper()
		tasks, err := s.ListLadoQueuedOverlapCandidates(ctx, entry)
		if err != nil {
			t.Fatalf("ListLadoQueuedOverlapCandidates %s: %v", entry, err)
		}
		var got []domain.LadoTaskID
		for _, task := range tasks {
			if task.ProjectID != "mer" || task.PlanRevisionID == "" {
				t.Fatalf("candidate %+v", task)
			}
			got = append(got, task.TaskID)
		}
		return got
	}
	if got := candidates(first); len(got) != 0 {
		t.Fatalf("candidates of the first = %v", got)
	}
	if got, want := candidates(third), []domain.LadoTaskID{"lado-task-1", "lado-task-2"}; !slices.Equal(got, want) {
		t.Fatalf("candidates of the third = %v, want %v", got, want)
	}
	if _, _, err := s.CancelLadoTask(ctx, "lado-task-2", ladoQueueAt); err != nil {
		t.Fatalf("CancelLadoTask: %v", err)
	}
	// Dispatching the first takes it off the queue.
	claimed := mustClaimLadoQueue(t, s, ladoVerifyAt, 1, ladoTokens("overlap"), first)[0]
	if err := s.LinkLadoExecution(ctx, first, claimed.LeaseToken, domain.LadoExecution{SessionID: mustLadoSession(t, s, "mer", false).ID, StartedAt: ladoVerifyAt}); err != nil {
		t.Fatalf("LinkLadoExecution: %v", err)
	}
	if got := candidates(third); len(got) != 0 {
		t.Fatalf("candidates after cancelling and dispatching = %v", got)
	}
	_ = second
}

// Revoking an approval resets a GitHub status to post again, keeps a
// GitLab one, audits once, and the head cannot be approved again; the
// execution's head no longer counts as approved.
func TestRevokeLadoIntegrationApproval(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	prURL := mustWriteLadoMergeablePR(t, s, exec.SessionID, 7, domain.PRStateOpen, ladoHead('a'), domain.MergeMergeable, at)
	record := ladoVerification(t, exec.SessionID, prURL, ladoHead('a'), at)
	mustUpsertLadoVerification(t, s, record)
	approval := ladoIntegrationApproval(t, "approval-1", record, domain.LadoIntegrationStatusPosted, at.Add(time.Minute))
	approval.StatusAttempts, approval.StatusPostedAt, approval.BaseTipSHA = 2, at.Add(2*time.Minute), ladoHead('e')
	mustApproveLadoIntegration(t, s, approval)
	got, ok := ladoApprovalOf(t, s, exec.SessionID, prURL, ladoHead('a'))
	if !ok || got.BaseTipSHA != ladoHead('e') || got.Revoked() {
		t.Fatalf("approval = %+v (%v)", got, ok)
	}
	if read, _, _ := s.LadoExecution(ctx, exec.SessionID); !read.Integration.HeadApproved {
		t.Fatalf("execution before the revocation = %+v", read.Integration)
	}

	event := domain.LadoAuditEvent{At: at.Add(time.Hour), Actor: "lado", Action: domain.LadoAuditIntegrationRevoked, SubjectKind: "execution", SubjectID: string(exec.SessionID)}
	before := ladoAuditProbe(t, s)
	for i, want := range []bool{true, false} {
		if revoked, err := s.RevokeLadoIntegrationApproval(ctx, approval.ID, domain.LadoCodeBaseOverlap, at.Add(time.Hour), event); err != nil || revoked != want {
			t.Fatalf("revoke #%d = (%v, %v), want %v", i+1, revoked, err, want)
		}
	}
	if after := ladoAuditProbe(t, s); after != before+2 {
		t.Fatalf("revoking appended %d audit events, want 1", after-before-1)
	}
	if revoked, err := s.RevokeLadoIntegrationApproval(ctx, "approval-missing", "", at, event); err != nil || revoked {
		t.Fatalf("revoking an unknown approval = (%v, %v)", revoked, err)
	}
	approvals, err := s.ListLadoIntegrationApprovals(ctx, exec.SessionID, prURL, ladoHead('a'))
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approvals = %+v (%v)", approvals, err)
	}
	got = approvals[0]
	if !got.Revoked() || !got.RevokedAt.Equal(at.Add(time.Hour)) || got.RevokedReason != domain.LadoCodeBaseOverlap ||
		got.StatusState != domain.LadoIntegrationStatusPending || got.StatusAttempts != 0 || got.StatusError != "" {
		t.Fatalf("revoked GitHub approval = %+v", got)
	}
	if _, ok := domain.LadoApprovalFor(approvals, "main"); ok {
		t.Fatal("LadoApprovalFor took a revoked approval")
	}
	if read, _, _ := s.LadoExecution(ctx, exec.SessionID); read.Integration.HeadApproved {
		t.Fatalf("execution after the revocation = %+v", read.Integration)
	}
	again := approval
	again.ID = "approval-2"
	if err := s.ApproveLadoIntegration(ctx, again, ladoIntegrationProof("nonce-approval-2", again), ladoIntegrationEvent(again)); !errors.Is(err, domain.ErrLadoIntegrationRevoked) {
		t.Fatalf("approving the revoked head again = %v, want ErrLadoIntegrationRevoked", err)
	}

	// A GitLab approval has no status to post, before or after.
	gitlab := mustLadoExecution(t, s, "2", at, false)
	glURL := mustWriteLadoMergeablePR(t, s, gitlab.SessionID, 8, domain.PRStateOpen, ladoHead('b'), domain.MergeMergeable, at)
	glRecord := ladoVerification(t, gitlab.SessionID, glURL, ladoHead('b'), at)
	mustUpsertLadoVerification(t, s, glRecord)
	glApproval := ladoIntegrationApproval(t, "approval-3", glRecord, domain.LadoIntegrationStatusNotApplicable, at.Add(time.Minute))
	mustApproveLadoIntegration(t, s, glApproval)
	if revoked, err := s.RevokeLadoIntegrationApproval(ctx, glApproval.ID, domain.LadoCodeBaseOverlap, at.Add(time.Hour), event); err != nil || !revoked {
		t.Fatalf("revoke the GitLab approval = (%v, %v)", revoked, err)
	}
	glApprovals, err := s.ListLadoIntegrationApprovals(ctx, gitlab.SessionID, glURL, ladoHead('b'))
	if err != nil || len(glApprovals) != 1 || !glApprovals[0].Revoked() || glApprovals[0].StatusState != domain.LadoIntegrationStatusNotApplicable {
		t.Fatalf("revoked GitLab approval = %+v (%v)", glApprovals, err)
	}
}
