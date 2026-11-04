package claudecode

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
)

// ResolveBinary resolves the executable path for the plugin.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.claudeBinary(ctx)
}

// InvalidateBinaryResolution makes the next operation resolve Claude Code
// again (LADO fork, issue #42). Installers can replace the CLI in place or
// move it, as the npm to native move of 2026-10-30 did.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

// claudeBinaryUsable reports whether a cached Claude path can still run: the
// file is there, and it is not an npm install whose native binary is gone.
func claudeBinaryUsable(path string) bool {
	if !binaryutil.CachedBinaryUsable(path) {
		return false
	}
	return !filepath.IsAbs(path) || !claudeShimTargetMissing(path)
}

// claudeShimTargetMissing reports whether path is Claude Code's npm launcher
// without the native binary it runs. The npm package's bin/claude.exe is a
// small script until its postinstall copies the platform's native binary
// over it; a removed platform package, or a skipped postinstall, leaves the
// script, which only prints "claude native binary not installed". On Windows
// npm's claude.cmd runs that file, or cli.js in packages before the native
// one. Only npm puts the package next to the shim; a claude.cmd of another
// layout (pnpm, Yarn) is taken as usable, since LADO cannot tell where its
// package is.
func claudeShimTargetMissing(path string) bool {
	if strings.EqualFold(filepath.Ext(path), ".cmd") {
		pkg := filepath.Join(filepath.Dir(path), "node_modules", "@anthropic-ai", "claude-code")
		if !directory(pkg) {
			return false
		}
		if exe := filepath.Join(pkg, "bin", "claude.exe"); regularFile(exe) {
			return !nativeExecutable(exe)
		}
		return !regularFile(filepath.Join(pkg, "cli.js"))
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return true
	}
	if isNpmClaudeLauncher(target) {
		return !nativeExecutable(target)
	}
	return false
}

// isNpmClaudeLauncher reports whether path is the npm package's
// @anthropic-ai/claude-code/bin/claude.exe.
func isNpmClaudeLauncher(path string) bool {
	bin := filepath.Dir(path)
	pkg := filepath.Dir(bin)
	scope := filepath.Dir(pkg)
	return strings.EqualFold(filepath.Base(path), "claude.exe") &&
		filepath.Base(bin) == "bin" &&
		filepath.Base(pkg) == "claude-code" &&
		filepath.Base(scope) == "@anthropic-ai"
}

func directory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// nativeMagics start an ELF, a PE and the Mach-O files.
var nativeMagics = [][]byte{
	{0x7f, 'E', 'L', 'F'},
	{'M', 'Z'},
	{0xfe, 0xed, 0xfa, 0xce}, {0xfe, 0xed, 0xfa, 0xcf},
	{0xce, 0xfa, 0xed, 0xfe}, {0xcf, 0xfa, 0xed, 0xfe},
	{0xca, 0xfe, 0xba, 0xbe},
}

// nativeExecutable reports whether path starts like a native executable.
func nativeExecutable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 4)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	for _, magic := range nativeMagics {
		if bytes.HasPrefix(head, magic) {
			return true
		}
	}
	return false
}
