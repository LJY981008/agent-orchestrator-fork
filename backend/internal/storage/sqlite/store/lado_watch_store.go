package store

// LADO fork: worker watch (issue #42, migrations_lado/0012). The watch
// settings, what LADO watches of each task session, the accounts at a usage
// limit and the one-line alerts; and the reads the usage view adds to #34's
// task timing facts: earlier plan worker attempts, subagent usage and the
// conversation files the compaction counter reads.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// LadoWatchSettings reads one settings row: the global row for
// domain.LadoWatchGlobalScope, else the project's. ok is false without a row.
func (s *Store) LadoWatchSettings(ctx context.Context, scope domain.ProjectID) (domain.LadoWatchOverrides, bool, error) {
	row, err := s.qr.GetLadoWatchSettings(ctx, string(scope))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoWatchOverrides{}, false, nil
	}
	if err != nil {
		return domain.LadoWatchOverrides{}, false, fmt.Errorf("read LADO watch settings: %w", err)
	}
	o := domain.LadoWatchOverrides{UpdatedAt: row.UpdatedAt.UTC()}
	if row.CompactWindow.Valid {
		v := int(row.CompactWindow.Int64)
		o.CompactWindow = &v
	}
	if row.TaskCostCents.Valid {
		v := row.TaskCostCents.Int64
		o.TaskCostCents = &v
	}
	if row.SessionCostCents.Valid {
		v := row.SessionCostCents.Int64
		o.SessionCostCents = &v
	}
	if row.SessionCompactions.Valid {
		v := int(row.SessionCompactions.Int64)
		o.SessionCompactions = &v
	}
	if row.StallMinutes.Valid {
		v := int(row.StallMinutes.Int64)
		o.StallMinutes = &v
	}
	if row.WakeEnabled.Valid {
		v := row.WakeEnabled.Int64 != 0
		o.WakeEnabled = &v
	}
	return o, true, nil
}

