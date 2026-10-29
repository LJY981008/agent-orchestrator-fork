package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// LadoQueueEntryID identifies a queue entry.
type LadoQueueEntryID string

// ErrLadoLeaseLost means a queue entry no longer carries the lease token a
// dispatcher claimed it with: another claim took it after the lease expired,
// or the entry was released or marked since.
var ErrLadoLeaseLost = errors.New("the queue entry's lease no longer holds")

// ErrLadoIssueNotFound means the tracker answered that a task's issue does not
// exist: it was deleted, or its repository moved out of reach.
var ErrLadoIssueNotFound = errors.New("the issue no longer exists on the tracker")

// LadoQueueEntry is one plan approval waiting for the dispatcher, and the
// record of how its dispatch went (docs/LJY-PRD/TDD.md §5.3). Approving a
// plan writes the entry in the approval's transaction; a newer approval of
// the task supersedes the task's open entries. Times are zero where the
// column is NULL.
type LadoQueueEntry struct {
	ID     LadoQueueEntryID
	TaskID LadoTaskID
	// ProjectID is the task's project, copied so dispatch filters without a
	// join.
	ProjectID        ProjectID
	PlanApprovalID   LadoPlanApprovalID
	PolicySnapshotID LadoPolicySnapshotID
	EnqueuedAt       time.Time
	// AttemptCount counts the claims that tried to dispatch the entry. A hold
	// gives its claim back: waiting for a slot, for memory or for the issue
	// is not an attempt.
	AttemptCount int
	// NextAttemptAt keeps the entry waiting until then.
	NextAttemptAt time.Time
	// LeaseToken, ClaimedAt and LeaseExpiresAt are the claim of the
	// dispatcher working on the entry, set and cleared together. A lease that
	// expired no longer holds the entry.
	LeaseToken     string
	ClaimedAt      time.Time
	LeaseExpiresAt time.Time
	// WaitingReason says why an open entry waits (LadoWaiting*).
	WaitingReason string
	// ReviewRequiredReason (LadoReview*) and BlockedReason (LadoBlocked*)
	// stop the entry: the task needs a new approval.
	ReviewRequiredReason string
	BlockedReason        string
	// ErrorMessage is the last attempt's error, without secrets.
	ErrorMessage string
	// DispatchedSessionID is the worker session the entry started.
	DispatchedSessionID SessionID
	// FailedAt is when the entry ran out of attempts.
	FailedAt     time.Time
	SupersededAt time.Time
	CancelledAt  time.Time
}

// LadoQueueItem is one row of the queue view: an open entry with its task's
// issue.
type LadoQueueItem struct {
	Entry    LadoQueueEntry
	IssueRef IssueID
	Title    string
}

// LadoQueueState is a queue entry's derived state. It is computed at read
// time and never stored.
type LadoQueueState string

// Queue entry states, in the order State tests them.
const (
	LadoQueueSuperseded     LadoQueueState = "superseded"
	LadoQueueCancelled      LadoQueueState = "cancelled"
	LadoQueueDispatched     LadoQueueState = "dispatched"
	LadoQueueFailed         LadoQueueState = "failed"
	LadoQueueReviewRequired LadoQueueState = "review_required"
	LadoQueueBlocked        LadoQueueState = "blocked"
	LadoQueueDispatching    LadoQueueState = "dispatching"
	LadoQueueWaiting        LadoQueueState = "waiting"
	LadoQueueQueued         LadoQueueState = "queued"
)

// State derives the entry's state at now. A superseded or cancelled entry is
// over whatever else it records; then come the outcomes of its dispatch and
// the marks that stop it. An open entry is Dispatching while a lease holds
// it, Waiting while it has a reason to wait or its next attempt is not due,
// and Queued otherwise. An expired lease is not Dispatching: the entry can be
// claimed again.
func (e LadoQueueEntry) State(now time.Time) LadoQueueState {
	switch {
	case !e.SupersededAt.IsZero():
		return LadoQueueSuperseded
	case !e.CancelledAt.IsZero():
		return LadoQueueCancelled
	case e.DispatchedSessionID != "":
		return LadoQueueDispatched
	case !e.FailedAt.IsZero():
		return LadoQueueFailed
	case e.ReviewRequiredReason != "":
		return LadoQueueReviewRequired
	case e.BlockedReason != "":
		return LadoQueueBlocked
	case e.LeaseToken != "" && e.LeaseExpiresAt.After(now):
		return LadoQueueDispatching
	case e.WaitingReason != "" || e.NextAttemptAt.After(now):
		return LadoQueueWaiting
	default:
		return LadoQueueQueued
	}
}

