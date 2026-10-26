package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// adoptedDocs is an MCP server LADO adopted from config.toml, with a key only
// Codex reads.
var adoptedDocs = ports.ChatMCPServerConfig{Name: "docs", Type: "http", URL: "https://docs", CodexConfig: map[string]any{"enabled_tools": []any{"read"}}}

// fakeLadoChatServers stands in for LADO's managed Codex servers: adoptedDocs
// and one named like AO's own server, which never reaches a session.
type fakeLadoChatServers struct {
	err   error
	asked []domain.SessionRecord
}

func (f *fakeLadoChatServers) servers(_ context.Context, rec domain.SessionRecord) ([]ports.ChatMCPServerConfig, error) {
	f.asked = append(f.asked, rec)
	if f.err != nil {
		return nil, f.err
	}
	return []ports.ChatMCPServerConfig{{Name: "AO", Type: "stdio", Command: "/bin/fake"}, adoptedDocs}, nil
}

func serverNames(servers []ports.ChatMCPServerConfig) []string {
	names := make([]string, 0, len(servers))
	for _, server := range servers {
		names = append(names, server.Name)
	}
	return names
}

func TestChatMCPListsCarryAdoptedCodexServers(t *testing.T) {
	ctx := context.Background()
	launcher := &recordingLauncher{}
	mgr, _, _ := newChatManager(launcher)
	mgr.executable = func() (string, error) { return "/opt/ao/bin/ao", nil }
	lado := &fakeLadoChatServers{}
	mgr.SetLadoChatServers(lado.servers)
	spawn := func(harness domain.AgentHarness) (domain.SessionRecord, error) {
		rec, _, _, err := mgr.Spawn(ctx, ports.SpawnConfig{
			ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: harness, RequestedMode: domain.SessionModeChat,
		})
		return rec, err
	}

	// A Codex session gets AO's server and the adopted one when it spawns and
	// when it restores; a Claude Code session gets AO's only, as its servers
	// come from its config home.
	codex, err := spawn(domain.HarnessCodex)
	if err != nil {
		t.Fatalf("Spawn Codex: %v", err)
	}
	if _, err := mgr.Kill(ctx, codex.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, err := mgr.RestoreWithMode(ctx, codex.ID); err != nil {
		t.Fatalf("RestoreWithMode: %v", err)
	}
	if _, err := spawn(domain.HarnessClaudeCode); err != nil {
		t.Fatalf("Spawn Claude Code: %v", err)
	}
	if len(launcher.started) != 3 {
		t.Fatalf("started %d controllers, want 3", len(launcher.started))
	}
	for i, want := range [][]string{{"ao", "docs"}, {"ao", "docs"}, {"ao"}} {
		if got := serverNames(launcher.started[i].MCPServers); !reflect.DeepEqual(got, want) {
			t.Fatalf("start %d has servers %v, want %v", i, got, want)
		}
	}
	if got := launcher.started[0].MCPServers[1]; !reflect.DeepEqual(got, adoptedDocs) {
		t.Fatalf("the adopted server reached the driver as %+v, want %+v", got, adoptedDocs)
	}
	if len(lado.asked) != 2 || lado.asked[0].ID != codex.ID || lado.asked[1].ID != codex.ID || lado.asked[0].ProjectID != chatTestProject {
		t.Fatalf("asked for %+v, want the Codex session at its spawn and restore", lado.asked)
	}

	// A failure fails the Codex launch before a controller starts.
	lado.err = errors.New("LADO_SETUP_CODEX_FAILED")
	if _, err := spawn(domain.HarnessCodex); !errors.Is(err, lado.err) || len(launcher.started) != 3 {
		t.Fatalf("spawn with a failing hook = %v with %d controllers, want its error and no new one", err, len(launcher.started))
	}

	// A Chat agent switch to Codex gets them for its target, whose standing
	// instructions LADO builds for Codex although the row still names the
	// source.
	manager, store, _ := newSwitchTestManager(t, &fakeRestartRuntime{fakeRuntime: &fakeRuntime{}})
	rec := store.sessions["proj-1"]
	rec.Mode = domain.SessionModeChat
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now().UTC()}
	rec.Metadata.RuntimeHandleID, rec.Metadata.RuntimeLaunchID, rec.Metadata.AgentSessionID = "", "", ""
	rec.Metadata.ProviderConversationID = "source-chat-native"
	rec.Metadata.ControllerGeneration = "source-chat-generation"
	store.sessions[rec.ID] = rec
	switcher := &switchAgentChatLauncher{recordingLauncher: &recordingLauncher{}, store: store, live: true}
	manager.chat = switcher
	manager.executable = mgr.executable
	lado.err, lado.asked = nil, nil
	manager.SetLadoChatServers(lado.servers)
	source := &recordingLadoSource{}
	manager.SetLadoPrompt(source.section)
	if _, err := switchAgentSynchronously(ctx, manager, rec.ID, SwitchAgentConfig{TargetHarness: domain.HarnessCodex, IdempotencyKey: "lado-codex"}); err != nil {
		t.Fatalf("SwitchAgent: %v", err)
	}
	if len(switcher.started) != 1 || !reflect.DeepEqual(serverNames(switcher.started[0].MCPServers), []string{"ao", "docs"}) || len(lado.asked) != 1 {
		t.Fatalf("switch target started %+v after %d asks, want AO's server and the adopted one", switcher.started, len(lado.asked))
	}
	if asked := source.asked(); len(asked) == 0 || asked[0].harness != domain.HarnessCodex {
		t.Fatalf("the target's LADO section was asked for %+v, want Codex", asked)
	}
}

// The LADO section is asked for the agent the prompt is for: the spawn's,
// before a task preparation's row names it, and else the row's.
func TestLadoPromptSectionKnowsTheHarness(t *testing.T) {
	ctx := context.Background()
	m, st, _ := newChatManager(&recordingLauncher{})
	m.workspace.(*fakeWorkspace).path = t.TempDir()
	m.runBackground = func(work func()) { work() }
	source := &recordingLadoSource{}
	m.SetLadoPrompt(source.section)
	token, err := m.PrepareTaskWorkspace(ctx, st.projects[string(chatTestProject)])
	if err != nil {
		t.Fatal(err)
	}
	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat, TaskPreparation: token,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := m.buildSystemPrompt(ctx, domain.KindWorker, chatTestProject, rec.ID, "", true); err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}
	want := []ladoSectionCall{{domain.KindWorker, chatTestProject, rec.ID, domain.HarnessCodex}, {domain.KindWorker, chatTestProject, rec.ID, domain.HarnessCodex}}
	if got := source.asked(); !reflect.DeepEqual(got, want) {
		t.Fatalf("asked %+v, want %+v", got, want)
	}

	// A terminal agent switch builds its target's prompt for the target.
	manager, _, _ := newSwitchTestManager(t, &fakeRestartRuntime{fakeRuntime: &fakeRuntime{}})
	switched := &recordingLadoSource{}
	manager.SetLadoPrompt(switched.section)
	if _, err := switchAgentSynchronously(ctx, manager, "proj-1", SwitchAgentConfig{TargetHarness: domain.HarnessCodex, IdempotencyKey: "lado-tui"}); err != nil {
		t.Fatalf("SwitchAgent: %v", err)
	}
	if asked := switched.asked(); len(asked) == 0 || asked[0].harness != domain.HarnessCodex {
		t.Fatalf("the target's LADO section was asked for %+v, want Codex", asked)
	}
}
