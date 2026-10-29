package controllers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
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
