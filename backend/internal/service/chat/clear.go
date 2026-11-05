package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/projectmemo"
)

// ContextResetUserClearReason marks the timeline boundary written by /clear.
// The renderer draws it as a divider rather than an ordinary system row.
const ContextResetUserClearReason = "user_clear"

// ClearContextResult reports the branch the session continues on. Cleared is
// false when there was no history to forget and nothing changed. Memo reports
// the orchestrator's project memo step before the clear.
type ClearContextResult struct {
	ActiveBranchID string
	ClearedAt      time.Time
	Cleared        bool
	Memo           projectmemo.ClearOutcome
}

// clearContextProviderItemID keys the boundary row on the session and the new
// branch so a retried write updates one row instead of adding a second.
func clearContextProviderItemID(id domain.SessionID, branchID string) string {
	return domain.ConversationClearBoundaryProviderItemID(id, branchID)
}

// ClearContext is AO's /clear for Chat. The ACP adapter hides Claude Code's own
// /clear, so AO starts a fresh provider conversation itself and records a
// provider boundary branch. AO's timeline keeps every earlier row; only the
// agent forgets them.
//
// The front half is EditMessage's: wake a hibernated session, take the
// controller gate, refuse while a turn runs or work is queued, then close the
// idle source before the fresh Start. A failed Start or branch write reopens
// the source controller.
//
// An orchestrator first writes its project memo (clear_memo.go). That step
// runs detached from the request, holds new work queued, and never stops the
// clear: its outcome is reported in the result's Memo.
func (s *Service) ClearContext(ctx context.Context, id domain.SessionID) (ClearContextResult, error) {
	_, release, err := s.workingController(ctx, id)
	if err != nil {
		return ClearContextResult{}, err
	}
	defer release()
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	if err := gate.lock(ctx); err != nil {
		return ClearContextResult{}, err
	}
	defer gate.unlock()
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return ClearContextResult{}, err
	}
	source, err := s.Controller(id)
	if err != nil {
		return ClearContextResult{}, err
	}
	memo := projectmemo.SkippedOutcome()
	memoStep := clearMemoStep{}
	held := false
	if projectID, ok := s.clearMemoProject(source); ok {
		before, err := s.store.ConversationForSession(ctx, id)
		if err != nil {
			return ClearContextResult{}, fmt.Errorf("load conversation before clear: %w", err)
		}
		if before.LatestSequence > 0 {
			if err := source.beginMemoHold(ctx); err != nil {
				return ClearContextResult{}, err
			}
			held = true
			defer func() {
				if held {
					source.releaseMemoHold()
				}
			}()
			// From here the clear finishes or recovers on its own: the memo
			// step can outlast the caller, and work is already being held.
			ctx = context.WithoutCancel(ctx)
			memoStep = s.writeClearMemo(ctx, source, projectID)
			memo = memoStep.outcome
			if memo.Status == projectmemo.ClearFailed {
				s.recordClearMemoFailure(ctx, source, memo)
			}
		}
	}
	sourceBranchID := source.conversation.ActiveBranchID
	sourceBranch, err := s.store.ConversationBranch(ctx, source.conversation.ID, sourceBranchID)
	if err != nil {
		return ClearContextResult{}, fmt.Errorf("load active conversation branch: %w", err)
	}
	if held {
		err = source.beginMemoBranchHandoff(memoStep.abandonedProviderTurnID)
	} else {
		err = source.BeginIdleBranchHandoff(ctx)
	}
	if err != nil {
		return ClearContextResult{}, err
	}
	abortSource := true
	defer func() {
		if abortSource {
			source.AbortHandoff()
		}
	}()
	// Read the head after the intake fence: nothing can add a row from here on,
	// so this sequence is exactly where the old context ends.
	current, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return ClearContextResult{}, fmt.Errorf("load conversation before clear: %w", err)
	}
	if current.ID != source.conversation.ID || current.ActiveBranchID != sourceBranchID {
		return ClearContextResult{}, ErrControllerHandoff
	}
	if current.LatestSequence == 0 {
		// Nothing to forget: a fresh conversation is already clear.
		return ClearContextResult{ActiveBranchID: sourceBranchID, ClearedAt: s.now(), Memo: memo}, nil
	}

	cfg, driver, err := s.branchLaunchConfig(source)
	if err != nil {
		return ClearContextResult{}, err
	}
	// The source is idle and fenced. Closing it is irreversible, so finish or
	// recover the boundary independently of request cancellation.
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.clearHandoffLimit)
	defer cancel()
	// Messages held during the memo step leave the source before it closes:
	// its stream end would fail them as orphaned. They are recorded again on
	// whichever controller the session ends up with.
	var carried []domain.ConversationMessage
	if held {
		carried, err = source.takeHeldQueue(operationCtx)
		if err != nil {
			s.log.Error("chat clear: take messages held during the memo step", "session", id, "error", err)
			source.AbortHandoff()
			abortSource = false
			held = false
			source.releaseMemoHold()
			s.deliverCarriedTurns(operationCtx, id, carried)
			return ClearContextResult{}, err
		}
		if memoStep.abandonedTurnID != "" {
			if err := s.store.SettleTurnByID(operationCtx, memoStep.abandonedTurnID,
				domain.TurnStateInterrupted, "", s.now()); err != nil {
				s.log.Warn("chat clear: settle abandoned memo turn", "session", id, "error", err)
			}
		}
		// The source is retired from here on; the hold must not reopen it.
		held = false
	}
	restore := func(cause error) (ClearContextResult, error) {
		defer s.deliverCarriedTurns(operationCtx, id, carried)
		if restoreErr := s.restoreClosedSourceController(
			ctx, id, source, sourceBranch, cfg, driver); restoreErr != nil {
			return ClearContextResult{}, errors.Join(cause, restoreErr)
		}
		abortSource = false
		return ClearContextResult{}, cause
	}
	if err := source.closeForBranchHandoff(operationCtx); err != nil {
		closeErr := fmt.Errorf("close source writer before clear: %w", err)
		if source.State() == ports.ChatControllerStopped {
			return restore(closeErr)
		}
		// The source still runs: reopen it, lift the memo hold and give the
		// held messages back. Without the release the source would keep
		// queueing every later message and refuse the next /clear.
		source.AbortHandoff()
		abortSource = false
		source.releaseMemoHold()
		s.deliverCarriedTurns(operationCtx, id, carried)
		return ClearContextResult{}, closeErr
	}
	launchEnv, err := s.prepareBranchControllerEnv(operationCtx, cfg)
	if err != nil {
		return restore(err)
	}
	providerScopeID := s.newID()
	provider, err := driver.Start(operationCtx, ports.ChatStartConfig{
		SessionID: cfg.SessionID, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath,
		Env: launchEnv, Model: cfg.Model, Effort: cfg.Effort,
		Permissions:  cfg.Permissions,
		SystemPrompt: cfg.SystemPrompt, AdditionalDirectories: cfg.AdditionalDirectories,
		MCPServers: cfg.MCPServers, ProviderScopeID: providerScopeID, ProviderIDsScoped: true,
	})
	if err != nil {
		return restore(classify(fmt.Errorf("start cleared conversation: %w", err)))
	}
	providerConversationID := ""
	if provider != nil {
		providerConversationID = provider.ProviderConversationID()
	}
	if provider == nil || providerConversationID == "" {
		if provider != nil {
			_ = provider.Close()
		}
		return restore(errors.New("cleared provider conversation is not ready"))
	}

	now := s.now()
	branchID := s.newID()
	generation := s.newID()
	branch := domain.ConversationBranch{
		ID: branchID, ConversationID: source.conversation.ID, SessionID: id,
		ProviderConversationID: providerConversationID, ParentBranchID: sourceBranchID,
		ForkAfterSequence: current.LatestSequence, CreatedAt: now,
		Strategy:        domain.ConversationBranchStrategyNative,
		ProviderScopeID: providerScopeID, ProviderIDsScoped: true,
	}
	if err := s.store.CreateAndActivateConversationBranch(operationCtx, id, branch, generation, now); err != nil {
		_ = cleanupUnpublishedConversation(provider, true)
		return restore(fmt.Errorf("activate cleared conversation: %w", err))
	}
	// The boundary row is a courtesy for the reader. The agent has already
	// forgotten, so a failed write must not undo the clear.
	if err := s.recordClearBoundary(operationCtx, id, source.conversation.ID, branchID, now); err != nil {
		s.log.Warn("chat clear: record context boundary", "session", id, "branch", branchID, "error", err)
	}
	conversation := source.conversation
	conversation.ActiveBranchID = branchID
	replacement := newController(id, source.owner(), conversation, generation, source.harness, provider,
		s.store, s.activity, s.log, s.newID, s.now, s.onAccountChanged, s.onCodexCapacityChanged, s.reader)
	replacement.memo = source.memo
	if err := s.installBranchController(operationCtx, id, source, replacement, sourceBranchID); err != nil {
		s.deliverCarriedTurns(operationCtx, id, carried)
		return ClearContextResult{}, err
	}
	abortSource = false
	// The held messages go first on the new branch, in the order they came.
	// The first of them is the new conversation's first turn, so it carries
	// the project memo.
	s.deliverCarriedTurns(operationCtx, id, carried)
	return ClearContextResult{ActiveBranchID: branchID, ClearedAt: now, Cleared: true, Memo: memo}, nil
}

func (s *Service) recordClearBoundary(
	ctx context.Context,
	id domain.SessionID,
	conversationID, branchID string,
	now time.Time,
) error {
	detail, err := json.Marshal(map[string]string{
		"event":  "context.reset",
		"reason": ContextResetUserClearReason,
	})
	if err != nil {
		return fmt.Errorf("encode clear boundary: %w", err)
	}
	return s.store.UpsertActivity(ctx, conversationID, "", domain.ConversationActivity{
		ID:             s.newID(),
		Kind:           domain.ActivityKindSystem,
		Status:         domain.ActivityStatusCompleted,
		Summary:        "Context cleared.",
		Detail:         detail,
		ProviderItemID: clearContextProviderItemID(id, branchID),
	}, now)
}
