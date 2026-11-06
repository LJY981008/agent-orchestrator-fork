package ladoverify

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// #64: a task that changes several repositories of a workspace opens one PR
// per repository. The loop pins each (track.go), verifies and settles each
// on its own, the task's PRs are approved together, merge in the plan's
// order and lose their approvals together.

// verifyMulti is what verifyStore keeps for an execution with several PRs:
// its session's PRs, newest first, and the PRs it tracks. Only a session
// whose PRs a test set (setSessionPRs) has them.
type verifyMulti struct {
	prs     map[domain.SessionID][]domain.LadoExecutionPR
	tracked map[domain.SessionID][]*verifyTracked
	// reviewPR names the PR of each review round recorded for one.
	reviewPR map[string]string
	// trackCalls counts TrackLadoExecutionPR calls that tracked a PR.
	trackCalls int
	// awaitingSweeps counts SettleLadoExecutionsAwaitingRepos calls; the
	// store tests cover what they settle.
	awaitingSweeps int
}

// verifyTracked is one tracking row: settled is the state the loop settled
// the PR in, empty while it has not.
type verifyTracked struct {
	url, repo string
	position  int
	replaced  bool
	settled   domain.PRState
}

func (s *verifyStore) multiLocked(session domain.SessionID) bool {
	_, ok := s.multi.prs[session]
	return ok
}

// setSessionPRs sets the session's PRs, newest first.
func (s *verifyStore) setSessionPRs(session domain.SessionID, prs ...domain.LadoExecutionPR) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.multi.prs == nil {
		s.multi.prs = map[domain.SessionID][]domain.LadoExecutionPR{}
	}
	s.multi.prs[session] = slices.Clone(prs)
}

// updatePR changes a PR of a session as AO observes it.
func (s *verifyStore) updatePR(url string, change func(*domain.LadoExecutionPR)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, prs := range s.multi.prs {
		for i := range prs {
			if prs[i].URL == url {
				change(&prs[i])
			}
		}
	}
}

func (s *verifyStore) prLocked(session domain.SessionID, url string) (domain.LadoExecutionPR, bool) {
	for _, pr := range s.multi.prs[session] {
		if pr.URL == url {
			return pr, true
		}
	}
	return domain.LadoExecutionPR{}, false
}

// sessionPRLocked answers AO's row of a session's PR.
func (s *verifyStore) sessionPRLocked(url string) (domain.PullRequest, bool) {
	for session, prs := range s.multi.prs {
		for _, pr := range prs {
			if pr.URL != url {
				continue
			}
			repo, _ := domain.LadoPRRepo(url)
			return domain.PullRequest{
				URL: url, SessionID: session, Number: pr.Number, HeadSHA: pr.HeadSHA, TargetBranch: pr.TargetBranch,
				Title: pr.Title, Merged: pr.Merged, Closed: pr.Closed, Provider: string(repo.Provider), Host: repo.Host, Repo: repo.Native,
				BaseSHA: s.baseSHAs[url],
			}, true
		}
	}
	return domain.PullRequest{}, false
}

func (s *verifyStore) liveTrackedLocked(session domain.SessionID) []*verifyTracked {
	var live []*verifyTracked
	for _, row := range s.multi.tracked[session] {
		if !row.replaced {
			live = append(live, row)
		}
	}
	slices.SortStableFunc(live, func(a, b *verifyTracked) int { return a.position - b.position })
	return live
}

func (s *verifyStore) trackedURLLocked(session domain.SessionID, url string) bool {
	return slices.ContainsFunc(s.multi.tracked[session], func(row *verifyTracked) bool { return row.url == url })
}

// integrationLocked is the approval of the PR's head into its target, as
// the store reads it.
func (s *verifyStore) integrationLocked(session domain.SessionID, pr domain.LadoExecutionPR) domain.LadoExecutionIntegration {
	var approvals []domain.LadoIntegrationApproval
	for _, a := range s.approvals {
		if a.ExecutionSessionID == session && a.PRURL == pr.URL && a.HeadSHA == pr.HeadSHA {
			approvals = append(approvals, a)
		}
	}
	approval, ok := domain.LadoApprovalFor(approvals, pr.TargetBranch)
	if !ok {
		return domain.LadoExecutionIntegration{}
	}
	return domain.LadoExecutionIntegration{HeadApproved: true, StatusState: approval.StatusState, MergedHeadApproved: pr.Merged}
}

// withTrackedLocked gives an execution whose session has PRs its tracked
// PRs, its PR (the first tracked one, else the newest) and its open
// untracked PRs, as the store does.
func (s *verifyStore) withTrackedLocked(exec domain.LadoExecution) domain.LadoExecution {
	if !s.multiLocked(exec.SessionID) {
		return exec
	}
	exec.PRs, exec.UntrackedPRs = nil, nil
	for _, row := range s.liveTrackedLocked(exec.SessionID) {
		pr, _ := s.prLocked(exec.SessionID, row.url)
		exec.PRs = append(exec.PRs, domain.LadoExecutionTrackedPR{
			PR: pr, Repo: row.repo, Position: row.position,
			Settled:     row.settled != "" && row.settled == prState(pr),
			Integration: s.integrationLocked(exec.SessionID, pr),
		})
	}
	prs := s.multi.prs[exec.SessionID]
	switch {
	case len(exec.PRs) > 0:
		exec.PR, exec.Integration = exec.PRs[0].PR, exec.PRs[0].Integration
	case len(prs) > 0:
		exec.PR = prs[0]
		exec.Integration = s.integrationLocked(exec.SessionID, prs[0])
	}
	for i, pr := range prs {
		if s.trackedURLLocked(exec.SessionID, pr.URL) || pr.Merged || pr.Closed || (len(exec.PRs) == 0 && i == 0) {
			continue
		}
		exec.UntrackedPRs = append(exec.UntrackedPRs, pr)
	}
	return exec
}

