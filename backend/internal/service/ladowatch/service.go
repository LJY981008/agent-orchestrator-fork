// Package ladowatch watches LADO's running workers (issue #42, plan steps
// 12–15): it finds a worker that stopped at its account's usage limit and
// resumes it once the limit resets, finds a worker that went quiet without a
// report and nudges it once, and tells the dispatcher and the plan workers
// which accounts are at their limit, so no new worker starts on one.
//
// LADO sends a worker only its two fixed messages
// (domain.LadoWakeLimitResumeText and domain.LadoWakeStallNudgeText), only
// while the worker rests between turns, never in answer to a question it
// asked the owner, at most LadoWakeMaxResumes and LadoWakeMaxNudges times a
// session, and only in a project that lets it. Every message is audited and
// raises an alert.
//
// The daemon's one-minute watch loop runs Tick: the compaction counter first
// (plan decision 10), then the limits, then the stalls.
package ladowatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Store reads LADO's running executions and their tasks, and keeps what LADO
// watches of each session, the accounts at their limit, the alerts and the
// audit log.
type Store interface {
	LiveLadoExecutions(ctx context.Context) ([]domain.LadoExecution, error)
	LadoExecution(ctx context.Context, sessionID domain.SessionID) (domain.LadoExecution, bool, error)
	LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error)
	LatestSessionReport(ctx context.Context, sessionID domain.SessionID) (domain.ReportState, string, bool, error)
	LadoEffectiveWatchSettings(ctx context.Context, project domain.ProjectID) (domain.LadoWatchSettings, error)

	EnsureLadoSessionWatch(ctx context.Context, session domain.SessionID, task domain.LadoTaskID, at time.Time) (bool, error)
	LadoSessionWatch(ctx context.Context, session domain.SessionID) (domain.LadoSessionWatch, bool, error)
	SetLadoSessionWatchLimit(ctx context.Context, session domain.SessionID, l domain.LadoWatchLimit, at time.Time) (bool, error)
	SetLadoSessionWatchStall(ctx context.Context, session domain.SessionID, st domain.LadoWatchStall, at time.Time) (bool, error)

	RecordLadoAccountLimit(ctx context.Context, l domain.LadoAccountLimit) error
	ReleaseLadoAccountLimit(ctx context.Context, key string) (bool, error)
	LadoAccountLimit(ctx context.Context, key string) (domain.LadoAccountLimit, bool, error)
	ListLadoAccountLimits(ctx context.Context) ([]domain.LadoAccountLimit, error)
	// GetAgentAccountDefault is the account a provider's new sessions start
	// on; ok is false when they start on the computer's own login.
	GetAgentAccountDefault(ctx context.Context, p domain.AgentAccountProvider) (string, bool, error)

	AddLadoAlert(ctx context.Context, a domain.LadoAlert) (domain.LadoAlertID, bool, error)
	AppendLadoAuditEvent(ctx context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error)
}

