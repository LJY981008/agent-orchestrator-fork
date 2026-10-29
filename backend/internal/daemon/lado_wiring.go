package daemon

// LADO fork: LADO's daemon wiring (docs/LJY-PRD/TDD.md §1.1, §5.1 and §6).

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoconnect"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladogate"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladonotify"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoproject"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoqueue"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladosetup"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// ladoGateTarget is a component that delivers messages or starts sessions.
type ladoGateTarget interface {
	SetWorkGate(ports.WorkGate)
}

// ladoSessionTarget is the session manager: it also builds orchestrators'
// standing instructions.
type ladoSessionTarget interface {
	ladoGateTarget
	SetLadoPrompt(sessionmanager.LadoPromptSection)
}

// wireLado installs one worker gate on the session manager and the chat
// service, and LADO's section of a session's standing instructions: an
// orchestrator's in a LADO project, the rules text of a worker LADO
// dispatched, and a plan worker's role. It returns LADO's project settings service for the API. The
// daemon calls it right after the session manager is built, before
// automations, tracker intake or the API can spawn or send, and before
// sessions restore.
func wireLado(store *sqlite.Store, sessions ladoSessionTarget, chat ladoGateTarget) *ladoproject.Service {
	gate := ladogate.New(store, store, store, store, store)
	gate.SetPlanWorkers(store)
	sessions.SetWorkGate(gate)
	chat.SetWorkGate(gate)
	sessions.SetLadoPrompt(ladotask.PromptSource{Settings: store, Sessions: store, Policies: store, Rules: store}.Section)
	return ladoproject.New(ladoproject.Deps{Store: store, Projects: store})
}

// ladoWork is LADO's services wireLadoWork builds.
type ladoWork struct {
	connections *ladoconnect.Service
	tasks       *ladotask.Service
	rules       *ladopolicy.Service
	queue       *ladoqueue.Service
	setup       *ladosetup.Service
	verify      *ladoverify.Service
}

// wireLadoWork builds LADO's repository connections, the local setup it
// manages with its session hooks, its rules with the built-in ones seeded,
// issue-backed tasks with their plans, which plan workers write and whose
// notices reach the orchestrators, whose approvals fix the rules and the
// setup and queue the plans, the dispatcher that starts the queued plans'
// workers and stops the worker of a cancelled task, and the loop that
// verifies the workers' PRs. The daemon calls it once AO's project service
// exists, after installLadoCredentials opened the token store and before
// sessions restore, and starts the dispatcher, then the verification loop,
// once workers may spawn. Without AO's session service the dispatcher only
// reads; it must not start.
func wireLadoWork(ctx context.Context, cfg config.Config, store *sqlite.Store, projects ladoProjectCloner, work ladoWorkDeps, log *slog.Logger) ladoWork {
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
	blobs := ladoblob.New(cfg.StateDir)
	setup := wireLadoSetup(cfg, store, blobs, work.setup, log)
	rules := ladopolicy.New(ladopolicy.Deps{Store: store, Projects: store, Blobs: blobs, AOBuild: cfg.Telemetry.AppVersion, Setup: setup, Log: log})
	setup.SetPolicy(rules)
	// A failed seed leaves the permission ceiling missing, which resolves to
	// a blocking problem the app shows, so the daemon still starts.
	if err := rules.EnsureBuiltins(ctx); err != nil {
		log.Error("LADO could not seed its built-in rules", "err", err)
	}
	deps := ladotask.Deps{Store: store, Projects: store, Clients: clients, Log: log, Blobs: blobs, Policy: rules, DataDir: cfg.DataDir}
	if work.sender != nil {
		deps.Orchestrators = ladoOrchestrators{sender: work.sender, store: store, dataDir: cfg.DataDir}
		deps.Notices = ladonotify.New(ladonotify.Deps{Store: store, Orchestrators: ladoNoticeOrchestrators{store: store, sender: work.sender}, Log: log})
	}
	if work.sessions != nil && work.sender != nil {
		deps.PlanSessions = ladoPlanSessions{sessions: work.sessions, sender: work.sender, store: store}
	}
	if work.artifacts != nil {
		deps.Artifacts = ladoArtifacts{capturer: work.artifacts, store: store}
	}
	tasks := ladotask.New(deps)
	queue := ladoqueue.New(ladoqueue.Deps{
		Store:    store,
		Tasks:    tasks,
		Policy:   rules,
		Sessions: work.sessions,
		Projects: store,
		Branches: work.branches,
		Memory:   work.memory,
		FreeDisk: ladoqueue.FreeDiskBytes,
		DataDir:  cfg.DataDir,
		Log:      log,
	})
	if work.sessions != nil {
		tasks.SetKiller(queue)
	}
	tasks.SetPlanQueue(queue)
	verify := wireLadoVerify(store, blobs, clients, work, log)
	return ladoWork{connections: connections, tasks: tasks, rules: rules, queue: queue, setup: setup, verify: verify}
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
