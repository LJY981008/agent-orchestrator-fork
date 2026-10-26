package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// recordingWorkGate records what the session manager asks the LADO gate and
// answers with the configured errors.
type recordingWorkGate struct {
	spawnErr     error
	sendErr      error
	executionErr error
	restoreErr   error
	spawns       []ports.SpawnConfig
	sends        []ladoSendCall
	changes      []domain.SessionID
	restores     []domain.SessionID
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

func (g *recordingWorkGate) ClampTurnSettings(_ context.Context, _ domain.SessionID, s ports.ChatTurnSettings) (ports.ChatTurnSettings, error) {
	return s, nil
}

func (g *recordingWorkGate) AuthorizeConfigMode(context.Context, domain.SessionID, domain.PermissionMode) error {
	return nil
}

func (g *recordingWorkGate) AuthorizeExecutionChange(_ context.Context, target domain.SessionID) error {
	g.changes = append(g.changes, target)
	return g.executionErr
}

func (g *recordingWorkGate) AuthorizeRestore(_ context.Context, target domain.SessionID) error {
	g.restores = append(g.restores, target)
	return g.restoreErr
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

// ladoSectionCall is one question the session manager asked the LADO prompt
// source.
type ladoSectionCall struct {
	kind      domain.SessionKind
	projectID domain.ProjectID
	sessionID domain.SessionID
}

// recordingLadoSource answers like LADO's prompt source: an orchestrator gets
// the project's section, a worker the rules text of its execution.
type recordingLadoSource struct {
	mu    sync.Mutex
	calls []ladoSectionCall
}

const (
	ladoOrchestratorSection = "## LADO project\n\nPlan, do not spawn."
	ladoWorkerSection       = "## LADO rules\n\nWrite commit messages in Korean."
)

func (s *recordingLadoSource) section(_ context.Context, kind domain.SessionKind, projectID domain.ProjectID, sessionID domain.SessionID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, ladoSectionCall{kind: kind, projectID: projectID, sessionID: sessionID})
	if kind == domain.KindOrchestrator {
		return ladoOrchestratorSection, nil
	}
	return ladoWorkerSection, nil
}

func (s *recordingLadoSource) asked() []ladoSectionCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ladoSectionCall(nil), s.calls...)
}

func TestLadoPromptSectionComesFromTheSource(t *testing.T) {
	st := newFakeStore()
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})
	source := &recordingLadoSource{}
	m.SetLadoPrompt(source.section)
	orchestrator, err := m.buildSystemPrompt(context.Background(), domain.KindOrchestrator, "mer", "mer-1", "", false)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}
	if !strings.Contains(orchestrator, ladoOrchestratorSection) || strings.Contains(orchestrator, ladoWorkerSection) {
		t.Fatalf("orchestrator prompt lacks its own LADO section:\n%s", orchestrator)
	}
	worker, err := m.buildSystemPrompt(context.Background(), domain.KindWorker, "mer", "mer-2", "", false)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}
	if !strings.Contains(worker, ladoWorkerSection) || strings.Contains(worker, ladoOrchestratorSection) {
		t.Fatalf("worker prompt lacks its own LADO section:\n%s", worker)
	}
	if _, err := m.buildSystemPrompt(context.Background(), domain.KindWorker, "", "scratch-1", "", false); err != nil {
		t.Fatalf("buildSystemPrompt without a project: %v", err)
	}
	want := []ladoSectionCall{{domain.KindOrchestrator, "mer", "mer-1"}, {domain.KindWorker, "mer", "mer-2"}}
	if got := source.asked(); !reflect.DeepEqual(got, want) {
		t.Fatalf("asked %+v, want %+v (never for a session without a project)", got, want)
	}
	m.SetLadoPrompt(func(context.Context, domain.SessionKind, domain.ProjectID, domain.SessionID) (string, error) {
		return "", errors.New("settings unreadable")
	})
	for _, kind := range []domain.SessionKind{domain.KindOrchestrator, domain.KindWorker} {
		if _, err := m.buildSystemPrompt(context.Background(), kind, "mer", "mer-1", "", false); err == nil {
			t.Fatalf("an unreadable LADO source must not silently drop the %s section", kind)
		}
	}
}

