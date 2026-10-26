package ladoqueue

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// The queue view lists the open entries of a project, or of every project,
// with their task's issue; a dispatched entry leaves it.
func TestQueueListsTheOpenEntries(t *testing.T) {
	sf := newStoreFixture(t, nil)
	ctx := context.Background()
	project, other := domain.ProjectID("proj-1"), domain.ProjectID("proj-2")

	for _, c := range []struct {
		name    string
		project *domain.ProjectID
		want    int
	}{
		{"every project", nil, 1},
		{"its project", &project, 1},
		{"another project", &other, 0},
	} {
		items, err := sf.svc.Queue(ctx, c.project)
		if err != nil || len(items) != c.want {
			t.Fatalf("%s: Queue = %+v, %v; want %d entries", c.name, items, err, c.want)
		}
		if c.want == 1 {
			if got := items[0]; got.Entry.ID != storeEntry || got.IssueRef != testIssueRef || got.Title != testIssueTitle {
				t.Fatalf("%s: entry %+v, want %s of %s %q", c.name, got, storeEntry, testIssueRef, testIssueTitle)
			}
		}
	}

	sf.tick(t, t0)
	if items, err := sf.svc.Queue(ctx, nil); err != nil || len(items) != 0 {
		t.Fatalf("Queue after the dispatch = %+v, %v; want none", items, err)
	}
}

// The execution view carries the run and what it runs: its task, the plan
// revision and its approval, and the rule snapshot that approval fixed.
func TestExecutionShowsWhatTheWorkerRuns(t *testing.T) {
	sf := newStoreFixture(t, nil)
	ctx := context.Background()
	sf.tick(t, t0)
	worker := sf.session(t, 1)

	view, err := sf.svc.Execution(ctx, worker.ID)
	if err != nil {
		t.Fatalf("Execution = %v", err)
	}
	ahead := 2
	if got := view.Execution; got.SessionID != worker.ID || got.Attempt != 1 || !got.StartedAt.Equal(t0) || !got.DeadlineAt.Equal(t0.Add(8*time.Hour)) ||
		got.PlanBaseSHA != "c0ffee" || got.DispatchBaseSHA != testDispatchSHA || !reflect.DeepEqual(got.BaseCommitsAhead, &ahead) {
		t.Fatalf("execution %+v, want the run of attempt 1 from %s with its base move", got, t0)
	}
	if view.Task.ID != "task-1" || view.Task.Issue.Title != testIssueTitle || view.Task.IssueRef != testIssueRef {
		t.Fatalf("task %+v, want task-1", view.Task)
	}
	// The store numbers the task's revisions, and rev-2 is its first.
	if view.Revision.ID != "rev-2" || view.Revision.Number != 1 || view.Revision.SHA256 != testPlanSHA {
		t.Fatalf("revision %+v, want rev-2 as number 1", view.Revision)
	}
	if want := testApproval(); view.Approval.ID != want.ID || !view.Approval.ApprovedAt.Equal(want.ApprovedAt) {
		t.Fatalf("approval %+v, want %+v", view.Approval, want)
	}
	snapshot := view.Snapshot
	if view.SnapshotSHA256 != testSnapshotSHA || snapshot.Agent != claudeAgent() || snapshot.PermissionCeiling != domain.PermissionModeAuto ||
		snapshot.Prompt != "## LADO rules\n\nRun the tests before you push.\n" {
		t.Fatalf("snapshot %s %+v, want the approval's", view.SnapshotSHA256, snapshot)
	}

	// A session that is not an execution has no view.
	_, err = sf.svc.Execution(ctx, "proj-1-orchestrator-1")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "LADO_EXECUTION_NOT_FOUND" || apiErr.Kind != apierr.KindNotFound {
		t.Fatalf("Execution of another session = %v, want LADO_EXECUTION_NOT_FOUND", err)
	}
}

// A settings change is stored with an audit event the API made; the current
// settings and settings out of range write nothing.
func TestSetSettingsAuditsTheChangeAsTheAPI(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	want := domain.LadoDispatchSettings{MaxConcurrent: 3, MaxRunMinutes: 120, HoldOnMemoryTight: false}

	stored, err := f.svc.SetSettings(ctx, want)
	want.UpdatedAt = t0
	if err != nil || stored != want {
		t.Fatalf("SetSettings = %+v, %v; want %+v", stored, err, want)
	}
	if len(f.store.settingsEvents) != 1 {
		t.Fatalf("audit events %+v, want one", f.store.settingsEvents)
	}
	event := f.store.settingsEvents[0]
	var payload map[string]map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("payload %s: %v", event.Payload, err)
	}
	wantPayload := map[string]map[string]any{
		"from": {"maxConcurrent": 2.0, "maxRunMinutes": 480.0, "holdOnMemoryTight": true},
		"to":   {"maxConcurrent": 3.0, "maxRunMinutes": 120.0, "holdOnMemoryTight": false},
	}
	if event.Actor != "api" || event.Action != "dispatch.settings_changed" || event.SubjectKind != "dispatch_settings" ||
		!event.At.Equal(t0) || !reflect.DeepEqual(payload, wantPayload) {
		t.Fatalf("audit event %+v with payload %v, want the API's change from the defaults", event, payload)
	}

	f.setClock(t0.Add(time.Minute))
	if again, err := f.svc.SetSettings(ctx, want); err != nil || again != want || len(f.store.settingsEvents) != 1 {
		t.Fatalf("SetSettings with the current settings = %+v, %v after %d events; want them unchanged", again, err, len(f.store.settingsEvents))
	}

	for _, bad := range []domain.LadoDispatchSettings{
		{MaxConcurrent: 0, MaxRunMinutes: 480},
		{MaxConcurrent: 9, MaxRunMinutes: 480},
		{MaxConcurrent: 2, MaxRunMinutes: 30},
		{MaxConcurrent: 2, MaxRunMinutes: -60},
	} {
		_, err := f.svc.SetSettings(ctx, bad)
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "LADO_DISPATCH_SETTINGS_INVALID" || apiErr.Kind != apierr.KindInvalid {
			t.Fatalf("SetSettings(%+v) = %v, want LADO_DISPATCH_SETTINGS_INVALID", bad, err)
		}
	}
	if current, err := f.svc.Settings(ctx); err != nil || current != want || len(f.store.settingsEvents) != 1 {
		t.Fatalf("settings after refused changes = %+v, %v; want %+v", current, err, want)
	}
}
