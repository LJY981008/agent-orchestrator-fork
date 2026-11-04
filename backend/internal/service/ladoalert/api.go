package ladoalert

// The watch settings and alerts the API serves (plan step 17). A settings row
// holds nulls: a project's null follows the global row, and the global row's
// null the built-in default (domain.LadoWatchDefaults). The settings apply
// from each session's next start.

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Alert list limits.
const (
	DefaultAlertLimit = 100
	MaxAlertLimit     = 500
)

// SettingsView is one settings row and what it resolves to.
type SettingsView struct {
	// Scope is the project, domain.LadoWatchGlobalScope for the global row.
	Scope domain.ProjectID
	// Values is the row as stored; a nil value follows Inherited.
	Values domain.LadoWatchOverrides
	// Inherited is what a nil value follows: the global row over the defaults
	// for a project, the defaults for the global row.
	Inherited domain.LadoWatchSettings
	// Effective is the settings the scope runs with.
	Effective domain.LadoWatchSettings
}

// GlobalSettings reads the global row.
func (s *Service) GlobalSettings(ctx context.Context) (SettingsView, error) {
	return s.settingsView(ctx, domain.LadoWatchGlobalScope)
}

// UpdateGlobalSettings replaces the global row.
func (s *Service) UpdateGlobalSettings(ctx context.Context, values domain.LadoWatchOverrides) (SettingsView, error) {
	return s.putSettings(ctx, domain.LadoWatchGlobalScope, values)
}

// ProjectSettings reads a project's row.
func (s *Service) ProjectSettings(ctx context.Context, project domain.ProjectID) (SettingsView, error) {
	if err := s.requireProject(ctx, project); err != nil {
		return SettingsView{}, err
	}
	return s.settingsView(ctx, project)
}

// UpdateProjectSettings replaces a project's row.
func (s *Service) UpdateProjectSettings(ctx context.Context, project domain.ProjectID, values domain.LadoWatchOverrides) (SettingsView, error) {
	if err := s.requireProject(ctx, project); err != nil {
		return SettingsView{}, err
	}
	return s.putSettings(ctx, project, values)
}

func (s *Service) putSettings(ctx context.Context, scope domain.ProjectID, values domain.LadoWatchOverrides) (SettingsView, error) {
	if err := values.Validate(); err != nil {
		return SettingsView{}, InvalidSettings(err)
	}
	values.UpdatedAt = s.deps.Clock().UTC()
	if err := s.deps.Store.PutLadoWatchSettings(ctx, scope, values); err != nil {
		if errors.Is(err, domain.ErrLadoWatchSettingOutOfRange) {
			return SettingsView{}, InvalidSettings(err)
		}
		s.deps.Log.Warn("LADO could not store the watch settings", "scope", scope, "err", err)
		return SettingsView{}, apierr.Internal("LADO_WATCH_SETTINGS_SAVE_FAILED", "Failed to save the watch settings")
	}
	return s.settingsView(ctx, scope)
}

// InvalidSettings is the 400 of a settings value outside its range.
func InvalidSettings(err error) error {
	return apierr.Invalid("LADO_WATCH_SETTING_OUT_OF_RANGE", err.Error(), nil)
}

func (s *Service) settingsView(ctx context.Context, scope domain.ProjectID) (SettingsView, error) {
	failed := func(err error) (SettingsView, error) {
		s.deps.Log.Warn("LADO could not read the watch settings", "scope", scope, "err", err)
		return SettingsView{}, apierr.Internal("LADO_WATCH_SETTINGS_LOAD_FAILED", "Failed to load the watch settings")
	}
	global, _, err := s.deps.Store.LadoWatchSettings(ctx, domain.LadoWatchGlobalScope)
	if err != nil {
		return failed(err)
	}
	if scope == domain.LadoWatchGlobalScope {
		return SettingsView{
			Scope:     scope,
			Values:    global,
			Inherited: domain.LadoWatchDefaults(),
			Effective: domain.LadoWatchEffective(global, domain.LadoWatchOverrides{}),
		}, nil
	}
	own, _, err := s.deps.Store.LadoWatchSettings(ctx, scope)
	if err != nil {
		return failed(err)
	}
	return SettingsView{
		Scope:     scope,
		Values:    own,
		Inherited: domain.LadoWatchEffective(global, domain.LadoWatchOverrides{}),
		Effective: domain.LadoWatchEffective(global, own),
	}, nil
}

