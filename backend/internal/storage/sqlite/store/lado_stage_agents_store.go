package store

// LADO fork: the model and effort each LADO stage's workers run with (issue
// #103, domain.LadoStageAgents).

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// LadoStageAgents returns one scope's stage settings: the global defaults for
// domain.LadoStageGlobalScope, else the project's override. A scope without
// settings gives an empty, non-nil map.
func (s *Store) LadoStageAgents(ctx context.Context, projectID domain.ProjectID) (domain.LadoStageAgents, error) {
	rows, err := s.qr.ListLadoStageAgents(ctx, string(projectID))
	if err != nil {
		return nil, fmt.Errorf("read LADO stage agents: %w", err)
	}
	agents := make(domain.LadoStageAgents, len(rows))
	for _, row := range rows {
		agents[domain.LadoStage(row.Stage)] = domain.LadoStageAgent{
			Harness: domain.AgentHarness(row.Harness),
			Model:   row.Model,
			Effort:  row.Effort,
		}
	}
	return agents, nil
}

// SetLadoStageAgents replaces one scope's stage settings with agents, and
// appends the audit event, in one transaction; the rows take the event's
// time. A stage agents leaves out follows the default afterwards, and an
// empty map clears the scope. agents must pass domain.ValidateLadoStageAgents,
// else nothing is kept and its error comes back; checking the models against
// the agents' catalogs is the caller's.
func (s *Store) SetLadoStageAgents(ctx context.Context, projectID domain.ProjectID, agents domain.LadoStageAgents, audit domain.LadoAuditEvent) error {
	if err := domain.ValidateLadoStageAgents(agents); err != nil {
		return err
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "set LADO stage agents", func(q *gen.Queries) error {
		if err := q.DeleteLadoStageAgents(ctx, string(projectID)); err != nil {
			return err
		}
		for _, stage := range domain.LadoStages {
			agent, ok := agents[stage]
			if !ok {
				continue
			}
			if err := q.InsertLadoStageAgent(ctx, gen.InsertLadoStageAgentParams{
				ProjectID: string(projectID),
				Stage:     string(stage),
				Harness:   string(agent.Harness),
				Model:     agent.Model,
				Effort:    agent.Effort,
				UpdatedAt: audit.At.UTC(),
			}); err != nil {
				return err
			}
		}
		_, err := appendLadoAuditEventTx(ctx, q, audit)
		return err
	})
}
