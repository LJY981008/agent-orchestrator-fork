package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// The store methods of the ladodbchange.Store contract in the #97 plan, with
// their signatures as the plan fixes them.
var _ interface {
	LadoDBChangeSettings(context.Context, domain.ProjectID) ([]domain.LadoDBChangeSettings, error)
	LadoDBChangeSetting(context.Context, domain.ProjectID, string) (domain.LadoDBChangeSettings, bool, error)
	SetLadoDBChangeSettings(context.Context, domain.LadoDBChangeSettings, string, []domain.LadoDBChangeApplication, *domain.LadoPresenceProof, []domain.LadoAuditEvent) error
	LadoDBChangeAppliedPaths(context.Context, domain.ProjectID, string, []string) (map[string]domain.LadoDBChangeApplication, error)
	ListLadoDBChangeApplications(context.Context, domain.ProjectID, string, []string) ([]domain.LadoDBChangeApplication, error)
	StartLadoDBChangeApplication(context.Context, domain.LadoDBChangeApplication) error
	FinishLadoDBChangeApplication(context.Context, string, domain.LadoDBChangeResult, *int, string, string, time.Time, domain.LadoAuditEvent) error
	InterruptLadoDBChangeApplications(context.Context, time.Time) (int, error)
	EnsureLadoDBChangeRun(context.Context, domain.LadoDBChangeRun) (domain.LadoDBChangeRun, bool, error)
	LadoDBChangeRun(context.Context, string) (domain.LadoDBChangeRun, bool, error)
	TransitionLadoDBChangeRun(context.Context, string, []domain.LadoDBChangeRunState, domain.LadoDBChangeRunState, int, string, time.Time, *domain.LadoPresenceProof, *domain.LadoAuditEvent) (bool, error)
	ListLadoDBChangeRunsByState(context.Context, []domain.LadoDBChangeRunState, int) ([]domain.LadoDBChangeRun, error)
	ListLadoDBChangeRunsForExecution(context.Context, domain.SessionID) ([]domain.LadoDBChangeRun, error)
	ListLadoDBChangeRunsForProject(context.Context, domain.ProjectID) ([]domain.LadoDBChangeRun, error)
	HoldLadoIntegrationStatus(context.Context, string, time.Time) error
	ReleaseLadoIntegrationStatus(context.Context, string) error
	LadoIntegrationStatusHeld(context.Context, string) (bool, error)
} = (*sqlite.Store)(nil)

const ladoDBChangePR = "https://github.com/acme/demo/pull/7"

func ladoDBChangeEvent(action string, at time.Time) domain.LadoAuditEvent {
	return domain.LadoAuditEvent{
		At: at, Actor: "human", Action: action, SubjectKind: "project", SubjectID: "mer",
		Payload: json.RawMessage(`{"repo":"back"}`),
	}
}

func ladoDBChangeProof(nonce string, at time.Time) *domain.LadoPresenceProof {
	return &domain.LadoPresenceProof{
		Nonce: nonce, Action: domain.LadoPresenceDBChanges, SubjectID: domain.LadoDBChangeSettingsSubject("mer", "back"),
		ExpiresAt: at.Add(time.Minute), UsedAt: at,
	}
}

func ladoDBChangeBaseline(path string, blob byte, at time.Time) domain.LadoDBChangeApplication {
	return domain.LadoDBChangeApplication{
		Path: path, BlobSHA: ladoHead(blob), Phase: domain.LadoDBChangePreMerge,
		Result: domain.LadoDBChangeResultBaseline, Actor: "human", StartedAt: at,
	}
}

