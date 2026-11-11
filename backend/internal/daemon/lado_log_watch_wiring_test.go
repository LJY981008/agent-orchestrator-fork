package daemon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// ladoLogWatchEngine is a fake Docker Engine: one compose container whose
// log stream stays open until the client closes it. It records every
// request.
type ladoLogWatchEngine struct {
	mu       sync.Mutex
	requests []string
	opened   chan struct{}
	closed   chan struct{}
}

func newLadoLogWatchEngine(t *testing.T) (*ladoLogWatchEngine, *httptest.Server) {
	t.Helper()
	e := &ladoLogWatchEngine{opened: make(chan struct{}, 4), closed: make(chan struct{}, 4)}
	id := strings.Repeat("ab", 32)
	labels := map[string]string{"com.docker.compose.project": "grp", "com.docker.compose.service": "api"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.requests = append(e.requests, r.Method+" "+r.URL.Path)
		e.mu.Unlock()
		switch r.URL.Path {
		case "/containers/json":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"Id": id, "Names": []string{"/grp-api-1"}, "Image": "api:1", "Labels": labels}})
		case "/containers/" + id + "/json":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": id, "Name": "/grp-api-1",
				"Config": map[string]any{"Image": "api:1", "Labels": labels},
				"State":  map[string]any{"Running": true, "StartedAt": "2026-11-10T08:00:00Z"},
			})
		case "/containers/" + id + "/logs":
			w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
			line := []byte("2026-11-10T08:00:01Z INFO started\n")
			frame := make([]byte, 8, 8+len(line))
			frame[0] = 1
			binary.BigEndian.PutUint32(frame[4:], uint32(len(line)))
			_, _ = w.Write(append(frame, line...))
			w.(http.Flusher).Flush()
			e.opened <- struct{}{}
			<-r.Context().Done()
			e.closed <- struct{}{}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return e, srv
}

func (e *ladoLogWatchEngine) seen() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.requests...)
}

// TestWireLadoLogWatchFollowsOnlyWhatIsTurnedOnAndClosesOnShutdown: the log
// watch the daemon wires sends no request to an Engine until a project turns
// it on through the API; then it reads the source with GETs on the four read
// paths only, and the daemon's shutdown closes the open log stream before
// the channel Start returned closes.
func TestWireLadoLogWatchFollowsOnlyWhatIsTurnedOnAndClosesOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	useLadoStoredTokens(t, nil)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	store := sqlitetest.MustOpen(t)
	at := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: at}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := store.SetLadoMode(ctx, "mer", true, at); err != nil {
		t.Fatalf("SetLadoMode: %v", err)
	}
	log := slog.New(slog.DiscardHandler)
	work := wireLadoWork(ctx, cfg, store, &recordingProjectCloner{}, ladoWorkDeps{}, log)
	watch := wireLadoLogWatch(cfg, store, work.tasks, log)
	api := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{LadoLogWatch: watch}, httpd.ControlDeps{}))
	t.Cleanup(api.Close)
	engine, engineSrv := newLadoLogWatchEngine(t)
	engineURL, _ := url.Parse(engineSrv.URL)

	done := watch.Start(ctx)

	call := func(method, body string) map[string]any {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, api.URL+"/api/v1/lado/projects/mer/log-watch", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s log-watch: %v", method, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s log-watch = %d %s", method, resp.StatusCode, raw)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		return out
	}

	view := call(http.MethodGet, "")
	if view["enabled"] != false || view["ladoMode"] != true || view["hourlyIssueCap"] != float64(domain.LadoLogDefaultHourlyIssueCap) {
		t.Fatalf("default view = %v", view)
	}
	// Nothing is turned on: no request reaches the Engine.
	time.Sleep(200 * time.Millisecond)
	if got := engine.seen(); len(got) != 0 {
		t.Fatalf("engine got %v before the log watch was on", got)
	}

	view = call(http.MethodPut, `{"enabled":true,"include":[],"exclude":[],"hourlyIssueCap":5,"autoPlan":false,"autoPlanDailyCap":5,"autoPlanMinCount":1,"disconnectAlertMinutes":10,
		"sources":[{"displayName":"prod1","endpoint":"tcp://`+engineURL.Host+`","targets":[{"kind":"compose_project","value":"grp"}]}]}`)
	sources, _ := view["sources"].([]any)
	if view["enabled"] != true || len(sources) != 1 {
		t.Fatalf("saved view = %v", view)
	}
	if src, _ := sources[0].(map[string]any); src["id"] == "" || src["tlsStored"] == nil {
		t.Fatalf("saved source = %v", src)
	}
	select {
	case <-engine.opened:
	case <-time.After(10 * time.Second):
		t.Fatalf("the log stream never opened; engine got %v", engine.seen())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the log watch did not stop after the daemon's context ended")
	}
	select {
	case <-engine.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the log stream's connection stayed open after the log watch stopped")
	}
	read := regexp.MustCompile(`^GET (/version|/containers/json|/containers/[0-9a-f]{12,64}/(json|logs))$`)
	for _, r := range engine.seen() {
		if !read.MatchString(r) {
			t.Errorf("engine received %s", r)
		}
	}
}

