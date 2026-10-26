package sessionmanager

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	claudeagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// fakeLadoHome stands in for LADO's config homes: it points a launch at home
// and records what it was asked, after checking with before.
type fakeLadoHome struct {
	home   string
	err    error
	before func(ports.LadoSetupHomeInput)
	calls  []ports.LadoSetupHomeInput
}

func (h *fakeLadoHome) PrepareClaudeHome(_ context.Context, in ports.LadoSetupHomeInput) (map[string]string, error) {
	if h.before != nil {
		h.before(in)
	}
	// The caller sets the result over this very map.
	in.Env = maps.Clone(in.Env)
	h.calls = append(h.calls, in)
	if h.err != nil {
		return nil, h.err
	}
	return map[string]string{"CLAUDE_CONFIG_DIR": h.home, "CLAUDE_SECURESTORAGE_CONFIG_DIR": "/login"}, nil
}

// trusted reports whether the .claude.json in dir trusts the workspace.
func trusted(t *testing.T, dir, workspace string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Projects map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config.Projects[filepath.ToSlash(workspace)].HasTrustDialogAccepted
}

func TestPrepareWorkspaceBuildsTheLadoHomeFirst(t *testing.T) {
	ctx := context.Background()
	claude := claudeagent.New()
	st := newFakeStore()
	st.sessions["s-1"] = domain.SessionRecord{ID: "s-1", ProjectID: "mer", ClientRequestID: "lado:lado-queue-1/1"}
	m := New(Deps{Store: st, Agents: switchTestAgents{domain.HarnessClaudeCode: claude, domain.HarnessCodex: fakeAgent{}}})
	workspace := t.TempDir()
	account := t.TempDir()
	home := t.TempDir()
	lado := &fakeLadoHome{home: home, before: func(ports.LadoSetupHomeInput) {
		if trusted(t, home, workspace) {
			t.Fatal("AO wrote the workspace's trust before LADO built the home")
		}
	}}

	// A nil hook keeps upstream behavior: the trust goes where the launch
	// environment points.
	env := map[string]string{"CLAUDE_CONFIG_DIR": account}
	if err := m.prepareWorkspace(ctx, claude, "s-1", workspace, "", "", ports.AgentConfig{}, env); err != nil {
		t.Fatalf("prepareWorkspace without a hook: %v", err)
	}
	if env["CLAUDE_CONFIG_DIR"] != account || len(env) != 1 || !trusted(t, account, workspace) {
		t.Fatalf("env %v without a hook, want it as it was and the trust in the account folder", env)
	}

	m.SetLadoSetupHome(lado)
	env = map[string]string{"CLAUDE_CONFIG_DIR": account}
	if err := m.prepareWorkspace(ctx, claude, "s-1", workspace, "", "", ports.AgentConfig{}, env); err != nil {
		t.Fatalf("prepareWorkspace: %v", err)
	}
	if len(lado.calls) != 1 || lado.calls[0].SessionID != "s-1" || lado.calls[0].ProjectID != "mer" || lado.calls[0].ClientRequestID != "lado:lado-queue-1/1" || lado.calls[0].Env["CLAUDE_CONFIG_DIR"] != account {
		t.Fatalf("hook calls %+v, want the session's project, client request and launch environment", lado.calls)
	}
	if env["CLAUDE_CONFIG_DIR"] != home || env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != "/login" {
		t.Fatalf("env %v, want the LADO home", env)
	}
	if !trusted(t, home, workspace) {
		t.Fatal("the workspace's trust is not in the home's .claude.json")
	}

	// Other agents never reach the hook.
	if err := m.prepareWorkspace(ctx, fakeAgent{}, "s-1", workspace, "", "", ports.AgentConfig{}, map[string]string{}); err != nil || len(lado.calls) != 1 {
		t.Fatalf("prepareWorkspace for Codex = %v with %d hook calls, want no call", err, len(lado.calls))
	}

	// A hook error fails the launch before anything is installed.
	other := t.TempDir()
	lado.before, lado.err = nil, errors.New("LADO_SETUP_HOME_FAILED")
	if err := m.prepareWorkspace(ctx, claude, "s-1", other, "", "", ports.AgentConfig{}, map[string]string{"CLAUDE_CONFIG_DIR": account}); !errors.Is(err, lado.err) {
		t.Fatalf("prepareWorkspace with a failing hook = %v, want its error", err)
	}
	if entries, err := os.ReadDir(other); err != nil || len(entries) != 0 {
		t.Fatalf("a failed launch installed %v (%v)", entries, err)
	}
}

