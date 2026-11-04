// Package ladolaunch decides the values LADO puts on an agent launch for the
// worker watch (issue #42): the token count a Claude Code or Codex session
// compacts at, and the settings that keep that count from mattering.
//
// The compact window goes only to sessions of a project in LADO mode, and to
// every one of them: workers, plan workers, Chat and terminal sessions, the
// orchestrator and AO's reviewers. It does not depend on LADO managing the
// project's Claude setup. Ordinary AO projects keep upstream behavior.
package ladolaunch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// AuditCompactBlocked is the audit action LADO records when a session starts
// with a setting that turns Claude Code's auto-compact off, so the compact
// window it got does nothing.
const AuditCompactBlocked = "lado.compact_blocked"

// Store is what the service reads and writes.
type Store interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error)
	LadoEffectiveWatchSettings(ctx context.Context, project domain.ProjectID) (domain.LadoWatchSettings, error)
	AppendLadoAuditEvent(ctx context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error)
}

// Projects finds a project's repository and environment.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// Options builds a Service.
type Options struct {
	// DataDir is AO's data directory, where LADO keeps its managed Claude
	// settings (<DataDir>/lado/harness/claude/settings.json).
	DataDir  string
	Store    Store
	Projects Projects
	// Home is the user's home folder; empty uses os.UserHomeDir.
	Home string
	// Getenv reads the daemon's environment; nil uses os.Getenv.
	Getenv func(string) string
	Log    *slog.Logger
	Now    func() time.Time
}

// Service answers the compact window of a project and the launch values of
// a session.
type Service struct {
	opts Options

	mu sync.Mutex
	// blocked holds the sessions whose compact_blocked audit is recorded, so
	// a resume does not record it again.
	blocked map[domain.SessionID]bool
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	return &Service{opts: opts, blocked: map[domain.SessionID]bool{}}
}

// CompactWindow returns the compact window of the project's sessions: the
// project's watch setting over the global one over the default. ok is false
// for a project with LADO mode off, whose sessions get nothing.
func (s *Service) CompactWindow(ctx context.Context, project domain.ProjectID) (window int, ok bool, err error) {
	if s == nil || s.opts.Store == nil || project == "" {
		return 0, false, nil
	}
	settings, err := s.opts.Store.LadoProjectSettings(ctx, project)
	if err != nil {
		return 0, false, fmt.Errorf("read the LADO settings of project %s: %w", project, err)
	}
	if !settings.LadoMode {
		return 0, false, nil
	}
	watch, err := s.opts.Store.LadoEffectiveWatchSettings(ctx, project)
	if err != nil {
		return 0, false, fmt.Errorf("read the watch settings of project %s: %w", project, err)
	}
	return watch.CompactWindow, true, nil
}

// LaunchValues returns the environment values LADO puts on a launch of
// harness for the session rec, nil for none: the compact window for Claude
// Code, and for Codex the value the Chat driver moves into the thread config.
// env is the launch environment so far, which it reads but does not change. A
// Claude Code start with auto-compact turned off records AuditCompactBlocked
// once per session.
func (s *Service) LaunchValues(ctx context.Context, rec domain.SessionRecord, harness domain.AgentHarness, env map[string]string) (map[string]string, error) {
	window, ok, err := s.CompactWindow(ctx, rec.ProjectID)
	if err != nil || !ok {
		return nil, err
	}
	value := strconv.Itoa(window)
	switch harness {
	case domain.HarnessClaudeCode:
		s.auditBlocked(ctx, rec, env)
		return map[string]string{domain.LadoClaudeCompactWindowEnv: value}, nil
	case domain.HarnessCodex:
		return map[string]string{domain.LadoCodexCompactLimitEnv: value}, nil
	}
	return nil, nil
}

// ReviewerValues returns the environment values LADO puts on an AO reviewer
// of the worker's project on harness, nil for none.
func (s *Service) ReviewerValues(ctx context.Context, project domain.ProjectID, harness domain.AgentHarness) (map[string]string, error) {
	window, ok, err := s.CompactWindow(ctx, project)
	if err != nil || !ok {
		return nil, err
	}
	value := strconv.Itoa(window)
	switch harness {
	case domain.HarnessClaudeCode:
		return map[string]string{domain.LadoClaudeCompactWindowEnv: value}, nil
	case domain.HarnessCodex:
		return map[string]string{domain.LadoCodexCompactLimitEnv: value}, nil
	}
	return nil, nil
}

// CodexOverrides returns the -c options that carry the compact limit into a
// Codex terminal session, nil for none. The codex adapter appends them to
// its launch and restore commands.
func (s *Service) CodexOverrides(ctx context.Context, sessionID string) ([]string, error) {
	if s == nil || s.opts.Store == nil || sessionID == "" {
		return nil, nil
	}
	rec, found, err := s.opts.Store.GetSession(ctx, domain.SessionID(sessionID))
	if err != nil {
		return nil, fmt.Errorf("read session %s: %w", sessionID, err)
	}
	if !found {
		return nil, nil
	}
	window, ok, err := s.CompactWindow(ctx, rec.ProjectID)
	if err != nil || !ok {
		return nil, err
	}
	return []string{"-c", domain.LadoCodexCompactLimitKey + "=" + strconv.Itoa(window)}, nil
}

// auditBlocked records AuditCompactBlocked when the session starts with
// auto-compact turned off. It never fails the launch.
func (s *Service) auditBlocked(ctx context.Context, rec domain.SessionRecord, env map[string]string) {
	s.mu.Lock()
	done := s.blocked[rec.ID]
	s.mu.Unlock()
	if done || rec.ID == "" {
		return
	}
	info := s.compactWarnings(ctx, compactInput{project: rec.ProjectID, workspace: rec.Metadata.WorkspacePath, env: env})
	if len(info) == 0 {
		return
	}
	payload, err := json.Marshal(struct {
		ProjectID domain.ProjectID `json:"projectId"`
		Harness   string           `json:"harness"`
		Warnings  []CompactWarning `json:"warnings"`
	}{rec.ProjectID, string(domain.HarnessClaudeCode), info})
	if err != nil {
		return
	}
	if _, err := s.opts.Store.AppendLadoAuditEvent(context.WithoutCancel(ctx), domain.LadoAuditEvent{
		At:          s.opts.Now().UTC(),
		Actor:       "lado",
		Action:      AuditCompactBlocked,
		SubjectKind: "session",
		SubjectID:   string(rec.ID),
		Payload:     payload,
	}); err != nil {
		s.opts.Log.Warn("LADO could not record that a session starts with auto-compact turned off", "session", rec.ID, "err", err)
		return
	}
	s.mu.Lock()
	s.blocked[rec.ID] = true
	s.mu.Unlock()
}
