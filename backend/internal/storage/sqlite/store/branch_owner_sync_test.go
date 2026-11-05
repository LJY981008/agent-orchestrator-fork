package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// The chat service rebuilds its in-memory controller owner with
// domain.SessionControllerOwner.ActivatedChatBranch after a branch swap. These
// tests pin that rule to what the real store writes, so the two cannot drift.
func TestBranchOwnerMatchesActivatedChatBranchAfterBranchActivation(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	// A live chat session after its chat commit: the runtime launch id is
	// already cleared, while the hook may have bound a native identity.
	session.Metadata.RuntimeLaunchID = ""
	session.Metadata.AgentSessionID = "thread-root"
	session.Metadata.AgentSessionIDLaunchID = "hook-launch"
	if err := s.UpdateSession(ctx, session); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	seedBranchTurns(t, s, session, conversation)
	rootBranchID := conversation.ActiveBranchID

	before := mustControllerOwner(t, s, session.ID)
	if before.RuntimeLaunchID != "" || before.AgentSessionIDLaunchID != "hook-launch" ||
		before.Mode != domain.SessionModeChat {
		t.Fatalf("seeded owner = %+v", before)
	}

	// /clear-style branch: created and activated in one transaction.
	child := domain.ConversationBranch{
		ID: "branch-cleared", ConversationID: conversation.ID, SessionID: session.ID,
		ParentBranchID: rootBranchID, ProviderConversationID: "thread-cleared",
		ForkAfterSequence: 4, CreatedAt: testNow.Add(time.Minute),
	}
	if err := s.CreateAndActivateConversationBranch(ctx, session.ID, child,
		"generation-cleared", child.CreatedAt); err != nil {
		t.Fatalf("CreateAndActivateConversationBranch: %v", err)
	}
	afterCreate := mustControllerOwner(t, s, session.ID)
	want := before.ActivatedChatBranch(session.Harness, "thread-cleared", "generation-cleared")
	if afterCreate != want {
		t.Fatalf("owner after CreateAndActivateConversationBranch = %+v, want %+v", afterCreate, want)
	}

	// Switching back to an existing branch follows the same rule.
	if err := s.ActivateConversationBranch(ctx, session.ID, conversation.ID, rootBranchID,
		"thread-root", "generation-root-again", testNow.Add(2*time.Minute)); err != nil {
		t.Fatalf("ActivateConversationBranch: %v", err)
	}
	afterActivate := mustControllerOwner(t, s, session.ID)
	want = afterCreate.ActivatedChatBranch(session.Harness, "thread-root", "generation-root-again")
	if afterActivate != want {
		t.Fatalf("owner after ActivateConversationBranch = %+v, want %+v", afterActivate, want)
	}

	// A second swap from the rebuilt owner still matches the stored row.
	second := domain.ConversationBranch{
		ID: "branch-cleared-2", ConversationID: conversation.ID, SessionID: session.ID,
		ParentBranchID: rootBranchID, ProviderConversationID: "thread-cleared-2",
		ForkAfterSequence: 4, CreatedAt: testNow.Add(3 * time.Minute),
	}
	if err := s.CreateAndActivateConversationBranch(ctx, session.ID, second,
		"generation-cleared-2", second.CreatedAt); err != nil {
		t.Fatalf("second CreateAndActivateConversationBranch: %v", err)
	}
	afterSecond := mustControllerOwner(t, s, session.ID)
	want = want.ActivatedChatBranch(session.Harness, "thread-cleared-2", "generation-cleared-2")
	if afterSecond != want {
		t.Fatalf("owner after second branch = %+v, want %+v", afterSecond, want)
	}
}

func mustControllerOwner(t *testing.T, s *sqlite.Store, id domain.SessionID) domain.SessionControllerOwner {
	t.Helper()
	got, found, err := s.GetSession(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("GetSession(%s): found=%v err=%v", id, found, err)
	}
	return got.ControllerOwner()
}
