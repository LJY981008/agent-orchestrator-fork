package controllers

// LADO fork: DTOs for the queue of approved plans, the executions it starts
// and the dispatch settings (docs/LJY-PRD/TDD.md §5.3 and §13).

import "time"

// ListLadoQueueQuery selects the queue entries to list.
type ListLadoQueueQuery struct {
	ProjectID string `query:"projectId,omitempty" description:"Project whose entries are listed; every project's when omitted."`
}

// LadoQueueItemResponse is one approved plan waiting for the dispatcher.
type LadoQueueItemResponse struct {
	ID        string `json:"id"`
	TaskID    string `json:"taskId"`
	ProjectID string `json:"projectId"`
	IssueRef  string `json:"issueRef"`
	Title     string `json:"title" description:"The issue's title as LADO last read it."`
	State     string `json:"state" enum:"queued,waiting,dispatching,dispatched,failed,review_required,blocked,superseded,cancelled" description:"Derived entry state. The queue lists open entries: queued, waiting (reason says why) and dispatching, while LADO starts the worker."`
	Reason    string `json:"reason,omitempty" enum:"rules_changed,issue_edited,plan_superseded,blocking_snapshot,base_ref_mismatch,agent_unsupported,slot_global,slot_project,memory_tight,issue_unreadable,retry,lado_mode_off,run_live" description:"Why the entry waits, needs review or is blocked, for the app to translate."`
	Attempts  int    `json:"attempts" description:"Attempts to start the worker so far. LADO gives up after three; waiting for a slot, memory or the issue is not an attempt."`
	// NextAttemptAt is omitted once it is due.
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty" description:"When LADO looks at the entry again; omitted once that is due."`
	Error         string     `json:"error,omitempty" description:"Why the last attempt failed, without secrets."`
	EnqueuedAt    time.Time  `json:"enqueuedAt" description:"When the plan was approved."`
}

// LadoQueueResponse lists the queue in the order the dispatcher takes it.
type LadoQueueResponse struct {
	Entries []LadoQueueItemResponse `json:"entries"`
}

// LadoTaskQueueResponse is the queue entry of a task's newest approval.
type LadoTaskQueueResponse struct {
	State         string     `json:"state" enum:"queued,waiting,dispatching,dispatched,failed,review_required,blocked,superseded,cancelled"`
	Reason        string     `json:"reason,omitempty" enum:"rules_changed,issue_edited,plan_superseded,blocking_snapshot,base_ref_mismatch,agent_unsupported,slot_global,slot_project,memory_tight,issue_unreadable,retry,lado_mode_off,run_live" description:"Why the entry waits, needs review or is blocked."`
	Attempts      int        `json:"attempts" description:"Attempts to start the worker so far; LADO gives up after three."`
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty" description:"When LADO looks at the entry again; omitted once that is due."`
	Error         string     `json:"error,omitempty" description:"Why starting the worker failed, without secrets."`
	EnqueuedAt    time.Time  `json:"enqueuedAt" description:"When the plan was approved; the dispatcher takes the oldest first."`
}

// LadoExecutionPRResponse is the newest PR of an execution's session.
type LadoExecutionPRResponse struct {
	URL          string `json:"url"`
	Number       int    `json:"number"`
	Merged       bool   `json:"merged"`
	Closed       bool   `json:"closed" description:"Closed without a merge."`
	HeadSHA      string `json:"headSha,omitempty" description:"The PR's head as AO last observed it, which LADO verifies; omitted until AO observed it."`
	TargetBranch string `json:"targetBranch,omitempty" description:"The branch the PR merges into."`
	Title        string `json:"title,omitempty"`
}

