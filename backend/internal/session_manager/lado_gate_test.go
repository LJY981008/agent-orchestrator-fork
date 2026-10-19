package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// recordingWorkGate records what the session manager asks the LADO gate and
// answers with the configured errors.
type recordingWorkGate struct {
	spawnErr error
	sendErr  error
	spawns   []ports.SpawnConfig
	sends    []ladoSendCall
}

type ladoSendCall struct {
	target domain.SessionID
	origin ports.SendOrigin
}

func (g *recordingWorkGate) AuthorizeSpawn(_ context.Context, cfg ports.SpawnConfig) error {
	g.spawns = append(g.spawns, cfg)
	return g.spawnErr
}

func (g *recordingWorkGate) AuthorizeSend(_ context.Context, target domain.SessionID, origin ports.SendOrigin) error {
	g.sends = append(g.sends, ladoSendCall{target: target, origin: origin})
	return g.sendErr
}

func TestLadoSendOriginReadsTheDeliveryFacts(t *testing.T) {
	report := domain.WrapReportDelivery("report-batch:1", "Reports since your previous turn:")
	cases := []struct {
		name    string
		message string
		options ports.MessageDeliveryOptions
		want    ports.SendOrigin
	}{
		{"person", "make it blue", ports.MessageDeliveryOptions{AuthoredByUser: true}, ports.SendOriginHuman},
		{"another session", "make it blue", ports.MessageDeliveryOptions{SenderSessionID: "mer-orchestrator-1"}, ports.SendOriginSession},
		{"session that also claims a person", "make it blue", ports.MessageDeliveryOptions{SenderSessionID: "mer-orchestrator-1", AuthoredByUser: true}, ports.SendOriginSession},
		{"report relay", report, ports.MessageDeliveryOptions{}, ports.SendOriginCoordination},
		{"session sending a report envelope", report, ports.MessageDeliveryOptions{SenderSessionID: "mer-orchestrator-1"}, ports.SendOriginSession},
		{"no sender facts", "CI failed on your PR", ports.MessageDeliveryOptions{}, ports.SendOriginDaemon},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ladoSendOrigin(tc.message, tc.options); got != tc.want {
				t.Fatalf("ladoSendOrigin = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSpawnRefusedByTheWorkGateCreatesNothing(t *testing.T) {
	m, st, rt, ws := newManager()
	refusal := errors.New("refused by LADO")
	gate := &recordingWorkGate{spawnErr: refusal}
	m.SetWorkGate(gate)

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build it"})
	if !errors.Is(err, refusal) {
		t.Fatalf("Spawn error = %v, want the gate's refusal", err)
	}
	if len(gate.spawns) != 1 || gate.spawns[0].ProjectID != "mer" || gate.spawns[0].Prompt != "build it" {
		t.Fatalf("gate saw %+v, want the caller's config", gate.spawns)
	}
	if len(st.sessions) != 0 || ws.createCount != 0 || rt.created != 0 {
		t.Fatalf("after a refusal: %d sessions, %d workspaces, %d runtimes; want none",
			len(st.sessions), ws.createCount, rt.created)
	}
}

func TestSpawnAllowedByTheWorkGateProceeds(t *testing.T) {
	m, st, _, _ := newManager()
	gate := &recordingWorkGate{}
	m.SetWorkGate(gate)

	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, ok := st.sessions[rec.ID]; !ok || len(gate.spawns) != 1 {
		t.Fatalf("session stored = %v, gate calls = %d; want the session and one gate call", ok, len(gate.spawns))
	}
}

func TestSendRefusedByTheWorkGateDeliversNothing(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = pastStartupGate(domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: "claude-code"})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, fakeAgent{}, msg, st)
	refusal := errors.New("refused by LADO")
	gate := &recordingWorkGate{sendErr: refusal}
	m.SetWorkGate(gate)

	err := m.SendWithOptions(ctx, "mer-1", "rebase now", nil, ports.MessageDeliveryOptions{SenderSessionID: "mer-orchestrator-1"})
	if !errors.Is(err, refusal) {
		t.Fatalf("SendWithOptions error = %v, want the gate's refusal", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("delivered %q, want nothing", msg.msgs)
	}
	if want := []ladoSendCall{{target: "mer-1", origin: ports.SendOriginSession}}; !reflect.DeepEqual(gate.sends, want) {
		t.Fatalf("gate saw %+v, want %+v", gate.sends, want)
	}
}

// TestSendRefusedByTheWorkGateStagesNoAttachment proves the gate runs before
// the attachment is written into the target's workspace: the session has no
// workspace, so staging would fail with its own error first.
func TestSendRefusedByTheWorkGateStagesNoAttachment(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = pastStartupGate(domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: "claude-code"})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, fakeAgent{}, msg, st)
	refusal := errors.New("refused by LADO")
	m.SetWorkGate(&recordingWorkGate{sendErr: refusal})

	attachment := &ports.SpawnAttachment{Ext: ".png", Data: []byte("snapshot-bytes")}
	err := m.SendWithOptions(ctx, "mer-1", "look at this", attachment, ports.MessageDeliveryOptions{SenderSessionID: "mer-orchestrator-1"})
	if !errors.Is(err, refusal) {
		t.Fatalf("SendWithOptions error = %v, want the gate's refusal before staging", err)
	}
}

func TestSendTellsTheWorkGateWhoSent(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = pastStartupGate(domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: "claude-code"})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, fakeAgent{}, msg, st)
	gate := &recordingWorkGate{}
	m.SetWorkGate(gate)

	if err := m.Send(ctx, "mer-1", "CI failed on your PR", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := m.SendWithOptions(ctx, "mer-1", "make it blue", nil, ports.MessageDeliveryOptions{AuthoredByUser: true}); err != nil {
		t.Fatalf("SendWithOptions: %v", err)
	}
	want := []ladoSendCall{
		{target: "mer-1", origin: ports.SendOriginDaemon},
		{target: "mer-1", origin: ports.SendOriginHuman},
	}
	if !reflect.DeepEqual(gate.sends, want) {
		t.Fatalf("gate saw %+v, want %+v", gate.sends, want)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("delivered %d messages, want 2", len(msg.msgs))
	}
}
