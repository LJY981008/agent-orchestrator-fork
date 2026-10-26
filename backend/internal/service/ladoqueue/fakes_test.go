package ladoqueue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/router"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// The services the daemon wires in implement the dispatcher's dependencies.
var (
	_ Store    = (*store.Store)(nil)
	_ Projects = (*store.Store)(nil)
	_ Tasks    = (*ladotask.Service)(nil)
	_ Policy   = (*ladopolicy.Service)(nil)
	_ Sessions = (*sessionsvc.Service)(nil)
	_ Branches = (*router.Workspace)(nil)
	_ Memory   = (*usage.MemoryReader)(nil)
	_ Git      = execGit{}
	// The dispatcher stops the worker of a task cancelled while it runs.
	_ ladotask.Killer = (*Service)(nil)
)

var t0 = time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)

const (
	testPlanSHA     = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	testSnapshotSHA = "5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e"
	testIssueRef    = domain.IssueID("github:acme/app#12")
	testIssueTitle  = "Fix the login timeout"
	testIssueBody   = "Sessions expire after 5 minutes instead of 30."
	testPlanText    = "---\ntask: task-1\nissue: github:acme/app#12\nbase_ref: main\n---\n\n# Plan\n\n1. Read the timeout from the settings.\n"
	testIssueFile   = "# Issue github:acme/app#12, as LADO pinned it\n\n## Body\n\n```text\nSessions expire after 5 minutes instead of 30.\n```\n"
	// testDispatchSHA is the commit the fixture's base names at dispatch;
	// the plan was made at c0ffee.
	testDispatchSHA = "d15ba7c4d15ba7c4d15ba7c4d15ba7c4d15ba7c4"
)

// testDispatchHash is the dispatch hash of the fixture's entries: the hash a
// spawn of their worker carries.
func testDispatchHash() string {
	return domain.LadoDispatchHash(testPlanSHA, testSnapshotSHA)
}

// pinnedIssueSHA is the hash of the issue as the plan was made from it.
func pinnedIssueSHA() string {
	snapshot, _ := domain.NewLadoIssueSnapshot(testIssueRef, testIssueTitle, testIssueBody, nil)
	return snapshot.SHA256()
}

func testRevision() domain.LadoPlanRevision {
	return domain.LadoPlanRevision{
		ID: "rev-2", TaskID: "task-1", RequestID: "req-2", Number: 2, SHA256: testPlanSHA,
		BaseRef: "main", BaseSHA: "c0ffee", IssueSnapshotSHA256: pinnedIssueSHA(), CreatedAt: t0.Add(-time.Hour),
	}
}

func testApproval() domain.LadoPlanApproval {
	return domain.LadoPlanApproval{ID: "approval-1", RevisionID: "rev-2", TaskID: "task-1", SHA256: testPlanSHA, ApprovedAt: t0.Add(-10 * time.Minute)}
}

func testTask() domain.LadoTask {
	return domain.LadoTask{
		ID: "task-1", ProjectID: "proj-1",
		Repo:     domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/app"},
		IssueRef: testIssueRef, Agent: "claude-code",
		Issue: domain.LadoObservedIssue{Title: testIssueTitle, State: domain.IssueOpen, URL: "https://github.com/acme/app/issues/12"},
		Plan: domain.LadoTaskPlan{
			Latest: testRevision(), Approval: testApproval(),
			Snapshot: domain.LadoPolicySnapshotRef{ID: "snap-1", SHA256: testSnapshotSHA},
		},
	}
}

func testEntry(id domain.LadoQueueEntryID, enqueued time.Time) domain.LadoQueueEntry {
	return domain.LadoQueueEntry{
		ID: id, TaskID: "task-1", ProjectID: "proj-1", PlanApprovalID: "approval-1",
		PolicySnapshotID: "snap-1", EnqueuedAt: enqueued,
	}
}

func testSnapshot(agent ladopolicy.SnapshotAgent, blocking bool) domain.LadoPolicySnapshot {
	content, err := json.Marshal(ladopolicy.SnapshotContent{
		Compiler: ladopolicy.Compiler, ProjectID: "proj-1", TaskID: "task-1", Agent: agent,
		PermissionCeiling: domain.PermissionModeAuto, Prompt: "## LADO rules\n\nRun the tests before you push.\n",
	})
	if err != nil {
		panic(err)
	}
	return domain.LadoPolicySnapshot{ID: "snap-1", ApprovalID: "approval-1", SHA256: testSnapshotSHA, Content: content, Blocking: blocking}
}

