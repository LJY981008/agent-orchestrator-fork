package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoproject"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoqueue"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

const ladoWorkerRules = "## LADO rules\n\nRun the tests before you push.\n"

// ladoSeed is what approving the plan of task lado-task-<n> left.
type ladoSeed struct {
	task     domain.LadoTaskID
	entry    domain.LadoQueueEntryID
	plan     string
	snapshot string
}

// seedLadoQueued seeds task lado-task-<n> of project, in LADO mode, with a
// pinned plan and its approval at at, which queued entry lado-queue-<n> with
// the approval's rule snapshot, as approving does.
func seedLadoQueued(t *testing.T, store *sqlite.Store, project domain.ProjectID, n int, at time.Time) ladoSeed {
	t.Helper()
	ctx := context.Background()
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	seed := ladoSeed{
		task:     domain.LadoTaskID(fmt.Sprintf("lado-task-%d", n)),
		entry:    domain.LadoQueueEntryID(fmt.Sprintf("lado-queue-%d", n)),
		plan:     fmt.Sprintf("a%063d", n),
		snapshot: fmt.Sprintf("5%063d", n),
	}
	must("register the project", store.UpsertProject(ctx, domain.ProjectRecord{ID: string(project), Path: "/tmp/" + string(project), RegisteredAt: at}))
	_, err := store.SetLadoMode(ctx, project, true, at)
	must("turn LADO mode on", err)
	issue := strings.Repeat("1", 64)
	_, err = store.CreateLadoTask(ctx, domain.LadoTask{
		ID: seed.task, ProjectID: project, Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"},
		IssueRef: domain.IssueID(fmt.Sprintf("github:acme/demo#%d", n)), Agent: "claude-code", CreatedAt: at, UpdatedAt: at,
		Issue: domain.LadoObservedIssue{
			Title: fmt.Sprintf("Task %d", n), State: domain.IssueOpen, URL: fmt.Sprintf("https://github.com/acme/demo/issues/%d", n),
			SnapshotSHA256: issue, ObservedAt: at,
		},
	})
	must("create the task", err)
	request := domain.LadoPlanRequestID(fmt.Sprintf("req-%d", n))
	orchestrator := domain.SessionID(string(project) + "-orchestrator-1")
	_, err = store.CreateLadoPlanRequest(ctx, domain.LadoPlanRequest{ID: request, TaskID: seed.task, SessionID: orchestrator, IssueSnapshotSHA256: issue, RequestedAt: at})
	must("request the plan", err)
	rev, _, err := store.PinLadoPlanRevision(ctx, domain.LadoPlanRevision{
		ID: domain.LadoPlanRevisionID(fmt.Sprintf("lado-plan-%d", n)), TaskID: seed.task, RequestID: request, SessionID: orchestrator,
		Path: domain.LadoPlanPath(seed.task), ArtifactVersion: 1, Source: "agent", SHA256: seed.plan,
		BaseRef: "main", BaseSHA: strings.Repeat("c", 40), IssueSnapshotSHA256: issue, CreatedAt: at,
	})
	must("pin the plan", err)
	content, err := json.Marshal(ladopolicy.SnapshotContent{
		Compiler: ladopolicy.Compiler, ProjectID: project, TaskID: seed.task,
		Agent:             ladopolicy.SnapshotAgent{Harness: domain.HarnessClaudeCode, Model: "claude-sonnet-5", PermissionMode: domain.PermissionModeAcceptEdits},
		PermissionCeiling: domain.PermissionModeAuto,
		Rules: []ladopolicy.SnapshotRule{
			{RuleID: domain.LadoRulePermissionCeiling, Name: "Permission ceiling", Version: 1},
			{RuleID: "team.tests", Name: "Run the tests", Version: 3},
		},
		Setup: []ladopolicy.SnapshotSetupItem{
			{ItemID: "lado-setup-item-1", Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindInstructions, Name: "CLAUDE.md", Version: 2},
		},
		Prompt: ladoWorkerRules,
	})
	must("encode the rule snapshot", err)
	approval := domain.LadoPlanApprovalID(fmt.Sprintf("lado-approval-%d", n))
	_, _, err = store.ApproveLadoPlan(ctx,
		domain.LadoPlanApproval{ID: approval, RevisionID: rev.ID, TaskID: seed.task, SHA256: seed.plan, ApprovedAt: at},
		domain.LadoPresenceProof{Nonce: "nonce-" + string(approval), Action: domain.LadoPresencePlanApprove, SubjectID: string(rev.ID), ExpiresAt: at.Add(time.Minute), UsedAt: at},
		domain.LadoAuditEvent{At: at, Actor: "human", Action: "plan.approved", SubjectKind: "plan_revision", SubjectID: string(rev.ID), Payload: []byte("{}")},
		&domain.LadoPolicySnapshot{ID: domain.LadoPolicySnapshotID(fmt.Sprintf("lado-snapshot-%d", n)), SHA256: seed.snapshot, Content: content},
		&domain.LadoQueueEntry{ID: seed.entry})
	must("approve the plan", err)
	return seed
}

