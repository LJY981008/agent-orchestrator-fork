// Package ladotask owns LADO's issue-backed tasks: creating an issue from the
// developer's request, importing open issues, tracking the issue of every
// task, and the plans and approvals that come before any work starts
// (docs/LJY-PRD/TDD.md §5.1 and §5.2, and
// docs/LJY-PRD/adr/0006-issues-as-work-items.md).
package ladotask

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Limits on what a task request may hold. GitHub caps issue titles at 256
// characters and bodies at 65,536; GitLab allows more.
const (
	maxTitleRunes = 256
	maxBodyRunes  = 65000
	maxImport     = 100
	// importableLimit caps the open issues offered for import.
	importableLimit = 200
	// markerSearchLimit is how many of a repository's newest issues LADO
	// searches for a task marker.
	markerSearchLimit = 100
)

// Store keeps LADO's tasks and reads LADO's project settings.
type Store interface {
	CreateLadoTask(ctx context.Context, task domain.LadoTask) (domain.LadoTask, error)
	LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error)
	ListLadoTasks(ctx context.Context, projectID domain.ProjectID, includeCancelled bool) ([]domain.LadoTask, error)
	ListPendingLadoTasks(ctx context.Context) ([]domain.LadoTask, error)
	ListLadoTasksToObserve(ctx context.Context) ([]domain.LadoTask, error)
	LiveLadoTaskIssueRefs(ctx context.Context) (map[domain.IssueID]bool, error)
	LinkLadoTaskIssue(ctx context.Context, id domain.LadoTaskID, ref domain.IssueID, issue domain.LadoObservedIssue, at time.Time) (domain.LadoTask, error)
	ObserveLadoTaskIssue(ctx context.Context, id domain.LadoTaskID, issue domain.LadoObservedIssue, at time.Time) error
	SetLadoTaskPreferences(ctx context.Context, id domain.LadoTaskID, agent, model string, at time.Time) (domain.LadoTask, bool, error)
	CancelLadoTask(ctx context.Context, id domain.LadoTaskID, at time.Time) (domain.LadoTask, bool, error)
	DeletePendingLadoTask(ctx context.Context, id domain.LadoTaskID) error
	LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error)

	CreateLadoPlanRequest(ctx context.Context, req domain.LadoPlanRequest) (domain.LadoPlanRequest, error)
	LadoPlanRequest(ctx context.Context, id domain.LadoPlanRequestID) (domain.LadoPlanRequest, bool, error)
	ListLadoPlanRequests(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoPlanRequest, error)
	ListLadoPlanRequestsForLiveTasks(ctx context.Context) ([]domain.LadoPlanRequest, error)
	MarkLadoPlanRequestDelivered(ctx context.Context, id domain.LadoPlanRequestID, at time.Time) error
	RecordLadoPlanRequestAttempt(ctx context.Context, id domain.LadoPlanRequestID, problem string) error
	FailLadoPlanRequest(ctx context.Context, id domain.LadoPlanRequestID, at time.Time, problem string) error
	LadoArtifactVersionPinned(ctx context.Context, sessionID domain.SessionID, path string, version int, sha256 string) (bool, error)
	PinLadoPlanRevision(ctx context.Context, rev domain.LadoPlanRevision) (domain.LadoPlanRevision, bool, error)
	LadoPlanRevision(ctx context.Context, id domain.LadoPlanRevisionID) (domain.LadoPlanRevision, bool, error)
	ListLadoPlanRevisions(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoPlanRevision, error)
	ListLadoPlanApprovals(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoPlanApproval, error)
	ApproveLadoPlan(ctx context.Context, approval domain.LadoPlanApproval, proof domain.LadoPresenceProof, event domain.LadoAuditEvent, snapshot *domain.LadoPolicySnapshot, entry *domain.LadoQueueEntry) (domain.LadoPlanApproval, domain.LadoAuditEvent, error)
	LadoPolicySnapshot(ctx context.Context, approvalID domain.LadoPlanApprovalID) (domain.LadoPolicySnapshot, bool, error)

	PlanWorkerStore
}

