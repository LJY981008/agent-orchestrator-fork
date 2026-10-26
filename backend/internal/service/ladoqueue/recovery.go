package ladoqueue

// Recovery of the claims a dispatch left when it stopped midway
// (docs/LJY-PRD/TDD.md §5.3, gate 13): the daemon stopped between the spawn
// and the link, or a write after the spawn failed. AO finds the session of an
// attempt by its client request id, so the attempt that started a worker
// keeps that one worker.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// abandonedAttempt is the error of an entry whose last attempt stopped before
// its worker started.
const abandonedAttempt = "the dispatch stopped before the worker started"

// recover settles every entry a lease token holds, before a pass claims
// anything: a claim takes an entry whose lease expired, which may have its
// worker already. Linking a worker needs the dispatch settings, for its
// deadline; while they cannot be read (nil), an entry with a worker to link
// keeps its lease for a later pass. It returns the reads and writes that
// failed, joined.
func (s *Service) recover(ctx context.Context, now time.Time, settings *domain.LadoDispatchSettings) error {
	leased, err := s.deps.Store.LeasedLadoQueueEntries(ctx)
	if err != nil {
		return fmt.Errorf("list the leased queue entries: %w", err)
	}
	var problems []error
	for _, entry := range leased {
		if err := s.recoverEntry(ctx, entry, now, settings); err != nil {
			problems = append(problems, fmt.Errorf("recover queue entry %s: %w", entry.ID, err))
		}
	}
	return errors.Join(problems...)
}

// recoverEntry settles one leased entry by the session AO keeps for the
// client request id of its attempt. A session the dispatcher did not start
// for the attempt is no worker of the entry's: the attempt failed. A session
// AO finished starting is the entry's execution whatever became of the
// lease: only the link is missing. While the lease holds, a dispatch may
// still be starting the worker, so the entry is left alone. Once it expired
// the attempt is over: a session AO never finished starting is stopped, and
// the entry is due again; one that could not be stopped keeps the lease, so
// that no next attempt starts a second worker beside it.
func (s *Service) recoverEntry(ctx context.Context, entry domain.LadoQueueEntry, now time.Time, settings *domain.LadoDispatchSettings) error {
	session, found, err := s.deps.Store.GetSessionByClientRequestID(ctx, domain.LadoClientRequestID(entry.ID, entry.AttemptCount))
	if err != nil {
		return err
	}
	if found {
		grant, err := s.deps.Store.LadoDispatchGrant(ctx, entry.ID, entry.AttemptCount, now)
		if err != nil {
			return err
		}
		if !startedFor(session, entry.ProjectID, grant.Hash) {
			s.deps.Log.Warn("LADO found another session under the client request id of a queue entry's attempt",
				"entry", entry.ID, "attempt", entry.AttemptCount, "session", session.ID)
			return s.attemptFailed(ctx, entry, foreignSession)
		}
	}
	switch {
	case found && session.ClientRequestCommitted:
		if settings == nil {
			return fmt.Errorf("link session %s: the dispatch settings could not be read", session.ID)
		}
		return s.relink(ctx, entry, session, *settings)
	case entry.LeaseExpiresAt.After(now):
		return nil
	case found && !session.IsTerminated:
		if err := s.stopUnfinished(ctx, entry, session.ID); err != nil {
			return err
		}
	}
	return s.abandon(ctx, entry, now)
}

// relink records the session AO started for the entry's attempt as its
// execution, which started when AO created the session. Where the base stood
// then is no longer known, so the execution records no base move.
func (s *Service) relink(ctx context.Context, entry domain.LadoQueueEntry, session domain.SessionRecord, settings domain.LadoDispatchSettings) error {
	ctx = context.WithoutCancel(ctx)
	err := s.link(ctx, entry, domain.LadoExecution{SessionID: session.ID, StartedAt: session.CreatedAt.UTC()}, settings)
	if errors.Is(err, domain.ErrLadoLeaseLost) {
		s.deps.Log.Warn("LADO lost the claim of a queue entry before linking the worker it started",
			"entry", entry.ID, "task", entry.TaskID, "session", session.ID)
		return nil
	}
	if err != nil {
		return err
	}
	s.deps.Log.Info("LADO linked the worker a stopped dispatch had started", "entry", entry.ID, "task", entry.TaskID,
		"session", session.ID, "attempt", entry.AttemptCount)
	s.stopIfUnwanted(ctx, entry, session.ID)
	return nil
}

// stopUnfinished stops a session AO began for an expired attempt but never
// finished starting, so that the next attempt's worker is the task's only
// one. A stop that failed while the session still runs is an error: the
// entry keeps its lease, and the next pass tries again.
func (s *Service) stopUnfinished(ctx context.Context, entry domain.LadoQueueEntry, id domain.SessionID) error {
	ctx = context.WithoutCancel(ctx)
	_, err := s.deps.Sessions.RequestKill(ctx, id)
	if err == nil {
		s.deps.Log.Info("LADO stopped a worker that never finished starting", "entry", entry.ID, "session", id)
		return nil
	}
	stopped := problem(err, "the session could not be stopped")
	rec, found, readErr := s.deps.Store.GetSessionByClientRequestID(ctx, domain.LadoClientRequestID(entry.ID, entry.AttemptCount))
	if readErr == nil && (!found || rec.IsTerminated) {
		s.deps.Log.Info("LADO's stop of a worker that never finished starting failed, but the session ended", "entry", entry.ID, "session", id,
			"problem", stopped)
		return nil
	}
	s.deps.Log.Warn("LADO could not stop a worker that never finished starting and keeps its queue entry's claim", "entry", entry.ID, "session", id,
		"problem", stopped)
	return fmt.Errorf("stop session %s: %s", id, stopped)
}

// abandon gives up an expired attempt that started no worker: the entry is
// due again at once, or fails when that was its last attempt.
func (s *Service) abandon(ctx context.Context, entry domain.LadoQueueEntry, now time.Time) error {
	ctx = context.WithoutCancel(ctx)
	if entry.AttemptCount >= maxAttempts {
		ok, err := s.deps.Store.FailLadoQueueEntry(ctx, entry.ID, entry.LeaseToken, abandonedAttempt, now)
		return s.settled(entry, "failed", ok, err, slog.LevelInfo)
	}
	ok, err := s.deps.Store.ReleaseLadoQueueEntry(ctx, entry.ID, entry.LeaseToken, now, domain.LadoWaitingRetry)
	return s.settled(entry, "waiting: "+domain.LadoWaitingRetry, ok, err, slog.LevelInfo)
}
