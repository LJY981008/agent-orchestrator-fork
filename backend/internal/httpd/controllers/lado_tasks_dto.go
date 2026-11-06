package controllers

// LADO fork: DTOs for issue-backed tasks (docs/LJY-PRD/TDD.md §5.1 and §13).

import "time"

// LadoTaskIDParam names a task in /api/v1/lado/tasks routes.
type LadoTaskIDParam struct {
	TaskID string `path:"taskId" description:"LADO task id."`
}

// ListLadoTasksQuery selects a project's tasks.
type ListLadoTasksQuery struct {
	ProjectID        string `query:"projectId" required:"true" description:"Project whose tasks are listed."`
	IncludeCancelled bool   `query:"includeCancelled,omitempty" description:"When true, cancelled tasks are listed too."`
}

// LadoTaskIssueResponse is a task's issue as LADO last observed it. The
// tracker owns the issue.
type LadoTaskIssueResponse struct {
	Ref        string    `json:"ref" description:"Canonical issue id, in the form AO stores in sessions.issue_id."`
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	State      string    `json:"state" enum:"open,in_progress,review,done,cancelled" description:"Normalized issue state. GitHub reports a not-planned close as cancelled."`
	URL        string    `json:"url"`
	ObservedAt time.Time `json:"observedAt"`
}

// LadoTaskResponse is one task.
type LadoTaskResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	// Repository is the canonical id of the repository that holds the issue.
	Repository string `json:"repository" description:"Canonical id of the repository that holds the issue, such as github:owner/repo."`
	State      string `json:"state" enum:"draft,planned,review_required,blocked,approved,queued,dispatching,running,validating,integration_pending,integrating,failed,completed,cancelled,closed_unmerged" description:"Derived task state. review_required: the plan needs a new approval; blocked: the plan or the setup must change first; queued: the approved plan waits for the dispatcher; dispatching: LADO starts its worker; running: the worker session lives without a PR; validating: LADO verifies the open PR's current head, and stateReason says what it waits for or what fails; integration_pending: the head passed, or needs confirming, and waits for the developer's integration approval; integrating: the head's integration is approved; failed: the worker did not start, timed out or ended without a PR; completed: the PR merged; closed_unmerged: the PR or the issue closed without a merge. approved is no longer sent."`
	// StateReason is domain.LadoTask.StateReason.
	StateReason string                     `json:"stateReason,omitempty" enum:"rules_changed,issue_edited,plan_superseded,approved_before_queue,blocking_snapshot,base_ref_mismatch,agent_unsupported,attempts_exhausted,timed_out,ended_without_pr,reported_pr_untracked,slot_global,slot_project,slot_workers,memory_tight,disk_low,paths_overlap,issue_unreadable,retry,lado_mode_off,run_live,account_limited,agent_unavailable,verifying,checks_pending,checks_failed,commands_running,worktree_not_at_head,worktree_changed,head_unavailable,commands_failed,command_timed_out,worktree_missing,commit_messages_failed,protected_path,pr_repo_not_in_workspace,review_pending,changes_requested,status_not_posted,merged_without_approval,base_overlap,merge_conflict,resolving,conflict_unresolved,pr_missing" description:"Why the task needs review, is blocked or failed, or what its queued plan waits for; reported_pr_untracked for running while the worker reported a pull request its session does not track, and for validating while LADO still tries to track one before an approval; for validating, pr_missing while a repository the task is expected to open a pull request in has none yet (a workspace task across repositories), conflict_unresolved when LADO's resolve workers could not resolve the open PR, resolving while one takes the base into it, merge_conflict while the PR conflicts with its base and base_overlap when the base moved under the head with a change to the same paths, all four before an approval; else verifying while LADO has not verified the head yet, else the code of the verification item that fails or waits; status_not_posted for integrating while LADO could not post the lado/integration status; merged_without_approval for completed when the merged head had no integration approval. For the app to translate."`
	Issue       LadoTaskIssueResponse      `json:"issue"`
	Agent       string                     `json:"agent,omitempty" description:"Agent chosen for the task's execution."`
	Model       string                     `json:"model,omitempty" description:"Model chosen for the task's execution."`
	Plan        LadoTaskPlanResponse       `json:"plan"`
	Queue       *LadoTaskQueueResponse     `json:"queue,omitempty" description:"The queue entry of the newest approval; omitted without one."`
	Execution   *LadoTaskExecutionResponse `json:"execution,omitempty" description:"The execution that opens the task's pull request for the newest approval, or one of an older approval that still decides the task's state because its session lives, its PR is open, integrated or merged: its one worker, or for a task split into parts its integration worker; omitted before that worker started."`
	// PreviousExecution is the newest execution when it is not Execution.
	PreviousExecution *LadoTaskExecutionResponse    `json:"previousExecution,omitempty" description:"The newest execution when it belongs to an older approval and ended without a merge (it timed out or its session ended without a PR, or its PR closed), so it no longer decides the task's state; omitted otherwise. The daemon refuses a new approval while its session lives."`
	Parts             *LadoTaskPartsSummaryResponse `json:"parts,omitempty" description:"How far the parts run of the newest approval got; omitted for a task without parts. GET /api/v1/lado/tasks/{taskId}/parts lists the parts."`
	CreatedAt         time.Time                     `json:"createdAt"`
	CancelledAt       *time.Time                    `json:"cancelledAt,omitempty"`
}

