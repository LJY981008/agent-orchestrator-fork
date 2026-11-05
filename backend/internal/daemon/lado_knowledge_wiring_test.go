package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// Both places that install LADO's standing instructions, wireLado and the
// setup's, which replaces it, give a LADO project's sessions the project
// knowledge section with the store's folder (issue #24).
func TestWireLadoGivesBothPromptSourcesTheProjectKnowledge(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	store := sqlitetest.MustOpen(t)
	at := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: at}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := store.SetLadoMode(ctx, "mer", true, at); err != nil {
		t.Fatalf("turn LADO mode on: %v", err)
	}
	log := slog.New(slog.DiscardHandler)
	knowledge := newLadoKnowledge(cfg, store, "", log)
	dir := filepath.Join(cfg.StateDir, "lado", "knowledge", "mer")
	if knowledge.Dir("mer") != dir {
		t.Fatalf("knowledge folder = %q, want %q under the state directory", knowledge.Dir("mer"), dir)
	}

	wantKnowledge := func(source string, sink func() (domain.LadoStandingSection, error)) {
		t.Helper()
		section, err := sink()
		if err != nil {
			t.Fatalf("%s: standing section: %v", source, err)
		}
		if !strings.Contains(section.Text, "## Project knowledge (LADO)") || !strings.Contains(section.Text, dir) {
			t.Fatalf("%s: standing section has no project knowledge with %s:\n%s", source, dir, section.Text)
		}
	}

	sessions := &ladoGateSink{}
	wireLado(store, sessions, &ladoGateSink{}, knowledge, log)
	wantKnowledge("wireLado", func() (domain.LadoStandingSection, error) {
		return sessions.prompt(ctx, domain.KindOrchestrator, "mer", "mer-orchestrator-1", domain.HarnessClaudeCode)
	})

	setupSessions := &ladoSetupSink{}
	home := t.TempDir()
	wireLadoSetup(cfg, store, ladoblob.New(cfg.StateDir), ladoSetupDeps{
		folders:   ladoToolFolders{claudeDir: filepath.Join(home, ".claude"), claudeJSON: filepath.Join(home, ".claude.json"), codexHome: filepath.Join(home, ".codex")},
		sessions:  setupSessions,
		processes: func(context.Context) ([]procmem.Process, error) { return nil, nil },
		reviewers: func(reviewcore.LadoSystemSection) {},
		knowledge: knowledge,
	}, log)
	if setupSessions.prompt == nil {
		t.Fatal("wireLadoSetup installed no LADO prompt section")
	}
	wantKnowledge("wireLadoSetup", func() (domain.LadoStandingSection, error) {
		return setupSessions.prompt(ctx, domain.KindOrchestrator, "mer", "mer-orchestrator-1", domain.HarnessCodex)
	})

	// Without a knowledge store the section stays out.
	bare := &ladoGateSink{}
	wireLado(store, bare, &ladoGateSink{}, nil, nil)
	if section, err := bare.prompt(ctx, domain.KindOrchestrator, "mer", "mer-orchestrator-1", domain.HarnessClaudeCode); err != nil || strings.Contains(section.Text, "## Project knowledge (LADO)") {
		t.Fatalf("standing section without a knowledge store = %+v, %v; want no knowledge section", section, err)
	}
}

// The knowledge service the daemon builds serves the API's knowledge routes,
// which answer 501 without one.
func TestLadoKnowledgeRoutesAreServed(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	log := slog.New(slog.DiscardHandler)
	get := func(deps httpd.APIDeps) *httptest.ResponseRecorder {
		router := httpd.NewRouterWithControl(cfg, log, nil, deps, httpd.ControlDeps{})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/lado/projects/mer/knowledge", nil))
		return w
	}

	if w := get(httpd.APIDeps{}); w.Code != http.StatusNotImplemented {
		t.Fatalf("knowledge route without a store = %d, want 501", w.Code)
	}
	w := get(httpd.APIDeps{LadoKnowledge: newLadoKnowledge(cfg, store, "", log)})
	if w.Code != http.StatusOK {
		t.Fatalf("knowledge route = %d %s, want 200", w.Code, w.Body)
	}
	var body struct {
		ProjectID string `json:"projectId"`
		Dir       string `json:"dir"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.ProjectID != "mer" || body.Dir != filepath.Join(cfg.StateDir, "lado", "knowledge", "mer") {
		t.Fatalf("knowledge route body = %s (%v)", w.Body, err)
	}
}
