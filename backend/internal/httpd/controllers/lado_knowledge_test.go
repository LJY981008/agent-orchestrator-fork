package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoknowledge"
)

var knowledgeT0 = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

// fakeLadoKnowledge records what the controller passes and answers with
// canned values or errors.
type fakeLadoKnowledge struct {
	index      ladoknowledge.Index
	importable []ladoknowledge.Importable
	entry      domain.LadoKnowledgeEntry
	result     ladoknowledge.ImportResult
	err        error
	claimErr   error

	claims     int
	writes     []ladoknowledge.WriteInput
	memoryPath *string
	fromID     domain.ProjectID
	gotProject domain.ProjectID
}

func (f *fakeLadoKnowledge) Index(_ context.Context, id domain.ProjectID) (ladoknowledge.Index, error) {
	f.gotProject = id
	return f.index, f.err
}

func (f *fakeLadoKnowledge) Importable(context.Context, domain.ProjectID) ([]ladoknowledge.Importable, error) {
	return f.importable, f.err
}

func (f *fakeLadoKnowledge) Claim(_ context.Context, id domain.ProjectID) error {
	f.claims++
	f.gotProject = id
	return f.claimErr
}

func (f *fakeLadoKnowledge) Write(_ context.Context, in ladoknowledge.WriteInput) (domain.LadoKnowledgeEntry, error) {
	f.writes = append(f.writes, in)
	return f.entry, f.err
}

func (f *fakeLadoKnowledge) ImportClaudeMemory(_ context.Context, id domain.ProjectID, path string) (ladoknowledge.ImportResult, error) {
	f.gotProject, f.memoryPath = id, &path
	return f.result, f.err
}

func (f *fakeLadoKnowledge) ImportProject(_ context.Context, id, fromID domain.ProjectID) (ladoknowledge.ImportResult, error) {
	f.gotProject, f.fromID = id, fromID
	return f.result, f.err
}

func decodeLadoAPIError(t *testing.T, body []byte) envelope.APIError {
	t.Helper()
	var apiErr envelope.APIError
	if err := json.Unmarshal(body, &apiErr); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return apiErr
}

func TestLadoAPI_KnowledgeRoutesAnswerNotImplementedWithoutAService(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/api/v1/lado/projects/app/knowledge", ""},
		{"POST", "/api/v1/lado/projects/app/knowledge/imports", `{"source":"claude_memory"}`},
		{"POST", "/api/v1/lado/projects/app/knowledge/claim", ""},
		{"PUT", "/api/v1/lado/knowledge/notes/build", `{"sessionId":"app-1","content":"x"}`},
	} {
		body, status, _ := doRequest(t, srv, route.method, route.path, route.body)
		if status != http.StatusNotImplemented {
			t.Fatalf("%s %s status = %d, body %s", route.method, route.path, status, body)
		}
	}
}

