package ladodbchange

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// t0 is when a test starts.
var t0 = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

// fakeStore is an in-memory Store. Its zero value is not ready; use
// newFakeStore.
type fakeStore struct {
	mu sync.Mutex

	projects   map[string]domain.ProjectRecord
	workspaces map[string][]domain.WorkspaceRepoRecord
	tasks      map[domain.LadoTaskID]domain.LadoTask
	executions map[domain.SessionID]domain.LadoExecution
	records    map[string]domain.LadoVerification // session NUL head
	prs        map[string]domain.PullRequest
	approvals  []domain.LadoIntegrationApproval
	notices    []domain.LadoOrchestratorNotice
	lado       map[domain.ProjectID]domain.LadoProjectSettings

	settings     map[string]domain.LadoDBChangeSettings // project NUL repo
	applications []domain.LadoDBChangeApplication
	runs         []domain.LadoDBChangeRun
	holds        map[string]time.Time
	usedProofs   map[string]bool
	audit        []domain.LadoAuditEvent

	// failSet, when set, is what SetLadoDBChangeSettings returns.
	failSet error
	// readErr, when set, fails the reads of applied paths.
	readErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		projects: map[string]domain.ProjectRecord{}, workspaces: map[string][]domain.WorkspaceRepoRecord{},
		tasks: map[domain.LadoTaskID]domain.LadoTask{}, executions: map[domain.SessionID]domain.LadoExecution{},
		records: map[string]domain.LadoVerification{}, prs: map[string]domain.PullRequest{},
		lado: map[domain.ProjectID]domain.LadoProjectSettings{}, settings: map[string]domain.LadoDBChangeSettings{},
		holds: map[string]time.Time{}, usedProofs: map[string]bool{},
	}
}

var _ Store = (*fakeStore)(nil)

func settingsKey(project domain.ProjectID, repo string) string {
	return string(project) + "\x00" + repo
}

func (f *fakeStore) LadoDBChangeSettings(_ context.Context, project domain.ProjectID) ([]domain.LadoDBChangeSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LadoDBChangeSettings
	for _, s := range f.settings {
		if s.ProjectID == project {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b domain.LadoDBChangeSettings) int { return strings.Compare(a.Repo, b.Repo) })
	return out, nil
}

func (f *fakeStore) LadoDBChangeSetting(_ context.Context, project domain.ProjectID, repo string) (domain.LadoDBChangeSettings, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.settings[settingsKey(project, repo)]
	return s, ok, nil
}

func (f *fakeStore) SetLadoDBChangeSettings(_ context.Context, s domain.LadoDBChangeSettings, base string, baseline []domain.LadoDBChangeApplication, proof *domain.LadoPresenceProof, audit []domain.LadoAuditEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSet != nil {
		return f.failSet
	}
	current, ok := f.settings[settingsKey(s.ProjectID, s.Repo)]
	want := ""
	if ok {
		want = current.SHA256()
	}
	if want != base {
		return domain.ErrLadoDBChangeSettingsChanged
	}
	if proof != nil {
		if f.usedProofs[proof.Nonce] {
			return domain.ErrLadoPresenceReplayed
		}
		f.usedProofs[proof.Nonce] = true
	}
	f.settings[settingsKey(s.ProjectID, s.Repo)] = s
	f.applications = append(f.applications, baseline...)
	f.audit = append(f.audit, audit...)
	return nil
}

func (f *fakeStore) LadoDBChangeAppliedPaths(_ context.Context, project domain.ProjectID, repo string, paths []string) (map[string]domain.LadoDBChangeApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return nil, f.readErr
	}
	out := map[string]domain.LadoDBChangeApplication{}
	for _, a := range f.applications {
		if a.ProjectID == project && a.Repo == repo && slices.Contains(paths, a.Path) && a.Result.CountsApplied() {
			out[a.Path] = a
		}
	}
	return out, nil
}

func (f *fakeStore) ListLadoDBChangeApplications(_ context.Context, project domain.ProjectID, repo string, paths []string) ([]domain.LadoDBChangeApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LadoDBChangeApplication
	for _, a := range f.applications {
		if a.ProjectID == project && a.Repo == repo && slices.Contains(paths, a.Path) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) StartLadoDBChangeApplication(_ context.Context, a domain.LadoDBChangeApplication) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applications = append(f.applications, a)
	return nil
}

func (f *fakeStore) FinishLadoDBChangeApplication(_ context.Context, id string, result domain.LadoDBChangeResult, exitCode *int, outputSHA256, message string, at time.Time, audit domain.LadoAuditEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, a := range f.applications {
		if a.ID == id {
			finished := at
			f.applications[i].Result, f.applications[i].ExitCode = result, exitCode
			f.applications[i].OutputSHA256, f.applications[i].Message, f.applications[i].FinishedAt = outputSHA256, message, &finished
			f.audit = append(f.audit, audit)
			return nil
		}
	}
	return fmt.Errorf("no application %s", id)
}

