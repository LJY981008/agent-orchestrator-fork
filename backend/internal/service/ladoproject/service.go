// Package ladoproject owns LADO's per-project settings (docs/LJY-PRD/TDD.md
// §4.2 and §13).
package ladoproject

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
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
	// SetLadoProjectCheckCommands replaces the commands while they still
	// hash to base, else fails with domain.ErrLadoCheckCommandsChanged.
	SetLadoProjectCheckCommands(ctx context.Context, projectID domain.ProjectID, base string, cmds []domain.LadoCheckCommand, proof *domain.LadoPresenceProof, audit domain.LadoAuditEvent) error
	SetLadoProjectCheckTimeout(ctx context.Context, projectID domain.ProjectID, minutes int, audit domain.LadoAuditEvent) error
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
	// onLadoModeOn is told the project whose LADO mode was turned on.
	onLadoModeOn atomic.Pointer[func(domain.ProjectID)]
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	return &Service{deps: deps}
}

// SetOnLadoModeOn sets what is told, after the change is stored, the project
// whose LADO mode was turned on: the verification loop, which reads the
// protection of the project's base branches again (docs/LJY-PRD/TDD.md
// §7.1). It must not block. nil tells nobody.
func (s *Service) SetOnLadoModeOn(fn func(domain.ProjectID)) {
	if fn == nil {
		s.onLadoModeOn.Store(nil)
		return
	}
	s.onLadoModeOn.Store(&fn)
}

