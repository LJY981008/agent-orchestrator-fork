package ladoproject_test

import (
	"context"
	"encoding/json"
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
	// proofs are what SetLadoModeWithProof recorded; events are the audit
	// events of every write that has one.
	proofs map[string]bool
	events []domain.LadoAuditEvent
}

func (f *fakeStore) LadoProjectSettings(_ context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error) {
	if f.readErr != nil {
		return domain.LadoProjectSettings{}, f.readErr
	}
	return f.stored(projectID), nil
}

// stored is the project's settings as the store keeps them: mode off and cap
// 1 without a row.
func (f *fakeStore) stored(projectID domain.ProjectID) domain.LadoProjectSettings {
	if settings, ok := f.settings[projectID]; ok {
		return settings
	}
	return domain.LadoProjectSettings{ProjectID: projectID, MaxConcurrent: 1}
}

func (f *fakeStore) save(settings domain.LadoProjectSettings) {
	if f.settings == nil {
		f.settings = map[domain.ProjectID]domain.LadoProjectSettings{}
	}
	f.settings[settings.ProjectID] = settings
}

func (f *fakeStore) SetLadoMode(_ context.Context, projectID domain.ProjectID, on bool, at time.Time) (domain.LadoProjectSettings, error) {
	f.writes++
	if f.writeErr != nil {
		return domain.LadoProjectSettings{}, f.writeErr
	}
	settings := f.stored(projectID)
	settings.LadoMode, settings.UpdatedAt = on, at
	f.save(settings)
	return settings, nil
}

func (f *fakeStore) SetLadoModeWithProof(ctx context.Context, projectID domain.ProjectID, on bool, at time.Time, proof domain.LadoPresenceProof, event domain.LadoAuditEvent) (domain.LadoProjectSettings, error) {
	if f.proofs[proof.Nonce] {
		return domain.LadoProjectSettings{}, domain.ErrLadoPresenceReplayed
	}
	settings, err := f.SetLadoMode(ctx, projectID, on, at)
	if err != nil {
		return settings, err
	}
	if f.proofs == nil {
		f.proofs = map[string]bool{}
	}
	f.proofs[proof.Nonce] = true
	f.events = append(f.events, event)
	return settings, nil
}

func (f *fakeStore) SetLadoProjectMaxConcurrent(_ context.Context, projectID domain.ProjectID, n int, event domain.LadoAuditEvent) error {
	f.writes++
	if f.writeErr != nil {
		return f.writeErr
	}
	settings := f.stored(projectID)
	settings.MaxConcurrent, settings.UpdatedAt = n, event.At
	f.save(settings)
	f.events = append(f.events, event)
	return nil
}

// presence answers as a verified proof for the action it is asked about.
func presence(nonce string) func(action, subject string) (domain.LadoPresenceProof, error) {
	return func(action, subject string) (domain.LadoPresenceProof, error) {
		return domain.LadoPresenceProof{Nonce: nonce, Action: action, SubjectID: subject, ExpiresAt: now.Add(time.Minute), UsedAt: now}, nil
	}
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

func newService(store *fakeStore) *ladoproject.Service {
	return ladoproject.New(ladoproject.Deps{
		Store: store, Projects: projects(),
		Clock: func() time.Time { return now },
	})
}

func boolPtr(v bool) *bool { return &v }

func intPtr(v int) *int { return &v }

// noPresence fails the test when the service asks for presence.
func noPresence(t *testing.T) func(action, subject string) (domain.LadoPresenceProof, error) {
	return func(action, _ string) (domain.LadoPresenceProof, error) {
		t.Helper()
		t.Fatalf("the change asked for presence (%s)", action)
		return domain.LadoPresenceProof{}, nil
	}
}

func requireAPIError(t *testing.T, err error, kind apierr.Kind, code string) {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != kind || apiErr.Code != code {
		t.Fatalf("error = %v, want %s (kind %d)", err, code, kind)
	}
}

func TestSettingsNeedAKnownProject(t *testing.T) {
	svc := newService(&fakeStore{})
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
	got, err := newService(&fakeStore{}).Settings(context.Background(), "mer")
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.ProjectID != "mer" || got.LadoMode || got.MaxConcurrent != 1 {
		t.Fatalf("settings = %+v, want mer with LADO mode off and cap 1", got)
	}
}

func TestUpdateSettingsAcceptsAnUnchangedValue(t *testing.T) {
	store := &fakeStore{}
	got, err := newService(store).UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{LadoMode: boolPtr(false), MaxConcurrent: intPtr(1)})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if got.LadoMode || got.MaxConcurrent != 1 || store.writes != 0 {
		t.Fatalf("settings = %+v after %d writes, want mode off, cap 1 and no write", got, store.writes)
	}
}

