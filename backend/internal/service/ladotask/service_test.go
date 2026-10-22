package ladotask

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// fakeTracker is one repository's issues.
type fakeTracker struct {
	mu      sync.Mutex
	issues  map[string]domain.Issue // by native id
	numbers int
	// createErr is returned without creating an issue.
	createErr error
	// lostAnswer creates the issue, then returns this error as if the answer
	// never arrived.
	lostAnswer error
	listErr    error
	getErr     error
	creates    int
	gets       int
	// comments are each issue's comments, by native id.
	comments    map[string][]domain.LadoIssueComment
	commentsErr error
}

func newFakeTracker() *fakeTracker {
	return &fakeTracker{issues: map[string]domain.Issue{}}
}

func (f *fakeTracker) add(repo domain.TrackerRepo, title, body string, state domain.NormalizedIssueState) domain.Issue {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.numbers++
	native := fmt.Sprintf("%s#%d", repo.Native, f.numbers)
	issue := domain.Issue{
		ID:    domain.TrackerID{Provider: repo.Provider, Native: native},
		Title: title, Body: body, State: state,
		URL: "https://example.com/" + strings.ReplaceAll(native, "#", "/issues/"),
	}
	f.issues[native] = issue
	return issue
}

func (f *fakeTracker) edit(native string, change func(*domain.Issue)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	issue := f.issues[native]
	change(&issue)
	f.issues[native] = issue
}

func (f *fakeTracker) CreateIssue(_ context.Context, repo domain.TrackerRepo, title, body string) (domain.Issue, error) {
	f.mu.Lock()
	f.creates++
	createErr, lost := f.createErr, f.lostAnswer
	f.mu.Unlock()
	if createErr != nil {
		return domain.Issue{}, createErr
	}
	issue := f.add(repo, title, body, domain.IssueOpen)
	if lost != nil {
		return domain.Issue{}, lost
	}
	return issue, nil
}

func (f *fakeTracker) Get(_ context.Context, id domain.TrackerID) (domain.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.getErr != nil {
		return domain.Issue{}, f.getErr
	}
	issue, ok := f.issues[id.Native]
	if !ok {
		return domain.Issue{}, errors.New("issue not found")
	}
	return issue, nil
}

