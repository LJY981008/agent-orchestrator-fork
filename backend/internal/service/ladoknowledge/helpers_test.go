package ladoknowledge

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

var t0 = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

type fakeStore struct {
	mu       sync.Mutex
	projects map[string]domain.ProjectRecord
	sessions map[domain.SessionID]domain.SessionRecord
	lado     map[domain.ProjectID]bool
	events   []domain.LadoAuditEvent
}

func newFakeStore() *fakeStore {
	return &fakeStore{projects: map[string]domain.ProjectRecord{}, sessions: map[domain.SessionID]domain.SessionRecord{}, lado: map[domain.ProjectID]bool{}}
}

func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	return p, ok, nil
}

func (f *fakeStore) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ProjectRecord
	for _, p := range f.projects {
		if p.ArchivedAt.IsZero() {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	return s, ok, nil
}

func (f *fakeStore) LadoProjectSettings(_ context.Context, id domain.ProjectID) (domain.LadoProjectSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return domain.LadoProjectSettings{ProjectID: id, LadoMode: f.lado[id]}, nil
}

func (f *fakeStore) AppendLadoAuditEvent(_ context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
	return ev, nil
}

func (f *fakeStore) actions(action string) []domain.LadoAuditEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LadoAuditEvent
	for _, ev := range f.events {
		if ev.Action == action {
			out = append(out, ev)
		}
	}
	return out
}

// countingGit records the git calls and can fail them.
type countingGit struct {
	mu    sync.Mutex
	calls []string
	fail  bool
}

func (g *countingGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	g.mu.Lock()
	g.calls = append(g.calls, strings.Join(args, " "))
	fail := g.fail
	g.mu.Unlock()
	if fail {
		return nil, errors.New("git is broken")
	}
	return ExecGit{}.Run(ctx, dir, args...)
}

func (g *countingGit) count(prefix string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, c := range g.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (g *countingGit) reset(fail bool) {
	g.mu.Lock()
	g.calls, g.fail = nil, fail
	g.mu.Unlock()
}

// env is a service over a temporary state folder with one LADO mode
// project, "app", whose repository has one commit on main that
// origin/main also names.
type env struct {
	t     *testing.T
	svc   *Service
	store *fakeStore
	git   *countingGit
	state string
	repo  string
	now   time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, store: newFakeStore(), git: &countingGit{}, state: t.TempDir(), now: t0}
	e.repo = e.newRepo("app", map[string]string{"a/b.go": "package a\n", "README.md": "# app\n"})
	e.store.projects["app"] = domain.ProjectRecord{ID: "app", Path: e.repo, Config: domain.ProjectConfig{DefaultBranch: "main"}}
	e.store.lado["app"] = true
	e.store.sessions["app-1"] = domain.SessionRecord{ID: "app-1", ProjectID: "app", Metadata: domain.SessionMetadata{WorkspacePath: e.repo}}
	e.svc = New(Options{StateDir: e.state, Store: e.store, Git: e.git, ClaudeDir: filepath.Join(e.state, "claude"), Now: func() time.Time { return e.now }})
	return e
}

// newRepo makes a repository with files committed on main and
// refs/remotes/origin/main at that commit.
func (e *env) newRepo(name string, files map[string]string) string {
	e.t.Helper()
	dir := filepath.Join(e.t.TempDir(), name)
	gitIn(e.t, "", "init", "-q", "-b", "main", dir)
	for k, v := range map[string]string{"user.name": "LADO", "user.email": "lado@example.com", "commit.gpgsign": "false"} {
		gitIn(e.t, dir, "config", k, v)
	}
	for path, content := range files {
		writeFile(e.t, filepath.Join(dir, filepath.FromSlash(path)), content)
	}
	gitIn(e.t, dir, "add", "-A")
	gitIn(e.t, dir, "commit", "-q", "-m", "start")
	gitIn(e.t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func intp(n int) *int { return &n }

func note(title, kind string, sources ...string) []byte {
	var b strings.Builder
	b.WriteString("---\ntitle: " + title + "\nkind: " + kind + "\n")
	if len(sources) > 0 {
		b.WriteString("sources:\n")
		for _, s := range sources {
			b.WriteString("  - path: '" + s + "'\n")
		}
	}
	b.WriteString("---\n" + title + " 본문\n")
	return []byte(b.String())
}

func (e *env) write(session domain.SessionID, slug string, base *int, content []byte) (domain.LadoKnowledgeEntry, error) {
	return e.svc.Write(context.Background(), WriteInput{SessionID: session, Slug: slug, BaseRevision: base, Content: content})
}

func (e *env) mustWrite(session domain.SessionID, slug string, base *int, content []byte) domain.LadoKnowledgeEntry {
	e.t.Helper()
	entry, err := e.write(session, slug, base, content)
	if err != nil {
		e.t.Fatalf("write %s: %v", slug, err)
	}
	return entry
}

func (e *env) index(projectID domain.ProjectID) Index {
	e.t.Helper()
	idx, err := e.svc.Index(context.Background(), projectID)
	if err != nil {
		e.t.Fatalf("index: %v", err)
	}
	return idx
}

func stateOf(idx Index, slug string) domain.LadoKnowledgeState {
	for _, n := range idx.Notes {
		if n.Slug == slug {
			return n.State
		}
	}
	return "missing"
}

// wantCode checks err is an apierr with code and returns it.
func wantCode(t *testing.T, err error, code string) *apierr.Error {
	t.Helper()
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	return ae
}
