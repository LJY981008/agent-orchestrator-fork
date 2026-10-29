package ladoqueue

// What the app reads about the queue and its executions, and the dispatch
// settings it changes (docs/LJY-PRD/TDD.md §5.3 and §13).

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

// ExecutionView is what the app shows of one execution: the run, the task
// and the plan revision it runs, when that revision was approved, and the
// rule snapshot the approval fixed.
type ExecutionView struct {
	Execution domain.LadoExecution
	Task      domain.LadoTask
	Revision  domain.LadoPlanRevision
	Approval  domain.LadoPlanApproval
	// SnapshotSHA256 is the rule snapshot's hash and Snapshot its content.
	SnapshotSHA256 string
	Snapshot       ladopolicy.SnapshotContent
}

// Queue returns the open queue entries of a project, or of every project
// for a nil projectID, in the order the dispatcher takes them.
func (s *Service) Queue(ctx context.Context, projectID *domain.ProjectID) ([]domain.LadoQueueItem, error) {
	items, err := s.deps.Store.ListLadoQueue(ctx, projectID)
	if err != nil {
		s.deps.Log.Error("LADO could not read its queue", "err", err)
		return nil, apierr.Internal("LADO_QUEUE_LOAD_FAILED", "Failed to load the queue")
	}
	return items, nil
}

// Execution returns the execution a session runs. A session that is not an
// execution answers LADO_EXECUTION_NOT_FOUND.
func (s *Service) Execution(ctx context.Context, sessionID domain.SessionID) (ExecutionView, error) {
	exec, ok, err := s.deps.Store.LadoExecution(ctx, sessionID)
	if err != nil {
		return ExecutionView{}, s.executionLoadFailed(sessionID, err)
	}
	if !ok {
		return ExecutionView{}, apierr.NotFound("LADO_EXECUTION_NOT_FOUND", "This session is not a LADO execution")
	}
	view := ExecutionView{Execution: exec}
	// The execution's task, revision, approval and snapshot are kept as long
	// as the execution, so one that is missing is a damaged store.
	task, ok, err := s.deps.Store.LadoTask(ctx, exec.TaskID)
	if err != nil || !ok {
		return ExecutionView{}, s.executionLoadFailed(sessionID, missing(err, "task"))
	}
	view.Task = task
	rev, ok, err := s.deps.Store.LadoPlanRevision(ctx, exec.PlanRevisionID)
	if err != nil || !ok {
		return ExecutionView{}, s.executionLoadFailed(sessionID, missing(err, "plan revision"))
	}
	view.Revision = rev
	approvals, err := s.deps.Store.ListLadoPlanApprovals(ctx, exec.TaskID)
	if err != nil {
		return ExecutionView{}, s.executionLoadFailed(sessionID, err)
	}
	for _, approval := range approvals {
		if approval.ID == exec.PlanApprovalID {
			view.Approval = approval
		}
	}
	if view.Approval.ID == "" {
		return ExecutionView{}, s.executionLoadFailed(sessionID, missing(nil, "approval"))
	}
	snapshot, ok, err := s.deps.Store.LadoPolicySnapshot(ctx, exec.PlanApprovalID)
	if err != nil || !ok {
		return ExecutionView{}, s.executionLoadFailed(sessionID, missing(err, "rule snapshot"))
	}
	if err := json.Unmarshal(snapshot.Content, &view.Snapshot); err != nil {
		return ExecutionView{}, s.executionLoadFailed(sessionID, err)
	}
	view.SnapshotSHA256 = snapshot.SHA256
	return view, nil
}

func (s *Service) executionLoadFailed(sessionID domain.SessionID, err error) error {
	s.deps.Log.Error("LADO could not read an execution", "session", sessionID, "err", err)
	return apierr.Internal("LADO_EXECUTION_LOAD_FAILED", "Failed to load the execution")
}

// missing returns err, or that the execution's record named what is gone.
func missing(err error, what string) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("the execution's %s is missing", what)
}

// Settings returns the dispatch settings, or the defaults while none are
// stored.
func (s *Service) Settings(ctx context.Context) (domain.LadoDispatchSettings, error) {
	settings, err := s.deps.Store.LadoDispatchSettings(ctx)
	if err != nil {
		s.deps.Log.Error("LADO could not read its dispatch settings", "err", err)
		return domain.LadoDispatchSettings{}, apierr.Internal("LADO_DISPATCH_SETTINGS_LOAD_FAILED", "Failed to load the dispatch settings")
	}
	return settings, nil
}