// claimLado claims the oldest claimable entry at now for two minutes, which
// must be want, and returns the claim's token.
func claimLado(t *testing.T, store *sqlite.Store, want domain.LadoQueueEntryID, now time.Time) string {
	t.Helper()
	token := "lease-" + string(want)
	claimed, err := store.ClaimLadoQueueEntries(context.Background(), now, now.Add(2*time.Minute), 1, nil, func() string { return token })
	if err != nil || len(claimed) != 1 || claimed[0].ID != want {
		t.Fatalf("claim = %+v, %v; want %s", claimed, err, want)
	}
	return token
}

// runLado claims the oldest claimable entry, which must be seed's, and links
// a new worker session of project to it as an execution started at now.
func runLado(t *testing.T, store *sqlite.Store, project domain.ProjectID, seed ladoSeed, now time.Time, exec domain.LadoExecution) domain.SessionID {
	t.Helper()
	ctx := context.Background()
	token := claimLado(t, store, seed.entry, now)
	worker, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: project, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		ClientRequestID: domain.LadoClientRequestID(seed.entry, 1), ClientRequestHash: domain.LadoDispatchHash(seed.plan, seed.snapshot),
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create the worker session: %v", err)
	}
	exec.SessionID, exec.StartedAt = worker.ID, now
	if err := store.LinkLadoExecution(ctx, seed.entry, token, exec); err != nil {
		t.Fatalf("link the execution: %v", err)
	}
	return worker.ID
}

func writeLadoPR(t *testing.T, store *sqlite.Store, session domain.SessionID, number int, merged bool, at time.Time) {
	t.Helper()
	if err := store.WritePR(context.Background(), domain.PullRequest{
		URL: fmt.Sprintf("https://github.com/acme/demo/pull/%d", number), SessionID: session, Number: number, Merged: merged, UpdatedAt: at,
	}, nil, nil); err != nil {
		t.Fatalf("write PR %d: %v", number, err)
	}
}

func newLadoQueueServer(t *testing.T, store *sqlite.Store) *httptest.Server {
	t.Helper()
	return newLadoTestServer(t, httpd.APIDeps{LadoQueue: ladoqueue.New(ladoqueue.Deps{Store: store})})
}

func ladoErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	return decodeLadoJSON[envelope.APIError](t, body).Code
}

