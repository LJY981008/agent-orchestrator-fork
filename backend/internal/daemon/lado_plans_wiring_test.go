package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type ladoSpawnerStub struct{ session domain.Session }

func (s ladoSpawnerStub) SpawnOrchestrator(context.Context, domain.ProjectID, bool, domain.SessionMode, domain.PermissionMode) (domain.Session, error) {
	return s.session, nil
}

type ladoSenderStub struct{ err error }

func (s ladoSenderStub) Send(context.Context, domain.SessionID, string, *ports.SpawnAttachment) error {
	return s.err
}

type ladoSessionsStub map[domain.SessionID]domain.SessionRecord

func (s ladoSessionsStub) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := s[id]
	return rec, ok, nil
}

func TestLadoOrchestratorsFindTheSessionAndItsArtifactDirectory(t *testing.T) {
	dataDir := t.TempDir()
	store := ladoSessionsStub{
		"mer-orch-1": {ID: "mer-orch-1", Kind: domain.KindOrchestrator, Metadata: domain.SessionMetadata{ArtifactDir: "/data/artifacts/mer-orch-1"}},
		"mer-orch-2": {ID: "mer-orch-2", Kind: domain.KindOrchestrator, IsTerminated: true},
	}
	orchestrators := ladoOrchestrators{spawner: ladoSpawnerStub{session: domain.Session{ID: "mer-orch-1"}}, store: store, dataDir: dataDir}
	got, err := orchestrators.Orchestrator(context.Background(), "mer")
	if err != nil || got.ID != "mer-orch-1" || got.ArtifactDir != "/data/artifacts/mer-orch-1" || got.Terminated {
		t.Fatalf("Orchestrator = %+v, %v", got, err)
	}
	old, ok, err := orchestrators.Session(context.Background(), "mer-orch-2")
	if err != nil || !ok || !old.Terminated || old.ArtifactDir != filepath.Join(dataDir, "artifacts", "mer-orch-2") {
		t.Fatalf("Session without a recorded directory = %+v, %v, %v; want AO's default directory", old, ok, err)
	}
	if _, ok, _ := orchestrators.Session(context.Background(), "nope"); ok {
		t.Fatal("an unknown session was found")
	}
}

func TestLadoOrchestratorsSendTellsRetryableFailuresApart(t *testing.T) {
	for _, tc := range []struct {
		err  error
		gone bool
	}{
		{sessionmanager.ErrNotFound, true},
		{sessionmanager.ErrTerminated, true},
		{sessionmanager.ErrAgentExited, true},
		{sessionmanager.ErrStartupPending, false},
		{sessionmanager.ErrSwitchInProgress, false},
		{sessionmanager.ErrAwaitingDecision, false},
	} {
		orchestrators := ladoOrchestrators{sender: ladoSenderStub{err: tc.err}}
		err := orchestrators.Send(context.Background(), "mer-orch-1", "plan request")
		if got := errors.Is(err, ladotask.ErrOrchestratorGone); got != tc.gone || !errors.Is(err, tc.err) {
			t.Errorf("Send with %v: gone = %v (err %v), want gone %v and the cause kept", tc.err, got, err, tc.gone)
		}
	}
}
