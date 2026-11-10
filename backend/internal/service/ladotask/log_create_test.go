package ladotask

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestCreateFromLogMakesALogTask(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx := context.Background()
	task, err := fx.svc.CreateFromLog(ctx, LogCreateInput{
		ProjectID: "demo",
		Title:     "운영 오류: KeyError\n@ app.py",
		Body:      "## 요약\n\n- 횟수: 3\n",
	})
	if err != nil {
		t.Fatalf("CreateFromLog: %v", err)
	}
	if task.Source != domain.LadoTaskSourceLog || task.IssueRef != "github:acme/demo#1" || task.Agent != "" {
		t.Fatalf("task = %+v, want a linked task with the log source", task)
	}
	issue := fx.tracker.issues["acme/demo#1"]
	if issue.Title != "운영 오류: KeyError @ app.py" {
		t.Fatalf("title = %q, want one line", issue.Title)
	}
	if issue.Body != "## 요약\n\n- 횟수: 3\n\n<!-- lado-task:lado-task-1 -->" {
		t.Fatalf("body = %q, want the text and the task marker", issue.Body)
	}
	stored := fx.tasks(t, false)
	if len(stored) != 1 || stored[0].Source != domain.LadoTaskSourceLog {
		t.Fatalf("stored tasks = %+v, want the log source kept", stored)
	}

	// A task the developer creates keeps an empty source.
	created, err := fx.svc.Create(ctx, CreateInput{ProjectID: "demo", Text: "Add login"})
	if err != nil || created.Source != "" {
		t.Fatalf("Create = %+v, %v; want an empty source", created, err)
	}
}

func TestCreateFromLogCutsWhatCreateWouldRefuse(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	task, err := fx.svc.CreateFromLog(context.Background(), LogCreateInput{
		ProjectID: "demo",
		Title:     strings.Repeat("가", 300),
		Body:      strings.Repeat("y", 70000),
	})
	if err != nil {
		t.Fatalf("CreateFromLog: %v", err)
	}
	issue := fx.tracker.issues[strings.TrimPrefix(string(task.IssueRef), "github:")]
	if n := utf8.RuneCountInString(issue.Title); n != maxTitleRunes {
		t.Fatalf("title runes = %d, want %d", n, maxTitleRunes)
	}
	marker := "\n\n" + domain.LadoTaskMarker(task.ID)
	if !strings.HasSuffix(issue.Body, marker) || utf8.RuneCountInString(strings.TrimSuffix(issue.Body, marker)) != maxBodyRunes {
		t.Fatalf("body runes = %d, want %d and the marker", utf8.RuneCountInString(issue.Body), maxBodyRunes)
	}

	// An empty title still makes an issue.
	task, err = fx.svc.CreateFromLog(context.Background(), LogCreateInput{ProjectID: "demo", Title: " \n ", Body: "x"})
	if err != nil || fx.tracker.issues[strings.TrimPrefix(string(task.IssueRef), "github:")].Title != "운영 오류" {
		t.Fatalf("empty title: %+v, %v", task, err)
	}
}

func TestCreateFromLogReturnsTheUnconfirmedTask(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	fx.tracker.lostAnswer = errors.New("connection reset by peer")
	fx.tracker.listErr = errors.New("rate limited")
	task, err := fx.svc.CreateFromLog(context.Background(), LogCreateInput{ProjectID: "demo", Title: "운영 오류", Body: "x"})
	if apiCode(err) != "LADO_ISSUE_CREATE_UNCONFIRMED" {
		t.Fatalf("err = %v, want LADO_ISSUE_CREATE_UNCONFIRMED", err)
	}
	pending := fx.pending(t)
	if task.ID == "" || task.IssueRef != "" || len(pending) != 1 || pending[0].ID != task.ID || pending[0].Source != domain.LadoTaskSourceLog {
		t.Fatalf("task = %+v, pending = %+v; want the pending log task returned", task, pending)
	}

	// Create keeps returning nothing in that case.
	created, err := fx.svc.Create(context.Background(), CreateInput{ProjectID: "demo", Text: "Add login"})
	if apiCode(err) != "LADO_ISSUE_CREATE_UNCONFIRMED" || created.ID != "" {
		t.Fatalf("Create = %+v, %v; want no task with the unconfirmed error", created, err)
	}
}

// linkFailStore fails the next links of a created issue to its task, as a
// busy database would.
type linkFailStore struct {
	*sqlite.Store
	fails int
}

func (s *linkFailStore) LinkLadoTaskIssue(ctx context.Context, id domain.LadoTaskID, ref domain.IssueID, issue domain.LadoObservedIssue, at time.Time) (domain.LadoTask, error) {
	if s.fails > 0 {
		s.fails--
		return domain.LadoTask{}, errors.New("database is locked")
	}
	return s.Store.LinkLadoTaskIssue(ctx, id, ref, issue, at)
}

