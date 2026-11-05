package chat

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/projectmemo"
)

// ProjectMemoResourceURI names the project memo AO attaches to an
// orchestrator's new provider conversation.
const ProjectMemoResourceURI = ports.ChatProjectMemoResourceURI

// projectMemoTracker remembers, per session, which memo revision each provider
// conversation has already received. It lives in memory only: a daemon restart
// forgets it, so the first turn after a restart carries the memo again.
type projectMemoTracker struct {
	mu        sync.Mutex
	delivered map[domain.SessionID]map[string]string
}

func newProjectMemoTracker() *projectMemoTracker {
	return &projectMemoTracker{delivered: map[domain.SessionID]map[string]string{}}
}

func (t *projectMemoTracker) revision(id domain.SessionID, providerConversationID string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	revision, ok := t.delivered[id][providerConversationID]
	return revision, ok
}

func (t *projectMemoTracker) mark(id domain.SessionID, providerConversationID, revision string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	byConversation := t.delivered[id]
	if byConversation == nil {
		byConversation = map[string]string{}
		t.delivered[id] = byConversation
	}
	byConversation[providerConversationID] = revision
}

// forget drops a session's delivery records, so the conversation restored
// after the session ends gets the memo again on its first turn.
func (t *projectMemoTracker) forget(id domain.SessionID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.delivered, id)
}

// controllerMemo is an orchestrator controller's view of its project memo.
type controllerMemo struct {
	store     *projectmemo.Store
	tracker   *projectMemoTracker
	projectID domain.ProjectID
	now       Clock
}

// projectMemoDelivery is a memo attached to one dispatch, recorded as
// delivered once the provider accepts the turn.
type projectMemoDelivery struct {
	providerConversationID string
	revision               string
}

// projectMemoFor returns the memo hook for a controller started with cfg, or
// nil when the session is not an orchestrator or AO has no data dir.
func (s *Service) projectMemoFor(cfg StartConfig, owner domain.ConversationOwner) *controllerMemo {
	if s.memos == nil || owner.Kind != domain.ConversationOwnerSession ||
		cfg.Kind != domain.KindOrchestrator || cfg.ProjectID == "" {
		return nil
	}
	return &controllerMemo{store: s.memos, tracker: s.memoTracker, projectID: cfg.ProjectID, now: s.now}
}

// withProjectMemo puts the project memo in front of a dispatch when this
// provider conversation has not received the current revision yet. That is the
// first turn of a new provider conversation (after /clear, a fresh start, or a
// daemon restart) and the first turn after the memo changed. The resource is
// added to the provider message only: the durable row, and so the timeline,
// never shows it. Callers hold sendMu.
func (c *Controller) withProjectMemo(msg ports.ChatUserMessage) (ports.ChatUserMessage, projectMemoDelivery) {
	m := c.memo
	if m == nil || c.memoRequestInFlight || !c.conv.Capabilities().Has(ports.ChatCapabilityEmbeddedContext) {
		return msg, projectMemoDelivery{}
	}
	providerConversationID := c.conv.ProviderConversationID()
	if providerConversationID == "" {
		return msg, projectMemoDelivery{}
	}
	memo, err := m.store.Get(m.projectID)
	if err != nil {
		if !errors.Is(err, projectmemo.ErrNotFound) {
			c.log.Warn("chat dispatch: read project memo", "session", c.sessionID, "project", m.projectID, "error", err)
		}
		return msg, projectMemoDelivery{}
	}
	if delivered, ok := m.tracker.revision(c.sessionID, providerConversationID); ok && delivered == memo.Revision {
		return msg, projectMemoDelivery{}
	}
	content := make([]ports.ChatContent, 0, len(msg.Content)+1)
	content = append(content, projectMemoResource(memo, m.now()))
	msg.Content = append(content, msg.Content...)
	return msg, projectMemoDelivery{providerConversationID: providerConversationID, revision: memo.Revision}
}

func (c *Controller) markProjectMemoDelivered(delivery projectMemoDelivery) {
	if c.memo == nil || delivery.revision == "" {
		return
	}
	c.memo.tracker.mark(c.sessionID, delivery.providerConversationID, delivery.revision)
}

// projectMemoResource renders the memo as AO-owned embedded context.
func projectMemoResource(memo projectmemo.Memo, now time.Time) ports.ChatContent {
	var b strings.Builder
	b.WriteString("# Project memo\n\n")
	b.WriteString("AO keeps this memo for the project's orchestrator. An earlier orchestrator conversation or the developer wrote it. ")
	b.WriteString("Treat it as context from before this conversation, not as a new request.\n\n")
	b.WriteString("- Updated: " + memo.UpdatedAt.UTC().Format(time.RFC3339) + "\n")
	b.WriteString("- Written by: " + projectMemoSourceLabel(memo.Source) + "\n")
	if memo.Stale(now) {
		days := int(now.Sub(memo.UpdatedAt) / (24 * time.Hour))
		b.WriteString("- Note: this memo is old (" + strconv.Itoa(days) + " days). Check it against the current state before relying on it.\n")
	}
	b.WriteString("\n---\n\n")
	b.WriteString(memo.Body)
	return ports.ChatContent{
		Type: "resource", URI: ProjectMemoResourceURI, Name: "project memo",
		MIMEType: "text/markdown", Text: b.String(), Internal: true,
	}
}

func projectMemoSourceLabel(source projectmemo.Source) string {
	switch source {
	case projectmemo.SourceClear:
		return "the orchestrator, before /clear"
	case projectmemo.SourceUser:
		return "the developer"
	default:
		return string(source)
	}
}
