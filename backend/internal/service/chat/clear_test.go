package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// clearBoundaries returns the /clear divider rows in a snapshot.
func clearBoundaries(t *testing.T, activities []domain.ConversationActivity) []domain.ConversationActivity {
	t.Helper()
	var out []domain.ConversationActivity
	for _, activity := range activities {
		if activity.Kind != domain.ActivityKindSystem || len(activity.Detail) == 0 {
			continue
		}
		var detail map[string]string
		if err := json.Unmarshal(activity.Detail, &detail); err != nil {
			continue
		}
		if detail["event"] == "context.reset" && detail["reason"] == chatsvc.ContextResetUserClearReason {
			out = append(out, activity)
		}
	}
	return out
}

func messageSequence(t *testing.T, snapshot store.ConversationSnapshot, text string) int64 {
	t.Helper()
	for _, message := range snapshot.Messages {
		if message.Text == text {
			return message.Sequence
		}
	}
	t.Fatalf("snapshot has no message %q: %+v", text, snapshot.Messages)
	return 0
}

func TestClearContextStartsFreshProviderBoundary(t *testing.T) {
	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	completeTurn(t, h, "A", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })
	before, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}

	result, err := h.svc.ClearContext(ctx, testSession)
	if err != nil {
		t.Fatalf("ClearContext: %v", err)
	}
	if !result.Cleared || result.ActiveBranchID == "" || result.ActiveBranchID == before.ActiveBranchID {
		t.Fatalf("ClearContext result = %+v, previous branch %q", result, before.ActiveBranchID)
	}

	driver.mu.Lock()
	startCalls := driver.startCalls
	startConfigs := append([]ports.ChatStartConfig(nil), driver.startConfigs...)
	resumes := len(driver.resumeCalls)
	driver.mu.Unlock()
	if startCalls != 2 || resumes != 0 {
		t.Fatalf("driver starts=%d resumes=%d, want one fresh Start", startCalls, resumes)
	}
	fresh := startConfigs[1]
	if fresh.ProviderScopeID == "" || !fresh.ProviderIDsScoped || fresh.SystemPrompt != "preserved prompt" {
		t.Fatalf("fresh Start config = %+v", fresh)
	}

	branch, err := h.st.ConversationBranch(ctx, h.ctrl.ConversationID(), result.ActiveBranchID)
	if err != nil {
		t.Fatalf("ConversationBranch: %v", err)
	}
	if !branch.Active || branch.ReplacedTurnID != "" || branch.ParentBranchID != before.ActiveBranchID ||
		branch.ForkAfterSequence != before.LatestSequence || branch.ProviderScopeID != fresh.ProviderScopeID ||
		branch.ProviderConversationID != "thread-fresh" {
		t.Fatalf("clear boundary branch = %+v, before=%+v", branch, before)
	}
	record, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	if record.Metadata.ProviderConversationID != "thread-fresh" {
		t.Fatalf("session provider conversation = %q, want thread-fresh", record.Metadata.ProviderConversationID)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(clearBoundaries(t, s.Activities)) == 1
	})
	requireMessageTexts(t, snapshot.Messages, []string{"A", "reply to A"})
	boundary := clearBoundaries(t, snapshot.Activities)[0]
	if boundary.Sequence <= before.LatestSequence ||
		boundary.ProviderItemID != "ao-context-reset:"+string(testSession)+":"+result.ActiveBranchID {
		t.Fatalf("clear boundary activity = %+v", boundary)
	}

	// The first message after /clear lands after the divider and reaches the
	// fresh provider conversation, not the old one.
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "B", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("Send after clear: %v", err)
	}
	snapshot = h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 3 })
	if seq := messageSequence(t, snapshot, "B"); seq <= boundary.Sequence {
		t.Fatalf("first message after clear sequence = %d, boundary = %d", seq, boundary.Sequence)
	}
	if sent := driver.fresh.sentTexts(); len(sent) != 1 || sent[0] != "B" {
		t.Fatalf("fresh provider sends = %v", sent)
	}
	if sent := h.conv.sentTexts(); len(sent) != 1 {
		t.Fatalf("source provider sends after clear = %v", sent)
	}
}

func TestClearContextWithoutHistoryChangesNothing(t *testing.T) {
	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	before, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	result, err := h.svc.ClearContext(ctx, testSession)
	if err != nil {
		t.Fatalf("ClearContext: %v", err)
	}
	if result.Cleared || result.ActiveBranchID != before.ActiveBranchID {
		t.Fatalf("empty clear result = %+v, want unchanged branch %q", result, before.ActiveBranchID)
	}
	driver.mu.Lock()
	startCalls := driver.startCalls
	driver.mu.Unlock()
	if startCalls != 1 {
		t.Fatalf("empty clear started a provider; starts=%d", startCalls)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "still works"}); err != nil {
		t.Fatalf("Send after empty clear: %v", err)
	}
}

