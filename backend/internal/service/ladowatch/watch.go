package ladowatch

// What LADO does with one running worker on a tick: the usage limit (plan
// steps 12 and 14) and the stall (plan steps 13 and 14).

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// sameMoment is how close two readings of one turn's end may be: the store
// keeps times to the nanosecond, so only another turn is further apart.
const sameMoment = time.Second

// watchLimit records a worker whose newest turn failed at its usage limit,
// with the reset its message gives, and the limit of the account the
// session runs on; it records the limit lifted once a later turn ended; and
// it resumes the worker once the limit reset (resumeIfDue).
func (s *Service) watchLimit(ctx context.Context, w *worker, now time.Time) error {
	lim := w.watch.LadoWatchLimit
	if w.hasTurn && w.turn.State == domain.TurnStateFailed {
		seen := turnEnd(w.turn)
		if msg, ok := parseLimit(w.turn.ErrorMessage, seen, s.deps.Local, domain.LadoLimitGuessedReset); ok {
			if lim.LimitState != domain.LadoLimitLimited || seen.Sub(lim.LimitSeenAt) > sameMoment {
				if err := s.recordLimit(ctx, w, msg, seen, now); err != nil {
					return err
				}
			}
			return s.resumeIfDue(ctx, w, now)
		}
	}
	if lim.LimitState != domain.LadoLimitLimited {
		return nil
	}
	if w.hasTurn && w.turn.State.Terminal() && turnEnd(w.turn).Sub(lim.LimitSeenAt) > sameMoment {
		return s.limitLifted(ctx, w, now)
	}
	return s.resumeIfDue(ctx, w, now)
}

