package domain

import "time"

// LadoProjectSettings are LADO's own settings for one project
// (docs/LJY-PRD/TDD.md §4.2). They live in lado_project_settings, not in
// projects.config, because AO replaces that JSON as a whole. A project without
// stored settings has LADO mode off.
type LadoProjectSettings struct {
	ProjectID ProjectID
	// LadoMode puts the project's workers under the worker gate (TDD §6).
	LadoMode bool
	// MaxConcurrent caps the executions that run at once in the project:
	// LadoDefaultProjectMaxConcurrent until it sets one.
	MaxConcurrent int
	// CheckCommands are the commands LADO runs on each PR head in the
	// execution's worktree (docs/LJY-PRD/TDD.md §8.2), at most
	// LadoCheckCommandsMax, and CheckTimeoutMinutes the time limit of each,
	// LadoCheckTimeoutMin to LadoCheckTimeoutMax (LadoCheckTimeoutDefault
	// until it sets one).
	CheckCommands       []LadoCheckCommand
	CheckTimeoutMinutes int
	UpdatedAt           time.Time
}
