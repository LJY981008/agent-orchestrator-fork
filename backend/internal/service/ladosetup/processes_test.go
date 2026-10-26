package ladosetup

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// Plan decision h: adoption and restore wait for the live AO sessions of
// Claude Code and Codex and for every other Claude Code or Codex process,
// found by program name or command-line marker. The daemon is not one of
// them, ended sessions are gone, and a lab can leave the processes out.
func TestRunningToolsAreFound(t *testing.T) {
	ctx := context.Background()
	sessions := fakeSessions{
		{ID: "app-2", ProjectID: "app", Harness: domain.HarnessCodex},
		{ID: "app-1", ProjectID: "app", Harness: domain.HarnessClaudeCode},
		{ID: "app-3", ProjectID: "app", Harness: domain.HarnessClaudeCode, IsTerminated: true},
		{ID: "app-4", ProjectID: "app", Harness: domain.AgentHarness("opencode")},
	}
	procs := []procmem.Process{
		{PID: 30, Command: "node /usr/lib/node_modules/@openai/codex/bin/codex.js"},
		{PID: 10, Command: "/home/me/.local/bin/claude --resume"},
		{PID: 11, Command: "claude.exe"},
		{PID: 12, Command: "codex app-server"},
		{PID: 13, Command: "codex.exe"},
		{PID: 14, Command: "node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"},
		{PID: 15, Command: "/opt/ao/acp-runtime/node/bin/node /opt/ao/acp-runtime/node_modules/@agentclientprotocol/claude-agent-acp/dist/index.js"},
		{PID: 16, Command: `node C:\Users\me\AppData\Roaming\npm\node_modules\@openai\codex\bin\codex.js`},
		{PID: 20, Command: "/Applications/Claude.app/Contents/MacOS/Claude"},
		{PID: 21, Command: "Claude.exe"},
		{PID: 22, Command: "vim /home/me/.claude/settings.json"},
		{PID: 23, Command: "grep claude"},
		{PID: os.Getpid(), Command: "claude daemon"},
	}
	deps := newFakeHome(t).deps()
	deps.Sessions = sessions
	deps.Processes = func(context.Context) ([]procmem.Process, error) { return procs, nil }

	running, err := New(deps).RunningTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantSessions := []LiveSession{
		{ID: "app-1", ProjectID: "app", Tool: domain.LadoSetupToolClaudeCode},
		{ID: "app-2", ProjectID: "app", Tool: domain.LadoSetupToolCodex},
	}
	if !slices.Equal(running.Sessions, wantSessions) {
		t.Fatalf("sessions %+v, want %+v", running.Sessions, wantSessions)
	}
	claude, codex := domain.LadoSetupToolClaudeCode, domain.LadoSetupToolCodex
	wantTools := map[int]domain.LadoSetupTool{10: claude, 11: claude, 12: codex, 13: codex, 14: claude, 15: claude, 16: codex, 30: codex}
	var pids []int
	for _, p := range running.Processes {
		pids = append(pids, p.PID)
		if p.Tool != wantTools[p.PID] {
			t.Fatalf("process %d (%s) is %q, want %q", p.PID, p.Command, p.Tool, wantTools[p.PID])
		}
	}
	if want := []int{10, 11, 12, 13, 14, 15, 16, 30}; !slices.Equal(pids, want) {
		t.Fatalf("processes %v, want %v", pids, want)
	}
	if running.ProcessesIgnored {
		t.Fatal("processes marked ignored")
	}

	t.Run("a lab leaves the processes out and says so", func(t *testing.T) {
		deps := deps
		deps.DevIgnoreProcesses = true
		deps.Processes = func(context.Context) ([]procmem.Process, error) {
			t.Fatal("the processes were listed")
			return nil, nil
		}
		running, err := New(deps).RunningTools(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !running.ProcessesIgnored || len(running.Processes) != 0 || !slices.Equal(running.Sessions, wantSessions) {
			t.Fatalf("running %+v, want the sessions and ignored processes", running)
		}
	})

	t.Run("without a session list there is no answer", func(t *testing.T) {
		deps := deps
		deps.Sessions = nil
		if _, err := New(deps).RunningTools(ctx); err == nil {
			t.Fatal("RunningTools answered without a session list")
		}
	})
}