func claudeAgent() ladopolicy.SnapshotAgent {
	return ladopolicy.SnapshotAgent{Harness: domain.HarnessClaudeCode, Model: "claude-sonnet-5", PermissionMode: domain.PermissionModeAcceptEdits}
}

// fixture is a dispatcher over fakes, with one approved task whose checks all
// pass. Tests change what they need before they add entries.
type fixture struct {
	store    *fakeStore
	tasks    *fakeTasks
	policy   *fakePolicy
	sessions *fakeSessions
	projects *fakeProjects
	branches *fakeBranches
	memory   *fakeMemory
	git      *fakeGit
	logs     *lockedBuffer
	svc      *Service

	mu  sync.Mutex
	now time.Time
	ids int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		store: &fakeStore{
			tasks: map[domain.LadoTaskID]domain.LadoTask{}, settings: domain.DefaultLadoDispatchSettings(),
			running: map[domain.ProjectID]int{}, caps: map[domain.ProjectID]int{}, modeOff: map[domain.ProjectID]bool{},
			sessions: map[string]domain.SessionRecord{},
		},
		tasks: &fakeTasks{
			live: domain.LadoIssueSnapshot{ID: testIssueRef, Title: testIssueTitle, Body: testIssueBody},
			input: ladotask.RunInput{
				PlanText: testPlanText, IssueContext: testIssueFile,
				Revision: testRevision(), Approval: testApproval(), Snapshot: testSnapshot(claudeAgent(), false),
			},
		},
		policy:   &fakePolicy{sha: testSnapshotSHA},
		sessions: &fakeSessions{},
		projects: &fakeProjects{project: domain.ProjectRecord{
			ID: "proj-1", Path: "/work/app", Kind: domain.ProjectKindSingleRepo,
			Config: domain.ProjectConfig{DefaultBranch: domain.DefaultBranchAuto},
		}},
		branches: &fakeBranches{resolved: ports.WorkspaceDefaultBranch{Remote: "origin", Branch: "main", BaseRef: "refs/remotes/origin/main"}},
		memory:   &fakeMemory{pressure: procmem.Pressure{Source: procmem.PressureSourcePSI, Raw: 1.5}},
		git:      &fakeGit{sha: testDispatchSHA, ahead: 2},
		logs:     &lockedBuffer{},
		now:      t0,
	}
	f.store.tasks["task-1"] = testTask()
	// A session AO stopped no longer runs.
	f.sessions.ended = f.store.endSession
	f.svc = New(Deps{
		Store: f.store, Tasks: f.tasks, Policy: f.policy, Sessions: f.sessions,
		Projects: f.projects, Branches: f.branches, Memory: f.memory, Git: f.git,
		Clock: f.clock, NewID: f.newID,
		Log: slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	return f
}

func (f *fixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fixture) setClock(at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = at
}

func (f *fixture) newID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids++
	return "lease-" + strconv.Itoa(f.ids)
}

// tick runs one pass at at, with the clock there too.
func (f *fixture) tick(t *testing.T, at time.Time) {
	t.Helper()
	f.setClock(at)
	if err := f.svc.Tick(context.Background(), at); err != nil {
		t.Fatalf("Tick(%s) = %v", at.Format(time.TimeOnly), err)
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeStore keeps entries the way the store's primitives do: a claim takes
// open entries whose lease is empty or expired and whose wait is over, oldest
// first, and every other write holds only with the claim's token.
type fakeStore struct {
	mu       sync.Mutex
	tasks    map[domain.LadoTaskID]domain.LadoTask
	entries  []domain.LadoQueueEntry
	settings domain.LadoDispatchSettings
	// running counts the executions whose session runs, by project; live
	// leases are counted from the entries.
	running map[domain.ProjectID]int
	// caps are the projects' run caps, 1 where unset; modeOff the projects
	// with LADO mode off.
	caps    map[domain.ProjectID]int
	modeOff map[domain.ProjectID]bool
	// sessions are AO's sessions by client request id.
	sessions   map[string]domain.SessionRecord
	sessionErr error
	// overdue are the running executions with a deadline.
	overdue  []domain.LadoExecution
	timeOuts []domain.LadoAuditEvent
	// live are the executions whose session runs; audits the audit events
	// written on their own.
	live   []domain.LadoExecution
	audits []domain.LadoAuditEvent
	// settingsErr fails reading the dispatch settings.
	settingsErr error
	claims      []int
	// fullAsked are the projects each claim left out.
	fullAsked  [][]domain.ProjectID
	links      []domain.LadoExecution
	linkErr    error
	waitWrites int
	taskReads  int
	// settingsEvents are the audit events of the stored dispatch settings.
	settingsEvents []domain.LadoAuditEvent
}

func (f *fakeStore) add(entries ...domain.LadoQueueEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, entries...)
}

func (f *fakeStore) entry(id domain.LadoQueueEntryID) domain.LadoQueueEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.entries {
		if e.ID == id {
			return e
		}
	}
	return domain.LadoQueueEntry{}
}

