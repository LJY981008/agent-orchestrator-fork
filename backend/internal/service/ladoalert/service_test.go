package ladoalert_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoalert"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladousage"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// The real store has every read and write the service needs.
var (
	_ ladoalert.Store = (*store.Store)(nil)
	_ ladoalert.Usage = (*ladousage.Service)(nil)
)

var t0 = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

const dollar = int64(1_000_000_000) // nanodollars

// fakeUsage is each task's usage, in memory.
type fakeUsage struct {
	mu    sync.Mutex
	tasks map[domain.LadoTaskID]ladousage.TaskUsage
	reads map[domain.LadoTaskID]int
}

func (f *fakeUsage) TaskUsage(_ context.Context, id domain.LadoTaskID) (ladousage.TaskUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads[id]++
	u, ok := f.tasks[id]
	if !ok {
		return ladousage.TaskUsage{}, apierr.NotFound("LADO_TASK_NOT_FOUND", "Unknown task")
	}
	return u, nil
}

func (f *fakeUsage) set(u ladousage.TaskUsage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks[u.TaskID] = u
}

type fixture struct {
	store *sqlite.Store
	usage *fakeUsage
	svc   *ladoalert.Service
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s := sqlitetest.MustOpen(t)
	ctx := context.Background()
	for _, id := range []string{"mer", "other"} {
		if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: id, Path: "/tmp/" + id, RegisteredAt: t0}); err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range []struct {
		id      domain.LadoTaskID
		project domain.ProjectID
		ref     domain.IssueID
	}{{"lado-task-1", "mer", "github:acme/demo#42"}, {"lado-task-2", "mer", "github:acme/demo#43"}, {"lado-task-3", "other", "github:acme/other#7"}} {
		if _, err := s.CreateLadoTask(ctx, domain.LadoTask{
			ID: task.id, ProjectID: task.project, IssueRef: task.ref,
			Repo:  domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"},
			Agent: "claude-code", Model: "opus", CreatedAt: t0, UpdatedAt: t0,
			Issue: domain.LadoObservedIssue{Title: "Watch", State: domain.IssueOpen, URL: "https://github.com/acme/demo/issues/42", SnapshotSHA256: "sha", ObservedAt: t0},
		}); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{store: s, usage: &fakeUsage{tasks: map[domain.LadoTaskID]ladousage.TaskUsage{}, reads: map[domain.LadoTaskID]int{}}, now: t0}
	f.svc = ladoalert.New(ladoalert.Deps{
		Store: s, Usage: f.usage, Clock: func() time.Time { return f.now },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return f
}

func (f *fixture) tick(t *testing.T) {
	t.Helper()
	if err := f.svc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	f.now = f.now.Add(time.Minute)
}

func (f *fixture) alerts(t *testing.T, task domain.LadoTaskID) []domain.LadoAlert {
	t.Helper()
	alerts, err := f.store.ListLadoAlertsByTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	return alerts
}

type alertKey struct {
	session domain.SessionID
	kind    domain.LadoAlertKind
	level   int
}

func keys(alerts []domain.LadoAlert) map[alertKey]domain.LadoAlert {
	out := map[alertKey]domain.LadoAlert{}
	for _, a := range alerts {
		out[alertKey{a.SessionID, a.Kind, a.Level}] = a
	}
	return out
}

func usageOf(task domain.LadoTaskID, cost int64, sessions ...ladousage.SessionUsage) ladousage.TaskUsage {
	u := ladousage.TaskUsage{TaskID: task, ProjectID: "mer", Sessions: sessions}
	u.CostNanos = cost
	return u
}

func session(id domain.SessionID, cost int64, compactions int) ladousage.SessionUsage {
	s := ladousage.SessionUsage{SessionID: id, Role: domain.LadoSessionPart, Compactions: compactions}
	s.CostNanos = cost
	return s
}

// A cost past the threshold raises one alert, past twice it one more, and
// the same threshold read on later ticks raises nothing again. The task's own
// alert has no session and stays one row across ticks.
func TestTickRaisesCostAlertsOnceAndOnceMorePastDouble(t *testing.T) {
	f := newFixture(t)
	// Defaults: the task $100, a session $30.
	f.usage.set(usageOf("lado-task-1", 99*dollar, session("mer-1", 29*dollar, 0)))
	f.tick(t)
	if got := f.alerts(t, "lado-task-1"); len(got) != 0 {
		t.Fatalf("under the thresholds: %+v", got)
	}

	f.usage.set(usageOf("lado-task-1", 101*dollar, session("mer-1", 31*dollar, 0)))
	f.tick(t)
	f.tick(t)
	got := keys(f.alerts(t, "lado-task-1"))
	if len(got) != 2 {
		t.Fatalf("past the thresholds over two ticks: %+v, want one task and one session alert", got)
	}
	task := got[alertKey{"", domain.LadoAlertTaskCost, domain.LadoAlertLevelCrossed}]
	if task.ID == 0 || task.Value != 101_00 || task.Threshold != 100_00 || !task.CreatedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("task alert = %+v", task)
	}
	if s := got[alertKey{"mer-1", domain.LadoAlertSessionCost, domain.LadoAlertLevelCrossed}]; s.Value != 31_00 || s.Threshold != 30_00 {
		t.Fatalf("session alert = %+v", s)
	}

	f.usage.set(usageOf("lado-task-1", 201*dollar, session("mer-1", 61*dollar, 0)))
	f.tick(t)
	f.tick(t)
	got = keys(f.alerts(t, "lado-task-1"))
	if len(got) != 4 {
		t.Fatalf("past twice the thresholds: %+v, want one more of each", got)
	}
	if a := got[alertKey{"", domain.LadoAlertTaskCost, domain.LadoAlertLevelDoubled}]; a.Value != 201_00 ||
		Line(a) != "LADO 알림: #42 작업 비용 추정 $201.00, 기준 $100.00 의 2배 넘음" {
		t.Fatalf("doubled task alert = %+v %q", a, Line(a))
	}
}

func Line(a domain.LadoAlert) string { return ladoalert.Line(a) }

// A cost that jumps past twice the threshold raises the doubled alert only,
// and the crossed one never after.
func TestTickRaisesOnlyTheDoubledAlertOnAJump(t *testing.T) {
	f := newFixture(t)
	f.usage.set(usageOf("lado-task-1", 250*dollar))
	f.tick(t)
	f.tick(t)
	got := f.alerts(t, "lado-task-1")
	if len(got) != 1 || got[0].Level != domain.LadoAlertLevelDoubled || got[0].SessionID != "" {
		t.Fatalf("alerts = %+v, want the doubled task alert alone", got)
	}
}

// A session that compacted as often as the threshold raises one alert that
// asks whether to split the part again; the project's settings decide.
func TestTickRaisesTheCompactionAlertOnceUnderTheProjectsThreshold(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	two := 2
	if err := f.store.PutLadoWatchSettings(ctx, "mer", domain.LadoWatchOverrides{SessionCompactions: &two}); err != nil {
		t.Fatal(err)
	}
	f.usage.set(usageOf("lado-task-1", 0, session("mer-1", 0, 1), session("mer-2", 0, 0)))
	f.tick(t)
	if got := f.alerts(t, "lado-task-1"); len(got) != 0 {
		t.Fatalf("under the threshold: %+v", got)
	}
	f.usage.set(usageOf("lado-task-1", 0, session("mer-1", 0, 2), session("mer-2", 0, 1)))
	f.tick(t)
	f.usage.set(usageOf("lado-task-1", 0, session("mer-1", 0, 5), session("mer-2", 0, 1)))
	f.tick(t)
	got := f.alerts(t, "lado-task-1")
	if len(got) != 1 || got[0].SessionID != "mer-1" || got[0].Kind != domain.LadoAlertSessionCompactions || got[0].Value != 2 || got[0].Threshold != 2 {
		t.Fatalf("alerts = %+v, want one compaction alert of mer-1", got)
	}
	if line := Line(got[0]); !strings.Contains(line, "파트를 다시 쪼갤지 확인") || !strings.HasPrefix(line, domain.LadoNoticePrefix) {
		t.Fatalf("line = %q, want the prefix and the split question", line)
	}
}

// A session AO no longer has adds no alert; a task whose usage cannot be read
// is skipped, and the others still raise theirs.
func TestTickSkipsMissingSessionsAndUnreadableTasks(t *testing.T) {
	f := newFixture(t)
	missing := session("mer-gone", 500*dollar, 9)
	missing.UsageMissing = true
	f.usage.set(usageOf("lado-task-1", 0, missing))
	f.usage.set(ladousage.TaskUsage{TaskID: "lado-task-3", ProjectID: "other", Totals: ladousage.Totals{CostNanos: 150 * dollar}})
	f.tick(t) // lado-task-2 has no usage: NotFound
	if got := f.alerts(t, "lado-task-1"); len(got) != 0 {
		t.Fatalf("a missing session raised %+v", got)
	}
	if got := f.alerts(t, "lado-task-3"); len(got) != 1 || got[0].Kind != domain.LadoAlertTaskCost {
		t.Fatalf("other project's task: %+v", got)
	}
}

// A merged task is evaluated once more, then later ticks skip it.
func TestTickEvaluatesAMergedTaskOnce(t *testing.T) {
	f := newFixture(t)
	u := usageOf("lado-task-1", 120*dollar)
	u.EndReason = ladousage.EndMerged
	f.usage.set(u)
	f.tick(t)
	f.tick(t)
	if n := f.usage.reads["lado-task-1"]; n != 1 {
		t.Fatalf("merged task read %d times, want once", n)
	}
	if got := f.alerts(t, "lado-task-1"); len(got) != 1 {
		t.Fatalf("alerts = %+v, want the one raised before it was left alone", got)
	}
	if n := f.usage.reads["lado-task-2"]; n != 2 {
		t.Fatalf("open task read %d times, want every tick", n)
	}
}

// Settings: a null follows the row below it, a project's value wins over the
// global one, and a value out of range is a 400 that writes nothing.
func TestSettingsLayerAndRefuseOutOfRange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	view, err := f.svc.GlobalSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Effective != domain.LadoWatchDefaults() || view.Values.CompactWindow != nil || view.Effective.CompactWindow != 200_000 {
		t.Fatalf("global without a row = %+v", view)
	}

	window, stall := 300_000, 60
	if _, err := f.svc.UpdateGlobalSettings(ctx, domain.LadoWatchOverrides{CompactWindow: &window, StallMinutes: &stall}); err != nil {
		t.Fatal(err)
	}
	own := 150_000
	off := false
	view, err = f.svc.UpdateProjectSettings(ctx, "mer", domain.LadoWatchOverrides{CompactWindow: &own, WakeEnabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	if view.Effective.CompactWindow != 150_000 || view.Effective.StallMinutes != 60 || view.Effective.WakeEnabled ||
		view.Inherited.CompactWindow != 300_000 || view.Values.StallMinutes != nil || view.Values.UpdatedAt.IsZero() {
		t.Fatalf("project view = %+v", view)
	}
	other, err := f.svc.ProjectSettings(ctx, "other")
	if err != nil || other.Effective.CompactWindow != 300_000 || !other.Effective.WakeEnabled {
		t.Fatalf("project without a row = %+v, %v", other, err)
	}

	for _, bad := range []int{99_999, 1_000_001} {
		_, err := f.svc.UpdateProjectSettings(ctx, "mer", domain.LadoWatchOverrides{CompactWindow: &bad})
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid || apiErr.Code != "LADO_WATCH_SETTING_OUT_OF_RANGE" {
			t.Fatalf("compact window %d: %v, want a 400", bad, err)
		}
	}
	if view, _ := f.svc.ProjectSettings(ctx, "mer"); view.Effective.CompactWindow != 150_000 {
		t.Fatalf("a refused change wrote: %+v", view)
	}
	if _, err := f.svc.ProjectSettings(ctx, "nobody"); !isKind(err, apierr.KindNotFound) {
		t.Fatalf("unknown project: %v, want a 404", err)
	}
}

func isKind(err error, kind apierr.Kind) bool {
	var apiErr *apierr.Error
	return errors.As(err, &apiErr) && apiErr.Kind == kind
}

// The alert list narrows to a task and to the unseen ones; marking one seen
// keeps its first time, and an unknown alert is a 404.
func TestAlertsListAndSeen(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.usage.set(usageOf("lado-task-1", 101*dollar, session("mer-1", 31*dollar, 3)))
	f.usage.set(usageOf("lado-task-2", 101*dollar))
	f.tick(t)

	all, err := f.svc.ProjectAlerts(ctx, ladoalert.AlertQuery{Project: "mer"})
	if err != nil || len(all) != 4 {
		t.Fatalf("project alerts = %+v, %v; want 4", all, err)
	}
	one, err := f.svc.ProjectAlerts(ctx, ladoalert.AlertQuery{Project: "mer", Task: "lado-task-1"})
	if err != nil || len(one) != 3 {
		t.Fatalf("task alerts = %+v, %v; want 3", one, err)
	}
	seen, err := f.svc.MarkSeen(ctx, one[0].ID)
	if err != nil || !seen.Seen() || seen.ProjectID != "mer" || seen.IssueRef != "github:acme/demo#42" {
		t.Fatalf("seen = %+v, %v", seen, err)
	}
	first := seen.SeenAt
	f.now = f.now.Add(time.Hour)
	if again, err := f.svc.MarkSeen(ctx, one[0].ID); err != nil || !again.SeenAt.Equal(first) {
		t.Fatalf("seen again = %+v, %v; want the first time kept", again, err)
	}
	unseen, err := f.svc.ProjectAlerts(ctx, ladoalert.AlertQuery{Project: "mer", UnseenOnly: true})
	if err != nil || len(unseen) != 3 {
		t.Fatalf("unseen = %+v, %v; want 3", unseen, err)
	}
	if unseen, _ := f.svc.ProjectAlerts(ctx, ladoalert.AlertQuery{Project: "mer", Task: "lado-task-1", UnseenOnly: true, Limit: 1}); len(unseen) != 1 || unseen[0].Seen() {
		t.Fatalf("task unseen with limit 1 = %+v", unseen)
	}
	if _, err := f.svc.ProjectAlerts(ctx, ladoalert.AlertQuery{Project: "other", Task: "lado-task-1"}); !isKind(err, apierr.KindNotFound) {
		t.Fatalf("another project's task: %v, want a 404", err)
	}
	if _, err := f.svc.ProjectAlerts(ctx, ladoalert.AlertQuery{Project: "mer", Limit: 501}); !isKind(err, apierr.KindInvalid) {
		t.Fatalf("limit 501: %v, want a 400", err)
	}
	if _, err := f.svc.MarkSeen(ctx, 9999); !isKind(err, apierr.KindNotFound) {
		t.Fatalf("unknown alert: %v, want a 404", err)
	}
}