func TestLadoAPI_GetKnowledgeListsNotesOwnerAndImportable(t *testing.T) {
	svc := &fakeLadoKnowledge{
		index: ladoknowledge.Index{
			ProjectID: "app", Dir: "/state/lado/knowledge/app",
			Owner: &ladoknowledge.Owner{ProjectID: "app", Path: "/src/app", RootCommit: "abc", ClaimedAt: knowledgeT0},
			Notes: []domain.LadoKnowledgeEntry{
				{Slug: "build", Title: "빌드", Kind: domain.LadoKnowledgeConvention, Revision: 3, UpdatedAt: knowledgeT0, Author: "app-1", State: domain.LadoKnowledgeStale},
				{Slug: "memo", Title: "Memo", Kind: domain.LadoKnowledgeResearch, Revision: 1, UpdatedAt: knowledgeT0, Author: "import:claude_memory"},
			},
			Truncated: true,
		},
		importable: []ladoknowledge.Importable{{ProjectID: "old-app", Notes: 4, OwnerPath: "/src/old", Registered: false}},
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoKnowledge: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/app/knowledge", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoKnowledgeResponse](t, body)
	if svc.gotProject != "app" || got.ProjectID != "app" || got.Dir != "/state/lado/knowledge/app" || got.Foreign || !got.Truncated {
		t.Fatalf("response = %+v (service got %q)", got, svc.gotProject)
	}
	if got.Owner == nil || got.Owner.Path != "/src/app" || got.Owner.RootCommit != "abc" || got.Owner.ProjectID != "app" {
		t.Fatalf("owner = %+v", got.Owner)
	}
	if len(got.Notes) != 2 || got.Notes[0].Slug != "build" || got.Notes[0].Kind != "convention" || got.Notes[0].Revision != 3 || got.Notes[0].State != "stale" || got.Notes[0].Author != "app-1" {
		t.Fatalf("notes = %+v", got.Notes)
	}
	// A note the service left without a state reads as unchecked.
	if got.Notes[1].State != "unchecked" {
		t.Fatalf("stateless note = %+v", got.Notes[1])
	}
	if len(got.Importable) != 1 || got.Importable[0].ProjectID != "old-app" || got.Importable[0].Notes != 4 || got.Importable[0].OwnerPath != "/src/old" || got.Importable[0].Registered {
		t.Fatalf("importable = %+v", got.Importable)
	}
}

func TestLadoAPI_GetKnowledgeSendsEmptyListsAndForeignOwner(t *testing.T) {
	svc := &fakeLadoKnowledge{index: ladoknowledge.Index{
		ProjectID: "app", Dir: "/k/app", Foreign: true,
		Owner: &ladoknowledge.Owner{ProjectID: "other", Path: "/src/other"},
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoKnowledge: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/app/knowledge", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if !strings.Contains(string(body), `"notes":[]`) || !strings.Contains(string(body), `"importable":[]`) {
		t.Fatalf("empty lists must be [] not null: %s", body)
	}
	got := decodeLadoJSON[controllers.LadoKnowledgeResponse](t, body)
	if !got.Foreign || got.Owner == nil || got.Owner.Path != "/src/other" || got.Owner.RootCommit != "" {
		t.Fatalf("foreign response = %+v", got)
	}

	// No owner.json yet: owner is left out.
	svc.index = ladoknowledge.Index{ProjectID: "app", Dir: "/k/app"}
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/app/knowledge", "")
	if strings.Contains(string(body), `"owner"`) {
		t.Fatalf("owner without owner.json: %s", body)
	}
}

func TestLadoAPI_GetKnowledgePassesServiceErrors(t *testing.T) {
	svc := &fakeLadoKnowledge{err: apierr.NotFound(ladoknowledge.CodeProjectNotFound, "Project not found")}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoKnowledge: svc})
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/nope/knowledge", "")
	wantLadoErrorCode(t, body, status, http.StatusNotFound, ladoknowledge.CodeProjectNotFound)
}

