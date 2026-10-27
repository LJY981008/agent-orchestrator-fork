package ladosetup

// Codex delivery (plan §10, decisions c and d). Codex keeps its login under
// CODEX_HOME and no variable moves it elsewhere, so a Codex session keeps the
// real CODEX_HOME and LADO adds the setup it manages on top: each MCP server
// as a -c option of a terminal launch, which lands in Codex's sessionFlags
// layer (S01 measured it), and as a tool server of a Chat thread; the
// instructions as a section of AO's standing instructions, on both paths.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// CodeCodexFailed is Codex setup LADO could not read for a session. Its
// details name the stage, never a path or content.
const CodeCodexFailed = "LADO_SETUP_CODEX_FAILED"

// codexInstructionOrder is the order Codex reads its instruction files in:
// it uses the first one with text.
var codexInstructionOrder = []string{"AGENTS.override.md", "AGENTS.md"}

// CodexOverrides returns the -c options that give a Codex terminal session
// the MCP servers LADO manages, mcp_servers.<name>=<inline table> in name
// order: the versions a LADO execution's rule snapshot pinned, else the
// current ones that are on for the session's project (decision c). It returns
// none while LADO manages no Codex item (decision a). A failure is
// CodeCodexFailed, which fails the launch.
func (s *Service) CodexOverrides(ctx context.Context, sessionID domain.SessionID, clientRequestID string, projectID domain.ProjectID) ([]string, error) {
	servers, err := s.codexItems(ctx, sessionID, clientRequestID, projectID, domain.LadoSetupKindMCPServer)
	if err != nil {
		return nil, err
	}
	var args []string
	for _, server := range servers {
		value, err := codexServerOverride(server.Item.Name, server.content)
		if err != nil {
			return nil, s.codexFailed(sessionID, homeStageContent, err)
		}
		args = append(args, "-c", value)
	}
	return args, nil
}

// CodexChatServers returns the servers of CodexOverrides as tool servers of a
// Codex Chat thread: the transport in the fields of ports.ChatMCPServerConfig
// and the rest of each table in its CodexConfig, which the Codex app-server
// driver sends along.
func (s *Service) CodexChatServers(ctx context.Context, sessionID domain.SessionID, clientRequestID string, projectID domain.ProjectID) ([]ports.ChatMCPServerConfig, error) {
	servers, err := s.codexItems(ctx, sessionID, clientRequestID, projectID, domain.LadoSetupKindMCPServer)
	if err != nil {
		return nil, err
	}
	out := make([]ports.ChatMCPServerConfig, 0, len(servers))
	for _, server := range servers {
		chat, err := chatServer(server.Item.Name, server.content)
		if err != nil {
			return nil, s.codexFailed(sessionID, homeStageContent, err)
		}
		out = append(out, chat)
	}
	return out, nil
}

// CodexInstructions returns the instructions LADO manages for a Codex
// session, picked as Codex picks them from CODEX_HOME: the text of
// AGENTS.override.md when it has any, else that of AGENTS.md, trimmed. It is
// "" when neither is on or both are blank.
func (s *Service) CodexInstructions(ctx context.Context, sessionID domain.SessionID, clientRequestID string, projectID domain.ProjectID) (string, error) {
	files, err := s.codexItems(ctx, sessionID, clientRequestID, projectID, domain.LadoSetupKindInstructions)
	if err != nil {
		return "", err
	}
	for _, name := range codexInstructionOrder {
		for _, file := range files {
			if text := strings.TrimSpace(string(file.content)); file.Item.Name == name && text != "" {
				return text, nil
			}
		}
	}
	return "", nil
}

// codexItem is a Codex item version a session gets, with its content.
type codexItem struct {
	domain.LadoSetupItemWithVersion
	content []byte
}

// codexItems returns the Codex items of kind a session gets (sessionItems),
// in name order, with their content.
func (s *Service) codexItems(ctx context.Context, sessionID domain.SessionID, clientRequestID string, projectID domain.ProjectID, kind domain.LadoSetupKind) ([]codexItem, error) {
	if s.deps.Store == nil {
		return nil, nil
	}
	items, _, err := s.sessionItems(ctx, domain.LadoSetupToolCodex, projectID, clientRequestID)
	if err != nil {
		return nil, s.codexFailed(sessionID, homeStageItems, err)
	}
	items = slices.DeleteFunc(items, func(iv domain.LadoSetupItemWithVersion) bool { return iv.Item.Kind != kind })
	if len(items) == 0 {
		return nil, nil
	}
	if s.deps.Blobs == nil {
		return nil, s.codexFailed(sessionID, homeStageItems, errors.New("LADO setup has no blob store"))
	}
	// A snapshot's pins come in no particular order.
	slices.SortFunc(items, func(a, b domain.LadoSetupItemWithVersion) int { return strings.Compare(a.Item.Name, b.Item.Name) })
	out := make([]codexItem, 0, len(items))
	for _, iv := range items {
		data, err := s.deps.Blobs.Get(iv.Version.SHA256, maxBlobBytes)
		if err != nil {
			return nil, s.codexFailed(sessionID, homeStageContent, fmt.Errorf("read %s from its blob: %w", iv.Item.Name, err))
		}
		out = append(out, codexItem{LadoSetupItemWithVersion: iv, content: data})
	}
	return out, nil
}

// codexFailed logs why a Codex session's setup could not be read and returns
// CodeCodexFailed, which names the stage only.
func (s *Service) codexFailed(id domain.SessionID, stage string, err error) error {
	s.deps.Log.Error("LADO could not read the Codex setup of a session", "session", id, "stage", stage, "err", err)
	return apierr.New(apierr.KindInternal, CodeCodexFailed,
		"LADO could not read this session's Codex setup, so the session did not start. The daemon log says why.",
		map[string]any{"stage": stage})
}

// chatServer is an MCP server's content as a Chat tool server: a url makes
// it an http server and a command a stdio one, and every key the typed fields
// do not hold stays in CodexConfig.
func chatServer(name string, content []byte) (ports.ChatMCPServerConfig, error) {
	table, err := codexServerTable(name, content)
	if err != nil {
		return ports.ChatMCPServerConfig{}, err
	}
	server := ports.ChatMCPServerConfig{Name: name, Type: "http"}
	typed := []string{"url", "http_headers"}
	if _, http := table["url"]; !http {
		server.Type = "stdio"
		typed = []string{"command", "args", "env"}
	}
	for _, key := range typed {
		value, set := table[key]
		if !set {
			continue
		}
		ok := false
		switch key {
		case "url":
			server.URL, ok = value.(string)
		case "command":
			server.Command, ok = value.(string)
		case "args":
			server.Args, ok = stringsOf(value)
		case "env":
			server.Env, ok = stringMapOf(value)
		case "http_headers":
			server.Headers, ok = stringMapOf(value)
		}
		if !ok {
			return ports.ChatMCPServerConfig{}, fmt.Errorf("the MCP server %s has a %s Codex cannot read", name, key)
		}
	}
	if server.URL == "" && server.Command == "" {
		return ports.ChatMCPServerConfig{}, fmt.Errorf("the MCP server %s has neither a url nor a command", name)
	}
	rest := maps.Clone(table)
	for _, key := range typed {
		delete(rest, key)
	}
	if len(rest) > 0 {
		server.CodexConfig = rest
	}
	return server, nil
}

func stringsOf(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func stringMapOf(value any) (map[string]string, bool) {
	table, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(table))
	for key, item := range table {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out[key] = s
	}
	return out, true
}
