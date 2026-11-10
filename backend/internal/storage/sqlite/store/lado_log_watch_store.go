package store

// LADO fork: production log watch (issue #87, migrations_lado/0017). A
// project's settings with its sources and targets, the read cursors, the
// fingerprints and their per-instance counts. Logs themselves are never
// stored.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// LadoLogWatchSettings reads a project's settings with its sources and
// targets, in the order they were saved. ok is false for a project that never
// saved any.
func (s *Store) LadoLogWatchSettings(ctx context.Context, project domain.ProjectID) (domain.LadoLogWatchSettings, bool, error) {
	row, err := s.qr.GetLadoLogWatchSettings(ctx, string(project))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoLogWatchSettings{}, false, nil
	}
	if err != nil {
		return domain.LadoLogWatchSettings{}, false, fmt.Errorf("read LADO log watch settings: %w", err)
	}
	settings, err := s.ladoLogWatchSettingsFromGen(ctx, s.qr, row)
	if err != nil {
		return domain.LadoLogWatchSettings{}, false, err
	}
	return settings, true, nil
}

// ListEnabledLadoLogWatchSettings returns the settings of every project with
// the log watch on, by project.
func (s *Store) ListEnabledLadoLogWatchSettings(ctx context.Context) ([]domain.LadoLogWatchSettings, error) {
	rows, err := s.qr.ListEnabledLadoLogWatchSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO log watch settings: %w", err)
	}
	out := make([]domain.LadoLogWatchSettings, 0, len(rows))
	for _, row := range rows {
		settings, err := s.ladoLogWatchSettingsFromGen(ctx, s.qr, row)
		if err != nil {
			return nil, err
		}
		out = append(out, settings)
	}
	return out, nil
}

