package ladousage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

var (
	t0      = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	project = domain.ProjectID("proj")
	taskID  = domain.LadoTaskID("task-1")
)

// seedPartsTask is a task whose plan took two designer attempts and two
// critics, one of which never wrote a critique, and whose parts run retried a
// part, merged it and integrated. The first worker of the part was deleted.
func seedPartsTask(f *fakeStore) fakeUsage {
	f.projects[string(project)] = true
	f.tasks[taskID] = domain.LadoTask{ID: taskID, ProjectID: project, IssueRef: "github:o/r#7", CreatedAt: t0}
	f.facts[taskID] = domain.LadoTaskTimingFacts{
		TaskID: taskID, ProjectID: project, IssueRef: "github:o/r#7", Title: "Task", CreatedAt: t0,
		PlanRequestedAt: []time.Time{t0.Add(time.Minute)},
		// #34 knows the request's last designer and the critic that wrote.
		PlanSessions: []domain.LadoTimingSession{
			{SessionID: "design-2", Role: domain.LadoSessionDesigner},
			{SessionID: "critic-1", Role: domain.LadoSessionCritic},
		},
		Approvals: []domain.LadoTimingApproval{{ID: "ap1", At: t0.Add(time.Hour)}},
		Executions: []domain.LadoTimingExecution{
			{SessionID: "part-a-1", Role: domain.LadoRolePart, PartID: "a", ApprovalID: "ap1", QueueEntryID: "q1", StartedAt: t0.Add(2 * time.Hour)},
			{SessionID: "part-a-2", Role: domain.LadoRolePart, PartID: "a", ApprovalID: "ap1", QueueEntryID: "q1", StartedAt: t0.Add(3 * time.Hour)},
			{SessionID: "merge-a", Role: domain.LadoRoleMerge, PartID: "a", ApprovalID: "ap1", QueueEntryID: "q1", StartedAt: t0.Add(4 * time.Hour)},
			{SessionID: "integ-1", Role: domain.LadoRoleIntegration, ApprovalID: "ap1", QueueEntryID: "q1", StartedAt: t0.Add(5 * time.Hour)},
		},
	}
	f.planWorkers[taskID] = []domain.LadoPlanWorkerSession{
		{SessionID: "design-1", RequestID: "r1", Role: domain.LadoPlanRoleDesigner, Attempt: 1, CreatedAt: t0.Add(2 * time.Minute)},
		{SessionID: "design-2", RequestID: "r1", Role: domain.LadoPlanRoleDesigner, Attempt: 2, CreatedAt: t0.Add(10 * time.Minute)},
		{SessionID: "critic-2", RequestID: "r1", Role: domain.LadoPlanRoleCritic, Attempt: 1, CreatedAt: t0.Add(25 * time.Minute)},
		{SessionID: "critic-1", RequestID: "r1", Role: domain.LadoPlanRoleCritic, Attempt: 1, CreatedAt: t0.Add(20 * time.Minute)},
	}
	for id, req := range map[domain.SessionID]domain.LadoRunRequest{
		"part-a-2": {Entry: "q1", Role: domain.LadoRolePart, Part: "a", Attempt: 2},
		"merge-a":  {Entry: "q1", Role: domain.LadoRoleMerge, Part: "a", Attempt: 1},
		"integ-1":  {Entry: "q1", Role: domain.LadoRoleIntegration, Attempt: 1},
	} {
		f.sessions[id] = domain.SessionRecord{ID: id, ClientRequestID: domain.LadoRunClientRequestID(req)}
	}
	f.conversations["integ-1"] = domain.ConversationRecord{Usage: &domain.ConversationUsage{ContextUsed: 120_000, ContextWindow: 200_000}}
	f.subagents["integ-1"] = domain.LadoSubagentUsage{SessionID: "integ-1", CostNanos: 4_000_000_000, InputTokens: 900, OutputTokens: 90}
	f.watches["integ-1"] = domain.LadoSessionWatch{SessionID: "integ-1", TaskID: taskID, LadoWatchCompactions: domain.LadoWatchCompactions{Compactions: 2}}
	f.watches["part-a-2"] = domain.LadoSessionWatch{SessionID: "part-a-2", TaskID: taskID, LadoWatchCompactions: domain.LadoWatchCompactions{Compactions: 1}}
	return fakeUsage{
		"design-1": usageOf(1_000_000_000, 100, 10, 1),
		"design-2": usageOf(2_000_000_000, 200, 20, 2),
		"critic-1": usageOf(3_000_000_000, 300, 30, 3),
		"critic-2": usageOf(4_000_000_000, 400, 40, 4),
		"part-a-2": usageOf(5_000_000_000, 500, 50, 5),
		"merge-a":  usageOf(6_000_000_000, 600, 60, 6),
		"integ-1":  usageOf(7_000_000_000, 700, 70, 7),
	}
}

