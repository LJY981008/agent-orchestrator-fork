// Package ladousage adds up what a LADO task's sessions spend (issue #42,
// plan steps 9–11). A task's sessions are #34's timing facts (the plan
// designers and critics, every execution) and every earlier plan worker
// attempt; their cost and tokens come from AO's usage summaries, and their
// compactions from the session watch rows CountCompactions keeps. Reads never
// write: only CountCompactions, which the watch loop calls once a minute,
// stores anything, and #34's lado_session_metrics is left alone.
package ladousage

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Store is what the usage view reads and the compaction counter keeps
// (*store.Store).
type Store interface {
	LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error)
	ListLadoTasks(ctx context.Context, projectID domain.ProjectID, includeCancelled bool) ([]domain.LadoTask, error)
	ListLadoTasksToObserve(ctx context.Context) ([]domain.LadoTask, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ConversationForSession(ctx context.Context, session domain.SessionID) (domain.ConversationRecord, error)
	LadoTaskTimingFacts(ctx context.Context, id domain.LadoTaskID) (domain.LadoTaskTimingFacts, bool, error)
	ListLadoPlanWorkerSessions(ctx context.Context, task domain.LadoTaskID) ([]domain.LadoPlanWorkerSession, error)
	LadoSessionSubagentUsage(ctx context.Context, session domain.SessionID) (domain.LadoSubagentUsage, error)
	ListLadoSessionWatch(ctx context.Context, task domain.LadoTaskID) ([]domain.LadoSessionWatch, error)
	LadoSessionWatch(ctx context.Context, session domain.SessionID) (domain.LadoSessionWatch, bool, error)
	EnsureLadoSessionWatch(ctx context.Context, session domain.SessionID, task domain.LadoTaskID, at time.Time) (bool, error)
	SetLadoSessionWatchCompactions(ctx context.Context, session domain.SessionID, c domain.LadoWatchCompactions, at time.Time) (bool, error)
	ListLadoSessionTranscriptSources(ctx context.Context, session domain.SessionID) ([]domain.LadoTranscriptSource, error)
}

// UsageReader is AO's per-session usage summary (*usage.SummaryReader). A
// session AO no longer has is an apierr NotFound.
type UsageReader interface {
	Get(ctx context.Context, sessionID domain.SessionID) (domain.SessionUsageSummary, error)
}

// Deps are the service's collaborators. Clock and Log default to the wall
// clock and slog.Default.
type Deps struct {
	Store Store
	Usage UsageReader
	Clock func() time.Time
	Log   *slog.Logger
}

// Service adds up tasks' usage and counts their sessions' compactions.
type Service struct{ deps Deps }

// New makes the service.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Service{deps: deps}
}

// EndReason says what ends a task's elapsed time.
type EndReason string

// End reasons, in the order they are checked.
const (
	// EndMerged: the task's pull request merged; the elapsed time is #34's
	// timeline total.
	EndMerged EndReason = "merged"
	// EndCancelled: the task was cancelled.
	EndCancelled EndReason = "cancelled"
	// EndClosed: the pull request of the newest execution that opens it was
	// closed without a merge.
	EndClosed EndReason = "closed"
	// EndInProgress: none of those; the time runs to now.
	EndInProgress EndReason = ""
)

// Totals is what a set of sessions used. Cost is AO's estimate at API
// prices, in nanodollars; a session without priced usage adds nothing.
type Totals struct {
	CostNanos         int64
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	// SubagentCostNanos and the subagent tokens are the part of the above
	// that the sessions' subagents used.
	SubagentCostNanos    int64
	SubagentInputTokens  int64
	SubagentOutputTokens int64
}

func (t *Totals) add(o Totals) {
	t.CostNanos += o.CostNanos
	t.InputTokens += o.InputTokens
	t.CachedInputTokens += o.CachedInputTokens
	t.OutputTokens += o.OutputTokens
	t.SubagentCostNanos += o.SubagentCostNanos
	t.SubagentInputTokens += o.SubagentInputTokens
	t.SubagentOutputTokens += o.SubagentOutputTokens
}

