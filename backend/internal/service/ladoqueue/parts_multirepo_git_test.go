package ladoqueue

// Parts runs and PRs in several repositories of a workspace (#64), on real
// git repositories: a root folder holding back, the issue's repository, and
// front. The run keeps an integration branch in each repository of the
// plan's repos list, merges each part repository by repository, resolves a
// conflict in the one repository it happened in, and records the
// repositories the parts changed as those the integration worker opens PRs
// in.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// multiRepoPlanText is a plan of task-1 that changes back and front, merged
// in that order: part a in back, part b in front.
const multiRepoPlanText = `---
task: task-1
issue: "github:acme/app#12"
base_ref: main
repos: [back, front]
steps:
  - Add the service
  - Add the page
  - Check everything together
acceptance_criteria:
  - works
affected_paths:
  - "back/**"
  - "front/**"
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
    affected_paths: ["back/svc/**", "front/api/**"]
  - id: b
    title: Page
    steps: [2]
    affected_paths: ["back/jobs/**", "front/web/**"]
---

# Plan
`

// localBaseGit reads each repository's own refs: the test repositories have
// no origin to fetch, so the base AO resolves, origin's main, is read as the
// repository's main.
type localBaseGit struct{ execGit }

func (g localBaseGit) RevParse(ctx context.Context, dir, ref string) (string, error) {
	if ref == "refs/remotes/origin/main" {
		ref = "refs/heads/main"
	}
	return g.execGit.RevParse(ctx, dir, ref)
}

// multiRepoFixture is the workspace fixture running plan, whose dispatcher
// reads the base of each repository in that repository.
type multiRepoFixture struct {
	*workspaceFixture
	backMain, frontMain string
}

func newMultiRepoFixture(t *testing.T, plan string) *multiRepoFixture {
	t.Helper()
	wf := newWorkspaceFixture(t, plan)
	wf.svc = wf.dispatcher(wf.db, localBaseGit{})
	return &multiRepoFixture{workspaceFixture: wf, backMain: wf.main, frontMain: gitIn(t, wf.front, nil, "rev-parse", "main")}
}

func branchIn(t *testing.T, dir, name string) string {
	t.Helper()
	sha, err := execPartGit{}.BranchSHA(context.Background(), dir, name)
	if err != nil {
		t.Fatalf("read branch %s in %s: %v", name, dir, err)
	}
	return sha
}

func (mf *multiRepoFixture) runRepos(t *testing.T) []domain.LadoPartRunRepo {
	t.Helper()
	repos, err := mf.db.LadoPartRunRepos(context.Background(), storeEntry)
	if err != nil {
		t.Fatalf("read the run's repositories: %v", err)
	}
	return repos
}

// partRepos returns how part went in each repository, by folder.
func (mf *multiRepoFixture) partRepos(t *testing.T, part string) map[string]domain.LadoTaskPartRepo {
	t.Helper()
	all, err := mf.db.LadoTaskPartRepos(context.Background(), storeEntry)
	if err != nil {
		t.Fatalf("read the parts' repositories: %v", err)
	}
	byRepo := map[string]domain.LadoTaskPartRepo{}
	for _, row := range all[part] {
		byRepo[row.Relative] = row
	}
	return byRepo
}

// integration returns the spawn of the run's integration worker and its
// execution.
func (mf *multiRepoFixture) integration(t *testing.T) (ports.SpawnConfig, domain.LadoExecution) {
	t.Helper()
	for _, cfg := range mf.sessions.spawned() {
		if cfg.ClientRequestID != "lado:lado-queue-1/integration/1" {
			continue
		}
		rec, found, err := mf.db.GetSessionByClientRequestID(context.Background(), cfg.ClientRequestID)
		if err != nil || !found {
			t.Fatalf("session of the integration worker = (%v, %v)", found, err)
		}
		return cfg, mf.execution(t, rec.ID)
	}
	t.Fatalf("no integration worker started: %v", spawnIDs(mf.sessions.spawned()))
	return ports.SpawnConfig{}, domain.LadoExecution{}
}