// LadoTaskExecutionResponse is a task's newest execution: the worker session
// LADO started for it.
type LadoTaskExecutionResponse struct {
	SessionID      string `json:"sessionId"`
	PlanRevisionID string `json:"planRevisionId" description:"The plan revision the worker runs."`
	// PlanRevisionNumber and PolicySHA256 come from the task's own plan
	// facts, which name the latest revision and the newest approval's
	// snapshot.
	PlanRevisionNumber int                      `json:"planRevisionNumber,omitempty" description:"That revision's number; omitted once a newer revision is pinned. GET /api/v1/lado/executions/{sessionId} always has it."`
	PolicySHA256       string                   `json:"policySha256,omitempty" description:"Hash of the rule snapshot the worker runs under; omitted for the run of an older approval. GET /api/v1/lado/executions/{sessionId} always has it."`
	StartedAt          time.Time                `json:"startedAt"`
	DeadlineAt         *time.Time               `json:"deadlineAt,omitempty" description:"When LADO stops the run; omitted without a limit."`
	TimedOut           bool                     `json:"timedOut" description:"LADO stopped the run at its deadline."`
	SessionEnded       bool                     `json:"sessionEnded" description:"The worker session ended. While it lives, the task cannot be approved again."`
	PR                 *LadoExecutionPRResponse `json:"pr,omitempty" description:"The newest PR of the worker session; omitted while it has none."`
}

// LadoSessionIDParam names an execution's session in
// /api/v1/lado/executions routes.
type LadoSessionIDParam struct {
	SessionID string `path:"sessionId" description:"AO session id of the worker."`
}

// LadoExecutionTaskResponse is the task an execution runs.
type LadoExecutionTaskResponse struct {
	ID       string `json:"id"`
	Title    string `json:"title" description:"The issue's title as LADO last read it."`
	IssueRef string `json:"issueRef"`
	IssueURL string `json:"issueUrl"`
}

// LadoExecutionPlanResponse is the plan revision an execution runs and its
// approval.
type LadoExecutionPlanResponse struct {
	RevisionID string    `json:"revisionId"`
	Number     int       `json:"number"`
	SHA256     string    `json:"sha256" description:"Hash of the approved plan file."`
	ApprovedAt time.Time `json:"approvedAt"`
}

// LadoExecutionRuleResponse is one rule of an execution's snapshot.
type LadoExecutionRuleResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name" description:"The rule's name when the plan was approved. A built-in rule keeps its English seed name, which the app translates."`
	Version int    `json:"version"`
}

// LadoExecutionSnapshotResponse is the rule snapshot an execution runs
// under: what its approval fixed.
type LadoExecutionSnapshotResponse struct {
	SHA256            string                      `json:"sha256"`
	Agent             string                      `json:"agent" description:"The agent harness the worker runs, such as claude-code."`
	Model             string                      `json:"model,omitempty"`
	PermissionMode    string                      `json:"permissionMode" enum:"accept-edits,auto,bypass-permissions" description:"The mode the worker started with."`
	PermissionCeiling string                      `json:"permissionCeiling" enum:"accept-edits,auto,bypass-permissions" description:"The highest mode a turn of the worker may use."`
	Rules             []LadoExecutionRuleResponse `json:"rules" description:"The rules that were on, by id."`
	Prompt            string                      `json:"prompt" description:"The rules text in the worker's system prompt; empty when no rule adds any."`
	// Setup are the local setup items the worker's session gets.
	Setup []LadoExecutionSetupItemResponse `json:"setup" description:"The local setup items that were on, the versions the worker's session gets."`
}

// LadoExecutionBaseResponse is where an execution's base stood: when its
// plan was made and when its worker started. A moved base is recorded, not
// refused.
type LadoExecutionBaseResponse struct {
	PlanSHA      string `json:"planSha,omitempty" description:"The commit the plan's base_ref named when LADO pinned the plan."`
	DispatchSHA  string `json:"dispatchSha,omitempty" description:"The commit the worker's base named when LADO started it."`
	CommitsAhead *int   `json:"commitsAhead,omitempty" description:"How many commits the base moved between the two; omitted when unknown."`
}

