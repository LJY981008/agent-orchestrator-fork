// Package probe measures, with the real binaries, the two delivery paths LADO's
// local setup is built on (step 7 plan, decisions b and d):
//
//   - Claude Code started with CLAUDE_CONFIG_DIR=<session home> and
//     CLAUDE_SECURESTORAGE_CONFIG_DIR=<the real Claude folder> keeps the
//     computer's own login and reads the home's settings, hooks, skills,
//     agents, commands, MCP servers and CLAUDE.md, in print mode and through
//     AO's packaged ACP adapter (Chat).
//   - Codex started with a CODEX_HOME takes `-c mcp_servers.<name>=<table>` as
//     its session-flags layer, above the home's config.toml.
//
// The tests run only with AO_LADO_SETUP_PROBE=1 and skip otherwise, so CI never
// looks for the binaries. Once asked for, a missing binary, build or login
// fails. They never write the real tool folders: Claude runs in a temp home
// and reaches the real login only through CLAUDE_SECURESTORAGE_CONFIG_DIR.
package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	probeSkill   = "lado-probe-skill"
	probeCommand = "lado-probe-cmd"
	probeAgent   = "lado-probe-agent"
	probeStyle   = "lado-probe-style"
	probeMCP     = "lado-probe-mcp"
)

func TestDeliveryProbeClaudeTUI(t *testing.T) {
	requireProbe(t)
	claude := claudeBinary(t)
	lab := newClaudeLab(t)
	t.Logf("lab home %s, %s=%q", lab.home, claudeSecureStorageEnv, lab.secure)

	// The login comes through the folder variable, and only through it.
	status := lab.authStatus(t, claude, lab.env(true))
	t.Logf("auth status with %s: keys %v; loggedIn=%v authMethod=%v apiProvider=%v subscriptionType=%v",
		claudeSecureStorageEnv, sortedKeys(status), status["loggedIn"], status["authMethod"], status["apiProvider"], status["subscriptionType"])
	if status["loggedIn"] != true {
		t.Fatalf("with %s=%q the home is not logged in (authMethod %v)", claudeSecureStorageEnv, lab.secure, status["authMethod"])
	}
	control := lab.authStatus(t, claude, lab.env(false))
	t.Logf("auth status without %s: keys %v; loggedIn=%v authMethod=%v",
		claudeSecureStorageEnv, sortedKeys(control), control["loggedIn"], control["authMethod"])
	if control["loggedIn"] != false {
		t.Fatalf("without %s the home is still logged in (authMethod %v): the variable is not what carries the login",
			claudeSecureStorageEnv, control["authMethod"])
	}

	globalConfig := filepath.Join(lab.home, ".claude.json")
	configBefore := fileSHA256(t, globalConfig)
	events := lab.printTurn(t, claude)
	t.Logf("the turn rewrote the home's .claude.json: %v", fileSHA256(t, globalConfig) != configBefore)
	init := findEvent(events, "system", "init")
	if init == nil {
		t.Fatalf("no system/init event among %d events", len(events))
	}
	t.Logf("init keys: %v", sortedKeys(init))
	t.Logf("init: model=%v permissionMode=%v apiKeySource=%v output_style=%v claude_code_version=%v memory_paths=%v",
		init["model"], init["permissionMode"], init["apiKeySource"], init["output_style"], init["claude_code_version"], init["memory_paths"])
	for _, want := range []struct {
		name  string
		lists []string
	}{
		{probeSkill, []string{"skills", "slash_commands"}},
		{probeCommand, []string{"slash_commands"}},
		{probeAgent, []string{"agents"}},
		{probeMCP, []string{"mcp_servers"}},
	} {
		found := listsHolding(init, want.name)
		t.Logf("init lists holding %s: %v", want.name, found)
		ok := false
		for _, list := range want.lists {
			ok = ok || contains(found, list)
		}
		if !ok {
			t.Errorf("init lists none of %v holding %s; it has %v", want.lists, want.name, sortedKeys(init))
		}
	}
	if servers, ok := init["mcp_servers"].([]any); ok {
		for _, server := range servers {
			if itemName(server) == probeMCP {
				t.Logf("init mcp_servers entry: %v", server)
			}
		}
	}
	if found := listsHolding(init, probeStyle); len(found) > 0 {
		t.Logf("output style %s is listed under %v", probeStyle, found)
	} else {
		t.Logf("output style %s is not in the init event (output_style=%v)", probeStyle, init["output_style"])
	}
	for _, event := range events {
		if subtype, _ := event["subtype"].(string); event["type"] == "system" && strings.HasPrefix(subtype, "hook") {
			t.Logf("hook event: subtype=%s hook_event=%v hook_name=%v", subtype, event["hook_event"], event["hook_name"])
		}
	}

	result := findEvent(events, "result", "")
	if result == nil {
		t.Fatalf("no result event among %d events", len(events))
	}
	text, _ := result["result"].(string)
	t.Logf("result: subtype=%v is_error=%v num_turns=%v text=%q", result["subtype"], result["is_error"], result["num_turns"], text)
	if !strings.Contains(text, lab.marker) {
		t.Errorf("the answer %q lacks %s: the home's CLAUDE.md was not read", text, lab.marker)
	}

	if _, err := os.Stat(lab.hookRan); err != nil {
		t.Errorf("the home's SessionStart hook did not run: %v", err)
	}

	sessionID, _ := init["session_id"].(string)
	transcript := lab.findTranscript(t, sessionID)
	if transcript == "" {
		t.Errorf("no transcript %s.jsonl under %s: the run did not write through the projects link", sessionID, filepath.Join(lab.real, "projects"))
	} else {
		t.Logf("transcript written through the link: %s", strings.TrimPrefix(transcript, lab.root))
		realDir, _ := realClaudeDir(t)
		if _, err := os.Lstat(filepath.Join(realDir, "projects", filepath.Base(filepath.Dir(transcript)))); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the real folder has a projects entry for the lab's working tree (err %v)", err)
		}
	}
	lab.stateLinksIntact(t)

	if _, err := os.Lstat(filepath.Join(lab.home, ".credentials.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the home has a .credentials.json (err %v): the login was written into the home", err)
	}
	if lab.secure != "" {
		info, err := os.Lstat(filepath.Join(lab.secure, ".credentials.json"))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			t.Logf("the real folder has no .credentials.json")
		case err != nil:
			t.Errorf("stat the real .credentials.json: %v", err)
		case !info.Mode().IsRegular():
			t.Errorf("the real .credentials.json is no longer a regular file: %v", info.Mode())
		default:
			t.Logf("the real .credentials.json is still a regular file")
		}
	}
	lab.logHomeEntries(t)
}