// Sessions reads AO's sessions: their agent, activity and whether they ended.
type Sessions interface {
	Session(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Conversations reads a Chat session's conversation: AO's conversation
// store implements it.
type Conversations interface {
	ConversationForSession(ctx context.Context, session domain.SessionID) (domain.ConversationRecord, error)
	// LatestConversationTurn is the conversation's newest turn, queued ones
	// included; domain.ErrNoConversationTurn when it has none.
	LatestConversationTurn(ctx context.Context, conversationID string) (domain.ConversationTurn, error)
}

// Accounts tells which agent account a session runs on
// (*agentaccounts.Service).
type Accounts interface {
	SessionAccountID(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) (string, error)
}

// Messenger delivers a message from the daemon to a worker session.
type Messenger interface {
	Send(ctx context.Context, id domain.SessionID, message string) error
}

// CompactCounter counts the compactions of the sessions of LADO's open
// tasks (ladousage.Service.CountCompactions).
type CompactCounter interface {
	CountCompactions(ctx context.Context) error
}

// Deps wires a Service.
type Deps struct {
	Store         Store
	Sessions      Sessions
	Conversations Conversations
	// Accounts tells a session's account; nil takes every session to run on
	// the computer's own login.
	Accounts Accounts
	// Messenger sends the wake messages; nil sends none, and the watch only
	// records and alerts.
	Messenger Messenger
	// CompactCounter runs first on every tick; nil counts nothing.
	CompactCounter CompactCounter
	// Clock stamps changes; nil uses time.Now.
	Clock func() time.Time
	// Local is the time zone a Codex reset time is read in; nil uses
	// time.Local.
	Local *time.Location
	Log   *slog.Logger
}

// Service watches the running workers.
type Service struct {
	deps Deps
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.Local == nil {
		deps.Local = time.Local
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Service{deps: deps}
}

// Tick counts the compactions, releases the account limits whose reset
// passed, then watches each running worker: its usage limit first and its
// stall after (plan decision 6: a worker at its limit is not stalled). A
// session that cannot be read is logged and left for the next tick; the
// error is the context's or the reads every session needs.
func (s *Service) Tick(ctx context.Context) error {
	if s.deps.CompactCounter != nil {
		if err := s.deps.CompactCounter.CountCompactions(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.deps.Log.Warn("LADO could not count compactions", "err", err)
		}
	}
	now := s.deps.Clock().UTC()
	var problems []error
	if err := s.releaseExpired(ctx, now); err != nil {
		problems = append(problems, err)
	}
	execs, err := s.deps.Store.LiveLadoExecutions(ctx)
	if err != nil {
		return errors.Join(append(problems, fmt.Errorf("list the running LADO workers: %w", err))...)
	}
	pass := &tickPass{settings: map[domain.ProjectID]domain.LadoWatchSettings{}, tasks: map[domain.LadoTaskID]domain.LadoTask{}}
	for _, exec := range execs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.watch(ctx, pass, exec, now); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.deps.Log.Warn("LADO could not watch a worker", "task", exec.TaskID, "session", exec.SessionID, "err", err)
		}
	}
	return errors.Join(problems...)
}

// tickPass caches what one tick reads of each task and project.
type tickPass struct {
	settings map[domain.ProjectID]domain.LadoWatchSettings
	tasks    map[domain.LadoTaskID]domain.LadoTask
}

// releaseExpired removes the account limits whose reset passed.
func (s *Service) releaseExpired(ctx context.Context, now time.Time) error {
	limits, err := s.deps.Store.ListLadoAccountLimits(ctx)
	if err != nil {
		return fmt.Errorf("list the LADO account limits: %w", err)
	}
	for _, l := range limits {
		if l.Active(now) {
			continue
		}
		if _, err := s.deps.Store.ReleaseLadoAccountLimit(ctx, l.AccountKey); err != nil {
			return fmt.Errorf("release the LADO account limit %s: %w", l.AccountKey, err)
		}
		s.deps.Log.Info("LADO released an account's usage limit at its reset", "account", l.AccountKey, "reset", l.ResetAt)
	}
	return nil
}

// LaunchAccountLimit returns the usage limit of the account a new session of
// harness would start on, when one holds at now: the provider's default
// account, or the computer's own login without one. ok is false for a
// harness without accounts and for an account without a limit. The
// dispatcher and the plan workers hold new workers while it holds (plan step
// 15).
func (s *Service) LaunchAccountLimit(ctx context.Context, harness domain.AgentHarness, now time.Time) (domain.LadoAccountLimit, bool, error) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok {
		return domain.LadoAccountLimit{}, false, nil
	}
	id, found, err := s.deps.Store.GetAgentAccountDefault(ctx, p)
	if err != nil {
		return domain.LadoAccountLimit{}, false, fmt.Errorf("read the default %s account: %w", p, err)
	}
	if !found {
		id = ""
	}
	key, ok := domain.LadoAccountKey(harness, id)
	if !ok {
		return domain.LadoAccountLimit{}, false, nil
	}
	l, ok, err := s.deps.Store.LadoAccountLimit(ctx, key)
	if err != nil || !ok || !l.Active(now) {
		return domain.LadoAccountLimit{}, false, err
	}
	return l, true, nil
}

// worker is what one tick knows of a running worker.
type worker struct {
	exec     domain.LadoExecution
	task     domain.LadoTask
	settings domain.LadoWatchSettings
	session  domain.SessionRecord
	// turn is the session's newest turn; hasTurn is false when it has none
	// or its conversation could not be read.
	turn    domain.ConversationTurn
	hasTurn bool
	watch   domain.LadoSessionWatch
	// report is the session's last report state, "" without one.
	report domain.ReportState
}

// resting reports whether the worker finished its turn and waits: AO shows it
// idle or at its prompt, and its newest turn, queued ones included, is over.
// A conversation LADO could not read is not resting.
func (w worker) resting() bool {
	state := w.session.Activity.State
	return (state == domain.ActivityIdle || state == domain.ActivityWaitingInput) && w.hasTurn && w.turn.State.Terminal()
}

// answering reports whether the worker's last report waits for the owner or
// says it is done; LADO never speaks for the owner.
func (w worker) answering() bool {
	return w.report == domain.ReportNeedsInput || w.report == domain.ReportDone
}

func (s *Service) watch(ctx context.Context, pass *tickPass, exec domain.LadoExecution, now time.Time) error {
	rec, found, err := s.deps.Sessions.Session(ctx, exec.SessionID)
	if err != nil {
		return fmt.Errorf("read the session: %w", err)
	}
	if !found || rec.IsTerminated || rec.Activity.State == domain.ActivityExited {
		return nil
	}
	w := worker{exec: exec, session: rec}
	if w.task, err = s.task(ctx, pass, exec.TaskID); err != nil {
		return err
	}
	if w.settings, err = s.settings(ctx, pass, w.task.ProjectID); err != nil {
		return err
	}
	w.turn, w.hasTurn = s.latestTurn(ctx, exec.SessionID)
	state, _, reported, err := s.deps.Store.LatestSessionReport(ctx, exec.SessionID)
	if err != nil {
		return fmt.Errorf("read the last report: %w", err)
	}
	if reported {
		w.report = state
	}
	if _, err := s.deps.Store.EnsureLadoSessionWatch(ctx, exec.SessionID, exec.TaskID, now); err != nil {
		return err
	}
	watch, ok, err := s.deps.Store.LadoSessionWatch(ctx, exec.SessionID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("the session's watch row is gone")
	}
	w.watch = watch
	if err := s.watchLimit(ctx, &w, now); err != nil {
		return fmt.Errorf("watch the usage limit: %w", err)
	}
	if err := s.watchStall(ctx, &w, now); err != nil {
		return fmt.Errorf("watch the stall: %w", err)
	}
	return nil
}