func TestLadoAPI_QueueListsEntries(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	first := seedLadoQueued(t, store, "mer", 1, now.Add(-3*time.Minute))
	second := seedLadoQueued(t, store, "mer", 2, now.Add(-2*time.Minute))
	third := seedLadoQueued(t, store, "web", 3, now.Add(-time.Minute))
	// The first attempt of the first entry failed; it is tried again in 30
	// seconds. The second entry is being started. The third waits for its
	// project's LADO mode.
	retryAt := now.Add(30 * time.Second)
	token := claimLado(t, store, first.entry, now)
	if ok, err := store.ReleaseLadoQueueEntry(ctx, first.entry, token, retryAt, domain.LadoWaitingRetry); err != nil || !ok {
		t.Fatalf("release = %v, %v", ok, err)
	}
	claimLado(t, store, second.entry, now)
	if _, err := store.SetLadoMode(ctx, "web", false, now); err != nil {
		t.Fatalf("turn LADO mode off: %v", err)
	}
	if _, err := store.SetLadoQueueWaiting(ctx, domain.LadoWaitingModeOff, nil); err != nil {
		t.Fatalf("SetLadoQueueWaiting: %v", err)
	}
	srv := newLadoQueueServer(t, store)

	list := func(query string) []controllers.LadoQueueItemResponse {
		t.Helper()
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/queue"+query, "")
		if status != http.StatusOK {
			t.Fatalf("GET queue%s status = %d, body %s", query, status, body)
		}
		return decodeLadoJSON[controllers.LadoQueueResponse](t, body).Entries
	}
	all := list("")
	want := []controllers.LadoQueueItemResponse{
		{ID: "lado-queue-1", TaskID: "lado-task-1", ProjectID: "mer", IssueRef: "github:acme/demo#1", Title: "Task 1", State: "waiting", Reason: "retry", Attempts: 1, NextAttemptAt: &retryAt, EnqueuedAt: now.Add(-3 * time.Minute)},
		{ID: "lado-queue-2", TaskID: "lado-task-2", ProjectID: "mer", IssueRef: "github:acme/demo#2", Title: "Task 2", State: "dispatching", Attempts: 1, EnqueuedAt: now.Add(-2 * time.Minute)},
		{ID: "lado-queue-3", TaskID: "lado-task-3", ProjectID: "web", IssueRef: "github:acme/demo#3", Title: "Task 3", State: "waiting", Reason: "lado_mode_off", EnqueuedAt: now.Add(-time.Minute)},
	}
	if len(all) != len(want) {
		t.Fatalf("queue = %+v, want %d entries", all, len(want))
	}
	for i := range want {
		got := all[i]
		if got.ID != want[i].ID || got.TaskID != want[i].TaskID || got.ProjectID != want[i].ProjectID || got.IssueRef != want[i].IssueRef ||
			got.Title != want[i].Title || got.State != want[i].State || got.Reason != want[i].Reason || got.Attempts != want[i].Attempts ||
			got.Error != "" || !got.EnqueuedAt.Equal(want[i].EnqueuedAt) || !sameLadoTime(got.NextAttemptAt, want[i].NextAttemptAt) {
			t.Fatalf("entry %d = %+v, want %+v", i, got, want[i])
		}
	}
	if mer := list("?projectId=mer"); len(mer) != 2 || mer[0].ID != "lado-queue-1" || mer[1].ID != "lado-queue-2" {
		t.Fatalf("queue of mer = %+v, want its two entries", mer)
	}
	if web := list("?projectId=web"); len(web) != 1 || web[0].ID != string(third.entry) {
		t.Fatalf("queue of web = %+v, want %s", web, third.entry)
	}
	body, _, _ := doRequest(t, srv, "GET", "/api/v1/lado/queue?projectId=none", "")
	if !strings.Contains(string(body), `"entries":[]`) {
		t.Fatalf("queue of a project without entries = %s, want an empty list", body)
	}
}

func sameLadoTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func TestLadoAPI_ExecutionDetail(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	approvedAt := now.Add(-time.Hour)
	seed := seedLadoQueued(t, store, "mer", 1, approvedAt)
	ahead := 3
	worker := runLado(t, store, "mer", seed, now, domain.LadoExecution{
		DeadlineAt: now.Add(8 * time.Hour), DispatchBaseSHA: strings.Repeat("d", 40), BaseCommitsAhead: &ahead,
	})
	writeLadoPR(t, store, worker, 7, false, now.Add(time.Minute))
	srv := newLadoQueueServer(t, store)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/executions/"+string(worker), "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoExecutionResponse](t, body)
	wantTask := controllers.LadoExecutionTaskResponse{ID: "lado-task-1", Title: "Task 1", IssueRef: "github:acme/demo#1", IssueURL: "https://github.com/acme/demo/issues/1"}
	if got.SessionID != string(worker) || got.Task != wantTask || got.Attempt != 1 || got.SessionEnded {
		t.Fatalf("execution = %+v, want attempt 1 of lado-task-1 in a live session", got)
	}
	if got.Plan.RevisionID != "lado-plan-1" || got.Plan.Number != 1 || got.Plan.SHA256 != seed.plan || !got.Plan.ApprovedAt.Equal(approvedAt) {
		t.Fatalf("plan = %+v, want lado-plan-1 approved at %s", got.Plan, approvedAt)
	}
	snapshot := got.Snapshot
	wantRules := []controllers.LadoExecutionRuleResponse{
		{ID: string(domain.LadoRulePermissionCeiling), Name: "Permission ceiling", Version: 1},
		{ID: "team.tests", Name: "Run the tests", Version: 3},
	}
	if snapshot.SHA256 != seed.snapshot || snapshot.Agent != "claude-code" || snapshot.Model != "claude-sonnet-5" || snapshot.PermissionMode != "accept-edits" ||
		snapshot.PermissionCeiling != "auto" || snapshot.Prompt != ladoWorkerRules || fmt.Sprint(snapshot.Rules) != fmt.Sprint(wantRules) {
		t.Fatalf("snapshot = %+v, want the approval's", snapshot)
	}
	wantSetup := []controllers.LadoExecutionSetupItemResponse{{ItemID: "lado-setup-item-1", Tool: "claude-code", Kind: "instructions", Name: "CLAUDE.md", Version: 2}}
	if !reflect.DeepEqual(snapshot.Setup, wantSetup) {
		t.Fatalf("snapshot setup = %+v, want %+v", snapshot.Setup, wantSetup)
	}
	if base := got.Base; base.PlanSHA != strings.Repeat("c", 40) || base.DispatchSHA != strings.Repeat("d", 40) || base.CommitsAhead == nil || *base.CommitsAhead != 3 {
		t.Fatalf("base = %+v, want the move from the plan's base", base)
	}
	if !got.StartedAt.Equal(now) || got.DeadlineAt == nil || !got.DeadlineAt.Equal(now.Add(8*time.Hour)) || got.TimedOutAt != nil {
		t.Fatalf("run started %s, deadline %v, timed out %v; want the eight-hour run", got.StartedAt, got.DeadlineAt, got.TimedOutAt)
	}
	wantPR := controllers.LadoExecutionPRResponse{URL: "https://github.com/acme/demo/pull/7", Number: 7}
	if got.PR == nil || *got.PR != wantPR {
		t.Fatalf("PR = %+v, want the open PR 7", got.PR)
	}

	// Stopped at its deadline, the execution says when.
	stoppedAt := now.Add(8 * time.Hour)
	if ok, err := store.MarkLadoExecutionTimedOut(ctx, worker, domain.LadoAuditEvent{
		At: stoppedAt, Actor: "lado", Action: "execution.timed_out", SubjectKind: "execution", SubjectID: string(worker), Payload: []byte("{}"),
	}); err != nil || !ok {
		t.Fatalf("MarkLadoExecutionTimedOut = %v, %v", ok, err)
	}
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/executions/"+string(worker), "")
	if got := decodeLadoJSON[controllers.LadoExecutionResponse](t, body); got.TimedOutAt == nil || !got.TimedOutAt.Equal(stoppedAt) {
		t.Fatalf("timed out at %v, want %s", got.TimedOutAt, stoppedAt)
	}

	// A session that is not an execution has none.
	other, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("create a session: %v", err)
	}
	for _, id := range []string{string(other.ID), "mer-404"} {
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/executions/"+id, "")
		if status != http.StatusNotFound || ladoErrorCode(t, body) != "LADO_EXECUTION_NOT_FOUND" {
			t.Fatalf("execution of %s: status = %d, body %s; want 404 LADO_EXECUTION_NOT_FOUND", id, status, body)
		}
	}
}

