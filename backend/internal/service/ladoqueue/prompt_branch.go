package ladoqueue

import (
	"fmt"
	"strings"
)

// sessionBranch says which branch a worker commits on, pushes and opens its
// pull requests from (#71). AO ties a pull request to a session only when
// its source branch is the session's branch, so one opened from a branch the
// worker made stays untracked and the task never leaves running.
type sessionBranch struct {
	// name is the branch LADO made for the worker, as an integration
	// worker's run.Branch. "" has the worker read the branch it starts on,
	// which AO named when it spawned the worker.
	name string
	// workspace says the worker works in a workspace project: each child
	// repository folder has its own checkout of the session branch.
	workspace bool
	// multiRepo says a named branch is in several repositories, as in an
	// integration run in several repositories (#64).
	multiRepo bool
}

// branchPrecedence keeps a LADO worker on its session branch where AO's
// general worker instructions suggest a sibling branch for a pull request.
const branchPrecedence = "Do not make a new branch or switch to another one, even where AO's general instructions suggest a sibling branch for a pull request: in a LADO run these instructions come first. "

// sessionBranchParagraph tells a worker to commit, push and open its pull
// requests from its session branch and from no other. It comes right before
// changeRequestParagraph, whose push uses the same branch. It ends with a
// line break.
func sessionBranchParagraph(sb sessionBranch) string {
	var b strings.Builder
	switch {
	case sb.name != "":
		lead := "Commit, push"
		if sb.multiRepo {
			lead = "In each repository, commit, push"
		}
		fmt.Fprintf(&b, "%s and open the pull request from the branch `%s` only. ", lead, sb.name)
		b.WriteString(branchPrecedence)
		fmt.Fprintf(&b, "LADO tracks only a pull request opened from `%s`; one opened from another branch stays untracked, and the task does not move on.\n", sb.name)
	case sb.workspace:
		b.WriteString("This is a workspace project: each repository folder has its own session branch, the branch it is on when you start. ")
		b.WriteString("Before you change anything in a repository, read its session branch with `git -C <folder> branch --show-current`. ")
		b.WriteString("Commit on that branch there, push it under the same name (`git -C <folder> push -u origin <that branch>`; on GitLab, the push with the merge request options below is that push) and open that repository's pull request from it. ")
		b.WriteString(branchPrecedence)
		b.WriteString("LADO tracks only a pull request opened from a session branch; one opened from another branch stays untracked, and the task stays running.\n")
	default:
		b.WriteString("Your session branch is the branch your worktree is on when you start: read it with `git branch --show-current` before you change anything. ")
		b.WriteString("Commit on that branch, push it under the same name (`git push -u origin <that branch>`; on GitLab, the push with the merge request options below is that push) and open the pull request from it. ")
		b.WriteString(branchPrecedence)
		b.WriteString("LADO tracks only a pull request opened from the session branch; one opened from another branch stays untracked, and the task stays running.\n")
	}
	return b.String()
}
