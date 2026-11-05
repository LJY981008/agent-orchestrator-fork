package controllers_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladorelay"
)

type fakeLadoRelayService struct {
	relay   domain.LadoRelay
	relays  []domain.LadoRelay
	err     error
	gotReq  ladorelay.Request
	gotTask domain.LadoTaskID
	calls   int
}

func (f *fakeLadoRelayService) Relay(_ context.Context, req ladorelay.Request) (domain.LadoRelay, error) {
	f.calls++
	f.gotReq = req
	return f.relay, f.err
}

func (f *fakeLadoRelayService) List(_ context.Context, taskID domain.LadoTaskID) ([]domain.LadoRelay, error) {
	f.calls++
	f.gotTask = taskID
	return f.relays, f.err
}

var ladoRelayTime = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

func sampleLadoRelay() domain.LadoRelay {
	return domain.LadoRelay{
		ID: "lado-relay-1", TaskID: "lado-task-1", ProjectID: "mer",
		OrchestratorSessionID: "mer-orchestrator-1", WorkerSessionID: "mer-7",
		WorkerRole:  domain.LadoRoleIntegration,
		Instruction: "후속 2건도 이어서 해", Evidence: "그 워커에게 후속 2건도 이어서 해",
		EvidenceMessageID: "msg-9", EvidenceAt: ladoRelayTime.Add(-time.Minute),
		BeyondEvidence: false, OutsidePlan: true,
		OutsidePlanReasons: []string{domain.LadoRelayFlaggedReason},
		State:              domain.LadoRelayDelivered,
		CreatedAt:          ladoRelayTime, DeliveredAt: ladoRelayTime.Add(time.Second),
	}
}

func TestLadoAPI_CreateRelay(t *testing.T) {
	svc := &fakeLadoRelayService{relay: sampleLadoRelay()}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRelay: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/relays",
		`{"sessionId":"mer-orchestrator-1","task":"#49","workerSessionId":"mer-7","message":"후속 2건도 이어서 해","evidence":"그 워커에게 후속 2건도 이어서 해","outsidePlan":true}`)
	if status != http.StatusCreated {
		t.Fatalf("POST = %d %s", status, body)
	}
	want := ladorelay.Request{
		SessionID: "mer-orchestrator-1", Task: "#49", WorkerSessionID: "mer-7",
		Message: "후속 2건도 이어서 해", Evidence: "그 워커에게 후속 2건도 이어서 해", OutsidePlan: true,
	}
	if !reflect.DeepEqual(svc.gotReq, want) {
		t.Fatalf("request = %+v, want %+v", svc.gotReq, want)
	}
	got := decodeLadoJSON[controllers.LadoRelayResponse](t, body)
	delivered := ladoRelayTime.Add(time.Second)
	wantResp := controllers.LadoRelayResponse{
		ID: "lado-relay-1", TaskID: "lado-task-1", ProjectID: "mer",
		OrchestratorSessionID: "mer-orchestrator-1", WorkerSessionID: "mer-7", WorkerRole: "integration",
		Instruction: "후속 2건도 이어서 해", Evidence: "그 워커에게 후속 2건도 이어서 해",
		EvidenceMessageID: "msg-9", EvidenceAt: ladoRelayTime.Add(-time.Minute),
		OutsidePlan: true, OutsidePlanReasons: []string{domain.LadoRelayFlaggedReason},
		State: "delivered", CreatedAt: ladoRelayTime, DeliveredAt: &delivered,
	}
	if !reflect.DeepEqual(got, wantResp) {
		t.Fatalf("response = %+v\nwant %+v", got, wantResp)
	}
}

