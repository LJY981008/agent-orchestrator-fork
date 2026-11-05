package daemon

// LADO fork: wiring of the relay of a developer's instruction from a LADO
// project's orchestrator to a running worker (issue #49,
// docs/LJY-PRD/adr/0010-worker-relay.md). The worker gate is unchanged: the
// relay delivers through the session manager as the daemon, with no sender
// session, so an orchestrator's own ao send stays LADO_SEND_REFUSED while
// the instruction LADO checked goes through.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladorelay"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var (
	_ ladorelay.Store              = (*sqlite.Store)(nil)
	_ ladoRelayConversationStore   = (*sqlite.Store)(nil)
	_ controllers.LadoRelayService = (*ladorelay.Service)(nil)
	_ ladoRelaySender              = (*sessionmanager.Manager)(nil)
)

// ladoRelaySender delivers a message with the caller's delivery facts: the
// session manager.
type ladoRelaySender interface {
	SendWithOptions(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) error
}

// wireLadoRelay builds the relay service. sender is what LADO's other
// services message sessions through; without a session manager that can
// deliver with options, relays are recorded as failed.
func wireLadoRelay(store *sqlite.Store, blobs *ladoblob.Store, sender ladoMessageSender, log *slog.Logger) *ladorelay.Service {
	deps := ladorelay.Deps{
		Store:         store,
		Conversations: ladoRelayConversations{store: store},
		Plans:         ladoApprovedPlans{store: store, blobs: blobs},
		Log:           log,
	}
	if relaySender, ok := sender.(ladoRelaySender); ok && relaySender != nil {
		deps.Messenger = ladoRelayMessenger{sender: relaySender}
	}
	return ladorelay.New(deps)
}

// ladoRelayMessenger delivers a relay as the daemon: no sender session and
// not authored by the developer, so the worker gate lets it through and
// stores nothing as the developer's. A Chat worker shows it under LADO's
// label.
type ladoRelayMessenger struct {
	sender ladoRelaySender
}

func (m ladoRelayMessenger) Relay(ctx context.Context, worker domain.SessionID, message string) error {
	return m.sender.SendWithOptions(ctx, worker, message, nil, ports.MessageDeliveryOptions{
		SenderDisplayName: domain.LadoRelaySenderLabel,
	})
}

// ladoRelayConversationStore reads a project's orchestrator conversation.
type ladoRelayConversationStore interface {
	ProjectConversation(ctx context.Context, project domain.ProjectID) (domain.ConversationRecord, error)
	ConversationMessages(ctx context.Context, conversationID string) ([]domain.ConversationMessage, error)
}

// ladoRelayConversations reads the developer's messages of a project's
// orchestrator conversation: role user, origin human, rows before a /clear
// boundary included (a /clear keeps every row).
type ladoRelayConversations struct {
	store ladoRelayConversationStore
}

func (c ladoRelayConversations) ProjectMessages(ctx context.Context, project domain.ProjectID) ([]domain.ConversationMessage, error) {
	conversation, err := c.store.ProjectConversation(ctx, project)
	if errors.Is(err, domain.ErrNoConversation) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the orchestrator conversation of project %s: %w", project, err)
	}
	messages, err := c.store.ConversationMessages(ctx, conversation.ID)
	if err != nil {
		return nil, fmt.Errorf("read the messages of conversation %s: %w", conversation.ID, err)
	}
	developer := make([]domain.ConversationMessage, 0, len(messages))
	for _, m := range messages {
		if m.Role == domain.MessageRoleUser && m.Origin == domain.MessageOriginHuman {
			developer = append(developer, m)
		}
	}
	return developer, nil
}
