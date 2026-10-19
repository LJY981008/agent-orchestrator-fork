package sessionmanager

// LADO fork: the worker gate's hook points in the session manager
// (docs/LJY-PRD/TDD.md §6).

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SetWorkGate installs LADO's worker gate. The daemon calls it before anything
// can spawn or send; a nil gate keeps upstream behavior.
func (m *Manager) SetWorkGate(gate ports.WorkGate) {
	m.workGate = gate
}

func (m *Manager) authorizeLadoSpawn(ctx context.Context, cfg ports.SpawnConfig) error {
	if m.workGate == nil {
		return nil
	}
	return m.workGate.AuthorizeSpawn(ctx, cfg)
}

func (m *Manager) authorizeLadoSend(ctx context.Context, id domain.SessionID, message string, options ports.MessageDeliveryOptions) error {
	if m.workGate == nil {
		return nil
	}
	return m.workGate.AuthorizeSend(ctx, id, ladoSendOrigin(message, options))
}

// ladoSendOrigin reads who sent a message from its delivery facts. A named
// sender session wins over every other fact, so a message from another session
// cannot pass as a person's or as a report relay. `ao send` names its session
// whenever it runs inside one (cli/send.go).
func ladoSendOrigin(message string, options ports.MessageDeliveryOptions) ports.SendOrigin {
	switch {
	case options.SenderSessionID != "":
		return ports.SendOriginSession
	case options.AuthoredByUser:
		return ports.SendOriginHuman
	}
	if _, ok := domain.CoordinationDeliveryID(message); ok {
		return ports.SendOriginCoordination
	}
	return ports.SendOriginDaemon
}