func (f *fakeStore) InterruptLadoDBChangeApplications(_ context.Context, at time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for i, a := range f.applications {
		if a.Result == domain.LadoDBChangeResultRunning {
			finished := at
			f.applications[i].Result, f.applications[i].FinishedAt = domain.LadoDBChangeResultInterrupted, &finished
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) EnsureLadoDBChangeRun(_ context.Context, r domain.LadoDBChangeRun) (domain.LadoDBChangeRun, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, run := range f.runs {
		if run.PRURL == r.PRURL && run.HeadSHA == r.HeadSHA && run.Kind == r.Kind {
			return run, false, nil
		}
	}
	f.runs = append(f.runs, r)
	return r, true, nil
}

func (f *fakeStore) LadoDBChangeRun(_ context.Context, id string) (domain.LadoDBChangeRun, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, run := range f.runs {
		if run.ID == id {
			return run, true, nil
		}
	}
	return domain.LadoDBChangeRun{}, false, nil
}

func (f *fakeStore) TransitionLadoDBChangeRun(_ context.Context, id string, from []domain.LadoDBChangeRunState, to domain.LadoDBChangeRunState, attempts int, errMsg string, at time.Time, proof *domain.LadoPresenceProof, audit *domain.LadoAuditEvent) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, run := range f.runs {
		if run.ID != id {
			continue
		}
		if !slices.Contains(from, run.State) {
			return false, nil
		}
		if proof != nil {
			if f.usedProofs[proof.Nonce] {
				return false, domain.ErrLadoPresenceReplayed
			}
			f.usedProofs[proof.Nonce] = true
		}
		f.runs[i].State, f.runs[i].Attempts, f.runs[i].Error, f.runs[i].UpdatedAt = to, attempts, errMsg, at
		if audit != nil {
			f.audit = append(f.audit, *audit)
		}
		return true, nil
	}
	return false, nil
}

func (f *fakeStore) ListLadoDBChangeRunsByState(_ context.Context, states []domain.LadoDBChangeRunState, limit int) ([]domain.LadoDBChangeRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LadoDBChangeRun
	for _, run := range f.runs {
		if slices.Contains(states, run.State) && len(out) < limit {
			out = append(out, run)
		}
	}
	return out, nil
}

func (f *fakeStore) ListLadoDBChangeRunsForExecution(_ context.Context, session domain.SessionID) ([]domain.LadoDBChangeRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LadoDBChangeRun
	for _, run := range f.runs {
		if run.ExecutionSessionID == session {
			out = append(out, run)
		}
	}
	return out, nil
}

func (f *fakeStore) ListLadoDBChangeRunsForProject(_ context.Context, project domain.ProjectID) ([]domain.LadoDBChangeRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LadoDBChangeRun
	for _, run := range f.runs {
		if run.ProjectID == project && run.State != domain.LadoDBChangeRunDone {
			out = append(out, run)
		}
	}
	return out, nil
}

func (f *fakeStore) HoldLadoIntegrationStatus(_ context.Context, approvalID string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holds[approvalID] = at
	return nil
}

func (f *fakeStore) ReleaseLadoIntegrationStatus(_ context.Context, approvalID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.holds, approvalID)
	return nil
}

func (f *fakeStore) LadoIntegrationStatusHeld(_ context.Context, approvalID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.holds[approvalID]
	return ok, nil
}

func (f *fakeStore) LadoExecution(_ context.Context, session domain.SessionID) (domain.LadoExecution, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	exec, ok := f.executions[session]
	return exec, ok, nil
}

func (f *fakeStore) GetLadoVerification(_ context.Context, session domain.SessionID, head string) (domain.LadoVerification, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.records[string(session)+"\x00"+head]
	return v, ok, nil
}

func (f *fakeStore) GetPR(_ context.Context, url string) (domain.PullRequest, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, ok := f.prs[url]
	return pr, ok, nil
}

func (f *fakeStore) ListLadoIntegrationApprovals(_ context.Context, session domain.SessionID, prURL, head string) ([]domain.LadoIntegrationApproval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LadoIntegrationApproval
	for _, a := range f.approvals {
		if a.ExecutionSessionID == session && (prURL == "" || a.PRURL == prURL) && (head == "" || a.HeadSHA == head) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) AddLadoOrchestratorNotice(_ context.Context, n domain.LadoOrchestratorNotice) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.notices {
		if existing.ProjectID == n.ProjectID && existing.Key == n.Key {
			return false, nil
		}
	}
	f.notices = append(f.notices, n)
	return true, nil
}

func (f *fakeStore) LadoPresenceProofUsed(_ context.Context, nonce string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.usedProofs[nonce], nil
}

func (f *fakeStore) LadoProjectSettings(_ context.Context, project domain.ProjectID) (domain.LadoProjectSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lado[project], nil
}

func (f *fakeStore) LadoTask(_ context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	task, ok := f.tasks[id]
	return task, ok, nil
}

