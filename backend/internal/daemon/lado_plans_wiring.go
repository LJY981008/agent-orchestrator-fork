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
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoqueue"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
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

// ladoWorkDeps are the AO parts LADO's plans and its dispatcher use. A
// missing part leaves the plan loop off, or the dispatcher unable to start
// workers, as in tests that only need tasks.
type ladoWorkDeps struct {
	sender    ladoMessageSender
	artifacts *artifactversions.Capturer
	// sessions starts and stops workers: AO's session service.
	sessions ladoqueue.Sessions
	// branches resolves the branch a worker starts from: the session
	// manager, through its workspace.
	branches ladoqueue.Branches
	// memory reads memory pressure: the daemon's usage memory reader.
	memory ladoqueue.Memory
	// setup is what LADO's local setup uses: the tool folders and the
	// session hooks.
	setup ladoSetupDeps
	// reviews is AO's review service, which reviews an execution's PR when
	// its plan requires a review.
	reviews reviewsvc.Manager
	// ladoModes is LADO's project settings service, whose turn-on of LADO
	// mode reads the protection of the project's base branches again.
	ladoModes ladoModeHook
}

// ladoMessageSender is AO's session manager.
type ladoMessageSender interface {
	Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error
}

// ladoSessionReader reads AO's session records.
type ladoSessionReader interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// ladoOrchestrators reads the orchestrators of plan requests made before
// plan workers in AO's store and messages them through the session manager.
type ladoOrchestrators struct {
	sender  ladoMessageSender
	store   ladoSessionReader
	dataDir string
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

// ladoPlanSessionService is what plan workers use of AO's session service:
// spawning and stopping sessions.
type ladoPlanSessionService interface {
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error)
	RequestKill(ctx context.Context, id domain.SessionID) (sessionmanager.KillResult, error)
}

// ladoPlanSessionStore reads plan worker sessions.
type ladoPlanSessionStore interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	GetSessionByClientRequestID(ctx context.Context, key string) (domain.SessionRecord, bool, error)
}

// ladoPlanSessions starts, reads, messages and stops LADO's plan workers
// through AO's session service, store and session manager
// (ladotask.PlanSessions).
type ladoPlanSessions struct {
	sessions ladoPlanSessionService
	sender   ladoMessageSender
	store    ladoPlanSessionStore
}

func (p ladoPlanSessions) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	return p.sessions.Spawn(ctx, cfg)
}

func (p ladoPlanSessions) Session(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	return p.store.GetSession(ctx, id)
}

func (p ladoPlanSessions) SessionByClientRequest(ctx context.Context, id string) (domain.SessionRecord, bool, error) {
	return p.store.GetSessionByClientRequestID(ctx, id)
}

// Send delivers a message as the daemon, which the worker gate lets reach a
// plan worker.
func (p ladoPlanSessions) Send(ctx context.Context, id domain.SessionID, message string) error {
	return p.sender.Send(ctx, id, message, nil)
}

func (p ladoPlanSessions) Stop(ctx context.Context, id domain.SessionID) error {
	_, err := p.sessions.RequestKill(ctx, id)
	return err
}

// ladoNoticeOrchestrators finds a project's live orchestrator in AO's store,
// without starting one, and messages it as the daemon (ladonotify).
type ladoNoticeOrchestrators struct {
	store interface {
		ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	}
	sender ladoMessageSender
}

func (o ladoNoticeOrchestrators) Orchestrator(ctx context.Context, project domain.ProjectID) (domain.SessionID, domain.ActivityState, bool, error) {
	recs, err := o.store.ListSessions(ctx, project)
	if err != nil {
		return "", "", false, err
	}
	var newest domain.SessionRecord
	for _, rec := range recs {
		if rec.Kind == domain.KindOrchestrator && !rec.IsTerminated && (newest.ID == "" || rec.CreatedAt.After(newest.CreatedAt)) {
			newest = rec
		}
	}
	if newest.ID == "" {
		return "", "", false, nil
	}
	return newest.ID, newest.Activity.State, true, nil
}

func (o ladoNoticeOrchestrators) Send(ctx context.Context, id domain.SessionID, message string) error {
	return o.sender.Send(ctx, id, message, nil)
}
