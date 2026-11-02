package domain_test

import (
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLadoTaskTimelineOf(t *testing.T) {
	base := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }
	planned := domain.LadoTaskTimingFacts{
		CreatedAt:         at(0),
		PlanRequestedAt:   []time.Time{at(2)},
		RevisionCreatedAt: []time.Time{at(12)},
		Approvals:         []domain.LadoTimingApproval{{ID: "a1", At: at(20)}},
	}
	single := planned
	single.QueueEntries = []domain.LadoTimingQueueEntry{{ID: "q1", ApprovalID: "a1"}}
	single.Executions = []domain.LadoTimingExecution{{
		SessionID: "w1", ApprovalID: "a1", QueueEntryID: "q1", StartedAt: at(21),
		PRSeenAt: at(50), VerifiedAt: at(60), SettledAt: at(70), SettledState: "merged",
	}}
	single.IntegrationApprovals = []domain.LadoTimingIntegrationApproval{{SessionID: "w1", At: at(65)}, {SessionID: "w1", At: at(68)}}

	parts := planned
	parts.QueueEntries = []domain.LadoTimingQueueEntry{{ID: "q1", ApprovalID: "a1", PartsStartedAt: at(22)}}
	parts.Executions = []domain.LadoTimingExecution{
		{SessionID: "p1", Role: domain.LadoRolePart, PartID: "store", ApprovalID: "a1", QueueEntryID: "q1", StartedAt: at(23)},
		{SessionID: "i1", Role: domain.LadoRoleIntegration, ApprovalID: "a1", QueueEntryID: "q1", StartedAt: at(40),
			PRSeenAt: at(55), PRSeenEstimated: true, VerifiedAt: at(58), VerifiedEstimated: true},
	}

	partsRunning := planned
	partsRunning.QueueEntries = parts.QueueEntries
	partsRunning.Executions = parts.Executions[:1]

	reapproved := planned
	reapproved.PlanRequestedAt = []time.Time{at(5), at(2)}
	reapproved.RevisionCreatedAt = []time.Time{at(12), at(80)}
	reapproved.Approvals = []domain.LadoTimingApproval{{ID: "a1", At: at(20)}, {ID: "a2", At: at(90)}}
	reapproved.QueueEntries = []domain.LadoTimingQueueEntry{{ID: "q1", ApprovalID: "a1"}, {ID: "q2", ApprovalID: "a2"}}
	reapproved.Executions = []domain.LadoTimingExecution{
		{SessionID: "w1", ApprovalID: "a1", QueueEntryID: "q1", StartedAt: at(21), PRSeenAt: at(50), SettledAt: at(75), SettledState: "closed"},
		{SessionID: "w2", ApprovalID: "a2", QueueEntryID: "q2", StartedAt: at(91), PRSeenAt: at(100), VerifiedAt: at(110), SettledAt: at(120), SettledState: "merged"},
	}
	reapproved.IntegrationApprovals = []domain.LadoTimingIntegrationApproval{{SessionID: "w1", At: at(60)}}

	closed := single
	closed.IntegrationApprovals = nil
	closed.Executions = []domain.LadoTimingExecution{{
		SessionID: "w1", ApprovalID: "a1", QueueEntryID: "q1", StartedAt: at(21), PRSeenAt: at(50), SettledAt: at(70), SettledState: "closed",
	}}

	estimatedEarly := single
	estimatedEarly.IntegrationApprovals = nil
	estimatedEarly.Executions = []domain.LadoTimingExecution{{
		SessionID: "w1", ApprovalID: "a1", QueueEntryID: "q1", StartedAt: at(21),
		PRSeenAt: at(19), PRSeenEstimated: true,
	}}

	type point struct {
		stage     domain.LadoTimingStage
		minute    int
		since     int
		estimated bool
	}
	for _, tc := range []struct {
		name                                 string
		facts                                domain.LadoTaskTimingFacts
		want                                 []point
		total                                int
		requests, revisions, approvals, runs int
	}{
		{
			name: "single run", facts: single, total: 70, requests: 1, revisions: 1, approvals: 1, runs: 1,
			want: []point{
				{domain.LadoTimingReceived, 0, 0, false}, {domain.LadoTimingPlanRequested, 2, 2, false},
				{domain.LadoTimingRevision, 12, 10, false}, {domain.LadoTimingApproved, 20, 8, false},
				{domain.LadoTimingRunStarted, 21, 1, false}, {domain.LadoTimingPROpened, 50, 29, false},
				{domain.LadoTimingVerified, 60, 10, false}, {domain.LadoTimingIntegrationApproved, 65, 5, false},
				{domain.LadoTimingMerged, 70, 5, false},
			},
		},
		{
			name: "parts run starts with its parts", facts: parts, requests: 1, revisions: 1, approvals: 1, runs: 1,
			want: []point{
				{domain.LadoTimingReceived, 0, 0, false}, {domain.LadoTimingPlanRequested, 2, 2, false},
				{domain.LadoTimingRevision, 12, 10, false}, {domain.LadoTimingApproved, 20, 8, false},
				{domain.LadoTimingRunStarted, 22, 2, false}, {domain.LadoTimingPROpened, 55, 33, true},
				{domain.LadoTimingVerified, 58, 3, true},
			},
		},
		{
			name: "parts still running", facts: partsRunning, requests: 1, revisions: 1, approvals: 1, runs: 0,
			want: []point{
				{domain.LadoTimingReceived, 0, 0, false}, {domain.LadoTimingPlanRequested, 2, 2, false},
				{domain.LadoTimingRevision, 12, 10, false}, {domain.LadoTimingApproved, 20, 8, false},
				{domain.LadoTimingRunStarted, 22, 2, false},
			},
		},
		{
			name: "approved again follows the newest run", facts: reapproved, total: 120, requests: 2, revisions: 2, approvals: 2, runs: 2,
			want: []point{
				{domain.LadoTimingReceived, 0, 0, false}, {domain.LadoTimingPlanRequested, 2, 2, false},
				{domain.LadoTimingRevision, 12, 10, false}, {domain.LadoTimingApproved, 90, 78, false},
				{domain.LadoTimingRunStarted, 91, 1, false}, {domain.LadoTimingPROpened, 100, 9, false},
				{domain.LadoTimingVerified, 110, 10, false}, {domain.LadoTimingMerged, 120, 10, false},
			},
		},
		{
			name: "before the run", facts: planned, requests: 1, revisions: 1, approvals: 1,
			want: []point{
				{domain.LadoTimingReceived, 0, 0, false}, {domain.LadoTimingPlanRequested, 2, 2, false},
				{domain.LadoTimingRevision, 12, 10, false}, {domain.LadoTimingApproved, 20, 8, false},
			},
		},
		{
			name: "closed without a merge", facts: closed, requests: 1, revisions: 1, approvals: 1, runs: 1,
			want: []point{
				{domain.LadoTimingReceived, 0, 0, false}, {domain.LadoTimingPlanRequested, 2, 2, false},
				{domain.LadoTimingRevision, 12, 10, false}, {domain.LadoTimingApproved, 20, 8, false},
				{domain.LadoTimingRunStarted, 21, 1, false}, {domain.LadoTimingPROpened, 50, 29, false},
			},
		},
		{
			name: "an estimate before the point before keeps a zero gap", facts: estimatedEarly, requests: 1, revisions: 1, approvals: 1, runs: 1,
			want: []point{
				{domain.LadoTimingReceived, 0, 0, false}, {domain.LadoTimingPlanRequested, 2, 2, false},
				{domain.LadoTimingRevision, 12, 10, false}, {domain.LadoTimingApproved, 20, 8, false},
				{domain.LadoTimingRunStarted, 21, 1, false}, {domain.LadoTimingPROpened, 19, 0, true},
			},
		},
		{name: "nothing yet", facts: domain.LadoTaskTimingFacts{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := domain.LadoTaskTimelineOf(tc.facts)
			var points []point
			for _, p := range got.Points {
				points = append(points, point{p.Stage, int(p.At.Sub(base) / time.Minute), int(p.SincePrev / time.Minute), p.Estimated})
			}
			if !slices.Equal(points, tc.want) {
				t.Fatalf("points = %v\nwant     %v", points, tc.want)
			}
			if got.Total != time.Duration(tc.total)*time.Minute {
				t.Fatalf("total = %v, want %d minutes", got.Total, tc.total)
			}
			if got.PlanRequests != tc.requests || got.Revisions != tc.revisions || got.Approvals != tc.approvals || got.Runs != tc.runs {
				t.Fatalf("counts = %d requests, %d revisions, %d approvals, %d runs; want %d, %d, %d, %d",
					got.PlanRequests, got.Revisions, got.Approvals, got.Runs, tc.requests, tc.revisions, tc.approvals, tc.runs)
			}
		})
	}
}

