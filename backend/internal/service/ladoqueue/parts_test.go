package ladoqueue

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// partsPlanText is a plan of task-1 with three parts: a and b at once, c
// after a, and one integration step.
const partsPlanText = `---
task: task-1
issue: "github:acme/app#12"
base_ref: main
steps:
  - Add the service
  - Add the page
  - Document the service
  - Check everything together
acceptance_criteria:
  - works
affected_paths:
  - "svc/**"
  - "web/**"
  - "docs/**"
  - "shared.txt"
test_commands: []
risk: low
permission_ceiling: auto
review:
  required: false
plan_source: issue
parts:
  - id: a
    title: Service
    steps: [1]
    affected_paths: ["svc/**"]
  - id: b
    title: Page
    steps: [2]
    affected_paths: ["web/**"]
  - id: c
    title: Docs
    steps: [3]
    affected_paths: ["docs/**"]
    depends_on: [a]
---

# Plan
`

// gitIn runs git in dir with a fixed identity and no signing, and returns
// its output.
func gitIn(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newPartsRepo makes a repository with a main branch and a feature branch
// and returns it with main's commit.
func newPartsRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, nil, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("start\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, nil, "add", "shared.txt")
	gitIn(t, dir, nil, "commit", "-q", "-m", "init")
	gitIn(t, dir, nil, "branch", "feature")
	return dir, gitIn(t, dir, nil, "rev-parse", "main")
}

// commitOn commits files on branch without a checkout, on top of its tip or
// of parents when given (a merge), and returns the commit.
func commitOn(t *testing.T, dir, branch string, files map[string]string, parents ...string) string {
	t.Helper()
	if len(parents) == 0 {
		parents = []string{gitIn(t, dir, nil, "rev-parse", "refs/heads/"+branch)}
	}
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "index")}
	gitIn(t, dir, index, "read-tree", parents[0])
	for path, content := range files {
		f := filepath.Join(t.TempDir(), "blob")
		if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		blob := gitIn(t, dir, nil, "hash-object", "-w", f)
		gitIn(t, dir, index, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path)
	}
	tree := gitIn(t, dir, index, "write-tree")
	args := []string{"commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	commit := gitIn(t, dir, nil, append(args, "-m", "feat: 작업을 한다")...)
	gitIn(t, dir, nil, "update-ref", "refs/heads/"+branch, commit)
	return commit
}

func fileAt(t *testing.T, dir, rev, path string) string {
	t.Helper()
	return gitIn(t, dir, nil, "show", rev+":"+path)
}

// partsFixture is the store fixture whose task-1 runs partsPlanText in a real
// repository.
type partsFixture struct {
	*storeFixture
	repo string
	main string
}

func newPartsFixture(t *testing.T) *partsFixture {
	t.Helper()
	repo, main := newPartsRepo(t)
	sf := newStoreFixture(t, nil)
	sf.tasks.input.PlanText = partsPlanText
	sf.projects.project.Path = repo
	sf.git.sha = main
	// A stopped session ends in AO's rows.
	sf.sessions.ended = func(id domain.SessionID) { endStoreSession(t, sf, id) }
	return &partsFixture{storeFixture: sf, repo: repo, main: main}
}

func endStoreSession(t *testing.T, sf *storeFixture, id domain.SessionID) {
	t.Helper()
	ctx := context.Background()
	rec, ok, err := sf.db.GetSession(ctx, id)
	if err != nil || !ok {
		t.Fatalf("read session %s: %v, %v", id, ok, err)
	}
	rec.IsTerminated = true
	if err := sf.db.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("end session %s: %v", id, err)
	}
}

// reportDone is the worker of session reporting done with note.
func (pf *partsFixture) reportDone(t *testing.T, id domain.SessionID, note string) {
	t.Helper()
	at := pf.clock()
	if _, err := pf.db.CreateReport(context.Background(), domain.ReportRecord{
		ID: "report-" + string(id) + "-" + at.Format("150405.000"), SessionID: id, ProjectID: "proj-1", State: domain.ReportDone, Note: note,
		CreatedAt: at, AvailableAt: at, SettlementDeadline: at.Add(5 * time.Minute), RepeatCount: 1, DeliveryState: domain.ReportPending,
	}); err != nil {
		t.Fatalf("report done for %s: %v", id, err)
	}
}

func (pf *partsFixture) parts(t *testing.T, entry domain.LadoQueueEntryID) map[string]domain.LadoTaskPart {
	t.Helper()
	parts, err := pf.db.LadoTaskParts(context.Background(), entry)
	if err != nil {
		t.Fatalf("read parts: %v", err)
	}
	byID := map[string]domain.LadoTaskPart{}
	for _, p := range parts {
		byID[p.PartID] = p
	}
	return byID
}

func (pf *partsFixture) branch(t *testing.T, name string) string {
	t.Helper()
	sha, err := execPartGit{}.BranchSHA(context.Background(), pf.repo, name)
	if err != nil {
		t.Fatalf("read branch %s: %v", name, err)
	}
	return sha
}

