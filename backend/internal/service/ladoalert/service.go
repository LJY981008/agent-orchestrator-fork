// Package ladoalert raises LADO's usage alerts and serves the watch settings
// and the alerts (issue #42, plan steps 16 and 17).
//
// The daemon's one-minute watch loop runs Tick after ladowatch.Tick. Tick
// reads each open task's usage (ladousage) and raises a one-line alert when
// the task's sessions together, or one session, cost more than the project's
// threshold, and once more past twice it; and when one session compacted as
// often as the project's compaction threshold. A task's own alert has an
// empty session. The store raises each (task, session, kind, level) once, so
// a threshold read again on the next tick adds nothing. ladowatch raises the
// limit and stall alerts itself; ladonotify moves every new alert to the
// orchestrator's notices.
package ladoalert

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladousage"
)

// nanosPerCent turns AO's cost estimate, in nanodollars, into cents.
const nanosPerCent = 10_000_000

// Store reads the tasks to watch and the settings, and keeps the alerts
// (*store.Store).
type Store interface {
	ListLadoTasksToObserve(ctx context.Context) ([]domain.LadoTask, error)
	LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)

	LadoWatchSettings(ctx context.Context, scope domain.ProjectID) (domain.LadoWatchOverrides, bool, error)
	PutLadoWatchSettings(ctx context.Context, scope domain.ProjectID, o domain.LadoWatchOverrides) error
	LadoEffectiveWatchSettings(ctx context.Context, project domain.ProjectID) (domain.LadoWatchSettings, error)

	AddLadoAlert(ctx context.Context, a domain.LadoAlert) (domain.LadoAlertID, bool, error)
	LadoAlert(ctx context.Context, id domain.LadoAlertID) (domain.LadoAlert, bool, error)
	MarkLadoAlertSeen(ctx context.Context, id domain.LadoAlertID, at time.Time) (bool, error)
	ListLadoAlertsByProject(ctx context.Context, project domain.ProjectID, unseenOnly bool, limit int) ([]domain.LadoAlert, error)
	ListLadoAlertsByTask(ctx context.Context, task domain.LadoTaskID) ([]domain.LadoAlert, error)
}

// Usage adds up a task's usage (*ladousage.Service).
type Usage interface {
	TaskUsage(ctx context.Context, id domain.LadoTaskID) (ladousage.TaskUsage, error)
}

// Deps wires a Service.
type Deps struct {
	Store Store
	// Usage reads the tasks' usage; nil raises no usage alert, and the API
	// still serves the settings and the alerts.
	Usage Usage
	// Clock stamps alerts; nil uses time.Now.
	Clock func() time.Time
	Log   *slog.Logger
}

// Service raises the usage alerts and serves the settings and the alerts.
type Service struct {
	deps Deps

	mu sync.Mutex
	// merged holds the tasks this process evaluated once after their merge;
	// a merged task spends nothing more, so later ticks skip it.
	merged map[domain.LadoTaskID]bool
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Service{deps: deps, merged: map[domain.LadoTaskID]bool{}}
}

// Tick raises the usage alerts of every task that is not cancelled. A merged
// task is evaluated once more, then left alone. A task that cannot be read is
// logged and left for the next tick; the error is the context's or a failure
// to list the tasks.
func (s *Service) Tick(ctx context.Context) error {
	if s.deps.Usage == nil {
		return nil
	}
	tasks, err := s.deps.Store.ListLadoTasksToObserve(ctx)
	if err != nil {
		return err
	}
	settings := map[domain.ProjectID]domain.LadoWatchSettings{}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		done := s.merged[task.ID]
		s.mu.Unlock()
		if done {
			continue
		}
		set, ok := settings[task.ProjectID]
		if !ok {
			if set, err = s.deps.Store.LadoEffectiveWatchSettings(ctx, task.ProjectID); err != nil {
				s.deps.Log.Warn("LADO could not read a project's watch settings", "project", task.ProjectID, "err", err)
				continue
			}
			settings[task.ProjectID] = set
		}
		usage, err := s.deps.Usage.TaskUsage(ctx, task.ID)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindNotFound {
				s.deps.Log.Warn("LADO could not read a task's usage for its alerts", "task", task.ID, "err", err)
			}
			continue
		}
		s.evaluate(ctx, usage, set)
		if usage.EndReason == ladousage.EndMerged {
			s.mu.Lock()
			s.merged[task.ID] = true
			s.mu.Unlock()
		}
	}
	return nil
}

