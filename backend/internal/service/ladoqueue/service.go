// Package ladoqueue is LADO's dispatcher: it starts the worker of each
// approved task in its turn (docs/LJY-PRD/TDD.md §5.3). Approving a plan
// queues it (service/ladotask); the dispatcher claims queue entries within
// the run caps, checks that the approval still holds, spawns the worker
// through AO's session service in Chat mode, and links the session to its
// entry as the task's execution. It also links the workers a dispatch that
// stopped midway had started, stops the runs past their deadline, and stops
// the runs their task no longer wants.
package ladoqueue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

const (
	defaultInterval = 15 * time.Second
	defaultLease    = 2 * time.Minute
)

// Store keeps the queue: the task reads a dispatch checks, the primitives
// that claim entries and record what became of them, the session AO keeps
// for a client request id with the dispatch hash its worker carries, the
// running executions, and what the app reads about the queue, its executions
// and the dispatch settings (views.go).
type Store interface {
	LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error)
	LadoDispatchSettings(ctx context.Context) (domain.LadoDispatchSettings, error)
	LadoProjectMaxConcurrent(ctx context.Context, projectID domain.ProjectID) (int, error)
	CountLiveLadoExecutions(ctx context.Context, now time.Time) (int, map[domain.ProjectID]int, error)
	SetLadoQueueWaiting(ctx context.Context, reason string, projectID *domain.ProjectID) (int64, error)
	ClaimLadoQueueEntries(ctx context.Context, now, leaseUntil time.Time, limit int, fullProjects []domain.ProjectID, newToken func() string) ([]domain.LadoQueueEntry, error)
	ReleaseLadoQueueEntry(ctx context.Context, id domain.LadoQueueEntryID, token string, nextAttempt time.Time, waiting string) (bool, error)
	HoldLadoQueueEntry(ctx context.Context, id domain.LadoQueueEntryID, token, waiting string, nextAttempt time.Time) (bool, error)
	MarkLadoQueueEntryReviewRequired(ctx context.Context, id domain.LadoQueueEntryID, token, reason string) (bool, error)
	MarkLadoQueueEntryBlocked(ctx context.Context, id domain.LadoQueueEntryID, token, reason string) (bool, error)
	FailLadoQueueEntry(ctx context.Context, id domain.LadoQueueEntryID, token, message string, at time.Time) (bool, error)
	CancelLadoQueueEntry(ctx context.Context, id domain.LadoQueueEntryID, token string, at time.Time) (bool, error)
	LinkLadoExecution(ctx context.Context, entryID domain.LadoQueueEntryID, token string, exec domain.LadoExecution) error
	LeasedLadoQueueEntries(ctx context.Context) ([]domain.LadoQueueEntry, error)
	GetSessionByClientRequestID(ctx context.Context, id string) (domain.SessionRecord, bool, error)
	LadoDispatchGrant(ctx context.Context, entryID domain.LadoQueueEntryID, attempt int, now time.Time) (domain.LadoDispatchGrant, error)
	LadoExecutionsPastDeadline(ctx context.Context, now time.Time) ([]domain.LadoExecution, error)
	MarkLadoExecutionTimedOut(ctx context.Context, sessionID domain.SessionID, event domain.LadoAuditEvent) (bool, error)
	LiveLadoExecutions(ctx context.Context) ([]domain.LadoExecution, error)
	AppendLadoAuditEvent(ctx context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error)
	ListLadoQueue(ctx context.Context, projectID *domain.ProjectID) ([]domain.LadoQueueItem, error)
	LadoExecution(ctx context.Context, sessionID domain.SessionID) (domain.LadoExecution, bool, error)
	LadoPlanRevision(ctx context.Context, id domain.LadoPlanRevisionID) (domain.LadoPlanRevision, bool, error)
	ListLadoPlanApprovals(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoPlanApproval, error)
	LadoPolicySnapshot(ctx context.Context, approvalID domain.LadoPlanApprovalID) (domain.LadoPolicySnapshot, bool, error)
	SetLadoDispatchSettings(ctx context.Context, settings domain.LadoDispatchSettings, event domain.LadoAuditEvent) (domain.LadoDispatchSettings, error)
	LadoProjectChecks(ctx context.Context, projectID domain.ProjectID) ([]domain.LadoCheckCommand, int, error)
}

// Tasks reads a task's issue now and the inputs its worker starts from.
// *ladotask.Service implements it.
type Tasks interface {
	IssueCheck(ctx context.Context, task domain.LadoTask) (ladotask.IssueCheck, error)
	RunInput(ctx context.Context, task domain.LadoTask) (ladotask.RunInput, error)
}

