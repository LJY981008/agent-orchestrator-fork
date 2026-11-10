package ladowatch

// The accounts at their usage limit (issue #92): which login each limit was
// recorded on, finding that the key now runs on another login, releasing a
// limit for any reason in one place, and telling the orchestrators.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// identityCheckEvery is how often LADO reads who a limited key is logged
	// in to.
	identityCheckEvery = time.Minute
	// identityReadTimeout bounds one read of a key's login.
	identityReadTimeout = 10 * time.Second
)

// Audit actors of a release.
const (
	actorLado = "lado"
	actorAPI  = "api"
)

// identityCheck is the last read of who one account key is logged in to.
type identityCheck struct {
	at      time.Time
	running bool
}

// fingerprint reads the fingerprint of the login accountID runs on, within
// identityReadTimeout. "" is unknown: no Identities, a failed read, or a login
// LADO cannot tell from another.
func (s *Service) fingerprint(ctx context.Context, key, accountID string) string {
	if s.deps.Identities == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, identityReadTimeout)
	defer cancel()
	fp, err := s.deps.Identities.AccountFingerprint(ctx, accountID)
	if err != nil {
		s.deps.Log.Debug("LADO could not read an account's login", "account", key, "err", err)
		return ""
	}
	return fp
}

// accountChanged reports whether the live limit l was released because its
// key now runs on another login than the one recorded with it. LADO reads the
// login at most once a minute a key; while a read runs, or within the minute,
// the limit holds as it is. A limit recorded without a login is never checked,
// and a login LADO cannot read keeps the limit.
func (s *Service) accountChanged(ctx context.Context, l domain.LadoAccountLimit, now time.Time) bool {
	if s.deps.Identities == nil || l.Identity == "" || !l.Active(now) {
		return false
	}
	_, accountID, ok := domain.ParseLadoAccountKey(l.AccountKey)
	if !ok {
		return false
	}
	s.mu.Lock()
	check := s.checks[l.AccountKey]
	if check == nil {
		check = &identityCheck{}
		s.checks[l.AccountKey] = check
	}
	if check.running || (!check.at.IsZero() && now.Before(check.at.Add(identityCheckEvery))) {
		s.mu.Unlock()
		return false
	}
	check.running, check.at = true, now
	s.mu.Unlock()

	fp := s.fingerprint(ctx, l.AccountKey, accountID)

	s.mu.Lock()
	check.running = false
	s.mu.Unlock()
	if fp == "" || fp == l.Identity {
		return false
	}
	if _, _, err := s.release(ctx, l.AccountKey, domain.LadoLimitReleaseAccountChanged, actorLado, "", now); err != nil {
		s.deps.Log.Warn("LADO could not release an account whose login changed", "account", l.AccountKey, "err", err)
		return false
	}
	// Released by this check or by another at the same time: either way the
	// key runs on another login now.
	return true
}

// release releases the limit of key for reason at now, once: of callers racing
// to release it only the first gets released true, and only it counts the
// waits, audits and tells every LADO mode project's orchestrator. waiting is
// the account_limited waits LADO counted then.
func (s *Service) release(ctx context.Context, key string, reason domain.LadoLimitReleaseReason, actor, note string, now time.Time) (released bool, waiting int, err error) {
	ok, err := s.deps.Store.MarkLadoAccountLimitReleased(ctx, key, reason, now)
	if err != nil || !ok {
		return false, 0, err
	}
	// What follows is told after the release, which already holds.
	ctx = context.WithoutCancel(ctx)
	if waiting, err = s.deps.Store.CountLadoAccountLimitedWaits(ctx); err != nil {
		s.deps.Log.Warn("LADO could not count the account limit waits", "account", key, "err", err)
		waiting = 0
	} else if err := s.deps.Store.SetLadoAccountLimitReleasedWaiting(ctx, key, waiting); err != nil {
		s.deps.Log.Warn("LADO could not record the account limit waits", "account", key, "err", err)
	}
	s.auditAccount(ctx, actor, "lado.limit_released", key, now, map[string]any{
		"reason": reason, "waiting": waiting, "note": note,
	})
	s.notice(ctx, "account_released", key, strconv.FormatInt(now.UnixNano(), 10),
		domain.LadoNoticeAccountReleased(key, reason, waiting), now)
	s.deps.Log.Info("LADO released an account's usage limit", "account", key, "reason", reason, "waiting", waiting)
	return true, waiting, nil
}

// notice tells every LADO mode project's orchestrator text once, under
// "<kind>:<project>:<key>:<stamp>".
func (s *Service) notice(ctx context.Context, kind, key, stamp, text string, now time.Time) {
	projects, err := s.deps.Store.ListLadoModeProjectIDs(ctx)
	if err != nil {
		s.deps.Log.Warn("LADO could not list the projects to tell of an account limit", "account", key, "err", err)
		return
	}
	for _, project := range projects {
		if _, err := s.deps.Store.AddLadoOrchestratorNotice(ctx, domain.LadoOrchestratorNotice{
			ProjectID: project, Key: kind + ":" + string(project) + ":" + key + ":" + stamp,
			Text: text, OccurredAt: now, CreatedAt: now,
		}); err != nil {
			s.deps.Log.Warn("LADO could not tell an orchestrator of an account limit", "project", project, "account", key, "err", err)
		}
	}
}