func TestSummarizeLadoActivitiesClassifiesCalls(t *testing.T) {
	base := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		fact domain.LadoActivityFact
		// want is "read", "search", "command", "edit", "tool" (a tool call
		// counted in ToolCalls only) or "" (not a tool call).
		want string
	}{
		{"acp read kind", domain.LadoActivityFact{Kind: domain.ActivityKindMCPTool, ToolKind: "read"}, "read"},
		{"acp search kind", domain.LadoActivityFact{Kind: domain.ActivityKindMCPTool, ToolKind: "search"}, "search"},
		{"acp fetch kind", domain.LadoActivityFact{Kind: domain.ActivityKindMCPTool, ToolKind: "fetch"}, "search"},
		{"claude Read", domain.LadoActivityFact{Kind: domain.ActivityKindMCPTool, ToolKind: "other", ToolName: "Read"}, "read"},
		{"claude Grep", domain.LadoActivityFact{Kind: domain.ActivityKindMCPTool, ToolName: "Grep"}, "search"},
		{"claude Glob", domain.LadoActivityFact{Kind: domain.ActivityKindMCPTool, ToolName: "Glob"}, "search"},
		{"other mcp tool", domain.LadoActivityFact{Kind: domain.ActivityKindMCPTool, ToolKind: "other", ToolName: "html_render"}, "tool"},
		{"rg", domain.LadoActivityFact{Kind: domain.ActivityKindCommand, Command: "rg -n LadoTask backend"}, "search"},
		{"sed -n", domain.LadoActivityFact{Kind: domain.ActivityKindCommand, Command: "sed -n 1,80p main.go"}, "read"},
		{"cat", domain.LadoActivityFact{Kind: domain.ActivityKindCommand, Command: "cat README.md | head"}, "read"},
		{"git log", domain.LadoActivityFact{Kind: domain.ActivityKindCommand, Command: "git log --oneline -5"}, "search"},
		{"git -C log", domain.LadoActivityFact{Kind: domain.ActivityKindCommand, Command: "git -C backend log -1"}, "search"},
		{"git commit", domain.LadoActivityFact{Kind: domain.ActivityKindCommand, Command: "git commit -m x"}, "command"},
		{"go test", domain.LadoActivityFact{Kind: domain.ActivityKindCommand, Command: "go test ./..."}, "command"},
		{"cd then rg", domain.LadoActivityFact{Kind: domain.ActivityKindCommand, Command: "cd x && rg foo"}, "search"},
		{"cd; ls", domain.LadoActivityFact{Kind: domain.ActivityKindCommand, Command: "cd /tmp; ls -la"}, "search"},
		{"env then grep", domain.LadoActivityFact{Kind: domain.ActivityKindCommand, Command: "LC_ALL=C grep -r x ."}, "search"},
		{"absolute path", domain.LadoActivityFact{Kind: domain.ActivityKindCommand, Command: "/usr/bin/find . -name x"}, "search"},
		{"empty command", domain.LadoActivityFact{Kind: domain.ActivityKindCommand}, "command"},
		{"acp execute with Bash", domain.LadoActivityFact{Kind: domain.ActivityKindCommand, ToolKind: "execute", ToolName: "Bash", Command: "npm run lint"}, "command"},
		{"file change", domain.LadoActivityFact{Kind: domain.ActivityKindFileChange, ToolKind: "edit"}, "edit"},
		{"reasoning", domain.LadoActivityFact{Kind: domain.ActivityKindReasoning}, ""},
		{"usage", domain.LadoActivityFact{Kind: domain.ActivityKindUsage}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.fact.CreatedAt, tc.fact.UpdatedAt = base, base
			m := domain.SummarizeLadoActivities([]domain.LadoActivityFact{tc.fact})
			got := ""
			switch {
			case m.ReadCalls == 1:
				got = "read"
			case m.SearchCalls == 1:
				got = "search"
			case m.CommandCalls == 1:
				got = "command"
			case m.EditCalls == 1:
				got = "edit"
			case m.ToolCalls == 1:
				got = "tool"
			}
			if got != tc.want {
				t.Fatalf("class = %q, want %q (metrics %+v)", got, tc.want, m)
			}
			if tc.want != "" && m.ToolCalls != 1 {
				t.Fatalf("tool calls = %d, want 1", m.ToolCalls)
			}
		})
	}
}

