package store

// LADO fork: issue-backed tasks (docs/LJY-PRD/TDD.md §3.1 and §5.1).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CreateLadoTask stores a new task. A task without an issue ref is pending:
// LADO is creating its issue, and the task stays out of project lists until
// LinkLadoTaskIssue. A second live task for one issue is refused with
// domain.ErrLadoIssueHasTask.
func (s *Store) CreateLadoTask(ctx context.Context, task domain.LadoTask) (domain.LadoTask, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoTask{}, err
	}
	defer s.writeMu.Unlock()
	row, err := s.qw.InsertLadoTask(ctx, gen.InsertLadoTaskParams{
		ID:                  string(task.ID),
		ProjectID:           string(task.ProjectID),
		IssueRepo:           domain.LadoRepoRef(task.Repo),
		IssueRef:            ladoNullString(string(task.IssueRef)),
		Agent:               task.Agent,
		Model:               task.Model,
		IssueTitle:          task.Issue.Title,
		IssueState:          string(task.Issue.State),
		IssueURL:            task.Issue.URL,
		IssueSnapshotSha256: task.Issue.SnapshotSHA256,
		IssueObservedAt:     ladoNullTime(task.Issue.ObservedAt),
		CreatedAt:           task.CreatedAt.UTC(),
		UpdatedAt:           task.UpdatedAt.UTC(),
	})
	if isSQLiteUnique(err) {
		return domain.LadoTask{}, domain.ErrLadoIssueHasTask
	}
	if err != nil {
		return domain.LadoTask{}, fmt.Errorf("create LADO task: %w", err)
	}
	return ladoTaskFromGen(row), nil
}

// LadoTask returns one task, pending or not.
func (s *Store) LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error) {
	row, err := s.qr.GetLadoTask(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoTask{}, false, nil
	}
	if err != nil {
		return domain.LadoTask{}, false, fmt.Errorf("read LADO task: %w", err)
	}
	task := ladoTaskFromGen(row)
	if task.Plan, err = s.ladoTaskPlan(ctx, task.ID); err != nil {
		return domain.LadoTask{}, false, err
	}
	return task, true, nil
}

// ListLadoTasks returns a project's tasks that have an issue, oldest first.
func (s *Store) ListLadoTasks(ctx context.Context, projectID domain.ProjectID, includeCancelled bool) ([]domain.LadoTask, error) {
	rows, err := s.qr.ListLadoTasksByProject(ctx, string(projectID))
	if err != nil {
		return nil, fmt.Errorf("list LADO tasks: %w", err)
	}
	plans, err := s.ladoProjectPlans(ctx, projectID)
	if err != nil {
		return nil, err
	}
	tasks := make([]domain.LadoTask, 0, len(rows))
	for _, row := range rows {
		task := ladoTaskFromGen(row)
		if !includeCancelled && !task.CancelledAt.IsZero() {
			continue
		}
		task.Plan = plans[task.ID]
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// ListLiveLadoTasks returns every project's tasks that have an issue and are
// not cancelled, with their plan facts, by project and oldest first.
func (s *Store) ListLiveLadoTasks(ctx context.Context) ([]domain.LadoTask, error) {
	rows, err := s.qr.ListLiveLadoTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list live LADO tasks: %w", err)
	}
	plans := map[domain.ProjectID]map[domain.LadoTaskID]domain.LadoTaskPlan{}
	tasks := make([]domain.LadoTask, 0, len(rows))
	for _, row := range rows {
		task := ladoTaskFromGen(row)
		projectPlans, ok := plans[task.ProjectID]
		if !ok {
			if projectPlans, err = s.ladoProjectPlans(ctx, task.ProjectID); err != nil {
				return nil, err
			}
			plans[task.ProjectID] = projectPlans
		}
		task.Plan = projectPlans[task.ID]
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// ListPendingLadoTasks returns the tasks whose issue LADO has not linked yet.
func (s *Store) ListPendingLadoTasks(ctx context.Context) ([]domain.LadoTask, error) {
	rows, err := s.qr.ListPendingLadoTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending LADO tasks: %w", err)
	}
	return ladoTasksFromGen(rows), nil
}

// ListLadoTasksToObserve returns the live tasks, least recently observed
// first.
func (s *Store) ListLadoTasksToObserve(ctx context.Context) ([]domain.LadoTask, error) {
	rows, err := s.qr.ListLadoTasksToObserve(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO tasks to observe: %w", err)
	}
	return ladoTasksFromGen(rows), nil
}

// LiveLadoTaskIssueRefs returns the issue refs of every task that is not
// cancelled.
func (s *Store) LiveLadoTaskIssueRefs(ctx context.Context) (map[domain.IssueID]bool, error) {
	rows, err := s.qr.ListLiveLadoTaskIssueRefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list live LADO task issues: %w", err)
	}
	refs := make(map[domain.IssueID]bool, len(rows))
	for _, ref := range rows {
		if ref.Valid {
			refs[domain.IssueID(ref.String)] = true
		}
	}
	return refs, nil
}

// LinkLadoTaskIssue records the issue LADO created for a pending task. It
// fails when the task is not pending.
func (s *Store) LinkLadoTaskIssue(ctx context.Context, id domain.LadoTaskID, ref domain.IssueID, issue domain.LadoObservedIssue, at time.Time) (domain.LadoTask, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoTask{}, err
	}
	defer s.writeMu.Unlock()
	row, err := s.qw.LinkLadoTaskIssue(ctx, gen.LinkLadoTaskIssueParams{
		IssueRef:            ladoNullString(string(ref)),
		IssueTitle:          issue.Title,
		IssueState:          string(issue.State),
		IssueURL:            issue.URL,
		IssueSnapshotSha256: issue.SnapshotSHA256,
		IssueObservedAt:     ladoNullTime(issue.ObservedAt),
		UpdatedAt:           at.UTC(),
		ID:                  string(id),
	})
	if isSQLiteUnique(err) {
		return domain.LadoTask{}, domain.ErrLadoIssueHasTask
	}
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoTask{}, fmt.Errorf("link LADO task %s: the task is not waiting for an issue", id)
	}
	if err != nil {
		return domain.LadoTask{}, fmt.Errorf("link LADO task issue: %w", err)
	}
	return ladoTaskFromGen(row), nil
}

