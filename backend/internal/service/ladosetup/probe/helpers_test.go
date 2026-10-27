package probe

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	probeEnv      = "AO_LADO_SETUP_PROBE"
	probeCodexEnv = "AO_LADO_SETUP_PROBE_CODEX"

	claudeConfigDirEnv     = "CLAUDE_CONFIG_DIR"
	claudeSecureStorageEnv = "CLAUDE_SECURESTORAGE_CONFIG_DIR"
)

// claudeStateEntries are the real folder's state entries a session home links
// to (plan decision b, the account linker's list without the item folders).
var claudeStateEntries = []string{
	"projects", "sessions", "session-env", "file-history", "shell-snapshots", "todos",
	"paste-cache", "tasks", "plans", "transcripts", "plugins",
}

const claudeHistoryFile = "history.jsonl"

// requireProbe skips unless the probe was asked for. Past this point a missing
// binary, build or login is a failure, never a skip.
func requireProbe(t *testing.T) {
	t.Helper()
	if os.Getenv(probeEnv) != "1" {
		t.Skipf("%s is not 1: the delivery probe runs only on request, with the real binaries", probeEnv)
	}
}

// claudeBinary is the claude on PATH, resolved through its links the way the
// ACP adapter receives it in CLAUDE_CODE_EXECUTABLE.
func claudeBinary(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("claude is not on PATH: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	t.Logf("claude: %s -> %s (%s)", path, resolved, commandVersion(t, resolved))
	return resolved
}

// codexBinary is the codex the gate helper names, else the one on PATH.
func codexBinary(t *testing.T) string {
	t.Helper()
	path := os.Getenv(probeCodexEnv)
	if path == "" {
		found, err := exec.LookPath("codex")
		if err != nil {
			t.Fatalf("codex: %s is unset and codex is not on PATH: %v", probeCodexEnv, err)
		}
		path = found
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("codex binary %s: %v", path, err)
	}
	t.Logf("codex: %s (%s)", path, commandVersion(t, path))
	return path
}

func commandVersion(t *testing.T, binary string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := aoprocess.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		t.Fatalf("%s --version: %v", binary, err)
	}
	return strings.TrimSpace(string(out))
}

// acpRuntime is AO's packaged ACP runtime (npm --prefix frontend run
// build:acp-runtime), or AO_ACP_RUNTIME_DIR like the daemon.
func acpRuntime(t *testing.T) (node, entry string) {
	t.Helper()
	dir := os.Getenv("AO_ACP_RUNTIME_DIR")
	if dir == "" {
		abs, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "frontend", "resources", "acp-runtime"))
		if err != nil {
			t.Fatal(err)
		}
		dir = abs
	}
	node = filepath.Join(dir, "node", "bin", "node")
	if runtime.GOOS == "windows" {
		node = filepath.Join(dir, "node", "node.exe")
	}
	adapter := filepath.Join(dir, "node_modules", "@agentclientprotocol", "claude-agent-acp")
	entry = filepath.Join(adapter, "dist", "index.js")
	for _, path := range []string{node, entry} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("ACP runtime: %v (run npm --prefix frontend run build:acp-runtime)", err)
		}
	}
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	readJSON(t, filepath.Join(adapter, "package.json"), &manifest)
	t.Logf("ACP runtime: node %s, %s %s at %s", commandVersion(t, node), manifest.Name, manifest.Version, entry)
	return node, entry
}

// realClaudeDir is the computer's own Claude folder as the daemon resolves
// it: CLAUDE_CONFIG_DIR when set, else ~/.claude.
func realClaudeDir(t *testing.T) (dir string, fromEnv bool) {
	t.Helper()
	if dir := os.Getenv(claudeConfigDirEnv); dir != "" {
		return dir, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home directory: %v", err)
	}
	return filepath.Join(home, ".claude"), false
}

// realClaudeJSON is the computer's own global config: inside CLAUDE_CONFIG_DIR
// when set, else ~/.claude.json.
func realClaudeJSON(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv(claudeConfigDirEnv); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home directory: %v", err)
	}
	return filepath.Join(home, ".claude.json")
}

