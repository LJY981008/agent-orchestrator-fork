package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladosetup"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// ladoSetupSink stands for the session manager and keeps the setup hooks.
type ladoSetupSink struct {
	home   ports.LadoSetupHome
	chat   sessionmanager.LadoChatServers
	prompt sessionmanager.LadoPromptSection
}

func (s *ladoSetupSink) SetLadoSetupHome(home ports.LadoSetupHome) { s.home = home }

func (s *ladoSetupSink) SetLadoChatServers(servers sessionmanager.LadoChatServers) { s.chat = servers }

func (s *ladoSetupSink) SetLadoPrompt(section sessionmanager.LadoPromptSection) { s.prompt = section }

// ladoCodexAgent stands for the Codex adapter and keeps LADO's -c options.
type ladoCodexAgent struct {
	ports.Agent
	overrides func(ctx context.Context, sessionID string) ([]string, error)
}

func (a *ladoCodexAgent) SetLadoOverrides(fn func(ctx context.Context, sessionID string) ([]string, error)) {
	a.overrides = fn
}

// ladoAgents resolves fixed adapters.
type ladoAgents map[domain.AgentHarness]ports.Agent

func (a ladoAgents) Agent(harness domain.AgentHarness) (ports.Agent, bool) {
	agent, ok := a[harness]
	return agent, ok
}

