package ladoqueue

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
		held := func(ids ...domain.LadoQueueEntryID) {
			t.Helper()
			for _, id := range ids {
				// Unclaimed, the entry waits for a slot, not for a time.
				if e := f.store.entry(id); e.WaitingReason != domain.LadoWaitingSlotProject || !e.NextAttemptAt.IsZero() ||
					e.AttemptCount != 0 || e.LeaseToken != "" {
					t.Fatalf("entry = %+v, want it waiting for its project's slot, unclaimed", e)
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
		held("entry-1", "entry-3", "entry-4")
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
		held("entry-3", "entry-4")
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

// B1: a project whose run slots are all taken is left out of the claims, so
// its entries wait without a claim and a hold each pass: their attempts stay,
// and the reason is written once. Other projects' entries are still claimed.
func TestFullProjectsAreNotClaimed(t *testing.T) {
	sf := newStoreFixture(t, nil)
	ctx := context.Background()
	settings := domain.DefaultLadoDispatchSettings()
	settings.MaxConcurrent = 4
	if _, err := sf.db.SetLadoDispatchSettings(ctx, settings, domain.LadoAuditEvent{At: t0, Actor: "api", Action: "dispatch.settings_changed", SubjectKind: "dispatch_settings", SubjectID: "1"}); err != nil {
		t.Fatalf("set the dispatch settings: %v", err)
	}
	// task-1's worker runs, as many as proj-1's cap allows.
	sf.tick(t, t0)
	if exec := sf.execution(t, sf.session(t, 1).ID); exec.TaskID != "task-1" {
		t.Fatalf("execution = %+v, want task-1's", exec)
	}
	if err := sf.db.UpsertProject(ctx, domain.ProjectRecord{ID: "proj-2", Path: "/work/web", RegisteredAt: t0}); err != nil {
		t.Fatal(err)
	}
	if _, err := sf.db.SetLadoMode(ctx, "proj-2", true, t0); err != nil {
		t.Fatal(err)
	}
	sf.projects.others = map[string]domain.ProjectRecord{"proj-2": {
		ID: "proj-2", Path: "/work/web", Kind: domain.ProjectKindSingleRepo, Config: domain.ProjectConfig{DefaultBranch: domain.DefaultBranchAuto},
	}}
	sf.setClock(t0.Add(time.Minute))
	waiting := sf.queueTask(t, "3", "proj-1")
	other := sf.queueTask(t, "4", "proj-2")
	changes := sf.taskChanges(t, "task-3")

	for i := range 6 {
		sf.tick(t, t0.Add(time.Duration(2+i)*time.Minute))
	}

	entry, ok, err := sf.db.LadoTask(ctx, "task-3")
	if err != nil || !ok {
		t.Fatalf("task-3 = (%v, %v)", ok, err)
	}
	if e := entry.Queue; e.ID != waiting || e.AttemptCount != 0 || e.LeaseToken != "" || e.WaitingReason != domain.LadoWaitingSlotProject {
		t.Fatalf("task-3's entry = %+v, want it waiting for proj-1's slot without an attempt", e)
	}
	if n := sf.taskChanges(t, "task-3") - changes; n != 1 {
		t.Fatalf("six passes published %d changes of task-3, want the one waiting reason", n)
	}
	var requests []string
	for _, cfg := range sf.sessions.spawned() {
		requests = append(requests, cfg.ClientRequestID)
	}
	if want := []string{"lado:lado-queue-1/1", "lado:" + string(other) + "/1"}; !reflect.DeepEqual(requests, want) {
		t.Fatalf("spawned %v, want %v", requests, want)
	}
}

// B2: each claim's lease runs from the claim, so a spawn that took long does
// not leave the next entry's lease expired before its worker starts.
func TestLeaseStartsAtTheClaim(t *testing.T) {
	f := newFixture(t)
	f.store.settings.MaxConcurrent = 2
	f.store.caps["proj-1"] = 2
	f.store.add(testEntry("entry-1", t0.Add(-10*time.Minute)), testEntry("entry-2", t0.Add(-5*time.Minute)))
	slow := t0.Add(3 * time.Minute)
	var leaseAtSecondSpawn time.Time
	f.sessions.onSpawn = func(cfg ports.SpawnConfig) {
		switch cfg.ClientRequestID {
		case "lado:entry-1/1":
			// AO takes three minutes, longer than a lease, to start the
			// first worker.
			f.setClock(slow)
		case "lado:entry-2/1":
			leaseAtSecondSpawn = f.store.entry("entry-2").LeaseExpiresAt
		}
	}

	f.setClock(t0)
	if err := f.svc.Tick(context.Background(), t0); err != nil {
		t.Fatalf("Tick = %v", err)
	}

	if n := len(f.sessions.spawned()); n != 2 {
		t.Fatalf("spawned %d workers, want 2", n)
	}
	if want := slow.Add(defaultLease); !leaseAtSecondSpawn.Equal(want) {
		t.Fatalf("entry-2's lease ran to %s while its worker started, want %s: the claim's time and a lease", leaseAtSecondSpawn, want)
	}
}
