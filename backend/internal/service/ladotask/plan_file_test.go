package ladotask

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// approvedRun approves the fixture's plan and returns the task and its
// revision.
func approvedRun(t *testing.T, pf *planFixture) (domain.LadoTask, domain.LadoPlanRevision) {
	t.Helper()
	rev := pf.planned(t)
	if _, err := pf.svc.ApprovePlan(context.Background(), rev.ID, rev.SHA256, policyA, pf.proof(rev.ID)); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	return pf.latest(t), rev
}

func TestRunInputKeepsThePinnedPlanAsAReadOnlyFile(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	task, rev := approvedRun(t, pf)

	in, err := pf.svc.RunInput(ctx, task)
	if err != nil {
		t.Fatalf("RunInput: %v", err)
	}
	want := filepath.Join(pf.dataDir, "lado", "runs", string(rev.ID), PlanFileName)
	if in.PlanFile != want {
		t.Fatalf("plan file = %q, want %q", in.PlanFile, want)
	}
	data, err := os.ReadFile(in.PlanFile)
	if err != nil || string(data) != in.PlanText {
		t.Fatalf("plan file holds %q (%v), want the pinned plan byte for byte", data, err)
	}
	pinned, _ := pf.blobs.Get(rev.SHA256, domain.LadoPlanMaxBytes)
	if string(data) != string(pinned) {
		t.Fatal("plan file differs from the pinned blob")
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(in.PlanFile); err != nil || info.Mode().Perm()&0o222 != 0 {
			t.Fatalf("plan file mode = %v (%v), want read-only", info.Mode(), err)
		}
	}

	// Each part, merge and integration worker reads the input again: the
	// read-only file is compared, not written again.
	again, err := pf.svc.RunInput(ctx, task)
	if err != nil || again.PlanFile != in.PlanFile {
		t.Fatalf("second RunInput = %q, %v; want the same file", again.PlanFile, err)
	}

	// A file that no longer holds the pinned plan fails the input.
	if err := os.Chmod(in.PlanFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.PlanFile, []byte("not the plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pf.svc.RunInput(ctx, task); err == nil {
		t.Fatal("RunInput succeeded over a plan file that differs from the pinned plan")
	}
}

func TestRunsDirRefusesIDsThatAreNotOneSegment(t *testing.T) {
	for _, id := range []domain.LadoPlanRevisionID{"", ".", "..", "a/b", `a\b`, "c:x"} {
		if got := RunsDir("/data", id); got != "" {
			t.Fatalf("RunsDir(%q) = %q, want none", id, got)
		}
	}
	if got := RunsDir("", "rev-1"); got != "" {
		t.Fatalf("RunsDir without a data dir = %q", got)
	}
	if _, err := writePlanFile(t.TempDir(), "..", []byte("x")); err == nil {
		t.Fatal("writePlanFile wrote outside the runs folder")
	}
}

func TestPlanDirResolverFindsTheRunsFolderBeforeTheExecutionIsLinked(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	task, rev := approvedRun(t, pf)
	if task.Queue.ID == "" {
		t.Fatal("approval queued no entry")
	}
	resolver := NewPlanDirResolver(pf.store, pf.dataDir)
	worker := domain.SessionRecord{ID: "demo-9", ProjectID: task.ProjectID, ClientRequestID: domain.LadoClientRequestID(task.Queue.ID, 1)}

	// No plan file yet: nothing to read.
	if dirs, err := resolver.AdditionalDirectories(ctx, worker); err != nil || dirs != nil {
		t.Fatalf("before RunInput: %q, %v; want none", dirs, err)
	}
	if _, err := pf.svc.RunInput(ctx, task); err != nil {
		t.Fatalf("RunInput: %v", err)
	}
	want := []string{filepath.Join(pf.dataDir, "lado", "runs", string(rev.ID))}
	for _, id := range []string{
		domain.LadoClientRequestID(task.Queue.ID, 1),
		domain.LadoRunClientRequestID(domain.LadoRunRequest{Entry: task.Queue.ID, Role: domain.LadoRolePart, Part: "store", Attempt: 2}),
		domain.LadoRunClientRequestID(domain.LadoRunRequest{Entry: task.Queue.ID, Role: domain.LadoRoleMerge, Part: "store", Attempt: 1}),
		domain.LadoRunClientRequestID(domain.LadoRunRequest{Entry: task.Queue.ID, Role: domain.LadoRoleIntegration, Attempt: 1}),
	} {
		worker.ClientRequestID = id
		if dirs, err := resolver.AdditionalDirectories(ctx, worker); err != nil || !reflect.DeepEqual(dirs, want) {
			t.Fatalf("%s: %q, %v; want %q", id, dirs, err, want)
		}
	}

	// Sessions that are not a LADO run's worker read nothing more.
	for _, id := range []string{
		"",
		"ui-request-1",
		domain.LadoPlanWorkerClientRequestID("req-1", domain.LadoPlanRoleDesigner, 1),
		domain.LadoClientRequestID("other-entry", 1),
	} {
		worker.ClientRequestID = id
		if dirs, err := resolver.AdditionalDirectories(ctx, worker); err != nil || dirs != nil {
			t.Fatalf("%q: %q, %v; want none", id, dirs, err)
		}
	}
}

type fakePlanDirStore struct {
	exec    domain.LadoExecution
	found   bool
	err     error
	listed  int
	listErr error
}

func (f *fakePlanDirStore) LadoExecution(context.Context, domain.SessionID) (domain.LadoExecution, bool, error) {
	return f.exec, f.found, f.err
}

func (f *fakePlanDirStore) ListLadoTasks(context.Context, domain.ProjectID, bool) ([]domain.LadoTask, error) {
	f.listed++
	return nil, f.listErr
}

func TestPlanDirResolverReadsTheLinkedExecutionsRevision(t *testing.T) {
	dataDir := t.TempDir()
	path, err := writePlanFile(dataDir, "rev-7", []byte("plan"))
	if err != nil {
		t.Fatal(err)
	}
	store := &fakePlanDirStore{exec: domain.LadoExecution{SessionID: "s-1", PlanRevisionID: "rev-7"}, found: true}
	resolver := NewPlanDirResolver(store, dataDir)
	rec := domain.SessionRecord{ID: "s-1", ProjectID: "demo", ClientRequestID: domain.LadoClientRequestID("entry-1", 1)}
	dirs, err := resolver.AdditionalDirectories(context.Background(), rec)
	if err != nil || !reflect.DeepEqual(dirs, []string{filepath.Dir(path)}) || store.listed != 0 {
		t.Fatalf("dirs = %q, %v, listed %d; want the execution's runs folder without a task scan", dirs, err, store.listed)
	}

	store.err = errors.New("db closed")
	if _, err := resolver.AdditionalDirectories(context.Background(), rec); err == nil {
		t.Fatal("a failed execution read was not reported")
	}
}
