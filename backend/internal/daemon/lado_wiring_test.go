package daemon

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoproject"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type ladoGateSink struct {
	gate ports.WorkGate
}

func (s *ladoGateSink) SetWorkGate(gate ports.WorkGate) { s.gate = gate }

// SetWorkGate keeps wiring_test.go's fake a sessionLifecycle.
func (f *fakeSessionLifecycle) SetWorkGate(ports.WorkGate) {}

func TestLadoDevModeReadsAOToggleValues(t *testing.T) {
	cases := map[string]bool{
		"": false, "on": true, "ON": true, " true ": true, "1": true, "yes": true,
		"off": false, "0": false, "no": false, "maybe": false,
	}
	for raw, want := range cases {
		if got := ladoDevMode(raw); got != want {
			t.Errorf("ladoDevMode(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestWireLadoInstallsOneGateOnEverySendAndSpawnPath(t *testing.T) {
	t.Setenv(ladoDevEnv, "on")
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	sessions, chat := &ladoGateSink{}, &ladoGateSink{}

	projects := wireLado(store, sessions, chat, slog.New(slog.DiscardHandler))

	if sessions.gate == nil || sessions.gate != chat.gate {
		t.Fatalf("session manager gate %v, chat gate %v; want one gate on both", sessions.gate, chat.gate)
	}
	on := true
	if _, err := projects.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{LadoMode: &on}); err != nil {
		t.Fatalf("turn LADO mode on: %v", err)
	}
	err := sessions.gate.AuthorizeSpawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "LADO_TASK_REQUIRED" {
		t.Fatalf("worker spawn after LADO mode on = %v, want LADO_TASK_REQUIRED", err)
	}
}

func TestWireLadoKeepsTheModeSwitchOffByDefault(t *testing.T) {
	t.Setenv(ladoDevEnv, "")
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	projects := wireLado(store, &ladoGateSink{}, &ladoGateSink{}, slog.New(slog.DiscardHandler))

	on := true
	_, err := projects.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{LadoMode: &on})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "LADO_MODE_UNAVAILABLE" {
		t.Fatalf("turn LADO mode on without %s = %v, want LADO_MODE_UNAVAILABLE", ladoDevEnv, err)
	}
}
