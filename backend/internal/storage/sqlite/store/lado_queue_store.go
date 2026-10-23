package store

// LADO fork: the queue of approved plans and the executions it starts
// (docs/LJY-PRD/TDD.md §3.1 and §5.3). Approving a plan queues it in the
// approval's transaction (ApproveLadoPlan), and task reads carry the queue
// entry of the newest approval and the newest execution.

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// queueLadoApprovalTx supersedes the task's open queue entries, which belong
// to older approvals, and then queues the stored approval when it has a rule
// snapshot and an entry. Of the entry only the id is the caller's: it takes
// the approval's task, id and time, the task's project and the snapshot's id.
func queueLadoApprovalTx(ctx context.Context, q *gen.Queries, approval domain.LadoPlanApproval, snapshot *domain.LadoPolicySnapshot, entry *domain.LadoQueueEntry) error {
	if _, err := q.SupersedeOpenLadoQueueEntries(ctx, gen.SupersedeOpenLadoQueueEntriesParams{
		SupersededAt: sql.NullTime{Time: approval.ApprovedAt.UTC(), Valid: true},
		TaskID:       string(approval.TaskID),
	}); err != nil {
		return err
	}
	if snapshot == nil || entry == nil {
		return nil
	}
	task, err := q.GetLadoTask(ctx, string(approval.TaskID))
	if err != nil {
		return err
	}
	return q.InsertLadoQueueEntry(ctx, gen.InsertLadoQueueEntryParams{
		ID:               string(entry.ID),
		TaskID:           task.ID,
		ProjectID:        task.ProjectID,
		PlanApprovalID:   string(approval.ID),
		PolicySnapshotID: string(snapshot.ID),
		EnqueuedAt:       approval.ApprovedAt.UTC(),
	})
}

// fillLadoQueueFacts sets each task's queue entry, the one of its newest
// approval, and its newest execution with what AO keeps about the execution's
// session: whether it ended and its newest PR. The plan facts must be read
// first. One query each reads the entries, executions and PRs of all the
// tasks.
func (s *Store) fillLadoQueueFacts(ctx context.Context, tasks []domain.LadoTask) error {
	// Only an approval has an entry, and only an entry starts an execution.
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task.Plan.Approval.ID != "" {
			ids = append(ids, string(task.ID))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	entries, err := s.qr.ListLadoQueueEntriesForTasks(ctx, ids)
	if err != nil {
		return fmt.Errorf("read LADO queue entries: %w", err)
	}
	// An approval has at most one entry.
	byApproval := make(map[domain.LadoPlanApprovalID]domain.LadoQueueEntry, len(entries))
	for _, row := range entries {
		byApproval[domain.LadoPlanApprovalID(row.PlanApprovalID)] = ladoQueueEntryFromGen(row)
	}
	rows, err := s.qr.ListLadoExecutionsForTasks(ctx, ids)
	if err != nil {
		return fmt.Errorf("read LADO executions: %w", err)
	}
	// Rows come newest first within each task.
	newest := make(map[domain.LadoTaskID]domain.LadoExecution, len(rows))
	sessions := make([]domain.SessionID, 0, len(rows))
	for _, row := range rows {
		id := domain.LadoTaskID(row.LadoExecution.TaskID)
		if _, ok := newest[id]; ok {
			continue
		}
		execution := ladoExecutionFromGen(row.LadoExecution)
		execution.SessionTerminated = row.SessionTerminated
		newest[id] = execution
		sessions = append(sessions, execution.SessionID)
	}
	prs, err := s.ListPRFactsForSessions(ctx, sessions)
	if err != nil {
		return fmt.Errorf("read LADO execution PRs: %w", err)
	}
	for i := range tasks {
		task := &tasks[i]
		task.Queue = byApproval[task.Plan.Approval.ID]
		execution, ok := newest[task.ID]
		if !ok {
			continue
		}
		// Newest first, as the session list reads them.
		if facts := prs[execution.SessionID]; len(facts) > 0 {
			pr := facts[0]
			execution.PR = domain.LadoExecutionPR{URL: pr.URL, Number: pr.Number, Merged: pr.Merged, Closed: pr.Closed}
		}
		task.Execution = execution
	}
	return nil
}

func ladoQueueEntryFromGen(row gen.LadoQueueEntry) domain.LadoQueueEntry {
	return domain.LadoQueueEntry{
		ID:                   domain.LadoQueueEntryID(row.ID),
		TaskID:               domain.LadoTaskID(row.TaskID),
		ProjectID:            domain.ProjectID(row.ProjectID),
		PlanApprovalID:       domain.LadoPlanApprovalID(row.PlanApprovalID),
		PolicySnapshotID:     domain.LadoPolicySnapshotID(row.PolicySnapshotID),
		EnqueuedAt:           row.EnqueuedAt.UTC(),
		AttemptCount:         int(row.AttemptCount),
		NextAttemptAt:        ladoTimeFromNull(row.NextAttemptAt),
		LeaseToken:           row.LeaseToken.String,
		ClaimedAt:            ladoTimeFromNull(row.ClaimedAt),
		LeaseExpiresAt:       ladoTimeFromNull(row.LeaseExpiresAt),
		WaitingReason:        row.WaitingReason,
		ReviewRequiredReason: row.ReviewRequiredReason,
		BlockedReason:        row.BlockedReason,
		ErrorMessage:         row.ErrorMessage,
		DispatchedSessionID:  domain.SessionID(row.DispatchedSessionID.String),
		FailedAt:             ladoTimeFromNull(row.FailedAt),
		SupersededAt:         ladoTimeFromNull(row.SupersededAt),
		CancelledAt:          ladoTimeFromNull(row.CancelledAt),
	}
}

// ladoExecutionFromGen converts the execution's own row; the session's facts
// come from AO's rows.
func ladoExecutionFromGen(row gen.LadoExecution) domain.LadoExecution {
	var ahead *int
	if row.BaseCommitsAhead.Valid {
		n := int(row.BaseCommitsAhead.Int64)
		ahead = &n
	}
	return domain.LadoExecution{
		SessionID:        domain.SessionID(row.SessionID),
		TaskID:           domain.LadoTaskID(row.TaskID),
		QueueEntryID:     domain.LadoQueueEntryID(row.QueueEntryID),
		PlanRevisionID:   domain.LadoPlanRevisionID(row.PlanRevisionID),
		PlanApprovalID:   domain.LadoPlanApprovalID(row.PlanApprovalID),
		PolicySnapshotID: domain.LadoPolicySnapshotID(row.PolicySnapshotID),
		Attempt:          int(row.Attempt),
		PlanBaseSHA:      row.PlanBaseSha,
		DispatchBaseSHA:  row.DispatchBaseSha,
		BaseCommitsAhead: ahead,
		StartedAt:        row.StartedAt.UTC(),
		DeadlineAt:       ladoTimeFromNull(row.DeadlineAt),
		TimedOutAt:       ladoTimeFromNull(row.TimedOutAt),
	}
}