// Reasons behind the states of queue entries and tasks. They are stable
// strings, sent to the renderer, which shows them in the developer's
// language.
//
// Review required: the task needs a new approval.
const (
	// LadoReviewRulesChanged means the rules no longer give the approval's
	// snapshot.
	LadoReviewRulesChanged = "rules_changed"
	// LadoReviewIssueEdited means the issue changed since the plan was made
	// from it.
	LadoReviewIssueEdited = "issue_edited"
	// LadoReviewPlanSuperseded means the entry's approval no longer covers
	// the task's latest revision, or a newer approval replaced it.
	LadoReviewPlanSuperseded = "plan_superseded"
	// LadoReviewApprovedBeforeQueue means the valid approval has no queue
	// entry that can still run: it was given before LADO had a queue, or its
	// entry was cancelled while the issue was closed. Approving again queues
	// the task.
	LadoReviewApprovedBeforeQueue = "approved_before_queue"
)

// Blocked: the plan or the setup has to change before the task can run.
const (
	// LadoBlockedSnapshot means the approval's rule snapshot holds a blocking
	// problem.
	LadoBlockedSnapshot = "blocking_snapshot"
	// LadoBlockedBaseRefMismatch means the plan's base_ref is not the branch
	// AO starts the project's workers from.
	LadoBlockedBaseRefMismatch = "base_ref_mismatch"
	// LadoBlockedAgentUnsupported means the snapshot's agent cannot run an
	// execution.
	LadoBlockedAgentUnsupported = "agent_unsupported"
)

// Waiting: why an open entry is not dispatched yet.
const (
	// LadoWaitingSlotGlobal means as many executions run as the dispatch
	// settings allow.
	LadoWaitingSlotGlobal = "slot_global"
	// LadoWaitingSlotProject means as many executions run in the project as
	// its cap allows.
	LadoWaitingSlotProject = "slot_project"
	// LadoWaitingMemoryTight means memory pressure is tight and the dispatch
	// settings hold entries then.
	LadoWaitingMemoryTight = "memory_tight"
	// LadoWaitingIssueUnreadable means the issue could not be read; LADO
	// reads it again at NextAttemptAt.
	LadoWaitingIssueUnreadable = "issue_unreadable"
	// LadoWaitingRetry means the next attempt waits for NextAttemptAt, after
	// an attempt failed or a check could not run.
	LadoWaitingRetry = "retry"
	// LadoWaitingModeOff means the project's LADO mode is off.
	LadoWaitingModeOff = "lado_mode_off"
	// LadoWaitingRunLive means the task's worker of an earlier approval
	// still runs; a task runs one worker at a time.
	LadoWaitingRunLive = "run_live"
)

// Failed: why a task's dispatch or execution failed.
const (
	// LadoFailedAttemptsExhausted means every attempt to start the worker
	// failed.
	LadoFailedAttemptsExhausted = "attempts_exhausted"
	// LadoFailedTimedOut means LADO stopped the execution at its deadline.
	LadoFailedTimedOut = "timed_out"
	// LadoFailedEndedWithoutPR means the worker session ended without opening
	// a PR.
	LadoFailedEndedWithoutPR = "ended_without_pr"
)