// A plan whose repos list names back and front runs its parts in both: the
// integration branch starts at each repository's own main, a part's branch
// is made in both, each part's commit merges into the repository it was
// made in, and how each part went is recorded per repository. The
// integration worker learns of both repositories and their order, and its
// execution expects a PR in back and then front.
func TestMultiRepoPartsRunMergesEachRepository(t *testing.T) {
	mf := newMultiRepoFixture(t, multiRepoPlanText)
	ctx := context.Background()
	integration := domain.LadoIntegrationBranch("task-1", storeEntry)

	mf.tick(t, t0)
	repos := mf.runRepos(t)
	if len(repos) != 2 || repos[0].Relative != "back" || repos[0].Dir != mf.back || repos[0].BaseSHA != mf.backMain ||
		repos[1].Relative != "front" || repos[1].Dir != mf.front || repos[1].BaseSHA != mf.frontMain || repos[1].Position != 1 ||
		repos[1].BaseBranch != "main" || repos[1].BaseRemote != "origin" {
		t.Fatalf("run repositories = %+v, want back then front at their mains", repos)
	}
	run, ok, err := mf.db.LadoPartRun(ctx, storeEntry)
	if err != nil || !ok || run.RepoDir != mf.back || run.BaseSHA != mf.backMain {
		t.Fatalf("parts run = %+v (%v, %v), want back's values", run, ok, err)
	}
	if branchIn(t, mf.back, integration) != mf.backMain || branchIn(t, mf.front, integration) != mf.frontMain {
		t.Fatal("the integration branches do not start at each repository's main")
	}
	parts := mf.parts(t, storeEntry)
	for _, id := range []string{"a", "b"} {
		if branchIn(t, mf.back, parts[id].Branch) != mf.backMain || branchIn(t, mf.front, parts[id].Branch) != mf.frontMain {
			t.Fatalf("part %s's branch %s is not in both repositories at their mains", id, parts[id].Branch)
		}
		if rows := mf.partRepos(t, id); rows["back"].StartSHA != mf.backMain || rows["front"].StartSHA != mf.frontMain {
			t.Fatalf("part %s's starts = %+v, want each repository's main", id, rows)
		}
	}
	for _, cfg := range mf.sessions.spawned() {
		if !strings.Contains(cfg.Prompt, "`back/` and `front/`") {
			t.Fatalf("part prompt %s does not name both repositories:\n%s", cfg.ClientRequestID, cfg.Prompt)
		}
	}

	aTip := commitOn(t, mf.back, parts["a"].Branch, map[string]string{"svc/a.go": "package svc\n"})
	bTip := commitOn(t, mf.front, parts["b"].Branch, map[string]string{"web/b.tsx": "export {}\n"})
	mf.reportDone(t, parts["a"].SessionID, "added the service")
	mf.reportDone(t, parts["b"].SessionID, "added the page")
	mf.tick(t, t0.Add(time.Minute))
	parts = mf.parts(t, storeEntry)
	if parts["a"].State != domain.LadoPartMerged || parts["b"].State != domain.LadoPartMerged {
		t.Fatalf("parts = %+v, want both merged", parts)
	}
	backHead, frontHead := branchIn(t, mf.back, integration), branchIn(t, mf.front, integration)
	if fileAt(t, mf.back, integration, "svc/a.go") != "package svc" || fileAt(t, mf.front, integration, "web/b.tsx") != "export {}" {
		t.Fatal("the integration branches lack the parts' files")
	}
	if ok, _ := (execPartGit{}).IsAncestor(ctx, mf.back, aTip, backHead); !ok {
		t.Fatalf("a's commit is not on back's integration branch")
	}
	if ok, _ := (execPartGit{}).IsAncestor(ctx, mf.front, bTip, frontHead); !ok {
		t.Fatalf("b's commit is not on front's integration branch")
	}
	a, b := mf.partRepos(t, "a"), mf.partRepos(t, "b")
	if a["back"].MergeTipSHA != aTip || a["back"].MergedSHA == mf.backMain || !a["front"].Empty() || a["front"].MergedSHA != mf.frontMain {
		t.Fatalf("part a's repositories = %+v, want a merge in back and none in front", a)
	}
	if b["front"].MergeTipSHA != bTip || b["front"].MergedSHA != frontHead || !b["back"].Empty() || b["back"].MergedSHA == "" {
		t.Fatalf("part b's repositories = %+v, want a merge in front and none in back", b)
	}
	if repos = mf.runRepos(t); repos[0].HeadSHA != backHead || repos[1].HeadSHA != frontHead {
		t.Fatalf("run repositories = %+v, want heads %s and %s", repos, backHead, frontHead)
	}
	if run, _, _ = mf.db.LadoPartRun(ctx, storeEntry); run.HeadSHA != backHead {
		t.Fatalf("run head = %s, want back's %s", run.HeadSHA, backHead)
	}

	cfg, exec := mf.integration(t)
	for _, want := range []string{"`back/`: tip `" + backHead + "`", "`front/`: tip `" + frontHead + "`", "`back/` against `main`, then `front/` against `main`", "open one pull request in it"} {
		if !strings.Contains(cfg.Prompt, want) {
			t.Fatalf("the integration prompt misses %q:\n%s", want, cfg.Prompt)
		}
	}
	if !slices.Equal(exec.PRRepos, []string{"back", "front"}) || !exec.PRReposFirm || exec.Branch != integration || exec.StartSHA != backHead {
		t.Fatalf("integration execution = %+v, want PRs expected in back then front, firmly", exec)
	}
	mf.requireUntouched(t)
}

