package ladologwatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// dockerTestID makes a 64-digit container id from a short tag.
func dockerTestID(tag string) string {
	id := fmt.Sprintf("%x", tag)
	return (id + strings.Repeat("0", 64))[:64]
}

type dockerTestContainer struct {
	ID        string
	Name      string
	Image     string
	Labels    map[string]string
	Tty       bool
	StartedAt string
}

type dockerTestRequest struct {
	Method string
	Path   string
	Query  url.Values
}

// dockerTestEngine is a fake Docker Engine that records every request it
// receives. With looseNames it reads a name filter as an unanchored
// substring, more loosely than a real Engine, so that Discover's own exact
// check is what keeps a name target narrow.
type dockerTestEngine struct {
	mu         sync.Mutex
	containers []dockerTestContainer
	requests   []dockerTestRequest
	gone       map[string]bool
	redirect   map[string]string
	looseNames bool
	srv        *httptest.Server
}

func newDockerTestEngine(t *testing.T, containers ...dockerTestContainer) *dockerTestEngine {
	t.Helper()
	e := &dockerTestEngine{containers: containers, gone: map[string]bool{}, redirect: map[string]string{}}
	e.srv = httptest.NewServer(e)
	t.Cleanup(e.srv.Close)
	return e
}

// endpoint is the engine's tcp://host:port.
func (e *dockerTestEngine) endpoint() string {
	u, _ := url.Parse(e.srv.URL)
	return "tcp://" + u.Host
}

func (e *dockerTestEngine) recorded() []dockerTestRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]dockerTestRequest(nil), e.requests...)
}