// toVerifyLocked lists an execution once per tracked PR to verify: open with
// a head, or merged or closed and not settled so.
func (s *verifyStore) toVerifyLocked(exec domain.LadoExecution) []domain.LadoExecution {
	full := s.withTrackedLocked(exec)
	toVerify := func(pr domain.LadoExecutionPR, settled bool) bool {
		if pr.Merged || pr.Closed {
			return !settled
		}
		return pr.HeadSHA != ""
	}
	if len(full.PRs) == 0 {
		if full.PR.URL != "" && toVerify(full.PR, s.settledPR[exec.SessionID] == settleKey(full.PR.URL, prState(full.PR))) {
			return []domain.LadoExecution{full}
		}
		return nil
	}
	var out []domain.LadoExecution
	for _, tracked := range full.PRs {
		if toVerify(tracked.PR, tracked.Settled) {
			out = append(out, withTrackedPR(full, tracked))
		}
	}
	return out
}

func (s *verifyStore) ListLadoExecutionPRsToTrack(_ context.Context, limit int) ([]domain.LadoExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.LadoExecution
	for _, exec := range s.executions {
		if !s.multiLocked(exec.SessionID) || len(out) == limit {
			continue
		}
		full := s.withTrackedLocked(exec)
		full.UntrackedPRs = nil
		for _, pr := range s.multi.prs[exec.SessionID] {
			if !s.trackedURLLocked(exec.SessionID, pr.URL) {
				full.UntrackedPRs = append(full.UntrackedPRs, pr)
			}
		}
		if len(full.UntrackedPRs) > 0 {
			out = append(out, full)
		}
	}
	return out, nil
}

func (s *verifyStore) TrackLadoExecutionPR(_ context.Context, session domain.SessionID, prURL, repo string, position int, _ time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.trackedURLLocked(session, prURL) || slices.ContainsFunc(s.liveTrackedLocked(session), func(row *verifyTracked) bool { return row.repo == repo }) {
		return false, nil
	}
	row := &verifyTracked{url: prURL, repo: repo, position: position}
	for _, state := range []domain.PRState{domain.PRStateMerged, domain.PRStateClosed} {
		if s.settledPR[session] == settleKey(prURL, state) {
			row.settled = state
		}
	}
	if s.multi.tracked == nil {
		s.multi.tracked = map[domain.SessionID][]*verifyTracked{}
	}
	s.multi.tracked[session] = append(s.multi.tracked[session], row)
	s.multi.trackCalls++
	return true, nil
}

func (s *verifyStore) ReplaceLadoExecutionPR(_ context.Context, session domain.SessionID, oldURL, newURL string, _ time.Time, audit *domain.LadoAuditEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := s.liveTrackedLocked(session)
	at := slices.IndexFunc(live, func(row *verifyTracked) bool { return row.url == oldURL })
	old, _ := s.prLocked(session, oldURL)
	replaceable := (old.Closed && !old.Merged) || (at >= 0 && old.Merged && live[at].settled == domain.PRStateMerged)
	if at < 0 || !replaceable || s.trackedURLLocked(session, newURL) {
		return false, nil
	}
	live[at].replaced = true
	s.multi.tracked[session] = append(s.multi.tracked[session], &verifyTracked{url: newURL, repo: live[at].repo, position: live[at].position})
	if audit != nil {
		s.audits = append(s.audits, *audit)
	}
	return true, nil
}

func (s *verifyStore) SettleLadoExecutionPR(ctx context.Context, session domain.SessionID, prURL string, state domain.PRState, at time.Time, audit *domain.LadoAuditEvent) error {
	s.mu.Lock()
	live := s.liveTrackedLocked(session)
	i := slices.IndexFunc(live, func(row *verifyTracked) bool { return row.url == prURL })
	if i < 0 {
		s.mu.Unlock()
		return s.SettleLadoExecution(ctx, session, prURL, state, at, audit)
	}
	defer s.mu.Unlock()
	if live[i].settled == state {
		return nil
	}
	live[i].settled = state
	if audit != nil {
		s.audits = append(s.audits, *audit)
	}
	for _, row := range live {
		pr, _ := s.prLocked(session, row.url)
		if row.settled == "" || row.settled != prState(pr) {
			return nil
		}
	}
	if s.settled == nil {
		s.settled, s.settledPR = map[domain.SessionID]time.Time{}, map[domain.SessionID]string{}
	}
	s.settled[session], s.settledPR[session] = at, settleKey(prURL, state)
	return nil
}

