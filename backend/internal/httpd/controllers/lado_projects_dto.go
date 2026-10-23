package controllers

// LADO fork: DTOs for LADO's project settings routes (docs/LJY-PRD/TDD.md §13).
// They live here rather than in dto.go to keep LADO out of upstream's hotspot.

// LadoProjectIDParam names the project in /api/v1/lado/projects routes.
type LadoProjectIDParam struct {
	ProjectID string `path:"projectId" description:"Project whose LADO settings are read or changed."`
}

// LadoProjectSettingsResponse is a project's LADO settings.
type LadoProjectSettingsResponse struct {
	ProjectID     string `json:"projectId"`
	LadoMode      bool   `json:"ladoMode" description:"Whether the project's workers are under LADO's worker gate."`
	MaxConcurrent int    `json:"maxConcurrent" description:"How many executions run at once in the project, 1 to 8."`
}

// UpdateLadoProjectSettingsRequest changes a project's LADO settings. It
// needs ladoMode, maxConcurrent or both; an omitted field keeps its value.
type UpdateLadoProjectSettingsRequest struct {
	LadoMode      *bool `json:"ladoMode,omitempty" description:"Put the project's workers under LADO's worker gate. Turning it on refuses while tracker intake is on; turning it off needs a human-presence proof, and queued work then waits while running workers go on."`
	MaxConcurrent *int  `json:"maxConcurrent,omitempty" minimum:"1" maximum:"8" description:"How many executions run at once in the project. It changes without a presence proof."`
}
