package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LADO fork (#46): restart proof for a Chat whose active branch came from
// /clear. Claude writes no transcript until the first prompt, so a controller
// that dies between /clear and that prompt leaves an id the provider cannot
// load. The root-only proof (nativeConversationNotStarted) never accepts such a
// branch, and that proof is also used by the Chat->TUI switch, so this one is
// kept apart and used only by the Chat restart path (providerNeverPersisted).

// clearedChatBranchStore is the storage the /clear proof reads. A store
// without it cannot prove anything, so the proof fails closed.
type clearedChatBranchStore interface {
	ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
	ConversationBranch(context.Context, string, string) (domain.ConversationBranch, error)
	ClearedConversationBranchUntouched(
		ctx context.Context,
		sessionID domain.SessionID,
		conversationID, branchID, expectedProviderConversationID string,
	) (bool, error)
}

// clearedChatBranchNotStarted reports durable proof that the session's active
// /clear branch has recorded nothing since the clear, so its reserved provider
// id may be replaced by a fresh conversation. The caller has already found no
// provider history behind the id.
//
// The hook facts mirror nativeConversationNotStarted with two differences:
//   - Metadata.Prompt (the worker's opening instruction) is not checked. /clear
//     needs history first, so that instruction was delivered on the root.
//   - LatestUserPromptAt may be set: branch activation clears the prompt text
//     but not its time. A time after the branch was created means a prompt
//     arrived after /clear.
//
// The rows themselves are proven by the store (ClearedConversationBranchUntouched):
// no turns or messages on the branch, only the boundary activity, and no
// sequence handed out past it.
func (m *Manager) clearedChatBranchNotStarted(ctx context.Context, rec domain.SessionRecord) bool {
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return false
	}
	providerID := rec.Metadata.ProviderConversationID
	if providerID == "" {
		return false
	}
	if rec.Metadata.LatestUserPrompt != "" || rec.Metadata.LatestAssistantUpdate != "" ||
		rec.Metadata.ConversationCheckpointUnsettled {
		return false
	}
	switch rec.Metadata.ConversationCheckpointState {
	case "", domain.ConversationCheckpointLegacy, domain.ConversationCheckpointEmpty:
	default:
		return false
	}
	if rec.Metadata.ConversationCheckpointGeneration != "" || rec.Metadata.ConversationCheckpointNativeID != "" {
		return false
	}
	if !transcriptDefinitelyAbsent(rec.Metadata.NativeTranscriptPath) {
		return false
	}
	store, ok := m.store.(clearedChatBranchStore)
	if !ok {
		return false
	}
	conversation, err := store.ConversationForSession(ctx, rec.ID)
	if err != nil || conversation.ActiveBranchID == "" {
		return false
	}
	branch, err := store.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil || branch.ParentBranchID == "" || branch.ProviderConversationID != providerID {
		return false
	}
	if at := rec.Metadata.LatestUserPromptAt; !at.IsZero() && at.After(branch.CreatedAt) {
		return false
	}
	untouched, err := store.ClearedConversationBranchUntouched(ctx, rec.ID, conversation.ID, branch.ID, providerID)
	return err == nil && untouched
}

// transcriptDefinitelyAbsent reports whether a transcript hint cannot hide
// provider history: there is none, or it is an absolute path with no entry.
// Relative paths, existing entries (symlinks included) and lookup errors fail.
func transcriptDefinitelyAbsent(path string) bool {
	if path == "" {
		return true
	}
	if !filepath.IsAbs(path) {
		return false
	}
	_, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist)
}

// moveReservedAgentSessionID mirrors ReplaceUnpersistedChatProvider in memory:
// an agent_session_id that still names the replaced provider id moves to the
// fresh id with no launch id; any other value, empty included, stays.
func moveReservedAgentSessionID(meta *domain.SessionMetadata, previous, next string) {
	if meta.AgentSessionID == "" || meta.AgentSessionID != previous {
		return
	}
	meta.AgentSessionID = next
	meta.AgentSessionIDLaunchID = ""
}
