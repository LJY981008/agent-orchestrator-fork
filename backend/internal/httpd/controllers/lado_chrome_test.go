package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladochrome"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

const ladoClaudeChromePath = "/api/v1/lado/claude-chrome"

func newLadoChromeService(t *testing.T) (*ladochrome.Service, *sqlite.Store) {
	t.Helper()
	store := sqlitetest.MustOpenAt(t, t.TempDir())
	return ladochrome.New(ladochrome.Options{DataDir: t.TempDir(), Store: store}), store
}

func decodeLadoChrome(t *testing.T, body []byte) controllers.LadoClaudeChromeSettingsResponse {
	t.Helper()
	var resp controllers.LadoClaudeChromeSettingsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return resp
}

func TestLadoAPI_ClaudeChromeDefaultsAndValidates(t *testing.T) {
	chrome, _ := newLadoChromeService(t)
	srv := newLadoTestServer(t, httpd.APIDeps{LadoClaudeChrome: chrome})

	body, status, _ := doRequest(t, srv, "GET", ladoClaudeChromePath, "")
	if status != http.StatusOK {
		t.Fatalf("GET status = %d, body %s", status, body)
	}
	if got := decodeLadoChrome(t, body); !got.ChatSessions || got.LadoWorkers {
		t.Fatalf("default = %+v, want chat on and workers off", got)
	}

	body, status = ladoHeaderRequest(t, srv, "PUT", ladoClaudeChromePath, `{"chatSessions":false}`, nil)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "LADO_CLAUDE_CHROME_INVALID")
}

func TestLadoAPI_ClaudeChromeTurnsOffWithoutProofAndOnOnlyWithPresence(t *testing.T) {
	chrome, _ := newLadoChromeService(t)
	srv := newLadoTestServer(t, httpd.APIDeps{LadoClaudeChrome: chrome})

	body, status := ladoHeaderRequest(t, srv, "PUT", ladoClaudeChromePath, `{"chatSessions":false,"ladoWorkers":false}`, nil)
	if status != http.StatusOK {
		t.Fatalf("turn off status = %d, body %s", status, body)
	}
	if got := decodeLadoChrome(t, body); got.ChatSessions || got.LadoWorkers {
		t.Fatalf("after turn off = %+v", got)
	}

	// No presence verifier: turning on is refused.
	body, status = ladoHeaderRequest(t, srv, "PUT", ladoClaudeChromePath, `{"chatSessions":true,"ladoWorkers":false}`, nil)
	wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_UNAVAILABLE")
	if got, _ := chrome.Settings(context.Background()); got.ChatSessions {
		t.Fatalf("refused turn on changed the setting: %+v", got)
	}
}

func TestLadoAPI_ClaudeChromeTurnOnSignsTheBodyAndRefusesAReplay(t *testing.T) {
	chrome, _ := newLadoChromeService(t)
	presence := &recordingPresence{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoClaudeChrome: chrome, LadoPresence: presence})
	header := map[string]string{"X-LADO-Presence": "v1.proof-from-the-app"}

	if _, status := ladoHeaderRequest(t, srv, "PUT", ladoClaudeChromePath, `{"chatSessions":false,"ladoWorkers":false}`, nil); status != http.StatusOK {
		t.Fatalf("turn off status = %d", status)
	}
	sent := `{"chatSessions":true,"ladoWorkers":true}`
	body, status := ladoHeaderRequest(t, srv, "PUT", ladoClaudeChromePath, sent, header)
	if status != http.StatusOK {
		t.Fatalf("turn on status = %d, body %s", status, body)
	}
	if got := decodeLadoChrome(t, body); !got.ChatSessions || !got.LadoWorkers {
		t.Fatalf("after turn on = %+v", got)
	}
	if presence.action != domain.LadoPresenceClaudeChromeOn || presence.subject != "chatSessions,ladoWorkers" ||
		presence.method != "PUT" || presence.path != ladoClaudeChromePath || string(presence.body) != sent {
		t.Fatalf("presence saw %+v", presence)
	}

	// recordingPresence returns the same nonce every time: the second turn on
	// reuses a proof.
	if _, status := ladoHeaderRequest(t, srv, "PUT", ladoClaudeChromePath, `{"chatSessions":false,"ladoWorkers":false}`, nil); status != http.StatusOK {
		t.Fatalf("turn off again status = %d", status)
	}
	body, status = ladoHeaderRequest(t, srv, "PUT", ladoClaudeChromePath, sent, header)
	wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_REPLAYED")
	if got, _ := chrome.Settings(context.Background()); got.ChatSessions || got.LadoWorkers {
		t.Fatalf("replayed turn on changed the setting: %+v", got)
	}
}

func TestLadoAPI_SessionClaudeChrome(t *testing.T) {
	chrome, store := newLadoChromeService(t)
	ctx := context.Background()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "proj", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	created, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "proj", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		Metadata:  domain.SessionMetadata{WorkspacePath: t.TempDir(), Branch: "ao/session"},
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoClaudeChrome: chrome})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/sessions/"+string(created.ID)+"/claude-chrome", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	var got controllers.LadoSessionClaudeChromeResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	want := controllers.LadoSessionClaudeChromeResponse{
		SessionID: string(created.ID), Supported: true, Enabled: true, Basis: "setting", Kind: "chat", SettingOn: true,
	}
	if got != want {
		t.Fatalf("session state = %+v, want %+v", got, want)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/sessions/missing-1/claude-chrome", "")
	wantLadoErrorCode(t, body, status, http.StatusNotFound, "SESSION_NOT_FOUND")
}

func TestLadoAPI_ClaudeChromeIsNotImplementedWithoutAService(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, req := range []struct{ method, path, body string }{
		{"GET", ladoClaudeChromePath, ""},
		{"PUT", ladoClaudeChromePath, `{"chatSessions":false,"ladoWorkers":false}`},
		{"GET", "/api/v1/lado/sessions/s-1/claude-chrome", ""},
	} {
		if _, status := ladoHeaderRequest(t, srv, req.method, req.path, req.body, nil); status != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501", req.method, req.path, status)
		}
	}
}
