package ladorelay

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestRelayDeliversToEachKindOfLiveWorker(t *testing.T) {
	cases := []struct {
		name, role, part string
	}{
		{"single", domain.LadoRoleSingle, ""},
		{"part", domain.LadoRolePart, "service"},
		{"merge", domain.LadoRoleMerge, "service"},
		{"integration", domain.LadoRoleIntegration, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.setExecutions(liveExecution(testWorker, tc.role, tc.part))
			got, err := f.svc.Relay(context.Background(), request())
			if err != nil {
				t.Fatalf("Relay: %v", err)
			}
			if got.State != domain.LadoRelayDelivered || !got.DeliveredAt.Equal(t0) {
				t.Errorf("relay = %s at %v, want delivered at %v", got.State, got.DeliveredAt, t0)
			}
			if len(f.messenger.sent) != 1 || f.messenger.sent[0].to != testWorker {
				t.Fatalf("sent = %+v, want one message to %s", f.messenger.sent, testWorker)
			}
			text := f.messenger.sent[0].text
			for _, want := range []string{domain.LadoRelayHeader + " (lado-relay-1)", testInstruction, "> " + testEvidence} {
				if !strings.Contains(text, want) {
					t.Errorf("worker text lacks %q:\n%s", want, text)
				}
			}
			stored, ok, _ := f.store.LadoRelay(context.Background(), "lado-relay-1")
			if !ok {
				t.Fatal("the relay was not recorded")
			}
			want := domain.LadoRelay{
				ID: "lado-relay-1", TaskID: testTask, ProjectID: testProject,
				OrchestratorSessionID: testOrchestrator, WorkerSessionID: testWorker, WorkerRole: tc.role, PartID: tc.part,
				Instruction: testInstruction, Evidence: testEvidence, EvidenceMessageID: "m-1", EvidenceAt: t0.Add(-time.Hour),
				State: domain.LadoRelayDelivered, CreatedAt: t0, DeliveredAt: t0,
			}
			if !relayEqual(stored, want) {
				t.Errorf("stored relay = %+v,\nwant %+v", stored, want)
			}
			if actions := f.auditActions(); !slices.Equal(actions, []string{domain.LadoRelayAuditSent}) {
				t.Errorf("audit = %v, want one relay_sent", actions)
			}
		})
	}
}

func relayEqual(a, b domain.LadoRelay) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func (f *fixture) auditActions() []string {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	var actions []string
	for _, ev := range f.store.audits {
		actions = append(actions, ev.Action)
	}
	return actions
}

