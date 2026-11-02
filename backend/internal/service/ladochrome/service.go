// Package ladochrome owns LADO's Claude in Chrome setting: which Claude Code
// Chat sessions start with the browser extension's tools, the environment
// value each controller start gets, and what a running session started with.
//
// The setting is two booleans in <DataDir>/lado/claude-chrome.json rather
// than a database column, so it needs no LADO migration (docs/LJY-PRD/TDD.md).
// Turning anything on needs human presence, because an agent on this machine
// can call /api/v1/lado and would otherwise switch its own browser on.
package ladochrome

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
)

// AuditAction is the audit log action of every settings change.
const AuditAction = "lado.claude_chrome.changed"

// Store is what the service needs from the database.
type Store interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	RecordLadoClaudeChromeChange(ctx context.Context, proof *domain.LadoPresenceProof, event domain.LadoAuditEvent) error
}

// Presence verifies the request's human-presence proof for an action. The
// service calls it only when a change turns something on.
type Presence func(action, subject string) (domain.LadoPresenceProof, error)

// Options builds a Service.
type Options struct {
	// DataDir is AO's data directory; the file is <DataDir>/lado/claude-chrome.json.
	DataDir string
	Store   Store
	Log     *slog.Logger
	Now     func() time.Time
}

// Service reads and changes the setting and answers per-session state.
type Service struct {
	path  string
	store Store
	log   *slog.Logger
	now   func() time.Time

	// mu serializes changes so the presence decision and the write see the
	// same current value; two racing requests cannot both skip the check.
	mu sync.Mutex

	launchMu sync.Mutex
	// launched is the value each session's last controller start got.
	launched map[domain.SessionID]bool
}

// New returns a Service.
func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		path:     filepath.Join(opts.DataDir, "lado", "claude-chrome.json"),
		store:    opts.Store,
		log:      log,
		now:      now,
		launched: map[domain.SessionID]bool{},
	}
}

// Path is the settings file.
func (s *Service) Path() string { return s.path }

// Settings returns the current setting. A missing or unreadable file gives
// the defaults so a broken file never blocks a launch.
func (s *Service) Settings(context.Context) (domain.LadoClaudeChromeSettings, error) {
	return s.read(), nil
}

func (s *Service) read() domain.LadoClaudeChromeSettings {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.DefaultLadoClaudeChromeSettings()
	}
	if err != nil {
		s.log.Warn("lado claude chrome: read settings; using defaults", "path", s.path, "error", err)
		return domain.DefaultLadoClaudeChromeSettings()
	}
	var file struct {
		ChatSessions *bool `json:"chatSessions"`
		LadoWorkers  *bool `json:"ladoWorkers"`
	}
	if err := json.Unmarshal(data, &file); err != nil || file.ChatSessions == nil || file.LadoWorkers == nil {
		s.log.Warn("lado claude chrome: settings file is damaged; using defaults", "path", s.path, "error", err)
		return domain.DefaultLadoClaudeChromeSettings()
	}
	return domain.LadoClaudeChromeSettings{ChatSessions: *file.ChatSessions, LadoWorkers: *file.LadoWorkers}
}

// SetSettings changes the setting. A change that turns anything on asks
// presence first and fails without writing anything when it is refused. The
// proof (when there is one) and the audit event are recorded before the
// file is written; a reused proof returns ladopresence.Replayed().
func (s *Service) SetSettings(ctx context.Context, next domain.LadoClaudeChromeSettings, presence Presence) (domain.LadoClaudeChromeSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.read()
	if next == current {
		return current, nil
	}
	var turningOn []string
	if next.ChatSessions && !current.ChatSessions {
		turningOn = append(turningOn, "chatSessions")
	}
	if next.LadoWorkers && !current.LadoWorkers {
		turningOn = append(turningOn, "ladoWorkers")
	}
	var proof *domain.LadoPresenceProof
	actor := "api"
	if len(turningOn) > 0 {
		if presence == nil {
			return current, apierr.Forbidden("LADO_PRESENCE_REQUIRED",
				"Turn Claude in Chrome on in the AO desktop app. It asks you to confirm.")
		}
		got, err := presence(domain.LadoPresenceClaudeChromeOn, strings.Join(turningOn, ","))
		if err != nil {
			return current, err
		}
		proof = &got
		actor = "human"
		if got.Dev {
			actor = "human:dev-presence"
		}
	}
	payload := map[string]any{"from": current, "to": next}
	if proof != nil {
		payload["proof"] = proof.Nonce
		payload["dev"] = proof.Dev
	}
	encoded, _ := json.Marshal(payload)
	err := s.store.RecordLadoClaudeChromeChange(ctx, proof, domain.LadoAuditEvent{
		At:          s.now().UTC(),
		Actor:       actor,
		Action:      AuditAction,
		SubjectKind: "daemon",
		SubjectID:   "claude_chrome",
		Payload:     encoded,
	})
	if errors.Is(err, domain.ErrLadoPresenceReplayed) {
		return current, ladopresence.Replayed()
	}
	if err != nil {
		return current, apierr.Internal("LADO_CLAUDE_CHROME_SAVE_FAILED", "Failed to record the Claude in Chrome change")
	}
	if err := s.write(next); err != nil {
		s.log.Error("lado claude chrome: write settings", "path", s.path, "error", err)
		return current, apierr.Internal("LADO_CLAUDE_CHROME_SAVE_FAILED", "Failed to save the Claude in Chrome setting")
	}
	return next, nil
}

