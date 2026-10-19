package chat

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLadoMessageOriginReadsTheMessage(t *testing.T) {
	cases := []struct {
		name string
		msg  ports.ChatUserMessage
		want ports.SendOrigin
	}{
		{"person", ports.ChatUserMessage{Origin: domain.MessageOriginHuman}, ports.SendOriginHuman},
		{"another session through a human route", ports.ChatUserMessage{Origin: domain.MessageOriginHuman, SenderSessionID: "orchestrator-1"}, ports.SendOriginSession},
		{"another session marked automation", ports.ChatUserMessage{Origin: domain.MessageOriginAutomation, SenderSessionID: "orchestrator-1"}, ports.SendOriginSession},
		{"daemon", ports.ChatUserMessage{Origin: domain.MessageOriginDaemon}, ports.SendOriginDaemon},
		{"automation", ports.ChatUserMessage{Origin: domain.MessageOriginAutomation}, ports.SendOriginAutomation},
		{"no origin", ports.ChatUserMessage{}, ports.SendOriginAutomation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ladoMessageOrigin(tc.msg); got != tc.want {
				t.Fatalf("ladoMessageOrigin = %q, want %q", got, tc.want)
			}
		})
	}
}