// evaluate raises the alerts a task's usage crosses under settings.
func (s *Service) evaluate(ctx context.Context, usage ladousage.TaskUsage, settings domain.LadoWatchSettings) {
	if level, ok := costLevel(usage.CostNanos, settings.TaskCostCents); ok {
		s.raise(ctx, domain.LadoAlert{
			TaskID: usage.TaskID, Kind: domain.LadoAlertTaskCost, Level: level,
			Value: usage.CostNanos / nanosPerCent, Threshold: settings.TaskCostCents,
		})
	}
	for _, session := range usage.Sessions {
		if session.SessionID == "" || session.UsageMissing {
			continue
		}
		if level, ok := costLevel(session.CostNanos, settings.SessionCostCents); ok {
			s.raise(ctx, domain.LadoAlert{
				TaskID: usage.TaskID, SessionID: session.SessionID, Kind: domain.LadoAlertSessionCost, Level: level,
				Value: session.CostNanos / nanosPerCent, Threshold: settings.SessionCostCents,
			})
		}
		if settings.SessionCompactions > 0 && session.Compactions >= settings.SessionCompactions {
			s.raise(ctx, domain.LadoAlert{
				TaskID: usage.TaskID, SessionID: session.SessionID, Kind: domain.LadoAlertSessionCompactions,
				Level: domain.LadoAlertLevelCrossed, Value: int64(session.Compactions), Threshold: int64(settings.SessionCompactions),
			})
		}
	}
}

// costLevel is the alert level a cost reaches against a threshold in cents:
// LadoAlertLevelCrossed past the threshold, LadoAlertLevelDoubled past twice
// it. ok is false under the threshold. A cost that jumps past twice the
// threshold raises only the doubled alert; it never falls back under it, so
// the crossed alert is not raised after it.
func costLevel(costNanos, thresholdCents int64) (int, bool) {
	if thresholdCents <= 0 {
		return 0, false
	}
	threshold := thresholdCents * nanosPerCent
	switch {
	case costNanos > 2*threshold:
		return domain.LadoAlertLevelDoubled, true
	case costNanos > threshold:
		return domain.LadoAlertLevelCrossed, true
	}
	return 0, false
}

// raise adds an alert once; one raised before adds nothing.
func (s *Service) raise(ctx context.Context, a domain.LadoAlert) {
	a.CreatedAt = s.deps.Clock().UTC()
	id, added, err := s.deps.Store.AddLadoAlert(context.WithoutCancel(ctx), a)
	if err != nil {
		s.deps.Log.Warn("LADO could not raise an alert", "task", a.TaskID, "session", a.SessionID, "kind", a.Kind, "level", a.Level, "err", err)
		return
	}
	if added {
		s.deps.Log.Info("LADO raised an alert", "alert", id, "task", a.TaskID, "session", a.SessionID, "kind", a.Kind, "level", a.Level)
	}
}

// Line is the alert's one line, with its task's issue number.
func Line(a domain.LadoAlert) string {
	return a.Line(IssueNumber(a.IssueRef))
}

// IssueNumber is the number of a canonical issue id, 0 when it has none.
func IssueNumber(ref domain.IssueID) int {
	id, err := domain.ParseLadoIssueRef(ref)
	if err != nil {
		return 0
	}
	return domain.LadoIssueNumber(id)
}
