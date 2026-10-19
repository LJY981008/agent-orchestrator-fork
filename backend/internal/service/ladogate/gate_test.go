package ladogate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladogate"
)

// fakeSettings holds the projects that have LADO mode on.
type fakeSettings struct {
	ladoMode map[domain.ProjectID]bool
	err      error
}

func (f fakeSettings) LadoProjectSettings(_ context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error) {
	if f.err != nil {
		return domain.LadoProjectSettings{}, f.err
	}
	return domain.LadoProjectSettings{ProjectID: projectID, LadoMode: f.ladoMode[projectID]}, nil
}

type fakeSessions struct {
	records map[domain.SessionID]domain.SessionRecord
	err     error
}

func (f fakeSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if f.err != nil {
		return domain.SessionRecord{}, false, f.err
	}
	rec, ok := f.records[id]
	return rec, ok, nil
}

func ladoProject() fakeSettings {
	return fakeSettings{ladoMode: map[domain.ProjectID]bool{"lado": true}}
}

func requireRefusal(t *testing.T, err error, code string) {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want an API error %s", err, code)
	}
	if apiErr.Kind != apierr.KindConflict || apiErr.Code != code {
		t.Fatalf("error = %s (kind %d), want conflict %s", apiErr.Code, apiErr.Kind, code)
	}
}

// TestAuthorizeSpawnRefusesEveryWorkerPathInALadoProject covers each spawn path
// of TDD §6.2, shaped as its caller builds the config.
func TestAuthorizeSpawnRefusesEveryWorkerPathInALadoProject(t *testing.T) {
	gate := ladogate.New(ladoProject(), fakeSessions{})
	paths := []struct {
		name string
		cfg  ports.SpawnConfig
	}{
		{"sessions route", ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker, Prompt: "build it"}},
		{"sessions route without a kind", ports.SpawnConfig{ProjectID: "lado", Prompt: "build it"}},
		{"sessions route with a client request", ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker, ClientRequestID: "task-1", ClientRequestHash: "h"}},
		{"ao spawn from an orchestrator", ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker, ParentSessionID: "lado-orchestrator-1", Prompt: "build it"}},
		{"orchestrator delegate", ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker, ParentSessionID: "lado-orchestrator-1", NameFromPrompt: true}},
		{"tracker intake", ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker, IssueID: "github:owner/repo#7", Prompt: "issue"}},
		{"cue", ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker, Prompt: "cue prompt"}},
		{"client request named like a dispatch, before the dispatcher exists", ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker, ClientRequestID: "lado:entry-1"}},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			requireRefusal(t, gate.AuthorizeSpawn(context.Background(), path.cfg), "LADO_TASK_REQUIRED")
		})
	}
}

func TestAuthorizeSpawnRefusesAutomationsInALadoProject(t *testing.T) {
	gate := ladogate.New(ladoProject(), fakeSessions{})
	run := domain.AutomationRunID("run-1")
	for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
		t.Run(string(kind), func(t *testing.T) {
			err := gate.AuthorizeSpawn(context.Background(), ports.SpawnConfig{
				ProjectID: "lado", Kind: kind, Prompt: "nightly", AutomationRunID: &run,
			})
			requireRefusal(t, err, "LADO_AUTOMATION_REFUSED")
		})
	}
}

func TestAuthorizeSpawnAllowsWhatLadoDoesNotGate(t *testing.T) {
	gate := ladogate.New(ladoProject(), fakeSessions{})
	allowed := []struct {
		name string
		cfg  ports.SpawnConfig
	}{
		{"orchestrator start in a LADO project", ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindOrchestrator}},
		{"worker in a project without LADO mode", ports.SpawnConfig{ProjectID: "plain", Kind: domain.KindWorker, ParentSessionID: "plain-orchestrator-1"}},
		{"standalone worker", ports.SpawnConfig{Kind: domain.KindWorker, Prompt: "scratch"}},
	}
	for _, tc := range allowed {
		t.Run(tc.name, func(t *testing.T) {
			if err := gate.AuthorizeSpawn(context.Background(), tc.cfg); err != nil {
				t.Fatalf("AuthorizeSpawn = %v, want nil", err)
			}
		})
	}
}

func TestAuthorizeSpawnFailsClosedWhenSettingsCannotBeRead(t *testing.T) {
	readErr := errors.New("database is locked")
	gate := ladogate.New(fakeSettings{err: readErr}, fakeSessions{})

	err := gate.AuthorizeSpawn(context.Background(), ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker})
	if !errors.Is(err, readErr) {
		t.Fatalf("AuthorizeSpawn = %v, want the read error", err)
	}
}

func TestAuthorizeSendToALadoWorkerByOrigin(t *testing.T) {
	sessions := fakeSessions{records: map[domain.SessionID]domain.SessionRecord{
		"lado-1": {ID: "lado-1", ProjectID: "lado", Kind: domain.KindWorker},
	}}
	gate := ladogate.New(ladoProject(), sessions)
	cases := []struct {
		origin  ports.SendOrigin
		refused bool
	}{
		{ports.SendOriginHuman, false},
		{ports.SendOriginCoordination, false},
		{ports.SendOriginDaemon, false},
		{ports.SendOriginSession, true},
		{ports.SendOriginAutomation, true},
	}
	for _, tc := range cases {
		t.Run(string(tc.origin), func(t *testing.T) {
			err := gate.AuthorizeSend(context.Background(), "lado-1", tc.origin)
			if !tc.refused {
				if err != nil {
					t.Fatalf("AuthorizeSend = %v, want nil", err)
				}
				return
			}
			requireRefusal(t, err, "LADO_SEND_REFUSED")
		})
	}
}

func TestAuthorizeSendFromASessionLeavesOtherTargetsAlone(t *testing.T) {
	sessions := fakeSessions{records: map[domain.SessionID]domain.SessionRecord{
		"lado-orchestrator-1": {ID: "lado-orchestrator-1", ProjectID: "lado", Kind: domain.KindOrchestrator},
		"plain-1":             {ID: "plain-1", ProjectID: "plain", Kind: domain.KindWorker},
		"scratch-1":           {ID: "scratch-1", Kind: domain.KindWorker},
	}}
	gate := ladogate.New(ladoProject(), sessions)
	for _, target := range []domain.SessionID{"lado-orchestrator-1", "plain-1", "scratch-1", "missing-1"} {
		t.Run(string(target), func(t *testing.T) {
			if err := gate.AuthorizeSend(context.Background(), target, ports.SendOriginSession); err != nil {
				t.Fatalf("AuthorizeSend = %v, want nil", err)
			}
		})
	}
}

func TestAuthorizeSendFailsClosedWhenItCannotRead(t *testing.T) {
	readErr := errors.New("database is locked")
	worker := fakeSessions{records: map[domain.SessionID]domain.SessionRecord{
		"lado-1": {ID: "lado-1", ProjectID: "lado", Kind: domain.KindWorker},
	}}
	cases := []struct {
		name string
		gate *ladogate.Gate
	}{
		{"session", ladogate.New(ladoProject(), fakeSessions{err: readErr})},
		{"settings", ladogate.New(fakeSettings{err: readErr}, worker)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.gate.AuthorizeSend(context.Background(), "lado-1", ports.SendOriginSession)
			if !errors.Is(err, readErr) {
				t.Fatalf("AuthorizeSend = %v, want the read error", err)
			}
		})
	}
}
