// Package ladoqueue is LADO's dispatcher: it starts the worker of each
// approved task in its turn (docs/LJY-PRD/TDD.md §5.3). Approving a plan
// queues it (service/ladotask); the dispatcher claims queue entries, checks
// that the approval still holds, spawns the worker through AO's session
// service in Chat mode, and links the session to its entry as the task's
// execution.
package ladoqueue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

const (
	defaultInterval = 15 * time.Second
	defaultLease    = 2 * time.Minute
)

// Store keeps the queue: the task reads a dispatch checks and the primitives
// that claim entries and record what became of them.
type Store interface {
	LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error)
	LadoDispatchSettings(ctx context.Context) (domain.LadoDispatchSettings, error)
	CountLiveLadoExecutions(ctx context.Context, now time.Time) (int, map[domain.ProjectID]int, error)
	ClaimLadoQueueEntries(ctx context.Context, now, leaseUntil time.Time, limit int, newToken func() string) ([]domain.LadoQueueEntry, error)
	ReleaseLadoQueueEntry(ctx context.Context, id domain.LadoQueueEntryID, token string, nextAttempt time.Time, waiting string) (bool, error)
	HoldLadoQueueEntry(ctx context.Context, id domain.LadoQueueEntryID, token, waiting string, nextAttempt time.Time) (bool, error)
	MarkLadoQueueEntryReviewRequired(ctx context.Context, id domain.LadoQueueEntryID, token, reason string) (bool, error)
	MarkLadoQueueEntryBlocked(ctx context.Context, id domain.LadoQueueEntryID, token, reason string) (bool, error)
	FailLadoQueueEntry(ctx context.Context, id domain.LadoQueueEntryID, token, message string, at time.Time) (bool, error)
	CancelLadoQueueEntry(ctx context.Context, id domain.LadoQueueEntryID, token string, at time.Time) (bool, error)
	LinkLadoExecution(ctx context.Context, entryID domain.LadoQueueEntryID, token string, exec domain.LadoExecution) error
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
	if deps.Interval <= 0 {
		deps.Interval = defaultInterval
	}
	if deps.Lease <= 0 {
		deps.Lease = defaultLease
	}
	return &Service{deps: deps}
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

// Tick runs one dispatch pass at now: it claims as many entries as there are
// free run slots, oldest first, and dispatches each. What an entry waits for
// or why it stopped is the entry's state, not an error; Tick returns the
// writes that failed, joined.
func (s *Service) Tick(ctx context.Context, now time.Time) error {
	now = now.UTC()
	// S08 recovers the leases a stopped daemon left and stops runs past
	// their deadline here, before the slots are counted.
	settings, err := s.deps.Store.LadoDispatchSettings(ctx)
	if err != nil {
		return err
	}
	// Running executions and live leases both take a slot.
	taken, _, err := s.deps.Store.CountLiveLadoExecutions(ctx, now)
	if err != nil {
		return err
	}
	free := settings.MaxConcurrent - taken
	if free <= 0 {
		return nil
	}
	claimed, err := s.deps.Store.ClaimLadoQueueEntries(ctx, now, now.Add(s.deps.Lease), free, s.deps.NewID)
	if err != nil {
		return err
	}
	var problems []error
	for _, entry := range claimed {
		if ctx.Err() != nil {
			// Stopping: hand the rest back without spending their attempts.
			if err := s.handBack(ctx, entry); err != nil {
				problems = append(problems, fmt.Errorf("queue entry %s: %w", entry.ID, err))
			}
			continue
		}
		if err := s.dispatch(ctx, entry, settings, now); err != nil {
			problems = append(problems, fmt.Errorf("queue entry %s: %w", entry.ID, err))
		}
	}
	return errors.Join(problems...)
}
