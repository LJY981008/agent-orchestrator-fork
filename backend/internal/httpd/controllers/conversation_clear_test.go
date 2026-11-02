package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// clearingConversationService adds the optional /clear capability to the
// shared Chat double.
type clearingConversationService struct {
	*fakeConversationService
	result  chatsvc.ClearContextResult
	err     error
	cleared []domain.SessionID
}

func (f *clearingConversationService) ClearContext(_ context.Context, id domain.SessionID) (chatsvc.ClearContextResult, error) {
	f.cleared = append(f.cleared, id)
	return f.result, f.err
}

func clearRequest(t *testing.T, svc controllers.ConversationService) (int, []byte) {
	t.Helper()
	r := chi.NewRouter()
	(&controllers.ConversationsController{Svc: svc}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/p1-1/conversation/clear", nil))
	return rec.Code, rec.Body.Bytes()
}

func decodeClearError(t *testing.T, body []byte) (string, string) {
	t.Helper()
	var envelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return envelope.Code, envelope.Message
}

func TestClearConversationRouteReportsTheNewBranch(t *testing.T) {
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	svc := &clearingConversationService{
		fakeConversationService: &fakeConversationService{},
		result:                  chatsvc.ClearContextResult{ActiveBranchID: "branch-2", ClearedAt: at, Cleared: true},
	}
	status, body := clearRequest(t, svc)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", status, body)
	}
	var got controllers.ClearConversationResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if got.ActiveBranchID != "branch-2" || !got.Cleared || !got.ClearedAt.Equal(at) {
		t.Fatalf("response = %+v", got)
	}
	if len(svc.cleared) != 1 || svc.cleared[0] != "p1-1" {
		t.Fatalf("cleared sessions = %v", svc.cleared)
	}
}

func TestClearConversationRouteRefusesARunningTurn(t *testing.T) {
	svc := &clearingConversationService{
		fakeConversationService: &fakeConversationService{},
		err:                     fmt.Errorf("clear: %w", chatsvc.ErrTurnRunning),
	}
	status, body := clearRequest(t, svc)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", status, body)
	}
	code, message := decodeClearError(t, body)
	if code != "CHAT_TURN_RUNNING" || !strings.Contains(message, "clearing") || strings.Contains(message, "rolling back") {
		t.Fatalf("error = %q %q", code, message)
	}
}

func TestClearConversationRouteMapsSharedChatErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"missing session", ports.ErrSessionNotFound, http.StatusNotFound, "SESSION_NOT_FOUND"},
		{"no controller", chatsvc.ErrNoController, http.StatusConflict, "CHAT_CONTROLLER_NOT_READY"},
		{"handoff", chatsvc.ErrControllerHandoff, http.StatusConflict, "CHAT_INTERFACE_TRANSITION"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := clearRequest(t, &clearingConversationService{
				fakeConversationService: &fakeConversationService{}, err: tc.err,
			})
			if status != tc.status {
				t.Fatalf("status = %d, want %d (%s)", status, tc.status, body)
			}
			if code, _ := decodeClearError(t, body); code != tc.code {
				t.Fatalf("code = %q, want %q", code, tc.code)
			}
		})
	}
}

func TestClearConversationRouteIsNotImplementedWithoutTheCapability(t *testing.T) {
	if status, body := clearRequest(t, &fakeConversationService{}); status != http.StatusNotImplemented {
		t.Fatalf("status without ClearContext = %d, want 501 (%s)", status, body)
	}
	if status, body := clearRequest(t, nil); status != http.StatusNotImplemented {
		t.Fatalf("status without a service = %d, want 501 (%s)", status, body)
	}
}