// TestCreateFromLogReturnsThePendingTaskWhenTheLinkFails: the issue exists
// but LADO could not save it on the task. The log watch gets the pending
// task, so it does not create a second issue, and ResolvePending links the
// issue by its marker. Create still returns nothing.
func TestCreateFromLogReturnsThePendingTaskWhenTheLinkFails(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx := context.Background()
	store := &linkFailStore{Store: fx.store, fails: 1}
	fx.svc.deps.Store = store
	task, err := fx.svc.CreateFromLog(ctx, LogCreateInput{ProjectID: "demo", Title: "운영 오류", Body: "x"})
	if apiCode(err) != "LADO_TASK_LINK_FAILED" {
		t.Fatalf("err = %v, want LADO_TASK_LINK_FAILED", err)
	}
	pending := fx.pending(t)
	if task.ID == "" || task.IssueRef != "" || len(pending) != 1 || pending[0].ID != task.ID || pending[0].Source != domain.LadoTaskSourceLog {
		t.Fatalf("task = %+v, pending = %+v; want the pending log task returned", task, pending)
	}

	fx.svc.ResolvePending(ctx, fx.now.Add(time.Hour))
	linked, ok, err := fx.store.LadoTask(ctx, task.ID)
	if err != nil || !ok || linked.IssueRef != "github:acme/demo#1" {
		t.Fatalf("task after ResolvePending = %+v, %v, %v; want the created issue linked", linked, ok, err)
	}
	if creates, _ := fx.tracker.counts(); creates != 1 {
		t.Fatalf("issues created = %d, want 1", creates)
	}

	store.fails = 1
	created, err := fx.svc.Create(ctx, CreateInput{ProjectID: "demo", Text: "Add login"})
	if apiCode(err) != "LADO_TASK_LINK_FAILED" || created.ID != "" {
		t.Fatalf("Create = %+v, %v; want no task with the link error", created, err)
	}
}

// TestCreateFromLogLinksAfterTheContextEnds: a request whose context ends
// right after the tracker created the issue still saves it on the task.
func TestCreateFromLogLinksAfterTheContextEnds(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fx.tracker.onCreate = cancel
	task, err := fx.svc.CreateFromLog(ctx, LogCreateInput{ProjectID: "demo", Title: "운영 오류", Body: "x"})
	if err != nil || task.IssueRef != "github:acme/demo#1" {
		t.Fatalf("CreateFromLog = %+v, %v; want the issue linked although the context ended", task, err)
	}
	if pending := fx.pending(t); len(pending) != 0 {
		t.Fatalf("pending = %+v, want none", pending)
	}
}

func TestCreateFromLogFailsLikeCreate(t *testing.T) {
	fx := newGitHubTaskFixture(t)
	ctx := context.Background()
	fx.tracker.createErr = errors.New("github tracker: 502 Bad Gateway")
	task, err := fx.svc.CreateFromLog(ctx, LogCreateInput{ProjectID: "demo", Title: "운영 오류", Body: "x"})
	if apiCode(err) != "LADO_ISSUE_CREATE_FAILED" || task.ID != "" || len(fx.pending(t)) != 0 {
		t.Fatalf("CreateFromLog = %+v, %v; want LADO_ISSUE_CREATE_FAILED and nothing kept", task, err)
	}
	fx.tracker.createErr = nil
	if _, err := fx.store.SetLadoMode(ctx, "demo", false, taskEpoch); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.CreateFromLog(ctx, LogCreateInput{ProjectID: "demo", Title: "운영 오류"}); apiCode(err) != "LADO_MODE_OFF" {
		t.Fatalf("err = %v, want LADO_MODE_OFF", err)
	}
	if _, err := fx.svc.LogRepositories(ctx, "demo"); apiCode(err) != "LADO_MODE_OFF" {
		t.Fatalf("LogRepositories err = %v, want LADO_MODE_OFF", err)
	}
}

func TestLogRepositoriesListTheLadoProjectsRepositories(t *testing.T) {
	ws := newWorkspaceTaskFixture(t)
	ctx := context.Background()
	repos, err := ws.svc.LogRepositories(ctx, "demo")
	if err != nil || len(repos) != 2 || repos[0].Repo != ws.back || repos[1].Repo != ws.front {
		t.Fatalf("LogRepositories = %+v, %v; want back and front", repos, err)
	}
	// A workspace's log task goes to the repository the settings name.
	if _, err := ws.svc.CreateFromLog(ctx, LogCreateInput{ProjectID: "demo", Title: "운영 오류"}); apiCode(err) != "LADO_REPOSITORY_REQUIRED" {
		t.Fatalf("err = %v, want LADO_REPOSITORY_REQUIRED", err)
	}
	task, err := ws.svc.CreateFromLog(ctx, LogCreateInput{ProjectID: "demo", Repository: "github:acme/back", Title: "운영 오류"})
	if err != nil || task.Repo != ws.back {
		t.Fatalf("CreateFromLog = %+v, %v; want the back repository", task, err)
	}
}