func (s *verifyStore) SettleLadoExecutionsAwaitingRepos(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.multi.awaitingSweeps++
	return 0, nil
}

// trackedOf returns the session's live tracked PRs as url@repo#position.
func (s *verifyStore) trackedOf(session domain.SessionID) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, row := range s.liveTrackedLocked(session) {
		out = append(out, row.url+"@"+row.repo+"#"+strconv.Itoa(row.position))
	}
	return out
}

func (s *verifyStore) settledStateOf(session domain.SessionID, url string) domain.PRState {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.multi.tracked[session] {
		if row.url == url {
			return row.settled
		}
	}
	return ""
}

func (s *verifyStore) ListLadoExecutionPRReviews(_ context.Context, session domain.SessionID, prURL string) ([]domain.LadoExecutionReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rounds []domain.LadoExecutionReview
	for _, round := range s.reviews {
		if url := s.multi.reviewPR[round.ReviewRunID]; round.ExecutionSessionID == session && (url == "" || url == prURL) {
			rounds = append(rounds, round)
		}
	}
	return rounds, nil
}

func (s *verifyStore) AddLadoExecutionPRReview(ctx context.Context, r domain.LadoExecutionReview, prURL string) error {
	rounds, _ := s.ListLadoExecutionPRReviews(ctx, r.ExecutionSessionID, prURL)
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Round != len(rounds)+1 || r.Round > domain.LadoReviewRoundCap {
		return errRoundNotNext
	}
	if s.multi.reviewPR == nil {
		s.multi.reviewPR = map[string]string{}
	}
	s.multi.reviewPR[r.ReviewRunID] = prURL
	s.reviews = append(s.reviews, r)
	return nil
}

var errRoundNotNext = &roundError{}

type roundError struct{}

func (*roundError) Error() string { return "the round is not the next one" }

// ApproveLadoIntegrations keeps the store's rules: one transaction, the
// proof's nonce for the first approval and "<nonce>#<n>" for the others,
// and an approval withdrawn for a sibling's base renewed in place.
func (s *verifyStore) ApproveLadoIntegrations(_ context.Context, approvals []domain.LadoIntegrationApproval, proof domain.LadoPresenceProof, audit domain.LadoAuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	renew := make([]int, len(approvals))
	for i, a := range approvals {
		nonce := derivedPresenceNonce(proof.Nonce, i)
		if slices.ContainsFunc(s.proofs, func(p domain.LadoPresenceProof) bool { return p.Nonce == nonce }) {
			return domain.ErrLadoPresenceReplayed
		}
		renew[i] = slices.IndexFunc(s.approvals, func(x domain.LadoIntegrationApproval) bool {
			return x.ExecutionSessionID == a.ExecutionSessionID && x.PRURL == a.PRURL && x.HeadSHA == a.HeadSHA && strings.EqualFold(x.TargetBranch, a.TargetBranch)
		})
		if at := renew[i]; at >= 0 && !s.approvals[at].Renewable() {
			if s.approvals[at].Revoked() {
				return domain.ErrLadoIntegrationRevoked
			}
			return domain.ErrLadoIntegrationAlreadyApproved
		}
		record, ok := s.records[verifyKey(a.ExecutionSessionID, a.HeadSHA)]
		if !ok {
			return domain.ErrLadoVerificationChanged
		}
		if sum, err := record.SHA256(); err != nil || sum != a.VerificationSHA256 {
			return domain.ErrLadoVerificationChanged
		}
	}
	for i, a := range approvals {
		nonce := derivedPresenceNonce(proof.Nonce, i)
		used := proof
		used.Nonce = nonce
		s.proofs = append(s.proofs, used)
		a.ProofNonce, a.Acknowledged = nonce, slices.Clone(a.Acknowledged)
		if at := renew[i]; at >= 0 {
			a.ID = s.approvals[at].ID
			s.approvals[at] = a
			continue
		}
		s.approvals = append(s.approvals, a)
	}
	s.audits = append(s.audits, audit)
	return nil
}

const (
	multiBackOrigin  = "https://github.com/acme/back.git"
	multiFrontOrigin = "https://github.com/acme/front.git"
)

