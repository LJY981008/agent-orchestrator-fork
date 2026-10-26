// Package ladosetup is LADO's local setup service (docs/LJY-PRD/HARNESS.md
// §9). It lists the developer's Claude Code and Codex setup and the setup
// files of the registered projects, says for each entry whether LADO can
// adopt it and why not, and finds what runs those tools now.
//
// The service never looks up a home or a tool folder itself: the daemon
// passes every path in Deps, so tests run against temporary homes and never
// reach the developer's real ~/.claude, ~/.claude.json or ~/.codex.
package ladosetup

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// Store keeps the setup LADO manages. *sqlite.Store implements it.
type Store interface {
	ListLiveLadoSetupItems(ctx context.Context) ([]domain.LadoSetupItemWithVersion, error)
	LadoSetupItem(ctx context.Context, id domain.LadoSetupItemID) (domain.LadoSetupItem, []domain.LadoSetupItemVersion, bool, error)
	AddLadoSetupItemVersion(ctx context.Context, itemID domain.LadoSetupItemID, baseVersion int, version domain.LadoSetupItemVersion, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error
	ListLadoSetupAdoptions(ctx context.Context) ([]domain.LadoSetupAdoption, error)
	LadoSetupAdoption(ctx context.Context, id domain.LadoSetupAdoptionID) (domain.LadoSetupAdoption, bool, error)
	ListLadoSetupItemsForAdoption(ctx context.Context, id domain.LadoSetupAdoptionID) ([]domain.LadoSetupItem, error)
	RecordLadoSetupAdoption(ctx context.Context, adoption domain.LadoSetupAdoption, items []domain.LadoSetupItem, versions []domain.LadoSetupItemVersion, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error
	RestoreLadoSetupAdoption(ctx context.Context, id domain.LadoSetupAdoptionID, report json.RawMessage, at time.Time, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error
	AppendLadoAuditEvent(ctx context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error)
	ListLadoSetupGlobalBindings(ctx context.Context) ([]domain.LadoSetupBinding, error)
	ListLadoSetupBindings(ctx context.Context, scope domain.LadoRuleScope, scopeID string) ([]domain.LadoSetupBinding, error)
	LadoSetupVersionsForClientRequest(ctx context.Context, clientRequestID string) ([]domain.LadoSetupItemWithVersion, bool, error)
}

// Blobs keeps the content of setup item versions. *ladoblob.Store
// implements it.
type Blobs interface {
	Put(data []byte) (string, error)
	Get(sum string, limit int64) ([]byte, error)
}

// Projects lists AO's registered projects. *sqlite.Store implements it.
type Projects interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
}

// Sessions lists AO's sessions. *sqlite.Store implements it.
type Sessions interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

// Deps wires a Service. Every path is the daemon's: an empty one lists
// nothing for that tool, as when the folder does not exist.
type Deps struct {
	// Store reads the managed items and the adoptions. Without it the
	// inventory has neither.
	Store Store
	Blobs Blobs
	// StateDir is AO's state dir. LADO keeps its managed folder, backups and
	// session config homes under <StateDir>/lado.
	StateDir string
	// ClaudeDir is the computer's own Claude folder ($CLAUDE_CONFIG_DIR or
	// ~/.claude), ClaudeJSON Claude's global config (~/.claude.json, or the
	// .claude.json inside $CLAUDE_CONFIG_DIR) and CodexHome the Codex folder
	// ($CODEX_HOME or ~/.codex).
	ClaudeDir  string
	ClaudeJSON string
	CodexHome  string
	// ClaudeDirFromEnv reports that ClaudeDir came from CLAUDE_CONFIG_DIR.
	ClaudeDirFromEnv bool
	// GOOS is the OS the daemon runs on, which decides whether adoption is
	// allowed (domain.LadoSetupDeliveryVerified); empty uses runtime.GOOS.
	GOOS string
	// Projects lists the projects whose main checkouts the inventory reads.
	// Without it no project is read.
	Projects Projects
	// Sessions lists AO's sessions, whose live Claude Code and Codex ones
	// hold adoption and restore back, and whose ended ones lose their config
	// homes. RunningTools and CleanupHomes refuse to answer without it.
	Sessions Sessions
	// Processes lists the computer's processes; nil uses procmem.Snapshot.
	Processes func(ctx context.Context) ([]procmem.Process, error)
	// DevIgnoreProcesses leaves the processes out of RunningTools, for labs
	// that run the daemon inside a Claude Code or Codex session. The daemon
	// sets it from AO_LADO_DEV_SETUP_IGNORE_PROCESSES=1.
	DevIgnoreProcesses bool
	// Clock stamps scans; nil uses time.Now.
	Clock func() time.Time
	// NewID returns a new unique id; nil uses uuid.NewString.
	NewID func() string
	Log   *slog.Logger
}

// Service lists the local setup and keeps the last list.
type Service struct {
	deps Deps
	// scanning admits one scan at a time, and writing one change of the
	// setup, such as an adoption. A caller that waits can give up with its
	// context.
	scanning chan struct{}
	writing  chan struct{}
	// ops change the original locations.
	ops fileOps
	// onStage and afterBackup let tests watch an adoption: onStage hears
	// each stage start, and afterBackup runs between writing the backup and
	// reading it back.
	onStage     func(stage string)
	afterBackup func(dir string)

	mu sync.Mutex
	// last is the last scan, nil before the first.
	last *Inventory

	// homesMu guards homesPrepared, when each session's config home was last
	// prepared, which keeps CleanupHomes off a home a launch is using.
	homesMu       sync.Mutex
	homesPrepared map[domain.SessionID]time.Time
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.GOOS == "" {
		deps.GOOS = runtime.GOOS
	}
	if deps.Processes == nil {
		deps.Processes = snapshotProcesses
	}
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Service{
		deps:     deps,
		scanning: make(chan struct{}, 1),
		writing:  make(chan struct{}, 1),
		ops:      fileOps{removeAll: os.RemoveAll, rename: os.Rename},
	}
}

// fileOps remove and replace originals. Tests stand in for them to fail the
// way a locked file does on Windows.
type fileOps struct {
	removeAll func(path string) error
	rename    func(oldpath, newpath string) error
}

// Inventory is one scan of the developer's setup (docs/LJY-PRD/HARNESS.md
// §9.1).
type Inventory struct {
	// Entries are the managed items, then what the Claude folder,
	// ~/.claude.json, the Codex folder and each project hold, each part in
	// name order.
	Entries []domain.LadoSetupEntry
	// Roots are the tool folders and files the scan read.
	Roots []Root
	// Projects are the projects the scan read.
	Projects []ProjectScan
	// Adoptions are the adoptions so far, oldest first.
	Adoptions []domain.LadoSetupAdoption
	// Drift is what changed outside LADO (plan §8): the managed copies that
	// no longer hold their item's latest version, then the new setup at the
	// original locations.
	Drift []domain.LadoSetupDrift
	// OS is the GOOS of the scan, and Verified tells for each tool whether
	// adoption is allowed on it.
	OS        string
	Verified  map[domain.LadoSetupTool]bool
	ScannedAt time.Time
}

// Root is a tool folder or file the inventory reads, as the daemon gave it.
type Root struct {
	Tool     domain.LadoSetupTool
	Location domain.LadoSetupLocation
	// Path is empty when the daemon gave none.
	Path  string
	Found bool
	// Error is why it could not be read; its entries are then missing.
	Error string
}

// ProjectScan is a project the inventory read.
type ProjectScan struct {
	ID   domain.ProjectID
	Name string
	Path string
	// Error is why the project could not be read; its entries are then
	// missing.
	Error string
}

// Inventory returns the last scan, scanning first when there is none.
func (s *Service) Inventory(ctx context.Context) (Inventory, error) {
	if inv, ok := s.cached(); ok {
		return inv, nil
	}
	if err := s.lockScan(ctx); err != nil {
		return Inventory{}, err
	}
	defer s.unlockScan()
	// Another caller may have scanned while this one waited.
	if inv, ok := s.cached(); ok {
		return inv, nil
	}
	return s.scanLocked(ctx)
}

// Scan reads the setup again and keeps the result for Inventory.
func (s *Service) Scan(ctx context.Context) (Inventory, error) {
	if err := s.lockScan(ctx); err != nil {
		return Inventory{}, err
	}
	defer s.unlockScan()
	return s.scanLocked(ctx)
}

func (s *Service) scanLocked(ctx context.Context) (Inventory, error) {
	inv, err := s.scan(ctx)
	if err != nil {
		return Inventory{}, err
	}
	s.mu.Lock()
	s.last = &inv
	s.mu.Unlock()
	return inv.clone(), nil
}

func (s *Service) cached() (Inventory, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return Inventory{}, false
	}
	return s.last.clone(), true
}

func (s *Service) lockScan(ctx context.Context) error {
	select {
	case s.scanning <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for the setup scan: %w", ctx.Err())
	}
}

func (s *Service) unlockScan() { <-s.scanning }

// clone copies the inventory's lists, so a caller cannot change the cached
// scan.
func (inv Inventory) clone() Inventory {
	inv.Entries = slices.Clone(inv.Entries)
	for i := range inv.Entries {
		inv.Entries[i].KeptInPlace = slices.Clone(inv.Entries[i].KeptInPlace)
	}
	inv.Roots = slices.Clone(inv.Roots)
	inv.Projects = slices.Clone(inv.Projects)
	inv.Adoptions = slices.Clone(inv.Adoptions)
	inv.Drift = slices.Clone(inv.Drift)
	inv.Verified = maps.Clone(inv.Verified)
	return inv
}
