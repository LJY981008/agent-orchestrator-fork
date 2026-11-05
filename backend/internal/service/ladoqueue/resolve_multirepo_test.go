package ladoqueue

// Resolve workers of executions that track a PR in each of several
// repositories of a workspace (#64): the resolve takes the first tracked PR
// that conflicts, in merge order, records it, and works in that PR's
// repository, from its remote and target branch.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	backPR  = "https://github.com/acme/app/pull/7"
	frontPR = "https://github.com/acme/web/pull/8"
)

// multiResolveFixture is the store fixture whose task-1 runs one worker in a
// workspace of back (the issue's repository) and front, each a clone of its
// own remote. The worker opened PR #7 in back, which merges cleanly, and PR
// #8 in front, which conflicts: front's main moved with a change to the same
// file since. LADO tracks both, back first.
type multiResolveFixture struct {
	*storeFixture
	frontRemote, front string
	exec               domain.SessionID
	frontHead, base    string
}

func newMultiResolveFixture(t *testing.T) *multiResolveFixture {
	t.Helper()
	ctx := context.Background()
	backRemote, backStart := newPartsRepo(t)
	frontRemote, frontStart := newPartsRepo(t)
	root := t.TempDir()
	for name, remote := range map[string]string{"back": backRemote, "front": frontRemote} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, nil, "clone", "-q", remote, ".")
	}
	sf := newStoreFixture(t, nil)
	sf.projects.project.Path, sf.projects.project.Kind = root, domain.ProjectKindWorkspace
	sf.projects.repos = []domain.WorkspaceRepoRecord{
		{ProjectID: "proj-1", Name: "back", RelativePath: "back", RepoOriginURL: "https://github.com/acme/app.git", DefaultBranch: "main"},
		{ProjectID: "proj-1", Name: "front", RelativePath: "front", RepoOriginURL: "https://github.com/acme/web.git", DefaultBranch: "main"},
	}
	sf.git.sha = backStart
	sf.sessions.ended = func(id domain.SessionID) { endStoreSession(t, sf, id) }
	sf.tick(t, t0)
	mf := &multiResolveFixture{storeFixture: sf, frontRemote: frontRemote, front: filepath.Join(root, "front"), exec: sf.session(t, 1).ID}

	gitIn(t, backRemote, nil, "branch", "feat-7", backStart)
	backHead := commitOn(t, backRemote, "feat-7", map[string]string{"svc.go": "package svc\n"})
	gitIn(t, frontRemote, nil, "branch", "feat-8", frontStart)
	mf.frontHead = commitOn(t, frontRemote, "feat-8", map[string]string{"shared.txt": "the plan's change\n"})
	mf.base = commitOn(t, frontRemote, "main", map[string]string{"shared.txt": "the base's change\n"})
	at := t0.Add(time.Minute)
	for _, pr := range []domain.PullRequest{
		{URL: backPR, Number: 7, HeadSHA: backHead, SourceBranch: "feat-7", Mergeability: domain.MergeMergeable},
		{URL: frontPR, Number: 8, HeadSHA: mf.frontHead, SourceBranch: "feat-8", Mergeability: domain.MergeConflicting},
	} {
		pr.SessionID, pr.TargetBranch, pr.Title, pr.UpdatedAt = mf.exec, "main", "Change", at
		if err := sf.db.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatalf("write PR %s: %v", pr.URL, err)
		}
	}
	for i, track := range [][2]string{{backPR, "back"}, {frontPR, "front"}} {
		if ok, err := sf.db.TrackLadoExecutionPR(ctx, mf.exec, track[0], track[1], i, at); err != nil || !ok {
			t.Fatalf("track %s = (%v, %v)", track[0], ok, err)
		}
	}
	rec, ok, err := sf.db.GetSession(ctx, mf.exec)
	if err != nil || !ok {
		t.Fatalf("read session %s: %v, %v", mf.exec, ok, err)
	}
	rec.Activity.State = domain.ActivityExited
	if err := sf.db.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("exit session %s: %v", mf.exec, err)
	}
	return mf
}

// The resolve worker of an execution whose front PR conflicts works on that
// PR: its branch is made in front at the PR's head, its prompt names front's
// folder and the PR's source branch, and the resolve records the PR. Back,
// whose PR merges cleanly, gets no branch.
func TestAResolveWorkerWorksInThePRsRepository(t *testing.T) {
	mf := newMultiResolveFixture(t)
	mf.tick(t, t0.Add(3*time.Minute))
	var spawns []ports.SpawnConfig
	for _, cfg := range mf.sessions.spawned() {
		if strings.Contains(cfg.ClientRequestID, "/_resolve/") {
			spawns = append(spawns, cfg)
		}
	}
	if len(spawns) != 1 {
		t.Fatalf("resolve workers = %v, want one", spawnIDs(spawns))
	}
	branch := domain.LadoResolveBranch("task-1", storeEntry, 1)
	if got := gitIn(t, mf.front, nil, "rev-parse", "refs/heads/"+branch); got != mf.frontHead {
		t.Fatalf("the resolve branch in front is at %s, want the PR's head %s", got, mf.frontHead)
	}
	back := filepath.Join(mf.projects.project.Path, "back")
	if got := ladoBranches(t, back); got != "" {
		t.Fatalf("back has LADO branches %q, want none", got)
	}
	for _, want := range []string{"`front/`", frontPR, "git push origin HEAD:feat-8", "git merge --no-ff origin/main", mf.base} {
		if !strings.Contains(spawns[0].Prompt, want) {
			t.Fatalf("the resolve prompt misses %q:\n%s", want, spawns[0].Prompt)
		}
	}
	r, ok, err := mf.db.LadoPRResolve(context.Background(), mf.exec)
	if err != nil || !ok || r.PRURL != frontPR || r.State != domain.LadoResolveResolving || r.StartHeadSHA != mf.frontHead || r.BaseTipSHA != mf.base {
		t.Fatalf("resolve = %+v (%v, %v), want front's PR resolving", r, ok, err)
	}
}

// A resolve left for back's PR after a failed attempt, while back no longer
// conflicts and front does, moves on: its PR and failed attempts are
// forgotten, and the next pass resolves front's PR from a fresh count.
func TestAResolveMovesOnToTheNextConflictingPR(t *testing.T) {
	mf := newMultiResolveFixture(t)
	ctx := context.Background()
	if ok, err := mf.db.UpsertLadoPRResolve(ctx, domain.LadoPRResolve{
		ExecutionSessionID: mf.exec, State: domain.LadoResolveNone, Attempts: 1, Error: "the resolve worker could not start",
		PRURL: backPR, UpdatedAt: t0.Add(time.Minute),
	}); err != nil || !ok {
		t.Fatalf("write back's resolve = (%v, %v)", ok, err)
	}
	mf.tick(t, t0.Add(2*time.Minute))
	r, _, _ := mf.db.LadoPRResolve(ctx, mf.exec)
	if r.PRURL != "" || r.Attempts != 0 || r.Error != "" || r.State != domain.LadoResolveNone {
		t.Fatalf("resolve = %+v, want back's forgotten", r)
	}
	mf.tick(t, t0.Add(3*time.Minute))
	if r, _, _ = mf.db.LadoPRResolve(ctx, mf.exec); r.PRURL != frontPR || r.State != domain.LadoResolveResolving {
		t.Fatalf("resolve = %+v, want front's PR resolving", r)
	}
}
