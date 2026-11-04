package httpd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/projectmemo"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

// memoProjects knows one project, "p1".
type memoProjects struct{ projectsvc.Manager }

func (memoProjects) Get(_ context.Context, id domain.ProjectID) (projectsvc.GetResult, error) {
	if id != "p1" {
		return projectsvc.GetResult{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	return projectsvc.GetResult{Status: "ok"}, nil
}

func memoRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	dataDir := t.TempDir()
	router := NewRouterWithControl(
		config.Config{DataDir: dataDir},
		discardLogger(),
		nil,
		APIDeps{Projects: memoProjects{}},
		ControlDeps{},
	)
	return router, dataDir
}

func memoCall(t *testing.T, router http.Handler, method, path, body string) (int, []byte) {
	t.Helper()
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func memoErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode error %s: %v", body, err)
	}
	return e.Code
}

func decodeMemo(t *testing.T, body []byte) controllers.ProjectMemoResponse {
	t.Helper()
	var got controllers.ProjectMemoResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode memo %s: %v", body, err)
	}
	return got
}

func saveBody(t *testing.T, body, base string) string {
	t.Helper()
	data, err := json.Marshal(controllers.SaveProjectMemoRequest{Body: body, BaseRevision: base})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const memoPath = "/api/v1/projects/p1/orchestrator-memo"

func TestProjectMemoRoutesSaveReadAndRestore(t *testing.T) {
	router, dataDir := memoRouter(t)

	status, body := memoCall(t, router, http.MethodGet, memoPath, "")
	if status != http.StatusOK {
		t.Fatalf("GET empty status = %d (%s)", status, body)
	}
	// An empty view: memo is null, revisions is an empty list, not null.
	if !strings.Contains(string(body), `"memo":null`) || !strings.Contains(string(body), `"revisions":[]`) {
		t.Fatalf("empty view = %s", body)
	}
	empty := decodeMemo(t, body)
	if empty.MaxChars != projectmemo.MaxRunes || empty.StaleAfterDays != 7 {
		t.Fatalf("limits = %d chars, %d days", empty.MaxChars, empty.StaleAfterDays)
	}

	status, body = memoCall(t, router, http.MethodPut, memoPath, saveBody(t, "first: 범위는 api 파트", ""))
	if status != http.StatusOK {
		t.Fatalf("PUT first status = %d (%s)", status, body)
	}
	first := decodeMemo(t, body)
	if first.Memo == nil || first.Memo.Body != "first: 범위는 api 파트" || first.Memo.Source != "user" ||
		first.Memo.Chars != 17 || first.Memo.Stale || len(first.Revisions) != 0 {
		t.Fatalf("first save = %+v", first)
	}

	status, body = memoCall(t, router, http.MethodPut, memoPath, saveBody(t, "second ghp_abcdefghijklmnopqrstuvwxyz0123", first.Memo.Revision))
	if status != http.StatusOK {
		t.Fatalf("PUT second status = %d (%s)", status, body)
	}
	second := decodeMemo(t, body)
	if second.Memo == nil || second.Memo.Body != "second "+projectmemo.Redacted {
		t.Fatalf("second memo = %+v", second.Memo)
	}
	if len(second.Revisions) != 1 || second.Revisions[0].Revision != first.Memo.Revision || second.Revisions[0].Chars != 17 {
		t.Fatalf("revisions = %+v", second.Revisions)
	}

	// Restore: read the old revision and save its body.
	status, body = memoCall(t, router, http.MethodGet, memoPath+"/revisions/"+first.Memo.Revision, "")
	if status != http.StatusOK {
		t.Fatalf("GET revision status = %d (%s)", status, body)
	}
	var rev controllers.ProjectMemoRevisionResponse
	if err := json.Unmarshal(body, &rev); err != nil {
		t.Fatal(err)
	}
	if rev.Memo.Revision != first.Memo.Revision || rev.Memo.Body != first.Memo.Body {
		t.Fatalf("revision = %+v", rev.Memo)
	}
	status, body = memoCall(t, router, http.MethodPut, memoPath, saveBody(t, rev.Memo.Body, second.Memo.Revision))
	if status != http.StatusOK {
		t.Fatalf("PUT restore status = %d (%s)", status, body)
	}
	restored := decodeMemo(t, body)
	if restored.Memo.Body != first.Memo.Body || len(restored.Revisions) != 2 {
		t.Fatalf("restored = %+v", restored)
	}

	// The store wrote under the API's data dir.
	if _, err := os.Stat(filepath.Join(dataDir, "project-memos", "p1", "current.json")); err != nil {
		t.Fatalf("memo file: %v", err)
	}
}

func TestProjectMemoRoutesRejectBadWrites(t *testing.T) {
	router, _ := memoRouter(t)
	status, body := memoCall(t, router, http.MethodPut, memoPath, saveBody(t, "base", ""))
	if status != http.StatusOK {
		t.Fatalf("seed status = %d (%s)", status, body)
	}
	current := decodeMemo(t, body).Memo.Revision

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		status int
		code   string
	}{
		{"too long", http.MethodPut, memoPath, saveBody(t, strings.Repeat("가", projectmemo.MaxRunes+1), current), http.StatusBadRequest, "PROJECT_MEMO_TOO_LONG"},
		{"empty body", http.MethodPut, memoPath, saveBody(t, "   ", current), http.StatusBadRequest, "PROJECT_MEMO_INVALID"},
		{"stale base", http.MethodPut, memoPath, saveBody(t, "edit", ""), http.StatusConflict, "PROJECT_MEMO_CONFLICT"},
		{"wrong base", http.MethodPut, memoPath, saveBody(t, "edit", "20260101T000000.000000000Z-00000000"), http.StatusConflict, "PROJECT_MEMO_CONFLICT"},
		{"unknown field", http.MethodPut, memoPath, `{"body":"x","baseRevision":"","source":"clear"}`, http.StatusBadRequest, "INVALID_JSON"},
		{"unknown project get", http.MethodGet, "/api/v1/projects/nope/orchestrator-memo", "", http.StatusNotFound, "PROJECT_NOT_FOUND"},
		{"unknown project put", http.MethodPut, "/api/v1/projects/nope/orchestrator-memo", saveBody(t, "x", ""), http.StatusNotFound, "PROJECT_NOT_FOUND"},
		{"unknown project revision", http.MethodGet, "/api/v1/projects/nope/orchestrator-memo/revisions/" + current, "", http.StatusNotFound, "PROJECT_NOT_FOUND"},
		{"missing revision", http.MethodGet, memoPath + "/revisions/20260101T000000.000000000Z-00000000", "", http.StatusNotFound, "PROJECT_MEMO_NOT_FOUND"},
		{"malformed revision", http.MethodGet, memoPath + "/revisions/not-a-revision", "", http.StatusBadRequest, "PROJECT_MEMO_INVALID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := memoCall(t, router, tc.method, tc.path, tc.body)
			if status != tc.status {
				t.Fatalf("status = %d, want %d (%s)", status, tc.status, body)
			}
			if code := memoErrorCode(t, body); code != tc.code {
				t.Fatalf("code = %q, want %q", code, tc.code)
			}
		})
	}

	// None of the refused writes changed the memo.
	_, body = memoCall(t, router, http.MethodGet, memoPath, "")
	if got := decodeMemo(t, body); got.Memo == nil || got.Memo.Revision != current || got.Memo.Body != "base" {
		t.Fatalf("memo after refusals = %+v", got.Memo)
	}
}

