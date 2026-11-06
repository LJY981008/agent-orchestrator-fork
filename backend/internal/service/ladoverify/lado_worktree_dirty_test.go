package ladoverify

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LADO #22: a worktree at the head with changes before the first run waits
// as worktree_dirty and lists its paths as git keeps them; another HEAD
// still waits as worktree_not_at_head and a change after a run as
// worktree_changed.
func TestWorktreeCheckListsTheDirtyPaths(t *testing.T) {
	ctx := context.Background()
	git := ExecGit{}
	r := newTestRepo(t, t0)
	base := r.git(t0, r.dir, "rev-parse", "refs/remotes/origin/main")
	wt := r.worktree(t0, "lado/dirty")
	r.write(wt, "old name.txt", "one\n")
	head := r.commit(t0, wt, "Add a file")

	if state := WorktreeCheck(ctx, git, wt, head, nil); state.Code != "" || state.Dirty != nil {
		t.Fatalf("clean worktree = %+v, want ready", state)
	}
	// A staged rename by its new name, and untracked names with a space, an
	// arrow and what reads as a link digest.
	r.git(t0, wt, "mv", "old name.txt", "new <- name.txt")
	r.write(wt, "notes draft.txt", "x\n")
	r.write(wt, "z link:target", "y\n")
	r.write(wt, ".husky/_/post-checkout", "#!/bin/sh\n")

	state := WorktreeCheck(ctx, git, wt, head, nil)
	want := []string{".husky/_/post-checkout", "new <- name.txt", "notes draft.txt", "z link:target"}
	if state.Code != domain.LadoCodeWorktreeDirty || state.DirtyCount != len(want) || !sameSet(state.Dirty, want) {
		t.Fatalf("dirty worktree = %+v, want worktree_dirty with %q", state, want)
	}
	if got := WorktreeReady(ctx, git, wt, head, nil); got != domain.LadoCodeWorktreeDirty {
		t.Fatalf("WorktreeReady = %q, want worktree_dirty", got)
	}

	// More than LadoDirtyFilesMax paths: the first five and the count.
	for i := range 4 {
		r.write(wt, fmt.Sprintf("extra/%d.txt", i), "z\n")
	}
	state = WorktreeCheck(ctx, git, wt, head, nil)
	if state.Code != domain.LadoCodeWorktreeDirty || len(state.Dirty) != domain.LadoDirtyFilesMax || state.DirtyCount != 8 {
		t.Fatalf("many dirty paths = %+v, want 5 of 8", state)
	}

	// Another HEAD, with the same changes, still waits for the head.
	if state := WorktreeCheck(ctx, git, wt, base, nil); state.Code != domain.LadoCodeWorktreeNotAtHead || state.Dirty != nil {
		t.Fatalf("another head = %+v, want worktree_not_at_head without paths", state)
	}
	// After a run, another change is worktree_changed, without paths.
	run := domain.LadoCheckRun{Name: "unit", Status: domain.LadoCheckRunPassed, StartedAt: t0, FinishedAt: t0.Add(time.Minute), WorktreeSHA256: "other"}
	if state := WorktreeCheck(ctx, git, wt, head, []domain.LadoCheckRun{run}); state.Code != domain.LadoCodeWorktreeChanged || state.Dirty != nil {
		t.Fatalf("after a run = %+v, want worktree_changed", state)
	}
	// A Git without DirtyPaths still tells worktree_dirty, without paths.
	if state := WorktreeCheck(ctx, statusOnlyGit{ExecGit{}}, wt, head, nil); state.Code != domain.LadoCodeWorktreeDirty || state.Dirty != nil || state.DirtyCount != 0 {
		t.Fatalf("without DirtyPaths = %+v, want worktree_dirty alone", state)
	}
}

// statusOnlyGit is a Git without DirtyPaths.
type statusOnlyGit struct{ Git }

func sameSet(got, want []string) bool {
	return len(got) == len(want) && !slices.ContainsFunc(want, func(w string) bool { return !slices.Contains(got, w) })
}

