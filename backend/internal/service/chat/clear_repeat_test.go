package chat_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// Every branch swap (/clear or message edit) rotates the browser capability
// under the session owner fence. After the first swap the service must expect
// the owner that branch activation wrote, or the next swap fails the CAS with
// "session controller ownership changed" and leaves the session stopped.

func TestClearContextThenEditKeepsTheBranchOwnerCurrent(t *testing.T) {
	h := newMemoHarness(t, memoHarnessOptions{
		fenceOwner: true,
		respond:    replyWithMemo("<ao-project-memo>memo</ao-project-memo>"),
	})
	ctx := context.Background()
	first := h.send(t, "A", domain.MessageOriginHuman)
	h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)
	if result, err := h.svc.ClearContext(ctx, testSession); err != nil || !result.Cleared {
		t.Fatalf("ClearContext = %+v, %v", result, err)
	}
	afterClear := h.send(t, "B", domain.MessageOriginHuman)
	h.awaitTurnState(t, afterClear.ID, domain.TurnStateCompleted)

	edited := h.editAndAwait(t, afterClear.ID, "B edited")
	h.requireBranchOwnerFenceHolds(t, edited)

	next := h.send(t, "C", domain.MessageOriginHuman)
	h.awaitTurnState(t, next.ID, domain.TurnStateCompleted)
	h.requireReply(t, "reply to C")
}

func TestBranchOwnerStaysCurrentAcrossTwoEdits(t *testing.T) {
	h := newMemoHarness(t, memoHarnessOptions{
		fenceOwner: true,
		respond:    replyWithMemo("<ao-project-memo>memo</ao-project-memo>"),
	})
	// Editing the first turn needs no replay of earlier context, so the
	// memo driver's capabilities are enough for both swaps.
	first := h.send(t, "A", domain.MessageOriginHuman)
	h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)

	once := h.editAndAwait(t, first.ID, "A edited once")
	twice := h.editAndAwait(t, once.Turn.ID, "A edited twice")
	if twice.ActiveBranchID == once.ActiveBranchID {
		t.Fatalf("second edit stayed on branch %q", once.ActiveBranchID)
	}
	h.requireBranchOwnerFenceHolds(t, twice)

	next := h.send(t, "C", domain.MessageOriginHuman)
	h.awaitTurnState(t, next.ID, domain.TurnStateCompleted)
	h.requireReply(t, "reply to C")
}

// editAndAwait edits a settled turn and waits for the replacement turn's answer.
func (h *memoHarness) editAndAwait(t *testing.T, turnID, text string) chatsvc.EditMessageResult {
	t.Helper()
	result, err := h.svc.EditMessage(context.Background(), testSession, turnID, ports.ChatUserMessage{
		Text: text, Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("EditMessage %q: %v", text, err)
	}
	if result.Turn.ID == "" || result.ActiveBranchID == "" || result.ActiveBranchID == result.SourceBranchID {
		t.Fatalf("EditMessage %q = %+v", text, result)
	}
	h.awaitTurnState(t, result.Turn.ID, domain.TurnStateCompleted)
	h.requireReply(t, "reply to "+text)
	return result
}

// requireBranchOwnerFenceHolds checks the stored owner after a swap: it is the
// owner branch activation writes, and the capability CAS still accepts it.
func (h *memoHarness) requireBranchOwnerFenceHolds(t *testing.T, edit chatsvc.EditMessageResult) {
	t.Helper()
	ctx := context.Background()
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	if conversation.ActiveBranchID != edit.ActiveBranchID {
		t.Fatalf("active branch = %q, want %q", conversation.ActiveBranchID, edit.ActiveBranchID)
	}
	branch, err := h.st.ConversationBranch(ctx, conversation.ID, edit.ActiveBranchID)
	if err != nil {
		t.Fatalf("ConversationBranch: %v", err)
	}
	record, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	owner := record.ControllerOwner()
	if owner.ProviderConversationID != branch.ProviderConversationID ||
		owner.AgentSessionID != branch.ProviderConversationID || owner.AgentSessionIDLaunchID != "" {
		t.Fatalf("stored owner = %+v, want native identity of branch %+v", owner, branch)
	}
	if record.Metadata.BrowserCapabilityVerifier != "verifier" {
		t.Fatalf("browser capability verifier = %q, want rotated", record.Metadata.BrowserCapabilityVerifier)
	}
	applied, err := h.st.UpdateBrowserCapabilityVerifier(ctx, testSession, owner, "verifier")
	if err != nil || !applied {
		t.Fatalf("UpdateBrowserCapabilityVerifier(stored owner) applied=%v err=%v", applied, err)
	}
}

// requireReply waits for an assistant message with text in the timeline.
func (h *memoHarness) requireReply(t *testing.T, text string) {
	t.Helper()
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, message := range s.Messages {
			if message.Role == domain.MessageRoleAssistant && message.Text == text {
				return true
			}
		}
		return false
	})
}