// Policy gives the snapshot hash the rules give a task's approved revision
// now. *ladopolicy.Service implements it.
type Policy interface {
	CurrentSHA256(ctx context.Context, task domain.LadoTask) (string, error)
}

// Sessions starts and stops workers through AO's session service
// (*session.Service).
type Sessions interface {
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error)
	RequestKill(ctx context.Context, id domain.SessionID) (sessionmanager.KillResult, error)
}

// Projects reads AO's project registry.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
}

// Memory reads the machine's memory pressure. The daemon's usage memory
// reader (*usage.MemoryReader) implements it.
type Memory interface {
	Pressure(ctx context.Context) (procmem.Pressure, error)
}

// Branches resolves the branch AO starts a repository's worktrees from, given
// the branch the project configures for it ("" for the repository's own
// default). AO's workspace adapter implements it, and AO's spawn calls it the
// same way.
type Branches interface {
	ResolveDefaultBranch(ctx context.Context, repoPath, configuredBranch string) (ports.WorkspaceDefaultBranch, error)
}

// Deps wires a Service.
type Deps struct {
	Store    Store
	Tasks    Tasks
	Policy   Policy
	Sessions Sessions
	Projects Projects
	// Branches tells which branch a worker starts from. Without it no entry
	// passes the base check.
	Branches Branches
	// Memory tells whether memory pressure is tight. Without it entries never
	// wait for memory.
	Memory Memory
	// Git records how far a worker's base moved since its plan was made; nil
	// uses the system git.
	Git Git
	// Clock stamps changes; nil uses time.Now.
	Clock func() time.Time
	// NewID returns a new unique id, used for lease tokens; nil uses
	// uuid.NewString.
	NewID func() string
	Log   *slog.Logger
	// Interval is how often Start dispatches; zero uses 15 seconds.
	Interval time.Duration
	// Lease is how long a claim holds an entry; zero uses two minutes. The
	// worker gate grants the spawn only while it holds.
	Lease time.Duration
}

// Service dispatches LADO's queue.
type Service struct {
	deps Deps
	// memoryUnreadable is set once a memory reading failed and was logged.
	memoryUnreadable atomic.Bool

	mu sync.Mutex
	// issueMisses counts, by entry, the reads of its issue that failed in a
	// row, for the next wait (issueUnreadable). It starts again empty when
	// the daemon does; an entry that leaves the queue while it waits for its
	// issue keeps its count until then.
	issueMisses map[domain.LadoQueueEntryID]int
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.Git == nil {
		deps.Git = execGit{}
	}
	if deps.Interval <= 0 {
		deps.Interval = defaultInterval
	}
	if deps.Lease <= 0 {
		deps.Lease = defaultLease
	}
	return &Service{deps: deps, issueMisses: map[domain.LadoQueueEntryID]int{}}
}

// Start dispatches at once and then every Interval until ctx is done. The
// returned channel closes once the loop has stopped.
func (s *Service) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.deps.Interval)
		defer ticker.Stop()
		for {
			if err := s.Tick(ctx, s.deps.Clock()); err != nil && ctx.Err() == nil {
				s.deps.Log.Warn("LADO dispatch pass failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

// Tick runs one dispatch pass at now. It first settles the claims of a
// dispatch that stopped midway, stops the runs past their deadline and the
// runs their task no longer wants; then, while run slots are free and memory
// allows, it claims entries one at a time, oldest first, and dispatches each.
// No entry is claimed while a lease could not be settled, because the lease
// may hide a worker, nor while the dispatch settings cannot be read; the
// rest of the pass does not wait for them. What an entry waits for or why it
// stopped is the entry's state, not an error; Tick returns the reads and
// writes that failed, joined.
func (s *Service) Tick(ctx context.Context, now time.Time) error {
	now = now.UTC()
	var problems []error
	settings, err := s.deps.Store.LadoDispatchSettings(ctx)
	known := &settings
	if err != nil {
		problems = append(problems, fmt.Errorf("read the dispatch settings: %w", err))
		known = nil
	}
	recovered := s.recover(ctx, now, known)
	if recovered != nil {
		problems = append(problems, recovered)
	}
	if err := s.stopOverdue(ctx, now); err != nil {
		problems = append(problems, err)
	}
	if err := s.stopUnwanted(ctx); err != nil {
		problems = append(problems, err)
	}
	if recovered == nil && known != nil {
		if err := s.admit(ctx, now, settings); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// Stop stops a worker session through AO. It is the task service's Killer,
// for a task cancelled while its worker runs.
func (s *Service) Stop(ctx context.Context, sessionID domain.SessionID) error {
	_, err := s.deps.Sessions.RequestKill(ctx, sessionID)
	return err
}