// LADO mode changes without a development flag now that the dispatcher
// starts approved tasks; the rules for each direction stay.
func TestModeSwitchNeedsNoDevFlag(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	svc := ladoproject.New(ladoproject.Deps{Store: store, Projects: projects(), Clock: func() time.Time { return now }})

	got, err := svc.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{LadoMode: boolPtr(true), Presence: noPresence(t)})
	if err != nil {
		t.Fatalf("turn LADO mode on: %v", err)
	}
	if !got.LadoMode || !got.UpdatedAt.Equal(now) || store.writes != 1 || len(store.events) != 0 {
		t.Fatalf("settings = %+v after %d writes and events %+v, want mode on at %s in one write", got, store.writes, store.events, now)
	}

	// Turning it off still needs presence.
	_, err = svc.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{LadoMode: boolPtr(false)})
	requireAPIError(t, err, apierr.KindForbidden, "LADO_PRESENCE_REQUIRED")
	if settings, _ := store.LadoProjectSettings(ctx, "mer"); !settings.LadoMode || store.writes != 1 {
		t.Fatalf("settings = %+v after %d writes, want mode still on", settings, store.writes)
	}
	got, err = svc.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{LadoMode: boolPtr(false), Presence: presence("nonce-1")})
	if err != nil || got.LadoMode || len(store.events) != 1 || store.events[0].Action != "project.lado_mode_off" {
		t.Fatalf("turn off with presence = %+v, %v, events %+v", got, err, store.events)
	}

	// Turning it on refuses while tracker intake is on.
	_, err = svc.UpdateSettings(ctx, "intake", ladoproject.UpdateInput{LadoMode: boolPtr(true)})
	requireAPIError(t, err, apierr.KindConflict, "LADO_TRACKER_INTAKE_ENABLED")
	if settings, _ := store.LadoProjectSettings(ctx, "intake"); settings.LadoMode || store.writes != 2 {
		t.Fatalf("intake project = %+v after %d writes, want mode off and no write", settings, store.writes)
	}
}

func TestUpdateSettingsSetsMaxConcurrent(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	svc := newService(store)

	_, err := svc.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{})
	requireAPIError(t, err, apierr.KindInvalid, "LADO_SETTINGS_EMPTY")
	for _, n := range []int{-1, 0, 9} {
		_, err := svc.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{MaxConcurrent: intPtr(n)})
		requireAPIError(t, err, apierr.KindInvalid, "LADO_DISPATCH_SETTINGS_INVALID")
	}
	if store.writes != 0 {
		t.Fatalf("writes = %d after refused changes, want none", store.writes)
	}

	// The cap changes without presence and is audited as the API's.
	got, err := svc.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{MaxConcurrent: intPtr(3), Presence: noPresence(t)})
	if err != nil {
		t.Fatalf("set the cap: %v", err)
	}
	if got.MaxConcurrent != 3 || got.LadoMode || !got.UpdatedAt.Equal(now) || store.writes != 1 {
		t.Fatalf("settings = %+v after %d writes, want cap 3 with LADO mode off in one write", got, store.writes)
	}
	if len(store.events) != 1 {
		t.Fatalf("events = %+v, want one", store.events)
	}
	event := store.events[0]
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode payload %s: %v", event.Payload, err)
	}
	if event.Action != "project.max_concurrent_changed" || event.Actor != "api" || event.SubjectKind != "project" || event.SubjectID != "mer" ||
		!event.At.Equal(now) || payload["from"] != float64(1) || payload["to"] != float64(3) {
		t.Fatalf("audit event = %+v with payload %v, want the API changing mer's cap from 1 to 3", event, payload)
	}
	if got, err := svc.Settings(ctx, "mer"); err != nil || got.MaxConcurrent != 3 {
		t.Fatalf("Settings = %+v, %v; want cap 3", got, err)
	}
	if _, err := svc.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{MaxConcurrent: intPtr(3)}); err != nil || store.writes != 1 {
		t.Fatalf("the same cap again = %v after %d writes, want no write", err, store.writes)
	}

	// With the mode in one request: turning it on and the cap both apply. A
	// refused mode change writes no cap either.
	got, err = svc.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{LadoMode: boolPtr(true), MaxConcurrent: intPtr(2)})
	if err != nil || !got.LadoMode || got.MaxConcurrent != 2 || store.writes != 3 {
		t.Fatalf("mode on with cap 2 = %+v, %v after %d writes", got, err, store.writes)
	}
	_, err = svc.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{LadoMode: boolPtr(false), MaxConcurrent: intPtr(4)})
	requireAPIError(t, err, apierr.KindForbidden, "LADO_PRESENCE_REQUIRED")
	if settings, _ := store.LadoProjectSettings(ctx, "mer"); !settings.LadoMode || settings.MaxConcurrent != 2 || store.writes != 3 {
		t.Fatalf("after a refused mode change: %+v after %d writes, want mode on and cap 2", settings, store.writes)
	}
	got, err = svc.UpdateSettings(ctx, "mer", ladoproject.UpdateInput{LadoMode: boolPtr(false), MaxConcurrent: intPtr(4), Presence: presence("nonce-1")})
	if err != nil || got.LadoMode || got.MaxConcurrent != 4 {
		t.Fatalf("mode off with cap 4 = %+v, %v", got, err)
	}

	_, err = svc.UpdateSettings(ctx, "missing", ladoproject.UpdateInput{MaxConcurrent: intPtr(2)})
	requireAPIError(t, err, apierr.KindNotFound, "PROJECT_NOT_FOUND")
}

