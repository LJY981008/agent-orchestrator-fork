package ladodbchange

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestHoldIntegrationStatus(t *testing.T) {
	f := newDetectFixture(t)
	ctx := context.Background()
	head := f.repo.commit(f.repo.dir, map[string]string{
		"db/003.sql": "SELECT 3;\n",
		"db/004.sql": "-- lado:after-deploy\nDROP TABLE x;\n",
	}, nil, "SQL")
	f.store.tasks["task-1"] = domain.LadoTask{ID: "task-1", ProjectID: testProject}
	f.store.executions["sess-1"] = domain.LadoExecution{SessionID: "sess-1", TaskID: "task-1"}
	item, _, err := f.s.Item(ctx, f.input(head, domain.LadoChangeDBSchema))
	if err != nil {
		t.Fatal(err)
	}
	record := domain.LadoVerification{ExecutionSessionID: "sess-1", HeadSHA: head, Items: []domain.LadoVerifyItem{item}}
	approval := domain.LadoIntegrationApproval{ID: "appr-1", ExecutionSessionID: "sess-1", HeadSHA: head}

	held, err := f.s.HoldIntegrationStatus(ctx, approval, record)
	if err != nil || !held {
		t.Fatalf("held = %v, %v", held, err)
	}
	if at, ok := f.store.holds["appr-1"]; !ok || !at.Equal(t0) {
		t.Fatalf("holds = %v", f.store.holds)
	}
	// Asking again keeps the one hold.
	if held, err := f.s.HoldIntegrationStatus(ctx, approval, record); err != nil || !held || len(f.store.holds) != 1 {
		t.Fatalf("again = %v, %v", held, err)
	}

	// Once the pre-merge file is applied the hold goes; the after-deploy
	// file never holds the status.
	f.store.applications = append(f.store.applications, domain.LadoDBChangeApplication{
		ProjectID: testProject, Path: "db/003.sql", BlobSHA: f.repo.blob(head, "db/003.sql"), Result: domain.LadoDBChangeResultApplied,
	})
	if held, err := f.s.HoldIntegrationStatus(ctx, approval, record); err != nil || held || len(f.store.holds) != 0 {
		t.Fatalf("after apply = %v, %v, holds %v", held, err, f.store.holds)
	}

	// A record without a db_changes item is never held.
	plain := domain.LadoVerification{ExecutionSessionID: "sess-1", HeadSHA: head}
	if held, err := f.s.HoldIntegrationStatus(ctx, domain.LadoIntegrationApproval{ID: "appr-2", ExecutionSessionID: "nobody"}, plain); err != nil || held {
		t.Fatalf("plain = %v, %v", held, err)
	}

	// An execution LADO cannot find is an error, so the status waits.
	if _, err := f.s.HoldIntegrationStatus(ctx, domain.LadoIntegrationApproval{ID: "appr-3", ExecutionSessionID: "nobody"}, record); err == nil {
		t.Fatal("missing execution")
	}
}