func newService(f *fakeStore, u fakeUsage, now time.Time) *Service {
	return New(Deps{Store: f, Usage: u, Clock: func() time.Time { return now }, Log: quietLog()})
}

func TestTaskUsageAddsUpEverySessionOfTheTask(t *testing.T) {
	f := newFakeStore()
	u := seedPartsTask(f)
	svc := newService(f, u, t0.Add(6*time.Hour))
	got, err := svc.TaskUsage(context.Background(), taskID)
	if err != nil {
		t.Fatalf("TaskUsage: %v", err)
	}

	type row struct {
		id      domain.SessionID
		role    domain.LadoSessionRole
		part    string
		attempt int
	}
	want := []row{
		{"design-2", domain.LadoSessionDesigner, "", 2},
		{"critic-1", domain.LadoSessionCritic, "", 1},
		{"design-1", domain.LadoSessionDesigner, "", 1},
		{"critic-2", domain.LadoSessionCritic, "", 1},
		{"part-a-1", domain.LadoSessionPart, "a", 0},
		{"part-a-2", domain.LadoSessionPart, "a", 2},
		{"merge-a", domain.LadoSessionMerge, "a", 1},
		{"integ-1", domain.LadoSessionIntegration, "", 1},
	}
	if len(got.Sessions) != len(want) {
		t.Fatalf("sessions = %+v, want %d", got.Sessions, len(want))
	}
	var sum Totals
	compactions := 0
	for i, w := range want {
		s := got.Sessions[i]
		if s.SessionID != w.id || s.Role != w.role || s.PartID != w.part || s.Attempt != w.attempt {
			t.Errorf("session %d = %s %s %q #%d, want %+v", i, s.SessionID, s.Role, s.PartID, s.Attempt, w)
		}
		sum.add(s.Totals)
		compactions += s.Compactions
	}
	if got.Totals != sum {
		t.Errorf("totals = %+v, want the sessions' sum %+v", got.Totals, sum)
	}
	if got.CostNanos != 28_000_000_000 || got.InputTokens != 2800 || got.CachedInputTokens != 280 || got.OutputTokens != 28 {
		t.Errorf("totals = %+v, want every session but the deleted one", got.Totals)
	}
	if got.SubagentCostNanos != 4_000_000_000 || got.SubagentInputTokens != 900 || got.SubagentOutputTokens != 90 {
		t.Errorf("subagent share = %+v", got.Totals)
	}
	if got.Compactions != 3 || compactions != 3 {
		t.Errorf("compactions = %d (sessions %d), want 3", got.Compactions, compactions)
	}

	deleted := got.Sessions[4]
	if !deleted.UsageMissing || deleted.CostNanos != 0 || got.UsageMissing != 1 {
		t.Errorf("deleted session = %+v, task missing %d; want usageMissing and no cost", deleted, got.UsageMissing)
	}
	integ := got.Sessions[7]
	if integ.ContextUsed != 120_000 || integ.ContextWindow != 200_000 || integ.Compactions != 2 {
		t.Errorf("integration session = %+v", integ)
	}
	if !got.InProgress() || got.Elapsed != 6*time.Hour {
		t.Errorf("elapsed = %v (%q), want 6h in progress", got.Elapsed, got.EndReason)
	}
}

// timingsStore answers what #34's TaskTimings reads; every session has one
// activity so the timings list it.
type timingsStore struct {
	ladotask.Store
	facts domain.LadoTaskTimingFacts
}

func (s timingsStore) LadoTaskTimingFacts(context.Context, domain.LadoTaskID) (domain.LadoTaskTimingFacts, bool, error) {
	return s.facts, true, nil
}

func (s timingsStore) ListLadoSessionMetrics(context.Context, domain.LadoTaskID) ([]domain.LadoSessionMetrics, error) {
	return nil, nil
}

func (s timingsStore) LadoSessionActivityFacts(context.Context, domain.SessionID) ([]domain.LadoActivityFact, error) {
	return []domain.LadoActivityFact{{Kind: domain.ActivityKind("message"), CreatedAt: t0}}, nil
}