// requireUntouched checks that LADO left main and the feature branch alone.
func (pf *partsFixture) requireUntouched(t *testing.T) {
	t.Helper()
	if got := gitIn(t, pf.repo, nil, "rev-parse", "refs/heads/main"); got != pf.main {
		t.Fatalf("main moved to %s", got)
	}
	if got := gitIn(t, pf.repo, nil, "rev-parse", "refs/heads/feature"); got != pf.main {
		t.Fatalf("feature moved to %s", got)
	}
	if head := gitIn(t, pf.repo, nil, "symbolic-ref", "HEAD"); head != "refs/heads/main" {
		t.Fatalf("the repository's HEAD is %s, want main still checked out", head)
	}
}

func spawnIDs(cfgs []ports.SpawnConfig) []string {
	ids := make([]string, len(cfgs))
	for i, cfg := range cfgs {
		ids[i] = cfg.ClientRequestID
	}
	return ids
}

// The whole run: a and b start at once on their own branches from the
// approved base, c waits for a; a merges as it reports done and c starts
// from the merge; b's merge conflicts, so a merge worker resolves it and the
// integration branch fast-forwards to it; once every part is merged, the
// integration worker starts on the integration branch and becomes the task's
// pull request worker. Every worker is a new session with its own request.
func TestAPartsRunMergesItsPartsAndStartsTheIntegrationWorker(t *testing.T) {
	pf := newPartsFixture(t)
	ctx := context.Background()
	integration := domain.LadoIntegrationBranch("task-1", storeEntry)

	pf.tick(t, t0)
	if got := pf.branch(t, integration); got != pf.main {
		t.Fatalf("integration branch = %q, want it made at the approved base %s", got, pf.main)
	}
	spawned := pf.sessions.spawned()
	if want := []string{"lado:lado-queue-1/a/1", "lado:lado-queue-1/b/1"}; !slices.Equal(spawnIDs(spawned), want) {
		t.Fatalf("spawned %v, want a and b at once", spawnIDs(spawned))
	}
	for i, part := range []string{"a", "b"} {
		cfg := spawned[i]
		if cfg.Branch != "lado/task-1/1/"+part || cfg.ClientRequestHash != testDispatchHash() || cfg.RequestedMode != domain.SessionModeChat {
			t.Fatalf("spawn of %s = %+v", part, cfg)
		}
		if got := pf.branch(t, cfg.Branch); got != pf.main {
			t.Fatalf("part branch %s = %s, want the integration tip", cfg.Branch, got)
		}
		if !strings.Contains(cfg.Prompt, "You are part `"+part+"`") || !strings.Contains(cfg.Prompt, "Do not push it") ||
			!strings.Contains(cfg.Prompt, "ao report --done") || !strings.Contains(cfg.Prompt, partsPlanText[:40]) {
			t.Fatalf("prompt of %s:\n%s", part, cfg.Prompt)
		}
	}
	parts := pf.parts(t, storeEntry)
	if parts["c"].State != domain.LadoPartWaiting || parts["c"].WaitingReason != domain.LadoWaitingPartDependency || parts["c"].WaitingDetail != "a" {
		t.Fatalf("part c = %+v, want it waiting for a", parts["c"])
	}
	task, _, _ := pf.db.LadoTask(ctx, "task-1")
	if state := task.StateAt(t0); state != domain.LadoTaskRunning || task.Parts != (domain.LadoTaskPartsSummary{Total: 3}) {
		t.Fatalf("task = %s with parts %+v, want running 0/3", state, task.Parts)
	}

	// a reports done: LADO stops its worker and merges it without a checkout.
	aTip := commitOn(t, pf.repo, "lado/task-1/1/a", map[string]string{"svc/a.go": "package svc\n", "shared.txt": "start\na\n"})
	pf.reportDone(t, parts["a"].SessionID, "added the service")
	pf.tick(t, t0.Add(time.Minute))
	parts = pf.parts(t, storeEntry)
	head := pf.branch(t, integration)
	if parts["a"].State != domain.LadoPartMerged || parts["a"].MergedSHA != head || parts["a"].Summary != "added the service" {
		t.Fatalf("part a = %+v, integration %s", parts["a"], head)
	}
	if parents := strings.Fields(gitIn(t, pf.repo, nil, "rev-list", "--parents", "-n", "1", head)); !slices.Equal(parents[1:], []string{pf.main, aTip}) {
		t.Fatalf("merge commit parents = %v, want main then a's tip (--no-ff)", parents[1:])
	}
	if subject := gitIn(t, pf.repo, nil, "log", "-1", "--format=%s", head); !strings.HasPrefix(subject, "chore(lado): 파트 a") {
		t.Fatalf("merge subject = %q, want a Korean conventional subject", subject)
	}
	if !slices.Contains(pf.sessions.killed(), parts["a"].SessionID) {
		t.Fatalf("a's worker was not stopped after done: kills %v", pf.sessions.killed())
	}
	if parts["c"].State != domain.LadoPartRunning || parts["c"].StartSHA != head || pf.branch(t, "lado/task-1/1/c") != head {
		t.Fatalf("part c = %+v, want it started from the merge %s", parts["c"], head)
	}
	pf.requireUntouched(t)

	// b changes the same line: its merge conflicts and goes to a merge worker.
	bTip := commitOn(t, pf.repo, "lado/task-1/1/b", map[string]string{"web/b.tsx": "export {}\n", "shared.txt": "start\nb\n"})
	pf.reportDone(t, parts["b"].SessionID, "added the page")
	pf.tick(t, t0.Add(2*time.Minute))
	parts = pf.parts(t, storeEntry)
	if parts["b"].State != domain.LadoPartConflict || parts["b"].MergeSessionID == "" || parts["b"].MergeBranch != "lado/task-1/1/merge-b" {
		t.Fatalf("part b = %+v, want a merge worker on its conflict", parts["b"])
	}
	if got := pf.branch(t, integration); got != head {
		t.Fatalf("a conflicting merge moved the integration branch to %s", got)
	}
	merge := pf.sessions.spawned()[len(pf.sessions.spawned())-1]
	if merge.ClientRequestID != "lado:lado-queue-1/merge-b/1" || merge.Branch != "lado/task-1/1/merge-b" || !strings.Contains(merge.Prompt, bTip) {
		t.Fatalf("merge worker spawn = %+v", merge)
	}
	resolved := commitOn(t, pf.repo, "lado/task-1/1/merge-b", map[string]string{"web/b.tsx": "export {}\n", "shared.txt": "start\na\nb\n"}, head, bTip)
	pf.reportDone(t, parts["b"].MergeSessionID, "kept both lines")
	pf.tick(t, t0.Add(3*time.Minute))
	parts = pf.parts(t, storeEntry)
	if parts["b"].State != domain.LadoPartMerged || pf.branch(t, integration) != resolved {
		t.Fatalf("part b = %+v, integration %s; want a fast-forward to %s", parts["b"], pf.branch(t, integration), resolved)
	}

	// c finishes without a commit: it merges as it is.
	pf.reportDone(t, parts["c"].SessionID, "nothing to change")
	pf.tick(t, t0.Add(4*time.Minute))
	parts = pf.parts(t, storeEntry)
	if parts["c"].State != domain.LadoPartMerged || parts["c"].MergedSHA != resolved {
		t.Fatalf("part c = %+v, want it merged empty", parts["c"])
	}
	last := pf.sessions.spawned()[len(pf.sessions.spawned())-1]
	if last.ClientRequestID != "lado:lado-queue-1/integration/1" || last.Branch != integration {
		t.Fatalf("last spawn = %+v, want the integration worker on %s", last, integration)
	}
	for _, want := range []string{"Review the parts against each other first", "the same thing implemented twice", "--needs-input",
		"added the service", "kept both lines", "steps 4 of the plan", "Closes #12", "lado/evidence.md"} {
		if !strings.Contains(last.Prompt, want) {
			t.Fatalf("integration prompt lacks %q:\n%s", want, last.Prompt)
		}
	}
	if fileAt(t, pf.repo, integration, "shared.txt") != "start\na\nb" {
		t.Fatalf("shared.txt on the integration branch = %q", fileAt(t, pf.repo, integration, "shared.txt"))
	}
	task, _, _ = pf.db.LadoTask(ctx, "task-1")
	if task.Execution.Role != domain.LadoRoleIntegration || task.Queue.DispatchedSessionID != task.Execution.SessionID ||
		task.Execution.DispatchBaseSHA != pf.main || task.Parts != (domain.LadoTaskPartsSummary{Total: 3, Merged: 3}) {
		t.Fatalf("task execution %+v, entry %+v, parts %+v; want the integration worker as its pull request worker", task.Execution, task.Queue, task.Parts)
	}
	if state := task.StateAt(t0.Add(4 * time.Minute)); state != domain.LadoTaskRunning {
		t.Fatalf("task = %s, want running until the integration worker opens its PR", state)
	}
	// Every worker was a new session of its own request.
	cfgs := pf.sessions.spawned()
	ids := spawnIDs(cfgs)
	if len(ids) != 5 || len(slices.Compact(slices.Sorted(slices.Values(ids)))) != 5 {
		t.Fatalf("spawn requests %v, want five distinct", ids)
	}
	pf.requireUntouched(t)
}

