package chat

// LADO fork: the worker gate's hook points for messages that reach a session's
// conversation without passing through the session manager
// (docs/LJY-PRD/TDD.md §6.3). Review conversations stay AO's, and side
// questions only come from people.

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SetWorkGate installs LADO's worker gate. The daemon calls it before serving
// requests; a nil gate keeps upstream behavior.
func (s *Service) SetWorkGate(gate ports.WorkGate) {
	s.workGate = gate
}

func (s *Service) authorizeLadoMessage(ctx context.Context, id domain.SessionID, msg ports.ChatUserMessage) error {
	if s.workGate == nil {
		return nil
	}
	return s.workGate.AuthorizeSend(ctx, id, ladoMessageOrigin(msg))
}

// ladoMessageOrigin reads who sent a message. A named sender session wins, as
// in the session manager: the conversation routes mark every request human and
// carry the CLI's sender separately.
func ladoMessageOrigin(msg ports.ChatUserMessage) ports.SendOrigin {
	if msg.SenderSessionID != "" {
		return ports.SendOriginSession
	}
	switch msg.Origin {
	case domain.MessageOriginHuman:
		return ports.SendOriginHuman
	case domain.MessageOriginDaemon:
		return ports.SendOriginDaemon
	default:
		return ports.SendOriginAutomation
	}
}
