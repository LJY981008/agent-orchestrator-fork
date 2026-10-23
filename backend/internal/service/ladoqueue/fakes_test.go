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
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
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
)

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
	logs     *lockedBuffer
	svc      *Service

	mu  sync.Mutex
	now time.Time
	ids int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		store: &fakeStore{tasks: map[domain.LadoTaskID]domain.LadoTask{}, settings: domain.DefaultLadoDispatchSettings()},
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
		logs:     &lockedBuffer{},
		now:      t0,
	}
	f.store.tasks["task-1"] = testTask()
	f.svc = New(Deps{
		Store: f.store, Tasks: f.tasks, Policy: f.policy, Sessions: f.sessions,
		Projects: f.projects, Branches: f.branches,
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
	// running counts executions whose session runs; live leases are counted
	// from the entries.
	running   int
	claims    []int
	links     []domain.LadoExecution
	linkErr   error
	taskReads int
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
	return f.settings, nil
}

func (f *fakeStore) CountLiveLadoExecutions(_ context.Context, now time.Time) (int, map[domain.ProjectID]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := f.running
	for _, e := range f.entries {
		if open(e) && e.LeaseToken != "" && e.LeaseExpiresAt.After(now) {
			total++
		}
	}
	return total, nil, nil
}

func open(e domain.LadoQueueEntry) bool {
	return e.DispatchedSessionID == "" && e.FailedAt.IsZero() && e.SupersededAt.IsZero() && e.CancelledAt.IsZero() &&
		e.ReviewRequiredReason == "" && e.BlockedReason == ""
}

func (f *fakeStore) ClaimLadoQueueEntries(_ context.Context, now, leaseUntil time.Time, limit int, newToken func() string) ([]domain.LadoQueueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = append(f.claims, limit)
	order := make([]int, len(f.entries))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return f.entries[a].EnqueuedAt.Compare(f.entries[b].EnqueuedAt) })
	var claimed []domain.LadoQueueEntry
	for _, i := range order {
		e := &f.entries[i]
		leased := e.LeaseToken != "" && e.LeaseExpiresAt.After(now)
		if len(claimed) == limit || !open(*e) || leased || e.NextAttemptAt.After(now) {
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
	if !f.leased(id, token, func(e *domain.LadoQueueEntry) { e.DispatchedSessionID = exec.SessionID }) {
		return domain.ErrLadoLeaseLost
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links = append(f.links, exec)
	return nil
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
	onCheck  func()
	input    ladotask.RunInput
	inputErr error
	// onInput runs inside RunInput.
	onInput func()
	checks  int
	inputs  int
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

func (f *fakeTasks) RunInput(context.Context, domain.LadoTask) (ladotask.RunInput, error) {
	f.mu.Lock()
	hook := f.onInput
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs++
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
// new session. onSpawn runs inside the spawn, as AO starts the worker.
type fakeSessions struct {
	mu      sync.Mutex
	configs []ports.SpawnConfig
	errs    []error
	onSpawn func(cfg ports.SpawnConfig)
	kills   []domain.SessionID
}

func (f *fakeSessions) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	f.mu.Lock()
	f.configs = append(f.configs, cfg)
	n := len(f.configs)
	var err error
	if n <= len(f.errs) {
		err = f.errs[n-1]
	}
	hook := f.onSpawn
	f.mu.Unlock()
	if hook != nil {
		hook(cfg)
	}
	if err != nil {
		return domain.Session{}, 0, 0, err
	}
	return domain.Session{SessionRecord: domain.SessionRecord{ID: domain.SessionID("sess-" + strconv.Itoa(n))}}, 0, 0, nil
}

func (f *fakeSessions) RequestKill(_ context.Context, id domain.SessionID) (sessionmanager.KillResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills = append(f.kills, id)
	return sessionmanager.KillResult{Freed: true}, nil
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

type fakeProjects struct {
	mu      sync.Mutex
	project domain.ProjectRecord
	repos   []domain.WorkspaceRepoRecord
	calls   int
}

func (f *fakeProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.project, f.project.ID == id, nil
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