func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	return p, ok, nil
}

func (f *fakeStore) ListWorkspaceRepos(_ context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.workspaces[projectID]), nil
}

// fakeBlobs is an in-memory blob store.
type fakeBlobs struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func (b *fakeBlobs) Put(data []byte) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.blobs == nil {
		b.blobs = map[string][]byte{}
	}
	sum := sha256Hex(string(data))
	b.blobs[sum] = slices.Clone(data)
	return sum, nil
}

func (b *fakeBlobs) Get(sum string, limit int64) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.blobs[sum]
	if !ok {
		return nil, os.ErrNotExist
	}
	if int64(len(data)) > limit {
		data = data[:limit]
	}
	return slices.Clone(data), nil
}

// testProject is the project of the tests: a single repository at dir.
const testProject domain.ProjectID = "app"

// newTestService returns a service over store with a state folder of its
// own and a clock at t0.
func newTestService(t *testing.T, store *fakeStore) *Service {
	t.Helper()
	n := 0
	return New(Deps{
		Store: store, Blobs: &fakeBlobs{}, StateDir: t.TempDir(),
		Clock: func() time.Time { return t0 },
		NewID: func() string { n++; return fmt.Sprintf("id-%d", n) },
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// testRepo is a bare origin and the developer's clone of it (dir), where
// LADO reads. Each repository has its own identity and no signing.
type testRepo struct {
	t           *testing.T
	origin, dir string
}

// newTestRepo makes origin with one commit on main holding files and clones
// it into dir, so refs/remotes/origin/HEAD names main.
func newTestRepo(t *testing.T, files map[string]string) *testRepo {
	t.Helper()
	root := t.TempDir()
	r := &testRepo{t: t, origin: filepath.Join(root, "origin.git"), dir: filepath.Join(root, "app")}
	seed := filepath.Join(root, "seed")
	r.git(root, "init", "-q", "--bare", "-b", "main", r.origin)
	r.git(root, "init", "-q", "-b", "main", seed)
	r.configure(seed)
	if files == nil {
		files = map[string]string{"README.md": "# app\n"}
	}
	r.commit(seed, files, nil, "Start")
	r.git(seed, "push", "-q", r.origin, "main")
	r.git(root, "clone", "-q", r.origin, r.dir)
	r.configure(r.dir)
	return r
}

func (r *testRepo) configure(dir string) {
	r.t.Helper()
	r.git(dir, "config", "user.name", "LADO")
	r.git(dir, "config", "user.email", "lado@example.com")
	r.git(dir, "config", "commit.gpgsign", "false")
}

// git runs git in dir and returns its output without the trailing line
// break.
func (r *testRepo) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// commit writes files, removes the paths of gone and commits all in dir,
// returning the commit.
func (r *testRepo) commit(dir string, files map[string]string, gone []string, message string) string {
	r.t.Helper()
	for path, content := range files {
		file := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	for _, path := range gone {
		r.git(dir, "rm", "-q", "--", path)
	}
	r.git(dir, "add", "-A")
	r.git(dir, "commit", "-q", "--allow-empty", "-m", message)
	return r.git(dir, "rev-parse", "HEAD")
}

// push commits files on origin's main from another clone, as another
// developer would.
func (r *testRepo) push(files map[string]string, message string) string {
	r.t.Helper()
	other := filepath.Join(r.t.TempDir(), "other")
	r.git(filepath.Dir(other), "clone", "-q", r.origin, other)
	r.configure(other)
	sha := r.commit(other, files, nil, message)
	r.git(other, "push", "-q", "origin", "main")
	return sha
}

// blob returns the blob of path at commit in dir.
func (r *testRepo) blob(commit, path string) string {
	r.t.Helper()
	return r.git(r.dir, "rev-parse", commit+":"+path)
}

// addProject registers a single-repository project at dir.
func (f *fakeStore) addProject(id domain.ProjectID, dir string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[string(id)] = domain.ProjectRecord{ID: string(id), Path: dir, DisplayName: string(id)}
}

// presenceOK is a presence check that hands out a fresh proof per call and
// records what it was asked.
type presenceOK struct {
	asked []string
	n     int
}

func (p *presenceOK) fn(action, subject string) (domain.LadoPresenceProof, error) {
	p.asked = append(p.asked, action+" "+subject)
	p.n++
	return domain.LadoPresenceProof{Nonce: fmt.Sprintf("nonce-%d", p.n), Action: action, SubjectID: subject}, nil
}

func TestNewFillsDefaults(t *testing.T) {
	s := New(Deps{})
	if s.deps.Git == nil || s.deps.Run == nil || s.deps.Clock == nil || s.deps.NewID == nil || s.deps.Log == nil {
		t.Fatalf("defaults missing: %+v", s.deps)
	}
	s.Kick()
	s.Kick() // a second kick while one waits does not block
	if len(s.kick) != 1 {
		t.Fatalf("kick channel holds %d", len(s.kick))
	}
}