func (s *Service) requireProject(ctx context.Context, project domain.ProjectID) error {
	if project == "" {
		return apierr.Invalid("LADO_PROJECT_REQUIRED", "Name the project.", nil)
	}
	_, ok, err := s.deps.Store.GetProject(ctx, string(project))
	if err != nil {
		s.deps.Log.Warn("LADO could not read a project", "project", project, "err", err)
		return apierr.Internal("LADO_PROJECT_LOAD_FAILED", "Failed to load the project")
	}
	if !ok {
		return apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	return nil
}

// AlertQuery selects a project's alerts.
type AlertQuery struct {
	Project domain.ProjectID
	// Task narrows the list to one of the project's tasks.
	Task domain.LadoTaskID
	// UnseenOnly leaves out the alerts the owner marked seen.
	UnseenOnly bool
	// Limit caps the list; 0 is DefaultAlertLimit, and more than
	// MaxAlertLimit is refused.
	Limit int
}

// ProjectAlerts lists a project's alerts, newest first.
func (s *Service) ProjectAlerts(ctx context.Context, q AlertQuery) ([]domain.LadoAlert, error) {
	if q.Limit == 0 {
		q.Limit = DefaultAlertLimit
	}
	if q.Limit < 1 || q.Limit > MaxAlertLimit {
		return nil, apierr.Invalid("LADO_ALERT_LIMIT_INVALID", "limit must be 1 to 500.", map[string]any{"limit": q.Limit})
	}
	if err := s.requireProject(ctx, q.Project); err != nil {
		return nil, err
	}
	failed := func(err error) ([]domain.LadoAlert, error) {
		s.deps.Log.Warn("LADO could not list alerts", "project", q.Project, "task", q.Task, "err", err)
		return nil, apierr.Internal("LADO_ALERTS_LOAD_FAILED", "Failed to load the alerts")
	}
	if q.Task == "" {
		alerts, err := s.deps.Store.ListLadoAlertsByProject(ctx, q.Project, q.UnseenOnly, q.Limit)
		if err != nil {
			return failed(err)
		}
		return alerts, nil
	}
	task, ok, err := s.deps.Store.LadoTask(ctx, q.Task)
	if err != nil {
		return failed(err)
	}
	if !ok || task.ProjectID != q.Project {
		return nil, apierr.NotFound("LADO_TASK_NOT_FOUND", "Unknown task")
	}
	all, err := s.deps.Store.ListLadoAlertsByTask(ctx, q.Task)
	if err != nil {
		return failed(err)
	}
	alerts := make([]domain.LadoAlert, 0, min(len(all), q.Limit))
	for _, a := range all {
		if q.UnseenOnly && a.Seen() {
			continue
		}
		alerts = append(alerts, a)
		if len(alerts) == q.Limit {
			break
		}
	}
	return alerts, nil
}

// UnseenAlertCounts counts each of a project's tasks' unseen alerts, over all
// of them, so the board's badges are right however many there are.
func (s *Service) UnseenAlertCounts(ctx context.Context, project domain.ProjectID) (map[domain.LadoTaskID]int, error) {
	if err := s.requireProject(ctx, project); err != nil {
		return nil, err
	}
	counts, err := s.deps.Store.LadoUnseenAlertCounts(ctx, project)
	if err != nil {
		s.deps.Log.Warn("LADO could not count unseen alerts", "project", project, "err", err)
		return nil, apierr.Internal("LADO_ALERTS_LOAD_FAILED", "Failed to load the alerts")
	}
	return counts, nil
}

// MarkSeen marks an alert seen and returns it. An alert seen before keeps
// its first time.
func (s *Service) MarkSeen(ctx context.Context, id domain.LadoAlertID) (domain.LadoAlert, error) {
	failed := func(err error) (domain.LadoAlert, error) {
		s.deps.Log.Warn("LADO could not mark an alert seen", "alert", id, "err", err)
		return domain.LadoAlert{}, apierr.Internal("LADO_ALERT_SEEN_FAILED", "Failed to mark the alert seen")
	}
	if id < 1 {
		return domain.LadoAlert{}, apierr.NotFound("LADO_ALERT_NOT_FOUND", "Unknown alert")
	}
	if _, err := s.deps.Store.MarkLadoAlertSeen(ctx, id, s.deps.Clock().UTC()); err != nil {
		return failed(err)
	}
	alert, ok, err := s.deps.Store.LadoAlert(ctx, id)
	if err != nil {
		return failed(err)
	}
	if !ok {
		return domain.LadoAlert{}, apierr.NotFound("LADO_ALERT_NOT_FOUND", "Unknown alert")
	}
	return alert, nil
}