// PutLadoLogWatchSettings saves a project's settings, sources and targets in
// one transaction, in the order given, and removes the sources and targets
// the settings no longer have; their read cursors go with them. Every source
// and target needs an id, and a target belongs to the source that holds it.
// A source id of another project is refused. The values the server keeps
// (BurstNotifiedAt, a source's DownSince, DownNotifiedAt and, once set,
// CertKey) stay as stored; a new source takes the CertKey given. An empty
// source kind is LadoLogSourceKindDockerTCP. It returns the ids of the
// removed sources.
func (s *Store) PutLadoLogWatchSettings(ctx context.Context, settings domain.LadoLogWatchSettings) ([]domain.LadoLogSourceID, error) {
	if settings.ProjectID == "" {
		return nil, fmt.Errorf("store LADO log watch settings: no project")
	}
	include, err := ladoLogPatternsJSON(settings.Include)
	if err != nil {
		return nil, err
	}
	exclude, err := ladoLogPatternsJSON(settings.Exclude)
	if err != nil {
		return nil, err
	}
	keepSources := map[string]bool{}
	keepTargets := map[string]bool{}
	for _, src := range settings.Sources {
		if src.ID == "" {
			return nil, fmt.Errorf("store LADO log watch settings: a source has no id")
		}
		keepSources[string(src.ID)] = true
		for _, target := range src.Targets {
			if target.ID == "" {
				return nil, fmt.Errorf("store LADO log watch settings: a target of source %s has no id", src.ID)
			}
			keepTargets[string(target.ID)] = true
		}
	}
	at := settings.UpdatedAt
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()
	project := string(settings.ProjectID)
	var removed []domain.LadoLogSourceID

	if err := s.writeMu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer s.writeMu.Unlock()
	err = s.inTx(ctx, "store LADO log watch settings", func(q *gen.Queries) error {
		if err := q.UpsertLadoLogWatchSettings(ctx, gen.UpsertLadoLogWatchSettingsParams{
			ProjectID:              project,
			Enabled:                ladoBool(settings.Enabled),
			Repository:             settings.Repository,
			IncludeJson:            include,
			ExcludeJson:            exclude,
			HourlyIssueCap:         int64(settings.HourlyIssueCap),
			AutoPlan:               ladoBool(settings.AutoPlan),
			AutoPlanDailyCap:       int64(settings.AutoPlanDailyCap),
			AutoPlanMinCount:       int64(settings.AutoPlanMinCount),
			DisconnectAlertMinutes: int64(settings.DisconnectAlertMinutes),
			UpdatedAt:              at,
		}); err != nil {
			return err
		}
		existing, err := q.ListLadoLogSourcesByProject(ctx, project)
		if err != nil {
			return err
		}
		for _, src := range existing {
			if keepSources[src.ID] {
				continue
			}
			if _, err := q.DeleteLadoLogSource(ctx, gen.DeleteLadoLogSourceParams{ID: src.ID, ProjectID: project}); err != nil {
				return err
			}
			removed = append(removed, domain.LadoLogSourceID(src.ID))
		}
		for i, src := range settings.Sources {
			kind := src.Kind
			if kind == "" {
				kind = domain.LadoLogSourceKindDockerTCP
			}
			n, err := q.UpdateLadoLogSource(ctx, gen.UpdateLadoLogSourceParams{
				Kind: kind, DisplayName: src.DisplayName, Endpoint: src.Endpoint,
				Tls: ladoBool(src.TLS), Paused: ladoBool(src.Paused), Position: int64(i),
				UpdatedAt: at, ID: string(src.ID), ProjectID: project,
			})
			if err != nil {
				return err
			}
			if n > 0 {
				continue
			}
			err = q.InsertLadoLogSource(ctx, gen.InsertLadoLogSourceParams{
				ID: string(src.ID), ProjectID: project, Kind: kind, DisplayName: src.DisplayName,
				Endpoint: src.Endpoint, Tls: ladoBool(src.TLS), CertKey: src.CertKey,
				Paused: ladoBool(src.Paused), Position: int64(i), CreatedAt: at, UpdatedAt: at,
			})
			if isSQLiteUnique(err) {
				return fmt.Errorf("source %s belongs to another project", src.ID)
			}
			if err != nil {
				return err
			}
		}
		targets, err := q.ListLadoLogTargetsByProject(ctx, project)
		if err != nil {
			return err
		}
		for _, target := range targets {
			if keepTargets[target.ID] {
				continue
			}
			if _, err := q.DeleteLadoLogTarget(ctx, target.ID); err != nil {
				return err
			}
		}
		for _, src := range settings.Sources {
			for j, target := range src.Targets {
				n, err := q.UpdateLadoLogTarget(ctx, gen.UpdateLadoLogTargetParams{
					SourceID: string(src.ID), Kind: string(target.Kind), Value: target.Value,
					ComposeProject: target.ComposeProject, Service: target.Service,
					Paused: ladoBool(target.Paused), Position: int64(j), UpdatedAt: at,
					ID: string(target.ID), ProjectID: project,
				})
				if err != nil {
					return err
				}
				if n > 0 {
					continue
				}
				err = q.InsertLadoLogTarget(ctx, gen.InsertLadoLogTargetParams{
					ID: string(target.ID), SourceID: string(src.ID), Kind: string(target.Kind),
					Value: target.Value, ComposeProject: target.ComposeProject, Service: target.Service,
					Paused: ladoBool(target.Paused), Position: int64(j), CreatedAt: at, UpdatedAt: at,
				})
				if isSQLiteUnique(err) {
					return fmt.Errorf("target %s belongs to another project", target.ID)
				}
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// SetLadoLogBurstNotified records when LADO told of a project's hourly issue
// cap.
func (s *Store) SetLadoLogBurstNotified(ctx context.Context, project domain.ProjectID, at time.Time) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if _, err := s.qw.SetLadoLogBurstNotified(ctx, gen.SetLadoLogBurstNotifiedParams{
		BurstNotifiedAt: ladoNullTime(at), ProjectID: string(project),
	}); err != nil {
		return fmt.Errorf("record LADO log burst notice: %w", err)
	}
	return nil
}

// SetLadoLogSourceDown records since when a source is disconnected and when
// LADO told of it; zero times clear them. An unknown source is
// domain.ErrLadoLogSourceNotFound.
func (s *Store) SetLadoLogSourceDown(ctx context.Context, source domain.LadoLogSourceID, downSince, notifiedAt time.Time) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.SetLadoLogSourceDown(ctx, gen.SetLadoLogSourceDownParams{
		DownSince: ladoNullTime(downSince), DownNotifiedAt: ladoNullTime(notifiedAt), ID: string(source),
	})
	if err != nil {
		return fmt.Errorf("record LADO log source state: %w", err)
	}
	if n == 0 {
		return domain.ErrLadoLogSourceNotFound
	}
	return nil
}

// SetLadoLogSourceCertKey records the directory name of a source's
// certificate copies, empty when it has none. An unknown source is
// domain.ErrLadoLogSourceNotFound.
func (s *Store) SetLadoLogSourceCertKey(ctx context.Context, source domain.LadoLogSourceID, certKey string) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.SetLadoLogSourceCertKey(ctx, gen.SetLadoLogSourceCertKeyParams{CertKey: certKey, ID: string(source)})
	if err != nil {
		return fmt.Errorf("record LADO log source certificates: %w", err)
	}
	if n == 0 {
		return domain.ErrLadoLogSourceNotFound
	}
	return nil
}

// LadoLogSourceProject returns the project a source belongs to.
func (s *Store) LadoLogSourceProject(ctx context.Context, source domain.LadoLogSourceID) (domain.ProjectID, bool, error) {
	project, err := s.qr.GetLadoLogSourceProject(ctx, string(source))
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read LADO log source: %w", err)
	}
	return domain.ProjectID(project), true, nil
}

// LadoLogCursor reads where reading an instance of a target goes on.
func (s *Store) LadoLogCursor(ctx context.Context, target domain.LadoLogTargetID, instance string) (domain.LadoLogCursor, bool, error) {
	row, err := s.qr.GetLadoLogCursor(ctx, gen.GetLadoLogCursorParams{TargetID: string(target), Instance: instance})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoLogCursor{}, false, nil
	}
	if err != nil {
		return domain.LadoLogCursor{}, false, fmt.Errorf("read LADO log cursor: %w", err)
	}
	return domain.LadoLogCursor{
		TargetID: domain.LadoLogTargetID(row.TargetID), Instance: row.Instance,
		Since: row.Since.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}, true, nil
}

