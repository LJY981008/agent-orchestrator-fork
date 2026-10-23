package ladoqueue

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

func TestAdmissionHoldsAtTheCaps(t *testing.T) {
	t.Run("the global cap", func(t *testing.T) {
		f := newFixture(t)
		f.store.settings.MaxConcurrent = 2
		f.store.running["proj-9"] = 2
		f.store.add(testEntry("entry-1", t0.Add(-10*time.Minute)), testEntry("entry-2", t0.Add(-5*time.Minute)))

		// Every slot is taken: the entries wait for one, unclaimed.
		f.tick(t, t0)
		for _, id := range []domain.LadoQueueEntryID{"entry-1", "entry-2"} {
			if e := f.store.entry(id); e.WaitingReason != domain.LadoWaitingSlotGlobal || e.AttemptCount != 0 || !e.NextAttemptAt.IsZero() || e.LeaseToken != "" {
				t.Fatalf("entry = %+v, want it waiting for a free slot without an attempt", e)
			}
		}
		if limits := f.store.claimLimits(); len(limits) != 0 {
			t.Fatalf("claimed with limits %v, want no claim", limits)
		}
		// A pass that finds the same reason writes nothing.
		written := f.store.writtenWaits()
		f.tick(t, t0.Add(15*time.Second))
		if n := f.store.writtenWaits(); n != written {
			t.Fatalf("the second pass wrote %d waiting reasons, want none", n-written)
		}

		// A slot frees: the oldest entry takes it, and the other one waits on.
		f.store.running["proj-9"] = 1
		f.tick(t, t0.Add(30*time.Second))
		if spawned := f.sessions.spawned(); len(spawned) != 1 || spawned[0].ClientRequestID != "lado:entry-1/1" {
			t.Fatalf("spawned %+v, want entry-1's worker alone", spawned)
		}
		if e := f.store.entry("entry-2"); e.State(t0.Add(30*time.Second)) != domain.LadoQueueWaiting || e.AttemptCount != 0 {
			t.Fatalf("entry-2 = %+v, want it still waiting without an attempt", e)
		}
	})

	t.Run("the project cap", func(t *testing.T) {
		f := newFixture(t)
		f.store.settings.MaxConcurrent = 4
		// proj-1 runs a worker already, as many as its cap allows.
		f.store.running["proj-1"] = 1
		other := testTask()
		other.ID, other.ProjectID = "task-2", "proj-2"
		f.store.setTask(other)
		f.projects.others = map[string]domain.ProjectRecord{"proj-2": {
			ID: "proj-2", Path: "/work/web", Kind: domain.ProjectKindSingleRepo, Config: domain.ProjectConfig{DefaultBranch: domain.DefaultBranchAuto},
		}}
		inProject := func(id domain.LadoQueueEntryID, task domain.LadoTaskID, project domain.ProjectID, age time.Duration) domain.LadoQueueEntry {
			e := testEntry(id, t0.Add(-age))
			e.TaskID, e.ProjectID = task, project
			return e
		}
		f.store.add(
			inProject("entry-1", "task-1", "proj-1", 40*time.Minute),
			inProject("entry-2", "task-2", "proj-2", 30*time.Minute),
			inProject("entry-3", "task-2", "proj-2", 20*time.Minute),
			inProject("entry-4", "task-1", "proj-1", 10*time.Minute),
		)
		held := func(at time.Time, ids ...domain.LadoQueueEntryID) {
			t.Helper()
			for _, id := range ids {
				if e := f.store.entry(id); e.WaitingReason != domain.LadoWaitingSlotProject || !e.NextAttemptAt.Equal(at.Add(15*time.Second)) ||
					e.AttemptCount != 0 || e.LeaseToken != "" {
					t.Fatalf("entry = %+v, want it waiting a pass for its project's slot without spending an attempt", e)
				}
			}
		}
		requests := func() []string {
			var ids []string
			for _, cfg := range f.sessions.spawned() {
				ids = append(ids, cfg.ClientRequestID)
			}
			return ids
		}

		// proj-1's entries wait for its slot while proj-2's start; the worker
		// this pass starts fills proj-2's slot as well.
		f.tick(t, t0)
		held(t0, "entry-1", "entry-3", "entry-4")
		if got, want := requests(), []string{"lado:entry-2/1"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("spawned %v, want %v", got, want)
		}
		if got := f.sessions.spawned()[0].ProjectID; got != "proj-2" {
			t.Fatalf("spawned in %s, want proj-2", got)
		}

		// proj-1's run ends: its oldest entry starts on its first attempt,
		// however many passes it waited.
		f.store.running["proj-1"] = 0
		next := t0.Add(15 * time.Second)
		f.tick(t, next)
		if got, want := requests(), []string{"lado:entry-2/1", "lado:entry-1/1"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("spawned %v, want %v", got, want)
		}
		held(next, "entry-3", "entry-4")
	})
}

