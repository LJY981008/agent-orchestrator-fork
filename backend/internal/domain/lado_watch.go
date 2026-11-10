package domain

// LADO fork: worker watch (issue #42). LADO caps the context a Claude Code
// session fills before it compacts, adds up what a task's sessions spend,
// raises one-line alerts when a threshold is crossed, and wakes a worker that
// stopped at a usage limit or went quiet. The settings, the session watch, the
// account limits and the alerts live in migrations_lado/0012_lado_watch.sql.

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// LadoWatchGlobalScope is the project id of the global settings row.
const LadoWatchGlobalScope ProjectID = ""

// LadoWatchSettings are the watch settings a project runs with: the global
// row's values over the built-in defaults, and the project's over both.
type LadoWatchSettings struct {
	// CompactWindow is the token count Claude Code compacts at,
	// CLAUDE_CODE_AUTO_COMPACT_WINDOW.
	CompactWindow int
	// TaskCostCents and SessionCostCents are the estimated cost, in cents, of
	// a task's sessions together and of one session, past which LADO alerts.
	TaskCostCents    int64
	SessionCostCents int64
	// SessionCompactions is how many times one session may compact before
	// LADO alerts.
	SessionCompactions int
	// StallMinutes is how long a running worker may rest without a report
	// before LADO takes it as stalled.
	StallMinutes int
	// WakeEnabled lets LADO send its two wake messages to the project's
	// workers.
	WakeEnabled bool
}

// Stall is StallMinutes as a duration.
func (s LadoWatchSettings) Stall() time.Duration { return time.Duration(s.StallMinutes) * time.Minute }

// LadoWatchOverrides is one settings row: the global row or a project's. A
// nil field follows the row below it, the global row for a project and the
// default for the global row.
type LadoWatchOverrides struct {
	CompactWindow      *int
	TaskCostCents      *int64
	SessionCostCents   *int64
	SessionCompactions *int
	StallMinutes       *int
	WakeEnabled        *bool
	UpdatedAt          time.Time
}

// Built-in defaults (owner's decision, plan decisions 1 and 2).
const (
	LadoDefaultCompactWindow      = 200_000
	LadoDefaultTaskCostCents      = 100_00
	LadoDefaultSessionCostCents   = 30_00
	LadoDefaultSessionCompactions = 3
	LadoDefaultStallMinutes       = 120
	LadoDefaultWakeEnabled        = true
)

// Ranges a settings value must fall in. The compact window range is Claude
// Code's own; values outside it are refused, not clamped.
const (
	LadoMinCompactWindow      = 100_000
	LadoMaxCompactWindow      = 1_000_000
	LadoMinCostCents          = 1
	LadoMaxCostCents          = 100_000_00
	LadoMinSessionCompactions = 1
	LadoMaxSessionCompactions = 100
	LadoMinStallMinutes       = 10
	LadoMaxStallMinutes       = 7 * 24 * 60
)

// LadoWatchDefaults are the settings without any row.
func LadoWatchDefaults() LadoWatchSettings {
	return LadoWatchSettings{
		CompactWindow:      LadoDefaultCompactWindow,
		TaskCostCents:      LadoDefaultTaskCostCents,
		SessionCostCents:   LadoDefaultSessionCostCents,
		SessionCompactions: LadoDefaultSessionCompactions,
		StallMinutes:       LadoDefaultStallMinutes,
		WakeEnabled:        LadoDefaultWakeEnabled,
	}
}

// ErrLadoWatchSettingOutOfRange marks a settings value outside its range.
var ErrLadoWatchSettingOutOfRange = errors.New("LADO watch setting out of range")

// Validate reports the first value outside its range, wrapping
// ErrLadoWatchSettingOutOfRange. Nil values are valid.
func (o LadoWatchOverrides) Validate() error {
	check := func(name string, v, lo, hi int64) error {
		if v < lo || v > hi {
			return fmt.Errorf("%w: %s %d is not between %d and %d", ErrLadoWatchSettingOutOfRange, name, v, lo, hi)
		}
		return nil
	}
	if o.CompactWindow != nil {
		if err := check("compactWindow", int64(*o.CompactWindow), LadoMinCompactWindow, LadoMaxCompactWindow); err != nil {
			return err
		}
	}
	if o.TaskCostCents != nil {
		if err := check("taskCostCents", *o.TaskCostCents, LadoMinCostCents, LadoMaxCostCents); err != nil {
			return err
		}
	}
	if o.SessionCostCents != nil {
		if err := check("sessionCostCents", *o.SessionCostCents, LadoMinCostCents, LadoMaxCostCents); err != nil {
			return err
		}
	}
	if o.SessionCompactions != nil {
		if err := check("sessionCompactions", int64(*o.SessionCompactions), LadoMinSessionCompactions, LadoMaxSessionCompactions); err != nil {
			return err
		}
	}
	if o.StallMinutes != nil {
		if err := check("stallMinutes", int64(*o.StallMinutes), LadoMinStallMinutes, LadoMaxStallMinutes); err != nil {
			return err
		}
	}
	return nil
}

