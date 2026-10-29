package ladotask

// LADO fork: how long a task's stages took and what its sessions spent on
// investigation (issue #34, step 0, decisions D6 and D7 of its plan). The
// timeline is derived from the stored facts at read time. A session's
// investigation metrics are stored when LADO stops a plan request's sessions
// (refreshMetrics) and when verification settles an execution's PR; a
// session without a stored row is counted from its activities at read time
// and not stored, so a read never writes.

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// The number of recent tasks a project's timings read.
const (
	TimingsDefaultLimit = 20
	TimingsMaxLimit     = 100
)

// TimingStore reads the facts of a task's timeline and keeps its sessions'
// investigation metrics (*store.Store).
type TimingStore interface {
	LadoTaskTimingFacts(ctx context.Context, id domain.LadoTaskID) (domain.LadoTaskTimingFacts, bool, error)
	ListLadoTaskIDsForTimings(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.LadoTaskID, error)
	LadoSessionActivityFacts(ctx context.Context, session domain.SessionID) ([]domain.LadoActivityFact, error)
	UpsertLadoSessionMetrics(ctx context.Context, m domain.LadoSessionMetrics) error
	ListLadoSessionMetrics(ctx context.Context, task domain.LadoTaskID) ([]domain.LadoSessionMetrics, error)
}

// TaskTimings returns a task's timeline and the investigation metrics of its
// plan and execution sessions: the stored ones, and for a session without a
// stored row, the ones its activities give now (Stored false).
func (s *Service) TaskTimings(ctx context.Context, id domain.LadoTaskID) (domain.LadoTaskTimings, error) {
	facts, ok, err := s.deps.Store.LadoTaskTimingFacts(ctx, id)
	if err != nil {
		s.deps.Log.Warn("LADO could not read a task's timings", "task", id, "err", err)
		return domain.LadoTaskTimings{}, apierr.Internal("LADO_TIMINGS_LOAD_FAILED", "Failed to load the task's timings")
	}
	if !ok || facts.IssueRef == "" {
		return domain.LadoTaskTimings{}, apierr.NotFound("LADO_TASK_NOT_FOUND", "Unknown task")
	}
	stored, err := s.deps.Store.ListLadoSessionMetrics(ctx, id)
	if err != nil {
		s.deps.Log.Warn("LADO could not read a task's session metrics", "task", id, "err", err)
		return domain.LadoTaskTimings{}, apierr.Internal("LADO_TIMINGS_LOAD_FAILED", "Failed to load the task's timings")
	}
	return domain.LadoTaskTimings{
		TaskID:   facts.TaskID,
		IssueRef: facts.IssueRef,
		Title:    facts.Title,
		Timeline: domain.LadoTaskTimelineOf(facts),
		Sessions: s.taskSessionMetrics(ctx, facts, stored),
	}, nil
}

// taskSessionMetrics lists the metrics of the plan sessions, then of the
// executions, then any other stored row. A session without a stored row and
// without activities, such as one that was deleted, is left out.
func (s *Service) taskSessionMetrics(ctx context.Context, facts domain.LadoTaskTimingFacts, stored []domain.LadoSessionMetrics) []domain.LadoSessionMetrics {
	byID := make(map[domain.SessionID]domain.LadoSessionMetrics, len(stored))
	for _, m := range stored {
		byID[m.SessionID] = m
	}
	sessions := make([]domain.LadoTimingSession, 0, len(facts.PlanSessions)+len(facts.Executions))
	sessions = append(sessions, facts.PlanSessions...)
	for _, e := range facts.Executions {
		sessions = append(sessions, domain.LadoTimingSession{SessionID: e.SessionID, Role: domain.LadoSessionRoleOfExecution(e.Role)})
	}
	out := []domain.LadoSessionMetrics{}
	seen := map[domain.SessionID]bool{}
	for _, session := range sessions {
		if session.SessionID == "" || seen[session.SessionID] {
			continue
		}
		seen[session.SessionID] = true
		if m, ok := byID[session.SessionID]; ok {
			out = append(out, m)
			continue
		}
		m, found, err := s.sessionMetrics(ctx, facts.TaskID, session)
		if err != nil {
			s.deps.Log.Warn("LADO could not count a session's investigation", "task", facts.TaskID, "session", session.SessionID, "err", err)
			continue
		}
		if found {
			out = append(out, m)
		}
	}
	for _, m := range stored {
		if !seen[m.SessionID] {
			out = append(out, m)
		}
	}
	return out
}