func (f *fakeStore) setTask(task domain.LadoTask) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks[task.ID] = task
}

func (f *fakeStore) linked() []domain.LadoExecution {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.links)
}

func (f *fakeStore) claimLimits() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.claims)
}

func (f *fakeStore) LadoTask(_ context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taskReads++
	task, ok := f.tasks[id]
	return task, ok, nil
}

func (f *fakeStore) LadoDispatchSettings(context.Context) (domain.LadoDispatchSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings, f.settingsErr
}

func (f *fakeStore) LadoProjectMaxConcurrent(_ context.Context, project domain.ProjectID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.caps[project]; ok {
		return n, nil
	}
	return domain.LadoDefaultProjectMaxConcurrent, nil
}

func (f *fakeStore) CountLiveLadoExecutions(_ context.Context, now time.Time) (int, map[domain.ProjectID]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	total, perProject := 0, map[domain.ProjectID]int{}
	for project, n := range f.running {
		total += n
		perProject[project] += n
	}
	for _, e := range f.entries {
		if open(e) && e.LeaseToken != "" && e.LeaseExpiresAt.After(now) {
			total++
			perProject[e.ProjectID]++
		}
	}
	return total, perProject, nil
}

// SetLadoQueueWaiting writes what the store's statement does: the reason of
// the open entries without a lease whose project's mode suits it, where it
// differs.
func (f *fakeStore) SetLadoQueueWaiting(_ context.Context, reason string, project *domain.ProjectID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for i := range f.entries {
		e := &f.entries[i]
		if !open(*e) || e.LeaseToken != "" || e.WaitingReason == reason || (project != nil && e.ProjectID != *project) ||
			f.modeOff[e.ProjectID] != (reason == domain.LadoWaitingModeOff) {
			continue
		}
		e.WaitingReason = reason
		n++
	}
	f.waitWrites += int(n)
	return n, nil
}

func (f *fakeStore) writtenWaits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.waitWrites
}

func (f *fakeStore) LeasedLadoQueueEntries(context.Context) ([]domain.LadoQueueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var leased []domain.LadoQueueEntry
	for _, e := range f.entries {
		if e.LeaseToken != "" {
			leased = append(leased, e)
		}
	}
	slices.SortStableFunc(leased, func(a, b domain.LadoQueueEntry) int { return a.ClaimedAt.Compare(b.ClaimedAt) })
	return leased, nil
}

func (f *fakeStore) GetSessionByClientRequestID(_ context.Context, id string) (domain.SessionRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessionErr != nil {
		return domain.SessionRecord{}, false, f.sessionErr
	}
	rec, ok := f.sessions[id]
	return rec, ok, nil
}

func (f *fakeStore) addSession(rec domain.SessionRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[rec.ClientRequestID] = rec
}

func (f *fakeStore) LadoExecutionsPastDeadline(_ context.Context, now time.Time) ([]domain.LadoExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var due []domain.LadoExecution
	for _, exec := range f.overdue {
		if exec.TimedOutAt.IsZero() && !exec.DeadlineAt.IsZero() && !exec.DeadlineAt.After(now) {
			due = append(due, exec)
		}
	}
	return due, nil
}

func (f *fakeStore) MarkLadoExecutionTimedOut(_ context.Context, id domain.SessionID, event domain.LadoAuditEvent) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.overdue {
		if exec := &f.overdue[i]; exec.SessionID == id && exec.TimedOutAt.IsZero() {
			exec.TimedOutAt = event.At
			f.timeOuts = append(f.timeOuts, event)
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) timedOut() ([]domain.LadoExecution, []domain.LadoAuditEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.overdue), slices.Clone(f.timeOuts)
}