// LadoWatchEffective lays the global row over the defaults and the project's
// row over both. A value out of range, which the store refuses to write,
// falls back to the row below it.
func LadoWatchEffective(global, project LadoWatchOverrides) LadoWatchSettings {
	return project.applyValid(global.applyValid(LadoWatchDefaults()))
}

// applyValid lays o's set values that are in range over s.
func (o LadoWatchOverrides) applyValid(s LadoWatchSettings) LadoWatchSettings {
	if o.CompactWindow != nil && (LadoWatchOverrides{CompactWindow: o.CompactWindow}).Validate() == nil {
		s.CompactWindow = *o.CompactWindow
	}
	if o.TaskCostCents != nil && (LadoWatchOverrides{TaskCostCents: o.TaskCostCents}).Validate() == nil {
		s.TaskCostCents = *o.TaskCostCents
	}
	if o.SessionCostCents != nil && (LadoWatchOverrides{SessionCostCents: o.SessionCostCents}).Validate() == nil {
		s.SessionCostCents = *o.SessionCostCents
	}
	if o.SessionCompactions != nil && (LadoWatchOverrides{SessionCompactions: o.SessionCompactions}).Validate() == nil {
		s.SessionCompactions = *o.SessionCompactions
	}
	if o.StallMinutes != nil && (LadoWatchOverrides{StallMinutes: o.StallMinutes}).Validate() == nil {
		s.StallMinutes = *o.StallMinutes
	}
	if o.WakeEnabled != nil {
		s.WakeEnabled = *o.WakeEnabled
	}
	return s
}

// The only two messages LADO sends a worker (plan decision 8). It never
// sends free text.
const (
	LadoWakeLimitResumeText = "사용 한도가 풀렸습니다. 멈춘 곳부터 이어서 진행하세요."
	LadoWakeStallNudgeText  = "지금 상태를 보고하거나 이어서 진행하세요."
)

// Wake limits.
const (
	// LadoWakeMaxResumes is how many limit resumes one session gets.
	LadoWakeMaxResumes = 3
	// LadoWakeMaxNudges is how many stall nudges one session gets; after
	// that LADO only alerts.
	LadoWakeMaxNudges = 1
	// LadoWakeResumeDelay is how long after the reset LADO resumes a worker.
	LadoWakeResumeDelay = 5 * time.Minute
	// LadoLimitGuessedReset is when LADO takes a limit to reset when it could
	// not read the time from the limit message.
	LadoLimitGuessedReset = 60 * time.Minute
)

// Waiting reasons a queue entry, a part and a plan request share while LADO
// holds a new worker without spending an attempt.
const (
	// LadoWaitingAccountLimited means the account the worker would run on is
	// at its usage limit; LADO starts it after the reset.
	LadoWaitingAccountLimited = "account_limited"
	// LadoWaitingAgentUnavailable means the agent's executable could not be
	// found or run before the start.
	LadoWaitingAgentUnavailable = "agent_unavailable"
)

// LadoAccountKey is the key of the account a session of harness runs on:
// "<provider>:<account id>", the provider's device account when accountID
// is empty. ok is false for a harness without accounts.
func LadoAccountKey(harness AgentHarness, accountID string) (string, bool) {
	p, ok := AgentAccountProviderFor(harness)
	if !ok {
		return "", false
	}
	if accountID == "" {
		accountID = DeviceAgentAccountID(p)
	}
	return string(p) + ":" + accountID, true
}

// LadoLimitState says whether a session stopped at a usage limit.
type LadoLimitState string

// Limit states.
const (
	LadoLimitNone    LadoLimitState = ""
	LadoLimitLimited LadoLimitState = "limited"
)

// LadoStallState says whether a running worker went quiet.
type LadoStallState string

// Stall states.
const (
	LadoStallNone    LadoStallState = ""
	LadoStallStalled LadoStallState = "stalled"
)

