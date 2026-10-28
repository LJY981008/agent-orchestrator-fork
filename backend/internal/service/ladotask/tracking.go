package ladotask

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	defaultRefreshInterval = 5 * time.Minute
	// readTimeout bounds one tracker call made by the tracking loop.
	readTimeout = 15 * time.Second
	// pendingGrace is how old a task without an issue must be before the
	// tracking loop resolves it, so it never races a request still in flight.
	pendingGrace = 2 * time.Minute
	// maxFailuresInARow ends a refresh pass early, so a tracker that is down
	// or rate limiting is not asked once per task.
	maxFailuresInARow = 3
)

// Start resolves tasks left without an issue by a stopped daemon, then reads
// the issue of every task on a timer. A second loop pins plan versions, runs
// the plan workers and delivers the requests made before them on a shorter
// timer, and at once when a request or a submission arrives, so a slow
// tracker never holds up plans. The returned channel closes once ctx is done
// and both loops have stopped.
func (s *Service) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	started := s.deps.Clock()
	var loops sync.WaitGroup
	loops.Add(1)
	go func() {
		defer loops.Done()
		s.ResolvePending(ctx, started)
		s.RefreshAll(ctx)
		ticker := time.NewTicker(s.deps.RefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.ResolvePending(ctx, s.deps.Clock().Add(-pendingGrace))
				s.RefreshAll(ctx)
			}
		}
	}()
	if s.deps.Orchestrators != nil && s.deps.Artifacts != nil && s.deps.Blobs != nil {
		loops.Add(1)
		go func() {
			defer loops.Done()
			ticker := time.NewTicker(s.deps.PlanInterval)
			defer ticker.Stop()
			for {
				s.WatchPlans(ctx)
				s.DrivePlanWorkers(ctx)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				case <-s.kick:
				}
			}
		}()
	}
	go func() {
		loops.Wait()
		close(done)
	}()
	return done
}

// ResolvePending finishes the tasks created before createdBefore that still
// have no issue: their request stopped between saving the task and saving
// its issue. A task whose marker is on one of the repository's newest issues
// gets that issue; any other is deleted, because its issue was never created.
// A task whose tracker cannot be reached waits for the next pass.
func (s *Service) ResolvePending(ctx context.Context, createdBefore time.Time) {
	pending, err := s.deps.Store.ListPendingLadoTasks(ctx)
	if err != nil {
		s.deps.Log.Warn("LADO could not list tasks without an issue", "err", err)
		return
	}
	for _, task := range pending {
		if ctx.Err() != nil {
			return
		}
		if !task.CreatedAt.Before(createdBefore) {
			continue
		}
		if err := s.resolvePending(ctx, task); err != nil {
			s.deps.Log.Warn("LADO could not resolve a task without an issue", "task", task.ID, "err", err)
		}
	}
}

func (s *Service) resolvePending(ctx context.Context, task domain.LadoTask) error {
	_, ok, err := s.deps.Projects.GetProject(ctx, string(task.ProjectID))
	if err != nil {
		return err
	}
	if !ok {
		s.deps.Log.Warn("LADO dropped a task without an issue whose project is gone", "task", task.ID, "project", task.ProjectID)
		return s.deps.Store.DeletePendingLadoTask(ctx, task.ID)
	}
	// The task records the repository its issue was being created in.
	repo := task.Repo
	client, err := s.deps.Clients.ForRepo(ctx, repo, "")
	if err != nil {
		return err
	}
	readCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	issue, found, err := findByMarker(readCtx, client, repo, task.ID)
	if err != nil {
		return err
	}
	if !found {
		s.deps.Log.Info("LADO dropped a task whose issue was never created", "task", task.ID)
		return s.deps.Store.DeletePendingLadoTask(ctx, task.ID)
	}
	if _, err := s.link(ctx, task, repo, issue); err != nil {
		return err
	}
	s.deps.Log.Info("LADO linked a task to the issue it created before a restart", "task", task.ID, "issue", domain.LadoIssueRef(issue.ID))
	return nil
}

// RefreshAll reads the issue of every task that is not cancelled.
func (s *Service) RefreshAll(ctx context.Context) {
	tasks, err := s.deps.Store.ListLadoTasksToObserve(ctx)
	if err != nil {
		s.deps.Log.Warn("LADO could not list tasks to refresh", "err", err)
		return
	}
	failures := 0
	for _, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		if err := s.observe(ctx, task); err != nil {
			s.deps.Log.Debug("LADO could not refresh a task's issue", "task", task.ID, "err", err)
			failures++
			if failures >= maxFailuresInARow {
				s.deps.Log.Warn("LADO paused issue refreshes after repeated failures", "err", err)
				return
			}
			continue
		}
		failures = 0
	}
}

// observe reads a task's issue and records what it found.
func (s *Service) observe(ctx context.Context, task domain.LadoTask) error {
	id, err := domain.ParseLadoIssueRef(task.IssueRef)
	if err != nil {
		return err
	}
	client, err := s.deps.Clients.ForRepo(ctx, domain.LadoIssueRepo(id), "")
	if err != nil {
		return err
	}
	readCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	issue, err := client.Get(readCtx, id)
	if err != nil {
		return fmt.Errorf("read %s: %w", task.IssueRef, err)
	}
	issue.ID = id
	now := s.deps.Clock().UTC()
	return s.deps.Store.ObserveLadoTaskIssue(ctx, task.ID, observedIssue(issue, now), now)
}
