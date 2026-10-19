package ladoproject_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoproject"
)

var now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

type fakeStore struct {
	settings map[domain.ProjectID]domain.LadoProjectSettings
	readErr  error
	writeErr error
	writes   int
}

func (f *fakeStore) LadoProjectSettings(_ context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error) {
	if f.readErr != nil {
		return domain.LadoProjectSettings{}, f.readErr
	}
	if settings, ok := f.settings[projectID]; ok {
		return settings, nil
	}
	return domain.LadoProjectSettings{ProjectID: projectID}, nil
}

func (f *fakeStore) SetLadoMode(_ context.Context, projectID domain.ProjectID, on bool, at time.Time) (domain.LadoProjectSettings, error) {
	f.writes++
	if f.writeErr != nil {
		return domain.LadoProjectSettings{}, f.writeErr
	}
	if f.settings == nil {
		f.settings = map[domain.ProjectID]domain.LadoProjectSettings{}
	}
	settings := domain.LadoProjectSettings{ProjectID: projectID, LadoMode: on, UpdatedAt: at}
	f.settings[projectID] = settings
	return settings, nil
}

type fakeProjects struct {
	records map[string]domain.ProjectRecord
	err     error
}

func (f fakeProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	if f.err != nil {
		return domain.ProjectRecord{}, false, f.err
	}
	rec, ok := f.records[id]
	return rec, ok, nil
}

func projects() fakeProjects {
	return fakeProjects{records: map[string]domain.ProjectRecord{
		"mer":      {ID: "mer"},
		"intake":   {ID: "intake", Config: domain.ProjectConfig{TrackerIntake: domain.TrackerIntakeConfig{Enabled: true}}},
		"archived": {ID: "archived", ArchivedAt: now.Add(-time.Hour)},
	}}
}

func newService(store *fakeStore, modeSwitch bool) *ladoproject.Service {
	return ladoproject.New(ladoproject.Deps{
		Store: store, Projects: projects(), ModeSwitch: modeSwitch,
		Clock: func() time.Time { return now },
	})
}

func boolPtr(v bool) *bool { return &v }

func requireAPIError(t *testing.T, err error, kind apierr.Kind, code string) {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != kind || apiErr.Code != code {
		t.Fatalf("error = %v, want %s (kind %d)", err, code, kind)
	}
}

func TestSettingsNeedAKnownProject(t *testing.T) {
	svc := newService(&fakeStore{}, true)
	for _, id := range []domain.ProjectID{"missing", "archived"} {
		t.Run(string(id), func(t *testing.T) {
			_, err := svc.Settings(context.Background(), id)
			requireAPIError(t, err, apierr.KindNotFound, "PROJECT_NOT_FOUND")
			_, err = svc.UpdateSettings(context.Background(), id, ladoproject.UpdateInput{LadoMode: boolPtr(true)})
			requireAPIError(t, err, apierr.KindNotFound, "PROJECT_NOT_FOUND")
		})
	}
}

func TestSettingsDefaultToModeOff(t *testing.T) {
	got, err := newService(&fakeStore{}, false).Settings(context.Background(), "mer")
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.ProjectID != "mer" || got.LadoMode {
		t.Fatalf("settings = %+v, want mer with LADO mode off", got)
	}
}

func TestUpdateSettingsRequiresLadoMode(t *testing.T) {
	store := &fakeStore{}
	_, err := newService(store, true).UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{})
	requireAPIError(t, err, apierr.KindInvalid, "LADO_MODE_REQUIRED")
	if store.writes != 0 {
		t.Fatalf("writes = %d, want none", store.writes)
	}
}

func TestUpdateSettingsAcceptsAnUnchangedValueWithoutTheSwitch(t *testing.T) {
	store := &fakeStore{}
	got, err := newService(store, false).UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{LadoMode: boolPtr(false)})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if got.LadoMode || store.writes != 0 {
		t.Fatalf("settings = %+v after %d writes, want mode off and no write", got, store.writes)
	}
}

func TestUpdateSettingsRefusesAChangeWithoutTheSwitch(t *testing.T) {
	store := &fakeStore{}
	_, err := newService(store, false).UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{LadoMode: boolPtr(true)})
	requireAPIError(t, err, apierr.KindConflict, "LADO_MODE_UNAVAILABLE")
	if store.writes != 0 {
		t.Fatalf("writes = %d, want none", store.writes)
	}
}

func TestUpdateSettingsTurnsLadoModeOn(t *testing.T) {
	store := &fakeStore{}
	got, err := newService(store, true).UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{LadoMode: boolPtr(true)})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if !got.LadoMode || !got.UpdatedAt.Equal(now) || store.writes != 1 {
		t.Fatalf("settings = %+v after %d writes, want mode on at %s in one write", got, store.writes, now)
	}
}

func TestUpdateSettingsRefusesLadoModeWhileTrackerIntakeIsOn(t *testing.T) {
	store := &fakeStore{}
	_, err := newService(store, true).UpdateSettings(context.Background(), "intake", ladoproject.UpdateInput{LadoMode: boolPtr(true)})
	requireAPIError(t, err, apierr.KindConflict, "LADO_TRACKER_INTAKE_ENABLED")
	if store.writes != 0 {
		t.Fatalf("writes = %d, want none", store.writes)
	}
}

func TestUpdateSettingsTurnsLadoModeOffWhateverTheIntake(t *testing.T) {
	store := &fakeStore{settings: map[domain.ProjectID]domain.LadoProjectSettings{
		"intake": {ProjectID: "intake", LadoMode: true},
	}}
	got, err := newService(store, true).UpdateSettings(context.Background(), "intake", ladoproject.UpdateInput{LadoMode: boolPtr(false)})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if got.LadoMode || store.writes != 1 {
		t.Fatalf("settings = %+v after %d writes, want mode off in one write", got, store.writes)
	}
}

func TestSettingsReportStorageFailures(t *testing.T) {
	failure := errors.New("database is locked")
	cases := []struct {
		name string
		svc  *ladoproject.Service
		code string
	}{
		{"project read", ladoproject.New(ladoproject.Deps{Store: &fakeStore{}, Projects: fakeProjects{err: failure}, ModeSwitch: true}), "PROJECT_LOAD_FAILED"},
		{"settings read", newService(&fakeStore{readErr: failure}, true), "LADO_SETTINGS_LOAD_FAILED"},
		{"settings write", newService(&fakeStore{writeErr: failure}, true), "LADO_SETTINGS_UPDATE_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.svc.UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{LadoMode: boolPtr(true)})
			requireAPIError(t, err, apierr.KindInternal, tc.code)
		})
	}
}