// LadoSessionWatch is what LADO watches of one task session. Its three groups
// of fields have separate writers, and each writes only its own.
type LadoSessionWatch struct {
	SessionID SessionID
	TaskID    LadoTaskID

	LadoWatchCompactions
	LadoWatchLimit
	LadoWatchStall

	CreatedAt time.Time
	UpdatedAt time.Time
}

// LadoWatchCompactions is the compaction counter's group: how far it read
// each conversation file, by path, and how many compactions it counted.
type LadoWatchCompactions struct {
	ReadOffsets map[string]int64
	Compactions int
	// CompactedAt is when the counter last found a compaction, zero before.
	CompactedAt time.Time
}

// LadoWatchLimit is the limit watch's group.
type LadoWatchLimit struct {
	LimitState LadoLimitState
	// LimitResetAt is when the limit resets; LimitResetGuessed marks a time
	// LADO guessed (LadoLimitGuessedReset).
	LimitResetAt      time.Time
	LimitResetGuessed bool
	// LimitSeenAt is when LADO saw the limit message.
	LimitSeenAt time.Time
	// Resumes is how many resume messages LADO sent, ResumedAt the last.
	Resumes   int
	ResumedAt time.Time
	// Stops is how many limit stops LADO recorded for the session; each
	// stop's alert has its number as its level.
	Stops int
}

// LadoWatchStall is the stall watch's group.
type LadoWatchStall struct {
	StallState LadoStallState
	StalledAt  time.Time
	// Nudges is how many nudges LADO sent, NudgedAt the last.
	Nudges   int
	NudgedAt time.Time
	// Stalls is how many stalls LADO recorded for the session; each stall's
	// alert has its number as its level.
	Stalls int
}

// LadoAccountLimit is an account at its usage limit, or one whose limit LADO
// released (issue #92): the row stays so LADO knows when and why it let go.
type LadoAccountLimit struct {
	// AccountKey is LadoAccountKey's key.
	AccountKey string
	// SessionID is the session LADO saw the limit in, empty if none.
	SessionID SessionID
	// Identity is the fingerprint of the account the key was logged in to
	// when LADO recorded the limit ("sha256:<hex>", never the email), empty
	// when LADO could not tell. A live limit keeps the first one recorded.
	Identity     string
	ResetAt      time.Time
	ResetGuessed bool
	DetectedAt   time.Time
	UpdatedAt    time.Time
	// ReleasedAt is when LADO released the limit, zero while it holds;
	// ReleaseReason says why and ReleasedWaiting how many account_limited
	// waits there were then.
	ReleasedAt      time.Time
	ReleaseReason   LadoLimitReleaseReason
	ReleasedWaiting int
}

// Active reports whether the limit still holds at now: it was not released
// and its reset has not come.
func (l LadoAccountLimit) Active(now time.Time) bool {
	return l.ReleasedAt.IsZero() && now.Before(l.ResetAt)
}

// Released reports whether LADO released the limit.
func (l LadoAccountLimit) Released() bool { return !l.ReleasedAt.IsZero() }

// ReleasedEarly reports whether the limit was released before its reset for
// a reason other than the reset itself, so the workers it stopped need not
// wait for the reset.
func (l LadoAccountLimit) ReleasedEarly() bool {
	return l.Released() && l.ReleaseReason != LadoLimitReleaseReset
}

// SparesSessionsBefore reports whether a session made before the release no
// longer limits the account again: the limit was released because the account
// changed or a person said so, and such a session may still run on the old
// login (plan decision 8).
func (l LadoAccountLimit) SparesSessionsBefore() bool {
	return l.Released() && (l.ReleaseReason == LadoLimitReleaseAccountChanged || l.ReleaseReason == LadoLimitReleaseManual)
}

// LadoLimitReleaseReason says why LADO released an account's limit.
type LadoLimitReleaseReason string

// Release reasons.
const (
	LadoLimitReleaseNone LadoLimitReleaseReason = ""
	// LadoLimitReleaseReset: the reset time came.
	LadoLimitReleaseReset LadoLimitReleaseReason = "reset"
	// LadoLimitReleaseAccountChanged: the key is logged in to another
	// account than the one that hit the limit.
	LadoLimitReleaseAccountChanged LadoLimitReleaseReason = "account_changed"
	// LadoLimitReleaseManual: a person released it through the API.
	LadoLimitReleaseManual LadoLimitReleaseReason = "manual"
	// LadoLimitReleaseTurnCompleted: a worker stopped at the limit completed
	// its next turn.
	LadoLimitReleaseTurnCompleted LadoLimitReleaseReason = "turn_completed"
)

