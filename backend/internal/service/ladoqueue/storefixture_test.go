package ladoqueue

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// storeEntry is the queue entry the store fixture's approval wrote.
const storeEntry domain.LadoQueueEntryID = "lado-queue-1"

// storeFixture is a dispatcher over a real store that holds one approved
// task, queued and passing every check; around the store are the fixture's
// fakes. AO's sessions are rows of the same store: a spawn creates the
// attempt's client request session and commits it, as AO does.
type storeFixture struct {
	*fixture
	db *store.Store
	// dir holds the store's database.
	dir string
}

// newStoreFixture seeds the task the way LADO makes one: a plan request, the
// pinned revision (changed by change first, when given) and its approval
// with a rule snapshot, which queues the task.
func newStoreFixture(t *testing.T, change func(*domain.LadoPlanRevision)) *storeFixture {
	t.Helper()
	ctx := context.Background()
	f := newFixture(t)
	dir := t.TempDir()
	db := sqlitetest.MustOpenAt(t, dir)
	at := t0.Add(-2 * time.Hour)
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	must("register the project", db.UpsertProject(ctx, domain.ProjectRecord{ID: "proj-1", Path: "/work/app", RegisteredAt: at}))
	_, err := db.SetLadoMode(ctx, "proj-1", true, at)
	must("turn LADO mode on", err)
	task := testTask()
	task.Plan, task.CreatedAt, task.UpdatedAt = domain.LadoTaskPlan{}, at, at
	_, err = db.CreateLadoTask(ctx, task)
	must("create the task", err)
	_, err = db.CreateLadoPlanRequest(ctx, domain.LadoPlanRequest{
		ID: "req-2", TaskID: "task-1", SessionID: "proj-1-orchestrator-1", IssueSnapshotSHA256: pinnedIssueSHA(), RequestedAt: at,
	})
	must("request the plan", err)
	rev := testRevision()
	rev.SessionID, rev.Path, rev.ArtifactVersion, rev.Source = "proj-1-orchestrator-1", domain.LadoPlanPath("task-1"), 1, "agent"
	if change != nil {
		change(&rev)
	}
	_, _, err = db.PinLadoPlanRevision(ctx, rev)
	must("pin the revision", err)
	approval, snapshot := testApproval(), testSnapshot(claudeAgent(), false)
	_, _, err = db.ApproveLadoPlan(ctx, approval,
		domain.LadoPresenceProof{
			Nonce: "nonce-1-AAAAAAAAAAAAAAAA", Action: domain.LadoPresencePlanApprove, SubjectID: string(rev.ID),
			ExpiresAt: approval.ApprovedAt.Add(time.Minute), UsedAt: approval.ApprovedAt,
		},
		domain.LadoAuditEvent{At: approval.ApprovedAt, Actor: "human", Action: "plan.approved", SubjectKind: "plan_revision", SubjectID: string(rev.ID)},
		&snapshot, &domain.LadoQueueEntry{ID: storeEntry})
	must("approve the plan", err)
	f.sessions.start = func(cfg ports.SpawnConfig) (domain.SessionID, error) {
		rec, fresh, err := db.CreateClientRequestSession(ctx, domain.SessionRecord{
			ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: cfg.Harness,
			ClientRequestID: cfg.ClientRequestID, ClientRequestHash: cfg.ClientRequestHash,
			CreatedAt: f.clock(), UpdatedAt: f.clock(),
		})
		if err == nil && !fresh {
			err = errors.New("the client request id was used before")
		}
		if err != nil {
			return "", err
		}
		return rec.ID, db.CommitClientRequestSession(ctx, rec.ID)
	}
	sf := &storeFixture{fixture: f, db: db, dir: dir}
	sf.svc = sf.dispatcher(db, f.git)
	return sf
}