// TestWorkerSystemPromptAsksForTheLadoSection covers the worker paths LADO
// uses: the Chat spawn of an execution and the rebuild when it restores.
func TestWorkerSystemPromptAsksForTheLadoSection(t *testing.T) {
	launcher := &recordingLauncher{}
	m, _, _ := newChatManager(launcher)
	source := &recordingLadoSource{}
	m.SetLadoPrompt(source.section)

	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Prompt: "implement the approved plan", RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(launcher.started) != 1 || !strings.Contains(launcher.started[0].SystemPrompt, ladoWorkerSection) {
		t.Fatalf("Chat worker started with %d controllers; system prompt lacks the LADO section", len(launcher.started))
	}
	if want := []ladoSectionCall{{domain.KindWorker, chatTestProject, rec.ID}}; !reflect.DeepEqual(source.asked(), want) {
		t.Fatalf("spawn asked %+v, want %+v", source.asked(), want)
	}

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["mer-7"] = domain.SessionRecord{
		ID: "mer-7", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-7", Branch: "b", AgentSessionID: "agent-x"},
	}
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	restorer := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: t.TempDir(), LookPath: lookPath})
	restored := &recordingLadoSource{}
	restorer.SetLadoPrompt(restored.section)
	if _, err := restorer.RestoreWithMode(context.Background(), "mer-7"); err != nil {
		t.Fatalf("RestoreWithMode: %v", err)
	}
	if !strings.Contains(agent.lastRestore.SystemPrompt, ladoWorkerSection) {
		t.Fatalf("restore system prompt lacks the LADO section:\n%s", agent.lastRestore.SystemPrompt)
	}
	if want := []ladoSectionCall{{domain.KindWorker, "mer", "mer-7"}}; !reflect.DeepEqual(restored.asked(), want) {
		t.Fatalf("restore asked %+v, want %+v", restored.asked(), want)
	}
}

func TestSwitchAgentAsksTheLadoGate(t *testing.T) {
	manager, store, _ := newSwitchTestManager(t, &fakeRestartRuntime{fakeRuntime: &fakeRuntime{}})
	refusal := errors.New("refused by LADO")
	gate := &recordingWorkGate{executionErr: refusal}
	manager.SetWorkGate(gate)

	_, err := manager.SwitchAgent(context.Background(), "proj-1", SwitchAgentConfig{
		TargetHarness: domain.HarnessCodex, IdempotencyKey: "lado-execution",
	})
	if !errors.Is(err, refusal) {
		t.Fatalf("SwitchAgent error = %v, want the gate's refusal", err)
	}
	if want := []domain.SessionID{"proj-1"}; !reflect.DeepEqual(gate.changes, want) {
		t.Fatalf("gate saw %v, want %v", gate.changes, want)
	}
	if active, found, err := store.GetActiveAgentSwitch(context.Background(), "proj-1"); err != nil || found {
		t.Fatalf("active switch after a refusal = (%+v, %v, %v), want none", active, found, err)
	}
	if harness := store.sessions["proj-1"].Harness; harness != domain.HarnessClaudeCode {
		t.Fatalf("harness after a refusal = %q, want claude-code", harness)
	}
}

