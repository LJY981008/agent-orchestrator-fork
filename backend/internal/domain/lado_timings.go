package domain

// LADO fork: how long a task's stages take and what its sessions spend on
// investigation (issue #34, step 0). The stage times are read from the
// records LADO already keeps; only when the verification loop first saw an
// execution's PR and when its head first verified are new, and each session's
// tool calls come from its conversation's activities. Everything here is a
// pure function of those facts.

import (
	"path"
	"slices"
	"sort"
	"strings"
	"time"
)

// LadoTimingStage is one stage of a task's way from intake to merge.
type LadoTimingStage string

// Stages, in the order a task passes them.
const (
	LadoTimingReceived            LadoTimingStage = "received"
	LadoTimingPlanRequested       LadoTimingStage = "plan_requested"
	LadoTimingRevision            LadoTimingStage = "revision"
	LadoTimingApproved            LadoTimingStage = "approved"
	LadoTimingRunStarted          LadoTimingStage = "run_started"
	LadoTimingPROpened            LadoTimingStage = "pr_opened"
	LadoTimingVerified            LadoTimingStage = "verified"
	LadoTimingIntegrationApproved LadoTimingStage = "integration_approved"
	LadoTimingMerged              LadoTimingStage = "merged"
)

// LadoTimingStages lists the stages in order.
var LadoTimingStages = []LadoTimingStage{
	LadoTimingReceived, LadoTimingPlanRequested, LadoTimingRevision, LadoTimingApproved, LadoTimingRunStarted,
	LadoTimingPROpened, LadoTimingVerified, LadoTimingIntegrationApproved, LadoTimingMerged,
}

// LadoTimingPoint is when a task reached a stage.
type LadoTimingPoint struct {
	Stage LadoTimingStage
	At    time.Time
	// SincePrev is the time since the point before, 0 for the first point
	// and for a point that, estimated, falls before the one before.
	SincePrev time.Duration
	// Estimated marks a time the migration inferred for an execution that ran
	// before LADO recorded it (migrations_lado/0011_lado_timings.sql).
	Estimated bool
}

// LadoTaskTimeline is the stages a task reached, in stage order; a stage it
// did not reach has no point.
type LadoTaskTimeline struct {
	Points []LadoTimingPoint
	// Total is from intake to merge, 0 before the merge.
	Total time.Duration
	// How often the task went round: plan requests, revisions, approvals and
	// the executions that open its pull request.
	PlanRequests, Revisions, Approvals, Runs int
}

// LadoTimingApproval is one plan approval of a task.
type LadoTimingApproval struct {
	ID LadoPlanApprovalID
	At time.Time
}

// LadoTimingQueueEntry is one queue entry of a task: the approval it runs and
// when its parts run started, zero without parts.
type LadoTimingQueueEntry struct {
	ID             LadoQueueEntryID
	ApprovalID     LadoPlanApprovalID
	PartsStartedAt time.Time
}

// LadoTimingExecution is one execution of a task with the times the
// verification loop recorded for it.
type LadoTimingExecution struct {
	SessionID    SessionID
	Role         string // LadoRole*
	PartID       string
	ApprovalID   LadoPlanApprovalID
	QueueEntryID LadoQueueEntryID
	StartedAt    time.Time
	// PRSeenAt is when the loop first saw the execution's PR, VerifiedAt when
	// its head first passed or asked for confirmation; zero when not yet.
	PRSeenAt          time.Time
	PRSeenEstimated   bool
	VerifiedAt        time.Time
	VerifiedEstimated bool
	// SettledAt is when the loop settled the PR, SettledState the PR's state
	// then ("merged" or "closed").
	SettledAt    time.Time
	SettledState string
}

// LadoTimingIntegrationApproval is one integration approval of an execution.
type LadoTimingIntegrationApproval struct {
	SessionID SessionID
	At        time.Time
}

