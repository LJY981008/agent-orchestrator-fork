package ladoqueue

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// prSource is the source branch of the fixture's PR.
const prSource = "feat-7"

// resolveFixture is the store fixture whose task-1 ran and opened PR #7 from
// prSource into main of a real remote, a repository the project's clone
// fetches from as origin. Since the PR was opened, main moved with a change
// to the same file, so the PR conflicts.
type resolveFixture struct {
	*storeFixture
	// remote is the repository the clone fetches from.
	remote string
	// repo is the project's clone.
	repo string
	// exec is the session of task-1's run, the PR's.
	exec domain.SessionID
	// head is the PR's head and base the tip of main it conflicts with.
	head, base string
	sender     *fakeSender
}

func newResolveFixture(t *testing.T) *resolveFixture {
	t.Helper()
	remote, start := newPartsRepo(t)
	repo := t.TempDir()
	gitIn(t, repo, nil, "clone", "-q", remote, ".")
	sf := newStoreFixture(t, nil)
	sf.projects.project.Path = repo
	sf.git.sha = start
	// A stopped session ends in AO's rows.
	sf.sessions.ended = func(id domain.SessionID) { endStoreSession(t, sf, id) }
	sender := &fakeSender{}
	sf.svc.deps.Sender = sender
	sf.tick(t, t0)
	rf := &resolveFixture{storeFixture: sf, remote: remote, repo: repo, exec: sf.session(t, 1).ID, sender: sender}
	gitIn(t, remote, nil, "branch", prSource, start)
	rf.head = commitOn(t, remote, prSource, map[string]string{"shared.txt": "the plan's change\n"})
	rf.base = commitOn(t, remote, "main", map[string]string{"shared.txt": "the base's change\n"})
	rf.writePR(t, rf.head, domain.PRStateOpen, domain.MergeConflicting, t0.Add(time.Minute))
	return rf
}

// writePR writes PR #7 of the run as AO's SCM observer does.
func (rf *resolveFixture) writePR(t *testing.T, head string, state domain.PRState, mergeability domain.Mergeability, at time.Time) {
	t.Helper()
	if err := rf.db.WriteSCMObservation(context.Background(), domain.PullRequest{
		URL: "https://github.com/acme/app/pull/7", SessionID: rf.exec, Number: 7, Draft: state == domain.PRStateDraft,
		Merged: state == domain.PRStateMerged, Closed: state == domain.PRStateClosed,
		HeadSHA: head, SourceBranch: prSource, TargetBranch: "main", Title: "Change 7", Mergeability: mergeability, UpdatedAt: at,
	}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write the PR: %v", err)
	}
}

// exitRun is the agent of the run's session exiting: AO keeps the session.
func (rf *resolveFixture) exitRun(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	rec, ok, err := rf.db.GetSession(ctx, rf.exec)
	if err != nil || !ok {
		t.Fatalf("read session %s: %v, %v", rf.exec, ok, err)
	}
	rec.Activity.State = domain.ActivityExited
	if err := rf.db.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("exit session %s: %v", rf.exec, err)
	}
}

// resolve returns the resolve row of the run's PR.
func (rf *resolveFixture) resolve(t *testing.T) domain.LadoPRResolve {
	t.Helper()
	r, ok, err := rf.db.LadoPRResolve(context.Background(), rf.exec)
	if err != nil || !ok {
		t.Fatalf("resolve of %s = (%v, %v), want one", rf.exec, ok, err)
	}
	return r
}

// resolver returns the session AO keeps for resolve attempt n.
func (rf *resolveFixture) resolver(t *testing.T, n int) domain.SessionRecord {
	t.Helper()
	rec, found, err := rf.db.GetSessionByClientRequestID(context.Background(), domain.LadoResolveClientRequestID(storeEntry, n))
	if err != nil || !found {
		t.Fatalf("session of resolve attempt %d = (%v, %v), want one", n, found, err)
	}
	return rec
}

