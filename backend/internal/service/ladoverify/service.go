package ladoverify

// LADO fork: the verification loop (decisions a, b, c, l, m and r of the
// step 8 plan; docs/LJY-PRD/TDD.md §8). Each pass first pins the PRs the
// executions' sessions opened (track.go, #64), then reads each tracked PR
// that is open, or merged or closed and not settled yet (an execution without
// tracked PRs: its session's newest PR). For an open PR it computes the record of the PR's current head, starting what
// the record still waits for (a check command, a review, the evidence pin),
// and tells the worker once about each of LADO's own items that fails on the
// head. A merged or closed PR is settled once. At the end the pass posts
// again the lado/integration statuses that could not be posted, and a
// pending one on the open GitLab heads still without an approval (#21).
//
// #53: the loop fetches each base branch at most every baseFetchEvery, so a
// record sees another PR's merge into its base (base_advanced, or
// base_overlap when it changed the same paths). A merge LADO settles fetches
// its target at once, and the next pass verifies the open executions of that
// target first, approved heads before the others. An approved head whose
// record turns base_overlap loses its approval.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	defaultInterval = 15 * time.Second
	// verifyBatch caps the executions one pass verifies: heads without a
	// record first, then those whose record is oldest.
	verifyBatch = 50
	// failedRetryAfter is how long the loop leaves an execution it could not
	// verify, such as one whose task or project is gone, before it tries
	// again, so that such executions never fill a pass.
	failedRetryAfter = 5 * time.Minute
	// statusBatch caps the approvals whose status one pass posts again.
	statusBatch = 50
	// protectionTTL is how long a read of a base branch's rules is kept.
	protectionTTL = 10 * time.Minute
	// baseResolveTimeout bounds resolving a repository's base branch, as the
	// dispatcher bounds it.
	baseResolveTimeout = 30 * time.Second
	// baseFetchEvery is how often the loop fetches one base branch of one
	// repository from its remote; baseFetchTimeout bounds each fetch.
	baseFetchEvery   = 2 * time.Minute
	baseFetchTimeout = 30 * time.Second
	// urgentListMax is how many executions a pass lists when a merge moved a
	// target, so that the open executions of that target come first.
	urgentListMax = 4 * verifyBatch
)

