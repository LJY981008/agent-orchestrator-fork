// Package ladoproject owns LADO's per-project settings (docs/LJY-PRD/TDD.md
// §4.2 and §13).
package ladoproject

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Store reads and writes LADO's project settings.
type Store interface {
	LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error)
	SetLadoMode(ctx context.Context, projectID domain.ProjectID, on bool, at time.Time) (domain.LadoProjectSettings, error)
}

// Projects reads AO's project registry.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// Deps wires a Service.
type Deps struct {
	Store    Store
	Projects Projects
	// ModeSwitch lets the API change a project's LADO mode. It stays off outside
	// development until LADO's dispatcher exists (build step 6), because a
	// project in LADO mode cannot start any worker before then.
	ModeSwitch bool
	// Clock stamps changes; nil uses time.Now.
	Clock func() time.Time
}

// Service reads and changes LADO's project settings.
type Service struct {
	deps Deps
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	return &Service{deps: deps}
}

// UpdateInput is a settings change. LadoMode is required.
type UpdateInput struct {
	LadoMode *bool
}

// Settings returns the project's LADO settings.
func (s *Service) Settings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error) {
	if _, err := s.project(ctx, projectID); err != nil {
		return domain.LadoProjectSettings{}, err
	}
	return s.current(ctx, projectID)
}

// UpdateSettings changes the project's LADO mode. Sending the current value
// changes nothing and needs no mode switch.
func (s *Service) UpdateSettings(ctx context.Context, projectID domain.ProjectID, in UpdateInput) (domain.LadoProjectSettings, error) {
	if in.LadoMode == nil {
		return domain.LadoProjectSettings{}, apierr.Invalid("LADO_MODE_REQUIRED", "ladoMode is required", nil)
	}
	project, err := s.project(ctx, projectID)
	if err != nil {
		return domain.LadoProjectSettings{}, err
	}
	current, err := s.current(ctx, projectID)
	if err != nil {
		return domain.LadoProjectSettings{}, err
	}
	if current.LadoMode == *in.LadoMode {
		return current, nil
	}
	if !s.deps.ModeSwitch {
		return domain.LadoProjectSettings{}, apierr.Conflict("LADO_MODE_UNAVAILABLE",
			"LADO mode cannot be changed yet: a project in LADO mode cannot start workers until LADO's dispatcher exists. Set AO_LADO_DEV=on on the daemon to try it.", nil)
	}
	if *in.LadoMode && project.Config.TrackerIntake.Enabled {
		return domain.LadoProjectSettings{}, apierr.Conflict("LADO_TRACKER_INTAKE_ENABLED",
			"Turn off tracker intake for this project first: LADO projects take issues by import.", nil)
	}
	updated, err := s.deps.Store.SetLadoMode(ctx, projectID, *in.LadoMode, s.deps.Clock())
	if err != nil {
		return domain.LadoProjectSettings{}, apierr.Internal("LADO_SETTINGS_UPDATE_FAILED", "Failed to update LADO project settings")
	}
	return updated, nil
}

func (s *Service) project(ctx context.Context, projectID domain.ProjectID) (domain.ProjectRecord, error) {
	project, ok, err := s.deps.Projects.GetProject(ctx, string(projectID))
	if err != nil {
		return domain.ProjectRecord{}, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok || !project.ArchivedAt.IsZero() {
		return domain.ProjectRecord{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	return project, nil
}

func (s *Service) current(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error) {
	settings, err := s.deps.Store.LadoProjectSettings(ctx, projectID)
	if err != nil {
		return domain.LadoProjectSettings{}, apierr.Internal("LADO_SETTINGS_LOAD_FAILED", "Failed to load LADO project settings")
	}
	return settings, nil
}