// LadoTaskListResponse lists a project's tasks, oldest first.
type LadoTaskListResponse struct {
	Tasks []LadoTaskResponse `json:"tasks"`
}

// CreateLadoTaskRequest creates an issue on the project's repository, then a
// task for it.
type CreateLadoTaskRequest struct {
	ProjectID  string `json:"projectId"`
	Repository string `json:"repository,omitempty" description:"Canonical id of the repository to create the issue in. Optional when the project has one repository."`
	Text       string `json:"text" description:"New Task text: the first line becomes the issue title and the rest its body."`
	Agent      string `json:"agent,omitempty"`
	Model      string `json:"model,omitempty"`
}

// UpdateLadoTaskRequest changes a task's preferences. An omitted field keeps
// its value.
type UpdateLadoTaskRequest struct {
	Agent *string `json:"agent,omitempty"`
	Model *string `json:"model,omitempty"`
}

// ImportLadoTasksRequest imports open issues as tasks. It writes nothing to
// the tracker.
type ImportLadoTasksRequest struct {
	ProjectID string   `json:"projectId"`
	IssueRefs []string `json:"issueRefs" description:"Canonical ids of the issues to import."`
}

// ImportLadoTasksResponse is the tasks an import created.
type ImportLadoTasksResponse struct {
	Tasks   []LadoTaskResponse `json:"tasks"`
	Skipped []string           `json:"skipped" description:"Issues left out because they already have a task."`
}

// LadoRepositoryResponse is one repository a project's issues can live in.
type LadoRepositoryResponse struct {
	Ref      string `json:"ref" description:"Canonical repository id, such as github:owner/repo."`
	Name     string `json:"name" description:"The workspace child's folder name, or the project's name."`
	Provider string `json:"provider" enum:"github,gitlab"`
	Host     string `json:"host"`
	Path     string `json:"path" description:"Repository path on the host."`
}

// LadoProjectRepositoriesResponse lists the repositories a project's issues
// can live in: a single-repo project's origin, or each workspace child with
// one.
type LadoProjectRepositoriesResponse struct {
	Repositories []LadoRepositoryResponse `json:"repositories"`
}

// LadoIssueResponse is an issue that can be imported.
type LadoIssueResponse struct {
	Repository LadoRepositoryResponse `json:"repository"`
	Ref        string                 `json:"ref"`
	Number     int                    `json:"number"`
	Title      string                 `json:"title"`
	URL        string                 `json:"url"`
	Labels     []string               `json:"labels"`
}

// LadoRepositoryFailureResponse is a repository whose issues could not be
// listed.
type LadoRepositoryFailureResponse struct {
	Repository LadoRepositoryResponse `json:"repository"`
	Problem    string                 `json:"problem"`
}

// LadoProjectIssuesResponse lists the open issues without a task in each of
// a project's repositories, and the repositories that could not be read.
type LadoProjectIssuesResponse struct {
	Issues   []LadoIssueResponse             `json:"issues"`
	Failures []LadoRepositoryFailureResponse `json:"failures"`
}