// Store is what the loop reads and writes in LADO's store (*store.Store).
type Store interface {
	ListLadoExecutionsToVerify(ctx context.Context, limit int) ([]domain.LadoExecution, error)
	// The tracked PRs of executions (track.go, #64).
	ListLadoExecutionPRsToTrack(ctx context.Context, limit int) ([]domain.LadoExecution, error)
	TrackLadoExecutionPR(ctx context.Context, session domain.SessionID, prURL, repo string, position int, at time.Time) (bool, error)
	ReplaceLadoExecutionPR(ctx context.Context, session domain.SessionID, oldURL, newURL string, at time.Time, audit *domain.LadoAuditEvent) (bool, error)
	LadoExecution(ctx context.Context, sessionID domain.SessionID) (domain.LadoExecution, bool, error)
	LadoProjectChecks(ctx context.Context, projectID domain.ProjectID) ([]domain.LadoCheckCommand, int, error)
	// GetPR reads AO's PR row, for the base tip AO recorded.
	GetPR(ctx context.Context, url string) (domain.PullRequest, bool, error)
	ListLadoCommitChecks(ctx context.Context, prURL, commit string) ([]domain.PullRequestCheck, error)
	StartLadoCheckRun(ctx context.Context, run domain.LadoCheckRun) error
	FinishLadoCheckRun(ctx context.Context, id string, status domain.LadoCheckRunStatus, exitCode *int, logSHA256, errorMessage, worktreeSHA256 string, at time.Time) error
	CancelRunningLadoCheckRuns(ctx context.Context, at time.Time) (int, error)
	ListLadoCheckRuns(ctx context.Context, session domain.SessionID, headSHA string) ([]domain.LadoCheckRun, error)
	GetLadoCheckRun(ctx context.Context, id string) (domain.LadoCheckRun, bool, error)
	GetLadoVerification(ctx context.Context, session domain.SessionID, headSHA string) (domain.LadoVerification, bool, error)
	UpsertLadoVerification(ctx context.Context, v domain.LadoVerification) (bool, error)
	ListLadoExecutionReviews(ctx context.Context, session domain.SessionID) ([]domain.LadoExecutionReview, error)
	AddLadoExecutionReview(ctx context.Context, r domain.LadoExecutionReview) error
	// The review rounds of one PR of an execution with several (#64).
	ListLadoExecutionPRReviews(ctx context.Context, session domain.SessionID, prURL string) ([]domain.LadoExecutionReview, error)
	AddLadoExecutionPRReview(ctx context.Context, r domain.LadoExecutionReview, prURL string) error
	MarkLadoExecutionReviewCancelled(ctx context.Context, runID string, at time.Time) error
	ReplaceLadoEvidenceLinks(ctx context.Context, session domain.SessionID, headSHA string, links []domain.LadoEvidenceLink) error
	SettleLadoExecutionPR(ctx context.Context, session domain.SessionID, prURL string, state domain.PRState, at time.Time, audit *domain.LadoAuditEvent) error
	// Settles the executions that waited for a repository's PR and no
	// longer do (#64).
	SettleLadoExecutionsAwaitingRepos(ctx context.Context) (int64, error)
	ListLadoIntegrationStatusRetries(ctx context.Context, limit int) ([]domain.LadoIntegrationApproval, error)
	SetLadoIntegrationStatus(ctx context.Context, id string, state domain.LadoIntegrationStatusState, attempts int, postedAt time.Time, errorMessage string) error
	// The integration approval and the merge gate.
	LadoPresenceProofUsed(ctx context.Context, nonce string) (bool, error)
	ApproveLadoIntegrations(ctx context.Context, approvals []domain.LadoIntegrationApproval, proof domain.LadoPresenceProof, audit domain.LadoAuditEvent) error
	ListLadoIntegrationApprovals(ctx context.Context, session domain.SessionID, prURL, headSHA string) ([]domain.LadoIntegrationApproval, error)
	ListLadoIntegrationApprovalsForPR(ctx context.Context, prURL, headSHA string) ([]domain.LadoIntegrationApproval, error)
	LadoSessionsNamingPR(ctx context.Context, prURL string) ([]domain.SessionID, error)
	ResetLadoIntegrationStatus(ctx context.Context, id string) error
	RevokeLadoIntegrationApproval(ctx context.Context, id, reason string, at time.Time, audit domain.LadoAuditEvent) (bool, error)
	LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error)
	// The stage timings and investigation metrics (timings.go).
	MarkLadoExecutionPRSeen(ctx context.Context, session domain.SessionID, prURL string, at time.Time) error
	MarkLadoExecutionVerified(ctx context.Context, session domain.SessionID, at time.Time) error
	LadoExecutionsForEntry(ctx context.Context, entryID domain.LadoQueueEntryID) ([]domain.LadoExecution, error)
	LadoSessionActivityFacts(ctx context.Context, session domain.SessionID) ([]domain.LadoActivityFact, error)
	UpsertLadoSessionMetrics(ctx context.Context, m domain.LadoSessionMetrics) error
}

// Tasks reads LADO's tasks (*store.Store).
type Tasks interface {
	LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error)
}

// Plans reads the plan of an approved revision.
type Plans interface {
	// ApprovedPlan returns the revision's pinned plan, parsed.
	ApprovedPlan(ctx context.Context, revision domain.LadoPlanRevisionID) (domain.LadoPlan, error)
}

// Snapshots reads the rule snapshot a plan approval fixed (*store.Store). It
// reports false for an approval given before LADO had rules.
type Snapshots interface {
	LadoPolicySnapshot(ctx context.Context, approvalID domain.LadoPlanApprovalID) (domain.LadoPolicySnapshot, bool, error)
}