// UpdateInput is a settings change. It needs at least one field; a nil
// field keeps its value.
type UpdateInput struct {
	LadoMode *bool
	// MaxConcurrent caps the executions that run at once in the project, 1
	// to domain.LadoMaxConcurrent.
	MaxConcurrent *int
	// CheckCommands replaces the commands LADO runs on each PR head in the
	// execution's worktree (docs/LJY-PRD/TDD.md §8.2), valid as
	// domain.ValidateLadoCheckCommands reads them. CheckCommandsBase is the
	// hash of the list the change started from
	// (domain.LadoCheckCommandsSHA256); a change of the list needs it and is
	// refused once the list moved on (U7 of #15).
	CheckCommands     *[]domain.LadoCheckCommand
	CheckCommandsBase string
	// CheckTimeoutMinutes is each check command's time limit,
	// domain.LadoCheckTimeoutMin to domain.LadoCheckTimeoutMax.
	CheckTimeoutMinutes *int
	// Presence checks the request's human-presence proof for an action. The
	// service calls it only when the change turns LADO mode off or changes
	// the check commands, which the daemon runs outside any agent sandbox
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
// executions, its check time limit and its check commands, in that order: a
// refused change leaves the ones after it as they were, and one that fails
// to save after others leaves those. Every value is checked before anything
// is written, and so is the presence a change of the check commands needs.
// Sending a current value changes nothing. The cap and the time limit change
// without presence, audited as the API's.
func (s *Service) UpdateSettings(ctx context.Context, projectID domain.ProjectID, in UpdateInput) (domain.LadoProjectSettings, error) {
	if in.LadoMode == nil && in.MaxConcurrent == nil && in.CheckCommands == nil && in.CheckTimeoutMinutes == nil {
		return domain.LadoProjectSettings{}, apierr.Invalid("LADO_SETTINGS_EMPTY", "Send ladoMode, maxConcurrent, checkTimeoutMinutes or checkCommands.", nil)
	}
	if err := validate(in); err != nil {
		return domain.LadoProjectSettings{}, err
	}
	project, err := s.project(ctx, projectID)
	if err != nil {
		return domain.LadoProjectSettings{}, err
	}
	current, err := s.current(ctx, projectID)
	if err != nil {
		return domain.LadoProjectSettings{}, err
	}
	var commandsProof *domain.LadoPresenceProof
	if in.CheckCommands != nil && !slices.Equal(*in.CheckCommands, current.CheckCommands) {
		if commandsProof, err = checkCommandsPresence(projectID, in.Presence); err != nil {
			return domain.LadoProjectSettings{}, err
		}
		if err := checkCommandsBase(in.CheckCommandsBase, current.CheckCommands); err != nil {
			return domain.LadoProjectSettings{}, err
		}
	}
	if in.LadoMode != nil && *in.LadoMode != current.LadoMode {
		if current, err = s.setMode(ctx, project, *in.LadoMode, in); err != nil {
			return domain.LadoProjectSettings{}, err
		}
	}
	wrote := false
	if in.MaxConcurrent != nil && *in.MaxConcurrent != current.MaxConcurrent {
		if err := s.setMaxConcurrent(ctx, projectID, current.MaxConcurrent, *in.MaxConcurrent); err != nil {
			return domain.LadoProjectSettings{}, err
		}
		wrote = true
	}
	if in.CheckTimeoutMinutes != nil && *in.CheckTimeoutMinutes != current.CheckTimeoutMinutes {
		if err := s.setCheckTimeout(ctx, projectID, current.CheckTimeoutMinutes, *in.CheckTimeoutMinutes); err != nil {
			return domain.LadoProjectSettings{}, err
		}
		wrote = true
	}
	if commandsProof != nil {
		if err := s.setCheckCommands(ctx, projectID, current.CheckCommands, *in.CheckCommands, in.CheckCommandsBase, *commandsProof); err != nil {
			return domain.LadoProjectSettings{}, err
		}
		wrote = true
	}
	if wrote {
		return s.current(ctx, projectID)
	}
	return current, nil
}

// validate refuses an invalid value before anything is read or written.
func validate(in UpdateInput) error {
	if n := in.MaxConcurrent; n != nil && (*n < 1 || *n > domain.LadoMaxConcurrent) {
		return apierr.Invalid("LADO_DISPATCH_SETTINGS_INVALID",
			fmt.Sprintf("maxConcurrent must be 1 to %d, got %d", domain.LadoMaxConcurrent, *n), map[string]any{"min": 1, "max": domain.LadoMaxConcurrent})
	}
	if n := in.CheckTimeoutMinutes; n != nil && (*n < domain.LadoCheckTimeoutMin || *n > domain.LadoCheckTimeoutMax) {
		return apierr.Invalid("LADO_CHECK_COMMANDS_INVALID",
			fmt.Sprintf("checkTimeoutMinutes must be %d to %d, got %d", domain.LadoCheckTimeoutMin, domain.LadoCheckTimeoutMax, *n),
			map[string]any{"index": -1, "field": "checkTimeoutMinutes", "reason": domain.LadoCheckCommandInvalid})
	}
	if in.CheckCommands != nil {
		if problem := domain.ValidateLadoCheckCommands(*in.CheckCommands); problem != nil {
			return checkCommandsInvalid(problem)
		}
	}
	return nil
}

// checkCommandsInvalid is the 400 of a check command list: the command's
// index, the field and the reason, as the settings screen shows them, and
// for a control character its position and the character.
func checkCommandsInvalid(problem *domain.LadoCheckCommandProblem) error {
	details := map[string]any{"index": problem.Index, "field": problem.Field, "reason": problem.Reason}
	switch problem.Reason {
	case domain.LadoCheckCommandTooMany:
		return apierr.Invalid("LADO_CHECK_COMMANDS_INVALID",
			fmt.Sprintf("A project has at most %d check commands.", domain.LadoCheckCommandsMax), details)
	case domain.LadoCheckCommandControl, domain.LadoCheckCommandBidi:
		details["position"], details["character"] = problem.Position, problem.Character
		return apierr.Invalid("LADO_CHECK_COMMANDS_INVALID",
			fmt.Sprintf("Check command %d has the control character %s at character %d. Write each command on one line, as the desktop app shows it.",
				problem.Index+1, problem.Character, problem.Position), details)
	}
	what := map[string]string{
		domain.LadoCheckCommandRequired:  "is missing",
		domain.LadoCheckCommandTooLong:   "is too long",
		domain.LadoCheckCommandInvalid:   "has characters it cannot have",
		domain.LadoCheckCommandDuplicate: "is the name of an earlier command",
	}[problem.Reason]
	return apierr.Invalid("LADO_CHECK_COMMANDS_INVALID",
		fmt.Sprintf("Check command %d: the %s %s.", problem.Index+1, problem.Field, what), details)
}

// checkCommandsBase refuses a change of the check commands that does not
// start from the current list: without the hash of the list it started
// from, or with the hash of another one.
func checkCommandsBase(base string, current []domain.LadoCheckCommand) error {
	if base == "" {
		return apierr.Invalid("LADO_CHECK_COMMANDS_INVALID", "Send checkCommandsBase, the checkCommandsSha256 of the list the change starts from.",
			map[string]any{"index": -1, "field": "checkCommandsBase", "reason": domain.LadoCheckCommandRequired})
	}
	if !strings.EqualFold(base, domain.LadoCheckCommandsSHA256(current)) {
		return checkCommandsChanged(current)
	}
	return nil
}

func checkCommandsChanged(current []domain.LadoCheckCommand) error {
	return apierr.Conflict("LADO_CHECK_COMMANDS_CHANGED",
		"The project's check commands changed since you opened them. Review the current list, then change it again.",
		map[string]any{"checkCommandsSha256": domain.LadoCheckCommandsSHA256(current)})
}

// checkCommandsPresence asks for the presence a change of the check
// commands needs, with the project as its subject.
func checkCommandsPresence(projectID domain.ProjectID, presence func(action, subject string) (domain.LadoPresenceProof, error)) (*domain.LadoPresenceProof, error) {
	if presence == nil {
		return nil, apierr.Forbidden("LADO_PRESENCE_REQUIRED",
			"Change the check commands in the AO desktop app. It asks you to confirm, then sends the change itself.")
	}
	proof, err := presence(domain.LadoPresenceCheckCommands, string(projectID))
	if err != nil {
		return nil, err
	}
	return &proof, nil
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
	if fn := s.onLadoModeOn.Load(); fn != nil {
		(*fn)(projectID)
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
func (s *Service) setMaxConcurrent(ctx context.Context, projectID domain.ProjectID, from, to int) error {
	if err := s.deps.Store.SetLadoProjectMaxConcurrent(ctx, projectID, to, s.apiEvent(projectID, "project.max_concurrent_changed", from, to)); err != nil {
		return apierr.Internal("LADO_SETTINGS_UPDATE_FAILED", "Failed to update LADO project settings")
	}
	return nil
}

// setCheckTimeout stores the project's check time limit with its audit
// event, the API's like the cap's.
func (s *Service) setCheckTimeout(ctx context.Context, projectID domain.ProjectID, from, to int) error {
	if err := s.deps.Store.SetLadoProjectCheckTimeout(ctx, projectID, to, s.apiEvent(projectID, "project.check_timeout_changed", from, to)); err != nil {
		return apierr.Internal("LADO_SETTINGS_UPDATE_FAILED", "Failed to update LADO project settings")
	}
	return nil
}

func (s *Service) apiEvent(projectID domain.ProjectID, action string, from, to int) domain.LadoAuditEvent {
	payload, _ := json.Marshal(map[string]any{"from": from, "to": to})
	return domain.LadoAuditEvent{
		At:          s.deps.Clock().UTC(),
		Actor:       "api",
		Action:      action,
		SubjectKind: "project",
		SubjectID:   string(projectID),
		Payload:     payload,
	}
}

// setCheckCommands stores the project's check commands with the presence
// proof that allowed the change and its audit event, which holds both lists
// whole.
func (s *Service) setCheckCommands(ctx context.Context, projectID domain.ProjectID, from, to []domain.LadoCheckCommand, base string, proof domain.LadoPresenceProof) error {
	actor := "human"
	if proof.Dev {
		actor = "human:dev-presence"
	}
	payload, _ := json.Marshal(map[string]any{
		"from":  append([]domain.LadoCheckCommand{}, from...),
		"to":    append([]domain.LadoCheckCommand{}, to...),
		"proof": proof.Nonce,
		"dev":   proof.Dev,
	})
	err := s.deps.Store.SetLadoProjectCheckCommands(ctx, projectID, base, to, &proof, domain.LadoAuditEvent{
		At:          s.deps.Clock().UTC(),
		Actor:       actor,
		Action:      "project.check_commands_changed",
		SubjectKind: "project",
		SubjectID:   string(projectID),
		Payload:     payload,
	})
	switch {
	case errors.Is(err, domain.ErrLadoPresenceReplayed):
		return ladopresence.Replayed()
	case errors.Is(err, domain.ErrLadoCheckCommandsChanged):
		// Another change landed between the read and the write.
		latest, readErr := s.current(ctx, projectID)
		if readErr != nil {
			return readErr
		}
		return checkCommandsChanged(latest.CheckCommands)
	case err != nil:
		return apierr.Internal("LADO_SETTINGS_UPDATE_FAILED", "Failed to update LADO project settings")
	}
	return nil
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
