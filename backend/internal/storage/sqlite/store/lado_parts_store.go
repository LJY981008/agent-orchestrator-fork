package store

// LADO fork: parallel parts (docs/LJY-PRD/TDD.md §5.4). A claimed queue entry
// whose plan has parts starts a parts run instead of one worker; the
// dispatcher then starts, merges and integrates the parts with the
// primitives below. Every write of a part names the state it read, so a
// write from a stale read changes nothing.

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

// StartLadoPartRun records that the leased entry starts its parts run: the
// run with its integration branch, one waiting row per part, and the entry's
// parts_started_at, which clears its lease, in one transaction with the
// run's audit event. When token no longer holds the entry nothing is written
// and the error is domain.ErrLadoLeaseLost.
func (s *Store) StartLadoPartRun(ctx context.Context, token string, run domain.LadoPartRun, parts []domain.LadoTaskPart, event domain.LadoAuditEvent) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "start LADO parts run", func(q *gen.Queries) error {
		n, err := q.StartLadoPartRunEntry(ctx, gen.StartLadoPartRunEntryParams{
			PartsStartedAt: ladoNullTime(run.StartedAt),
			ID:             string(run.QueueEntryID),
			LeaseToken:     ladoNullString(token),
		})
		if err != nil {
			return err
		}
		if n == 0 || token == "" {
			return domain.ErrLadoLeaseLost
		}
		var ahead sql.NullInt64
		if run.BaseCommitsAhead != nil {
			ahead = sql.NullInt64{Int64: int64(*run.BaseCommitsAhead), Valid: true}
		}
		if err := q.InsertLadoPartRun(ctx, gen.InsertLadoPartRunParams{
			QueueEntryID:         string(run.QueueEntryID),
			TaskID:               string(run.TaskID),
			ProjectID:            string(run.ProjectID),
			PlanRevisionID:       string(run.PlanRevisionID),
			RepoDir:              run.RepoDir,
			Branch:               run.Branch,
			BaseSha:              run.BaseSHA,
			HeadSha:              run.HeadSHA,
			PlanBaseSha:          run.PlanBaseSHA,
			BaseCommitsAhead:     ahead,
			BaseBranch:           run.BaseBranch,
			BaseRemote:           run.BaseRemote,
			DispatchLocalBaseSha: run.DispatchLocalBaseSHA,
			StartedAt:            run.StartedAt.UTC(),
		}); err != nil {
			return err
		}
		for _, part := range parts {
			if err := q.InsertLadoTaskPart(ctx, gen.InsertLadoTaskPartParams{
				QueueEntryID:  string(run.QueueEntryID),
				PartID:        part.PartID,
				TaskID:        string(run.TaskID),
				Title:         part.Title,
				Position:      int64(part.Position),
				Phase:         int64(part.Phase),
				StepsJson:     ladoJSON(part.Steps),
				PathsJson:     ladoJSON(part.AffectedPaths),
				DependsOnJson: ladoJSON(part.DependsOn),
				WaitingReason: part.WaitingReason,
				UpdatedAt:     run.StartedAt.UTC(),
			}); err != nil {
				return err
			}
		}
		_, err = appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	return unwrapLadoSentinel(err)
}

// ActiveLadoPartRuns returns the parts runs in progress: started, without an
// integration worker yet, neither failed nor superseded nor cancelled, in
// the order their plans were approved.
func (s *Store) ActiveLadoPartRuns(ctx context.Context) ([]domain.LadoActivePartRun, error) {
	rows, err := s.qr.ListActiveLadoPartRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active LADO parts runs: %w", err)
	}
	runs := make([]domain.LadoActivePartRun, 0, len(rows))
	for _, row := range rows {
		runs = append(runs, domain.LadoActivePartRun{Run: ladoPartRunFromGen(row.LadoPartRun), Entry: ladoQueueEntryFromGen(row.LadoQueueEntry)})
	}
	return runs, nil
}

