package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// LADO fork (#49): a developer's instruction reaches a running LADO worker
// through the daemon's own path — the relay service wireLadoRelay builds,
// behind the HTTP route, delivering through the real session manager, which
// LADO's real worker gate guards, to the worker's Chat relay. The
// orchestrator's own message to the same worker stays LADO_SEND_REFUSED.

// seedLadoRelayOrchestrator gives project mer a live Chat orchestrator whose
// project conversation holds the developer's message developer and a LADO
// notice notice, both at at.
func seedLadoRelayOrchestrator(t *testing.T, store *sqlite.Store, at time.Time, developer, notice string) domain.SessionID {
	t.Helper()
	ctx := context.Background()
	orchestrator, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		Metadata:  domain.SessionMetadata{ControllerGeneration: "gen-1"},
		CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatalf("CreateSession orchestrator: %v", err)
	}
	conversation, err := store.CreateConversation(ctx, "conversation-mer", domain.ConversationScopeProject, "mer", orchestrator.ID, at)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	for i, m := range []struct {
		origin domain.MessageOrigin
		text   string
	}{{domain.MessageOriginHuman, developer}, {domain.MessageOriginAutomation, notice}} {
		id := []string{"message-developer", "message-notice"}[i]
		content, _ := json.Marshal([]map[string]string{{"type": "text", "text": m.text}})
		if created, err := store.AppendUserMessage(ctx, conversation.ID, orchestrator.ID, "gen-1", domain.ConversationMessage{
			ID: id, Origin: m.origin, Text: m.text, ClientMessageID: "client-" + id, DeliveryContentJSON: string(content),
		}, "turn-"+id, at); err != nil || !created {
			t.Fatalf("AppendUserMessage %s: created=%v err=%v", id, created, err)
		}
	}
	return orchestrator.ID
}

