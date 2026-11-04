package controllers_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoalert"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladolaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

var ladoWatchTime = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

// newLadoWatchStore returns a store with projects mer and other, and task
// lado-task-1 of mer.
func newLadoWatchStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s := sqlitetest.MustOpen(t)
	ctx := context.Background()
	for _, id := range []string{"mer", "other"} {
		if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: id, Path: "/tmp/" + id, RegisteredAt: ladoWatchTime}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateLadoTask(ctx, domain.LadoTask{
		ID: "lado-task-1", ProjectID: "mer", IssueRef: "github:acme/demo#42",
		Repo:  domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"},
		Agent: "claude-code", Model: "opus", CreatedAt: ladoWatchTime, UpdatedAt: ladoWatchTime,
		Issue: domain.LadoObservedIssue{Title: "Watch", State: domain.IssueOpen, URL: "https://github.com/acme/demo/issues/42", SnapshotSHA256: "sha", ObservedAt: ladoWatchTime},
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

// Without rows the settings are the defaults, compact 200000; a project's
// value wins over the global one and its null follows it; a value out of
// range is a 400 that writes nothing; an unknown project is a 404.
func TestLadoAPI_WatchSettings(t *testing.T) {
	s := newLadoWatchStore(t)
	srv := newLadoTestServer(t, httpd.APIDeps{LadoWatch: ladoalert.New(ladoalert.Deps{
		Store: s, Clock: func() time.Time { return ladoWatchTime }, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/watch-settings", "")
	if status != http.StatusOK {
		t.Fatalf("GET global = %d %s", status, body)
	}
	global := decodeLadoJSON[controllers.LadoWatchSettingsResponse](t, body)
	if global.ProjectID != "" || global.Effective.CompactWindow != 200_000 || global.Effective.TaskCostCents != 100_00 ||
		global.Effective.SessionCostCents != 30_00 || global.Effective.SessionCompactions != 3 || global.Effective.StallMinutes != 120 ||
		!global.Effective.WakeEnabled || global.Values.CompactWindow != nil || global.UpdatedAt != nil || global.Defaults != global.Effective {
		t.Fatalf("global without a row = %+v", global)
	}

	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/lado/watch-settings", `{"compactWindow":300000,"stallMinutes":60}`)
	if status != http.StatusOK {
		t.Fatalf("PUT global = %d %s", status, body)
	}
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/lado/projects/mer/watch-settings", `{"compactWindow":150000,"wakeEnabled":false,"stallMinutes":null}`)
	if status != http.StatusOK {
		t.Fatalf("PUT project = %d %s", status, body)
	}
	project := decodeLadoJSON[controllers.LadoWatchSettingsResponse](t, body)
	if project.ProjectID != "mer" || project.Effective.CompactWindow != 150_000 || project.Effective.StallMinutes != 60 || project.Effective.WakeEnabled ||
		project.Inherited.CompactWindow != 300_000 || project.Values.StallMinutes != nil || project.Values.CompactWindow == nil || project.UpdatedAt == nil {
		t.Fatalf("project = %+v", project)
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/other/watch-settings", "")
	if other := decodeLadoJSON[controllers.LadoWatchSettingsResponse](t, body); status != http.StatusOK || other.Effective.CompactWindow != 300_000 || !other.Effective.WakeEnabled {
		t.Fatalf("project without a row = %d %+v", status, other)
	}

	for _, bad := range []string{`{"compactWindow":99999}`, `{"compactWindow":1000001}`, `{"taskCostCents":0}`, `{"stallMinutes":5}`} {
		body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/projects/mer/watch-settings", bad)
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, "LADO_WATCH_SETTING_OUT_OF_RANGE")
		body, status, _ = doRequest(t, srv, "PUT", "/api/v1/lado/watch-settings", bad)
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, "LADO_WATCH_SETTING_OUT_OF_RANGE")
	}
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/lado/watch-settings", `{"compactWindow":"big"}`)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/watch-settings", "")
	if got := decodeLadoJSON[controllers.LadoWatchSettingsResponse](t, body); got.Effective.CompactWindow != 150_000 {
		t.Fatalf("a refused change wrote: %+v", got)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/nobody/watch-settings", "")
	wantLadoErrorCode(t, body, status, http.StatusNotFound, "PROJECT_NOT_FOUND")
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/lado/projects/nobody/watch-settings", `{}`)
	wantLadoErrorCode(t, body, status, http.StatusNotFound, "PROJECT_NOT_FOUND")
}

// The alert list carries each alert's one line and desktop notification
// id, narrows to a task and to the unseen ones, and seen marks one.
func TestLadoAPI_Alerts(t *testing.T) {
	s := newLadoWatchStore(t)
	ctx := context.Background()
	var ids []domain.LadoAlertID
	for i, a := range []domain.LadoAlert{
		{TaskID: "lado-task-1", Kind: domain.LadoAlertTaskCost, Level: 1, Value: 120_00, Threshold: 100_00},
		{TaskID: "lado-task-1", SessionID: "mer-7", Kind: domain.LadoAlertSessionCompactions, Level: 1, Value: 3, Threshold: 3},
	} {
		a.CreatedAt = ladoWatchTime.Add(time.Duration(i) * time.Minute)
		id, added, err := s.AddLadoAlert(ctx, a)
		if err != nil || !added {
			t.Fatalf("add alert: %v %v", added, err)
		}
		ids = append(ids, id)
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoWatch: ladoalert.New(ladoalert.Deps{
		Store: s, Clock: func() time.Time { return ladoWatchTime.Add(time.Hour) }, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/alerts", "")
	if status != http.StatusOK {
		t.Fatalf("GET = %d %s", status, body)
	}
	list := decodeLadoJSON[controllers.LadoAlertListResponse](t, body)
	if len(list.Alerts) != 2 {
		t.Fatalf("alerts = %+v", list.Alerts)
	}
	newest := list.Alerts[0]
	if newest.ID != int64(ids[1]) || newest.SessionID != "mer-7" || newest.Kind != "session_compactions" || newest.IssueRef != "github:acme/demo#42" ||
		newest.Line != "LADO 알림: #42 세션 mer-7 compact 3번(기준 3번) — 파트를 다시 쪼갤지 확인하세요" ||
		newest.NotificationID != fmt.Sprintf("lado-alert:%d", ids[1]) || newest.SeenAt != nil || newest.ProjectID != "mer" {
		t.Fatalf("newest = %+v", newest)
	}
	if task := list.Alerts[1]; task.SessionID != "" || task.Line != "LADO 알림: #42 작업 비용 추정 $120.00, 기준 $100.00 넘음" {
		t.Fatalf("task alert = %+v", task)
	}

	body, status, _ = doRequest(t, srv, "POST", fmt.Sprintf("/api/v1/lado/alerts/%d/seen", ids[0]), "")
	if seen := decodeLadoJSON[controllers.LadoAlertResponse](t, body); status != http.StatusOK || seen.SeenAt == nil || !seen.SeenAt.Equal(ladoWatchTime.Add(time.Hour)) {
		t.Fatalf("seen = %d %s", status, body)
	}
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/alerts?unseen=true&taskId=lado-task-1&limit=5", "")
	if unseen := decodeLadoJSON[controllers.LadoAlertListResponse](t, body); len(unseen.Alerts) != 1 || unseen.Alerts[0].ID != int64(ids[1]) {
		t.Fatalf("unseen = %s", body)
	}
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/other/alerts", "")
	if other := decodeLadoJSON[controllers.LadoAlertListResponse](t, body); other.Alerts == nil || len(other.Alerts) != 0 {
		t.Fatalf("another project's alerts = %s", body)
	}

	for _, bad := range []string{"?unseen=maybe", "?limit=0", "?limit=501", "?limit=x"} {
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/alerts"+bad, "")
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_QUERY")
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/other/alerts?taskId=lado-task-1", "")
	wantLadoErrorCode(t, body, status, http.StatusNotFound, "LADO_TASK_NOT_FOUND")
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/nobody/alerts", "")
	wantLadoErrorCode(t, body, status, http.StatusNotFound, "PROJECT_NOT_FOUND")
	for _, bad := range []string{"0", "x", "9999"} {
		body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/alerts/"+bad+"/seen", "")
		wantLadoErrorCode(t, body, status, http.StatusNotFound, "LADO_ALERT_NOT_FOUND")
	}
}

// ladoCompactStore is what ladolaunch reads: a project in LADO mode or not,
// and the watch settings of the real store.
type ladoCompactStore struct {
	*sqlite.Store
	ladoMode map[domain.ProjectID]bool
}

func (s ladoCompactStore) LadoProjectSettings(_ context.Context, id domain.ProjectID) (domain.LadoProjectSettings, error) {
	return domain.LadoProjectSettings{ProjectID: id, LadoMode: s.ladoMode[id]}, nil
}

// The effective view tells the project's compact window, whether its
// sessions get it, and the settings that turn auto-compact off.
func TestLadoAPI_EffectiveCompactInfo(t *testing.T) {
	s := newLadoWatchStore(t)
	ctx := context.Background()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(`{"autoCompactEnabled":false,"autoCompactWindow":900000}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: repo, RegisteredAt: ladoWatchTime}); err != nil {
		t.Fatal(err)
	}
	window := 120_000
	if err := s.PutLadoWatchSettings(ctx, "mer", domain.LadoWatchOverrides{CompactWindow: &window}); err != nil {
		t.Fatal(err)
	}
	launch := ladolaunch.New(ladolaunch.Options{
		DataDir: t.TempDir(), Home: t.TempDir(), Getenv: func(string) string { return "" },
		Store: ladoCompactStore{Store: s, ladoMode: map[domain.ProjectID]bool{"mer": true}}, Projects: s,
	})
	rules := &fakeLadoRuleService{effective: ladopolicy.EffectiveView{Resolution: ladopolicy.Resolution{
		Ceiling: domain.PermissionModeAuto, Mode: domain.PermissionModeAuto, Content: ladopolicy.SnapshotContent{ProjectID: "mer"},
	}}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRules: rules, LadoCompact: launch})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/effective?projectId=mer", "")
	if status != http.StatusOK {
		t.Fatalf("GET = %d %s", status, body)
	}
	resp := decodeLadoJSON[controllers.LadoEffectiveResponse](t, body)
	want := controllers.LadoCompactWarningResponse{Source: "project_settings", Setting: "autoCompactEnabled", Path: filepath.Join(repo, ".claude", "settings.json")}
	if resp.CompactWindow != 120_000 || !resp.CompactApplies || len(resp.CompactWarnings) != 1 || resp.CompactWarnings[0] != want {
		t.Fatalf("compact = %d %v %+v", resp.CompactWindow, resp.CompactApplies, resp.CompactWarnings)
	}

	// A project in LADO mode off gets no window; the view says so.
	rules.effective.Content.ProjectID = "other"
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/effective?projectId=other", "")
	if resp := decodeLadoJSON[controllers.LadoEffectiveResponse](t, body); resp.CompactApplies || resp.CompactWindow != 200_000 || resp.CompactWarnings == nil {
		t.Fatalf("LADO mode off = %+v %v %+v", resp.CompactWindow, resp.CompactApplies, resp.CompactWarnings)
	}

	// Without the compact service the view keeps the default window.
	srv = newLadoTestServer(t, httpd.APIDeps{LadoRules: rules})
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/effective?projectId=other", "")
	if resp := decodeLadoJSON[controllers.LadoEffectiveResponse](t, body); resp.CompactApplies || resp.CompactWindow != 200_000 || resp.CompactWarnings == nil {
		t.Fatalf("without the service = %+v", resp)
	}
}
