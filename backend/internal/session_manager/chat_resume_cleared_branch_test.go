package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	claudeagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

const (
	clearedRootThread   = "root-thread"
	clearedBranchThread = "cleared-thread"
	clearedBranchID     = "clear-branch"
	clearedGeneration   = "clear-generation"
)

// commitRecordingLauncher keeps every ChatControllerCommit the manager returns
// from ControllerReady, so a test can compare the owner the chat service would
// fence with against the owner the store holds.
type commitRecordingLauncher struct {
	*recordingLauncher
	commits []ChatControllerCommit
}

func (l *commitRecordingLauncher) StartChat(ctx context.Context, cfg ChatStart) (ChatStarted, error) {
	ready := cfg.ControllerReady
	if ready != nil {
		cfg.ControllerReady = func(started ChatStarted) (ChatControllerCommit, error) {
			commit, err := ready(started)
			if err == nil {
				l.commits = append(l.commits, commit)
			}
			return commit, err
		}
	}
	return l.recordingLauncher.StartChat(ctx, cfg)
}

type clearedChatFixture struct {
	store        *sqlite.Store
	session      domain.SessionRecord
	conversation domain.ConversationRecord
	rootID       string
	clearedAt    time.Time
}

// seedClearedChat builds what a Chat /clear leaves before its first prompt: a
// root with two answered prompts, then an active child branch bound to the
// cleared provider id, with the boundary activity when boundary is set.
func seedClearedChat(t *testing.T, kind domain.SessionKind, boundary bool) clearedChatFixture {
	t.Helper()
	// An empty Claude config dir: no transcript exists for any id.
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	store := sqlitetest.MustOpenAt(t, t.TempDir())
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "proj", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	scope := domain.ConversationScopeSession
	if kind == domain.KindOrchestrator {
		scope = domain.ConversationScopeProject
	}
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	created, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "proj", Kind: kind, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{
			WorkspacePath: t.TempDir(), Branch: "ao/session",
			ProviderConversationID: clearedRootThread, ControllerGeneration: "root-generation",
			AgentSessionID: clearedRootThread,
		},
		Activity:      domain.Activity{State: domain.ActivityExited, LastActivityAt: now},
		FirstSignalAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, "conversation-1", scope, "proj", created.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	var lastPrompt time.Time
	for i, turn := range []string{"root-turn-1", "root-turn-2"} {
		lastPrompt = now.Add(time.Duration(i) * time.Second)
		ok, err := store.AppendUserMessage(ctx, conversation.ID, created.ID, "root-generation", domain.ConversationMessage{
			ID: "message-" + turn, Origin: domain.MessageOriginHuman, Text: "prompt " + turn,
			ClientMessageID: "client-" + turn,
		}, turn, lastPrompt)
		if err != nil || !ok {
			t.Fatalf("append root prompt %s: created=%v err=%v", turn, ok, err)
		}
	}
	// The hooks recorded the last root prompt; activation clears its text but
	// keeps its time.
	created, _, err = store.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	created.Metadata.LatestUserPrompt = "prompt root-turn-2"
	created.Metadata.LatestUserPromptAt = lastPrompt
	if err := store.UpdateSession(ctx, created); err != nil {
		t.Fatal(err)
	}
	before, err := store.ConversationForSession(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	clearedAt := now.Add(time.Minute)
	if err := store.CreateAndActivateConversationBranch(ctx, created.ID, domain.ConversationBranch{
		ID: clearedBranchID, ConversationID: conversation.ID, SessionID: created.ID,
		ProviderConversationID: clearedBranchThread, ParentBranchID: before.ActiveBranchID,
		ForkAfterSequence: before.LatestSequence, CreatedAt: clearedAt,
		Strategy:        domain.ConversationBranchStrategyNative,
		ProviderScopeID: "clear-scope", ProviderIDsScoped: true,
	}, clearedGeneration, clearedAt); err != nil {
		t.Fatal(err)
	}
	if boundary {
		if err := store.UpsertActivity(ctx, conversation.ID, "", domain.ConversationActivity{
			ID: "clear-boundary", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
			Summary: "Context cleared.", Detail: []byte(`{"event":"context.reset","reason":"user_clear"}`),
			ProviderItemID: domain.ConversationClearBoundaryProviderItemID(created.ID, clearedBranchID),
		}, clearedAt); err != nil {
			t.Fatal(err)
		}
	}
	session, _, err := store.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.Metadata.ProviderConversationID != clearedBranchThread || session.Metadata.AgentSessionID != clearedBranchThread {
		t.Fatalf("activation did not bind the session to the cleared branch: %+v", session.ControllerOwner())
	}
	return clearedChatFixture{
		store: store, session: session, conversation: conversation,
		rootID: before.ActiveBranchID, clearedAt: clearedAt,
	}
}

func (f clearedChatFixture) manager(launcher *commitRecordingLauncher) *Manager {
	manager := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: claudeagent.New()}, Workspace: &fakeWorkspace{},
		Store: f.store, Messenger: &fakeMessenger{}, Chat: launcher,
		Lifecycle: &sqliteTransitionLifecycle{store: f.store},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	// A live browser authority makes the launch prove the exact durable owner.
	manager.browserCapabilities = &recordingBrowserAuthority{authority: browsersvc.NewAuthority()}
	return manager
}

