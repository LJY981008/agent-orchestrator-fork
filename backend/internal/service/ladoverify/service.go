package ladoverify

// LADO fork: the verification loop (decisions a, b, c, l, m and r of the
// step 8 plan; docs/LJY-PRD/TDD.md §8). Each pass reads the executions whose
// session's newest PR is open, or merged or closed and not settled yet. For
// an open PR it computes the record of the PR's current head, starting what
// the record still waits for (a check command, a review, the evidence pin),
// and tells the worker once about each of LADO's own items that fails on the
// head. A merged or closed PR is settled once. At the end the pass posts
// again the lado/integration statuses that could not be posted.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
)

// Store is what the loop reads and writes in LADO's store (*store.Store).
type Store interface {
	ListLadoExecutionsToVerify(ctx context.Context, limit int) ([]domain.LadoExecution, error)
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
	MarkLadoExecutionReviewCancelled(ctx context.Context, runID string, at time.Time) error
	ReplaceLadoEvidenceLinks(ctx context.Context, session domain.SessionID, headSHA string, links []domain.LadoEvidenceLink) error
	SettleLadoExecution(ctx context.Context, session domain.SessionID, prURL string, state domain.PRState, at time.Time, audit *domain.LadoAuditEvent) error
	ListLadoIntegrationStatusRetries(ctx context.Context, limit int) ([]domain.LadoIntegrationApproval, error)
	SetLadoIntegrationStatus(ctx context.Context, id string, state domain.LadoIntegrationStatusState, attempts int, postedAt time.Time, errorMessage string) error
	// The integration approval and the merge gate.
	LadoPresenceProofUsed(ctx context.Context, nonce string) (bool, error)
	ApproveLadoIntegration(ctx context.Context, a domain.LadoIntegrationApproval, proof domain.LadoPresenceProof, audit domain.LadoAuditEvent) error
	GetLadoIntegrationApproval(ctx context.Context, session domain.SessionID, headSHA string) (domain.LadoIntegrationApproval, bool, error)
	LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error)
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
	// headFetched holds, by execution and head, when the loop last fetched
	// a head the repository lacked; failedAt, by execution, when a pass last
	// could not verify it.
	headFetched map[string]time.Time
	failedAt    map[domain.SessionID]time.Time
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
		deps:        deps,
		protection:  NewProtection(deps.Clients, deps.Clock, protectionTTL),
		kick:        make(chan struct{}, 1),
		statusTried: map[string]time.Time{},
		headFetched: map[string]time.Time{},
		failedAt:    map[domain.SessionID]time.Time{},
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
// else. Each execution is verified or settled on its own: what fails for one
// is returned, joined with the others, after the rest ran, and the loop
// leaves that execution for failedRetryAfter. A pass verifies up to
// verifyBatch executions in the store's order.
func (s *Service) Tick(ctx context.Context) error {
	s.passMu.Lock()
	defer s.passMu.Unlock()
	if err := s.cancelLeftovers(ctx); err != nil {
		return err
	}
	var problems []error
	resting := s.resting()
	executions, err := s.deps.Store.ListLadoExecutionsToVerify(ctx, verifyBatch+len(resting))
	if err != nil {
		problems = append(problems, fmt.Errorf("list the executions to verify: %w", err))
	}
	verified := 0
	for _, exec := range executions {
		if ctx.Err() != nil || verified == verifyBatch {
			break
		}
		if resting[exec.SessionID] {
			continue
		}
		verified++
		err := s.verifyExecution(ctx, exec)
		if ctx.Err() != nil {
			break
		}
		s.mu.Lock()
		if err != nil {
			s.failedAt[exec.SessionID] = s.now()
		} else {
			delete(s.failedAt, exec.SessionID)
		}
		s.mu.Unlock()
		if err != nil {
			problems = append(problems, fmt.Errorf("execution %s: %w", exec.SessionID, err))
		}
	}
	if ctx.Err() == nil {
		if err := s.retryStatuses(ctx); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// resting returns the executions a pass could not verify less than
// failedRetryAfter ago, and forgets the others.
func (s *Service) resting() map[domain.SessionID]bool {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	resting := map[domain.SessionID]bool{}
	for session, at := range s.failedAt {
		if now.Sub(at) < failedRetryAfter {
			resting[session] = true
		} else {
			delete(s.failedAt, session)
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
	switch {
	case exec.PR.Merged || exec.PR.Closed:
		return s.settle(ctx, exec)
	case exec.PR.HeadSHA == "":
		return nil
	}
	_, err := s.verifyHead(ctx, exec, verifyOptions{startWork: true})
	return err
}

func (s *Service) now() time.Time {
	return s.deps.Clock().UTC()
}

// noRepoClients stands in for missing repository clients.
type noRepoClients struct{}

func (noRepoClients) ForRepo(context.Context, domain.TrackerRepo) (ports.LadoRepoClient, error) {
	return nil, errors.New("no repository clients are wired")
}