func TestLadoAPI_ClaimKnowledgeClaimsThenAnswersTheFolder(t *testing.T) {
	svc := &fakeLadoKnowledge{index: ladoknowledge.Index{
		ProjectID: "app", Dir: "/k/app",
		Owner: &ladoknowledge.Owner{ProjectID: "app", Path: "/src/app"},
		Notes: []domain.LadoKnowledgeEntry{{Slug: "build", Title: "Build", Kind: domain.LadoKnowledgeMap, Revision: 1, State: domain.LadoKnowledgeFresh}},
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoKnowledge: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/projects/app/knowledge/claim", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoKnowledgeResponse](t, body)
	if svc.claims != 1 || svc.gotProject != "app" || got.Foreign || len(got.Notes) != 1 || got.Notes[0].State != "fresh" {
		t.Fatalf("claims = %d, response = %+v", svc.claims, got)
	}

	svc.claimErr = apierr.NotFound(ladoknowledge.CodeProjectNotFound, "Project not found")
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/nope/knowledge/claim", "")
	wantLadoErrorCode(t, body, status, http.StatusNotFound, ladoknowledge.CodeProjectNotFound)
}

func TestLadoAPI_ImportKnowledgeRoutesBySource(t *testing.T) {
	svc := &fakeLadoKnowledge{result: ladoknowledge.ImportResult{
		Imported: []string{"build"},
		Skipped:  []ladoknowledge.ImportSkip{{Name: "MEMORY.md", Reason: ladoknowledge.SkipNotAFile}, {Name: "x.md", Reason: ladoknowledge.SkipExists}},
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoKnowledge: svc})
	const path = "/api/v1/lado/projects/app/knowledge/imports"

	body, status, _ := doRequest(t, srv, "POST", path, `{"source":"claude_memory","path":"/old/app"}`)
	if status != http.StatusOK {
		t.Fatalf("claude_memory status = %d, body %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoKnowledgeImportResponse](t, body)
	if svc.memoryPath == nil || *svc.memoryPath != "/old/app" || svc.gotProject != "app" {
		t.Fatalf("service got path %v project %q", svc.memoryPath, svc.gotProject)
	}
	if len(got.Imported) != 1 || got.Imported[0] != "build" || len(got.Skipped) != 2 || got.Skipped[1].Name != "x.md" || got.Skipped[1].Reason != "exists" {
		t.Fatalf("result = %+v", got)
	}

	// No path: the service tries the project's own.
	svc.memoryPath = nil
	if body, status, _ := doRequest(t, srv, "POST", path, `{"source":"claude_memory"}`); status != http.StatusOK || svc.memoryPath == nil || *svc.memoryPath != "" {
		t.Fatalf("claude_memory without path: status %d body %s path %v", status, body, svc.memoryPath)
	}

	if body, status, _ := doRequest(t, srv, "POST", path, `{"source":"project","fromProjectId":"old-app"}`); status != http.StatusOK || svc.fromID != "old-app" {
		t.Fatalf("project import: status %d body %s from %q", status, body, svc.fromID)
	}

	// Nothing imported: lists are [] not null.
	svc.result = ladoknowledge.ImportResult{}
	body, _, _ = doRequest(t, srv, "POST", path, `{"source":"project","fromProjectId":"old-app"}`)
	if !strings.Contains(string(body), `"imported":[]`) || !strings.Contains(string(body), `"skipped":[]`) {
		t.Fatalf("empty import result: %s", body)
	}
}

func TestLadoAPI_ImportKnowledgeValidatesTheRequest(t *testing.T) {
	svc := &fakeLadoKnowledge{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoKnowledge: svc})
	const path = "/api/v1/lado/projects/app/knowledge/imports"

	for _, tc := range []struct{ name, body, field string }{
		{"no source", `{}`, "source"},
		{"unknown source", `{"source":"obsidian"}`, "source"},
		{"project without fromProjectId", `{"source":"project"}`, "fromProjectId"},
		{"project with path", `{"source":"project","fromProjectId":"old","path":"/x"}`, "path"},
		{"claude_memory with fromProjectId", `{"source":"claude_memory","fromProjectId":"old"}`, "fromProjectId"},
	} {
		body, status, _ := doRequest(t, srv, "POST", path, tc.body)
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, ladoknowledge.CodeInvalidImport)
		if got := decodeLadoAPIError(t, body); got.Details["field"] != tc.field {
			t.Fatalf("%s: details = %v, want field %s", tc.name, got.Details, tc.field)
		}
	}
	body, status, _ := doRequest(t, srv, "POST", path, `{"source":"claude_memory","extra":1}`)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	body, status, _ = doRequest(t, srv, "POST", path, `{"source":"claude_memory","path":"`+strings.Repeat("a", 20<<10)+`"}`)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	if svc.memoryPath != nil || svc.fromID != "" {
		t.Fatalf("a refused request reached the service: path %v from %q", svc.memoryPath, svc.fromID)
	}

	// Service errors keep their envelope.
	svc.err = apierr.NotFound(ladoknowledge.CodeImportNotFound, "No knowledge folder has that project id.")
	body, status, _ = doRequest(t, srv, "POST", path, `{"source":"project","fromProjectId":"gone"}`)
	wantLadoErrorCode(t, body, status, http.StatusNotFound, ladoknowledge.CodeImportNotFound)
	svc.err = apierr.Conflict(ladoknowledge.CodeForeign, "foreign", map[string]any{"ownerPath": "/src/other"})
	body, status, _ = doRequest(t, srv, "POST", path, `{"source":"claude_memory"}`)
	wantLadoErrorCode(t, body, status, http.StatusConflict, ladoknowledge.CodeForeign)
}

func TestLadoAPI_PutKnowledgeNotePassesTheWrite(t *testing.T) {
	svc := &fakeLadoKnowledge{entry: domain.LadoKnowledgeEntry{
		Slug: "build", Title: "빌드", Kind: domain.LadoKnowledgeConvention, Revision: 4, UpdatedAt: knowledgeT0, Author: "app-1", State: domain.LadoKnowledgeFresh,
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoKnowledge: svc})
	content := "---\ntitle: 빌드\nkind: convention\n---\n본문\n"
	req, _ := json.Marshal(map[string]any{"sessionId": "app-1", "content": content, "baseRevision": 3})

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/knowledge/notes/build", string(req))
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoKnowledgeNoteResponse](t, body)
	if got.Slug != "build" || got.Revision != 4 || got.State != "fresh" || got.Author != "app-1" || !got.UpdatedAt.Equal(knowledgeT0) {
		t.Fatalf("response = %+v", got)
	}
	if len(svc.writes) != 1 {
		t.Fatalf("writes = %d", len(svc.writes))
	}
	w := svc.writes[0]
	if w.SessionID != "app-1" || w.Slug != "build" || string(w.Content) != content || w.BaseRevision == nil || *w.BaseRevision != 3 {
		t.Fatalf("write input = %+v", w)
	}

	// A new note leaves baseRevision out.
	if _, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/knowledge/notes/new-note", `{"sessionId":"app-1","content":"x"}`); status != http.StatusOK {
		t.Fatalf("new note status = %d", status)
	}
	if w := svc.writes[1]; w.Slug != "new-note" || w.BaseRevision != nil {
		t.Fatalf("new note input = %+v", w)
	}
}