func TestCommandsItemCarriesTheDirtyPaths(t *testing.T) {
	commands := []domain.LadoCheckCommand{{Name: "unit", Command: "make unit"}}
	dirty := []string{"a", "b", "c", "d", "e", "f"}
	item, err := EvaluateCommands(CommandsInput{Commands: commands, WorktreeCode: domain.LadoCodeWorktreeDirty, Dirty: dirty, DirtyCount: 9})
	if err != nil {
		t.Fatal(err)
	}
	expectItemOutcome(t, item, domain.LadoVerifyCommands, domain.LadoVerifyWait, domain.LadoCodeWorktreeDirty)
	details := commandsDetailsOf(t, item)
	if !slices.Equal(details.Dirty, dirty[:domain.LadoDirtyFilesMax]) || details.DirtyCount != 9 {
		t.Fatalf("details = %+v, want the first five paths and 9", details)
	}
	// Another code carries no paths, so older records keep their hash.
	item, err = EvaluateCommands(CommandsInput{Commands: commands, WorktreeCode: domain.LadoCodeWorktreeNotAtHead, Dirty: dirty, DirtyCount: 9})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(item.Details), "dirty") {
		t.Fatalf("worktree_not_at_head details = %s, want no dirty paths", item.Details)
	}
	// A failure wins over the wait.
	failed := checkRunAt("r1", "unit", domain.LadoCheckRunFailed, t0, 1)
	item, err = EvaluateCommands(CommandsInput{Commands: append(commands, domain.LadoCheckCommand{Name: "lint", Command: "make lint"}),
		Runs: []domain.LadoCheckRun{failed}, WorktreeCode: domain.LadoCodeWorktreeDirty, Dirty: dirty[:1], DirtyCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	expectItemOutcome(t, item, domain.LadoVerifyCommands, domain.LadoVerifyFail, domain.LadoCodeCommandsFailed)
}

func commandsDetailsOf(t *testing.T, item domain.LadoVerifyItem) domain.LadoCommandsDetails {
	t.Helper()
	var details domain.LadoCommandsDetails
	if err := json.Unmarshal(item.Details, &details); err != nil {
		t.Fatal(err)
	}
	return details
}

// dirtyPathGit is the fixture's Git with DirtyPaths, which lists what was
// set for each worktree.
type dirtyPathGit struct {
	*verifyGit
	paths map[string][]string
}

func (g dirtyPathGit) DirtyPaths(_ context.Context, dir string) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.paths[dir]), nil
}

// newDirtyFixture is a verification fixture whose Git lists dirty paths,
// with one check command, and the paths map it reads.
func newDirtyFixture(t *testing.T) (*verifyFixture, map[string][]string) {
	t.Helper()
	paths := map[string][]string{}
	f := newVerifyFixture(t, func(d *Deps) { d.Git = dirtyPathGit{verifyGit: d.Git.(*verifyGit), paths: paths} })
	f.store.setChecks([]domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}}, 30)
	return f, paths
}

// makeDirty gives the worktree untracked files: the status hash input and
// the paths DirtyPaths lists.
func makeDirty(f *verifyFixture, paths map[string][]string, worktree string, files ...string) {
	f.git.mu.Lock()
	paths[worktree] = files
	f.git.mu.Unlock()
	var status strings.Builder
	for _, file := range files {
		status.WriteString("?? " + file + " x\n")
	}
	f.git.setStatus(worktree, status.String())
}

func (s *Service) dirtyWaitOf(key domain.SessionID) (dirtyWait, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wait, ok := s.dirtySince[key]
	return wait, ok
}

func (s *Service) dirtyWaits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.dirtySince)
}

// LADO #22: the worker hears once per head, after worktreeDirtyNotifyAfter,
// which files keep its worktree from being clean and how to clean it.
func TestTickAsksTheWorkerToCleanADirtyWorktree(t *testing.T) {
	f, paths := newDirtyFixture(t)
	exec := f.addExecution("1", verifyHead1)
	wt := f.reads.worktree(exec.SessionID)
	files := []string{".husky/_/post-checkout", ".husky/_/post-commit", "a b.txt", "c <- d.txt", "e", "f", "g"}
	makeDirty(f, paths, wt, files...)

	f.tick()
	record := f.record(exec.SessionID, verifyHead1)
	item := verifyItemOf(t, record, domain.LadoVerifyCommands)
	if item.Outcome != domain.LadoVerifyWait || item.Code != domain.LadoCodeWorktreeDirty {
		t.Fatalf("commands item = %s %s, want wait worktree_dirty", item.Outcome, item.Code)
	}
	if details := commandsDetailsOf(t, item); !slices.Equal(details.Dirty, files[:5]) || details.DirtyCount != len(files) {
		t.Fatalf("details = %+v, want the first five files of %d", details, len(files))
	}
	if outcome, reason := record.Overall(); outcome != domain.LadoVerifyWait || reason != domain.LadoCodeWorktreeDirty {
		t.Fatalf("record = %s %s, want wait worktree_dirty", outcome, reason)
	}
	first, ok := f.svc.dirtyWaitOf(exec.SessionID)
	if !ok || first.head != verifyHead1 {
		t.Fatalf("wait = %+v, %v, want one for head 1", first, ok)
	}

	// Before two minutes: no message, and the time it started stays.
	f.clock.advance(worktreeDirtyNotifyAfter - time.Second)
	f.tick()
	if got := f.sender.to(exec.SessionID); len(got) != 0 {
		t.Fatalf("messages before two minutes = %q, want none", got)
	}
	if wait, _ := f.svc.dirtyWaitOf(exec.SessionID); !wait.since.Equal(first.since) {
		t.Fatalf("wait since = %s, want %s kept", wait.since, first.since)
	}

	// After: one message with the files, then none again for the head.
	f.clock.advance(time.Second)
	f.tick()
	f.clock.advance(time.Minute)
	f.tick()
	got := f.sender.to(exec.SessionID)
	if len(got) != 1 {
		t.Fatalf("messages = %q, want one", got)
	}
	message := got[0]
	for _, want := range []string{shortCommit(verifyHead1), "not committed", ".gitignore", "a b.txt\nc <- d.txt\ne", "\nand 2 more"} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q lacks %q", message, want)
		}
	}
	if strings.Contains(message, "info/exclude") || strings.Contains(message, "f\n") {
		t.Errorf("message %q names info/exclude or a sixth file", message)
	}
	if record := f.record(exec.SessionID, verifyHead1); !slices.Contains(record.Notified, domain.LadoNotifiedWorktreeDirty) {
		t.Fatalf("notified = %v, want worktree_dirty", record.Notified)
	}

	// A new head measures again, and the map keeps one entry.
	f.push(exec, verifyHead2)
	f.tick()
	wait, ok := f.svc.dirtyWaitOf(exec.SessionID)
	if !ok || wait.head != verifyHead2 || !wait.since.Equal(f.svc.now()) || f.svc.dirtyWaits() != 1 {
		t.Fatalf("wait after a new head = %+v, %v (%d entries), want head 2 from now", wait, ok, f.svc.dirtyWaits())
	}
	if got := f.sender.to(exec.SessionID); len(got) != 1 {
		t.Fatalf("messages after a new head = %d, want still 1", len(got))
	}

	// A clean worktree forgets the wait.
	f.git.setDirty(wt, false)
	f.tick()
	if _, ok := f.svc.dirtyWaitOf(exec.SessionID); ok {
		t.Fatal("the wait stays after the worktree was cleaned")
	}
}

