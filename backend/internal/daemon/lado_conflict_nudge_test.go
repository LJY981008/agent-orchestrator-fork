package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// LADO fork (#53): AO's merge-conflict nudge reaches a LADO run's pull
// request worker through the daemon's own path — lifecycle's urgent gate
// with the real agent registry, the mode-aware messenger bound to the
// session manager, LADO's worker gate (which lets AO's own daemon sends
// through) and the Chat relay. A single run's worker and a parts run's
// integration worker each get one nudge per conflicting observation, both
// idle after `ao report --done` and at a claude-code waiting_input prompt,
// and another once the conflict clears and comes back. An exited or
// terminated worker gets nothing: LADO's conflict resolver takes it over.

// ladoRelayedTurn is one Chat turn the session manager relayed.
type ladoRelayedTurn struct {
	id      domain.SessionID
	text    string
	options ports.MessageDeliveryOptions
}

// ladoRelayChat is the Chat launcher: it records the relayed turns. Any other
// launcher call panics, as nothing here starts or stops a controller.
type ladoRelayChat struct {
	sessionmanager.ChatLauncher
	mu    sync.Mutex
	turns []ladoRelayedTurn
}

func (c *ladoRelayChat) RelaySessionChatTurn(_ context.Context, id domain.SessionID, text, _ string, options ports.MessageDeliveryOptions) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.turns = append(c.turns, ladoRelayedTurn{id: id, text: text, options: options})
	return "turn", nil
}

func (c *ladoRelayChat) relayed() []ladoRelayedTurn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ladoRelayedTurn(nil), c.turns...)
}

// ladoPaneMessenger is the terminal messenger. A Chat worker's nudge must
// never reach it.
type ladoPaneMessenger struct {
	mu    sync.Mutex
	sends int
}

func (m *ladoPaneMessenger) Send(context.Context, domain.SessionID, string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sends++
	return nil
}

func (m *ladoPaneMessenger) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sends
}

// ladoSendOrigins wraps LADO's gate and records each send origin it is asked.
type ladoSendOrigins struct {
	ports.WorkGate
	mu      sync.Mutex
	origins []ports.SendOrigin
}

func (g *ladoSendOrigins) AuthorizeSend(ctx context.Context, target domain.SessionID, origin ports.SendOrigin) error {
	g.mu.Lock()
	g.origins = append(g.origins, origin)
	g.mu.Unlock()
	return g.WorkGate.AuthorizeSend(ctx, target, origin)
}

func (g *ladoSendOrigins) asked() []ports.SendOrigin {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]ports.SendOrigin(nil), g.origins...)
}