// write replaces the file through a temporary file and a rename, so a crash
// leaves either the old or the new value.
func (s *Service) write(settings domain.LadoClaudeChromeSettings) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create settings folder: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".claude-chrome-*.json")
	if err != nil {
		return fmt.Errorf("create temporary settings file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("restrict temporary settings file: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary settings file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary settings file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary settings file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace settings file: %w", err)
	}
	return nil
}

// covered reports whether a session gets the environment value: only Claude
// Code in Chat. A terminal session keeps Claude Code's own /chrome choice,
// which this value would override.
func covered(rec domain.SessionRecord, harness domain.AgentHarness) bool {
	return harness == domain.HarnessClaudeCode && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat
}

func kindOf(rec domain.SessionRecord) domain.LadoChromeSessionKind {
	if domain.IsLadoWorkerClientRequestID(rec.ClientRequestID) {
		return domain.LadoChromeKindLadoWorker
	}
	return domain.LadoChromeKindChat
}

func settingFor(settings domain.LadoClaudeChromeSettings, kind domain.LadoChromeSessionKind) bool {
	if kind == domain.LadoChromeKindLadoWorker {
		return settings.LadoWorkers
	}
	return settings.ChatSessions
}

// LaunchEnv is the CLAUDE_CODE_ENABLE_CFC value for a controller start, and
// false when the session is not covered. harness is the agent being started,
// not the stored rec.Harness, which still names the old agent while an agent
// switch is in flight. Every answer is remembered as the session's launch
// value.
func (s *Service) LaunchEnv(rec domain.SessionRecord, harness domain.AgentHarness) (string, bool) {
	if !covered(rec, harness) {
		return "", false
	}
	on := settingFor(s.read(), kindOf(rec))
	s.launchMu.Lock()
	s.launched[rec.ID] = on
	s.launchMu.Unlock()
	if on {
		return "1", true
	}
	return "0", true
}

// SessionChrome is one session's state. A running controller answers with
// the value it started with (basis launch). A session without a recorded
// start, or one hibernated or terminated, answers with what its next start
// gets (basis setting): a hibernated session wakes with the current setting.
func (s *Service) SessionChrome(ctx context.Context, id domain.SessionID) (domain.LadoSessionChrome, error) {
	rec, found, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.LadoSessionChrome{}, apierr.Internal("SESSION_READ_FAILED", "Failed to read the session")
	}
	if !found {
		return domain.LadoSessionChrome{}, apierr.NotFound("SESSION_NOT_FOUND", "session not found")
	}
	if !covered(rec, rec.Harness) {
		return domain.LadoSessionChrome{Supported: false}, nil
	}
	kind := kindOf(rec)
	settingOn := settingFor(s.read(), kind)
	state := domain.LadoSessionChrome{
		Supported: true, Kind: kind, SettingOn: settingOn,
		Enabled: settingOn, Basis: domain.LadoChromeBasisSetting,
	}
	if rec.HibernatedAt != nil || rec.IsTerminated {
		return state, nil
	}
	s.launchMu.Lock()
	launched, ok := s.launched[id]
	s.launchMu.Unlock()
	if ok {
		state.Enabled = launched
		state.Basis = domain.LadoChromeBasisLaunch
	}
	return state, nil
}