// Projects reads AO's project registry (*store.Store).
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
}

// Sessions reads AO's session records (*store.Store).
type Sessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Branches resolves the branch a repository's workers start from (AO's
// session manager), as the dispatcher does.
type Branches interface {
	ResolveDefaultBranch(ctx context.Context, repoPath, configuredBranch string) (ports.WorkspaceDefaultBranch, error)
}

// Sender messages a worker session as the daemon (AO's session manager).
type Sender interface {
	Send(ctx context.Context, id domain.SessionID, message string) error
}

// Deps wires a Service.
type Deps struct {
	Store     Store
	Tasks     Tasks
	Plans     Plans
	Snapshots Snapshots
	Projects  Projects
	Sessions  Sessions
	// Git reads the repositories; nil uses the system git.
	Git Git
	// Clients reach the repositories' hosts, for the base branch's rules and
	// the lado/integration status. Without them every read fails.
	Clients RepoClients
	// Reviews is AO's review service. Without it a review the plan requires
	// waits.
	Reviews Reviews
	// Artifacts reads the sessions' artifact versions. Without them no
	// evidence file is found.
	Artifacts Artifacts
	// Blobs keeps check command logs and pinned evidence files.
	Blobs Blobs
	// Sender tells workers what failed. Without it none is told.
	Sender Sender
	// Branches resolves a repository's base branch for ProjectProtection.
	// Without it the branch the project configures is read, if any.
	Branches Branches
	// Run runs one check command and waits for it; nil uses RunCommand.
	Run func(ctx context.Context, spec CommandSpec) CommandResult
	// Env is the environment check commands start from, before CommandEnv
	// takes AO's variables and the tokens out; nil uses os.Environ.
	Env func() []string
	// Clock stamps records; nil uses time.Now.
	Clock func() time.Time
	// NewID returns a new unique id, for check runs and evidence links; nil
	// uses uuid.NewString.
	NewID func() string
	Log   *slog.Logger
	// Interval is how often Start runs a pass; zero uses 15 seconds.
	Interval time.Duration
}

// Service runs LADO's verification loop.
type Service struct {
	deps       Deps
	protection *Protection
	// kick asks the loop for a pass now; one waiting request is enough.
	kick chan struct{}
	// wg counts the loop and the check commands it started.
	wg sync.WaitGroup
	// passMu keeps passes from overlapping, verifyMu keeps two computations
	// of a record (a pass, an integration approval) from overlapping.
	passMu   sync.Mutex
	verifyMu sync.Mutex

	mu sync.Mutex
	// leftoversCancelled is set once the check runs a stopped daemon left
	// running were cancelled; no command starts before.
	leftoversCancelled bool
	// commandRunning is the daemon's one check command slot.
	commandRunning bool
	// statusTried holds, by approval, when this daemon last tried to post
	// its status.
	statusTried map[string]time.Time
	// headLocks are the locks of the heads lado/integration statuses are
	// posted on (lockHead), and waitingPosted holds, by PR and head
	// (waitingKey), when this daemon last looked at an open GitLab head for
	// its waiting status (gitlab_pending.go), for waitingRepostEvery.
	headLocks     map[string]*headLock
	waitingPosted map[string]time.Time
	// headFetched holds, by execution and head, when the loop last fetched
	// a head the repository lacked; failedAt, by execution and PR
	// (passKey), when a pass last could not verify it; trackFailedAt, by
	// execution, when a pass last could not pin its PRs.
	headFetched   map[string]time.Time
	failedAt      map[domain.SessionID]time.Time
	trackFailedAt map[domain.SessionID]time.Time
	// baseFetched holds, by repository, remote and branch (fetchKey), when
	// LADO last fetched a base branch; urgent holds the targets
	// (targetKey) a settled merge moved, which the next pass fetches at
	// once and verifies first.
	baseFetched map[string]time.Time
	urgent      map[string]bool
	// baseResolved holds, by repository folder, the default branch and
	// remote LADO last resolved for a PR in another repository than its
	// issue (#59); prURLWarned the sessions and PR URLs whose repository
	// LADO could not read and said so.
	baseResolved map[string]resolvedBase
	prURLWarned  map[string]bool
	// prSeen and verified hold the executions whose first PR sighting and
	// first verification this daemon stored (timings.go).
	prSeen   map[domain.SessionID]bool
	verified map[domain.SessionID]bool
	// runCtx is the context of the loop Start began, while it runs; the
	// background reads RefreshProtection starts run under it.
	runCtx context.Context
	// liveHeads reads PR heads from their hosts for integration approvals
	// (SetLiveHeads).
	liveHeads LiveHeads
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Git == nil {
		deps.Git = ExecGit{}
	}
	if deps.Clients == nil {
		deps.Clients = noRepoClients{}
	}
	if deps.Run == nil {
		deps.Run = RunCommand
	}
	if deps.Env == nil {
		deps.Env = os.Environ
	}
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.Interval <= 0 {
		deps.Interval = defaultInterval
	}
	return &Service{
		deps:          deps,
		protection:    NewProtection(deps.Clients, deps.Clock, protectionTTL),
		kick:          make(chan struct{}, 1),
		statusTried:   map[string]time.Time{},
		headLocks:     map[string]*headLock{},
		waitingPosted: map[string]time.Time{},
		headFetched:   map[string]time.Time{},
		failedAt:      map[domain.SessionID]time.Time{},
		trackFailedAt: map[domain.SessionID]time.Time{},
		baseFetched:   map[string]time.Time{},
		urgent:        map[string]bool{},
		baseResolved:  map[string]resolvedBase{},
		prURLWarned:   map[string]bool{},
		prSeen:        map[domain.SessionID]bool{},
		verified:      map[domain.SessionID]bool{},
	}
}