func TestDeliveryProbeClaudeChat(t *testing.T) {
	requireProbe(t)
	claude := claudeBinary(t)
	node, entry := acpRuntime(t)
	lab := newClaudeLab(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	peer := startPeer(t, lab.work, lab.env(true, "CLAUDE_CODE_EXECUTABLE="+claude), true, node, entry)
	var initialized map[string]any
	peer.call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo":      map[string]any{"name": "agent-orchestrator", "title": "Agent Orchestrator", "version": "0.1.0"},
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
	}, &initialized)
	t.Logf("initialize: protocolVersion=%v agentInfo=%v authMethods=%d", initialized["protocolVersion"], initialized["agentInfo"], lenOf(initialized["authMethods"]))
	var session map[string]any
	peer.call(ctx, "session/new", map[string]any{"cwd": lab.work, "mcpServers": []any{}}, &session)
	sessionID, _ := session["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("session/new returned no sessionId: keys %v", sortedKeys(session))
	}
	t.Logf("session/new: keys %v", sortedKeys(session))

	waitCtx, waitCancel := context.WithTimeout(ctx, 60*time.Second)
	defer waitCancel()
	var names []string
	for !contains(names, probeCommand) {
		frame, ok := peer.waitNotification(waitCtx, isCommandsUpdate)
		if !ok {
			t.Fatalf("no available_commands_update listing %s within 60 s (last list %v)\nstderr: %s", probeCommand, names, peer.stderr.String())
		}
		var params struct {
			SessionID string `json:"sessionId"`
			Update    struct {
				SessionUpdate     string           `json:"sessionUpdate"`
				AvailableCommands []map[string]any `json:"availableCommands"`
			} `json:"update"`
		}
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			t.Fatalf("decode session/update: %v: %s", err, frame.Params)
		}
		names = names[:0]
		for _, command := range params.Update.AvailableCommands {
			name, _ := command["name"].(string)
			names = append(names, name)
			if name == probeCommand || name == probeSkill {
				t.Logf("available command %s: %v", name, command)
			}
		}
		t.Logf("session/update for %s: available_commands_update with %d commands; command keys %v",
			params.SessionID, len(names), commandKeys(params.Update.AvailableCommands))
	}
	if contains(names, probeSkill) {
		t.Logf("the skill %s is listed as a command too", probeSkill)
	} else {
		t.Logf("the skill %s is not among the available commands", probeSkill)
	}
}