// PutLadoLogCursor saves where reading an instance of a target goes on. A
// cursor of a target the settings removed meanwhile is dropped without an
// error. A zero UpdatedAt is now.
func (s *Store) PutLadoLogCursor(ctx context.Context, c domain.LadoLogCursor) error {
	if c.TargetID == "" || c.Instance == "" || c.Since.IsZero() {
		return fmt.Errorf("store LADO log cursor: target, instance and time are required")
	}
	updated := c.UpdatedAt
	if updated.IsZero() {
		updated = time.Now()
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "store LADO log cursor", func(q *gen.Queries) error {
		n, err := q.CountLadoLogTargets(ctx, string(c.TargetID))
		if err != nil || n == 0 {
			return err
		}
		return q.UpsertLadoLogCursor(ctx, gen.UpsertLadoLogCursorParams{
			TargetID: string(c.TargetID), Instance: c.Instance, Since: c.Since.UTC(), UpdatedAt: updated.UTC(),
		})
	})
}

// LadoLogFingerprint reads a project's fingerprint with its per-instance
// counts.
func (s *Store) LadoLogFingerprint(ctx context.Context, project domain.ProjectID, fingerprint string) (domain.LadoLogFingerprint, bool, error) {
	row, err := s.qr.GetLadoLogFingerprint(ctx, gen.GetLadoLogFingerprintParams{ProjectID: string(project), Fingerprint: fingerprint})
	return s.ladoLogFingerprintRead(ctx, row, err)
}

// LadoLogFingerprintByID reads a fingerprint by id with its per-instance
// counts.
func (s *Store) LadoLogFingerprintByID(ctx context.Context, id domain.LadoLogFingerprintID) (domain.LadoLogFingerprint, bool, error) {
	row, err := s.qr.GetLadoLogFingerprintByID(ctx, int64(id))
	return s.ladoLogFingerprintRead(ctx, row, err)
}

func (s *Store) ladoLogFingerprintRead(ctx context.Context, row gen.LadoLogFingerprint, err error) (domain.LadoLogFingerprint, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoLogFingerprint{}, false, nil
	}
	if err != nil {
		return domain.LadoLogFingerprint{}, false, fmt.Errorf("read LADO log fingerprint: %w", err)
	}
	f, err := ladoLogFingerprintWithInstances(ctx, s.qr, row)
	if err != nil {
		return domain.LadoLogFingerprint{}, false, err
	}
	return f, true, nil
}

