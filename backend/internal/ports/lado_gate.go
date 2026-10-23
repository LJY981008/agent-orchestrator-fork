package ports

// LADO fork: the worker gate (docs/LJY-PRD/TDD.md §6 and
// docs/LJY-PRD/adr/0003-enforcement-points.md).

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// WorkGate authorizes work in LADO projects. A nil gate keeps upstream
// behavior, and projects with LADO mode off are never affected.
type WorkGate interface {
	// AuthorizeSpawn refuses a new session that LADO did not dispatch.
	AuthorizeSpawn(ctx context.Context, cfg SpawnConfig) error
	// AuthorizeSend refuses a message to target from an origin that LADO does
	// not allow for it.
	AuthorizeSend(ctx context.Context, target domain.SessionID, origin SendOrigin) error
	// ClampTurnSettings caps the approval of a LADO execution's next turns at
	// its rule snapshot's permission ceiling. Other sessions get s unchanged.
	ClampTurnSettings(ctx context.Context, target domain.SessionID, s ChatTurnSettings) (ChatTurnSettings, error)
	// AuthorizeConfigMode refuses a config option that would switch a LADO
	// execution to an approval mode above its ceiling: the provider applies the
	// option before AO records it.
	AuthorizeConfigMode(ctx context.Context, target domain.SessionID, mode domain.PermissionMode) error
	// AuthorizeExecutionChange refuses an agent switch or an interface
	// transition of a LADO execution, which runs as it was approved.
	AuthorizeExecutionChange(ctx context.Context, target domain.SessionID) error
}

// SendOrigin is who a message to a session comes from, read from the facts AO
// already records with each delivery.
type SendOrigin string

// Send origins.
const (
	// SendOriginHuman is a person: the desktop, or `ao send` run outside a
	// session.
	SendOriginHuman SendOrigin = "human"
	// SendOriginSession is another AO session, such as `ao send` from an
	// orchestrator.
	SendOriginSession SendOrigin = "session"
	// SendOriginCoordination is AO relaying worker reports.
	SendOriginCoordination SendOrigin = "coordination"
	// SendOriginDaemon is a message that names no sender: AO's own lifecycle
	// nudges and notices, and cue prompts.
	SendOriginDaemon SendOrigin = "daemon"
	// SendOriginAutomation is a chat message attributed to automation that
	// names no sender session.
	SendOriginAutomation SendOrigin = "automation"
)
