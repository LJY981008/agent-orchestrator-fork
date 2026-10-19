package daemon

// LADO fork: LADO's daemon wiring (docs/LJY-PRD/TDD.md §1.1 and §6).

import (
	"log/slog"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladogate"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoproject"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// ladoDevEnv turns on LADO features that are not ready for daily use. Today
// that is changing a project's LADO mode: until LADO's dispatcher exists (build
// step 6), a project in LADO mode cannot start any worker.
const ladoDevEnv = "AO_LADO_DEV"

// ladoGateTarget is a component that delivers messages or starts sessions.
type ladoGateTarget interface {
	SetWorkGate(ports.WorkGate)
}

// wireLado installs one worker gate on the session manager and the chat
// service, and returns LADO's project settings service for the API. The daemon
// calls it right after the session manager is built, before automations,
// tracker intake or the API can spawn or send.
func wireLado(store *sqlite.Store, sessions, chat ladoGateTarget, log *slog.Logger) *ladoproject.Service {
	gate := ladogate.New(store, store)
	sessions.SetWorkGate(gate)
	chat.SetWorkGate(gate)
	devMode := ladoDevMode(os.Getenv(ladoDevEnv))
	if devMode {
		log.Warn("LADO dev mode: project LADO mode can be changed", "env", ladoDevEnv)
	}
	return ladoproject.New(ladoproject.Deps{Store: store, Projects: store, ModeSwitch: devMode})
}

// ladoDevMode accepts the values AO's own toggles accept for "on".
func ladoDevMode(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true", "1", "yes":
		return true
	}
	return false
}
