package daemon

// LADO fork: what LADO's plans and approvals need from AO: the project's
// orchestrator, its artifact versions and the desktop token for presence
// proofs (docs/LJY-PRD/TDD.md §5.1 and §5.2,
// docs/LJY-PRD/adr/0002-human-presence-approval-channel.md).

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/artifactversions"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// ladoDevPresenceEnv lets approvals through without the desktop app's
// confirmation, for the renderer running in a browser during development.
const ladoDevPresenceEnv = "AO_LADO_DEV_PRESENCE"

// newLadoPresence builds the presence verifier from the daemon's desktop
// token. fromApp says the token came from the desktop app over stdin; a
// daemon that made up its own shares it with nobody, so its approvals fail
// closed.
func newLadoPresence(token string, fromApp bool, log *slog.Logger) *ladopresence.Verifier {
	dev := ladoDevPresenceOn(os.Getenv(ladoDevPresenceEnv))
	if dev {
		log.Warn("LADO dev presence: approvals are accepted without the desktop app's confirmation", "env", ladoDevPresenceEnv)
	}
	if !fromApp {
		log.Info("LADO approvals need the AO desktop app: this daemon did not get its token from the app")
	}
	return ladopresence.New(token, fromApp, dev, nil)
}

// ladoDevPresenceOn accepts the values AO's own toggles accept for "on".
func ladoDevPresenceOn(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true", "1", "yes":
		return true
	}
	return false
}

// ladoWorkDeps are the AO parts LADO's plans use. A missing part leaves the
// plan loop off, as in tests that only need tasks.
type ladoWorkDeps struct {
	orchestrators ladoOrchestratorSpawner
	sender        ladoMessageSender
	artifacts     *artifactversions.Capturer
}

// ladoOrchestratorSpawner is AO's session service.
type ladoOrchestratorSpawner interface {
	SpawnOrchestrator(ctx context.Context, projectID domain.ProjectID, clean bool, mode domain.SessionMode, approval domain.PermissionMode) (domain.Session, error)
}

// ladoMessageSender is AO's session manager.
type ladoMessageSender interface {
	Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error
}

// ladoSessionReader reads AO's session records.
type ladoSessionReader interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// ladoOrchestrators finds a project's orchestrator through AO's session
// service and messages it through the session manager.
type ladoOrchestrators struct {
	spawner ladoOrchestratorSpawner
	sender  ladoMessageSender
	store   ladoSessionReader
	dataDir string
}

// Orchestrator returns the project's newest active orchestrator, or starts
// one, as the desktop does when a project is added.
func (o ladoOrchestrators) Orchestrator(ctx context.Context, projectID domain.ProjectID) (ladotask.OrchestratorSession, error) {
	sess, err := o.spawner.SpawnOrchestrator(ctx, projectID, false, "", "")
	if err != nil {
		return ladotask.OrchestratorSession{}, err
	}
	found, ok, err := o.Session(ctx, sess.ID)
	if err != nil {
		return ladotask.OrchestratorSession{}, err
	}
	if !ok {
		return ladotask.OrchestratorSession{}, fmt.Errorf("orchestrator %s is not recorded", sess.ID)
	}
	return found, nil
}

func (o ladoOrchestrators) Session(ctx context.Context, id domain.SessionID) (ladotask.OrchestratorSession, bool, error) {
	rec, ok, err := o.store.GetSession(ctx, id)
	if err != nil || !ok {
		return ladotask.OrchestratorSession{}, ok, err
	}
	return ladotask.OrchestratorSession{
		ID:          rec.ID,
		ArtifactDir: cmp.Or(rec.Metadata.ArtifactDir, sessionartifacts.Dir(o.dataDir, rec.ID)),
		Terminated:  rec.IsTerminated,
	}, true, nil
}

// Send delivers a plan request as the daemon. A session that is gone or
// whose agent exited cannot take it however often LADO tries.
func (o ladoOrchestrators) Send(ctx context.Context, id domain.SessionID, message string) error {
	err := o.sender.Send(ctx, id, message, nil)
	switch {
	case errors.Is(err, sessionmanager.ErrNotFound), errors.Is(err, sessionmanager.ErrTerminated):
		return fmt.Errorf("%w: %w", ladotask.ErrOrchestratorGone, err)
	case errors.Is(err, sessionmanager.ErrAgentExited):
		return fmt.Errorf("%w: its agent exited; restart the orchestrator, then ask again: %w", ladotask.ErrOrchestratorGone, err)
	}
	return err
}

// ladoArtifacts reads the plan versions AO captured.
type ladoArtifacts struct {
	capturer *artifactversions.Capturer
	store    ladoSessionReader
}

func (a ladoArtifacts) Versions(ctx context.Context, id domain.SessionID, path string) ([]domain.SessionArtifactVersion, error) {
	return a.capturer.List(ctx, id, path)
}

func (a ladoArtifacts) Read(ctx context.Context, version domain.SessionArtifactVersion) ([]byte, error) {
	rec, ok, err := a.store.GetSession(ctx, version.SessionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("session %s is gone", version.SessionID)
	}
	return a.capturer.ReadVersion(rec, version)
}