// LadoTimingSession is a plan worker session of a task: a request's designer
// or one of its critics.
type LadoTimingSession struct {
	SessionID SessionID
	Role      LadoSessionRole
}

// LadoTaskTimingFacts are the stored facts a task's timeline is read from.
type LadoTaskTimingFacts struct {
	TaskID    LadoTaskID
	ProjectID ProjectID
	IssueRef  IssueID
	Title     string
	CreatedAt time.Time
	// PlanRequestedAt and RevisionCreatedAt hold the times of every plan
	// request and every revision.
	PlanRequestedAt      []time.Time
	RevisionCreatedAt    []time.Time
	Approvals            []LadoTimingApproval
	QueueEntries         []LadoTimingQueueEntry
	Executions           []LadoTimingExecution
	IntegrationApprovals []LadoTimingIntegrationApproval
	PlanSessions         []LadoTimingSession
}

// LadoTaskTimelineOf reads a task's timeline from its facts. Intake, the
// first plan request and the first revision come first; the later stages
// follow the newest execution that opens the task's pull request (role "" or
// "integration"), or, before one starts, the newest approval: its approval,
// when its run started (its parts run's start when it has parts), when the
// loop first saw its PR and first verified it, its first integration approval
// and its merge.
func LadoTaskTimelineOf(f LadoTaskTimingFacts) LadoTaskTimeline {
	var t LadoTaskTimeline
	t.PlanRequests, t.Revisions, t.Approvals = len(f.PlanRequestedAt), len(f.RevisionCreatedAt), len(f.Approvals)
	at := map[LadoTimingStage]LadoTimingPoint{}
	set := func(stage LadoTimingStage, when time.Time, estimated bool) {
		if !when.IsZero() {
			at[stage] = LadoTimingPoint{Stage: stage, At: when, Estimated: estimated}
		}
	}
	set(LadoTimingReceived, f.CreatedAt, false)
	set(LadoTimingPlanRequested, earliestLadoTime(f.PlanRequestedAt), false)
	set(LadoTimingRevision, earliestLadoTime(f.RevisionCreatedAt), false)

	var run *LadoTimingExecution
	for i := range f.Executions {
		e := &f.Executions[i]
		if e.Role != LadoRoleSingle && e.Role != LadoRoleIntegration {
			continue
		}
		t.Runs++
		if run == nil || e.StartedAt.After(run.StartedAt) {
			run = e
		}
	}
	approval := LadoTimingApproval{}
	if run != nil {
		for _, a := range f.Approvals {
			if a.ID == run.ApprovalID {
				approval = a
			}
		}
	} else {
		for _, a := range f.Approvals {
			if approval.ID == "" || a.At.After(approval.At) {
				approval = a
			}
		}
	}
	set(LadoTimingApproved, approval.At, false)
	set(LadoTimingRunStarted, ladoRunStart(f, run, approval.ID), false)
	if run != nil {
		set(LadoTimingPROpened, run.PRSeenAt, run.PRSeenEstimated)
		set(LadoTimingVerified, run.VerifiedAt, run.VerifiedEstimated)
		var integration time.Time
		for _, a := range f.IntegrationApprovals {
			if a.SessionID == run.SessionID && (integration.IsZero() || a.At.Before(integration)) {
				integration = a.At
			}
		}
		set(LadoTimingIntegrationApproved, integration, false)
		if run.SettledState == string(PRStateMerged) {
			set(LadoTimingMerged, run.SettledAt, false)
		}
	}

	for _, stage := range LadoTimingStages {
		p, ok := at[stage]
		if !ok {
			continue
		}
		if n := len(t.Points); n > 0 {
			p.SincePrev = max(p.At.Sub(t.Points[n-1].At), 0)
		}
		t.Points = append(t.Points, p)
	}
	if merged, ok := at[LadoTimingMerged]; ok && !f.CreatedAt.IsZero() {
		t.Total = max(merged.At.Sub(f.CreatedAt), 0)
	}
	return t
}