func TestMemoryTightHoldsWhenEnabled(t *testing.T) {
	tight := procmem.Pressure{Source: procmem.PressureSourcePSI, Raw: 35}
	fine := procmem.Pressure{Source: procmem.PressureSourcePSI, Raw: 2}
	const unread = "LADO cannot read memory pressure"
	for _, c := range []struct {
		name     string
		hold     bool
		pressure procmem.Pressure
		err      error
		noReader bool
		// waits is whether the entry waits for memory; logged whether the
		// first failed reading is logged.
		waits  bool
		logged bool
	}{
		{"tight memory while the setting holds", true, tight, nil, false, true, false},
		{"tight memory with the setting off", false, tight, nil, false, false, false},
		{"a platform without a reading", true, procmem.Pressure{}, procmem.ErrUnsupported, false, false, true},
		{"a reading that fails", true, procmem.Pressure{}, errors.New("open /proc/pressure/memory: permission denied"), false, false, true},
		{"no memory reader", true, procmem.Pressure{}, nil, true, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.settings.HoldOnMemoryTight = c.hold
			f.memory.set(c.pressure, c.err)
			if c.noReader {
				f.svc.deps.Memory = nil
			}
			f.store.add(testEntry("entry-1", t0.Add(-10*time.Minute)))

			f.tick(t, t0)

			e := f.store.entry("entry-1")
			if c.waits {
				if e.WaitingReason != domain.LadoWaitingMemoryTight || e.AttemptCount != 0 || len(f.store.claimLimits()) != 0 || len(f.sessions.spawned()) != 0 {
					t.Fatalf("entry %+v after %d spawns, want it waiting for memory, unclaimed", e, len(f.sessions.spawned()))
				}
				// Once memory eases, the entry starts.
				f.memory.set(fine, nil)
				f.tick(t, t0.Add(15*time.Second))
				if e := f.store.entry("entry-1"); e.State(t0) != domain.LadoQueueDispatched || len(f.sessions.spawned()) != 1 {
					t.Fatalf("entry %+v after %d spawns, want it dispatched", e, len(f.sessions.spawned()))
				}
				return
			}
			if e.State(t0) != domain.LadoQueueDispatched || len(f.sessions.spawned()) != 1 {
				t.Fatalf("entry %+v after %d spawns, want it dispatched", e, len(f.sessions.spawned()))
			}
			if !c.hold && f.memory.readings() != 0 {
				t.Fatalf("read memory %d times with the setting off, want none", f.memory.readings())
			}
			f.tick(t, t0.Add(15*time.Second))
			if n := strings.Count(f.logs.String(), unread); n != map[bool]int{true: 1}[c.logged] {
				t.Fatalf("logged %q %d times over two passes, want it once when a reading failed:\n%s", unread, n, f.logs.String())
			}
		})
	}
}

