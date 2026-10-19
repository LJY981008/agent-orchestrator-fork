// Package ladotask owns LADO's issue-backed tasks: creating an issue from the
// developer's request, importing open issues, and tracking the issue of every
// task (docs/LJY-PRD/TDD.md §5.1 and
// docs/LJY-PRD/adr/0006-issues-as-work-items.md). Later build steps add plans
// and approvals here.
package ladotask

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
}

// Projects reads AO's project registry.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
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
}

// Service creates, imports and tracks tasks.
type Service struct {
	deps Deps
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
	return &Service{deps: deps}
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
	return tasks, nil
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
			return task, nil
		}
		return s.task(ctx, id)
	}
	return task, nil
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
	return updated, nil
}

// Cancel cancels a task. It never closes or edits the issue.
func (s *Service) Cancel(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, error) {
	cancelled, ok, err := s.deps.Store.CancelLadoTask(ctx, id, s.deps.Clock().UTC())
	if err != nil {
		return domain.LadoTask{}, apierr.Internal("LADO_TASK_CANCEL_FAILED", "Failed to cancel the task")
	}
	if !ok {
		return domain.LadoTask{}, apierr.NotFound("LADO_TASK_NOT_FOUND", "Unknown task")
	}
	return cancelled, nil
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