// AddLadoLogOccurrences adds what one instance showed of a fingerprint: it
// creates the fingerprint when it is new, raises its count by Delta, moves
// its first and last seen times outward, and raises the instance's count
// likewise, taking its newest image tag. It returns the fingerprint as
// stored.
func (s *Store) AddLadoLogOccurrences(ctx context.Context, d domain.LadoLogOccurrenceDelta) (domain.LadoLogFingerprint, error) {
	if d.Project == "" || d.Fingerprint == "" || d.Delta < 0 || d.LastSeen.IsZero() {
		return domain.LadoLogFingerprint{}, fmt.Errorf("add LADO log occurrences: project, fingerprint, a count of at least 0 and a time are required")
	}
	first, last := d.FirstSeen.UTC(), d.LastSeen.UTC()
	if d.FirstSeen.IsZero() || first.After(last) {
		first = last
	}
	now := time.Now().UTC()
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoLogFingerprint{}, err
	}
	defer s.writeMu.Unlock()
	var out domain.LadoLogFingerprint
	err := s.inTx(ctx, "add LADO log occurrences", func(q *gen.Queries) error {
		row, err := q.GetLadoLogFingerprint(ctx, gen.GetLadoLogFingerprintParams{ProjectID: string(d.Project), Fingerprint: d.Fingerprint})
		var id int64
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if id, err = q.InsertLadoLogFingerprint(ctx, gen.InsertLadoLogFingerprintParams{
				ProjectID: string(d.Project), Fingerprint: d.Fingerprint, Service: d.Service,
				ExceptionKind: d.Kind, Location: d.Location, Count: int64(d.Delta),
				FirstSeenAt: first, LastSeenAt: last, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			id = row.ID
			if err := q.UpdateLadoLogFingerprintCounts(ctx, gen.UpdateLadoLogFingerprintCountsParams{
				Count:       row.Count + int64(d.Delta),
				FirstSeenAt: ladoEarlier(row.FirstSeenAt.UTC(), first),
				LastSeenAt:  ladoLater(row.LastSeenAt.UTC(), last),
				UpdatedAt:   now,
				ID:          id,
			}); err != nil {
				return err
			}
		}
		if d.Instance != "" {
			count, image, seen := int64(d.Delta), d.Image, last
			old, err := q.GetLadoLogInstanceCount(ctx, gen.GetLadoLogInstanceCountParams{FingerprintID: id, Instance: d.Instance})
			switch {
			case errors.Is(err, sql.ErrNoRows):
			case err != nil:
				return err
			default:
				count += old.Count
				seen = ladoLater(old.LastSeenAt.UTC(), last)
				if image == "" || old.LastSeenAt.After(last) {
					image = old.ImageTag
				}
			}
			if err := q.UpsertLadoLogInstanceCount(ctx, gen.UpsertLadoLogInstanceCountParams{
				FingerprintID: id, Instance: d.Instance, ImageTag: image, Count: count, LastSeenAt: seen,
			}); err != nil {
				return err
			}
		}
		stored, err := q.GetLadoLogFingerprintByID(ctx, id)
		if err != nil {
			return err
		}
		out, err = ladoLogFingerprintWithInstances(ctx, q, stored)
		return err
	})
	if err != nil {
		return domain.LadoLogFingerprint{}, err
	}
	return out, nil
}

// LinkLadoLogFingerprintTask records the task LADO created for a fingerprint
// and, once confirmed, its issue. Linking a new task starts a new issue: it
// records at as the issue's creation, which the hourly cap counts, and
// forgets the earlier task's plan request. Linking the same task again fills
// in the issue and keeps the rest; an empty issue ref or previous issue ref
// then keeps the stored one. An unknown fingerprint is
// domain.ErrLadoLogFingerprintNotFound.
func (s *Store) LinkLadoLogFingerprintTask(ctx context.Context, id domain.LadoLogFingerprintID, task domain.LadoTaskID, issueRef, previousIssueRef domain.IssueID, at time.Time) error {
	if task == "" {
		return fmt.Errorf("link LADO log fingerprint: no task")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "link LADO log fingerprint", func(q *gen.Queries) error {
		row, err := q.GetLadoLogFingerprintByID(ctx, int64(id))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrLadoLogFingerprintNotFound
		}
		if err != nil {
			return err
		}
		params := gen.UpdateLadoLogFingerprintLinkParams{
			TaskID:           ladoNullString(string(task)),
			IssueRef:         ladoNullString(string(issueRef)),
			PreviousIssueRef: string(previousIssueRef),
			IssueCreatedAt:   ladoNullTime(at),
			AutoPlanHeld:     "",
			UpdatedAt:        at.UTC(),
			ID:               row.ID,
		}
		if row.TaskID.Valid && row.TaskID.String == string(task) {
			if issueRef == "" {
				params.IssueRef = row.IssueRef
			}
			if previousIssueRef == "" {
				params.PreviousIssueRef = row.PreviousIssueRef
			}
			if row.IssueCreatedAt.Valid {
				params.IssueCreatedAt = row.IssueCreatedAt
			}
			params.AutoPlanRequestedAt = row.AutoPlanRequestedAt
			params.AutoPlanHeld = row.AutoPlanHeld
		}
		_, err = q.UpdateLadoLogFingerprintLink(ctx, params)
		return err
	})
}