// The board's pressure light and the dispatcher read memory the same way
// (packages/product-ui/src/resource-presentation.ts pressureStateFromRaw).
func TestMemoryTightThresholds(t *testing.T) {
	for _, c := range []struct {
		source string
		raw    float64
		want   bool
	}{
		{procmem.PressureSourcePSI, 0, false},
		{procmem.PressureSourcePSI, 20, false},
		{procmem.PressureSourcePSI, 20.01, true},
		{procmem.PressureSourcePSI, 80, true},
		{procmem.PressureSourceMemoryStatus, 1, false},
		{procmem.PressureSourceMemoryStatus, 2, false},
		{procmem.PressureSourceMemoryStatus, 3.99, false},
		{procmem.PressureSourceMemoryStatus, 4, true},
		{procmem.PressureSourceAvailablePct, 75, false},
		{procmem.PressureSourceAvailablePct, 90, false},
		{procmem.PressureSourceAvailablePct, 90.5, true},
		// Any other source reads as the available-percent fallback, as on the
		// board.
		{"", 0, false},
		{"", 95, true},
	} {
		if got := tightPressure(procmem.Pressure{Source: c.source, Raw: c.raw}); got != c.want {
			t.Errorf("tightPressure(%s %v) = %v, want %v", c.source, c.raw, got, c.want)
		}
	}
}

// Decision l of the step 6 plan: LADO mode off pauses dispatch for the
// project, and its entries show what they wait for.
func TestModeOffProjectsWait(t *testing.T) {
	sf := newStoreFixture(t, nil)
	ctx := context.Background()
	setMode := func(on bool) {
		t.Helper()
		if _, err := sf.db.SetLadoMode(ctx, "proj-1", on, t0.Add(-time.Minute)); err != nil {
			t.Fatalf("SetLadoMode(%v): %v", on, err)
		}
	}
	waits := func(at time.Time, reason string) {
		t.Helper()
		queue, err := sf.db.ListLadoQueue(ctx, nil)
		if err != nil || len(queue) != 1 {
			t.Fatalf("queue = %+v (%v), want the task's entry", queue, err)
		}
		e := queue[0].Entry
		if e.ID != storeEntry || e.State(at) != domain.LadoQueueWaiting || e.WaitingReason != reason || e.AttemptCount != 0 {
			t.Fatalf("queued entry = %+v, want it waiting for %s without an attempt", e, reason)
		}
		task, _, err := sf.db.LadoTask(ctx, "task-1")
		if err != nil || task.StateAt(at) != domain.LadoTaskQueued || task.Queue.WaitingReason != reason {
			t.Fatalf("task %s waiting for %q (%v), want it queued for %s", task.StateAt(at), task.Queue.WaitingReason, err, reason)
		}
	}
	setMode(false)

	sf.tick(t, t0)
	waits(t0, domain.LadoWaitingModeOff)
	// Tight memory holds the other projects, as the default settings say;
	// this one waits for its mode.
	sf.memory.set(procmem.Pressure{Source: procmem.PressureSourcePSI, Raw: 60}, nil)
	sf.tick(t, t0.Add(15*time.Second))
	waits(t0.Add(15*time.Second), domain.LadoWaitingModeOff)
	if n := len(sf.sessions.spawned()); n != 0 {
		t.Fatalf("spawned %d workers with LADO mode off, want none", n)
	}

	// Turned on again, the project's entry starts.
	sf.memory.set(procmem.Pressure{Source: procmem.PressureSourcePSI, Raw: 1}, nil)
	setMode(true)
	at := t0.Add(30 * time.Second)
	sf.tick(t, at)
	worker := sf.session(t, 1)
	if exec := sf.execution(t, worker.ID); exec.QueueEntryID != storeEntry {
		t.Fatalf("execution = %+v, want the entry's", exec)
	}
	if queue, err := sf.db.ListLadoQueue(ctx, nil); err != nil || len(queue) != 0 {
		t.Fatalf("queue = %+v (%v), want it empty", queue, err)
	}
	if n := len(sf.sessions.spawned()); n != 1 {
		t.Fatalf("spawned %d workers, want 1", n)
	}
}
