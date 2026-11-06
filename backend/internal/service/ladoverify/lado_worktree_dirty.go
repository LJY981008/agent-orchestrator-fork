package ladoverify

// LADO #22: a worktree at the PR's head that holds changes before the head's
// first check command waits as worktree_dirty, as when a tool wrote hooks
// into an untracked folder of it. When that lasts worktreeDirtyNotifyAfter,
// the worker is told once per head which files keep it from being clean and
// how to clean it. A worker that ended is not told: the session card shows
// the code and the files to the developer.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// worktreeDirtyNotifyAfter is how long a head's commands item waits as
// worktree_dirty before the worker is told.
const worktreeDirtyNotifyAfter = 2 * time.Minute

// dirtyWait is when this daemon first saw a head's commands item wait as
// worktree_dirty. The loop keeps one per execution (passKey), in memory: a
// daemon that restarts measures again, which only delays the message.
type dirtyWait struct {
	head  string
	since time.Time
}

// requestClean keeps the time the head's commands item started to wait as
// worktree_dirty and, once that is worktreeDirtyNotifyAfter ago, asks the
// live worker once per head to clean its worktree, returning the notified
// list with domain.LadoNotifiedWorktreeDirty. An item with another code
// forgets the time. A message that could not be sent goes out on a later
// pass.
func (s *Service) requestClean(ctx context.Context, in headInputs, ev evaluation, notified []string) []string {
	key := passKey(in.exec)
	i := slices.IndexFunc(ev.items, func(item domain.LadoVerifyItem) bool { return item.Kind == domain.LadoVerifyCommands })
	if i < 0 || ev.items[i].Code != domain.LadoCodeWorktreeDirty {
		s.forgetDirty(key)
		return notified
	}
	now := s.now()
	s.mu.Lock()
	wait, ok := s.dirtySince[key]
	if !ok || !strings.EqualFold(wait.head, in.head) {
		wait = dirtyWait{head: in.head, since: now}
		s.dirtySince[key] = wait
	}
	s.mu.Unlock()
	if s.deps.Sender == nil || !in.live || now.Sub(wait.since) < worktreeDirtyNotifyAfter ||
		slices.Contains(notified, domain.LadoNotifiedWorktreeDirty) {
		return notified
	}
	var details domain.LadoCommandsDetails
	if err := json.Unmarshal(ev.items[i].Details, &details); err != nil {
		s.deps.Log.Warn("LADO could not read the files of a worktree that is not clean", "session", in.exec.SessionID, "head", in.head, "err", err)
		return notified
	}
	if err := s.deps.Sender.Send(ctx, in.owner, cleanMessage(in.head, details)); err != nil {
		s.deps.Log.Warn("LADO could not ask a worker to clean its worktree; it tries again", "session", in.owner, "head", in.head, "err", err)
		return notified
	}
	return append(slices.Clone(notified), domain.LadoNotifiedWorktreeDirty)
}

// cleanMessage asks the worker to clean a worktree at head, listing the
// files the item names and how many more there are. It does not suggest
// .git/info/exclude: a linked worktree's .git is a file, and the exclude
// file is shared by the main checkout and every worktree.
func cleanMessage(head string, details domain.LadoCommandsDetails) string {
	message := fmt.Sprintf("LADO checks head %s of your pull request. Your worktree is at that head, but it has changes "+
		"that are not committed, so LADO waits to run the project's check commands there: they must test exactly the pushed commit. "+
		"Commit these files, or delete them; if they are generated, list them in the repository's .gitignore or in a .gitignore "+
		"inside their folder, then commit and push. LADO runs the commands once git status is clean.", shortCommit(head))
	if len(details.Dirty) == 0 {
		return message + "\n\nRun git status in your worktree to see them."
	}
	list := strings.Join(details.Dirty, "\n")
	if more := details.DirtyCount - len(details.Dirty); more > 0 {
		list += fmt.Sprintf("\nand %d more", more)
	}
	return message + "\n\n" + list
}

// forgetDirty forgets when an execution's head started to wait as
// worktree_dirty.
func (s *Service) forgetDirty(key domain.SessionID) {
	s.mu.Lock()
	delete(s.dirtySince, key)
	s.mu.Unlock()
}

// pruneDirty forgets the waits of the executions a complete listing of
// those to verify left out: they settled or are gone.
func (s *Service) pruneDirty(listed []domain.LadoExecution) {
	keep := make(map[domain.SessionID]bool, len(listed))
	for _, exec := range listed {
		keep[passKey(exec)] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.dirtySince {
		if !keep[key] {
			delete(s.dirtySince, key)
		}
	}
}
