package ladotask

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakePlanSessions is AO's session service for plan workers: every spawn is a
// Chat session with an artifact directory of its own, busy until a test says
// its turn ended.
type fakePlanSessions struct {
	t         *testing.T
	mu        sync.Mutex
	records   map[domain.SessionID]domain.SessionRecord
	spawned   []ports.SpawnConfig
	sent      map[domain.SessionID][]string
	stopped   []domain.SessionID
	spawnErrs []error
	next      int
}

func newFakePlanSessions(t *testing.T) *fakePlanSessions {
	return &fakePlanSessions{t: t, records: map[domain.SessionID]domain.SessionRecord{}, sent: map[domain.SessionID][]string{}}
}

func (f *fakePlanSessions) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spawned = append(f.spawned, cfg)
	if len(f.spawnErrs) > 0 {
		err := f.spawnErrs[0]
		f.spawnErrs = f.spawnErrs[1:]
		if err != nil {
			return domain.Session{}, 0, 0, err
		}
	}
	f.next++
	id := domain.SessionID(fmt.Sprintf("demo-plan-%d", f.next))
	f.records[id] = domain.SessionRecord{
		ID: id, ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: cfg.Harness, Mode: cfg.RequestedMode,
		ClientRequestID: cfg.ClientRequestID, ClientRequestHash: cfg.ClientRequestHash, ClientRequestCommitted: true,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{ArtifactDir: f.t.TempDir()},
	}
	return domain.Session{ID: id}, 0, 0, nil
}

func (f *fakePlanSessions) Session(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.records[id]
	return rec, ok, nil
}

func (f *fakePlanSessions) SessionByClientRequest(_ context.Context, id string) (domain.SessionRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.records {
		if rec.ClientRequestID == id {
			return rec, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}

func (f *fakePlanSessions) Send(_ context.Context, id domain.SessionID, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.records[id]
	if !ok || rec.IsTerminated {
		return errors.New("session gone")
	}
	f.sent[id] = append(f.sent[id], message)
	// A message starts a turn.
	rec.Activity = domain.Activity{State: domain.ActivityActive, LastActivityAt: rec.Activity.LastActivityAt}
	f.records[id] = rec
	return nil
}

func (f *fakePlanSessions) Stop(_ context.Context, id domain.SessionID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.records[id]
	rec.IsTerminated = true
	f.records[id] = rec
	f.stopped = append(f.stopped, id)
	return nil
}

// idle ends the session's turn at at.
func (f *fakePlanSessions) idle(id domain.SessionID, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.records[id]
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: at}
	f.records[id] = rec
}

// end ends the session, as when its agent exits.
func (f *fakePlanSessions) end(id domain.SessionID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.records[id]
	rec.IsTerminated = true
	f.records[id] = rec
}

func (f *fakePlanSessions) spawns() []ports.SpawnConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ports.SpawnConfig(nil), f.spawned...)
}

func (f *fakePlanSessions) messages(id domain.SessionID) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent[id]...)
}

func (f *fakePlanSessions) stops() []domain.SessionID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.SessionID(nil), f.stopped...)
}

func (f *fakePlanSessions) record(id domain.SessionID) domain.SessionRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.records[id]
}

// fakeQueue is the dispatcher's base resolution and memory reading.
type fakeQueue struct {
	mu    sync.Mutex
	tight bool
	err   error
}

func (q *fakeQueue) WorkerBase(context.Context, domain.LadoTask) (ports.WorkspaceDefaultBranch, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return ports.WorkspaceDefaultBranch{}, q.err
	}
	return ports.WorkspaceDefaultBranch{Branch: "main", Remote: "origin", BaseRef: "origin/main"}, nil
}

func (q *fakeQueue) MemoryTight(context.Context) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.tight
}

// designer returns the request's designer session, once it started.
func (pf *planFixture) designer(t *testing.T, id domain.LadoPlanRequestID) domain.SessionID {
	t.Helper()
	req, ok, err := pf.store.LadoPlanRequest(context.Background(), id)
	if err != nil || !ok || req.WorkerSession() == "" {
		t.Fatalf("request %s = %+v (%v %v), want a designer", id, req, ok, err)
	}
	return req.WorkerSession()
}

// request reads a request back.
func (pf *planFixture) request(t *testing.T, id domain.LadoPlanRequestID) domain.LadoPlanRequest {
	t.Helper()
	req, ok, err := pf.store.LadoPlanRequest(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("request %s = (%v, %v)", id, ok, err)
	}
	return req
}

// capture records the plan file the daemon wrote into a designer's artifact
// directory as an AO artifact version, as AO's capture does.
func (pf *planFixture) capture(t *testing.T, session domain.SessionID) domain.SessionArtifactVersion {
	t.Helper()
	rec := pf.sessions.record(session)
	content, err := os.ReadFile(filepath.Join(rec.Metadata.ArtifactDir, filepath.FromSlash(domain.LadoPlanPath(pf.task.ID))))
	if err != nil {
		t.Fatalf("read the plan file of %s: %v", session, err)
	}
	return pf.artifacts.write(session, domain.LadoPlanPath(pf.task.ID), string(content), domain.ArtifactVersionSourceAgent, pf.now)
}

// workerPlan is a plan file naming paths, with a review section saying
// review, empty for none.
func (pf *planFixture) workerPlan(source, review string, paths ...string) string {
	affected := ""
	for _, p := range paths {
		affected += "\n  - " + p
	}
	body := "Plan."
	if review != "" {
		body += "\n\n## 계획 검수\n\n" + review
	}
	return fmt.Sprintf(`---
task: %s
issue: %s
base_ref: main
steps:
  - Add the login route
acceptance_criteria:
  - A user can sign in
affected_paths:%s
test_commands:
  - go test ./...
risk: low
permission_ceiling: accept-edits
review:
  required: true
plan_source: %s
---

%s
`, pf.task.ID, pf.task.IssueRef, affected, source, body)
}
