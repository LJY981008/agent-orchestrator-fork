package store

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// RecordLadoClaudeChromeChange stores a Claude in Chrome settings change:
// the presence proof that allowed it (only when something turned on) and its
// audit event, in one transaction. A used nonce returns
// domain.ErrLadoPresenceReplayed and leaves nothing behind. The setting's
// value lives in a file the service writes after this succeeds.
func (s *Store) RecordLadoClaudeChromeChange(ctx context.Context, proof *domain.LadoPresenceProof, event domain.LadoAuditEvent) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "record Claude in Chrome change", func(q *gen.Queries) error {
		if err := insertLadoPresenceProofIfAny(ctx, q, proof); err != nil {
			return err
		}
		_, err := appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	return unwrapLadoSentinel(err)
}
