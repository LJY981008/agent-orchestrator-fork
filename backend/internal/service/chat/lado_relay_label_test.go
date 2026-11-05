package chat_test

// LADO fork: the label of a developer's instruction LADO relays to a Chat
// worker (docs/LJY-PRD/TDD.md §6.3). The relay sends with a display name and
// no sender session; the timeline shows the name from the stored message.

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// ladoRelayLabel is domain.LadoRelaySenderLabel, written out so this test
// does not depend on the relay's domain file.
const ladoRelayLabel = "LADO 전달 · 개발자 지시"

func TestRelayWithDisplayNameStoresTheLabelWithoutASenderSession(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	_, err := h.svc.RelaySessionChatTurn(ctx, testSession, "LADO 전달: 개발자 지시\n\n후속 2건을 이어서 하세요.", "",
		ports.MessageDeliveryOptions{SenderDisplayName: "  " + ladoRelayLabel + " "})
	if err != nil {
		t.Fatalf("RelaySessionChatTurn: %v", err)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) >= 1
	})
	msg := snapshot.Messages[0]
	if msg.Origin != domain.MessageOriginAutomation {
		t.Fatalf("origin = %q, want %q", msg.Origin, domain.MessageOriginAutomation)
	}
	if msg.SenderSessionID != "" || msg.SenderProjectID != "" {
		t.Fatalf("sender session = (%q, %q), want none", msg.SenderSessionID, msg.SenderProjectID)
	}
	if msg.SenderDisplayName != ladoRelayLabel {
		t.Fatalf("sender display name = %q, want %q", msg.SenderDisplayName, ladoRelayLabel)
	}
	if msg.AuthoredByUser {
		t.Fatal("a LADO relay was stored as user-authored")
	}
}

// A display name never labels a message from another session: that label
// comes from the session itself, so a session cannot pass itself off as LADO.
func TestRelayFromASessionIgnoresTheDisplayName(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	_, err := h.svc.RelaySessionChatTurn(ctx, testSession, "rebase onto main", "",
		ports.MessageDeliveryOptions{SenderSessionID: "p1-orchestrator-1", SenderDisplayName: ladoRelayLabel})
	if err != nil {
		t.Fatalf("RelaySessionChatTurn: %v", err)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) >= 1
	})
	msg := snapshot.Messages[0]
	if msg.SenderSessionID != "p1-orchestrator-1" {
		t.Fatalf("sender session = %q, want p1-orchestrator-1", msg.SenderSessionID)
	}
	if msg.SenderDisplayName == ladoRelayLabel {
		t.Fatalf("a session's message carries LADO's label %q", msg.SenderDisplayName)
	}
}

func TestRelayWithoutDisplayNameKeepsTheAutomationLabel(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.RelayChatTurn(ctx, testSession, "CI failed on your PR"); err != nil {
		t.Fatalf("RelayChatTurn: %v", err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) >= 1
	})
	if got := snapshot.Messages[0].SenderDisplayName; got != "" {
		t.Fatalf("sender display name = %q, want none", got)
	}
}