func open(e domain.LadoQueueEntry) bool {
	return e.DispatchedSessionID == "" && e.FailedAt.IsZero() && e.SupersededAt.IsZero() && e.CancelledAt.IsZero() &&
		e.ReviewRequiredReason == "" && e.BlockedReason == ""
}

func (f *fakeStore) ClaimLadoQueueEntries(_ context.Context, now, leaseUntil time.Time, limit int, fullProjects []domain.ProjectID, newToken func() string) ([]domain.LadoQueueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = append(f.claims, limit)
	f.fullAsked = append(f.fullAsked, slices.Clone(fullProjects))
	order := make([]int, len(f.entries))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return f.entries[a].EnqueuedAt.Compare(f.entries[b].EnqueuedAt) })
	var claimed []domain.LadoQueueEntry
	for _, i := range order {
		e := &f.entries[i]
		leased := e.LeaseToken != "" && e.LeaseExpiresAt.After(now)
		if len(claimed) == limit || !open(*e) || leased || e.NextAttemptAt.After(now) || f.modeOff[e.ProjectID] || slices.Contains(fullProjects, e.ProjectID) {
			continue
		}
		e.LeaseToken, e.ClaimedAt, e.LeaseExpiresAt = newToken(), now, leaseUntil
		e.AttemptCount++
		e.WaitingReason = ""
		claimed = append(claimed, *e)
	}
	return claimed, nil
}

// leased applies write to the entry token holds, clearing the lease.
func (f *fakeStore) leased(id domain.LadoQueueEntryID, token string, write func(*domain.LadoQueueEntry)) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.entries {
		e := &f.entries[i]
		if e.ID != id {
			continue
		}
		if token == "" || e.LeaseToken != token {
			return false
		}
		write(e)
		e.LeaseToken, e.ClaimedAt, e.LeaseExpiresAt = "", time.Time{}, time.Time{}
		return true
	}
	return false
}

func (f *fakeStore) ReleaseLadoQueueEntry(_ context.Context, id domain.LadoQueueEntryID, token string, next time.Time, waiting string) (bool, error) {
	return f.leased(id, token, func(e *domain.LadoQueueEntry) { e.NextAttemptAt, e.WaitingReason = next, waiting }), nil
}

func (f *fakeStore) HoldLadoQueueEntry(_ context.Context, id domain.LadoQueueEntryID, token, waiting string, next time.Time) (bool, error) {
	return f.leased(id, token, func(e *domain.LadoQueueEntry) {
		e.AttemptCount--
		e.WaitingReason, e.NextAttemptAt = waiting, next
	}), nil
}

func (f *fakeStore) MarkLadoQueueEntryReviewRequired(_ context.Context, id domain.LadoQueueEntryID, token, reason string) (bool, error) {
	return f.leased(id, token, func(e *domain.LadoQueueEntry) { e.ReviewRequiredReason = reason }), nil
}

func (f *fakeStore) MarkLadoQueueEntryBlocked(_ context.Context, id domain.LadoQueueEntryID, token, reason string) (bool, error) {
	return f.leased(id, token, func(e *domain.LadoQueueEntry) { e.BlockedReason = reason }), nil
}

func (f *fakeStore) FailLadoQueueEntry(_ context.Context, id domain.LadoQueueEntryID, token, message string, at time.Time) (bool, error) {
	return f.leased(id, token, func(e *domain.LadoQueueEntry) { e.FailedAt, e.ErrorMessage = at, message }), nil
}

func (f *fakeStore) CancelLadoQueueEntry(_ context.Context, id domain.LadoQueueEntryID, token string, at time.Time) (bool, error) {
	return f.leased(id, token, func(e *domain.LadoQueueEntry) { e.CancelledAt = at }), nil
}

