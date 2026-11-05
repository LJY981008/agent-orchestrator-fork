package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const (
	clearedRootProvider    = "root-thread"
	clearedBranchProvider  = "cleared-thread"
	clearedFreshProvider   = "fresh-thread"
	clearedBranchID        = "clear-branch"
	clearedBranchGenerator = "clear-generation"
)

type clearedBranchFixture struct {
	st           *sqlite.Store
	session      domain.SessionRecord
	conversation domain.ConversationRecord
	rootID       string
	branchID     string
	now          time.Time
}

// seedClearedBranch builds the state a Chat /clear leaves: a root with two
// answered prompts, then an active child branch bound to the cleared provider
// conversation, the way ClearContext publishes it. The boundary row is written
// only when boundary is set, since ClearContext tolerates a failed write.
func seedClearedBranch(t *testing.T, scope domain.ConversationScope, boundary bool) clearedBranchFixture {
	t.Helper()
	ctx := context.Background()
	st := newTestStore(t)
	seedProject(t, st, "cleared")
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	rec := sampleRecord("cleared")
	if scope == domain.ConversationScopeProject {
		rec.Kind = domain.KindOrchestrator
	}
	rec.Mode = domain.SessionModeChat
	rec.Metadata.ProviderConversationID = clearedRootProvider
	rec.Metadata.ControllerGeneration = "root-generation"
	session, err := st.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := st.CreateConversation(ctx, "cleared-conversation", scope, session.ProjectID, session.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	for i, turn := range []string{"root-turn-1", "root-turn-2"} {
		created, err := st.AppendUserMessage(ctx, conversation.ID, session.ID, "root-generation", domain.ConversationMessage{
			ID: "root-message-" + turn, Origin: domain.MessageOriginHuman, Text: "root prompt " + turn,
			ClientMessageID: "root-client-" + turn,
		}, turn, now.Add(time.Duration(i)*time.Second))
		if err != nil || !created {
			t.Fatalf("append root prompt %s: created=%v err=%v", turn, created, err)
		}
	}
	before, err := st.ConversationForSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	clearedAt := now.Add(time.Minute)
	if err := st.CreateAndActivateConversationBranch(ctx, session.ID, domain.ConversationBranch{
		ID: clearedBranchID, ConversationID: conversation.ID, SessionID: session.ID,
		ProviderConversationID: clearedBranchProvider, ParentBranchID: before.ActiveBranchID,
		ForkAfterSequence: before.LatestSequence, CreatedAt: clearedAt,
		Strategy:        domain.ConversationBranchStrategyNative,
		ProviderScopeID: "clear-scope", ProviderIDsScoped: true,
	}, clearedBranchGenerator, clearedAt); err != nil {
		t.Fatal(err)
	}
	if boundary {
		if err := st.UpsertActivity(ctx, conversation.ID, "", domain.ConversationActivity{
			ID: "clear-boundary", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
			Summary: "Context cleared.", Detail: []byte(`{"event":"context.reset","reason":"user_clear"}`),
			ProviderItemID: domain.ConversationClearBoundaryProviderItemID(session.ID, clearedBranchID),
		}, clearedAt); err != nil {
			t.Fatal(err)
		}
	}
	return clearedBranchFixture{
		st: st, session: session, conversation: conversation,
		rootID: before.ActiveBranchID, branchID: clearedBranchID, now: clearedAt,
	}
}

func (f clearedBranchFixture) untouched(t *testing.T, session domain.SessionID, branchID string) bool {
	t.Helper()
	untouched, err := f.st.ClearedConversationBranchUntouched(context.Background(),
		session, f.conversation.ID, branchID, clearedBranchProvider)
	if err != nil {
		t.Fatal(err)
	}
	return untouched
}

func (f clearedBranchFixture) branch(t *testing.T, id string) domain.ConversationBranch {
	t.Helper()
	branch, err := f.st.ConversationBranch(context.Background(), f.conversation.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return branch
}

func (f clearedBranchFixture) currentSession(t *testing.T, id domain.SessionID) domain.SessionRecord {
	t.Helper()
	rec, _, err := f.st.GetSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// requireUnchanged checks that a refused swap left the session, both branches
// and the conversation's timeline where they were.
func (f clearedBranchFixture) requireUnchanged(
	t *testing.T,
	session domain.SessionRecord,
	owner domain.SessionID,
	root, branch domain.ConversationBranch,
	latest int64,
) {
	t.Helper()
	ctx := context.Background()
	current := f.currentSession(t, session.ID)
	if current.Metadata.ProviderConversationID != session.Metadata.ProviderConversationID ||
		current.Metadata.AgentSessionID != session.Metadata.AgentSessionID ||
		current.Metadata.AgentSessionIDLaunchID != session.Metadata.AgentSessionIDLaunchID {
		t.Fatalf("refused swap moved the session: before=%+v after=%+v", session.ControllerOwner(), current.ControllerOwner())
	}
	if after := f.branch(t, root.ID); after.ProviderConversationID != root.ProviderConversationID {
		t.Fatalf("refused swap moved the root: before=%+v after=%+v", root, after)
	}
	if after := f.branch(t, branch.ID); after.ProviderConversationID != branch.ProviderConversationID ||
		after.ProviderScopeID != branch.ProviderScopeID {
		t.Fatalf("refused swap moved the branch: before=%+v after=%+v", branch, after)
	}
	conv, err := f.st.ConversationForSession(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if conv.ID != f.conversation.ID || conv.LatestSequence != latest {
		t.Fatalf("refused swap changed the timeline: latest=%d want %d", conv.LatestSequence, latest)
	}
}

// An empty /clear branch, with or without its boundary row, is proven
// untouched and moves to the fresh provider conversation together with the
// session's provider id and agent_session_id.
func TestReplaceUnpersistedChatProviderMovesUntouchedClearedBranch(t *testing.T) {
	cases := []struct {
		name     string
		scope    domain.ConversationScope
		boundary bool
	}{
		{name: "worker boundary only", scope: domain.ConversationScopeSession, boundary: true},
		{name: "orchestrator boundary only", scope: domain.ConversationScopeProject, boundary: true},
		{name: "worker no boundary", scope: domain.ConversationScopeSession},
		{name: "orchestrator no boundary", scope: domain.ConversationScopeProject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := seedClearedBranch(t, tc.scope, tc.boundary)
			ctx := context.Background()
			activated := f.currentSession(t, f.session.ID)
			if activated.Metadata.AgentSessionID != clearedBranchProvider {
				t.Fatalf("branch activation did not reserve agent_session_id: %+v", activated.ControllerOwner())
			}
			root := f.branch(t, f.rootID)
			before := f.branch(t, f.branchID)
			if !f.untouched(t, f.session.ID, f.branchID) {
				t.Fatal("empty /clear branch was not proven untouched")
			}

			if err := f.st.ReplaceUnpersistedChatProvider(ctx, f.session.ID, clearedBranchProvider, clearedFreshProvider); err != nil {
				t.Fatalf("replace cleared branch provider: %v", err)
			}

			after := f.branch(t, f.branchID)
			if after.ProviderConversationID != clearedFreshProvider || after.ProviderScopeID != before.ProviderScopeID {
				t.Fatalf("cleared branch not rebound: before=%+v after=%+v", before, after)
			}
			if kept := f.branch(t, f.rootID); kept.ProviderConversationID != root.ProviderConversationID {
				t.Fatalf("swap moved the root: before=%+v after=%+v", root, kept)
			}
			current := f.currentSession(t, f.session.ID)
			if current.Metadata.ProviderConversationID != clearedFreshProvider ||
				current.Metadata.AgentSessionID != clearedFreshProvider ||
				current.Metadata.AgentSessionIDLaunchID != "" {
				t.Fatalf("session owner not moved: %+v", current.ControllerOwner())
			}
			if current.Metadata.ControllerGeneration != clearedBranchGenerator || current.Mode != domain.SessionModeChat {
				t.Fatalf("swap changed the controller generation: %+v", current.ControllerOwner())
			}
			conv, err := f.st.ConversationForSession(ctx, f.session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if conv.ActiveBranchID != f.branchID {
				t.Fatalf("swap moved the conversation head: %q", conv.ActiveBranchID)
			}
			// Nothing on the old id remains to prove, so the check is now false.
			if f.untouched(t, f.session.ID, f.branchID) {
				t.Fatal("proof still matched the replaced provider id")
			}
		})
	}
}

// Anything recorded on the /clear branch after the boundary, an edit branch,
// or another current owner refuses both the proof and the swap, and the swap
// changes nothing.
func TestReplaceUnpersistedChatProviderRefusesTouchedClearedBranch(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, f *clearedBranchFixture) (domain.SessionID, string)
	}{
		{name: "user message after boundary", mutate: func(t *testing.T, f *clearedBranchFixture) (domain.SessionID, string) {
			created, err := f.st.AppendUserMessage(context.Background(), f.conversation.ID, f.session.ID, clearedBranchGenerator,
				domain.ConversationMessage{
					ID: "cleared-message", Origin: domain.MessageOriginHuman, Text: "after clear",
					ClientMessageID: "cleared-client",
				}, "cleared-turn", f.now.Add(time.Second))
			if err != nil || !created {
				t.Fatalf("append after clear: created=%v err=%v", created, err)
			}
			return f.session.ID, f.branchID
		}},
		{name: "activity after boundary", mutate: func(t *testing.T, f *clearedBranchFixture) (domain.SessionID, string) {
			if err := f.st.UpsertActivity(context.Background(), f.conversation.ID, "", domain.ConversationActivity{
				ID: "cleared-activity", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
				Summary: "after clear", ProviderItemID: "provider-item-after-clear",
			}, f.now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			return f.session.ID, f.branchID
		}},
		{name: "provider turn after boundary", mutate: func(t *testing.T, f *clearedBranchFixture) (domain.SessionID, string) {
			if err := f.st.AdoptProviderTurn(context.Background(), f.conversation.ID, f.session.ID,
				clearedBranchGenerator, "cleared-provider-turn", "provider-turn-after-clear", f.now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			return f.session.ID, f.branchID
		}},
		{name: "edit branch", mutate: func(t *testing.T, f *clearedBranchFixture) (domain.SessionID, string) {
			// An edit replaces root-turn-2 on a child of the root. It is as
			// empty as a /clear branch but is never a fresh start.
			conv, err := f.st.ConversationForSession(context.Background(), f.session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.st.CreateAndActivateConversationBranch(context.Background(), f.session.ID, domain.ConversationBranch{
				ID: "edit-branch", ConversationID: f.conversation.ID, SessionID: f.session.ID,
				ProviderConversationID: clearedBranchProvider, ParentBranchID: f.rootID,
				ForkAfterTurnID: "root-turn-1", ReplacedTurnID: "root-turn-2",
				ForkAfterSequence: conv.LatestSequence, CreatedAt: f.now.Add(time.Second),
				Strategy:        domain.ConversationBranchStrategyNative,
				ProviderScopeID: "edit-scope", ProviderIDsScoped: true,
			}, "edit-generation", f.now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			return f.session.ID, "edit-branch"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := seedClearedBranch(t, domain.ConversationScopeSession, true)
			ctx := context.Background()
			session, branchID := tc.mutate(t, &f)
			before := f.currentSession(t, session)
			root := f.branch(t, f.rootID)
			branch := f.branch(t, branchID)
			conv, err := f.st.ConversationForSession(ctx, session)
			if err != nil {
				t.Fatal(err)
			}

			if f.untouched(t, session, branchID) {
				t.Fatal("touched branch was proven untouched")
			}
			if err := f.st.ReplaceUnpersistedChatProvider(ctx, session, clearedBranchProvider, clearedFreshProvider); err == nil {
				t.Fatal("swap succeeded on a touched branch")
			}
			f.requireUnchanged(t, before, session, root, branch, conv.LatestSequence)
		})
	}
}

// A project conversation taken over by a replacement orchestrator is no longer
// the previous orchestrator's to restart fresh, even though the /clear branch
// it left is still empty.
func TestReplaceUnpersistedChatProviderRefusesClearedBranchOfAnotherOwner(t *testing.T) {
	f := seedClearedBranch(t, domain.ConversationScopeProject, true)
	ctx := context.Background()
	rec := sampleRecord("cleared")
	rec.Kind = domain.KindOrchestrator
	rec.Mode = domain.SessionModeChat
	replacement, err := f.st.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateConversation(ctx, "ignored", domain.ConversationScopeProject,
		replacement.ProjectID, replacement.ID, f.now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	conv, err := f.st.ConversationForSession(ctx, replacement.ID)
	if err != nil {
		t.Fatal(err)
	}
	if conv.ID != f.conversation.ID || conv.ActiveBranchID != f.branchID {
		t.Fatalf("replacement did not take the project conversation: %+v", conv)
	}
	before := f.currentSession(t, f.session.ID)
	root := f.branch(t, f.rootID)
	branch := f.branch(t, f.branchID)

	if f.untouched(t, f.session.ID, f.branchID) {
		t.Fatal("previous owner proved a branch it no longer holds")
	}
	if err := f.st.ReplaceUnpersistedChatProvider(ctx, f.session.ID, clearedBranchProvider, clearedFreshProvider); err == nil {
		t.Fatal("previous owner swapped a branch it no longer holds")
	}
	f.requireUnchanged(t, before, replacement.ID, root, branch, conv.LatestSequence)
}

// The root swap keeps its old behaviour, except that an agent_session_id still
// naming the reserved id now moves with it (a hook may have reserved it).
func TestReplaceUnpersistedChatProviderClearedBranchRootAgentSessionID(t *testing.T) {
	cases := []struct {
		name         string
		agentSession string
		launch       string
		wantAgent    string
		wantLaunch   string
	}{
		{name: "empty agent session", wantAgent: ""},
		{name: "reserved agent session", agentSession: "reserved-chat-id", launch: "launch-1", wantAgent: "fresh-chat-id"},
		{name: "other agent session", agentSession: "other-native-id", launch: "launch-1", wantAgent: "other-native-id", wantLaunch: "launch-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			ctx := context.Background()
			seedProject(t, st, "root-agent")
			now := time.Now()
			rec := sampleRecord("root-agent")
			rec.Mode = domain.SessionModeChat
			rec.Metadata.ProviderConversationID = "reserved-chat-id"
			rec.Metadata.ControllerGeneration = "chat-generation"
			rec.Metadata.AgentSessionID = tc.agentSession
			rec.Metadata.AgentSessionIDLaunchID = tc.launch
			sess, err := st.CreateSession(ctx, rec)
			if err != nil {
				t.Fatal(err)
			}
			seeded := func() domain.SessionRecord {
				current, _, err := st.GetSession(ctx, sess.ID)
				if err != nil {
					t.Fatal(err)
				}
				return current
			}()
			if seeded.Metadata.AgentSessionID != tc.agentSession || seeded.Metadata.AgentSessionIDLaunchID != tc.launch {
				t.Fatalf("seed did not persist agent session: %+v", seeded.ControllerOwner())
			}
			conversation, err := st.CreateConversation(ctx, "root-conversation", domain.ConversationScopeSession, sess.ProjectID, sess.ID, now)
			if err != nil {
				t.Fatal(err)
			}
			// The /clear proof never accepts a root.
			if untouched, err := st.ClearedConversationBranchUntouched(ctx, sess.ID, conversation.ID,
				conversation.ActiveBranchID, "reserved-chat-id"); err != nil || untouched {
				t.Fatalf("root accepted as a /clear branch: untouched=%v err=%v", untouched, err)
			}

			if err := st.ReplaceUnpersistedChatProvider(ctx, sess.ID, "reserved-chat-id", "fresh-chat-id"); err != nil {
				t.Fatal(err)
			}

			root, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
			if err != nil {
				t.Fatal(err)
			}
			current, _, err := st.GetSession(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if root.ProviderConversationID != "fresh-chat-id" || current.Metadata.ProviderConversationID != "fresh-chat-id" {
				t.Fatalf("root swap did not move provider: session=%q root=%+v", current.Metadata.ProviderConversationID, root)
			}
			if current.Metadata.AgentSessionID != tc.wantAgent || current.Metadata.AgentSessionIDLaunchID != tc.wantLaunch {
				t.Fatalf("agent session = %q/%q, want %q/%q", current.Metadata.AgentSessionID,
					current.Metadata.AgentSessionIDLaunchID, tc.wantAgent, tc.wantLaunch)
			}
		})
	}
}