// LadoPartRun returns the parts run of a queue entry.
func (s *Store) LadoPartRun(ctx context.Context, entryID domain.LadoQueueEntryID) (domain.LadoPartRun, bool, error) {
	row, err := s.qr.GetLadoPartRun(ctx, string(entryID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoPartRun{}, false, nil
	}
	if err != nil {
		return domain.LadoPartRun{}, false, fmt.Errorf("read LADO parts run: %w", err)
	}
	return ladoPartRunFromGen(row), true, nil
}

// LadoTaskParts returns the parts of a queue entry's run in the plan's order.
func (s *Store) LadoTaskParts(ctx context.Context, entryID domain.LadoQueueEntryID) ([]domain.LadoTaskPart, error) {
	rows, err := s.qr.ListLadoTaskParts(ctx, string(entryID))
	if err != nil {
		return nil, fmt.Errorf("list LADO task parts: %w", err)
	}
	parts := make([]domain.LadoTaskPart, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, ladoTaskPartFromGen(row))
	}
	return parts, nil
}

// UpdateLadoTaskPart writes a part's progress, unless its stored state is no
// longer oldState: it reports whether it wrote.
func (s *Store) UpdateLadoTaskPart(ctx context.Context, part domain.LadoTaskPart, oldState domain.LadoPartState) (bool, error) {
	return s.writeLadoRow(ctx, "update LADO task part", func(q *gen.Queries) (int64, error) {
		return q.UpdateLadoTaskPart(ctx, ladoTaskPartParams(part, oldState))
	})
}