func TestChatResumeBuildsTheLadoHome(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	store := sqlitetest.MustOpenAt(t, t.TempDir())
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "proj", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	created, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "proj", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		ClientRequestID: "lado:lado-queue-1/1",
		Metadata: domain.SessionMetadata{
			WorkspacePath: t.TempDir(), Branch: "ao/session",
			ProviderConversationID: "92fadfcb-3d31-4dd6-93c1-097bf823cc19", ControllerGeneration: "chat-generation",
		},
		Activity:      domain.Activity{State: domain.ActivityExited, LastActivityAt: now},
		FirstSignalAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConversation(ctx, "conversation-1", domain.ConversationScopeSession, "proj", created.ID, now); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	manager := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: claudeagent.New()}, Workspace: &fakeWorkspace{},
		Store: store, Messenger: &fakeMessenger{}, Chat: launcher,
		Lifecycle: &sqliteTransitionLifecycle{store: store},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	lado := &fakeLadoHome{home: "/state/lado/homes/s/claude", err: errors.New("LADO_SETUP_HOME_FAILED")}
	manager.SetLadoSetupHome(lado)

	// A hook error fails the resume before a controller starts.
	if _, err := manager.ResumeAgentWithMode(ctx, created.ID); !errors.Is(err, lado.err) || len(launcher.started) != 0 {
		t.Fatalf("resume with a failing hook = %v with %d controllers, want its error and none", err, len(launcher.started))
	}

	lado.err = nil
	if _, err := manager.ResumeAgentWithMode(ctx, created.ID); err != nil {
		t.Fatalf("ResumeAgentWithMode: %v", err)
	}
	if len(launcher.started) != 1 {
		t.Fatalf("started %d chat controllers, want 1", len(launcher.started))
	}
	// The controller's own environment, which the driver launches with.
	if env := launcher.started[0].Env; env["CLAUDE_CONFIG_DIR"] != lado.home || env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != "/login" {
		t.Fatalf("controller env %v, want the LADO home", env)
	}
	last := lado.calls[len(lado.calls)-1]
	if last.SessionID != created.ID || last.ProjectID != "proj" || last.ClientRequestID != "lado:lado-queue-1/1" {
		t.Fatalf("hook call %+v, want the session's project and client request", last)
	}
}

func TestSafeNativeTranscriptPathFollowsProjectsLink(t *testing.T) {
	ctx := context.Background()
	real := t.TempDir()
	transcript := filepath.Join(real, "projects", "-ws", "abc.jsonl")
	credentials := filepath.Join(real, ".credentials.json")
	for _, path := range []string{transcript, credentials} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	realTranscript, err := filepath.EvalSymlinks(transcript)
	if err != nil {
		t.Fatal(err)
	}
	// A config home, like an account folder, links projects to the folder
	// that keeps the transcripts.
	home := t.TempDir()
	if err := os.Symlink(filepath.Join(real, "projects"), filepath.Join(home, "projects")); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	if err := os.Symlink(credentials, filepath.Join(home, "linked.jsonl")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "x.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, path, configDir, want string
	}{
		{"through the home's link", filepath.Join(home, "projects", "-ws", "abc.jsonl"), home, realTranscript},
		{"the resolved transcript", transcript, home, realTranscript},
		{"inside the folder itself", transcript, real, realTranscript},
		{"elsewhere in the linked folder's parent", credentials, home, ""},
		{"a link in the home to elsewhere", filepath.Join(home, "linked.jsonl"), home, ""},
		{"outside", outside, home, ""},
	} {
		if got := safeNativeTranscriptPath(ctx, c.path, c.configDir); got != c.want {
			t.Errorf("%s: safeNativeTranscriptPath = %q, want %q", c.name, got, c.want)
		}
	}
}