// A worker that ended is not told; the card shows the files.
func TestTickDoesNotTellAnEndedWorkerToClean(t *testing.T) {
	f, paths := newDirtyFixture(t)
	exec := f.addExecution("1", verifyHead1)
	makeDirty(f, paths, f.reads.worktree(exec.SessionID), "build.log")
	f.reads.endSession(exec.SessionID)

	f.tick()
	f.clock.advance(worktreeDirtyNotifyAfter + time.Minute)
	f.tick()
	if got := f.sender.to(exec.SessionID); len(got) != 0 {
		t.Fatalf("messages to an ended worker = %q, want none", got)
	}
	record := f.record(exec.SessionID, verifyHead1)
	if slices.Contains(record.Notified, domain.LadoNotifiedWorktreeDirty) {
		t.Fatalf("notified = %v, want no worktree_dirty", record.Notified)
	}
	if item := verifyItemOf(t, record, domain.LadoVerifyCommands); item.Code != domain.LadoCodeWorktreeDirty ||
		!slices.Equal(commandsDetailsOf(t, item).Dirty, []string{"build.log"}) {
		t.Fatalf("commands item = %s %s, want worktree_dirty with build.log", item.Code, item.Details)
	}
}

// A message that could not be sent goes out on a later pass; a settled PR
// forgets the wait.
func TestTickRetriesTheCleanMessageAndForgetsASettledPR(t *testing.T) {
	f, paths := newDirtyFixture(t)
	exec := f.addExecution("1", verifyHead1)
	makeDirty(f, paths, f.reads.worktree(exec.SessionID), "out.txt")
	f.tick()
	f.clock.advance(worktreeDirtyNotifyAfter)
	f.sender.failNext(exec.SessionID, 1)
	f.tick()
	if record := f.record(exec.SessionID, verifyHead1); slices.Contains(record.Notified, domain.LadoNotifiedWorktreeDirty) {
		t.Fatalf("notified after a failed send = %v, want no worktree_dirty", record.Notified)
	}
	f.tick()
	if got := f.sender.to(exec.SessionID); len(got) != 1 || !strings.HasSuffix(got[0], "\n\nout.txt") {
		t.Fatalf("messages = %q, want one naming out.txt", got)
	}

	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.PR.Merged = true })
	f.tick()
	if n := f.svc.dirtyWaits(); n != 0 {
		t.Fatalf("waits after the PR merged = %d, want none", n)
	}
}

func TestPruneDirtyForgetsExecutionsNotListed(t *testing.T) {
	s := New(Deps{})
	s.dirtySince["lado-1"] = dirtyWait{head: verifyHead1, since: t0}
	s.dirtySince["lado-2"] = dirtyWait{head: verifyHead2, since: t0}
	s.pruneDirty([]domain.LadoExecution{{SessionID: "lado-2"}})
	if _, ok := s.dirtyWaitOf("lado-1"); ok {
		t.Fatal("lado-1 kept its wait")
	}
	if _, ok := s.dirtyWaitOf("lado-2"); !ok {
		t.Fatal("lado-2 lost its wait")
	}
}