// ClearLadoLogFingerprintTask forgets a fingerprint's task and issue, and
// the task's plan request, so its next occurrence creates them again. The
// previous issue and the counts stay. An unknown fingerprint is
// domain.ErrLadoLogFingerprintNotFound.
func (s *Store) ClearLadoLogFingerprintTask(ctx context.Context, id domain.LadoLogFingerprintID) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "clear LADO log fingerprint", func(q *gen.Queries) error {
		row, err := q.GetLadoLogFingerprintByID(ctx, int64(id))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrLadoLogFingerprintNotFound
		}
		if err != nil {
			return err
		}
		_, err = q.UpdateLadoLogFingerprintLink(ctx, gen.UpdateLadoLogFingerprintLinkParams{
			PreviousIssueRef: row.PreviousIssueRef,
			IssueCreatedAt:   row.IssueCreatedAt,
			UpdatedAt:        time.Now().UTC(),
			ID:               row.ID,
		})
		return err
	})
}

// SetLadoLogAutoPlan records that LADO requested a plan for a fingerprint's
// task at requestedAt, or why it held the request (requestedAt zero). An
// unknown fingerprint is domain.ErrLadoLogFingerprintNotFound.
func (s *Store) SetLadoLogAutoPlan(ctx context.Context, id domain.LadoLogFingerprintID, requestedAt time.Time, held domain.LadoLogAutoPlanHold) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.SetLadoLogAutoPlan(ctx, gen.SetLadoLogAutoPlanParams{
		AutoPlanRequestedAt: ladoNullTime(requestedAt), AutoPlanHeld: string(held),
		UpdatedAt: time.Now().UTC(), ID: int64(id),
	})
	if err != nil {
		return fmt.Errorf("record LADO log plan request: %w", err)
	}
	if n == 0 {
		return domain.ErrLadoLogFingerprintNotFound
	}
	return nil
}

// SetLadoLogFingerprintIgnored puts a fingerprint on or off the ignore list
// and returns it. An unknown fingerprint is
// domain.ErrLadoLogFingerprintNotFound.
func (s *Store) SetLadoLogFingerprintIgnored(ctx context.Context, id domain.LadoLogFingerprintID, ignored bool, at time.Time) (domain.LadoLogFingerprint, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoLogFingerprint{}, err
	}
	var ignoredAt time.Time
	if ignored {
		ignoredAt = at
	}
	n, err := s.qw.SetLadoLogFingerprintIgnored(ctx, gen.SetLadoLogFingerprintIgnoredParams{
		Ignored: ladoBool(ignored), IgnoredAt: ladoNullTime(ignoredAt), UpdatedAt: at.UTC(), ID: int64(id),
	})
	s.writeMu.Unlock()
	if err != nil {
		return domain.LadoLogFingerprint{}, fmt.Errorf("record LADO log ignore: %w", err)
	}
	if n == 0 {
		return domain.LadoLogFingerprint{}, domain.ErrLadoLogFingerprintNotFound
	}
	f, ok, err := s.LadoLogFingerprintByID(ctx, id)
	if err != nil {
		return domain.LadoLogFingerprint{}, err
	}
	if !ok {
		return domain.LadoLogFingerprint{}, domain.ErrLadoLogFingerprintNotFound
	}
	return f, nil
}

