package controllers

// LADO fork: DTOs of plan workers (docs/LJY-PRD/TDD.md §5.1 and §13): what a
// task shows of its plan worker, a draft's review, what making a revision
// took, and what plan workers submit.

import "time"

// LadoPlanRequestIDParam names a plan request in /api/v1/lado/plan-requests
// routes.
type LadoPlanRequestIDParam struct {
	RequestID string `path:"requestId" description:"LADO plan request id."`
}

// LadoPlanWorkerResponse is where a task's newest plan worker request is.
type LadoPlanWorkerResponse struct {
	RequestID         string     `json:"requestId"`
	Phase             string     `json:"phase" enum:"waiting,designing,critiquing,revising,submitted,fixing,done,failed,replaced" description:"waiting: for a plan worker slot or for memory; designing: the designer drafts the plan; critiquing: a critic reviews the draft, or LADO decides to skip the review; revising: the designer answers the review; submitted: LADO pins the final plan; fixing: the designer fixes the pinned plan's problems, once; done: the plan is pinned and the sessions stopped; failed and replaced end the request."`
	WaitingReason     string     `json:"waitingReason,omitempty" enum:"plan_worker_capacity,memory_tight" description:"Why the request waits: every plan worker slot of the Runs settings is taken, or memory is tight."`
	FailureCode       string     `json:"failureCode,omitempty" enum:"plan_worker_spawn_failed,plan_worker_no_plan,plan_worker_timed_out,plan_worker_agent_unsupported,replaced" description:"Why the request failed, for the app to translate."`
	DesignerSessionID string     `json:"designerSessionId,omitempty" description:"The designer session, once it started."`
	CriticSessionID   string     `json:"criticSessionId,omitempty" description:"The newest critic session of the draft's review, once one started."`
	Review            string     `json:"review,omitempty" enum:"running,done,failed,skipped" description:"How the newest review of the draft went; omitted before one."`
	StartedAt         *time.Time `json:"startedAt,omitempty" description:"When the designer started."`
}

// LadoPlanFindingResponse is one finding of a critic, with how the plan's
// review section answers it.
type LadoPlanFindingResponse struct {
	ID              string `json:"id"`
	Severity        string `json:"severity" enum:"P1,major,minor"`
	Title           string `json:"title"`
	FailureScenario string `json:"failureScenario,omitempty"`
	Where           string `json:"where,omitempty"`
	Suggestion      string `json:"suggestion,omitempty"`
	Answer          string `json:"answer,omitempty" enum:"addressed,not_addressed" description:"How the plan's review section answers the finding; omitted when it does not."`
}

// LadoPlanCritiqueResponse is one review of a plan worker's draft.
type LadoPlanCritiqueResponse struct {
	RequestID     string                    `json:"requestId"`
	Attempt       int                       `json:"attempt" description:"1 and 2 for the critic sessions, 0 for a skipped review."`
	SessionID     string                    `json:"sessionId,omitempty" description:"The critic session."`
	State         string                    `json:"state" enum:"running,done,failed,skipped"`
	SkipReason    string                    `json:"skipReason,omitempty" description:"Why LADO skipped the review."`
	FailureReason string                    `json:"failureReason,omitempty" description:"Why the critic gave no result."`
	Findings      []LadoPlanFindingResponse `json:"findings"`
	StartedAt     time.Time                 `json:"startedAt"`
	FinishedAt    *time.Time                `json:"finishedAt,omitempty"`
}

// LadoPlanSessionUsageResponse is what one plan worker session used.
type LadoPlanSessionUsageResponse struct {
	SessionID        string `json:"sessionId"`
	Role             string `json:"role" enum:"design,critique"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
	CostNanos        *int64 `json:"costNanos,omitempty" description:"AO's cost estimate in nano dollars; omitted when AO priced none of the session's events."`
}

// LadoPlanMetricsResponse is what making a revision took.
type LadoPlanMetricsResponse struct {
	DesignMs     int64                          `json:"designMs" description:"From the designer's start to its draft."`
	CritiqueMs   int64                          `json:"critiqueMs" description:"Over every critic session of the review."`
	ReviseMs     int64                          `json:"reviseMs" description:"From the review's result to the final plan."`
	Review       string                         `json:"review" enum:"done,skipped,failed,none"`
	P1           int                            `json:"p1"`
	Major        int                            `json:"major"`
	Minor        int                            `json:"minor"`
	Addressed    int                            `json:"addressed" description:"P1 and major findings the plan says it took."`
	NotAddressed int                            `json:"notAddressed" description:"P1 and major findings the plan says it did not take, with a reason."`
	Sessions     []LadoPlanSessionUsageResponse `json:"sessions"`
	ChangeKinds  []string                       `json:"changeKinds"`
	UpdatedAt    time.Time                      `json:"updatedAt"`
}

// SubmitLadoPlanRequest is a plan worker's plan.
type SubmitLadoPlanRequest struct {
	SessionID string `json:"sessionId" description:"The submitting session; it must be the request's designer."`
	Content   string `json:"content" description:"The whole plan file."`
	Draft     bool   `json:"draft,omitempty" description:"The draft LADO reviews; without it the plan is final and LADO pins it."`
}

// LadoPlanFindingInput is one finding a critic submits.
type LadoPlanFindingInput struct {
	ID              string `json:"id"`
	Severity        string `json:"severity" enum:"P1,major,minor"`
	Title           string `json:"title"`
	FailureScenario string `json:"failure_scenario,omitempty"`
	Where           string `json:"where,omitempty"`
	Suggestion      string `json:"suggestion,omitempty"`
}

// SubmitLadoCritiqueRequest is a critic's findings.
type SubmitLadoCritiqueRequest struct {
	SessionID string                 `json:"sessionId" description:"The submitting session; it must be the request's running critic."`
	Findings  []LadoPlanFindingInput `json:"findings"`
}