// A part worker that ends without done is started again in a new session on a
// new branch; when that one ends too, the task fails and names the part.
func TestAPartThatEndsWithoutDoneIsRetriedThenFailsTheTask(t *testing.T) {
	pf := newPartsFixture(t)
	ctx := context.Background()
	pf.tick(t, t0)
	first := pf.parts(t, storeEntry)["a"]
	endStoreSession(t, pf.storeFixture, first.SessionID)
	pf.tick(t, t0.Add(time.Minute))
	second := pf.parts(t, storeEntry)["a"]
	if second.State != domain.LadoPartRunning || second.Attempts != 2 || second.SessionID == first.SessionID || second.Branch != "lado/task-1/1/a.2" {
		t.Fatalf("part a after its worker ended = %+v, want a new worker on a new branch", second)
	}
	if last := pf.sessions.spawned()[len(pf.sessions.spawned())-1]; last.ClientRequestID != "lado:lado-queue-1/a/2" {
		t.Fatalf("retry request = %s", last.ClientRequestID)
	}
	endStoreSession(t, pf.storeFixture, second.SessionID)
	pf.tick(t, t0.Add(2*time.Minute))
	if a := pf.parts(t, storeEntry)["a"]; a.State != domain.LadoPartFailed {
		t.Fatalf("part a = %+v, want failed", a)
	}
	task, _, _ := pf.db.LadoTask(ctx, "task-1")
	if state, reason := task.StateAt(t0.Add(2*time.Minute)), task.StateReason(); state != domain.LadoTaskFailed || reason != domain.LadoFailedAttemptsExhausted ||
		!strings.Contains(task.Queue.ErrorMessage, "part a") {
		t.Fatalf("task = %s (%s), error %q; want failed with part a named", state, reason, task.Queue.ErrorMessage)
	}
	spawns := len(pf.sessions.spawned())
	pf.tick(t, t0.Add(3*time.Minute))
	if len(pf.sessions.spawned()) != spawns {
		t.Fatal("a failed run started another worker")
	}
}

