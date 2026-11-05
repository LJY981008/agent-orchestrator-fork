package controllers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// A task's parts run reads through GET /lado/tasks/{taskId}/parts: none for
// a task without parts, the integration branch and each part once the run
// started, and the task and queue responses say how far it got.
func TestLadoAPI_TaskParts(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	srv := newLadoQueueServer(t, store)
	now := time.Now().UTC().Truncate(time.Second)
	seed := seedLadoQueued(t, store, "mer", 1, now.Add(-time.Minute))

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/"+string(seed.task)+"/parts", "")
	if got := decodeLadoJSON[controllers.LadoTaskPartsResponse](t, body); status != http.StatusOK || got.Branch != "" || len(got.Parts) != 0 {
		t.Fatalf("parts of a task without them = %d %s", status, body)
	}
	if _, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-9/parts", ""); status != http.StatusNotFound {
		t.Fatalf("parts of an unknown task = %d, want 404", status)
	}

	token := claimLado(t, store, seed.entry, now)
	base := strings.Repeat("a", 40)
	if err := store.StartLadoPartRun(ctx, token, domain.LadoPartRun{
		QueueEntryID: seed.entry, TaskID: seed.task, ProjectID: "mer", PlanRevisionID: "lado-plan-1", RepoDir: "/tmp/mer",
		Branch: "lado/task-1/integration", BaseSHA: base, HeadSHA: base, StartedAt: now,
	}, []domain.LadoTaskPart{
		{PartID: "api", Title: "API", Phase: 1, Steps: []int{1}, AffectedPaths: []string{"api/**"}},
		{PartID: "web", Title: "Web", Position: 1, Phase: 2, Steps: []int{2}, AffectedPaths: []string{"web/**"}, DependsOn: []string{"api"}, WaitingReason: domain.LadoWaitingPartDependency},
	}, domain.LadoAuditEvent{At: now, Actor: "lado", Action: "parts.started", SubjectKind: "queue_entry", SubjectID: string(seed.entry)}); err != nil {
		t.Fatalf("start the parts run: %v", err)
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/tasks/"+string(seed.task)+"/parts", "")
	got := decodeLadoJSON[controllers.LadoTaskPartsResponse](t, body)
	if status != http.StatusOK || got.Branch != "lado/task-1/integration" || got.HeadSHA != base || len(got.Parts) != 2 {
		t.Fatalf("parts = %d %s", status, body)
	}
	web := got.Parts[1]
	if web.ID != "web" || web.Phase != 2 || web.State != "waiting" || web.WaitingReason != "part_dependency" ||
		len(web.DependsOn) != 1 || web.Steps[0] != 2 {
		t.Fatalf("part web = %+v", web)
	}
	if api := got.Parts[0]; api.WaitingReason != "" || len(api.DependsOn) != 0 {
		t.Fatalf("part api = %+v, want no wait and an empty list", api)
	}
}

