package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// ladoRecordingGate records what the chat service asks the LADO gate. It
// answers every send with err and every approval mode with modeErr, and caps
// every turn's approval at capTo when that is set.
type ladoRecordingGate struct {
	err      error
	origins  []ports.SendOrigin
	capTo    domain.PermissionMode
	clampErr error
	turns    []ports.ChatTurnSettings
	modeErr  error
	modes    []domain.PermissionMode
}

func (g *ladoRecordingGate) AuthorizeSpawn(context.Context, ports.SpawnConfig) error { return nil }

func (g *ladoRecordingGate) AuthorizeSend(_ context.Context, _ domain.SessionID, origin ports.SendOrigin) error {
	g.origins = append(g.origins, origin)
	return g.err
}

func (g *ladoRecordingGate) ClampTurnSettings(_ context.Context, _ domain.SessionID, s ports.ChatTurnSettings) (ports.ChatTurnSettings, error) {
	g.turns = append(g.turns, s)
	if g.clampErr != nil {
		return ports.ChatTurnSettings{}, g.clampErr
	}
	if g.capTo != "" {
		s.Approval = g.capTo
	}
	return s, nil
}

func (g *ladoRecordingGate) AuthorizeConfigMode(_ context.Context, _ domain.SessionID, mode domain.PermissionMode) error {
	g.modes = append(g.modes, mode)
	return g.modeErr
}

func (g *ladoRecordingGate) AuthorizeExecutionChange(context.Context, domain.SessionID) error {
	return nil
}

func (g *ladoRecordingGate) AuthorizeRestore(context.Context, domain.SessionID) error {
	return nil
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

func TestSetTurnSettingsUsesTheLadoCap(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	gate := &ladoRecordingGate{capTo: domain.PermissionModeAuto}
	h.svc.SetWorkGate(gate)

	got, err := h.svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{
		Model: "gpt-5.5", ReasoningEffort: "high", ApprovalMode: domain.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("SetTurnSettings: %v", err)
	}
	want := domain.ConversationSettings{Model: "gpt-5.5", ReasoningEffort: "high", ApprovalMode: domain.PermissionModeAuto}
	if got != want {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
	if asked := []ports.ChatTurnSettings{{Model: "gpt-5.5", Effort: "high", Approval: domain.PermissionModeBypassPermissions}}; !reflect.DeepEqual(gate.turns, asked) {
		t.Fatalf("gate saw %+v, want %+v", gate.turns, asked)
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	if conversation.Settings != want {
		t.Fatalf("stored settings = %+v, want %+v", conversation.Settings, want)
	}

	// A gate that cannot decide changes nothing.
	refusal := errors.New("LADO gate: database is locked")
	gate.clampErr = refusal
	if _, err := h.svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{Model: "gpt-5.4"}); !errors.Is(err, refusal) {
		t.Fatalf("SetTurnSettings error = %v, want the gate's", err)
	}
	if conversation, err := h.st.ConversationForSession(ctx, testSession); err != nil || conversation.Settings != want {
		t.Fatalf("stored settings after a gate error = %+v (%v), want %+v", conversation.Settings, err, want)
	}
}

// ladoModeConversation offers Claude Code's permission-mode option and records
// every option that reaches the provider.
type ladoModeConversation struct {
	*fakeConversation
	mu      sync.Mutex
	mode    string
	applied []string
}

func (c *ladoModeConversation) ListConfigOptions(context.Context) ([]ports.ChatConfigOption, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return []ports.ChatConfigOption{{
		ID: "mode", Type: ports.ChatConfigOptionSelect,
		Current: ports.ChatConfigOptionValue{Select: c.mode},
		Choices: []ports.ChatConfigOptionChoice{
			{Value: "default"}, {Value: "acceptEdits"}, {Value: "auto"}, {Value: "bypassPermissions"},
		},
	}}, nil
}

func (c *ladoModeConversation) SetConfigOption(ctx context.Context, id string, value ports.ChatConfigOptionValue) ([]ports.ChatConfigOption, error) {
	c.mu.Lock()
	c.applied = append(c.applied, id+":"+value.Select)
	if id == "mode" {
		c.mode = value.Select
	}
	c.mu.Unlock()
	return c.ListConfigOptions(ctx)
}

func (c *ladoModeConversation) appliedOptions() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.applied...)
}

