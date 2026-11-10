package ladotask

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
