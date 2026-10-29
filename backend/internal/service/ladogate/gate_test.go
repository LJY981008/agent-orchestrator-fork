package ladogate_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

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

// fakeLease is the lease the dispatcher holds on a queue entry.
type fakeLease struct {
	attempt    int
	project    domain.ProjectID
	hash       string
	leaseUntil time.Time
}

// fakeGrants answers like the store: an entry's grant is active while its
// lease holds it for the attempt asked about, and an unknown entry has none.
type fakeGrants struct {
	leases map[domain.LadoQueueEntryID]fakeLease
	// parts are the parts run starts in progress, by client request id.
	parts map[string]fakeLease
	err   error
}

func (f fakeGrants) LadoPartSpawnGrant(_ context.Context, request string, now time.Time) (domain.LadoDispatchGrant, error) {
	if f.err != nil {
		return domain.LadoDispatchGrant{}, f.err
	}
	lease, ok := f.parts[request]
	if !ok {
		return domain.LadoDispatchGrant{}, nil
	}
	return domain.LadoDispatchGrant{ProjectID: lease.project, Hash: lease.hash, Active: now.Before(lease.leaseUntil)}, nil
}

func (f fakeGrants) LadoDispatchGrant(_ context.Context, entryID domain.LadoQueueEntryID, attempt int, now time.Time) (domain.LadoDispatchGrant, error) {
	if f.err != nil {
		return domain.LadoDispatchGrant{}, f.err
	}
	lease, ok := f.leases[entryID]
	if !ok {
		return domain.LadoDispatchGrant{}, nil
	}
	return domain.LadoDispatchGrant{
		ProjectID: lease.project,
		Hash:      lease.hash,
		Active:    lease.attempt == attempt && now.Before(lease.leaseUntil),
	}, nil
}

// fakePolicies maps client request ids to the policy of their entry.
type fakePolicies struct {
	policies map[string]domain.LadoEntryPolicy
	err      error
}

func (f fakePolicies) LadoEntryPolicyByClientRequest(_ context.Context, clientRequestID string) (domain.LadoEntryPolicy, bool, error) {
	if f.err != nil {
		return domain.LadoEntryPolicy{}, false, f.err
	}
	policy, ok := f.policies[clientRequestID]
	return policy, ok, nil
}

// fakeExecutions holds LADO's executions and their tasks.
type fakeExecutions struct {
	executions map[domain.SessionID]domain.LadoExecution
	tasks      map[domain.LadoTaskID]domain.LadoTask
	err        error
}

func (f fakeExecutions) LadoExecution(_ context.Context, id domain.SessionID) (domain.LadoExecution, bool, error) {
	if f.err != nil {
		return domain.LadoExecution{}, false, f.err
	}
	exec, ok := f.executions[id]
	return exec, ok, nil
}

func (f fakeExecutions) LadoTask(_ context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error) {
	if f.err != nil {
		return domain.LadoTask{}, false, f.err
	}
	task, ok := f.tasks[id]
	return task, ok, nil
}

func ladoProject() fakeSettings {
	return fakeSettings{ladoMode: map[domain.ProjectID]bool{"lado": true}}
}

// newGate is a gate with no queue entries.
func newGate(settings ladogate.Settings, sessions ladogate.Sessions) *ladogate.Gate {
	return ladogate.New(settings, sessions, fakeGrants{}, fakePolicies{}, fakeExecutions{})
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
	gate := newGate(ladoProject(), fakeSessions{})
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
		{"client request named like a dispatch, without a grant", ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker, ClientRequestID: "lado:entry-1/1", ClientRequestHash: "h"}},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			requireRefusal(t, gate.AuthorizeSpawn(context.Background(), path.cfg), "LADO_TASK_REQUIRED")
		})
	}
}

func TestAuthorizeSpawnRefusesAutomationsInALadoProject(t *testing.T) {
	gate := newGate(ladoProject(), fakeSessions{})
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
	gate := newGate(ladoProject(), fakeSessions{})
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
	gate := newGate(fakeSettings{err: readErr}, fakeSessions{})

	err := gate.AuthorizeSpawn(context.Background(), ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker})
	if !errors.Is(err, readErr) {
		t.Fatalf("AuthorizeSpawn = %v, want the read error", err)
	}
}