// secureStorageDir is plan decision b's CLAUDE_SECURESTORAGE_CONFIG_DIR for the
// computer's own login: the daemon's CLAUDE_CONFIG_DIR when set, else "" on
// macOS (the Keychain item without a folder suffix) and the real folder
// elsewhere, where the login is <dir>/.credentials.json.
func secureStorageDir(t *testing.T) string {
	t.Helper()
	dir, fromEnv := realClaudeDir(t)
	if !fromEnv && runtime.GOOS == "darwin" {
		return ""
	}
	return dir
}

// claudeLab is a session home in the shape of plan decision b, over a stand-in
// for the real folder's state entries. Only the login is real: it is reached
// through CLAUDE_SECURESTORAGE_CONFIG_DIR, never copied or linked.
type claudeLab struct {
	root    string
	real    string // stand-in for the real folder's state entries
	home    string // CLAUDE_CONFIG_DIR
	work    string // the session's working tree
	hookRan string // the home's SessionStart hook creates it
	marker  string // only the home's CLAUDE.md knows it
	secure  string // CLAUDE_SECURESTORAGE_CONFIG_DIR
}

func newClaudeLab(t *testing.T) claudeLab {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lab := claudeLab{
		root:    root,
		real:    filepath.Join(root, "real"),
		home:    filepath.Join(root, "home", "claude"),
		work:    filepath.Join(root, "work"),
		hookRan: filepath.Join(root, "hook-ran"),
		marker:  "LADOPROBE-" + randomHex(t, 4),
		secure:  secureStorageDir(t),
	}
	for _, dir := range []string{lab.real, lab.home, lab.work} {
		mkdir(t, dir)
	}

	for _, name := range claudeStateEntries {
		mkdir(t, filepath.Join(lab.real, name))
	}
	writeFile(t, filepath.Join(lab.real, claudeHistoryFile), "", 0o600)
	for _, name := range append(append([]string(nil), claudeStateEntries...), claudeHistoryFile) {
		if err := os.Symlink(filepath.Join(lab.real, name), filepath.Join(lab.home, name)); err != nil {
			t.Fatalf("link state entry %s: %v", name, err)
		}
	}

	writeFile(t, filepath.Join(lab.home, "CLAUDE.md"),
		"If asked for the probe word, answer with exactly "+lab.marker+".\n", 0o600)
	settings := map[string]any{"hooks": map[string]any{"SessionStart": []any{
		map[string]any{"hooks": []any{map[string]any{
			"type": "command", "command": `touch "` + filepath.ToSlash(lab.hookRan) + `"`,
		}}},
	}}}
	writeJSON(t, filepath.Join(lab.home, "settings.json"), settings)
	writeFile(t, filepath.Join(lab.home, "skills", "lado-probe-skill", "SKILL.md"),
		"---\nname: lado-probe-skill\ndescription: LADO delivery probe skill. Never use it.\n---\n\nThis skill exists only so the probe can see that Claude Code loaded it.\n", 0o600)
	writeFile(t, filepath.Join(lab.home, "agents", "lado-probe-agent.md"),
		"---\nname: lado-probe-agent\ndescription: LADO delivery probe agent. Never use it.\n---\n\nYou exist only so the probe can see that Claude Code loaded you.\n", 0o600)
	writeFile(t, filepath.Join(lab.home, "commands", "lado-probe-cmd.md"),
		"---\ndescription: LADO delivery probe command\n---\n\nReply with the word probe.\n", 0o600)
	writeFile(t, filepath.Join(lab.home, "output-styles", "lado-probe-style.md"),
		"---\nname: lado-probe-style\ndescription: LADO delivery probe output style\n---\n\nAnswer as you normally would.\n", 0o600)
	lab.writeGlobalConfig(t)

	gitCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := aoprocess.CommandContext(gitCtx, "git",
		"-c", "user.name=probe", "-c", "user.email=probe@example.invalid", "init", "-q", lab.work).CombinedOutput()
	if err != nil {
		t.Fatalf("git init %s: %v\n%s", lab.work, err, out)
	}
	return lab
}