// PlanWorkerStore keeps plan worker requests, their reviews and what making
// each revision took, and reads the Runs settings and the sessions' usage.
type PlanWorkerStore interface {
	LadoDispatchSettings(ctx context.Context) (domain.LadoDispatchSettings, error)
	ListOpenLadoPlanWorkerRequests(ctx context.Context) ([]domain.LadoPlanRequest, error)
	SetLadoPlanRequestWaiting(ctx context.Context, id domain.LadoPlanRequestID, reason string) error
	BeginLadoPlanWorkerAttempt(ctx context.Context, id domain.LadoPlanRequestID) (int, bool, error)
	RecordLadoPlanWorkerProblem(ctx context.Context, id domain.LadoPlanRequestID, problem string) error
	StartLadoPlanWorker(ctx context.Context, id domain.LadoPlanRequestID, session domain.SessionID, at time.Time, event *domain.LadoAuditEvent) (bool, error)
	RecordLadoPlanDraft(ctx context.Context, id domain.LadoPlanRequestID, sum string, at time.Time, event *domain.LadoAuditEvent) (bool, error)
	RecordLadoPlanFeedback(ctx context.Context, id domain.LadoPlanRequestID, at time.Time, event *domain.LadoAuditEvent) (bool, error)
	RecordLadoPlanFinal(ctx context.Context, id domain.LadoPlanRequestID, sum string, at time.Time, event *domain.LadoAuditEvent) (bool, error)
	RecordLadoPlanFixRequested(ctx context.Context, id domain.LadoPlanRequestID, at time.Time) (bool, error)
	RecordLadoPlanReminder(ctx context.Context, id domain.LadoPlanRequestID, at time.Time) error
	CleanLadoPlanRequest(ctx context.Context, id domain.LadoPlanRequestID, at time.Time, event *domain.LadoAuditEvent) (bool, error)
	FailLadoPlanWorkerRequest(ctx context.Context, id domain.LadoPlanRequestID, at time.Time, code, detail string, event *domain.LadoAuditEvent) (bool, error)
	CreateLadoPlanCritique(ctx context.Context, c domain.LadoPlanCritique, event *domain.LadoAuditEvent) error
	LadoPlanCritique(ctx context.Context, id domain.LadoPlanRequestID, attempt int) (domain.LadoPlanCritique, bool, error)
	ListLadoPlanCritiques(ctx context.Context, id domain.LadoPlanRequestID) ([]domain.LadoPlanCritique, error)
	ListLadoPlanCritiquesByTask(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoPlanCritique, error)
	SetLadoPlanCritiqueSession(ctx context.Context, id domain.LadoPlanRequestID, attempt int, session domain.SessionID, event *domain.LadoAuditEvent) (bool, error)
	FinishLadoPlanCritique(ctx context.Context, id domain.LadoPlanRequestID, attempt int, state domain.LadoPlanCritiqueState, findings []domain.LadoPlanFinding, reason string, at time.Time, event *domain.LadoAuditEvent) (bool, error)
	SetLadoPlanRevisionMetrics(ctx context.Context, m domain.LadoPlanRevisionMetrics) error
	LadoPlanRevisionMetrics(ctx context.Context, id domain.LadoPlanRevisionID) (domain.LadoPlanRevisionMetrics, bool, error)
	ListLadoPlanRevisionMetrics(ctx context.Context, taskID domain.LadoTaskID) (map[domain.LadoPlanRevisionID]domain.LadoPlanRevisionMetrics, error)
	LadoSessionUsage(ctx context.Context, id domain.SessionID) (domain.LadoPlanSessionUsage, error)
}

// Projects reads AO's project registry.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
}

// Policy resolves LADO's rules for a task (docs/LJY-PRD/HARNESS.md §8).
type Policy interface {
	// Snapshot fixes the rules for approving the task's plan revision.
	Snapshot(ctx context.Context, task domain.LadoTask, rev domain.LadoPlanRevision) (domain.LadoPolicySnapshot, error)
	// CurrentSHA256 is the snapshot hash the rules give the task's approved
	// revision now.
	CurrentSHA256(ctx context.Context, task domain.LadoTask) (string, error)
	// CurrentSHA256s is CurrentSHA256 for many tasks, reading the rules
	// once. A task it cannot resolve is left out.
	CurrentSHA256s(ctx context.Context, tasks []domain.LadoTask) (map[domain.LadoTaskID]string, error)
	// Summary is what a plan worker learns of the rules the task's worker
	// follows now.
	Summary(ctx context.Context, task domain.LadoTask) (domain.LadoRuleSummary, error)
}

// Killer stops the session of a running execution. LADO's dispatcher
// implements it through AO's session service.
type Killer interface {
	Stop(ctx context.Context, sessionID domain.SessionID) error
}

