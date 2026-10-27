package store

// LADO fork: the queue of approved plans and the executions it starts
// (docs/LJY-PRD/TDD.md §3.1 and §5.3). Approving a plan queues it in the
// approval's transaction (ApproveLadoPlan), and task reads carry the queue
// entry of the newest approval and the newest execution. The dispatcher
// claims, hands back, stops and links entries with the primitives below;
// each write that names a lease token holds only while that token holds the
// entry.

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
// session, whether it ended and its newest PR, and what LADO verified and
// approved about that PR's head. The plan facts must be read first. One query
// each reads the entries, executions, PRs, records and approvals of all the
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
	executions := make([]domain.LadoExecution, 0, len(rows))
	seen := make(map[domain.LadoTaskID]bool, len(rows))
	for _, row := range rows {
		id := domain.LadoTaskID(row.LadoExecution.TaskID)
		if seen[id] {
			continue
		}
		seen[id] = true
		execution := ladoExecutionFromGen(row.LadoExecution)
		execution.SessionTerminated = row.SessionTerminated
		executions = append(executions, execution)
	}
	if err := s.fillLadoExecutionFacts(ctx, executions); err != nil {
		return err
	}
	newest := make(map[domain.LadoTaskID]domain.LadoExecution, len(executions))
	for _, execution := range executions {
		newest[execution.TaskID] = execution
	}
	for i := range tasks {
		task := &tasks[i]
		task.Queue = byApproval[task.Plan.Approval.ID]
		task.Execution = newest[task.ID]
	}
	return nil
}

// fillLadoExecutionFacts sets each execution's newest PR and the
// verification facts of that PR's head. Every read that gives an execution to
// a task's state goes through it, so the state is the same in task lists, one
// task and the execution view.
func (s *Store) fillLadoExecutionFacts(ctx context.Context, executions []domain.LadoExecution) error {
	if err := s.fillLadoExecutionPRs(ctx, executions); err != nil {
		return err
	}
	return s.fillLadoVerification(ctx, executions)
}

// fillLadoExecutionPRs sets each execution's newest PR, reading the PRs of
// all their sessions at once.
func (s *Store) fillLadoExecutionPRs(ctx context.Context, executions []domain.LadoExecution) error {
	if len(executions) == 0 {
		return nil
	}
	sessions := make([]domain.SessionID, 0, len(executions))
	for _, execution := range executions {
		sessions = append(sessions, execution.SessionID)
	}
	rows, err := s.qr.ListLadoExecutionPRs(ctx, sessions)
	if err != nil {
		return fmt.Errorf("read LADO execution PRs: %w", err)
	}
	// Rows come newest first within each session.
	newest := make(map[domain.SessionID]gen.ListLadoExecutionPRsRow, len(rows))
	for _, row := range rows {
		if _, ok := newest[row.SessionID]; !ok {
			newest[row.SessionID] = row
		}
	}
	for i := range executions {
		if row, ok := newest[executions[i].SessionID]; ok {
			executions[i].PR = ladoExecutionPR(row.URL, row.Number, row.PRState, row.HeadSha, row.TargetBranch, row.Title)
		}
	}
	return nil
}