func (s *Service) auditAccount(ctx context.Context, actor, action, key string, at time.Time, payload map[string]any) {
	data, _ := json.Marshal(payload)
	if _, err := s.deps.Store.AppendLadoAuditEvent(context.WithoutCancel(ctx), domain.LadoAuditEvent{
		At: at, Actor: actor, Action: action, SubjectKind: "account", SubjectID: key, Payload: data,
	}); err != nil {
		s.deps.Log.Warn("LADO could not audit an account limit event", "action", action, "account", key, "err", err)
	}
}

// releaseLimits releases the limits not yet released whose reset passed, and
// those whose key now runs on another login. A released limit is left alone.
func (s *Service) releaseLimits(ctx context.Context, now time.Time) error {
	limits, err := s.deps.Store.ListLadoAccountLimits(ctx)
	if err != nil {
		return fmt.Errorf("list the LADO account limits: %w", err)
	}
	for _, l := range limits {
		if l.Released() {
			continue
		}
		if l.Active(now) {
			s.accountChanged(ctx, l, now)
			continue
		}
		if _, _, err := s.release(ctx, l.AccountKey, domain.LadoLimitReleaseReset, actorLado, "", now); err != nil {
			return fmt.Errorf("release the LADO account limit %s: %w", l.AccountKey, err)
		}
	}
	return nil
}

// recordAccountLimit records the limit of key that session hit, with the
// login it runs on, and tells the orchestrators when the limit is new. A
// session made before LADO released key early because its login changed or a
// person said so may still run on the old login: its limit stays the
// session's, and the account is not held again (plan decision 8).
func (s *Service) recordAccountLimit(ctx context.Context, key string, session domain.SessionRecord, msg limitMessage, now time.Time) error {
	old, found, err := s.deps.Store.LadoAccountLimit(ctx, key)
	if err != nil {
		return err
	}
	if found && old.SparesSessionsBefore() && session.CreatedAt.Before(old.ReleasedAt) {
		s.deps.Log.Info("LADO kept the account free of a limit an older session hit", "account", key, "session", session.ID,
			"released", old.ReleasedAt, "reason", old.ReleaseReason)
		return nil
	}
	live := found && old.Active(now)
	identity := ""
	// A live limit keeps the login it was first recorded with.
	if !live || old.Identity == "" {
		if _, accountID, ok := domain.ParseLadoAccountKey(key); ok {
			identity = s.fingerprint(ctx, key, accountID)
		}
	}
	if err := s.deps.Store.RecordLadoAccountLimit(ctx, domain.LadoAccountLimit{
		AccountKey: key, SessionID: session.ID, Identity: identity, ResetAt: msg.ResetAt, ResetGuessed: msg.Guessed,
		DetectedAt: now, UpdatedAt: now,
	}); err != nil {
		return err
	}
	if !live {
		s.notice(ctx, "account_limit", key, strconv.FormatInt(now.Unix(), 10),
			domain.LadoNoticeAccountLimited(key, msg.ResetAt, msg.Guessed, now, s.deps.Local), now)
	}
	return nil
}

// releasedEarly returns when the limit of the account the worker runs on was
// released before its reset after the worker's stop: by a changed login, a
// person, or another worker's completed turn. ok is false otherwise, and when
// LADO cannot tell the worker's account.
func (s *Service) releasedEarly(ctx context.Context, w worker) (time.Time, bool) {
	key, ok := s.accountKey(ctx, w)
	if !ok {
		return time.Time{}, false
	}
	l, found, err := s.deps.Store.LadoAccountLimit(ctx, key)
	if err != nil {
		s.deps.Log.Warn("LADO could not read a worker's account limit", "session", w.session.ID, "account", key, "err", err)
		return time.Time{}, false
	}
	if !found || !l.ReleasedEarly() || !l.ReleasedAt.After(w.watch.LimitSeenAt) {
		return time.Time{}, false
	}
	return l.ReleasedAt, true
}

// AccountLimits lists every account limit LADO recorded, released ones too.
func (s *Service) AccountLimits(ctx context.Context) ([]domain.LadoAccountLimit, error) {
	return s.deps.Store.ListLadoAccountLimits(ctx)
}

// ReleaseAccountLimit releases the limit of key because a person said so, with
// their note, and returns it released and the account_limited waits LADO
// counted then. It is domain.ErrLadoAccountLimitNotFound without a limit of
// key, and domain.ErrLadoAccountLimitNotActive when the limit was already
// released or its reset passed. The limited workers on key resume on the next
// watch tick.
func (s *Service) ReleaseAccountLimit(ctx context.Context, key, note string) (domain.LadoAccountLimit, int, error) {
	l, found, err := s.deps.Store.LadoAccountLimit(ctx, key)
	if err != nil {
		return domain.LadoAccountLimit{}, 0, err
	}
	if !found {
		return domain.LadoAccountLimit{}, 0, domain.ErrLadoAccountLimitNotFound
	}
	now := s.deps.Clock().UTC()
	if !l.Active(now) {
		return l, 0, domain.ErrLadoAccountLimitNotActive
	}
	released, waiting, err := s.release(ctx, key, domain.LadoLimitReleaseManual, actorAPI, note, now)
	if err != nil {
		return domain.LadoAccountLimit{}, 0, err
	}
	if !released {
		return l, 0, domain.ErrLadoAccountLimitNotActive
	}
	l, found, err = s.deps.Store.LadoAccountLimit(ctx, key)
	if err != nil {
		return domain.LadoAccountLimit{}, 0, err
	}
	if !found {
		return domain.LadoAccountLimit{}, 0, domain.ErrLadoAccountLimitNotFound
	}
	return l, waiting, nil
}