func TestDeliveryProbeCodex(t *testing.T) {
	requireProbe(t)
	codex := codexBinary(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "codex")
	userConfig := filepath.Join(home, "config.toml")
	writeFile(t, userConfig, "[mcp_servers.lado_file]\ncommand = \"true\"\n", 0o600)
	work := filepath.Join(root, "work")
	mkdir(t, work)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	override := `mcp_servers.lado_probe={command="true",args=[]}`
	peer := startPeer(t, work, append(cleanEnviron(), "CODEX_HOME="+home), false, codex, "-c", override, "app-server")
	yes := true
	var initialized map[string]any
	peer.call(ctx, codexproto.MethodInitialize, codexproto.InitializeParams{
		ClientInfo:   codexproto.ClientInfo{Name: "agent-orchestrator", Version: "0.1.0"},
		Capabilities: &codexproto.InitializeCapabilities{ExperimentalAPI: &yes},
	}, &initialized)
	t.Logf("initialize: keys %v", sortedKeys(initialized))
	peer.notify(codexproto.MethodInitialized, nil)

	var read struct {
		Config  map[string]any                            `json:"config"`
		Layers  []codexproto.ConfigLayer                  `json:"layers"`
		Origins map[string]codexproto.ConfigLayerMetadata `json:"origins"`
	}
	peer.call(ctx, codexproto.MethodConfigRead, codexproto.ConfigReadParams{IncludeLayers: &yes}, &read)
	for _, layer := range read.Layers {
		t.Logf("layer %s file=%s version=%s disabled=%v", layer.Name.Type, deref(layer.Name.File), layer.Version, layer.DisabledReason != nil)
	}

	for _, want := range []struct {
		server string
		layer  codexproto.ConfigLayerSourceType
		file   string
	}{
		{"lado_probe", codexproto.ConfigLayerSourceTypeSessionFlags, ""},
		{"lado_file", codexproto.ConfigLayerSourceTypeUser, userConfig},
	} {
		prefix := "mcp_servers." + want.server
		keys := originsUnder(read.Origins, prefix)
		if len(keys) == 0 {
			t.Errorf("config/read has no origin for %s; mcp_servers origins: %v", prefix, originsUnder(read.Origins, "mcp_servers"))
			continue
		}
		for _, key := range keys {
			meta := read.Origins[key]
			t.Logf("origin %s: %s file=%s version=%s", key, meta.Name.Type, deref(meta.Name.File), meta.Version)
			if meta.Name.Type != want.layer {
				t.Errorf("origin of %s is %s, want %s", key, meta.Name.Type, want.layer)
			}
			if want.file != "" && filepath.Clean(deref(meta.Name.File)) != want.file {
				t.Errorf("origin file of %s is %s, want %s", key, deref(meta.Name.File), want.file)
			}
		}
	}
	servers, _ := read.Config["mcp_servers"].(map[string]any)
	t.Logf("effective mcp_servers: %v", servers)
	if probe, _ := servers["lado_probe"].(map[string]any); probe["command"] != "true" {
		t.Errorf("the effective config lacks the -c server: %v", servers["lado_probe"])
	}
}