// Start runs a pass at once, then every Interval and whenever Kick asks for
// one, until ctx is done. Check commands a pass starts run under ctx too, so
// its end cancels them. Wait waits for both.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	s.runCtx = ctx
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			s.runCtx = nil
			s.mu.Unlock()
		}()
		ticker := time.NewTicker(s.deps.Interval)
		defer ticker.Stop()
		for {
			if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
				s.deps.Log.Warn("LADO verification pass failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.kick:
			}
		}
	}()
}

// Wait waits for the loop Start began, for the check commands passes
// started, which end as cancelled once their context is done, and for the
// reads RefreshProtection started.
func (s *Service) Wait() {
	s.wg.Wait()
}

// Kick asks the loop for a pass as soon as the one under way, if any, ends.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// RefreshProtection reads again, in the background, the rules of the base
// branch of each of the project's repositories, as when LADO mode is turned
// on (decision i), so the next pass and the project's screen start from a
// fresh read. What fails is logged. Before Start, or once the loop stopped,
// it does nothing; Wait waits for the reads it started.
func (s *Service) RefreshProtection(projectID domain.ProjectID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := s.runCtx
	if ctx == nil {
		return
	}
	// The loop holds the wait group until it clears runCtx, so this Add
	// never races Wait.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		reads, err := s.ProjectProtection(ctx, projectID, true)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.deps.Log.Warn("LADO could not read the protection of a project's base branches", "project", projectID, "err", err)
			return
		}
		for _, read := range reads {
			if read.Err != nil {
				s.deps.Log.Warn("LADO could not read a base branch's protection", "project", projectID,
					"repo", domain.LadoRepoRef(read.Repo), "branch", read.Branch, "err", redactedErrorLine(read.Err))
			}
		}
	}()
}