// TestWireLadoLogWatchChecksTargetRepositoriesAgainstTheWorkspace: the
// wired log watch checks a target's issue repository against the
// workspace's repositories (ladotask through the real store), saves it in
// its canonical form, reads it back from the store, and refuses a followed
// target without one and a repository outside the workspace (#91).
func TestWireLadoLogWatchChecksTargetRepositoriesAgainstTheWorkspace(t *testing.T) {
	ctx := context.Background()
	useLadoStoredTokens(t, nil)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	store := sqlitetest.MustOpen(t)
	at := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertWorkspaceProject(ctx, domain.ProjectRecord{ID: "mer", Path: t.TempDir(), Kind: domain.ProjectKindWorkspace, RegisteredAt: at}, []domain.WorkspaceRepoRecord{
		{ProjectID: "mer", Name: "back", RelativePath: "back", RepoOriginURL: "https://github.com/acme/back.git", DefaultBranch: "main"},
		{ProjectID: "mer", Name: "front", RelativePath: "front", RepoOriginURL: "https://github.com/acme/front.git", DefaultBranch: "main"},
	}); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	log := slog.New(slog.DiscardHandler)
	work := wireLadoWork(ctx, cfg, store, &recordingProjectCloner{}, ladoWorkDeps{}, log)
	watch := wireLadoLogWatch(cfg, store, work.tasks, log)
	api := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{LadoLogWatch: watch}, httpd.ControlDeps{}))
	t.Cleanup(api.Close)

	call := func(method, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, api.URL+"/api/v1/lado/projects/mer/log-watch", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s log-watch: %v", method, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	put := func(targets string) (int, string) {
		return call(http.MethodPut, `{"enabled":true,"include":[],"exclude":[],"hourlyIssueCap":5,"autoPlan":false,"autoPlanDailyCap":5,"autoPlanMinCount":1,"disconnectAlertMinutes":10,
			"sources":[{"displayName":"prod1","endpoint":"tcp://192.0.2.10:2375","targets":`+targets+`}]}`)
	}

	for _, tc := range []struct{ name, targets string }{
		{"no repository anywhere", `[{"kind":"compose_project","value":"back"}]`},
		{"a repository outside the workspace", `[{"kind":"compose_project","value":"back","repository":"github:acme/other"}]`},
	} {
		status, body := put(tc.targets)
		if status != http.StatusBadRequest || !strings.Contains(body, "LADO_LOG_WATCH_INVALID") || !strings.Contains(body, "sources[0].targets[0].repository") {
			t.Fatalf("%s: PUT = %d %s", tc.name, status, body)
		}
	}

	status, body := put(`[{"kind":"compose_project","value":"back","repository":"github:ACME/Back"},{"kind":"compose_project","value":"front","repository":"github:acme/front"}]`)
	if status != http.StatusOK {
		t.Fatalf("PUT = %d %s", status, body)
	}
	status, body = call(http.MethodGet, "")
	var view struct {
		Sources []struct {
			Targets []struct {
				Value      string `json:"value"`
				Repository string `json:"repository"`
			} `json:"targets"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(body), &view); status != http.StatusOK || err != nil || len(view.Sources) != 1 || len(view.Sources[0].Targets) != 2 {
		t.Fatalf("GET = %d %s (%v)", status, body, err)
	}
	if got := view.Sources[0].Targets; got[0].Repository != "github:acme/back" || got[1].Repository != "github:acme/front" {
		t.Fatalf("saved targets = %+v", got)
	}
}