// The wiring gives LADO's local setup the daemon's folders and installs every
// hook before anything spawns: Claude Code config homes, Codex -c options,
// the MCP servers of Codex Chat sessions, the Codex instructions in the
// standing instructions, and the setup in the rule policy, whose ceiling the
// setup's conflicts read. The start scans the setup and removes the config
// homes of ended sessions.
func TestWireLadoInstallsTheSetupHooks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	useLadoStoredTokens(t, nil)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	store := sqlitetest.MustOpen(t)
	at := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: at}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// The computer's own tool folders, in a home of the test's.
	home := t.TempDir()
	folders := ladoToolFolders{claudeDir: filepath.Join(home, ".claude"), claudeJSON: filepath.Join(home, ".claude.json"), codexHome: filepath.Join(home, ".codex")}
	writeLadoFile(t, filepath.Join(folders.claudeDir, "NOTES.md"), "notes\n")
	// A config home a session left when it ended before the daemon stopped.
	stale := filepath.Join(cfg.StateDir, "lado", "homes", "mer-gone")
	writeLadoFile(t, filepath.Join(stale, "claude", "CLAUDE.md"), "old\n")
	sessions, codex := &ladoSetupSink{}, &ladoCodexAgent{}
	log := slog.New(slog.DiscardHandler)

	_, _, rules, _, setup := wireLadoWork(ctx, cfg, store, &recordingProjectCloner{}, ladoWorkDeps{setup: ladoSetupDeps{
		folders:  folders,
		sessions: sessions,
		agents:   ladoAgents{domain.HarnessCodex: codex},
		// Claude Code runs on this machine; the test's setup sees no program.
		processes: func(context.Context) ([]procmem.Process, error) { return nil, nil },
	}}, log)

	if setup == nil || sessions.home != ports.LadoSetupHome(setup) || sessions.chat == nil || sessions.prompt == nil || codex.overrides == nil {
		t.Fatalf("hooks: home %v (service %p), chat set %v, prompt set %v, Codex options set %v",
			sessions.home, setup, sessions.chat != nil, sessions.prompt != nil, codex.overrides != nil)
	}

	select {
	case <-startLadoSetup(ctx, setup, log):
	case <-time.After(30 * time.Second):
		t.Fatal("the start scan did not finish within 30s")
	}
	if _, err := os.Stat(stale); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the ended session's config home is still there: %v", err)
	}
	inv, err := setup.Inventory(ctx)
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	notes := slices.IndexFunc(inv.Entries, func(e domain.LadoSetupEntry) bool {
		return e.Path == filepath.Join(folders.claudeDir, "NOTES.md") && e.Status == domain.LadoSetupStatusAdoptable
	})
	if notes < 0 || !inv.ScannedAt.After(at.Add(-time.Second)) {
		t.Fatalf("the start scan did not read the daemon's Claude folder: %+v", inv.Entries)
	}

	// Setup LADO manages reaches sessions through the hooks.
	nonces := 0
	presence := func(action, subject string) (domain.LadoPresenceProof, error) {
		nonces++
		return domain.LadoPresenceProof{Nonce: fmt.Sprintf("wiring-nonce-%04d-abcdefg", nonces), Action: action, SubjectID: subject, ExpiresAt: at.Add(time.Minute), UsedAt: at}, nil
	}
	for _, c := range []ladosetup.Change{
		{Op: ladosetup.OpCreate, Tool: domain.LadoSetupToolCodex, Kind: domain.LadoSetupKindMCPServer, Name: "search", Content: json.RawMessage(`{"url":"https://search"}`)},
		{Op: ladosetup.OpCreate, Tool: domain.LadoSetupToolCodex, Kind: domain.LadoSetupKindInstructions, Name: "AGENTS.md", Content: json.RawMessage(`"Answer in Korean."`)},
		{Op: ladosetup.OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindInstructions, Name: "CLAUDE.md", Content: json.RawMessage(`"# managed\n"`)},
		{Op: ladosetup.OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: "permissions", Content: json.RawMessage(`{"defaultMode":"bypassPermissions"}`)},
	} {
		if _, err := setup.Apply(ctx, c, presence); err != nil {
			t.Fatalf("create %s %s: %v", c.Tool, c.Name, err)
		}
	}
	worker, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	args, err := codex.overrides(ctx, string(worker.ID))
	if err != nil || !slices.Equal(args, []string{"-c", `mcp_servers.search={url = "https://search"}`}) {
		t.Fatalf("Codex options for the worker = %q, %v", args, err)
	}
	if args, err := codex.overrides(ctx, "mer-reviewer-1"); err != nil || args != nil {
		t.Fatalf("Codex options for a session AO has no record of = %q, %v; want none", args, err)
	}
	servers, err := sessions.chat(ctx, worker)
	if err != nil || len(servers) != 1 || servers[0].Name != "search" {
		t.Fatalf("Codex Chat servers = %+v, %v", servers, err)
	}
	section, err := sessions.prompt(ctx, domain.KindWorker, "mer", worker.ID, domain.HarnessCodex)
	if err != nil || !strings.Contains(section, "## User instructions (managed by LADO)") || !strings.Contains(section, "Answer in Korean.") {
		t.Fatalf("standing instructions for the Codex worker = %q, %v", section, err)
	}
	claude, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	vars, err := sessions.home.PrepareClaudeHome(ctx, ports.LadoSetupHomeInput{SessionID: claude.ID, ProjectID: "mer", Env: map[string]string{}})
	if err != nil {
		t.Fatalf("PrepareClaudeHome: %v", err)
	}
	configDir := vars["CLAUDE_CONFIG_DIR"]
	if data, err := os.ReadFile(filepath.Join(configDir, "CLAUDE.md")); err != nil || string(data) != "# managed\n" ||
		!strings.HasPrefix(configDir, filepath.Join(cfg.StateDir, "lado", "homes")) || vars["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != folders.claudeDir {
		t.Fatalf("Claude Code home %v holds CLAUDE.md %q (%v)", vars, data, err)
	}

	// The rule policy resolves the setup, and the setup reads the rules'
	// permission ceiling.
	view, err := rules.Effective(ctx, ladopolicy.EffectiveInput{ProjectID: "mer"})
	if err != nil || view.Setup.AOSystemSHA256 == "" {
		t.Fatalf("Effective setup = %+v, %v; want the AO system layer resolved", view.Setup, err)
	}
	conflicts, err := setup.ScopeConflicts(ctx, ladosetup.ScopeConflictsInput{})
	if err != nil {
		t.Fatalf("ScopeConflicts: %v", err)
	}
	if i := slices.IndexFunc(conflicts, func(c ladosetup.Conflict) bool { return c.Code == ladosetup.ConflictDefaultModeAboveCeiling }); i < 0 ||
		conflicts[i].Details["ceiling"] != string(domain.PermissionModeAuto) {
		t.Fatalf("conflicts = %+v, want the default mode above the rules' auto ceiling", conflicts)
	}
}

func writeLadoFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