func TestSummarizeLadoActivitiesTimesInvestigation(t *testing.T) {
	base := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return base.Add(time.Duration(seconds) * time.Second) }
	fact := func(kind domain.ActivityKind, created, updated int, command string) domain.LadoActivityFact {
		return domain.LadoActivityFact{Kind: kind, Command: command, CreatedAt: at(created), UpdatedAt: at(updated)}
	}
	worker := []domain.LadoActivityFact{
		fact(domain.ActivityKindReasoning, 0, 1, ""),
		fact(domain.ActivityKindCommand, 10, 12, "rg foo"),
		fact(domain.ActivityKindCommand, 20, 21, "sed -n 1,20p a.go"),
		fact(domain.ActivityKindFileChange, 100, 101, ""),
		fact(domain.ActivityKindCommand, 110, 300, "go test ./..."),
		fact(domain.ActivityKindFileChange, 400, 401, ""),
	}
	m := domain.SummarizeLadoActivities(worker)
	if m.ToolCalls != 5 || m.ReadCalls != 1 || m.SearchCalls != 1 || m.CommandCalls != 1 || m.EditCalls != 2 {
		t.Fatalf("calls = %+v, want 5 tool calls: 1 read, 1 search, 1 command, 2 edits", m)
	}
	if m.InvestigationCalls() != 2 {
		t.Fatalf("investigation calls = %d, want 2", m.InvestigationCalls())
	}
	if !m.FirstActivityAt.Equal(at(0)) || !m.FirstEditAt.Equal(at(100)) || !m.LastActivityAt.Equal(at(401)) {
		t.Fatalf("times = first %v, edit %v, last %v", m.FirstActivityAt, m.FirstEditAt, m.LastActivityAt)
	}
	if m.Investigate != 90*time.Second {
		t.Fatalf("investigate = %v, want from the first tool call to the first edit, 90s", m.Investigate)
	}

	// A plan worker edits nothing: its whole session from the first tool call
	// is investigation.
	designer := []domain.LadoActivityFact{
		fact(domain.ActivityKindMCPTool, 30, 31, ""),
		fact(domain.ActivityKindCommand, 5, 6, "cat plan.md"),
		fact(domain.ActivityKindCommand, 200, 250, "git status"),
	}
	m = domain.SummarizeLadoActivities(designer)
	if m.EditCalls != 0 || !m.FirstEditAt.IsZero() || m.Investigate != 245*time.Second {
		t.Fatalf("designer = %+v, want 245s of investigation and no edit", m)
	}

	if m := domain.SummarizeLadoActivities(nil); m.ToolCalls != 0 || m.Investigate != 0 || !m.FirstActivityAt.IsZero() {
		t.Fatalf("no activity = %+v, want zero metrics", m)
	}
	if m := domain.SummarizeLadoActivities([]domain.LadoActivityFact{fact(domain.ActivityKindReasoning, 0, 5, "")}); m.Investigate != 0 || !m.LastActivityAt.Equal(at(5)) {
		t.Fatalf("no tool call = %+v, want no investigation", m)
	}
}