// SetSettings replaces the dispatch settings and returns them as stored. The
// next dispatch pass uses them; running executions keep their deadlines.
// A zero MaxPlanWorkers keeps the stored plan request cap, and a zero
// PlanReview.MaxPaths the stored review skip settings, so a caller that knows
// only the executions' settings changes nothing else. Nothing verifies who
// sent the change, so its audit event's actor is "api". Sending the current
// settings changes nothing.
func (s *Service) SetSettings(ctx context.Context, settings domain.LadoDispatchSettings) (domain.LadoDispatchSettings, error) {
	current, err := s.Settings(ctx)
	if err != nil {
		return domain.LadoDispatchSettings{}, err
	}
	if settings.MaxPlanWorkers == 0 {
		settings.MaxPlanWorkers = current.MaxPlanWorkers
	}
	if settings.PlanReview.MaxPaths == 0 {
		settings.PlanReview = current.PlanReview
	} else if settings.PlanReview.Patterns == nil {
		settings.PlanReview.Patterns = current.PlanReview.Patterns
	}
	// The worker cap and the disk threshold keep their values when the
	// change leaves them out.
	if settings.MaxWorkers == 0 {
		settings.MaxWorkers = current.MaxWorkers
	}
	if settings.MinFreeDiskGB == 0 {
		settings.HoldOnDiskLow, settings.MinFreeDiskGB = current.HoldOnDiskLow, current.MinFreeDiskGB
	}
	if err := settings.Validate(); err != nil {
		return domain.LadoDispatchSettings{}, InvalidSettings(strings.TrimPrefix(err.Error(), domain.ErrLadoDispatchSettingsInvalid.Error()+": "))
	}
	if sameSettings(current, settings) {
		return current, nil
	}
	now := s.deps.Clock().UTC()
	payload, _ := json.Marshal(map[string]any{"from": settingsPayload(current), "to": settingsPayload(settings)})
	stored, err := s.deps.Store.SetLadoDispatchSettings(ctx, settings, domain.LadoAuditEvent{
		At:          now,
		Actor:       "api",
		Action:      "dispatch.settings_changed",
		SubjectKind: "dispatch_settings",
		SubjectID:   "global",
		Payload:     payload,
	})
	if err != nil {
		s.deps.Log.Error("LADO could not store its dispatch settings", "err", err)
		return domain.LadoDispatchSettings{}, apierr.Internal("LADO_DISPATCH_SETTINGS_UPDATE_FAILED", "Failed to update the dispatch settings")
	}
	return stored, nil
}

// InvalidSettings is the LADO_DISPATCH_SETTINGS_INVALID error, with the
// ranges the settings take.
func InvalidSettings(message string) error {
	return apierr.Invalid("LADO_DISPATCH_SETTINGS_INVALID", message, map[string]any{
		"min": 1, "max": domain.LadoMaxConcurrent, "runMinutes": domain.LadoRunMinuteChoices,
	})
}

func sameSettings(a, b domain.LadoDispatchSettings) bool {
	return a.MaxConcurrent == b.MaxConcurrent && a.MaxRunMinutes == b.MaxRunMinutes && a.HoldOnMemoryTight == b.HoldOnMemoryTight &&
		a.MaxPlanWorkers == b.MaxPlanWorkers && a.PlanReview.Skip == b.PlanReview.Skip && a.PlanReview.MaxPaths == b.PlanReview.MaxPaths &&
		reflect.DeepEqual(a.PlanReview.Patterns, b.PlanReview.Patterns) &&
		a.MaxWorkers == b.MaxWorkers && a.HoldOnDiskLow == b.HoldOnDiskLow && a.MinFreeDiskGB == b.MinFreeDiskGB
}

func settingsPayload(s domain.LadoDispatchSettings) map[string]any {
	return map[string]any{
		"maxConcurrent": s.MaxConcurrent, "maxRunMinutes": s.MaxRunMinutes, "holdOnMemoryTight": s.HoldOnMemoryTight,
		"maxPlanWorkers": s.MaxPlanWorkers, "planReviewSkip": s.PlanReview.Skip, "planReviewSkipMaxPaths": s.PlanReview.MaxPaths,
		"planReviewPatterns": s.PlanReview.Patterns,
		"maxWorkers":         s.MaxWorkers, "holdOnDiskLow": s.HoldOnDiskLow, "minFreeDiskGb": s.MinFreeDiskGB,
	}
}