// addMultiRepoExecution adds an execution of a task in workspace project ws
// whose issue is in the child repository back (acme/back) and whose plan
// changes back and front (acme/front), in that merge order. Its session
// opened a back PR at backHead and, later, a front PR at frontHead, which
// LADO does not track yet; the worker's worktrees of both are at those
// heads. It returns the execution and the two PRs.
func (f *verifyFixture) addMultiRepoExecution(key, backHead, frontHead string) (domain.LadoExecution, domain.LadoExecutionPR, domain.LadoExecutionPR) {
	f.t.Helper()
	exec := f.addExecution(key, backHead)
	root := f.t.TempDir()
	f.reads.setProject(domain.ProjectRecord{ID: "ws", Path: root, Kind: domain.ProjectKindWorkspace}, []domain.WorkspaceRepoRecord{
		{ProjectID: "ws", Name: "back", RelativePath: "back", RepoOriginURL: multiBackOrigin, DefaultBranch: "main"},
		{ProjectID: "ws", Name: "front", RelativePath: "front", RepoOriginURL: multiFrontOrigin, DefaultBranch: "main"},
	})
	f.reads.updateTask(exec.TaskID, func(task *domain.LadoTask) {
		task.ProjectID = "ws"
		task.Repo = domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/back"}
	})
	f.reads.updatePlan(exec.PlanRevisionID, func(plan *domain.LadoPlan) {
		plan.AffectedPaths = []string{"back/src/**", "front/src/**"}
		plan.Repos = []string{"back", "front"}
	})
	worker := f.reads.worktree(exec.SessionID)
	for repo, head := range map[string]string{"back": backHead, "front": frontHead} {
		tree := filepath.Join(worker, repo)
		if err := os.MkdirAll(tree, 0o755); err != nil {
			f.t.Fatal(err)
		}
		f.git.setHead(tree, head)
	}
	number, err := strconv.Atoi(key)
	if err != nil {
		f.t.Fatal(err)
	}
	back := domain.LadoExecutionPR{URL: "https://github.com/acme/back/pull/" + key, Number: number, HeadSHA: backHead, TargetBranch: "main", Title: exec.PR.Title}
	front := domain.LadoExecutionPR{URL: "https://github.com/acme/front/pull/" + key, Number: number, HeadSHA: frontHead, TargetBranch: "main", Title: exec.PR.Title}
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) {
		e.PR, e.PRRepos = back, []string{"back", "front"}
	})
	f.store.setSessionPRs(exec.SessionID, front, back)
	exec.PR, exec.PRRepos = back, []string{"back", "front"}
	return exec, back, front
}

// addMultiRepoApproval is addMultiRepoExecution whose PRs the host has at
// their heads.
func (f *approveFixture) addMultiRepoApproval(key, backHead, frontHead string) (domain.LadoExecution, domain.LadoExecutionPR, domain.LadoExecutionPR) {
	f.t.Helper()
	exec, back, front := f.addMultiRepoExecution(key, backHead, frontHead)
	f.heads.set(back.URL, backHead)
	f.heads.set(front.URL, frontHead)
	return exec, back, front
}

// pushPR moves a session's PR and the worker's worktree of its repository
// to head.
func (f *verifyFixture) pushPR(exec domain.LadoExecution, pr domain.LadoExecutionPR, repo, head string) {
	f.store.updatePR(pr.URL, func(p *domain.LadoExecutionPR) { p.HeadSHA = head })
	f.git.setHead(filepath.Join(f.reads.worktree(exec.SessionID), repo), head)
}

func (f *verifyFixture) execution(session domain.SessionID) domain.LadoExecution {
	f.t.Helper()
	exec, ok, err := f.store.LadoExecution(f.ctx, session)
	if err != nil || !ok {
		f.t.Fatalf("execution %s = %v, %v", session, ok, err)
	}
	return exec
}

// The loop pins the back PR and the front PR, each in its repository and
// in the plan's order, and verifies each in its own repository. A later
// update of either PR does not move what it tracks.
func TestTrackPinsOnePRPerRepository(t *testing.T) {
	f := newVerifyFixture(t)
	exec, back, front := f.addMultiRepoExecution("7", verifyHead1, verifyHead2)

	f.tick()
	want := []string{back.URL + "@back#0", front.URL + "@front#1"}
	if got := f.store.trackedOf(exec.SessionID); !slices.Equal(got, want) {
		t.Fatalf("tracked = %v, want %v", got, want)
	}
	if record := f.record(exec.SessionID, verifyHead1); record.PRURL != back.URL {
		t.Errorf("back head record names %s, want %s", record.PRURL, back.URL)
	}
	if record := f.record(exec.SessionID, verifyHead2); record.PRURL != front.URL {
		t.Errorf("front head record names %s, want %s", record.PRURL, front.URL)
	}
	root := f.reads.project("ws").Path
	for _, repo := range []string{"back", "front"} {
		if !f.git.asked(filepath.Join(root, repo)) {
			t.Errorf("git read %v, want the %s repository", f.git.askedDirs(), repo)
		}
	}
	got := f.execution(exec.SessionID)
	if got.PR.URL != back.URL || len(got.PRs) != 2 || len(got.UntrackedPRs) != 0 {
		t.Errorf("execution PR = %s with %d tracked and untracked %v, want back first, two tracked, none untracked", got.PR.URL, len(got.PRs), got.UntrackedPRs)
	}

	// The back PR is updated after the front one: the newest PR changes,
	// what LADO tracks does not.
	f.store.setSessionPRs(exec.SessionID, back, front)
	calls := f.store.multi.trackCalls
	f.clock.advance(time.Minute)
	f.tick()
	if got := f.store.trackedOf(exec.SessionID); !slices.Equal(got, want) || f.store.multi.trackCalls != calls {
		t.Fatalf("tracked after an update = %v (%d calls), want %v unchanged", got, f.store.multi.trackCalls-calls, want)
	}
}

