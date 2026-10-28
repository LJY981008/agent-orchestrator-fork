package ladoqueue

// Admission (decision f of the step 6 plan): how many executions run at once,
// overall and in each project, and whether memory is too tight to start one
// more. An entry that waits for a slot or for memory does not spend an
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

// admit claims entries one at a time, oldest first, while a run slot is free,
// and dispatches each. Entries of projects with LADO mode off wait for it.
// While memory is tight and the settings hold dispatch then, or while the
// running executions fill every slot, the open entries are marked waiting
// for that and nothing is claimed. A project whose running executions fill
// its own slots is left out of the claims, and its open entries wait for a
// slot there; a worker this pass starts takes its slots at once, and may
// fill its project too. Each project's cap is read once a pass.
func (s *Service) admit(ctx context.Context, now time.Time, settings domain.LadoDispatchSettings) error {
	if _, err := s.deps.Store.SetLadoQueueWaiting(ctx, domain.LadoWaitingModeOff, nil); err != nil {
		return err
	}
	if settings.HoldOnMemoryTight && s.memoryTight(ctx) {
		_, err := s.deps.Store.SetLadoQueueWaiting(ctx, domain.LadoWaitingMemoryTight, nil)
		return err
	}
	// Running executions and live leases both take a slot.
	total, perProject, err := s.deps.Store.CountLiveLadoExecutions(ctx, now)
	if err != nil {
		return err
	}
	free := settings.MaxConcurrent - total
	if free <= 0 {
		_, err := s.deps.Store.SetLadoQueueWaiting(ctx, domain.LadoWaitingSlotGlobal, nil)
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
	var full []domain.ProjectID
	// fill leaves a project out of the claims; its open entries wait for a
	// slot there, which is written only when the reason changes.
	fill := func(project domain.ProjectID) {
		full = append(full, project)
		if _, err := s.deps.Store.SetLadoQueueWaiting(ctx, domain.LadoWaitingSlotProject, &project); err != nil {
			problems = append(problems, err)
		}
	}
	for _, project := range slices.Sorted(maps.Keys(perProject)) {
		limit, err := capOf(project)
		if err != nil {
			// Without its cap the project may be full: it waits a pass.
			problems = append(problems, err)
			full = append(full, project)
			continue
		}
		if perProject[project] >= limit {
			fill(project)
		}
	}
	for range maxClaimsPerPass {
		if free <= 0 || ctx.Err() != nil {
			break
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
		if perProject[entry.ProjectID] >= limit {
			// Only a race gets here: the claim left full projects out.
			if err := s.hold(ctx, entry, domain.LadoWaitingSlotProject, now.Add(s.deps.Interval)); err != nil {
				problems = append(problems, fmt.Errorf("queue entry %s: %w", entry.ID, err))
			}
			full = append(full, entry.ProjectID)
			continue
		}
		started, err := s.dispatch(ctx, entry, settings, now)
		if err != nil {
			problems = append(problems, fmt.Errorf("queue entry %s: %w", entry.ID, err))
		}
		if started {
			free--
			perProject[entry.ProjectID]++
			if perProject[entry.ProjectID] >= limit {
				fill(entry.ProjectID)
			}
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
