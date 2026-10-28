package daemon

// LADO fork: LADO's local setup in the daemon (step 7 plan §9, §10 and §13).
// The service gets the computer's own tool folders as the daemon finds them,
// and its hooks go into the session manager, the Codex adapter and AO's
// reviewer launcher before anything spawns or restores, so every session AO
// launches gets the setup LADO manages and every reviewer its instructions.
// The first scan and the removal of ended sessions' config homes run in the
// background once the daemon starts.

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladosetup"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// ladoSetupIgnoreProcessesEnv leaves the computer's Claude Code and Codex
// programs out of what holds an adoption or a restore back, for labs that run
// the daemon inside one of them.
const ladoSetupIgnoreProcessesEnv = "AO_LADO_DEV_SETUP_IGNORE_PROCESSES"

// ladoToolFolders are the computer's own Claude Code and Codex folders.
type ladoToolFolders struct {
	// claudeDir is the Claude folder and claudeJSON Claude's global config;
	// claudeFromEnv reports that claudeDir came from CLAUDE_CONFIG_DIR.
	claudeDir, claudeJSON string
	claudeFromEnv         bool
	codexHome             string
}

// deviceClaudeFolder finds the computer's own Claude folder and global
// config file as Claude Code does: $CLAUDE_CONFIG_DIR with the .claude.json
// inside it, else ~/.claude and ~/.claude.json. Both are empty without
// either.
func deviceClaudeFolder() (dir, configFile string, fromEnv bool) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir, filepath.Join(dir, ".claude.json"), true
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json"), false
	}
	return "", "", false
}

// ladoDeviceFolders are the computer's tool folders, with codexHome the
// Codex folder the daemon resolved.
func ladoDeviceFolders(codexHome string) ladoToolFolders {
	dir, configFile, fromEnv := deviceClaudeFolder()
	return ladoToolFolders{claudeDir: dir, claudeJSON: configFile, claudeFromEnv: fromEnv, codexHome: codexHome}
}

// ladoSetupSessions is the session manager: it launches Claude Code with a
// config home and Codex Chat sessions with their MCP servers, and builds the
// standing instructions.
type ladoSetupSessions interface {
	SetLadoSetupHome(ports.LadoSetupHome)
	SetLadoChatServers(sessionmanager.LadoChatServers)
	SetLadoPrompt(sessionmanager.LadoPromptSection)
}

// ladoCodexOverrides is the Codex adapter, whose launch and restore commands
// take LADO's -c options.
type ladoCodexOverrides interface {
	SetLadoOverrides(fn func(ctx context.Context, sessionID string) ([]string, error))
}

// ladoSetupDeps are the AO parts LADO's local setup uses. Without sessions or
// agents no hook goes in, as in tests of other parts.
type ladoSetupDeps struct {
	folders  ladoToolFolders
	sessions ladoSetupSessions
	// agents resolves the adapters that launch sessions; LADO's -c options
	// go on Codex's.
	agents ports.AgentResolver
	// processes lists the computer's processes; nil uses procmem.
	processes func(context.Context) ([]procmem.Process, error)
	// reviewers installs LADO's section of AO reviewers' system prompts;
	// nil uses review.SetLadoSystemSection.
	reviewers func(reviewcore.LadoSystemSection)
}

// wireLadoSetup builds LADO's local setup service over the daemon's folders
// and installs its hooks: Claude Code config homes and the MCP servers of
// Codex Chat sessions on the session manager, -c options on the Codex
// adapter, the Codex instructions in the LADO section of standing
// instructions, which replaces the one wireLado installed, and the adopted
// instructions in AO reviewers' system prompts.
func wireLadoSetup(cfg config.Config, store *sqlite.Store, blobs *ladoblob.Store, deps ladoSetupDeps, log *slog.Logger) *ladosetup.Service {
	ignore := os.Getenv(ladoSetupIgnoreProcessesEnv) == "1"
	if ignore {
		log.Warn("LADO dev setup: running Claude Code and Codex programs do not hold adoption or restore back", "env", ladoSetupIgnoreProcessesEnv)
	}
	setup := ladosetup.New(ladosetup.Deps{
		Store:              store,
		Blobs:              blobs,
		StateDir:           cfg.StateDir,
		ClaudeDir:          deps.folders.claudeDir,
		ClaudeJSON:         deps.folders.claudeJSON,
		ClaudeDirFromEnv:   deps.folders.claudeFromEnv,
		CodexHome:          deps.folders.codexHome,
		Projects:           store,
		Sessions:           store,
		Processes:          deps.processes,
		DevIgnoreProcesses: ignore,
		Log:                log,
	})
	if deps.sessions != nil {
		deps.sessions.SetLadoSetupHome(setup)
		deps.sessions.SetLadoChatServers(func(ctx context.Context, rec domain.SessionRecord) ([]ports.ChatMCPServerConfig, error) {
			return setup.CodexChatServers(ctx, rec.ID, rec.ClientRequestID, rec.ProjectID)
		})
		deps.sessions.SetLadoPrompt(ladotask.PromptSource{Settings: store, Sessions: store, Policies: store, Setup: setup}.Section)
		reviewers := deps.reviewers
		if reviewers == nil {
			reviewers = reviewcore.SetLadoSystemSection
		}
		reviewers(setup.ReviewerSection)
	}
	if deps.agents != nil {
		installLadoCodexOverrides(deps.agents, store, setup, log)
	}
	return setup
}

// installLadoCodexOverrides puts LADO's -c options on the Codex adapter that
// launches sessions. A session the store does not have, such as a
// reviewer's, gets none.
func installLadoCodexOverrides(agents ports.AgentResolver, sessions ladoSessionReader, setup *ladosetup.Service, log *slog.Logger) {
	agent, ok := agents.Agent(domain.HarnessCodex)
	if !ok {
		return
	}
	codex, ok := agent.(ladoCodexOverrides)
	if !ok {
		log.Error("LADO cannot give Codex sessions the setup it manages: the Codex adapter takes no launch options")
		return
	}
	codex.SetLadoOverrides(func(ctx context.Context, sessionID string) ([]string, error) {
		rec, ok, err := sessions.GetSession(ctx, domain.SessionID(sessionID))
		if err != nil || !ok {
			return nil, err
		}
		return setup.CodexOverrides(ctx, rec.ID, rec.ClientRequestID, rec.ProjectID)
	})
}

// startLadoSetup marks partial the adoptions a stopped daemon left pending,
// scans the local setup once, then removes the config homes of ended
// sessions, in the background, so the daemon starts without waiting. A
// failure is logged. The channel closes when all are done.
func startLadoSetup(ctx context.Context, setup *ladosetup.Service, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := setup.RecoverAdoptions(ctx); err != nil {
			log.Warn("LADO could not mark the adoptions a stopped daemon left unfinished", "err", err)
		}
		if _, err := setup.Scan(ctx); err != nil {
			log.Warn("LADO could not scan the local setup", "err", err)
		}
		if err := setup.CleanupHomes(ctx); err != nil {
			log.Warn("LADO could not remove the config homes of ended sessions", "err", err)
		}
	}()
	return done
}