func TestRelayRefusals(t *testing.T) {
	ended := liveExecution(testWorker, domain.LadoRoleSingle, "")
	ended.Execution.SessionTerminated = true
	exited := liveExecution(testWorker, domain.LadoRoleSingle, "")
	exited.SessionActivity = domain.ActivityExited
	timedOut := liveExecution(testWorker, domain.LadoRoleSingle, "")
	timedOut.Execution.TimedOutAt = t0.Add(-time.Minute)
	timedOut.Execution.SessionTerminated = true
	settled := liveExecution(testWorker, domain.LadoRoleIntegration, "")
	settled.Execution.SettledAt = t0.Add(-time.Minute)
	older := liveExecution("proj-1-worker-0", domain.LadoRoleSingle, "")
	older.Execution.SessionTerminated = true

	cases := []struct {
		name   string
		change func(*fixture, *Request)
		code   string
		// detail is a details key and the value it must hold.
		detail, value string
	}{
		{"no session", func(_ *fixture, r *Request) { r.SessionID = "" }, domain.LadoRelayInvalid, "field", "sessionId"},
		{"no task", func(_ *fixture, r *Request) { r.Task = " " }, domain.LadoRelayInvalid, "field", "task"},
		{"no message", func(_ *fixture, r *Request) { r.Message = "\n" }, domain.LadoRelayInvalid, "field", "message"},
		{"long message", func(_ *fixture, r *Request) {
			r.Message = strings.Repeat("가", domain.LadoRelayMaxInstructionChars+1)
		}, domain.LadoRelayInvalid, "field", "message"},
		{"short evidence", func(_ *fixture, r *Request) { r.Evidence = "해  줘" }, domain.LadoRelayInvalid, "field", "evidence"},
		{"long evidence", func(_ *fixture, r *Request) {
			r.Evidence = strings.Repeat("a", domain.LadoRelayMaxEvidenceChars+1)
		}, domain.LadoRelayInvalid, "field", "evidence"},
		{"bad task", func(_ *fixture, r *Request) { r.Task = "the last one" }, domain.LadoRelayInvalid, "field", "task"},
		{"caller is a worker", func(_ *fixture, r *Request) { r.SessionID = testWorker }, domain.LadoRelayNotOrchestrator, "sessionId", string(testWorker)},
		{"caller unknown", func(_ *fixture, r *Request) { r.SessionID = "nobody" }, domain.LadoRelayNotOrchestrator, "sessionId", "nobody"},
		{"caller ended", func(f *fixture, _ *Request) {
			rec := f.store.sessions[testOrchestrator]
			rec.IsTerminated = true
			f.store.sessions[testOrchestrator] = rec
		}, domain.LadoRelayNotOrchestrator, "sessionId", string(testOrchestrator)},
		{"terminal orchestrator", func(f *fixture, _ *Request) {
			rec := f.store.sessions[testOrchestrator]
			rec.Mode = domain.SessionModeTUI
			// Whatever the last prompt says, it is no evidence.
			rec.Metadata.LatestUserPrompt = testSaid
			f.store.sessions[testOrchestrator] = rec
		}, domain.LadoRelayEvidenceUnverified, "reason", domain.LadoRelayEvidenceTerminalOrchestrator},
		{"not a LADO project", func(f *fixture, _ *Request) { f.store.ladoMode[testProject] = false },
			domain.LadoRelayNotLadoProject, "projectId", string(testProject)},
		{"unknown task id", func(_ *fixture, r *Request) { r.Task = "lado-task-none" }, domain.LadoRelayTaskNotFound, "task", "lado-task-none"},
		{"unknown issue", func(_ *fixture, r *Request) { r.Task = "#50" }, domain.LadoRelayTaskNotFound, "task", "#50"},
		{"another project's task", func(f *fixture, r *Request) {
			f.store.tasks = append(f.store.tasks, domain.LadoTask{ID: "lado-task-other", ProjectID: "proj-2", IssueRef: "github:acme/other#49"})
			r.Task = "lado-task-other"
		}, domain.LadoRelayWrongProject, "taskId", "lado-task-other"},
		{"cancelled task", func(f *fixture, _ *Request) { f.store.tasks[0].CancelledAt = t0.Add(-time.Minute) },
			domain.LadoRelayNoWorker, "reason", domain.LadoRelayNoWorkerCancelled},
		{"no execution", func(f *fixture, _ *Request) { f.setExecutions() }, domain.LadoRelayNoWorker, "reason", domain.LadoRelayNoWorkerNone},
		{"session ended", func(f *fixture, _ *Request) { f.setExecutions(ended, older) }, domain.LadoRelayNoWorker, "reason", domain.LadoRelayNoWorkerEnded},
		{"agent exited", func(f *fixture, _ *Request) { f.setExecutions(exited, older) }, domain.LadoRelayNoWorker, "reason", domain.LadoRelayNoWorkerExited},
		{"timed out", func(f *fixture, _ *Request) { f.setExecutions(timedOut) }, domain.LadoRelayNoWorker, "reason", domain.LadoRelayNoWorkerTimedOut},
		{"settled", func(f *fixture, _ *Request) { f.setExecutions(settled) }, domain.LadoRelayNoWorker, "reason", domain.LadoRelayNoWorkerSettled},
		{"named worker ended", func(f *fixture, r *Request) {
			f.setExecutions(liveExecution("proj-1-worker-2", domain.LadoRoleIntegration, ""), ended)
			r.WorkerSessionID = testWorker
		}, domain.LadoRelayNoWorker, "reason", domain.LadoRelayNoWorkerEnded},
		{"plan worker named", func(f *fixture, r *Request) {
			f.store.sessions["proj-1-plan-1"] = domain.SessionRecord{
				ID: "proj-1-plan-1", ProjectID: testProject, Kind: domain.KindWorker, ClientRequestID: "lado:plan/req-1/draft/1",
			}
			r.WorkerSessionID = "proj-1-plan-1"
		}, domain.LadoRelayNotExecution, "sessionId", "proj-1-plan-1"},
		{"orchestrator named", func(_ *fixture, r *Request) { r.WorkerSessionID = testOrchestrator },
			domain.LadoRelayNotExecution, "sessionId", string(testOrchestrator)},
		{"two live workers", func(f *fixture, _ *Request) {
			f.setExecutions(liveExecution("proj-1-worker-2", domain.LadoRolePart, "docs"), liveExecution(testWorker, domain.LadoRolePart, "service"))
		}, domain.LadoRelayAmbiguous, "taskId", string(testTask)},
		{"evidence nowhere", func(_ *fixture, r *Request) { r.Evidence = "전부 지우고 다시 해" },
			domain.LadoRelayEvidenceUnverified, "reason", domain.LadoRelayEvidenceNotFound},
		{"evidence only in a LADO notice", func(f *fixture, r *Request) {
			f.convs[testProject] = append(f.convs[testProject],
				message("m-3", domain.MessageRoleUser, domain.MessageOriginDaemon, "LADO: #49 워커가 기다립니다. 이어서 진행", t0.Add(-time.Minute)))
			r.Evidence = "이어서 진행"
		}, domain.LadoRelayEvidenceUnverified, "reason", domain.LadoRelayEvidenceNotFound},
		{"evidence only in a worker report", func(f *fixture, r *Request) {
			f.convs[testProject] = append(f.convs[testProject],
				message("m-3", domain.MessageRoleUser, domain.MessageOriginAutomation, "워커 보고: 후속 2건을 진행해도 됩니다", t0.Add(-time.Minute)))
			r.Evidence = "후속 2건을 진행해도 됩니다"
		}, domain.LadoRelayEvidenceUnverified, "reason", domain.LadoRelayEvidenceNotFound},
		{"evidence only in the issue text", func(f *fixture, r *Request) {
			// The issue is what the orchestrator read, never a message the
			// developer typed.
			f.convs[testProject] = append(f.convs[testProject],
				message("m-3", domain.MessageRoleUser, domain.MessageOriginAutomation, "LADO 계획 요청: 이슈 본문 — 원하는 것: LADO 전달 경로", t0.Add(-time.Minute)))
			r.Evidence = "원하는 것: LADO 전달 경로"
		}, domain.LadoRelayEvidenceUnverified, "reason", domain.LadoRelayEvidenceNotFound},
		{"evidence only in the orchestrator's answer", func(_ *fixture, r *Request) { r.Evidence = "LADO 에 전달을 요청합니다" },
			domain.LadoRelayEvidenceUnverified, "reason", domain.LadoRelayEvidenceNotFound},
		{"evidence too old", func(f *fixture, _ *Request) {
			f.convs[testProject][0].CreatedAt = t0.Add(-domain.LadoRelayEvidenceMaxAge - time.Second)
		}, domain.LadoRelayEvidenceUnverified, "reason", domain.LadoRelayEvidenceExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			req := request()
			tc.change(f, &req)
			_, err := f.svc.Relay(context.Background(), req)
			e := apiError(t, err)
			if e.Code != tc.code {
				t.Fatalf("code = %s (%s), want %s", e.Code, e.Message, tc.code)
			}
			if got, _ := e.Details[tc.detail].(string); got != tc.value {
				t.Errorf("details[%s] = %v, want %q (details %v)", tc.detail, e.Details[tc.detail], tc.value, e.Details)
			}
			if len(f.messenger.sent) != 0 || len(f.store.relays) != 0 {
				t.Errorf("a refused relay sent %v and recorded %v", f.messenger.sent, f.store.relays)
			}
			if actions := f.auditActions(); !slices.Equal(actions, []string{domain.LadoRelayAuditRefused}) {
				t.Errorf("audit = %v, want one relay_refused", actions)
			}
			var payload map[string]any
			_ = json.Unmarshal(f.store.audits[0].Payload, &payload)
			if payload["code"] != tc.code || f.store.audits[0].SubjectID != string(req.SessionID) {
				t.Errorf("refusal audit = %s %s, want code %s for the caller", f.store.audits[0].SubjectID, f.store.audits[0].Payload, tc.code)
			}
		})
	}
}

