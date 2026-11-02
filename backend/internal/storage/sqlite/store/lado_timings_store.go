package store

// LADO fork: stage timings and investigation metrics (issue #34, step 0). A
// task's timeline is read from the records LADO already keeps; the
// verification loop records when it first saw an execution's PR and when the
// head first verified, and the sessions' investigation metrics are counted
// from their conversations' activities (migrations_lado/0011).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// LadoTaskTimingFacts reads what a task's timeline and its sessions are
// derived from. ok is false for a task that does not exist.
func (s *Store) LadoTaskTimingFacts(ctx context.Context, id domain.LadoTaskID) (domain.LadoTaskTimingFacts, bool, error) {
	head, err := s.qr.GetLadoTaskTimingHead(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoTaskTimingFacts{}, false, nil
	}
	if err != nil {
		return domain.LadoTaskTimingFacts{}, false, fmt.Errorf("read LADO task timings: %w", err)
	}
	facts := domain.LadoTaskTimingFacts{
		TaskID:    domain.LadoTaskID(head.ID),
		ProjectID: domain.ProjectID(head.ProjectID),
		IssueRef:  domain.IssueID(head.IssueRef.String),
		Title:     head.IssueTitle,
		CreatedAt: head.CreatedAt.UTC(),
	}
	task := string(id)

	requests, err := s.qr.ListLadoPlanRequestTimings(ctx, task)
	if err != nil {
		return domain.LadoTaskTimingFacts{}, false, fmt.Errorf("read LADO plan request times: %w", err)
	}
	seen := map[domain.SessionID]bool{}
	addSession := func(session domain.SessionID, role domain.LadoSessionRole) {
		if session == "" || seen[session] {
			return
		}
		seen[session] = true
		facts.PlanSessions = append(facts.PlanSessions, domain.LadoTimingSession{SessionID: session, Role: role})
	}
	for _, r := range requests {
		facts.PlanRequestedAt = append(facts.PlanRequestedAt, r.RequestedAt.UTC())
		// An orchestrator request's session is the project's orchestrator,
		// and a worker request holds a placeholder until its designer starts.
		session := domain.SessionID(r.SessionID)
		if r.Kind == string(domain.LadoPlanRequestWorker) && session != domain.LadoPlanRequestPlaceholder(domain.LadoPlanRequestID(r.ID)) {
			addSession(session, domain.LadoSessionDesigner)
		}
	}
	critics, err := s.qr.ListLadoPlanCritiqueSessionsByTask(ctx, task)
	if err != nil {
		return domain.LadoTaskTimingFacts{}, false, fmt.Errorf("read LADO plan critic sessions: %w", err)
	}
	for _, session := range critics {
		addSession(domain.SessionID(session), domain.LadoSessionCritic)
	}

	revisions, err := s.qr.ListLadoPlanRevisionTimes(ctx, task)
	if err != nil {
		return domain.LadoTaskTimingFacts{}, false, fmt.Errorf("read LADO plan revision times: %w", err)
	}
	for _, at := range revisions {
		facts.RevisionCreatedAt = append(facts.RevisionCreatedAt, at.UTC())
	}
	approvals, err := s.qr.ListLadoPlanApprovalTimes(ctx, task)
	if err != nil {
		return domain.LadoTaskTimingFacts{}, false, fmt.Errorf("read LADO plan approval times: %w", err)
	}
	for _, a := range approvals {
		facts.Approvals = append(facts.Approvals, domain.LadoTimingApproval{ID: domain.LadoPlanApprovalID(a.ID), At: a.ApprovedAt.UTC()})
	}
	entries, err := s.qr.ListLadoQueueEntryTimings(ctx, task)
	if err != nil {
		return domain.LadoTaskTimingFacts{}, false, fmt.Errorf("read LADO queue entry times: %w", err)
	}
	for _, e := range entries {
		facts.QueueEntries = append(facts.QueueEntries, domain.LadoTimingQueueEntry{
			ID:             domain.LadoQueueEntryID(e.ID),
			ApprovalID:     domain.LadoPlanApprovalID(e.PlanApprovalID),
			PartsStartedAt: ladoTimeFromNull(e.PartsStartedAt),
		})
	}
	executions, err := s.qr.ListLadoExecutionTimings(ctx, task)
	if err != nil {
		return domain.LadoTaskTimingFacts{}, false, fmt.Errorf("read LADO execution times: %w", err)
	}
	for _, e := range executions {
		facts.Executions = append(facts.Executions, domain.LadoTimingExecution{
			SessionID:         domain.SessionID(e.SessionID),
			Role:              e.Role,
			PartID:            e.PartID,
			ApprovalID:        domain.LadoPlanApprovalID(e.PlanApprovalID),
			QueueEntryID:      domain.LadoQueueEntryID(e.QueueEntryID),
			StartedAt:         e.StartedAt.UTC(),
			PRSeenAt:          ladoTimeFromNull(e.PRSeenAt),
			PRSeenEstimated:   e.PRSeenEstimated != 0,
			VerifiedAt:        ladoTimeFromNull(e.VerifiedAt),
			VerifiedEstimated: e.VerifiedEstimated != 0,
			SettledAt:         ladoTimeFromNull(e.SettledAt),
			SettledState:      e.SettledPRState,
		})
	}
	integrations, err := s.qr.ListLadoIntegrationApprovalTimes(ctx, task)
	if err != nil {
		return domain.LadoTaskTimingFacts{}, false, fmt.Errorf("read LADO integration approval times: %w", err)
	}
	for _, a := range integrations {
		facts.IntegrationApprovals = append(facts.IntegrationApprovals, domain.LadoTimingIntegrationApproval{
			SessionID: domain.SessionID(a.ExecutionSessionID), At: a.ApprovedAt.UTC(),
		})
	}
	return facts, true, nil
}