func TestClearContextRefusesRunningTurnAndQueuedWork(t *testing.T) {
	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "running", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// A second message waits in the queue behind the running turn.
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "queued", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("Send queued: %v", err)
	}
	before, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	beforeRecord, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if _, err := h.svc.ClearContext(ctx, testSession); !errors.Is(err, chatsvc.ErrTurnRunning) {
		t.Fatalf("ClearContext busy error = %v, want ErrTurnRunning", err)
	}
	after, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil || after.ActiveBranchID != before.ActiveBranchID {
		t.Fatalf("active branch after busy clear = %q, want %q, err=%v", after.ActiveBranchID, before.ActiveBranchID, err)
	}
	afterRecord, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if afterRecord.Metadata.ProviderConversationID != beforeRecord.Metadata.ProviderConversationID ||
		afterRecord.Metadata.ControllerGeneration != beforeRecord.Metadata.ControllerGeneration {
		t.Fatalf("session metadata changed by busy clear: %+v -> %+v", beforeRecord.Metadata, afterRecord.Metadata)
	}
	driver.mu.Lock()
	startCalls := driver.startCalls
	driver.mu.Unlock()
	if startCalls != 1 {
		t.Fatalf("busy clear started a provider; starts=%d", startCalls)
	}

}

func TestClearContextStartFailureRestoresSource(t *testing.T) {
	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	completeTurn(t, h, "A", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })
	before, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	driver.mu.Lock()
	driver.startErr = errors.New("provider unavailable")
	driver.mu.Unlock()

	if _, err := h.svc.ClearContext(ctx, testSession); err == nil {
		t.Fatal("ClearContext succeeded after Start failure")
	}
	after, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil || after.ActiveBranchID != before.ActiveBranchID {
		t.Fatalf("active branch after failed clear = %q, want %q, err=%v", after.ActiveBranchID, before.ActiveBranchID, err)
	}
	driver.mu.Lock()
	resumed := append([]ports.ChatResumeConfig(nil), driver.resumeCalls...)
	driver.mu.Unlock()
	if len(resumed) != 1 || resumed[0].ProviderConversationID != "thread-1" {
		t.Fatalf("source restore resumes = %+v", resumed)
	}
	snapshot := h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return true })
	if got := clearBoundaries(t, snapshot.Activities); len(got) != 0 {
		t.Fatalf("failed clear wrote a boundary: %+v", got)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "source reopened"}); err != nil {
		t.Fatalf("Send after failed clear: %v", err)
	}
	if sent := driver.resumed["thread-1"].sentTexts(); len(sent) != 1 || sent[0] != "source reopened" {
		t.Fatalf("restored source sends = %v", sent)
	}
}

// failClearBoundaryStore refuses the /clear divider row only.
type failClearBoundaryStore struct {
	*store.Store
}

func (s failClearBoundaryStore) UpsertActivity(
	ctx context.Context, conversationID, providerTurnID string, activity domain.ConversationActivity, now time.Time,
) error {
	if len(activity.Detail) > 0 {
		var detail map[string]string
		if json.Unmarshal(activity.Detail, &detail) == nil && detail["reason"] == chatsvc.ContextResetUserClearReason {
			return errors.New("disk full")
		}
	}
	return s.Store.UpsertActivity(ctx, conversationID, providerTurnID, activity, now)
}

func TestClearContextSucceedsWhenBoundaryRowFails(t *testing.T) {
	h, _, _ := newEditHarnessWithStore(t, false, func(st *store.Store) chatsvc.Store {
		return failClearBoundaryStore{Store: st}
	})
	ctx := context.Background()
	completeTurn(t, h, "A", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })
	result, err := h.svc.ClearContext(ctx, testSession)
	if err != nil || !result.Cleared {
		t.Fatalf("ClearContext = %+v, %v; want success without the divider", result, err)
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil || conversation.ActiveBranchID != result.ActiveBranchID {
		t.Fatalf("active branch = %q, want %q, err=%v", conversation.ActiveBranchID, result.ActiveBranchID, err)
	}
}