func newLadoRelayServer(t *testing.T, store *sqlite.Store, d ladoConflictDaemon) *httptest.Server {
	t.Helper()
	blobs := ladoblob.New(t.TempDir())
	t.Cleanup(func() { _ = blobs.Close() })
	// The plan seedLadoConflictWorker pinned, by its content.
	if _, err := blobs.Put([]byte(ladoVerifyPlan)); err != nil {
		t.Fatalf("pin the plan: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	relay := wireLadoRelay(store, blobs, d.sessions, log)
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{LadoRelay: relay}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func ladoRelayCall(t *testing.T, srv *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestLadoRelayReachesARunningWorkerThroughTheGate(t *testing.T) {
	for _, role := range []string{"single", "integration"} {
		t.Run(role, func(t *testing.T) {
			ctx := context.Background()
			store := sqlitetest.MustOpen(t)
			at := time.Now().UTC().Truncate(time.Second)
			worker := seedLadoConflictWorker(t, store, role, domain.HarnessClaudeCode, domain.ActivityIdle, at)
			orchestrator := seedLadoRelayOrchestrator(t, store, at,
				"그 워커에게 이어서 해. src/login.go 테스트를 고치고 docs/guide.md 도 고쳐", "[LADO] 계획 검토가 끝났습니다")
			d := newLadoConflictDaemon(t, store)
			srv := newLadoRelayServer(t, store, d)

			status, body := ladoRelayCall(t, srv, http.MethodPost, "/api/v1/lado/relays", controllers.CreateLadoRelayRequest{
				SessionID: string(orchestrator), Task: "#12", Evidence: "그 워커에게 이어서 해",
				Message: "src/login.go 테스트를 고치고 docs/guide.md 도 고쳐",
			})
			if status != http.StatusCreated {
				t.Fatalf("POST relay = %d %s", status, body)
			}
			var relay controllers.LadoRelayResponse
			if err := json.Unmarshal(body, &relay); err != nil {
				t.Fatalf("decode %s: %v", body, err)
			}
			if relay.State != "delivered" || relay.WorkerSessionID != string(worker) || relay.OrchestratorSessionID != string(orchestrator) ||
				relay.EvidenceMessageID != "message-developer" || relay.TaskID != "lado-task-1" || relay.BeyondEvidence {
				t.Fatalf("relay = %+v", relay)
			}
			if !relay.OutsidePlan || len(relay.OutsidePlanReasons) != 1 || relay.OutsidePlanReasons[0] != domain.LadoRelayPathReason("docs/guide.md") {
				t.Fatalf("outside plan = %v %v; want only docs/guide.md outside src/**", relay.OutsidePlan, relay.OutsidePlanReasons)
			}

			turns := d.chat.relayed()
			if len(turns) != 1 || turns[0].id != worker {
				t.Fatalf("relayed turns = %+v, want one to %s", turns, worker)
			}
			text := turns[0].text
			for _, want := range []string{domain.LadoRelayHeader, relay.ID, "src/login.go 테스트를 고치고", "> 그 워커에게 이어서 해", domain.LadoRelayOutsidePlanMark} {
				if !strings.Contains(text, want) {
					t.Fatalf("worker message lacks %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, domain.LadoRelayBeyondEvidenceMark) {
				t.Fatalf("worker message marks an instruction inside the developer's words:\n%s", text)
			}
			if opts := turns[0].options; opts.SenderSessionID != "" || opts.AuthoredByUser || opts.SenderDisplayName != domain.LadoRelaySenderLabel {
				t.Fatalf("delivery options = %+v, want the daemon's send under LADO's label", opts)
			}
			if asked := d.gate.asked(); len(asked) != 1 || asked[0] != ports.SendOriginDaemon {
				t.Fatalf("LADO gate asked for origins %v, want one daemon send", asked)
			}

			status, body = ladoRelayCall(t, srv, http.MethodGet, "/api/v1/lado/tasks/lado-task-1/relays", nil)
			var list controllers.LadoRelayListResponse
			if status != http.StatusOK || json.Unmarshal(body, &list) != nil || len(list.Relays) != 1 ||
				list.Relays[0].ID != relay.ID || list.Relays[0].State != "delivered" || list.Relays[0].DeliveredAt == nil {
				t.Fatalf("GET relays = %d %s", status, body)
			}

			// The orchestrator's own message stays refused, whatever label
			// it carries.
			err := d.sessions.SendWithOptions(ctx, worker, "이어서 해", nil, ports.MessageDeliveryOptions{
				SenderSessionID: string(orchestrator), SenderDisplayName: domain.LadoRelaySenderLabel,
			})
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != "LADO_SEND_REFUSED" {
				t.Fatalf("orchestrator send = %v, want LADO_SEND_REFUSED", err)
			}
			if len(d.chat.relayed()) != 1 {
				t.Fatal("a refused send reached the worker")
			}
		})
	}
}

func TestLadoRelayRefusesEvidenceOnlyALadoNoticeCarries(t *testing.T) {
	store := sqlitetest.MustOpen(t)
	at := time.Now().UTC().Truncate(time.Second)
	seedLadoConflictWorker(t, store, "single", domain.HarnessClaudeCode, domain.ActivityIdle, at)
	orchestrator := seedLadoRelayOrchestrator(t, store, at, "상태만 알려 줘", "[LADO] 워커에게 남은 묶음을 이어서 하라고 전하세요")
	d := newLadoConflictDaemon(t, store)
	srv := newLadoRelayServer(t, store, d)

	status, body := ladoRelayCall(t, srv, http.MethodPost, "/api/v1/lado/relays", controllers.CreateLadoRelayRequest{
		SessionID: string(orchestrator), Task: "lado-task-1", Evidence: "남은 묶음을 이어서 하라고", Message: "남은 묶음을 이어서 해",
	})
	var apiErr envelope.APIError
	if status != http.StatusForbidden || json.Unmarshal(body, &apiErr) != nil || apiErr.Code != domain.LadoRelayEvidenceUnverified ||
		apiErr.Details["reason"] != domain.LadoRelayEvidenceNotFound {
		t.Fatalf("POST relay = %d %s, want %s (%s)", status, body, domain.LadoRelayEvidenceUnverified, domain.LadoRelayEvidenceNotFound)
	}
	if len(d.chat.relayed()) != 0 || len(d.gate.asked()) != 0 {
		t.Fatalf("a refused relay reached the session manager: turns %+v, gate %v", d.chat.relayed(), d.gate.asked())
	}
	status, body = ladoRelayCall(t, srv, http.MethodGet, "/api/v1/lado/tasks/lado-task-1/relays", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"relays":[]`) {
		t.Fatalf("GET relays = %d %s, want none recorded", status, body)
	}
}