func TestTaskUsageRolesMatchTheTaskTimings(t *testing.T) {
	f := newFakeStore()
	u := seedPartsTask(f)
	got, err := newService(f, u, t0.Add(6*time.Hour)).TaskUsage(context.Background(), taskID)
	if err != nil {
		t.Fatalf("TaskUsage: %v", err)
	}
	timings, err := ladotask.New(ladotask.Deps{Store: timingsStore{facts: f.facts[taskID]}, Log: quietLog()}).
		TaskTimings(context.Background(), taskID)
	if err != nil {
		t.Fatalf("TaskTimings: %v", err)
	}
	roles := map[domain.SessionID]domain.LadoSessionRole{}
	for _, s := range got.Sessions {
		roles[s.SessionID] = s.Role
	}
	if len(timings.Sessions) == 0 {
		t.Fatal("the timings list no sessions")
	}
	for i, s := range timings.Sessions {
		if roles[s.SessionID] != s.Role {
			t.Errorf("session %s: usage role %q, timings role %q", s.SessionID, roles[s.SessionID], s.Role)
		}
		// The timings' order is kept; the usage adds the earlier plan
		// attempts after the plan sessions.
		if j := indexOf(got.Sessions, s.SessionID); i < 2 && j != i {
			t.Errorf("plan session %s at %d, timings at %d", s.SessionID, j, i)
		}
	}
}

func indexOf(sessions []SessionUsage, id domain.SessionID) int {
	for i, s := range sessions {
		if s.SessionID == id {
			return i
		}
	}
	return -1
}

func TestTaskUsageElapsedEnds(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	cases := []struct {
		name   string
		edit   func(*domain.LadoTask, *domain.LadoTaskTimingFacts)
		want   time.Duration
		reason EndReason
	}{
		{
			name: "merged ends at the timings' total",
			edit: func(task *domain.LadoTask, facts *domain.LadoTaskTimingFacts) {
				facts.Executions[3].SettledState, facts.Executions[3].SettledAt = string(domain.PRStateMerged), t0.Add(7*time.Hour)
				// A later cancellation does not move a merged task's end.
				task.CancelledAt = t0.Add(9 * time.Hour)
			},
			want: 7 * time.Hour, reason: EndMerged,
		},
		{
			name: "cancelled ends at the cancellation",
			edit: func(task *domain.LadoTask, facts *domain.LadoTaskTimingFacts) {
				task.CancelledAt = t0.Add(8 * time.Hour)
				facts.Executions[3].SettledState, facts.Executions[3].SettledAt = string(domain.PRStateClosed), t0.Add(6*time.Hour)
			},
			want: 8 * time.Hour, reason: EndCancelled,
		},
		{
			name: "a closed pull request ends at its settle time",
			edit: func(_ *domain.LadoTask, facts *domain.LadoTaskTimingFacts) {
				facts.Executions[3].SettledState, facts.Executions[3].SettledAt = string(domain.PRStateClosed), t0.Add(6*time.Hour)
			},
			want: 6 * time.Hour, reason: EndClosed,
		},
		{
			name: "a closed part's pull request does not end the task",
			edit: func(_ *domain.LadoTask, facts *domain.LadoTaskTimingFacts) {
				facts.Executions[0].SettledState, facts.Executions[0].SettledAt = string(domain.PRStateClosed), t0.Add(3*time.Hour)
			},
			want: 48 * time.Hour, reason: EndInProgress,
		},
		{
			name: "in progress runs to now",
			edit: func(*domain.LadoTask, *domain.LadoTaskTimingFacts) {},
			want: 48 * time.Hour, reason: EndInProgress,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeStore()
			u := seedPartsTask(f)
			task, facts := f.tasks[taskID], f.facts[taskID]
			facts.Executions = append([]domain.LadoTimingExecution(nil), facts.Executions...)
			tc.edit(&task, &facts)
			f.tasks[taskID], f.facts[taskID] = task, facts
			got, err := newService(f, u, now).TaskUsage(context.Background(), taskID)
			if err != nil {
				t.Fatalf("TaskUsage: %v", err)
			}
			if got.Elapsed != tc.want || got.EndReason != tc.reason || !got.StartedAt.Equal(t0) || !got.EndedAt.Equal(t0.Add(tc.want)) {
				t.Errorf("elapsed = %v %q (%v..%v), want %v %q", got.Elapsed, got.EndReason, got.StartedAt, got.EndedAt, tc.want, tc.reason)
			}
			if tc.reason == EndMerged {
				if total := domain.LadoTaskTimelineOf(facts).Total; got.Elapsed != total {
					t.Errorf("merged elapsed %v, timings total %v", got.Elapsed, total)
				}
			}
		})
	}
}

func TestTaskUsageUnknownTask(t *testing.T) {
	f := newFakeStore()
	_, err := newService(f, fakeUsage{}, t0).TaskUsage(context.Background(), "nope")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindNotFound {
		t.Fatalf("err = %v, want not found", err)
	}
}

func TestTaskUsageFailsOnAUsageReadError(t *testing.T) {
	f := newFakeStore()
	seedPartsTask(f)
	svc := New(Deps{Store: f, Usage: failingUsage{}, Log: quietLog()})
	_, err := svc.TaskUsage(context.Background(), taskID)
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInternal {
		t.Fatalf("err = %v, want an internal error", err)
	}
}