// seedLadoConflictWorker gives project mer in LADO mode a task whose
// approved plan is claimed from the queue and a Chat worker with harness
// that the run started: for role "single" the dispatcher's worker linked as
// the plan's execution, for role "integration" a parts run's integration
// worker linked under its spawn request. The worker is in state, its first
// hook seen.
func seedLadoConflictWorker(t *testing.T, store *sqlite.Store, role string, harness domain.AgentHarness, state domain.ActivityState, at time.Time) domain.SessionID {
	t.Helper()
	ctx := context.Background()
	origin := "https://gitlab.com/acme/demo.git"
	repo, err := domain.LadoTrackerRepoFromOrigin(origin)
	if err != nil {
		t.Fatalf("origin: %v", err)
	}
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: filepath.Join(t.TempDir(), "gone"), RepoOriginURL: origin, RegisteredAt: at}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := store.SetLadoMode(ctx, "mer", true, at); err != nil {
		t.Fatalf("turn LADO mode on: %v", err)
	}
	blobs := ladoblob.New(t.TempDir())
	t.Cleanup(func() { _ = blobs.Close() })
	plan, err := blobs.Put([]byte(ladoVerifyPlan))
	if err != nil {
		t.Fatalf("pin the plan: %v", err)
	}
	issue := strings.Repeat("1", 64)
	if _, err := store.CreateLadoTask(ctx, domain.LadoTask{
		ID: "lado-task-1", ProjectID: "mer", Repo: repo, IssueRef: domain.LadoIssueRef(domain.TrackerID{Provider: repo.Provider, Native: repo.Native + "#12"}),
		CreatedAt: at, UpdatedAt: at,
		Issue: domain.LadoObservedIssue{Title: "Add login", State: domain.IssueOpen, SnapshotSHA256: issue, ObservedAt: at},
	}); err != nil {
		t.Fatalf("CreateLadoTask: %v", err)
	}
	if _, err := store.CreateLadoPlanRequest(ctx, domain.LadoPlanRequest{ID: "req-1", TaskID: "lado-task-1", SessionID: "mer-orchestrator-1", IssueSnapshotSHA256: issue, RequestedAt: at}); err != nil {
		t.Fatalf("CreateLadoPlanRequest: %v", err)
	}
	rev, _, err := store.PinLadoPlanRevision(ctx, domain.LadoPlanRevision{
		ID: "lado-plan-1", TaskID: "lado-task-1", RequestID: "req-1", SessionID: "mer-orchestrator-1", Path: domain.LadoPlanPath("lado-task-1"),
		ArtifactVersion: 1, Source: "agent", SHA256: plan, BaseRef: "main", IssueSnapshotSHA256: issue, CreatedAt: at,
	})
	if err != nil {
		t.Fatalf("PinLadoPlanRevision: %v", err)
	}
	content, err := json.Marshal(map[string]string{"permissionCeiling": "auto"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := domain.LadoPolicySnapshot{ID: "lado-snapshot-1", SHA256: strings.Repeat("3", 64), Content: content}
	proof := domain.LadoPresenceProof{Nonce: "AAAAAAAAAAAAAAAAAAAAAA", Action: domain.LadoPresencePlanApprove, SubjectID: string(rev.ID), ExpiresAt: at.Add(time.Minute), UsedAt: at}
	event := domain.LadoAuditEvent{At: at, Actor: "human", Action: "plan.approved", SubjectKind: "plan_revision", SubjectID: string(rev.ID), Payload: []byte("{}")}
	if _, _, err := store.ApproveLadoPlan(ctx, domain.LadoPlanApproval{ID: "approval-1", RevisionID: rev.ID, TaskID: rev.TaskID, SHA256: plan, ApprovedAt: at},
		proof, event, &snapshot, &domain.LadoQueueEntry{ID: "lado-queue-1"}); err != nil {
		t.Fatalf("ApproveLadoPlan: %v", err)
	}
	claimed, err := store.ClaimLadoQueueEntries(ctx, at, at.Add(time.Hour), 1, nil, func() string { return "lease-1" })
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v; want lado-queue-1", claimed, err)
	}
	entry := claimed[0]

	requestID := domain.LadoClientRequestID(entry.ID, entry.AttemptCount)
	sha := strings.Repeat("a", 40)
	if role == "integration" {
		if err := store.StartLadoPartRun(ctx, entry.LeaseToken, domain.LadoPartRun{
			QueueEntryID: entry.ID, TaskID: "lado-task-1", ProjectID: "mer", PlanRevisionID: rev.ID, RepoDir: "/repo",
			Branch: "lado/task-1/integration", BaseSHA: sha, HeadSHA: sha, StartedAt: at,
		}, []domain.LadoTaskPart{{PartID: "a", Title: "A", Phase: 1, Steps: []int{1}, AffectedPaths: []string{"src/**"}}},
			domain.LadoAuditEvent{At: at, Actor: "lado", Action: "parts.started", SubjectKind: "queue_entry", SubjectID: string(entry.ID)}); err != nil {
			t.Fatalf("StartLadoPartRun: %v", err)
		}
		requestID = domain.LadoRunClientRequestID(domain.LadoRunRequest{Entry: entry.ID, Role: domain.LadoRoleIntegration, Attempt: 1})
		if ok, err := store.SetLadoPartSpawnRequest(ctx, entry.ID, requestID, at.Add(2*time.Minute)); !ok || err != nil {
			t.Fatalf("SetLadoPartSpawnRequest = %v, %v", ok, err)
		}
	}
	worker, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: harness, Mode: domain.SessionModeChat,
		ClientRequestID: requestID, ClientRequestHash: strings.Repeat("e", 64),
		CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if role == "integration" {
		if err := store.LinkLadoPartExecution(ctx, requestID, domain.LadoExecution{SessionID: worker.ID, QueueEntryID: entry.ID, Attempt: 1, Role: domain.LadoRoleIntegration,
			Branch: "lado/task-1/integration", StartSHA: sha, DispatchBaseSHA: sha, StartedAt: at}, nil, ""); err != nil {
			t.Fatalf("LinkLadoPartExecution: %v", err)
		}
	} else if err := store.LinkLadoExecution(ctx, entry.ID, entry.LeaseToken, domain.LadoExecution{SessionID: worker.ID, StartedAt: at}); err != nil {
		t.Fatalf("LinkLadoExecution: %v", err)
	}
	task, _, err := store.LadoTask(ctx, "lado-task-1")
	if err != nil || task.Execution.SessionID != worker.ID {
		t.Fatalf("task execution = %+v, %v; want the %s worker %s", task.Execution, err, role, worker.ID)
	}

	worker.Activity = domain.Activity{State: state, LastActivityAt: at}
	worker.FirstSignalAt = at
	if err := store.UpdateSession(ctx, worker); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	return worker.ID
}