// fillLadoVerification sets what LADO verified and approved about each
// execution's PR at its current head: the head's record, by its outcome and
// reason, and its integration approval, by its status state, which once the
// PR merged is the merged head's. The PRs must be read first. A record or an
// approval of another head says nothing about the current one, and a PR
// whose head AO has not observed has neither.
func (s *Store) fillLadoVerification(ctx context.Context, executions []domain.LadoExecution) error {
	type head struct {
		session domain.SessionID
		sha     string
	}
	var sessions, shas []string
	for _, execution := range executions {
		if execution.PR.URL != "" && execution.PR.HeadSHA != "" {
			sessions = append(sessions, string(execution.SessionID))
			shas = append(shas, execution.PR.HeadSHA)
		}
	}
	if len(sessions) == 0 {
		return nil
	}
	// Each query reads the rows of every session and head asked for; only the
	// pairs asked for count.
	records, err := s.qr.ListLadoVerificationOutcomes(ctx, gen.ListLadoVerificationOutcomesParams{SessionIds: sessions, HeadShas: shas})
	if err != nil {
		return fmt.Errorf("read LADO verification records: %w", err)
	}
	verified := make(map[head]domain.LadoExecutionVerification, len(records))
	for _, row := range records {
		verified[head{domain.SessionID(row.ExecutionSessionID), row.HeadSha}] = domain.LadoExecutionVerification{
			HeadSHA: row.HeadSha,
			Outcome: domain.LadoVerifyOutcome(row.Outcome),
			Reason:  row.Reason,
		}
	}
	approvals, err := s.qr.ListLadoIntegrationApprovalStates(ctx, gen.ListLadoIntegrationApprovalStatesParams{SessionIds: sessions, HeadShas: shas})
	if err != nil {
		return fmt.Errorf("read LADO integration approvals: %w", err)
	}
	approved := make(map[head]domain.LadoIntegrationStatusState, len(approvals))
	for _, row := range approvals {
		approved[head{domain.SessionID(row.ExecutionSessionID), row.HeadSha}] = domain.LadoIntegrationStatusState(row.StatusState)
	}
	for i := range executions {
		execution := &executions[i]
		if execution.PR.URL == "" || execution.PR.HeadSHA == "" {
			continue
		}
		key := head{execution.SessionID, execution.PR.HeadSHA}
		execution.Verification = verified[key]
		if state, ok := approved[key]; ok {
			execution.Integration = domain.LadoExecutionIntegration{
				HeadApproved:       true,
				StatusState:        state,
				MergedHeadApproved: execution.PR.Merged,
			}
		}
	}
	return nil
}

// ladoExecutionPR is the PR of an execution from AO's PR row.
func ladoExecutionPR(url string, number int64, state domain.PRState, headSHA, targetBranch, title string) domain.LadoExecutionPR {
	return domain.LadoExecutionPR{
		URL:          url,
		Number:       int(number),
		Merged:       state == domain.PRStateMerged,
		Closed:       state == domain.PRStateClosed,
		HeadSHA:      headSHA,
		TargetBranch: targetBranch,
		Title:        title,
	}
}

