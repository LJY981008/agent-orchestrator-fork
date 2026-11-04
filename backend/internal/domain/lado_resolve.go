package domain

import (
	"strconv"
	"time"
)

// A resolve worker takes the base into an execution's open PR when the PR
// conflicts with its base, or the base moved under its head with a change to
// the same paths, and the execution's own session no longer runs
// (terminated, or its agent exited). It is a merge worker without a part:
// it starts on its own branch from the PR's head, merges the base, runs the
// checks and pushes to the PR's source branch without force. The PR stays
// the execution's.

// LadoMaxResolveAttempts is how many resolve workers in a row may fail for
// one PR before LADO stops and asks a person (LadoResolveNeedsHuman). A
// resolved PR starts the count again.
const LadoMaxResolveAttempts = 2

// LadoResolveDoneGrace is how long after a resolve worker reported done LADO
// waits for the PR's source branch to show the resolution before it counts
// the attempt as failed.
const LadoResolveDoneGrace = 10 * time.Minute

// LadoResolveState is where resolving an execution's PR stands.
type LadoResolveState string

// Resolve states. None: no resolve worker runs and none gave up. Resolving:
// a resolve worker runs or LADO waits for its push to show. Needs human: the
// workers failed LadoMaxResolveAttempts times in a row, or one asked for a
// person.
const (
	LadoResolveNone       LadoResolveState = ""
	LadoResolveResolving  LadoResolveState = "resolving"
	LadoResolveNeedsHuman LadoResolveState = "needs_human"
)

// LadoExecutionResolve is what an execution read carries of its PR's
// resolve: the state, the failed attempts in a row, the newest resolve
// worker's session and the last failure.
type LadoExecutionResolve struct {
	State     LadoResolveState
	Attempts  int
	SessionID SessionID
	Error     string
}

// LadoPRResolve is LADO's row for resolving one execution's PR
// (lado_pr_resolves).
type LadoPRResolve struct {
	// ExecutionSessionID is the execution whose PR is resolved, TaskID and
	// QueueEntryID its task and entry.
	ExecutionSessionID SessionID
	TaskID             LadoTaskID
	QueueEntryID       LadoQueueEntryID
	State              LadoResolveState
	// Attempts counts the resolve workers that failed in a row.
	Attempts int
	// Spawns counts the resolve workers LADO started for the PR; the next
	// one's client request id and branch take Spawns+1.
	Spawns int
	// SessionID is the newest resolve worker's session, StartHeadSHA the
	// PR's head and BaseTipSHA the remote base branch's commit when it
	// started.
	SessionID    SessionID
	StartHeadSHA string
	BaseTipSHA   string
	// SpawnRequest is the client request id of a resolve worker being
	// started, empty otherwise, which the worker gate grants until
	// SpawnExpiresAt.
	SpawnRequest   string
	SpawnExpiresAt time.Time
	// DoneSeenAt is when LADO saw the worker report done; LadoResolveDoneGrace
	// runs from it.
	DoneSeenAt time.Time
	// NotifiedHeadSHA is the PR head whose new conflict the live resolve
	// worker was told about.
	NotifiedHeadSHA string
	// Error is the last failure, without secrets.
	Error string
	// Version counts the row's writes; an upsert writes only over the
	// version it read.
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Execution is the resolve as an execution read carries it.
func (r LadoPRResolve) Execution() LadoExecutionResolve {
	return LadoExecutionResolve{State: r.State, Attempts: r.Attempts, SessionID: r.SessionID, Error: r.Error}
}

// ladoResolveID is the middle of a resolve worker's client request id. A part
// id starts with a letter, so it is none.
const ladoResolveID = "_resolve"

// LadoResolveClientRequestID is the client request id of a resolve worker
// of an entry's execution: "lado:<entry>/_resolve/<attempt>", where attempt
// is the worker's number for the PR from 1 (LadoPRResolve.Spawns+1).
func LadoResolveClientRequestID(entry LadoQueueEntryID, attempt int) string {
	return LadoRunClientRequestID(LadoRunRequest{Entry: entry, Role: LadoRoleMerge, Attempt: attempt})
}

// LadoResolveBranch is the branch a resolve worker starts on, from the PR's
// head: "lado/<task>/<entry>/_resolve.<attempt>". A part id has no
// underscore, so it is no part's branch.
func LadoResolveBranch(task LadoTaskID, entry LadoQueueEntryID, attempt int) string {
	return ladoRunBranch(task, entry, ladoResolveID+"."+strconv.Itoa(max(attempt, 1)))
}

// LadoSessionLive reports whether a session still works: it was not
// terminated and its agent did not exit. Only a live session takes fix
// requests, and an execution whose session is not live gets a resolve
// worker.
func LadoSessionLive(s SessionRecord) bool {
	return LadoSessionStateLive(s.IsTerminated, s.Activity.State)
}

// LadoSessionStateLive is LadoSessionLive from the two facts it reads.
func LadoSessionStateLive(terminated bool, activity ActivityState) bool {
	return !terminated && activity != ActivityExited
}