// SessionUsage is what one session of a task used.
type SessionUsage struct {
	SessionID domain.SessionID
	// Role is #34's session role, the same the task's timings show.
	Role domain.LadoSessionRole
	// PartID is the part of a part or merge worker.
	PartID string
	// Attempt is the session's attempt from 1, 0 when it cannot be read
	// (a deleted session).
	Attempt int
	Totals
	// ContextUsed and ContextWindow are how full the session's conversation
	// is now, 0 when the provider has not reported.
	ContextUsed   int64
	ContextWindow int64
	// Compactions is the stored count of the session's compactions.
	Compactions int
	// UsageMissing marks a session AO no longer has: its usage was deleted
	// with it, so it counts as nothing.
	UsageMissing bool
	// Incomplete marks usage AO knows it did not collect in full.
	Incomplete bool
}

// TaskUsage is what a task's sessions used together and one by one.
type TaskUsage struct {
	TaskID    domain.LadoTaskID
	ProjectID domain.ProjectID
	IssueRef  domain.IssueID
	Title     string
	Totals
	// Compactions adds up the sessions' compactions.
	Compactions int
	// StartedAt is the task's intake, EndedAt the end EndReason names (now
	// while in progress), Elapsed the time between.
	StartedAt time.Time
	EndedAt   time.Time
	Elapsed   time.Duration
	EndReason EndReason
	// UsageMissing counts the sessions AO no longer has; Incomplete is set
	// when any session's usage is incomplete.
	UsageMissing int
	Incomplete   bool
	// Sessions are the plan sessions, then the executions, as the task's
	// timings list them, the earlier plan worker attempts after the plan
	// sessions.
	Sessions []SessionUsage
}

// InProgress reports whether the elapsed time still runs.
func (u TaskUsage) InProgress() bool { return u.EndReason == EndInProgress }

// ProjectUsage is the totals of a project's tasks, for its board.
type ProjectUsage struct {
	ProjectID domain.ProjectID
	// Tasks are the project's tasks that are not cancelled, oldest first,
	// without their sessions.
	Tasks []TaskUsage
}

// taskSession is one session of a task with what the usage view shows of
// where it came from.
type taskSession struct {
	id      domain.SessionID
	role    domain.LadoSessionRole
	part    string
	attempt int
}

// TaskUsage adds up a task's sessions' usage.
func (s *Service) TaskUsage(ctx context.Context, id domain.LadoTaskID) (TaskUsage, error) {
	usage, err := s.taskUsage(ctx, id, true)
	if err != nil {
		return TaskUsage{}, err
	}
	return usage, nil
}