func TestAuthorizeSendToALadoWorkerByOrigin(t *testing.T) {
	sessions := fakeSessions{records: map[domain.SessionID]domain.SessionRecord{
		"lado-1": {ID: "lado-1", ProjectID: "lado", Kind: domain.KindWorker},
	}}
	gate := newGate(ladoProject(), sessions)
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
	gate := newGate(ladoProject(), sessions)
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
		{"session", newGate(ladoProject(), fakeSessions{err: readErr})},
		{"settings", newGate(fakeSettings{err: readErr}, worker)},
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

// TestAuthorizeSpawnNeedsAnActiveGrant covers the one worker spawn a LADO
// project allows: the dispatcher's, for the entry and attempt it holds now.
func TestAuthorizeSpawnNeedsAnActiveGrant(t *testing.T) {
	now := time.Now()
	grants := fakeGrants{leases: map[domain.LadoQueueEntryID]fakeLease{
		"entry-1": {attempt: 2, project: "lado", hash: "hash-1", leaseUntil: now.Add(time.Hour)},
		"entry-2": {attempt: 1, project: "lado", hash: "hash-2", leaseUntil: now.Add(-time.Minute)},
		"entry-3": {attempt: 1, project: "other", hash: "hash-3", leaseUntil: now.Add(time.Hour)},
	}}
	gate := ladogate.New(ladoProject(), fakeSessions{}, grants, fakePolicies{}, fakeExecutions{})
	worker := func(clientRequestID, hash string) ports.SpawnConfig {
		return ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker, Prompt: "build it", ClientRequestID: clientRequestID, ClientRequestHash: hash}
	}
	if err := gate.AuthorizeSpawn(context.Background(), worker("lado:entry-1/2", "hash-1")); err != nil {
		t.Fatalf("AuthorizeSpawn with an active grant = %v, want nil", err)
	}
	refused := []struct {
		name string
		cfg  ports.SpawnConfig
	}{
		{"no client request", worker("", "")},
		{"not a dispatch id", worker("task-1", "hash-1")},
		{"dispatch id without an attempt", worker("lado:entry-1", "hash-1")},
		{"dispatch id with a padded attempt", worker("lado:entry-1/02", "hash-1")},
		{"another attempt", worker("lado:entry-1/1", "hash-1")},
		{"expired lease", worker("lado:entry-2/1", "hash-2")},
		{"entry of another project", worker("lado:entry-3/1", "hash-3")},
		{"another hash", worker("lado:entry-1/2", "hash-2")},
		{"no hash", worker("lado:entry-1/2", "")},
		{"unknown entry", worker("lado:entry-9/1", "hash-1")},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			requireRefusal(t, gate.AuthorizeSpawn(context.Background(), tc.cfg), "LADO_TASK_REQUIRED")
		})
	}

	run := domain.AutomationRunID("run-1")
	automation := worker("lado:entry-1/2", "hash-1")
	automation.AutomationRunID = &run
	requireRefusal(t, gate.AuthorizeSpawn(context.Background(), automation), "LADO_AUTOMATION_REFUSED")

	readErr := errors.New("database is locked")
	unreadable := ladogate.New(ladoProject(), fakeSessions{}, fakeGrants{err: readErr}, fakePolicies{}, fakeExecutions{})
	if err := unreadable.AuthorizeSpawn(context.Background(), worker("lado:entry-1/2", "hash-1")); !errors.Is(err, readErr) {
		t.Fatalf("AuthorizeSpawn when the grant cannot be read = %v, want the read error", err)
	}
	// What the gate never asks a grant for stays as it was.
	for _, cfg := range []ports.SpawnConfig{
		{ProjectID: "lado", Kind: domain.KindOrchestrator},
		{ProjectID: "plain", Kind: domain.KindWorker, Prompt: "build it"},
	} {
		if err := unreadable.AuthorizeSpawn(context.Background(), cfg); err != nil {
			t.Fatalf("AuthorizeSpawn %+v = %v, want nil", cfg, err)
		}
	}
}

// executionGate has one execution, lado-1 with ceiling auto, and sessions
// that are not executions.
func executionGate(sessionErr, policyErr error) *ladogate.Gate {
	sessions := fakeSessions{err: sessionErr, records: map[domain.SessionID]domain.SessionRecord{
		"lado-1":              {ID: "lado-1", ProjectID: "lado", Kind: domain.KindWorker, ClientRequestID: "lado:entry-1/1"},
		"lado-2":              {ID: "lado-2", ProjectID: "lado", Kind: domain.KindWorker, ClientRequestID: "lado:entry-2/1"},
		"plain-1":             {ID: "plain-1", ProjectID: "plain", Kind: domain.KindWorker, ClientRequestID: "task-1"},
		"lado-orchestrator-1": {ID: "lado-orchestrator-1", ProjectID: "lado", Kind: domain.KindOrchestrator},
	}}
	policies := fakePolicies{err: policyErr, policies: map[string]domain.LadoEntryPolicy{
		"lado:entry-1/1": {Ceiling: domain.PermissionModeAuto, Prompt: "Write commit messages in Korean."},
		// A snapshot without a ceiling never dispatches; the gate refuses
		// rather than guess one.
		"lado:entry-2/1": {},
	}}
	return ladogate.New(ladoProject(), sessions, fakeGrants{}, policies, fakeExecutions{})
}

