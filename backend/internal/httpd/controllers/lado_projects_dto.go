package controllers

// LADO fork: DTOs for LADO's project settings routes (docs/LJY-PRD/TDD.md §13).
// They live here rather than in dto.go to keep LADO out of upstream's hotspot.

// LadoProjectIDParam names the project in /api/v1/lado/projects routes.
type LadoProjectIDParam struct {
	ProjectID string `path:"projectId" description:"Project whose LADO settings are read or changed."`
}

// LadoProjectSettingsResponse is a project's LADO settings.
type LadoProjectSettingsResponse struct {
	ProjectID string `json:"projectId"`
	LadoMode  bool   `json:"ladoMode" description:"Whether the project's workers are under LADO's worker gate."`
}

// UpdateLadoProjectSettingsRequest changes a project's LADO settings.
type UpdateLadoProjectSettingsRequest struct {
	LadoMode *bool `json:"ladoMode" description:"Put the project's workers under LADO's worker gate. Changing it needs AO_LADO_DEV=on until LADO's dispatcher exists."`
}