// In one repository the PR pinned first stays tracked while it is open: a
// second open PR is untracked, also in a single-repository project. Once
// the worker closes the pinned PR, the newer one takes its place.
func TestTrackLeavesASecondOpenPRUntracked(t *testing.T) {
	f := newVerifyFixture(t)
	exec := f.addExecution("3", verifyHead1)
	first := exec.PR
	f.store.setSessionPRs(exec.SessionID, first)

	f.tick()
	if got := f.store.trackedOf(exec.SessionID); !slices.Equal(got, []string{first.URL + "@#0"}) {
		t.Fatalf("tracked = %v, want the first PR in the project's one repository", got)
	}

	second := domain.LadoExecutionPR{URL: "https://github.com/acme/demo/pull/4", Number: 4, HeadSHA: verifyHead2, TargetBranch: "main", Title: first.Title}
	f.store.setSessionPRs(exec.SessionID, second, first)
	f.clock.advance(time.Minute)
	f.tick()
	if got := f.store.trackedOf(exec.SessionID); !slices.Equal(got, []string{first.URL + "@#0"}) {
		t.Fatalf("tracked with a second open PR = %v, want the first only", got)
	}
	if _, ok := f.store.recordOf(exec.SessionID, verifyHead2); ok {
		t.Error("the untracked PR's head was verified")
	}
	got := f.execution(exec.SessionID)
	if got.PR.URL != first.URL || len(got.UntrackedPRs) != 1 || got.UntrackedPRs[0].URL != second.URL {
		t.Errorf("execution PR = %s, untracked %v; want the first tracked and the second untracked", got.PR.URL, got.UntrackedPRs)
	}

	f.store.updatePR(first.URL, func(pr *domain.LadoExecutionPR) { pr.Closed = true })
	f.clock.advance(time.Minute)
	f.tick()
	if got := f.store.trackedOf(exec.SessionID); !slices.Equal(got, []string{second.URL + "@#0"}) {
		t.Fatalf("tracked once the first closed = %v, want the second in its place", got)
	}
	if !slices.ContainsFunc(f.store.auditEvents(), func(e domain.LadoAuditEvent) bool {
		return e.Action == ladoAuditPRReplaced && strings.Contains(string(e.Payload), second.URL)
	}) {
		t.Errorf("audit events = %+v, want the replacement", f.store.auditEvents())
	}
	if _, ok := f.store.recordOf(exec.SessionID, verifyHead2); !ok {
		t.Error("the PR that took the closed one's place was not verified")
	}
}

// A follow-up PR the worker opens in a repository whose tracked PR merged
// and was settled takes that PR's place (review of #64), as the session's
// newest PR did before LADO tracked PRs: it is tracked, verified and
// approved, in a single-repository project too.
func TestTrackFollowUpPRAfterAMerge(t *testing.T) {
	f := newApproveFixture(t)
	exec := f.add("3", verifyHead1)
	first := exec.PR
	f.store.setSessionPRs(exec.SessionID, first)
	f.tick()

	f.store.updatePR(first.URL, func(pr *domain.LadoExecutionPR) { pr.Merged = true })
	f.clock.advance(time.Minute)
	f.tick()
	if got := f.store.settledStateOf(exec.SessionID, first.URL); got != domain.PRStateMerged {
		t.Fatalf("first settled as %q, want merged", got)
	}
	if f.store.multi.awaitingSweeps == 0 {
		t.Fatal("a pass did not settle the executions awaiting repositories")
	}

	second := domain.LadoExecutionPR{URL: "https://github.com/acme/demo/pull/4", Number: 4, HeadSHA: verifyHead2, TargetBranch: "main", Title: first.Title}
	merged := first
	merged.Merged = true
	f.store.setSessionPRs(exec.SessionID, second, merged)
	f.heads.set(second.URL, verifyHead2)
	f.clock.advance(time.Minute)
	f.tick()
	if got := f.store.trackedOf(exec.SessionID); !slices.Equal(got, []string{second.URL + "@#0"}) {
		t.Fatalf("tracked after a follow-up PR = %v, want it in the merged one's place", got)
	}
	if !slices.ContainsFunc(f.store.auditEvents(), func(e domain.LadoAuditEvent) bool {
		return e.Action == ladoAuditPRReplaced && strings.Contains(string(e.Payload), second.URL)
	}) {
		t.Errorf("audit events = %+v, want the replacement", f.store.auditEvents())
	}
	if _, ok := f.store.recordOf(exec.SessionID, verifyHead2); !ok {
		t.Fatal("the follow-up PR's head was not verified")
	}
	if got := f.execution(exec.SessionID); got.PR.URL != second.URL || len(got.UntrackedPRs) != 0 {
		t.Fatalf("execution PR = %s, untracked %v; want the follow-up tracked", got.PR.URL, got.UntrackedPRs)
	}
	pr := f.previewPR(exec, second, verifyHead2)
	result, err := f.svc.ApproveIntegration(f.ctx, ApproveInput{
		SessionID: exec.SessionID, PRURL: second.URL, HeadSHA: verifyHead2,
		VerificationSHA256: pr.VerificationSHA256, Acknowledge: pr.Acknowledge, Presence: presenceProof("nonce-follow-up", false),
	})
	if err != nil || result.Approval == nil || result.Approval.PRURL != second.URL {
		t.Fatalf("approve the follow-up PR = %+v, %v; want its approval", result.Approval, err)
	}
}