// sessionMetrics counts a session's investigation from its activities, not
// stored; found is false for a session without any.
func (s *Service) sessionMetrics(ctx context.Context, task domain.LadoTaskID, session domain.LadoTimingSession) (domain.LadoSessionMetrics, bool, error) {
	facts, err := s.deps.Store.LadoSessionActivityFacts(ctx, session.SessionID)
	if err != nil || len(facts) == 0 {
		return domain.LadoSessionMetrics{}, false, err
	}
	m := domain.SummarizeLadoActivities(facts)
	m.SessionID, m.TaskID, m.Role, m.UpdatedAt = session.SessionID, task, session.Role, s.deps.Clock().UTC()
	return m, true, nil
}

// ProjectTimings returns a project's baseline over its newest tasks with an
// issue: each task's timings and the medians over them. A limit of 0 reads
// TimingsDefaultLimit tasks.
func (s *Service) ProjectTimings(ctx context.Context, projectID domain.ProjectID, limit int) (domain.LadoTimingsSummary, error) {
	if projectID == "" {
		return domain.LadoTimingsSummary{}, apierr.Invalid("LADO_TIMINGS_PROJECT_REQUIRED", "Name the project.", nil)
	}
	if limit == 0 {
		limit = TimingsDefaultLimit
	}
	if limit < 1 || limit > TimingsMaxLimit {
		return domain.LadoTimingsSummary{}, apierr.Invalid("LADO_TIMINGS_LIMIT_INVALID", "Read between 1 and 100 tasks.",
			map[string]any{"min": 1, "max": TimingsMaxLimit})
	}
	if _, err := s.project(ctx, projectID); err != nil {
		return domain.LadoTimingsSummary{}, err
	}
	ids, err := s.deps.Store.ListLadoTaskIDsForTimings(ctx, projectID, limit)
	if err != nil {
		s.deps.Log.Warn("LADO could not list a project's tasks for timings", "project", projectID, "err", err)
		return domain.LadoTimingsSummary{}, apierr.Internal("LADO_TIMINGS_LOAD_FAILED", "Failed to load the project's timings")
	}
	tasks := make([]domain.LadoTaskTimings, 0, len(ids))
	for _, id := range ids {
		timings, err := s.TaskTimings(ctx, id)
		var apiErr *apierr.Error
		if errors.As(err, &apiErr) && apiErr.Kind == apierr.KindNotFound {
			continue // deleted since it was listed
		}
		if err != nil {
			return domain.LadoTimingsSummary{}, err
		}
		tasks = append(tasks, timings)
	}
	return domain.SummarizeLadoTimings(projectID, tasks), nil
}

// storePlanSessionMetrics stores the investigation metrics of a plan worker
// request's designer and critics.
func (s *Service) storePlanSessionMetrics(ctx context.Context, req domain.LadoPlanRequest, critiques []domain.LadoPlanCritique) {
	if !req.IsWorker() {
		return
	}
	sessions := []domain.LadoTimingSession{{SessionID: req.WorkerSession(), Role: domain.LadoSessionDesigner}}
	for _, c := range critiques {
		sessions = append(sessions, domain.LadoTimingSession{SessionID: c.SessionID, Role: domain.LadoSessionCritic})
	}
	seen := map[domain.SessionID]bool{}
	for _, session := range sessions {
		if session.SessionID == "" || seen[session.SessionID] {
			continue
		}
		seen[session.SessionID] = true
		facts, err := s.deps.Store.LadoSessionActivityFacts(ctx, session.SessionID)
		if err != nil {
			s.deps.Log.Warn("LADO could not read a plan worker's activities", "request", req.ID, "session", session.SessionID, "err", err)
			continue
		}
		m := domain.SummarizeLadoActivities(facts)
		m.SessionID, m.TaskID, m.Role, m.UpdatedAt = session.SessionID, req.TaskID, session.Role, s.deps.Clock().UTC()
		if err := s.deps.Store.UpsertLadoSessionMetrics(ctx, m); err != nil {
			s.deps.Log.Warn("LADO could not record a plan worker's metrics", "request", req.ID, "session", session.SessionID, "err", err)
		}
	}
}