func TestLadoAPI_DispatchSettings(t *testing.T) {
	store := sqlitetest.MustOpen(t)
	srv := newLadoQueueServer(t, store)
	get := func() controllers.LadoDispatchSettingsResponse {
		t.Helper()
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/dispatch-settings", "")
		if status != http.StatusOK {
			t.Fatalf("GET status = %d, body %s", status, body)
		}
		return decodeLadoJSON[controllers.LadoDispatchSettingsResponse](t, body)
	}

	if got := get(); got.MaxConcurrent != 2 || got.MaxRunMinutes != 480 || !got.HoldOnMemoryTight || got.UpdatedAt != nil {
		t.Fatalf("defaults = %+v, want 2 runs of up to 480 minutes held while memory is tight", got)
	}

	// The change needs no presence proof.
	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/dispatch-settings", `{"maxConcurrent":3,"maxRunMinutes":120,"holdOnMemoryTight":false}`)
	if status != http.StatusOK {
		t.Fatalf("PUT status = %d, body %s", status, body)
	}
	if got := decodeLadoJSON[controllers.LadoDispatchSettingsResponse](t, body); got.MaxConcurrent != 3 || got.MaxRunMinutes != 120 || got.HoldOnMemoryTight || got.UpdatedAt == nil {
		t.Fatalf("PUT = %+v, want the stored settings", got)
	}
	stored := get()
	if stored.MaxConcurrent != 3 || stored.MaxRunMinutes != 120 || stored.HoldOnMemoryTight || stored.UpdatedAt == nil {
		t.Fatalf("GET after PUT = %+v, want the new settings", stored)
	}

	for _, c := range []struct {
		body, code string
	}{
		{`{"maxConcurrent":9,"maxRunMinutes":120,"holdOnMemoryTight":false}`, "LADO_DISPATCH_SETTINGS_INVALID"},
		{`{"maxConcurrent":0,"maxRunMinutes":120,"holdOnMemoryTight":false}`, "LADO_DISPATCH_SETTINGS_INVALID"},
		{`{"maxConcurrent":3,"maxRunMinutes":30,"holdOnMemoryTight":false}`, "LADO_DISPATCH_SETTINGS_INVALID"},
		// A missing or null field is not 0 minutes or no memory hold.
		{`{"maxConcurrent":3,"holdOnMemoryTight":false}`, "LADO_DISPATCH_SETTINGS_INVALID"},
		{`{"maxConcurrent":3,"maxRunMinutes":120}`, "LADO_DISPATCH_SETTINGS_INVALID"},
		{`{"maxConcurrent":3,"maxRunMinutes":120,"holdOnMemoryTight":null}`, "LADO_DISPATCH_SETTINGS_INVALID"},
		{`{"maxConcurrent":3,"maxRunMinutes":120,"holdOnMemoryTight":false,"maxPerProject":1}`, "INVALID_JSON"},
		{`not json`, "INVALID_JSON"},
	} {
		body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/dispatch-settings", c.body)
		if status != http.StatusBadRequest || ladoErrorCode(t, body) != c.code {
			t.Fatalf("PUT %s: status = %d, body %s; want 400 %s", c.body, status, body, c.code)
		}
	}
	// Nothing was written: the settings keep the time of the change.
	if got := get(); got.MaxConcurrent != 3 || got.MaxRunMinutes != 120 || got.HoldOnMemoryTight || !sameLadoTime(got.UpdatedAt, stored.UpdatedAt) {
		t.Fatalf("settings after refused changes = %+v, want %+v", got, stored)
	}
}