// queueTask seeds another task as newStoreFixture seeds task-1: task-<key>
// in project, a LADO project already, for issue #<key>, approved now with its
// own revision, approval and rule snapshot. Its worker starts from them. It
// returns the task's queue entry, lado-queue-<key>.
func (sf *storeFixture) queueTask(t *testing.T, key string, project domain.ProjectID) domain.LadoQueueEntryID {
	t.Helper()
	ctx := context.Background()
	at := sf.clock()
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("task-%s: %s: %v", key, what, err)
		}
	}
	task := testTask()
	task.ID, task.ProjectID, task.IssueRef = domain.LadoTaskID("task-"+key), project, domain.IssueID("github:acme/app#"+key)
	task.Plan, task.CreatedAt, task.UpdatedAt = domain.LadoTaskPlan{}, at, at
	_, err := sf.db.CreateLadoTask(ctx, task)
	must("create the task", err)
	orchestrator := domain.SessionID(string(project) + "-orchestrator-1")
	request := domain.LadoPlanRequestID("req-" + key)
	_, err = sf.db.CreateLadoPlanRequest(ctx, domain.LadoPlanRequest{
		ID: request, TaskID: task.ID, SessionID: orchestrator, IssueSnapshotSHA256: pinnedIssueSHA(), RequestedAt: at,
	})
	must("request the plan", err)
	rev := testRevision()
	rev.ID, rev.TaskID, rev.RequestID, rev.Number, rev.CreatedAt = domain.LadoPlanRevisionID("rev-"+key), task.ID, request, 1, at
	rev.SessionID, rev.Path, rev.ArtifactVersion, rev.Source = orchestrator, domain.LadoPlanPath(task.ID), 1, "agent"
	_, _, err = sf.db.PinLadoPlanRevision(ctx, rev)
	must("pin the revision", err)
	approval := domain.LadoPlanApproval{ID: domain.LadoPlanApprovalID("approval-" + key), RevisionID: rev.ID, TaskID: task.ID, SHA256: rev.SHA256, ApprovedAt: at}
	snapshot := testSnapshot(claudeAgent(), false)
	snapshot.ID, snapshot.ApprovalID = domain.LadoPolicySnapshotID("snap-"+key), approval.ID
	entry := domain.LadoQueueEntryID("lado-queue-" + key)
	_, _, err = sf.db.ApproveLadoPlan(ctx, approval,
		domain.LadoPresenceProof{
			Nonce: "nonce-" + key + "-AAAAAAAAAAAAAAAA", Action: domain.LadoPresencePlanApprove, SubjectID: string(rev.ID),
			ExpiresAt: at.Add(time.Minute), UsedAt: at,
		},
		domain.LadoAuditEvent{At: at, Actor: "human", Action: "plan.approved", SubjectKind: "plan_revision", SubjectID: string(rev.ID)},
		&snapshot, &domain.LadoQueueEntry{ID: entry})
	must("approve the plan", err)
	sf.tasks.mu.Lock()
	defer sf.tasks.mu.Unlock()
	if sf.tasks.inputs == nil {
		sf.tasks.inputs = map[domain.LadoTaskID]ladotask.RunInput{}
	}
	in := sf.tasks.input
	in.Revision, in.Approval, in.Snapshot = rev, approval, snapshot
	sf.tasks.inputs[task.ID] = in
	return entry
}

// taskChanges counts the changes the store published for a task.
func (sf *storeFixture) taskChanges(t *testing.T, task domain.LadoTaskID) int {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(sf.dir, "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	defer func() { _ = raw.Close() }()
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM change_log
		WHERE json_extract(payload, '$.kind') = 'lado_task' AND json_extract(payload, '$.taskId') = ?`, string(task)).Scan(&n); err != nil {
		t.Fatalf("count the changes of %s: %v", task, err)
	}
	return n
}

// dispatcher returns a new dispatcher over st, as a daemon that starts again
// builds one, with git.
func (sf *storeFixture) dispatcher(st Store, git Git) *Service {
	return New(Deps{
		Store: st, Tasks: sf.tasks, Policy: sf.policy, Sessions: sf.sessions,
		Projects: sf.projects, Branches: sf.branches, Memory: sf.memory, Git: git,
		Clock: sf.clock, NewID: sf.newID,
		Log: slog.New(slog.NewTextHandler(sf.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
}

// session returns the session AO keeps for the attempt of the store's entry.
func (sf *storeFixture) session(t *testing.T, attempt int) domain.SessionRecord {
	t.Helper()
	rec, found, err := sf.db.GetSessionByClientRequestID(context.Background(), domain.LadoClientRequestID(storeEntry, attempt))
	if err != nil || !found {
		t.Fatalf("session of attempt %d = (%v, %v), want one", attempt, found, err)
	}
	return rec
}

// execution returns the execution the session runs.
func (sf *storeFixture) execution(t *testing.T, id domain.SessionID) domain.LadoExecution {
	t.Helper()
	exec, ok, err := sf.db.LadoExecution(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("execution of %s = (%v, %v), want one", id, ok, err)
	}
	return exec
}

// crashBeforeLink stands for a daemon that stops after AO started the worker
// and before the dispatcher linked it: its first link fails, and nothing
// else does.
type crashBeforeLink struct {
	*store.Store
	crashed atomic.Bool
}

func (c *crashBeforeLink) LinkLadoExecution(ctx context.Context, id domain.LadoQueueEntryID, token string, exec domain.LadoExecution) error {
	if c.crashed.CompareAndSwap(false, true) {
		return errors.New("the daemon stopped")
	}
	return c.Store.LinkLadoExecution(ctx, id, token, exec)
}