func TestLadoAPI_CreateRelayShapeOfASingleWorker(t *testing.T) {
	relay := sampleLadoRelay()
	relay.WorkerRole, relay.OutsidePlan, relay.OutsidePlanReasons = domain.LadoRoleSingle, false, nil
	relay.BeyondEvidence = true
	svc := &fakeLadoRelayService{relay: relay}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRelay: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/relays",
		`{"sessionId":"mer-orchestrator-1","task":"lado-task-1","message":"a","evidence":"abcd"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST = %d %s", status, body)
	}
	text := string(body)
	for _, want := range []string{`"outsidePlanReasons":[]`, `"beyondEvidence":true`, `"outsidePlan":false`} {
		if !strings.Contains(text, want) {
			t.Fatalf("body %s lacks %s", text, want)
		}
	}
	for _, absent := range []string{`"workerRole"`, `"partId"`, `"failure"`} {
		if strings.Contains(text, absent) {
			t.Fatalf("body %s has %s", text, absent)
		}
	}
	if svc.gotReq.WorkerSessionID != "" || svc.gotReq.OutsidePlan {
		t.Fatalf("request = %+v", svc.gotReq)
	}
}

func TestLadoAPI_CreateRelayRejectsUnknownFields(t *testing.T) {
	svc := &fakeLadoRelayService{relay: sampleLadoRelay()}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRelay: svc})

	// The sender display name is not an API field: nothing outside the
	// daemon can label a message as LADO's.
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/relays",
		`{"sessionId":"o","task":"#1","message":"m","evidence":"abcd","senderDisplayName":"x"}`)
	if status != http.StatusBadRequest || svc.calls != 0 {
		t.Fatalf("POST = %d %s, calls %d", status, body, svc.calls)
	}
}

func TestLadoAPI_CreateRelayErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"invalid", apierr.Invalid(domain.LadoRelayInvalid, "The instruction is required.", map[string]any{"field": "message"}), http.StatusBadRequest},
		{"not orchestrator", apierr.New(apierr.KindForbidden, domain.LadoRelayNotOrchestrator, "Only a live orchestrator…", nil), http.StatusForbidden},
		{"terminal orchestrator", apierr.New(apierr.KindForbidden, domain.LadoRelayEvidenceUnverified, "terminal",
			map[string]any{"reason": domain.LadoRelayEvidenceTerminalOrchestrator}), http.StatusForbidden},
		{"wrong project", apierr.New(apierr.KindForbidden, domain.LadoRelayWrongProject, "another project", nil), http.StatusForbidden},
		{"not lado", apierr.Conflict(domain.LadoRelayNotLadoProject, "not LADO", nil), http.StatusConflict},
		{"task not found", apierr.NotFound(domain.LadoRelayTaskNotFound, "No task"), http.StatusNotFound},
		{"no worker", apierr.Conflict(domain.LadoRelayNoWorker, "ended", map[string]any{"reason": domain.LadoRelayNoWorkerEnded}), http.StatusConflict},
		{"not execution", apierr.Conflict(domain.LadoRelayNotExecution, "plan worker", nil), http.StatusConflict},
		{"ambiguous", apierr.Conflict(domain.LadoRelayAmbiguous, "two workers", map[string]any{
			"candidates": []map[string]any{{"sessionId": "mer-7", "role": "part", "partId": "api"}},
		}), http.StatusConflict},
		{"delivery failed", apierr.Conflict(domain.LadoRelayDeliveryFailed, "exited", map[string]any{"relayId": "lado-relay-2"}), http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeLadoRelayService{err: tc.err}
			srv := newLadoTestServer(t, httpd.APIDeps{LadoRelay: svc})
			body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/relays",
				`{"sessionId":"o","task":"#1","message":"m","evidence":"abcd"}`)
			if status != tc.status {
				t.Fatalf("status = %d %s, want %d", status, body, tc.status)
			}
			got := decodeLadoJSON[envelope.APIError](t, body)
			var e *apierr.Error
			if !errors.As(tc.err, &e) {
				t.Fatalf("case error %v is no API error", tc.err)
			}
			if got.Code != e.Code {
				t.Fatalf("code = %q, want %q", got.Code, e.Code)
			}
			if len(e.Details) > 0 && len(got.Details) == 0 {
				t.Fatalf("details dropped: %s", body)
			}
		})
	}
}

func TestLadoAPI_ListTaskRelays(t *testing.T) {
	older := sampleLadoRelay()
	older.ID, older.CreatedAt = "lado-relay-0", ladoRelayTime.Add(-time.Hour)
	older.State, older.Failure, older.DeliveredAt = domain.LadoRelayFailed, "agent exited", time.Time{}
	svc := &fakeLadoRelayService{relays: []domain.LadoRelay{sampleLadoRelay(), older}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRelay: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1/relays", "")
	if status != http.StatusOK || svc.gotTask != "lado-task-1" {
		t.Fatalf("GET = %d %s, task %q", status, body, svc.gotTask)
	}
	got := decodeLadoJSON[controllers.LadoRelayListResponse](t, body)
	if len(got.Relays) != 2 || got.Relays[0].ID != "lado-relay-1" || got.Relays[1].ID != "lado-relay-0" {
		t.Fatalf("relays = %+v", got.Relays)
	}
	if r := got.Relays[1]; r.State != "failed" || r.Failure != "agent exited" || r.DeliveredAt != nil {
		t.Fatalf("failed relay = %+v", r)
	}
}

func TestLadoAPI_ListTaskRelaysEmptyAndMissing(t *testing.T) {
	svc := &fakeLadoRelayService{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRelay: svc})
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1/relays", "")
	if status != http.StatusOK || !strings.Contains(string(body), `"relays":[]`) {
		t.Fatalf("GET = %d %s", status, body)
	}

	svc.err = apierr.NotFound(domain.LadoRelayTaskNotFound, "No LADO task lado-task-x.")
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-x/relays", "")
	if status != http.StatusNotFound || decodeLadoJSON[envelope.APIError](t, body).Code != domain.LadoRelayTaskNotFound {
		t.Fatalf("GET missing = %d %s", status, body)
	}
}

func TestLadoAPI_RelayRoutesWithoutServiceAre501(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/relays", `{}`); status != http.StatusNotImplemented {
		t.Fatalf("POST = %d", status)
	}
	if _, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1/relays", ""); status != http.StatusNotImplemented {
		t.Fatalf("GET = %d", status)
	}
}