func (f *fakeStore) LinkLadoExecution(_ context.Context, id domain.LadoQueueEntryID, token string, exec domain.LadoExecution) error {
	f.mu.Lock()
	err := f.linkErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	var project domain.ProjectID
	if !f.leased(id, token, func(e *domain.LadoQueueEntry) { e.DispatchedSessionID, project = exec.SessionID, e.ProjectID }) {
		return domain.ErrLadoLeaseLost
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links = append(f.links, exec)
	// The linked session runs and takes a slot from now on.
	f.running[project]++
	return nil
}

// LadoDispatchGrant answers like the store: every entry's worker carries the
// fixture's dispatch hash.
func (f *fakeStore) LadoDispatchGrant(_ context.Context, id domain.LadoQueueEntryID, attempt int, now time.Time) (domain.LadoDispatchGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.entries {
		if e.ID == id {
			active := e.State(now) == domain.LadoQueueDispatching && e.AttemptCount == attempt
			return domain.LadoDispatchGrant{ProjectID: e.ProjectID, Hash: testDispatchHash(), Active: active}, nil
		}
	}
	return domain.LadoDispatchGrant{}, nil
}

func (f *fakeStore) LiveLadoExecutions(context.Context) ([]domain.LadoExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.live), nil
}

// endSession is AO stopping a session: its execution no longer runs.
func (f *fakeStore) endSession(id domain.SessionID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live = slices.DeleteFunc(f.live, func(exec domain.LadoExecution) bool { return exec.SessionID == id })
}

func (f *fakeStore) AppendLadoAuditEvent(_ context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, ev)
	return ev, nil
}

func (f *fakeStore) audited() []domain.LadoAuditEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.audits)
}

// A dispatch reads none of the views below; views_test.go reads them from the
// real store, and stores the settings here to see their audit events.

func (f *fakeStore) ListLadoQueue(context.Context, *domain.ProjectID) ([]domain.LadoQueueItem, error) {
	return nil, nil
}

func (f *fakeStore) LadoExecution(context.Context, domain.SessionID) (domain.LadoExecution, bool, error) {
	return domain.LadoExecution{}, false, nil
}

func (f *fakeStore) LadoPlanRevision(context.Context, domain.LadoPlanRevisionID) (domain.LadoPlanRevision, bool, error) {
	return domain.LadoPlanRevision{}, false, nil
}

func (f *fakeStore) ListLadoPlanApprovals(context.Context, domain.LadoTaskID) ([]domain.LadoPlanApproval, error) {
	return nil, nil
}

func (f *fakeStore) LadoPolicySnapshot(context.Context, domain.LadoPlanApprovalID) (domain.LadoPolicySnapshot, bool, error) {
	return domain.LadoPolicySnapshot{}, false, nil
}

func (f *fakeStore) SetLadoDispatchSettings(_ context.Context, settings domain.LadoDispatchSettings, event domain.LadoAuditEvent) (domain.LadoDispatchSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	settings.UpdatedAt = event.At
	f.settings = settings
	f.settingsEvents = append(f.settingsEvents, event)
	return settings, nil
}

// fakeTrackerClient stands for the tracker client a real task service reads
// issues with: it holds a token, as the real one does.
type fakeTrackerClient struct {
	token string
}

// fakeTasks answers IssueCheck from the live issue, hashed as the task
// service hashes it, and RunInput from what LADO pinned.
type fakeTasks struct {
	mu       sync.Mutex
	client   *fakeTrackerClient
	live     domain.LadoIssueSnapshot
	comments []domain.LadoIssueComment
	state    domain.NormalizedIssueState
	checkErr error
	// onCheck runs inside IssueCheck, as the tracker answers.
	onCheck func()
	input   ladotask.RunInput
	// inputs are the inputs of other tasks than the fixture's.
	inputs   map[domain.LadoTaskID]ladotask.RunInput
	inputErr error
	// onInput runs inside RunInput.
	onInput    func()
	checks     int
	inputCalls int
}

func (f *fakeTasks) IssueCheck(_ context.Context, task domain.LadoTask) (ladotask.IssueCheck, error) {
	f.mu.Lock()
	hook := f.onCheck
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	if f.checkErr != nil {
		return ladotask.IssueCheck{}, fmt.Errorf("check the issue of task %s: %w", task.ID, f.checkErr)
	}
	snapshot, _ := domain.NewLadoIssueSnapshot(f.live.ID, f.live.Title, f.live.Body, f.comments)
	state := f.state
	if state == "" {
		state = domain.IssueOpen
	}
	return ladotask.IssueCheck{SHA256: snapshot.SHA256(), State: state}, nil
}

func (f *fakeTasks) RunInput(_ context.Context, task domain.LadoTask) (ladotask.RunInput, error) {
	f.mu.Lock()
	hook := f.onInput
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputCalls++
	if in, ok := f.inputs[task.ID]; ok {
		return in, f.inputErr
	}
	return f.input, f.inputErr
}

