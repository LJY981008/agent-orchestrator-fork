package ladologwatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// The daemon wires the handler to LADO's store and the task service.
var (
	_ IncidentStore   = (*sqlite.Store)(nil)
	_ IncidentNotices = (*sqlite.Store)(nil)
	_ IncidentTasks   = (*ladotask.Service)(nil)
	_ IncidentPlans   = (*ladotask.Service)(nil)
)

var incidentEpoch = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// incidentFakeStore keeps fingerprints and tasks in memory, as the sqlite
// store does.
type incidentFakeStore struct {
	mu        sync.Mutex
	nextID    domain.LadoLogFingerprintID
	fps       map[string]*domain.LadoLogFingerprint // by project/fingerprint
	tasks     map[domain.LadoTaskID]domain.LadoTask
	burstAt   time.Time
	occWrites int
}

func newIncidentFakeStore() *incidentFakeStore {
	return &incidentFakeStore{fps: map[string]*domain.LadoLogFingerprint{}, tasks: map[domain.LadoTaskID]domain.LadoTask{}}
}

func (s *incidentFakeStore) byID(id domain.LadoLogFingerprintID) *domain.LadoLogFingerprint {
	for _, f := range s.fps {
		if f.ID == id {
			return f
		}
	}
	return nil
}

func incidentCopy(f *domain.LadoLogFingerprint) domain.LadoLogFingerprint {
	out := *f
	out.Instances = append([]domain.LadoLogInstanceCount(nil), f.Instances...)
	return out
}

func (s *incidentFakeStore) LadoLogFingerprint(_ context.Context, project domain.ProjectID, fp string) (domain.LadoLogFingerprint, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.fps[string(project)+"/"+fp]
	if f == nil {
		return domain.LadoLogFingerprint{}, false, nil
	}
	return incidentCopy(f), true, nil
}

func (s *incidentFakeStore) AddLadoLogOccurrences(_ context.Context, d domain.LadoLogOccurrenceDelta) (domain.LadoLogFingerprint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.occWrites++
	key := string(d.Project) + "/" + d.Fingerprint
	f := s.fps[key]
	if f == nil {
		s.nextID++
		f = &domain.LadoLogFingerprint{ID: s.nextID, ProjectID: d.Project, Fingerprint: d.Fingerprint, Service: d.Service,
			ExceptionKind: d.Kind, Location: d.Location, FirstSeenAt: d.FirstSeen, LastSeenAt: d.LastSeen}
		s.fps[key] = f
	}
	f.Count += d.Delta
	if d.LastSeen.After(f.LastSeenAt) {
		f.LastSeenAt = d.LastSeen
	}
	found := false
	for i := range f.Instances {
		if f.Instances[i].Instance == d.Instance {
			f.Instances[i].Count += d.Delta
			found = true
		}
	}
	if !found {
		f.Instances = append(f.Instances, domain.LadoLogInstanceCount{Instance: d.Instance, Image: d.Image, Count: d.Delta, LastSeenAt: d.LastSeen})
	}
	return incidentCopy(f), nil
}

func (s *incidentFakeStore) LinkLadoLogFingerprintTask(_ context.Context, id domain.LadoLogFingerprintID, task domain.LadoTaskID, issueRef, previous domain.IssueID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.byID(id)
	if f == nil {
		return domain.ErrLadoLogFingerprintNotFound
	}
	if f.TaskID == task {
		if issueRef != "" {
			f.IssueRef = issueRef
		}
		if previous != "" {
			f.PreviousIssueRef = previous
		}
		return nil
	}
	f.TaskID, f.IssueRef, f.PreviousIssueRef, f.IssueCreatedAt = task, issueRef, previous, at
	f.AutoPlanRequestedAt, f.AutoPlanHeld = time.Time{}, ""
	return nil
}

func (s *incidentFakeStore) ClearLadoLogFingerprintTask(_ context.Context, id domain.LadoLogFingerprintID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.byID(id)
	if f == nil {
		return domain.ErrLadoLogFingerprintNotFound
	}
	f.TaskID, f.IssueRef, f.AutoPlanRequestedAt, f.AutoPlanHeld = "", "", time.Time{}, ""
	return nil
}