// A part whose merge conflicts in front only keeps its merge in back: the
// merge worker starts in front alone, from front's integration head, and
// once it reports done front's integration branch moves to its merge while
// back's stays where the part's merge left it.
func TestMultiRepoConflictInOneRepositoryIsResolvedThere(t *testing.T) {
	mf := newMultiRepoFixture(t, multiRepoPlanText)
	integration := domain.LadoIntegrationBranch("task-1", storeEntry)

	mf.tick(t, t0)
	parts := mf.parts(t, storeEntry)
	commitOn(t, mf.back, parts["a"].Branch, map[string]string{"svc/a.go": "package svc\n"})
	commitOn(t, mf.front, parts["a"].Branch, map[string]string{"shared.txt": "start\na\n"})
	commitOn(t, mf.back, parts["b"].Branch, map[string]string{"jobs/b.go": "package jobs\n"})
	bFront := commitOn(t, mf.front, parts["b"].Branch, map[string]string{"shared.txt": "start\nb\n"})
	mf.reportDone(t, parts["a"].SessionID, "a")
	mf.reportDone(t, parts["b"].SessionID, "b")
	mf.tick(t, t0.Add(time.Minute))

	parts = mf.parts(t, storeEntry)
	b := parts["b"]
	if parts["a"].State != domain.LadoPartMerged || b.State != domain.LadoPartConflict || b.MergeRepo != "front" || b.MergeSessionID == "" {
		t.Fatalf("parts = %+v, want a merged and b in conflict in front with a merge worker", parts)
	}
	backHead := branchIn(t, mf.back, integration)
	if fileAt(t, mf.back, integration, "jobs/b.go") != "package jobs" {
		t.Fatal("b's merge in back is not on back's integration branch")
	}
	if rows := mf.partRepos(t, "b"); rows["back"].MergedSHA != backHead || rows["front"].MergedSHA != "" || rows["front"].MergeTipSHA != bFront {
		t.Fatalf("part b's repositories = %+v, want back merged and front waiting", rows)
	}
	frontHead := branchIn(t, mf.front, integration)
	if got := branchIn(t, mf.front, b.MergeBranch); got != frontHead {
		t.Fatalf("the merge branch in front is at %s, want front's integration head %s", got, frontHead)
	}
	if got := branchIn(t, mf.back, b.MergeBranch); got != "" {
		t.Fatalf("back has the merge branch at %s, want none", got)
	}
	var merge ports.SpawnConfig
	for _, cfg := range mf.sessions.spawned() {
		if req, ok := domain.ParseLadoRunClientRequestID(cfg.ClientRequestID); ok && req.Role == domain.LadoRoleMerge {
			merge = cfg
		}
	}
	for _, want := range []string{"`front/`", bFront, "git merge --no-ff " + bFront} {
		if !strings.Contains(merge.Prompt, want) {
			t.Fatalf("the merge prompt misses %q:\n%s", want, merge.Prompt)
		}
	}

	resolved := commitOn(t, mf.front, b.MergeBranch, map[string]string{"shared.txt": "start\na\nb\n"}, frontHead, bFront)
	mf.reportDone(t, b.MergeSessionID, "kept both lines")
	mf.tick(t, t0.Add(2*time.Minute))
	b = mf.parts(t, storeEntry)["b"]
	if b.State != domain.LadoPartMerged || b.MergeRepo != "" || !strings.Contains(b.Summary, "Merge worker: kept both lines") {
		t.Fatalf("part b = %+v, want it merged with the merge worker's note", b)
	}
	if got := branchIn(t, mf.front, integration); got != resolved {
		t.Fatalf("front's integration branch = %s, want the merge worker's %s", got, resolved)
	}
	if got := branchIn(t, mf.back, integration); got != backHead {
		t.Fatalf("back's integration branch moved to %s, want it kept at %s", got, backHead)
	}
	_, exec := mf.integration(t)
	if !slices.Equal(exec.PRRepos, []string{"back", "front"}) || !exec.PRReposFirm {
		t.Fatalf("integration execution PR repositories = %v (firm %v), want back and front", exec.PRRepos, exec.PRReposFirm)
	}
	mf.requireUntouched(t)
}