// ladoRunStart is when the run of approval started: its parts run's start
// when its queue entry has one, else run's start, else, before an execution
// that opens the PR, the earliest execution of the approval.
func ladoRunStart(f LadoTaskTimingFacts, run *LadoTimingExecution, approval LadoPlanApprovalID) time.Time {
	for _, q := range f.QueueEntries {
		if q.PartsStartedAt.IsZero() {
			continue
		}
		if (run != nil && q.ID == run.QueueEntryID) || (run == nil && q.ApprovalID == approval && approval != "") {
			return q.PartsStartedAt
		}
	}
	if run != nil {
		return run.StartedAt
	}
	var start time.Time
	for _, e := range f.Executions {
		if approval != "" && e.ApprovalID == approval && (start.IsZero() || e.StartedAt.Before(start)) {
			start = e.StartedAt
		}
	}
	return start
}

func earliestLadoTime(times []time.Time) time.Time {
	var first time.Time
	for _, t := range times {
		if !t.IsZero() && (first.IsZero() || t.Before(first)) {
			first = t
		}
	}
	return first
}

// LadoSessionRole is what a session did for its task.
type LadoSessionRole string

// Session roles: a plan request's designer and critics, and the execution
// roles (LadoRole*), the one worker of a task without parts being "worker".
const (
	LadoSessionDesigner    LadoSessionRole = "designer"
	LadoSessionCritic      LadoSessionRole = "critic"
	LadoSessionWorker      LadoSessionRole = "worker"
	LadoSessionPart        LadoSessionRole = "part"
	LadoSessionMerge       LadoSessionRole = "merge"
	LadoSessionIntegration LadoSessionRole = "integration"
)

// LadoSessionRoles lists the session roles in the order summaries show them.
var LadoSessionRoles = []LadoSessionRole{
	LadoSessionDesigner, LadoSessionCritic, LadoSessionWorker, LadoSessionPart, LadoSessionMerge, LadoSessionIntegration,
}

// Valid reports whether r is a known session role.
func (r LadoSessionRole) Valid() bool { return slices.Contains(LadoSessionRoles, r) }

// LadoSessionRoleOfExecution is the session role of an execution role
// (LadoRole*).
func LadoSessionRoleOfExecution(role string) LadoSessionRole {
	if role == LadoRoleSingle {
		return LadoSessionWorker
	}
	return LadoSessionRole(role)
}

// LadoActivityFact is what the investigation metrics read of one activity of
// a session's conversation: its kind, the ACP tool kind and the provider's
// tool name, and the shell command a command ran.
type LadoActivityFact struct {
	Kind                        ActivityKind
	ToolKind, ToolName, Command string
	CreatedAt, UpdatedAt        time.Time
}

// LadoSessionMetrics is what one session spent on investigation.
type LadoSessionMetrics struct {
	SessionID SessionID
	TaskID    LadoTaskID
	Role      LadoSessionRole
	// ToolCalls counts commands, file changes and MCP tool calls; ReadCalls,
	// SearchCalls, CommandCalls and EditCalls split them, and an MCP tool
	// call that neither reads nor searches is in ToolCalls only.
	ToolCalls, ReadCalls, SearchCalls, CommandCalls, EditCalls int
	FirstActivityAt, FirstEditAt, LastActivityAt               time.Time
	// Investigate is from the first tool call to the first edit, or to the
	// last activity without one.
	Investigate time.Duration
	// Stored says the metrics were read from a stored row; false when they
	// were computed at read time.
	Stored    bool
	UpdatedAt time.Time
}

// InvestigationCalls are the calls that read or search.
func (m LadoSessionMetrics) InvestigationCalls() int { return m.ReadCalls + m.SearchCalls }

// The ways a tool call is counted.
type ladoCallClass int

const (
	ladoCallOther ladoCallClass = iota
	ladoCallRead
	ladoCallSearch
	ladoCallCommand
	ladoCallEdit
)