// LadoExecution is one dispatched run of a task: the AO worker session a
// queue entry started (docs/LJY-PRD/TDD.md §3.1, §5.3). AO's session row
// stays as it is, and the link lives next to it.
type LadoExecution struct {
	SessionID        SessionID
	TaskID           LadoTaskID
	QueueEntryID     LadoQueueEntryID
	PlanRevisionID   LadoPlanRevisionID
	PlanApprovalID   LadoPlanApprovalID
	PolicySnapshotID LadoPolicySnapshotID
	// Attempt is the entry's attempt that started the session, from 1.
	Attempt int
	// PlanBaseSHA is the commit the plan's base ref named when LADO pinned
	// the plan, DispatchBaseSHA the one it named at dispatch, and
	// BaseCommitsAhead how many commits lie between them, nil when unknown. A
	// moved base is recorded, not refused.
	PlanBaseSHA      string
	DispatchBaseSHA  string
	BaseCommitsAhead *int
	// BaseBranch and BaseRemote are the base branch and its remote the
	// dispatcher resolved, and DispatchLocalBaseSHA the commit of the local
	// branch then, empty without one (docs/LJY-PRD/TDD.md §7.3).
	BaseBranch           string
	BaseRemote           string
	DispatchLocalBaseSHA string
	StartedAt            time.Time
	// DeadlineAt is when LADO stops the run, zero without a limit; TimedOutAt
	// is when it did.
	DeadlineAt time.Time
	TimedOutAt time.Time
	// SettledAt is when verification settled the execution's merged or
	// closed PR, zero while it still verifies.
	SettledAt time.Time
	// IntegrationGated says that LADO dispatched the execution while it
	// gated integrations (build step 8 on): only then does a merge without
	// an integration approval count as one. Executions dispatched before
	// had no such gate.
	IntegrationGated bool
	// SessionTerminated and PR are read with the execution from AO's session
	// and PR rows.
	SessionTerminated bool
	PR                LadoExecutionPR
	// Verification and Integration are read with the execution from LADO's
	// verification records and integration approvals.
	Verification LadoExecutionVerification
	Integration  LadoExecutionIntegration
}

// LadoExecutionPR is the newest PR of an execution's session, zero while the
// session has none. HeadSHA is the PR's head as AO last observed it, and
// TargetBranch the branch it merges into.
type LadoExecutionPR struct {
	URL          string
	Number       int
	Merged       bool
	Closed       bool
	HeadSHA      string
	TargetBranch string
	Title        string
}

// LadoExecutionVerification is the verification record of an execution's PR
// at its current head: the head the record is for and its overall outcome
// and reason (LadoVerification.Overall). It is zero while that head has no
// record.
type LadoExecutionVerification struct {
	HeadSHA string
	Outcome LadoVerifyOutcome
	Reason  string
}

// LadoExecutionIntegration is what an execution's integration approvals say:
// whether the PR's current head has one and how posting its commit status
// went, and, once the PR merged, whether the merged head had one.
type LadoExecutionIntegration struct {
	HeadApproved       bool
	StatusState        LadoIntegrationStatusState
	MergedHeadApproved bool
}

// LadoDispatchSettings are the dispatcher's settings for every project
// (docs/LJY-PRD/TDD.md §5.3). Without a stored row the defaults apply.
type LadoDispatchSettings struct {
	// MaxConcurrent caps the executions that run at once across projects.
	// Each project has its own cap as well.
	MaxConcurrent int
	// MaxRunMinutes is how long an execution may run before LADO stops it,
	// one of LadoRunMinuteChoices; 0 means no limit.
	MaxRunMinutes int
	// HoldOnMemoryTight keeps entries waiting while memory pressure is tight,
	// and plan requests too.
	HoldOnMemoryTight bool
	// MaxPlanWorkers caps the plan requests whose workers run at once across
	// projects, apart from the executions' caps; a designer and its critic
	// count as one (docs/LJY-PRD/TDD.md §5.1).
	MaxPlanWorkers int
	// PlanReview decides when a draft's review is skipped.
	PlanReview LadoPlanReviewSettings
	UpdatedAt  time.Time
}

// LadoMaxConcurrent is the highest cap on running executions, overall and per
// project. The lowest is 1.
const LadoMaxConcurrent = 8

// LadoDefaultProjectMaxConcurrent is a project's cap on running executions
// until it sets one.
const LadoDefaultProjectMaxConcurrent = 1

// LadoRunMinuteChoices are the maximum run times an execution may have, in
// minutes: no limit, or 1, 2, 4, 8, 12 or 24 hours.
var LadoRunMinuteChoices = []int{0, 60, 120, 240, 480, 720, 1440}

// ErrLadoDispatchSettingsInvalid means dispatch settings are out of range.
// The services report it as LADO_DISPATCH_SETTINGS_INVALID.
var ErrLadoDispatchSettingsInvalid = errors.New("invalid dispatch settings")

