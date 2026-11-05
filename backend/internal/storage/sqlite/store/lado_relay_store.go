package store

// LADO fork: relays of developer instructions to running workers (issue #49,
// docs/LJY-PRD/adr/0010-worker-relay.md). The relay service records each
// relay as sending before it delivers the message and marks it delivered or
// failed afterwards. It reads a task's executions of every role, with whether
// each session ended and its activity, to pick the worker.

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

// InsertLadoRelay records r as sending at r.CreatedAt. Its state, failure
// and delivery time are not written; MarkLadoRelayDelivered and
// MarkLadoRelayFailed set them.
func (s *Store) InsertLadoRelay(ctx context.Context, r domain.LadoRelay) error {
	if r.ID == "" || r.TaskID == "" || r.CreatedAt.IsZero() || r.EvidenceAt.IsZero() {
		return errors.New("insert LADO relay: an id, a task and the times are required")
	}
	reasons := r.OutsidePlanReasons
	if reasons == nil {
		reasons = []string{}
	}
	reasonsJSON, err := json.Marshal(reasons)
	if err != nil {
		return fmt.Errorf("insert LADO relay: encode outside-plan reasons: %w", err)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if err := s.qw.InsertLadoRelay(ctx, gen.InsertLadoRelayParams{
		ID:                     string(r.ID),
		TaskID:                 string(r.TaskID),
		ProjectID:              string(r.ProjectID),
		OrchestratorSessionID:  string(r.OrchestratorSessionID),
		WorkerSessionID:        string(r.WorkerSessionID),
		WorkerRole:             r.WorkerRole,
		PartID:                 r.PartID,
		Instruction:            r.Instruction,
		Evidence:               r.Evidence,
		EvidenceMessageID:      r.EvidenceMessageID,
		EvidenceAt:             r.EvidenceAt.UTC(),
		BeyondEvidence:         ladoBool(r.BeyondEvidence),
		OutsidePlan:            ladoBool(r.OutsidePlan),
		OutsidePlanReasonsJson: string(reasonsJSON),
		CreatedAt:              r.CreatedAt.UTC(),
	}); err != nil {
		return fmt.Errorf("insert LADO relay: %w", err)
	}
	return nil
}

// MarkLadoRelayDelivered marks a sending relay delivered at at. It reports
// false, writing nothing, for a relay that is unknown or no longer sending.
func (s *Store) MarkLadoRelayDelivered(ctx context.Context, id domain.LadoRelayID, at time.Time) (bool, error) {
	if at.IsZero() {
		return false, errors.New("mark LADO relay delivered: a time is required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.MarkLadoRelayDelivered(ctx, gen.MarkLadoRelayDeliveredParams{
		DeliveredAt: ladoNullTime(at),
		ID:          string(id),
	})
	if err != nil {
		return false, fmt.Errorf("mark LADO relay delivered: %w", err)
	}
	return n == 1, nil
}

// MarkLadoRelayFailed marks a sending relay failed for failure. It reports
// false, writing nothing, for a relay that is unknown or no longer sending.
func (s *Store) MarkLadoRelayFailed(ctx context.Context, id domain.LadoRelayID, failure string) (bool, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.MarkLadoRelayFailed(ctx, gen.MarkLadoRelayFailedParams{Failure: failure, ID: string(id)})
	if err != nil {
		return false, fmt.Errorf("mark LADO relay failed: %w", err)
	}
	return n == 1, nil
}

// LadoRelay reads one relay; false when it does not exist.
func (s *Store) LadoRelay(ctx context.Context, id domain.LadoRelayID) (domain.LadoRelay, bool, error) {
	row, err := s.qr.GetLadoRelay(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoRelay{}, false, nil
	}
	if err != nil {
		return domain.LadoRelay{}, false, fmt.Errorf("read LADO relay: %w", err)
	}
	return ladoRelayFromGen(row), true, nil
}

// LadoRelaysByTask lists a task's relays, newest first.
func (s *Store) LadoRelaysByTask(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoRelay, error) {
	rows, err := s.qr.ListLadoRelaysByTask(ctx, string(taskID))
	if err != nil {
		return nil, fmt.Errorf("read LADO relays: %w", err)
	}
	relays := make([]domain.LadoRelay, 0, len(rows))
	for _, row := range rows {
		relays = append(relays, ladoRelayFromGen(row))
	}
	return relays, nil
}

// LadoRelayExecutions lists every execution of a task, of every role, newest
// first, with whether each session ended (Execution.SessionTerminated) and
// its activity. An execution whose session row is gone counts as ended and
// has no activity. PR and verification facts are not read.
func (s *Store) LadoRelayExecutions(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoRelayExecution, error) {
	rows, err := s.qr.ListLadoRelayExecutionsByTask(ctx, string(taskID))
	if err != nil {
		return nil, fmt.Errorf("read LADO relay executions: %w", err)
	}
	executions := make([]domain.LadoRelayExecution, 0, len(rows))
	for _, row := range rows {
		execution := ladoExecutionFromGen(row.LadoExecution)
		execution.SessionTerminated = row.SessionTerminated
		executions = append(executions, domain.LadoRelayExecution{
			Execution:       execution,
			SessionActivity: domain.ActivityState(row.SessionActivity),
		})
	}
	return executions, nil
}

func ladoRelayFromGen(row gen.LadoRelay) domain.LadoRelay {
	reasons := []string{}
	// The column only ever holds what the store wrote.
	_ = json.Unmarshal([]byte(row.OutsidePlanReasonsJson), &reasons)
	return domain.LadoRelay{
		ID:                    domain.LadoRelayID(row.ID),
		TaskID:                domain.LadoTaskID(row.TaskID),
		ProjectID:             domain.ProjectID(row.ProjectID),
		OrchestratorSessionID: domain.SessionID(row.OrchestratorSessionID),
		WorkerSessionID:       domain.SessionID(row.WorkerSessionID),
		WorkerRole:            row.WorkerRole,
		PartID:                row.PartID,
		Instruction:           row.Instruction,
		Evidence:              row.Evidence,
		EvidenceMessageID:     row.EvidenceMessageID,
		EvidenceAt:            row.EvidenceAt.UTC(),
		BeyondEvidence:        row.BeyondEvidence == 1,
		OutsidePlan:           row.OutsidePlan == 1,
		OutsidePlanReasons:    reasons,
		State:                 domain.LadoRelayState(row.State),
		Failure:               row.Failure,
		CreatedAt:             row.CreatedAt.UTC(),
		DeliveredAt:           ladoTimeFromNull(row.DeliveredAt),
	}
}