func TestLadoAPI_ProjectSettingsMaxConcurrent(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoProjects: ladoproject.New(ladoproject.Deps{Store: store, Projects: store}), LadoPresence: presenceVerifier()})
	path := "/api/v1/lado/projects/mer/settings"
	put := func(body string, wantStatus int) []byte {
		t.Helper()
		resp, status, _ := doRequest(t, srv, "PUT", path, body)
		if status != wantStatus {
			t.Fatalf("PUT %s: status = %d, body %s; want %d", body, status, resp, wantStatus)
		}
		return resp
	}
	settings := func(body []byte) controllers.LadoProjectSettingsResponse {
		t.Helper()
		return decodeLadoJSON[controllers.LadoProjectSettingsResponse](t, body)
	}
	current := func() controllers.LadoProjectSettingsResponse {
		t.Helper()
		body, status, _ := doRequest(t, srv, "GET", path, "")
		if status != http.StatusOK {
			t.Fatalf("GET status = %d, body %s", status, body)
		}
		return settings(body)
	}

	if got := current(); got != (controllers.LadoProjectSettingsResponse{ProjectID: "mer", MaxConcurrent: 1}) {
		t.Fatalf("settings before = %+v, want LADO mode off with cap 1", got)
	}
	// The cap alone changes without presence and keeps the mode.
	if got := settings(put(`{"maxConcurrent":3}`, http.StatusOK)); got != (controllers.LadoProjectSettingsResponse{ProjectID: "mer", MaxConcurrent: 3}) {
		t.Fatalf("cap only = %+v, want cap 3 with LADO mode off", got)
	}
	// The mode alone keeps the cap.
	if got := settings(put(`{"ladoMode":true}`, http.StatusOK)); got != (controllers.LadoProjectSettingsResponse{ProjectID: "mer", LadoMode: true, MaxConcurrent: 3}) {
		t.Fatalf("mode only = %+v, want LADO mode on with cap 3", got)
	}
	if code := ladoErrorCode(t, put(`{}`, http.StatusBadRequest)); code != "LADO_SETTINGS_EMPTY" {
		t.Fatalf("empty change = %s, want LADO_SETTINGS_EMPTY", code)
	}
	if code := ladoErrorCode(t, put(`{"maxConcurrent":9}`, http.StatusBadRequest)); code != "LADO_DISPATCH_SETTINGS_INVALID" {
		t.Fatalf("cap 9 = %s, want LADO_DISPATCH_SETTINGS_INVALID", code)
	}
	// Turning LADO mode off needs presence; refused, it leaves the cap too.
	for _, body := range []string{`{"ladoMode":false}`, `{"ladoMode":false,"maxConcurrent":2}`} {
		if code := ladoErrorCode(t, put(body, http.StatusForbidden)); code != "LADO_PRESENCE_REQUIRED" {
			t.Fatalf("unsigned %s = %s, want LADO_PRESENCE_REQUIRED", body, code)
		}
	}
	if got := current(); !got.LadoMode || got.MaxConcurrent != 3 {
		t.Fatalf("settings after refused changes = %+v, want LADO mode on with cap 3", got)
	}
	off := `{"ladoMode":false}`
	body, status := signedRequest(t, srv, "PUT", path, off, off)
	if got := settings(body); status != http.StatusOK || got.LadoMode || got.MaxConcurrent != 3 {
		t.Fatalf("signed mode off: status = %d, settings %+v; want LADO mode off with cap 3", status, got)
	}
}