// Each repository whose merge conflicts gets merge workers of its own, as a
// run in one repository gives each conflict (review of #64): part b
// conflicts in back and one merge worker resolves it, then b conflicts in
// front, and that repository's first merge worker ending without reporting
// done is followed by another rather than failing the run.
func TestMultiRepoEachConflictingRepositoryGetsItsOwnMergeAttempts(t *testing.T) {
	mf := newMultiRepoFixture(t, multiRepoPlanText)
	integration := domain.LadoIntegrationBranch("task-1", storeEntry)

	mf.tick(t, t0)
	parts := mf.parts(t, storeEntry)
	commitOn(t, mf.back, parts["a"].Branch, map[string]string{"shared.txt": "start\na\n"})
	commitOn(t, mf.front, parts["a"].Branch, map[string]string{"shared.txt": "start\na\n"})
	bBack := commitOn(t, mf.back, parts["b"].Branch, map[string]string{"shared.txt": "start\nb\n"})
	commitOn(t, mf.front, parts["b"].Branch, map[string]string{"shared.txt": "start\nb\n"})
	mf.reportDone(t, parts["a"].SessionID, "a")
	mf.reportDone(t, parts["b"].SessionID, "b")
	mf.tick(t, t0.Add(time.Minute))

	b := mf.parts(t, storeEntry)["b"]
	if b.State != domain.LadoPartConflict || b.MergeRepo != "back" || b.MergeAttempts != 1 || b.MergeSessionID == "" {
		t.Fatalf("part b = %+v, want it in conflict in back with its first merge worker", b)
	}
	backHead := branchIn(t, mf.back, integration)
	commitOn(t, mf.back, b.MergeBranch, map[string]string{"shared.txt": "start\na\nb\n"}, backHead, bBack)
	mf.reportDone(t, b.MergeSessionID, "kept both lines in back")
	mf.tick(t, t0.Add(2*time.Minute))

	b = mf.parts(t, storeEntry)["b"]
	if b.State != domain.LadoPartConflict || b.MergeRepo != "front" || b.MergeAttempts != 2 || b.MergeAttemptsBase != 1 || b.MergeSessionID == "" {
		t.Fatalf("part b = %+v, want it in conflict in front with front's first merge worker", b)
	}
	first := b.MergeSessionID
	endStoreSession(t, mf.storeFixture, first)
	mf.tick(t, t0.Add(3*time.Minute))

	b = mf.parts(t, storeEntry)["b"]
	if b.State != domain.LadoPartConflict || b.MergeRepo != "front" || b.MergeAttempts != 3 || b.MergeSessionID == "" || b.MergeSessionID == first {
		t.Fatalf("part b = %+v, want front's second merge worker after the first ended without done", b)
	}
	endStoreSession(t, mf.storeFixture, b.MergeSessionID)
	mf.tick(t, t0.Add(4*time.Minute))
	if b = mf.parts(t, storeEntry)["b"]; b.State != domain.LadoPartFailed {
		t.Fatalf("part b = %+v, want the run failed once front's two merge workers ended without done", b)
	}
}