// LadoExecutionResponse is one execution as the session view shows it.
type LadoExecutionResponse struct {
	SessionID    string                        `json:"sessionId"`
	Task         LadoExecutionTaskResponse     `json:"task"`
	Plan         LadoExecutionPlanResponse     `json:"plan"`
	Snapshot     LadoExecutionSnapshotResponse `json:"snapshot"`
	Base         LadoExecutionBaseResponse     `json:"base"`
	Attempt      int                           `json:"attempt" description:"The attempt that started the worker, from 1."`
	StartedAt    time.Time                     `json:"startedAt"`
	DeadlineAt   *time.Time                    `json:"deadlineAt,omitempty" description:"When LADO stops the run; omitted without a limit."`
	TimedOutAt   *time.Time                    `json:"timedOutAt,omitempty" description:"When LADO stopped the run at its deadline."`
	SessionEnded bool                          `json:"sessionEnded"`
	State        string                        `json:"state" enum:"running,validating,integration_pending,integrating,failed,completed,closed_unmerged" description:"The state the run gives its task, as the daemon derives it: running without a PR, failed when it timed out or ended without one, and once a PR is open that PR's verification and integration."`
	StateReason  string                        `json:"stateReason,omitempty" enum:"timed_out,ended_without_pr,verifying,checks_pending,checks_failed,commands_running,worktree_not_at_head,worktree_changed,head_unavailable,commands_failed,command_timed_out,worktree_missing,commit_messages_failed,protected_path,review_pending,changes_requested,status_not_posted,merged_without_approval" description:"Why the run is in its state, as the task's stateReason says it."`
	PR           *LadoExecutionPRResponse      `json:"pr,omitempty" description:"The newest PR of the worker session; omitted while it has none."`
	// Verification and Integration are those of the PR's current head.
	Verification *LadoVerificationResponse        `json:"verification,omitempty" description:"What LADO verified about the PR's current head; omitted until LADO computed it, while the task is validating with verifying."`
	Integration  *LadoIntegrationApprovalResponse `json:"integration,omitempty" description:"The integration approval of the PR's current head; omitted without one."`
}

// LadoDispatchSettingsResponse is the dispatcher's settings for every
// project.
type LadoDispatchSettingsResponse struct {
	MaxConcurrent     int                       `json:"maxConcurrent" minimum:"1" maximum:"8" description:"How many executions run at once across projects. Each project has its own cap too."`
	MaxRunMinutes     int                       `json:"maxRunMinutes" enum:"[0,60,120,240,480,720,1440]" description:"How long an execution may run before LADO stops it; 0 means no limit."`
	HoldOnMemoryTight bool                      `json:"holdOnMemoryTight" description:"Keep queued work and plan requests waiting while memory pressure is tight."`
	MaxPlanWorkers    int                       `json:"maxPlanWorkers" minimum:"1" maximum:"8" description:"How many plan requests run their plan workers at once across projects, apart from the executions' caps; a designer and its critic count as one."`
	PlanReview        LadoPlanReviewSettingsDTO `json:"planReview" description:"When LADO skips the review of a plan worker's draft."`
	UpdatedAt         *time.Time                `json:"updatedAt,omitempty" description:"When the settings last changed; omitted while the defaults apply."`
}

// LadoPlanReviewSettingsDTO says when LADO skips the review of a draft: with
// skip on, a plan with at most maxPaths affected paths that neither declares
// nor, by the patterns, touches a database schema, an external API,
// authentication or data deletion.
type LadoPlanReviewSettingsDTO struct {
	Skip     bool                `json:"skip" description:"Skip the review of small plans; off reviews every draft."`
	MaxPaths int                 `json:"maxPaths" minimum:"1" maximum:"20" description:"The most affected paths a plan may name and skip the review."`
	Patterns map[string][]string `json:"patterns" description:"Globs that infer a change kind (db_schema, external_api, auth, data_deletion) from a plan's affected paths."`
}

// UpdateLadoDispatchSettingsRequest replaces the dispatch settings. The
// executions' fields are required; the plan worker fields keep their values
// when omitted. The change needs no presence proof.
type UpdateLadoDispatchSettingsRequest struct {
	MaxConcurrent     *int                       `json:"maxConcurrent" nullable:"false" minimum:"1" maximum:"8"`
	MaxRunMinutes     *int                       `json:"maxRunMinutes" nullable:"false" enum:"[0,60,120,240,480,720,1440]"`
	HoldOnMemoryTight *bool                      `json:"holdOnMemoryTight" nullable:"false"`
	MaxPlanWorkers    *int                       `json:"maxPlanWorkers,omitempty" minimum:"1" maximum:"8"`
	PlanReview        *LadoPlanReviewSettingsDTO `json:"planReview,omitempty"`
}
