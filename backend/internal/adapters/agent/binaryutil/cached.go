package binaryutil

// LADO fork: an adapter that caches its resolved binary checks it before each
// use (issue #42). On 2026-10-30 Claude Code moved from its npm install to its
// native one and removed the old files while the daemon kept the old path, so
// every new Claude session failed until the daemon restarted.

import (
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

// CachedBinaryUsable reports whether a cached binary path can still be used:
// an absolute path names an executable regular file, its links followed. A
// bare name, which only a test or an explicit override caches, is left to the
// operating system's lookup.
func CachedBinaryUsable(path string) bool {
	if path == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		return true
	}
	return hookutil.IsExecutableFile(path)
}
