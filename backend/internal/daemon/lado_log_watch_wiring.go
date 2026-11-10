package daemon

// LADO fork: wiring of the production log watch (issue #87,
// docs/LJY-PRD/adr/0012-log-watch.md). The runner follows the Docker Engine
// API sources of each project whose log watch and LADO mode are on; its
// incident handler turns what it finds into issues, tasks, plan requests and
// orchestrator notices through LADO's task service and the notice outbox
// that service/ladonotify delivers.

import (
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladologwatch"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var (
	_ controllers.LadoLogWatchService = (*ladologwatch.Service)(nil)
	_ ladologwatch.ServiceStore       = (*sqlite.Store)(nil)
	_ ladologwatch.ServiceProjects    = (*sqlite.Store)(nil)
	_ ladologwatch.IncidentStore      = (*sqlite.Store)(nil)
	_ ladologwatch.IncidentNotices    = (*sqlite.Store)(nil)
	_ ladologwatch.IncidentTasks      = (*ladotask.Service)(nil)
	_ ladologwatch.IncidentPlans      = (*ladotask.Service)(nil)
	_ ladologwatch.IncidentSink       = (*ladologwatch.IncidentHandler)(nil)
)

// wireLadoLogWatch builds the log watch: its incident handler over LADO's
// store and task service, and the runner with the TLS copies under
// <StateDir>/lado/credentials/docker and the Docker Engine API sources. The
// daemon starts it with Start once workers may spawn (an automatic plan
// request starts a plan worker) and waits on the channel Start returns,
// which closes once every log stream is closed. Until a project turns its
// log watch on, the runner sends no request to any Engine.
func wireLadoLogWatch(cfg config.Config, store *sqlite.Store, tasks *ladotask.Service, log *slog.Logger) *ladologwatch.Service {
	handler := ladologwatch.NewIncidentHandler(ladologwatch.HandlerDeps{
		Store:   store,
		Tasks:   tasks,
		Plans:   tasks,
		Notices: store,
		Log:     log,
	})
	return ladologwatch.New(ladologwatch.Deps{
		Store:    store,
		Sink:     handler,
		Certs:    ladologwatch.NewCertStore(cfg.StateDir),
		Projects: store,
		Notices:  store,
		Log:      log,
	})
}