// resolveSpawns returns the resolve workers spawned, in order.
func (rf *resolveFixture) resolveSpawns() []ports.SpawnConfig {
	var got []ports.SpawnConfig
	for _, cfg := range rf.sessions.spawned() {
		if strings.Contains(cfg.ClientRequestID, "/_resolve/") {
			got = append(got, cfg)
		}
	}
	return got
}

// pushResolved is a resolve worker's push: the PR's source branch on the
// remote takes in the base.
func (rf *resolveFixture) pushResolved(t *testing.T) string {
	t.Helper()
	return commitOn(t, rf.remote, prSource, map[string]string{"shared.txt": "both changes\n"}, rf.head, rf.base)
}

func (rf *resolveFixture) taskReason(t *testing.T) string {
	t.Helper()
	task, ok, err := rf.db.LadoTask(context.Background(), "task-1")
	if err != nil || !ok {
		t.Fatalf("read task-1 = (%v, %v)", ok, err)
	}
	return task.StateReason()
}

func (rf *resolveFixture) alerts(t *testing.T) []domain.LadoAlert {
	t.Helper()
	alerts, err := rf.db.ListLadoAlertsByTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("read the alerts: %v", err)
	}
	var got []domain.LadoAlert
	for _, a := range alerts {
		if a.Kind == domain.LadoAlertConflictUnresolved {
			got = append(got, a)
		}
	}
	return got
}

// fakeSender records the messages sent to sessions.
type fakeSender struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeSender) Send(_ context.Context, id domain.SessionID, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, string(id)+": "+message)
	return nil
}

func (f *fakeSender) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

// A conflicting PR whose run still works is left to the run; once the run's
// agent exited, a resolve worker starts on a branch made at the PR's head,
// is linked as a merge execution of the entry and is told to push to the
// PR's source branch. The exited session is left as it is.
func TestAResolveWorkerTakesTheConflictOfAnExitedRun(t *testing.T) {
	rf := newResolveFixture(t)
	rf.tick(t, t0.Add(2*time.Minute))
	if got := rf.resolveSpawns(); len(got) != 0 {
		t.Fatalf("resolve workers while the run works = %d, want none", len(got))
	}

	rf.exitRun(t)
	rf.tick(t, t0.Add(3*time.Minute))
	spawns := rf.resolveSpawns()
	if len(spawns) != 1 {
		t.Fatalf("resolve workers = %d, want one", len(spawns))
	}
	cfg := spawns[0]
	branch := domain.LadoResolveBranch("task-1", storeEntry, 1)
	if cfg.ClientRequestID != "lado:lado-queue-1/_resolve/1" || cfg.Branch != branch || cfg.ProjectID != "proj-1" || cfg.Kind != domain.KindWorker ||
		cfg.ClientRequestHash != domain.LadoDispatchHash(rf.tasks.input.Revision.SHA256, rf.tasks.input.Snapshot.SHA256) {
		t.Fatalf("resolve spawn = %+v", cfg)
	}
	for _, want := range []string{"git push origin HEAD:" + prSource, "git merge --no-ff origin/main", rf.base, "ao report --done", "Never force-push"} {
		if !strings.Contains(cfg.Prompt, want) {
			t.Fatalf("the resolve prompt misses %q:\n%s", want, cfg.Prompt)
		}
	}
	if got := gitIn(t, rf.repo, nil, "rev-parse", "refs/heads/"+branch); got != rf.head {
		t.Fatalf("the resolve branch is at %s, want the PR's head %s", got, rf.head)
	}
	worker := rf.resolver(t, 1)
	exec := rf.execution(t, worker.ID)
	if exec.Role != domain.LadoRoleMerge || exec.PartID != "" || exec.QueueEntryID != storeEntry || exec.Branch != branch || exec.StartSHA != rf.head {
		t.Fatalf("resolve execution = %+v", exec)
	}
	r := rf.resolve(t)
	if r.State != domain.LadoResolveResolving || r.SessionID != worker.ID || r.SpawnRequest != "" || r.StartHeadSHA != rf.head || r.BaseTipSHA != rf.base || r.Attempts != 0 {
		t.Fatalf("resolve = %+v", r)
	}
	if got := rf.taskReason(t); got != domain.LadoValidatingResolving {
		t.Fatalf("task reason = %q, want %q", got, domain.LadoValidatingResolving)
	}
	if killed := rf.sessions.killed(); len(killed) != 0 {
		t.Fatalf("killed = %v, want the exited run left as it is", killed)
	}
	if rec, _, _ := rf.db.GetSession(context.Background(), rf.exec); rec.IsTerminated {
		t.Fatal("the exited run's session was ended")
	}

	// The next pass starts no other worker.
	rf.tick(t, t0.Add(4*time.Minute))
	if got := rf.resolveSpawns(); len(got) != 1 {
		t.Fatalf("resolve workers after another pass = %d, want one", len(got))
	}
}