// Once back merged first, the loop still verifies front's new head and
// settles its merge: the execution itself is settled with its last PR.
func TestBackMergedFirstFrontStillVerifiedAndSettled(t *testing.T) {
	f := newVerifyFixture(t)
	exec, back, front := f.addMultiRepoExecution("8", verifyHead1, verifyHead2)
	f.tick()

	f.store.updatePR(back.URL, func(pr *domain.LadoExecutionPR) { pr.Merged = true })
	f.clock.advance(time.Minute)
	f.tick()
	if got := f.store.settledStateOf(exec.SessionID, back.URL); got != domain.PRStateMerged {
		t.Fatalf("back settled as %q, want merged", got)
	}
	if _, settled := f.store.settledAt(exec.SessionID); settled {
		t.Fatal("the execution settled while its front PR is open")
	}

	f.pushPR(exec, front, "front", verifyHead3)
	f.clock.advance(time.Minute)
	f.tick()
	if record := f.record(exec.SessionID, verifyHead3); record.PRURL != front.URL {
		t.Errorf("front's new head record names %s, want %s", record.PRURL, front.URL)
	}

	f.store.updatePR(front.URL, func(pr *domain.LadoExecutionPR) { pr.Merged = true })
	f.clock.advance(time.Minute)
	f.tick()
	if got := f.store.settledStateOf(exec.SessionID, front.URL); got != domain.PRStateMerged {
		t.Fatalf("front settled as %q, want merged", got)
	}
	if at, settled := f.store.settledAt(exec.SessionID); !settled || !at.Equal(f.clock.Now()) {
		t.Fatalf("execution settled = %v at %s, want now with its last PR", settled, at)
	}
}

// A merge request of a GitLab instance under a relative URL root
// (https://host/gitlab/group/repo/-/merge_requests/N) is verified in the
// child repository whose origin is git@host:group/repo.git, under the
// child's repository.
func TestGitLabRelativeRootMRIsVerifiedInItsChildRepository(t *testing.T) {
	f := newVerifyFixture(t)
	exec, _ := f.addWorkspaceExecution("9", verifyHead1)
	root := f.reads.project("ws").Path
	f.reads.setProject(f.reads.project("ws"), []domain.WorkspaceRepoRecord{
		{ProjectID: "ws", Name: "web", RelativePath: "web", RepoOriginURL: "git@gitlab.example.com:team/web.git", DefaultBranch: "main"},
		{ProjectID: "ws", Name: "api", RelativePath: "api", RepoOriginURL: "git@gitlab.example.com:team/api.git", DefaultBranch: "main"},
	})
	f.reads.updateTask(exec.TaskID, func(task *domain.LadoTask) {
		task.Repo = domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Host: "gitlab.example.com", Native: "team/api"}
	})
	url := "https://gitlab.example.com/gitlab/team/api/-/merge_requests/9"
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.PR.URL = url })

	f.tick()
	record := f.record(exec.SessionID, verifyHead1)
	if item := verifyItemOf(t, record, domain.LadoVerifyBaseBranch); item.Code == domain.LadoCodePRRepoNotInWorkspace {
		t.Fatalf("base_branch item = %s %s, want the merge request verified in api", item.Outcome, item.Code)
	}
	if !f.git.asked(filepath.Join(root, "api")) {
		t.Errorf("git read %v, want the api repository", f.git.askedDirs())
	}
	if reads := f.host.branchReads(); !slices.ContainsFunc(reads, func(read string) bool { return strings.HasPrefix(read, "team/api@") }) {
		t.Errorf("rule reads = %v, want team/api's", reads)
	}
}

// approveTask approves the task's PRs together under a proof with nonce,
// recording the proof's subject.
func (f *approveFixture) approveTask(exec domain.LadoExecution, nonce string, subject *string, prs ...ApprovePR) (ApproveResult, error) {
	presence := presenceProof(nonce, false)
	return f.svc.ApproveIntegration(f.ctx, ApproveInput{
		SessionID: exec.SessionID, PRs: prs,
		Presence: func(action, s string) (domain.LadoPresenceProof, error) {
			if subject != nil {
				*subject = s
			}
			return presence(action, s)
		},
	})
}

// previewPR is the dry run of one PR of a task.
func (f *approveFixture) previewPR(exec domain.LadoExecution, pr domain.LadoExecutionPR, head string) ApprovePR {
	f.t.Helper()
	result, err := f.svc.ApproveIntegration(f.ctx, ApproveInput{SessionID: exec.SessionID, PRURL: pr.URL, HeadSHA: head, DryRun: true})
	if err != nil {
		f.t.Fatalf("dry run of %s = %v", pr.URL, err)
	}
	if len(result.PRs) != 1 || result.PRs[0].PRURL != pr.URL || result.SHA256 != result.PRs[0].SHA256 {
		f.t.Fatalf("dry run of %s answered %+v", pr.URL, result.PRs)
	}
	return ApprovePR{PRURL: pr.URL, HeadSHA: head, VerificationSHA256: result.SHA256, Acknowledge: result.Confirm}
}