func TestClampTurnSettingsCapsExecutions(t *testing.T) {
	gate := executionGate(nil, nil)
	turn := func(approval domain.PermissionMode) ports.ChatTurnSettings {
		return ports.ChatTurnSettings{Model: "gpt-5.5", Effort: "high", Approval: approval}
	}
	cases := []struct {
		name   string
		target domain.SessionID
		in     domain.PermissionMode
		want   domain.PermissionMode
	}{
		{"above the ceiling", "lado-1", domain.PermissionModeBypassPermissions, domain.PermissionModeAuto},
		{"default", "lado-1", domain.PermissionModeDefault, domain.PermissionModeAuto},
		{"at the ceiling", "lado-1", domain.PermissionModeAuto, domain.PermissionModeAuto},
		{"below the ceiling", "lado-1", domain.PermissionModeAcceptEdits, domain.PermissionModeAcceptEdits},
		{"no choice", "lado-1", "", ""},
		{"not an execution", "plain-1", domain.PermissionModeBypassPermissions, domain.PermissionModeBypassPermissions},
		{"orchestrator", "lado-orchestrator-1", domain.PermissionModeDefault, domain.PermissionModeDefault},
		{"unknown session", "missing-1", domain.PermissionModeBypassPermissions, domain.PermissionModeBypassPermissions},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gate.ClampTurnSettings(context.Background(), tc.target, turn(tc.in))
			if err != nil {
				t.Fatalf("ClampTurnSettings: %v", err)
			}
			if want := turn(tc.want); got != want {
				t.Fatalf("ClampTurnSettings = %+v, want %+v", got, want)
			}
		})
	}
	if _, err := gate.ClampTurnSettings(context.Background(), "lado-2", turn(domain.PermissionModeAcceptEdits)); err == nil {
		t.Fatal("ClampTurnSettings for an execution without a ceiling = nil, want an error")
	}
	readErr := errors.New("database is locked")
	for name, unreadable := range map[string]*ladogate.Gate{
		"session": executionGate(readErr, nil),
		"policy":  executionGate(nil, readErr),
	} {
		if _, err := unreadable.ClampTurnSettings(context.Background(), "lado-1", turn(domain.PermissionModeAuto)); !errors.Is(err, readErr) {
			t.Fatalf("ClampTurnSettings when the %s cannot be read = %v, want the read error", name, err)
		}
	}
}