func TestRelayAmbiguousListsTheCandidates(t *testing.T) {
	f := newFixture(t)
	f.setExecutions(liveExecution("proj-1-worker-2", domain.LadoRoleIntegration, ""), liveExecution(testWorker, domain.LadoRolePart, "service"))
	_, err := f.svc.Relay(context.Background(), request())
	e := apiError(t, err)
	candidates, _ := e.Details["candidates"].([]map[string]any)
	want := []map[string]any{
		{"sessionId": "proj-1-worker-2", "role": "integration", "partId": ""},
		{"sessionId": string(testWorker), "role": "part", "partId": "service"},
	}
	if j, w := mustJSON(candidates), mustJSON(want); j != w {
		t.Errorf("candidates = %s, want %s", j, w)
	}

	// Naming one of them relays to it.
	req := request()
	req.WorkerSessionID = testWorker
	got, err := f.svc.Relay(context.Background(), req)
	if err != nil {
		t.Fatalf("Relay to the named worker: %v", err)
	}
	if got.WorkerSessionID != testWorker || got.WorkerRole != domain.LadoRolePart || got.PartID != "service" {
		t.Errorf("relay went to %s (%s/%s), want %s part service", got.WorkerSessionID, got.WorkerRole, got.PartID, testWorker)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestRelayPicksTheLiveWorkerOverEndedOnes(t *testing.T) {
	f := newFixture(t)
	ended := liveExecution("proj-1-worker-0", domain.LadoRoleSingle, "")
	ended.Execution.SessionTerminated = true
	f.setExecutions(ended, liveExecution(testWorker, domain.LadoRoleSingle, ""))
	got, err := f.svc.Relay(context.Background(), request())
	if err != nil || got.WorkerSessionID != testWorker {
		t.Fatalf("Relay = (%s, %v), want the live worker %s", got.WorkerSessionID, err, testWorker)
	}
}

func TestRelayResolvesTheTask(t *testing.T) {
	cases := []struct {
		name, task string
		extra      []domain.LadoTask
		want       domain.LadoTaskID
		code       string
	}{
		{name: "task id", task: string(testTask), want: testTask},
		{name: "issue number with #", task: "#49", want: testTask},
		{name: "bare issue number", task: "49", want: testTask},
		{name: "canonical issue id", task: "github:acme/app#49", want: testTask},
		{name: "a cancelled task of the issue yields", task: "#49", want: testTask,
			extra: []domain.LadoTask{{ID: "lado-task-0", ProjectID: testProject, IssueRef: "github:acme/app#49", CancelledAt: t0.Add(-time.Hour)}}},
		{name: "another project's issue is not seen", task: "#49", want: testTask,
			extra: []domain.LadoTask{{ID: "lado-task-other", ProjectID: "proj-2", IssueRef: "github:acme/app#49"}}},
		{name: "two repositories", task: "#49", code: domain.LadoRelayTaskAmbiguous,
			extra: []domain.LadoTask{{ID: "lado-task-2", ProjectID: testProject, IssueRef: "github:acme/web#49"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.tasks = append(f.store.tasks, tc.extra...)
			req := request()
			req.Task = tc.task
			got, err := f.svc.Relay(context.Background(), req)
			if tc.code != "" {
				if e := apiError(t, err); e.Code != tc.code {
					t.Fatalf("code = %s, want %s", e.Code, tc.code)
				}
				return
			}
			if err != nil || got.TaskID != tc.want {
				t.Fatalf("Relay = (%s, %v), want task %s", got.TaskID, err, tc.want)
			}
		})
	}
}

func TestRelayEvidence(t *testing.T) {
	t.Run("evidence from before a /clear counts", func(t *testing.T) {
		// A /clear writes a boundary and keeps every earlier row, so the
		// conversation still holds what the developer said before it.
		f := newFixture(t)
		f.convs[testProject] = []domain.ConversationMessage{
			message("m-old", domain.MessageRoleUser, domain.MessageOriginHuman, testSaid, t0.Add(-20*time.Hour)),
			message("m-ack", domain.MessageRoleAssistant, domain.MessageOriginProvider, "네.", t0.Add(-20*time.Hour+time.Minute)),
			// The /clear boundary is an activity, not a message; after it the
			// developer talks about something else.
			message("m-new", domain.MessageRoleUser, domain.MessageOriginHuman, "이제 다른 이야기를 하자", t0.Add(-time.Hour)),
		}
		got, err := f.svc.Relay(context.Background(), request())
		if err != nil || got.EvidenceMessageID != "m-old" {
			t.Fatalf("Relay = (%s, %v), want evidence m-old", got.EvidenceMessageID, err)
		}
	})
	t.Run("the newest matching message is the evidence", func(t *testing.T) {
		f := newFixture(t)
		f.convs[testProject] = append(f.convs[testProject],
			message("m-again", domain.MessageRoleUser, domain.MessageOriginHuman, "다시 말할게. "+testSaid, t0.Add(-time.Minute)))
		got, err := f.svc.Relay(context.Background(), request())
		if err != nil || got.EvidenceMessageID != "m-again" || !got.EvidenceAt.Equal(t0.Add(-time.Minute)) {
			t.Fatalf("Relay = (%s at %v, %v), want m-again", got.EvidenceMessageID, got.EvidenceAt, err)
		}
	})
	t.Run("white space does not matter", func(t *testing.T) {
		f := newFixture(t)
		req := request()
		req.Evidence = "그 워커에게\n  이어서   해"
		if _, err := f.svc.Relay(context.Background(), req); err != nil {
			t.Fatalf("Relay: %v", err)
		}
	})
	t.Run("a young message counts beside an old one", func(t *testing.T) {
		f := newFixture(t)
		f.convs[testProject] = []domain.ConversationMessage{
			message("m-old", domain.MessageRoleUser, domain.MessageOriginHuman, testSaid, t0.Add(-48*time.Hour)),
			message("m-young", domain.MessageRoleUser, domain.MessageOriginHuman, testSaid, t0.Add(-23*time.Hour)),
		}
		got, err := f.svc.Relay(context.Background(), request())
		if err != nil || got.EvidenceMessageID != "m-young" {
			t.Fatalf("Relay = (%s, %v), want m-young", got.EvidenceMessageID, err)
		}
	})
}

func TestRelayMarksAnInstructionBeyondTheEvidence(t *testing.T) {
	t.Run("inside the developer's message", func(t *testing.T) {
		f := newFixture(t)
		got, err := f.svc.Relay(context.Background(), request())
		if err != nil || got.BeyondEvidence {
			t.Fatalf("Relay = (beyond %v, %v), want not beyond the evidence", got.BeyondEvidence, err)
		}
		if strings.Contains(f.messenger.sent[0].text, domain.LadoRelayBeyondEvidenceMark) {
			t.Errorf("worker text marks a quoted instruction:\n%s", f.messenger.sent[0].text)
		}
	})
	t.Run("not in the developer's message", func(t *testing.T) {
		f := newFixture(t)
		req := request()
		req.Message = "후속 2건(backend/internal/service/ladorelay/plan.go 정리)과 남은 묶음을 이어서 진행하세요"
		got, err := f.svc.Relay(context.Background(), req)
		if err != nil {
			t.Fatalf("Relay: %v", err)
		}
		if !got.BeyondEvidence || got.State != domain.LadoRelayDelivered {
			t.Fatalf("relay = beyond %v, %s; want delivered and beyond the evidence", got.BeyondEvidence, got.State)
		}
		text := f.messenger.sent[0].text
		for _, want := range []string{domain.LadoRelayBeyondEvidenceMark, "개발자 원문을 기준으로 판단", "> " + testEvidence} {
			if !strings.Contains(text, want) {
				t.Errorf("worker text lacks %q:\n%s", want, text)
			}
		}
		if stored, _, _ := f.store.LadoRelay(context.Background(), got.ID); !stored.BeyondEvidence {
			t.Error("the stored relay is not marked beyond the evidence")
		}
	})
}

func TestRelayMarksAnInstructionOutsideThePlan(t *testing.T) {
	cases := []struct {
		name        string
		role, part  string
		message     string
		flagged     bool
		wantReasons []string
	}{
		{name: "paths inside the plan", message: "backend/internal/service/ladorelay/plan.go 와 docs/LJY-PRD/TDD.md 를 고치세요"},
		{name: "no paths", message: "테스트를 다시 돌리고 보고하세요"},
		{name: "a URL is no path", message: "https://github.com/acme/app/pull/3 리뷰를 반영하세요"},
		{name: "flagged", message: "새 단계: 릴리스 노트를 쓰세요", flagged: true,
			wantReasons: []string{domain.LadoRelayFlaggedReason}},
		{name: "a path outside the plan", message: "frontend/src/App.tsx 와 backend/internal/service/ladorelay/plan.go 를 고치세요",
			wantReasons: []string{domain.LadoRelayPathReason("frontend/src/App.tsx")}},
		{name: "flagged and outside", message: "`frontend/src/App.tsx`를 고치세요", flagged: true,
			wantReasons: []string{domain.LadoRelayFlaggedReason, domain.LadoRelayPathReason("frontend/src/App.tsx")}},
		{name: "a part worker is bound by its part", role: domain.LadoRolePart, part: "service",
			message:     "docs/LJY-PRD/TDD.md 도 고치세요",
			wantReasons: []string{domain.LadoRelayPathReason("docs/LJY-PRD/TDD.md")}},
		{name: "a merge worker is bound by its part", role: domain.LadoRoleMerge, part: "docs",
			message:     "backend/internal/service/ladorelay/plan.go 충돌을 푸세요",
			wantReasons: []string{domain.LadoRelayPathReason("backend/internal/service/ladorelay/plan.go")}},
		{name: "an integration worker has the whole plan", role: domain.LadoRoleIntegration,
			message: "docs/LJY-PRD/TDD.md 와 backend/internal/service/ladorelay/plan.go 를 맞추세요"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.setExecutions(liveExecution(testWorker, tc.role, tc.part))
			req := request()
			req.Message, req.OutsidePlan = tc.message, tc.flagged
			got, err := f.svc.Relay(context.Background(), req)
			if err != nil {
				t.Fatalf("Relay: %v", err)
			}
			if got.State != domain.LadoRelayDelivered {
				t.Fatalf("relay = %s, want delivered", got.State)
			}
			stored, _, _ := f.store.LadoRelay(context.Background(), got.ID)
			if stored.OutsidePlan != (len(tc.wantReasons) > 0) || !slices.Equal(stored.OutsidePlanReasons, tc.wantReasons) {
				t.Errorf("stored outside plan = %v %q, want %q", stored.OutsidePlan, stored.OutsidePlanReasons, tc.wantReasons)
			}
			text := f.messenger.sent[0].text
			if marked := strings.Contains(text, domain.LadoRelayOutsidePlanMark); marked != (len(tc.wantReasons) > 0) {
				t.Errorf("worker text outside-plan mark = %v, want %v:\n%s", marked, len(tc.wantReasons) > 0, text)
			}
			for _, reason := range tc.wantReasons {
				if !strings.Contains(text, reason) {
					t.Errorf("worker text lacks the reason %q:\n%s", reason, text)
				}
			}
		})
	}
}

// A plan LADO cannot read never stops a relay: outside plan is a mark, so
// the relay is delivered with the reason that the paths went unchecked, and
// the failure is logged.
func TestRelayDeliversWhenThePlanCannotBeRead(t *testing.T) {
	cases := []struct {
		name        string
		message     string
		flagged     bool
		wantReasons []string
	}{
		{name: "a path to check", message: "frontend/src/App.tsx 를 고치세요",
			wantReasons: []string{domain.LadoRelayPlanUnreadReason}},
		{name: "flagged with a path", message: "backend/internal/service/ladorelay/plan.go 를 고치세요", flagged: true,
			wantReasons: []string{domain.LadoRelayFlaggedReason, domain.LadoRelayPlanUnreadReason}},
		{name: "no path reads no plan", message: "테스트를 다시 돌리고 보고하세요"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			delete(f.plans, testRevision)
			req := request()
			req.Message, req.OutsidePlan = tc.message, tc.flagged
			got, err := f.svc.Relay(context.Background(), req)
			if err != nil {
				t.Fatalf("Relay: %v", err)
			}
			if got.State != domain.LadoRelayDelivered || len(f.messenger.sent) != 1 {
				t.Fatalf("relay = %s with %d messages, want delivered once", got.State, len(f.messenger.sent))
			}
			stored, _, _ := f.store.LadoRelay(context.Background(), got.ID)
			if stored.OutsidePlan != (len(tc.wantReasons) > 0) || !slices.Equal(stored.OutsidePlanReasons, tc.wantReasons) {
				t.Errorf("stored outside plan = %v %q, want %q", stored.OutsidePlan, stored.OutsidePlanReasons, tc.wantReasons)
			}
			text := f.messenger.sent[0].text
			for _, reason := range tc.wantReasons {
				if !strings.Contains(text, reason) {
					t.Errorf("worker text lacks the reason %q:\n%s", reason, text)
				}
			}
			unread := slices.Contains(tc.wantReasons, domain.LadoRelayPlanUnreadReason)
			if logged := strings.Contains(f.logs.String(), "could not read the approved plan"); logged != unread {
				t.Errorf("warning logged = %v, want %v: %s", logged, unread, f.logs.String())
			}
		})
	}
}

func TestRelayRecordsAFailedDelivery(t *testing.T) {
	f := newFixture(t)
	f.messenger.err = errors.New("the worker is waiting for a decision")
	_, err := f.svc.Relay(context.Background(), request())
	e := apiError(t, err)
	if e.Code != domain.LadoRelayDeliveryFailed || e.Details["relayId"] != "lado-relay-1" ||
		e.Details["reason"] != "the worker is waiting for a decision" {
		t.Fatalf("error = %s %v, want delivery failed for lado-relay-1", e.Code, e.Details)
	}
	stored, ok, _ := f.store.LadoRelay(context.Background(), "lado-relay-1")
	if !ok || stored.State != domain.LadoRelayFailed || stored.Failure != "the worker is waiting for a decision" {
		t.Errorf("stored relay = %+v, want failed with the reason", stored)
	}
	if actions := f.auditActions(); !slices.Equal(actions, []string{domain.LadoRelayAuditSent}) {
		t.Errorf("audit = %v, want the relay audited once", actions)
	}
	var payload map[string]any
	_ = json.Unmarshal(f.store.audits[0].Payload, &payload)
	if payload["state"] != string(domain.LadoRelayFailed) {
		t.Errorf("audit payload = %s, want state failed", f.store.audits[0].Payload)
	}
}

func TestRelayWithoutAMessengerFails(t *testing.T) {
	f := newFixture(t)
	f.svc.deps.Messenger = nil
	_, err := f.svc.Relay(context.Background(), request())
	if e := apiError(t, err); e.Code != domain.LadoRelayDeliveryFailed {
		t.Fatalf("code = %s, want delivery failed", e.Code)
	}
}

func TestListReturnsATasksRelaysNewestFirst(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Relay(context.Background(), request()); err != nil {
		t.Fatalf("first Relay: %v", err)
	}
	f.now = t0.Add(time.Minute)
	if _, err := f.svc.Relay(context.Background(), request()); err != nil {
		t.Fatalf("second Relay: %v", err)
	}
	relays, err := f.svc.List(context.Background(), testTask)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var ids []domain.LadoRelayID
	for _, r := range relays {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, []domain.LadoRelayID{"lado-relay-2", "lado-relay-1"}) {
		t.Errorf("List = %v, want newest first", ids)
	}

	_, err = f.svc.List(context.Background(), "lado-task-none")
	if e := apiError(t, err); e.Code != domain.LadoRelayTaskNotFound {
		t.Errorf("List of an unknown task = %s, want not found", e.Code)
	}
}