// CountLadoLogIssuesSince counts a project's fingerprints whose issue LADO
// started creating at or after since.
func (s *Store) CountLadoLogIssuesSince(ctx context.Context, project domain.ProjectID, since time.Time) (int, error) {
	n, err := s.qr.CountLadoLogIssuesSince(ctx, gen.CountLadoLogIssuesSinceParams{ProjectID: string(project), Since: ladoNullTime(since)})
	if err != nil {
		return 0, fmt.Errorf("count LADO log issues: %w", err)
	}
	return int(n), nil
}

// CountLadoLogAutoPlansSince counts a project's fingerprints whose plan LADO
// requested at or after since.
func (s *Store) CountLadoLogAutoPlansSince(ctx context.Context, project domain.ProjectID, since time.Time) (int, error) {
	n, err := s.qr.CountLadoLogAutoPlansSince(ctx, gen.CountLadoLogAutoPlansSinceParams{ProjectID: string(project), Since: ladoNullTime(since)})
	if err != nil {
		return 0, fmt.Errorf("count LADO log plan requests: %w", err)
	}
	return int(n), nil
}

// ListLadoLogIncidents returns a project's fingerprints: every one whose task
// is live (not cancelled, its issue neither done nor cancelled), then the
// otherLimit newest of the rest, each newest first, with their per-instance
// counts.
func (s *Store) ListLadoLogIncidents(ctx context.Context, project domain.ProjectID, otherLimit int) ([]domain.LadoLogFingerprint, error) {
	open, err := s.qr.ListLadoLogOpenIncidents(ctx, string(project))
	if err != nil {
		return nil, fmt.Errorf("list LADO log incidents: %w", err)
	}
	var others []gen.LadoLogFingerprint
	if otherLimit > 0 {
		if others, err = s.qr.ListLadoLogOtherIncidents(ctx, gen.ListLadoLogOtherIncidentsParams{
			ProjectID: string(project), RowLimit: int64(otherLimit),
		}); err != nil {
			return nil, fmt.Errorf("list LADO log incidents: %w", err)
		}
	}
	counts, err := s.qr.ListLadoLogInstanceCountsByProject(ctx, string(project))
	if err != nil {
		return nil, fmt.Errorf("list LADO log instance counts: %w", err)
	}
	byFingerprint := map[int64][]domain.LadoLogInstanceCount{}
	for _, c := range counts {
		byFingerprint[c.FingerprintID] = append(byFingerprint[c.FingerprintID], ladoLogInstanceCountFromGen(c))
	}
	out := make([]domain.LadoLogFingerprint, 0, len(open)+len(others))
	for _, row := range append(open, others...) {
		f := ladoLogFingerprintFromGen(row)
		f.Instances = byFingerprint[row.ID]
		if f.Instances == nil {
			f.Instances = []domain.LadoLogInstanceCount{}
		}
		out = append(out, f)
	}
	return out, nil
}