func TestAuthorizeConfigModeAndExecutionChange(t *testing.T) {
	gate := executionGate(nil, nil)
	ctx := context.Background()
	for _, mode := range []domain.PermissionMode{domain.PermissionModeBypassPermissions, domain.PermissionModeDefault} {
		err := gate.AuthorizeConfigMode(ctx, "lado-1", mode)
		requireRefusal(t, err, "LADO_APPROVAL_ABOVE_CEILING")
		var apiErr *apierr.Error
		errors.As(err, &apiErr)
		want := map[string]any{"sessionId": "lado-1", "mode": string(mode), "ceiling": "auto"}
		if !reflect.DeepEqual(apiErr.Details, want) {
			t.Fatalf("details = %v, want %v", apiErr.Details, want)
		}
	}
	allowed := []struct {
		target domain.SessionID
		mode   domain.PermissionMode
	}{
		{"lado-1", domain.PermissionModeAuto},
		{"lado-1", domain.PermissionModeAcceptEdits},
		{"plain-1", domain.PermissionModeBypassPermissions},
		{"lado-orchestrator-1", domain.PermissionModeDefault},
		{"missing-1", domain.PermissionModeBypassPermissions},
	}
	for _, tc := range allowed {
		if err := gate.AuthorizeConfigMode(ctx, tc.target, tc.mode); err != nil {
			t.Fatalf("AuthorizeConfigMode(%s, %s) = %v, want nil", tc.target, tc.mode, err)
		}
	}

	err := gate.AuthorizeExecutionChange(ctx, "lado-1")
	requireRefusal(t, err, "LADO_EXECUTION_FIXED")
	var apiErr *apierr.Error
	errors.As(err, &apiErr)
	if want := map[string]any{"sessionId": "lado-1"}; !reflect.DeepEqual(apiErr.Details, want) {
		t.Fatalf("details = %v, want %v", apiErr.Details, want)
	}
	for _, target := range []domain.SessionID{"plain-1", "lado-orchestrator-1", "missing-1"} {
		if err := gate.AuthorizeExecutionChange(ctx, target); err != nil {
			t.Fatalf("AuthorizeExecutionChange(%s) = %v, want nil", target, err)
		}
	}
	// An execution whose snapshot has no ceiling is refused by both.
	if err := gate.AuthorizeConfigMode(ctx, "lado-2", domain.PermissionModeAcceptEdits); err == nil {
		t.Fatal("AuthorizeConfigMode for an execution without a ceiling = nil, want an error")
	}
	if err := gate.AuthorizeExecutionChange(ctx, "lado-2"); err == nil {
		t.Fatal("AuthorizeExecutionChange for an execution without a ceiling = nil, want an error")
	}

	readErr := errors.New("database is locked")
	for name, unreadable := range map[string]*ladogate.Gate{
		"session": executionGate(readErr, nil),
		"policy":  executionGate(nil, readErr),
	} {
		if err := unreadable.AuthorizeConfigMode(ctx, "lado-1", domain.PermissionModeAcceptEdits); !errors.Is(err, readErr) {
			t.Fatalf("AuthorizeConfigMode when the %s cannot be read = %v, want the read error", name, err)
		}
		if err := unreadable.AuthorizeExecutionChange(ctx, "lado-1"); !errors.Is(err, readErr) {
			t.Fatalf("AuthorizeExecutionChange when the %s cannot be read = %v, want the read error", name, err)
		}
	}
}

// B3: a LADO client request id is the dispatcher's in every project and
// outside them, because the dispatcher takes the session that holds one for
// its queue entry's worker. Without the dispatcher's grant it is refused even
// where LADO mode is off.
func TestLadoIDsNeedAGrantEverywhere(t *testing.T) {
	now := time.Now()
	grants := fakeGrants{leases: map[domain.LadoQueueEntryID]fakeLease{
		"entry-1": {attempt: 1, project: "plain", hash: "hash-1", leaseUntil: now.Add(time.Hour)},
	}}
	gate := ladogate.New(ladoProject(), fakeSessions{}, grants, fakePolicies{}, fakeExecutions{})
	refused := []struct {
		name string
		cfg  ports.SpawnConfig
	}{
		{"standalone", ports.SpawnConfig{Kind: domain.KindWorker, ClientRequestID: "lado:entry-9/1", ClientRequestHash: "hash-1"}},
		{"a project without LADO mode", ports.SpawnConfig{ProjectID: "plain", Kind: domain.KindWorker, ClientRequestID: "lado:entry-9/1", ClientRequestHash: "hash-1"}},
		{"an orchestrator", ports.SpawnConfig{ProjectID: "plain", Kind: domain.KindOrchestrator, ClientRequestID: "lado:entry-9/1"}},
		{"another hash", ports.SpawnConfig{ProjectID: "plain", Kind: domain.KindWorker, ClientRequestID: "lado:entry-1/1", ClientRequestHash: "hash-2"}},
		{"another project", ports.SpawnConfig{ProjectID: "other", Kind: domain.KindWorker, ClientRequestID: "lado:entry-1/1", ClientRequestHash: "hash-1"}},
		{"standalone with the grant's hash", ports.SpawnConfig{Kind: domain.KindWorker, ClientRequestID: "lado:entry-1/1", ClientRequestHash: "hash-1"}},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			requireRefusal(t, gate.AuthorizeSpawn(context.Background(), tc.cfg), "LADO_TASK_REQUIRED")
		})
	}
	// The dispatcher's spawn passes, and other client request ids outside
	// LADO projects stay AO's.
	for _, cfg := range []ports.SpawnConfig{
		{ProjectID: "plain", Kind: domain.KindWorker, ClientRequestID: "lado:entry-1/1", ClientRequestHash: "hash-1"},
		{ProjectID: "plain", Kind: domain.KindWorker, ClientRequestID: "task-1", ClientRequestHash: "hash-1"},
		{Kind: domain.KindWorker, ClientRequestID: "lado-1"},
	} {
		if err := gate.AuthorizeSpawn(context.Background(), cfg); err != nil {
			t.Fatalf("AuthorizeSpawn %+v = %v, want nil", cfg, err)
		}
	}
}