// DefaultLadoDispatchSettings are the settings without a stored row: two
// executions at once, up to eight hours each, held while memory is tight; two
// plan requests at once, and no review of a small plan.
func DefaultLadoDispatchSettings() LadoDispatchSettings {
	return LadoDispatchSettings{MaxConcurrent: 2, MaxRunMinutes: 480, HoldOnMemoryTight: true,
		MaxPlanWorkers: LadoDefaultMaxPlanWorkers, PlanReview: DefaultLadoPlanReviewSettings()}
}

// LadoDefaultMaxPlanWorkers is how many plan requests run at once until the
// developer sets another number, from 1 to LadoMaxConcurrent.
const LadoDefaultMaxPlanWorkers = 2

// Validate refuses a cap outside 1 to LadoMaxConcurrent and a run time that
// is not one of LadoRunMinuteChoices, for executions and plan requests alike,
// and review skip settings out of range.
func (s LadoDispatchSettings) Validate() error {
	if s.MaxConcurrent < 1 || s.MaxConcurrent > LadoMaxConcurrent {
		return fmt.Errorf("%w: maxConcurrent must be 1 to %d, got %d", ErrLadoDispatchSettingsInvalid, LadoMaxConcurrent, s.MaxConcurrent)
	}
	if !slices.Contains(LadoRunMinuteChoices, s.MaxRunMinutes) {
		return fmt.Errorf("%w: maxRunMinutes must be one of %v, got %d", ErrLadoDispatchSettingsInvalid, LadoRunMinuteChoices, s.MaxRunMinutes)
	}
	if s.MaxPlanWorkers < 1 || s.MaxPlanWorkers > LadoMaxConcurrent {
		return fmt.Errorf("%w: maxPlanWorkers must be 1 to %d, got %d", ErrLadoDispatchSettingsInvalid, LadoMaxConcurrent, s.MaxPlanWorkers)
	}
	if err := s.PlanReview.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrLadoDispatchSettingsInvalid, err)
	}
	return nil
}

// LadoDispatchGrant is what the worker gate checks before it lets the
// dispatcher spawn a worker for a queue entry: the entry's project, the
// dispatch hash of its approval, and whether a lease holds the entry right
// now for the attempt asked about.
type LadoDispatchGrant struct {
	ProjectID ProjectID
	Hash      string
	Active    bool
}

// LadoEntryPolicy is what an execution keeps from the rule snapshot of its
// queue entry for as long as it runs: the permission ceiling its turns may not
// rise above, and the rules text for the worker's system prompt (empty when
// no rule adds any).
type LadoEntryPolicy struct {
	Ceiling PermissionMode
	Prompt  string
}

// ladoClientRequestPrefix starts the client request id of every worker the
// dispatcher spawns.
const ladoClientRequestPrefix = "lado:"

// LadoClientRequestID is the client request id of one dispatch attempt,
// "lado:<entry>/<attempt>". AO keeps a request id that failed unusable, so
// each attempt has its own; after a crash, the same id finds the session AO
// already made for the attempt.
func LadoClientRequestID(entry LadoQueueEntryID, attempt int) string {
	return ladoClientRequestPrefix + string(entry) + "/" + strconv.Itoa(attempt)
}

// ParseLadoClientRequestID reads back an id LadoClientRequestID made. It
// accepts only that exact form: the prefix, an entry id, one slash, and the
// attempt in decimal from 1 without a sign or leading zeros.
func ParseLadoClientRequestID(s string) (LadoQueueEntryID, int, bool) {
	rest, ok := strings.CutPrefix(s, ladoClientRequestPrefix)
	if !ok {
		return "", 0, false
	}
	// A second slash stays in number, which then does not parse.
	entry, number, ok := strings.Cut(rest, "/")
	attempt, err := strconv.Atoi(number)
	if !ok || entry == "" || err != nil || attempt < 1 || strconv.Itoa(attempt) != number {
		return "", 0, false
	}
	return LadoQueueEntryID(entry), attempt, true
}

// LadoDispatchHash is the client request hash of a dispatch: the hex sha256
// of the approved plan's sha256 and the rule snapshot's, joined by a newline.
// The worker gate grants a spawn only with the hash of the entry's approval.
func LadoDispatchHash(planSHA256, snapshotSHA256 string) string {
	sum := sha256.Sum256([]byte(planSHA256 + "\n" + snapshotSHA256))
	return hex.EncodeToString(sum[:])
}
