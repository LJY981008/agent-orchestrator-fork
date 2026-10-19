// Package ladogate is LADO's worker gate (docs/LJY-PRD/TDD.md §6): in a project
// with LADO mode on, workers start only when LADO dispatches them, and only a
// person or AO itself messages a worker.
package ladogate

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Settings reads LADO's per-project settings.
type Settings interface {
	LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error)
}

// Sessions reads the session a message is addressed to.
type Sessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Gate implements ports.WorkGate. Every read error refuses the work.
type Gate struct {
	settings Settings
	sessions Sessions
}

var _ ports.WorkGate = (*Gate)(nil)

// New returns a gate over LADO's settings and AO's sessions.
func New(settings Settings, sessions Sessions) *Gate {
	return &Gate{settings: settings, sessions: sessions}
}

// AuthorizeSpawn refuses automations and every new worker in a LADO project;
// orchestrators still start there. LADO's dispatcher (build step 6) adds the
// one path that may start workers: a grant for the lease it holds.
func (g *Gate) AuthorizeSpawn(ctx context.Context, cfg ports.SpawnConfig) error {
	if cfg.ProjectID == "" {
		return nil
	}
	on, err := g.ladoMode(ctx, cfg.ProjectID)
	if err != nil || !on {
		return err
	}
	switch {
	case cfg.AutomationRunID != nil:
		return apierr.Conflict("LADO_AUTOMATION_REFUSED",
			"This project is in LADO mode: automations cannot start sessions in it.",
			map[string]any{"projectId": string(cfg.ProjectID)})
	case cfg.Kind == domain.KindOrchestrator:
		return nil
	}
	return apierr.Conflict("LADO_TASK_REQUIRED",
		"This project is in LADO mode: workers start only from approved tasks. Create a task instead.",
		map[string]any{"projectId": string(cfg.ProjectID)})
}

// AuthorizeSend refuses messages from other sessions and from automation to a
// worker of a LADO project. People, AO's report relay and AO's own nudges still
// reach it, and messages to orchestrators are never refused.
func (g *Gate) AuthorizeSend(ctx context.Context, target domain.SessionID, origin ports.SendOrigin) error {
	switch origin {
	case ports.SendOriginHuman, ports.SendOriginCoordination, ports.SendOriginDaemon:
		return nil
	}
	rec, found, err := g.sessions.GetSession(ctx, target)
	if err != nil {
		return fmt.Errorf("LADO gate: read session %s: %w", target, err)
	}
	if !found || rec.ProjectID == "" || rec.Kind == domain.KindOrchestrator {
		return nil
	}
	on, err := g.ladoMode(ctx, rec.ProjectID)
	if err != nil || !on {
		return err
	}
	return apierr.Conflict("LADO_SEND_REFUSED",
		"This project is in LADO mode: only a person or AO itself can message its workers.",
		map[string]any{"sessionId": string(target), "origin": string(origin)})
}

func (g *Gate) ladoMode(ctx context.Context, projectID domain.ProjectID) (bool, error) {
	settings, err := g.settings.LadoProjectSettings(ctx, projectID)
	if err != nil {
		return false, fmt.Errorf("LADO gate: %w", err)
	}
	return settings.LadoMode, nil
}