// B7: a person cannot restore or resume an execution LADO stopped for good;
// other executions and other sessions restore as AO restores them.
func TestAuthorizeRestoreRefusesStoppedRuns(t *testing.T) {
	executions := fakeExecutions{
		executions: map[domain.SessionID]domain.LadoExecution{
			"lado-1": {SessionID: "lado-1", TaskID: "task-1", TimedOutAt: time.Now().Add(-time.Minute)},
			"lado-2": {SessionID: "lado-2", TaskID: "task-2"},
			"lado-3": {SessionID: "lado-3", TaskID: "task-3", SessionTerminated: true},
		},
		tasks: map[domain.LadoTaskID]domain.LadoTask{
			"task-1": {ID: "task-1"},
			"task-2": {ID: "task-2", CancelledAt: time.Now().Add(-time.Hour)},
			"task-3": {ID: "task-3"},
		},
	}
	gate := ladogate.New(ladoProject(), fakeSessions{}, fakeGrants{}, fakePolicies{}, executions)
	for id, reason := range map[domain.SessionID]string{"lado-1": "timed_out", "lado-2": "task_cancelled"} {
		err := gate.AuthorizeRestore(context.Background(), id)
		requireRefusal(t, err, "LADO_EXECUTION_FIXED")
		var apiErr *apierr.Error
		if errors.As(err, &apiErr); apiErr.Details["sessionId"] != string(id) || apiErr.Details["reason"] != reason {
			t.Fatalf("AuthorizeRestore(%s) details = %v, want the session and %s", id, apiErr.Details, reason)
		}
	}
	// An execution that ended on its own, and a session that is not one.
	for _, id := range []domain.SessionID{"lado-3", "plain-1"} {
		if err := gate.AuthorizeRestore(context.Background(), id); err != nil {
			t.Fatalf("AuthorizeRestore(%s) = %v, want nil", id, err)
		}
	}
	readErr := errors.New("database is locked")
	unreadable := ladogate.New(ladoProject(), fakeSessions{}, fakeGrants{}, fakePolicies{}, fakeExecutions{err: readErr})
	if err := unreadable.AuthorizeRestore(context.Background(), "lado-1"); !errors.Is(err, readErr) {
		t.Fatalf("AuthorizeRestore when the execution cannot be read = %v, want the read error", err)
	}
}

func TestAuthorizeSpawnGrantsOnlyThePartsRunStartInProgress(t *testing.T) {
	now := time.Now()
	grants := fakeGrants{parts: map[string]fakeLease{
		"lado:entry-1/api/1":         {project: "lado", hash: "hash-1", leaseUntil: now.Add(time.Hour)},
		"lado:entry-1/merge-api/2":   {project: "lado", hash: "hash-1", leaseUntil: now.Add(time.Hour)},
		"lado:entry-1/integration/1": {project: "lado", hash: "hash-1", leaseUntil: now.Add(time.Hour)},
		"lado:entry-2/web/1":         {project: "lado", hash: "hash-2", leaseUntil: now.Add(-time.Minute)},
	}}
	gate := ladogate.New(ladoProject(), fakeSessions{}, grants, fakePolicies{}, fakeExecutions{})
	worker := func(id, hash string) ports.SpawnConfig {
		return ports.SpawnConfig{ProjectID: "lado", Kind: domain.KindWorker, Prompt: "part", ClientRequestID: id, ClientRequestHash: hash}
	}
	for _, id := range []string{"lado:entry-1/api/1", "lado:entry-1/merge-api/2", "lado:entry-1/integration/1"} {
		if err := gate.AuthorizeSpawn(context.Background(), worker(id, "hash-1")); err != nil {
			t.Fatalf("AuthorizeSpawn(%s) = %v, want the start in progress granted", id, err)
		}
	}
	for _, cfg := range []ports.SpawnConfig{
		worker("lado:entry-1/api/2", "hash-1"),
		worker("lado:entry-1/web/1", "hash-1"),
		worker("lado:entry-2/web/1", "hash-2"),
		worker("lado:entry-1/api/1", "hash-2"),
		{Kind: domain.KindWorker, ClientRequestID: "lado:entry-9/api/1", ClientRequestHash: "hash-1"},
	} {
		requireRefusal(t, gate.AuthorizeSpawn(context.Background(), cfg), "LADO_TASK_REQUIRED")
	}
}