func TestProjectMemoReportsAStaleMemo(t *testing.T) {
	dataDir := t.TempDir()
	old := time.Now().Add(-8 * 24 * time.Hour)
	if _, err := projectmemo.New(dataDir).WithClock(func() time.Time { return old }).
		Save("p1", projectmemo.SaveInput{Body: "old", Source: projectmemo.SourceClear}); err != nil {
		t.Fatal(err)
	}
	router := NewRouterWithControl(config.Config{DataDir: dataDir}, discardLogger(), nil, APIDeps{Projects: memoProjects{}}, ControlDeps{})
	_, body := memoCall(t, router, http.MethodGet, memoPath, "")
	got := decodeMemo(t, body)
	if got.Memo == nil || !got.Memo.Stale || got.Memo.Source != "clear" {
		t.Fatalf("memo = %+v", got.Memo)
	}
}

func TestProjectMemoRoutesAreNotImplementedWithoutAService(t *testing.T) {
	// No project manager: the default service is not built.
	router := NewRouterWithControl(config.Config{DataDir: t.TempDir()}, discardLogger(), nil, APIDeps{}, ControlDeps{})
	if status, body := memoCall(t, router, http.MethodGet, memoPath, ""); status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (%s)", status, body)
	}
}

// slowClearer finishes /clear after the ordinary request budget would have
// run out, as an orchestrator /clear that first writes its memo does. It stops
// early, with the context's error, if the request context ends first.
type slowClearer struct {
	controllers.ConversationService
	delay  time.Duration
	budget chan time.Duration
}

func (s *slowClearer) ClearContext(ctx context.Context, _ domain.SessionID) (chatsvc.ClearContextResult, error) {
	s.budget <- remainingRequestBudget(ctx)
	select {
	case <-ctx.Done():
		return chatsvc.ClearContextResult{}, ctx.Err()
	case <-time.After(s.delay):
	}
	return chatsvc.ClearContextResult{
		ActiveBranchID: "branch-2",
		ClearedAt:      time.Now().UTC(),
		Cleared:        true,
		Memo:           projectmemo.ClearOutcome{Status: projectmemo.ClearSaved, Revision: "rev-1", UpdatedAt: time.Now().UTC()},
	}, nil
}

func TestClearRouteOutlivesTheOrdinaryRequestTimeout(t *testing.T) {
	const configuredTimeout = 50 * time.Millisecond
	clearer := &slowClearer{delay: 4 * configuredTimeout, budget: make(chan time.Duration, 1)}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: configuredTimeout},
		discardLogger(),
		nil,
		APIDeps{Conversations: clearer},
		ControlDeps{},
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/ao-1/conversation/clear", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if budget := <-clearer.budget; budget < clearRouteTimeout-time.Minute || budget > clearRouteTimeout {
		t.Fatalf("clear budget = %s, want about %s", budget, clearRouteTimeout)
	}
	var got controllers.ClearConversationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Cleared || got.Memo == nil || got.Memo.Status != "saved" || got.Memo.Revision != "rev-1" || got.Memo.UpdatedAt == nil {
		t.Fatalf("clear response = %s", rec.Body.String())
	}

	// A longer configured timeout wins over the clear floor.
	long := NewRouterWithControl(config.Config{RequestTimeout: 2 * clearRouteTimeout}, discardLogger(), nil,
		APIDeps{Conversations: &slowClearer{budget: clearer.budget}}, ControlDeps{})
	long.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/sessions/ao-1/conversation/clear", nil))
	if budget := <-clearer.budget; budget <= clearRouteTimeout {
		t.Fatalf("clear budget with a long configured timeout = %s, want more than %s", budget, clearRouteTimeout)
	}
}
