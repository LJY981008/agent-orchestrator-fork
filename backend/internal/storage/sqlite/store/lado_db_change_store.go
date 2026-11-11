package store

// LADO fork: production database changes tied to the integration approval
// (issue #97, docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md).
// The store keeps each repository's settings, the runs that apply a PR head's
// SQL files, every file's application with the baseline rows, and the
// integration approvals whose lado/integration status waits for their
// pre-merge SQL. These methods are the ladodbchange.Store contract of the
// plan; secrets and command output never reach this store.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// errLadoDBChangeRunMoved rolls back a run transition whose run is not in a
// state it may move from; the caller reports false.
var errLadoDBChangeRunMoved = errors.New("the database change run is not in a state it may move from")

// LadoDBChangeSettings lists a project's stored settings, one per
// repository, in repository order. A repository without a row has none.
func (s *Store) LadoDBChangeSettings(ctx context.Context, project domain.ProjectID) ([]domain.LadoDBChangeSettings, error) {
	rows, err := s.qr.ListLadoDBChangeSettings(ctx, string(project))
	if err != nil {
		return nil, fmt.Errorf("read LADO database change settings: %w", err)
	}
	settings := make([]domain.LadoDBChangeSettings, 0, len(rows))
	for _, row := range rows {
		setting, err := ladoDBChangeSettingsFromGen(row)
		if err != nil {
			return nil, err
		}
		settings = append(settings, setting)
	}
	return settings, nil
}

// LadoDBChangeSetting reads one repository's settings; false when it has
// none.
func (s *Store) LadoDBChangeSetting(ctx context.Context, project domain.ProjectID, repo string) (domain.LadoDBChangeSettings, bool, error) {
	return ladoDBChangeSettingTx(ctx, s.qr, project, repo)
}

func ladoDBChangeSettingTx(ctx context.Context, q *gen.Queries, project domain.ProjectID, repo string) (domain.LadoDBChangeSettings, bool, error) {
	row, err := q.GetLadoDBChangeSetting(ctx, gen.GetLadoDBChangeSettingParams{ProjectID: string(project), Repo: repo})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoDBChangeSettings{}, false, nil
	}
	if err != nil {
		return domain.LadoDBChangeSettings{}, false, fmt.Errorf("read LADO database change setting: %w", err)
	}
	setting, err := ladoDBChangeSettingsFromGen(row)
	return setting, err == nil, err
}

