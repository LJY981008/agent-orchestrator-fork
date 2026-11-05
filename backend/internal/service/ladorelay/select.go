package ladorelay

// Picking the task and the worker of a relay (plan decision 5). A task is
// named by its id or by its issue, and only one of the orchestrator's own
// project. The worker is an execution of the task (single, part, merge or
// integration) whose session still runs and that LADO has not timed out or
// settled; a plan worker is not an execution. LADO never guesses between
// live workers: a message to the wrong part cannot be taken back.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// resolveTask finds the task ref names in project.
func (s *Service) resolveTask(ctx context.Context, project domain.ProjectID, ref string) (domain.LadoTask, error) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "lado-task-") {
		task, ok, err := s.deps.Store.LadoTask(ctx, domain.LadoTaskID(ref))
		if err != nil {
			return domain.LadoTask{}, fmt.Errorf("read LADO task %s: %w", ref, err)
		}
		if !ok {
			return domain.LadoTask{}, taskNotFound(ref)
		}
		if task.ProjectID != project {
			return domain.LadoTask{}, apierr.New(apierr.KindForbidden, domain.LadoRelayWrongProject,
				fmt.Sprintf("Task %s belongs to another project; an orchestrator relays only to its own project's workers.", ref),
				map[string]any{"taskId": ref, "projectId": string(project)})
		}
		return task, nil
	}
	match, err := issueMatcher(ref)
	if err != nil {
		return domain.LadoTask{}, err
	}
	tasks, err := s.deps.Store.ListLadoTasks(ctx, project, true)
	if err != nil {
		return domain.LadoTask{}, fmt.Errorf("list LADO tasks of project %s: %w", project, err)
	}
	var found []domain.LadoTask
	for _, task := range tasks {
		if match(task.IssueRef) {
			found = append(found, task)
		}
	}
	if len(found) > 1 {
		// A cancelled task of the issue does not compete with a live one.
		var live []domain.LadoTask
		for _, task := range found {
			if task.CancelledAt.IsZero() {
				live = append(live, task)
			}
		}
		if len(live) > 0 {
			found = live
		}
	}
	switch len(found) {
	case 0:
		return domain.LadoTask{}, taskNotFound(ref)
	case 1:
		return found[0], nil
	}
	candidates := make([]map[string]any, 0, len(found))
	for _, task := range found {
		candidates = append(candidates, map[string]any{"taskId": string(task.ID), "issue": string(task.IssueRef)})
	}
	return domain.LadoTask{}, apierr.Conflict(domain.LadoRelayTaskAmbiguous,
		fmt.Sprintf("Issue %s names more than one task of this project; name the task by its id.", ref),
		map[string]any{"candidates": candidates})
}

// issueMatcher reads ref as an issue number (#49 or 49) or a canonical issue
// id and returns what matches a task's issue.
func issueMatcher(ref string) (func(domain.IssueID) bool, error) {
	if strings.Contains(ref, ":") {
		return func(id domain.IssueID) bool { return string(id) == ref }, nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(ref, "#"))
	if err != nil || n <= 0 {
		return nil, apierr.Invalid(domain.LadoRelayInvalid,
			fmt.Sprintf("%q names no task: give a task id (lado-task-…) or an issue number (#49).", ref),
			map[string]any{"field": "task"})
	}
	return func(id domain.IssueID) bool {
		tracker, err := domain.ParseLadoIssueRef(id)
		return err == nil && domain.LadoIssueNumber(tracker) == n
	}, nil
}

func taskNotFound(ref string) error {
	return apierr.New(apierr.KindNotFound, domain.LadoRelayTaskNotFound,
		fmt.Sprintf("This project has no LADO task %s.", ref), map[string]any{"task": ref})
}

// pickWorker returns the live execution of task that the relay goes to:
// named, or the only one.
func (s *Service) pickWorker(ctx context.Context, task domain.LadoTask, named domain.SessionID) (domain.LadoExecution, error) {
	if !task.CancelledAt.IsZero() {
		return domain.LadoExecution{}, noWorker(task.ID, domain.LadoRelayNoWorkerCancelled, "")
	}
	executions, err := s.deps.Store.LadoRelayExecutions(ctx, task.ID)
	if err != nil {
		return domain.LadoExecution{}, fmt.Errorf("read executions of task %s: %w", task.ID, err)
	}
	if named != "" {
		for _, e := range executions {
			if e.Execution.SessionID != named {
				continue
			}
			if reason := gone(e); reason != "" {
				return domain.LadoExecution{}, noWorker(task.ID, reason, named)
			}
			return e.Execution, nil
		}
		return domain.LadoExecution{}, apierr.Conflict(domain.LadoRelayNotExecution,
			fmt.Sprintf("Session %s is not a worker LADO started to carry out task %s; a plan worker or another session takes no relay.", named, task.ID),
			map[string]any{"taskId": string(task.ID), "sessionId": string(named)})
	}
	var live []domain.LadoExecution
	for _, e := range executions {
		if gone(e) == "" {
			live = append(live, e.Execution)
		}
	}
	switch len(live) {
	case 0:
		reason := domain.LadoRelayNoWorkerNone
		if len(executions) > 0 {
			// Executions come newest first.
			reason = gone(executions[0])
		}
		return domain.LadoExecution{}, noWorker(task.ID, reason, "")
	case 1:
		return live[0], nil
	}
	candidates := make([]map[string]any, 0, len(live))
	for _, e := range live {
		candidates = append(candidates, map[string]any{
			"sessionId": string(e.SessionID), "role": roleName(e.Role), "partId": e.PartID,
		})
	}
	return domain.LadoExecution{}, apierr.Conflict(domain.LadoRelayAmbiguous,
		fmt.Sprintf("Task %s has %d live workers; name one with --session.", task.ID, len(live)),
		map[string]any{"taskId": string(task.ID), "candidates": candidates})
}

// gone is why an execution takes no more messages, "" while it does. A
// settled or timed out execution says more than its ended session.
func gone(e domain.LadoRelayExecution) string {
	switch {
	case !e.Execution.SettledAt.IsZero():
		return domain.LadoRelayNoWorkerSettled
	case !e.Execution.TimedOutAt.IsZero():
		return domain.LadoRelayNoWorkerTimedOut
	case e.Execution.SessionTerminated:
		return domain.LadoRelayNoWorkerEnded
	case e.SessionActivity == domain.ActivityExited:
		return domain.LadoRelayNoWorkerExited
	}
	return ""
}

// noWorkerText says why no worker takes the relay.
var noWorkerText = map[string]string{
	domain.LadoRelayNoWorkerEnded:     "its worker's session has ended",
	domain.LadoRelayNoWorkerExited:    "its worker's agent has exited",
	domain.LadoRelayNoWorkerTimedOut:  "LADO stopped its worker at the time limit",
	domain.LadoRelayNoWorkerSettled:   "its pull request was merged or closed and verification settled it",
	domain.LadoRelayNoWorkerCancelled: "the task was cancelled",
	domain.LadoRelayNoWorkerNone:      "no worker has started on it",
}

func noWorker(task domain.LadoTaskID, reason string, session domain.SessionID) error {
	details := map[string]any{"taskId": string(task), "reason": reason}
	if session != "" {
		details["sessionId"] = string(session)
	}
	return apierr.Conflict(domain.LadoRelayNoWorker,
		fmt.Sprintf("Task %s has no live worker to relay to: %s. Going on needs a new task or plan request.", task, noWorkerText[reason]),
		details)
}

// roleName names an execution's role; the single worker's role is "".
func roleName(role string) string {
	if role == domain.LadoRoleSingle {
		return "single"
	}
	return role
}