// LadoLimitReleaseReasons lists the release reasons.
var LadoLimitReleaseReasons = []LadoLimitReleaseReason{
	LadoLimitReleaseReset, LadoLimitReleaseAccountChanged, LadoLimitReleaseManual, LadoLimitReleaseTurnCompleted,
}

// Valid reports whether r is a known reason; the empty reason is not one.
func (r LadoLimitReleaseReason) Valid() bool {
	for _, known := range LadoLimitReleaseReasons {
		if r == known {
			return true
		}
	}
	return false
}

// Errors of releasing an account limit by hand.
var (
	// ErrLadoAccountLimitNotFound: LADO has no limit of the account.
	ErrLadoAccountLimitNotFound = errors.New("LADO account limit not found")
	// ErrLadoAccountLimitNotActive: the limit was already released or its
	// reset passed.
	ErrLadoAccountLimitNotActive = errors.New("LADO account limit is not active")
)

// ladoAccountKeyPattern is what ParseLadoAccountKey takes.
var ladoAccountKeyPattern = regexp.MustCompile(`^(claude|codex):([A-Za-z0-9._-]+)$`)

// ParseLadoAccountKey splits a LadoAccountKey key into its provider and
// account id. ok is false for anything else, such as an unknown provider, an
// empty account or characters an account id never has.
func ParseLadoAccountKey(key string) (AgentAccountProvider, string, bool) {
	m := ladoAccountKeyPattern.FindStringSubmatch(key)
	if m == nil {
		return "", "", false
	}
	return AgentAccountProvider(m[1]), m[2], true
}

// The account limit notice lines, after LadoNoticePrefix (issue #92).
const (
	ladoNoticeAccountLimited       = "%s 한도 — 새 워커 대기, %s 리셋"
	ladoNoticeAccountLimitedGuess  = "%s 한도 — 새 워커 대기, %s 리셋 추정"
	ladoNoticeAccountReleased      = "%s 한도 해제(%s) — 계정 한도 대기 %d건 다시 확인"
	ladoNoticeAccountReasonUnknown = "알 수 없는 이유"
)

// ladoNoticeReleaseReasons are the short reasons a release notice gives.
var ladoNoticeReleaseReasons = map[LadoLimitReleaseReason]string{
	LadoLimitReleaseReset:          "리셋",
	LadoLimitReleaseAccountChanged: "계정 바뀜",
	LadoLimitReleaseManual:         "수동 해제",
	LadoLimitReleaseTurnCompleted:  "작업 재개 확인",
}

// ladoNoticeAccount names the account of key: "Claude 계정" for the device
// account and "Claude 계정(<id>)" for a named one. A key ParseLadoAccountKey
// refuses is named as it is.
func ladoNoticeAccount(key string) string {
	provider, id, ok := ParseLadoAccountKey(key)
	if !ok {
		return "계정(" + key + ")"
	}
	name := "Claude"
	if provider == AgentAccountProviderCodex {
		name = "Codex"
	}
	if id == DeviceAgentAccountID(provider) {
		return name + " 계정"
	}
	return name + " 계정(" + id + ")"
}

// ladoNoticeClock is at in loc as "15:04", or "1/2 15:04" when it is not on
// now's day there. A nil loc is time.Local.
func ladoNoticeClock(at, now time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	at, now = at.In(loc), now.In(loc)
	ay, am, ad := at.Date()
	if ny, nm, nd := now.Date(); ay == ny && am == nm && ad == nd {
		return at.Format("15:04")
	}
	return at.Format("1/2 15:04")
}

// LadoNoticeAccountLimited is the orchestrator line for an account that hit
// its limit: "LADO 알림: Claude 계정 한도 — 새 워커 대기, 19:00 리셋", with
// "리셋 추정" when LADO guessed the reset. The time is in loc (nil is
// time.Local), with the date when it is not on now's day.
func LadoNoticeAccountLimited(key string, resetAt time.Time, guessed bool, now time.Time, loc *time.Location) string {
	format := ladoNoticeAccountLimited
	if guessed {
		format = ladoNoticeAccountLimitedGuess
	}
	return ladoNoticeLine(format, ladoNoticeAccount(key), ladoNoticeClock(resetAt, now, loc))
}