func (e *dockerTestEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	e.requests = append(e.requests, dockerTestRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query()})
	redirect := e.redirect[r.URL.Path]
	containers := append([]dockerTestContainer(nil), e.containers...)
	gone := map[string]bool{}
	for k, v := range e.gone {
		gone[k] = v
	}
	loose := e.looseNames
	e.mu.Unlock()
	if redirect != "" {
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/version":
		_, _ = w.Write([]byte(`{"Version":"27.0.1","ApiVersion":"1.47"}`))
		return
	case "/containers/json":
		var filters map[string][]string
		if raw := r.URL.Query().Get("filters"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &filters); err != nil {
				http.Error(w, `{"message":"bad filters"}`, http.StatusBadRequest)
				return
			}
		}
		out := []map[string]any{}
		for _, c := range containers {
			if !dockerTestFilter(c, filters, loose) {
				continue
			}
			out = append(out, map[string]any{"Id": c.ID, "Names": []string{"/" + c.Name}, "Image": c.Image, "Labels": c.Labels, "State": "running"})
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/containers/")
	id, action, _ := strings.Cut(rest, "/")
	for _, c := range containers {
		if c.ID != id || gone[id] {
			continue
		}
		switch action {
		case "json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": c.ID, "Name": "/" + c.Name,
				"Config": map[string]any{"Image": c.Image, "Labels": c.Labels, "Tty": c.Tty},
				"State":  map[string]any{"Running": true, "StartedAt": c.StartedAt},
			})
			return
		case "logs":
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			_, _ = w.Write([]byte("2026-11-10T00:00:00.000000001Z hello\n"))
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"message":"No such container"}`))
}

func dockerTestFilter(c dockerTestContainer, filters map[string][]string, loose bool) bool {
	for _, label := range filters["label"] {
		key, value, hasValue := strings.Cut(label, "=")
		got, ok := c.Labels[key]
		if !ok || (hasValue && got != value) {
			return false
		}
	}
	for _, name := range filters["name"] {
		if loose {
			name = strings.TrimSuffix(strings.TrimPrefix(name, "^/"), "$")
		}
		re, err := regexp.Compile(name)
		if err != nil || !re.MatchString("/"+c.Name) {
			return false
		}
	}
	return true
}

func newDockerTestSource(t *testing.T, endpoint string, targets ...DockerTarget) *DockerSource {
	t.Helper()
	s, err := NewDockerSource(DockerConfig{Endpoint: endpoint, Targets: targets})
	if err != nil {
		t.Fatalf("NewDockerSource: %v", err)
	}
	return s
}

func TestDockerAllowedTable(t *testing.T) {
	want := url.URL{Scheme: "http", Host: "engine.example.test:2375"}
	id := strings.Repeat("ab", 32)
	base := "http://engine.example.test:2375"
	cases := []struct {
		name   string
		method string
		url    string
		body   string
		header map[string]string
		ok     bool
	}{
		{name: "version", url: base + "/version", ok: true},
		{name: "list with filters", url: base + "/containers/json?filters=" + url.QueryEscape(`{"label":["com.docker.compose.project=grp"]}`), ok: true},
		{name: "list all", url: base + "/containers/json?all=1", ok: true},
		{name: "inspect", url: base + "/containers/" + id + "/json", ok: true},
		{name: "inspect short id", url: base + "/containers/" + id[:12] + "/json", ok: true},
		{name: "logs since", url: base + "/containers/" + id + "/logs?follow=1&stdout=1&stderr=1&timestamps=1&since=1700000000.000000001", ok: true},
		{name: "logs tail", url: base + "/containers/" + id + "/logs?follow=1&stdout=1&stderr=1&timestamps=1&tail=0", ok: true},

		{name: "POST", method: http.MethodPost, url: base + "/version"},
		{name: "PUT", method: http.MethodPut, url: base + "/version"},
		{name: "DELETE", method: http.MethodDelete, url: base + "/containers/" + id + "/json"},
		{name: "HEAD", method: http.MethodHead, url: base + "/version"},
		{name: "PATCH", method: http.MethodPatch, url: base + "/version"},
		{name: "OPTIONS", method: http.MethodOptions, url: base + "/version"},

		{name: "archive", url: base + "/containers/" + id + "/archive?path=/"},
		{name: "export", url: base + "/containers/" + id + "/export"},
		{name: "attach", url: base + "/containers/" + id + "/attach"},
		{name: "top", url: base + "/containers/" + id + "/top"},
		{name: "exec", url: base + "/exec/" + id + "/json"},
		{name: "images", url: base + "/images/json"},
		{name: "info", url: base + "/info"},
		{name: "versioned path", url: base + "/v1.47/version"},
		{name: "create", url: base + "/containers/create"},
		{name: "logs trailing", url: base + "/containers/" + id + "/logs/x"},
		{name: "id uppercase", url: base + "/containers/" + strings.ToUpper(id) + "/json"},
		{name: "id too short", url: base + "/containers/" + id[:11] + "/json"},
		{name: "id not hex", url: base + "/containers/api/json"},
		{name: "percent encoded dots", url: base + "/containers/%2e%2e/json"},
		{name: "percent encoded slash", url: base + "/containers/" + id + "%2Fjson"},
		{name: "dot dot", url: base + "/containers/" + id + "/../../info"},
		{name: "double slash", url: base + "//version"},

		{name: "version query", url: base + "/version?x=1"},
		{name: "list size", url: base + "/containers/json?size=1"},
		{name: "inspect size", url: base + "/containers/" + id + "/json?size=1"},
		{name: "logs details", url: base + "/containers/" + id + "/logs?details=1"},
		{name: "logs bad follow", url: base + "/containers/" + id + "/logs?follow=yes"},
		{name: "logs bad since", url: base + "/containers/" + id + "/logs?since=-1"},
		{name: "logs bad tail", url: base + "/containers/" + id + "/logs?tail=x"},
		{name: "logs twice", url: base + "/containers/" + id + "/logs?tail=0&tail=1"},
		{name: "bad filters", url: base + "/containers/json?filters=x"},

		{name: "other host", url: "http://other.example.test:2375/version"},
		{name: "other port", url: "http://engine.example.test:2376/version"},
		{name: "other scheme", url: "https://engine.example.test:2375/version"},
		{name: "user info", url: "http://user:pw@engine.example.test:2375/version"},
		{name: "fragment", url: base + "/version#x"},
		{name: "body", url: base + "/version", body: "x"},
		{name: "upgrade", url: base + "/containers/" + id + "/json", header: map[string]string{"Upgrade": "tcp"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			var req *http.Request
			var err error
			if tc.body != "" {
				req, err = http.NewRequest(method, tc.url, strings.NewReader(tc.body))
			} else {
				req, err = http.NewRequest(method, tc.url, nil)
			}
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			for k, v := range tc.header {
				req.Header.Set(k, v)
			}
			err = dockerAllowed(req, want)
			if tc.ok && err != nil {
				t.Fatalf("dockerAllowed refused an allowed request: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("dockerAllowed let %s %s through", method, tc.url)
			}
		})
	}
}

func TestDockerRefusalsNeverReachTheEngine(t *testing.T) {
	engine := newDockerTestEngine(t)
	other := newDockerTestEngine(t)
	s := newDockerTestSource(t, engine.endpoint())
	id := dockerTestID("api")
	ctx := context.Background()

	for _, path := range []string{
		"/containers/" + id + "/archive",
		"/containers/" + id + "/export",
		"/containers/" + id + "/attach",
		"/exec/" + id + "/start",
		"/images/json",
		"/info",
		"/containers/%2e%2e/json",
		"/containers/" + id + "/../json",
	} {
		if _, err := s.dockerGet(ctx, path, nil); err == nil {
			t.Errorf("dockerGet(%q) succeeded", path)
		}
	}
	if _, err := s.dockerGet(ctx, "/version", url.Values{"x": {"1"}}); err == nil {
		t.Error("dockerGet with a query key outside the list succeeded")
	}
	if _, err := s.dockerGet(ctx, "/containers/"+id+"/logs", url.Values{"details": {"1"}}); err == nil {
		t.Error("dockerGet of logs with details succeeded")
	}

	// A request made around dockerGet still meets the transport's check.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodHead, http.MethodPatch} {
		for _, client := range []*http.Client{s.client, s.stream} {
			req, err := http.NewRequest(method, engine.srv.URL+"/containers/"+id+"/json", nil)
			if err != nil {
				t.Fatal(err)
			}
			if resp, err := client.Do(req); err == nil {
				_ = resp.Body.Close()
				t.Errorf("%s went through the transport", method)
			}
		}
	}
	req, err := http.NewRequest(http.MethodGet, other.srv.URL+"/version", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := s.client.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Error("a GET to another host went through the transport")
	}
	req, err = http.NewRequest(http.MethodGet, engine.srv.URL+"/info", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := s.stream.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Error("GET /info went through the stream transport")
	}

	if got := engine.recorded(); len(got) != 0 {
		t.Fatalf("engine received %d refused requests: %+v", len(got), got)
	}
	if got := other.recorded(); len(got) != 0 {
		t.Fatalf("other host received %d requests: %+v", len(got), got)
	}
}

func TestDockerRefusesRedirects(t *testing.T) {
	engine := newDockerTestEngine(t)
	other := newDockerTestEngine(t)
	engine.redirect["/version"] = "/containers/json"
	engine.redirect["/containers/json"] = other.srv.URL + "/version"
	s := newDockerTestSource(t, engine.endpoint())
	ctx := context.Background()

	var v map[string]any
	err := s.dockerGetJSON(ctx, "/version", nil, &v)
	if err == nil || !errors.Is(err, errDockerRedirect) {
		t.Fatalf("redirect within the engine: err = %v, want errDockerRedirect", err)
	}
	err = s.dockerGetJSON(ctx, "/containers/json", nil, &v)
	if err == nil || !errors.Is(err, errDockerRedirect) {
		t.Fatalf("redirect to another host: err = %v, want errDockerRedirect", err)
	}
	got := engine.recorded()
	if len(got) != 2 || got[0].Path != "/version" || got[1].Path != "/containers/json" {
		t.Fatalf("engine requests = %+v, want only the two first requests", got)
	}
	if n := len(other.recorded()); n != 0 {
		t.Fatalf("redirect target received %d requests", n)
	}
}

func TestDockerRequestsStayOnTheReadList(t *testing.T) {
	api := dockerTestContainer{ID: dockerTestID("api"), Name: "grp-api-1", Image: "registry.example.test/api:1.2", Labels: map[string]string{
		dockerLabelComposeProject: "grp", dockerLabelComposeService: "api",
	}, StartedAt: "2026-11-10T00:00:00Z"}
	engine := newDockerTestEngine(t, api)
	cfg := DockerConfig{Endpoint: engine.endpoint(), Targets: []DockerTarget{
		{ID: "t1", Kind: dockerTargetComposeProject, Value: "grp"},
		{ID: "t2", Kind: dockerTargetName, Value: "grp-api-1"},
		{ID: "t3", Kind: dockerTargetLabel, Value: "com.docker.compose.service=api"},
	}}
	ctx := context.Background()
	check, err := CheckDocker(ctx, cfg)
	if err != nil {
		t.Fatalf("CheckDocker: %v", err)
	}
	if check.EngineVersion != "27.0.1" || check.APIVersion != "1.47" || !check.Insecure || len(check.Instances) != 1 {
		t.Fatalf("check = %+v", check)
	}
	s := newDockerTestSource(t, cfg.Endpoint, cfg.Targets...)
	instances, err := s.Discover(ctx)
	if err != nil || len(instances) != 1 {
		t.Fatalf("Discover = %+v, %v", instances, err)
	}
	resp, err := s.dockerGet(ctx, "/containers/"+api.ID+"/logs", url.Values{
		"follow": {"1"}, "stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}, "tail": {"0"},
	})
	if err != nil {
		t.Fatalf("dockerGet logs: %v", err)
	}
	_ = resp.Body.Close()

	read := regexp.MustCompile(`^(/version|/containers/json|/containers/[0-9a-f]{12,64}/(json|logs))$`)
	paths := map[string]bool{}
	for _, r := range engine.recorded() {
		if r.Method != http.MethodGet || !read.MatchString(r.Path) {
			t.Errorf("engine received %s %s", r.Method, r.Path)
		}
		key := r.Path
		if strings.HasPrefix(key, "/containers/"+api.ID) {
			key = strings.Replace(key, api.ID, "{id}", 1)
		}
		paths[key] = true
	}
	for _, want := range []string{"/version", "/containers/json", "/containers/{id}/json", "/containers/{id}/logs"} {
		if !paths[want] {
			t.Errorf("engine never received GET %s (got %v)", want, paths)
		}
	}
	if len(paths) != 4 {
		t.Errorf("engine paths = %v, want the four read paths", paths)
	}
}

func TestDockerSourceHasOneRequestMaker(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string]bool{
		"POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true,
		"OPTIONS": true, "CONNECT": true, "TRACE": true,
	}
	makers := map[string]bool{
		"NewRequest": true, "NewRequestWithContext": true, "Get": true, "Post": true, "PostForm": true, "Head": true,
	}
	fset := token.NewFileSet()
	found := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range f.Decls {
			fn := ""
			if fd, ok := decl.(*ast.FuncDecl); ok {
				fn = fd.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					pkg, ok := n.X.(*ast.Ident)
					if ok && pkg.Name == "http" {
						if strings.HasPrefix(n.Sel.Name, "Method") && n.Sel.Name != "MethodGet" {
							t.Errorf("%s: %s uses http.%s", fset.Position(n.Pos()), fn, n.Sel.Name)
						}
						if makers[n.Sel.Name] {
							if file != "docker_api.go" || fn != "dockerGet" || n.Sel.Name != "NewRequestWithContext" {
								t.Errorf("%s: http.%s outside dockerGet (in %s)", fset.Position(n.Pos()), n.Sel.Name, fn)
							} else {
								found++
							}
						}
					}
					if n.Sel.Name == "InsecureSkipVerify" {
						t.Errorf("%s: InsecureSkipVerify", fset.Position(n.Pos()))
					}
				case *ast.KeyValueExpr:
					if id, ok := n.Key.(*ast.Ident); ok && id.Name == "InsecureSkipVerify" {
						t.Errorf("%s: InsecureSkipVerify", fset.Position(n.Pos()))
					}
				case *ast.BasicLit:
					if n.Kind == token.STRING {
						if v, err := strconv.Unquote(n.Value); err == nil && methods[v] {
							t.Errorf("%s: method literal %q", fset.Position(n.Pos()), v)
						}
					}
				}
				return true
			})
		}
	}
	if found != 1 {
		t.Fatalf("http.NewRequestWithContext in dockerGet found %d times, want 1", found)
	}
}

func TestNewDockerSourceEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"unix:///var/run/docker.sock",
		"npipe:////./pipe/docker_engine",
		"ssh://deploy@192.0.2.10",
		"http://192.0.2.10:2375",
		"https://192.0.2.10:2376",
		"tcp://user:pw@192.0.2.10:2375",
		"tcp://192.0.2.10",
		"tcp://192.0.2.10:0",
		"tcp://192.0.2.10:70000",
		"tcp://192.0.2.10:2375/path",
		"tcp://192.0.2.10:2375?x=1",
		"tcp://192.0.2.10:2375#x",
		"tcp://-engine.example.test:2375",
		"",
	} {
		if _, err := NewDockerSource(DockerConfig{Endpoint: endpoint}); err == nil {
			t.Errorf("NewDockerSource(%q) succeeded", endpoint)
		}
	}
	for _, endpoint := range []string{"tcp://192.0.2.10:2375", "tcp://engine.example.test:2375/", "tcp://[2001:db8::1]:2375"} {
		if _, err := NewDockerSource(DockerConfig{Endpoint: endpoint}); err != nil {
			t.Errorf("NewDockerSource(%q): %v", endpoint, err)
		}
	}
	if _, err := NewDockerSource(DockerConfig{Endpoint: "tcp://192.0.2.10:2376", TLS: true}); err == nil {
		t.Error("TLS without a CA succeeded")
	}
	s := newDockerTestSource(t, "tcp://192.0.2.10:2375")
	for _, client := range []*http.Client{s.client, s.stream} {
		rt, ok := client.Transport.(*dockerReadOnlyTransport)
		if !ok {
			t.Fatalf("transport is %T, want *dockerReadOnlyTransport", client.Transport)
		}
		if tr, ok := rt.base.(*http.Transport); !ok || tr.Proxy != nil {
			t.Fatal("transport uses a proxy")
		}
		if client.CheckRedirect == nil {
			t.Fatal("client follows redirects")
		}
	}
}

func TestDockerGetStatusError(t *testing.T) {
	engine := newDockerTestEngine(t)
	s := newDockerTestSource(t, engine.endpoint())
	_, err := s.dockerInspect(context.Background(), dockerTestID("gone"))
	if !dockerNotFound(err) {
		t.Fatalf("inspect of a missing container: err = %v, want a 404", err)
	}
	if !strings.Contains(err.Error(), "No such container") {
		t.Fatalf("error = %q, want the engine's message", err)
	}
	if _, err := s.dockerInspect(context.Background(), "../info"); err == nil {
		t.Fatal("inspect of a non-hex id succeeded")
	}
}
