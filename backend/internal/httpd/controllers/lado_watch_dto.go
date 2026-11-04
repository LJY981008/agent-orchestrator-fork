package controllers

// LADO fork: DTOs of the worker watch settings and alerts (issue #42).
// Amounts are whole cents; a settings value of null follows the row below it.

import "time"

// LadoWatchSettingsValues is one settings row as stored. null follows the
// global row for a project, and the built-in default for the global row.
type LadoWatchSettingsValues struct {
	CompactWindow      *int   `json:"compactWindow" minimum:"100000" maximum:"1000000" description:"The token count Claude Code sessions of a project in LADO mode compact at (CLAUDE_CODE_AUTO_COMPACT_WINDOW), and Codex sessions' compact limit."`
	TaskCostCents      *int64 `json:"taskCostCents" minimum:"1" maximum:"10000000" description:"The estimated cost, in cents, of a task's sessions together past which LADO alerts, and once more past twice it."`
	SessionCostCents   *int64 `json:"sessionCostCents" minimum:"1" maximum:"10000000" description:"The estimated cost, in cents, of one session past which LADO alerts, and once more past twice it."`
	SessionCompactions *int   `json:"sessionCompactions" minimum:"1" maximum:"100" description:"How many times one session may compact before LADO alerts."`
	StallMinutes       *int   `json:"stallMinutes" minimum:"10" maximum:"10080" description:"How long a running worker may rest without a report before LADO takes it as stalled."`
	WakeEnabled        *bool  `json:"wakeEnabled" description:"Let LADO send its two fixed wake messages to the workers: the limit resume and the stall nudge."`
}

// LadoWatchSettingsResolved is a settings row resolved: every value set.
type LadoWatchSettingsResolved struct {
	CompactWindow      int   `json:"compactWindow"`
	TaskCostCents      int64 `json:"taskCostCents"`
	SessionCostCents   int64 `json:"sessionCostCents"`
	SessionCompactions int   `json:"sessionCompactions"`
	StallMinutes       int   `json:"stallMinutes"`
	WakeEnabled        bool  `json:"wakeEnabled"`
}

// LadoWatchSettingsResponse is one settings row, what its nulls follow and
// what it resolves to. A change applies from each session's next start.
type LadoWatchSettingsResponse struct {
	ProjectID string                    `json:"projectId" description:"The project; empty for the global row."`
	Values    LadoWatchSettingsValues   `json:"values" description:"The row as stored; null follows inherited."`
	Inherited LadoWatchSettingsResolved `json:"inherited" description:"What a null value follows: the global row over the defaults for a project, the defaults for the global row."`
	Effective LadoWatchSettingsResolved `json:"effective" description:"The settings the scope runs with."`
	Defaults  LadoWatchSettingsResolved `json:"defaults" description:"The built-in defaults."`
	UpdatedAt *time.Time                `json:"updatedAt,omitempty" description:"When the row was last saved; omitted without a row."`
}

// UpdateLadoWatchSettingsRequest replaces one settings row. A value that is
// null or missing follows the row below it; a value out of range is refused
// with 400. The change needs no presence proof.
type UpdateLadoWatchSettingsRequest LadoWatchSettingsValues

// LadoAlertIDParam names one alert.
type LadoAlertIDParam struct {
	AlertID string `path:"alertId" description:"LADO alert id."`
}

// ListLadoAlertsQuery selects a project's alerts.
type ListLadoAlertsQuery struct {
	TaskID string `query:"taskId,omitempty" description:"Only this task's alerts."`
	Unseen bool   `query:"unseen,omitempty" description:"When true, only the alerts nobody marked seen."`
	Limit  int    `query:"limit,omitempty" minimum:"1" maximum:"500" description:"At most this many, newest first; 100 when omitted."`
}

// LadoAlertResponse is one one-line alert of a task or one of its sessions.
type LadoAlertResponse struct {
	ID             int64      `json:"id"`
	TaskID         string     `json:"taskId"`
	SessionID      string     `json:"sessionId" description:"The session; empty for an alert of the task's total or of the task itself."`
	ProjectID      string     `json:"projectId"`
	IssueRef       string     `json:"issueRef"`
	Kind           string     `json:"kind" enum:"task_cost,session_cost,session_compactions,limited,limit_resumed,stall_nudged,stalled,agent_unavailable"`
	Level          int        `json:"level" description:"1 past a cost threshold, 2 past twice it; the resume's number for limit_resumed; otherwise 1."`
	Value          int64      `json:"value" description:"Cents for a cost, a count for compactions, minutes for a stall, the reset's Unix seconds for limited."`
	Threshold      int64      `json:"threshold" description:"The threshold the value is held against, in the value's unit; 0 when there is none."`
	Line           string     `json:"line" description:"The alert's one line, as the orchestrator gets it."`
	NotificationID string     `json:"notificationId" description:"The desktop notification id: lado-alert:<id>."`
	CreatedAt      time.Time  `json:"createdAt"`
	SeenAt         *time.Time `json:"seenAt" description:"null until someone marks it seen."`
}

// LadoAlertListResponse is a project's alerts, newest first.
type LadoAlertListResponse struct {
	Alerts []LadoAlertResponse `json:"alerts"`
}

// LadoCompactWarningResponse is a setting that turns Claude Code's
// auto-compact off, so the compact window does nothing.
type LadoCompactWarningResponse struct {
	Source  string `json:"source" enum:"project_settings,project_local_settings,user_settings,user_config,lado_settings,project_env,launch_env,daemon_env" description:"Where it was found."`
	Setting string `json:"setting" enum:"autoCompactEnabled,DISABLE_AUTO_COMPACT,DISABLE_COMPACT"`
	Path    string `json:"path,omitempty" description:"The file it was found in; omitted for an environment."`
}
