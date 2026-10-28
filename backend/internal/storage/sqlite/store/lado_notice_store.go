package store

// LADO fork: the outbox of the one-line notices LADO tells a project's
// orchestrator (docs/LJY-PRD/TDD.md §5.1), and the stored facts they come
// from.

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// LadoNoticeSince returns when notices started: events before it are never
// told.
func (s *Store) LadoNoticeSince(ctx context.Context) (time.Time, error) {
	since, err := s.qr.GetLadoNoticeSince(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("read LADO notice start: %w", err)
	}
	return since.UTC(), nil
}

// AddLadoOrchestratorNotice queues a notice once per key. It reports whether
// the notice is new.
func (s *Store) AddLadoOrchestratorNotice(ctx context.Context, n domain.LadoOrchestratorNotice) (bool, error) {
	return s.ladoPlanWrite(ctx, "add LADO notice", nil, func(q *gen.Queries) (int64, error) {
		return q.InsertLadoOrchestratorNotice(ctx, gen.InsertLadoOrchestratorNoticeParams{
			ProjectID: string(n.ProjectID), NoticeKey: n.Key, Text: n.Text,
			OccurredAt: n.OccurredAt.UTC(), CreatedAt: n.CreatedAt.UTC(),
		})
	})
}

// ListWaitingLadoOrchestratorNotices returns the notices neither sent nor
// dropped, by project and in the order they happened.
func (s *Store) ListWaitingLadoOrchestratorNotices(ctx context.Context) ([]domain.LadoOrchestratorNotice, error) {
	rows, err := s.qr.ListWaitingLadoOrchestratorNotices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO notices: %w", err)
	}
	return ladoNoticesFromGen(rows), nil
}

// ListLadoOrchestratorNotices returns a project's notices, in the order they
// happened.
func (s *Store) ListLadoOrchestratorNotices(ctx context.Context, projectID domain.ProjectID) ([]domain.LadoOrchestratorNotice, error) {
	rows, err := s.qr.ListLadoOrchestratorNoticesByProject(ctx, string(projectID))
	if err != nil {
		return nil, fmt.Errorf("list LADO notices: %w", err)
	}
	return ladoNoticesFromGen(rows), nil
}

// MarkLadoOrchestratorNoticesSent records that the notices went to the
// orchestrator in one message.
func (s *Store) MarkLadoOrchestratorNoticesSent(ctx context.Context, ids []int64, to domain.SessionID, at time.Time) error {
	_, err := s.ladoPlanWrite(ctx, "mark LADO notices sent", nil, func(q *gen.Queries) (int64, error) {
		for _, id := range ids {
			if _, err := q.MarkLadoOrchestratorNoticeSent(ctx, gen.MarkLadoOrchestratorNoticeSentParams{SentAt: ladoNullTime(at), SentTo: string(to), ID: id}); err != nil {
				return 0, err
			}
		}
		return 1, nil
	})
	return err
}

// DropLadoOrchestratorNotices gives up notices nobody took in time.
func (s *Store) DropLadoOrchestratorNotices(ctx context.Context, ids []int64, at time.Time) error {
	_, err := s.ladoPlanWrite(ctx, "drop LADO notices", nil, func(q *gen.Queries) (int64, error) {
		for _, id := range ids {
			if _, err := q.DropLadoOrchestratorNotice(ctx, gen.DropLadoOrchestratorNoticeParams{DroppedAt: ladoNullTime(at), ID: id}); err != nil {
				return 0, err
			}
		}
		return 1, nil
	})
	return err
}

// LadoPlanRequestNoticeFact is a plan request a notice may tell of.
type LadoPlanRequestNoticeFact struct {
	ID          domain.LadoPlanRequestID
	ProjectID   domain.ProjectID
	IssueRef    domain.IssueID
	Kind        domain.LadoPlanRequestKind
	RequestedAt time.Time
	FailedAt    time.Time
	FailureCode string
}

// ListLadoPlanRequestNoticeFacts returns the plan requests made or failed
// since a time.
func (s *Store) ListLadoPlanRequestNoticeFacts(ctx context.Context, since time.Time) ([]LadoPlanRequestNoticeFact, error) {
	rows, err := s.qr.ListLadoPlanRequestNoticeFacts(ctx, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("list LADO plan request facts: %w", err)
	}
	out := make([]LadoPlanRequestNoticeFact, 0, len(rows))
	for _, row := range rows {
		out = append(out, LadoPlanRequestNoticeFact{
			ID: domain.LadoPlanRequestID(row.ID), ProjectID: domain.ProjectID(row.ProjectID), IssueRef: domain.IssueID(row.IssueRef.String),
			Kind: domain.LadoPlanRequestKind(row.Kind), RequestedAt: row.RequestedAt.UTC(), FailedAt: ladoTimeFromNull(row.FailedAt), FailureCode: row.FailureCode,
		})
	}
	return out, nil
}

// LadoPlanRevisionNoticeFact is a pinned revision a notice may tell of.
type LadoPlanRevisionNoticeFact struct {
	TaskID    domain.LadoTaskID
	ProjectID domain.ProjectID
	IssueRef  domain.IssueID
	Number    int
	CreatedAt time.Time
}

// ListLadoPlanRevisionNoticeFacts returns the revisions pinned since a time.
func (s *Store) ListLadoPlanRevisionNoticeFacts(ctx context.Context, since time.Time) ([]LadoPlanRevisionNoticeFact, error) {
	rows, err := s.qr.ListLadoPlanRevisionNoticeFacts(ctx, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("list LADO plan revision facts: %w", err)
	}
	out := make([]LadoPlanRevisionNoticeFact, 0, len(rows))
	for _, row := range rows {
		out = append(out, LadoPlanRevisionNoticeFact{
			TaskID: domain.LadoTaskID(row.TaskID), ProjectID: domain.ProjectID(row.ProjectID), IssueRef: domain.IssueID(row.IssueRef.String),
			Number: int(row.Number), CreatedAt: row.CreatedAt.UTC(),
		})
	}
	return out, nil
}

// ListLadoProjectsWithRunsSince returns the projects with an execution that
// started, or a queue entry that failed, since a time.
func (s *Store) ListLadoProjectsWithRunsSince(ctx context.Context, since time.Time) ([]domain.ProjectID, error) {
	rows, err := s.qr.ListLadoProjectsWithRunsSince(ctx, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("list LADO projects with runs: %w", err)
	}
	out := make([]domain.ProjectID, 0, len(rows))
	for _, id := range rows {
		out = append(out, domain.ProjectID(id))
	}
	return out, nil
}

func ladoNoticesFromGen(rows []gen.LadoOrchestratorNotice) []domain.LadoOrchestratorNotice {
	out := make([]domain.LadoOrchestratorNotice, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.LadoOrchestratorNotice{
			ID: row.ID, ProjectID: domain.ProjectID(row.ProjectID), Key: row.NoticeKey, Text: row.Text,
			OccurredAt: row.OccurredAt.UTC(), CreatedAt: row.CreatedAt.UTC(), SentAt: ladoTimeFromNull(row.SentAt),
			SentTo: domain.SessionID(row.SentTo), DroppedAt: ladoTimeFromNull(row.DroppedAt),
		})
	}
	return out
}