// ProjectProtection reads the rules of the base branch of each of the
// project's repositories: a single-repository project's origin, or each
// workspace repository whose origin is on a host LADO supports. The base
// branch is the one the dispatcher starts workers from. force reads past the
// cache. A repository whose base branch cannot be told has a read with only
// its error.
func (s *Service) ProjectProtection(ctx context.Context, projectID domain.ProjectID, force bool) ([]ProtectionRead, error) {
	places, err := s.projectRepos(ctx, projectID)
	if err != nil {
		return nil, err
	}
	reads := make([]ProtectionRead, 0, len(places))
	for _, place := range places {
		branch, err := s.baseBranchOf(ctx, place)
		if err != nil {
			reads = append(reads, ProtectionRead{Provider: place.repo.Provider, Repo: place.repo, Err: err, ReadAt: s.now()})
			continue
		}
		reads = append(reads, s.protection.Read(ctx, place.repo, branch, force))
	}
	return reads, nil
}

// repoPlace is one of a project's repositories: where it is and the base
// branch the project configures for it, if any.
type repoPlace struct {
	repo       domain.TrackerRepo
	dir        string
	configured string
}

// projectRepos lists the project's repositories as LADO's tasks list them.
func (s *Service) projectRepos(ctx context.Context, projectID domain.ProjectID) ([]repoPlace, error) {
	project, ok, err := s.deps.Projects.GetProject(ctx, string(projectID))
	switch {
	case err != nil:
		return nil, fmt.Errorf("read project %s: %w", projectID, err)
	case !ok:
		return nil, fmt.Errorf("project %s not found", projectID)
	}
	var places []repoPlace
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		// An origin on a host LADO does not support has no protection to read.
		if repo, err := domain.LadoTrackerRepoFromOrigin(project.RepoOriginURL); project.RepoOriginURL != "" && err == nil {
			places = append(places, repoPlace{repo: repo, dir: project.Path, configured: project.Config.WorktreeBaseBranch()})
		}
		return places, nil
	}
	children, err := s.deps.Projects.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("read the workspace repositories of project %s: %w", project.ID, err)
	}
	for _, child := range children {
		repo, err := domain.LadoTrackerRepoFromOrigin(child.RepoOriginURL)
		if child.RepoOriginURL == "" || err != nil {
			continue
		}
		places = append(places, repoPlace{repo: repo, dir: filepath.Join(project.Path, filepath.FromSlash(child.RelativePath)), configured: child.DefaultBranch})
	}
	return places, nil
}

// baseBranchOf resolves a repository's base branch as the dispatcher does.
func (s *Service) baseBranchOf(ctx context.Context, place repoPlace) (string, error) {
	if s.deps.Branches == nil {
		if place.configured == "" {
			return "", errors.New("the project names no base branch and no branch resolver is wired")
		}
		return place.configured, nil
	}
	ctx, cancel := context.WithTimeout(ctx, baseResolveTimeout)
	defer cancel()
	base, err := s.deps.Branches.ResolveDefaultBranch(ctx, place.dir, place.configured)
	switch {
	case err != nil:
		return "", fmt.Errorf("resolve the base branch: %w", err)
	case base.Branch == "":
		return "", errors.New("resolve the base branch: no branch")
	}
	return base.Branch, nil
}

