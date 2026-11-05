package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/projectmemo"
)

// The /clear memo step. Before an orchestrator's conversation is cleared, AO
// asks it for a short handoff memo, saves the memo as the project memo, and
// then clears. The step never blocks the clear: a failure is reported and the
// clear goes ahead with the earlier memo unchanged.
//
// While the memo turn runs the controller is in memo hold: messages from
// people, workers and the daemon are accepted as durable queued turns but not
// dispatched. The clear then carries them to the new branch, where they
// dispatch in order and the first one carries the memo.

const (
	// clearMemoBudget bounds the memo step, the shrink request included.
	clearMemoBudget = 90 * time.Second
	// clearMemoInterruptWait bounds the wait for a memo turn AO interrupted.
	// A turn still running after it is abandoned: closing the source ends it.
	clearMemoInterruptWait = 15 * time.Second
	// carriedTurnsDeliveryLimit bounds recording held messages on the
	// controller the session continues with.
	carriedTurnsDeliveryLimit = 15 * time.Second
	clearMemoPollInterval     = 50 * time.Millisecond
	clearMemoReportLimit      = 5 * time.Second
)

// Memo step failure codes, reported in ClearContextResult.Memo.Code.
const (
	ClearMemoCodeSendFailed = "memo_send_failed"
	ClearMemoCodeTurnFailed = "memo_turn_failed"
	ClearMemoCodeTimeout    = "memo_timeout"
	ClearMemoCodeMissing    = "memo_missing"
	ClearMemoCodeTooLong    = "memo_too_long"
	ClearMemoCodeSaveFailed = "memo_save_failed"
)

// ClearMemoFailedEvent is the detail event of the timeline row that records a
// failed memo step.
const ClearMemoFailedEvent = "project_memo.failed"

var projectMemoBlock = regexp.MustCompile(`(?s)<ao-project-memo>(.*?)</ao-project-memo>`)

// clearMemoStep is what the memo step leaves for the clear that follows.
type clearMemoStep struct {
	outcome projectmemo.ClearOutcome
	// abandonedTurnID and abandonedProviderTurnID name a memo turn that was
	// still running after Interrupt. The clear settles it as interrupted and
	// closes the source with it.
	abandonedTurnID         string
	abandonedProviderTurnID string
}

// clearMemoProject reports the project whose memo /clear on source writes.
func (s *Service) clearMemoProject(source *Controller) (domain.ProjectID, bool) {
	if s.memos == nil || source.memo == nil {
		return "", false
	}
	return source.memo.projectID, true
}

// writeClearMemo runs the memo step on a controller already in memo hold. ctx
// must not be the request context: the step finishes even if the caller goes.
func (s *Service) writeClearMemo(ctx context.Context, source *Controller, projectID domain.ProjectID) clearMemoStep {
	budgetCtx, cancel := context.WithTimeout(ctx, s.clearMemoBudget)
	defer cancel()

	current, err := s.memos.Get(projectID)
	hasCurrent := err == nil
	if err != nil && !errors.Is(err, projectmemo.ErrNotFound) {
		s.log.Warn("chat clear: read project memo", "session", source.sessionID, "project", projectID, "error", err)
	}
	prompt := clearMemoRequest(current.Body, hasCurrent)
	for attempt := 0; ; attempt++ {
		turn, err := source.sendMemoRequest(budgetCtx, prompt)
		if err != nil {
			return clearMemoStep{outcome: projectmemo.FailedOutcome(ClearMemoCodeSendFailed,
				"AO could not send the memo request: "+err.Error())}
		}
		settled, err := source.awaitTurnSettled(budgetCtx, turn.ID)
		if err != nil {
			step := clearMemoStep{outcome: projectmemo.FailedOutcome(ClearMemoCodeTimeout,
				fmt.Sprintf("The orchestrator did not finish the memo within %s.", s.clearMemoBudget))}
			if !s.stopMemoTurn(ctx, source, turn) {
				step.abandonedTurnID = turn.ID
				step.abandonedProviderTurnID = turn.ProviderTurnID
			}
			return step
		}
		if settled.State != domain.TurnStateCompleted {
			message := fmt.Sprintf("The memo turn ended as %s.", settled.State)
			if settled.ErrorMessage != "" {
				message = fmt.Sprintf("The memo turn ended as %s: %s", settled.State, settled.ErrorMessage)
			}
			return clearMemoStep{outcome: projectmemo.FailedOutcome(ClearMemoCodeTurnFailed, message)}
		}
		reply, err := source.assistantText(ctx, turn.ID)
		if err != nil {
			return clearMemoStep{outcome: projectmemo.FailedOutcome(ClearMemoCodeSaveFailed,
				"AO could not read the memo reply: "+err.Error())}
		}
		body, ok := extractProjectMemo(reply)
		if !ok {
			return clearMemoStep{outcome: projectmemo.FailedOutcome(ClearMemoCodeMissing,
				"The reply had no <ao-project-memo> block.")}
		}
		saved, err := s.memos.Save(projectID, projectmemo.SaveInput{Body: body, Source: projectmemo.SourceClear})
		switch {
		case err == nil:
			return clearMemoStep{outcome: projectmemo.SavedOutcome(saved)}
		case errors.Is(err, projectmemo.ErrTooLong) && attempt == 0:
			prompt = clearMemoShrinkRequest(utf8.RuneCountInString(body))
			continue
		case errors.Is(err, projectmemo.ErrTooLong):
			return clearMemoStep{outcome: projectmemo.FailedOutcome(ClearMemoCodeTooLong,
				fmt.Sprintf("The memo was still over %d characters after AO asked to shorten it.", projectmemo.MaxRunes))}
		case errors.Is(err, projectmemo.ErrInvalid):
			return clearMemoStep{outcome: projectmemo.FailedOutcome(ClearMemoCodeMissing,
				"The memo block was empty or not valid text.")}
		default:
			return clearMemoStep{outcome: projectmemo.FailedOutcome(ClearMemoCodeSaveFailed,
				"AO could not save the memo: "+err.Error())}
		}
	}
}