// SummarizeLadoActivities counts a session's tool calls and times its
// investigation. The caller fills SessionID, TaskID and Role.
func SummarizeLadoActivities(facts []LadoActivityFact) LadoSessionMetrics {
	var m LadoSessionMetrics
	var firstTool time.Time
	earlier := func(a, b time.Time) bool { return a.IsZero() || b.Before(a) }
	for _, f := range facts {
		if f.CreatedAt.IsZero() {
			continue
		}
		if earlier(m.FirstActivityAt, f.CreatedAt) {
			m.FirstActivityAt = f.CreatedAt
		}
		if last := latestLadoTime(f.CreatedAt, f.UpdatedAt); last.After(m.LastActivityAt) {
			m.LastActivityAt = last
		}
		class, ok := classifyLadoActivity(f)
		if !ok {
			continue
		}
		m.ToolCalls++
		if earlier(firstTool, f.CreatedAt) {
			firstTool = f.CreatedAt
		}
		switch class {
		case ladoCallRead:
			m.ReadCalls++
		case ladoCallSearch:
			m.SearchCalls++
		case ladoCallCommand:
			m.CommandCalls++
		case ladoCallEdit:
			m.EditCalls++
			if earlier(m.FirstEditAt, f.CreatedAt) {
				m.FirstEditAt = f.CreatedAt
			}
		}
	}
	if !firstTool.IsZero() {
		end := m.FirstEditAt
		if end.IsZero() {
			end = m.LastActivityAt
		}
		m.Investigate = max(end.Sub(firstTool), 0)
	}
	return m
}

func latestLadoTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// classifyLadoActivity reports whether f is a tool call and how it counts.
func classifyLadoActivity(f LadoActivityFact) (ladoCallClass, bool) {
	switch f.Kind {
	case ActivityKindFileChange:
		return ladoCallEdit, true
	case ActivityKindCommand, ActivityKindMCPTool:
	default:
		return ladoCallOther, false
	}
	switch {
	case f.ToolKind == "read" || f.ToolName == "Read" || f.ToolName == "NotebookRead":
		return ladoCallRead, true
	case f.ToolKind == "search" || f.ToolKind == "fetch" ||
		slices.Contains([]string{"Grep", "Glob", "WebSearch", "WebFetch"}, f.ToolName):
		return ladoCallSearch, true
	}
	if f.Kind == ActivityKindMCPTool {
		return ladoCallOther, true
	}
	return classifyLadoCommand(f.Command), true
}

var (
	ladoReadCommands   = []string{"cat", "head", "tail", "sed", "nl", "less", "bat", "wc"}
	ladoSearchCommands = []string{"rg", "grep", "ag", "find", "fd", "ls", "tree"}
	ladoSearchGit      = []string{"log", "show", "diff", "grep", "ls-files", "ls-tree", "blame", "status"}
)

// classifyLadoCommand classifies a shell command by its first real command:
// the first piece, split at &&, ||, ;, and |, that is not a cd. It reads the
// first word only, a heuristic.
func classifyLadoCommand(command string) ladoCallClass {
	words := ladoFirstCommand(command)
	if len(words) == 0 {
		return ladoCallCommand
	}
	name := path.Base(words[0])
	switch {
	case slices.Contains(ladoReadCommands, name):
		return ladoCallRead
	case slices.Contains(ladoSearchCommands, name):
		return ladoCallSearch
	case name == "git" && slices.Contains(ladoSearchGit, ladoGitSubcommand(words[1:])):
		return ladoCallSearch
	}
	return ladoCallCommand
}

// ladoFirstCommand is the words of command's first piece that is not a cd,
// without leading NAME=value assignments.
func ladoFirstCommand(command string) []string {
	pieces := strings.FieldsFunc(command, func(r rune) bool { return r == '&' || r == ';' || r == '|' || r == '\n' })
	for _, piece := range pieces {
		words := strings.Fields(piece)
		for len(words) > 0 && strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "-") {
			words = words[1:]
		}
		if len(words) == 0 || words[0] == "cd" {
			continue
		}
		return words
	}
	return nil
}

