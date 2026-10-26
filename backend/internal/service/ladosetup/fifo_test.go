//go:build linux || darwin

package ladosetup

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A named pipe where a file is read would block the scan forever, and every
// scan after it. LADO reads it as unreadable instead, through a link too.
func TestInventoryDoesNotWaitOnNamedPipes(t *testing.T) {
	h := newFakeHome(t)
	fifo := func(path string) string {
		t.Helper()
		h.mkdir(filepath.Dir(path))
		if err := syscall.Mkfifo(path, 0o644); err != nil {
			t.Skipf("cannot make a named pipe here: %v", err)
		}
		return path
	}
	fifo(filepath.Join(h.claude, "settings.json"))
	fifo(filepath.Join(h.claude, "skills", "pipe"))
	fifo(filepath.Join(h.claude, "commands", "review", "SKILL.md"))
	h.symlink(fifo(filepath.Join(h.root, "dotfiles", "config.toml")), filepath.Join(h.codex, "config.toml"))
	fifo(h.claudeJSON)

	type result struct {
		inv Inventory
		err error
	}
	done := make(chan result, 1)
	go func() {
		inv, err := New(h.deps()).Scan(context.Background())
		done <- result{inv, err}
	}()
	var inv Inventory
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Scan: %v", r.err)
		}
		inv = r.inv
	case <-time.After(10 * time.Second):
		t.Fatal("the scan waits on a named pipe")
	}
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationClaudeDir, "", "settings.json"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonUnreadable)
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSkill, "pipe"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonUnreadable)
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindCommand, "review/SKILL.md"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonUnreadable)
	wantStatus(t, entryAt(t, inv, domain.LadoSetupLocationCodexHome, "", "config.toml"), domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonUnreadable)
	for _, root := range inv.Roots {
		if root.Location == domain.LadoSetupLocationClaudeJSON && root.Error == "" {
			t.Fatalf("a named pipe at ~/.claude.json gives the root %+v, want its error", root)
		}
	}
}
