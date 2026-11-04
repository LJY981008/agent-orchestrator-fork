package daemon

// LADO fork: the worker watch in the daemon (issue #42). Its launch hooks go
// into the session manager, the Codex adapter and AO's reviewer launcher
// before anything spawns or restores: the compact window of a project in
// LADO mode and the folder of a run's pinned plan. The dispatcher and the
// plan workers hold new workers while their account is at its usage limit or
// their agent cannot start. A one-minute loop counts the compactions, watches
// the running workers (ladowatch) and raises the usage alerts (ladoalert), in
// that order, whether or not anyone has the Usage tab open.

import (
	"context"
	"log/slog"
	"time"

	codexagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoalert"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladolaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladousage"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladowatch"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// ladoWatchInterval is how often the watch loop runs (plan decision 10).
const ladoWatchInterval = time.Minute

// ladoWatchSessions is the session manager's side of the worker watch: the
// launch values and the read folders of a LADO run's Chat sessions.
type ladoWatchSessions interface {
	SetLadoLaunchValues(sessionmanager.LadoLaunchValues)
	SetLadoAdditionalDirectories(sessionmanager.LadoReadDirectories)
}

// ladoCodexCompact is the Codex adapter, whose launch and restore commands
// take the compact limit as a -c option.
type ladoCodexCompact interface {
	SetLadoCompactOverrides(fn func(ctx context.Context, sessionID string) ([]string, error))
}

// The session manager and the Codex adapter take the hooks; a signature
// that drifts fails the build rather than leaving the hooks out at run time.
var (
	_ ladoWatchSessions = (*sessionmanager.Manager)(nil)
	_ ladoCodexCompact  = (*codexagent.Plugin)(nil)
)

// ladoWatchServices are the worker watch's services.
type ladoWatchServices struct {
	launch *ladolaunch.Service
	usage  *ladousage.Service
	watch  *ladowatch.Service
	alerts *ladoalert.Service
}

// ladoWatchHooks are where the watch's launch hooks go. A nil field installs
// nothing there, as in tests of other parts.
type ladoWatchHooks struct {
	sessions any
	agents   ports.AgentResolver
	// reviewerEnv installs the reviewers' values; nil uses
	// review.SetLadoReviewerEnv, which only a daemon with sessions calls.
	reviewerEnv func(reviewcore.LadoReviewerEnv)
}

// wireLadoWatch builds the worker watch's services over the store and
// installs their launch hooks. accounts tells a session's agent account and
// sender delivers the wake messages; without them the watch takes every
// session to run on the computer's login, and only records and alerts.
func wireLadoWatch(cfg config.Config, store *sqlite.Store, hooks ladoWatchHooks, accounts ladowatch.Accounts, sender ladoMessageSender, log *slog.Logger) ladoWatchServices {
	launch := ladolaunch.New(ladolaunch.Options{DataDir: cfg.DataDir, Store: store, Projects: store, Log: log})
	usage := ladousage.New(ladousage.Deps{Store: store, Usage: usagesvc.NewSummaryReader(store), Log: log})
	deps := ladowatch.Deps{
		Store:          store,
		Sessions:       ladoWatchSessionRecords{store: store},
		Conversations:  store,
		CompactCounter: usage,
		Log:            log,
	}
	if accounts != nil {
		deps.Accounts = accounts
	}
	if sender != nil {
		deps.Messenger = ladoWakeMessenger{sender: sender}
	}
	watch := ladowatch.New(deps)
	alerts := ladoalert.New(ladoalert.Deps{Store: store, Usage: usage, Log: log})

	if sessions, ok := hooks.sessions.(ladoWatchSessions); ok && hooks.sessions != nil {
		sessions.SetLadoLaunchValues(launch)
		sessions.SetLadoAdditionalDirectories(ladotask.NewPlanDirResolver(store, cfg.DataDir).AdditionalDirectories)
		reviewerEnv := hooks.reviewerEnv
		if reviewerEnv == nil {
			reviewerEnv = reviewcore.SetLadoReviewerEnv
		}
		reviewerEnv(launch.ReviewerValues)
	} else if hooks.sessions != nil {
		log.Error("LADO cannot give sessions their compact window: the session manager takes no launch values")
	}
	if hooks.agents != nil {
		if agent, ok := hooks.agents.Agent(domain.HarnessCodex); ok {
			if codex, ok := agent.(ladoCodexCompact); ok {
				codex.SetLadoCompactOverrides(launch.CodexOverrides)
			} else {
				log.Error("LADO cannot give Codex terminal sessions their compact limit: the Codex adapter takes no launch options")
			}
		}
	}
	return ladoWatchServices{launch: launch, usage: usage, watch: watch, alerts: alerts}
}

// startLadoWatch runs the watch loop every interval until ctx ends: the
// compactions and the workers first (ladowatch.Tick, which counts the
// compactions before anything else), then the usage alerts, which read the
// counts. A failed pass is logged and the next one tries again. The channel
// closes once the loop stopped.
func startLadoWatch(ctx context.Context, w ladoWatchServices, interval time.Duration, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runLadoWatchPass(ctx, w, log)
			}
		}
	}()
	return done
}

// runLadoWatchPass is one pass of the watch loop.
func runLadoWatchPass(ctx context.Context, w ladoWatchServices, log *slog.Logger) {
	if w.watch != nil {
		if err := w.watch.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Warn("LADO watch pass failed", "err", err)
		}
	}
	if w.alerts != nil && ctx.Err() == nil {
		if err := w.alerts.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Warn("LADO alert pass failed", "err", err)
		}
	}
}

// ladoWatchSessionRecords reads AO's session records for the watch.
type ladoWatchSessionRecords struct {
	store ladoSessionReader
}

func (r ladoWatchSessionRecords) Session(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	return r.store.GetSession(ctx, id)
}

// ladoWakeMessenger delivers LADO's wake messages as the daemon, which the
// worker gate lets reach a LADO worker.
type ladoWakeMessenger struct {
	sender ladoMessageSender
}

func (m ladoWakeMessenger) Send(ctx context.Context, id domain.SessionID, message string) error {
	return m.sender.Send(ctx, id, message, nil)
}
