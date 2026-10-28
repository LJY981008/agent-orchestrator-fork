package store

// LADO fork: plan worker requests, their critiques and the revision metrics
// (docs/LJY-PRD/TDD.md §5.1). Each transition of a request writes only when
// the request is still where the transition starts, and its audit event
// commits with it.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ladoPlanWrite runs one guarded write and, when it changed a row, appends
// event in the same transaction. It reports whether the row changed.
func (s *Store) ladoPlanWrite(ctx context.Context, what string, event *domain.LadoAuditEvent, write func(q *gen.Queries) (int64, error)) (bool, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	changed := false
	err := s.inTx(ctx, what, func(q *gen.Queries) error {
		n, err := write(q)
		if err != nil || n == 0 {
			return err
		}
		changed = true
		if event == nil {
			return nil
		}
		_, err = appendLadoAuditEventTx(ctx, q, *event)
		return err
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// ListOpenLadoPlanWorkerRequests returns the plan worker requests that neither
// failed nor were cleaned up, oldest first.
func (s *Store) ListOpenLadoPlanWorkerRequests(ctx context.Context) ([]domain.LadoPlanRequest, error) {
	rows, err := s.qr.ListOpenLadoPlanWorkerRequests(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO plan worker requests: %w", err)
	}
	return ladoPlanRequestsFromGen(rows), nil
}

// SetLadoPlanRequestWaiting records why a request's designer has not started.
func (s *Store) SetLadoPlanRequestWaiting(ctx context.Context, id domain.LadoPlanRequestID, reason string) error {
	_, err := s.ladoPlanWrite(ctx, "set LADO plan request waiting", nil, func(q *gen.Queries) (int64, error) {
		return q.SetLadoPlanRequestWaiting(ctx, gen.SetLadoPlanRequestWaitingParams{WaitingReason: reason, ID: string(id)})
	})
	return err
}

// BeginLadoPlanWorkerAttempt counts a spawn of the request's designer before
// it is made and returns the attempt's number. It reports false for a request
// whose designer started, or that failed or was cleaned up.
func (s *Store) BeginLadoPlanWorkerAttempt(ctx context.Context, id domain.LadoPlanRequestID) (int, bool, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return 0, false, err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.BeginLadoPlanWorkerAttempt(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("begin LADO plan worker attempt: %w", err)
	}
	return int(n), true, nil
}

// RecordLadoPlanWorkerProblem keeps why a designer did not start.
func (s *Store) RecordLadoPlanWorkerProblem(ctx context.Context, id domain.LadoPlanRequestID, problem string) error {
	_, err := s.ladoPlanWrite(ctx, "record LADO plan worker problem", nil, func(q *gen.Queries) (int64, error) {
		return q.RecordLadoPlanWorkerProblem(ctx, gen.RecordLadoPlanWorkerProblemParams{DeliveryError: problem, ID: string(id)})
	})
	return err
}

// StartLadoPlanWorker records the designer session of a request.
func (s *Store) StartLadoPlanWorker(ctx context.Context, id domain.LadoPlanRequestID, session domain.SessionID, at time.Time, event *domain.LadoAuditEvent) (bool, error) {
	return s.ladoPlanWrite(ctx, "start LADO plan worker", event, func(q *gen.Queries) (int64, error) {
		return q.StartLadoPlanWorker(ctx, gen.StartLadoPlanWorkerParams{SessionID: string(session), WorkerStartedAt: ladoNullTime(at), ID: string(id)})
	})
}

// RecordLadoPlanDraft records the designer's draft, once.
func (s *Store) RecordLadoPlanDraft(ctx context.Context, id domain.LadoPlanRequestID, sum string, at time.Time, event *domain.LadoAuditEvent) (bool, error) {
	return s.ladoPlanWrite(ctx, "record LADO plan draft", event, func(q *gen.Queries) (int64, error) {
		return q.RecordLadoPlanDraft(ctx, gen.RecordLadoPlanDraftParams{DraftSha256: sum, DraftSubmittedAt: ladoNullTime(at), ID: string(id)})
	})
}

// RecordLadoPlanFeedback records that the designer got what came of its
// draft's review, once.
func (s *Store) RecordLadoPlanFeedback(ctx context.Context, id domain.LadoPlanRequestID, at time.Time, event *domain.LadoAuditEvent) (bool, error) {
	return s.ladoPlanWrite(ctx, "record LADO plan feedback", event, func(q *gen.Queries) (int64, error) {
		return q.RecordLadoPlanFeedback(ctx, gen.RecordLadoPlanFeedbackParams{FeedbackSentAt: ladoNullTime(at), ID: string(id)})
	})
}

// RecordLadoPlanFinal records the plan the designer submitted last.
func (s *Store) RecordLadoPlanFinal(ctx context.Context, id domain.LadoPlanRequestID, sum string, at time.Time, event *domain.LadoAuditEvent) (bool, error) {
	return s.ladoPlanWrite(ctx, "record LADO plan", event, func(q *gen.Queries) (int64, error) {
		return q.RecordLadoPlanFinal(ctx, gen.RecordLadoPlanFinalParams{FinalSha256: sum, SubmittedAt: ladoNullTime(at), ID: string(id)})
	})
}

// RecordLadoPlanFixRequested records that LADO sent a revision's problems
// back, once per request.
func (s *Store) RecordLadoPlanFixRequested(ctx context.Context, id domain.LadoPlanRequestID, at time.Time) (bool, error) {
	return s.ladoPlanWrite(ctx, "record LADO plan fix request", nil, func(q *gen.Queries) (int64, error) {
		return q.RecordLadoPlanFixRequested(ctx, gen.RecordLadoPlanFixRequestedParams{FixRequestedAt: ladoNullTime(at), ID: string(id)})
	})
}

// RecordLadoPlanReminder records a reminder to submit.
func (s *Store) RecordLadoPlanReminder(ctx context.Context, id domain.LadoPlanRequestID, at time.Time) error {
	_, err := s.ladoPlanWrite(ctx, "record LADO plan reminder", nil, func(q *gen.Queries) (int64, error) {
		return q.RecordLadoPlanReminder(ctx, gen.RecordLadoPlanReminderParams{RemindedAt: ladoNullTime(at), ID: string(id)})
	})
	return err
}

// CleanLadoPlanRequest records that LADO stopped the request's sessions.
func (s *Store) CleanLadoPlanRequest(ctx context.Context, id domain.LadoPlanRequestID, at time.Time, event *domain.LadoAuditEvent) (bool, error) {
	return s.ladoPlanWrite(ctx, "clean LADO plan request", event, func(q *gen.Queries) (int64, error) {
		return q.CleanLadoPlanRequest(ctx, gen.CleanLadoPlanRequestParams{CleanedAt: ladoNullTime(at), ID: string(id)})
	})
}

// FailLadoPlanWorkerRequest records that a plan worker request failed with
// code, and the details.
func (s *Store) FailLadoPlanWorkerRequest(ctx context.Context, id domain.LadoPlanRequestID, at time.Time, code, detail string, event *domain.LadoAuditEvent) (bool, error) {
	return s.ladoPlanWrite(ctx, "fail LADO plan worker request", event, func(q *gen.Queries) (int64, error) {
		return q.FailLadoPlanWorkerRequest(ctx, gen.FailLadoPlanWorkerRequestParams{FailedAt: ladoNullTime(at), FailureCode: code, DeliveryError: detail, ID: string(id)})
	})
}

// CountRunningLadoPlanWorkers counts the plan worker requests whose designer
// started and that neither failed nor were cleaned up.
func (s *Store) CountRunningLadoPlanWorkers(ctx context.Context) (int, error) {
	n, err := s.qr.CountRunningLadoPlanWorkers(ctx)
	if err != nil {
		return 0, fmt.Errorf("count LADO plan workers: %w", err)
	}
	return int(n), nil
}

// CreateLadoPlanCritique stores a review of a draft: a critic session about
// to start, or a skipped review.
func (s *Store) CreateLadoPlanCritique(ctx context.Context, c domain.LadoPlanCritique, event *domain.LadoAuditEvent) error {
	findings, err := json.Marshal(nonNilLadoFindings(c.Findings))
	if err != nil {
		return fmt.Errorf("encode LADO plan findings: %w", err)
	}
	_, err = s.ladoPlanWrite(ctx, "create LADO plan critique", event, func(q *gen.Queries) (int64, error) {
		err := q.InsertLadoPlanCritique(ctx, gen.InsertLadoPlanCritiqueParams{
			ID: string(c.ID), RequestID: string(c.RequestID), Attempt: int64(c.Attempt), SessionID: string(c.SessionID),
			State: string(c.State), SkipReason: c.SkipReason, FailureReason: c.FailureReason, FindingsJson: string(findings),
			StartedAt: c.StartedAt.UTC(), FinishedAt: ladoNullTime(c.FinishedAt),
		})
		return 1, err
	})
	return err
}

// LadoPlanCritique returns one attempt's review of a request's draft.
func (s *Store) LadoPlanCritique(ctx context.Context, id domain.LadoPlanRequestID, attempt int) (domain.LadoPlanCritique, bool, error) {
	row, err := s.qr.GetLadoPlanCritique(ctx, gen.GetLadoPlanCritiqueParams{RequestID: string(id), Attempt: int64(attempt)})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoPlanCritique{}, false, nil
	}
	if err != nil {
		return domain.LadoPlanCritique{}, false, fmt.Errorf("read LADO plan critique: %w", err)
	}
	return ladoPlanCritiqueFromGen(row), true, nil
}

// ListLadoPlanCritiques returns a request's reviews, by attempt.
func (s *Store) ListLadoPlanCritiques(ctx context.Context, id domain.LadoPlanRequestID) ([]domain.LadoPlanCritique, error) {
	rows, err := s.qr.ListLadoPlanCritiquesByRequest(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("list LADO plan critiques: %w", err)
	}
	return ladoPlanCritiquesFromGen(rows), nil
}

// ListLadoPlanCritiquesByTask returns the reviews of a task's requests, by
// request and attempt.
func (s *Store) ListLadoPlanCritiquesByTask(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoPlanCritique, error) {
	rows, err := s.qr.ListLadoPlanCritiquesByTask(ctx, string(taskID))
	if err != nil {
		return nil, fmt.Errorf("list LADO plan critiques: %w", err)
	}
	return ladoPlanCritiquesFromGen(rows), nil
}

// SetLadoPlanCritiqueSession records the critic session of a running review.
func (s *Store) SetLadoPlanCritiqueSession(ctx context.Context, id domain.LadoPlanRequestID, attempt int, session domain.SessionID, event *domain.LadoAuditEvent) (bool, error) {
	return s.ladoPlanWrite(ctx, "set LADO plan critic", event, func(q *gen.Queries) (int64, error) {
		return q.SetLadoPlanCritiqueSession(ctx, gen.SetLadoPlanCritiqueSessionParams{SessionID: string(session), RequestID: string(id), Attempt: int64(attempt)})
	})
}

// FinishLadoPlanCritique ends a running review as done, with its findings,
// or as failed, with the reason.
func (s *Store) FinishLadoPlanCritique(ctx context.Context, id domain.LadoPlanRequestID, attempt int, state domain.LadoPlanCritiqueState, findings []domain.LadoPlanFinding, reason string, at time.Time, event *domain.LadoAuditEvent) (bool, error) {
	encoded, err := json.Marshal(nonNilLadoFindings(findings))
	if err != nil {
		return false, fmt.Errorf("encode LADO plan findings: %w", err)
	}
	return s.ladoPlanWrite(ctx, "finish LADO plan critique", event, func(q *gen.Queries) (int64, error) {
		return q.FinishLadoPlanCritique(ctx, gen.FinishLadoPlanCritiqueParams{
			State: string(state), FailureReason: reason, FindingsJson: string(encoded), FinishedAt: ladoNullTime(at),
			RequestID: string(id), Attempt: int64(attempt),
		})
	})
}

// SetLadoPlanRevisionMetrics stores or replaces what making a revision took.
func (s *Store) SetLadoPlanRevisionMetrics(ctx context.Context, m domain.LadoPlanRevisionMetrics) error {
	sessions, err := json.Marshal(nonNilLadoUsages(m.Sessions))
	if err != nil {
		return fmt.Errorf("encode LADO plan usage: %w", err)
	}
	kinds, err := json.Marshal(nonNilStrings(m.ChangeKinds))
	if err != nil {
		return fmt.Errorf("encode LADO change kinds: %w", err)
	}
	_, err = s.ladoPlanWrite(ctx, "set LADO plan revision metrics", nil, func(q *gen.Queries) (int64, error) {
		return 1, q.UpsertLadoPlanRevisionMetrics(ctx, gen.UpsertLadoPlanRevisionMetricsParams{
			RevisionID: string(m.RevisionID), RequestID: string(m.RequestID),
			DesignMs: m.Design.Milliseconds(), CritiqueMs: m.Critique.Milliseconds(), ReviseMs: m.Revise.Milliseconds(),
			Review: string(m.Review), P1Count: int64(m.P1), MajorCount: int64(m.Major), MinorCount: int64(m.Minor),
			AddressedCount: int64(m.Addressed), NotAddressedCount: int64(m.NotAddressed),
			SessionsJson: string(sessions), ChangeKindsJson: string(kinds), UpdatedAt: m.UpdatedAt.UTC(),
		})
	})
	return err
}

// LadoPlanRevisionMetrics returns one revision's metrics.
func (s *Store) LadoPlanRevisionMetrics(ctx context.Context, id domain.LadoPlanRevisionID) (domain.LadoPlanRevisionMetrics, bool, error) {
	row, err := s.qr.GetLadoPlanRevisionMetrics(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoPlanRevisionMetrics{}, false, nil
	}
	if err != nil {
		return domain.LadoPlanRevisionMetrics{}, false, fmt.Errorf("read LADO plan revision metrics: %w", err)
	}
	return ladoPlanMetricsFromGen(row), true, nil
}

// ListLadoPlanRevisionMetrics returns the metrics of a task's revisions, by
// revision.
func (s *Store) ListLadoPlanRevisionMetrics(ctx context.Context, taskID domain.LadoTaskID) (map[domain.LadoPlanRevisionID]domain.LadoPlanRevisionMetrics, error) {
	rows, err := s.qr.ListLadoPlanRevisionMetricsByTask(ctx, string(taskID))
	if err != nil {
		return nil, fmt.Errorf("list LADO plan revision metrics: %w", err)
	}
	out := make(map[domain.LadoPlanRevisionID]domain.LadoPlanRevisionMetrics, len(rows))
	for _, row := range rows {
		m := ladoPlanMetricsFromGen(row)
		out[m.RevisionID] = m
	}
	return out, nil
}

// LadoSessionUsage sums what AO recorded of a session's model use: tokens in
// and out, cache reads and, from the provider's own usage, cache writes, with
// AO's cost estimate when it priced any event.
func (s *Store) LadoSessionUsage(ctx context.Context, id domain.SessionID) (domain.LadoPlanSessionUsage, error) {
	row, err := s.qr.SumLadoSessionUsage(ctx, id)
	if err != nil {
		return domain.LadoPlanSessionUsage{}, fmt.Errorf("read the usage of session %s: %w", id, err)
	}
	usage := domain.LadoPlanSessionUsage{
		SessionID: id, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
		CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens,
	}
	if row.PricedEvents > 0 {
		cost := row.CostNanos
		usage.CostNanos = &cost
	}
	return usage, nil
}

func ladoPlanCritiquesFromGen(rows []gen.LadoPlanCritique) []domain.LadoPlanCritique {
	out := make([]domain.LadoPlanCritique, 0, len(rows))
	for _, row := range rows {
		out = append(out, ladoPlanCritiqueFromGen(row))
	}
	return out
}

func ladoPlanCritiqueFromGen(row gen.LadoPlanCritique) domain.LadoPlanCritique {
	var findings []domain.LadoPlanFinding
	// The column only ever holds what the store wrote.
	_ = json.Unmarshal([]byte(row.FindingsJson), &findings)
	return domain.LadoPlanCritique{
		ID:            domain.LadoPlanCritiqueID(row.ID),
		RequestID:     domain.LadoPlanRequestID(row.RequestID),
		Attempt:       int(row.Attempt),
		SessionID:     domain.SessionID(row.SessionID),
		State:         domain.LadoPlanCritiqueState(row.State),
		SkipReason:    row.SkipReason,
		FailureReason: row.FailureReason,
		Findings:      findings,
		StartedAt:     row.StartedAt.UTC(),
		FinishedAt:    ladoTimeFromNull(row.FinishedAt),
	}
}

func ladoPlanMetricsFromGen(row gen.LadoPlanRevisionMetric) domain.LadoPlanRevisionMetrics {
	var sessions []domain.LadoPlanSessionUsage
	var kinds []string
	_ = json.Unmarshal([]byte(row.SessionsJson), &sessions)
	_ = json.Unmarshal([]byte(row.ChangeKindsJson), &kinds)
	return domain.LadoPlanRevisionMetrics{
		RevisionID:   domain.LadoPlanRevisionID(row.RevisionID),
		RequestID:    domain.LadoPlanRequestID(row.RequestID),
		Design:       time.Duration(row.DesignMs) * time.Millisecond,
		Critique:     time.Duration(row.CritiqueMs) * time.Millisecond,
		Revise:       time.Duration(row.ReviseMs) * time.Millisecond,
		Review:       domain.LadoPlanReview(row.Review),
		P1:           int(row.P1Count),
		Major:        int(row.MajorCount),
		Minor:        int(row.MinorCount),
		Addressed:    int(row.AddressedCount),
		NotAddressed: int(row.NotAddressedCount),
		Sessions:     sessions,
		ChangeKinds:  kinds,
		UpdatedAt:    row.UpdatedAt.UTC(),
	}
}

func nonNilLadoFindings(findings []domain.LadoPlanFinding) []domain.LadoPlanFinding {
	if findings == nil {
		return []domain.LadoPlanFinding{}
	}
	return findings
}

func nonNilLadoUsages(usages []domain.LadoPlanSessionUsage) []domain.LadoPlanSessionUsage {
	if usages == nil {
		return []domain.LadoPlanSessionUsage{}
	}
	return usages
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