// Tick runs one pass. The first pass cancels the check runs a stopped daemon
// left running, so that they run again; a pass that cannot does nothing
// else. A pass then pins the PRs executions opened (trackPRs). Each PR of an
// execution is verified or settled on its own: what fails for one is
// returned, joined with the others, after the rest ran, and the loop leaves
// that PR of the execution for failedRetryAfter. A pass verifies up to
// verifyBatch executions in the store's order, except that after a merge
// moved a target the open executions of that target come first, those with
// an approved head before the others. An execution whose PRs all settled
// while a repository still waited for its PR settles once it no longer
// waits.
func (s *Service) Tick(ctx context.Context) error {
	s.passMu.Lock()
	defer s.passMu.Unlock()
	if err := s.cancelLeftovers(ctx); err != nil {
		return err
	}
	var problems []error
	if err := s.trackPRs(ctx); err != nil {
		problems = append(problems, err)
	}
	resting := s.resting()
	urgent := s.takeUrgent()
	limit := verifyBatch
	if len(urgent) > 0 {
		limit = urgentListMax
	}
	executions, err := s.deps.Store.ListLadoExecutionsToVerify(ctx, limit+len(resting))
	if err != nil {
		problems = append(problems, fmt.Errorf("list the executions to verify: %w", err))
	}
	keys := map[domain.SessionID]string{}
	if len(urgent) > 0 {
		executions, keys = s.urgentFirst(ctx, executions, urgent)
	}
	verified := 0
	for _, exec := range executions {
		if ctx.Err() != nil || verified == verifyBatch {
			break
		}
		pass := passKey(exec)
		if resting[pass] {
			continue
		}
		verified++
		// The first open head of an urgent target fetches it; the others
		// of that target compare with that fetch.
		key := keys[pass]
		force := urgent[key] && !exec.PR.Merged && !exec.PR.Closed && exec.PR.HeadSHA != ""
		if force {
			urgent[key] = false
		}
		err := s.verifyExecutionIn(ctx, exec, force)
		if ctx.Err() != nil {
			break
		}
		s.mu.Lock()
		if err != nil {
			s.failedAt[pass] = s.now()
		} else {
			delete(s.failedAt, pass)
		}
		s.mu.Unlock()
		if err != nil {
			problems = append(problems, fmt.Errorf("execution %s, pull request %s: %w", exec.SessionID, exec.PR.URL, err))
		}
	}
	if ctx.Err() == nil {
		if _, err := s.deps.Store.SettleLadoExecutionsAwaitingRepos(ctx); err != nil {
			problems = append(problems, fmt.Errorf("settle the executions awaiting repositories: %w", err))
		}
	}
	if ctx.Err() == nil {
		if err := s.retryStatuses(ctx); err != nil {
			problems = append(problems, err)
		}
	}
	if ctx.Err() == nil {
		// What fails is logged, not returned: it is no fault of the pass.
		s.postWaitingStatuses(ctx, executions)
	}
	return errors.Join(problems...)
}

// passKey names what one turn of a pass verifies: the execution, or, for an
// execution with several tracked PRs, which comes once for each (#64), the
// execution and its PR.
func passKey(exec domain.LadoExecution) domain.SessionID {
	if len(exec.PRs) <= 1 {
		return exec.SessionID
	}
	return exec.SessionID + "\x00" + domain.SessionID(exec.PR.URL)
}

// resting returns the executions or their PRs (passKey) a pass could not
// verify less than failedRetryAfter ago, and forgets the others.
func (s *Service) resting() map[domain.SessionID]bool {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	resting := map[domain.SessionID]bool{}
	for key, at := range s.failedAt {
		if now.Sub(at) < failedRetryAfter {
			resting[key] = true
		} else {
			delete(s.failedAt, key)
		}
	}
	return resting
}

// cancelLeftovers marks the check runs a stopped daemon left running as
// cancelled, once: a command whose newest run was cancelled runs again.
func (s *Service) cancelLeftovers(ctx context.Context) error {
	s.mu.Lock()
	done := s.leftoversCancelled
	s.mu.Unlock()
	if done {
		return nil
	}
	n, err := s.deps.Store.CancelRunningLadoCheckRuns(ctx, s.now())
	if err != nil {
		return fmt.Errorf("cancel the check runs a stopped daemon left running: %w", err)
	}
	if n > 0 {
		s.deps.Log.Info("LADO cancelled the check runs a stopped daemon left running; they run again", "runs", n)
	}
	s.mu.Lock()
	s.leftoversCancelled = true
	s.mu.Unlock()
	return nil
}

// verifyExecution settles a merged or closed PR and computes the record of
// an open PR's head. A head AO has not observed yet waits for AO (the store
// does not list it).
func (s *Service) verifyExecution(ctx context.Context, exec domain.LadoExecution) error {
	return s.verifyExecutionIn(ctx, exec, false)
}

