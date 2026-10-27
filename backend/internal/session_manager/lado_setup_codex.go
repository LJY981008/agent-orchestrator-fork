package sessionmanager

// LADO fork: the MCP servers LADO manages reach a Codex Chat session among the
// tool servers AO passes to the Chat driver, next to its own ao server
// (docs/LJY-PRD/HARNESS.md §9). A Codex terminal session gets them as -c
// options from the codex adapter, and a Claude Code session from its config
// home (lado_setup_home.go).

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LadoChatServers returns the MCP servers LADO manages for a Codex session.
type LadoChatServers func(ctx context.Context, rec domain.SessionRecord) ([]ports.ChatMCPServerConfig, error)

// SetLadoChatServers installs LADO's servers for Codex Chat sessions. The
// daemon calls it before anything can spawn; nil keeps upstream behavior.
func (m *Manager) SetLadoChatServers(servers LadoChatServers) {
	m.ladoChatMCP = servers
}

// chatMCPServers is the tool servers a Chat session on harness gets: AO's
// own, then for Codex the ones LADO manages, except one named ao, which the
// adoption's conflict check blocks too. An error fails the launch rather than
// start the session without its managed servers.
func (m *Manager) chatMCPServers(ctx context.Context, rec domain.SessionRecord, harness domain.AgentHarness, env map[string]string) ([]ports.ChatMCPServerConfig, error) {
	servers := m.aoMCPServers(harness, env)
	if m.ladoChatMCP == nil || harness != domain.HarnessCodex {
		return servers, nil
	}
	managed, err := m.ladoChatMCP(ctx, rec)
	if err != nil {
		return nil, err
	}
	for _, server := range managed {
		if strings.EqualFold(server.Name, "ao") {
			m.logger.Warn("LADO leaves out a managed MCP server named like AO's own", "sessionID", rec.ID, "server", server.Name)
			continue
		}
		servers = append(servers, server)
	}
	return servers, nil
}