// LadoNoticeAccountReleased is the orchestrator line for a released limit:
// "LADO 알림: Claude 계정 한도 해제(계정 바뀜) — 계정 한도 대기 n건 다시
// 확인". waiting is every account_limited wait, of any provider: a wait does
// not record which provider's limit holds it.
func LadoNoticeAccountReleased(key string, reason LadoLimitReleaseReason, waiting int) string {
	text, ok := ladoNoticeReleaseReasons[reason]
	if !ok {
		text = ladoNoticeAccountReasonUnknown
	}
	return ladoNoticeLine(ladoNoticeAccountReleased, ladoNoticeAccount(key), text, waiting)
}

// LadoAlertID identifies an alert.
type LadoAlertID int64

// LadoAlertKind is what an alert tells of.
type LadoAlertKind string

// Alert kinds.
const (
	// LadoAlertTaskCost: the task's sessions together cost more than the
	// threshold. Its session is empty. Value and Threshold are cents.
	LadoAlertTaskCost LadoAlertKind = "task_cost"
	// LadoAlertSessionCost: one session cost more than the threshold. Value
	// and Threshold are cents.
	LadoAlertSessionCost LadoAlertKind = "session_cost"
	// LadoAlertSessionCompactions: one session compacted as often as the
	// threshold. Value and Threshold are counts.
	LadoAlertSessionCompactions LadoAlertKind = "session_compactions"
	// LadoAlertLimited: a worker stopped at a usage limit. Level is the
	// stop's number in the session, Value the reset time in Unix seconds.
	LadoAlertLimited LadoAlertKind = "limited"
	// LadoAlertLimitResumed: LADO sent the limit resume message. Level and
	// Value are the resume's number, Threshold LadoWakeMaxResumes.
	LadoAlertLimitResumed LadoAlertKind = "limit_resumed"
	// LadoAlertStallNudged: LADO sent the stall nudge. Value is the stall
	// time in minutes.
	LadoAlertStallNudged LadoAlertKind = "stall_nudged"
	// LadoAlertStalled: a worker stayed quiet after the nudge, or the project
	// does not let LADO wake it. Level is the stall's number in the session,
	// Value the stall time in minutes.
	LadoAlertStalled LadoAlertKind = "stalled"
	// LadoAlertAgentUnavailable: LADO holds a new worker because the agent's
	// executable is unavailable. Its session is empty.
	LadoAlertAgentUnavailable LadoAlertKind = "agent_unavailable"
	// LadoAlertConflictUnresolved: LADO's resolve workers could not resolve
	// an execution's PR (LadoResolveNeedsHuman). Its session is the
	// execution's, Value the failed attempts in a row.
	LadoAlertConflictUnresolved LadoAlertKind = "conflict_unresolved"
)

// LadoAlertKinds lists the alert kinds.
var LadoAlertKinds = []LadoAlertKind{
	LadoAlertTaskCost, LadoAlertSessionCost, LadoAlertSessionCompactions, LadoAlertLimited,
	LadoAlertLimitResumed, LadoAlertStallNudged, LadoAlertStalled, LadoAlertAgentUnavailable,
	LadoAlertConflictUnresolved,
}

// Valid reports whether k is a known kind.
func (k LadoAlertKind) Valid() bool {
	for _, known := range LadoAlertKinds {
		if k == known {
			return true
		}
	}
	return false
}

// Cost alert levels: an alert at the threshold, and one more past twice it.
const (
	LadoAlertLevelCrossed = 1
	LadoAlertLevelDoubled = 2
)

// LadoAlert is one one-line alert. (TaskID, SessionID, Kind, Level) is
// raised once.
type LadoAlert struct {
	ID        LadoAlertID
	TaskID    LadoTaskID
	SessionID SessionID
	Kind      LadoAlertKind
	Level     int
	Value     int64
	Threshold int64
	CreatedAt time.Time
	SeenAt    time.Time
	// ProjectID and IssueRef are the task's, filled where a read joins them.
	ProjectID ProjectID
	IssueRef  IssueID
}

// Seen reports whether the owner marked the alert seen.
func (a LadoAlert) Seen() bool { return !a.SeenAt.IsZero() }

// NotificationID is the desktop notification id of the alert.
func (a LadoAlert) NotificationID() string { return fmt.Sprintf("lado-alert:%d", a.ID) }