func TestClearContextBoundsMessageEditing(t *testing.T) {
	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	first := completeTurn(t, h, "A", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })
	if _, err := h.svc.ClearContext(ctx, testSession); err != nil {
		t.Fatalf("ClearContext: %v", err)
	}

	// The agent no longer remembers the pre-clear prompt, so editing it would
	// fork history the current provider does not own.
	if _, err := h.svc.EditMessage(ctx, testSession, first, ports.ChatUserMessage{
		Text: "A edited", Origin: domain.MessageOriginHuman,
	}); !errors.Is(err, chatsvc.ErrEditTurnInvalid) {
		t.Fatalf("edit before clear error = %v, want ErrEditTurnInvalid", err)
	}

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "B", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatalf("Send after clear: %v", err)
	}
	cleared := driver.fresh
	cleared.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-101"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-101", TurnState: domain.TurnStateCompleted},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, candidate := range s.Turns {
			if candidate.ID == turn.ID {
				return candidate.State.Terminal()
			}
		}
		return false
	})
	result, err := h.svc.EditMessage(ctx, testSession, turn.ID, ports.ChatUserMessage{
		Text: "B edited", ClientMessageID: "edit-b", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("edit after clear: %v", err)
	}
	driver.mu.Lock()
	startCalls := driver.startCalls
	resumes := len(driver.resumeCalls)
	driver.mu.Unlock()
	if startCalls != 3 || resumes != 0 {
		t.Fatalf("edit after clear starts=%d resumes=%d, want a fresh Start", startCalls, resumes)
	}
	if sent := driver.fresh.sentTexts(); len(sent) != 1 || sent[0] != "B edited" {
		t.Fatalf("edited provider sends = %v", sent)
	}
	if result.ActiveBranchID == "" {
		t.Fatalf("edit after clear result = %+v", result)
	}
}

func TestClearContextWakesHibernatedSession(t *testing.T) {
	h, old := settledHibernationHarness(t, domain.TurnStateCompleted)
	ctx := context.Background()
	if hibernated, err := h.svc.HibernateChat(ctx, testSession); err != nil || !hibernated {
		t.Fatalf("HibernateChat = %v, %v", hibernated, err)
	}
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	cleared := newFakeConversation()
	cleared.providerConversationID = "thread-cleared"
	cleared.turnSeq = 50
	var starts atomic.Int32
	var nextID atomic.Int32
	wakeService := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
		Drivers: fakeRegistry{driver: fakeDriver{
			conv: resumed,
			start: func(ports.ChatStartConfig) (ports.ChatConversation, error) {
				starts.Add(1)
				return cleared, nil
			},
		}},
		Activity: h.activity, Log: slog.New(slog.DiscardHandler), Now: h.now,
		NewID: func() string { return fmt.Sprintf("clear-wake-%d", nextID.Add(1)) },
	})
	t.Cleanup(func() { _ = wakeService.Stop(context.Background(), testSession) })
	wakeService.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
		rec, found, err := h.st.GetSession(ctx, id)
		if err != nil || !found || rec.HibernatedAt == nil {
			return fmt.Errorf("read hibernated session: found=%v marker=%v err=%w", found, rec.HibernatedAt, err)
		}
		if ok, err := h.st.SetSessionHibernated(ctx, id, rec.Revision, nil); err != nil || !ok {
			return fmt.Errorf("clear hibernation: applied=%v err=%w", ok, err)
		}
		_, err = wakeService.Start(ctx, chatsvc.StartConfig{
			SessionID: id, ProjectID: testProject, Harness: domain.HarnessCodex,
			WorkspacePath: t.TempDir(), ProviderConversationID: rec.Metadata.ProviderConversationID,
		})
		return err
	})

	result, err := wakeService.ClearContext(ctx, testSession)
	if err != nil || !result.Cleared {
		t.Fatalf("ClearContext on hibernated session = %+v, %v", result, err)
	}
	if starts.Load() != 1 {
		t.Fatalf("fresh starts = %d, want 1", starts.Load())
	}
	rec, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found || rec.HibernatedAt != nil {
		t.Fatalf("session after clear: found=%v marker=%v err=%v", found, rec.HibernatedAt, err)
	}
	if rec.Metadata.ProviderConversationID != "thread-cleared" || rec.Metadata.ProviderConversationID == old.ProviderConversationID() {
		t.Fatalf("provider conversation after clear = %q", rec.Metadata.ProviderConversationID)
	}
	if _, err := wakeService.Send(ctx, testSession, ports.ChatUserMessage{Text: "after clear", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("Send after clear: %v", err)
	}
	if sent := cleared.sentTexts(); len(sent) != 1 || sent[0] != "after clear" {
		t.Fatalf("cleared provider sends = %v", sent)
	}
}
