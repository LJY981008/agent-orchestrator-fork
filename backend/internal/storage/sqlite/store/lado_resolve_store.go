package store

// LADO fork: resolving an execution's PR (issue #53). When an execution's
// open PR conflicts with its base, or the base moved under its head with a
// change to the same paths, and the execution's own session no longer works,
// the dispatcher starts a resolve worker (role merge, no part) on the PR. Its
// row in lado_pr_resolves records the state, the failed attempts in a row,
// the newest worker and the spawn in progress. The resolver writes the row
// over the version it read; the spawn request has its own writes, as the
// parts run spawn request has, and the worker gate grants it.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// LadoPRResolve returns the resolve row of an execution; false while it has
// none.
func (s *Store) LadoPRResolve(ctx context.Context, executionSessionID domain.SessionID) (domain.LadoPRResolve, bool, error) {
	row, err := s.qr.GetLadoPRResolve(ctx, string(executionSessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoPRResolve{}, false, nil
	}
	if err != nil {
		return domain.LadoPRResolve{}, false, fmt.Errorf("read LADO PR resolve: %w", err)
	}
	return ladoPRResolveFromGen(row), true, nil
}

// UpsertLadoPRResolve writes the fields of r the resolver owns: the state,
// the attempts, the worker's session, its start head and base tip, when its
// done report was seen, the head it was told about and the error, at
// r.UpdatedAt. A version of 0 creates the row for execution
// r.ExecutionSessionID, taking its task and entry; any other writes over that
// version only. It reports false, writing nothing, when the row changed since
// the caller read it (or was created meanwhile). The spawn request and the
// spawn count are not written here.
func (s *Store) UpsertLadoPRResolve(ctx context.Context, r domain.LadoPRResolve) (bool, error) {
	if r.ExecutionSessionID == "" || r.UpdatedAt.IsZero() {
		return false, errors.New("upsert LADO PR resolve: an execution and a time are required")
	}
	switch r.State {
	case domain.LadoResolveNone, domain.LadoResolveResolving, domain.LadoResolveNeedsHuman:
	default:
		return false, fmt.Errorf("upsert LADO PR resolve: unknown state %q", r.State)
	}
	if r.Attempts < 0 {
		return false, errors.New("upsert LADO PR resolve: attempts cannot be negative")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	written := false
	err := s.inTx(ctx, "upsert LADO PR resolve", func(q *gen.Queries) error {
		if r.Version > 0 {
			n, err := q.UpdateLadoPRResolve(ctx, gen.UpdateLadoPRResolveParams{
				State:              string(r.State),
				Attempts:           int64(r.Attempts),
				SessionID:          string(r.SessionID),
				StartHeadSha:       r.StartHeadSHA,
				BaseTipSha:         r.BaseTipSHA,
				DoneSeenAt:         ladoNullTime(r.DoneSeenAt),
				NotifiedHeadSha:    r.NotifiedHeadSHA,
				Error:              r.Error,
				UpdatedAt:          r.UpdatedAt.UTC(),
				ExecutionSessionID: string(r.ExecutionSessionID),
				OldVersion:         int64(r.Version),
			})
			written = n > 0
			return err
		}
		exec, err := q.GetLadoExecution(ctx, string(r.ExecutionSessionID))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("session %s is not a LADO execution", r.ExecutionSessionID)
		}
		if err != nil {
			return err
		}
		n, err := q.InsertLadoPRResolve(ctx, gen.InsertLadoPRResolveParams{
			ExecutionSessionID: string(r.ExecutionSessionID),
			TaskID:             exec.LadoExecution.TaskID,
			QueueEntryID:       exec.LadoExecution.QueueEntryID,
			State:              string(r.State),
			Attempts:           int64(r.Attempts),
			SessionID:          string(r.SessionID),
			StartHeadSha:       r.StartHeadSHA,
			BaseTipSha:         r.BaseTipSHA,
			DoneSeenAt:         ladoNullTime(r.DoneSeenAt),
			NotifiedHeadSha:    r.NotifiedHeadSHA,
			Error:              r.Error,
			CreatedAt:          r.UpdatedAt.UTC(),
		})
		written = n > 0
		return err
	})
	if err != nil {
		return false, err
	}
	return written, nil
}

// SetLadoResolveSpawnRequest records that the dispatcher starts a resolve
// worker for the execution's PR now; the worker gate grants that spawn until
// expires. It returns the worker's client request id,
// "lado:<entry>/_resolve/<n>" (domain.LadoResolveClientRequestID), whose n is
// one more than any resolve worker of the entry before, so an id never
// repeats; the attempt and the branch follow from it. It reports false while
// another spawn holds the row, and for an execution without a resolve row
// (UpsertLadoPRResolve creates it).
func (s *Store) SetLadoResolveSpawnRequest(ctx context.Context, executionSessionID domain.SessionID, now, expires time.Time) (string, bool, error) {
	if now.IsZero() || !expires.After(now) {
		return "", false, errors.New("set LADO resolve spawn request: a time and a later expiry are required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return "", false, err
	}
	defer s.writeMu.Unlock()
	request := ""
	err := s.inTx(ctx, "set LADO resolve spawn request", func(q *gen.Queries) error {
		row, err := q.GetLadoPRResolve(ctx, string(executionSessionID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.SpawnRequest != "" {
			return nil
		}
		spawns, err := q.MaxLadoResolveSpawns(ctx, row.QueueEntryID)
		if err != nil {
			return err
		}
		next := max(spawns, row.Spawns) + 1
		id := domain.LadoResolveClientRequestID(domain.LadoQueueEntryID(row.QueueEntryID), int(next))
		n, err := q.SetLadoResolveSpawnRequest(ctx, gen.SetLadoResolveSpawnRequestParams{
			Request:            id,
			ExpiresAt:          ladoNullTime(expires),
			Spawns:             next,
			UpdatedAt:          now.UTC(),
			ExecutionSessionID: string(executionSessionID),
		})
		if err != nil || n == 0 {
			return err
		}
		request = id
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return request, request != "", nil
}

// ClearLadoResolveSpawnRequest ends the grant of request, if it still holds
// the execution's resolve row.
func (s *Store) ClearLadoResolveSpawnRequest(ctx context.Context, executionSessionID domain.SessionID, request string) (bool, error) {
	return s.writeLadoRow(ctx, "clear LADO resolve spawn request", func(q *gen.Queries) (int64, error) {
		return q.ClearLadoResolveSpawnRequest(ctx, gen.ClearLadoResolveSpawnRequestParams{
			ExecutionSessionID: string(executionSessionID), Request: request,
		})
	})
}

// ListExpiredLadoResolveSpawns returns the resolve rows whose spawn request
// expired at or before now, oldest first, for recovery: the dispatcher
// stopped between starting a worker and linking it.
func (s *Store) ListExpiredLadoResolveSpawns(ctx context.Context, now time.Time) ([]domain.LadoPRResolve, error) {
	rows, err := s.qr.ListExpiredLadoResolveSpawns(ctx, ladoNullTime(now))
	if err != nil {
		return nil, fmt.Errorf("list expired LADO resolve spawns: %w", err)
	}
	resolves := make([]domain.LadoPRResolve, 0, len(rows))
	for _, row := range rows {
		resolves = append(resolves, ladoPRResolveFromGen(row))
	}
	return resolves, nil
}

// LinkLadoResolveExecution records that session exec.SessionID runs the
// resolve worker of request: one transaction stores the execution (role
// merge, no part) with the task, entry, plan, approval, rule snapshot,
// attempt and base facts of the execution whose PR it resolves, and the
// session, start, deadline, branch and start commit the caller gives; it
// ends the spawn request and makes the row resolving with that session, its
// done report not seen yet. When request no longer holds a resolve row
// nothing is written and the error is domain.ErrLadoLeaseLost. Linking the
// same session again changes nothing and returns nil.
func (s *Store) LinkLadoResolveExecution(ctx context.Context, request string, exec domain.LadoExecution) error {
	if exec.SessionID == "" || exec.StartedAt.IsZero() {
		return errors.New("link LADO resolve execution: a session and a start time are required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "link LADO resolve execution", func(q *gen.Queries) error {
		linked, err := q.GetLadoExecution(ctx, string(exec.SessionID))
		switch {
		case err == nil && linked.LadoExecution.Role == domain.LadoRoleMerge && linked.LadoExecution.PartID == "" &&
			(exec.QueueEntryID == "" || linked.LadoExecution.QueueEntryID == string(exec.QueueEntryID)):
			return nil
		case err == nil:
			return fmt.Errorf("session %s already runs queue entry %s", exec.SessionID, linked.LadoExecution.QueueEntryID)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if request == "" {
			return domain.ErrLadoLeaseLost
		}
		row, err := q.GetLadoPRResolveBySpawnRequest(ctx, request)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrLadoLeaseLost
		}
		if err != nil {
			return err
		}
		original, err := q.GetLadoExecution(ctx, row.ExecutionSessionID)
		if err != nil {
			return fmt.Errorf("read the execution resolved: %w", err)
		}
		o := original.LadoExecution
		if err := q.InsertLadoExecution(ctx, gen.InsertLadoExecutionParams{
			SessionID:            string(exec.SessionID),
			TaskID:               o.TaskID,
			QueueEntryID:         o.QueueEntryID,
			PlanRevisionID:       o.PlanRevisionID,
			PlanApprovalID:       o.PlanApprovalID,
			PolicySnapshotID:     o.PolicySnapshotID,
			Attempt:              o.Attempt,
			PlanBaseSha:          o.PlanBaseSha,
			DispatchBaseSha:      o.DispatchBaseSha,
			BaseCommitsAhead:     o.BaseCommitsAhead,
			StartedAt:            exec.StartedAt.UTC(),
			DeadlineAt:           ladoNullTime(exec.DeadlineAt),
			BaseBranch:           o.BaseBranch,
			BaseRemote:           o.BaseRemote,
			DispatchLocalBaseSha: o.DispatchLocalBaseSha,
			Role:                 domain.LadoRoleMerge,
			PartID:               "",
			Branch:               exec.Branch,
			StartSha:             exec.StartSHA,
		}); err != nil {
			return err
		}
		n, err := q.LinkLadoResolveSpawn(ctx, gen.LinkLadoResolveSpawnParams{
			SessionID:          string(exec.SessionID),
			UpdatedAt:          exec.StartedAt.UTC(),
			ExecutionSessionID: row.ExecutionSessionID,
			Request:            request,
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

// ListLadoExecutionsToResolve returns up to limit executions the resolver
// looks at, oldest first: those that ran a PR (role empty or integration) whose
// newest PR is open with an observed head and conflicts with its base, or
// whose record of that head failed with base_overlap, and those whose
// resolve row still has a state or a spawn request, whatever their PR (so the
// resolver can stop and clear it once the PR merged or closed). Each comes
// with its PR, verification and resolve facts, its project, the PR's source
// branch and whether its session and its resolve worker still work.
func (s *Store) ListLadoExecutionsToResolve(ctx context.Context, limit int) ([]domain.LadoResolveCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.qr.ListLadoExecutionsToResolve(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("list LADO executions to resolve: %w", err)
	}
	executions := make([]domain.LadoExecution, 0, len(rows))
	for _, row := range rows {
		execution := ladoExecutionFromGen(row.LadoExecution)
		execution.SessionTerminated = row.SessionTerminated
		executions = append(executions, execution)
	}
	if err := s.fillLadoExecutionPRs(ctx, executions); err != nil {
		return nil, err
	}
	if err := s.fillLadoVerification(ctx, executions); err != nil {
		return nil, err
	}
	resolves, err := s.fillLadoResolves(ctx, executions)
	if err != nil {
		return nil, err
	}
	candidates := make([]domain.LadoResolveCandidate, 0, len(rows))
	for i, row := range rows {
		candidates = append(candidates, domain.LadoResolveCandidate{
			Execution:          executions[i],
			ProjectID:          domain.ProjectID(row.ProjectID),
			PRSourceBranch:     row.PRSourceBranch,
			SessionLive:        row.SessionLive,
			ResolveSessionLive: row.ResolveSessionLive,
			Resolve:            resolves[executions[i].SessionID],
		})
	}
	return candidates, nil
}

// LadoExecutionSessionsToObserve returns the sessions of the executions that
// ran a PR (role empty or integration) whose newest PR is open and not settled,
// whether the session ended or not: AO's PR observer keeps observing them
// for LADO.
func (s *Store) LadoExecutionSessionsToObserve(ctx context.Context) ([]domain.SessionID, error) {
	ids, err := s.qr.ListLadoExecutionSessionsToObserve(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO execution sessions to observe: %w", err)
	}
	sessions := make([]domain.SessionID, 0, len(ids))
	for _, id := range ids {
		sessions = append(sessions, domain.SessionID(id))
	}
	return sessions, nil
}

// LadoOverlapHolders returns the tasks whose paths other tasks wait for when
// overlap waiting is on: as LadoRunLoad's tasks, except that an execution
// that timed out, whose entry or task was cancelled, or whose entry's PR
// merged or closed no longer holds, even while its session runs. Active
// leases and running parts runs hold.
func (s *Store) LadoOverlapHolders(ctx context.Context, now time.Time) ([]domain.LadoLiveRunTask, error) {
	rows, err := s.qr.ListLadoOverlapHolders(ctx, ladoNullTime(now))
	if err != nil {
		return nil, fmt.Errorf("list LADO overlap holders: %w", err)
	}
	tasks := make([]domain.LadoLiveRunTask, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, domain.LadoLiveRunTask{
			TaskID:         domain.LadoTaskID(row.TaskID),
			ProjectID:      domain.ProjectID(row.ProjectID),
			IssueRef:       domain.IssueID(row.IssueRef),
			PlanRevisionID: domain.LadoPlanRevisionID(row.PlanRevisionID),
		})
	}
	return tasks, nil
}

// ListLadoQueuedOverlapCandidates returns the entries of the entry's project,
// of other tasks, whose plan was approved before the entry's (ties by entry
// id) and that still wait for a worker: queued, waiting or dispatching, in
// approval order, as tasks with the plan revision their approval names. A
// blocked entry, one that needs a review, failed, ran parts, was dispatched,
// superseded or cancelled is not among them, so it never holds a later one.
func (s *Store) ListLadoQueuedOverlapCandidates(ctx context.Context, entryID domain.LadoQueueEntryID) ([]domain.LadoLiveRunTask, error) {
	rows, err := s.qr.ListLadoQueuedOverlapCandidates(ctx, string(entryID))
	if err != nil {
		return nil, fmt.Errorf("list LADO queued overlap candidates: %w", err)
	}
	tasks := make([]domain.LadoLiveRunTask, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, domain.LadoLiveRunTask{
			TaskID:         domain.LadoTaskID(row.TaskID),
			ProjectID:      domain.ProjectID(row.ProjectID),
			IssueRef:       domain.IssueID(row.IssueRef),
			PlanRevisionID: domain.LadoPlanRevisionID(row.PlanRevisionID),
		})
	}
	return tasks, nil
}

// fillLadoResolves sets each execution's resolve from its resolve row and
// returns the rows by execution.
func (s *Store) fillLadoResolves(ctx context.Context, executions []domain.LadoExecution) (map[domain.SessionID]domain.LadoPRResolve, error) {
	if len(executions) == 0 {
		return nil, nil
	}
	sessions := make([]string, 0, len(executions))
	for _, execution := range executions {
		sessions = append(sessions, string(execution.SessionID))
	}
	rows, err := s.qr.ListLadoPRResolvesForSessions(ctx, sessions)
	if err != nil {
		return nil, fmt.Errorf("read LADO PR resolves: %w", err)
	}
	resolves := make(map[domain.SessionID]domain.LadoPRResolve, len(rows))
	for _, row := range rows {
		resolves[domain.SessionID(row.ExecutionSessionID)] = ladoPRResolveFromGen(row)
	}
	for i := range executions {
		if resolve, ok := resolves[executions[i].SessionID]; ok {
			executions[i].Resolve = resolve.Execution()
		}
	}
	return resolves, nil
}

func ladoPRResolveFromGen(row gen.LadoPRResolf) domain.LadoPRResolve {
	return domain.LadoPRResolve{
		ExecutionSessionID: domain.SessionID(row.ExecutionSessionID),
		TaskID:             domain.LadoTaskID(row.TaskID),
		QueueEntryID:       domain.LadoQueueEntryID(row.QueueEntryID),
		State:              domain.LadoResolveState(row.State),
		Attempts:           int(row.Attempts),
		Spawns:             int(row.Spawns),
		SessionID:          domain.SessionID(row.SessionID),
		StartHeadSHA:       row.StartHeadSha,
		BaseTipSHA:         row.BaseTipSha,
		SpawnRequest:       row.SpawnRequest,
		SpawnExpiresAt:     ladoTimeFromNull(row.SpawnExpiresAt),
		DoneSeenAt:         ladoTimeFromNull(row.DoneSeenAt),
		NotifiedHeadSHA:    row.NotifiedHeadSha,
		Error:              row.Error,
		Version:            int(row.Version),
		CreatedAt:          row.CreatedAt.UTC(),
		UpdatedAt:          row.UpdatedAt.UTC(),
	}
}