// ClaimLadoQueueEntries leases up to limit entries until leaseUntil, oldest
// first, in one transaction: open entries whose lease is empty or expired at
// now and whose next attempt is due, in projects with LADO mode on other
// than fullProjects, whose run slots are all taken. Each claim takes a token
// from newToken, counts an attempt and clears the waiting reason, and writes
// only while the entry is still claimable. It returns the claimed entries as
// stored.
func (s *Store) ClaimLadoQueueEntries(ctx context.Context, now, leaseUntil time.Time, limit int, fullProjects []domain.ProjectID, newToken func() string) ([]domain.LadoQueueEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	if !leaseUntil.After(now) {
		return nil, errors.New("claim LADO queue entries: the lease must end after now")
	}
	// An empty JSON array leaves no project out.
	full, err := json.Marshal(append([]domain.ProjectID{}, fullProjects...))
	if err != nil {
		return nil, fmt.Errorf("claim LADO queue entries: %w", err)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer s.writeMu.Unlock()
	var claimed []domain.LadoQueueEntry
	err = s.inTx(ctx, "claim LADO queue entries", func(q *gen.Queries) error {
		at := ladoNullTime(now)
		rows, err := q.ListClaimableLadoQueueEntries(ctx, gen.ListClaimableLadoQueueEntriesParams{Now: at, FullProjects: string(full), MaxEntries: int64(limit)})
		if err != nil {
			return err
		}
		for _, row := range rows {
			token := newToken()
			if token == "" {
				return errors.New("a lease token is required")
			}
			n, err := q.ClaimLadoQueueEntry(ctx, gen.ClaimLadoQueueEntryParams{
				LeaseToken:     ladoNullString(token),
				Now:            at,
				LeaseExpiresAt: ladoNullTime(leaseUntil),
				ID:             row.ID,
				FullProjects:   string(full),
			})
			if err != nil {
				return err
			}
			if n == 0 {
				// No longer claimable: another writer got there first.
				continue
			}
			stored, err := q.GetLadoQueueEntry(ctx, row.ID)
			if err != nil {
				return err
			}
			claimed = append(claimed, ladoQueueEntryFromGen(stored))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// ReleaseLadoQueueEntry hands a leased entry back to the queue: it clears the
// lease and keeps the entry waiting with reason waiting until nextAttempt, or
// due at once for a zero nextAttempt. The attempt the claim counted stays
// counted. It reports false, changing nothing, when token no longer holds the
// entry.
func (s *Store) ReleaseLadoQueueEntry(ctx context.Context, id domain.LadoQueueEntryID, token string, nextAttempt time.Time, waiting string) (bool, error) {
	return s.writeLadoRow(ctx, "release LADO queue entry", func(q *gen.Queries) (int64, error) {
		return q.ReleaseLadoQueueEntry(ctx, gen.ReleaseLadoQueueEntryParams{
			NextAttemptAt: ladoNullTime(nextAttempt),
			WaitingReason: waiting,
			ID:            string(id),
			LeaseToken:    ladoNullString(token),
		})
	})
}

// HoldLadoQueueEntry hands a leased entry back as ReleaseLadoQueueEntry does
// and gives back the attempt its claim counted: waiting for a free slot, for
// memory or for the issue is not an attempt.
func (s *Store) HoldLadoQueueEntry(ctx context.Context, id domain.LadoQueueEntryID, token, waiting string, nextAttempt time.Time) (bool, error) {
	return s.writeLadoRow(ctx, "hold LADO queue entry", func(q *gen.Queries) (int64, error) {
		return q.HoldLadoQueueEntry(ctx, gen.HoldLadoQueueEntryParams{
			WaitingReason: waiting,
			NextAttemptAt: ladoNullTime(nextAttempt),
			ID:            string(id),
			LeaseToken:    ladoNullString(token),
		})
	})
}

// SetLadoQueueWaiting records why the open entries no lease holds wait, of
// projectID or of every project for a nil one, and returns how many it
// changed. The entries of a project whose LADO mode is off wait for nothing
// else: domain.LadoWaitingModeOff marks exactly those, and any other reason
// the entries of projects with LADO mode on. Attempts and next attempts stay,
// and an entry that already waits for reason is not written again, so a
// dispatch pass that finds the same reason publishes no change. A claim
// clears the reason.
func (s *Store) SetLadoQueueWaiting(ctx context.Context, reason string, projectID *domain.ProjectID) (int64, error) {
	if reason == "" {
		return 0, errors.New("set LADO queue waiting: a reason is required")
	}
	var project sql.NullString
	if projectID != nil {
		project = sql.NullString{String: string(*projectID), Valid: true}
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return 0, err
	}
	defer s.writeMu.Unlock()
	var n int64
	var err error
	if reason == domain.LadoWaitingModeOff {
		n, err = s.qw.SetLadoQueueWaitingModeOff(ctx, gen.SetLadoQueueWaitingModeOffParams{Reason: reason, ProjectID: project})
	} else {
		n, err = s.qw.SetLadoQueueWaiting(ctx, gen.SetLadoQueueWaitingParams{Reason: reason, ProjectID: project})
	}
	if err != nil {
		return 0, fmt.Errorf("set LADO queue waiting: %w", err)
	}
	return n, nil
}

// MarkLadoQueueEntryReviewRequired stops a leased entry for reason
// (domain.LadoReview*) until the task is approved again, and clears the
// lease. It reports false, changing nothing, when token no longer holds the
// entry.
func (s *Store) MarkLadoQueueEntryReviewRequired(ctx context.Context, id domain.LadoQueueEntryID, token, reason string) (bool, error) {
	if reason == "" {
		return false, errors.New("mark LADO queue entry review required: a reason is required")
	}
	return s.writeLadoRow(ctx, "mark LADO queue entry review required", func(q *gen.Queries) (int64, error) {
		return q.MarkLadoQueueEntryReviewRequired(ctx, gen.MarkLadoQueueEntryReviewRequiredParams{
			Reason:     reason,
			ID:         string(id),
			LeaseToken: ladoNullString(token),
		})
	})
}

// MarkLadoQueueEntryBlocked stops a leased entry for reason
// (domain.LadoBlocked*) until the plan or the setup changes, and clears the
// lease. It reports false, changing nothing, when token no longer holds the
// entry.
func (s *Store) MarkLadoQueueEntryBlocked(ctx context.Context, id domain.LadoQueueEntryID, token, reason string) (bool, error) {
	if reason == "" {
		return false, errors.New("mark LADO queue entry blocked: a reason is required")
	}
	return s.writeLadoRow(ctx, "mark LADO queue entry blocked", func(q *gen.Queries) (int64, error) {
		return q.MarkLadoQueueEntryBlocked(ctx, gen.MarkLadoQueueEntryBlockedParams{
			Reason:     reason,
			ID:         string(id),
			LeaseToken: ladoNullString(token),
		})
	})
}

// FailLadoQueueEntry records that a leased entry ran out of attempts at at,
// with the last attempt's error, and clears the lease. It reports false,
// changing nothing, when token no longer holds the entry.
func (s *Store) FailLadoQueueEntry(ctx context.Context, id domain.LadoQueueEntryID, token, message string, at time.Time) (bool, error) {
	if at.IsZero() {
		return false, errors.New("fail LADO queue entry: a time is required")
	}
	return s.writeLadoRow(ctx, "fail LADO queue entry", func(q *gen.Queries) (int64, error) {
		return q.FailLadoQueueEntry(ctx, gen.FailLadoQueueEntryParams{
			FailedAt:     ladoNullTime(at),
			ErrorMessage: message,
			ID:           string(id),
			LeaseToken:   ladoNullString(token),
		})
	})
}

// CancelLadoQueueEntry cancels a leased entry at at, because its task's issue
// closed, and clears the lease. It reports false, changing nothing, when
// token no longer holds the entry.
func (s *Store) CancelLadoQueueEntry(ctx context.Context, id domain.LadoQueueEntryID, token string, at time.Time) (bool, error) {
	if at.IsZero() {
		return false, errors.New("cancel LADO queue entry: a time is required")
	}
	return s.writeLadoRow(ctx, "cancel LADO queue entry", func(q *gen.Queries) (int64, error) {
		return q.CancelLadoQueueEntry(ctx, gen.CancelLadoQueueEntryParams{
			CancelledAt: ladoNullTime(at),
			ID:          string(id),
			LeaseToken:  ladoNullString(token),
		})
	})
}

// writeLadoRow runs one write statement under the write lock and reports
// whether it changed a row.
func (s *Store) writeLadoRow(ctx context.Context, what string, write func(*gen.Queries) (int64, error)) (bool, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	n, err := write(s.qw)
	if err != nil {
		return false, fmt.Errorf("%s: %w", what, err)
	}
	return n > 0, nil
}

// LinkLadoExecution records that session exec.SessionID runs a leased entry:
// one transaction stores the execution and marks the entry dispatched to the
// session, which clears the lease. The execution takes the entry's task,
// approval, rule snapshot and attempt, and the revision the approval names
// with its base SHA; the caller gives the session, the base SHA at dispatch,
// the commits ahead, the base branch and remote it resolved with the local
// branch's commit then, and the times. Only the token is checked, not when the
// lease ends, so recovery can link the session of an expired lease nobody
// claimed since. When token no longer holds the entry, nothing is written and
// the error is domain.ErrLadoLeaseLost. Linking the entry's session again
// changes nothing and returns nil, so a link that may have happened can be
// repeated. An entry superseded or cancelled while leased is linked too: its
// session exists, and stopping it is the caller's choice.
func (s *Store) LinkLadoExecution(ctx context.Context, entryID domain.LadoQueueEntryID, token string, exec domain.LadoExecution) error {
	if exec.SessionID == "" || exec.StartedAt.IsZero() {
		return errors.New("link LADO execution: a session and a start time are required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "link LADO execution", func(q *gen.Queries) error {
		linked, err := q.GetLadoExecution(ctx, string(exec.SessionID))
		switch {
		case err == nil && linked.LadoExecution.QueueEntryID == string(entryID):
			return nil
		case err == nil:
			return fmt.Errorf("session %s already runs queue entry %s", exec.SessionID, linked.LadoExecution.QueueEntryID)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		row, err := q.GetLadoQueueEntryPlan(ctx, string(entryID))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrLadoLeaseLost
		}
		if err != nil {
			return err
		}
		entry := row.LadoQueueEntry
		if token == "" || entry.LeaseToken.String != token {
			return domain.ErrLadoLeaseLost
		}
		var ahead sql.NullInt64
		if exec.BaseCommitsAhead != nil {
			ahead = sql.NullInt64{Int64: int64(*exec.BaseCommitsAhead), Valid: true}
		}
		if err := q.InsertLadoExecution(ctx, gen.InsertLadoExecutionParams{
			SessionID:            string(exec.SessionID),
			TaskID:               entry.TaskID,
			QueueEntryID:         entry.ID,
			PlanRevisionID:       row.PlanRevisionID,
			PlanApprovalID:       entry.PlanApprovalID,
			PolicySnapshotID:     entry.PolicySnapshotID,
			Attempt:              entry.AttemptCount,
			PlanBaseSha:          row.PlanBaseSha,
			DispatchBaseSha:      exec.DispatchBaseSHA,
			BaseCommitsAhead:     ahead,
			StartedAt:            exec.StartedAt.UTC(),
			DeadlineAt:           ladoNullTime(exec.DeadlineAt),
			BaseBranch:           exec.BaseBranch,
			BaseRemote:           exec.BaseRemote,
			DispatchLocalBaseSha: exec.DispatchLocalBaseSHA,
		}); err != nil {
			return err
		}
		n, err := q.SetLadoQueueEntryDispatched(ctx, gen.SetLadoQueueEntryDispatchedParams{
			DispatchedSessionID: ladoNullString(string(exec.SessionID)),
			ID:                  entry.ID,
			LeaseToken:          entry.LeaseToken,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return domain.ErrLadoLeaseLost
		}
		return nil
	})
	return unwrapLadoSentinel(err)
}

// LeasedLadoQueueEntries returns every entry a lease token holds, expired or
// not, by claim time: what a dispatcher was working on, for recovery.
func (s *Store) LeasedLadoQueueEntries(ctx context.Context) ([]domain.LadoQueueEntry, error) {
	rows, err := s.qr.ListLeasedLadoQueueEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list leased LADO queue entries: %w", err)
	}
	entries := make([]domain.LadoQueueEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, ladoQueueEntryFromGen(row))
	}
	return entries, nil
}

// CountLiveLadoExecutions counts what takes a run slot at now, in all and by
// project: executions whose session still runs, and entries an unexpired
// lease holds, which may be starting one. A session AO no longer has does not
// run.
func (s *Store) CountLiveLadoExecutions(ctx context.Context, now time.Time) (int, map[domain.ProjectID]int, error) {
	// Leases first: a link between the two reads moves an entry from its
	// lease to an execution, which then counts twice rather than not at all.
	leases, err := s.qr.CountActiveLadoLeasesByProject(ctx, ladoNullTime(now))
	if err != nil {
		return 0, nil, fmt.Errorf("count LADO queue leases: %w", err)
	}
	live, err := s.qr.CountLiveLadoExecutionsByProject(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("count live LADO executions: %w", err)
	}
	total := 0
	perProject := make(map[domain.ProjectID]int, len(leases)+len(live))
	for _, row := range leases {
		perProject[domain.ProjectID(row.ProjectID)] += int(row.Leased)
		total += int(row.Leased)
	}
	for _, row := range live {
		perProject[domain.ProjectID(row.ProjectID)] += int(row.Live)
		total += int(row.Live)
	}
	return total, perProject, nil
}

// LadoExecutionsPastDeadline returns the executions whose deadline is at or
// before now, whose session still runs and that were not marked timed out,
// by deadline. An execution without a deadline never times out.
func (s *Store) LadoExecutionsPastDeadline(ctx context.Context, now time.Time) ([]domain.LadoExecution, error) {
	rows, err := s.qr.ListLadoExecutionsPastDeadline(ctx, ladoNullTime(now))
	if err != nil {
		return nil, fmt.Errorf("list LADO executions past their deadline: %w", err)
	}
	// The query keeps only sessions that still run.
	executions := make([]domain.LadoExecution, 0, len(rows))
	for _, row := range rows {
		executions = append(executions, ladoExecutionFromGen(row))
	}
	if err := s.fillLadoExecutionFacts(ctx, executions); err != nil {
		return nil, err
	}
	return executions, nil
}

// LiveLadoExecutions returns the executions whose session still runs, oldest
// first, without their PR facts.
func (s *Store) LiveLadoExecutions(ctx context.Context) ([]domain.LadoExecution, error) {
	rows, err := s.qr.ListLiveLadoExecutions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list live LADO executions: %w", err)
	}
	executions := make([]domain.LadoExecution, 0, len(rows))
	for _, row := range rows {
		executions = append(executions, ladoExecutionFromGen(row))
	}
	return executions, nil
}

// MarkLadoExecutionTimedOut records that LADO stopped an execution at its
// deadline, at the event's time, with its audit event in one transaction. It
// reports false, writing neither, for a session that is not an execution or
// was marked before, whose first time stays.
func (s *Store) MarkLadoExecutionTimedOut(ctx context.Context, sessionID domain.SessionID, event domain.LadoAuditEvent) (bool, error) {
	if event.At.IsZero() {
		return false, errors.New("mark LADO execution timed out: a time is required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	marked := false
	err := s.inTx(ctx, "mark LADO execution timed out", func(q *gen.Queries) error {
		n, err := q.MarkLadoExecutionTimedOut(ctx, gen.MarkLadoExecutionTimedOutParams{
			TimedOutAt: ladoNullTime(event.At),
			SessionID:  string(sessionID),
		})
		if err != nil || n == 0 {
			return err
		}
		marked = true
		_, err = appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	if err != nil {
		return false, err
	}
	return marked, nil
}

// LadoExecution returns the execution a session runs, with whether the
// session ended, its newest PR and the verification facts of that PR's head.
// It reports false for a session that is not an execution.
func (s *Store) LadoExecution(ctx context.Context, sessionID domain.SessionID) (domain.LadoExecution, bool, error) {
	row, err := s.qr.GetLadoExecution(ctx, string(sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoExecution{}, false, nil
	}
	if err != nil {
		return domain.LadoExecution{}, false, fmt.Errorf("read LADO execution: %w", err)
	}
	return s.ladoExecutionWithFacts(ctx, row.LadoExecution, row.SessionTerminated)
}

// ladoExecutionWithFacts converts one execution, with whether its session
// ended, and reads its newest PR and the verification facts of its head.
func (s *Store) ladoExecutionWithFacts(ctx context.Context, row gen.LadoExecution, terminated bool) (domain.LadoExecution, bool, error) {
	executions := []domain.LadoExecution{ladoExecutionFromGen(row)}
	executions[0].SessionTerminated = terminated
	if err := s.fillLadoExecutionFacts(ctx, executions); err != nil {
		return domain.LadoExecution{}, false, err
	}
	return executions[0], true, nil
}

// LadoDispatchGrant reads what the worker gate checks before a worker spawns
// for an attempt of an entry: the entry's project, the dispatch hash of the
// revision its approval names and of its rule snapshot, and whether the entry
// is Dispatching at now for that attempt. An unknown entry has an empty
// grant.
func (s *Store) LadoDispatchGrant(ctx context.Context, entryID domain.LadoQueueEntryID, attempt int, now time.Time) (domain.LadoDispatchGrant, error) {
	row, err := s.qr.GetLadoQueueEntryPlan(ctx, string(entryID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoDispatchGrant{}, nil
	}
	if err != nil {
		return domain.LadoDispatchGrant{}, fmt.Errorf("read LADO dispatch grant: %w", err)
	}
	entry := ladoQueueEntryFromGen(row.LadoQueueEntry)
	return domain.LadoDispatchGrant{
		ProjectID: entry.ProjectID,
		Hash:      domain.LadoDispatchHash(row.PlanSha256, row.SnapshotSha256),
		// Dispatching: a lease that has not expired holds the entry, which was
		// neither dispatched, stopped, superseded nor cancelled since.
		Active: entry.State(now) == domain.LadoQueueDispatching && entry.AttemptCount == attempt,
	}, nil
}

// LadoEntryPolicyByClientRequest returns the permission ceiling and the rules
// text of the rule snapshot of the entry a client request id
// "lado:<entry>/<attempt>" names. The entry is found before its execution is
// linked, while the worker still starts. Any other id, and an unknown entry,
// report false.
func (s *Store) LadoEntryPolicyByClientRequest(ctx context.Context, clientRequestID string) (domain.LadoEntryPolicy, bool, error) {
	entryID, _, ok := domain.ParseLadoClientRequestID(clientRequestID)
	if !ok {
		return domain.LadoEntryPolicy{}, false, nil
	}
	raw, err := s.qr.GetLadoQueueEntrySnapshot(ctx, string(entryID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoEntryPolicy{}, false, nil
	}
	if err != nil {
		return domain.LadoEntryPolicy{}, false, fmt.Errorf("read LADO entry policy: %w", err)
	}
	// The two fields of the snapshot content (ladopolicy.SnapshotContent)
	// that an execution keeps.
	var content struct {
		PermissionCeiling domain.PermissionMode `json:"permissionCeiling"`
		Prompt            string                `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		return domain.LadoEntryPolicy{}, false, fmt.Errorf("decode LADO entry policy: %w", err)
	}
	return domain.LadoEntryPolicy{Ceiling: content.PermissionCeiling, Prompt: content.Prompt}, true, nil
}

// LadoDispatchSettings returns the dispatch settings, or
// domain.DefaultLadoDispatchSettings while none are stored.
func (s *Store) LadoDispatchSettings(ctx context.Context) (domain.LadoDispatchSettings, error) {
	row, err := s.qr.GetLadoDispatchSettings(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DefaultLadoDispatchSettings(), nil
	}
	if err != nil {
		return domain.LadoDispatchSettings{}, fmt.Errorf("read LADO dispatch settings: %w", err)
	}
	return ladoDispatchSettingsFromGen(row), nil
}

// SetLadoDispatchSettings stores the dispatch settings and their audit event
// in one transaction and returns them as stored; the change takes the event's
// time. The caller validates the settings (LadoDispatchSettings.Validate);
// the table refuses a cap outside 1 to 8.
func (s *Store) SetLadoDispatchSettings(ctx context.Context, settings domain.LadoDispatchSettings, event domain.LadoAuditEvent) (domain.LadoDispatchSettings, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoDispatchSettings{}, err
	}
	defer s.writeMu.Unlock()
	var stored domain.LadoDispatchSettings
	err := s.inTx(ctx, "set LADO dispatch settings", func(q *gen.Queries) error {
		if err := q.UpsertLadoDispatchSettings(ctx, gen.UpsertLadoDispatchSettingsParams{
			MaxConcurrent:     int64(settings.MaxConcurrent),
			MaxRunMinutes:     int64(settings.MaxRunMinutes),
			HoldOnMemoryTight: boolInt(settings.HoldOnMemoryTight),
			UpdatedAt:         event.At.UTC(),
		}); err != nil {
			return err
		}
		row, err := q.GetLadoDispatchSettings(ctx)
		if err != nil {
			return err
		}
		stored = ladoDispatchSettingsFromGen(row)
		_, err = appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	if err != nil {
		return domain.LadoDispatchSettings{}, err
	}
	return stored, nil
}

// LadoProjectMaxConcurrent returns how many executions may run at once in a
// project: domain.LadoDefaultProjectMaxConcurrent until it sets a cap.
func (s *Store) LadoProjectMaxConcurrent(ctx context.Context, projectID domain.ProjectID) (int, error) {
	n, err := s.qr.GetLadoProjectMaxConcurrent(ctx, string(projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoDefaultProjectMaxConcurrent, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read LADO project cap: %w", err)
	}
	return int(n), nil
}

// SetLadoProjectMaxConcurrent stores how many executions may run at once in a
// project, with its audit event, in one transaction; the change takes the
// event's time. A project without LADO settings gets them with LADO mode off.
// The caller validates n; the table refuses one outside 1 to 8.
func (s *Store) SetLadoProjectMaxConcurrent(ctx context.Context, projectID domain.ProjectID, n int, event domain.LadoAuditEvent) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "set LADO project cap", func(q *gen.Queries) error {
		if err := q.UpsertLadoProjectMaxConcurrent(ctx, gen.UpsertLadoProjectMaxConcurrentParams{
			ProjectID:     string(projectID),
			MaxConcurrent: int64(n),
			UpdatedAt:     event.At.UTC(),
		}); err != nil {
			return err
		}
		_, err := appendLadoAuditEventTx(ctx, q, event)
		return err
	})
}

// ListLadoQueue returns the queue view: the open entries of a project, or of
// every project for a nil projectID, with their task's issue, in the order
// the dispatcher takes them. An entry that left the queue (dispatched,
// failed, stopped for review or blocked, superseded or cancelled) shows in
// its task's state instead.
func (s *Store) ListLadoQueue(ctx context.Context, projectID *domain.ProjectID) ([]domain.LadoQueueItem, error) {
	var project sql.NullString
	if projectID != nil {
		project = sql.NullString{String: string(*projectID), Valid: true}
	}
	rows, err := s.qr.ListOpenLadoQueueEntriesWithIssues(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list LADO queue: %w", err)
	}
	items := make([]domain.LadoQueueItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, domain.LadoQueueItem{
			Entry:    ladoQueueEntryFromGen(row.LadoQueueEntry),
			IssueRef: domain.IssueID(row.IssueRef.String),
			Title:    row.IssueTitle,
		})
	}
	return items, nil
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
		SessionID:            domain.SessionID(row.SessionID),
		TaskID:               domain.LadoTaskID(row.TaskID),
		QueueEntryID:         domain.LadoQueueEntryID(row.QueueEntryID),
		PlanRevisionID:       domain.LadoPlanRevisionID(row.PlanRevisionID),
		PlanApprovalID:       domain.LadoPlanApprovalID(row.PlanApprovalID),
		PolicySnapshotID:     domain.LadoPolicySnapshotID(row.PolicySnapshotID),
		Attempt:              int(row.Attempt),
		PlanBaseSHA:          row.PlanBaseSha,
		DispatchBaseSHA:      row.DispatchBaseSha,
		BaseCommitsAhead:     ahead,
		BaseBranch:           row.BaseBranch,
		BaseRemote:           row.BaseRemote,
		DispatchLocalBaseSHA: row.DispatchLocalBaseSha,
		StartedAt:            row.StartedAt.UTC(),
		DeadlineAt:           ladoTimeFromNull(row.DeadlineAt),
		TimedOutAt:           ladoTimeFromNull(row.TimedOutAt),
		SettledAt:            ladoTimeFromNull(row.SettledAt),
	}
}

func ladoDispatchSettingsFromGen(row gen.LadoDispatchSetting) domain.LadoDispatchSettings {
	return domain.LadoDispatchSettings{
		MaxConcurrent:     int(row.MaxConcurrent),
		MaxRunMinutes:     int(row.MaxRunMinutes),
		HoldOnMemoryTight: row.HoldOnMemoryTight == 1,
		UpdatedAt:         row.UpdatedAt.UTC(),
	}
}
