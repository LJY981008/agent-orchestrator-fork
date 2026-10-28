package ladoverify

// Settling (decision l of the step 8 plan): once an execution's PR merged or
// closed, its verification ends. A merge of a head without an integration
// approval went around LADO, which the audit log records, for an execution
// dispatched under integration approval. A PR is settled per URL and state:
// a closed PR that opens again and merges, or a newer PR, settles again.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// settle records that the execution's merged or closed PR needs no more
// verification, and audits execution.merged_without_approval when the merged
// head had no integration approval and the execution was dispatched under
// integration approval. The store settles a PR in one state once, so the
// event is appended once.
func (s *Service) settle(ctx context.Context, exec domain.LadoExecution) error {
	now := s.now()
	state := domain.PRStateClosed
	if exec.PR.Merged {
		state = domain.PRStateMerged
	}
	var audit *domain.LadoAuditEvent
	if exec.PR.Merged && exec.IntegrationGated && !exec.Integration.MergedHeadApproved {
		payload, err := json.Marshal(map[string]any{"task": exec.TaskID, "session": exec.SessionID, "pr": exec.PR.URL, "head": exec.PR.HeadSHA})
		if err != nil {
			return err
		}
		audit = &domain.LadoAuditEvent{
			At:          now,
			Actor:       "lado",
			Action:      "execution.merged_without_approval",
			SubjectKind: "execution",
			SubjectID:   string(exec.SessionID),
			Payload:     payload,
		}
	}
	if err := s.deps.Store.SettleLadoExecution(ctx, exec.SessionID, exec.PR.URL, state, now, audit); err != nil {
		return fmt.Errorf("settle the execution: %w", err)
	}
	if audit != nil {
		s.deps.Log.Warn("LADO saw a pull request merged without an integration approval", "task", exec.TaskID, "session", exec.SessionID, "pr", exec.PR.URL, "head", exec.PR.HeadSHA)
	}
	return nil
}
