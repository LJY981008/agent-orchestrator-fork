package controllers

// LADO fork: DTOs for LADO's project settings routes (docs/LJY-PRD/TDD.md §13).
// They live here rather than in dto.go to keep LADO out of upstream's hotspot.

// LadoProjectIDParam names the project in /api/v1/lado/projects routes.
type LadoProjectIDParam struct {
	ProjectID string `path:"projectId" description:"Project whose LADO settings are read or changed."`
}

// LadoCheckCommandBody is one of a project's check commands
// (docs/LJY-PRD/TDD.md §8.2).
type LadoCheckCommandBody struct {
	Name    string `json:"name" minLength:"1" maxLength:"40" pattern:"^[A-Za-z0-9][A-Za-z0-9 ._-]*$" description:"Unique within the project; evidence names a command by it."`
	Command string `json:"command" minLength:"1" maxLength:"2000" description:"One line, run with sh -c (cmd /d /s /c on Windows) in the execution's worktree, outside any agent sandbox."`
}

// LadoProjectSettingsResponse is a project's LADO settings.
type LadoProjectSettingsResponse struct {
	ProjectID              string                 `json:"projectId"`
	LadoMode               bool                   `json:"ladoMode" description:"Whether the project's workers are under LADO's worker gate."`
	MaxConcurrent          int                    `json:"maxConcurrent" description:"How many tasks run at once in the project, 1 to 8; a task split into parts counts once."`
	CheckCommands          []LadoCheckCommandBody `json:"checkCommands" description:"The commands LADO runs, in order, on each head of an execution's PR."`
	CheckCommandsSHA256    string                 `json:"checkCommandsSha256" description:"The hash of the check commands, which a change sends back as checkCommandsBase."`
	CheckTimeoutMinutes    int                    `json:"checkTimeoutMinutes" description:"Each check command's time limit in minutes, 5 to 120."`
	OverlapWait            bool                   `json:"overlapWait" description:"Hold a task while a live run of another task of the project may touch the same files."`
	OverlapIgnore          []string               `json:"overlapIgnore" description:"The globs whose overlap does not hold work back, between tasks and between a task's parts."`
	OverlapIgnoreIsDefault bool                   `json:"overlapIgnoreIsDefault" description:"overlapIgnore is LADO's default list."`
}

// UpdateLadoProjectSettingsRequest changes a project's LADO settings. It
// needs at least one field; an omitted field keeps its value.
type UpdateLadoProjectSettingsRequest struct {
	LadoMode              *bool                   `json:"ladoMode,omitempty" description:"Put the project's workers under LADO's worker gate. Turning it on refuses while tracker intake is on; turning it off needs a human-presence proof, and queued work then waits while running workers go on."`
	MaxConcurrent         *int                    `json:"maxConcurrent,omitempty" minimum:"1" maximum:"8" description:"How many executions run at once in the project. It changes without a presence proof."`
	CheckCommands         *[]LadoCheckCommandBody `json:"checkCommands,omitempty" maxItems:"10" description:"Replace the check commands, 0 to 10. A change needs a human-presence proof, because the daemon runs them outside any agent sandbox, and checkCommandsBase; an invalid list refuses with LADO_CHECK_COMMANDS_INVALID and its index, field and reason (a command with a control character, such as a line break, or a bidirectional control also with its position and the character as U+XXXX); a list that moved on since checkCommandsBase refuses with 409 LADO_CHECK_COMMANDS_CHANGED and the current checkCommandsSha256."`
	CheckCommandsBase     string                  `json:"checkCommandsBase,omitempty" description:"The checkCommandsSha256 of the list the change starts from; a change of checkCommands needs it."`
	CheckTimeoutMinutes   *int                    `json:"checkTimeoutMinutes,omitempty" minimum:"5" maximum:"120" description:"Each check command's time limit in minutes. It changes without a presence proof."`
	OverlapWait           *bool                   `json:"overlapWait,omitempty" description:"Hold a task while a live run of another task of the project may touch the same files. It changes without a presence proof."`
	OverlapIgnore         *[]string               `json:"overlapIgnore,omitempty" maxItems:"50" description:"Replace the globs whose overlap does not hold work back; an invalid glob refuses with LADO_OVERLAP_IGNORE_INVALID. It changes without a presence proof."`
	OverlapIgnoreDefaults bool                    `json:"overlapIgnoreDefaults,omitempty" description:"Go back to LADO's default list of globs whose overlap does not count."`
}