func (f clearedChatFixture) current(t *testing.T) domain.SessionRecord {
	t.Helper()
	rec, _, err := f.store.GetSession(context.Background(), f.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func (f clearedChatFixture) branch(t *testing.T, id string) domain.ConversationBranch {
	t.Helper()
	branch, err := f.store.ConversationBranch(context.Background(), f.conversation.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return branch
}

func (f clearedChatFixture) messageIDs(t *testing.T) []string {
	t.Helper()
	messages, err := f.store.ConversationMessages(context.Background(), f.conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	return ids
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A Chat restarted after /clear but before its first prompt has a provider id
// Claude never persisted. The empty /clear branch is as untouched as an empty
// root: the restart starts fresh and moves the session and the branch to the
// new id, leaving the root's history alone.
func TestResumeClearedBranchStartsFreshWhenProviderNeverPersisted(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     domain.SessionKind
		boundary bool
	}{
		{"worker with boundary", domain.KindWorker, true},
		{"orchestrator with boundary", domain.KindOrchestrator, true},
		{"worker without boundary", domain.KindWorker, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := seedClearedChat(t, tc.kind, tc.boundary)
			rootMessages := f.messageIDs(t)
			launcher := &commitRecordingLauncher{recordingLauncher: &recordingLauncher{}}

			_, err := f.manager(launcher).ResumeAgentWithMode(ctx, f.session.ID)

			if len(launcher.started) != 1 {
				t.Fatalf("started %d chat controllers, want 1 (err=%v)", len(launcher.started), err)
			}
			start := launcher.started[0]
			if start.ProviderConversationID != clearedBranchThread {
				t.Fatalf("stored id was not offered first: %q", start.ProviderConversationID)
			}
			if !start.FreshIfProviderConversationMissing {
				t.Fatal("fresh-if-missing = false for an untouched /clear branch")
			}
			if err != nil {
				t.Fatalf("ResumeAgentWithMode: %v", err)
			}
			current := f.current(t)
			if current.Metadata.ProviderConversationID != "thread-1" || current.Metadata.AgentSessionID != "thread-1" ||
				current.Metadata.AgentSessionIDLaunchID != "" {
				t.Fatalf("session was not moved to the fresh id: %+v", current.ControllerOwner())
			}
			conversation, err := f.store.ConversationForSession(ctx, f.session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if conversation.ActiveBranchID != clearedBranchID {
				t.Fatalf("active branch = %q, want the /clear branch", conversation.ActiveBranchID)
			}
			if branch := f.branch(t, clearedBranchID); branch.ProviderConversationID != "thread-1" {
				t.Fatalf("/clear branch provider = %q, want thread-1", branch.ProviderConversationID)
			}
			if root := f.branch(t, f.rootID); root.ProviderConversationID != clearedRootThread {
				t.Fatalf("root provider moved: %q", root.ProviderConversationID)
			}
			if got := f.messageIDs(t); !sameIDs(got, rootMessages) {
				t.Fatalf("messages changed: got %v want %v", got, rootMessages)
			}
			if len(launcher.commits) != 1 {
				t.Fatalf("recorded %d commits, want 1", len(launcher.commits))
			}
			if got, want := launcher.commits[0].ControllerOwner, current.ControllerOwner(); got != want {
				t.Fatalf("memory owner differs from the store:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

// Anything recorded on the /clear branch, or a hook fact from after the clear,
// is real work that a missing transcript must not discard: the restart keeps
// requiring the stored conversation and changes nothing.
func TestResumeClearedBranchWithWorkKeepsRequiringHistory(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after func(t *testing.T, f clearedChatFixture)
	}{
		{"user message after the boundary", func(t *testing.T, f clearedChatFixture) {
			ok, err := f.store.AppendUserMessage(context.Background(), f.conversation.ID, f.session.ID, clearedGeneration,
				domain.ConversationMessage{
					ID: "message-after-clear", Origin: domain.MessageOriginHuman, Text: "after clear",
					ClientMessageID: "client-after-clear",
				}, "turn-after-clear", f.clearedAt.Add(time.Second))
			if err != nil || !ok {
				t.Fatalf("append: created=%v err=%v", ok, err)
			}
		}},
		{"provider turn after the boundary", func(t *testing.T, f clearedChatFixture) {
			if err := f.store.AdoptProviderTurn(context.Background(), f.conversation.ID, f.session.ID, clearedGeneration,
				"provider-turn-row", "provider-turn-1", f.clearedAt.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
		}},
		{"latest user prompt after the clear", func(t *testing.T, f clearedChatFixture) {
			rec := f.current(t)
			rec.Metadata.LatestUserPrompt = "after clear"
			if err := f.store.UpdateSession(context.Background(), rec); err != nil {
				t.Fatal(err)
			}
		}},
		{"latest user prompt time after the branch", func(t *testing.T, f clearedChatFixture) {
			rec := f.current(t)
			rec.Metadata.LatestUserPromptAt = f.clearedAt.Add(time.Minute)
			if err := f.store.UpdateSession(context.Background(), rec); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := seedClearedChat(t, domain.KindWorker, true)
			tc.after(t, f)
			before := f.current(t)
			messages := f.messageIDs(t)
			// Without freshness the driver cannot load the missing id and fails.
			launcher := &commitRecordingLauncher{recordingLauncher: &recordingLauncher{
				startErr: errors.New("session/load: resource not found"),
			}}

			_, err := f.manager(launcher).ResumeAgentWithMode(ctx, f.session.ID)

			if len(launcher.started) != 1 {
				t.Fatalf("started %d chat controllers, want 1 (err=%v)", len(launcher.started), err)
			}
			if launcher.started[0].FreshIfProviderConversationMissing {
				t.Fatal("fresh-if-missing = true for a /clear branch with recorded work")
			}
			if err == nil {
				t.Fatal("resume succeeded without the stored conversation")
			}
			current := f.current(t)
			if current.Metadata.ProviderConversationID != before.Metadata.ProviderConversationID ||
				current.Metadata.AgentSessionID != before.Metadata.AgentSessionID {
				t.Fatalf("session moved: before=%+v after=%+v", before.ControllerOwner(), current.ControllerOwner())
			}
			if branch := f.branch(t, clearedBranchID); branch.ProviderConversationID != clearedBranchThread {
				t.Fatalf("/clear branch provider moved: %q", branch.ProviderConversationID)
			}
			if root := f.branch(t, f.rootID); root.ProviderConversationID != clearedRootThread {
				t.Fatalf("root provider moved: %q", root.ProviderConversationID)
			}
			if got := f.messageIDs(t); !sameIDs(got, messages) {
				t.Fatalf("messages changed: got %v want %v", got, messages)
			}
		})
	}
}

// The root swap moves a hook-reserved agent_session_id along with the provider
// id, and the owner ControllerReady hands back agrees with the store.
func TestResumeClearedBranchRootSwapMovesReservedAgentSessionID(t *testing.T) {
	const reserved = "92fadfcb-3d31-4dd6-93c1-097bf823cc19"
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	store := sqlitetest.MustOpenAt(t, t.TempDir())
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "proj", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	created, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "proj", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{
			WorkspacePath: t.TempDir(), Branch: "ao/session",
			ProviderConversationID: reserved, ControllerGeneration: "chat-generation",
			// A SessionStart hook announced the reserved id under an old launch.
			AgentSessionID: reserved, AgentSessionIDLaunchID: "launch-old",
		},
		Activity:      domain.Activity{State: domain.ActivityExited, LastActivityAt: now},
		FirstSignalAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, "conversation-1", domain.ConversationScopeSession, "proj", created.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	f := clearedChatFixture{store: store, session: created, conversation: conversation}
	launcher := &commitRecordingLauncher{recordingLauncher: &recordingLauncher{}}

	_, err = f.manager(launcher).ResumeAgentWithMode(ctx, created.ID)

	if len(launcher.started) != 1 || !launcher.started[0].FreshIfProviderConversationMissing {
		t.Fatalf("untouched root did not start fresh: started=%d err=%v", len(launcher.started), err)
	}
	if err != nil {
		t.Fatalf("ResumeAgentWithMode: %v", err)
	}
	assertFreshProviderOnRoot(t, store, created.ID, conversation)
	current := f.current(t)
	if current.Metadata.AgentSessionID != "thread-1" || current.Metadata.AgentSessionIDLaunchID != "" {
		t.Fatalf("stored agent session id = %q (launch %q), want thread-1 with no launch",
			current.Metadata.AgentSessionID, current.Metadata.AgentSessionIDLaunchID)
	}
	if len(launcher.commits) != 1 {
		t.Fatalf("recorded %d commits, want 1", len(launcher.commits))
	}
	owner := launcher.commits[0].ControllerOwner
	if owner.AgentSessionID != "thread-1" || owner != current.ControllerOwner() {
		t.Fatalf("memory owner differs from the store:\n got %+v\nwant %+v", owner, current.ControllerOwner())
	}
}