// writeGlobalConfig copies the real global config into the home with only the
// probe's MCP server and the working tree trusted, as AO's trust write does.
func (l claudeLab) writeGlobalConfig(t *testing.T) {
	t.Helper()
	source := realClaudeJSON(t)
	config := map[string]json.RawMessage{}
	data, err := os.ReadFile(source)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatalf("parse %s: %v", source, err)
		}
	case errors.Is(err, os.ErrNotExist):
		t.Logf("%s does not exist; the home starts from an empty global config", source)
	default:
		t.Fatalf("read %s: %v", source, err)
	}
	config["mcpServers"] = mustJSON(t, map[string]any{
		"lado-probe-mcp": map[string]any{"type": "stdio", "command": "true", "args": []string{}},
	})
	projects := map[string]json.RawMessage{}
	if raw, ok := config["projects"]; ok {
		if err := json.Unmarshal(raw, &projects); err != nil {
			t.Fatalf("parse projects of %s: %v", source, err)
		}
	}
	projects[filepath.ToSlash(l.work)] = mustJSON(t, map[string]any{"hasTrustDialogAccepted": true})
	config["projects"] = mustJSON(t, projects)
	writeJSON(t, filepath.Join(l.home, ".claude.json"), config)
}

// env is the lab's environment: the test process's own without the parent's
// Claude, Anthropic and Codex variables, plus the home and, when asked, the
// real login's folder. The probe may run inside a Claude Code session
// (CLAUDECODE, CLAUDE_CODE_*), which a daemon launch never inherits, and a
// login variable from the parent would hide whether the folder variable
// carries the login.
func (l claudeLab) env(withSecureStorage bool, extra ...string) []string {
	env := cleanEnviron()
	env = append(env, claudeConfigDirEnv+"="+l.home)
	if withSecureStorage {
		env = append(env, claudeSecureStorageEnv+"="+l.secure)
	}
	return append(env, extra...)
}

func cleanEnviron() []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "CLAUDE") || strings.HasPrefix(upper, "ANTHROPIC_") || strings.HasPrefix(upper, "CODEX_") {
			continue
		}
		env = append(env, entry)
	}
	return env
}

// authStatus runs `claude auth status --json`, which exits 1 when logged out
// and still prints its report.
func (l claudeLab) authStatus(t *testing.T, claude string, env []string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, claude, "auth", "status", "--json")
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.Dir = l.work
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	report := map[string]any{}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &report); err != nil {
		t.Fatalf("claude auth status --json (exit %v): %v\nstdout: %s\nstderr: %s", runErr, err, stdout.String(), stderr.String())
	}
	return report
}

// stateLinksIntact fails when Claude replaced a home state link with its own
// entry, which would split the session's history from the real folder.
func (l claudeLab) stateLinksIntact(t *testing.T) {
	t.Helper()
	for _, name := range append(append([]string(nil), claudeStateEntries...), claudeHistoryFile) {
		link := filepath.Join(l.home, name)
		target, err := os.Readlink(link)
		if err != nil {
			t.Errorf("home state entry %s is no longer a link: %v", name, err)
			continue
		}
		if target != filepath.Join(l.real, name) {
			t.Errorf("home state entry %s points at %s", name, target)
		}
	}
}

// rpcFrame is the union of JSON-RPC shapes on a newline-delimited stdio pipe.
// ACP carries "jsonrpc":"2.0"; Codex's app-server omits it.
type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// rpcPeer is a stdio JSON-RPC child process driven from the test goroutine.
// Notifications that arrive while a call waits are kept for waitNotification;
// requests from the child are refused so nothing stalls.
type rpcPeer struct {
	t       *testing.T
	name    string
	jsonrpc bool
	stdin   io.WriteCloser
	frames  chan rpcFrame
	stderr  *tailBuffer
	nextID  int
	pending []rpcFrame
}

func startPeer(t *testing.T, dir string, env []string, jsonrpc bool, binary string, args ...string) *rpcPeer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := aoprocess.CommandContext(ctx, binary, args...)
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.Dir = dir
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	peer := &rpcPeer{
		t: t, name: filepath.Base(binary), jsonrpc: jsonrpc, stdin: stdin,
		frames: make(chan rpcFrame, 4096), stderr: &tailBuffer{limit: 16 << 10},
	}
	cmd.Stderr = peer.stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start %s: %v", binary, err)
	}
	exited := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
		for scanner.Scan() {
			var frame rpcFrame
			if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
				continue // not a protocol frame
			}
			peer.frames <- frame
		}
		close(peer.frames)
		exited <- cmd.Wait()
	}()
	// The process tree goes before the lab's temp dir (cleanups run last in,
	// first out): a child still writing there would fail its removal.
	t.Cleanup(func() {
		_ = stdin.Close()
		cancel()
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			t.Errorf("%s did not exit after its process tree was stopped", peer.name)
		}
	})
	return peer
}