// The alert lines, after LadoNoticePrefix. They name the task, the session
// and the reason and nothing more.
const (
	ladoAlertTaskCost           = "#%d 작업 비용 추정 %s, 기준 %s 넘음"
	ladoAlertTaskCostDoubled    = "#%d 작업 비용 추정 %s, 기준 %s 의 2배 넘음"
	ladoAlertSessionCost        = "#%d 세션 %s 비용 추정 %s, 기준 %s 넘음"
	ladoAlertSessionCostDoubled = "#%d 세션 %s 비용 추정 %s, 기준 %s 의 2배 넘음"
	ladoAlertCompactions        = "#%d 세션 %s compact %d번(기준 %d번) — 파트를 다시 쪼갤지 확인하세요"
	ladoAlertLimited            = "#%d 세션 %s 사용 한도로 멈춤"
	ladoAlertLimitResumed       = "#%d 세션 %s 에 한도 재개 문구를 보냄(%d/%d)"
	ladoAlertStallNudged        = "#%d 세션 %s 가 %d분 넘게 멈춰 재촉 문구를 보냄"
	ladoAlertStalled            = "#%d 세션 %s 가 %d분 넘게 멈춤"
	ladoAlertAgentUnavailable   = "#%d 에이전트 실행 파일을 쓸 수 없어 워커를 기다리게 함"
	ladoAlertConflictUnresolved = "#%d 실행 %s 의 PR 충돌을 LADO가 풀지 못함 — 직접 해결하세요"
	ladoAlertUnknown            = "#%d 세션 %s 알림 %s"
)

// Line is the alert's one line for the orchestrator and the desktop:
// LadoNoticePrefix, the task's issue number and the reason.
func (a LadoAlert) Line(issue int) string {
	session := string(a.SessionID)
	doubled := a.Level >= LadoAlertLevelDoubled
	switch a.Kind {
	case LadoAlertTaskCost:
		if doubled {
			return ladoNoticeLine(ladoAlertTaskCostDoubled, issue, LadoCents(a.Value), LadoCents(a.Threshold))
		}
		return ladoNoticeLine(ladoAlertTaskCost, issue, LadoCents(a.Value), LadoCents(a.Threshold))
	case LadoAlertSessionCost:
		if doubled {
			return ladoNoticeLine(ladoAlertSessionCostDoubled, issue, session, LadoCents(a.Value), LadoCents(a.Threshold))
		}
		return ladoNoticeLine(ladoAlertSessionCost, issue, session, LadoCents(a.Value), LadoCents(a.Threshold))
	case LadoAlertSessionCompactions:
		return ladoNoticeLine(ladoAlertCompactions, issue, session, a.Value, a.Threshold)
	case LadoAlertLimited:
		return ladoNoticeLine(ladoAlertLimited, issue, session)
	case LadoAlertLimitResumed:
		return ladoNoticeLine(ladoAlertLimitResumed, issue, session, a.Value, a.Threshold)
	case LadoAlertStallNudged:
		return ladoNoticeLine(ladoAlertStallNudged, issue, session, a.Value)
	case LadoAlertStalled:
		return ladoNoticeLine(ladoAlertStalled, issue, session, a.Value)
	case LadoAlertAgentUnavailable:
		return ladoNoticeLine(ladoAlertAgentUnavailable, issue)
	case LadoAlertConflictUnresolved:
		return ladoNoticeLine(ladoAlertConflictUnresolved, issue, session)
	}
	return ladoNoticeLine(ladoAlertUnknown, issue, session, a.Kind)
}

// LadoCents formats cents as dollars: "$12.34".
func LadoCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100)
}

// LadoPlanWorkerSession is one plan worker session of a task, any attempt:
// the request it worked for, its role and attempt as its client request id
// names them.
type LadoPlanWorkerSession struct {
	SessionID SessionID
	RequestID LadoPlanRequestID
	Role      LadoPlanRole
	Attempt   int
	CreatedAt time.Time
}

// SessionRole is the session's role in the task's timings and usage.
func (s LadoPlanWorkerSession) SessionRole() LadoSessionRole {
	if s.Role == LadoPlanRoleCritic {
		return LadoSessionCritic
	}
	return LadoSessionDesigner
}

// LadoSubagentUsage is what a session's subagents used: their events' tokens
// and estimated cost.
type LadoSubagentUsage struct {
	SessionID SessionID
	Events    int64
	// InputTokens and OutputTokens add up the events that report them.
	InputTokens  int64
	OutputTokens int64
	// CostNanos adds up the priced events, PricedEvents counts them.
	CostNanos    int64
	PricedEvents int64
}

// LadoTranscriptSource is one conversation file of a session that AO's usage
// pipeline reads: Claude's main transcript (UsageSourceClaudeMain) or a Codex
// rollout (UsageSourceCodexRollout).
type LadoTranscriptSource struct {
	Kind UsageSourceKind
	Path string
}
