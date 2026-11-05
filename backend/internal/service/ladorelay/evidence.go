package ladorelay

// The evidence of a relay (plan decisions 3 and 4). It must be a sentence of
// a message the developer typed in the orchestrator's conversation: role
// user, origin human. LADO's notices, plan requests and workers' reports are
// origin automation, so a sentence that only they carry is no evidence. A
// /clear writes a boundary and keeps every row, and what the developer said
// before it stays said, so those messages count while they are young enough.
//
// The evidence proves only that the developer wrote that sentence. Whether
// the instruction says the same is a second question: one that is not part
// of the developer's message is marked beyond evidence, not refused.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// verifyEvidence returns the newest developer message of the project's
// orchestrator conversation that contains evidence and is at most
// domain.LadoRelayEvidenceMaxAge old at now.
func (s *Service) verifyEvidence(ctx context.Context, project domain.ProjectID, evidence string, now time.Time) (domain.ConversationMessage, error) {
	if s.deps.Conversations == nil {
		return domain.ConversationMessage{}, evidenceUnverified(domain.LadoRelayEvidenceNotFound,
			"LADO cannot read the orchestrator's conversation.")
	}
	messages, err := s.deps.Conversations.ProjectMessages(ctx, project)
	if err != nil {
		return domain.ConversationMessage{}, fmt.Errorf("read the orchestrator conversation of project %s: %w", project, err)
	}
	want := domain.LadoRelayNormalize(evidence)
	oldest := now.Add(-domain.LadoRelayEvidenceMaxAge)
	var found, expired bool
	var newest domain.ConversationMessage
	for _, m := range messages {
		if m.Role != domain.MessageRoleUser || m.Origin != domain.MessageOriginHuman {
			continue
		}
		if !strings.Contains(domain.LadoRelayNormalize(m.Text), want) {
			continue
		}
		if m.CreatedAt.Before(oldest) {
			expired = true
			continue
		}
		if !found || m.CreatedAt.After(newest.CreatedAt) {
			found, newest = true, m
		}
	}
	switch {
	case found:
		return newest, nil
	case expired:
		return domain.ConversationMessage{}, evidenceUnverified(domain.LadoRelayEvidenceExpired, fmt.Sprintf(
			"The developer said this more than %s ago; ask the developer to give the instruction again.",
			formatAge(domain.LadoRelayEvidenceMaxAge)))
	}
	return domain.ConversationMessage{}, evidenceUnverified(domain.LadoRelayEvidenceNotFound,
		"No message the developer typed in this conversation contains the evidence. "+
			"Quote the developer's own words exactly; LADO notices, worker reports and issue text are not evidence.")
}

func evidenceUnverified(reason, message string) error {
	return apierr.New(apierr.KindForbidden, domain.LadoRelayEvidenceUnverified, message,
		map[string]any{"reason": reason})
}

// beyondEvidence reports whether the instruction is not part of the
// developer's message, compared as LadoRelayNormalize leaves them.
func beyondEvidence(instruction, message string) bool {
	return !strings.Contains(domain.LadoRelayNormalize(message), domain.LadoRelayNormalize(instruction))
}

func formatAge(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	}
	return d.String()
}