// Cancelling the task stops every live part worker and ends the run.
func TestCancellingATaskStopsItsPartWorkers(t *testing.T) {
	pf := newPartsFixture(t)
	ctx := context.Background()
	pf.tick(t, t0)
	parts := pf.parts(t, storeEntry)
	if _, _, err := pf.db.CancelLadoTask(ctx, "task-1", t0.Add(30*time.Second)); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	pf.tick(t, t0.Add(time.Minute))
	killed := pf.sessions.killed()
	for _, part := range []string{"a", "b"} {
		if !slices.Contains(killed, parts[part].SessionID) {
			t.Fatalf("part %s's worker %s was not stopped: kills %v", part, parts[part].SessionID, killed)
		}
	}
	task, _, err := pf.db.LadoTask(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Queue.CancelledAt.IsZero() || task.StateAt(t0.Add(time.Minute)) != domain.LadoTaskCancelled {
		t.Fatalf("entry = %+v, want the parts run ended with the task", task.Queue)
	}
}

// The worker cap counts LADO workers: with one slot, b waits for it.
func TestPartsWaitForAWorkerSlot(t *testing.T) {
	pf := newPartsFixture(t)
	settings := domain.DefaultLadoDispatchSettings()
	settings.MaxWorkers = 1
	if _, err := pf.db.SetLadoDispatchSettings(context.Background(), settings, domain.LadoAuditEvent{At: t0, Actor: "test", Action: "dispatch.settings_changed", SubjectKind: "dispatch_settings", SubjectID: "global"}); err != nil {
		t.Fatal(err)
	}
	pf.tick(t, t0)
	parts := pf.parts(t, storeEntry)
	if len(pf.sessions.spawned()) != 1 || parts["a"].State != domain.LadoPartRunning ||
		parts["b"].State != domain.LadoPartWaiting || parts["b"].WaitingReason != domain.LadoWaitingSlotWorkers {
		t.Fatalf("spawned %v, parts %+v; want a running and b waiting for a worker slot", spawnIDs(pf.sessions.spawned()), parts)
	}
}

// A daemon that stops after moving the integration branch and before
// recording the merge finds the merge made when it starts again: it records
// it and does not merge twice.
func TestAMergeIsNotAppliedTwiceAfterARestart(t *testing.T) {
	pf := newPartsFixture(t)
	integration := domain.LadoIntegrationBranch("task-1", storeEntry)
	pf.tick(t, t0)
	parts := pf.parts(t, storeEntry)
	commitOn(t, pf.repo, "lado/task-1/1/a", map[string]string{"svc/a.go": "package svc\n"})
	pf.reportDone(t, parts["a"].SessionID, "done")
	crashing := &crashAfterMove{PartGit: execPartGit{}}
	pf.svc = New(Deps{
		Store: pf.db, Tasks: pf.tasks, Policy: pf.policy, Sessions: pf.sessions, Projects: pf.projects, Branches: pf.branches,
		Memory: pf.memory, Git: pf.git, PartGit: crashing, Clock: pf.clock, NewID: pf.newID,
		Log: slog.New(slog.NewTextHandler(pf.logs, nil)),
	})
	pf.setClock(t0.Add(time.Minute))
	if err := pf.svc.Tick(context.Background(), t0.Add(time.Minute)); err == nil {
		t.Fatal("the pass that stopped after the move reported no error")
	}
	moved := pf.branch(t, integration)
	if a := pf.parts(t, storeEntry)["a"]; a.State != domain.LadoPartMerging || moved == pf.main {
		t.Fatalf("part a = %+v, integration %s; want the merge made and not recorded", a, moved)
	}
	pf.svc = pf.dispatcher(pf.db, pf.git)
	pf.tick(t, t0.Add(2*time.Minute))
	a := pf.parts(t, storeEntry)["a"]
	if a.State != domain.LadoPartMerged || a.MergedSHA != moved || pf.branch(t, integration) != moved {
		t.Fatalf("part a = %+v, integration %s; want the earlier merge %s recorded", a, pf.branch(t, integration), moved)
	}
	run, _, _ := pf.db.LadoPartRun(context.Background(), storeEntry)
	if run.HeadSHA != moved {
		t.Fatalf("run head = %s, want %s", run.HeadSHA, moved)
	}
}

// crashAfterMove moves the branch and then fails once, as a daemon that
// stops right after the move.
type crashAfterMove struct {
	PartGit
	crashed atomic.Bool
}

// BranchSHA fails once the daemon stopped: nothing after the move runs.
func (c *crashAfterMove) BranchSHA(ctx context.Context, dir, name string) (string, error) {
	if c.crashed.Load() {
		return "", errors.New("the daemon stopped")
	}
	return c.PartGit.BranchSHA(ctx, dir, name)
}

func (c *crashAfterMove) MoveBranch(ctx context.Context, dir, name, newSHA, oldSHA string) error {
	if err := c.PartGit.MoveBranch(ctx, dir, name, newSHA, oldSHA); err != nil {
		return err
	}
	if c.crashed.CompareAndSwap(false, true) {
		return errors.New("the daemon stopped")
	}
	return nil
}

// task2Plan is task-2's plan: two parts on other paths than task-1's.
const task2Plan = `---
task: task-7
issue: "github:acme/app#7"
base_ref: main
steps: [one, two]
acceptance_criteria: [works]
affected_paths: ["api/**", "cli/**"]
test_commands: []
risk: low
permission_ceiling: auto
review: {required: false}
plan_source: issue
parts:
  - {id: e, title: API, steps: [1], affected_paths: ["api/**"]}
  - {id: f, title: CLI, steps: [2], affected_paths: ["cli/**"]}
---
`

// fourParts is task-1 split into A, B and C at once and D after all three.
const fourParts = `parts:
  - {id: a, title: A, steps: [1], affected_paths: ["svc/**"]}
  - {id: b, title: B, steps: [2], affected_paths: ["web/**"]}
  - {id: c, title: C, steps: [3], affected_paths: ["docs/**"]}
  - {id: d, title: D, steps: [4], affected_paths: ["shared.txt"], depends_on: [a, b, c]}
`

// Two tasks run at once: task-1's A, B and C and task-2's E and F start
// together within a task cap of 2 and a worker cap of 5, and D starts once
// A, B and C are merged.
func TestTwoTasksRunTheirPartsAtOnce(t *testing.T) {
	pf := newPartsFixture(t)
	ctx := context.Background()
	pf.tasks.input.PlanText = strings.Replace(partsPlanText, partsPlanText[strings.Index(partsPlanText, "parts:"):strings.Index(partsPlanText, "---\n\n# Plan")], fourParts, 1)
	settings := domain.DefaultLadoDispatchSettings()
	settings.MaxConcurrent, settings.MaxWorkers = 2, 5
	if _, err := pf.db.SetLadoDispatchSettings(ctx, settings, domain.LadoAuditEvent{At: t0, Actor: "test", Action: "dispatch.settings_changed", SubjectKind: "dispatch_settings", SubjectID: "global"}); err != nil {
		t.Fatal(err)
	}
	if err := pf.db.SetLadoProjectMaxConcurrent(ctx, "proj-1", 2, domain.LadoAuditEvent{At: t0, Actor: "test", Action: "project.cap_changed", SubjectKind: "project", SubjectID: "proj-1"}); err != nil {
		t.Fatal(err)
	}
	entry2 := pf.queueTask(t, "7", "proj-1")
	in := pf.tasks.inputs["task-7"]
	in.PlanText = task2Plan
	pf.tasks.inputs["task-7"] = in

	pf.tick(t, t0)
	want := []string{"lado:lado-queue-1/a/1", "lado:lado-queue-1/b/1", "lado:lado-queue-1/c/1", "lado:lado-queue-7/e/1", "lado:lado-queue-7/f/1"}
	if got := spawnIDs(pf.sessions.spawned()); !slices.Equal(slices.Sorted(slices.Values(got)), want) {
		t.Fatalf("spawned %v, want the five parts of both tasks at once", got)
	}
	if d := pf.parts(t, storeEntry)["d"]; d.State != domain.LadoPartWaiting || d.WaitingReason != domain.LadoWaitingPartDependency {
		t.Fatalf("part d = %+v, want it waiting for a, b and c", d)
	}
	load, err := pf.db.LadoRunLoad(ctx, t0)
	if err != nil || load.TaskCount() != 2 || load.Workers != 5 {
		t.Fatalf("load = %d tasks and %d workers (%v), want 2 tasks and 5 workers", load.TaskCount(), load.Workers, err)
	}
	if e := pf.parts(t, entry2)["e"]; e.Branch != "lado/task-7/7/e" {
		t.Fatalf("part e = %+v", e)
	}
	parts := pf.parts(t, storeEntry)
	for i, part := range []string{"a", "b", "c"} {
		dir := []string{"svc", "web", "docs"}[i]
		commitOn(t, pf.repo, "lado/task-1/1/"+part, map[string]string{dir + "/x.txt": part})
		pf.reportDone(t, parts[part].SessionID, part+" done")
	}
	pf.tick(t, t0.Add(time.Minute))
	if d := pf.parts(t, storeEntry)["d"]; d.State != domain.LadoPartRunning || d.StartSHA != pf.branch(t, domain.LadoIntegrationBranch("task-1", storeEntry)) {
		t.Fatalf("part d = %+v, want it started once a, b and c merged", d)
	}
}

// The task cap counts tasks: a task whose three parts run takes one slot,
// and another task waits for a task slot, not a worker slot.
func TestTheTaskCapCountsTasksNotWorkers(t *testing.T) {
	pf := newPartsFixture(t)
	ctx := context.Background()
	settings := domain.DefaultLadoDispatchSettings()
	settings.MaxConcurrent = 1
	if _, err := pf.db.SetLadoDispatchSettings(ctx, settings, domain.LadoAuditEvent{At: t0, Actor: "test", Action: "dispatch.settings_changed", SubjectKind: "dispatch_settings", SubjectID: "global"}); err != nil {
		t.Fatal(err)
	}
	entry2 := pf.queueTask(t, "7", "proj-1")
	pf.tick(t, t0)
	if got := len(pf.sessions.spawned()); got != 2 {
		t.Fatalf("spawned %d, want task-1's a and b", got)
	}
	task2, _, _ := pf.db.LadoTask(ctx, "task-7")
	if task2.Queue.ID != entry2 || task2.Queue.WaitingReason != domain.LadoWaitingSlotGlobal {
		t.Fatalf("task-7 entry = %+v, want it waiting for a task slot", task2.Queue)
	}
}

// A part worker AO could not start waits 30 seconds, then two minutes,
// before the part starts again; the third failed start fails the task.
func TestAPartWhoseWorkerDoesNotStartWaitsBeforeItTriesAgain(t *testing.T) {
	pf := newPartsFixture(t)
	ctx := context.Background()
	spawnErr := errors.New("the chat driver is not ready")
	pf.sessions.errs = []error{spawnErr}
	pf.tick(t, t0)
	a := pf.parts(t, storeEntry)["a"]
	if a.State != domain.LadoPartWaiting || a.WaitingReason != domain.LadoWaitingRetry || a.Attempts != 1 {
		t.Fatalf("part a after a failed start = %+v, want it waiting to try again", a)
	}
	spawns := len(pf.sessions.spawned())
	pf.tick(t, t0.Add(15*time.Second))
	if len(pf.sessions.spawned()) != spawns {
		t.Fatal("part a started again before its wait was over")
	}
	pf.tick(t, t0.Add(31*time.Second))
	a = pf.parts(t, storeEntry)["a"]
	if a.State != domain.LadoPartRunning || a.Attempts != 2 || a.Branch != "lado/task-1/1/a.2" {
		t.Fatalf("part a after its wait = %+v, want its second worker running", a)
	}
	if task, _, _ := pf.db.LadoTask(ctx, "task-1"); task.StateAt(t0.Add(31*time.Second)) != domain.LadoTaskRunning {
		t.Fatalf("task = %s, want running", task.StateAt(t0))
	}
}

// When another part merges while a merge worker resolves a conflict, the
// worker's branch no longer fast-forwards; LADO merges it onto the moved
// integration branch instead of spending the merge worker's attempt.
func TestAMergeWorkersBranchIsMergedOntoAMovedIntegrationBranch(t *testing.T) {
	pf := newPartsFixture(t)
	integration := domain.LadoIntegrationBranch("task-1", storeEntry)
	pf.tick(t, t0)
	parts := pf.parts(t, storeEntry)
	commitOn(t, pf.repo, "lado/task-1/1/a", map[string]string{"svc/a.go": "package svc\n", "shared.txt": "start\na\n"})
	pf.reportDone(t, parts["a"].SessionID, "a done")
	pf.tick(t, t0.Add(time.Minute))
	headA := pf.branch(t, integration)
	bTip := commitOn(t, pf.repo, "lado/task-1/1/b", map[string]string{"web/b.tsx": "export {}\n", "shared.txt": "start\nb\n"})
	pf.reportDone(t, parts["b"].SessionID, "b done")
	pf.tick(t, t0.Add(2*time.Minute))
	parts = pf.parts(t, storeEntry)
	if parts["b"].State != domain.LadoPartConflict || parts["b"].MergeSessionID == "" {
		t.Fatalf("part b = %+v, want a merge worker", parts["b"])
	}
	// c merges while the merge worker works.
	commitOn(t, pf.repo, "lado/task-1/1/c", map[string]string{"docs/c.md": "c\n"})
	pf.reportDone(t, parts["c"].SessionID, "c done")
	pf.tick(t, t0.Add(3*time.Minute))
	headC := pf.branch(t, integration)
	if headC == headA || pf.parts(t, storeEntry)["c"].State != domain.LadoPartMerged {
		t.Fatal("c did not merge while b's merge worker worked")
	}
	resolved := commitOn(t, pf.repo, "lado/task-1/1/merge-b", map[string]string{"web/b.tsx": "export {}\n", "shared.txt": "start\na\nb\n"}, headA, bTip)
	pf.reportDone(t, pf.parts(t, storeEntry)["b"].MergeSessionID, "kept both")
	spawns := len(pf.sessions.spawned())
	pf.tick(t, t0.Add(4*time.Minute))
	b := pf.parts(t, storeEntry)["b"]
	head := pf.branch(t, integration)
	if b.State != domain.LadoPartMerged || b.MergedSHA != head || b.MergeAttempts != 1 {
		t.Fatalf("part b = %+v, want it merged by its first merge worker", b)
	}
	if parents := strings.Fields(gitIn(t, pf.repo, nil, "rev-list", "--parents", "-n", "1", head)); !slices.Equal(parents[1:], []string{headC, resolved}) {
		t.Fatalf("merge parents = %v, want the moved head then the merge worker's commit", parents[1:])
	}
	if fileAt(t, pf.repo, head, "shared.txt") != "start\na\nb" || fileAt(t, pf.repo, head, "docs/c.md") != "c" {
		t.Fatal("the integration branch lost a side")
	}
	if last := pf.sessions.spawned()[len(pf.sessions.spawned())-1]; len(pf.sessions.spawned()) != spawns+1 || last.ClientRequestID != "lado:lado-queue-1/integration/1" {
		t.Fatalf("spawned %v after the merge, want the integration worker only", spawnIDs(pf.sessions.spawned()[spawns:]))
	}
	pf.requireUntouched(t)
}

// A task approved again runs its new parts run on branches of its own: the
// earlier run's branches, which a worker moved, are left alone and do not
// stop the new run.
func TestATaskApprovedAgainRunsItsPartsOnNewBranches(t *testing.T) {
	pf := newPartsFixture(t)
	ctx := context.Background()
	pf.tick(t, t0)
	first := pf.parts(t, storeEntry)
	commitOn(t, pf.repo, first["a"].Branch, map[string]string{"svc/a.go": "package svc\n"})
	oldIntegration := domain.LadoIntegrationBranch("task-1", storeEntry)
	commitOn(t, pf.repo, oldIntegration, map[string]string{"stray.txt": "moved\n"})

	at := t0.Add(time.Minute)
	approval := domain.LadoPlanApproval{ID: "approval-again", RevisionID: "rev-2", TaskID: "task-1", SHA256: testPlanSHA, ApprovedAt: at}
	snapshot := testSnapshot(claudeAgent(), false)
	snapshot.ID, snapshot.ApprovalID = "snap-again", approval.ID
	entry := domain.LadoQueueEntryID("lado-queue-2nd")
	if _, _, err := pf.db.ApproveLadoPlan(ctx, approval,
		domain.LadoPresenceProof{Nonce: "nonce-again-AAAAAAAAAAAAAAAA", Action: domain.LadoPresencePlanApprove, SubjectID: "rev-2", ExpiresAt: at.Add(time.Minute), UsedAt: at},
		domain.LadoAuditEvent{At: at, Actor: "human", Action: "plan.approved", SubjectKind: "plan_revision", SubjectID: "rev-2"},
		&snapshot, &domain.LadoQueueEntry{ID: entry}); err != nil {
		t.Fatalf("approve again: %v", err)
	}
	pf.tasks.mu.Lock()
	pf.tasks.input.Approval, pf.tasks.input.Snapshot = approval, snapshot
	pf.tasks.mu.Unlock()
	task, _, _ := pf.db.LadoTask(ctx, "task-1")
	task.Plan.Snapshot = domain.LadoPolicySnapshotRef{ID: snapshot.ID, SHA256: snapshot.SHA256}
	pf.policy.sha = snapshot.SHA256

	pf.tick(t, at.Add(time.Minute))
	for _, part := range []string{"a", "b"} {
		if !slices.Contains(pf.sessions.killed(), first[part].SessionID) {
			t.Fatalf("the earlier run's worker of %s still runs", part)
		}
	}
	newIntegration := domain.LadoIntegrationBranch("task-1", entry)
	if newIntegration == oldIntegration || pf.branch(t, newIntegration) != pf.main {
		t.Fatalf("new integration branch %s = %s, want its own branch at the base", newIntegration, pf.branch(t, newIntegration))
	}
	second := pf.parts(t, entry)
	if second["a"].State != domain.LadoPartRunning || second["a"].Branch != "lado/task-1/2nd/a" || pf.branch(t, second["a"].Branch) != pf.main {
		t.Fatalf("part a of the new run = %+v, want a fresh branch at the base", second["a"])
	}
	if old, _, _ := pf.db.LadoTask(ctx, "task-1"); old.Queue.ID != entry || old.StateAt(at.Add(time.Minute)) != domain.LadoTaskRunning {
		t.Fatalf("task = %s with entry %s, want the new run running", old.StateAt(at), old.Queue.ID)
	}
}

// A daemon that stops after moving the integration branch to a merge
// worker's result, and before recording it, records the move when it starts
// again: for a fast-forward and for a merge onto a moved head alike, without
// merging twice.
func TestAMergeWorkersMoveIsRecordedAfterARestart(t *testing.T) {
	for _, moved := range []bool{false, true} {
		t.Run(map[bool]string{false: "fast-forward", true: "merge onto a moved head"}[moved], func(t *testing.T) {
			pf := newPartsFixture(t)
			integration := domain.LadoIntegrationBranch("task-1", storeEntry)
			pf.tick(t, t0)
			parts := pf.parts(t, storeEntry)
			commitOn(t, pf.repo, parts["a"].Branch, map[string]string{"shared.txt": "start\na\n"})
			pf.reportDone(t, parts["a"].SessionID, "a")
			pf.tick(t, t0.Add(time.Minute))
			headA := pf.branch(t, integration)
			bTip := commitOn(t, pf.repo, parts["b"].Branch, map[string]string{"shared.txt": "start\nb\n"})
			pf.reportDone(t, parts["b"].SessionID, "b")
			pf.tick(t, t0.Add(2*time.Minute))
			if moved {
				commitOn(t, pf.repo, pf.parts(t, storeEntry)["c"].Branch, map[string]string{"docs/c.md": "c\n"})
				pf.reportDone(t, pf.parts(t, storeEntry)["c"].SessionID, "c")
				pf.tick(t, t0.Add(3*time.Minute))
			}
			b := pf.parts(t, storeEntry)["b"]
			commitOn(t, pf.repo, b.MergeBranch, map[string]string{"shared.txt": "start\na\nb\n"}, headA, bTip)
			pf.reportDone(t, b.MergeSessionID, "kept both")
			crashing := &crashAfterMove{PartGit: execPartGit{}}
			pf.svc = New(Deps{
				Store: pf.db, Tasks: pf.tasks, Policy: pf.policy, Sessions: pf.sessions, Projects: pf.projects, Branches: pf.branches,
				Memory: pf.memory, Git: pf.git, PartGit: crashing, Clock: pf.clock, NewID: pf.newID,
				Log: slog.New(slog.NewTextHandler(pf.logs, nil)),
			})
			pf.setClock(t0.Add(4 * time.Minute))
			_ = pf.svc.Tick(context.Background(), t0.Add(4*time.Minute))
			after := pf.branch(t, integration)
			if pf.parts(t, storeEntry)["b"].State != domain.LadoPartConflict || !crashing.crashed.Load() {
				t.Fatalf("part b = %+v, want the move made and not recorded", pf.parts(t, storeEntry)["b"])
			}
			pf.svc = pf.dispatcher(pf.db, pf.git)
			pf.tick(t, t0.Add(5*time.Minute))
			b = pf.parts(t, storeEntry)["b"]
			if b.State != domain.LadoPartMerged || b.MergedSHA != after || pf.branch(t, integration) != after {
				t.Fatalf("part b = %+v, integration %s; want the earlier move %s recorded", b, pf.branch(t, integration), after)
			}
			if run, _, _ := pf.db.LadoPartRun(context.Background(), storeEntry); run.HeadSHA != after {
				t.Fatalf("run head = %s, want %s", run.HeadSHA, after)
			}
		})
	}
}

// An integration worker AO could not start is started again only after its
// wait, in a new session.
func TestAnIntegrationWorkerThatDoesNotStartWaitsBeforeItTriesAgain(t *testing.T) {
	pf := newPartsFixture(t)
	pf.sessions.errs = []error{nil, nil, nil, errors.New("the chat driver is not ready")}
	pf.tick(t, t0)
	parts := pf.parts(t, storeEntry)
	pf.reportDone(t, parts["a"].SessionID, "nothing")
	pf.reportDone(t, parts["b"].SessionID, "nothing")
	pf.tick(t, t0.Add(time.Minute))
	pf.reportDone(t, pf.parts(t, storeEntry)["c"].SessionID, "nothing")
	pf.tick(t, t0.Add(2*time.Minute))
	spawns := pf.sessions.spawned()
	if len(spawns) != 4 || spawns[3].ClientRequestID != "lado:lado-queue-1/integration/1" {
		t.Fatalf("spawned %v, want the integration worker's first start", spawnIDs(spawns))
	}
	pf.tick(t, t0.Add(2*time.Minute+15*time.Second))
	if len(pf.sessions.spawned()) != 4 {
		t.Fatal("the integration worker started again before its wait was over")
	}
	pf.tick(t, t0.Add(2*time.Minute+31*time.Second))
	spawns = pf.sessions.spawned()
	if len(spawns) != 5 || spawns[4].ClientRequestID != "lado:lado-queue-1/integration/2" {
		t.Fatalf("spawned %v, want the integration worker's second start", spawnIDs(spawns))
	}
	if task, _, _ := pf.db.LadoTask(context.Background(), "task-1"); task.Execution.Role != domain.LadoRoleIntegration {
		t.Fatalf("task execution = %+v, want the integration worker", task.Execution)
	}
}
