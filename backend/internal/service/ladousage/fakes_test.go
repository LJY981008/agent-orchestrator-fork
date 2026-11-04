package ladousage

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// The real store has every read and write the service needs.
var _ Store = (*store.Store)(nil)

type fakeStore struct {
	mu            sync.Mutex
	projects      map[string]bool
	tasks         map[domain.LadoTaskID]domain.LadoTask
	facts         map[domain.LadoTaskID]domain.LadoTaskTimingFacts
	planWorkers   map[domain.LadoTaskID][]domain.LadoPlanWorkerSession
	sessions      map[domain.SessionID]domain.SessionRecord
	conversations map[domain.SessionID]domain.ConversationRecord
	subagents     map[domain.SessionID]domain.LadoSubagentUsage
	watches       map[domain.SessionID]domain.LadoSessionWatch
	sources       map[domain.SessionID][]domain.LadoTranscriptSource
	compactWrites int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		projects:      map[string]bool{},
		tasks:         map[domain.LadoTaskID]domain.LadoTask{},
		facts:         map[domain.LadoTaskID]domain.LadoTaskTimingFacts{},
		planWorkers:   map[domain.LadoTaskID][]domain.LadoPlanWorkerSession{},
		sessions:      map[domain.SessionID]domain.SessionRecord{},
		conversations: map[domain.SessionID]domain.ConversationRecord{},
		subagents:     map[domain.SessionID]domain.LadoSubagentUsage{},
		watches:       map[domain.SessionID]domain.LadoSessionWatch{},
		sources:       map[domain.SessionID][]domain.LadoTranscriptSource{},
	}
}

func (f *fakeStore) LadoTask(_ context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error) {
	t, ok := f.tasks[id]
	return t, ok, nil
}

func (f *fakeStore) ListLadoTasks(_ context.Context, project domain.ProjectID, includeCancelled bool) ([]domain.LadoTask, error) {
	var out []domain.LadoTask
	for _, t := range f.tasks {
		if t.ProjectID == project && (includeCancelled || t.CancelledAt.IsZero()) {
			out = append(out, t)
		}
	}
	sortTasks(out)
	return out, nil
}

func (f *fakeStore) ListLadoTasksToObserve(context.Context) ([]domain.LadoTask, error) {
	var out []domain.LadoTask
	for _, t := range f.tasks {
		if t.CancelledAt.IsZero() && t.IssueRef != "" {
			out = append(out, t)
		}
	}
	sortTasks(out)
	return out, nil
}

func sortTasks(tasks []domain.LadoTask) {
	for i := range tasks {
		for j := i + 1; j < len(tasks); j++ {
			if tasks[j].CreatedAt.Before(tasks[i].CreatedAt) {
				tasks[i], tasks[j] = tasks[j], tasks[i]
			}
		}
	}
}

func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	return domain.ProjectRecord{}, f.projects[id], nil
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r, ok := f.sessions[id]
	return r, ok, nil
}

func (f *fakeStore) ConversationForSession(_ context.Context, id domain.SessionID) (domain.ConversationRecord, error) {
	c, ok := f.conversations[id]
	if !ok {
		return domain.ConversationRecord{}, store.ErrConversationNotFound
	}
	return c, nil
}

func (f *fakeStore) LadoTaskTimingFacts(_ context.Context, id domain.LadoTaskID) (domain.LadoTaskTimingFacts, bool, error) {
	facts, ok := f.facts[id]
	return facts, ok, nil
}

func (f *fakeStore) ListLadoPlanWorkerSessions(_ context.Context, id domain.LadoTaskID) ([]domain.LadoPlanWorkerSession, error) {
	return append([]domain.LadoPlanWorkerSession(nil), f.planWorkers[id]...), nil
}

func (f *fakeStore) LadoSessionSubagentUsage(_ context.Context, id domain.SessionID) (domain.LadoSubagentUsage, error) {
	return f.subagents[id], nil
}

func (f *fakeStore) ListLadoSessionWatch(_ context.Context, task domain.LadoTaskID) ([]domain.LadoSessionWatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LadoSessionWatch
	for _, w := range f.watches {
		if w.TaskID == task {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *fakeStore) LadoSessionWatch(_ context.Context, id domain.SessionID) (domain.LadoSessionWatch, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.watches[id]
	w.ReadOffsets = maps.Clone(w.ReadOffsets)
	return w, ok, nil
}

func (f *fakeStore) EnsureLadoSessionWatch(_ context.Context, id domain.SessionID, task domain.LadoTaskID, at time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.watches[id]; ok {
		return false, nil
	}
	f.watches[id] = domain.LadoSessionWatch{
		SessionID: id, TaskID: task, CreatedAt: at, UpdatedAt: at,
		LadoWatchCompactions: domain.LadoWatchCompactions{ReadOffsets: map[string]int64{}},
	}
	return true, nil
}

func (f *fakeStore) SetLadoSessionWatchCompactions(_ context.Context, id domain.SessionID, c domain.LadoWatchCompactions, at time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.watches[id]
	if !ok {
		return false, nil
	}
	c.ReadOffsets = maps.Clone(c.ReadOffsets)
	w.LadoWatchCompactions, w.UpdatedAt = c, at
	f.watches[id] = w
	f.compactWrites++
	return true, nil
}

func (f *fakeStore) ListLadoSessionTranscriptSources(_ context.Context, id domain.SessionID) ([]domain.LadoTranscriptSource, error) {
	return f.sources[id], nil
}

// fakeUsage answers each session's usage summary; a session without one is
// unknown to AO.
type fakeUsage map[domain.SessionID]domain.SessionUsageSummary

func (u fakeUsage) Get(_ context.Context, id domain.SessionID) (domain.SessionUsageSummary, error) {
	s, ok := u[id]
	if !ok {
		return domain.SessionUsageSummary{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s, nil
}

func usageOf(costNanos, input, cached, output int64) domain.SessionUsageSummary {
	return domain.SessionUsageSummary{Totals: domain.UsageMetricTotals{
		InputTokens: &input, CachedInputTokens: &cached, OutputTokens: &output,
		EstimatedCost: &domain.EstimatedCost{TotalNanos: costNanos},
	}}
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
