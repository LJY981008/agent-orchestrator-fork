package chat

// LADO fork: the worker gate's hook points for messages that reach a session's
// conversation without passing through the session manager
// (docs/LJY-PRD/TDD.md §6.3), and for the approval of a LADO execution's turns
// (§6.4). Review conversations stay AO's, and side questions only come from
// people.

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

// clampLadoTurnSettings lets the gate cap the approval of a session's next
// turns before the controller records them.
func (s *Service) clampLadoTurnSettings(ctx context.Context, id domain.SessionID, settings domain.ConversationSettings) (domain.ConversationSettings, error) {
	if s.workGate == nil {
		return settings, nil
	}
	turn, err := s.workGate.ClampTurnSettings(ctx, id, ports.ChatTurnSettings{
		Model: settings.Model, Effort: settings.ReasoningEffort, Approval: settings.ApprovalMode,
	})
	if err != nil {
		return domain.ConversationSettings{}, err
	}
	settings.Model, settings.ReasoningEffort, settings.ApprovalMode = turn.Model, turn.Effort, turn.Approval
	return settings, nil
}

// authorizeLadoConfigOption asks the gate about a config option that selects
// an approval mode, before the provider applies it.
func (s *Service) authorizeLadoConfigOption(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, configID string, value ports.ChatConfigOptionValue) error {
	if s.workGate == nil {
		return nil
	}
	mode := ladoConfigMode(harness, configID, value)
	if mode == "" {
		return nil
	}
	return s.workGate.AuthorizeConfigMode(ctx, id, mode)
}

// ladoConfigMode is the approval mode a config option value selects, or ""
// when the option is no approval control. It reads the same mapping
// settingsFromConfigOptions applies to the provider's answer.
func ladoConfigMode(harness domain.AgentHarness, configID string, value ports.ChatConfigOptionValue) domain.PermissionMode {
	probe := []ports.ChatConfigOption{{ID: configID, Current: value, Choices: []ports.ChatConfigOptionChoice{{Value: value.Select}}}}
	for _, option := range permissionConfigOptions(harness, probe) {
		for _, choice := range option.Choices {
			if choice.PermissionMode != "" {
				return choice.PermissionMode
			}
		}
	}
	return ""
}
