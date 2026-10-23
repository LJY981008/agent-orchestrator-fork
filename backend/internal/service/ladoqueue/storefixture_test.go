package ladoqueue

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
}

// newStoreFixture seeds the task the way LADO makes one: a plan request, the
// pinned revision (changed by change first, when given) and its approval
// with a rule snapshot, which queues the task.
func newStoreFixture(t *testing.T, change func(*domain.LadoPlanRevision)) *storeFixture {
	t.Helper()
	ctx := context.Background()
	f := newFixture(t)
	db := sqlitetest.MustOpen(t)
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
	sf := &storeFixture{fixture: f, db: db}
	sf.svc = sf.dispatcher(db, f.git)
	return sf
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
