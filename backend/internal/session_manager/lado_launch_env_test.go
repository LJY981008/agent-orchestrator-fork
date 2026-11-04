package sessionmanager

import (
	"context"
	"testing"
	"time"

	claudeagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladochrome"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// chromeResume resumes one Claude Code Chat session whose project env turns
// Claude in Chrome on, with the Chrome source and no LADO config home.
func chromeResume(t *testing.T, clientRequestID string) map[string]string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	store := sqlitetest.MustOpenAt(t, t.TempDir())
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "proj", Path: t.TempDir(),
		Config: domain.ProjectConfig{Env: map[string]string{domain.LadoClaudeChromeEnv: "1"}},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	created, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "proj", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		ClientRequestID: clientRequestID,
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
	manager.SetLadoClaudeChrome(ladochrome.New(ladochrome.Options{DataDir: t.TempDir()}))
	if _, err := manager.ResumeAgentWithMode(ctx, created.ID); err != nil {
		t.Fatalf("ResumeAgentWithMode: %v", err)
	}
	if len(launcher.started) != 1 {
		t.Fatalf("started %d chat controllers, want 1", len(launcher.started))
	}
	return launcher.started[0].Env
}

func TestChatResumeGetsClaudeChromeWithoutASetupHome(t *testing.T) {
	if env := chromeResume(t, ""); env[domain.LadoClaudeChromeEnv] != "1" {
		t.Fatalf("chat session %s = %q, want 1", domain.LadoClaudeChromeEnv, env[domain.LadoClaudeChromeEnv])
	}
}

// The project env says 1, but LADO workers are off by default: LADO's value
// wins so an inherited variable cannot turn the browser on for them.
func TestClaudeChromeOverridesTheProjectEnvForLadoWorkers(t *testing.T) {
	if env := chromeResume(t, "lado:lado-queue-1/1"); env[domain.LadoClaudeChromeEnv] != "0" {
		t.Fatalf("LADO worker %s = %q, want 0", domain.LadoClaudeChromeEnv, env[domain.LadoClaudeChromeEnv])
	}
}

// recordingChrome answers 1 for Claude Code Chat and remembers what it saw.
type recordingChrome struct {
	harnesses []domain.AgentHarness
}

func (c *recordingChrome) LaunchEnv(rec domain.SessionRecord, harness domain.AgentHarness) (string, bool) {
	c.harnesses = append(c.harnesses, harness)
	if harness != domain.HarnessClaudeCode || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return "", false
	}
	return "1", true
}

// During a codex → Claude Code switch the stored harness is still codex; the
// value follows the agent being started.
func TestApplyLadoSetupHomeGivesClaudeChromeToTheStartingAgent(t *testing.T) {
	ctx := context.Background()
	claude := claudeagent.New()
	st := newFakeStore()
	st.sessions["s-1"] = domain.SessionRecord{ID: "s-1", ProjectID: "mer", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat}
	m := New(Deps{Store: st, Agents: switchTestAgents{domain.HarnessClaudeCode: claude, domain.HarnessCodex: fakeAgent{}}})
	chrome := &recordingChrome{}
	m.SetLadoClaudeChrome(chrome)

	env := map[string]string{"PATH": "/bin"}
	if err := m.applyLadoSetupHome(ctx, claude, "s-1", env); err != nil {
		t.Fatalf("applyLadoSetupHome: %v", err)
	}
	if env[domain.LadoClaudeChromeEnv] != "1" {
		t.Fatalf("switching session env = %v, want %s=1", env, domain.LadoClaudeChromeEnv)
	}
	if len(chrome.harnesses) != 1 || chrome.harnesses[0] != domain.HarnessClaudeCode {
		t.Fatalf("LaunchEnv harnesses = %v, want claude-code", chrome.harnesses)
	}

	// A codex agent's environment is left alone.
	codexEnv := map[string]string{"PATH": "/bin"}
	if err := m.applyLadoSetupHome(ctx, fakeAgent{}, "s-1", codexEnv); err != nil {
		t.Fatalf("applyLadoSetupHome codex: %v", err)
	}
	if _, ok := codexEnv[domain.LadoClaudeChromeEnv]; ok || len(chrome.harnesses) != 1 {
		t.Fatalf("codex env = %v (calls %v), want untouched", codexEnv, chrome.harnesses)
	}

	// Without a LADO config home a missing record never fails the launch.
	if err := m.applyLadoSetupHome(ctx, claude, "missing", map[string]string{}); err != nil {
		t.Fatalf("missing record with only the Chrome source = %v, want nil", err)
	}
}