// ProjectRepo is one repository of a project whose tracker can hold the
// project's issues: a single-repo project's origin, or each child of a
// workspace project that has one.
type ProjectRepo struct {
	// Name is the workspace child's folder name, or the project's name.
	Name string
	Repo domain.TrackerRepo
}

// Deps wires a Service.
type Deps struct {
	Store    Store
	Projects Projects
	Clients  ports.LadoRepoClients
	// Clock stamps changes; nil uses time.Now.
	Clock func() time.Time
	// NewID returns a new unique id; nil uses uuid.NewString.
	NewID func() string
	Log   *slog.Logger
	// RefreshInterval is how often the issue of every task is read again;
	// zero uses five minutes.
	RefreshInterval time.Duration

	// Blobs pins plans and issue snapshots.
	Blobs Blobs
	// Orchestrators finds a project's orchestrator and delivers plan
	// requests to it.
	Orchestrators Orchestrators
	// Artifacts reads the versions AO records of the plan files.
	Artifacts Artifacts
	// Git resolves a plan's base ref; nil uses the system git.
	Git Git
	// PlanInterval is how often new plan versions are pinned and waiting
	// requests delivered; zero uses ten seconds.
	PlanInterval time.Duration
	// DeliveryTimeout is how long a request is retried; zero uses ten
	// minutes.
	DeliveryTimeout time.Duration
	// Policy fixes the rules in each approval and tells whether they changed
	// since. Without it no plan can be approved.
	Policy Policy
	// Killer stops the worker of a task that is cancelled while it runs.
	// Without it the worker runs on.
	Killer Killer

	// PlanSessions starts, messages and stops plan worker sessions: AO's
	// session service. Without it, plan requests wait for it.
	PlanSessions PlanSessions
	// Queue resolves a plan's base and reads memory pressure, as the
	// dispatcher does. The daemon installs it with SetPlanQueue.
	Queue PlanQueue
	// DataDir holds LADO's own files of each plan request
	// (<DataDir>/lado/plan-requests/<request>/), which no artifact capture
	// reads.
	DataDir string
	// Notices tells each project's orchestrator what happened to its tasks,
	// on the plan loop's timer (service/ladonotify); nil tells nothing.
	Notices interface {
		Tick(ctx context.Context) error
	}
}

// Service creates, imports and tracks tasks.
type Service struct {
	deps Deps
	// kick asks the plan loop for a pass now: a plan request or a submission
	// arrived.
	kick chan struct{}
	// criticReminders are when LADO reminded each running critic.
	criticMu        sync.Mutex
	criticReminders map[domain.LadoPlanCritiqueID]time.Time
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.RefreshInterval <= 0 {
		deps.RefreshInterval = defaultRefreshInterval
	}
	if deps.Git == nil {
		deps.Git = execGit{}
	}
	if deps.PlanInterval <= 0 {
		deps.PlanInterval = defaultPlanInterval
	}
	if deps.DeliveryTimeout <= 0 {
		deps.DeliveryTimeout = defaultDeliveryTimeout
	}
	return &Service{deps: deps, kick: make(chan struct{}, 1)}
}

// SetPlanQueue installs the Queue of Deps. The dispatcher that implements it
// is built from this service, so the daemon installs it afterwards, before
// the plan loop starts.
func (s *Service) SetPlanQueue(queue PlanQueue) {
	s.deps.Queue = queue
}

// kickPlans asks the plan loop for a pass as soon as the one under way ends.
func (s *Service) kickPlans() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// SetKiller installs the Killer of Deps. The dispatcher that implements it
// is built from this service, so the daemon installs it afterwards, before
// the API serves.
func (s *Service) SetKiller(killer Killer) {
	s.deps.Killer = killer
}

// CreateInput is a New Task composer request in a LADO project.
type CreateInput struct {
	ProjectID domain.ProjectID
	// Repository is the canonical id of the repository to create the issue
	// in. It may be empty when the project has one repository.
	Repository string
	// Text is the composer text: the first line becomes the issue title and
	// the rest its body.
	Text  string
	Agent string
	Model string
}

