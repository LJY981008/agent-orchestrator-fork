package sessionmanager

// LADO fork: the worker gate's hook points in the session manager
// (docs/LJY-PRD/TDD.md §6), and the LADO section of an orchestrator's standing
// instructions (§5.1).

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

// LadoPromptSection returns LADO's section of an orchestrator's standing
// instructions for a project, or "" when the project is not in LADO mode.
type LadoPromptSection func(ctx context.Context, projectID domain.ProjectID) (string, error)

// SetLadoPrompt installs LADO's orchestrator section. The daemon calls it
// before any session starts or restores; nil keeps upstream behavior.
func (m *Manager) SetLadoPrompt(section LadoPromptSection) {
	m.ladoPrompt = section
}

// ladoPromptSection is the LADO section for an orchestrator of a project.
// Workers get none: LADO gives them the approved plan instead.
func (m *Manager) ladoPromptSection(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID) (string, error) {
	if m.ladoPrompt == nil || kind != domain.KindOrchestrator || projectID == "" {
		return "", nil
	}
	return m.ladoPrompt(ctx, projectID)
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