// PutLadoWatchSettings replaces one settings row, the global row for
// domain.LadoWatchGlobalScope. A nil value follows the row below it. A value
// out of range is refused with domain.ErrLadoWatchSettingOutOfRange. A zero
// UpdatedAt is now.
func (s *Store) PutLadoWatchSettings(ctx context.Context, scope domain.ProjectID, o domain.LadoWatchOverrides) error {
	if err := o.Validate(); err != nil {
		return fmt.Errorf("store LADO watch settings: %w", err)
	}
	updated := o.UpdatedAt
	if updated.IsZero() {
		updated = time.Now()
	}
	nullInt := func(v *int) sql.NullInt64 {
		if v == nil {
			return sql.NullInt64{}
		}
		return sql.NullInt64{Int64: int64(*v), Valid: true}
	}
	nullInt64 := func(v *int64) sql.NullInt64 {
		if v == nil {
			return sql.NullInt64{}
		}
		return sql.NullInt64{Int64: *v, Valid: true}
	}
	var wake sql.NullInt64
	if o.WakeEnabled != nil {
		wake = sql.NullInt64{Int64: ladoBool(*o.WakeEnabled), Valid: true}
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertLadoWatchSettings(ctx, gen.UpsertLadoWatchSettingsParams{
		ProjectID:          string(scope),
		CompactWindow:      nullInt(o.CompactWindow),
		TaskCostCents:      nullInt64(o.TaskCostCents),
		SessionCostCents:   nullInt64(o.SessionCostCents),
		SessionCompactions: nullInt(o.SessionCompactions),
		StallMinutes:       nullInt(o.StallMinutes),
		WakeEnabled:        wake,
		UpdatedAt:          updated.UTC(),
	}); err != nil {
		return fmt.Errorf("store LADO watch settings: %w", err)
	}
	return nil
}

// LadoEffectiveWatchSettings is the settings project runs with: its row over
// the global row over the defaults.
func (s *Store) LadoEffectiveWatchSettings(ctx context.Context, project domain.ProjectID) (domain.LadoWatchSettings, error) {
	global, _, err := s.LadoWatchSettings(ctx, domain.LadoWatchGlobalScope)
	if err != nil {
		return domain.LadoWatchSettings{}, err
	}
	var own domain.LadoWatchOverrides
	if project != domain.LadoWatchGlobalScope {
		if own, _, err = s.LadoWatchSettings(ctx, project); err != nil {
			return domain.LadoWatchSettings{}, err
		}
	}
	return domain.LadoWatchEffective(global, own), nil
}

// EnsureLadoSessionWatch makes the watch row of a task's session, empty,
// unless it has one. created reports whether it made the row.
func (s *Store) EnsureLadoSessionWatch(ctx context.Context, session domain.SessionID, task domain.LadoTaskID, at time.Time) (bool, error) {
	if session == "" || task == "" {
		return false, errors.New("make LADO session watch: a session and a task are required")
	}
	if at.IsZero() {
		at = time.Now()
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.EnsureLadoSessionWatch(ctx, gen.EnsureLadoSessionWatchParams{
		SessionID: string(session), TaskID: string(task), At: at.UTC(),
	})
	if err != nil {
		return false, fmt.Errorf("make LADO session watch: %w", err)
	}
	return n == 1, nil
}

// SetLadoSessionWatchCompactions writes the compaction counter's columns of
// a session's watch row and no other. ok is false without a row.
func (s *Store) SetLadoSessionWatchCompactions(ctx context.Context, session domain.SessionID, c domain.LadoWatchCompactions, at time.Time) (bool, error) {
	if c.Compactions < 0 {
		return false, errors.New("store LADO compactions: the count cannot be negative")
	}
	offsets := c.ReadOffsets
	if offsets == nil {
		offsets = map[string]int64{}
	}
	for path, offset := range offsets {
		if path == "" || offset < 0 {
			return false, fmt.Errorf("store LADO compactions: bad read offset %q=%d", path, offset)
		}
	}
	encoded, err := json.Marshal(offsets)
	if err != nil {
		return false, fmt.Errorf("store LADO compactions: %w", err)
	}
	return s.updateLadoSessionWatch(ctx, "compactions", func(q *gen.Queries, updated time.Time) (int64, error) {
		return q.UpdateLadoSessionWatchCompactions(ctx, gen.UpdateLadoSessionWatchCompactionsParams{
			ReadOffsetsJson: string(encoded),
			Compactions:     int64(c.Compactions),
			CompactedAt:     ladoNullTime(c.CompactedAt),
			UpdatedAt:       updated,
			SessionID:       string(session),
		})
	}, at)
}

// SetLadoSessionWatchLimit writes the limit watch's columns of a session's
// watch row and no other. ok is false without a row.
func (s *Store) SetLadoSessionWatchLimit(ctx context.Context, session domain.SessionID, l domain.LadoWatchLimit, at time.Time) (bool, error) {
	if l.LimitState != domain.LadoLimitNone && l.LimitState != domain.LadoLimitLimited {
		return false, fmt.Errorf("store LADO limit watch: unknown state %q", l.LimitState)
	}
	if l.Resumes < 0 {
		return false, errors.New("store LADO limit watch: the resume count cannot be negative")
	}
	return s.updateLadoSessionWatch(ctx, "limit watch", func(q *gen.Queries, updated time.Time) (int64, error) {
		return q.UpdateLadoSessionWatchLimit(ctx, gen.UpdateLadoSessionWatchLimitParams{
			LimitState:      string(l.LimitState),
			LimitResetAt:    ladoNullTime(l.LimitResetAt),
			LimitResetGuess: ladoBool(l.LimitResetGuessed),
			LimitSeenAt:     ladoNullTime(l.LimitSeenAt),
			Resumes:         int64(l.Resumes),
			ResumedAt:       ladoNullTime(l.ResumedAt),
			UpdatedAt:       updated,
			SessionID:       string(session),
		})
	}, at)
}

// SetLadoSessionWatchStall writes the stall watch's columns of a session's
// watch row and no other. ok is false without a row.
func (s *Store) SetLadoSessionWatchStall(ctx context.Context, session domain.SessionID, st domain.LadoWatchStall, at time.Time) (bool, error) {
	if st.StallState != domain.LadoStallNone && st.StallState != domain.LadoStallStalled {
		return false, fmt.Errorf("store LADO stall watch: unknown state %q", st.StallState)
	}
	if st.Nudges < 0 {
		return false, errors.New("store LADO stall watch: the nudge count cannot be negative")
	}
	return s.updateLadoSessionWatch(ctx, "stall watch", func(q *gen.Queries, updated time.Time) (int64, error) {
		return q.UpdateLadoSessionWatchStall(ctx, gen.UpdateLadoSessionWatchStallParams{
			StallState: string(st.StallState),
			StalledAt:  ladoNullTime(st.StalledAt),
			Nudges:     int64(st.Nudges),
			NudgedAt:   ladoNullTime(st.NudgedAt),
			UpdatedAt:  updated,
			SessionID:  string(session),
		})
	}, at)
}

func (s *Store) updateLadoSessionWatch(ctx context.Context, what string, update func(*gen.Queries, time.Time) (int64, error), at time.Time) (bool, error) {
	if at.IsZero() {
		at = time.Now()
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	n, err := update(s.qw, at.UTC())
	if err != nil {
		return false, fmt.Errorf("store LADO %s: %w", what, err)
	}
	return n == 1, nil
}

// LadoSessionWatch reads a session's watch row. ok is false without one.
func (s *Store) LadoSessionWatch(ctx context.Context, session domain.SessionID) (domain.LadoSessionWatch, bool, error) {
	row, err := s.qr.GetLadoSessionWatch(ctx, string(session))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoSessionWatch{}, false, nil
	}
	if err != nil {
		return domain.LadoSessionWatch{}, false, fmt.Errorf("read LADO session watch: %w", err)
	}
	w, err := ladoSessionWatchFromRow(row)
	return w, err == nil, err
}

// ListLadoSessionWatch reads the watch rows of a task's sessions, in the
// order they were made.
func (s *Store) ListLadoSessionWatch(ctx context.Context, task domain.LadoTaskID) ([]domain.LadoSessionWatch, error) {
	rows, err := s.qr.ListLadoSessionWatchByTask(ctx, string(task))
	if err != nil {
		return nil, fmt.Errorf("list LADO session watch: %w", err)
	}
	watches := make([]domain.LadoSessionWatch, 0, len(rows))
	for _, row := range rows {
		w, err := ladoSessionWatchFromRow(row)
		if err != nil {
			return nil, err
		}
		watches = append(watches, w)
	}
	return watches, nil
}

func ladoSessionWatchFromRow(row gen.LadoSessionWatch) (domain.LadoSessionWatch, error) {
	offsets := map[string]int64{}
	if err := json.Unmarshal([]byte(row.ReadOffsetsJson), &offsets); err != nil {
		return domain.LadoSessionWatch{}, fmt.Errorf("read LADO session watch %s offsets: %w", row.SessionID, err)
	}
	return domain.LadoSessionWatch{
		SessionID: domain.SessionID(row.SessionID),
		TaskID:    domain.LadoTaskID(row.TaskID),
		LadoWatchCompactions: domain.LadoWatchCompactions{
			ReadOffsets: offsets,
			Compactions: int(row.Compactions),
			CompactedAt: ladoTimeFromNull(row.CompactedAt),
		},
		LadoWatchLimit: domain.LadoWatchLimit{
			LimitState:        domain.LadoLimitState(row.LimitState),
			LimitResetAt:      ladoTimeFromNull(row.LimitResetAt),
			LimitResetGuessed: row.LimitResetGuess != 0,
			LimitSeenAt:       ladoTimeFromNull(row.LimitSeenAt),
			Resumes:           int(row.Resumes),
			ResumedAt:         ladoTimeFromNull(row.ResumedAt),
		},
		LadoWatchStall: domain.LadoWatchStall{
			StallState: domain.LadoStallState(row.StallState),
			StalledAt:  ladoTimeFromNull(row.StalledAt),
			Nudges:     int(row.Nudges),
			NudgedAt:   ladoTimeFromNull(row.NudgedAt),
		},
		CreatedAt: row.CreatedAt.UTC(),
		UpdatedAt: row.UpdatedAt.UTC(),
	}, nil
}

// RecordLadoAccountLimit records an account at its usage limit until
// l.ResetAt. A later record replaces the reset time and keeps when LADO first
// saw the limit. A zero DetectedAt or UpdatedAt is now.
func (s *Store) RecordLadoAccountLimit(ctx context.Context, l domain.LadoAccountLimit) error {
	if l.AccountKey == "" || l.ResetAt.IsZero() {
		return errors.New("record LADO account limit: an account and a reset time are required")
	}
	now := time.Now()
	if l.DetectedAt.IsZero() {
		l.DetectedAt = now
	}
	if l.UpdatedAt.IsZero() {
		l.UpdatedAt = l.DetectedAt
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertLadoAccountLimit(ctx, gen.UpsertLadoAccountLimitParams{
		AccountKey: l.AccountKey,
		SessionID:  string(l.SessionID),
		ResetAt:    l.ResetAt.UTC(),
		ResetGuess: ladoBool(l.ResetGuessed),
		DetectedAt: l.DetectedAt.UTC(),
		UpdatedAt:  l.UpdatedAt.UTC(),
	}); err != nil {
		return fmt.Errorf("record LADO account limit: %w", err)
	}
	return nil
}

// ReleaseLadoAccountLimit removes an account's limit. ok is false when it
// had none.
func (s *Store) ReleaseLadoAccountLimit(ctx context.Context, key string) (bool, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.DeleteLadoAccountLimit(ctx, key)
	if err != nil {
		return false, fmt.Errorf("release LADO account limit: %w", err)
	}
	return n == 1, nil
}

// LadoAccountLimit reads an account's limit. ok is false when it has none;
// a limit whose reset passed stays until it is released.
func (s *Store) LadoAccountLimit(ctx context.Context, key string) (domain.LadoAccountLimit, bool, error) {
	row, err := s.qr.GetLadoAccountLimit(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoAccountLimit{}, false, nil
	}
	if err != nil {
		return domain.LadoAccountLimit{}, false, fmt.Errorf("read LADO account limit: %w", err)
	}
	return ladoAccountLimitFromRow(row), true, nil
}

// ListLadoAccountLimits reads every recorded account limit.
func (s *Store) ListLadoAccountLimits(ctx context.Context) ([]domain.LadoAccountLimit, error) {
	rows, err := s.qr.ListLadoAccountLimits(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO account limits: %w", err)
	}
	limits := make([]domain.LadoAccountLimit, 0, len(rows))
	for _, row := range rows {
		limits = append(limits, ladoAccountLimitFromRow(row))
	}
	return limits, nil
}

func ladoAccountLimitFromRow(row gen.LadoAccountLimit) domain.LadoAccountLimit {
	return domain.LadoAccountLimit{
		AccountKey:   row.AccountKey,
		SessionID:    domain.SessionID(row.SessionID),
		ResetAt:      row.ResetAt.UTC(),
		ResetGuessed: row.ResetGuess != 0,
		DetectedAt:   row.DetectedAt.UTC(),
		UpdatedAt:    row.UpdatedAt.UTC(),
	}
}

// AddLadoAlert raises an alert once: a second alert of the same task,
// session, kind and level is not written, and added is false. A task's own
// alert has an empty session. A zero CreatedAt is now.
func (s *Store) AddLadoAlert(ctx context.Context, a domain.LadoAlert) (domain.LadoAlertID, bool, error) {
	if a.TaskID == "" || !a.Kind.Valid() || a.Level < 1 {
		return 0, false, fmt.Errorf("add LADO alert: a task, a known kind and a level from 1 are required, not %q level %d", a.Kind, a.Level)
	}
	created := a.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return 0, false, err
	}
	defer s.writeMu.Unlock()
	id, err := s.qw.InsertLadoAlert(ctx, gen.InsertLadoAlertParams{
		TaskID:    string(a.TaskID),
		SessionID: string(a.SessionID),
		Kind:      string(a.Kind),
		Level:     int64(a.Level),
		Value:     a.Value,
		Threshold: a.Threshold,
		CreatedAt: created.UTC(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("add LADO alert: %w", err)
	}
	return domain.LadoAlertID(id), true, nil
}

// MarkLadoAlertSeen marks an alert seen at at. ok is false when it does not
// exist or was seen before.
func (s *Store) MarkLadoAlertSeen(ctx context.Context, id domain.LadoAlertID, at time.Time) (bool, error) {
	if at.IsZero() {
		at = time.Now()
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.MarkLadoAlertSeen(ctx, gen.MarkLadoAlertSeenParams{SeenAt: ladoNullTime(at), ID: int64(id)})
	if err != nil {
		return false, fmt.Errorf("mark LADO alert seen: %w", err)
	}
	return n == 1, nil
}

// LadoAlert reads one alert with its task's project and issue. ok is false
// when it does not exist.
func (s *Store) LadoAlert(ctx context.Context, id domain.LadoAlertID) (domain.LadoAlert, bool, error) {
	row, err := s.qr.GetLadoAlert(ctx, int64(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoAlert{}, false, nil
	}
	if err != nil {
		return domain.LadoAlert{}, false, fmt.Errorf("read LADO alert: %w", err)
	}
	return ladoAlertFromRow(gen.ListLadoAlertsSinceRow(row)), true, nil
}

// ListLadoAlertsByProject reads up to limit of a project's alerts, newest
// first; only the unseen ones when unseenOnly.
func (s *Store) ListLadoAlertsByProject(ctx context.Context, project domain.ProjectID, unseenOnly bool, limit int) ([]domain.LadoAlert, error) {
	if limit < 1 {
		return nil, errors.New("list LADO alerts: a positive limit is required")
	}
	rows, err := s.qr.ListLadoAlertsByProject(ctx, gen.ListLadoAlertsByProjectParams{
		ProjectID: string(project), UnseenOnly: ladoBool(unseenOnly), MaxRows: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list LADO alerts: %w", err)
	}
	alerts := make([]domain.LadoAlert, 0, len(rows))
	for _, row := range rows {
		alerts = append(alerts, ladoAlertFromRow(gen.ListLadoAlertsSinceRow(row)))
	}
	return alerts, nil
}

// ListLadoAlertsByTask reads a task's alerts, newest first.
func (s *Store) ListLadoAlertsByTask(ctx context.Context, task domain.LadoTaskID) ([]domain.LadoAlert, error) {
	rows, err := s.qr.ListLadoAlertsByTask(ctx, string(task))
	if err != nil {
		return nil, fmt.Errorf("list LADO task alerts: %w", err)
	}
	alerts := make([]domain.LadoAlert, 0, len(rows))
	for _, row := range rows {
		alerts = append(alerts, ladoAlertFromRow(gen.ListLadoAlertsSinceRow(row)))
	}
	return alerts, nil
}

// ListLadoAlertsSince reads the alerts raised at or after since, oldest
// first, with their tasks' projects and issues.
func (s *Store) ListLadoAlertsSince(ctx context.Context, since time.Time) ([]domain.LadoAlert, error) {
	rows, err := s.qr.ListLadoAlertsSince(ctx, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("list LADO alerts since: %w", err)
	}
	alerts := make([]domain.LadoAlert, 0, len(rows))
	for _, row := range rows {
		alerts = append(alerts, ladoAlertFromRow(row))
	}
	return alerts, nil
}

func ladoAlertFromRow(row gen.ListLadoAlertsSinceRow) domain.LadoAlert {
	return domain.LadoAlert{
		ID:        domain.LadoAlertID(row.ID),
		TaskID:    domain.LadoTaskID(row.TaskID),
		SessionID: domain.SessionID(row.SessionID),
		Kind:      domain.LadoAlertKind(row.Kind),
		Level:     int(row.Level),
		Value:     row.Value,
		Threshold: row.Threshold,
		CreatedAt: row.CreatedAt.UTC(),
		SeenAt:    ladoTimeFromNull(row.SeenAt),
		ProjectID: domain.ProjectID(row.ProjectID),
		IssueRef:  domain.IssueID(row.IssueRef.String),
	}
}

// ListLadoPlanWorkerSessions reads every plan worker session of a task's
// plan requests, each role and attempt, in the order they started. A
// request's own session_id names only its last designer; this reads the
// earlier attempts as well, from the sessions' client request ids.
func (s *Store) ListLadoPlanWorkerSessions(ctx context.Context, task domain.LadoTaskID) ([]domain.LadoPlanWorkerSession, error) {
	rows, err := s.qr.ListLadoPlanWorkerSessionsByTask(ctx, string(task))
	if err != nil {
		return nil, fmt.Errorf("list LADO plan worker sessions: %w", err)
	}
	sessions := make([]domain.LadoPlanWorkerSession, 0, len(rows))
	for _, row := range rows {
		req, role, attempt, ok := domain.ParseLadoPlanWorkerClientRequestID(row.ClientRequestID)
		if !ok || string(req) != row.RequestID {
			// Another request's id that only shares the prefix, as
			// "lado:plan/r1/" does with a request named "r1/x".
			continue
		}
		sessions = append(sessions, domain.LadoPlanWorkerSession{
			SessionID: row.SessionID,
			RequestID: req,
			Role:      role,
			Attempt:   attempt,
			CreatedAt: row.CreatedAt.UTC(),
		})
	}
	return sessions, nil
}

// LadoSessionSubagentUsage adds up what a session's subagents used. A
// session without usage, or one AO no longer has, used nothing.
func (s *Store) LadoSessionSubagentUsage(ctx context.Context, session domain.SessionID) (domain.LadoSubagentUsage, error) {
	row, err := s.qr.GetLadoSessionSubagentUsage(ctx, session)
	if err != nil {
		return domain.LadoSubagentUsage{}, fmt.Errorf("read LADO subagent usage: %w", err)
	}
	return domain.LadoSubagentUsage{
		SessionID:    session,
		Events:       row.EventCount,
		InputTokens:  row.InputTokens,
		OutputTokens: row.OutputTokens,
		CostNanos:    row.PricedTotalNanos,
		PricedEvents: row.PricedEventCount,
	}, nil
}

// ListLadoSessionTranscriptSources reads the conversation files of a session
// that AO's usage pipeline reads: Claude's main transcript and Codex rollouts.
func (s *Store) ListLadoSessionTranscriptSources(ctx context.Context, session domain.SessionID) ([]domain.LadoTranscriptSource, error) {
	rows, err := s.qr.ListLadoSessionTranscriptSources(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("list LADO transcript sources: %w", err)
	}
	sources := make([]domain.LadoTranscriptSource, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.ArtifactPath) == "" {
			continue
		}
		sources = append(sources, domain.LadoTranscriptSource{Kind: row.Kind, Path: row.ArtifactPath})
	}
	return sources, nil
}

func ladoBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