func TestSetConfigOptionAsksTheLadoGate(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	const worker = domain.SessionID("p1-2")
	if _, err := st.CreateSession(ctx, domain.SessionRecord{
		ID: worker, ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Mode: domain.SessionModeChat, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed worker: %v", err)
	}
	conv := &ladoModeConversation{fakeConversation: newFakeConversation(), mode: "acceptEdits"}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "lado-config-option-id" },
	})
	t.Cleanup(func() { _ = svc.Stop(ctx, worker) })
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: worker, ProjectID: testProject, Harness: domain.HarnessClaudeCode, WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	started := len(conv.appliedOptions())
	refusal := errors.New("refused by LADO")
	gate := &ladoRecordingGate{modeErr: refusal}
	svc.SetWorkGate(gate)

	if _, err := svc.SetConfigOption(ctx, worker, "mode", ports.ChatConfigOptionValue{Select: "bypassPermissions"}); !errors.Is(err, refusal) {
		t.Fatalf("SetConfigOption error = %v, want the gate's refusal", err)
	}
	if want := []domain.PermissionMode{domain.PermissionModeBypassPermissions}; !reflect.DeepEqual(gate.modes, want) {
		t.Fatalf("gate saw %v, want %v", gate.modes, want)
	}
	if applied := conv.appliedOptions()[started:]; len(applied) != 0 {
		t.Fatalf("provider received %v after a refusal, want nothing", applied)
	}

	// An option that selects no approval mode does not ask the gate, and a
	// mode the gate allows reaches the provider and is recorded.
	gate.modeErr = nil
	if _, err := svc.SetConfigOption(ctx, worker, "model", ports.ChatConfigOptionValue{Select: "opus"}); err != nil {
		t.Fatalf("SetConfigOption model: %v", err)
	}
	if _, err := svc.SetConfigOption(ctx, worker, "mode", ports.ChatConfigOptionValue{Select: "auto"}); err != nil {
		t.Fatalf("SetConfigOption mode: %v", err)
	}
	if want := []domain.PermissionMode{domain.PermissionModeBypassPermissions, domain.PermissionModeAuto}; !reflect.DeepEqual(gate.modes, want) {
		t.Fatalf("gate saw %v, want %v", gate.modes, want)
	}
	if applied, want := conv.appliedOptions()[started:], []string{"model:opus", "mode:auto"}; !reflect.DeepEqual(applied, want) {
		t.Fatalf("provider received %v, want %v", applied, want)
	}
	if conversation, err := st.ConversationForSession(ctx, worker); err != nil || conversation.Settings.ApprovalMode != domain.PermissionModeAuto {
		t.Fatalf("stored approval = %q (%v), want auto", conversation.Settings.ApprovalMode, err)
	}
}

// ladoReadOnlyGate marks one session read-only, as LADO's gate marks a plan
// worker.
type ladoReadOnlyGate struct {
	ladoRecordingGate
	readOnly domain.SessionID
}

func (g *ladoReadOnlyGate) ReadOnlySession(_ context.Context, id domain.SessionID) bool {
	return id == g.readOnly
}

// A session LADO keeps read-only, a plan worker, starts its provider in the
// read-only sandbox, as a reviewer does; another session does not.
func TestLadoReadOnlySessionStartsReadOnly(t *testing.T) {
	for _, readOnly := range []bool{true, false} {
		st := openStore(t)
		provider := newFakeConversation()
		var started ports.ChatStartConfig
		svc := chatsvc.New(chatsvc.Options{
			Store: st, Sessions: st,
			Drivers: fakeRegistry{driver: fakeDriver{conv: provider, startCfg: &started}},
			Log:     slog.New(slog.DiscardHandler),
			NewID:   func() string { return "plan-conversation" },
		})
		gate := &ladoReadOnlyGate{}
		if readOnly {
			gate.readOnly = testSession
		}
		svc.SetWorkGate(gate)
		if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
			SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if started.ReadOnly != readOnly {
			t.Fatalf("read-only %v: provider started with ReadOnly %v", readOnly, started.ReadOnly)
		}
		svc.StopAll(context.Background())
	}
}