// verifyExecutionIn is verifyExecution; urgent fetches the base branch past
// baseFetchEvery, as after a merge into it. A head whose record turns
// base_overlap loses its integration approvals, unless its host says the PR
// merged (#75).
func (s *Service) verifyExecutionIn(ctx context.Context, exec domain.LadoExecution, urgent bool) error {
	s.markPRSeen(ctx, exec)
	switch {
	case exec.PR.Merged || exec.PR.Closed:
		return s.settle(ctx, exec)
	case exec.PR.HeadSHA == "":
		return nil
	}
	// The fix request and the revocation of the pass read the host once
	// (#75).
	host := s.newHostMerges()
	record, err := s.verifyHead(ctx, exec, verifyOptions{startWork: true, loopFetch: true, forceFetch: urgent, host: host})
	if err != nil {
		return err
	}
	_, err = s.revokeOverlapped(ctx, exec, record, host)
	return err
}

// targetKey names the target branch of an execution's PR in its project's
// repository, which a merge into it moves: the PR's repository when its URL
// can be read (#59), else the task's. The repository and the branch compare
// without case, and the remote does not count: the repository names the
// target, and a PR in another repository than its issue fetches from the
// remote resolved for it, not the dispatcher's.
func targetKey(task domain.LadoTask, exec domain.LadoExecution) string {
	repo := task.Repo
	if exec.PR.URL != "" {
		if pr, err := domain.LadoPRRepo(exec.PR.URL); err == nil {
			repo = pr
		}
	}
	return string(task.ProjectID) + "\x00" + strings.ToLower(domain.LadoRepoRef(repo)) + "\x00" +
		strings.ToLower(cmp.Or(exec.PR.TargetBranch, exec.BaseBranch))
}

// markUrgent asks the next pass to fetch the execution's target at once and
// to verify the open executions of that target first, and kicks it. Without
// the execution's task it only kicks.
func (s *Service) markUrgent(ctx context.Context, exec domain.LadoExecution) {
	task, ok, err := s.deps.Tasks.LadoTask(ctx, exec.TaskID)
	if err == nil && ok {
		s.mu.Lock()
		s.urgent[targetKey(task, exec)] = true
		s.mu.Unlock()
	}
	s.Kick()
}

// takeUrgent returns the targets marked urgent and forgets them.
func (s *Service) takeUrgent() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	urgent := s.urgent
	s.urgent = map[string]bool{}
	return urgent
}

// urgentFirst orders the open executions of an urgent target first, those
// whose head is approved before the others, and keeps the store's order
// otherwise. It returns the target keys of the executions' PRs (passKey)
// whose task it could read.
func (s *Service) urgentFirst(ctx context.Context, executions []domain.LadoExecution, urgent map[string]bool) ([]domain.LadoExecution, map[domain.SessionID]string) {
	keys := map[domain.SessionID]string{}
	tasks := map[domain.LadoTaskID]domain.LadoTask{}
	for _, exec := range executions {
		task, read := tasks[exec.TaskID]
		if !read {
			found, ok, err := s.deps.Tasks.LadoTask(ctx, exec.TaskID)
			if err != nil || !ok {
				// The pass reports it when it verifies the execution.
				continue
			}
			task, tasks[exec.TaskID] = found, found
		}
		keys[passKey(exec)] = targetKey(task, exec)
	}
	rank := func(exec domain.LadoExecution) int {
		switch {
		case exec.PR.Merged || exec.PR.Closed || !urgent[keys[passKey(exec)]]:
			return 2
		case exec.Integration.HeadApproved:
			return 0
		}
		return 1
	}
	executions = slices.Clone(executions)
	slices.SortStableFunc(executions, func(a, b domain.LadoExecution) int { return cmp.Compare(rank(a), rank(b)) })
	return executions, keys
}

func (s *Service) now() time.Time {
	return s.deps.Clock().UTC()
}

// noRepoClients stands in for missing repository clients.
type noRepoClients struct{}

func (noRepoClients) ForRepo(context.Context, domain.TrackerRepo) (ports.LadoRepoClient, error) {
	return nil, errors.New("no repository clients are wired")
}