type failingUsage struct{}

func (failingUsage) Get(context.Context, domain.SessionID) (domain.SessionUsageSummary, error) {
	return domain.SessionUsageSummary{}, errors.New("disk on fire")
}

func TestProjectUsageListsTaskTotals(t *testing.T) {
	f := newFakeStore()
	u := seedPartsTask(f)
	f.tasks["task-0"] = domain.LadoTask{ID: "task-0", ProjectID: project, IssueRef: "github:o/r#6", CreatedAt: t0.Add(-time.Hour)}
	f.facts["task-0"] = domain.LadoTaskTimingFacts{TaskID: "task-0", ProjectID: project, IssueRef: "github:o/r#6", CreatedAt: t0.Add(-time.Hour)}
	f.tasks["gone"] = domain.LadoTask{ID: "gone", ProjectID: project, IssueRef: "github:o/r#5", CreatedAt: t0, CancelledAt: t0.Add(time.Hour)}
	f.tasks["other"] = domain.LadoTask{ID: "other", ProjectID: "elsewhere", IssueRef: "github:o/r#4", CreatedAt: t0}

	got, err := newService(f, u, t0.Add(time.Hour)).ProjectUsage(context.Background(), project)
	if err != nil {
		t.Fatalf("ProjectUsage: %v", err)
	}
	if len(got.Tasks) != 2 || got.Tasks[0].TaskID != "task-0" || got.Tasks[1].TaskID != taskID {
		t.Fatalf("tasks = %+v, want task-0 then task-1", got.Tasks)
	}
	if got.Tasks[1].CostNanos != 28_000_000_000 || got.Tasks[1].Sessions != nil || got.Tasks[1].UsageMissing != 1 {
		t.Errorf("task-1 = %+v", got.Tasks[1])
	}
	if got.Tasks[0].CostNanos != 0 || got.Tasks[0].Elapsed != 2*time.Hour {
		t.Errorf("task-0 = %+v", got.Tasks[0])
	}

	_, err = newService(f, u, t0).ProjectUsage(context.Background(), "missing")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindNotFound {
		t.Fatalf("unknown project err = %v, want not found", err)
	}
}

// A task approved again runs its parts under a new queue entry with the same
// part ids; each session carries its own execution's queue entry so the
// parts view adds up only the run it shows (#69). Plan sessions carry none.
func TestTaskUsageSessionsCarryTheirExecutionsQueueEntry(t *testing.T) {
	f := newFakeStore()
	u := seedPartsTask(f)
	facts := f.facts[taskID]
	facts.Approvals = append(facts.Approvals, domain.LadoTimingApproval{ID: "ap2", At: t0.Add(6 * time.Hour)})
	facts.Executions = append(facts.Executions,
		domain.LadoTimingExecution{SessionID: "part-a-3", Role: domain.LadoRolePart, PartID: "a", ApprovalID: "ap2", QueueEntryID: "q2", StartedAt: t0.Add(7 * time.Hour)},
		domain.LadoTimingExecution{SessionID: "part-a-4", Role: domain.LadoRolePart, PartID: "a", ApprovalID: "ap2", QueueEntryID: "q2", StartedAt: t0.Add(8 * time.Hour)},
	)
	f.facts[taskID] = facts
	u["part-a-3"] = usageOf(8_000_000_000, 800, 80, 8)
	u["part-a-4"] = usageOf(9_000_000_000, 900, 90, 9)

	got, err := newService(f, u, t0.Add(9*time.Hour)).TaskUsage(context.Background(), taskID)
	if err != nil {
		t.Fatalf("TaskUsage: %v", err)
	}
	want := map[domain.SessionID]domain.LadoQueueEntryID{
		"design-2": "", "critic-1": "", "design-1": "", "critic-2": "",
		"part-a-1": "q1", "part-a-2": "q1", "merge-a": "q1", "integ-1": "q1",
		"part-a-3": "q2", "part-a-4": "q2",
	}
	if len(got.Sessions) != len(want) {
		t.Fatalf("sessions = %+v, want %d", got.Sessions, len(want))
	}
	var q2Part int64
	for _, s := range got.Sessions {
		if q, ok := want[s.SessionID]; !ok || s.QueueEntryID != q {
			t.Errorf("session %s queue entry = %q, want %q", s.SessionID, s.QueueEntryID, q)
		}
		if s.Role == domain.LadoSessionPart && s.PartID == "a" && s.QueueEntryID == "q2" {
			q2Part += s.CostNanos
		}
	}
	if q2Part != 17_000_000_000 {
		t.Errorf("part a of q2 cost %d, want only q2's two sessions", q2Part)
	}
}
