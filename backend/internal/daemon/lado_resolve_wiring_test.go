package daemon

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// LADO fork (#53): the SCM observer keeps observing the open PR of a LADO
// run whose worker session ended, through the store's list of executions
// not settled yet, so the dispatcher sees the PR conflict and starts a
// resolve worker. Once the PR merged, the run is no longer listed.
func TestSCMObserverConfigKeepsObservingEndedLadoRuns(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	at := time.Now().UTC().Truncate(time.Second)
	cfg := scmObserverConfig(store, nil, nil, slog.New(slog.DiscardHandler))
	if cfg.RetainedSessions == nil {
		t.Fatal("RetainedSessions is nil; the daemon must give the observer LADO's unsettled runs")
	}

	worker := seedLadoConflictWorker(t, store, "single", domain.HarnessClaudeCode, domain.ActivityIdle, at)
	rec, _, err := store.GetSession(ctx, worker)
	if err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	if err := store.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate the worker: %v", err)
	}
	if ids, err := cfg.RetainedSessions(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("RetainedSessions before a PR = %v, %v; want none", ids, err)
	}
	prURL := writeLadoConflictPR(t, store, worker, at)
	ids, err := cfg.RetainedSessions(ctx)
	if err != nil || !slices.Contains(ids, worker) {
		t.Fatalf("RetainedSessions = %v, %v; want the ended worker %s of the open PR", ids, err, worker)
	}

	pr, ok, err := store.GetPR(ctx, prURL)
	if err != nil || !ok {
		t.Fatalf("GetPR = %v, %v", ok, err)
	}
	pr.Merged, pr.UpdatedAt = true, at.Add(time.Minute)
	if err := store.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("merge the PR: %v", err)
	}
	if ids, err := cfg.RetainedSessions(ctx); err != nil || slices.Contains(ids, worker) {
		t.Fatalf("RetainedSessions after the merge = %v, %v; want the worker gone", ids, err)
	}
}

// The dispatcher tells a running resolve worker about a new conflict through
// the session manager, as the verification loop tells a worker what failed;
// without the session manager it tells nobody.
func TestLadoQueueSenderSendsThroughTheSessionManager(t *testing.T) {
	if ladoQueueSender(nil) != nil {
		t.Fatal("ladoQueueSender(nil) is not nil; the dispatcher would send into nothing")
	}
	sessions := &recordingSender{}
	sender := ladoQueueSender(sessions)
	if err := sender.Send(context.Background(), "mer-9", "conflicts again"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if want := []string{"mer-9: conflicts again"}; !slices.Equal(sessions.sent, want) {
		t.Fatalf("sent %v, want %v", sessions.sent, want)
	}
}