// ProjectUsage adds up the usage of each of a project's tasks that is not
// cancelled.
func (s *Service) ProjectUsage(ctx context.Context, projectID domain.ProjectID) (ProjectUsage, error) {
	if projectID == "" {
		return ProjectUsage{}, apierr.Invalid("LADO_USAGE_PROJECT_REQUIRED", "Name the project.", nil)
	}
	if _, ok, err := s.deps.Store.GetProject(ctx, string(projectID)); err != nil {
		s.deps.Log.Warn("LADO could not read a project for its usage", "project", projectID, "err", err)
		return ProjectUsage{}, apierr.Internal("LADO_USAGE_LOAD_FAILED", "Failed to load the project's usage")
	} else if !ok {
		return ProjectUsage{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	tasks, err := s.deps.Store.ListLadoTasks(ctx, projectID, false)
	if err != nil {
		s.deps.Log.Warn("LADO could not list a project's tasks for usage", "project", projectID, "err", err)
		return ProjectUsage{}, apierr.Internal("LADO_USAGE_LOAD_FAILED", "Failed to load the project's usage")
	}
	out := ProjectUsage{ProjectID: projectID, Tasks: make([]TaskUsage, 0, len(tasks))}
	for _, task := range tasks {
		usage, err := s.taskUsage(ctx, task.ID, false)
		var apiErr *apierr.Error
		if errors.As(err, &apiErr) && apiErr.Kind == apierr.KindNotFound {
			continue // deleted since it was listed
		}
		if err != nil {
			return ProjectUsage{}, err
		}
		usage.Sessions = nil
		out.Tasks = append(out.Tasks, usage)
	}
	return out, nil
}

// taskUsage reads a task's usage; detail adds each session's context size.
func (s *Service) taskUsage(ctx context.Context, id domain.LadoTaskID, detail bool) (TaskUsage, error) {
	failed := func(what string, err error) error {
		s.deps.Log.Warn("LADO could not read a task's usage", "task", id, "step", what, "err", err)
		return apierr.Internal("LADO_USAGE_LOAD_FAILED", "Failed to load the task's usage")
	}
	task, ok, err := s.deps.Store.LadoTask(ctx, id)
	if err != nil {
		return TaskUsage{}, failed("task", err)
	}
	if !ok || task.IssueRef == "" {
		return TaskUsage{}, apierr.NotFound("LADO_TASK_NOT_FOUND", "Unknown task")
	}
	facts, ok, err := s.deps.Store.LadoTaskTimingFacts(ctx, id)
	if err != nil {
		return TaskUsage{}, failed("timing facts", err)
	}
	if !ok {
		return TaskUsage{}, apierr.NotFound("LADO_TASK_NOT_FOUND", "Unknown task")
	}
	sessions, err := s.taskSessions(ctx, facts)
	if err != nil {
		return TaskUsage{}, failed("plan worker sessions", err)
	}
	watches, err := s.deps.Store.ListLadoSessionWatch(ctx, id)
	if err != nil {
		return TaskUsage{}, failed("session watch", err)
	}
	compactions := make(map[domain.SessionID]int, len(watches))
	for _, w := range watches {
		compactions[w.SessionID] = w.Compactions
	}

	out := TaskUsage{
		TaskID: facts.TaskID, ProjectID: facts.ProjectID, IssueRef: facts.IssueRef, Title: facts.Title,
		Sessions: make([]SessionUsage, 0, len(sessions)),
	}
	out.StartedAt, out.EndedAt, out.Elapsed, out.EndReason = elapsed(facts, task.CancelledAt, s.deps.Clock().UTC())
	for _, session := range sessions {
		su, err := s.sessionUsage(ctx, session, detail)
		if err != nil {
			return TaskUsage{}, failed("session "+string(session.id), err)
		}
		su.Compactions = compactions[session.id]
		out.add(su.Totals)
		out.Compactions += su.Compactions
		if su.UsageMissing {
			out.UsageMissing++
		}
		out.Incomplete = out.Incomplete || su.Incomplete
		out.Sessions = append(out.Sessions, su)
	}
	return out, nil
}

// taskSessions lists a task's sessions as its timings do: the plan sessions,
// then each earlier plan worker attempt the timings miss, then the
// executions. A session is listed once, with its first role.
func (s *Service) taskSessions(ctx context.Context, facts domain.LadoTaskTimingFacts) ([]taskSession, error) {
	attempts, err := s.deps.Store.ListLadoPlanWorkerSessions(ctx, facts.TaskID)
	if err != nil {
		return nil, err
	}
	planAttempt := make(map[domain.SessionID]int, len(attempts))
	for _, a := range attempts {
		planAttempt[a.SessionID] = a.Attempt
	}
	out := make([]taskSession, 0, len(facts.PlanSessions)+len(attempts)+len(facts.Executions))
	seen := map[domain.SessionID]bool{}
	add := func(ts taskSession) {
		if ts.id == "" || seen[ts.id] {
			return
		}
		seen[ts.id] = true
		out = append(out, ts)
	}
	for _, p := range facts.PlanSessions {
		add(taskSession{id: p.SessionID, role: p.Role, attempt: planAttempt[p.SessionID]})
	}
	sort.SliceStable(attempts, func(i, j int) bool { return attempts[i].CreatedAt.Before(attempts[j].CreatedAt) })
	for _, a := range attempts {
		add(taskSession{id: a.SessionID, role: a.SessionRole(), attempt: a.Attempt})
	}
	for _, e := range facts.Executions {
		add(taskSession{id: e.SessionID, role: domain.LadoSessionRoleOfExecution(e.Role), part: e.PartID})
	}
	// An execution's attempt is in its session's client request id.
	for i := range out {
		if out[i].attempt != 0 || isPlanRole(out[i].role) {
			continue
		}
		rec, ok, err := s.deps.Store.GetSession(ctx, out[i].id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if req, ok := domain.ParseLadoRunClientRequestID(rec.ClientRequestID); ok {
			out[i].attempt = req.Attempt
		}
	}
	return out, nil
}

func isPlanRole(r domain.LadoSessionRole) bool {
	return r == domain.LadoSessionDesigner || r == domain.LadoSessionCritic
}

// sessionUsage reads one session's usage. A session AO no longer has is
// UsageMissing and used nothing.
func (s *Service) sessionUsage(ctx context.Context, session taskSession, detail bool) (SessionUsage, error) {
	out := SessionUsage{SessionID: session.id, Role: session.role, PartID: session.part, Attempt: session.attempt}
	summary, err := s.deps.Usage.Get(ctx, session.id)
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) && apiErr.Kind == apierr.KindNotFound {
		out.UsageMissing = true
		return out, nil
	}
	if err != nil {
		return SessionUsage{}, err
	}
	out.Incomplete = summary.Incomplete
	totals := summary.Totals
	if totals.EstimatedCost != nil {
		out.CostNanos = totals.EstimatedCost.TotalNanos
	}
	out.InputTokens = deref(totals.InputTokens)
	out.CachedInputTokens = deref(totals.CachedInputTokens)
	out.OutputTokens = deref(totals.OutputTokens)
	sub, err := s.deps.Store.LadoSessionSubagentUsage(ctx, session.id)
	if err != nil {
		return SessionUsage{}, err
	}
	out.SubagentCostNanos = sub.CostNanos
	out.SubagentInputTokens = sub.InputTokens
	out.SubagentOutputTokens = sub.OutputTokens
	if detail {
		// A session without a conversation, or one the provider has not
		// reported for, has no context size; that is not a failure.
		if conv, err := s.deps.Store.ConversationForSession(ctx, session.id); err == nil && conv.Usage != nil {
			out.ContextUsed, out.ContextWindow = conv.Usage.ContextUsed, conv.Usage.ContextWindow
		}
	}
	return out, nil
}

func deref(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// elapsed is a task's elapsed time (plan step 9): from its intake to, in this
// order, the merge (#34's timeline total, so the two views agree), its
// cancellation, the close without a merge of the pull request of its newest
// execution that opens one, or now.
func elapsed(facts domain.LadoTaskTimingFacts, cancelledAt, now time.Time) (start, end time.Time, d time.Duration, reason EndReason) {
	start = facts.CreatedAt.UTC()
	timeline := domain.LadoTaskTimelineOf(facts)
	for _, p := range timeline.Points {
		if p.Stage == domain.LadoTimingMerged {
			return start, start.Add(timeline.Total), timeline.Total, EndMerged
		}
	}
	end, reason = now, EndInProgress
	if !cancelledAt.IsZero() {
		end, reason = cancelledAt.UTC(), EndCancelled
	} else if run := newestPRRun(facts); run != nil && run.SettledState == string(domain.PRStateClosed) && !run.SettledAt.IsZero() {
		end, reason = run.SettledAt.UTC(), EndClosed
	}
	if start.IsZero() {
		return start, end, 0, reason
	}
	return start, end, max(end.Sub(start), 0), reason
}

// newestPRRun is the newest execution that opens the task's pull request, as
// #34's timeline picks it.
func newestPRRun(facts domain.LadoTaskTimingFacts) *domain.LadoTimingExecution {
	var run *domain.LadoTimingExecution
	for i := range facts.Executions {
		e := &facts.Executions[i]
		if e.Role != domain.LadoRoleSingle && e.Role != domain.LadoRoleIntegration {
			continue
		}
		if run == nil || e.StartedAt.After(run.StartedAt) {
			run = e
		}
	}
	return run
}