// stopMemoTurn interrupts a memo turn that ran out of budget and waits a
// bounded time for it to settle. It reports whether the turn settled.
func (s *Service) stopMemoTurn(ctx context.Context, source *Controller, turn domain.ConversationTurn) bool {
	waitCtx, cancel := context.WithTimeout(ctx, s.clearMemoInterruptWait)
	defer cancel()
	if err := source.interruptMemoTurn(waitCtx, turn.ProviderTurnID); err != nil {
		s.log.Warn("chat clear: interrupt memo turn", "session", source.sessionID, "turn", turn.ID, "error", err)
	}
	if _, err := source.awaitTurnSettled(waitCtx, turn.ID); err != nil {
		s.log.Warn("chat clear: memo turn did not stop; clearing without it",
			"session", source.sessionID, "turn", turn.ID)
		return false
	}
	return true
}

// recordClearMemoFailure leaves a system row above the /clear divider, so the
// failure is still visible after the composer notice is gone.
func (s *Service) recordClearMemoFailure(ctx context.Context, source *Controller, outcome projectmemo.ClearOutcome) {
	detail, err := json.Marshal(map[string]string{
		"event": ClearMemoFailedEvent, "code": outcome.Code, "message": outcome.Message,
	})
	if err != nil {
		return
	}
	reportCtx, cancel := context.WithTimeout(ctx, clearMemoReportLimit)
	defer cancel()
	if err := s.store.UpsertActivity(reportCtx, source.conversation.ID, "", domain.ConversationActivity{
		ID:             s.newID(),
		Kind:           domain.ActivityKindSystem,
		Status:         domain.ActivityStatusFailed,
		Summary:        "Project memo was not saved before /clear. " + outcome.Message,
		Detail:         detail,
		ProviderItemID: "ao-project-memo:" + string(source.sessionID) + ":" + s.newID(),
	}, s.now()); err != nil {
		s.log.Warn("chat clear: record memo failure", "session", source.sessionID, "error", err)
	}
}

func clearMemoRequest(current string, hasCurrent bool) string {
	var b strings.Builder
	b.WriteString("AO is about to clear this conversation (/clear). Before it does, write a short handoff memo for the next orchestrator conversation in this project. ")
	b.WriteString("AO saves it as the project memo and gives it to that conversation.\n\n")
	b.WriteString("Include:\n")
	b.WriteString("- work in progress and its state\n")
	b.WriteString("- the scope, order and ways of working the developer decided\n")
	b.WriteString("- confirmations you are waiting for\n")
	b.WriteString("- what to do next\n\n")
	b.WriteString("Leave out secrets (tokens, passwords, keys) and the conversation transcript. ")
	fmt.Fprintf(&b, "Keep it under %d characters. Write it in the language the developer uses.\n\n", projectmemo.MaxRunes)
	b.WriteString("Do not use any tools and do not start other work. Reply with only the memo, inside one block:\n\n")
	b.WriteString("<ao-project-memo>\n...\n</ao-project-memo>\n")
	if hasCurrent && strings.TrimSpace(current) != "" {
		b.WriteString("\nThis is the current project memo. Yours replaces it: keep what still holds and drop what is done or outdated.\n\n")
		b.WriteString("<current-project-memo>\n")
		b.WriteString(current)
		b.WriteString("\n</current-project-memo>\n")
	} else {
		b.WriteString("\nThere is no project memo yet.\n")
	}
	return b.String()
}

