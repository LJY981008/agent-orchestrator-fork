package ladoqueue

// Admission (decision f of the step 6 plan, as parallel parts revised it):
// how many tasks run at once, overall and in each project, how many LADO
// workers run at once, and whether memory or disk is too tight to start one
// more. An entry that waits for a slot, memory or disk does not spend an
// attempt.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// maxClaimsPerPass bounds the claims of one pass, whatever the entries turn
// out to need.
const maxClaimsPerPass = 50

// admit claims entries one at a time, oldest first, while a task slot and a
// worker slot are free, and dispatches each. Entries of projects with LADO
// mode off wait for it. While memory is tight and the settings hold dispatch
// then, while the free disk space is low, while the running workers fill
// every worker slot, or while the running tasks fill every task slot, the
// open entries are marked waiting for that and nothing is claimed. A project
// whose running tasks fill its own slots is left out of the claims, and its
// open entries wait for a slot there. What runs is read again before each
// claim, so a worker or a parts run this pass starts takes its slots at
// once. Each project's cap is read once a pass.
func (s *Service) admit(ctx context.Context, now time.Time, settings domain.LadoDispatchSettings) error {
	if _, err := s.deps.Store.SetLadoQueueWaiting(ctx, domain.LadoWaitingModeOff, nil); err != nil {
		return err
	}
	if settings.HoldOnMemoryTight && s.memoryTight(ctx) {
		_, err := s.deps.Store.SetLadoQueueWaiting(ctx, domain.LadoWaitingMemoryTight, nil)
		return err
	}
	if s.diskLow(settings, "") {
		_, err := s.deps.Store.SetLadoQueueWaiting(ctx, domain.LadoWaitingDiskLow, nil)
		return err
	}
	var problems []error
	caps := map[domain.ProjectID]int{}
	capOf := func(project domain.ProjectID) (int, error) {
		if limit, ok := caps[project]; ok {
			return limit, nil
		}
		limit, err := s.deps.Store.LadoProjectMaxConcurrent(ctx, project)
		if err != nil {
			return 0, fmt.Errorf("read the run cap of project %s: %w", project, err)
		}
		caps[project] = limit
		return limit, nil
	}
	marked := map[domain.ProjectID]bool{}
	for range maxClaimsPerPass {
		if ctx.Err() != nil {
			break
		}
		load, err := s.deps.Store.LadoRunLoad(ctx, now)
		if err != nil {
			problems = append(problems, err)
			break
		}
		if load.Workers >= settings.MaxWorkers {
			if _, err := s.deps.Store.SetLadoQueueWaiting(ctx, domain.LadoWaitingSlotWorkers, nil); err != nil {
				problems = append(problems, err)
			}
			break
		}
		if load.TaskCount() >= settings.MaxConcurrent {
			if _, err := s.deps.Store.SetLadoQueueWaiting(ctx, domain.LadoWaitingSlotGlobal, nil); err != nil {
				problems = append(problems, err)
			}
			break
		}
		perProject := load.TasksByProject()
		var full []domain.ProjectID
		for _, project := range slices.Sorted(maps.Keys(perProject)) {
			limit, err := capOf(project)
			if err != nil {
				// Without its cap the project may be full: it waits a pass.
				problems = append(problems, err)
				full = append(full, project)
				continue
			}
			if perProject[project] >= limit {
				full = append(full, project)
				// Its open entries wait for a slot there, written once.
				if !marked[project] {
					marked[project] = true
					if _, err := s.deps.Store.SetLadoQueueWaiting(ctx, domain.LadoWaitingSlotProject, &project); err != nil {
						problems = append(problems, err)
					}
				}
			}
		}
		claimed, err := s.deps.Store.ClaimLadoQueueEntries(ctx, now, s.leaseEnd(now), 1, full, s.deps.NewID)
		if err != nil {
			problems = append(problems, err)
			break
		}
		if len(claimed) == 0 {
			break
		}
		entry := claimed[0]
		limit, err := capOf(entry.ProjectID)
		if err != nil {
			problems = append(problems, err)
			if err := s.handBack(ctx, entry); err != nil {
				problems = append(problems, fmt.Errorf("queue entry %s: %w", entry.ID, err))
			}
			break
		}
		if perProject[entry.ProjectID] >= limit && !load.Runs(entry.TaskID) {
			// Only a race gets here: the claim left full projects out.
			if err := s.hold(ctx, entry, domain.LadoWaitingSlotProject, now.Add(s.deps.Interval)); err != nil {
				problems = append(problems, fmt.Errorf("queue entry %s: %w", entry.ID, err))
			}
			continue
		}
		if _, err := s.dispatch(ctx, entry, settings, load, now); err != nil {
			problems = append(problems, fmt.Errorf("queue entry %s: %w", entry.ID, err))
		}
	}
	return errors.Join(problems...)
}

// leaseEnd is when a claim made now ends: a lease from the clock's time, as
// the spawns before the claim took theirs, and never from before the pass.
func (s *Service) leaseEnd(now time.Time) time.Time {
	from := s.deps.Clock().UTC()
	if from.Before(now) {
		from = now
	}
	return from.Add(s.deps.Lease)
}

// memoryTight reports whether memory pressure is tight now. Pressure that
// cannot be read, as on a platform without a reading, is not tight; the
// first such failure is logged.
func (s *Service) memoryTight(ctx context.Context) bool {
	if s.deps.Memory == nil {
		s.memoryUnread(errors.New("no memory reader is wired"))
		return false
	}
	pressure, err := s.deps.Memory.Pressure(ctx)
	if err != nil {
		s.memoryUnread(err)
		return false
	}
	return tightPressure(pressure)
}

func (s *Service) memoryUnread(err error) {
	if s.memoryUnreadable.Swap(true) {
		return
	}
	level := slog.LevelWarn
	if errors.Is(err, procmem.ErrUnsupported) {
		level = slog.LevelInfo
	}
	s.deps.Log.Log(context.Background(), level, "LADO cannot read memory pressure, so queued tasks do not wait for memory", "err", err)
}

// MemoryTight reports whether memory is tight now, as the dispatcher reads
// it. LADO's plan workers wait on it too (ladotask.PlanQueue).
func (s *Service) MemoryTight(ctx context.Context) bool {
	return s.memoryTight(ctx)
}

// tightPressure reports whether a pressure reading is tight by the thresholds
// the board's pressure light uses (packages/product-ui/src/resource-presentation.ts
// pressureStateFromRaw).
func tightPressure(p procmem.Pressure) bool {
	switch p.Source {
	case procmem.PressureSourcePSI:
		// The share of the last ten seconds some task stalled on memory.
		return p.Raw > 20
	case procmem.PressureSourceMemoryStatus:
		// macOS's kernel level: 1 normal, 2 warn, 4 critical.
		return p.Raw >= 4
	default:
		// available_pct: 100 minus the percent of memory available.
		return p.Raw > 90
	}
}