// MergeLadoTaskPart records a merge LADO made or found made: the part's new
// state and the run's head moved from oldHead to part.MergedSHA, in one
// transaction with its audit event. It reports false, writing nothing, when
// the part is no longer in oldState or the head is no longer oldHead.
func (s *Store) MergeLadoTaskPart(ctx context.Context, part domain.LadoTaskPart, oldState domain.LadoPartState, oldHead string, event domain.LadoAuditEvent) (bool, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	errStale := errors.New("stale")
	err := s.inTx(ctx, "merge LADO task part", func(q *gen.Queries) error {
		n, err := q.UpdateLadoTaskPart(ctx, ladoTaskPartParams(part, oldState))
		if err != nil {
			return err
		}
		if n == 0 {
			return errStale
		}
		// An empty part leaves the head where it is, and still checks it.
		n, err = q.SetLadoPartRunHead(ctx, gen.SetLadoPartRunHeadParams{HeadSha: part.MergedSHA, QueueEntryID: string(part.QueueEntryID), OldHeadSha: oldHead})
		if err != nil {
			return err
		}
		if n == 0 {
			return errStale
		}
		_, err = appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	if errors.Is(err, errStale) {
		return false, nil
	}
	return err == nil, err
}

// FailLadoPartRun fails a parts run whose part ran out of attempts, with its
// audit event: the task shows Failed. It reports whether it wrote.
func (s *Store) FailLadoPartRun(ctx context.Context, entryID domain.LadoQueueEntryID, message string, event domain.LadoAuditEvent) (bool, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	failed := false
	err := s.inTx(ctx, "fail LADO parts run", func(q *gen.Queries) error {
		n, err := q.FailLadoPartRun(ctx, gen.FailLadoPartRunParams{FailedAt: ladoNullTime(event.At), ErrorMessage: message, ID: string(entryID)})
		if err != nil || n == 0 {
			return err
		}
		failed = true
		_, err = appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	return failed, err
}

// EndLadoPartRun ends a parts run its task no longer wants: superseded by a
// newer approval, or cancelled with its task. It reports whether it wrote.
func (s *Store) EndLadoPartRun(ctx context.Context, entryID domain.LadoQueueEntryID, superseded bool, at time.Time) (bool, error) {
	return s.writeLadoRow(ctx, "end LADO parts run", func(q *gen.Queries) (int64, error) {
		if superseded {
			return q.SupersedeLadoPartRun(ctx, gen.SupersedeLadoPartRunParams{At: ladoNullTime(at), ID: string(entryID)})
		}
		return q.CancelLadoPartRun(ctx, gen.CancelLadoPartRunParams{At: ladoNullTime(at), ID: string(entryID)})
	})
}

// SetLadoPartSpawnRequest records the client request id of the parts run
// worker the dispatcher starts now; the worker gate grants that spawn until
// expires. It reports false while another request holds the entry or the run
// is over.
func (s *Store) SetLadoPartSpawnRequest(ctx context.Context, entryID domain.LadoQueueEntryID, request string, expires time.Time) (bool, error) {
	return s.writeLadoRow(ctx, "set LADO part spawn request", func(q *gen.Queries) (int64, error) {
		return q.SetLadoPartSpawnRequest(ctx, gen.SetLadoPartSpawnRequestParams{Request: request, ExpiresAt: ladoNullTime(expires), ID: string(entryID)})
	})
}

// ClearLadoPartSpawnRequest ends the grant of request, if it still holds.
func (s *Store) ClearLadoPartSpawnRequest(ctx context.Context, entryID domain.LadoQueueEntryID, request string) (bool, error) {
	return s.writeLadoRow(ctx, "clear LADO part spawn request", func(q *gen.Queries) (int64, error) {
		return q.ClearLadoPartSpawnRequest(ctx, gen.ClearLadoPartSpawnRequestParams{ID: string(entryID), Request: request})
	})
}

// LadoPartSpawnRequests returns the entries with a parts run spawn in
// progress, expired or not, for recovery.
func (s *Store) LadoPartSpawnRequests(ctx context.Context) ([]domain.LadoQueueEntry, error) {
	rows, err := s.qr.ListLadoPartSpawnRequests(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO part spawn requests: %w", err)
	}
	entries := make([]domain.LadoQueueEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, ladoQueueEntryFromGen(row))
	}
	return entries, nil
}

// LadoPartSpawnGrant reads what the worker gate checks before a parts run
// worker spawns: the entry's project, its dispatch hash, and whether the
// entry's current spawn request is clientRequestID and unexpired at now.
func (s *Store) LadoPartSpawnGrant(ctx context.Context, clientRequestID string, now time.Time) (domain.LadoDispatchGrant, error) {
	run, ok := domain.ParseLadoRunClientRequestID(clientRequestID)
	if !ok || run.Role == domain.LadoRoleSingle {
		return domain.LadoDispatchGrant{}, nil
	}
	row, err := s.qr.GetLadoPartSpawnGrant(ctx, string(run.Entry))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoDispatchGrant{}, nil
	}
	if err != nil {
		return domain.LadoDispatchGrant{}, fmt.Errorf("read LADO part spawn grant: %w", err)
	}
	entry := ladoQueueEntryFromGen(row.LadoQueueEntry)
	return domain.LadoDispatchGrant{
		ProjectID: entry.ProjectID,
		Hash:      domain.LadoDispatchHash(row.PlanSha256, row.SnapshotSha256),
		Active: entry.PartSpawnRequest == clientRequestID && entry.PartSpawnExpiresAt.After(now) &&
			entry.FailedAt.IsZero() && entry.SupersededAt.IsZero() && entry.CancelledAt.IsZero() && entry.DispatchedSessionID == "",
	}, nil
}

// LinkLadoPartExecution records that session exec.SessionID runs the parts
// run worker of request: one transaction stores the execution with its role,
// part, branch and start, writes part (when given) unless its state is no
// longer oldState, clears the spawn request, and for the integration worker
// marks the entry dispatched to the session. When request no longer holds
// the entry nothing is written and the error is domain.ErrLadoLeaseLost.
// Linking the same session again changes nothing and returns nil.
func (s *Store) LinkLadoPartExecution(ctx context.Context, request string, exec domain.LadoExecution, part *domain.LadoTaskPart, oldState domain.LadoPartState) error {
	if exec.SessionID == "" || exec.StartedAt.IsZero() {
		return errors.New("link LADO part execution: a session and a start time are required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "link LADO part execution", func(q *gen.Queries) error {
		linked, err := q.GetLadoExecution(ctx, string(exec.SessionID))
		switch {
		case err == nil && linked.LadoExecution.QueueEntryID == string(exec.QueueEntryID):
			return nil
		case err == nil:
			return fmt.Errorf("session %s already runs queue entry %s", exec.SessionID, linked.LadoExecution.QueueEntryID)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		row, err := q.GetLadoQueueEntryPlan(ctx, string(exec.QueueEntryID))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrLadoLeaseLost
		}
		if err != nil {
			return err
		}
		entry := row.LadoQueueEntry
		if request == "" || entry.PartSpawnRequest != request {
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
			Attempt:              int64(max(exec.Attempt, 1)),
			PlanBaseSha:          row.PlanBaseSha,
			DispatchBaseSha:      exec.DispatchBaseSHA,
			BaseCommitsAhead:     ahead,
			StartedAt:            exec.StartedAt.UTC(),
			DeadlineAt:           ladoNullTime(exec.DeadlineAt),
			BaseBranch:           exec.BaseBranch,
			BaseRemote:           exec.BaseRemote,
			DispatchLocalBaseSha: exec.DispatchLocalBaseSHA,
			Role:                 exec.Role,
			PartID:               exec.PartID,
			Branch:               exec.Branch,
			StartSha:             exec.StartSHA,
		}); err != nil {
			return err
		}
		if part != nil {
			n, err := q.UpdateLadoTaskPart(ctx, ladoTaskPartParams(*part, oldState))
			if err != nil {
				return err
			}
			if n == 0 {
				return domain.ErrLadoLeaseLost
			}
		}
		var n int64
		if exec.Role == domain.LadoRoleIntegration {
			n, err = q.SetLadoPartRunDispatched(ctx, gen.SetLadoPartRunDispatchedParams{
				DispatchedSessionID: ladoNullString(string(exec.SessionID)), ID: entry.ID, Request: request,
			})
		} else {
			n, err = q.ClearLadoPartSpawnRequest(ctx, gen.ClearLadoPartSpawnRequestParams{ID: entry.ID, Request: request})
		}
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

// LadoRunLoad reads what runs at now: the tasks with a live execution, an
// active lease or a running parts run, and how many LADO workers run or are
// starting.
func (s *Store) LadoRunLoad(ctx context.Context, now time.Time) (domain.LadoRunLoad, error) {
	// Workers first: a link between the two reads moves a start into a
	// session, which then counts twice rather than not at all.
	workers, err := s.qr.CountLiveLadoWorkers(ctx, ladoNullTime(now))
	if err != nil {
		return domain.LadoRunLoad{}, fmt.Errorf("count live LADO workers: %w", err)
	}
	rows, err := s.qr.ListLiveLadoRunTasks(ctx, ladoNullTime(now))
	if err != nil {
		return domain.LadoRunLoad{}, fmt.Errorf("list running LADO tasks: %w", err)
	}
	load := domain.LadoRunLoad{Workers: int(workers), Tasks: make([]domain.LadoLiveRunTask, 0, len(rows))}
	for _, row := range rows {
		load.Tasks = append(load.Tasks, domain.LadoLiveRunTask{
			TaskID:         domain.LadoTaskID(row.TaskID),
			ProjectID:      domain.ProjectID(row.ProjectID),
			IssueRef:       domain.IssueID(row.IssueRef),
			PlanRevisionID: domain.LadoPlanRevisionID(row.PlanRevisionID),
		})
	}
	return load, nil
}

// LadoExecutionsForEntry returns an entry's executions of every role, oldest
// first, with whether each session ended.
func (s *Store) LadoExecutionsForEntry(ctx context.Context, entryID domain.LadoQueueEntryID) ([]domain.LadoExecution, error) {
	rows, err := s.qr.ListLadoExecutionsForEntry(ctx, string(entryID))
	if err != nil {
		return nil, fmt.Errorf("list LADO executions of an entry: %w", err)
	}
	executions := make([]domain.LadoExecution, 0, len(rows))
	for _, row := range rows {
		exec := ladoExecutionFromGen(row.LadoExecution)
		exec.SessionTerminated = row.SessionTerminated
		executions = append(executions, exec)
	}
	return executions, nil
}

// LatestSessionReport returns the state and note of a session's newest AO
// report; false while it has none.
func (s *Store) LatestSessionReport(ctx context.Context, sessionID domain.SessionID) (domain.ReportState, string, bool, error) {
	row, err := s.qr.LatestSessionReportState(ctx, string(sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("read the newest report of session %s: %w", sessionID, err)
	}
	return domain.ReportState(row.State), row.Note, true, nil
}

func ladoPartRunFromGen(row gen.LadoPartRun) domain.LadoPartRun {
	var ahead *int
	if row.BaseCommitsAhead.Valid {
		n := int(row.BaseCommitsAhead.Int64)
		ahead = &n
	}
	return domain.LadoPartRun{
		QueueEntryID:         domain.LadoQueueEntryID(row.QueueEntryID),
		TaskID:               domain.LadoTaskID(row.TaskID),
		ProjectID:            domain.ProjectID(row.ProjectID),
		RepoDir:              row.RepoDir,
		Branch:               row.Branch,
		BaseSHA:              row.BaseSha,
		HeadSHA:              row.HeadSha,
		PlanRevisionID:       domain.LadoPlanRevisionID(row.PlanRevisionID),
		PlanBaseSHA:          row.PlanBaseSha,
		BaseCommitsAhead:     ahead,
		BaseBranch:           row.BaseBranch,
		BaseRemote:           row.BaseRemote,
		DispatchLocalBaseSHA: row.DispatchLocalBaseSha,
		StartedAt:            row.StartedAt.UTC(),
		IntegrationAttempts:  int(row.IntegrationAttempts),
		IntegrationRetryAt:   ladoTimeFromNull(row.IntegrationRetryAt),
	}
}

// BumpLadoPartRunIntegrationAttempts counts one more start of the run's
// integration worker, while the run still has old starts, and records when a
// start that fails may be tried again; it reports whether it wrote.
func (s *Store) BumpLadoPartRunIntegrationAttempts(ctx context.Context, entryID domain.LadoQueueEntryID, old int, retryAt time.Time) (bool, error) {
	return s.writeLadoRow(ctx, "count a LADO integration start", func(q *gen.Queries) (int64, error) {
		return q.BumpLadoPartRunIntegrationAttempts(ctx, gen.BumpLadoPartRunIntegrationAttemptsParams{
			RetryAt: ladoNullTime(retryAt), QueueEntryID: string(entryID), OldAttempts: int64(old),
		})
	})
}

func ladoTaskPartFromGen(row gen.LadoTaskPart) domain.LadoTaskPart {
	part := domain.LadoTaskPart{
		QueueEntryID:   domain.LadoQueueEntryID(row.QueueEntryID),
		TaskID:         domain.LadoTaskID(row.TaskID),
		PartID:         row.PartID,
		Title:          row.Title,
		Position:       int(row.Position),
		Phase:          int(row.Phase),
		State:          domain.LadoPartState(row.State),
		Attempts:       int(row.Attempts),
		MergeAttempts:  int(row.MergeAttempts),
		WaitingReason:  row.WaitingReason,
		WaitingDetail:  row.WaitingDetail,
		SessionID:      domain.SessionID(row.SessionID),
		Branch:         row.Branch,
		StartSHA:       row.StartSha,
		MergeFromSHA:   row.MergeFromSha,
		MergeTipSHA:    row.MergeTipSha,
		MergedSHA:      row.MergedSha,
		MergeSessionID: domain.SessionID(row.MergeSessionID),
		MergeBranch:    row.MergeBranch,
		Summary:        row.Summary,
		Error:          row.ErrorMessage,
		UpdatedAt:      row.UpdatedAt.UTC(),
	}
	// The columns only ever hold what the store wrote.
	_ = json.Unmarshal([]byte(row.StepsJson), &part.Steps)
	_ = json.Unmarshal([]byte(row.PathsJson), &part.AffectedPaths)
	_ = json.Unmarshal([]byte(row.DependsOnJson), &part.DependsOn)
	return part
}

func ladoTaskPartParams(part domain.LadoTaskPart, oldState domain.LadoPartState) gen.UpdateLadoTaskPartParams {
	updated := part.UpdatedAt
	if updated.IsZero() {
		updated = time.Now()
	}
	return gen.UpdateLadoTaskPartParams{
		State:          string(part.State),
		Attempts:       int64(part.Attempts),
		MergeAttempts:  int64(part.MergeAttempts),
		WaitingReason:  part.WaitingReason,
		WaitingDetail:  part.WaitingDetail,
		SessionID:      string(part.SessionID),
		Branch:         part.Branch,
		StartSha:       part.StartSHA,
		MergeFromSha:   part.MergeFromSHA,
		MergeTipSha:    part.MergeTipSHA,
		MergedSha:      part.MergedSHA,
		MergeSessionID: string(part.MergeSessionID),
		MergeBranch:    part.MergeBranch,
		Summary:        part.Summary,
		ErrorMessage:   part.Error,
		UpdatedAt:      updated.UTC(),
		QueueEntryID:   string(part.QueueEntryID),
		PartID:         part.PartID,
		OldState:       string(oldState),
	}
}

// ladoJSON encodes a list for a JSON column; nil is an empty list.
func ladoJSON[T any](values []T) string {
	if values == nil {
		return "[]"
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(data)
}