// ladoConflictDaemon is the daemon's conflict nudge path over store.
type ladoConflictDaemon struct {
	lcm      *lifecycle.Manager
	sessions *sessionmanager.Manager
	chat     *ladoRelayChat
	pane     *ladoPaneMessenger
	gate     *ladoSendOrigins
}

// newLadoConflictDaemon wires what daemon.go wires for a lifecycle nudge:
// the lifecycle manager with startLifecycle's gates over the real agent
// registry, sending through the mode-aware messenger bound to the session
// manager, which LADO's gate guards. The lifecycle background observers are
// left out: they would race the observations the test drives.
func newLadoConflictDaemon(t *testing.T, store *sqlite.Store) ladoConflictDaemon {
	t.Helper()
	reg, err := buildAgentRegistry()
	if err != nil {
		t.Fatalf("agent registry: %v", err)
	}
	agents := agentRegistry{reg: reg}
	pane := &ladoPaneMessenger{}
	chat := &ladoRelayChat{}
	sessions := sessionmanager.New(sessionmanager.Deps{Agents: agents, Store: store, Messenger: pane, Chat: chat, DataDir: t.TempDir()})
	// wireLado builds the daemon's gate; the session manager gets it wrapped
	// so the test sees which origin each send was authorized as.
	built := &ladoGateSink{}
	wireLado(store, built, &ladoGateSink{})
	if built.gate == nil {
		t.Fatal("wireLado installed no gate")
	}
	gate := &ladoSendOrigins{WorkGate: built.gate}
	sessions.SetWorkGate(gate)

	messenger := newModeAwareMessenger()
	lcm := lifecycle.New(store, messenger,
		lifecycle.WithActiveSteering(activeTurnSteering(agents)),
		lifecycle.WithStartupSignalGate(startupSignalGatesInput(agents)),
		lifecycle.WithUrgentNudgeGate(urgentNudgeWaitingInputSafe(agents)),
	)
	messenger.Bind(sessionLifecycleMessenger{sessions})
	return ladoConflictDaemon{lcm: lcm, sessions: sessions, chat: chat, pane: pane, gate: gate}
}

// observe applies one SCM observation of the worker's pull request.
func (d ladoConflictDaemon) observe(t *testing.T, worker domain.SessionID, prURL string, state domain.Mergeability) {
	t.Helper()
	if err := d.lcm.ApplySCMObservation(context.Background(), worker, ports.SCMObservation{
		Fetched:      true,
		PR:           ports.SCMPRObservation{URL: prURL, Number: 9},
		Mergeability: ports.SCMMergeabilityObservation{State: string(state), Conflict: state == domain.MergeConflicting},
	}); err != nil {
		t.Fatalf("ApplySCMObservation(%s): %v", state, err)
	}
}

// writeLadoConflictPR writes the worker's open pull request, as AO's SCM
// observer does before it reports mergeability.
func writeLadoConflictPR(t *testing.T, store *sqlite.Store, worker domain.SessionID, at time.Time) string {
	t.Helper()
	const prURL = "https://gitlab.com/acme/demo/-/merge_requests/9"
	if err := store.WriteSCMObservation(context.Background(), domain.PullRequest{URL: prURL, SessionID: worker, Number: 9, UpdatedAt: at},
		nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write the pull request: %v", err)
	}
	return prURL
}