// A task approved again after its worker ended without a PR runs the new
// approval's parts: the task read shows the parts run, and the ended run
// comes as previousExecution, not as execution. The seed goes through the
// real sqlite store, so this also checks which execution the store reads.
func TestLadoAPI_TaskApprovedAgainRunsPartsWithItsPreviousExecution(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	seed := seedLadoQueued(t, store, "mer", 1, now.Add(-time.Hour))
	endedWorker := runLado(t, store, "mer", seed, now.Add(-30*time.Minute), domain.LadoExecution{})
	ended, _, err := store.GetSession(ctx, endedWorker)
	if err != nil {
		t.Fatalf("read the worker session: %v", err)
	}
	ended.IsTerminated = true
	if err := store.UpdateSession(ctx, ended); err != nil {
		t.Fatalf("end the worker session: %v", err)
	}
	if _, _, err := store.ApproveLadoPlan(ctx,
		domain.LadoPlanApproval{ID: "lado-approval-1b", RevisionID: "lado-plan-1", TaskID: seed.task, SHA256: seed.plan, ApprovedAt: now.Add(-time.Minute)},
		domain.LadoPresenceProof{Nonce: "nonce-lado-approval-1b", Action: domain.LadoPresencePlanApprove, SubjectID: "lado-plan-1", ExpiresAt: now, UsedAt: now.Add(-time.Minute)},
		domain.LadoAuditEvent{At: now.Add(-time.Minute), Actor: "human", Action: "plan.approved", SubjectKind: "plan_revision", SubjectID: "lado-plan-1", Payload: []byte("{}")},
		&domain.LadoPolicySnapshot{ID: "lado-snapshot-1b", SHA256: strings.Repeat("6", 64), Content: []byte("{}")},
		&domain.LadoQueueEntry{ID: "lado-queue-1b"}); err != nil {
		t.Fatalf("approve again: %v", err)
	}
	token := claimLado(t, store, "lado-queue-1b", now)
	base := strings.Repeat("a", 40)
	if err := store.StartLadoPartRun(ctx, token, domain.LadoPartRun{
		QueueEntryID: "lado-queue-1b", TaskID: seed.task, ProjectID: "mer", PlanRevisionID: "lado-plan-1", RepoDir: "/tmp/mer",
		Branch: "lado/task-1/integration", BaseSHA: base, HeadSHA: base, StartedAt: now,
	}, []domain.LadoTaskPart{
		{PartID: "api", Title: "API", Phase: 1, Steps: []int{1}, AffectedPaths: []string{"api/**"}},
		{PartID: "web", Title: "Web", Position: 1, Phase: 1, Steps: []int{2}, AffectedPaths: []string{"web/**"}},
	}, domain.LadoAuditEvent{At: now, Actor: "lado", Action: "parts.started", SubjectKind: "queue_entry", SubjectID: "lado-queue-1b"}); err != nil {
		t.Fatalf("start the parts run: %v", err)
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: ladotask.New(ladotask.Deps{Store: store, Projects: store})})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks?projectId=mer", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	tasks := decodeLadoJSON[controllers.LadoTaskListResponse](t, body).Tasks
	if len(tasks) != 1 {
		t.Fatalf("tasks = %s, want one", body)
	}
	got := tasks[0]
	if got.State != "running" || got.Queue == nil || got.Queue.State != "running_parts" || got.Parts == nil || got.Parts.Total != 2 {
		t.Fatalf("task = %s, want it running its parts", body)
	}
	if got.Execution != nil {
		t.Fatalf("execution %+v, want none before the integration worker starts", got.Execution)
	}
	if prev := got.PreviousExecution; prev == nil || prev.SessionID != string(endedWorker) || prev.PlanRevisionID != "lado-plan-1" ||
		prev.PlanRevisionNumber != 1 || prev.PolicySHA256 != "" || !prev.SessionEnded || prev.PR != nil {
		t.Fatalf("previous execution %+v, want the ended run %s of lado-plan-1 without the new approval's snapshot", prev, endedWorker)
	}
}

// The worker cap and the disk threshold are dispatch settings; a change
// without them keeps them.
func TestLadoAPI_DispatchSettingsWorkersAndDisk(t *testing.T) {
	store := sqlitetest.MustOpen(t)
	srv := newLadoQueueServer(t, store)
	body, _, _ := doRequest(t, srv, "GET", "/api/v1/lado/dispatch-settings", "")
	if got := decodeLadoJSON[controllers.LadoDispatchSettingsResponse](t, body); got.MaxWorkers != 6 || !got.HoldOnDiskLow || got.MinFreeDiskGB != 10 {
		t.Fatalf("defaults = %+v, want 6 workers and 10 GB", got)
	}
	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/dispatch-settings", `{"maxConcurrent":4,"maxRunMinutes":480,"holdOnMemoryTight":true,"maxWorkers":8,"holdOnDiskLow":false,"minFreeDiskGb":20}`)
	if got := decodeLadoJSON[controllers.LadoDispatchSettingsResponse](t, body); status != http.StatusOK || got.MaxWorkers != 8 || got.HoldOnDiskLow || got.MinFreeDiskGB != 20 {
		t.Fatalf("PUT = %d %s", status, body)
	}
	body, _, _ = doRequest(t, srv, "PUT", "/api/v1/lado/dispatch-settings", `{"maxConcurrent":3,"maxRunMinutes":480,"holdOnMemoryTight":true}`)
	if kept := decodeLadoJSON[controllers.LadoDispatchSettingsResponse](t, body); kept.MaxWorkers != 8 || kept.HoldOnDiskLow || kept.MinFreeDiskGB != 20 {
		t.Fatalf("a change without them = %+v, want them kept", kept)
	}
	for _, bad := range []string{`"maxWorkers":13`, `"maxWorkers":0`, `"minFreeDiskGb":0`, `"minFreeDiskGb":1001`} {
		body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/dispatch-settings", `{"maxConcurrent":3,"maxRunMinutes":480,"holdOnMemoryTight":true,`+bad+`}`)
		if status != http.StatusBadRequest || ladoErrorCode(t, body) != "LADO_DISPATCH_SETTINGS_INVALID" {
			t.Fatalf("%s = %d %s", bad, status, body)
		}
	}
}