func (s *incidentFakeStore) SetLadoLogAutoPlan(_ context.Context, id domain.LadoLogFingerprintID, at time.Time, held domain.LadoLogAutoPlanHold) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.byID(id)
	if f == nil {
		return domain.ErrLadoLogFingerprintNotFound
	}
	f.AutoPlanRequestedAt, f.AutoPlanHeld = at, held
	return nil
}

func (s *incidentFakeStore) CountLadoLogIssuesSince(_ context.Context, project domain.ProjectID, since time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, f := range s.fps {
		if f.ProjectID == project && !f.IssueCreatedAt.IsZero() && !f.IssueCreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (s *incidentFakeStore) CountLadoLogAutoPlansSince(_ context.Context, project domain.ProjectID, since time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, f := range s.fps {
		if f.ProjectID == project && !f.AutoPlanRequestedAt.IsZero() && !f.AutoPlanRequestedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (s *incidentFakeStore) SetLadoLogBurstNotified(_ context.Context, _ domain.ProjectID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.burstAt = at
	return nil
}

func (s *incidentFakeStore) LadoTask(_ context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	return task, ok, nil
}

func (s *incidentFakeStore) fingerprint(t *testing.T, fp string) domain.LadoLogFingerprint {
	t.Helper()
	f, ok, _ := s.LadoLogFingerprint(context.Background(), "demo", fp)
	if !ok {
		t.Fatalf("fingerprint %s was not recorded", fp)
	}
	return f
}

func (s *incidentFakeStore) setTask(task domain.LadoTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[task.ID] = task
}

func (s *incidentFakeStore) deleteTask(id domain.LadoTaskID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, id)
	// ON DELETE SET NULL.
	for _, f := range s.fps {
		if f.TaskID == id {
			f.TaskID = ""
		}
	}
}

// incidentFakeTasks creates tasks the way ladotask.CreateFromLog does.
type incidentFakeTasks struct {
	store   *incidentFakeStore
	mu      sync.Mutex
	inputs  []ladotask.LogCreateInput
	issues  int
	tasks   int
	failing error
	// unconfirmed makes the next creation end unconfirmed.
	unconfirmed bool
}

func (f *incidentFakeTasks) CreateFromLog(_ context.Context, in ladotask.LogCreateInput) (domain.LadoTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, in)
	if f.failing != nil {
		return domain.LadoTask{}, f.failing
	}
	f.tasks++
	task := domain.LadoTask{ID: domain.LadoTaskID(fmt.Sprintf("lado-task-%d", f.tasks)), ProjectID: in.ProjectID, Source: domain.LadoTaskSourceLog,
		Issue: domain.LadoObservedIssue{State: domain.IssueOpen}}
	if f.unconfirmed {
		f.unconfirmed = false
		f.store.setTask(task)
		return task, apierr.Unavailable("LADO_ISSUE_CREATE_UNCONFIRMED", "unconfirmed")
	}
	f.issues++
	task.IssueRef = domain.IssueID(fmt.Sprintf("github:acme/demo#%d", f.issues+40))
	f.store.setTask(task)
	return task, nil
}

func (f *incidentFakeTasks) created() []ladotask.LogCreateInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ladotask.LogCreateInput(nil), f.inputs...)
}

type incidentFakePlans struct {
	mu    sync.Mutex
	tasks []domain.LadoTaskID
	err   error
}

func (f *incidentFakePlans) RequestPlan(_ context.Context, in ladotask.PlanRequestInput) (domain.LadoPlanRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = append(f.tasks, in.TaskID)
	if f.err != nil {
		return domain.LadoPlanRequest{}, f.err
	}
	return domain.LadoPlanRequest{ID: "req", TaskID: in.TaskID}, nil
}

func (f *incidentFakePlans) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tasks)
}

type incidentFakeNotices struct {
	mu      sync.Mutex
	notices []domain.LadoOrchestratorNotice
}

func (f *incidentFakeNotices) AddLadoOrchestratorNotice(_ context.Context, n domain.LadoOrchestratorNotice) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, old := range f.notices {
		if old.Key == n.Key {
			return false, nil
		}
	}
	f.notices = append(f.notices, n)
	return true, nil
}

func (f *incidentFakeNotices) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.notices))
	for _, n := range f.notices {
		out = append(out, n.Text)
	}
	return out
}