func TestLadoRunWorkerGetsAOsMergeConflictNudge(t *testing.T) {
	cases := []struct {
		name  string
		state domain.ActivityState
	}{
		{"idle after done", domain.ActivityIdle},
		{"waiting_input", domain.ActivityWaitingInput},
	}
	for _, role := range []string{"single", "integration"} {
		for _, tc := range cases {
			t.Run(role+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				store := sqlitetest.MustOpen(t)
				at := time.Now().UTC().Truncate(time.Second)
				worker := seedLadoConflictWorker(t, store, role, domain.HarnessClaudeCode, tc.state, at)
				if _, err := store.CreateReport(ctx, domain.ReportRecord{
					ID: "report-done", SessionID: worker, ProjectID: "mer", State: domain.ReportDone, Note: "PR opened",
					CreatedAt: at, AvailableAt: at.Add(domain.ReportSettlementWindow), SettlementDeadline: at.Add(domain.ReportSettlementWindow),
					DeliveryState: domain.ReportPending, RepeatCount: 1,
				}); err != nil {
					t.Fatalf("report done: %v", err)
				}
				prURL := writeLadoConflictPR(t, store, worker, at)
				d := newLadoConflictDaemon(t, store)

				d.observe(t, worker, prURL, domain.MergeConflicting)
				d.observe(t, worker, prURL, domain.MergeConflicting)
				turns := d.chat.relayed()
				if len(turns) != 1 || turns[0].id != worker || !strings.Contains(turns[0].text, "merge conflicts") {
					t.Fatalf("relayed turns after two conflicting observations = %+v, want one merge-conflict nudge to %s", turns, worker)
				}
				if turns[0].options.AuthoredByUser || turns[0].options.SenderSessionID != "" {
					t.Fatalf("nudge delivery options = %+v, want AO's own send", turns[0].options)
				}

				d.observe(t, worker, prURL, domain.MergeMergeable)
				d.observe(t, worker, prURL, domain.MergeConflicting)
				turns = d.chat.relayed()
				if len(turns) != 2 || turns[1].id != worker || !strings.Contains(turns[1].text, "merge conflicts") {
					t.Fatalf("relayed turns after the conflict came back = %+v, want a second nudge", turns)
				}
				for _, origin := range d.gate.asked() {
					if origin != ports.SendOriginDaemon {
						t.Fatalf("LADO gate asked for origins %v, want only %q", d.gate.asked(), ports.SendOriginDaemon)
					}
				}
				if got := len(d.gate.asked()); got != 2 {
					t.Fatalf("LADO gate asked %d times, want once per nudge", got)
				}
				if d.pane.count() != 0 {
					t.Fatalf("a Chat worker's nudge reached the terminal messenger %d times", d.pane.count())
				}

				// The gate is live for this worker: another session's message
				// is refused while AO's own nudge went through.
				err := d.sessions.SendWithOptions(ctx, worker, "rebase now", nil, ports.MessageDeliveryOptions{SenderSessionID: "mer-orchestrator-1"})
				var apiErr *apierr.Error
				if !errors.As(err, &apiErr) || apiErr.Code != "LADO_SEND_REFUSED" {
					t.Fatalf("session-origin send = %v, want LADO_SEND_REFUSED", err)
				}
				if len(d.chat.relayed()) != 2 {
					t.Fatal("a refused send reached the worker")
				}
			})
		}
	}
}

// codex reports a permission prompt as waiting_input, so the urgent nudge
// waits there for a LADO worker too.
func TestLadoRunWorkerAtCodexWaitingInputGetsNoConflictNudge(t *testing.T) {
	for _, role := range []string{"single", "integration"} {
		t.Run(role, func(t *testing.T) {
			store := sqlitetest.MustOpen(t)
			at := time.Now().UTC().Truncate(time.Second)
			worker := seedLadoConflictWorker(t, store, role, domain.HarnessCodex, domain.ActivityWaitingInput, at)
			prURL := writeLadoConflictPR(t, store, worker, at)
			d := newLadoConflictDaemon(t, store)
			d.observe(t, worker, prURL, domain.MergeConflicting)
			if turns := d.chat.relayed(); len(turns) != 0 {
				t.Fatalf("relayed %+v, want nothing at codex's waiting_input", turns)
			}
		})
	}
}

func TestLadoRunWorkerExitedOrTerminatedGetsNoConflictNudge(t *testing.T) {
	gone := []struct {
		name string
		mark func(*domain.SessionRecord)
	}{
		{"exited", func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityExited }},
		{"terminated", func(r *domain.SessionRecord) { r.IsTerminated = true }},
	}
	for _, role := range []string{"single", "integration"} {
		for _, g := range gone {
			t.Run(role+"/"+g.name, func(t *testing.T) {
				ctx := context.Background()
				store := sqlitetest.MustOpen(t)
				at := time.Now().UTC().Truncate(time.Second)
				worker := seedLadoConflictWorker(t, store, role, domain.HarnessClaudeCode, domain.ActivityIdle, at)
				rec, _, err := store.GetSession(ctx, worker)
				if err != nil {
					t.Fatal(err)
				}
				g.mark(&rec)
				if err := store.UpdateSession(ctx, rec); err != nil {
					t.Fatalf("mark %s: %v", g.name, err)
				}
				if rec, _, _ = store.GetSession(ctx, worker); rec.Activity.State != domain.ActivityExited && !rec.IsTerminated {
					t.Fatalf("worker = %+v, want it %s", rec.Activity, g.name)
				}
				prURL := writeLadoConflictPR(t, store, worker, at)
				d := newLadoConflictDaemon(t, store)
				d.observe(t, worker, prURL, domain.MergeConflicting)
				d.observe(t, worker, prURL, domain.MergeMergeable)
				d.observe(t, worker, prURL, domain.MergeConflicting)
				if turns := d.chat.relayed(); len(turns) != 0 {
					t.Fatalf("relayed %+v, want nothing to a %s worker", turns, g.name)
				}
				if asked := d.gate.asked(); len(asked) != 0 {
					t.Fatalf("LADO gate asked %v, want no send attempted", asked)
				}
			})
		}
	}
}