// SetLadoDBChangeSettings stores one repository's settings in one
// transaction with the presence proof that allowed the change when one is
// given, the baseline rows and the audit events, in that order. The stored
// settings must still hash to baseSHA256 (domain.LadoDBChangeSettings.SHA256,
// "" for a repository without settings), else the change is refused with
// domain.ErrLadoDBChangeSettingsChanged and nothing is kept; a proof used
// before is refused with domain.ErrLadoPresenceReplayed.
//
// UpdatedAt is the change's time; when zero, the first audit event's time.
// A zero EnabledAt keeps the stored one, and settings turned on without
// either take UpdatedAt. Baseline rows get an id when they have none and the
// settings' project and repository when theirs are empty; their result is
// written as given (domain.LadoDBChangeResultBaseline). The caller validates
// the settings (domain.ValidateLadoDBChangeSettings).
func (s *Store) SetLadoDBChangeSettings(ctx context.Context, settings domain.LadoDBChangeSettings, baseSHA256 string,
	baseline []domain.LadoDBChangeApplication, proof *domain.LadoPresenceProof, audit []domain.LadoAuditEvent,
) error {
	if settings.ProjectID == "" {
		return errors.New("set LADO database change settings: a project is required")
	}
	updated := settings.UpdatedAt
	if updated.IsZero() && len(audit) > 0 {
		updated = audit[0].At
	}
	if updated.IsZero() {
		return errors.New("set LADO database change settings: a time is required")
	}
	paths, err := json.Marshal(ladoStringsOrEmpty(settings.Paths))
	if err != nil {
		return fmt.Errorf("set LADO database change settings: encode paths: %w", err)
	}
	secrets, err := json.Marshal(ladoStringsOrEmpty(settings.SecretNames))
	if err != nil {
		return fmt.Errorf("set LADO database change settings: encode secret names: %w", err)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err = s.inTx(ctx, "set LADO database change settings", func(q *gen.Queries) error {
		stored, ok, err := ladoDBChangeSettingTx(ctx, q, settings.ProjectID, settings.Repo)
		if err != nil {
			return err
		}
		storedSHA := ""
		if ok {
			storedSHA = stored.SHA256()
		}
		if !strings.EqualFold(baseSHA256, storedSHA) {
			return domain.ErrLadoDBChangeSettingsChanged
		}
		if err := insertLadoPresenceProofIfAny(ctx, q, proof); err != nil {
			return err
		}
		enabledAt := settings.EnabledAt
		if enabledAt.IsZero() && ok {
			enabledAt = stored.EnabledAt
		}
		if enabledAt.IsZero() && settings.Enabled {
			enabledAt = updated
		}
		if err := q.UpsertLadoDBChangeSetting(ctx, gen.UpsertLadoDBChangeSettingParams{
			ProjectID:       string(settings.ProjectID),
			Repo:            settings.Repo,
			Enabled:         ladoBool(settings.Enabled),
			PathsJson:       string(paths),
			Command:         settings.Command,
			TimeoutMinutes:  int64(settings.TimeoutMinutes),
			SecretNamesJson: string(secrets),
			BaselineCommit:  settings.BaselineCommit,
			EnabledAt:       ladoNullTime(enabledAt),
			UpdatedAt:       updated.UTC(),
		}); err != nil {
			return err
		}
		for _, app := range baseline {
			if app.ID == "" {
				app.ID = "lado-dbapp-" + uuid.NewString()
			}
			if app.ProjectID == "" {
				app.ProjectID = settings.ProjectID
			}
			if app.Repo == "" {
				app.Repo = settings.Repo
			}
			if app.StartedAt.IsZero() {
				app.StartedAt = updated
			}
			if err := insertLadoDBChangeApplication(ctx, q, app); err != nil {
				return fmt.Errorf("baseline %s: %w", app.Path, err)
			}
		}
		for _, event := range audit {
			if _, err := appendLadoAuditEventTx(ctx, q, event); err != nil {
				return err
			}
		}
		return nil
	})
	return unwrapLadoDBChangeSentinel(err)
}

// LadoDBChangeAppliedPaths returns, per path, the latest application of a
// repository's file that makes it applied (an applied or baseline row);
// paths without one are missing from the map. No paths reads every path of
// the repository.
func (s *Store) LadoDBChangeAppliedPaths(ctx context.Context, project domain.ProjectID, repo string, paths []string) (map[string]domain.LadoDBChangeApplication, error) {
	apps, err := s.ListLadoDBChangeApplications(ctx, project, repo, paths)
	if err != nil {
		return nil, err
	}
	applied := map[string]domain.LadoDBChangeApplication{}
	// Oldest first, so the last one kept per path is the latest.
	for _, app := range apps {
		if app.Result.CountsApplied() {
			applied[app.Path] = app
		}
	}
	return applied, nil
}

// ListLadoDBChangeApplications lists a repository's applications and baseline
// rows of the given paths, oldest first; no paths lists every path.
func (s *Store) ListLadoDBChangeApplications(ctx context.Context, project domain.ProjectID, repo string, paths []string) ([]domain.LadoDBChangeApplication, error) {
	var rows []gen.LadoDbChangeApplication
	var err error
	if len(paths) == 0 {
		rows, err = s.qr.ListLadoDBChangeApplications(ctx, gen.ListLadoDBChangeApplicationsParams{ProjectID: string(project), Repo: repo})
	} else {
		rows, err = s.qr.ListLadoDBChangeApplicationsByPath(ctx, gen.ListLadoDBChangeApplicationsByPathParams{
			ProjectID: string(project), Repo: repo, Paths: paths,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("read LADO database change applications: %w", err)
	}
	apps := make([]domain.LadoDBChangeApplication, 0, len(rows))
	for _, row := range rows {
		apps = append(apps, ladoDBChangeApplicationFromGen(row))
	}
	return apps, nil
}

// StartLadoDBChangeApplication records an application before its command
// runs. It needs an id, a project, a path, a blob and a start time; an empty
// result is written as running.
func (s *Store) StartLadoDBChangeApplication(ctx context.Context, app domain.LadoDBChangeApplication) error {
	if app.ID == "" || app.StartedAt.IsZero() {
		return errors.New("start LADO database change application: an id and a start time are required")
	}
	if app.Result == "" {
		app.Result = domain.LadoDBChangeResultRunning
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if err := insertLadoDBChangeApplication(ctx, s.qw, app); err != nil {
		return fmt.Errorf("start LADO database change application: %w", err)
	}
	return nil
}

// FinishLadoDBChangeApplication ends a running application with its result
// (applied, failed or interrupted), exit code, output blob and message, and
// appends its audit event, in one transaction; an event without an action is
// not written. An unknown application is domain.ErrLadoDBChangeNotFound; one
// that is no longer running is refused and nothing is written.
func (s *Store) FinishLadoDBChangeApplication(ctx context.Context, id string, result domain.LadoDBChangeResult, exitCode *int,
	outputSHA256, message string, at time.Time, audit domain.LadoAuditEvent,
) error {
	switch result {
	case domain.LadoDBChangeResultApplied, domain.LadoDBChangeResultFailed, domain.LadoDBChangeResultInterrupted:
	default:
		return fmt.Errorf("finish LADO database change application: result %q does not end an application", result)
	}
	if at.IsZero() {
		return errors.New("finish LADO database change application: a time is required")
	}
	exit := sql.NullInt64{}
	if exitCode != nil {
		exit = sql.NullInt64{Int64: int64(*exitCode), Valid: true}
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "finish LADO database change application", func(q *gen.Queries) error {
		n, err := q.FinishLadoDBChangeApplication(ctx, gen.FinishLadoDBChangeApplicationParams{
			Result:       string(result),
			ExitCode:     exit,
			OutputSha256: outputSHA256,
			Message:      message,
			FinishedAt:   ladoNullTime(at),
			ID:           id,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			current, err := q.GetLadoDBChangeApplication(ctx, id)
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrLadoDBChangeNotFound
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("application %s is %s, not running", id, current.Result)
		}
		if audit.Action == "" {
			return nil
		}
		_, err = appendLadoAuditEventTx(ctx, q, audit)
		return err
	})
	return unwrapLadoDBChangeSentinel(err)
}

// InterruptLadoDBChangeApplications marks every application a stopped daemon
// left running as interrupted at at, and returns how many it marked.
func (s *Store) InterruptLadoDBChangeApplications(ctx context.Context, at time.Time) (int, error) {
	if at.IsZero() {
		return 0, errors.New("interrupt LADO database change applications: a time is required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return 0, err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.InterruptLadoDBChangeApplications(ctx, ladoNullTime(at))
	if err != nil {
		return 0, fmt.Errorf("interrupt LADO database change applications: %w", err)
	}
	return int(n), nil
}

// EnsureLadoDBChangeRun creates run unless its PR, head and kind have one
// already, and returns the stored run with true when it created it. A run
// without an id gets one; a zero UpdatedAt takes CreatedAt, which is
// required. An existing run is returned as it is.
func (s *Store) EnsureLadoDBChangeRun(ctx context.Context, run domain.LadoDBChangeRun) (domain.LadoDBChangeRun, bool, error) {
	if run.ProjectID == "" || run.PRURL == "" || run.HeadSHA == "" || run.CreatedAt.IsZero() {
		return domain.LadoDBChangeRun{}, false, errors.New("ensure LADO database change run: a project, a PR, a head and a time are required")
	}
	if run.ID == "" {
		run.ID = "lado-dbrun-" + uuid.NewString()
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = run.CreatedAt
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoDBChangeRun{}, false, err
	}
	defer s.writeMu.Unlock()
	var stored domain.LadoDBChangeRun
	var created bool
	err := s.inTx(ctx, "ensure LADO database change run", func(q *gen.Queries) error {
		n, err := q.InsertLadoDBChangeRun(ctx, gen.InsertLadoDBChangeRunParams{
			ID:                 run.ID,
			ProjectID:          string(run.ProjectID),
			Repo:               run.Repo,
			ExecutionSessionID: string(run.ExecutionSessionID),
			TaskID:             string(run.TaskID),
			PRURL:              run.PRURL,
			HeadSha:            run.HeadSHA,
			Kind:               string(run.Kind),
			RunTrigger:         string(run.Trigger),
			State:              string(run.State),
			ApprovalID:         run.ApprovalID,
			Attempts:           int64(run.Attempts),
			Error:              run.Error,
			CreatedAt:          run.CreatedAt.UTC(),
			UpdatedAt:          run.UpdatedAt.UTC(),
		})
		if err != nil {
			return err
		}
		created = n == 1
		row, err := q.GetLadoDBChangeRunByHead(ctx, gen.GetLadoDBChangeRunByHeadParams{
			PRURL: run.PRURL, HeadSha: run.HeadSHA, Kind: string(run.Kind),
		})
		if err != nil {
			return err
		}
		stored = ladoDBChangeRunFromGen(row)
		return nil
	})
	if err != nil {
		return domain.LadoDBChangeRun{}, false, err
	}
	return stored, created, nil
}

// LadoDBChangeRun reads one run; false when it does not exist.
func (s *Store) LadoDBChangeRun(ctx context.Context, id string) (domain.LadoDBChangeRun, bool, error) {
	row, err := s.qr.GetLadoDBChangeRun(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoDBChangeRun{}, false, nil
	}
	if err != nil {
		return domain.LadoDBChangeRun{}, false, fmt.Errorf("read LADO database change run: %w", err)
	}
	return ladoDBChangeRunFromGen(row), true, nil
}

// TransitionLadoDBChangeRun moves a run that is in one of from to to, with
// its attempts and last error, at at, in one transaction with the presence
// proof that allowed it and its audit event when they are given. It reports
// false, writing nothing, when the run is unknown or in another state; no
// from moves the run whatever its state. A proof used before is refused with
// domain.ErrLadoPresenceReplayed.
func (s *Store) TransitionLadoDBChangeRun(ctx context.Context, id string, from []domain.LadoDBChangeRunState, to domain.LadoDBChangeRunState,
	attempts int, errMsg string, at time.Time, proof *domain.LadoPresenceProof, audit *domain.LadoAuditEvent,
) (bool, error) {
	if at.IsZero() {
		return false, errors.New("transition LADO database change run: a time is required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "transition LADO database change run", func(q *gen.Queries) error {
		if err := insertLadoPresenceProofIfAny(ctx, q, proof); err != nil {
			return err
		}
		var n int64
		var err error
		if len(from) == 0 {
			n, err = q.SetLadoDBChangeRun(ctx, gen.SetLadoDBChangeRunParams{
				ToState: string(to), Attempts: int64(attempts), Error: errMsg, UpdatedAt: at.UTC(), ID: id,
			})
		} else {
			states := make([]string, len(from))
			for i, state := range from {
				states[i] = string(state)
			}
			n, err = q.TransitionLadoDBChangeRun(ctx, gen.TransitionLadoDBChangeRunParams{
				ToState: string(to), Attempts: int64(attempts), Error: errMsg, UpdatedAt: at.UTC(), ID: id, FromStates: states,
			})
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return errLadoDBChangeRunMoved
		}
		if audit != nil {
			if _, err := appendLadoAuditEventTx(ctx, q, *audit); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errLadoDBChangeRunMoved) {
		return false, nil
	}
	if err != nil {
		return false, unwrapLadoDBChangeSentinel(err)
	}
	return true, nil
}

// ListLadoDBChangeRunsByState lists up to limit runs in the given states,
// least recently moved first; a limit of 0 or less lists them all, and no
// states lists none.
func (s *Store) ListLadoDBChangeRunsByState(ctx context.Context, states []domain.LadoDBChangeRunState, limit int) ([]domain.LadoDBChangeRun, error) {
	if len(states) == 0 {
		return []domain.LadoDBChangeRun{}, nil
	}
	names := make([]string, len(states))
	for i, state := range states {
		names[i] = string(state)
	}
	rows, err := s.qr.ListLadoDBChangeRunsByState(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("list LADO database change runs: %w", err)
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return ladoDBChangeRunsFromGen(rows), nil
}

// ListLadoDBChangeRunsForExecution lists an execution's runs, oldest first.
func (s *Store) ListLadoDBChangeRunsForExecution(ctx context.Context, session domain.SessionID) ([]domain.LadoDBChangeRun, error) {
	rows, err := s.qr.ListLadoDBChangeRunsForExecution(ctx, string(session))
	if err != nil {
		return nil, fmt.Errorf("list LADO database change runs of an execution: %w", err)
	}
	return ladoDBChangeRunsFromGen(rows), nil
}

// ListLadoDBChangeRunsForProject lists a project's runs that are not done,
// oldest first.
func (s *Store) ListLadoDBChangeRunsForProject(ctx context.Context, project domain.ProjectID) ([]domain.LadoDBChangeRun, error) {
	rows, err := s.qr.ListLadoDBChangeRunsForProject(ctx, string(project))
	if err != nil {
		return nil, fmt.Errorf("list LADO database change runs of a project: %w", err)
	}
	return ladoDBChangeRunsFromGen(rows), nil
}

// HoldLadoIntegrationStatus records that an approval's lado/integration
// status waits for its pre-merge SQL, so the status retry list skips it.
// Holding it again keeps the first time. The approval must exist.
func (s *Store) HoldLadoIntegrationStatus(ctx context.Context, approvalID string, at time.Time) error {
	if approvalID == "" || at.IsZero() {
		return errors.New("hold LADO integration status: an approval and a time are required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if err := s.qw.HoldLadoIntegrationStatus(ctx, gen.HoldLadoIntegrationStatusParams{ApprovalID: approvalID, HeldAt: at.UTC()}); err != nil {
		return fmt.Errorf("hold LADO integration status: %w", err)
	}
	return nil
}

// ReleaseLadoIntegrationStatus lets an approval's status post again; an
// approval that is not held stays as it is.
func (s *Store) ReleaseLadoIntegrationStatus(ctx context.Context, approvalID string) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if err := s.qw.ReleaseLadoIntegrationStatus(ctx, approvalID); err != nil {
		return fmt.Errorf("release LADO integration status: %w", err)
	}
	return nil
}

// LadoIntegrationStatusHeld reports whether an approval's status is held.
func (s *Store) LadoIntegrationStatusHeld(ctx context.Context, approvalID string) (bool, error) {
	n, err := s.qr.CountLadoIntegrationStatusHolds(ctx, approvalID)
	if err != nil {
		return false, fmt.Errorf("read LADO integration status hold: %w", err)
	}
	return n > 0, nil
}

func insertLadoDBChangeApplication(ctx context.Context, q *gen.Queries, app domain.LadoDBChangeApplication) error {
	exit := sql.NullInt64{}
	if app.ExitCode != nil {
		exit = sql.NullInt64{Int64: int64(*app.ExitCode), Valid: true}
	}
	finished := sql.NullTime{}
	if app.FinishedAt != nil {
		finished = ladoNullTime(*app.FinishedAt)
	}
	return q.InsertLadoDBChangeApplication(ctx, gen.InsertLadoDBChangeApplicationParams{
		ID:                 app.ID,
		ProjectID:          string(app.ProjectID),
		Repo:               app.Repo,
		Path:               app.Path,
		BlobSha:            app.BlobSHA,
		Phase:              string(app.Phase),
		Result:             string(app.Result),
		RunID:              app.RunID,
		ExecutionSessionID: string(app.ExecutionSessionID),
		TaskID:             string(app.TaskID),
		PRURL:              app.PRURL,
		HeadSha:            app.HeadSHA,
		ExitCode:           exit,
		OutputSha256:       app.OutputSHA256,
		Message:            app.Message,
		Actor:              app.Actor,
		StartedAt:          app.StartedAt.UTC(),
		FinishedAt:         finished,
	})
}

func ladoDBChangeSettingsFromGen(row gen.LadoDbChangeSetting) (domain.LadoDBChangeSettings, error) {
	paths := []string{}
	if err := json.Unmarshal([]byte(row.PathsJson), &paths); err != nil {
		return domain.LadoDBChangeSettings{}, fmt.Errorf("decode LADO database change paths: %w", err)
	}
	secrets := []string{}
	if err := json.Unmarshal([]byte(row.SecretNamesJson), &secrets); err != nil {
		return domain.LadoDBChangeSettings{}, fmt.Errorf("decode LADO database change secret names: %w", err)
	}
	return domain.LadoDBChangeSettings{
		ProjectID:      domain.ProjectID(row.ProjectID),
		Repo:           row.Repo,
		Enabled:        row.Enabled == 1,
		Paths:          paths,
		Command:        row.Command,
		TimeoutMinutes: int(row.TimeoutMinutes),
		SecretNames:    secrets,
		BaselineCommit: row.BaselineCommit,
		EnabledAt:      ladoTimeFromNull(row.EnabledAt),
		UpdatedAt:      row.UpdatedAt.UTC(),
	}, nil
}

func ladoDBChangeApplicationFromGen(row gen.LadoDbChangeApplication) domain.LadoDBChangeApplication {
	app := domain.LadoDBChangeApplication{
		ID:                 row.ID,
		ProjectID:          domain.ProjectID(row.ProjectID),
		Repo:               row.Repo,
		Path:               row.Path,
		BlobSHA:            row.BlobSha,
		Phase:              domain.LadoDBChangePhase(row.Phase),
		Result:             domain.LadoDBChangeResult(row.Result),
		RunID:              row.RunID,
		ExecutionSessionID: domain.SessionID(row.ExecutionSessionID),
		TaskID:             domain.LadoTaskID(row.TaskID),
		PRURL:              row.PRURL,
		HeadSHA:            row.HeadSha,
		OutputSHA256:       row.OutputSha256,
		Message:            row.Message,
		Actor:              row.Actor,
		StartedAt:          row.StartedAt.UTC(),
	}
	if row.ExitCode.Valid {
		code := int(row.ExitCode.Int64)
		app.ExitCode = &code
	}
	if row.FinishedAt.Valid {
		finished := row.FinishedAt.Time.UTC()
		app.FinishedAt = &finished
	}
	return app
}

func ladoDBChangeRunFromGen(row gen.LadoDbChangeRun) domain.LadoDBChangeRun {
	return domain.LadoDBChangeRun{
		ID:                 row.ID,
		ProjectID:          domain.ProjectID(row.ProjectID),
		Repo:               row.Repo,
		ExecutionSessionID: domain.SessionID(row.ExecutionSessionID),
		TaskID:             domain.LadoTaskID(row.TaskID),
		PRURL:              row.PRURL,
		HeadSHA:            row.HeadSha,
		Kind:               domain.LadoDBChangeRunKind(row.Kind),
		Trigger:            domain.LadoDBChangeTrigger(row.RunTrigger),
		State:              domain.LadoDBChangeRunState(row.State),
		ApprovalID:         row.ApprovalID,
		Attempts:           int(row.Attempts),
		Error:              row.Error,
		CreatedAt:          row.CreatedAt.UTC(),
		UpdatedAt:          row.UpdatedAt.UTC(),
	}
}

func ladoDBChangeRunsFromGen(rows []gen.LadoDbChangeRun) []domain.LadoDBChangeRun {
	runs := make([]domain.LadoDBChangeRun, 0, len(rows))
	for _, row := range rows {
		runs = append(runs, ladoDBChangeRunFromGen(row))
	}
	return runs
}

// ladoStringsOrEmpty stores a nil list as an empty one.
func ladoStringsOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// unwrapLadoDBChangeSentinel returns the bare sentinel a database change
// write failed with, so callers may compare it, or err as it is.
func unwrapLadoDBChangeSentinel(err error) error {
	for _, sentinel := range []error{
		domain.ErrLadoDBChangeSettingsChanged,
		domain.ErrLadoDBChangeNotFound,
		domain.ErrLadoPresenceReplayed,
	} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return err
}
