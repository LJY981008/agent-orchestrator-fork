// Package ladoproject owns LADO's per-project settings (docs/LJY-PRD/TDD.md
// §4.2 and §13).
package ladoproject

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
)

// Store reads and writes LADO's project settings.
type Store interface {
	LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error)
	SetLadoMode(ctx context.Context, projectID domain.ProjectID, on bool, at time.Time) (domain.LadoProjectSettings, error)
	SetLadoModeWithProof(ctx context.Context, projectID domain.ProjectID, on bool, at time.Time, proof domain.LadoPresenceProof, event domain.LadoAuditEvent) (domain.LadoProjectSettings, error)
	SetLadoProjectMaxConcurrent(ctx context.Context, projectID domain.ProjectID, n int, event domain.LadoAuditEvent) error
}

// Projects reads AO's project registry.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// Deps wires a Service.
type Deps struct {
	Store    Store
	Projects Projects
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

// UpdateInput is a settings change. It needs LadoMode, MaxConcurrent or both;
// a nil field keeps its value.
type UpdateInput struct {
	LadoMode *bool
	// MaxConcurrent caps the executions that run at once in the project, 1
	// to domain.LadoMaxConcurrent.
	MaxConcurrent *int
	// Presence checks the request's human-presence proof for an action. The
	// service calls it only when the change turns LADO mode off, the one
	// settings change that needs presence from step 4b
	// (docs/LJY-PRD/TDD.md §13).
	Presence func(action, subject string) (domain.LadoPresenceProof, error)
}

// Settings returns the project's LADO settings.
func (s *Service) Settings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error) {
	if _, err := s.project(ctx, projectID); err != nil {
		return domain.LadoProjectSettings{}, err
	}
	return s.current(ctx, projectID)
}

// UpdateSettings changes the project's LADO mode, its cap on running
// executions, or both. The mode goes first, so a refused mode change leaves
// the cap as it was; a cap that fails to save after it leaves the new mode.
// Sending a current value changes nothing. The cap changes without presence,
// audited as the API's.
func (s *Service) UpdateSettings(ctx context.Context, projectID domain.ProjectID, in UpdateInput) (domain.LadoProjectSettings, error) {
	if in.LadoMode == nil && in.MaxConcurrent == nil {
		return domain.LadoProjectSettings{}, apierr.Invalid("LADO_SETTINGS_EMPTY", "Send ladoMode, maxConcurrent or both.", nil)
	}
	if n := in.MaxConcurrent; n != nil && (*n < 1 || *n > domain.LadoMaxConcurrent) {
		return domain.LadoProjectSettings{}, apierr.Invalid("LADO_DISPATCH_SETTINGS_INVALID",
			fmt.Sprintf("maxConcurrent must be 1 to %d, got %d", domain.LadoMaxConcurrent, *n), map[string]any{"min": 1, "max": domain.LadoMaxConcurrent})
	}
	project, err := s.project(ctx, projectID)
	if err != nil {
		return domain.LadoProjectSettings{}, err
	}
	current, err := s.current(ctx, projectID)
	if err != nil {
		return domain.LadoProjectSettings{}, err
	}
	if in.LadoMode != nil && *in.LadoMode != current.LadoMode {
		if current, err = s.setMode(ctx, project, *in.LadoMode, in); err != nil {
			return domain.LadoProjectSettings{}, err
		}
	}
	if in.MaxConcurrent != nil && *in.MaxConcurrent != current.MaxConcurrent {
		return s.setMaxConcurrent(ctx, projectID, current.MaxConcurrent, *in.MaxConcurrent)
	}
	return current, nil
}

// setMode turns LADO mode on or off. Turning it on refuses while tracker
// intake is on, because LADO projects take issues by import.
func (s *Service) setMode(ctx context.Context, project domain.ProjectRecord, on bool, in UpdateInput) (domain.LadoProjectSettings, error) {
	projectID := domain.ProjectID(project.ID)
	if !on {
		return s.turnOff(ctx, projectID, in)
	}
	if project.Config.TrackerIntake.Enabled {
		return domain.LadoProjectSettings{}, apierr.Conflict("LADO_TRACKER_INTAKE_ENABLED",
			"Turn off tracker intake for this project first: LADO projects take issues by import.", nil)
	}
	updated, err := s.deps.Store.SetLadoMode(ctx, projectID, true, s.deps.Clock())
	if err != nil {
		return domain.LadoProjectSettings{}, apierr.Internal("LADO_SETTINGS_UPDATE_FAILED", "Failed to update LADO project settings")
	}
	return updated, nil
}

// turnOff turns LADO mode off with a human-presence proof, recorded with its
// audit event. Turning it off would let workers start without approved
// plans, so an agent must not be able to do it.
func (s *Service) turnOff(ctx context.Context, projectID domain.ProjectID, in UpdateInput) (domain.LadoProjectSettings, error) {
	if in.Presence == nil {
		return domain.LadoProjectSettings{}, apierr.Forbidden("LADO_PRESENCE_REQUIRED", "Turn LADO mode off in the AO desktop app. It asks you to confirm.")
	}
	proof, err := in.Presence(domain.LadoPresenceModeOff, string(projectID))
	if err != nil {
		return domain.LadoProjectSettings{}, err
	}
	now := s.deps.Clock().UTC()
	actor := "human"
	if proof.Dev {
		actor = "human:dev-presence"
	}
	payload, _ := json.Marshal(map[string]any{"proof": proof.Nonce, "dev": proof.Dev})
	updated, err := s.deps.Store.SetLadoModeWithProof(ctx, projectID, false, now, proof, domain.LadoAuditEvent{
		At:          now,
		Actor:       actor,
		Action:      "project.lado_mode_off",
		SubjectKind: "project",
		SubjectID:   string(projectID),
		Payload:     payload,
	})
	if errors.Is(err, domain.ErrLadoPresenceReplayed) {
		return domain.LadoProjectSettings{}, ladopresence.Replayed()
	}
	if err != nil {
		return domain.LadoProjectSettings{}, apierr.Internal("LADO_SETTINGS_UPDATE_FAILED", "Failed to update LADO project settings")
	}
	return updated, nil
}

// setMaxConcurrent stores the project's cap with its audit event. Nothing
// verifies who sent the change, so the event's actor is "api".
func (s *Service) setMaxConcurrent(ctx context.Context, projectID domain.ProjectID, from, to int) (domain.LadoProjectSettings, error) {
	now := s.deps.Clock().UTC()
	payload, _ := json.Marshal(map[string]any{"from": from, "to": to})
	if err := s.deps.Store.SetLadoProjectMaxConcurrent(ctx, projectID, to, domain.LadoAuditEvent{
		At:          now,
		Actor:       "api",
		Action:      "project.max_concurrent_changed",
		SubjectKind: "project",
		SubjectID:   string(projectID),
		Payload:     payload,
	}); err != nil {
		return domain.LadoProjectSettings{}, apierr.Internal("LADO_SETTINGS_UPDATE_FAILED", "Failed to update LADO project settings")
	}
	return s.current(ctx, projectID)
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