// ObserveLadoTaskIssue records what LADO last read from a task's issue.
func (s *Store) ObserveLadoTaskIssue(ctx context.Context, id domain.LadoTaskID, issue domain.LadoObservedIssue, at time.Time) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if _, err := s.qw.ObserveLadoTaskIssue(ctx, gen.ObserveLadoTaskIssueParams{
		IssueTitle:          issue.Title,
		IssueState:          string(issue.State),
		IssueURL:            issue.URL,
		IssueSnapshotSha256: issue.SnapshotSHA256,
		IssueObservedAt:     ladoNullTime(issue.ObservedAt),
		UpdatedAt:           at.UTC(),
		ID:                  string(id),
	}); err != nil {
		return fmt.Errorf("observe LADO task issue: %w", err)
	}
	return nil
}

// SetLadoTaskPreferences changes a task's agent and model. It reports false
// for an unknown or pending task.
func (s *Store) SetLadoTaskPreferences(ctx context.Context, id domain.LadoTaskID, agent, model string, at time.Time) (domain.LadoTask, bool, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoTask{}, false, err
	}
	defer s.writeMu.Unlock()
	row, err := s.qw.SetLadoTaskPreferences(ctx, gen.SetLadoTaskPreferencesParams{
		Agent:     agent,
		Model:     model,
		UpdatedAt: at.UTC(),
		ID:        string(id),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoTask{}, false, nil
	}
	if err != nil {
		return domain.LadoTask{}, false, fmt.Errorf("set LADO task preferences: %w", err)
	}
	task := ladoTaskFromGen(row)
	if task.Plan, err = s.ladoTaskPlan(ctx, task.ID); err != nil {
		return domain.LadoTask{}, false, err
	}
	return task, true, nil
}

// CancelLadoTask cancels a task. Cancelling a cancelled task keeps the first
// cancellation. It reports false for an unknown or pending task.
func (s *Store) CancelLadoTask(ctx context.Context, id domain.LadoTaskID, at time.Time) (domain.LadoTask, bool, error) {
	if err := s.cancelLadoTask(ctx, id, at); err != nil {
		return domain.LadoTask{}, false, err
	}
	task, ok, err := s.LadoTask(ctx, id)
	if err != nil || !ok || task.IssueRef == "" {
		return domain.LadoTask{}, false, err
	}
	return task, true, nil
}

func (s *Store) cancelLadoTask(ctx context.Context, id domain.LadoTaskID, at time.Time) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if _, err := s.qw.CancelLadoTask(ctx, gen.CancelLadoTaskParams{
		CancelledAt: ladoNullTime(at),
		UpdatedAt:   at.UTC(),
		ID:          string(id),
	}); err != nil {
		return fmt.Errorf("cancel LADO task: %w", err)
	}
	return nil
}

// DeletePendingLadoTask removes a task whose issue was never created. Tasks
// with an issue are never deleted.
func (s *Store) DeletePendingLadoTask(ctx context.Context, id domain.LadoTaskID) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if _, err := s.qw.DeletePendingLadoTask(ctx, string(id)); err != nil {
		return fmt.Errorf("delete pending LADO task: %w", err)
	}
	return nil
}

func ladoTasksFromGen(rows []gen.LadoTask) []domain.LadoTask {
	tasks := make([]domain.LadoTask, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, ladoTaskFromGen(row))
	}
	return tasks
}

func ladoTaskFromGen(row gen.LadoTask) domain.LadoTask {
	// The column only ever holds what LadoRepoRef wrote.
	repo, _ := domain.ParseLadoRepoRef(row.IssueRepo)
	return domain.LadoTask{
		ID:        domain.LadoTaskID(row.ID),
		ProjectID: domain.ProjectID(row.ProjectID),
		Repo:      repo,
		IssueRef:  domain.IssueID(row.IssueRef.String),
		Agent:     row.Agent,
		Model:     row.Model,
		Issue: domain.LadoObservedIssue{
			Title:          row.IssueTitle,
			State:          domain.NormalizedIssueState(row.IssueState),
			URL:            row.IssueURL,
			SnapshotSHA256: row.IssueSnapshotSha256,
			ObservedAt:     ladoTimeFromNull(row.IssueObservedAt),
		},
		CreatedAt:   row.CreatedAt.UTC(),
		UpdatedAt:   row.UpdatedAt.UTC(),
		CancelledAt: ladoTimeFromNull(row.CancelledAt),
	}
}

func ladoNullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func ladoNullTime(value time.Time) sql.NullTime {
	if value.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value.UTC(), Valid: true}
}

func ladoTimeFromNull(value sql.NullTime) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}
