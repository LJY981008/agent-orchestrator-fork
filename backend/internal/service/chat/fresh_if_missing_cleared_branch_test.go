package chat_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

const (
	clearedBranchRootProvider  = "root-provider-thread"
	clearedBranchProvider      = "cleared-provider-thread"
	clearedBranchFreshProvider = "fresh-provider-thread"
	clearedBranchTestID        = "cleared-branch"
	clearedBranchSystemPrompt  = "orchestrator standing instructions"
)

// A controller restarted right after /clear, before the first message, finds
// no native history for the cleared provider conversation. Session Manager
// proves the /clear branch untouched and asks for a fresh conversation in its
// place. The reopened controller must keep answering on the /clear branch,
// with the standing instructions handed to the provider, while the root's
// history stays where it was.
func TestFreshIfMissingRestartAnswersOnClearedBranch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		boundary bool
	}{
		{name: "boundary row", boundary: true},
		{name: "no boundary row", boundary: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t)
			ctx := context.Background()
			now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

			// The session ran on the root provider conversation, then /clear
			// moved it to a new, still empty, provider conversation.
			record, found, err := st.GetSession(ctx, testSession)
			if err != nil || !found {
				t.Fatalf("GetSession: found=%v err=%v", found, err)
			}
			record.Metadata.ProviderConversationID = clearedBranchRootProvider
			record.Metadata.ControllerGeneration = "root-generation"
			if err := st.UpdateSession(ctx, record); err != nil {
				t.Fatalf("seed root provider: %v", err)
			}
			conversation, err := st.CreateConversation(ctx, "cleared-branch-conversation",
				domain.ConversationScopeProject, testProject, testSession, now)
			if err != nil {
				t.Fatalf("CreateConversation: %v", err)
			}
			for i, turn := range []string{"root-turn-1", "root-turn-2"} {
				created, err := st.AppendUserMessage(ctx, conversation.ID, testSession, "root-generation",
					domain.ConversationMessage{
						ID: "root-message-" + turn, Origin: domain.MessageOriginHuman,
						Text: "root prompt " + turn, ClientMessageID: "root-client-" + turn,
					}, turn, now.Add(time.Duration(i)*time.Second))
				if err != nil || !created {
					t.Fatalf("append root prompt %s: created=%v err=%v", turn, created, err)
				}
			}
			before, err := st.ConversationForSession(ctx, testSession)
			if err != nil {
				t.Fatalf("ConversationForSession: %v", err)
			}
			rootID := before.ActiveBranchID
			clearedAt := now.Add(time.Minute)
			if err := st.CreateAndActivateConversationBranch(ctx, testSession, domain.ConversationBranch{
				ID: clearedBranchTestID, ConversationID: conversation.ID, SessionID: testSession,
				ProviderConversationID: clearedBranchProvider, ParentBranchID: rootID,
				ForkAfterSequence: before.LatestSequence, CreatedAt: clearedAt,
				Strategy:        domain.ConversationBranchStrategyNative,
				ProviderScopeID: "cleared-scope", ProviderIDsScoped: true,
			}, "cleared-generation", clearedAt); err != nil {
				t.Fatalf("activate /clear branch: %v", err)
			}
			if tc.boundary {
				if err := st.UpsertActivity(ctx, conversation.ID, "", domain.ConversationActivity{
					ID: "cleared-boundary", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
					Summary: "Context cleared.", Detail: []byte(`{"event":"context.reset","reason":"user_clear"}`),
					ProviderItemID: domain.ConversationClearBoundaryProviderItemID(testSession, clearedBranchTestID),
				}, clearedAt); err != nil {
					t.Fatalf("write /clear boundary: %v", err)
				}
			}
			untouched, err := st.ClearedConversationBranchUntouched(ctx, testSession, conversation.ID,
				clearedBranchTestID, clearedBranchProvider)
			if err != nil || !untouched {
				t.Fatalf("seeded /clear branch untouched = %v, err=%v; want the restart's proof to hold", untouched, err)
			}

			// The provider never persisted the cleared conversation and
			// starts a fresh one in its place.
			fresh := newFakeConversation()
			fresh.providerConversationID = clearedBranchFreshProvider
			fresh.onSend = func(providerTurnID string) {
				sent := fresh.sentMessages()
				answerTurn(fresh, providerTurnID, "reply to "+sent[len(sent)-1].Text, domain.TurnStateCompleted)
			}
			var resumed ports.ChatResumeConfig
			var started ports.ChatStartConfig
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st,
				Reader: fullSnapshotReader(st), PageReader: boundedSnapshotReader(st),
				Drivers: fakeRegistry{driver: fakeDriver{conv: fresh, resumeCfg: &resumed, startCfg: &started}},
				Log:     slog.New(slog.DiscardHandler),
				NewID:   newSequentialID("cleared-restart"),
				Now:     func() time.Time { return clearedAt.Add(time.Minute) },
			})
			t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

			var commitOwner domain.SessionControllerOwner
			ctrl, err := svc.Start(ctx, chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Kind: domain.KindOrchestrator,
				Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
				SystemPrompt:                       clearedBranchSystemPrompt,
				ProviderConversationID:             clearedBranchProvider,
				FreshIfProviderConversationMissing: true,
				HistoryMode:                        ports.ChatHistoryImport,
				ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
					// Session Manager's ControllerReady moves the session and its
					// untouched /clear branch onto the fresh conversation.
					if err := st.ReplaceUnpersistedChatProvider(ctx, testSession,
						clearedBranchProvider, result.ProviderConversationID); err != nil {
						return chatsvc.ControllerCommit{}, err
					}
					moved, _, err := st.GetSession(ctx, testSession)
					if err != nil {
						return chatsvc.ControllerCommit{}, err
					}
					commitOwner = moved.ControllerOwner()
					return chatsvc.ControllerCommit{Conversation: result.Conversation, ControllerOwner: commitOwner}, nil
				},
			})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}

			// The driver resumed (not started) the cleared conversation, with
			// leave to start fresh and with the standing instructions.
			if started.SessionID != "" {
				t.Fatalf("driver Start was called (%+v); a restart must resume", started)
			}
			if resumed.ProviderConversationID != clearedBranchProvider || !resumed.FreshIfMissing || !resumed.HasPriorTurns {
				t.Fatalf("resume config = provider %q fresh=%v prior=%v, want %q fresh prior",
					resumed.ProviderConversationID, resumed.FreshIfMissing, resumed.HasPriorTurns, clearedBranchProvider)
			}
			if resumed.SystemPrompt != clearedBranchSystemPrompt {
				t.Fatalf("resume SystemPrompt = %q, want %q", resumed.SystemPrompt, clearedBranchSystemPrompt)
			}
			if !resumed.ProviderIDsScoped || resumed.ProviderScopeID != "cleared-scope" {
				t.Fatalf("resume provider scope = %q scoped=%v, want the /clear branch scope",
					resumed.ProviderScopeID, resumed.ProviderIDsScoped)
			}
			if ctrl.ProviderConversationID() != clearedBranchFreshProvider {
				t.Fatalf("controller provider = %q, want %q", ctrl.ProviderConversationID(), clearedBranchFreshProvider)
			}

			// The session and the /clear branch now name the fresh
			// conversation; the root keeps its own.
			session, _, err := st.GetSession(ctx, testSession)
			if err != nil {
				t.Fatalf("GetSession: %v", err)
			}
			if session.ControllerOwner() != commitOwner ||
				session.Metadata.ProviderConversationID != clearedBranchFreshProvider ||
				session.Metadata.AgentSessionID != clearedBranchFreshProvider ||
				session.Metadata.ControllerGeneration != ctrl.Generation() {
				t.Fatalf("session owner = %+v, want fresh provider and controller generation %q (commit %+v)",
					session.ControllerOwner(), ctrl.Generation(), commitOwner)
			}
			branch, err := st.ConversationBranch(ctx, conversation.ID, clearedBranchTestID)
			if err != nil {
				t.Fatalf("load /clear branch: %v", err)
			}
			if branch.ProviderConversationID != clearedBranchFreshProvider || branch.ProviderScopeID != "cleared-scope" {
				t.Fatalf("/clear branch = provider %q scope %q, want %q in the same scope",
					branch.ProviderConversationID, branch.ProviderScopeID, clearedBranchFreshProvider)
			}
			root, err := st.ConversationBranch(ctx, conversation.ID, rootID)
			if err != nil {
				t.Fatalf("load root branch: %v", err)
			}
			if root.ProviderConversationID == clearedBranchFreshProvider {
				t.Fatalf("root branch moved to the fresh provider: %+v", root)
			}

			// The next message lands on the /clear branch and gets its answer.
			turn, err := svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after restart", Origin: domain.MessageOriginHuman})
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			done := awaitClearedBranchTurn(t, st, turn.ID)
			if done.BranchID != clearedBranchTestID {
				t.Fatalf("turn branch = %q, want the /clear branch %q", done.BranchID, clearedBranchTestID)
			}
			if sent := fresh.sentTexts(); len(sent) != 1 || sent[0] != "after restart" {
				t.Fatalf("provider got %v, want the one message", sent)
			}
			current, err := st.ConversationForSession(ctx, testSession)
			if err != nil {
				t.Fatalf("ConversationForSession: %v", err)
			}
			if current.ID != conversation.ID || current.ActiveBranchID != clearedBranchTestID {
				t.Fatalf("conversation = %s on branch %s, want %s on %s",
					current.ID, current.ActiveBranchID, conversation.ID, clearedBranchTestID)
			}

			snapshot, err := st.LoadConversationSnapshot(ctx, conversation.ID)
			if err != nil {
				t.Fatalf("LoadConversationSnapshot: %v", err)
			}
			if snapshot.ActiveBranch.ID != clearedBranchTestID {
				t.Fatalf("snapshot branch = %q, want %q", snapshot.ActiveBranch.ID, clearedBranchTestID)
			}
			texts := map[string]domain.ConversationMessage{}
			for _, message := range snapshot.Messages {
				texts[message.Text] = message
			}
			for _, want := range []string{"root prompt root-turn-1", "root prompt root-turn-2"} {
				if _, ok := texts[want]; !ok {
					t.Fatalf("root message %q is gone from the timeline: %+v", want, snapshot.Messages)
				}
			}
			reply, ok := texts["reply to after restart"]
			if !ok || reply.Role != domain.MessageRoleAssistant || reply.TurnID != turn.ID {
				t.Fatalf("answer = %+v (found=%v), want the assistant reply on turn %s", reply, ok, turn.ID)
			}
			if prompt, ok := texts["after restart"]; !ok || prompt.TurnID != turn.ID {
				t.Fatalf("prompt = %+v (found=%v), want it on turn %s", prompt, ok, turn.ID)
			}
		})
	}
}

func newSequentialID(prefix string) func() string {
	var next atomic.Int64
	return func() string { return fmt.Sprintf("%s-%03d", prefix, next.Add(1)) }
}

func awaitClearedBranchTurn(t *testing.T, st interface {
	TurnByID(context.Context, string) (domain.ConversationTurn, error)
}, turnID string) domain.ConversationTurn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		turn, err := st.TurnByID(context.Background(), turnID)
		if err == nil && turn.State == domain.TurnStateCompleted {
			return turn
		}
		time.Sleep(10 * time.Millisecond)
	}
	turn, err := st.TurnByID(context.Background(), turnID)
	t.Fatalf("turn %s = %+v (err=%v), want completed", turnID, turn, err)
	return domain.ConversationTurn{}
}
