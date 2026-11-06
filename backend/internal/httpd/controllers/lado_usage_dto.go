package controllers

// LADO fork: DTOs of what a task's sessions spent (issue #42). Costs are
// AO's estimates at API prices in nanodollars, not the bill of a
// subscription; times are RFC 3339 and durations whole milliseconds.

import "time"

// LadoUsageTotalsResponse is what a set of sessions used.
type LadoUsageTotalsResponse struct {
	CostNanos            int64 `json:"costNanos" description:"Estimated cost at API prices, in nanodollars. A session without priced usage adds nothing."`
	InputTokens          int64 `json:"inputTokens"`
	CachedInputTokens    int64 `json:"cachedInputTokens"`
	OutputTokens         int64 `json:"outputTokens"`
	SubagentCostNanos    int64 `json:"subagentCostNanos" description:"The part of costNanos the sessions' subagents used."`
	SubagentInputTokens  int64 `json:"subagentInputTokens"`
	SubagentOutputTokens int64 `json:"subagentOutputTokens"`
}

// LadoSessionUsageResponse is what one session of a task used.
type LadoSessionUsageResponse struct {
	SessionID     string                  `json:"sessionId"`
	Role          string                  `json:"role" enum:"designer,critic,worker,part,merge,integration" description:"The role the task's timings show for the session."`
	PartID        string                  `json:"partId,omitempty" description:"The part of a part or merge worker."`
	QueueEntryID  string                  `json:"queueEntryId,omitempty" description:"The queue entry of the execution the session ran; omitted for a plan session. Sessions of one execution, retries included, share it."`
	Attempt       int                     `json:"attempt" description:"The session's attempt from 1; 0 when it cannot be read, as for a deleted session."`
	Totals        LadoUsageTotalsResponse `json:"totals"`
	ContextUsed   int64                   `json:"contextUsed" description:"Tokens in the session's context now; 0 before the provider reports."`
	ContextWindow int64                   `json:"contextWindow"`
	Compactions   int                     `json:"compactions" description:"How often the session compacted, as the watch loop counted it."`
	UsageMissing  bool                    `json:"usageMissing" description:"AO no longer has the session; its usage was deleted with it and counts as nothing."`
	Incomplete    bool                    `json:"incomplete" description:"AO knows it did not collect the session's usage in full."`
}

// LadoTaskUsageSummaryResponse is what a task's sessions used together.
type LadoTaskUsageSummaryResponse struct {
	TaskID          string                  `json:"taskId"`
	IssueRef        string                  `json:"issueRef,omitempty"`
	Title           string                  `json:"title"`
	Totals          LadoUsageTotalsResponse `json:"totals"`
	Compactions     int                     `json:"compactions"`
	StartedAt       *time.Time              `json:"startedAt,omitempty" description:"The task's intake."`
	EndedAt         time.Time               `json:"endedAt" description:"When the elapsed time ends: now while the task is in progress."`
	ElapsedMs       int64                   `json:"elapsedMs" description:"From intake to the merge (the timings' total), the cancellation, the close without a merge of the pull request, or now."`
	EndReason       string                  `json:"endReason" enum:"merged,cancelled,closed,in_progress"`
	InProgress      bool                    `json:"inProgress"`
	SessionsMissing int                     `json:"sessionsMissing" description:"How many of the task's sessions AO no longer has."`
	Incomplete      bool                    `json:"incomplete" description:"Some session's usage is incomplete."`
}

// LadoTaskUsageResponse is what a task's sessions used together and one by
// one.
type LadoTaskUsageResponse struct {
	Task     LadoTaskUsageSummaryResponse `json:"task"`
	Sessions []LadoSessionUsageResponse   `json:"sessions" description:"The plan sessions, the earlier plan worker attempts, then the executions."`
}

// LadoProjectUsageResponse is the totals of a project's tasks that are not
// cancelled, for its board.
type LadoProjectUsageResponse struct {
	ProjectID string                         `json:"projectId"`
	Tasks     []LadoTaskUsageSummaryResponse `json:"tasks"`
}