func TestLadoAPI_PutKnowledgeNoteValidatesTheBody(t *testing.T) {
	svc := &fakeLadoKnowledge{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoKnowledge: svc})
	const path = "/api/v1/lado/knowledge/notes/build"

	for _, tc := range []struct{ name, body, code, field string }{
		{"no session", `{"content":"x"}`, ladoknowledge.CodeInvalidNote, "sessionId"},
		{"blank session", `{"sessionId":"  ","content":"x"}`, ladoknowledge.CodeInvalidNote, "sessionId"},
		{"no content", `{"sessionId":"app-1"}`, ladoknowledge.CodeInvalidNote, "content"},
		{"blank content", `{"sessionId":"app-1","content":" \n"}`, ladoknowledge.CodeInvalidNote, "content"},
		{"over the body cap", `{"sessionId":"app-1","content":"` + strings.Repeat("a", 600<<10) + `"}`, ladoknowledge.CodeInvalidNote, "content"},
	} {
		body, status, _ := doRequest(t, srv, "PUT", path, tc.body)
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, tc.code)
		if got := decodeLadoAPIError(t, body); got.Details["field"] != tc.field {
			t.Fatalf("%s: details = %v, want field %s", tc.name, got.Details, tc.field)
		}
	}
	for _, bad := range []string{`{"sessionId":"app-1","content":"x","project":"other"}`, `not json`, `{"sessionId":"app-1","content":"x","baseRevision":"3"}`} {
		body, status, _ := doRequest(t, srv, "PUT", path, bad)
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	}
	if len(svc.writes) != 0 {
		t.Fatalf("a refused request reached the service: %+v", svc.writes)
	}
}

func TestLadoAPI_PutKnowledgeNoteKeepsServiceErrorEnvelopes(t *testing.T) {
	svc := &fakeLadoKnowledge{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoKnowledge: svc})
	const path = "/api/v1/lado/knowledge/notes/build"
	for _, tc := range []struct {
		err    *apierr.Error
		status int
	}{
		{apierr.Invalid(ladoknowledge.CodeSecret, "secret", map[string]any{"kind": "github_token"}), http.StatusBadRequest},
		{apierr.NotFound(ladoknowledge.CodeSessionNotFound, "Session not found"), http.StatusNotFound},
		{apierr.Conflict(ladoknowledge.CodeExists, "exists", nil), http.StatusConflict},
		{apierr.Conflict(ladoknowledge.CodeSessionEnded, "ended", nil), http.StatusConflict},
		{apierr.Conflict(ladoknowledge.CodeNotLadoProject, "not lado", nil), http.StatusConflict},
		{apierr.Conflict(ladoknowledge.CodeReadOnly, "critic", nil), http.StatusConflict},
		{apierr.Conflict(ladoknowledge.CodeForeign, "foreign", map[string]any{"ownerPath": "/src/other"}), http.StatusConflict},
	} {
		svc.err = tc.err
		body, status, _ := doRequest(t, srv, "PUT", path, `{"sessionId":"app-1","content":"x"}`)
		wantLadoErrorCode(t, body, status, tc.status, tc.err.Code)
	}

	// CHANGED carries the current revision for the writer to merge against.
	svc.err = apierr.Conflict(ladoknowledge.CodeChanged, "changed", map[string]any{"currentRevision": 5})
	body, status, _ := doRequest(t, srv, "PUT", path, `{"sessionId":"app-1","content":"x","baseRevision":4}`)
	wantLadoErrorCode(t, body, status, http.StatusConflict, ladoknowledge.CodeChanged)
	if got := decodeLadoAPIError(t, body); got.Details["currentRevision"] != float64(5) {
		t.Fatalf("CHANGED details = %v", got.Details)
	}
}