func TestSummarizeLadoTimings(t *testing.T) {
	base := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	timeline := func(gaps ...int) domain.LadoTaskTimeline {
		var tl domain.LadoTaskTimeline
		at := base
		for i, gap := range gaps {
			at = at.Add(time.Duration(gap) * time.Minute)
			tl.Points = append(tl.Points, domain.LadoTimingPoint{
				Stage: domain.LadoTimingStages[i], At: at, SincePrev: time.Duration(gap) * time.Minute,
			})
		}
		return tl
	}
	session := func(role domain.LadoSessionRole, calls, reads, searches, minutes int, stored bool) domain.LadoSessionMetrics {
		return domain.LadoSessionMetrics{
			Role: role, ToolCalls: calls, ReadCalls: reads, SearchCalls: searches,
			Investigate: time.Duration(minutes) * time.Minute, Stored: stored,
		}
	}
	tasks := []domain.LadoTaskTimings{
		{TaskID: "t1", Timeline: timeline(0, 2, 10), Sessions: []domain.LadoSessionMetrics{
			session(domain.LadoSessionDesigner, 10, 4, 4, 5, true), session(domain.LadoSessionWorker, 40, 10, 10, 20, true),
		}},
		{TaskID: "t2", Timeline: timeline(0, 4, 30), Sessions: []domain.LadoSessionMetrics{
			session(domain.LadoSessionDesigner, 20, 8, 8, 15, false), session(domain.LadoSessionCritic, 6, 3, 3, 4, true),
		}},
		{TaskID: "t3", Timeline: timeline(0, 6), Sessions: []domain.LadoSessionMetrics{
			session(domain.LadoSessionDesigner, 30, 9, 9, 7, true),
		}},
	}
	got := domain.SummarizeLadoTimings("demo", tasks)
	if got.ProjectID != "demo" || len(got.Tasks) != 3 {
		t.Fatalf("summary = %s with %d tasks", got.ProjectID, len(got.Tasks))
	}
	wantGaps := []domain.LadoTimingGap{
		{From: domain.LadoTimingReceived, To: domain.LadoTimingPlanRequested, Count: 3, Median: 4 * time.Minute, Max: 6 * time.Minute},
		{From: domain.LadoTimingPlanRequested, To: domain.LadoTimingRevision, Count: 2, Median: 20 * time.Minute, Max: 30 * time.Minute},
	}
	if !slices.Equal(got.Gaps, wantGaps) {
		t.Fatalf("gaps = %+v\nwant %+v", got.Gaps, wantGaps)
	}
	wantRoles := []domain.LadoInvestigationSummary{
		{Role: domain.LadoSessionDesigner, Sessions: 3, MedianCalls: 20, MedianInvestigationCalls: 16, MedianInvestigate: 7 * time.Minute},
		{Role: domain.LadoSessionCritic, Sessions: 1, MedianCalls: 6, MedianInvestigationCalls: 6, MedianInvestigate: 4 * time.Minute},
		{Role: domain.LadoSessionWorker, Sessions: 1, MedianCalls: 40, MedianInvestigationCalls: 20, MedianInvestigate: 20 * time.Minute},
	}
	if !slices.Equal(got.Investigation, wantRoles) {
		t.Fatalf("investigation = %+v\nwant %+v", got.Investigation, wantRoles)
	}
	if empty := domain.SummarizeLadoTimings("demo", nil); len(empty.Gaps) != 0 || len(empty.Investigation) != 0 {
		t.Fatalf("empty summary = %+v", empty)
	}
}

func TestLadoSessionRoleOfExecution(t *testing.T) {
	for role, want := range map[string]domain.LadoSessionRole{
		domain.LadoRoleSingle: domain.LadoSessionWorker, domain.LadoRolePart: domain.LadoSessionPart,
		domain.LadoRoleMerge: domain.LadoSessionMerge, domain.LadoRoleIntegration: domain.LadoSessionIntegration,
	} {
		if got := domain.LadoSessionRoleOfExecution(role); got != want || !got.Valid() {
			t.Fatalf("role of %q = %q, want %q", role, got, want)
		}
	}
	if domain.LadoSessionRole("orchestrator").Valid() {
		t.Fatal("an unknown role is valid")
	}
}