func TestLadoAPI_TaskCarriesQueueAndExecution(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-time.Hour)
	// Each task is claimed before the next is seeded, so a claim takes it.
	running := seedLadoQueued(t, store, "mer", 1, at)
	runningWorker := runLado(t, store, "mer", running, now, domain.LadoExecution{DeadlineAt: now.Add(8 * time.Hour)})
	writeLadoPR(t, store, runningWorker, 11, false, now)
	timedOut := seedLadoQueued(t, store, "mer", 2, at)
	timedOutWorker := runLado(t, store, "mer", timedOut, now.Add(-9*time.Hour), domain.LadoExecution{DeadlineAt: now.Add(-time.Hour)})
	if ok, err := store.MarkLadoExecutionTimedOut(ctx, timedOutWorker, domain.LadoAuditEvent{
		At: now, Actor: "lado", Action: "execution.timed_out", SubjectKind: "execution", SubjectID: string(timedOutWorker), Payload: []byte("{}"),
	}); err != nil || !ok {
		t.Fatalf("MarkLadoExecutionTimedOut = %v, %v", ok, err)
	}
	merged := seedLadoQueued(t, store, "mer", 3, at)
	mergedWorker := runLado(t, store, "mer", merged, now, domain.LadoExecution{})
	writeLadoPR(t, store, mergedWorker, 13, true, now)
	// The second attempt of lado-task-4 starts; the first one's retry time
	// has passed.
	dispatching := seedLadoQueued(t, store, "mer", 4, at)
	token := claimLado(t, store, dispatching.entry, now.Add(-2*time.Minute))
	if ok, err := store.ReleaseLadoQueueEntry(ctx, dispatching.entry, token, now.Add(-time.Minute), domain.LadoWaitingRetry); err != nil || !ok {
		t.Fatalf("release = %v, %v", ok, err)
	}
	claimLado(t, store, dispatching.entry, now)
	blocked := seedLadoQueued(t, store, "mer", 6, at)
	token = claimLado(t, store, blocked.entry, now)
	if ok, err := store.MarkLadoQueueEntryBlocked(ctx, blocked.entry, token, domain.LadoBlockedBaseRefMismatch); err != nil || !ok {
		t.Fatalf("block = %v, %v", ok, err)
	}
	// lado-task-7's worker ended without a PR, and its plan was approved
	// again: the task waits with the new approval, and its newest execution
	// ran under the old one's snapshot.
	again := seedLadoQueued(t, store, "mer", 7, at)
	endedWorker := runLado(t, store, "mer", again, now.Add(-2*time.Hour), domain.LadoExecution{})
	ended, _, err := store.GetSession(ctx, endedWorker)
	if err != nil {
		t.Fatalf("read the worker session: %v", err)
	}
	ended.IsTerminated = true
	if err := store.UpdateSession(ctx, ended); err != nil {
		t.Fatalf("end the worker session: %v", err)
	}
	if _, _, err := store.ApproveLadoPlan(ctx,
		domain.LadoPlanApproval{ID: "lado-approval-7b", RevisionID: "lado-plan-7", TaskID: "lado-task-7", SHA256: again.plan, ApprovedAt: now},
		domain.LadoPresenceProof{Nonce: "nonce-lado-approval-7b", Action: domain.LadoPresencePlanApprove, SubjectID: "lado-plan-7", ExpiresAt: now.Add(time.Minute), UsedAt: now},
		domain.LadoAuditEvent{At: now, Actor: "human", Action: "plan.approved", SubjectKind: "plan_revision", SubjectID: "lado-plan-7", Payload: []byte("{}")},
		&domain.LadoPolicySnapshot{ID: "lado-snapshot-7b", SHA256: strings.Repeat("6", 64), Content: []byte("{}")},
		&domain.LadoQueueEntry{ID: "lado-queue-7b"}); err != nil {
		t.Fatalf("approve again: %v", err)
	}
	seedLadoQueued(t, store, "mer", 5, at)
	if _, err := store.SetLadoQueueWaiting(ctx, domain.LadoWaitingSlotGlobal, nil); err != nil {
		t.Fatalf("SetLadoQueueWaiting: %v", err)
	}
	// The orchestrator pinned a newer plan for lado-task-3 while its worker
	// ran; the run keeps the revision it started with.
	if _, _, err := store.PinLadoPlanRevision(ctx, domain.LadoPlanRevision{
		ID: "lado-plan-3b", TaskID: "lado-task-3", RequestID: "req-3", SessionID: "mer-orchestrator-1", Path: domain.LadoPlanPath("lado-task-3"),
		ArtifactVersion: 2, Source: "agent", SHA256: strings.Repeat("b", 64), BaseRef: "main", IssueSnapshotSHA256: strings.Repeat("1", 64), CreatedAt: now,
	}); err != nil {
		t.Fatalf("pin a newer plan: %v", err)
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: ladotask.New(ladotask.Deps{Store: store, Projects: store})})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks?projectId=mer", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	tasks := map[string]controllers.LadoTaskResponse{}
	for _, task := range decodeLadoJSON[controllers.LadoTaskListResponse](t, body).Tasks {
		tasks[task.ID] = task
	}
	for _, c := range []struct {
		task, state, reason string
		queue, queueReason  string
		attempts            int
		worker              domain.SessionID
		snapshot            string
		revisionNumber      int
	}{
		{"lado-task-1", "running", "", "dispatched", "", 1, runningWorker, running.snapshot, 1},
		{"lado-task-2", "failed", "timed_out", "dispatched", "", 1, timedOutWorker, timedOut.snapshot, 1},
		// The newer revision hides the run's revision number from the task.
		{"lado-task-3", "completed", "", "dispatched", "", 1, mergedWorker, merged.snapshot, 0},
		{"lado-task-4", "dispatching", "", "dispatching", "", 2, "", "", 0},
		{"lado-task-5", "queued", "slot_global", "waiting", "slot_global", 0, "", "", 0},
		{"lado-task-6", "blocked", "base_ref_mismatch", "blocked", "base_ref_mismatch", 1, "", "", 0},
		// The run of the older approval keeps its revision but not the new
		// approval's snapshot.
		{"lado-task-7", "queued", "slot_global", "waiting", "slot_global", 0, endedWorker, "", 1},
	} {
		got, ok := tasks[c.task]
		if !ok {
			t.Fatalf("tasks %v lack %s", tasks, c.task)
		}
		if got.State != c.state || got.StateReason != c.reason || got.Queue == nil || got.Queue.State != c.queue || got.Queue.Reason != c.queueReason ||
			got.Queue.Attempts != c.attempts || got.Queue.NextAttemptAt != nil {
			t.Fatalf("%s: state %q (%q), queue %+v; want %q (%q) with entry %q (%q) after %d attempts and no next attempt",
				c.task, got.State, got.StateReason, got.Queue, c.state, c.reason, c.queue, c.queueReason, c.attempts)
		}
		if c.worker == "" {
			if got.Execution != nil {
				t.Fatalf("%s: execution %+v, want none", c.task, got.Execution)
			}
			continue
		}
		if exec := got.Execution; exec == nil || exec.SessionID != string(c.worker) || exec.PlanRevisionID != strings.Replace(c.task, "task", "plan", 1) ||
			exec.PlanRevisionNumber != c.revisionNumber || exec.PolicySHA256 != c.snapshot {
			t.Fatalf("%s: execution %+v, want %s running %s (number %d) under snapshot %s", c.task, exec, c.worker,
				strings.Replace(c.task, "task", "plan", 1), c.revisionNumber, c.snapshot)
		}
	}
	exec := tasks["lado-task-1"].Execution
	if !exec.StartedAt.Equal(now) || exec.DeadlineAt == nil || !exec.DeadlineAt.Equal(now.Add(8*time.Hour)) ||
		exec.TimedOut || exec.SessionEnded || exec.PR == nil || *exec.PR != (controllers.LadoExecutionPRResponse{URL: "https://github.com/acme/demo/pull/11", Number: 11}) {
		t.Fatalf("running execution %+v (PR %+v), want the live run with its open PR", exec, exec.PR)
	}
	if exec := tasks["lado-task-7"].Execution; !exec.SessionEnded || exec.TimedOut || exec.PR != nil {
		t.Fatalf("ended execution %+v, want its session ended without a PR", exec)
	}
	if exec := tasks["lado-task-2"].Execution; !exec.TimedOut || exec.PR != nil {
		t.Fatalf("timed out execution %+v, want it timed out without a PR", exec)
	}
	if exec := tasks["lado-task-3"].Execution; exec.PR == nil || !exec.PR.Merged || exec.DeadlineAt != nil {
		t.Fatalf("merged execution %+v, want its merged PR and no deadline", exec)
	}
}

func TestLadoAPI_QueueRoutesWithoutAServiceAnswerNotImplemented(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, route := range [][2]string{
		{"GET", "/api/v1/lado/queue"},
		{"GET", "/api/v1/lado/executions/mer-1"},
		{"GET", "/api/v1/lado/dispatch-settings"},
		{"PUT", "/api/v1/lado/dispatch-settings"},
	} {
		body, status, _ := doRequest(t, srv, route[0], route[1], `{"maxConcurrent":2,"maxRunMinutes":480,"holdOnMemoryTight":true}`)
		if status != http.StatusNotImplemented {
			t.Fatalf("%s %s: status = %d, body %s", route[0], route[1], status, body)
		}
	}
}