// A run whose parts left front as it was expects a PR in back only: the
// front part, allowed to end without a commit, merges nothing, front's
// integration branch stays at its main, and the integration worker is told
// to open no PR in front.
func TestMultiRepoUnchangedRepositoryGetsNoPR(t *testing.T) {
	plan := strings.Replace(multiRepoPlanText, `    affected_paths: ["back/jobs/**", "front/web/**"]
`, `    affected_paths: ["front/web/**"]
    allow_empty: true
`, 1)
	mf := newMultiRepoFixture(t, plan)
	integration := domain.LadoIntegrationBranch("task-1", storeEntry)

	mf.tick(t, t0)
	parts := mf.parts(t, storeEntry)
	commitOn(t, mf.back, parts["a"].Branch, map[string]string{"svc/a.go": "package svc\n"})
	mf.reportDone(t, parts["a"].SessionID, "added the service")
	mf.reportDone(t, parts["b"].SessionID, "nothing to change")
	mf.tick(t, t0.Add(time.Minute))
	parts = mf.parts(t, storeEntry)
	if parts["a"].State != domain.LadoPartMerged || parts["b"].State != domain.LadoPartMerged {
		t.Fatalf("parts = %+v, want both merged", parts)
	}
	if got := branchIn(t, mf.front, integration); got != mf.frontMain {
		t.Fatalf("front's integration branch = %s, want it at front's main", got)
	}
	cfg, exec := mf.integration(t)
	if !slices.Equal(exec.PRRepos, []string{"back"}) || !exec.PRReposFirm {
		t.Fatalf("integration execution PR repositories = %v (firm %v), want back only", exec.PRRepos, exec.PRReposFirm)
	}
	for _, want := range []string{"`front/`: tip `" + mf.frontMain + "`, which the parts did not change", "Open no pull request in `front/`", "`back/` against `main`."} {
		if !strings.Contains(cfg.Prompt, want) {
			t.Fatalf("the integration prompt misses %q:\n%s", want, cfg.Prompt)
		}
	}
}

// A parts plan naming paths outside its repos list fails at dispatch with
// LADO_PARTS_OUTSIDE_REPO naming both repositories, and starts nothing.
func TestMultiRepoPlanOutsideItsReposFails(t *testing.T) {
	plan := strings.Replace(multiRepoPlanText, `  - "front/**"
`, `  - "front/**"
  - "docs/**"
`, 1)
	mf := newMultiRepoFixture(t, plan)
	mf.tick(t, t0)
	task, _, _ := mf.db.LadoTask(context.Background(), "task-1")
	if task.Queue.FailedAt.IsZero() || !strings.HasPrefix(task.Queue.ErrorMessage, "LADO_PARTS_OUTSIDE_REPO: ") ||
		!strings.Contains(task.Queue.ErrorMessage, "back/ and front/") || !strings.Contains(task.Queue.ErrorMessage, "docs/**") {
		t.Fatalf("entry = %+v, want it failed for docs/** outside back and front", task.Queue)
	}
	for _, dir := range []string{mf.back, mf.front} {
		if got := ladoBranches(t, dir); got != "" {
			t.Fatalf("%s has LADO branches %q, want none", dir, got)
		}
	}
}

// singleRepoPlanText is a plan without parts whose worker changes back and
// front.
const singleRepoPlanText = `---
task: task-1
issue: "github:acme/app#12"
base_ref: main
repos: [back, front]
steps:
  - Change both
acceptance_criteria:
  - works
affected_paths:
  - "back/**"
  - "front/**"
test_commands: []
risk: low
permission_ceiling: auto
review:
  required: false
plan_source: issue
---

# Plan
`

