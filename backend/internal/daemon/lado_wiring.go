package daemon

// LADO fork: LADO's daemon wiring (docs/LJY-PRD/TDD.md §1.1, §5.1 and §6).

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoconnect"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladogate"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoproject"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
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

// wireLadoWork builds LADO's repository connections and issue-backed tasks.
// The daemon calls it once AO's project service exists, after
// installLadoCredentials opened the token store.
func wireLadoWork(cfg config.Config, store *sqlite.Store, projects ladoProjectCloner, log *slog.Logger) (*ladoconnect.Service, *ladotask.Service) {
	if ladoStoredTokens == nil {
		installLadoCredentials(cfg, log)
	}
	clients := newLadoRepoClients(cfg.GitLab)
	connections := ladoconnect.New(ladoconnect.Deps{
		Credentials: ladoStoredTokens,
		Clients:     clients,
		Projects:    store,
		Cloner:      ladoCloner{projects: projects},
		Log:         log,
	})
	tasks := ladotask.New(ladotask.Deps{Store: store, Projects: store, Clients: clients, Log: log})
	return connections, tasks
}

// ladoProjectCloner is AO's clone flow, with LADO's clones into a workspace.
type ladoProjectCloner interface {
	Clone(ctx context.Context, in projectsvc.CloneInput) (projectsvc.Project, error)
	CloneIntoNewWorkspace(ctx context.Context, parent, name string, in projectsvc.LadoWorkspaceCloneInput) (projectsvc.Project, error)
	CloneIntoWorkspace(ctx context.Context, id domain.ProjectID, in projectsvc.LadoWorkspaceCloneInput) (projectsvc.Project, error)
}

// ladoCloner clones through AO's clone flow, which registers the project or
// the workspace's new repository.
type ladoCloner struct {
	projects ladoProjectCloner
}

func (c ladoCloner) Clone(ctx context.Context, in ladoconnect.CloneRequest) (domain.ProjectID, error) {
	var (
		project projectsvc.Project
		err     error
	)
	workspaceClone := projectsvc.LadoWorkspaceCloneInput{RemoteURL: in.RemoteURL, GitConfig: in.GitConfig}
	switch in.Placement {
	case ladoconnect.PlaceNewWorkspace:
		project, err = c.projects.CloneIntoNewWorkspace(ctx, in.DestinationParent, in.WorkspaceName, workspaceClone)
	case ladoconnect.PlaceWorkspace:
		project, err = c.projects.CloneIntoWorkspace(ctx, in.WorkspaceID, workspaceClone)
	default:
		project, err = c.projects.Clone(ctx, projectsvc.CloneInput{
			RemoteURL:         in.RemoteURL,
			DestinationParent: in.DestinationParent,
			GitConfig:         in.GitConfig,
		})
	}
	if err != nil {
		return "", err
	}
	return project.ID, nil
}

// ladoDevMode accepts the values AO's own toggles accept for "on".
func ladoDevMode(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true", "1", "yes":
		return true
	}
	return false
}