// After done, a push that does not show yet is waited for without a
// failure; one seen late, after the grace and with the worker gone, still
// resolves the PR, and the count starts again.
func TestAResolvePushSeenLateAfterDoneCounts(t *testing.T) {
	rf := newResolveFixture(t)
	rf.exitRun(t)
	rf.tick(t, t0.Add(2*time.Minute))
	worker := rf.resolver(t, 1).ID
	pf := &partsFixture{storeFixture: rf.storeFixture}

	done := t0.Add(5 * time.Minute)
	rf.setClock(done)
	pf.reportDone(t, worker, "merged main")
	rf.tick(t, done)
	r := rf.resolve(t)
	if r.State != domain.LadoResolveResolving || r.Attempts != 0 || !r.DoneSeenAt.Equal(done) || r.Error != "" {
		t.Fatalf("resolve right after done = %+v, want it waiting for the push", r)
	}
	rf.tick(t, done.Add(3*time.Minute))
	if r := rf.resolve(t); r.State != domain.LadoResolveResolving || r.Attempts != 0 {
		t.Fatalf("resolve within the grace = %+v, want no failure", r)
	}

	// The worker ended and the push shows only after the grace.
	endStoreSession(t, rf.storeFixture, worker)
	rf.pushResolved(t)
	rf.tick(t, done.Add(domain.LadoResolveDoneGrace+5*time.Minute))
	r = rf.resolve(t)
	if r.State != domain.LadoResolveNone || r.Attempts != 0 || r.Error != "" || !r.DoneSeenAt.IsZero() {
		t.Fatalf("resolve after the late push = %+v, want it resolved", r)
	}
	if got := rf.resolveSpawns(); len(got) != 1 {
		t.Fatalf("resolve workers = %d, want one", len(got))
	}
	if got := rf.alerts(t); len(got) != 0 {
		t.Fatalf("alerts = %+v, want none", got)
	}
}

// A worker that reports done but whose push never shows fails once the
// grace is over; two failures in a row leave the PR to a person with one
// alert, and no third worker starts.
func TestTwoFailedResolvesLeaveThePRToAPerson(t *testing.T) {
	rf := newResolveFixture(t)
	rf.exitRun(t)
	pf := &partsFixture{storeFixture: rf.storeFixture}

	// The first worker reports done; its push never comes.
	at := t0.Add(2 * time.Minute)
	rf.tick(t, at)
	first := rf.resolver(t, 1).ID
	pf.reportDone(t, first, "merged main")
	rf.tick(t, at.Add(time.Minute))
	rf.tick(t, at.Add(time.Minute+domain.LadoResolveDoneGrace+time.Second))
	r := rf.resolve(t)
	if r.State != domain.LadoResolveNone || r.Attempts != 1 || !strings.Contains(r.Error, "did not take in the base") {
		t.Fatalf("resolve after the first failure = %+v", r)
	}
	if rec, _, _ := rf.db.GetSession(context.Background(), first); !rec.IsTerminated {
		t.Fatal("the failed worker still runs")
	}

	// The second ends without a report.
	at = at.Add(time.Minute + domain.LadoResolveDoneGrace + 2*time.Minute)
	rf.tick(t, at)
	second := rf.resolver(t, 2)
	if got := rf.resolveSpawns(); len(got) != 2 || got[1].ClientRequestID != "lado:lado-queue-1/_resolve/2" {
		t.Fatalf("resolve workers = %+v, want a second", got)
	}
	endStoreSession(t, rf.storeFixture, second.ID)
	rf.tick(t, at.Add(time.Minute))
	r = rf.resolve(t)
	if r.State != domain.LadoResolveNeedsHuman || r.Attempts != 2 || !strings.Contains(r.Error, "without reporting done") {
		t.Fatalf("resolve after the second failure = %+v", r)
	}
	if got := rf.taskReason(t); got != domain.LadoValidatingConflictUnresolved {
		t.Fatalf("task reason = %q, want %q", got, domain.LadoValidatingConflictUnresolved)
	}
	alerts := rf.alerts(t)
	if len(alerts) != 1 || alerts[0].SessionID != rf.exec || alerts[0].Value != 2 {
		t.Fatalf("alerts = %+v, want one for the run", alerts)
	}

	for i := 2; i <= 4; i++ {
		rf.tick(t, at.Add(time.Duration(i)*time.Minute))
	}
	if got := rf.resolveSpawns(); len(got) != 2 {
		t.Fatalf("resolve workers = %d after a person took over, want two", len(got))
	}
	if got := rf.alerts(t); len(got) != 1 {
		t.Fatalf("alerts = %d, want still one", len(got))
	}
}