// A single worker whose plan names its repositories opens a PR in each it
// changes: its execution keeps them as declared, not as facts, and its
// prompt says so, with what to report for a repository it did not change.
func TestASingleWorkerKeepsThePlansRepos(t *testing.T) {
	mf := newMultiRepoFixture(t, singleRepoPlanText)
	mf.tick(t, t0)
	spawns := mf.sessions.spawned()
	if len(spawns) != 1 {
		t.Fatalf("spawned %v, want the single worker", spawnIDs(spawns))
	}
	for _, want := range []string{"`back/`, then `front/`", "open one pull request", "say so in your done report"} {
		if !strings.Contains(spawns[0].Prompt, want) {
			t.Fatalf("the worker prompt misses %q:\n%s", want, spawns[0].Prompt)
		}
	}
	exec := mf.execution(t, mf.session(t, 1).ID)
	if !slices.Equal(exec.PRRepos, []string{"back", "front"}) || exec.PRReposFirm {
		t.Fatalf("execution PR repositories = %v (firm %v), want back and front as declared", exec.PRRepos, exec.PRReposFirm)
	}
}

// crashOnFrontMerge stands for a daemon that stops after LADO merged a part
// in front and moved front's integration branch, before it recorded the
// merge: every record of a merge in front fails, and nothing else does.
type crashOnFrontMerge struct {
	*store.Store
	crashes atomic.Int32
}

func (c *crashOnFrontMerge) MergeLadoTaskPartRepo(ctx context.Context, part domain.LadoTaskPart, old domain.LadoPartState, repo domain.LadoTaskPartRepo,
	oldHead string, event *domain.LadoAuditEvent) (bool, error) {
	if repo.Relative == "front" && !repo.Empty() {
		c.crashes.Add(1)
		return false, errors.New("the daemon stopped")
	}
	return c.Store.MergeLadoTaskPartRepo(ctx, part, old, repo, oldHead, event)
}

// A merge in front that LADO made and moved the branch to, but did not
// record before it stopped, is recorded once it starts again, without
// merging again; back's merge stays as it was.
func TestMultiRepoMergeLeftUnrecordedIsRecoveredInItsRepository(t *testing.T) {
	mf := newMultiRepoFixture(t, multiRepoPlanText)
	integration := domain.LadoIntegrationBranch("task-1", storeEntry)
	crash := &crashOnFrontMerge{Store: mf.db}
	mf.svc = mf.dispatcher(crash, localBaseGit{})

	mf.tick(t, t0)
	parts := mf.parts(t, storeEntry)
	commitOn(t, mf.back, parts["a"].Branch, map[string]string{"svc/a.go": "package svc\n"})
	bTip := commitOn(t, mf.front, parts["b"].Branch, map[string]string{"web/b.tsx": "export {}\n"})
	mf.reportDone(t, parts["a"].SessionID, "a")
	mf.reportDone(t, parts["b"].SessionID, "b")
	mf.setClock(t0.Add(time.Minute))
	if err := mf.svc.Tick(context.Background(), t0.Add(time.Minute)); err == nil || crash.crashes.Load() == 0 {
		t.Fatalf("the pass that stopped before recording reported %v after %d crashes, want an error", err, crash.crashes.Load())
	}
	b := mf.parts(t, storeEntry)["b"]
	front := branchIn(t, mf.front, integration)
	if b.State != domain.LadoPartMerging || front == mf.frontMain {
		t.Fatalf("part b = %+v with front at %s, want it merging with front's branch moved", b, front)
	}
	backHead := branchIn(t, mf.back, integration)

	mf.svc = mf.dispatcher(mf.db, localBaseGit{})
	mf.tick(t, t0.Add(2*time.Minute))
	b = mf.parts(t, storeEntry)["b"]
	rows := mf.partRepos(t, "b")
	if b.State != domain.LadoPartMerged || rows["front"].MergedSHA != front || rows["front"].MergeTipSHA != bTip {
		t.Fatalf("part b = %+v, repositories %+v; want it merged at front's moved head %s", b, rows, front)
	}
	if got := branchIn(t, mf.front, integration); got != front {
		t.Fatalf("front's integration branch = %s, want it left at %s", got, front)
	}
	if got := branchIn(t, mf.back, integration); got != backHead {
		t.Fatalf("back's integration branch = %s, want it left at %s", got, backHead)
	}
	if repos := mf.runRepos(t); repos[1].HeadSHA != front {
		t.Fatalf("front's recorded head = %s, want %s", repos[1].HeadSHA, front)
	}
}