func (p *rpcPeer) write(frame map[string]any) {
	p.t.Helper()
	if p.jsonrpc {
		frame["jsonrpc"] = "2.0"
	}
	data, err := json.Marshal(frame)
	if err != nil {
		p.t.Fatal(err)
	}
	if _, err := p.stdin.Write(append(data, '\n')); err != nil {
		p.t.Fatalf("write to %s: %v\nstderr: %s", p.name, err, p.stderr.String())
	}
}

func (p *rpcPeer) notify(method string, params any) {
	p.t.Helper()
	frame := map[string]any{"method": method}
	if params != nil {
		frame["params"] = params
	}
	p.write(frame)
}

// call sends a request and waits for its response.
func (p *rpcPeer) call(ctx context.Context, method string, params, out any) {
	p.t.Helper()
	p.nextID++
	id := strconv.Itoa(p.nextID)
	p.write(map[string]any{"id": p.nextID, "method": method, "params": params})
	for {
		frame, ok := p.next(ctx)
		if !ok {
			p.t.Fatalf("%s: no response from %s: %v\nstderr: %s", method, p.name, ctx.Err(), p.stderr.String())
		}
		if frame.Method != "" {
			p.dispatch(frame)
			continue
		}
		if string(frame.ID) != id {
			continue
		}
		if frame.Error != nil {
			p.t.Fatalf("%s: %s answered %d %s %s", method, p.name, frame.Error.Code, frame.Error.Message, frame.Error.Data)
		}
		if out != nil {
			if err := json.Unmarshal(frame.Result, out); err != nil {
				p.t.Fatalf("%s: decode %s: %v", method, frame.Result, err)
			}
		}
		return
	}
}

// waitNotification returns the first notification, kept or new, that match
// accepts, or false when ctx ends first.
func (p *rpcPeer) waitNotification(ctx context.Context, match func(rpcFrame) bool) (rpcFrame, bool) {
	p.t.Helper()
	for i, frame := range p.pending {
		if match(frame) {
			p.pending = append(p.pending[:i], p.pending[i+1:]...)
			return frame, true
		}
	}
	for {
		frame, ok := p.next(ctx)
		if !ok {
			return rpcFrame{}, false
		}
		if frame.Method == "" {
			continue
		}
		if len(frame.ID) > 0 {
			p.dispatch(frame)
			continue
		}
		if match(frame) {
			return frame, true
		}
		p.pending = append(p.pending, frame)
	}
}

func (p *rpcPeer) next(ctx context.Context) (rpcFrame, bool) {
	select {
	case frame, ok := <-p.frames:
		return frame, ok
	case <-ctx.Done():
		return rpcFrame{}, false
	}
}

// dispatch keeps a notification and refuses a request from the child.
func (p *rpcPeer) dispatch(frame rpcFrame) {
	if len(frame.ID) == 0 {
		p.pending = append(p.pending, frame)
		return
	}
	p.t.Logf("%s asked %s; refused", p.name, frame.Method)
	p.write(map[string]any{"id": frame.ID, "error": map[string]any{"code": -32601, "message": "not supported by the probe"}})
}

// tailBuffer keeps the last limit bytes a child wrote, for failure messages.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if extra := len(b.data) - b.limit; extra > 0 {
		b.data = b.data[extra:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

// listsHolding names the keys of event whose list holds name, as a string or
// as an object's "name".
func listsHolding(event map[string]any, name string) []string {
	var keys []string
	for key, value := range event {
		items, ok := value.([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			if itemName(item) == name {
				keys = append(keys, key)
				break
			}
		}
	}
	sort.Strings(keys)
	return keys
}

func itemName(item any) string {
	switch v := item.(type) {
	case string:
		return v
	case map[string]any:
		name, _ := v["name"].(string)
		return name
	default:
		return ""
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(buf)
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(data)+"\n", 0o600)
}

func readJSON(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