// A worker that asks for a decision leaves the PR to a person at once; it
// keeps running for them.
func TestAResolveWorkerAskingForADecisionNeedsAPerson(t *testing.T) {
	rf := newResolveFixture(t)
	rf.exitRun(t)
	rf.tick(t, t0.Add(2*time.Minute))
	worker := rf.resolver(t, 1).ID
	at := t0.Add(3 * time.Minute)
	if _, err := rf.db.CreateReport(context.Background(), domain.ReportRecord{
		ID: "report-ask", SessionID: worker, ProjectID: "proj-1", State: domain.ReportNeedsInput, Note: "keep which side?",
		CreatedAt: at, AvailableAt: at, RepeatCount: 1, DeliveryState: domain.ReportPending,
	}); err != nil {
		t.Fatalf("report needs input: %v", err)
	}
	rf.tick(t, at)
	r := rf.resolve(t)
	if r.State != domain.LadoResolveNeedsHuman || r.Attempts != 0 || !strings.Contains(r.Error, "keep which side?") {
		t.Fatalf("resolve = %+v, want it left to a person", r)
	}
	if killed := rf.sessions.killed(); len(killed) != 0 {
		t.Fatalf("killed = %v, want the worker left for the person", killed)
	}
	if got := rf.alerts(t); len(got) != 1 {
		t.Fatalf("alerts = %d, want one", len(got))
	}
}

// A person resolves the PR a worker asked about: once it no longer
// conflicts, the resolve clears and the worker that waited for the person
// stops. When the PR conflicts again, a new resolve worker takes it.
func TestAClearedResolveStopsItsWorkerAndANewConflictGetsANewOne(t *testing.T) {
	rf := newResolveFixture(t)
	rf.exitRun(t)
	rf.tick(t, t0.Add(2*time.Minute))
	worker := rf.resolver(t, 1).ID
	at := t0.Add(3 * time.Minute)
	if _, err := rf.db.CreateReport(context.Background(), domain.ReportRecord{
		ID: "report-ask", SessionID: worker, ProjectID: "proj-1", State: domain.ReportNeedsInput, Note: "keep which side?",
		CreatedAt: at, AvailableAt: at, RepeatCount: 1, DeliveryState: domain.ReportPending,
	}); err != nil {
		t.Fatalf("report needs input: %v", err)
	}
	rf.tick(t, at)
	if r := rf.resolve(t); r.State != domain.LadoResolveNeedsHuman {
		t.Fatalf("resolve = %+v, want it left to a person", r)
	}

	// The person merged the base into the PR's branch.
	resolved := rf.pushResolved(t)
	rf.writePR(t, resolved, domain.PRStateOpen, domain.MergeMergeable, t0.Add(4*time.Minute))
	rf.tick(t, t0.Add(4*time.Minute))
	if r := rf.resolve(t); r.State != domain.LadoResolveNone || r.Attempts != 0 {
		t.Fatalf("resolve = %+v, want it cleared", r)
	}
	if killed := rf.sessions.killed(); len(killed) != 1 || killed[0] != worker {
		t.Fatalf("killed = %v, want the worker that waited for the person", killed)
	}

	// The base moves again and the PR conflicts: a new worker starts.
	rf.base = commitOn(t, rf.remote, "main", map[string]string{"shared.txt": "the base's next change\n"})
	rf.writePR(t, resolved, domain.PRStateOpen, domain.MergeConflicting, t0.Add(10*time.Minute))
	rf.tick(t, t0.Add(10*time.Minute))
	if got := rf.resolveSpawns(); len(got) != 2 {
		t.Fatalf("resolve spawns = %d, want a second worker for the new conflict", len(got))
	}
	if r := rf.resolve(t); r.State != domain.LadoResolveResolving || r.SessionID == worker {
		t.Fatalf("resolve = %+v, want a new worker resolving", r)
	}
}