// The task's two PRs are approved with one proof: two approvals in one
// write, the second under the derived nonce, each with its status, and the
// proof's subject names both heads in merge order.
func TestApproveTaskApprovesEveryPRWithOneProof(t *testing.T) {
	f := newApproveFixture(t)
	exec, back, front := f.addMultiRepoApproval("7", verifyHead1, verifyHead2)
	f.tick()

	backPR, frontPR := f.previewPR(exec, back, verifyHead1), f.previewPR(exec, front, verifyHead2)
	// Only one of the task's PRs is not the task's approval.
	_, err := f.approveTask(exec, "nonce-one", nil, backPR)
	requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_PRS_CHANGED")

	var subject string
	result, err := f.approveTask(exec, "nonce-task", &subject, frontPR, backPR)
	if err != nil {
		t.Fatalf("approve the task = %v", err)
	}
	if want := string(exec.SessionID) + "@" + verifyHead1 + "," + verifyHead2; subject != want {
		t.Errorf("presence subject = %q, want %q", subject, want)
	}
	if len(result.PRs) != 2 || result.PRs[0].PRURL != back.URL || result.PRs[1].PRURL != front.URL {
		t.Fatalf("result PRs = %+v, want back then front", result.PRs)
	}
	nonces := []string{}
	for _, pr := range result.PRs {
		if pr.Approval == nil || pr.Approval.PRURL != pr.PRURL || pr.Approval.StatusState != domain.LadoIntegrationStatusPosted {
			t.Fatalf("approval of %s = %+v, want recorded and posted", pr.PRURL, pr.Approval)
		}
		nonces = append(nonces, pr.Approval.ProofNonce)
	}
	if !slices.Equal(nonces, []string{"nonce-task", "nonce-task#1"}) {
		t.Errorf("proof nonces = %v, want the proof's and its derived one", nonces)
	}
	if f.store.approvalCount() != 2 {
		t.Errorf("approvals = %d, want 2", f.store.approvalCount())
	}
	if posts := f.host.postsMade(); len(posts) != 2 || posts[0].repo.Native != "acme/back" || posts[1].repo.Native != "acme/front" {
		t.Errorf("posts = %+v, want one on each repository", posts)
	}
	approved := slices.DeleteFunc(f.store.auditEvents(), func(e domain.LadoAuditEvent) bool { return e.Action != "integration.approved" })
	if len(approved) != 1 || !strings.Contains(string(approved[0].Payload), `"prs":[`) {
		t.Errorf("approval audit events = %+v, want one naming both PRs", approved)
	}
	// Both are approved: the task's approval is refused.
	_, err = f.approveTask(exec, "nonce-again", nil, backPR, frontPR)
	requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_ALREADY_APPROVED")
}

// A PR that waits holds the whole task's approval back, and so does a
// repository the task is expected to open a PR in without one.
func TestApproveTaskRefusesWhileAPRIsNotReady(t *testing.T) {
	f := newApproveFixture(t)
	exec, back, front := f.addMultiRepoApproval("7", verifyHead1, verifyHead2)
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}}, 30)
	f.tick()
	// The back head's command runs; the front head's waits its turn.
	backPR, frontPR := f.previewPR(exec, back, verifyHead1), f.previewPR(exec, front, verifyHead2)
	_, err := f.approveTask(exec, "nonce-wait", nil, backPR, frontPR)
	details := requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_NOT_READY").Details
	if reasons, _ := details["reasons"].([]string); !slices.Contains(reasons, domain.LadoCodeCommandsRunning) {
		t.Errorf("reasons = %v, want %s", details["reasons"], domain.LadoCodeCommandsRunning)
	}
}

// pr_missing: an integration execution's repositories are facts, so a
// repository without its PR holds the approval back; a single worker's are
// what the plan declared, so once it reported done the PR it did not open
// is not waited for. An integration execution whose front did not change
// expects only back.
func TestApproveTaskWithARepositoryWithoutAPR(t *testing.T) {
	for _, tc := range []struct {
		name       string
		repos      []string
		firm, done bool
		missing    bool
	}{
		{"an integration execution waits", []string{"back", "front"}, true, true, true},
		{"a working single worker waits", []string{"back", "front"}, false, false, true},
		{"a single worker that reported done does not", []string{"back", "front"}, false, true, false},
		{"an integration execution whose front did not change does not", []string{"back"}, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newApproveFixture(t)
			exec, back, _ := f.addMultiRepoApproval("7", verifyHead1, verifyHead2)
			f.store.setSessionPRs(exec.SessionID, back)
			f.store.update(exec.SessionID, func(e *domain.LadoExecution) {
				e.PRRepos, e.PRReposFirm, e.WorkerDone = tc.repos, tc.firm, tc.done
			})
			f.tick()
			backPR := f.previewPR(exec, back, verifyHead1)
			result, err := f.approveTask(exec, "nonce-"+strconv.FormatBool(tc.missing), nil, backPR)
			if tc.missing {
				details := requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_NOT_READY").Details
				if reasons, _ := details["reasons"].([]string); !slices.Equal(reasons, []string{domain.LadoValidatingPRMissing}) {
					t.Errorf("reasons = %v, want [pr_missing]", details["reasons"])
				}
				if missing, _ := details["missingRepos"].([]string); !slices.Equal(missing, []string{"front"}) {
					t.Errorf("missing = %v, want [front]", details["missingRepos"])
				}
				return
			}
			if err != nil || result.Approval == nil || result.Approval.PRURL != back.URL {
				t.Fatalf("approve back alone = %+v, %v; want it approved", result.Approval, err)
			}
		})
	}
}