func TestStartInterfaceTransitionAsksTheLadoGate(t *testing.T) {
	manager, store, _, _, log := newTransitionManager(t, domain.SessionModeTUI)
	refusal := errors.New("refused by LADO")
	gate := &recordingWorkGate{executionErr: refusal}
	manager.SetWorkGate(gate)

	_, err := manager.StartInterfaceTransition(context.Background(), "session-1", domain.SessionModeChat,
		domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if !errors.Is(err, refusal) {
		t.Fatalf("StartInterfaceTransition error = %v, want the gate's refusal", err)
	}
	if want := []domain.SessionID{"session-1"}; !reflect.DeepEqual(gate.changes, want) {
		t.Fatalf("gate saw %v, want %v", gate.changes, want)
	}
	store.mu.Lock()
	transitions := len(store.transitions)
	store.mu.Unlock()
	if transitions != 0 || len(*log) != 0 {
		t.Fatalf("after a refusal: %d transitions, steps %v; want none", transitions, *log)
	}
	if mode := store.sessions["session-1"].Mode; mode != domain.SessionModeTUI {
		t.Fatalf("mode after a refusal = %q, want tui", mode)
	}
}

// LADO's dispatcher resolves a worker's base through the session manager's
// workspace, as a spawn does.
func TestResolveDefaultBranchAsksTheWorkspace(t *testing.T) {
	st := newFakeStore()
	target := ports.WorkspaceDefaultBranch{Remote: "upstream", Branch: "release", BaseRef: "refs/remotes/upstream/release"}
	ws := &fakeWorkspace{resolved: map[string]ports.WorkspaceDefaultBranch{"/work/app": target}}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}})

	got, err := m.ResolveDefaultBranch(context.Background(), "/work/app", "upstream/release")
	if err != nil || got != target {
		t.Fatalf("ResolveDefaultBranch = %+v, %v; want %+v", got, err, target)
	}
	if want := []resolveDefaultBranchCall{{repoPath: "/work/app", configuredBranch: "upstream/release"}}; !reflect.DeepEqual(ws.resolves, want) {
		t.Fatalf("workspace asked %+v, want %+v", ws.resolves, want)
	}

	// A workspace that cannot resolve branches says so.
	plain := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: struct{ ports.Workspace }{ws}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}})
	if _, err := plain.ResolveDefaultBranch(context.Background(), "/work/app", ""); !errors.Is(err, ports.ErrWorkspaceDefaultBranchUnresolved) {
		t.Fatalf("ResolveDefaultBranch without the capability = %v, want ErrWorkspaceDefaultBranchUnresolved", err)
	}
}

// A person's restore and resume of a session ask the LADO gate first, and a
// refusal changes nothing.
func TestRestoreAsksTheLadoGate(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["mer-7"] = domain.SessionRecord{
		ID: "mer-7", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-7", Branch: "b", AgentSessionID: "agent-x"},
	}
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: t.TempDir(), LookPath: lookPath})
	refusal := errors.New("refused by LADO")
	gate := &recordingWorkGate{restoreErr: refusal}
	m.SetWorkGate(gate)

	if _, err := m.RestoreWithMode(context.Background(), "mer-7"); !errors.Is(err, refusal) {
		t.Fatalf("RestoreWithMode error = %v, want the gate's refusal", err)
	}
	if _, err := m.ResumeAgentWithMode(context.Background(), "mer-7"); !errors.Is(err, refusal) {
		t.Fatalf("ResumeAgentWithMode error = %v, want the gate's refusal", err)
	}
	if want := []domain.SessionID{"mer-7", "mer-7"}; !reflect.DeepEqual(gate.restores, want) {
		t.Fatalf("gate saw %v, want %v", gate.restores, want)
	}
	if agent.restoreCalls != 0 || !st.sessions["mer-7"].IsTerminated {
		t.Fatalf("after the refusals: %d agent restores, terminated %v; want none and still terminated", agent.restoreCalls, st.sessions["mer-7"].IsTerminated)
	}

	// The gate lets other sessions restore.
	gate.restoreErr = nil
	if _, err := m.RestoreWithMode(context.Background(), "mer-7"); err != nil || agent.restoreCalls != 1 {
		t.Fatalf("RestoreWithMode = %v after %d agent restores, want a restore", err, agent.restoreCalls)
	}
}
