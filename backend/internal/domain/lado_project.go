package domain

import "time"

// LadoProjectSettings are LADO's own settings for one project
// (docs/LJY-PRD/TDD.md §4.2). They live in lado_project_settings, not in
// projects.config, because AO replaces that JSON as a whole. A project without
// stored settings has LADO mode off.
type LadoProjectSettings struct {
	ProjectID ProjectID
	// LadoMode puts the project's workers under the worker gate (TDD §6).
	LadoMode  bool
	UpdatedAt time.Time
}
