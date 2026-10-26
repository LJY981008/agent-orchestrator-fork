package ladosetup

// What runs Claude Code or Codex now (plan decision h). Adoption and restore
// move files those tools read, so neither starts while an AO session of the
// tool is live or another Claude Code or Codex process runs: Windows locks
// open files, and a running session would lose its setup mid-run.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// LiveSession is an AO session of Claude Code or Codex that has not ended.
type LiveSession struct {
	ID        domain.SessionID
	ProjectID domain.ProjectID
	Tool      domain.LadoSetupTool
}

// ToolProcess is a Claude Code or Codex process on the computer.
type ToolProcess struct {
	procmem.Process
	Tool domain.LadoSetupTool
}

// Running is what runs Claude Code or Codex now.
type Running struct {
	Sessions  []LiveSession
	Processes []ToolProcess
	// ProcessesIgnored means DevIgnoreProcesses left the processes out.
	ProcessesIgnored bool
}

// processMarkers are command-line parts that mark a Claude Code or Codex
// process started through Node: the npm packages and the ACP adapter AO's
// Chat sessions run Claude through.
var processMarkers = []struct {
	text string
	tool domain.LadoSetupTool
}{
	{"@anthropic-ai/claude-code", domain.LadoSetupToolClaudeCode},
	{"claude-agent-acp", domain.LadoSetupToolClaudeCode},
	{"@openai/codex", domain.LadoSetupToolCodex},
}

// RunningTools lists the live AO sessions of Claude Code and Codex and the
// Claude Code and Codex processes other than the daemon, sessions by id and
// processes by PID.
func (s *Service) RunningTools(ctx context.Context) (Running, error) {
	if s.deps.Sessions == nil {
		return Running{}, errors.New("LADO setup has no session list")
	}
	records, err := s.deps.Sessions.ListAllSessions(ctx)
	if err != nil {
		return Running{}, fmt.Errorf("list the sessions: %w", err)
	}
	var running Running
	for _, rec := range records {
		tool := domain.LadoSetupTool(rec.Harness)
		if rec.IsTerminated || !slices.Contains(setupTools, tool) {
			continue
		}
		running.Sessions = append(running.Sessions, LiveSession{ID: rec.ID, ProjectID: rec.ProjectID, Tool: tool})
	}
	slices.SortFunc(running.Sessions, func(a, b LiveSession) int { return strings.Compare(string(a.ID), string(b.ID)) })
	if s.deps.DevIgnoreProcesses {
		running.ProcessesIgnored = true
		return running, nil
	}
	procs, err := s.deps.Processes(ctx)
	if err != nil {
		return Running{}, fmt.Errorf("list the processes: %w", err)
	}
	self := os.Getpid()
	for _, p := range procs {
		if tool := commandTool(p.Command); tool != "" && p.PID != self {
			running.Processes = append(running.Processes, ToolProcess{Process: p, Tool: tool})
		}
	}
	slices.SortFunc(running.Processes, func(a, b ToolProcess) int { return cmp.Compare(a.PID, b.PID) })
	return running, nil
}

// commandTool tells which tool a command line runs: by the base name of its
// program, which the Windows process list gives alone, or by a marker. The
// names are matched exactly, so the Claude desktop app ("Claude",
// "Claude.exe") is not Claude Code.
func commandTool(command string) domain.LadoSetupTool {
	program, _, _ := strings.Cut(strings.TrimSpace(command), " ")
	switch program[strings.LastIndexAny(program, `/\`)+1:] {
	case "claude", "claude.exe":
		return domain.LadoSetupToolClaudeCode
	case "codex", "codex.exe":
		return domain.LadoSetupToolCodex
	}
	command = strings.ReplaceAll(command, `\`, "/")
	for _, m := range processMarkers {
		if strings.Contains(command, m.text) {
			return m.tool
		}
	}
	return ""
}

// snapshotProcesses lists the computer's processes through procmem.
func snapshotProcesses(ctx context.Context) ([]procmem.Process, error) {
	table, err := procmem.Snapshot(ctx, nil)
	if err != nil {
		return nil, err
	}
	return table.All(), nil
}
