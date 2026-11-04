package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func countLadoChromeAudit(t *testing.T, raw *sql.DB) int {
	t.Helper()
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM lado_audit_events WHERE action = 'lado.claude_chrome.changed'`).Scan(&n); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	return n
}

func ladoChromeEvent(at time.Time) domain.LadoAuditEvent {
	return domain.LadoAuditEvent{
		At: at, Actor: "human", Action: "lado.claude_chrome.changed",
		SubjectKind: "daemon", SubjectID: "claude_chrome",
		Payload: json.RawMessage(`{"from":{"chatSessions":false},"to":{"chatSessions":true}}`),
	}
}

func TestRecordLadoClaudeChromeChangeStoresProofAndAuditTogether(t *testing.T) {
	ctx := context.Background()
	s, raw := openLadoAuditStore(t)
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	proof := &domain.LadoPresenceProof{
		Nonce: "chrome-nonce-0001", Action: domain.LadoPresenceClaudeChromeOn, SubjectID: "chatSessions",
		ExpiresAt: at.Add(time.Minute), UsedAt: at,
	}
	if err := s.RecordLadoClaudeChromeChange(ctx, proof, ladoChromeEvent(at)); err != nil {
		t.Fatalf("RecordLadoClaudeChromeChange: %v", err)
	}
	used, err := s.LadoPresenceProofUsed(ctx, proof.Nonce)
	if err != nil || !used {
		t.Fatalf("proof used = %v, %v; want stored", used, err)
	}
	if n := countLadoChromeAudit(t, raw); n != 1 {
		t.Fatalf("audit events = %d, want 1", n)
	}

	// The same nonce again is a replay: nothing more is written.
	err = s.RecordLadoClaudeChromeChange(ctx, proof, ladoChromeEvent(at.Add(time.Second)))
	if !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("replayed proof = %v, want ErrLadoPresenceReplayed", err)
	}
	if n := countLadoChromeAudit(t, raw); n != 1 {
		t.Fatalf("audit events after replay = %d, want still 1", n)
	}
}

func TestRecordLadoClaudeChromeChangeWithoutProofWritesOnlyAudit(t *testing.T) {
	ctx := context.Background()
	s, raw := openLadoAuditStore(t)
	if err := s.RecordLadoClaudeChromeChange(ctx, nil, ladoChromeEvent(time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("RecordLadoClaudeChromeChange: %v", err)
	}
	if n := countLadoChromeAudit(t, raw); n != 1 {
		t.Fatalf("audit events = %d, want 1", n)
	}
}