// printTurn runs one print-mode turn in the lab and returns its stream-json events.
func (l claudeLab) printTurn(t *testing.T, claude string) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, claude, "-p", "--model", "haiku", "--max-turns", "1",
		"--output-format", "stream-json", "--verbose", "What is the probe word? Reply with the word only.")
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.Dir = l.work
	cmd.Env = l.env(true)
	cmd.WaitDelay = 5 * time.Second
	var stdout bytes.Buffer
	stderr := &tailBuffer{limit: 16 << 10}
	cmd.Stdout, cmd.Stderr = &stdout, stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("claude -p: %v\nstdout: %s\nstderr: %s", err, lastBytes(stdout.Bytes(), 4<<10), stderr.String())
	}
	var events []map[string]any
	for _, line := range bytes.Split(stdout.Bytes(), []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		event := map[string]any{}
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("claude -p printed a line that is not JSON: %v: %s", err, line)
		}
		events = append(events, event)
	}
	return events
}

// findTranscript returns the session's transcript under the stand-in real
// folder, written through the home's projects link.
func (l claudeLab) findTranscript(t *testing.T, sessionID string) string {
	t.Helper()
	if sessionID == "" {
		return ""
	}
	var found string
	err := filepath.WalkDir(filepath.Join(l.real, "projects"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == sessionID+".jsonl" {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the stand-in projects folder: %v", err)
	}
	return found
}

// logHomeEntries records what Claude added to the home beside the probe's own
// entries, for the step that builds homes.
func (l claudeLab) logHomeEntries(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(l.home)
	if err != nil {
		t.Fatal(err)
	}
	ours := map[string]bool{"CLAUDE.md": true, "settings.json": true, ".claude.json": true, "skills": true,
		"agents": true, "commands": true, "output-styles": true, claudeHistoryFile: true}
	for _, name := range claudeStateEntries {
		ours[name] = true
	}
	var added []string
	for _, entry := range entries {
		if !ours[entry.Name()] {
			added = append(added, entry.Name())
		}
	}
	t.Logf("entries Claude added to the home: %v", added)
}

func findEvent(events []map[string]any, kind, subtype string) map[string]any {
	for _, event := range events {
		if event["type"] != kind {
			continue
		}
		if subtype == "" || event["subtype"] == subtype {
			return event
		}
	}
	return nil
}

func isCommandsUpdate(frame rpcFrame) bool {
	if frame.Method != "session/update" {
		return false
	}
	var params struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
		} `json:"update"`
	}
	return json.Unmarshal(frame.Params, &params) == nil && params.Update.SessionUpdate == "available_commands_update"
}

func commandKeys(commands []map[string]any) []string {
	seen := map[string]bool{}
	for _, command := range commands {
		for key := range command {
			seen[key] = true
		}
	}
	return sortedKeys(seen)
}

func originsUnder(origins map[string]codexproto.ConfigLayerMetadata, prefix string) []string {
	var keys []string
	for _, key := range sortedKeys(origins) {
		if key == prefix || strings.HasPrefix(key, prefix+".") {
			keys = append(keys, key)
		}
	}
	return keys
}

func deref(path *codexproto.AbsolutePathBuf) string {
	if path == nil {
		return ""
	}
	return string(*path)
}

func lenOf(value any) int {
	items, _ := value.([]any)
	return len(items)
}

func lastBytes(data []byte, n int) []byte {
	if len(data) > n {
		return data[len(data)-n:]
	}
	return data
}