func clearMemoShrinkRequest(runes int) string {
	return fmt.Sprintf("That memo is %d characters; the limit is %d. Rewrite it shorter, removing the oldest or least important items first. "+
		"Do not use any tools. Reply with only the memo, inside one <ao-project-memo> block.", runes, projectmemo.MaxRunes)
}

// extractProjectMemo returns the last <ao-project-memo> block in reply.
func extractProjectMemo(reply string) (string, bool) {
	matches := projectMemoBlock.FindAllStringSubmatch(reply, -1)
	if len(matches) == 0 {
		return "", false
	}
	body := strings.TrimSpace(matches[len(matches)-1][1])
	return body, body != ""
}

// beginMemoHold starts the memo hold on an idle controller. It refuses like
// BeginIdleBranchHandoff while a turn runs or work is queued.
func (c *Controller) beginMemoHold(ctx context.Context) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handoff != controllerHandoffNone || c.memoHold {
		return ErrControllerHandoff
	}
	if c.pendingTurnID != "" || c.compactionPending {
		return ErrTurnRunning
	}
	if _, err := c.store.NextQueuedTurn(ctx, c.conversation.ID); err == nil {
		return ErrTurnRunning
	} else if !errors.Is(err, domain.ErrNoQueuedTurn) {
		return fmt.Errorf("check queue before memo hold: %w", err)
	}
	c.memoHold = true
	return nil
}

// memoHeld reports whether a /clear memo step holds this controller's work.
func (c *Controller) memoHeld() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.memoHold
}

// releaseMemoHold ends the hold when the clear did not happen, so the work
// queued during the memo step drains through this controller as usual.
func (c *Controller) releaseMemoHold() {
	c.mu.Lock()
	held := c.memoHold
	c.memoHold = false
	c.mu.Unlock()
	if held {
		go func() {
			_ = c.drain(context.Background()) // drain logs failures; no caller waits.
		}()
	}
}

// sendMemoRequest dispatches the memo request straight to the provider. It
// skips Service.Send, so no worker reports ride on it or are acknowledged by
// it, and it bypasses the hold that keeps every other message queued.
func (c *Controller) sendMemoRequest(ctx context.Context, text string) (domain.ConversationTurn, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.Lock()
	held := c.memoHold && c.handoff == controllerHandoffNone
	c.mu.Unlock()
	if !held {
		return domain.ConversationTurn{}, ErrControllerHandoff
	}
	c.memoRequestInFlight = true
	defer func() { c.memoRequestInFlight = false }()
	return c.sendLocked(ctx, ports.ChatUserMessage{Text: text, Origin: domain.MessageOriginDaemon}, false)
}

// awaitTurnSettled polls a turn until it leaves queued or running.
func (c *Controller) awaitTurnSettled(ctx context.Context, turnID string) (domain.ConversationTurn, error) {
	ticker := time.NewTicker(clearMemoPollInterval)
	defer ticker.Stop()
	for {
		turn, err := c.store.TurnByID(ctx, turnID)
		if err == nil && turn.State != domain.TurnStateQueued && turn.State != domain.TurnStateRunning {
			return turn, nil
		}
		if err != nil && ctx.Err() == nil {
			return domain.ConversationTurn{}, err
		}
		select {
		case <-ctx.Done():
			return domain.ConversationTurn{}, ctx.Err()
		case <-c.stopped:
			return domain.ConversationTurn{}, errors.New("chat controller stopped during the memo turn")
		case <-ticker.C:
		}
	}
}