// Settings are saved against the hash of what was stored, with their
// baseline rows, proof and audit events in one transaction; a stale base or
// a used proof keeps nothing.
func TestLadoDBChangeSettingsInTheStore(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt

	if got, err := s.LadoDBChangeSettings(ctx, "mer"); err != nil || len(got) != 0 {
		t.Fatalf("LadoDBChangeSettings before any = %+v, %v; want none", got, err)
	}
	if _, ok, err := s.LadoDBChangeSetting(ctx, "mer", "back"); ok || err != nil {
		t.Fatalf("LadoDBChangeSetting before any = (%v, %v), want none", ok, err)
	}

	back := domain.LadoDBChangeSettings{
		ProjectID: "mer", Repo: "back", Enabled: true, Paths: []string{"db/*.sql"}, Command: `psql "$DSN" -f {file}`,
		TimeoutMinutes: 15, SecretNames: []string{"DSN"}, BaselineCommit: ladoHead('c'), UpdatedAt: at,
	}
	baseline := []domain.LadoDBChangeApplication{ladoDBChangeBaseline("db/001.sql", '1', at), ladoDBChangeBaseline("db/002.sql", '2', at)}
	audit := []domain.LadoAuditEvent{ladoDBChangeEvent(domain.LadoAuditDBChangesChanged, at), ladoDBChangeEvent(domain.LadoAuditDBChangeBaseline, at)}

	// A base for settings that do not exist is refused and keeps nothing.
	before := ladoAuditProbe(t, s)
	if err := s.SetLadoDBChangeSettings(ctx, back, strings.Repeat("0", 64), baseline, ladoDBChangeProof("nonce-stale", at), audit); !errors.Is(err, domain.ErrLadoDBChangeSettingsChanged) {
		t.Fatalf("stale base = %v, want ErrLadoDBChangeSettingsChanged", err)
	}
	if used, err := s.LadoPresenceProofUsed(ctx, "nonce-stale"); used || err != nil {
		t.Fatalf("proof of a refused change used = (%v, %v), want unused", used, err)
	}
	if after := ladoAuditProbe(t, s); after != before+1 {
		t.Fatalf("a refused change wrote %d audit events", after-before-1)
	}

	before = ladoAuditProbe(t, s)
	if err := s.SetLadoDBChangeSettings(ctx, back, "", baseline, ladoDBChangeProof("nonce-1", at), audit); err != nil {
		t.Fatalf("SetLadoDBChangeSettings: %v", err)
	}
	if after := ladoAuditProbe(t, s); after != before+3 {
		t.Fatalf("the change wrote %d audit events, want 2", after-before-1)
	}
	if used, err := s.LadoPresenceProofUsed(ctx, "nonce-1"); !used || err != nil {
		t.Fatalf("proof used = (%v, %v), want used", used, err)
	}
	got, ok, err := s.LadoDBChangeSetting(ctx, "mer", "back")
	want := back
	want.EnabledAt, want.UpdatedAt = at.UTC(), at.UTC()
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("LadoDBChangeSetting = %+v (%v, %v)\nwant %+v", got, ok, err, want)
	}
	applied, err := s.LadoDBChangeAppliedPaths(ctx, "mer", "back", nil)
	if err != nil || len(applied) != 2 || applied["db/001.sql"].BlobSHA != ladoHead('1') ||
		applied["db/002.sql"].Result != domain.LadoDBChangeResultBaseline || applied["db/002.sql"].ProjectID != "mer" ||
		applied["db/002.sql"].Repo != "back" || applied["db/002.sql"].ID == "" {
		t.Fatalf("baseline = %+v, %v; want both files recorded for mer/back", applied, err)
	}

	// The proof is spent: using it again is refused and keeps nothing.
	again := back
	again.Command = "psql -f {file}"
	if err := s.SetLadoDBChangeSettings(ctx, again, got.SHA256(), nil, ladoDBChangeProof("nonce-1", at), nil); !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("replayed proof = %v, want ErrLadoPresenceReplayed", err)
	}
	// The base must be the stored settings' hash.
	if err := s.SetLadoDBChangeSettings(ctx, again, "", nil, nil, nil); !errors.Is(err, domain.ErrLadoDBChangeSettingsChanged) {
		t.Fatalf("empty base of stored settings = %v, want ErrLadoDBChangeSettingsChanged", err)
	}

	// Turning them off later keeps when they were turned on; the time comes
	// from the audit event when the settings have none.
	off := back
	off.Enabled, off.UpdatedAt, off.SecretNames = false, time.Time{}, nil
	later := at.Add(time.Hour)
	if err := s.SetLadoDBChangeSettings(ctx, off, strings.ToUpper(got.SHA256()), nil, nil,
		[]domain.LadoAuditEvent{ladoDBChangeEvent(domain.LadoAuditDBChangesChanged, later)}); err != nil {
		t.Fatalf("turning the settings off: %v", err)
	}
	got, _, err = s.LadoDBChangeSetting(ctx, "mer", "back")
	if err != nil || got.Enabled || !got.EnabledAt.Equal(at) || !got.UpdatedAt.Equal(later) || got.SecretNames == nil || len(got.SecretNames) != 0 {
		t.Fatalf("settings turned off = %+v, %v; want off, enabled at %v, updated at %v, no secrets", got, err, at, later)
	}

	// The root repository sorts first.
	root := domain.LadoDBChangeSettings{ProjectID: "mer", Paths: []string{"sql/*.sql"}, UpdatedAt: later}
	if err := s.SetLadoDBChangeSettings(ctx, root, "", nil, nil, nil); err != nil {
		t.Fatalf("root settings: %v", err)
	}
	list, err := s.LadoDBChangeSettings(ctx, "mer")
	if err != nil || len(list) != 2 || list[0].Repo != "" || list[1].Repo != "back" || !list[0].EnabledAt.IsZero() {
		t.Fatalf("LadoDBChangeSettings = %+v, %v; want root then back, root never enabled", list, err)
	}
	if other, err := s.LadoDBChangeSettings(ctx, "other"); err != nil || len(other) != 0 {
		t.Fatalf("another project's settings = %+v, %v; want none", other, err)
	}
}

