package ladoqueue

import (
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// pathsPlan is the fixture's plan naming paths.
func pathsPlan(paths ...string) string {
	text := "---\ntask: task-1\nissue: github:acme/app#12\nbase_ref: main\naffected_paths:\n"
	for _, p := range paths {
		text += "  - \"" + p + "\"\n"
	}
	return text + "---\n\n# Plan\n"
}

// A task whose paths overlap a live run of another task of the project waits
// with that task's issue; overlaps only on ignored files, another project's
// run, and a project with the wait off do not hold it.
func TestATaskWaitsForAnOverlappingRunOfTheProject(t *testing.T) {
	cases := []struct {
		name    string
		ours    []string
		theirs  []string
		project domain.ProjectID
		off     bool
		waits   bool
	}{
		{"overlap", []string{"backend/internal/service/**"}, []string{"backend/internal/service/ladoqueue/x.go"}, "proj-1", false, true},
		{"apart", []string{"backend/**"}, []string{"frontend/**"}, "proj-1", false, false},
		{"only ignored files", []string{"docs/**", "frontend/src/renderer/i18n/*.json"}, []string{"docs/LJY-PRD/TDD.md", "frontend/src/renderer/i18n/*.json"}, "proj-1", false, false},
		{"another project", []string{"backend/**"}, []string{"backend/**"}, "proj-2", false, false},
		{"wait off", []string{"backend/**"}, []string{"backend/**"}, "proj-1", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.settings.MaxConcurrent = 4
			f.store.caps["proj-1"] = 4
			f.store.overlapOff = tc.off
			f.tasks.input.PlanText = pathsPlan(tc.ours...)
			f.tasks.revisionPaths = map[domain.LadoPlanRevisionID][]string{"rev-9": tc.theirs}
			f.store.liveTasks = []domain.LadoLiveRunTask{{TaskID: "task-9", ProjectID: tc.project, IssueRef: "github:acme/app#9", PlanRevisionID: "rev-9"}}
			f.store.add(testEntry("entry-1", t0.Add(-time.Minute)))
			f.tick(t, t0)
			entry := f.store.entry("entry-1")
			if tc.waits {
				if entry.WaitingReason != domain.LadoWaitingPathsOverlap || entry.WaitingDetail != "github:acme/app#9" || len(f.sessions.spawned()) != 0 || entry.AttemptCount != 0 {
					t.Fatalf("entry = %+v, spawns %d; want it waiting for #9 without spending an attempt", entry, len(f.sessions.spawned()))
				}
				return
			}
			if entry.DispatchedSessionID == "" {
				t.Fatalf("entry = %+v, want it dispatched", entry)
			}
		})
	}
}

// New runs wait while the disk is low, and start again once it has room.
func TestNewRunsWaitWhileTheDiskIsLow(t *testing.T) {
	f := newFixture(t)
	free := uint64(2 << 30)
	f.svc.deps.FreeDisk = func(string) (uint64, error) { return free, nil }
	f.svc.deps.DataDir = "/data"
	f.store.add(testEntry("entry-1", t0.Add(-time.Minute)))
	f.tick(t, t0)
	if entry := f.store.entry("entry-1"); entry.WaitingReason != domain.LadoWaitingDiskLow || len(f.sessions.spawned()) != 0 {
		t.Fatalf("entry = %+v, want it waiting for disk", entry)
	}
	free = 50 << 30
	f.tick(t, t0.Add(time.Minute))
	if entry := f.store.entry("entry-1"); entry.DispatchedSessionID == "" {
		t.Fatalf("entry = %+v, want it dispatched once the disk has room", entry)
	}
}

// A disk reading that fails holds nothing.
func TestAnUnreadableDiskHoldsNothing(t *testing.T) {
	f := newFixture(t)
	f.svc.deps.FreeDisk = func(string) (uint64, error) { return 0, errors.New("statfs: no such file") }
	f.svc.deps.DataDir = "/data"
	f.store.add(testEntry("entry-1", t0.Add(-time.Minute)))
	f.tick(t, t0)
	if entry := f.store.entry("entry-1"); entry.DispatchedSessionID == "" {
		t.Fatalf("entry = %+v, want it dispatched", entry)
	}
}

// Entries wait for a worker slot while the running workers fill the worker
// cap, whatever the task cap allows.
func TestEntriesWaitForAWorkerSlot(t *testing.T) {
	f := newFixture(t)
	f.store.settings.MaxConcurrent, f.store.settings.MaxWorkers = 4, 3
	f.store.caps["proj-1"] = 4
	f.store.extraWorkers = 3
	f.store.add(testEntry("entry-1", t0.Add(-time.Minute)))
	f.tick(t, t0)
	if entry := f.store.entry("entry-1"); entry.WaitingReason != domain.LadoWaitingSlotWorkers || len(f.sessions.spawned()) != 0 {
		t.Fatalf("entry = %+v, want it waiting for a worker slot", entry)
	}
}
