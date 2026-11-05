package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/projectmemo"
)

type fakeProjectMemoService struct {
	memo      projectmemo.Memo
	ok        bool
	revisions []projectmemo.Memo
	err       error

	savedBody, savedBase string
	revisionAsked        string
}

func (f *fakeProjectMemoService) Current(context.Context, domain.ProjectID) (projectmemo.Memo, bool, []projectmemo.Memo, error) {
	return f.memo, f.ok, f.revisions, f.err
}

func (f *fakeProjectMemoService) Save(_ context.Context, _ domain.ProjectID, body, base string) (projectmemo.Memo, error) {
	f.savedBody, f.savedBase = body, base
	if f.err != nil {
		return projectmemo.Memo{}, f.err
	}
	f.memo, f.ok = projectmemo.Memo{Revision: "rev-2", Source: projectmemo.SourceUser, Body: body, UpdatedAt: time.Now()}, true
	return f.memo, nil
}

func (f *fakeProjectMemoService) Revision(_ context.Context, _ domain.ProjectID, rev string) (projectmemo.Memo, error) {
	f.revisionAsked = rev
	return f.memo, f.err
}

func projectMemoRequest(t *testing.T, c *controllers.ProjectMemoController, method, path, body string) (int, []byte) {
	t.Helper()
	r := chi.NewRouter()
	c.Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec.Code, rec.Body.Bytes()
}

func TestProjectMemoControllerRendersTheView(t *testing.T) {
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	svc := &fakeProjectMemoService{
		memo: projectmemo.Memo{Revision: "rev-1", Source: projectmemo.SourceClear, Body: "다음: 통합", UpdatedAt: now.Add(-8 * 24 * time.Hour)},
		ok:   true,
		revisions: []projectmemo.Memo{
			{Revision: "rev-0", Source: projectmemo.SourceUser, Body: "abc", UpdatedAt: now.Add(-9 * 24 * time.Hour)},
		},
	}
	c := &controllers.ProjectMemoController{Svc: svc, Now: func() time.Time { return now }}
	status, body := projectMemoRequest(t, c, http.MethodGet, "/projects/p1/orchestrator-memo", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d (%s)", status, body)
	}
	var got controllers.ProjectMemoResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Memo == nil || got.Memo.Chars != 6 || !got.Memo.Stale || got.Memo.Source != "clear" {
		t.Fatalf("memo = %+v", got.Memo)
	}
	if len(got.Revisions) != 1 || got.Revisions[0].Revision != "rev-0" || got.Revisions[0].Chars != 3 {
		t.Fatalf("revisions = %+v", got.Revisions)
	}
	if strings.Contains(string(body), `"abc"`) {
		t.Fatalf("revision summaries carry no body: %s", body)
	}

	status, body = projectMemoRequest(t, c, http.MethodGet, "/projects/p1/orchestrator-memo/revisions/rev-1", "")
	if status != http.StatusOK || svc.revisionAsked != "rev-1" || !strings.Contains(string(body), `"body":"다음: 통합"`) {
		t.Fatalf("revision status = %d, asked %q, body %s", status, svc.revisionAsked, body)
	}
}

func TestProjectMemoControllerSavesAsAPerson(t *testing.T) {
	svc := &fakeProjectMemoService{}
	c := &controllers.ProjectMemoController{Svc: svc}
	status, body := projectMemoRequest(t, c, http.MethodPut, "/projects/p1/orchestrator-memo", `{"body":"memo","baseRevision":"rev-1"}`)
	if status != http.StatusOK || svc.savedBody != "memo" || svc.savedBase != "rev-1" {
		t.Fatalf("status = %d, saved %q on %q (%s)", status, svc.savedBody, svc.savedBase, body)
	}
	if !strings.Contains(string(body), `"revision":"rev-2"`) {
		t.Fatalf("body = %s", body)
	}

	for _, bad := range []string{`{"body":"x","baseRevision":"","extra":1}`, `{"body":`, `{"body":"x","baseRevision":""}{}`} {
		status, body := projectMemoRequest(t, c, http.MethodPut, "/projects/p1/orchestrator-memo", bad)
		if status != http.StatusBadRequest || !strings.Contains(string(body), "INVALID_JSON") {
			t.Fatalf("%s: status = %d (%s)", bad, status, body)
		}
	}
	huge := `{"body":"` + strings.Repeat("a", 300<<10) + `","baseRevision":""}`
	if status, body := projectMemoRequest(t, c, http.MethodPut, "/projects/p1/orchestrator-memo", huge); status != http.StatusBadRequest ||
		!strings.Contains(string(body), "PROJECT_MEMO_TOO_LONG") {
		t.Fatalf("huge body: status = %d (%s)", status, body)
	}
}

func TestProjectMemoControllerPassesServiceErrorsThrough(t *testing.T) {
	svc := &fakeProjectMemoService{err: apierr.Conflict("PROJECT_MEMO_CONFLICT", "changed", nil)}
	c := &controllers.ProjectMemoController{Svc: svc}
	status, body := projectMemoRequest(t, c, http.MethodPut, "/projects/p1/orchestrator-memo", `{"body":"x","baseRevision":""}`)
	if status != http.StatusConflict || !strings.Contains(string(body), "PROJECT_MEMO_CONFLICT") {
		t.Fatalf("status = %d (%s)", status, body)
	}
}

func TestProjectMemoControllerIsNotImplementedWithoutAService(t *testing.T) {
	c := &controllers.ProjectMemoController{}
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/projects/p1/orchestrator-memo", ""},
		{http.MethodPut, "/projects/p1/orchestrator-memo", `{"body":"x","baseRevision":""}`},
		{http.MethodGet, "/projects/p1/orchestrator-memo/revisions/rev-1", ""},
	} {
		if status, body := projectMemoRequest(t, c, tc.method, tc.path, tc.body); status != http.StatusNotImplemented {
			t.Fatalf("%s %s status = %d, want 501 (%s)", tc.method, tc.path, status, body)
		}
	}
}