// Applications start running, finish once with their audit event, and the
// latest applied or baseline row of each path makes it applied; a restart
// interrupts the ones left running.
func TestLadoDBChangeApplicationsInTheStore(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	if err := s.SetLadoDBChangeSettings(ctx, domain.LadoDBChangeSettings{ProjectID: "mer", Repo: "back", UpdatedAt: at}, "",
		[]domain.LadoDBChangeApplication{ladoDBChangeBaseline("db/001.sql", '1', at)}, nil, nil); err != nil {
		t.Fatalf("SetLadoDBChangeSettings: %v", err)
	}

	start := func(id, path string, blob byte, started time.Time) domain.LadoDBChangeApplication {
		t.Helper()
		app := domain.LadoDBChangeApplication{
			ID: id, ProjectID: "mer", Repo: "back", Path: path, BlobSHA: ladoHead(blob), Phase: domain.LadoDBChangePreMerge,
			RunID: "run-1", ExecutionSessionID: "session-1", TaskID: "lado-task-1", PRURL: ladoDBChangePR, HeadSHA: ladoHead('a'),
			Actor: "lado", StartedAt: started,
		}
		if err := s.StartLadoDBChangeApplication(ctx, app); err != nil {
			t.Fatalf("StartLadoDBChangeApplication %s: %v", id, err)
		}
		app.Result, app.StartedAt = domain.LadoDBChangeResultRunning, started.UTC()
		return app
	}
	if err := s.StartLadoDBChangeApplication(ctx, domain.LadoDBChangeApplication{ProjectID: "mer", Path: "x"}); err == nil {
		t.Fatal("an application without an id or time was accepted")
	}

	first := start("app-1", "db/001.sql", '9', at.Add(time.Minute))
	second := start("app-2", "db/002.sql", '2', at.Add(2*time.Minute))
	third := start("app-3", "db/003.sql", '3', at.Add(3*time.Minute))

	// Running rows do not make a file applied: db/001.sql keeps its baseline.
	applied, err := s.LadoDBChangeAppliedPaths(ctx, "mer", "back", []string{"db/001.sql", "db/002.sql"})
	if err != nil || len(applied) != 1 || applied["db/001.sql"].Result != domain.LadoDBChangeResultBaseline {
		t.Fatalf("applied while running = %+v, %v; want the baseline alone", applied, err)
	}

	zero, finished := 0, at.Add(4*time.Minute)
	output := strings.Repeat("d", 64)
	before := ladoAuditProbe(t, s)
	if err := s.FinishLadoDBChangeApplication(ctx, first.ID, domain.LadoDBChangeResultApplied, &zero, output, "ok", finished,
		ladoDBChangeEvent(domain.LadoAuditDBChangeApplied, finished)); err != nil {
		t.Fatalf("FinishLadoDBChangeApplication: %v", err)
	}
	if after := ladoAuditProbe(t, s); after != before+2 {
		t.Fatalf("finishing wrote %d audit events, want 1", after-before-1)
	}
	two := 2
	if err := s.FinishLadoDBChangeApplication(ctx, second.ID, domain.LadoDBChangeResultFailed, &two, "", "ERROR: relation exists", finished,
		domain.LadoAuditEvent{}); err != nil {
		t.Fatalf("FinishLadoDBChangeApplication failed: %v", err)
	}
	before = ladoAuditProbe(t, s)
	if err := s.FinishLadoDBChangeApplication(ctx, first.ID, domain.LadoDBChangeResultFailed, nil, "", "again", finished,
		ladoDBChangeEvent(domain.LadoAuditDBChangeFailed, finished)); err == nil {
		t.Fatal("finishing an application twice was accepted")
	}
	if after := ladoAuditProbe(t, s); after != before+1 {
		t.Fatal("a refused finish wrote an audit event")
	}
	if err := s.FinishLadoDBChangeApplication(ctx, "app-missing", domain.LadoDBChangeResultApplied, nil, "", "", finished,
		domain.LadoAuditEvent{}); !errors.Is(err, domain.ErrLadoDBChangeNotFound) {
		t.Fatalf("finishing an unknown application = %v, want ErrLadoDBChangeNotFound", err)
	}
	if err := s.FinishLadoDBChangeApplication(ctx, third.ID, domain.LadoDBChangeResultRunning, nil, "", "", finished,
		domain.LadoAuditEvent{}); err == nil {
		t.Fatal("finishing an application as running was accepted")
	}

	first.Result, first.ExitCode, first.OutputSHA256, first.Message = domain.LadoDBChangeResultApplied, &zero, output, "ok"
	finishedUTC := finished.UTC()
	first.FinishedAt = &finishedUTC
	second.Result, second.ExitCode, second.Message, second.FinishedAt = domain.LadoDBChangeResultFailed, &two, "ERROR: relation exists", &finishedUTC

	// The applied row is newer than the baseline, so it is the one kept.
	applied, err = s.LadoDBChangeAppliedPaths(ctx, "mer", "back", []string{"db/001.sql", "db/002.sql", "db/003.sql"})
	if err != nil || len(applied) != 1 || !reflect.DeepEqual(applied["db/001.sql"], first) {
		t.Fatalf("applied = %+v, %v\nwant db/001.sql = %+v", applied, err, first)
	}

	list, err := s.ListLadoDBChangeApplications(ctx, "mer", "back", []string{"db/002.sql", "db/003.sql"})
	if err != nil || !reflect.DeepEqual(list, []domain.LadoDBChangeApplication{second, third}) {
		t.Fatalf("ListLadoDBChangeApplications(paths) = %+v, %v\nwant %+v", list, err, []domain.LadoDBChangeApplication{second, third})
	}
	all, err := s.ListLadoDBChangeApplications(ctx, "mer", "back", nil)
	if err != nil || len(all) != 4 || all[0].Result != domain.LadoDBChangeResultBaseline || all[3].ID != third.ID {
		t.Fatalf("ListLadoDBChangeApplications(all) = %+v, %v; want the baseline first and four rows", all, err)
	}
	if other, err := s.ListLadoDBChangeApplications(ctx, "mer", "", nil); err != nil || len(other) != 0 {
		t.Fatalf("the root repository's applications = %+v, %v; want none", other, err)
	}

	restarted := at.Add(time.Hour)
	if n, err := s.InterruptLadoDBChangeApplications(ctx, restarted); n != 1 || err != nil {
		t.Fatalf("InterruptLadoDBChangeApplications = %d, %v; want the one left running", n, err)
	}
	if n, err := s.InterruptLadoDBChangeApplications(ctx, restarted); n != 0 || err != nil {
		t.Fatalf("interrupting again = %d, %v; want none", n, err)
	}
	list, err = s.ListLadoDBChangeApplications(ctx, "mer", "back", []string{"db/003.sql"})
	if err != nil || len(list) != 1 || list[0].Result != domain.LadoDBChangeResultInterrupted || list[0].FinishedAt == nil ||
		!list[0].FinishedAt.Equal(restarted) || list[0].ExitCode != nil {
		t.Fatalf("interrupted application = %+v, %v", list, err)
	}
	if err := s.FinishLadoDBChangeApplication(ctx, third.ID, domain.LadoDBChangeResultApplied, &zero, "", "", restarted,
		domain.LadoAuditEvent{}); err == nil {
		t.Fatal("finishing an interrupted application was accepted")
	}
}