type incidentFixture struct {
	h        *IncidentHandler
	store    *incidentFakeStore
	tasks    *incidentFakeTasks
	plans    *incidentFakePlans
	notices  *incidentFakeNotices
	now      time.Time
	settings domain.LadoLogWatchSettings
}

func newIncidentFixture(t *testing.T) *incidentFixture {
	t.Helper()
	fx := &incidentFixture{store: newIncidentFakeStore(), plans: &incidentFakePlans{}, notices: &incidentFakeNotices{}, now: incidentEpoch}
	fx.tasks = &incidentFakeTasks{store: fx.store}
	fx.settings = domain.DefaultLadoLogWatchSettings("demo")
	fx.settings.Enabled = true
	fx.settings.Sources = []domain.LadoLogSource{{ID: "src-1", Kind: domain.LadoLogSourceKindDockerTCP, DisplayName: "prod1", Endpoint: "tcp://198.51.100.7:2375"}}
	fx.h = NewIncidentHandler(HandlerDeps{
		Store: fx.store, Tasks: fx.tasks, Plans: fx.plans, Notices: fx.notices,
		Clock: func() time.Time { return fx.now },
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return fx
}

func incidentOccurrence(fp, instance string, at time.Time) Occurrence {
	return Occurrence{
		Service:          "grp/api",
		Instance:         instance,
		Image:            "registry.example.test/grp/api:1.4.2",
		ServiceInstances: 2,
		Incident: Incident{
			ExceptionKind: "java.lang.IllegalStateException",
			Location:      "com.example.OrderService.place(OrderService.java:42)",
			Fingerprint:   fp,
			Lines:         []string{"java.lang.IllegalStateException: no stock at 198.51.100.7", "\tat com.example.OrderService.place(OrderService.java:42)"},
			At:            at,
			Stream:        "stderr",
		},
	}
}

// handle hands an occurrence over at the fixture's time.
func (fx *incidentFixture) handle(fp, instance string) {
	fx.h.Handle(context.Background(), "demo", fx.settings, incidentOccurrence(fp, instance, fx.now))
}

// advance moves the clock and flushes.
func (fx *incidentFixture) advance(d time.Duration) {
	fx.now = fx.now.Add(d)
	fx.h.Flush(context.Background(), fx.now)
}

// newIssue makes fp's issue: one occurrence and the 60-second window.
func (fx *incidentFixture) newIssue(fp string) {
	fx.handle(fp, "prod1/api-1")
	fx.advance(incidentWindow)
}

func TestIncidentReplicasInTheWindowMakeOneIssue(t *testing.T) {
	fx := newIncidentFixture(t)
	fx.handle("fp-a", "prod1/grp-api-1")
	if f := fx.store.fingerprint(t, "fp-a"); f.Count != 1 {
		t.Fatalf("count = %d, want the first occurrence written at once", f.Count)
	}
	fx.now = fx.now.Add(20 * time.Second)
	fx.h.Handle(context.Background(), "demo", fx.settings, incidentOccurrence("fp-a", "prod2/grp-api-1", fx.now))
	fx.advance(30 * time.Second)
	if len(fx.tasks.created()) != 0 {
		t.Fatal("an issue was created inside the window")
	}
	fx.advance(10 * time.Second)

	created := fx.tasks.created()
	if len(created) != 1 {
		t.Fatalf("issues = %d, want one for both replicas", len(created))
	}
	body := created[0].Body
	for _, want := range []string{"2개 중 2개", "prod1/grp-api-1", "prod2/grp-api-1", IssueExcerptHeading, IssuePlanWorkerNote, "grp/api:1.4.2"} {
		if !strings.Contains(body, want) {
			t.Errorf("issue body lacks %q:\n%s", want, body)
		}
	}
	for _, leak := range []string{"198.51.100.7", "registry.example.test"} {
		if strings.Contains(body, leak) || strings.Contains(created[0].Title, leak) {
			t.Errorf("issue leaks %q", leak)
		}
	}
	if !strings.Contains(created[0].Title, "java.lang.IllegalStateException") || created[0].ProjectID != "demo" {
		t.Fatalf("issue = %+v", created[0])
	}
	f := fx.store.fingerprint(t, "fp-a")
	if f.TaskID != "lado-task-1" || f.IssueRef != "github:acme/demo#41" || f.Count != 2 {
		t.Fatalf("fingerprint = %+v, want the task and issue linked and two occurrences", f)
	}
	if fx.plans.count() != 1 {
		t.Fatalf("plan requests = %d, want 1", fx.plans.count())
	}
	if got := fx.notices.texts(); len(got) != 1 || got[0] != "LADO 알림: 서버 오류 감지 — #41 (java.lang.IllegalStateException, 2회, 계획 요청됨)" {
		t.Fatalf("notices = %q", got)
	}
	if fx.notices.notices[0].Key != fmt.Sprintf("log:%d:github:acme/demo#41", f.ID) {
		t.Fatalf("notice key = %q", fx.notices.notices[0].Key)
	}
}

func TestIncidentOpenIssueOnlyRaisesTheCounts(t *testing.T) {
	fx := newIncidentFixture(t)
	fx.newIssue("fp-a")
	writes := fx.store.occWrites
	for range 5 {
		fx.handle("fp-a", "prod1/api-1")
		fx.now = fx.now.Add(time.Second)
	}
	if fx.store.occWrites != writes {
		t.Fatal("counts were written before the save interval")
	}
	fx.advance(incidentSaveInterval)
	f := fx.store.fingerprint(t, "fp-a")
	if f.Count != 6 || fx.store.occWrites != writes+1 {
		t.Fatalf("count = %d, writes = %d; want 6 in one write", f.Count, fx.store.occWrites-writes)
	}
	fx.advance(incidentWindow * 2)
	if n := len(fx.tasks.created()); n != 1 {
		t.Fatalf("issues = %d, want no new issue for an open one", n)
	}
	if fx.plans.count() != 1 || len(fx.notices.texts()) != 1 {
		t.Fatalf("plans = %d, notices = %d; want nothing more", fx.plans.count(), len(fx.notices.texts()))
	}
}

func TestIncidentClosedIssueRecursWithALink(t *testing.T) {
	for name, close := range map[string]func(*domain.LadoTask){
		"issue closed":   func(task *domain.LadoTask) { task.Issue.State = domain.IssueDone },
		"task cancelled": func(task *domain.LadoTask) { task.CancelledAt = incidentEpoch },
	} {
		t.Run(name, func(t *testing.T) {
			fx := newIncidentFixture(t)
			fx.newIssue("fp-a")
			task, _, _ := fx.store.LadoTask(context.Background(), "lado-task-1")
			close(&task)
			fx.store.setTask(task)

			fx.handle("fp-a", "prod1/api-1")
			fx.advance(incidentSaveInterval)
			fx.advance(incidentWindow)
			created := fx.tasks.created()
			if len(created) != 2 {
				t.Fatalf("issues = %d, want a new one", len(created))
			}
			if !strings.Contains(created[1].Body, "#41 (닫힌 뒤 다시 발생)") {
				t.Fatalf("new issue does not link the old one:\n%s", created[1].Body)
			}
			f := fx.store.fingerprint(t, "fp-a")
			if f.TaskID != "lado-task-2" || f.IssueRef != "github:acme/demo#42" || f.PreviousIssueRef != "github:acme/demo#41" {
				t.Fatalf("fingerprint = %+v", f)
			}
			texts := fx.notices.texts()
			if len(texts) != 2 || texts[1] != "LADO 알림: 서버 오류 감지 — #42 (java.lang.IllegalStateException, 이전 #41, 계획 요청됨)" {
				t.Fatalf("notices = %q", texts)
			}
		})
	}
}

func TestIncidentHourlyCapHoldsIssuesAndTellsOnce(t *testing.T) {
	fx := newIncidentFixture(t)
	for i := range domain.LadoLogDefaultHourlyIssueCap {
		fx.newIssue(fmt.Sprintf("fp-%d", i))
	}
	if n := len(fx.tasks.created()); n != 5 {
		t.Fatalf("issues = %d, want 5", n)
	}
	fx.newIssue("fp-over-1")
	fx.newIssue("fp-over-2")
	if n := len(fx.tasks.created()); n != 5 {
		t.Fatalf("issues = %d, want none over the cap", n)
	}
	if f := fx.store.fingerprint(t, "fp-over-1"); f.TaskID != "" || f.Count != 1 {
		t.Fatalf("held fingerprint = %+v, want it recorded without a task", f)
	}
	burst := 0
	for _, text := range fx.notices.texts() {
		if strings.Contains(text, "오류 폭주") {
			burst++
		}
	}
	if burst != 1 || fx.store.burstAt.IsZero() {
		t.Fatalf("burst notices = %d (stored %v), want exactly one", burst, fx.store.burstAt)
	}

	// Once the hour passed, the held fingerprint gets its issue when it
	// shows again.
	fx.advance(time.Hour)
	fx.handle("fp-over-1", "prod1/api-1")
	fx.advance(incidentSaveInterval)
	fx.advance(incidentWindow)
	if n := len(fx.tasks.created()); n != 6 {
		t.Fatalf("issues = %d, want the held fingerprint's issue after the cap lifted", n)
	}
}

func TestIncidentBurstNoticeOncePerHour(t *testing.T) {
	fx := newIncidentFixture(t)
	fx.settings.HourlyIssueCap = 1
	fx.newIssue("fp-0")
	fx.newIssue("fp-1")
	fx.advance(30 * time.Minute)
	fx.newIssue("fp-2")
	count := func() int {
		n := 0
		for _, text := range fx.notices.texts() {
			if strings.Contains(text, "오류 폭주") {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("burst notices = %d, want one within the hour", count())
	}
	// fp-0's issue is out of the last hour, but the cap still holds: fp-1 and
	// fp-2 are not counted (they have no issue), so fp-3 gets one.
	fx.advance(31 * time.Minute)
	fx.newIssue("fp-3")
	if n := len(fx.tasks.created()); n != 2 {
		t.Fatalf("issues = %d, want 2", n)
	}
	fx.newIssue("fp-4")
	if count() != 2 {
		t.Fatalf("burst notices = %d, want a second one after an hour", count())
	}
}

func TestIncidentIgnoredFingerprintMakesNoIssue(t *testing.T) {
	fx := newIncidentFixture(t)
	fx.handle("fp-a", "prod1/api-1")
	f := fx.store.fingerprint(t, "fp-a")
	fx.store.mu.Lock()
	fx.store.fps["demo/fp-a"].Ignored = true
	fx.store.mu.Unlock()
	fx.advance(incidentWindow)
	fx.handle("fp-a", "prod1/api-1")
	fx.advance(incidentSaveInterval)
	fx.advance(incidentWindow)
	if n := len(fx.tasks.created()); n != 0 {
		t.Fatalf("issues = %d, want none for an ignored fingerprint", n)
	}
	if got := fx.store.fingerprint(t, "fp-a"); got.Count != f.Count+1 {
		t.Fatalf("count = %d, want the ignored fingerprint still counted", got.Count)
	}

	// A fingerprint ignored before LADO first saw it this run makes none
	// either.
	fx2 := newIncidentFixture(t)
	fx2.handle("fp-b", "prod1/api-1")
	fx2.store.mu.Lock()
	fx2.store.fps["demo/fp-b"].Ignored = true
	fx2.store.mu.Unlock()
	fx2.h = NewIncidentHandler(fx2.h.deps)
	fx2.handle("fp-b", "prod1/api-1")
	fx2.advance(incidentWindow)
	if n := len(fx2.tasks.created()); n != 0 {
		t.Fatalf("issues = %d, want none", n)
	}
}

func TestIncidentUnconfirmedCreationStartsOverWhenItsTaskIsGone(t *testing.T) {
	fx := newIncidentFixture(t)
	fx.tasks.unconfirmed = true
	fx.newIssue("fp-a")
	f := fx.store.fingerprint(t, "fp-a")
	if f.TaskID != "lado-task-1" || f.IssueRef != "" {
		t.Fatalf("fingerprint = %+v, want the pending task id kept", f)
	}
	if len(fx.notices.texts()) != 0 || fx.plans.count() != 0 {
		t.Fatal("an unconfirmed creation was told or planned")
	}
	// While the task waits, nothing new is created.
	fx.handle("fp-a", "prod1/api-1")
	fx.advance(incidentSaveInterval)
	fx.advance(incidentWindow)
	if n := len(fx.tasks.created()); n != 1 {
		t.Fatalf("issues = %d, want the pending one only", n)
	}

	// The tracking loop found no issue and deleted the task: task_id NULL.
	fx.store.deleteTask("lado-task-1")
	fx.handle("fp-a", "prod1/api-1")
	fx.advance(incidentSaveInterval)
	fx.advance(incidentWindow)
	if n := len(fx.tasks.created()); n != 2 {
		t.Fatalf("issues = %d, want a new one after the task was deleted", n)
	}
	if f := fx.store.fingerprint(t, "fp-a"); f.TaskID != "lado-task-2" || f.IssueRef != "github:acme/demo#41" {
		t.Fatalf("fingerprint = %+v, want the new task linked", f)
	}
}

func TestIncidentUnconfirmedCreationLinksTheIssueFoundLater(t *testing.T) {
	fx := newIncidentFixture(t)
	fx.tasks.unconfirmed = true
	fx.newIssue("fp-a")
	// The tracking loop found the issue by its marker.
	task, _, _ := fx.store.LadoTask(context.Background(), "lado-task-1")
	task.IssueRef = "github:acme/demo#7"
	fx.store.setTask(task)

	fx.handle("fp-a", "prod1/api-1")
	fx.advance(incidentSaveInterval)
	if f := fx.store.fingerprint(t, "fp-a"); f.TaskID != "lado-task-1" || f.IssueRef != "github:acme/demo#7" {
		t.Fatalf("fingerprint = %+v, want the found issue linked", f)
	}
	if n := len(fx.tasks.created()); n != 1 {
		t.Fatalf("issues = %d, want no new one", n)
	}
	if texts := fx.notices.texts(); len(texts) != 1 || !strings.Contains(texts[0], "#7 (") || fx.plans.count() != 1 {
		t.Fatalf("notices = %q, plans = %d; want the issue told and planned once", texts, fx.plans.count())
	}
}

func TestIncidentAutoPlan(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		fx := newIncidentFixture(t)
		fx.settings.AutoPlan = false
		fx.newIssue("fp-a")
		if fx.plans.count() != 0 {
			t.Fatal("a plan was requested with automatic plans off")
		}
		if f := fx.store.fingerprint(t, "fp-a"); f.AutoPlanHeld != domain.LadoLogAutoPlanHeldOff {
			t.Fatalf("held = %q", f.AutoPlanHeld)
		}
		if texts := fx.notices.texts(); len(texts) != 1 || !strings.HasSuffix(texts[0], "1회, 자동 계획 꺼짐)") {
			t.Fatalf("notices = %q", texts)
		}
	})
	t.Run("daily cap", func(t *testing.T) {
		fx := newIncidentFixture(t)
		fx.settings.AutoPlanDailyCap = 2
		for i := range 3 {
			fx.newIssue(fmt.Sprintf("fp-%d", i))
		}
		if fx.plans.count() != 2 {
			t.Fatalf("plan requests = %d, want the cap of 2", fx.plans.count())
		}
		texts := fx.notices.texts()
		if len(texts) != 3 || !strings.HasSuffix(texts[2], "보류(상한))") {
			t.Fatalf("notices = %q, want the third held", texts)
		}
		if f := fx.store.fingerprint(t, "fp-2"); f.AutoPlanHeld != domain.LadoLogAutoPlanHeldCap {
			t.Fatalf("held = %q", f.AutoPlanHeld)
		}
		// A day later the cap counts afresh, but a held fingerprint is not
		// asked again.
		fx.advance(25 * time.Hour)
		fx.handle("fp-2", "prod1/api-1")
		fx.advance(incidentSaveInterval)
		if fx.plans.count() != 2 {
			t.Fatalf("plan requests = %d, want no late request for a capped one", fx.plans.count())
		}
	})
	t.Run("minimum count reached late", func(t *testing.T) {
		fx := newIncidentFixture(t)
		fx.settings.AutoPlanMinCount = 3
		fx.newIssue("fp-a")
		if fx.plans.count() != 0 {
			t.Fatal("a plan was requested below the minimum count")
		}
		if texts := fx.notices.texts(); len(texts) != 1 || !strings.HasSuffix(texts[0], "1회, 보류(3회 미만))") {
			t.Fatalf("notices = %q", texts)
		}
		fx.handle("fp-a", "prod1/api-1")
		fx.advance(incidentSaveInterval)
		if fx.plans.count() != 0 {
			t.Fatal("a plan was requested at 2 of 3")
		}
		fx.handle("fp-a", "prod1/api-1")
		fx.advance(incidentSaveInterval)
		if fx.plans.count() != 1 {
			t.Fatalf("plan requests = %d, want one at the minimum", fx.plans.count())
		}
		f := fx.store.fingerprint(t, "fp-a")
		if f.AutoPlanRequestedAt.IsZero() || f.AutoPlanHeld != "" {
			t.Fatalf("fingerprint = %+v, want the request recorded", f)
		}
		fx.handle("fp-a", "prod1/api-1")
		fx.advance(incidentSaveInterval)
		if fx.plans.count() != 1 {
			t.Fatal("the plan was requested twice")
		}
	})
	t.Run("request fails", func(t *testing.T) {
		fx := newIncidentFixture(t)
		fx.plans.err = apierr.Unavailable("LADO_PLAN_WORKERS_UNAVAILABLE", "no plan workers")
		fx.newIssue("fp-a")
		if f := fx.store.fingerprint(t, "fp-a"); f.AutoPlanHeld != domain.LadoLogAutoPlanHeldFailed || !f.AutoPlanRequestedAt.IsZero() {
			t.Fatalf("fingerprint = %+v", f)
		}
		if texts := fx.notices.texts(); len(texts) != 1 || !strings.Contains(texts[0], "보류") {
			t.Fatalf("notices = %q", texts)
		}
	})
}

func TestIncidentEveryNewFingerprintMakesALogTask(t *testing.T) {
	fx := newIncidentFixture(t)
	fx.settings.Repository = "github:acme/demo"
	fx.newIssue("fp-a")
	fx.newIssue("fp-b")
	created := fx.tasks.created()
	if len(created) != 2 || created[0].Repository != "github:acme/demo" {
		t.Fatalf("created = %+v", created)
	}
	for _, id := range []domain.LadoTaskID{"lado-task-1", "lado-task-2"} {
		task, ok, _ := fx.store.LadoTask(context.Background(), id)
		if !ok || task.Source != domain.LadoTaskSourceLog {
			t.Fatalf("task %s = %+v, want a log task", id, task)
		}
	}
	if fx.plans.count() != 2 {
		t.Fatalf("plan requests = %d, want one per task", fx.plans.count())
	}
}

func TestIncidentTrackerFailureWaitsBeforeTryingAgain(t *testing.T) {
	fx := newIncidentFixture(t)
	fx.tasks.failing = errors.New("tracker down")
	fx.newIssue("fp-a")
	if n := len(fx.tasks.created()); n != 1 {
		t.Fatalf("attempts = %d", n)
	}
	fx.handle("fp-a", "prod1/api-1")
	fx.advance(incidentSaveInterval)
	fx.advance(incidentWindow)
	if n := len(fx.tasks.created()); n != 1 {
		t.Fatalf("attempts = %d, want none within the retry delay", n)
	}
	fx.tasks.failing = nil
	fx.advance(incidentRetryDelay)
	fx.handle("fp-a", "prod1/api-1")
	fx.advance(incidentSaveInterval)
	fx.advance(incidentWindow)
	if n := len(fx.tasks.created()); n != 2 || fx.store.fingerprint(t, "fp-a").IssueRef == "" {
		t.Fatalf("attempts = %d, want the issue after the delay", n)
	}
}

func TestIncidentProjectsAreSerializedAndRace(t *testing.T) {
	fx := newIncidentFixture(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 20 {
				fx.h.Handle(context.Background(), "demo", fx.settings, incidentOccurrence(fmt.Sprintf("fp-%d", j%3), fmt.Sprintf("prod1/api-%d", i), incidentEpoch))
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 20 {
			fx.h.Flush(context.Background(), incidentEpoch.Add(incidentSaveInterval))
		}
	}()
	wg.Wait()
	fx.h.Flush(context.Background(), incidentEpoch.Add(time.Hour))
	total := 0
	for j := range 3 {
		total += fx.store.fingerprint(t, fmt.Sprintf("fp-%d", j)).Count
	}
	if total != 160 {
		t.Fatalf("counted %d occurrences, want 160", total)
	}
}
