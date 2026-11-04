package claudecode

// LADO fork (issue #42): on 2026-10-30 Claude Code moved from its npm install
// to its native one and removed the old files while the daemon kept the old
// path. The adapter checks its cached path before each use.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// npmStub is the npm package's bin/claude.exe before its postinstall copies
// the native binary over it (@anthropic-ai/claude-code 2.1.317).
const npmStub = "echo \"Error: claude native binary not installed.\" >&2\nexit 1\n"

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// isolate leaves only the folders the test makes for Claude to be found in.
func isolate(t *testing.T) (home, bin string) {
	t.Helper()
	home, bin = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	for _, v := range []string{"NVM_DIR", "VOLTA_HOME", "FNM_DIR", "XDG_DATA_HOME", "npm_config_prefix", "NPM_CONFIG_PREFIX"} {
		t.Setenv(v, "")
	}
	return home, bin
}

func systemClaudePresent() bool {
	for _, p := range claudeBinarySpec.UnixPaths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func TestCachedClaudePathIsResolvedAgainWhenItsFileIsGone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable bits")
	}
	_, bin := isolate(t)
	current := filepath.Join(bin, "claude")
	writeExecutable(t, current, "\x7fELF native")
	old := filepath.Join(t.TempDir(), "claude")
	writeExecutable(t, old, "\x7fELF old")

	p := New()
	p.resolvedBinary = old
	ctx := context.Background()
	if got, err := p.ResolveBinary(ctx); err != nil || got != old {
		t.Fatalf("while the cached file is there = %q, %v; want the cached %q", got, err, old)
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	if got, err := p.ResolveBinary(ctx); err != nil || got != current {
		t.Fatalf("after its file is gone = %q, %v; want %q", got, err, current)
	}

	// The launch command uses the path found again.
	argv, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{SessionID: "s-1"})
	if err != nil || len(argv) == 0 || argv[0] != current {
		t.Fatalf("launch command = %q, %v; want it to start %q", argv, err, current)
	}

	// InvalidateBinaryResolution drops the cache outright.
	p.InvalidateBinaryResolution()
	if p.resolvedBinary != "" {
		t.Fatalf("cache after InvalidateBinaryResolution = %q", p.resolvedBinary)
	}
}

func TestNpmLauncherWithoutItsNativeBinaryIsNoInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix npm layout")
	}
	home, bin := isolate(t)
	pkg := filepath.Join(t.TempDir(), "lib", "node_modules", "@anthropic-ai", "claude-code")
	stub := filepath.Join(pkg, "bin", "claude.exe")
	writeExecutable(t, stub, npmStub)
	if err := os.Symlink(stub, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(home, ".local", "bin", "claude")
	writeExecutable(t, native, "\x7fELF native")

	if !claudeShimTargetMissing(filepath.Join(bin, "claude")) {
		t.Fatal("the npm launcher's stub reads as an install")
	}
	got, err := ResolveClaudeBinary(context.Background())
	if err != nil {
		t.Fatalf("ResolveClaudeBinary: %v", err)
	}
	if got == filepath.Join(bin, "claude") {
		t.Fatalf("resolved the npm launcher %q whose native binary is gone", got)
	}
	if !systemClaudePresent() && got != native {
		t.Fatalf("resolved %q, want the native install %q", got, native)
	}

	// A cached npm launcher whose native binary went away is resolved again.
	p := New()
	p.resolvedBinary = filepath.Join(bin, "claude")
	if again, err := p.ResolveBinary(context.Background()); err != nil || again == filepath.Join(bin, "claude") {
		t.Fatalf("cached npm launcher = %q, %v; want it resolved again", again, err)
	}

	// Once postinstall put the native binary in place the launcher is fine.
	writeExecutable(t, stub, "\x7fELF native from npm")
	if claudeShimTargetMissing(filepath.Join(bin, "claude")) {
		t.Fatal("the npm launcher with its native binary reads as missing")
	}
	// A dangling link is missing too.
	dangling := filepath.Join(t.TempDir(), "claude")
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), dangling); err != nil {
		t.Fatal(err)
	}
	if !claudeShimTargetMissing(dangling) {
		t.Fatal("a dangling link reads as an install")
	}
}

func TestOldNpmLayoutWithCliJSStaysAnInstall(t *testing.T) {
	pkg := filepath.Join(t.TempDir(), "lib", "node_modules", "@anthropic-ai", "claude-code")
	cli := filepath.Join(pkg, "cli.js")
	writeExecutable(t, cli, "#!/usr/bin/env node\n")
	if claudeShimTargetMissing(cli) {
		t.Fatal("the cli.js layout reads as missing")
	}
}

func TestWindowsCmdShimNeedsItsTarget(t *testing.T) {
	npm := t.TempDir()
	shim := filepath.Join(npm, "claude.cmd")
	writeExecutable(t, shim, "@echo off\r\n")
	if !claudeShimTargetMissing(shim) {
		t.Fatal("a claude.cmd without its package reads as an install")
	}
	pkg := filepath.Join(npm, "node_modules", "@anthropic-ai", "claude-code")
	exe := filepath.Join(pkg, "bin", "claude.exe")
	writeExecutable(t, exe, npmStub)
	if !claudeShimTargetMissing(shim) {
		t.Fatal("a claude.cmd whose claude.exe is the stub reads as an install")
	}
	writeExecutable(t, exe, "MZ native")
	if claudeShimTargetMissing(shim) {
		t.Fatal("a claude.cmd with its native claude.exe reads as missing")
	}
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(pkg, "cli.js"), "#!/usr/bin/env node\n")
	if claudeShimTargetMissing(shim) {
		t.Fatal("a claude.cmd of the cli.js layout reads as missing")
	}
}

// fixtureClaude is a Claude path a test caches: an executable file, as the
// adapter now checks a cached absolute path before it uses it.
func fixtureClaude(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	writeExecutable(t, path, "\x7fELF fixture")
	return path
}