// A run is created once per PR, head and kind, moves only from the states
// given, with its proof and audit event, and lists by state, execution and
// project.
func TestLadoDBChangeRunsInTheStore(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	run := domain.LadoDBChangeRun{
		ProjectID: "mer", Repo: "back", ExecutionSessionID: "session-1", TaskID: "lado-task-1", PRURL: ladoDBChangePR,
		HeadSHA: ladoHead('a'), Kind: domain.LadoDBChangeRunPreMerge, Trigger: domain.LadoDBChangeTriggerApproval,
		State: domain.LadoDBChangeRunQueued, ApprovalID: "approval-1", CreatedAt: at,
	}
	if _, _, err := s.EnsureLadoDBChangeRun(ctx, domain.LadoDBChangeRun{ProjectID: "mer"}); err == nil {
		t.Fatal("a run without a PR, head or time was accepted")
	}
	created, ok, err := s.EnsureLadoDBChangeRun(ctx, run)
	if err != nil || !ok || created.ID == "" {
		t.Fatalf("EnsureLadoDBChangeRun = %+v (%v, %v), want a new run with an id", created, ok, err)
	}
	want := run
	want.ID, want.CreatedAt, want.UpdatedAt = created.ID, at.UTC(), at.UTC()
	if !reflect.DeepEqual(created, want) {
		t.Fatalf("created run = %+v\nwant %+v", created, want)
	}
	// The same PR, head and kind returns the stored run as it is.
	dup := run
	dup.ID, dup.Trigger, dup.State, dup.CreatedAt = "run-dup", domain.LadoDBChangeTriggerMergedUnapplied, domain.LadoDBChangeRunAwaitingHuman, at.Add(time.Hour)
	if again, ok, err := s.EnsureLadoDBChangeRun(ctx, dup); err != nil || ok || !reflect.DeepEqual(again, created) {
		t.Fatalf("ensuring again = %+v (%v, %v), want the stored run", again, ok, err)
	}
	if got, ok, err := s.LadoDBChangeRun(ctx, created.ID); err != nil || !ok || !reflect.DeepEqual(got, created) {
		t.Fatalf("LadoDBChangeRun = %+v (%v, %v)", got, ok, err)
	}
	if _, ok, err := s.LadoDBChangeRun(ctx, "run-missing"); ok || err != nil {
		t.Fatalf("an unknown run = (%v, %v), want none", ok, err)
	}
	after := run
	after.ID, after.Kind, after.Trigger, after.State = "run-after", domain.LadoDBChangeRunAfterDeploy, domain.LadoDBChangeTriggerAfterMerge, domain.LadoDBChangeRunAwaitingHuman
	after.CreatedAt = at.Add(time.Minute)
	afterRun, ok, err := s.EnsureLadoDBChangeRun(ctx, after)
	if err != nil || !ok || afterRun.ID != "run-after" {
		t.Fatalf("the head's after-deploy run = %+v (%v, %v)", afterRun, ok, err)
	}

	// CAS: the run moves only from a state given.
	moved := at.Add(2 * time.Minute)
	if ok, err := s.TransitionLadoDBChangeRun(ctx, created.ID, []domain.LadoDBChangeRunState{domain.LadoDBChangeRunFailed},
		domain.LadoDBChangeRunApplying, 1, "", moved, nil, nil); ok || err != nil {
		t.Fatalf("moving from a state the run is not in = (%v, %v), want false", ok, err)
	}
	if ok, err := s.TransitionLadoDBChangeRun(ctx, created.ID, []domain.LadoDBChangeRunState{domain.LadoDBChangeRunQueued},
		domain.LadoDBChangeRunApplying, 1, "", moved, nil, nil); !ok || err != nil {
		t.Fatalf("queued to applying = (%v, %v)", ok, err)
	}
	failedAt := moved.Add(time.Minute)
	if ok, err := s.TransitionLadoDBChangeRun(ctx, created.ID, []domain.LadoDBChangeRunState{domain.LadoDBChangeRunApplying},
		domain.LadoDBChangeRunFailed, 1, "db/002.sql: exit 3", failedAt, nil, nil); !ok || err != nil {
		t.Fatalf("applying to failed = (%v, %v)", ok, err)
	}
	got, _, _ := s.LadoDBChangeRun(ctx, created.ID)
	if got.State != domain.LadoDBChangeRunFailed || got.Attempts != 1 || got.Error != "db/002.sql: exit 3" || !got.UpdatedAt.Equal(failedAt) || !got.CreatedAt.Equal(at) {
		t.Fatalf("failed run = %+v", got)
	}

	// A retry spends its proof and writes its audit event; a refused one
	// spends nothing.
	retry := failedAt.Add(time.Minute)
	proof := &domain.LadoPresenceProof{Nonce: "nonce-retry", Action: domain.LadoPresenceDBChangeApply, SubjectID: "x", ExpiresAt: retry.Add(time.Minute), UsedAt: retry}
	event := ladoDBChangeEvent("lado.db_change_retry", retry)
	before := ladoAuditProbe(t, s)
	if ok, err := s.TransitionLadoDBChangeRun(ctx, created.ID, []domain.LadoDBChangeRunState{domain.LadoDBChangeRunAwaitingHuman},
		domain.LadoDBChangeRunQueued, 1, "", retry, proof, &event); ok || err != nil {
		t.Fatalf("retrying from the wrong state = (%v, %v), want false", ok, err)
	}
	if used, _ := s.LadoPresenceProofUsed(ctx, "nonce-retry"); used {
		t.Fatal("a refused move spent its proof")
	}
	if ok, err := s.TransitionLadoDBChangeRun(ctx, created.ID, []domain.LadoDBChangeRunState{domain.LadoDBChangeRunFailed, domain.LadoDBChangeRunMergeFailed},
		domain.LadoDBChangeRunQueued, 1, "", retry, proof, &event); !ok || err != nil {
		t.Fatalf("retrying = (%v, %v)", ok, err)
	}
	if got := ladoAuditProbe(t, s); got != before+2 {
		t.Fatalf("the moves wrote %d audit events, want 1", got-before-1)
	}
	if used, _ := s.LadoPresenceProofUsed(ctx, "nonce-retry"); !used {
		t.Fatal("the retry did not spend its proof")
	}
	if ok, err := s.TransitionLadoDBChangeRun(ctx, created.ID, nil, domain.LadoDBChangeRunAwaitingHuman, 1, "", retry, proof, nil); ok || !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("a replayed proof = (%v, %v), want ErrLadoPresenceReplayed", ok, err)
	}
	if ok, err := s.TransitionLadoDBChangeRun(ctx, "run-missing", nil, domain.LadoDBChangeRunDone, 0, "", retry, nil, nil); ok || err != nil {
		t.Fatalf("moving an unknown run = (%v, %v), want false", ok, err)
	}

	// Lists.
	byState, err := s.ListLadoDBChangeRunsByState(ctx, []domain.LadoDBChangeRunState{domain.LadoDBChangeRunQueued, domain.LadoDBChangeRunAwaitingHuman}, 0)
	if err != nil || len(byState) != 2 || byState[0].ID != "run-after" || byState[1].ID != created.ID {
		t.Fatalf("ListLadoDBChangeRunsByState = %+v, %v; want the least recently moved first", byState, err)
	}
	if limited, err := s.ListLadoDBChangeRunsByState(ctx, []domain.LadoDBChangeRunState{domain.LadoDBChangeRunQueued, domain.LadoDBChangeRunAwaitingHuman}, 1); err != nil ||
		len(limited) != 1 || limited[0].ID != "run-after" {
		t.Fatalf("ListLadoDBChangeRunsByState(1) = %+v, %v", limited, err)
	}
	if none, err := s.ListLadoDBChangeRunsByState(ctx, nil, 10); err != nil || len(none) != 0 {
		t.Fatalf("ListLadoDBChangeRunsByState(no states) = %+v, %v", none, err)
	}
	if ok, err := s.TransitionLadoDBChangeRun(ctx, "run-after", nil, domain.LadoDBChangeRunDone, 1, "", retry, nil, nil); !ok || err != nil {
		t.Fatalf("after-deploy run done = (%v, %v)", ok, err)
	}
	forExec, err := s.ListLadoDBChangeRunsForExecution(ctx, "session-1")
	if err != nil || !slices.Equal(ladoDBChangeRunIDs(forExec), []string{created.ID, "run-after"}) {
		t.Fatalf("ListLadoDBChangeRunsForExecution = %+v, %v", forExec, err)
	}
	if other, err := s.ListLadoDBChangeRunsForExecution(ctx, "session-2"); err != nil || len(other) != 0 {
		t.Fatalf("another execution's runs = %+v, %v", other, err)
	}
	forProject, err := s.ListLadoDBChangeRunsForProject(ctx, "mer")
	if err != nil || !slices.Equal(ladoDBChangeRunIDs(forProject), []string{created.ID}) {
		t.Fatalf("ListLadoDBChangeRunsForProject = %+v, %v; want the run that is not done", forProject, err)
	}
}

