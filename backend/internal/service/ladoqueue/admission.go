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
// for that and nothing is claimed. An entry whose project fills its own slots
// waits for the next pass, and the next claim may be of another project. The
// workers this pass starts take their slots at once.
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
	caps := map[domain.ProjectID]int{}
	var problems []error
	for range maxClaimsPerPass {
		if free <= 0 || ctx.Err() != nil {
			break
		}
		claimed, err := s.deps.Store.ClaimLadoQueueEntries(ctx, now, now.Add(s.deps.Lease), 1, s.deps.NewID)
		if err != nil {
			problems = append(problems, err)
			break
		}
		if len(claimed) == 0 {
			break
		}
		entry := claimed[0]
		limit, ok := caps[entry.ProjectID]
		if !ok {
			if limit, err = s.deps.Store.LadoProjectMaxConcurrent(ctx, entry.ProjectID); err != nil {
				problems = append(problems, fmt.Errorf("read the run cap of project %s: %w", entry.ProjectID, err))
				if err := s.handBack(ctx, entry); err != nil {
					problems = append(problems, fmt.Errorf("queue entry %s: %w", entry.ID, err))
				}
				break
			}
			caps[entry.ProjectID] = limit
		}
		if perProject[entry.ProjectID] >= limit {
			if err := s.hold(ctx, entry, domain.LadoWaitingSlotProject, now.Add(s.deps.Interval)); err != nil {
				problems = append(problems, fmt.Errorf("queue entry %s: %w", entry.ID, err))
			}
			continue
		}
		started, err := s.dispatch(ctx, entry, settings, now)
		if err != nil {
			problems = append(problems, fmt.Errorf("queue entry %s: %w", entry.ID, err))
		}
		if started {
			free--
			perProject[entry.ProjectID]++
		}
	}
	return errors.Join(problems...)
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