func (s *Store) ladoLogWatchSettingsFromGen(ctx context.Context, q *gen.Queries, row gen.LadoLogWatchSetting) (domain.LadoLogWatchSettings, error) {
	settings := domain.LadoLogWatchSettings{
		ProjectID:              domain.ProjectID(row.ProjectID),
		Enabled:                row.Enabled != 0,
		Repository:             row.Repository,
		HourlyIssueCap:         int(row.HourlyIssueCap),
		AutoPlan:               row.AutoPlan != 0,
		AutoPlanDailyCap:       int(row.AutoPlanDailyCap),
		AutoPlanMinCount:       int(row.AutoPlanMinCount),
		DisconnectAlertMinutes: int(row.DisconnectAlertMinutes),
		BurstNotifiedAt:        ladoTimeFromNull(row.BurstNotifiedAt),
		UpdatedAt:              row.UpdatedAt.UTC(),
	}
	if err := json.Unmarshal([]byte(row.IncludeJson), &settings.Include); err != nil {
		return domain.LadoLogWatchSettings{}, fmt.Errorf("read LADO log watch include patterns: %w", err)
	}
	if err := json.Unmarshal([]byte(row.ExcludeJson), &settings.Exclude); err != nil {
		return domain.LadoLogWatchSettings{}, fmt.Errorf("read LADO log watch exclude patterns: %w", err)
	}
	if settings.Include == nil {
		settings.Include = []string{}
	}
	if settings.Exclude == nil {
		settings.Exclude = []string{}
	}
	sources, err := q.ListLadoLogSourcesByProject(ctx, row.ProjectID)
	if err != nil {
		return domain.LadoLogWatchSettings{}, fmt.Errorf("list LADO log sources: %w", err)
	}
	targets, err := q.ListLadoLogTargetsByProject(ctx, row.ProjectID)
	if err != nil {
		return domain.LadoLogWatchSettings{}, fmt.Errorf("list LADO log targets: %w", err)
	}
	bySource := map[string][]domain.LadoLogTarget{}
	for _, t := range targets {
		bySource[t.SourceID] = append(bySource[t.SourceID], domain.LadoLogTarget{
			ID:             domain.LadoLogTargetID(t.ID),
			SourceID:       domain.LadoLogSourceID(t.SourceID),
			Kind:           domain.LadoLogTargetKind(t.Kind),
			Value:          t.Value,
			ComposeProject: t.ComposeProject,
			Service:        t.Service,
			Paused:         t.Paused != 0,
		})
	}
	settings.Sources = make([]domain.LadoLogSource, 0, len(sources))
	for _, src := range sources {
		ts := bySource[src.ID]
		if ts == nil {
			ts = []domain.LadoLogTarget{}
		}
		settings.Sources = append(settings.Sources, domain.LadoLogSource{
			ID:             domain.LadoLogSourceID(src.ID),
			Kind:           src.Kind,
			DisplayName:    src.DisplayName,
			Endpoint:       src.Endpoint,
			TLS:            src.Tls != 0,
			CertKey:        src.CertKey,
			Paused:         src.Paused != 0,
			DownSince:      ladoTimeFromNull(src.DownSince),
			DownNotifiedAt: ladoTimeFromNull(src.DownNotifiedAt),
			Targets:        ts,
		})
	}
	return settings, nil
}

func ladoLogFingerprintWithInstances(ctx context.Context, q *gen.Queries, row gen.LadoLogFingerprint) (domain.LadoLogFingerprint, error) {
	f := ladoLogFingerprintFromGen(row)
	counts, err := q.ListLadoLogInstanceCounts(ctx, row.ID)
	if err != nil {
		return domain.LadoLogFingerprint{}, fmt.Errorf("list LADO log instance counts: %w", err)
	}
	f.Instances = make([]domain.LadoLogInstanceCount, 0, len(counts))
	for _, c := range counts {
		f.Instances = append(f.Instances, ladoLogInstanceCountFromGen(c))
	}
	return f, nil
}

func ladoLogFingerprintFromGen(row gen.LadoLogFingerprint) domain.LadoLogFingerprint {
	return domain.LadoLogFingerprint{
		ID:                  domain.LadoLogFingerprintID(row.ID),
		ProjectID:           domain.ProjectID(row.ProjectID),
		Fingerprint:         row.Fingerprint,
		Service:             row.Service,
		ExceptionKind:       row.ExceptionKind,
		Location:            row.Location,
		Count:               int(row.Count),
		FirstSeenAt:         row.FirstSeenAt.UTC(),
		LastSeenAt:          row.LastSeenAt.UTC(),
		TaskID:              domain.LadoTaskID(row.TaskID.String),
		IssueRef:            domain.IssueID(row.IssueRef.String),
		PreviousIssueRef:    domain.IssueID(row.PreviousIssueRef),
		IssueCreatedAt:      ladoTimeFromNull(row.IssueCreatedAt),
		AutoPlanRequestedAt: ladoTimeFromNull(row.AutoPlanRequestedAt),
		AutoPlanHeld:        domain.LadoLogAutoPlanHold(row.AutoPlanHeld),
		Ignored:             row.Ignored != 0,
		IgnoredAt:           ladoTimeFromNull(row.IgnoredAt),
	}
}

func ladoLogInstanceCountFromGen(row gen.LadoLogInstanceCount) domain.LadoLogInstanceCount {
	return domain.LadoLogInstanceCount{
		Instance: row.Instance, Image: row.ImageTag, Count: int(row.Count), LastSeenAt: row.LastSeenAt.UTC(),
	}
}

func ladoLogPatternsJSON(patterns []string) (string, error) {
	if patterns == nil {
		patterns = []string{}
	}
	data, err := json.Marshal(patterns)
	if err != nil {
		return "", fmt.Errorf("encode LADO log watch patterns: %w", err)
	}
	return string(data), nil
}

func ladoEarlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func ladoLater(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