func TestUpdateSettingsTurnsLadoModeOffWhateverTheIntake(t *testing.T) {
	store := &fakeStore{settings: map[domain.ProjectID]domain.LadoProjectSettings{
		"intake": {ProjectID: "intake", LadoMode: true},
	}}
	got, err := newService(store).UpdateSettings(context.Background(), "intake", ladoproject.UpdateInput{LadoMode: boolPtr(false), Presence: presence("nonce-1")})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if got.LadoMode || store.writes != 1 {
		t.Fatalf("settings = %+v after %d writes, want mode off in one write", got, store.writes)
	}
	if !store.proofs["nonce-1"] || len(store.events) != 1 || store.events[0].Action != "project.lado_mode_off" || store.events[0].SubjectID != "intake" {
		t.Fatalf("proofs %v, events %+v; want the proof and its audit event", store.proofs, store.events)
	}
}

func TestTurningLadoModeOffNeedsPresence(t *testing.T) {
	on := func() *fakeStore {
		return &fakeStore{settings: map[domain.ProjectID]domain.LadoProjectSettings{"mer": {ProjectID: "mer", LadoMode: true}}}
	}
	store := on()
	_, err := newService(store).UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{LadoMode: boolPtr(false)})
	requireAPIError(t, err, apierr.KindForbidden, "LADO_PRESENCE_REQUIRED")
	if store.writes != 0 {
		t.Fatalf("writes = %d, want none", store.writes)
	}
	refused := func(string, string) (domain.LadoPresenceProof, error) {
		return domain.LadoPresenceProof{}, apierr.Forbidden("LADO_PRESENCE_INVALID", "bad proof")
	}
	_, err = newService(store).UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{LadoMode: boolPtr(false), Presence: refused})
	requireAPIError(t, err, apierr.KindForbidden, "LADO_PRESENCE_INVALID")
	if store.writes != 0 {
		t.Fatalf("writes = %d after a refused proof, want none", store.writes)
	}

	store = on()
	svc := newService(store)
	if _, err := svc.UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{LadoMode: boolPtr(false), Presence: presence("nonce-1")}); err != nil {
		t.Fatalf("turn off: %v", err)
	}
	if _, err := svc.UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{LadoMode: boolPtr(true), Presence: func(string, string) (domain.LadoPresenceProof, error) {
		t.Fatal("turning LADO mode on asked for presence")
		return domain.LadoPresenceProof{}, nil
	}}); err != nil {
		t.Fatalf("turn on: %v", err)
	}
	_, err = svc.UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{LadoMode: boolPtr(false), Presence: presence("nonce-1")})
	requireAPIError(t, err, apierr.KindForbidden, "LADO_PRESENCE_REPLAYED")
}

func TestSettingsReportStorageFailures(t *testing.T) {
	failure := errors.New("database is locked")
	cases := []struct {
		name string
		svc  *ladoproject.Service
		code string
	}{
		{"project read", ladoproject.New(ladoproject.Deps{Store: &fakeStore{}, Projects: fakeProjects{err: failure}}), "PROJECT_LOAD_FAILED"},
		{"settings read", newService(&fakeStore{readErr: failure}), "LADO_SETTINGS_LOAD_FAILED"},
		{"settings write", newService(&fakeStore{writeErr: failure}), "LADO_SETTINGS_UPDATE_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.svc.UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{LadoMode: boolPtr(true)})
			requireAPIError(t, err, apierr.KindInternal, tc.code)
			_, err = tc.svc.UpdateSettings(context.Background(), "mer", ladoproject.UpdateInput{MaxConcurrent: intPtr(2)})
			requireAPIError(t, err, apierr.KindInternal, tc.code)
		})
	}
}
