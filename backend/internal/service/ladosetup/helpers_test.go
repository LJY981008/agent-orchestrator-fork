package ladosetup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// scanAt is when the tests' scans run.
var scanAt = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

// fakeHome is a computer's home under t.TempDir(): the Claude folder,
// ~/.claude.json, the Codex folder and AO's state dir. No test reads the
// developer's real folders.
type fakeHome struct {
	t          *testing.T
	root       string
	claude     string
	claudeJSON string
	codex      string
	state      string
}

func newFakeHome(t *testing.T) *fakeHome {
	t.Helper()
	root := t.TempDir()
	return &fakeHome{
		t:          t,
		root:       root,
		claude:     filepath.Join(root, ".claude"),
		claudeJSON: filepath.Join(root, ".claude.json"),
		codex:      filepath.Join(root, ".codex"),
		state:      filepath.Join(root, "state"),
	}
}

// deps wires a service to the home on Linux, with a quiet log.
func (h *fakeHome) deps() Deps {
	return Deps{
		StateDir:   h.state,
		ClaudeDir:  h.claude,
		ClaudeJSON: h.claudeJSON,
		CodexHome:  h.codex,
		GOOS:       "linux",
		Clock:      func() time.Time { return scanAt },
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// write creates the file at path with content, and its folders.
func (h *fakeHome) write(path, content string) string {
	h.t.Helper()
	return h.writeMode(path, content, 0o644)
}

func (h *fakeHome) writeMode(path, content string, mode os.FileMode) string {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		h.t.Fatal(err)
	}
	// WriteFile keeps the mode of a file that exists, and umask trims it.
	if err := os.Chmod(path, mode); err != nil {
		h.t.Fatal(err)
	}
	return path
}

func (h *fakeHome) mkdir(path string) string {
	h.t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		h.t.Fatal(err)
	}
	return path
}

// symlink makes link point at target, or skips the test where links cannot
// be made (Windows without Developer Mode).
func (h *fakeHome) symlink(target, link string) {
	h.t.Helper()
	h.mkdir(filepath.Dir(link))
	if err := os.Symlink(target, link); err != nil {
		h.t.Skipf("cannot make a symbolic link here: %v", err)
	}
}

func mustScan(t *testing.T, deps Deps) Inventory {
	t.Helper()
	inv, err := New(deps).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return inv
}

// entryAt returns the one entry at location with kind and name.
func entryAt(t *testing.T, inv Inventory, location domain.LadoSetupLocation, kind domain.LadoSetupKind, name string) domain.LadoSetupEntry {
	t.Helper()
	var found []domain.LadoSetupEntry
	for _, e := range inv.Entries {
		if e.Location == location && e.Kind == kind && e.Name == name {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d entries at %s with kind %q and name %q, want 1; entries:\n%s", len(found), location, kind, name, dumpEntries(inv))
	}
	return found[0]
}

// entryAtPath returns the one entry at path, which tells apart lines of one
// name, such as the Codex folder skills/ and the config.toml key skills.
func entryAtPath(t *testing.T, inv Inventory, path string) domain.LadoSetupEntry {
	t.Helper()
	var found []domain.LadoSetupEntry
	for _, e := range inv.Entries {
		if e.Path == path {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d entries at %s, want 1; entries:\n%s", len(found), path, dumpEntries(inv))
	}
	return found[0]
}

func dumpEntries(inv Inventory) string {
	var out string
	for _, e := range inv.Entries {
		out += "  " + string(e.Location) + " " + string(e.Kind) + " " + e.Name + " " + string(e.Status) + "/" + string(e.Reason) + " " + e.Path + "\n"
	}
	return out
}

// wantStatus checks an entry's status and reason. An entry LADO does not
// adopt has no content sums and proposes nothing.
func wantStatus(t *testing.T, e domain.LadoSetupEntry, status domain.LadoSetupStatus, reason domain.LadoSetupReason) {
	t.Helper()
	if e.Status != status || e.Reason != reason {
		t.Fatalf("%s %q is %s/%s, want %s/%s", e.Kind, e.Name, e.Status, e.Reason, status, reason)
	}
	if status != domain.LadoSetupStatusAdoptable && status != domain.LadoSetupStatusManaged && status != domain.LadoSetupStatusRepoTracked {
		if e.SHA256 != "" || e.Size != 0 || e.DefaultAction != "" || e.ItemID != "" {
			t.Fatalf("%s %q is %s but has sum %q, size %d, action %q, item %q", e.Kind, e.Name, status, e.SHA256, e.Size, e.DefaultAction, e.ItemID)
		}
	}
}

// wantAdoptable checks an adoptable entry's content and proposed action.
func wantAdoptable(t *testing.T, e domain.LadoSetupEntry, content []byte, action domain.LadoSetupAction) {
	t.Helper()
	wantStatus(t, e, domain.LadoSetupStatusAdoptable, "")
	if e.SHA256 != sumOf(content) || e.Size != int64(len(content)) {
		t.Fatalf("%s %q has sum %s and size %d, want %s and %d (%s)", e.Kind, e.Name, e.SHA256, e.Size, sumOf(content), len(content), content)
	}
	if e.DefaultAction != action {
		t.Fatalf("%s %q proposes %q, want %q", e.Kind, e.Name, e.DefaultAction, action)
	}
}

func sumOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// fakeProjects lists fixed projects.
type fakeProjects []domain.ProjectRecord

func (p fakeProjects) ListProjects(context.Context) ([]domain.ProjectRecord, error) { return p, nil }

// fakeSessions lists fixed sessions.
type fakeSessions []domain.SessionRecord

func (s fakeSessions) ListAllSessions(context.Context) ([]domain.SessionRecord, error) { return s, nil }
