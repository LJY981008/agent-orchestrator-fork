package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type ladoSenderStub struct{ err error }

func (s ladoSenderStub) Send(context.Context, domain.SessionID, string, *ports.SpawnAttachment) error {
	return s.err
}

type ladoSessionsStub map[domain.SessionID]domain.SessionRecord

func (s ladoSessionsStub) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := s[id]
	return rec, ok, nil
}

func TestLadoOrchestratorsReadTheSessionAndItsArtifactDirectory(t *testing.T) {
	dataDir := t.TempDir()
	store := ladoSessionsStub{
		"mer-orch-1": {ID: "mer-orch-1", Kind: domain.KindOrchestrator, Metadata: domain.SessionMetadata{ArtifactDir: "/data/artifacts/mer-orch-1"}},
		"mer-orch-2": {ID: "mer-orch-2", Kind: domain.KindOrchestrator, IsTerminated: true},
	}
	orchestrators := ladoOrchestrators{store: store, dataDir: dataDir}
	got, ok, err := orchestrators.Session(context.Background(), "mer-orch-1")
	if err != nil || !ok || got.ID != "mer-orch-1" || got.ArtifactDir != "/data/artifacts/mer-orch-1" || got.Terminated {
		t.Fatalf("Session = %+v, %v, %v", got, ok, err)
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

func TestLadoDevPresenceReadsAOToggleValues(t *testing.T) {
	cases := map[string]bool{
		"": false, "on": true, "ON": true, " true ": true, "1": true, "yes": true,
		"off": false, "0": false, "no": false, "maybe": false,
	}
	for raw, want := range cases {
		if got := ladoDevPresenceOn(raw); got != want {
			t.Errorf("ladoDevPresenceOn(%q) = %v, want %v", raw, got, want)
		}
	}
}

type ladoSessionListStub []domain.SessionRecord

func (s ladoSessionListStub) ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error) {
	return s, nil
}

// Notices go to the project's newest live orchestrator, which LADO never
// starts for them.
func TestLadoNoticeOrchestratorsFindTheNewestLiveOne(t *testing.T) {
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	o := ladoNoticeOrchestrators{store: ladoSessionListStub{
		{ID: "mer-orch-1", Kind: domain.KindOrchestrator, CreatedAt: at, Activity: domain.Activity{State: domain.ActivityActive}},
		{ID: "mer-orch-2", Kind: domain.KindOrchestrator, CreatedAt: at.Add(time.Hour), Activity: domain.Activity{State: domain.ActivityIdle}},
		{ID: "mer-orch-3", Kind: domain.KindOrchestrator, CreatedAt: at.Add(2 * time.Hour), IsTerminated: true},
		{ID: "mer-4", Kind: domain.KindWorker, CreatedAt: at.Add(3 * time.Hour)},
	}}
	id, activity, ok, err := o.Orchestrator(context.Background(), "mer")
	if err != nil || !ok || id != "mer-orch-2" || activity != domain.ActivityIdle {
		t.Fatalf("Orchestrator = %s %s %v %v", id, activity, ok, err)
	}
	none := ladoNoticeOrchestrators{store: ladoSessionListStub{{ID: "mer-4", Kind: domain.KindWorker}}}
	if _, _, ok, err := none.Orchestrator(context.Background(), "mer"); ok || err != nil {
		t.Fatalf("a project without a live orchestrator = %v, %v", ok, err)
	}
}
