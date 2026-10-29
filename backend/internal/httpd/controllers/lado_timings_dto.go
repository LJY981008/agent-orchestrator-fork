package controllers

// LADO fork: DTOs of how long a task's stages took and what its sessions
// spent on investigation (issue #34). Times are RFC 3339, durations whole
// milliseconds.

import "time"

// LadoTimingsQuery selects the project whose baseline is summed up.
type LadoTimingsQuery struct {
	ProjectID string `query:"projectId" required:"true" description:"Project whose tasks are summed up."`
	Limit     int    `query:"limit,omitempty" minimum:"1" maximum:"100" description:"How many of the newest tasks with an issue to sum up. Defaults to 20."`
}

// LadoTimingPointResponse is when a task reached a stage.
type LadoTimingPointResponse struct {
	Stage       string    `json:"stage" enum:"received,plan_requested,revision,approved,run_started,pr_opened,verified,integration_approved,merged"`
	At          time.Time `json:"at"`
	SincePrevMs int64     `json:"sincePrevMs" description:"Milliseconds since the point before; 0 for the first point."`
	Estimated   bool      `json:"estimated" description:"The time was inferred for an execution that ran before LADO recorded it; an estimated verified time is an upper bound."`
}

// LadoTaskTimelineResponse is the stages a task reached, in stage order.
type LadoTaskTimelineResponse struct {
	Points       []LadoTimingPointResponse `json:"points" description:"A stage the task did not reach has no point."`
	TotalMs      int64                     `json:"totalMs" description:"Milliseconds from intake to merge; 0 before the merge."`
	PlanRequests int                       `json:"planRequests"`
	Revisions    int                       `json:"revisions"`
	Approvals    int                       `json:"approvals"`
	Runs         int                       `json:"runs" description:"The executions that open the task's pull request."`
}

// LadoSessionMetricsResponse is what one session of a task spent on
// investigation.
type LadoSessionMetricsResponse struct {
	SessionID          string     `json:"sessionId"`
	Role               string     `json:"role" enum:"designer,critic,worker,part,merge,integration"`
	ToolCalls          int        `json:"toolCalls" description:"Commands, file changes and MCP tool calls."`
	ReadCalls          int        `json:"readCalls"`
	SearchCalls        int        `json:"searchCalls"`
	CommandCalls       int        `json:"commandCalls" description:"Commands that neither read nor search."`
	EditCalls          int        `json:"editCalls"`
	InvestigationCalls int        `json:"investigationCalls" description:"Read and search calls."`
	FirstActivityAt    *time.Time `json:"firstActivityAt,omitempty"`
	FirstEditAt        *time.Time `json:"firstEditAt,omitempty"`
	LastActivityAt     *time.Time `json:"lastActivityAt,omitempty"`
	InvestigateMs      int64      `json:"investigateMs" description:"Milliseconds from the first tool call to the first edit, or to the last activity without one."`
	Stored             bool       `json:"stored" description:"False when the metrics were computed at read time from the session's activities."`
	UpdatedAt          *time.Time `json:"updatedAt,omitempty" description:"When the stored row was written; omitted for computed metrics."`
}

// LadoTaskTimingsResponse is one task's timeline and its sessions' metrics.
type LadoTaskTimingsResponse struct {
	TaskID   string                       `json:"taskId"`
	IssueRef string                       `json:"issueRef,omitempty"`
	Title    string                       `json:"title"`
	Timeline LadoTaskTimelineResponse     `json:"timeline"`
	Sessions []LadoSessionMetricsResponse `json:"sessions"`
}

// LadoTimingGapResponse is the time between two adjacent stages over a
// project's tasks.
type LadoTimingGapResponse struct {
	From     string `json:"from" enum:"received,plan_requested,revision,approved,run_started,pr_opened,verified,integration_approved,merged"`
	To       string `json:"to" enum:"received,plan_requested,revision,approved,run_started,pr_opened,verified,integration_approved,merged"`
	Count    int    `json:"count" description:"The tasks that went from one stage straight to the other."`
	MedianMs int64  `json:"medianMs"`
	MaxMs    int64  `json:"maxMs"`
}

// LadoInvestigationSummaryResponse is the median investigation of one
// session role over a project's tasks.
type LadoInvestigationSummaryResponse struct {
	Role                     string `json:"role" enum:"designer,critic,worker,part,merge,integration"`
	Sessions                 int    `json:"sessions"`
	MedianCalls              int    `json:"medianCalls"`
	MedianInvestigationCalls int    `json:"medianInvestigationCalls"`
	MedianInvestigateMs      int64  `json:"medianInvestigateMs"`
}

// LadoTimingsSummaryResponse is a project's baseline: its recent tasks and
// the medians over them.
type LadoTimingsSummaryResponse struct {
	ProjectID     string                             `json:"projectId"`
	Tasks         []LadoTaskTimingsResponse          `json:"tasks"`
	Gaps          []LadoTimingGapResponse            `json:"gaps"`
	Investigation []LadoInvestigationSummaryResponse `json:"investigation"`
}