// knowledgeStore is the little the real knowledge service reads.
type knowledgeStore struct {
	mu       sync.Mutex
	projects map[string]domain.ProjectRecord
	sessions map[domain.SessionID]domain.SessionRecord
	lado     map[domain.ProjectID]bool
}

func (s *knowledgeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	return p, ok, nil
}

func (s *knowledgeStore) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.ProjectRecord{}
	for _, p := range s.projects {
		out = append(out, p)
	}
	return out, nil
}

func (s *knowledgeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	return rec, ok, nil
}

func (s *knowledgeStore) LadoProjectSettings(_ context.Context, id domain.ProjectID) (domain.LadoProjectSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return domain.LadoProjectSettings{ProjectID: id, LadoMode: s.lado[id]}, nil
}

func (s *knowledgeStore) AppendLadoAuditEvent(_ context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error) {
	return ev, nil
}

// TestLadoAPI_KnowledgeRoutesWithTheRealService runs the routes over the
// knowledge service and a folder outside git: a write, the index, the
// optimistic check and the import guards keep their codes end to end.
func TestLadoAPI_KnowledgeRoutesWithTheRealService(t *testing.T) {
	state, projectDir := t.TempDir(), t.TempDir()
	store := &knowledgeStore{
		projects: map[string]domain.ProjectRecord{
			"app":   {ID: "app", Path: projectDir},
			"plain": {ID: "plain", Path: t.TempDir()},
		},
		sessions: map[domain.SessionID]domain.SessionRecord{
			"app-1":   {ID: "app-1", ProjectID: "app", Metadata: domain.SessionMetadata{WorkspacePath: projectDir}},
			"app-2":   {ID: "app-2", ProjectID: "app", IsTerminated: true},
			"plain-1": {ID: "plain-1", ProjectID: "plain"},
		},
		lado: map[domain.ProjectID]bool{"app": true},
	}
	svc := ladoknowledge.New(ladoknowledge.Options{StateDir: state, Store: store, ClaudeDir: filepath.Join(state, "claude")})
	srv := newLadoTestServer(t, httpd.APIDeps{LadoKnowledge: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/app/knowledge", "")
	if status != http.StatusOK {
		t.Fatalf("empty GET status = %d, body %s", status, body)
	}
	if got := decodeLadoJSON[controllers.LadoKnowledgeResponse](t, body); len(got.Notes) != 0 || got.Dir != filepath.Join(state, "lado", "knowledge", "app") {
		t.Fatalf("empty GET = %+v", got)
	}

	put := func(session, slug, content string, base *int) ([]byte, int) {
		t.Helper()
		req := map[string]any{"sessionId": session, "content": content}
		if base != nil {
			req["baseRevision"] = *base
		}
		data, _ := json.Marshal(req)
		body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/knowledge/notes/"+slug, string(data))
		return body, status
	}
	content := "---\ntitle: 빌드 순서\nkind: convention\n---\nnpm run api 를 먼저 돈다.\n"
	body, status = put("app-1", "build", content, nil)
	if status != http.StatusOK {
		t.Fatalf("write status = %d, body %s", status, body)
	}
	if got := decodeLadoJSON[controllers.LadoKnowledgeNoteResponse](t, body); got.Revision != 1 || got.State != "unchecked" || got.Author != "app-1" {
		t.Fatalf("written = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(state, "lado", "knowledge", "app", "notes", "build.md")); err != nil {
		t.Fatalf("note file: %v", err)
	}

	one := 1
	body, status = put("app-1", "build", content, nil)
	wantLadoErrorCode(t, body, status, http.StatusConflict, ladoknowledge.CodeExists)
	if body, status = put("app-1", "build", content, &one); status != http.StatusOK {
		t.Fatalf("update status = %d, body %s", status, body)
	}
	body, status = put("app-1", "build", content, &one)
	wantLadoErrorCode(t, body, status, http.StatusConflict, ladoknowledge.CodeChanged)
	if got := decodeLadoAPIError(t, body); got.Details["currentRevision"] != float64(2) {
		t.Fatalf("CHANGED details = %v", got.Details)
	}
	body, status = put("app-1", "Bad_Slug", content, nil)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, ladoknowledge.CodeInvalidNote)
	body, status = put("app-1", "leak", content+"ghp_"+strings.Repeat("a", 36)+"\n", nil)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, ladoknowledge.CodeSecret)
	body, status = put("nobody", "other", content, nil)
	wantLadoErrorCode(t, body, status, http.StatusNotFound, ladoknowledge.CodeSessionNotFound)
	body, status = put("app-2", "other", content, nil)
	wantLadoErrorCode(t, body, status, http.StatusConflict, ladoknowledge.CodeSessionEnded)
	body, status = put("plain-1", "other", content, nil)
	wantLadoErrorCode(t, body, status, http.StatusConflict, ladoknowledge.CodeNotLadoProject)

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/app/knowledge", "")
	got := decodeLadoJSON[controllers.LadoKnowledgeResponse](t, body)
	if status != http.StatusOK || len(got.Notes) != 1 || got.Notes[0].Slug != "build" || got.Notes[0].Revision != 2 || got.Notes[0].Title != "빌드 순서" {
		t.Fatalf("GET after writes: status %d, %+v", status, got)
	}
	if got.Owner == nil || got.Owner.ProjectID != "app" {
		t.Fatalf("owner after the first write = %+v", got.Owner)
	}

	// plain sees app's folder as importable and imports from it; app itself
	// and an escaping id are refused.
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/plain/knowledge", "")
	if got := decodeLadoJSON[controllers.LadoKnowledgeResponse](t, body); len(got.Importable) != 1 || got.Importable[0].ProjectID != "app" || got.Importable[0].Notes != 1 || !got.Importable[0].Registered {
		t.Fatalf("plain importable = %+v", got.Importable)
	}
	const imports = "/api/v1/lado/projects/plain/knowledge/imports"
	body, status, _ = doRequest(t, srv, "POST", imports, `{"source":"project","fromProjectId":"../x"}`)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, ladoknowledge.CodeInvalidImport)
	body, status, _ = doRequest(t, srv, "POST", imports, `{"source":"project","fromProjectId":"plain"}`)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, ladoknowledge.CodeInvalidImport)
	body, status, _ = doRequest(t, srv, "POST", imports, `{"source":"project","fromProjectId":"gone"}`)
	wantLadoErrorCode(t, body, status, http.StatusNotFound, ladoknowledge.CodeImportNotFound)
	body, status, _ = doRequest(t, srv, "POST", imports, `{"source":"project","fromProjectId":"app"}`)
	if res := decodeLadoJSON[controllers.LadoKnowledgeImportResponse](t, body); status != http.StatusOK || len(res.Imported) != 1 || res.Imported[0] != "build" {
		t.Fatalf("import status %d, %s", status, body)
	}
	body, status, _ = doRequest(t, srv, "POST", imports, `{"source":"claude_memory"}`)
	if res := decodeLadoJSON[controllers.LadoKnowledgeImportResponse](t, body); status != http.StatusOK || len(res.Imported) != 0 || len(res.Skipped) == 0 || res.Skipped[0].Reason != ladoknowledge.SkipClaudeMemoryNotFound {
		t.Fatalf("claude memory import status %d, %s", status, body)
	}
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/nope/knowledge/imports", `{"source":"claude_memory"}`)
	wantLadoErrorCode(t, body, status, http.StatusNotFound, ladoknowledge.CodeProjectNotFound)

	// Claim of the project's own folder keeps it and answers the folder.
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/app/knowledge/claim", "")
	if got := decodeLadoJSON[controllers.LadoKnowledgeResponse](t, body); status != http.StatusOK || got.Foreign || len(got.Notes) != 1 {
		t.Fatalf("claim status %d, %s", status, body)
	}
}
