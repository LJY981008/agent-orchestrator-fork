package ladoverify

// LADO fork: what the loop records for a task's stage timings (issue #34,
// step 0, decision D4 of its plan): when it first saw an execution's PR, when
// the head first passed or asked for confirmation, and, once the PR settled,
// what each session of the queue entry spent on investigation. A record that
// cannot be stored is logged; verification goes on without it.

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// markPRSeen records the first time the loop saw the execution's PR. The
// store keeps the first time; the loop writes once per execution.
func (s *Service) markPRSeen(ctx context.Context, exec domain.LadoExecution) {
	if exec.PR.URL == "" || s.timingMarked(s.prSeen, exec.SessionID) {
		return
	}
	if err := s.deps.Store.MarkLadoExecutionPRSeen(ctx, exec.SessionID, exec.PR.URL, s.now()); err != nil {
		s.deps.Log.Warn("LADO could not record when it saw a pull request", "session", exec.SessionID, "pr", exec.PR.URL, "err", err)
		return
	}
	s.setTimingMarked(s.prSeen, exec.SessionID)
}

// markVerified records the first time a head of the execution passed or
// asked for confirmation.
func (s *Service) markVerified(ctx context.Context, record domain.LadoVerification) {
	if outcome, _ := record.Overall(); outcome != domain.LadoVerifyPass && outcome != domain.LadoVerifyConfirm {
		return
	}
	if s.timingMarked(s.verified, record.ExecutionSessionID) {
		return
	}
	if err := s.deps.Store.MarkLadoExecutionVerified(ctx, record.ExecutionSessionID, record.ComputedAt); err != nil {
		s.deps.Log.Warn("LADO could not record when a head verified", "session", record.ExecutionSessionID, "head", record.HeadSHA, "err", err)
		return
	}
	s.setTimingMarked(s.verified, record.ExecutionSessionID)
}

// storeEntryMetrics stores the investigation metrics of every session of the
// execution's queue entry: its workers, parts, merge and integration.
func (s *Service) storeEntryMetrics(ctx context.Context, exec domain.LadoExecution) {
	executions, err := s.deps.Store.LadoExecutionsForEntry(ctx, exec.QueueEntryID)
	if err != nil {
		s.deps.Log.Warn("LADO could not read the executions to record their metrics", "entry", exec.QueueEntryID, "err", err)
		return
	}
	for _, e := range executions {
		facts, err := s.deps.Store.LadoSessionActivityFacts(ctx, e.SessionID)
		if err != nil {
			s.deps.Log.Warn("LADO could not read a session's activities", "session", e.SessionID, "err", err)
			continue
		}
		m := domain.SummarizeLadoActivities(facts)
		m.SessionID, m.TaskID, m.Role, m.UpdatedAt = e.SessionID, e.TaskID, domain.LadoSessionRoleOfExecution(e.Role), s.now()
		if err := s.deps.Store.UpsertLadoSessionMetrics(ctx, m); err != nil {
			s.deps.Log.Warn("LADO could not record a session's metrics", "session", e.SessionID, "err", err)
		}
	}
}

func (s *Service) timingMarked(marked map[domain.SessionID]bool, session domain.SessionID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return marked[session]
}

func (s *Service) setTimingMarked(marked map[domain.SessionID]bool, session domain.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	marked[session] = true
}