// ladoGitSubcommand is the subcommand of git's arguments, past its global
// options.
func ladoGitSubcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "-C" || arg == "-c" || arg == "--git-dir" || arg == "--work-tree":
			i++
		case strings.HasPrefix(arg, "-"):
		default:
			return arg
		}
	}
	return ""
}

// LadoTaskTimings is one task's timeline and its sessions' metrics.
type LadoTaskTimings struct {
	TaskID   LadoTaskID
	IssueRef IssueID
	Title    string
	Timeline LadoTaskTimeline
	Sessions []LadoSessionMetrics
}

// LadoTimingGap is the time between two adjacent points over a project's
// tasks: how many tasks went from From straight to To, and the median and
// longest time they took.
type LadoTimingGap struct {
	From, To    LadoTimingStage
	Count       int
	Median, Max time.Duration
}

// LadoInvestigationSummary is the median investigation of one session role
// over a project's tasks.
type LadoInvestigationSummary struct {
	Role                     LadoSessionRole
	Sessions                 int
	MedianCalls              int
	MedianInvestigationCalls int
	MedianInvestigate        time.Duration
}

// LadoTimingsSummary is a project's baseline: its recent tasks and the
// medians over them.
type LadoTimingsSummary struct {
	ProjectID     ProjectID
	Tasks         []LadoTaskTimings
	Gaps          []LadoTimingGap
	Investigation []LadoInvestigationSummary
}

// SummarizeLadoTimings sums tasks up: each gap between adjacent points, in
// stage order, and each session role's investigation, computed metrics
// included.
func SummarizeLadoTimings(projectID ProjectID, tasks []LadoTaskTimings) LadoTimingsSummary {
	summary := LadoTimingsSummary{ProjectID: projectID, Tasks: tasks}
	type pair struct{ from, to LadoTimingStage }
	gaps := map[pair][]time.Duration{}
	roles := map[LadoSessionRole][]LadoSessionMetrics{}
	for _, task := range tasks {
		points := task.Timeline.Points
		for i := 1; i < len(points); i++ {
			key := pair{points[i-1].Stage, points[i].Stage}
			gaps[key] = append(gaps[key], points[i].SincePrev)
		}
		for _, s := range task.Sessions {
			roles[s.Role] = append(roles[s.Role], s)
		}
	}
	order := func(stage LadoTimingStage) int { return slices.Index(LadoTimingStages, stage) }
	for key, durations := range gaps {
		summary.Gaps = append(summary.Gaps, LadoTimingGap{
			From: key.from, To: key.to, Count: len(durations),
			Median: medianLado(durations), Max: slices.Max(durations),
		})
	}
	sort.Slice(summary.Gaps, func(i, j int) bool {
		a, b := summary.Gaps[i], summary.Gaps[j]
		if order(a.From) != order(b.From) {
			return order(a.From) < order(b.From)
		}
		return order(a.To) < order(b.To)
	})
	for _, role := range LadoSessionRoles {
		sessions := roles[role]
		if len(sessions) == 0 {
			continue
		}
		calls := make([]int, len(sessions))
		investigation := make([]int, len(sessions))
		durations := make([]time.Duration, len(sessions))
		for i, s := range sessions {
			calls[i], investigation[i], durations[i] = s.ToolCalls, s.InvestigationCalls(), s.Investigate
		}
		summary.Investigation = append(summary.Investigation, LadoInvestigationSummary{
			Role: role, Sessions: len(sessions),
			MedianCalls: medianLado(calls), MedianInvestigationCalls: medianLado(investigation),
			MedianInvestigate: medianLado(durations),
		})
	}
	return summary
}

// medianLado is the middle value of values, the mean of the two middle ones
// for an even count (rounded down), 0 for none.
func medianLado[T int | time.Duration](values []T) T {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