func (s *Service) task(ctx context.Context, pass *tickPass, id domain.LadoTaskID) (domain.LadoTask, error) {
	if task, ok := pass.tasks[id]; ok {
		return task, nil
	}
	task, ok, err := s.deps.Store.LadoTask(ctx, id)
	if err != nil {
		return domain.LadoTask{}, fmt.Errorf("read task %s: %w", id, err)
	}
	if !ok {
		return domain.LadoTask{}, fmt.Errorf("task %s not found", id)
	}
	pass.tasks[id] = task
	return task, nil
}

func (s *Service) settings(ctx context.Context, pass *tickPass, project domain.ProjectID) (domain.LadoWatchSettings, error) {
	if settings, ok := pass.settings[project]; ok {
		return settings, nil
	}
	settings, err := s.deps.Store.LadoEffectiveWatchSettings(ctx, project)
	if err != nil {
		return domain.LadoWatchSettings{}, fmt.Errorf("read the watch settings of %s: %w", project, err)
	}
	pass.settings[project] = settings
	return settings, nil
}

// latestTurn reads the newest turn of the session's conversation. ok is false
// when it has none or it could not be read, which keeps LADO quiet.
func (s *Service) latestTurn(ctx context.Context, session domain.SessionID) (domain.ConversationTurn, bool) {
	if s.deps.Conversations == nil {
		return domain.ConversationTurn{}, false
	}
	conv, err := s.deps.Conversations.ConversationForSession(ctx, session)
	if err != nil {
		s.deps.Log.Debug("LADO could not read a worker's conversation", "session", session, "err", err)
		return domain.ConversationTurn{}, false
	}
	turn, err := s.deps.Conversations.LatestConversationTurn(ctx, conv.ID)
	if err != nil {
		if !errors.Is(err, domain.ErrNoConversationTurn) {
			s.deps.Log.Debug("LADO could not read a worker's newest turn", "session", session, "err", err)
		}
		return domain.ConversationTurn{}, false
	}
	return turn, true
}

// accountKey is the key of the account the worker's session runs on.
func (s *Service) accountKey(ctx context.Context, w worker) (string, bool) {
	accountID := ""
	if s.deps.Accounts != nil {
		id, err := s.deps.Accounts.SessionAccountID(ctx, w.session.ID, w.session.Harness)
		if err != nil {
			s.deps.Log.Warn("LADO could not tell a worker's account", "session", w.session.ID, "err", err)
			return "", false
		}
		accountID = id
	}
	return domain.LadoAccountKey(w.session.Harness, accountID)
}

// send delivers one of LADO's two wake messages, and audits it.
func (s *Service) send(ctx context.Context, w worker, kind, text string, now time.Time, payload map[string]any) error {
	if err := s.deps.Messenger.Send(ctx, w.session.ID, text); err != nil {
		return fmt.Errorf("send the %s message: %w", kind, err)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["task"], payload["kind"], payload["text"] = w.task.ID, kind, text
	s.audit(ctx, "lado.nudge_sent", w.session.ID, now, payload)
	s.deps.Log.Info("LADO woke a worker", "task", w.task.ID, "session", w.session.ID, "kind", kind)
	return nil
}

func (s *Service) audit(ctx context.Context, action string, session domain.SessionID, at time.Time, payload map[string]any) {
	data, _ := json.Marshal(payload)
	if _, err := s.deps.Store.AppendLadoAuditEvent(context.WithoutCancel(ctx), domain.LadoAuditEvent{
		At: at, Actor: "lado", Action: action, SubjectKind: "session", SubjectID: string(session), Payload: data,
	}); err != nil {
		s.deps.Log.Warn("LADO could not audit a watch event", "action", action, "session", session, "err", err)
	}
}

// alert raises an alert of the worker's task and session once.
func (s *Service) alert(ctx context.Context, w worker, kind domain.LadoAlertKind, level int, value, threshold int64, now time.Time) {
	if _, _, err := s.deps.Store.AddLadoAlert(context.WithoutCancel(ctx), domain.LadoAlert{
		TaskID: w.task.ID, SessionID: w.session.ID, Kind: kind, Level: max(level, 1), Value: value, Threshold: threshold, CreatedAt: now,
	}); err != nil {
		s.deps.Log.Warn("LADO could not raise an alert", "task", w.task.ID, "session", w.session.ID, "kind", kind, "err", err)
	}
}