type fakePolicy struct {
	mu    sync.Mutex
	sha   string
	err   error
	calls int
}

func (f *fakePolicy) CurrentSHA256(context.Context, domain.LadoTask) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.sha, f.err
}

// fakeSessions records each spawn and answers with errs in turn, then with a
// new session, the one start makes when set. onSpawn runs inside the spawn,
// as AO starts the worker. A kill fails with killErrs' error for its
// session; one that succeeds tells ended.
type fakeSessions struct {
	mu       sync.Mutex
	configs  []ports.SpawnConfig
	errs     []error
	onSpawn  func(cfg ports.SpawnConfig)
	start    func(cfg ports.SpawnConfig) (domain.SessionID, error)
	kills    []domain.SessionID
	killErrs map[domain.SessionID]error
	ended    func(domain.SessionID)
}

func (f *fakeSessions) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	f.mu.Lock()
	f.configs = append(f.configs, cfg)
	n := len(f.configs)
	var err error
	if n <= len(f.errs) {
		err = f.errs[n-1]
	}
	hook, start := f.onSpawn, f.start
	f.mu.Unlock()
	if hook != nil {
		hook(cfg)
	}
	if err != nil {
		return domain.Session{}, 0, 0, err
	}
	id := domain.SessionID("sess-" + strconv.Itoa(n))
	if start != nil {
		if id, err = start(cfg); err != nil {
			return domain.Session{}, 0, 0, err
		}
	}
	return domain.Session{SessionRecord: domain.SessionRecord{ID: id}}, 0, 0, nil
}

func (f *fakeSessions) RequestKill(_ context.Context, id domain.SessionID) (sessionmanager.KillResult, error) {
	f.mu.Lock()
	f.kills = append(f.kills, id)
	if err := f.killErrs[id]; err != nil {
		f.mu.Unlock()
		return sessionmanager.KillResult{}, err
	}
	ended := f.ended
	f.mu.Unlock()
	if ended != nil {
		ended(id)
	}
	return sessionmanager.KillResult{Freed: true}, nil
}

func (f *fakeSessions) setKillErr(id domain.SessionID, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.killErrs == nil {
		f.killErrs = map[domain.SessionID]error{}
	}
	f.killErrs[id] = err
}

func (f *fakeSessions) spawned() []ports.SpawnConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.configs)
}

func (f *fakeSessions) killed() []domain.SessionID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.kills)
}

// fakeProjects holds the fixture's project and any others a test adds.
type fakeProjects struct {
	mu      sync.Mutex
	project domain.ProjectRecord
	others  map[string]domain.ProjectRecord
	repos   []domain.WorkspaceRepoRecord
	calls   int
}

func (f *fakeProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.project.ID == id {
		return f.project, true, nil
	}
	project, ok := f.others[id]
	return project, ok, nil
}

func (f *fakeProjects) ListWorkspaceRepos(_ context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if projectID != f.project.ID {
		return nil, errors.New("unknown project")
	}
	return f.repos, nil
}

// fakeBranches resolves every repository to one branch and records what it
// was asked.
type fakeBranches struct {
	mu       sync.Mutex
	resolved ports.WorkspaceDefaultBranch
	err      error
	asked    [][2]string
}

func (f *fakeBranches) ResolveDefaultBranch(_ context.Context, repoPath, configured string) (ports.WorkspaceDefaultBranch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, [2]string{repoPath, configured})
	return f.resolved, f.err
}

func (f *fakeBranches) calls() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

// fakeMemory answers every reading with pressure, or with err.
type fakeMemory struct {
	mu       sync.Mutex
	pressure procmem.Pressure
	err      error
	reads    int
}

func (f *fakeMemory) Pressure(context.Context) (procmem.Pressure, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.pressure, f.err
}

func (f *fakeMemory) set(pressure procmem.Pressure, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pressure, f.err = pressure, err
}

func (f *fakeMemory) readings() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

// fakeGit names one commit for every ref and counts ahead commits from any
// other, or fails with its errors.
type fakeGit struct {
	mu       sync.Mutex
	sha      string
	ahead    int
	revErr   error
	countErr error
}

func (f *fakeGit) RevParse(context.Context, string, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sha, f.revErr
}

func (f *fakeGit) CountCommits(_ context.Context, _, from, to string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.countErr != nil {
		return 0, f.countErr
	}
	if from == to {
		return 0, nil
	}
	return f.ahead, nil
}