// Create creates the issue for a new task, then the task. The task row exists
// first without an issue ref, and the issue body carries the task's marker, so
// an interrupted request leaves at most one issue and the task finds it again
// (docs/LJY-PRD/TDD.md §5.1). When the tracker refuses the issue, nothing is
// kept.
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.LadoTask, error) {
	title, body := domain.SplitLadoTaskText(in.Text)
	switch {
	case title == "":
		return domain.LadoTask{}, apierr.Invalid("LADO_TASK_TITLE_REQUIRED", "Describe the task on the first line; it becomes the issue title.", nil)
	case utf8.RuneCountInString(title) > maxTitleRunes:
		return domain.LadoTask{}, apierr.Invalid("LADO_TASK_TITLE_TOO_LONG", fmt.Sprintf("Keep the first line within %d characters; it becomes the issue title.", maxTitleRunes), map[string]any{"max": maxTitleRunes})
	case utf8.RuneCountInString(body) > maxBodyRunes:
		return domain.LadoTask{}, apierr.Invalid("LADO_TASK_BODY_TOO_LONG", fmt.Sprintf("Keep the description within %d characters.", maxBodyRunes), map[string]any{"max": maxBodyRunes})
	}
	repos, err := s.ladoRepos(ctx, in.ProjectID)
	if err != nil {
		return domain.LadoTask{}, err
	}
	target, err := pickRepo(repos, strings.TrimSpace(in.Repository))
	if err != nil {
		return domain.LadoTask{}, err
	}
	repo := target.Repo
	client, err := s.client(ctx, repo)
	if err != nil {
		return domain.LadoTask{}, err
	}
	now := s.deps.Clock().UTC()
	pending, err := s.deps.Store.CreateLadoTask(ctx, domain.LadoTask{
		ID:        s.newTaskID(),
		ProjectID: in.ProjectID,
		Repo:      repo,
		Agent:     strings.TrimSpace(in.Agent),
		Model:     strings.TrimSpace(in.Model),
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		return domain.LadoTask{}, apierr.Internal("LADO_TASK_CREATE_FAILED", "Failed to save the task")
	}
	issue, err := client.CreateIssue(ctx, repo, title, issueBody(body, pending.ID))
	if err == nil {
		return s.link(ctx, pending, repo, issue)
	}
	if errors.Is(err, ports.ErrLadoIssueNotCreated) {
		s.dropPending(ctx, pending.ID)
		reason := trackerMessage(err)
		return domain.LadoTask{}, apierr.Conflict("LADO_ISSUE_NOT_CREATED", "The tracker did not create the issue: "+reason, map[string]any{"reason": reason})
	}
	// The tracker may have created the issue before the answer was lost.
	found, ok, searchErr := findByMarker(ctx, client, repo, pending.ID)
	switch {
	case ok:
		return s.link(ctx, pending, repo, found)
	case searchErr == nil:
		s.dropPending(ctx, pending.ID)
		return domain.LadoTask{}, apierr.New(apierr.KindUnavailable, "LADO_ISSUE_CREATE_FAILED", fmt.Sprintf("Could not create the issue: %v", err), map[string]any{"reason": err.Error()})
	default:
		s.deps.Log.Warn("LADO could not confirm an issue creation; the task waits for the next check", "task", pending.ID, "err", err, "search_err", searchErr)
		return domain.LadoTask{}, apierr.Unavailable("LADO_ISSUE_CREATE_UNCONFIRMED",
			"LADO could not confirm whether the issue was created and will check again. If the task does not appear within a few minutes, try again.")
	}
}

// ImportInput lists issues to import as tasks.
type ImportInput struct {
	ProjectID domain.ProjectID
	IssueRefs []domain.IssueID
}

// ImportResult is the tasks an import created and the issues it left out
// because they already have a task.
type ImportResult struct {
	Tasks   []domain.LadoTask
	Skipped []domain.IssueID
}

// Import creates a task for each issue. The issues may come from any of the
// project's repositories. It reads the issues and writes nothing to the
// tracker.
func (s *Service) Import(ctx context.Context, in ImportInput) (ImportResult, error) {
	if len(in.IssueRefs) == 0 {
		return ImportResult{}, apierr.Invalid("LADO_IMPORT_EMPTY", "Choose at least one issue to import.", nil)
	}
	if len(in.IssueRefs) > maxImport {
		return ImportResult{}, apierr.Invalid("LADO_IMPORT_TOO_LARGE", fmt.Sprintf("Import at most %d issues at a time.", maxImport), map[string]any{"max": maxImport})
	}
	repos, err := s.ladoRepos(ctx, in.ProjectID)
	if err != nil {
		return ImportResult{}, err
	}
	ids := make([]domain.TrackerID, 0, len(in.IssueRefs))
	for _, ref := range in.IssueRefs {
		id, err := domain.ParseLadoIssueRef(ref)
		if err != nil {
			return ImportResult{}, apierr.Invalid("LADO_ISSUE_REF_INVALID", err.Error(), nil)
		}
		if _, ok := findRepo(repos, domain.LadoIssueRepo(id)); !ok {
			return ImportResult{}, apierr.Invalid("LADO_ISSUE_NOT_IN_REPOSITORY", fmt.Sprintf("%s is not an issue of this project's repositories.", ref), map[string]any{"issue": string(ref)})
		}
		ids = append(ids, id)
	}
	clients := map[string]ports.LadoRepoClient{}
	result := ImportResult{Tasks: []domain.LadoTask{}}
	for _, id := range ids {
		project, _ := findRepo(repos, domain.LadoIssueRepo(id))
		key := domain.LadoRepoRef(project.Repo)
		client, ok := clients[key]
		if !ok {
			if client, err = s.client(ctx, project.Repo); err != nil {
				return result, err
			}
			clients[key] = client
		}
		issue, err := client.Get(ctx, id)
		if err != nil {
			return result, apierr.New(apierr.KindUnavailable, "LADO_ISSUE_READ_FAILED", fmt.Sprintf("Could not read %s: %v", domain.LadoIssueRef(id), err),
				map[string]any{"issue": string(domain.LadoIssueRef(id)), "reason": err.Error()})
		}
		issue.ID.Host = id.Host
		now := s.deps.Clock().UTC()
		task, err := s.deps.Store.CreateLadoTask(ctx, domain.LadoTask{
			ID:        s.newTaskID(),
			ProjectID: in.ProjectID,
			Repo:      project.Repo,
			IssueRef:  domain.LadoIssueRef(issue.ID),
			Issue:     observedIssue(issue, now),
			CreatedAt: now,
			UpdatedAt: now,
		})
		if errors.Is(err, domain.ErrLadoIssueHasTask) {
			result.Skipped = append(result.Skipped, domain.LadoIssueRef(issue.ID))
			continue
		}
		if err != nil {
			return result, apierr.Internal("LADO_TASK_CREATE_FAILED", "Failed to save an imported task")
		}
		result.Tasks = append(result.Tasks, task)
	}
	return result, nil
}

// ImportableIssue is an open issue without a task, with its repository.
type ImportableIssue struct {
	Repo  ProjectRepo
	Issue domain.Issue
}

// RepoFailure is a repository whose issues could not be listed.
type RepoFailure struct {
	Repo    ProjectRepo
	Problem string
}

// ImportableIssues lists the open issues without a task in each of the
// project's repositories. A repository that cannot be read is reported and
// does not hide the others.
func (s *Service) ImportableIssues(ctx context.Context, projectID domain.ProjectID) ([]ImportableIssue, []RepoFailure, error) {
	repos, err := s.ladoRepos(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	live, err := s.deps.Store.LiveLadoTaskIssueRefs(ctx)
	if err != nil {
		return nil, nil, apierr.Internal("LADO_TASKS_LOAD_FAILED", "Failed to load tasks")
	}
	importable := []ImportableIssue{}
	var failures []RepoFailure
	for _, project := range repos {
		client, err := s.deps.Clients.ForRepo(ctx, project.Repo, "")
		if err != nil {
			failures = append(failures, RepoFailure{Repo: project, Problem: err.Error()})
			continue
		}
		issues, err := client.List(ctx, project.Repo, domain.ListFilter{State: domain.ListOpen, Limit: importableLimit})
		if err != nil {
			failures = append(failures, RepoFailure{Repo: project, Problem: err.Error()})
			continue
		}
		for _, issue := range issues {
			issue.ID.Host = project.Repo.Host
			if !live[domain.LadoIssueRef(issue.ID)] {
				importable = append(importable, ImportableIssue{Repo: project, Issue: issue})
			}
		}
	}
	return importable, failures, nil
}

// Repositories lists the repositories a project's issues can live in.
func (s *Service) Repositories(ctx context.Context, projectID domain.ProjectID) ([]ProjectRepo, error) {
	project, err := s.project(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return s.projectRepos(ctx, project)
}

// List returns a project's tasks, oldest first.
func (s *Service) List(ctx context.Context, projectID domain.ProjectID, includeCancelled bool) ([]domain.LadoTask, error) {
	if _, err := s.project(ctx, projectID); err != nil {
		return nil, err
	}
	tasks, err := s.deps.Store.ListLadoTasks(ctx, projectID, includeCancelled)
	if err != nil {
		return nil, apierr.Internal("LADO_TASKS_LOAD_FAILED", "Failed to load tasks")
	}
	return s.withPolicies(ctx, tasks), nil
}

// Get returns a task after reading its issue again, as when the task view
// opens. When the tracker cannot be reached, it returns the last observation.
func (s *Service) Get(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, error) {
	task, err := s.task(ctx, id)
	if err != nil {
		return domain.LadoTask{}, err
	}
	if task.CancelledAt.IsZero() {
		if err := s.observe(ctx, task); err != nil {
			s.deps.Log.Debug("LADO could not refresh a task's issue", "task", id, "err", err)
			return s.withPolicy(ctx, task), nil
		}
		if task, err = s.task(ctx, id); err != nil {
			return domain.LadoTask{}, err
		}
	}
	return s.withPolicy(ctx, task), nil
}

// withPolicy fills what the rules give the task's approved revision now, so
// its state shows whether they changed since the approval. A task without a
// valid approval needs nothing; a failure leaves the hash empty, which reads
// as no change.
func (s *Service) withPolicy(ctx context.Context, task domain.LadoTask) domain.LadoTask {
	if s.deps.Policy == nil || !task.Plan.ApprovalValid() {
		return task
	}
	sum, err := s.deps.Policy.CurrentSHA256(ctx, task)
	if err != nil {
		s.deps.Log.Debug("LADO could not resolve a task's rules", "task", task.ID, "err", err)
		return task
	}
	task.Plan.PolicySHA256 = sum
	return task
}

// withPolicies is withPolicy for a task list, which resolves the rules once
// for all of its approved tasks.
func (s *Service) withPolicies(ctx context.Context, tasks []domain.LadoTask) []domain.LadoTask {
	if s.deps.Policy == nil {
		return tasks
	}
	approved := make([]domain.LadoTask, 0, len(tasks))
	for _, task := range tasks {
		if task.Plan.ApprovalValid() {
			approved = append(approved, task)
		}
	}
	if len(approved) == 0 {
		return tasks
	}
	sums, err := s.deps.Policy.CurrentSHA256s(ctx, approved)
	if err != nil {
		s.deps.Log.Debug("LADO could not resolve the rules of a task list", "err", err)
		return tasks
	}
	// A task left out keeps an empty hash.
	for i := range tasks {
		tasks[i].Plan.PolicySHA256 = sums[tasks[i].ID]
	}
	return tasks
}

// UpdatePreferences changes a task's agent or model. A nil field keeps its
// value.
func (s *Service) UpdatePreferences(ctx context.Context, id domain.LadoTaskID, agent, model *string) (domain.LadoTask, error) {
	if agent == nil && model == nil {
		return domain.LadoTask{}, apierr.Invalid("LADO_TASK_UPDATE_EMPTY", "Send an agent or a model.", nil)
	}
	task, err := s.task(ctx, id)
	if err != nil {
		return domain.LadoTask{}, err
	}
	if agent != nil {
		task.Agent = strings.TrimSpace(*agent)
	}
	if model != nil {
		task.Model = strings.TrimSpace(*model)
	}
	updated, ok, err := s.deps.Store.SetLadoTaskPreferences(ctx, id, task.Agent, task.Model, s.deps.Clock().UTC())
	if err != nil {
		return domain.LadoTask{}, apierr.Internal("LADO_TASK_UPDATE_FAILED", "Failed to update the task")
	}
	if !ok {
		return domain.LadoTask{}, apierr.NotFound("LADO_TASK_NOT_FOUND", "Unknown task")
	}
	// The approval's rules name the agent and model, so a change may need
	// review.
	return s.withPolicy(ctx, updated), nil
}

// Cancel cancels a task and its open queue entries, then stops the session
// of its execution while that still lives. It never closes or edits the
// issue. The cancellation stands when the session cannot be stopped; a worker
// that a dispatch in flight starts after it is the dispatcher's to stop.
func (s *Service) Cancel(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, error) {
	cancelled, ok, err := s.deps.Store.CancelLadoTask(ctx, id, s.deps.Clock().UTC())
	if err != nil {
		return domain.LadoTask{}, apierr.Internal("LADO_TASK_CANCEL_FAILED", "Failed to cancel the task")
	}
	if !ok {
		return domain.LadoTask{}, apierr.NotFound("LADO_TASK_NOT_FOUND", "Unknown task")
	}
	if execution := cancelled.Execution; execution.SessionID != "" && !execution.SessionTerminated && s.deps.Killer != nil {
		// The cancellation is stored, so the stop goes on without the request.
		if err := s.deps.Killer.Stop(context.WithoutCancel(ctx), execution.SessionID); err != nil {
			s.deps.Log.Warn("LADO could not stop the worker of a cancelled task", "task", id, "session", execution.SessionID, "err", err)
		}
	}
	return s.withPolicy(ctx, cancelled), nil
}

// link records a created issue on its pending task.
func (s *Service) link(ctx context.Context, pending domain.LadoTask, repo domain.TrackerRepo, issue domain.Issue) (domain.LadoTask, error) {
	issue.ID.Host = repo.Host
	now := s.deps.Clock().UTC()
	linked, err := s.deps.Store.LinkLadoTaskIssue(ctx, pending.ID, domain.LadoIssueRef(issue.ID), observedIssue(issue, now), now)
	if err != nil {
		s.deps.Log.Error("LADO could not link a created issue to its task", "task", pending.ID, "issue", domain.LadoIssueRef(issue.ID), "err", err)
		return domain.LadoTask{}, apierr.Internal("LADO_TASK_LINK_FAILED", "The issue was created, but LADO failed to save it on the task")
	}
	return linked, nil
}

func (s *Service) dropPending(ctx context.Context, id domain.LadoTaskID) {
	if err := s.deps.Store.DeletePendingLadoTask(context.WithoutCancel(ctx), id); err != nil {
		s.deps.Log.Warn("LADO could not drop a task whose issue was not created", "task", id, "err", err)
	}
}

func (s *Service) newTaskID() domain.LadoTaskID {
	return domain.LadoTaskID("lado-task-" + s.deps.NewID())
}

func (s *Service) client(ctx context.Context, repo domain.TrackerRepo) (ports.LadoRepoClient, error) {
	client, err := s.deps.Clients.ForRepo(ctx, repo, "")
	if err != nil {
		return nil, apierr.Conflict("LADO_TRACKER_UNAVAILABLE", fmt.Sprintf("LADO cannot reach the repository's tracker: %v. Connect the repository with an access token.", err),
			map[string]any{"repository": domain.LadoRepoRef(repo), "reason": err.Error()})
	}
	return client, nil
}

func (s *Service) task(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, error) {
	task, ok, err := s.deps.Store.LadoTask(ctx, id)
	if err != nil {
		return domain.LadoTask{}, apierr.Internal("LADO_TASK_LOAD_FAILED", "Failed to load the task")
	}
	if !ok || task.IssueRef == "" {
		return domain.LadoTask{}, apierr.NotFound("LADO_TASK_NOT_FOUND", "Unknown task")
	}
	return task, nil
}

func (s *Service) project(ctx context.Context, projectID domain.ProjectID) (domain.ProjectRecord, error) {
	project, ok, err := s.deps.Projects.GetProject(ctx, string(projectID))
	if err != nil {
		return domain.ProjectRecord{}, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok || !project.ArchivedAt.IsZero() {
		return domain.ProjectRecord{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	return project, nil
}

// ladoRepos returns the repositories of a project in LADO mode.
func (s *Service) ladoRepos(ctx context.Context, projectID domain.ProjectID) ([]ProjectRepo, error) {
	project, err := s.project(ctx, projectID)
	if err != nil {
		return nil, err
	}
	settings, err := s.deps.Store.LadoProjectSettings(ctx, projectID)
	if err != nil {
		return nil, apierr.Internal("LADO_SETTINGS_LOAD_FAILED", "Failed to load LADO project settings")
	}
	if !settings.LadoMode {
		return nil, apierr.Conflict("LADO_MODE_OFF", "This project is not in LADO mode.", nil)
	}
	repos, err := s.projectRepos(ctx, project)
	if err != nil {
		return nil, err
	}
	if len(repos) == 0 {
		return nil, apierr.Conflict("LADO_REPOSITORY_NOT_CONNECTED",
			"This project has no GitHub or GitLab repository with an origin remote. Connect one first.", nil)
	}
	return repos, nil
}

// projectRepos returns a project's repositories: a single-repo project's
// origin, or every workspace child with an origin on a host LADO supports.
func (s *Service) projectRepos(ctx context.Context, project domain.ProjectRecord) ([]ProjectRepo, error) {
	repos := []ProjectRepo{}
	if project.Kind == domain.ProjectKindWorkspace {
		children, err := s.deps.Projects.ListWorkspaceRepos(ctx, project.ID)
		if err != nil {
			return nil, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load the workspace's repositories")
		}
		for _, child := range children {
			if child.RepoOriginURL == "" {
				continue
			}
			if repo, err := domain.LadoTrackerRepoFromOrigin(child.RepoOriginURL); err == nil {
				repos = append(repos, ProjectRepo{Name: child.Name, Repo: repo})
			}
		}
		return repos, nil
	}
	if project.RepoOriginURL == "" {
		return repos, nil
	}
	repo, err := domain.LadoTrackerRepoFromOrigin(project.RepoOriginURL)
	if err != nil {
		return nil, apierr.Conflict("LADO_REPOSITORY_UNSUPPORTED", err.Error(), nil)
	}
	name := project.DisplayName
	if name == "" {
		name = project.ID
	}
	return append(repos, ProjectRepo{Name: name, Repo: repo}), nil
}

// pickRepo chooses the repository for a new issue. A project with one
// repository needs no choice.
func pickRepo(repos []ProjectRepo, ref string) (ProjectRepo, error) {
	if ref == "" {
		if len(repos) == 1 {
			return repos[0], nil
		}
		return ProjectRepo{}, apierr.Invalid("LADO_REPOSITORY_REQUIRED", "Choose the repository to create the issue in.", nil)
	}
	want, err := domain.ParseLadoRepoRef(ref)
	if err != nil {
		return ProjectRepo{}, apierr.Invalid("LADO_REPOSITORY_REF_INVALID", err.Error(), nil)
	}
	if repo, ok := findRepo(repos, want); ok {
		return repo, nil
	}
	return ProjectRepo{}, apierr.Invalid("LADO_REPOSITORY_NOT_IN_PROJECT", fmt.Sprintf("%s is not one of this project's repositories.", ref), map[string]any{"repository": ref})
}

func findRepo(repos []ProjectRepo, want domain.TrackerRepo) (ProjectRepo, bool) {
	for _, repo := range repos {
		if domain.LadoSameRepo(repo.Repo, want) {
			return repo, true
		}
	}
	return ProjectRepo{}, false
}

// issueBody appends the task marker to the developer's text.
func issueBody(body string, id domain.LadoTaskID) string {
	if body == "" {
		return domain.LadoTaskMarker(id)
	}
	return body + "\n\n" + domain.LadoTaskMarker(id)
}

// observedIssue is what LADO records from a read of an issue.
func observedIssue(issue domain.Issue, at time.Time) domain.LadoObservedIssue {
	ref := domain.LadoIssueRef(issue.ID)
	return domain.LadoObservedIssue{
		Title:          issue.Title,
		State:          issue.State,
		URL:            issue.URL,
		SnapshotSHA256: domain.LadoIssueSnapshot{ID: ref, Title: issue.Title, Body: issue.Body}.SHA256(),
		ObservedAt:     at,
	}
}

// findByMarker searches the repository's newest issues for a task's marker.
func findByMarker(ctx context.Context, client ports.LadoRepoClient, repo domain.TrackerRepo, id domain.LadoTaskID) (domain.Issue, bool, error) {
	issues, err := client.List(ctx, repo, domain.ListFilter{State: domain.ListAll, Limit: markerSearchLimit})
	if err != nil {
		return domain.Issue{}, false, err
	}
	marker := domain.LadoTaskMarker(id)
	for _, issue := range issues {
		if strings.Contains(issue.Body, marker) {
			return issue, true, nil
		}
	}
	return domain.Issue{}, false, nil
}

// trackerMessage drops the error chain's sentinel prefix, keeping what the
// tracker said.
func trackerMessage(err error) string {
	message := err.Error()
	if prefix := ports.ErrLadoIssueNotCreated.Error() + ": "; strings.HasPrefix(message, prefix) {
		return strings.TrimPrefix(message, prefix)
	}
	return message
}