// A new head whose record turned base_overlap without a conflict is not told
// by the dispatcher: the verification loop's fix request, with the files,
// goes to the worker (ladoverify readOwner), so it does not hear twice.
func TestAResolveWorkerIsNotToldTwiceOfABaseOverlap(t *testing.T) {
	rf := newResolveFixture(t)
	rf.exitRun(t)
	rf.tick(t, t0.Add(2*time.Minute))
	rf.resolver(t, 1)

	moved := strings.Repeat("c", 40)
	rf.writePR(t, moved, domain.PRStateOpen, domain.MergeMergeable, t0.Add(4*time.Minute))
	at := t0.Add(4 * time.Minute)
	if _, err := rf.db.UpsertLadoVerification(context.Background(), domain.LadoVerification{
		ExecutionSessionID: rf.exec, HeadSHA: moved, PRURL: "https://github.com/acme/app/pull/7", ComputedAt: at,
		Items: []domain.LadoVerifyItem{{
			Kind: domain.LadoVerifyBaseBranch, Outcome: domain.LadoVerifyFail, Code: domain.LadoCodeBaseOverlap,
			Details: []byte(`{"branch":"main","remote":"origin","advanced":true,"overlap":["shared.txt"],"overlapCount":1}`),
		}},
	}); err != nil {
		t.Fatalf("record base_overlap: %v", err)
	}
	rf.tick(t, at)
	rf.tick(t, t0.Add(5*time.Minute))
	if got := rf.sender.messages(); len(got) != 0 {
		t.Fatalf("messages = %v, want none from the dispatcher for a base_overlap head", got)
	}
}

// A running worker hears of each new conflicting head once; a PR that
// merged stops it and clears its resolve.
func TestAResolveWorkerHearsOfNewConflictsAndStopsWithTheMerge(t *testing.T) {
	rf := newResolveFixture(t)
	rf.exitRun(t)
	rf.tick(t, t0.Add(2*time.Minute))
	worker := rf.resolver(t, 1).ID

	rf.tick(t, t0.Add(3*time.Minute))
	if got := rf.sender.messages(); len(got) != 0 {
		t.Fatalf("messages at the head the worker started at = %v, want none", got)
	}
	moved := strings.Repeat("c", 40)
	rf.writePR(t, moved, domain.PRStateOpen, domain.MergeConflicting, t0.Add(4*time.Minute))
	rf.tick(t, t0.Add(4*time.Minute))
	rf.tick(t, t0.Add(5*time.Minute))
	got := rf.sender.messages()
	if len(got) != 1 || !strings.HasPrefix(got[0], string(worker)+": ") || !strings.Contains(got[0], prSource) {
		t.Fatalf("messages = %v, want one to the worker", got)
	}
	if r := rf.resolve(t); r.NotifiedHeadSHA != moved {
		t.Fatalf("resolve = %+v, want the head it told", r)
	}

	rf.writePR(t, moved, domain.PRStateMerged, domain.MergeMergeable, t0.Add(6*time.Minute))
	rf.tick(t, t0.Add(6*time.Minute))
	if killed := rf.sessions.killed(); len(killed) != 1 || killed[0] != worker {
		t.Fatalf("killed = %v, want the resolve worker", killed)
	}
	if r := rf.resolve(t); r.State != domain.LadoResolveNone || r.Attempts != 0 || r.SpawnRequest != "" {
		t.Fatalf("resolve after the merge = %+v, want it cleared", r)
	}
}

