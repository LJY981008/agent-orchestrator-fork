package acp

import (
	"context"
	"io"
	"log/slog"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A Chat restarted right after /clear resumes a conversation that already has
// turns (HasPriorTurns) on a provider session that was never persisted. The
// driver then opens a fresh provider session in its place. That session has
// never seen AO's standing instructions, so they must reach the provider again
// on whichever channel the binding uses: session/new _meta (Claude) or the
// first prompt's preamble. A resume that marked the session as already
// instructed would drop the orchestrator's rules from the new conversation.
func TestACPDriverResumeFreshWithPriorTurnsKeepsStandingInstructions(t *testing.T) {
	notFound := &acpsdk.RequestError{Code: -32002, Message: "Resource not found: cleared-provider-session"}
	resumeFresh := func(t *testing.T, cfg Config) (*fakeAgent, ports.ChatConversation) {
		t.Helper()
		agent := &fakeAgent{
			capabilities:       &acpsdk.AgentCapabilities{LoadSession: true},
			failLoadFrom:       1,
			failLoadErr:        notFound,
			promptNoPermission: true,
		}
		driver := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
		driver.useTestProcess(fakeSpawn(agent))
		conv, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
			SessionID:              "orchestrator-1",
			ProviderConversationID: "cleared-provider-session",
			WorkspacePath:          t.TempDir(),
			FreshIfMissing:         true,
			HasPriorTurns:          true,
			SystemPrompt:           standingInstructions,
		})
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		t.Cleanup(func() { _ = conv.Close() })
		if got := conv.ProviderConversationID(); got != "claude-session-1" {
			t.Fatalf("provider conversation = %q, want the fresh session", got)
		}
		agent.mu.Lock()
		loadCalls, newCwd := agent.loadCalls, agent.newParams.Cwd
		agent.mu.Unlock()
		if loadCalls != 1 || newCwd == "" {
			t.Fatalf("load calls = %d, session/new cwd = %q; want a failed load then a fresh session", loadCalls, newCwd)
		}
		_ = nextEvent(t, conv.Events()) // controller.ready
		return agent, conv
	}

	t.Run("session meta", func(t *testing.T) {
		cfg := Config{
			Harness:      domain.HarnessClaudeCode,
			Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
			Probe:        func(context.Context) error { return nil },
			Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
			// The shape claudeacp's claudeSessionMeta sends.
			SessionMeta: func(cfg LaunchConfig) map[string]any {
				return map[string]any{"systemPrompt": map[string]any{
					"type": "preset", "preset": "claude_code", "append": cfg.SystemPrompt,
				}}
			},
		}
		agent, conv := resumeFresh(t, cfg)
		agent.mu.Lock()
		newMeta := agent.newParams.Meta
		agent.mu.Unlock()
		prompt, ok := newMeta["systemPrompt"].(map[string]any)
		if !ok || prompt["append"] != standingInstructions {
			t.Fatalf("fresh session/new metadata = %#v, want the standing instructions", newMeta)
		}
		sent := runOneTurn(t, agent, conv, "first message after /clear")
		if len(sent) == 0 || sent[len(sent)-1].Text == nil || sent[len(sent)-1].Text.Text != "first message after /clear" {
			t.Fatalf("first prompt = %#v, want the user's message", sent)
		}
	})

	t.Run("first prompt preamble", func(t *testing.T) {
		agent, conv := resumeFresh(t, preambleConfig())
		sent := runOneTurn(t, agent, conv, "first message after /clear")
		if got := preambleText(sent); got != standingInstructions {
			t.Fatalf("fresh first prompt standing context = %q, want %q (prompt %#v)", got, standingInstructions, sent)
		}
		if len(sent) < 2 || sent[1].Text == nil || sent[1].Text.Text != "first message after /clear" {
			t.Fatalf("standing context did not lead the user text: %#v", sent)
		}
	})
}