// approvedTask runs the loop and approves the task's back and front heads.
func (f *approveFixture) approvedTask(exec domain.LadoExecution, back, front domain.LadoExecutionPR) {
	f.t.Helper()
	f.tick()
	if _, err := f.approveTask(exec, "nonce-first", nil, f.previewPR(exec, back, back.HeadSHA), f.previewPR(exec, front, front.HeadSHA)); err != nil {
		f.t.Fatalf("approve the task = %v", err)
	}
}

func (f *approveFixture) approvalOf(session domain.SessionID, pr domain.LadoExecutionPR, head string) domain.LadoIntegrationApproval {
	f.t.Helper()
	approvals, _ := f.store.ListLadoIntegrationApprovals(f.ctx, session, pr.URL, head)
	if len(approvals) != 1 {
		f.t.Fatalf("approvals of %s at %s = %+v, want one", pr.URL, shortCommit(head), approvals)
	}
	return approvals[0]
}

// The merge gate lets front merge only after back, the plan's order.
func TestMergeGateKeepsThePlansOrder(t *testing.T) {
	f := newApproveFixture(t)
	exec, back, front := f.addMultiRepoApproval("7", verifyHead1, verifyHead2)
	f.approvedTask(exec, back, front)
	gatePR := func(pr domain.LadoExecutionPR) domain.PullRequest {
		return domain.PullRequest{URL: pr.URL, SessionID: exec.SessionID, Number: pr.Number, TargetBranch: "main"}
	}

	err := f.svc.AuthorizeMerge(f.ctx, gatePR(front), verifyHead2)
	requireDetails(t, err, apierr.KindConflict, "LADO_INTEGRATION_MERGE_ORDER", map[string]any{"prUrl": front.URL, "waitingFor": []string{back.URL}})
	if err := f.svc.AuthorizeMerge(f.ctx, gatePR(back), verifyHead1); err != nil {
		t.Fatalf("merge back = %v, want allowed", err)
	}
	f.store.updatePR(back.URL, func(pr *domain.LadoExecutionPR) { pr.Merged = true })
	if err := f.svc.AuthorizeMerge(f.ctx, gatePR(front), verifyHead2); err != nil {
		t.Fatalf("merge front after back = %v, want allowed", err)
	}
}

// Once back's base takes a merge of the same paths, back loses its
// approval and front, unmerged, loses its own as sibling_revoked: the merge
// gate wants a new approval for front. Back's new head and front's same
// head are approved again together, front's row renewed.
func TestSiblingRevokedAndApprovedAgain(t *testing.T) {
	f := newApproveFixture(t)
	exec, back, front := f.addMultiRepoApproval("7", verifyHead1, verifyHead2)
	f.approvedTask(exec, back, front)
	frontApproval := f.approvalOf(exec.SessionID, front, verifyHead2)

	f.git.moveBase(verifyTip, []string{"src/login.go"}, verifyHead1)
	f.clock.advance(3 * time.Minute)
	f.tick()
	if got := f.approvalOf(exec.SessionID, back, verifyHead1); got.RevokedReason != domain.LadoCodeBaseOverlap {
		t.Fatalf("back approval revoked for %q, want base_overlap", got.RevokedReason)
	}
	revoked := f.approvalOf(exec.SessionID, front, verifyHead2)
	if revoked.RevokedReason != domain.LadoIntegrationSiblingRevoked || !revoked.Renewable() {
		t.Fatalf("front approval revoked for %q, want sibling_revoked", revoked.RevokedReason)
	}
	posts := f.host.postsMade()
	if last := posts[len(posts)-1]; last.repo.Native != "acme/front" || last.status.State != "pending" || last.status.Description != siblingRevokedStatusDescription {
		t.Errorf("last post = %+v, want front's pending sibling status", last)
	}
	err := f.svc.AuthorizeMerge(f.ctx, domain.PullRequest{URL: front.URL, SessionID: exec.SessionID, Number: front.Number, TargetBranch: "main"}, verifyHead2)
	requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_APPROVAL_REQUIRED")

	// Back takes in its base; front stays as it was.
	f.pushPR(exec, back, "back", verifyHead3)
	f.heads.set(back.URL, verifyHead3)
	f.clock.advance(time.Minute)
	f.tick()
	backPR := f.previewPR(exec, domain.LadoExecutionPR{URL: back.URL}, verifyHead3)
	frontPR := f.previewPR(exec, front, verifyHead2)
	if _, err := f.approveTask(exec, "nonce-again", nil, backPR, frontPR); err != nil {
		t.Fatalf("approve again = %v", err)
	}
	renewed := f.approvalOf(exec.SessionID, front, verifyHead2)
	if renewed.ID != frontApproval.ID || renewed.Revoked() || renewed.ProofNonce != "nonce-again#1" {
		t.Errorf("front approval = %+v, want %s renewed under the new proof", renewed, frontApproval.ID)
	}
	if got := f.approvalOf(exec.SessionID, back, verifyHead3); got.Revoked() {
		t.Errorf("back's new approval = %+v, want it to hold", got)
	}
	if f.store.approvalCount() != 3 {
		t.Errorf("approvals = %d, want back's two and front's one", f.store.approvalCount())
	}
}