// crashBeforeResolveLink stands for a daemon that stops after AO started a
// resolve worker and before the dispatcher linked it: its first link fails,
// and the worker cannot be stopped then.
type crashBeforeResolveLink struct {
	*store.Store
	sessions *fakeSessions
	crashed  atomic.Bool
}

func (c *crashBeforeResolveLink) LinkLadoResolveExecution(ctx context.Context, request string, exec domain.LadoExecution) error {
	if c.crashed.CompareAndSwap(false, true) {
		c.sessions.setKillErr(exec.SessionID, errors.New("the daemon stopped"))
		return errors.New("the daemon stopped")
	}
	return c.Store.LinkLadoResolveExecution(ctx, request, exec)
}

// A start stopped between the spawn and the link keeps its grant; once the
// grant expired, the next daemon links the worker AO started, without a
// second spawn.
func TestAResolveStartStoppedBeforeItsLinkIsLinkedLater(t *testing.T) {
	rf := newResolveFixture(t)
	rf.exitRun(t)
	crashing := &crashBeforeResolveLink{Store: rf.db, sessions: rf.sessions}
	rf.svc = rf.dispatcher(crashing, rf.git)
	at := t0.Add(2 * time.Minute)
	rf.setClock(at)
	if err := rf.svc.Tick(context.Background(), at); err == nil {
		t.Fatal("the stopped start reported no problem")
	}
	worker := rf.resolver(t, 1)
	r := rf.resolve(t)
	if r.SpawnRequest != "lado:lado-queue-1/_resolve/1" || r.State == domain.LadoResolveResolving {
		t.Fatalf("resolve after the stop = %+v, want the request kept", r)
	}
	if _, ok, _ := rf.db.LadoExecution(context.Background(), worker.ID); ok {
		t.Fatal("the stopped start linked the worker")
	}

	// The daemon starts again; the grant expires first.
	rf.sessions.setKillErr(worker.ID, nil)
	rf.svc = rf.dispatcher(rf.db, rf.git)
	rf.tick(t, at.Add(time.Minute))
	if r := rf.resolve(t); r.SpawnRequest == "" {
		t.Fatalf("resolve before the grant expired = %+v, want it held", r)
	}
	rf.tick(t, at.Add(defaultLease+time.Minute))
	r = rf.resolve(t)
	if r.SpawnRequest != "" || r.State != domain.LadoResolveResolving || r.SessionID != worker.ID {
		t.Fatalf("resolve after recovery = %+v, want the worker linked", r)
	}
	if exec := rf.execution(t, worker.ID); exec.Role != domain.LadoRoleMerge || exec.Branch != domain.LadoResolveBranch("task-1", storeEntry, 1) {
		t.Fatalf("recovered execution = %+v", exec)
	}
	if got := rf.resolveSpawns(); len(got) != 1 {
		t.Fatalf("resolve workers = %d, want the one AO started", len(got))
	}
	if killed := rf.sessions.killed(); len(killed) != 1 {
		t.Fatalf("killed = %v, want only the stopped pass's try", killed)
	}
}

// A start whose session AO never finished is stopped and its grant cleared;
// the attempt counts as failed.
func TestAResolveWorkerThatDidNotStartIsCleared(t *testing.T) {
	rf := newResolveFixture(t)
	rf.exitRun(t)
	rf.sessions.errs = []error{nil, errors.New("the harness is missing")}
	rf.tick(t, t0.Add(2*time.Minute))
	if got := rf.resolveSpawns(); len(got) != 1 {
		t.Fatalf("resolve workers = %d, want one try", len(got))
	}
	r := rf.resolve(t)
	if r.SpawnRequest != "" || r.State != domain.LadoResolveNone || r.Attempts != 1 || !strings.Contains(r.Error, "could not start") {
		t.Fatalf("resolve = %+v, want a failed attempt", r)
	}
}