// ListLadoTaskIDsForTimings returns up to limit of a project's tasks that
// have an issue, newest first.
func (s *Store) ListLadoTaskIDsForTimings(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.LadoTaskID, error) {
	if limit < 1 {
		return nil, errors.New("list LADO tasks for timings: a positive limit is required")
	}
	rows, err := s.qr.ListLadoTaskIDsForTimings(ctx, gen.ListLadoTaskIDsForTimingsParams{
		ProjectID: string(projectID), Limit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list LADO tasks for timings: %w", err)
	}
	ids := make([]domain.LadoTaskID, 0, len(rows))
	for _, id := range rows {
		ids = append(ids, domain.LadoTaskID(id))
	}
	return ids, nil
}

// MarkLadoExecutionPRSeen records that the verification loop saw execution
// session's PR at at. The first time recorded, estimated or not, stays.
func (s *Store) MarkLadoExecutionPRSeen(ctx context.Context, session domain.SessionID, prURL string, at time.Time) error {
	if session == "" || prURL == "" || at.IsZero() {
		return errors.New("mark LADO execution PR seen: a session, a PR and a time are required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if err := s.qw.MarkLadoExecutionPRSeen(ctx, gen.MarkLadoExecutionPRSeenParams{
		SessionID: string(session), PRURL: prURL, PRSeenAt: ladoNullTime(at),
	}); err != nil {
		return fmt.Errorf("mark LADO execution PR seen: %w", err)
	}
	return nil
}

// MarkLadoExecutionVerified records that execution session's head passed or
// asked for confirmation at at. The first time recorded stays.
func (s *Store) MarkLadoExecutionVerified(ctx context.Context, session domain.SessionID, at time.Time) error {
	if session == "" || at.IsZero() {
		return errors.New("mark LADO execution verified: a session and a time are required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if err := s.qw.MarkLadoExecutionVerified(ctx, gen.MarkLadoExecutionVerifiedParams{
		SessionID: string(session), VerifiedAt: ladoNullTime(at),
	}); err != nil {
		return fmt.Errorf("mark LADO execution verified: %w", err)
	}
	return nil
}

// LadoSessionActivityFacts reads what the investigation metrics count of a
// session's conversation, in order; none for a session without one.
func (s *Store) LadoSessionActivityFacts(ctx context.Context, session domain.SessionID) ([]domain.LadoActivityFact, error) {
	rows, err := s.qr.ListLadoSessionActivityFacts(ctx, &session)
	if err != nil {
		return nil, fmt.Errorf("read LADO session activities: %w", err)
	}
	facts := make([]domain.LadoActivityFact, 0, len(rows))
	for _, r := range rows {
		facts = append(facts, domain.LadoActivityFact{
			Kind: r.Kind, ToolKind: r.ToolKind, ToolName: r.ToolName, Command: r.Command,
			CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
		})
	}
	return facts, nil
}

// UpsertLadoSessionMetrics stores a session's investigation metrics,
// replacing the ones stored before. A zero UpdatedAt is now.
func (s *Store) UpsertLadoSessionMetrics(ctx context.Context, m domain.LadoSessionMetrics) error {
	if m.SessionID == "" || m.TaskID == "" || !m.Role.Valid() {
		return fmt.Errorf("store LADO session metrics: a session, a task and a known role are required, not %q", m.Role)
	}
	if m.ToolCalls < 0 || m.ReadCalls < 0 || m.SearchCalls < 0 || m.CommandCalls < 0 || m.EditCalls < 0 || m.Investigate < 0 {
		return errors.New("store LADO session metrics: counts and times cannot be negative")
	}
	updated := m.UpdatedAt
	if updated.IsZero() {
		updated = time.Now()
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertLadoSessionMetrics(ctx, gen.UpsertLadoSessionMetricsParams{
		SessionID:       string(m.SessionID),
		TaskID:          string(m.TaskID),
		Role:            string(m.Role),
		ToolCalls:       int64(m.ToolCalls),
		ReadCalls:       int64(m.ReadCalls),
		SearchCalls:     int64(m.SearchCalls),
		CommandCalls:    int64(m.CommandCalls),
		EditCalls:       int64(m.EditCalls),
		FirstActivityAt: ladoNullTime(m.FirstActivityAt),
		FirstEditAt:     ladoNullTime(m.FirstEditAt),
		LastActivityAt:  ladoNullTime(m.LastActivityAt),
		InvestigateMs:   m.Investigate.Milliseconds(),
		UpdatedAt:       updated.UTC(),
	}); err != nil {
		return fmt.Errorf("store LADO session metrics: %w", err)
	}
	return nil
}

// ListLadoSessionMetrics returns the stored investigation metrics of a task's
// sessions, in the order they started.
func (s *Store) ListLadoSessionMetrics(ctx context.Context, task domain.LadoTaskID) ([]domain.LadoSessionMetrics, error) {
	rows, err := s.qr.ListLadoSessionMetricsByTask(ctx, string(task))
	if err != nil {
		return nil, fmt.Errorf("list LADO session metrics: %w", err)
	}
	metrics := make([]domain.LadoSessionMetrics, 0, len(rows))
	for _, r := range rows {
		metrics = append(metrics, domain.LadoSessionMetrics{
			SessionID:       domain.SessionID(r.SessionID),
			TaskID:          domain.LadoTaskID(r.TaskID),
			Role:            domain.LadoSessionRole(r.Role),
			ToolCalls:       int(r.ToolCalls),
			ReadCalls:       int(r.ReadCalls),
			SearchCalls:     int(r.SearchCalls),
			CommandCalls:    int(r.CommandCalls),
			EditCalls:       int(r.EditCalls),
			FirstActivityAt: ladoTimeFromNull(r.FirstActivityAt),
			FirstEditAt:     ladoTimeFromNull(r.FirstEditAt),
			LastActivityAt:  ladoTimeFromNull(r.LastActivityAt),
			Investigate:     time.Duration(r.InvestigateMs) * time.Millisecond,
			Stored:          true,
			UpdatedAt:       r.UpdatedAt.UTC(),
		})
	}
	return metrics, nil
}