// assistantText joins the settled assistant messages of one turn.
func (c *Controller) assistantText(ctx context.Context, turnID string) (string, error) {
	messages, err := c.store.ConversationMessages(ctx, c.conversation.ID)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, 2)
	for _, message := range messages {
		if message.TurnID == turnID && message.Role == domain.MessageRoleAssistant && strings.TrimSpace(message.Text) != "" {
			parts = append(parts, message.Text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// interruptMemoTurn cancels the memo turn only. Unlike Interrupt it leaves the
// queue alone: messages held during the memo step belong to the new branch.
func (c *Controller) interruptMemoTurn(ctx context.Context, providerTurnID string) error {
	if providerTurnID == "" || !c.awaitTurnAcknowledged(ctx, providerTurnID) {
		return nil
	}
	if err := c.conv.Interrupt(ctx, providerTurnID); err != nil && !errors.Is(err, ports.ErrChatNoActiveTurn) {
		return err
	}
	return nil
}

// beginMemoBranchHandoff is BeginIdleBranchHandoff for the end of the memo
// step. The memo turn this clear abandoned and the work queued during the hold
// do not refuse the clear: the turn ends with the source, and the queue moves
// to the new branch.
func (c *Controller) beginMemoBranchHandoff(abandonedProviderTurnID string) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.memoHold || c.handoff != controllerHandoffNone {
		return ErrControllerHandoff
	}
	if c.compactionPending || (c.pendingTurnID != "" && c.pendingTurnID != abandonedProviderTurnID) {
		return ErrTurnRunning
	}
	c.handoff = controllerHandoffIdleBranch
	c.branchHandoffDone = make(chan struct{})
	return nil
}

// takeHeldQueue removes the turns queued during the hold, oldest first, and
// returns their messages for the new branch. The source's stream end would
// otherwise fail them as orphaned. The intake fence must already be up. On an
// error the messages taken so far are still returned: they are cancelled here
// and must be delivered again by the caller.
func (c *Controller) takeHeldQueue(ctx context.Context) ([]domain.ConversationMessage, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	rows, err := c.store.ConversationMessages(ctx, c.conversation.ID)
	if err != nil {
		return nil, fmt.Errorf("read held messages: %w", err)
	}
	byTurn := make(map[string]domain.ConversationMessage, len(rows))
	for _, row := range rows {
		if row.Role == domain.MessageRoleUser && row.TurnID != "" {
			byTurn[row.TurnID] = row
		}
	}
	var taken []domain.ConversationMessage
	for {
		queued, err := c.store.NextQueuedTurn(ctx, c.conversation.ID)
		if errors.Is(err, domain.ErrNoQueuedTurn) {
			return taken, nil
		}
		if err != nil {
			return taken, fmt.Errorf("read held queue: %w", err)
		}
		message, found := byTurn[queued.TurnID]
		if !found {
			// The queue row alone still has everything the provider needs.
			message = domain.ConversationMessage{
				ID: queued.TurnID, TurnID: queued.TurnID, Role: domain.MessageRoleUser,
				Text: queued.Text, Origin: queued.Origin, ClientMessageID: queued.ClientMessageID,
				DeliveryContentJSON: queued.DeliveryContentJSON,
			}
		}
		if err := c.store.CancelQueuedTurnByID(ctx, c.conversation.ID, queued.TurnID, c.now()); err != nil {
			return taken, fmt.Errorf("move held message %s: %w", queued.TurnID, err)
		}
		taken = append(taken, message)
	}
}

// acceptCarriedTurns records messages held during a memo step as new queued
// turns of this controller, in order, and starts draining them.
func (c *Controller) acceptCarriedTurns(ctx context.Context, messages []domain.ConversationMessage) error {
	if len(messages) == 0 {
		return nil
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.handoffActive() {
		return ErrControllerHandoff
	}
	var errs []error
	for _, original := range messages {
		record := domain.ConversationMessage{
			ID:                  c.newID(),
			Text:                original.Text,
			Origin:              normalizeOrigin(original.Origin),
			ClientMessageID:     carriedClientMessageID(original.ClientMessageID),
			ClientPayloadHash:   original.ClientPayloadHash,
			DeliveryContentJSON: original.DeliveryContentJSON,
			SenderSessionID:     original.SenderSessionID,
			SenderProjectID:     original.SenderProjectID,
			SenderDisplayName:   original.SenderDisplayName,
			AuthoredByUser:      original.AuthoredByUser,
			Continuation:        original.Continuation,
			InteractionAt:       original.InteractionAt,
		}
		var err error
		if c.reviewID == "" {
			_, err = c.store.AppendUserMessage(ctx, c.conversation.ID, c.sessionID, c.generation, record, c.newID(), c.now())
		} else {
			_, err = c.store.AppendReviewUserMessage(ctx, c.conversation.ID, c.sessionID, c.reviewID, c.generation, record, c.newID(), c.now())
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("carry held message %s: %w", original.ID, err))
		}
	}
	if err := c.drainLocked(ctx, true); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// carriedClientMessageID keeps a carried message idempotent without colliding
// with the cancelled original, which still owns the client's id.
func carriedClientMessageID(id string) string {
	if id == "" {
		return ""
	}
	return id + ":after-clear"
}

// deliverCarriedTurns hands held messages to the session's live controller.
// A failure is logged with the count: the originals are already cancelled.
func (s *Service) deliverCarriedTurns(ctx context.Context, id domain.SessionID, messages []domain.ConversationMessage) {
	if len(messages) == 0 {
		return
	}
	// The clear's own deadline may be spent (a source close that timed out),
	// and these messages exist nowhere else: deliver them on a fresh bound.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), carriedTurnsDeliveryLimit)
	defer cancel()
	controller, err := s.Controller(id)
	if err == nil {
		err = controller.acceptCarriedTurns(ctx, messages)
	}
	if err != nil {
		s.log.Error("chat clear: deliver messages held during the memo step",
			"session", id, "messages", len(messages), "error", err)
	}
}