func ladoDBChangeRunIDs(runs []domain.LadoDBChangeRun) []string {
	ids := make([]string, len(runs))
	for i, run := range runs {
		ids[i] = run.ID
	}
	return ids
}

// A held approval leaves the status retry list until it is released; the
// approval itself does not change.
func TestLadoIntegrationStatusHoldsLeaveTheRetryList(t *testing.T) {
	s := openLadoVerifyStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	exec := mustLadoExecution(t, s, "1", at, false)
	approve := func(id string, head byte, approved time.Time) domain.LadoIntegrationApproval {
		t.Helper()
		record := ladoVerification(t, exec.SessionID, ladoDBChangePR, ladoHead(head), at)
		mustUpsertLadoVerification(t, s, record)
		a := ladoIntegrationApproval(t, id, record, domain.LadoIntegrationStatusPending, approved)
		mustApproveLadoIntegration(t, s, a)
		a.ProofNonce = "nonce-" + id
		return a
	}
	first := approve("approval-1", '1', at.Add(time.Minute))
	second := approve("approval-2", '2', at.Add(2*time.Minute))
	expect := func(step string, want ...domain.LadoIntegrationApproval) {
		t.Helper()
		got, err := s.ListLadoIntegrationStatusRetries(ctx, 10)
		if err != nil || len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
			t.Fatalf("%s: ListLadoIntegrationStatusRetries = %+v (%v), want %+v", step, got, err, want)
		}
	}
	expect("start", first, second)

	if held, err := s.LadoIntegrationStatusHeld(ctx, first.ID); held || err != nil {
		t.Fatalf("held before any hold = (%v, %v)", held, err)
	}
	if err := s.HoldLadoIntegrationStatus(ctx, first.ID, at.Add(3*time.Minute)); err != nil {
		t.Fatalf("HoldLadoIntegrationStatus: %v", err)
	}
	if err := s.HoldLadoIntegrationStatus(ctx, first.ID, at.Add(4*time.Minute)); err != nil {
		t.Fatalf("holding again: %v", err)
	}
	if held, err := s.LadoIntegrationStatusHeld(ctx, first.ID); !held || err != nil {
		t.Fatalf("held = (%v, %v), want held", held, err)
	}
	expect("held", second)
	if got, ok := ladoApprovalOf(t, s, exec.SessionID, first.PRURL, first.HeadSHA); !ok || !reflect.DeepEqual(got, first) {
		t.Fatalf("held approval = %+v, want it unchanged %+v", got, first)
	}
	if err := s.HoldLadoIntegrationStatus(ctx, "approval-missing", at); err == nil {
		t.Fatal("holding an unknown approval was accepted")
	}

	if err := s.ReleaseLadoIntegrationStatus(ctx, first.ID); err != nil {
		t.Fatalf("ReleaseLadoIntegrationStatus: %v", err)
	}
	if err := s.ReleaseLadoIntegrationStatus(ctx, first.ID); err != nil {
		t.Fatalf("releasing again: %v", err)
	}
	if held, err := s.LadoIntegrationStatusHeld(ctx, first.ID); held || err != nil {
		t.Fatalf("held after the release = (%v, %v)", held, err)
	}
	expect("released", first, second)
}
