package chat_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ladoRecordingGate records the origins the chat service asks the LADO gate
// about and answers every send with err.
type ladoRecordingGate struct {
	err     error
	origins []ports.SendOrigin
}

func (g *ladoRecordingGate) AuthorizeSpawn(context.Context, ports.SpawnConfig) error { return nil }

func (g *ladoRecordingGate) AuthorizeSend(_ context.Context, _ domain.SessionID, origin ports.SendOrigin) error {
	g.origins = append(g.origins, origin)
	return g.err
}

func TestLadoGateRefusesAnotherSessionsMessageBeforeDelivery(t *testing.T) {
	refusal := errors.New("refused by LADO")
	// The conversation routes mark every request human and carry the CLI's
	// sender separately (httpd/controllers/conversation_steer.go).
	fromOrchestrator := ports.ChatUserMessage{
		Text: "rebase now", ClientMessageID: "lado-1", Origin: domain.MessageOriginHuman,
		SenderSessionID: "p1-orchestrator-1",
	}
	paths := []struct {
		name    string
		deliver func(*harness) error
	}{
		{"send", func(h *harness) error {
			_, err := h.svc.Send(context.Background(), testSession, fromOrchestrator)
			return err
		}},
		{"steer", func(h *harness) error {
			_, err := h.svc.Steer(context.Background(), testSession, fromOrchestrator)
			return err
		}},
		{"steer or send", func(h *harness) error {
			_, err := h.svc.SteerOrSend(context.Background(), testSession, fromOrchestrator, false)
			return err
		}},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			h := newHarness(t)
			gate := &ladoRecordingGate{err: refusal}
			h.svc.SetWorkGate(gate)

			if err := path.deliver(h); !errors.Is(err, refusal) {
				t.Fatalf("error = %v, want the gate's refusal", err)
			}
			if want := []ports.SendOrigin{ports.SendOriginSession}; !reflect.DeepEqual(gate.origins, want) {
				t.Fatalf("gate saw %v, want %v", gate.origins, want)
			}
			if calls := h.conv.sendCallCount(); calls != 0 {
				t.Fatalf("provider received %d sends, want none", calls)
			}
		})
	}
}

func TestLadoGateLeavesSteerRecoveryAlone(t *testing.T) {
	h := newHarness(t)
	refusal := errors.New("refused by LADO")
	gate := &ladoRecordingGate{err: refusal}
	h.svc.SetWorkGate(gate)

	_, err := h.svc.SteerOrSend(context.Background(), testSession,
		ports.ChatUserMessage{ClientMessageID: "never-delivered"}, true)
	if errors.Is(err, refusal) || len(gate.origins) != 0 {
		t.Fatalf("recover-only: error %v, gate calls %v; want no gate call", err, gate.origins)
	}
}

func TestLadoGateLetsAPersonsMessageThrough(t *testing.T) {
	h := newHarness(t)
	gate := &ladoRecordingGate{}
	h.svc.SetWorkGate(gate)

	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "make it blue", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if want := []ports.SendOrigin{ports.SendOriginHuman}; !reflect.DeepEqual(gate.origins, want) {
		t.Fatalf("gate saw %v, want %v", gate.origins, want)
	}
}
