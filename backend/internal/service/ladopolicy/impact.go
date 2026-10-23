package ladopolicy

// Change impact (docs/LJY-PRD/HARNESS.md §14): before LADO makes a rule
// change, and instead of making it on a dry run, it compares what the rules
// give each LADO project and each approved task now with what they would
// give after the change.

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Impact is what a rule change reaches.
type Impact struct {
	// Projects are the LADO projects whose rules change.
	Projects []ImpactProject
	// Tasks are the tasks whose approval matches the rules now and would
	// need a new one after the change.
	Tasks []ImpactTask
}

// ImpactProject is a LADO project a rule change reaches.
type ImpactProject struct {
	ID   domain.ProjectID
	Name string
}

// ImpactTask is an approved task a rule change would send back to review.
type ImpactTask struct {
	ID        domain.LadoTaskID
	ProjectID domain.ProjectID
	// Title is the issue's title as LADO last read it.
	Title    string
	IssueRef domain.IssueID
}

func noImpact() Impact {
	return Impact{Projects: []ImpactProject{}, Tasks: []ImpactTask{}}
}

// impact compares the rules before and after a change for every LADO project
// that is not archived, and for every live task of those projects whose
// valid approval is not dispatched yet and whose snapshot is what the rules
// give it now: a queued, dispatching, blocked or failed task, or one that
// needs review for another reason than the rules. A task whose rules changed
// already, whose issue closed, or whose approval's worker runs or ran (its
// execution keeps the snapshot) has nothing to lose. A task whose pinned plan
// cannot be read is left out and logged, so one missing pin does not stop
// rule changes.
func (s *Service) impact(ctx context.Context, before, after []domain.LadoRule) (Impact, error) {
	impact := noImpact()
	ids, err := s.deps.Store.ListLadoModeProjectIDs(ctx)
	if err != nil {
		return Impact{}, s.loadFailed(err)
	}
	projects := make(map[domain.ProjectID]projectScope, len(ids))
	for _, id := range ids {
		project, ok, err := s.deps.Projects.GetProject(ctx, string(id))
		if err != nil {
			return Impact{}, s.loadFailed(err)
		}
		if !ok || !project.ArchivedAt.IsZero() {
			continue
		}
		scope := projectScope{record: project, aoRules: ReadAORules(ctx, project)}
		projects[id] = scope
		in := s.projectInput(scope)
		if changes(in, before, after) {
			impact.Projects = append(impact.Projects, ImpactProject{ID: id, Name: projectName(project)})
		}
	}
	if len(projects) == 0 {
		return impact, nil
	}
	tasks, err := s.deps.Store.ListLiveLadoTasks(ctx)
	if err != nil {
		return Impact{}, s.loadFailed(err)
	}
	for _, task := range tasks {
		scope, ok := projects[task.ProjectID]
		if !ok || !task.Plan.ApprovalValid() {
			continue
		}
		in, err := s.taskInput(ctx, task, scope, task.Plan.Latest)
		if err != nil {
			s.deps.Log.Warn("LADO left a task out of a rule change's impact: its pinned plan is unreadable", "task", task.ID, "err", err)
			continue
		}
		in.Rules = before
		task.Plan.PolicySHA256 = Resolve(in).SHA256
		if task.Plan.RulesChanged() || !awaitsDispatch(task) {
			continue
		}
		in.Rules = after
		if Resolve(in).SHA256 == task.Plan.Snapshot.SHA256 {
			continue
		}
		impact.Tasks = append(impact.Tasks, ImpactTask{ID: task.ID, ProjectID: task.ProjectID, Title: task.Issue.Title, IssueRef: task.IssueRef})
	}
	return impact, nil
}

// awaitsDispatch reports whether the task's valid approval still waits for
// its worker: the task is not running, done or closed, and no execution of
// this approval exists, such as one that failed.
func awaitsDispatch(task domain.LadoTask) bool {
	switch task.State() {
	case domain.LadoTaskQueued, domain.LadoTaskDispatching, domain.LadoTaskBlocked, domain.LadoTaskFailed, domain.LadoTaskReviewRequired:
	default:
		return false
	}
	return task.Execution.SessionID == "" || task.Execution.PlanApprovalID != task.Plan.Approval.ID
}

// changes reports whether the rules after a change give in another snapshot
// than the rules before it.
func changes(in Input, before, after []domain.LadoRule) bool {
	in.Rules = before
	then := Resolve(in).SHA256
	in.Rules = after
	return Resolve(in).SHA256 != then
}
