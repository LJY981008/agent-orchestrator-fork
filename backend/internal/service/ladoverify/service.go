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
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	defaultInterval = 15 * time.Second
	// verifyBatch caps the executions one pass verifies, oldest first.
	verifyBatch = 50
	// statusBatch caps the approvals whose status one pass posts again.
	statusBatch = 50
	// protectionTTL is how long a read of a base branch's rules is kept.
	protectionTTL = 10 * time.Minute
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
	FinishLadoCheckRun(ctx context.Context, id string, status domain.LadoCheckRunStatus, exitCode *int, logSHA256, errorMessage string, at time.Time) error
	CancelRunningLadoCheckRuns(ctx context.Context, at time.Time) (int, error)
	ListLadoCheckRuns(ctx context.Context, session domain.SessionID, headSHA string) ([]domain.LadoCheckRun, error)
	GetLadoVerification(ctx context.Context, session domain.SessionID, headSHA string) (domain.LadoVerification, bool, error)
	UpsertLadoVerification(ctx context.Context, v domain.LadoVerification) (bool, error)
	ListLadoExecutionReviews(ctx context.Context, session domain.SessionID) ([]domain.LadoExecutionReview, error)
	AddLadoExecutionReview(ctx context.Context, r domain.LadoExecutionReview) error
	MarkLadoExecutionReviewCancelled(ctx context.Context, runID string, at time.Time) error
	ReplaceLadoEvidenceLinks(ctx context.Context, session domain.SessionID, headSHA string, links []domain.LadoEvidenceLink) error
	SettleLadoExecution(ctx context.Context, session domain.SessionID, at time.Time, audit *domain.LadoAuditEvent) error
	ListLadoIntegrationStatusRetries(ctx context.Context, limit int) ([]domain.LadoIntegrationApproval, error)
	SetLadoIntegrationStatus(ctx context.Context, id string, state domain.LadoIntegrationStatusState, attempts int, postedAt time.Time, errorMessage string) error
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
	}
}

// Start runs a pass at once, then every Interval and whenever Kick asks for
// one, until ctx is done. Check commands a pass starts run under ctx too, so
// its end cancels them. Wait waits for both.
func (s *Service) Start(ctx context.Context) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
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

// Wait waits for the loop Start began and for the check commands passes
// started, which end as cancelled once their context is done.
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

// Tick runs one pass. The first pass cancels the check runs a stopped daemon
// left running, so that they run again; a pass that cannot does nothing
// else. Each execution is verified or settled on its own: what fails for one
// is returned, joined with the others, after the rest ran.
func (s *Service) Tick(ctx context.Context) error {
	s.passMu.Lock()
	defer s.passMu.Unlock()
	if err := s.cancelLeftovers(ctx); err != nil {
		return err
	}
	var problems []error
	executions, err := s.deps.Store.ListLadoExecutionsToVerify(ctx, verifyBatch)
	if err != nil {
		problems = append(problems, fmt.Errorf("list the executions to verify: %w", err))
	}
	for _, exec := range executions {
		if ctx.Err() != nil {
			break
		}
		if err := s.verifyExecution(ctx, exec); err != nil {
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
// an open PR's head. A head AO has not observed yet waits for AO.
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