func (f *fakeTracker) List(_ context.Context, _ domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []domain.Issue
	for _, issue := range f.issues {
		open := issue.State != domain.IssueDone && issue.State != domain.IssueCancelled
		if filter.State == domain.ListOpen && !open {
			continue
		}
		out = append(out, issue)
	}
	// Newest first, as the trackers list.
	sort.Slice(out, func(i, j int) bool { return issueNumber(out[i]) > issueNumber(out[j]) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func issueNumber(issue domain.Issue) int {
	return domain.LadoIssueNumber(issue.ID)
}

func (f *fakeTracker) Preflight(context.Context) error { return nil }

func (f *fakeTracker) Login(context.Context) (string, error) { return "ljy", nil }

func (f *fakeTracker) CheckAccess(context.Context, domain.TrackerRepo) (ports.LadoRepoAccess, error) {
	return ports.LadoRepoAccess{}, errors.New("not used")
}

func (f *fakeTracker) IssueComments(_ context.Context, id domain.TrackerID) ([]domain.LadoIssueComment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commentsErr != nil {
		return nil, f.commentsErr
	}
	return append([]domain.LadoIssueComment(nil), f.comments[id.Native]...), nil
}

// comment adds a comment to an issue.
func (f *fakeTracker) comment(native string, c domain.LadoIssueComment) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.comments == nil {
		f.comments = map[string][]domain.LadoIssueComment{}
	}
	f.comments[native] = append(f.comments[native], c)
}

func (f *fakeTracker) counts() (creates, gets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates, f.gets
}

type fakeClients struct {
	tracker *fakeTracker
	// byRepo holds a workspace's trackers by canonical repository id. A
	// repository missing from it uses tracker.
	byRepo map[string]*fakeTracker
	err    error
	repos  []domain.TrackerRepo
}

func (c *fakeClients) ForRepo(_ context.Context, repo domain.TrackerRepo, token string) (ports.LadoRepoClient, error) {
	if token != "" {
		return nil, errors.New("the task service must use the host's token chain")
	}
	c.repos = append(c.repos, repo)
	if c.err != nil {
		return nil, c.err
	}
	if tracker, ok := c.byRepo[domain.LadoRepoRef(repo)]; ok {
		return tracker, nil
	}
	return c.tracker, nil
}

var taskEpoch = time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)

type taskFixture struct {
	svc     *Service
	store   *sqlite.Store
	tracker *fakeTracker
	clients *fakeClients
	repo    domain.TrackerRepo
	now     time.Time
	// dataDir holds the store's database.
	dataDir string
}

func newTaskFixture(t *testing.T, origin string) *taskFixture {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	store := sqlitetest.MustOpenAt(t, dataDir)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "demo", Path: t.TempDir(), RepoOriginURL: origin, RegisteredAt: taskEpoch}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := store.SetLadoMode(ctx, "demo", true, taskEpoch); err != nil {
		t.Fatalf("turn on LADO mode: %v", err)
	}
	repo, err := domain.LadoTrackerRepoFromOrigin(origin)
	if err != nil {
		t.Fatalf("origin %s: %v", origin, err)
	}
	fx := &taskFixture{store: store, tracker: newFakeTracker(), repo: repo, now: taskEpoch, dataDir: dataDir}
	fx.clients = &fakeClients{tracker: fx.tracker}
	ids := 0
	fx.svc = New(Deps{
		Store:    store,
		Projects: store,
		Clients:  fx.clients,
		Clock:    func() time.Time { return fx.now },
		NewID: func() string {
			ids++
			return strconv.Itoa(ids)
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return fx
}

func newGitHubTaskFixture(t *testing.T) *taskFixture {
	return newTaskFixture(t, "git@github.com:acme/demo.git")
}

// workspaceFixture is a workspace project whose back and front children each
// have their own repository and tracker. Its docs child has no origin.
type workspaceFixture struct {
	*taskFixture
	back, front               domain.TrackerRepo
	backTracker, frontTracker *fakeTracker
}

func newWorkspaceTaskFixture(t *testing.T) *workspaceFixture {
	t.Helper()
	fx := newTaskFixture(t, "git@github.com:acme/demo.git")
	ctx := context.Background()
	children := []domain.WorkspaceRepoRecord{
		{Name: "back", RelativePath: "back", RepoOriginURL: "git@github.com:acme/back.git", RegisteredAt: taskEpoch},
		{Name: "docs", RelativePath: "docs", RegisteredAt: taskEpoch},
		{Name: "front", RelativePath: "front", RepoOriginURL: "https://github.com/acme/front.git", RegisteredAt: taskEpoch},
	}
	project := domain.ProjectRecord{ID: "demo", Kind: domain.ProjectKindWorkspace, Path: t.TempDir(), RegisteredAt: taskEpoch}
	if err := fx.store.UpsertWorkspaceProject(ctx, project, children); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	ws := &workspaceFixture{
		taskFixture:  fx,
		back:         domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/back"},
		front:        domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/front"},
		backTracker:  newFakeTracker(),
		frontTracker: newFakeTracker(),
	}
	fx.clients.byRepo = map[string]*fakeTracker{"github:acme/back": ws.backTracker, "github:acme/front": ws.frontTracker}
	return ws
}

func apiCode(err error) string {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func (fx *taskFixture) pending(t *testing.T) []domain.LadoTask {
	t.Helper()
	pending, err := fx.store.ListPendingLadoTasks(context.Background())
	if err != nil {
		t.Fatalf("ListPendingLadoTasks: %v", err)
	}
	return pending
}

func (fx *taskFixture) tasks(t *testing.T, includeCancelled bool) []domain.LadoTask {
	t.Helper()
	tasks, err := fx.store.ListLadoTasks(context.Background(), "demo", includeCancelled)
	if err != nil {
		t.Fatalf("ListLadoTasks: %v", err)
	}
	return tasks
}

func TestCreateMakesOneIssueAndLinksIt(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	task, err := fx.svc.Create(context.Background(), CreateInput{
		ProjectID: "demo", Text: "Add login\n\nUse the existing session store.", Agent: "claude-code", Model: "opus",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	issue := fx.tracker.issues["acme/demo#1"]
	if issue.Title != "Add login" || issue.Body != "Use the existing session store.\n\n<!-- lado-task:lado-task-1 -->" {
		t.Fatalf("created issue = %+v, want the first line as title and the rest plus the marker as body", issue)
	}
	if task.ID != "lado-task-1" || task.IssueRef != "github:acme/demo#1" || task.Agent != "claude-code" || task.Model != "opus" {
		t.Fatalf("task = %+v", task)
	}
	wantHash := domain.LadoIssueSnapshot{ID: "github:acme/demo#1", Title: issue.Title, Body: issue.Body}.SHA256()
	if task.Issue.Title != "Add login" || task.Issue.State != domain.IssueOpen || task.Issue.URL != issue.URL ||
		task.Issue.SnapshotSHA256 != wantHash || !task.Issue.ObservedAt.Equal(taskEpoch) || task.State() != domain.LadoTaskDraft {
		t.Fatalf("observed issue = %+v, want the created issue with its snapshot hash", task.Issue)
	}
	if creates, _ := fx.tracker.counts(); creates != 1 {
		t.Fatalf("issues created = %d, want 1", creates)
	}
	if len(fx.pending(t)) != 0 {
		t.Fatal("a task was left without its issue")
	}
}

func TestCreateNeedsALadoProject(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx := context.Background()
	if _, err := fx.store.SetLadoMode(ctx, "demo", false, taskEpoch); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.Create(ctx, CreateInput{ProjectID: "demo", Text: "Add login"}); apiCode(err) != "LADO_MODE_OFF" {
		t.Fatalf("err = %v, want LADO_MODE_OFF", err)
	}
	if _, err := fx.svc.Create(ctx, CreateInput{ProjectID: "missing", Text: "Add login"}); apiCode(err) != "PROJECT_NOT_FOUND" {
		t.Fatalf("err = %v, want PROJECT_NOT_FOUND", err)
	}
	if creates, _ := fx.tracker.counts(); creates != 0 {
		t.Fatalf("issues created = %d, want none", creates)
	}
}

func TestCreateChecksTheText(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	for text, code := range map[string]string{
		"   \n  ":                              "LADO_TASK_TITLE_REQUIRED",
		strings.Repeat("x", 257):               "LADO_TASK_TITLE_TOO_LONG",
		"Title\n" + strings.Repeat("y", 65001): "LADO_TASK_BODY_TOO_LONG",
	} {
		if _, err := fx.svc.Create(context.Background(), CreateInput{ProjectID: "demo", Text: text}); apiCode(err) != code {
			t.Errorf("Create(%.20q...) err = %v, want %s", text, err, code)
		}
	}
}

func TestCreateKeepsNothingWhenTheTrackerRefuses(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	fx.tracker.createErr = fmt.Errorf("%w: github tracker: 410 Issues are disabled for this repo", ports.ErrLadoIssueNotCreated)
	_, err := fx.svc.Create(context.Background(), CreateInput{ProjectID: "demo", Text: "Add login"})
	if apiCode(err) != "LADO_ISSUE_NOT_CREATED" || !strings.Contains(err.Error(), "Issues are disabled for this repo") {
		t.Fatalf("err = %v, want LADO_ISSUE_NOT_CREATED with the tracker's reason", err)
	}
	// The app shows the reason in the developer's language around it.
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Details["reason"] != "github tracker: 410 Issues are disabled for this repo" {
		t.Fatalf("details = %+v, want the tracker's reason alone", apiErr)
	}
	if len(fx.pending(t)) != 0 || len(fx.tasks(t, true)) != 0 {
		t.Fatal("a refused issue left a task behind")
	}
}

func TestCreateFindsAnIssueWhoseAnswerWasLost(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	fx.tracker.add(fx.repo, "Older issue", "no marker", domain.IssueOpen)
	fx.tracker.lostAnswer = errors.New("connection reset by peer")
	task, err := fx.svc.Create(context.Background(), CreateInput{ProjectID: "demo", Text: "Add login"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.IssueRef != "github:acme/demo#2" {
		t.Fatalf("task issue = %s, want the issue that carries its marker", task.IssueRef)
	}
	if creates, _ := fx.tracker.counts(); creates != 1 {
		t.Fatalf("issues created = %d, want exactly 1", creates)
	}
}

func TestCreateKeepsNothingWhenNoIssueWasCreated(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	fx.tracker.createErr = errors.New("github tracker: 502 Bad Gateway")
	_, err := fx.svc.Create(context.Background(), CreateInput{ProjectID: "demo", Text: "Add login"})
	if apiCode(err) != "LADO_ISSUE_CREATE_FAILED" {
		t.Fatalf("err = %v, want LADO_ISSUE_CREATE_FAILED", err)
	}
	if len(fx.pending(t)) != 0 {
		t.Fatal("a task without an issue was kept although the search found none")
	}
}

func TestCreateLeavesAnUnconfirmedTaskForTheNextPass(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	fx.tracker.lostAnswer = errors.New("connection reset by peer")
	fx.tracker.listErr = errors.New("rate limited")
	_, err := fx.svc.Create(context.Background(), CreateInput{ProjectID: "demo", Text: "Add login"})
	if apiCode(err) != "LADO_ISSUE_CREATE_UNCONFIRMED" {
		t.Fatalf("err = %v, want LADO_ISSUE_CREATE_UNCONFIRMED", err)
	}
	if len(fx.pending(t)) != 1 {
		t.Fatal("the unconfirmed task was not kept for the next pass")
	}

	fx.tracker.listErr = nil
	fx.svc.ResolvePending(context.Background(), taskEpoch)
	if len(fx.pending(t)) != 1 {
		t.Fatal("a pass resolved a task younger than its cutoff")
	}
	fx.svc.ResolvePending(context.Background(), taskEpoch.Add(time.Minute))
	tasks := fx.tasks(t, false)
	if len(tasks) != 1 || tasks[0].IssueRef != "github:acme/demo#1" {
		t.Fatalf("tasks = %+v, want the task linked to its issue", tasks)
	}
	if creates, _ := fx.tracker.counts(); creates != 1 {
		t.Fatalf("issues created = %d, want exactly 1", creates)
	}
}

// Acceptance gate 16: a daemon stopped between saving a task and saving its
// issue leaves one issue, which the task gets after the restart.
func TestRestartResolvesTasksWithoutAnIssue(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx := context.Background()
	stopped := taskEpoch.Add(-time.Hour)
	for _, id := range []domain.LadoTaskID{"lado-task-created", "lado-task-never"} {
		if _, err := fx.store.CreateLadoTask(ctx, domain.LadoTask{ID: id, ProjectID: "demo", Repo: fx.repo, CreatedAt: stopped, UpdatedAt: stopped}); err != nil {
			t.Fatal(err)
		}
	}
	fx.tracker.add(fx.repo, "Add login", "Body\n\n"+domain.LadoTaskMarker("lado-task-created"), domain.IssueOpen)

	// Start runs this pass first, with the daemon's start time as the cutoff.
	fx.svc.ResolvePending(ctx, taskEpoch)

	tasks := fx.tasks(t, false)
	if len(tasks) != 1 || tasks[0].ID != "lado-task-created" || tasks[0].IssueRef != "github:acme/demo#1" {
		t.Fatalf("tasks = %+v, want lado-task-created linked to #1", tasks)
	}
	if len(fx.pending(t)) != 0 {
		t.Fatal("a task whose issue was never created survived the restart")
	}
	if creates, _ := fx.tracker.counts(); creates != 0 {
		t.Fatalf("the restart created %d issues, want none", creates)
	}
}

func TestStartStopsWithItsContext(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := fx.svc.Start(ctx)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the tracking loop did not stop with its context")
	}
}

// Acceptance gate 16: importing an issue writes nothing to the tracker.
func TestImportWritesNothingToTheTracker(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx := context.Background()
	fx.tracker.add(fx.repo, "First", "one", domain.IssueOpen)
	fx.tracker.add(fx.repo, "Second", "two", domain.IssueOpen)

	result, err := fx.svc.Import(ctx, ImportInput{ProjectID: "demo", IssueRefs: []domain.IssueID{"github:acme/demo#1", "github:acme/demo#2"}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Tasks) != 2 || result.Tasks[0].Issue.Title != "First" || result.Tasks[1].IssueRef != "github:acme/demo#2" {
		t.Fatalf("imported = %+v", result.Tasks)
	}
	again, err := fx.svc.Import(ctx, ImportInput{ProjectID: "demo", IssueRefs: []domain.IssueID{"github:acme/demo#1"}})
	if err != nil {
		t.Fatalf("Import again: %v", err)
	}
	if len(again.Tasks) != 0 || len(again.Skipped) != 1 || again.Skipped[0] != "github:acme/demo#1" {
		t.Fatalf("second import = %+v, want #1 skipped", again)
	}
	if creates, _ := fx.tracker.counts(); creates != 0 {
		t.Fatalf("import created %d issues, want none", creates)
	}
	for refs, code := range map[string]string{
		"github:acme/other#1": "LADO_ISSUE_NOT_IN_REPOSITORY",
		"acme/demo#1":         "LADO_ISSUE_REF_INVALID",
		"":                    "LADO_IMPORT_EMPTY",
	} {
		var issueRefs []domain.IssueID
		if refs != "" {
			issueRefs = []domain.IssueID{domain.IssueID(refs)}
		}
		if _, err := fx.svc.Import(ctx, ImportInput{ProjectID: "demo", IssueRefs: issueRefs}); apiCode(err) != code {
			t.Errorf("Import(%q) err = %v, want %s", refs, err, code)
		}
	}
}

func TestImportableIssuesLeaveOutIssuesWithALiveTask(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx := context.Background()
	for _, title := range []string{"One", "Two", "Three"} {
		fx.tracker.add(fx.repo, title, "", domain.IssueOpen)
	}
	fx.tracker.add(fx.repo, "Closed", "", domain.IssueDone)
	if _, err := fx.svc.Import(ctx, ImportInput{ProjectID: "demo", IssueRefs: []domain.IssueID{"github:acme/demo#1", "github:acme/demo#2"}}); err != nil {
		t.Fatal(err)
	}
	tasks := fx.tasks(t, false)
	if _, err := fx.svc.Cancel(ctx, tasks[1].ID); err != nil {
		t.Fatal(err)
	}
	issues, failures, err := fx.svc.ImportableIssues(ctx, "demo")
	if err != nil || len(failures) != 0 {
		t.Fatalf("ImportableIssues: %v, failures %+v", err, failures)
	}
	var titles []string
	for _, item := range issues {
		titles = append(titles, item.Issue.Title)
		if item.Repo.Repo != fx.repo || item.Repo.Name != "demo" {
			t.Fatalf("issue repository = %+v, want the project's origin named after the project", item.Repo)
		}
	}
	if strings.Join(titles, ",") != "Three,Two" {
		t.Fatalf("importable = %v, want the open issues without a live task, newest first", titles)
	}
}

func TestRefreshAllRecordsIssueChanges(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx := context.Background()
	fx.tracker.add(fx.repo, "One", "", domain.IssueOpen)
	fx.tracker.add(fx.repo, "Two", "", domain.IssueOpen)
	if _, err := fx.svc.Import(ctx, ImportInput{ProjectID: "demo", IssueRefs: []domain.IssueID{"github:acme/demo#1", "github:acme/demo#2"}}); err != nil {
		t.Fatal(err)
	}
	tasks := fx.tasks(t, false)
	if _, err := fx.svc.Cancel(ctx, tasks[1].ID); err != nil {
		t.Fatal(err)
	}
	fx.tracker.edit("acme/demo#1", func(issue *domain.Issue) {
		issue.Title = "One, renamed"
		issue.State = domain.IssueDone
	})
	_, getsBefore := fx.tracker.counts()
	fx.now = taskEpoch.Add(5 * time.Minute)

	fx.svc.RefreshAll(ctx)

	_, getsAfter := fx.tracker.counts()
	if getsAfter-getsBefore != 1 {
		t.Fatalf("issue reads = %d, want 1 (cancelled tasks are not read)", getsAfter-getsBefore)
	}
	refreshed, err := fx.svc.Get(ctx, tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Issue.Title != "One, renamed" || refreshed.State() != domain.LadoTaskClosedUnmerged {
		t.Fatalf("refreshed task = %+v, want the renamed, closed issue", refreshed)
	}
}

func TestGetFallsBackToTheLastObservation(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx := context.Background()
	fx.tracker.add(fx.repo, "One", "", domain.IssueOpen)
	if _, err := fx.svc.Import(ctx, ImportInput{ProjectID: "demo", IssueRefs: []domain.IssueID{"github:acme/demo#1"}}); err != nil {
		t.Fatal(err)
	}
	fx.tracker.getErr = errors.New("tracker down")
	task, err := fx.svc.Get(ctx, "lado-task-1")
	if err != nil || task.Issue.Title != "One" {
		t.Fatalf("Get = %+v, %v; want the stored task", task, err)
	}
	if _, err := fx.svc.Get(ctx, "lado-task-missing"); apiCode(err) != "LADO_TASK_NOT_FOUND" {
		t.Fatalf("Get(missing) err = %v, want LADO_TASK_NOT_FOUND", err)
	}
}

func TestCancelAndPreferences(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx := context.Background()
	fx.tracker.add(fx.repo, "One", "", domain.IssueOpen)
	if _, err := fx.svc.Import(ctx, ImportInput{ProjectID: "demo", IssueRefs: []domain.IssueID{"github:acme/demo#1"}}); err != nil {
		t.Fatal(err)
	}
	agent := "codex"
	updated, err := fx.svc.UpdatePreferences(ctx, "lado-task-1", &agent, nil)
	if err != nil || updated.Agent != "codex" {
		t.Fatalf("UpdatePreferences = %+v, %v", updated, err)
	}
	if _, err := fx.svc.UpdatePreferences(ctx, "lado-task-1", nil, nil); apiCode(err) != "LADO_TASK_UPDATE_EMPTY" {
		t.Fatalf("empty update err = %v", err)
	}
	cancelled, err := fx.svc.Cancel(ctx, "lado-task-1")
	if err != nil || cancelled.State() != domain.LadoTaskCancelled {
		t.Fatalf("Cancel = %+v, %v", cancelled, err)
	}
	if creates, _ := fx.tracker.counts(); creates != 0 {
		t.Fatal("cancelling wrote to the tracker")
	}
	if _, err := fx.svc.Cancel(ctx, "lado-task-missing"); apiCode(err) != "LADO_TASK_NOT_FOUND" {
		t.Fatalf("Cancel(missing) err = %v", err)
	}
}

func TestSelfManagedGitLabRefsKeepTheHost(t *testing.T) {
	fx := newTaskFixture(t, "git@gitlab.example.com:group/project.git")
	task, err := fx.svc.Create(context.Background(), CreateInput{ProjectID: "demo", Text: "Add login"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.IssueRef != "gitlab:group/project#1@gitlab.example.com" {
		t.Fatalf("issue ref = %s, want the self-managed host in it", task.IssueRef)
	}
	if fx.clients.repos[0].Host != "gitlab.example.com" {
		t.Fatalf("client repo = %+v, want the self-managed host", fx.clients.repos[0])
	}
	if _, err := fx.svc.Get(context.Background(), task.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if last := fx.clients.repos[len(fx.clients.repos)-1]; last != fx.repo {
		t.Fatalf("refresh used repo %+v, want %+v", last, fx.repo)
	}
}

func TestCreateWithoutAToken(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	fx.clients.err = errors.New("no token")
	if _, err := fx.svc.Create(context.Background(), CreateInput{ProjectID: "demo", Text: "Add login"}); apiCode(err) != "LADO_TRACKER_UNAVAILABLE" {
		t.Fatalf("err = %v, want LADO_TRACKER_UNAVAILABLE", err)
	}
	if len(fx.pending(t)) != 0 {
		t.Fatal("a task was saved although the tracker was unreachable")
	}
}

func TestRepositoriesListAWorkspacesChildrenWithAnOrigin(t *testing.T) {
	ws := newWorkspaceTaskFixture(t)
	repos, err := ws.svc.Repositories(context.Background(), "demo")
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	want := []ProjectRepo{{Name: "back", Repo: ws.back}, {Name: "front", Repo: ws.front}}
	if len(repos) != len(want) || repos[0] != want[0] || repos[1] != want[1] {
		t.Fatalf("repositories = %+v, want %+v", repos, want)
	}

	single := newGitHubTaskFixture(t)
	repos, err = single.svc.Repositories(context.Background(), "demo")
	if err != nil || len(repos) != 1 || repos[0].Repo != single.repo || repos[0].Name != "demo" {
		t.Fatalf("single-repo repositories = %+v, %v", repos, err)
	}
}

func TestWorkspaceTaskGoesToTheChosenRepository(t *testing.T) {
	ws := newWorkspaceTaskFixture(t)
	ctx := context.Background()
	if _, err := ws.svc.Create(ctx, CreateInput{ProjectID: "demo", Text: "Fix the login form"}); apiCode(err) != "LADO_REPOSITORY_REQUIRED" {
		t.Fatalf("err = %v, want LADO_REPOSITORY_REQUIRED", err)
	}
	for ref, code := range map[string]string{
		"github:acme/other": "LADO_REPOSITORY_NOT_IN_PROJECT",
		"acme/front":        "LADO_REPOSITORY_REF_INVALID",
	} {
		if _, err := ws.svc.Create(ctx, CreateInput{ProjectID: "demo", Repository: ref, Text: "Fix the login form"}); apiCode(err) != code {
			t.Errorf("Create(repository %q) err = %v, want %s", ref, err, code)
		}
	}
	if len(ws.pending(t)) != 0 {
		t.Fatal("a refused repository choice left a task behind")
	}

	task, err := ws.svc.Create(ctx, CreateInput{ProjectID: "demo", Repository: "github:acme/front", Text: "Fix the login form"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.IssueRef != "github:acme/front#1" || task.Repo != ws.front {
		t.Fatalf("task = %+v, want the issue in acme/front", task)
	}
	if creates, _ := ws.frontTracker.counts(); creates != 1 {
		t.Fatalf("front issues created = %d, want 1", creates)
	}
	if creates, _ := ws.backTracker.counts(); creates != 0 {
		t.Fatalf("back issues created = %d, want none", creates)
	}
	stored, err := ws.store.ListLadoTasks(ctx, "demo", false)
	if err != nil || len(stored) != 1 || stored[0].Repo != ws.front {
		t.Fatalf("stored tasks = %+v, %v; want the front repository kept", stored, err)
	}
}

func TestWorkspaceImportsFromEveryRepository(t *testing.T) {
	ws := newWorkspaceTaskFixture(t)
	ctx := context.Background()
	ws.backTracker.add(ws.back, "Login API returns 500", "", domain.IssueOpen)
	ws.frontTracker.add(ws.front, "Login button misaligned", "", domain.IssueOpen)

	issues, failures, err := ws.svc.ImportableIssues(ctx, "demo")
	if err != nil || len(failures) != 0 {
		t.Fatalf("ImportableIssues: %v, failures %+v", err, failures)
	}
	got := map[string]string{}
	for _, item := range issues {
		got[string(domain.LadoIssueRef(item.Issue.ID))] = item.Repo.Name
	}
	if len(got) != 2 || got["github:acme/back#1"] != "back" || got["github:acme/front#1"] != "front" {
		t.Fatalf("importable = %v, want one issue from each child", got)
	}

	result, err := ws.svc.Import(ctx, ImportInput{ProjectID: "demo", IssueRefs: []domain.IssueID{"github:acme/back#1", "github:acme/front#1"}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Tasks) != 2 || result.Tasks[0].Repo != ws.back || result.Tasks[1].Repo != ws.front {
		t.Fatalf("imported = %+v, want one task per repository", result.Tasks)
	}
	if _, err := ws.svc.Import(ctx, ImportInput{ProjectID: "demo", IssueRefs: []domain.IssueID{"github:acme/demo#1"}}); apiCode(err) != "LADO_ISSUE_NOT_IN_REPOSITORY" {
		t.Fatalf("err = %v, want LADO_ISSUE_NOT_IN_REPOSITORY for a repository outside the workspace", err)
	}

	ws.svc.RefreshAll(ctx)
	if _, backGets := ws.backTracker.counts(); backGets != 2 {
		t.Fatalf("back reads = %d, want the import and one refresh", backGets)
	}
	if _, frontGets := ws.frontTracker.counts(); frontGets != 2 {
		t.Fatalf("front reads = %d, want the import and one refresh", frontGets)
	}
}

func TestWorkspaceReportsARepositoryItCannotRead(t *testing.T) {
	ws := newWorkspaceTaskFixture(t)
	ws.backTracker.add(ws.back, "Login API returns 500", "", domain.IssueOpen)
	ws.frontTracker.listErr = errors.New("403 Resource not accessible by personal access token")

	issues, failures, err := ws.svc.ImportableIssues(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ImportableIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Repo.Name != "back" {
		t.Fatalf("issues = %+v, want the back issue still listed", issues)
	}
	if len(failures) != 1 || failures[0].Repo.Name != "front" || !strings.Contains(failures[0].Problem, "403") {
		t.Fatalf("failures = %+v, want front reported with the tracker's reason", failures)
	}
}

func TestWorkspacePendingTaskResolvesInItsRepository(t *testing.T) {
	ws := newWorkspaceTaskFixture(t)
	ctx := context.Background()
	stopped := taskEpoch.Add(-time.Hour)
	if _, err := ws.store.CreateLadoTask(ctx, domain.LadoTask{ID: "lado-task-front", ProjectID: "demo", Repo: ws.front, CreatedAt: stopped, UpdatedAt: stopped}); err != nil {
		t.Fatal(err)
	}
	ws.frontTracker.add(ws.front, "Fix the login form", domain.LadoTaskMarker("lado-task-front"), domain.IssueOpen)

	ws.svc.ResolvePending(ctx, taskEpoch)

	tasks := ws.tasks(t, false)
	if len(tasks) != 1 || tasks[0].IssueRef != "github:acme/front#1" {
		t.Fatalf("tasks = %+v, want the task linked to its front issue", tasks)
	}
}

func TestWorkspaceWithoutARepositoryOrigin(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx := context.Background()
	project := domain.ProjectRecord{ID: "demo", Kind: domain.ProjectKindWorkspace, Path: t.TempDir(), RegisteredAt: taskEpoch}
	children := []domain.WorkspaceRepoRecord{{Name: "back", RelativePath: "back", RegisteredAt: taskEpoch}}
	if err := fx.store.UpsertWorkspaceProject(ctx, project, children); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.Create(ctx, CreateInput{ProjectID: "demo", Text: "Add login"}); apiCode(err) != "LADO_REPOSITORY_NOT_CONNECTED" {
		t.Fatalf("err = %v, want LADO_REPOSITORY_NOT_CONNECTED", err)
	}
}