// recordLimit records a new limit stop of the worker: its watch row, the
// account's limit, an audit event and an alert. The resumes it already had
// still count against the session's cap.
func (s *Service) recordLimit(ctx context.Context, w *worker, msg limitMessage, seen, now time.Time) error {
	next := w.watch.LadoWatchLimit
	next.LimitState, next.LimitResetAt, next.LimitResetGuessed, next.LimitSeenAt = domain.LadoLimitLimited, msg.ResetAt, msg.Guessed, seen
	next.Stops++
	if _, err := s.deps.Store.SetLadoSessionWatchLimit(ctx, w.session.ID, next, now); err != nil {
		return err
	}
	w.watch.LadoWatchLimit = next
	if key, ok := s.accountKey(ctx, *w); ok {
		if err := s.deps.Store.RecordLadoAccountLimit(ctx, domain.LadoAccountLimit{
			AccountKey: key, SessionID: w.session.ID, ResetAt: msg.ResetAt, ResetGuessed: msg.Guessed, DetectedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
	}
	s.audit(ctx, "lado.limit_detected", w.session.ID, now, map[string]any{
		"task": w.task.ID, "limit": msg.Name, "resetAt": msg.ResetAt, "resetGuessed": msg.Guessed,
	})
	// Each stop is its own alert, also past the resume cap or with waking off.
	s.alert(ctx, *w, domain.LadoAlertLimited, next.Stops, msg.ResetAt.Unix(), 0, now)
	s.deps.Log.Info("LADO found a worker stopped at its usage limit", "task", w.task.ID, "session", w.session.ID,
		"limit", msg.Name, "reset", msg.ResetAt, "guessed", msg.Guessed)
	return nil
}

// limitLifted records that a worker moved past its limit: a turn after the
// stop ended. One that completed also releases its account's limit, which
// lets new workers start before the reset LADO guessed.
func (s *Service) limitLifted(ctx context.Context, w *worker, now time.Time) error {
	next := w.watch.LadoWatchLimit
	next.LimitState, next.LimitResetAt, next.LimitResetGuessed = domain.LadoLimitNone, time.Time{}, false
	if _, err := s.deps.Store.SetLadoSessionWatchLimit(ctx, w.session.ID, next, now); err != nil {
		return err
	}
	w.watch.LadoWatchLimit = next
	if w.turn.State != domain.TurnStateCompleted {
		return nil
	}
	if key, ok := s.accountKey(ctx, *w); ok {
		if _, err := s.deps.Store.ReleaseLadoAccountLimit(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// resumeIfDue sends the limit resume message to a worker whose limit reset
// LadoWakeResumeDelay ago: once a stop, at most LadoWakeMaxResumes times a
// session, only while it rests and has not asked the owner, and only where
// the project lets LADO wake its workers. A worker busy at the reset is
// resumed on a later tick; one whose newest turn the owner stopped is not.
func (s *Service) resumeIfDue(ctx context.Context, w *worker, now time.Time) error {
	lim := w.watch.LadoWatchLimit
	switch {
	case lim.LimitState != domain.LadoLimitLimited,
		now.Before(lim.LimitResetAt.Add(domain.LadoWakeResumeDelay)),
		lim.Resumes >= domain.LadoWakeMaxResumes,
		!lim.ResumedAt.IsZero() && !lim.ResumedAt.Before(lim.LimitSeenAt),
		!w.settings.WakeEnabled, s.deps.Messenger == nil,
		w.answering(), !w.resting(), w.ownerStopped():
		return nil
	}
	next := lim
	next.Resumes, next.ResumedAt = lim.Resumes+1, now
	// The count is written before the message goes, so a failed write never
	// sends it twice; a message that did not go gives the count back.
	if _, err := s.deps.Store.SetLadoSessionWatchLimit(ctx, w.session.ID, next, now); err != nil {
		return err
	}
	if err := s.send(ctx, *w, "limit_resume", domain.LadoWakeLimitResumeText, now, map[string]any{
		"resume": next.Resumes, "resetAt": lim.LimitResetAt,
	}); err != nil {
		if _, undoErr := s.deps.Store.SetLadoSessionWatchLimit(context.WithoutCancel(ctx), w.session.ID, lim, now); undoErr != nil {
			s.deps.Log.Warn("LADO could not give back a resume that did not go", "session", w.session.ID, "err", undoErr)
		}
		return err
	}
	w.watch.LadoWatchLimit = next
	s.alert(ctx, *w, domain.LadoAlertLimitResumed, next.Resumes, int64(next.Resumes), domain.LadoWakeMaxResumes, now)
	return nil
}

// stallWatched reports whether LADO watches an execution of role for stalls:
// the one worker of a task without parts, and the part, merge and
// integration workers. Plan workers remind themselves (service/ladotask).
func stallWatched(role string) bool {
	switch role {
	case domain.LadoRoleSingle, domain.LadoRolePart, domain.LadoRoleMerge, domain.LadoRoleIntegration:
		return true
	}
	return false
}

// watchStall records a worker that rested past the project's stall time
// without a report that waits for the owner or says it is done, and whose
// run opened no pull request: its first stall gets the nudge message, where
// the project lets LADO wake its workers, and a stall after that, or one LADO
// may not wake, an alert only, numbered by the session's stalls. A worker
// whose newest turn the owner stopped gets an alert only too. A worker at its
// usage limit is not stalled.
// Once the worker moves again, the stall is over.
func (s *Service) watchStall(ctx context.Context, w *worker, now time.Time) error {
	st := w.watch.LadoWatchStall
	stalled := w.watch.LimitState != domain.LadoLimitLimited && stallWatched(w.exec.Role) &&
		w.resting() && !w.answering() && now.Sub(quietSince(*w)) >= w.settings.Stall()
	if stalled {
		// The PR facts are read only for a worker that otherwise stalls.
		exec, ok, err := s.deps.Store.LadoExecution(ctx, w.exec.SessionID)
		if err != nil {
			return fmt.Errorf("read the execution: %w", err)
		}
		if ok && exec.PR.URL != "" {
			// CI and reviews of an open PR have their own messages.
			stalled = false
		}
	}
	if !stalled {
		if st.StallState != domain.LadoStallStalled {
			return nil
		}
		next := st
		next.StallState, next.StalledAt = domain.LadoStallNone, time.Time{}
		_, err := s.deps.Store.SetLadoSessionWatchStall(ctx, w.session.ID, next, now)
		return err
	}
	minutes := int64(now.Sub(quietSince(*w)) / time.Minute)
	fresh := st.StallState != domain.LadoStallStalled
	if fresh {
		next := st
		next.StallState, next.StalledAt = domain.LadoStallStalled, now
		next.Stalls++
		if _, err := s.deps.Store.SetLadoSessionWatchStall(ctx, w.session.ID, next, now); err != nil {
			return err
		}
		st = next
		w.watch.LadoWatchStall = next
		s.deps.Log.Info("LADO found a worker stalled", "task", w.task.ID, "session", w.session.ID, "minutes", minutes)
	}
	if w.settings.WakeEnabled && s.deps.Messenger != nil && st.Nudges < domain.LadoWakeMaxNudges && !w.ownerStopped() {
		return s.nudge(ctx, w, st, minutes, now)
	}
	if fresh {
		s.alert(ctx, *w, domain.LadoAlertStalled, st.Stalls, minutes, int64(w.settings.StallMinutes), now)
	}
	return nil
}

// nudge sends the stall nudge message to a stalled worker. A message that
// did not go is tried again on the next tick of the same stall.
func (s *Service) nudge(ctx context.Context, w *worker, st domain.LadoWatchStall, minutes int64, now time.Time) error {
	next := st
	next.Nudges, next.NudgedAt = st.Nudges+1, now
	if _, err := s.deps.Store.SetLadoSessionWatchStall(ctx, w.session.ID, next, now); err != nil {
		return err
	}
	if err := s.send(ctx, *w, "stall_nudge", domain.LadoWakeStallNudgeText, now, map[string]any{"stallMinutes": minutes}); err != nil {
		if _, undoErr := s.deps.Store.SetLadoSessionWatchStall(context.WithoutCancel(ctx), w.session.ID, st, now); undoErr != nil {
			s.deps.Log.Warn("LADO could not give back a nudge that did not go", "session", w.session.ID, "err", undoErr)
		}
		return err
	}
	w.watch.LadoWatchStall = next
	s.alert(ctx, *w, domain.LadoAlertStallNudged, next.Nudges, minutes, int64(w.settings.StallMinutes), now)
	return nil
}

// turnEnd is when a turn ended, or when it started or was asked for when the
// end is unknown: a time that does not move from one tick to the next.
func turnEnd(t domain.ConversationTurn) time.Time {
	switch {
	case t.CompletedAt != nil:
		return t.CompletedAt.UTC()
	case t.StartedAt != nil:
		return t.StartedAt.UTC()
	}
	return t.RequestedAt.UTC()
}

// quietSince is when the worker last did something: its last activity or
// the end of its newest turn, whichever is later.
func quietSince(w worker) time.Time {
	last := w.session.Activity.LastActivityAt.UTC()
	if w.hasTurn {
		if end := turnEnd(w.turn); end.After(last) {
			last = end
		}
	}
	return last
}
