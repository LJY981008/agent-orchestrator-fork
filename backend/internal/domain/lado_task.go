package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// LadoTaskID identifies a LADO task.
type LadoTaskID string

// ErrLadoIssueHasTask means the issue already belongs to a task that is not
// cancelled. One issue has at most one live task (docs/LJY-PRD/TDD.md §3.1).
var ErrLadoIssueHasTask = errors.New("the issue already has a LADO task")

// LadoTask is LADO's record of one issue it works on (docs/LJY-PRD/TDD.md
// §3.1). The issue on the project's repository is the work item; the task
// keeps the facts LADO last observed about it, and the tracker stays the
// owner of the issue's text and state.
type LadoTask struct {
	ID        LadoTaskID
	ProjectID ProjectID
	// Repo is the repository whose tracker holds the issue. A workspace
	// project has several; the task records which one, also while LADO is
	// still creating the issue.
	Repo TrackerRepo
	// IssueRef is the canonical issue id. It is empty only while LADO creates
	// the issue, and such a task is never shown.
	IssueRef IssueID
	// Agent and Model are the developer's choices for the task's execution.
	Agent       string
	Model       string
	Issue       LadoObservedIssue
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CancelledAt time.Time
	// Plan holds the facts about the task's plans that its state derives
	// from. Store reads that list or get tasks fill it.
	Plan LadoTaskPlan
	// Queue is the queue entry of the newest approval, zero when it has none.
	Queue LadoQueueEntry
	// Execution is the task's newest execution, zero when it has none.
	Execution LadoExecution
}

// LadoTaskPlan is what a task's state needs to know about its plans: the
// newest plan request, the newest revision, the revision the newest approval
// covers, and the rules of that approval then and now.
type LadoTaskPlan struct {
	Request LadoPlanRequest
	Latest  LadoPlanRevision
	// Approval is the newest approval of any of the task's revisions. It is
	// valid only while it covers the latest revision with that revision's
	// sha256 (docs/LJY-PRD/TDD.md §3.2).
	Approval LadoPlanApproval
	// Snapshot is the rule snapshot of the newest approval. It is empty for
	// an approval given before LADO had rules.
	Snapshot LadoPolicySnapshotRef
	// PolicySHA256 is the snapshot hash the rules give the approved revision
	// now. The task service fills it; empty means it was not computed.
	PolicySHA256 string
}

// RulesChanged reports whether the rules now differ from the ones the newest
// approval fixed. Without a current hash it reports no change.
func (p LadoTaskPlan) RulesChanged() bool {
	return p.PolicySHA256 != "" && p.Snapshot.SHA256 != p.PolicySHA256
}

// ApprovalValid reports whether the newest approval covers the latest
// revision, byte for byte.
func (p LadoTaskPlan) ApprovalValid() bool {
	return p.Latest.ID != "" && p.Approval.RevisionID == p.Latest.ID && p.Approval.SHA256 == p.Latest.SHA256
}

// RequestPending reports whether the newest plan request still waits for a
// revision: no revision came after it and LADO has not given up delivering it.
func (p LadoTaskPlan) RequestPending() bool {
	if p.Request.ID == "" || !p.Request.FailedAt.IsZero() {
		return false
	}
	return p.Latest.ID == "" || p.Latest.RequestID != p.Request.ID
}

// LadoObservedIssue is what LADO last read from a task's issue.
type LadoObservedIssue struct {
	Title string
	State NormalizedIssueState
	URL   string
	// SnapshotSHA256 is the sha256 of the issue's canonical snapshot.
	SnapshotSHA256 string
	ObservedAt     time.Time
}

// LadoTaskState is a task's derived state (docs/LJY-PRD/TDD.md §3.2). It is
// computed at read time and never stored.
type LadoTaskState string

// Task states (docs/LJY-PRD/TDD.md §3.2).
const (
	LadoTaskDraft          LadoTaskState = "draft"
	LadoTaskPlanned        LadoTaskState = "planned"
	LadoTaskReviewRequired LadoTaskState = "review_required"
	LadoTaskBlocked        LadoTaskState = "blocked"
	// LadoTaskApproved stays for the API's state enum, but State no longer
	// returns it: a valid approval now has its queue entry, so the task is
	// Queued or later, and an approval given before the queue existed asks to
	// be approved again (LadoReviewApprovedBeforeQueue).
	LadoTaskApproved    LadoTaskState = "approved"
	LadoTaskQueued      LadoTaskState = "queued"
	LadoTaskDispatching LadoTaskState = "dispatching"
	LadoTaskRunning     LadoTaskState = "running"
	// LadoTaskValidating, LadoTaskIntegrationPending and LadoTaskIntegrating
	// follow an execution's open PR: its current head is being verified,
	// verified and waiting for the integration approval, or approved.
	LadoTaskValidating         LadoTaskState = "validating"
	LadoTaskIntegrationPending LadoTaskState = "integration_pending"
	LadoTaskIntegrating        LadoTaskState = "integrating"
	LadoTaskFailed             LadoTaskState = "failed"
	LadoTaskCompleted          LadoTaskState = "completed"
	LadoTaskCancelled          LadoTaskState = "cancelled"
	LadoTaskClosedUnmerged     LadoTaskState = "closed_unmerged"
)

// Reasons of the states after a PR. A Validating task's reason is otherwise
// the code of the verification item that fails or waits first (LadoCode*).
const (
	// LadoValidatingVerifying means the PR's current head has no
	// verification record yet.
	LadoValidatingVerifying = "verifying"
	// LadoIntegratingStatusNotPosted means LADO could not post the
	// lado/integration commit status on the approved head; it tries again.
	LadoIntegratingStatusNotPosted = "status_not_posted"
	// LadoCompletedWithoutApproval means the PR merged a head that had no
	// integration approval.
	LadoCompletedWithoutApproval = "merged_without_approval"
)

// State derives the task's state now, as StateAt does.
func (t LadoTask) State() LadoTaskState {
	return t.StateAt(time.Now())
}

// StateAt derives the task's state at now. A cancelled task is Cancelled.
// After dispatch its execution decides: Completed once the PR merged, Closed
// unmerged once the PR closed without a merge, Failed when the run timed out
// or its session ended without a PR, and Running while it has no PR. With
// its PR open, the PR's current head decides: Integrating once it has an
// integration approval, Integration pending once its verification record
// passes or needs confirming, and Validating while it has no record or one
// that waits or fails. A failed run leaves a task whose issue is closed
// Closed unmerged, and a run of an older approval no longer counts once it
// ended without a merge: it still does while it runs, is verified or is
// integrated. Without an execution, a closed issue makes the task Closed
// unmerged. A valid approval needs review when the rules changed since it or
// its queue entry was marked so, and is Blocked when its snapshot holds a
// blocking problem or the entry was blocked; review wins over Blocked. An
// entry out of attempts is Failed, one a lease holds is Dispatching, and one
// that waits is Queued. A valid approval without an entry that can still run
// needs review too: approving again queues the task. A task whose latest
// revision has no valid approval is Planned, and one without a revision a
// Draft.
func (t LadoTask) StateAt(now time.Time) LadoTaskState {
	state, _ := t.stateAt(now)
	return state
}

// StateReason returns the reason behind the task's state now: what needs
// review (LadoReview*), what blocks it (LadoBlocked*), why it failed
// (LadoFailed*), why its queue entry waits (LadoWaiting*), what its PR's
// head waits for or fails on (LadoValidatingVerifying or a LadoCode*), that
// the approved head's commit status is not posted
// (LadoIntegratingStatusNotPosted), or that the merged head had no
// integration approval (LadoCompletedWithoutApproval). Other states have
// none.
func (t LadoTask) StateReason() string {
	_, reason := t.stateAt(time.Now())
	return reason
}

func (t LadoTask) stateAt(now time.Time) (LadoTaskState, string) {
	if !t.CancelledAt.IsZero() {
		return LadoTaskCancelled, ""
	}
	closed := t.Issue.State == IssueDone || t.Issue.State == IssueCancelled
	if e := t.Execution; e.SessionID != "" {
		state, reason := e.taskState()
		// A run of an older approval that ended without a merge is history:
		// a task approved again after a failed run shows its new entry.
		current := e.PlanApprovalID == t.Plan.Approval.ID
		switch state {
		case LadoTaskRunning, LadoTaskValidating, LadoTaskIntegrationPending, LadoTaskIntegrating, LadoTaskCompleted:
			current = true
		}
		if current && (state != LadoTaskFailed || !closed) {
			return state, reason
		}
	}
	if closed {
		return LadoTaskClosedUnmerged, ""
	}
	// The approval's facts, its queue entry among them, count only while it
	// covers the latest revision.
	if !t.Plan.ApprovalValid() {
		if t.Plan.Latest.ID != "" {
			return LadoTaskPlanned, ""
		}
		return LadoTaskDraft, ""
	}
	// Task reads carry the newest approval's entry; another one is not this
	// approval's.
	var entryState LadoQueueState
	if t.Queue.ID != "" && t.Queue.PlanApprovalID == t.Plan.Approval.ID {
		entryState = t.Queue.State(now)
	}
	switch {
	case t.Plan.RulesChanged():
		return LadoTaskReviewRequired, LadoReviewRulesChanged
	case entryState == LadoQueueReviewRequired:
		return LadoTaskReviewRequired, t.Queue.ReviewRequiredReason
	case t.Plan.Snapshot.Blocking:
		return LadoTaskBlocked, LadoBlockedSnapshot
	case entryState == LadoQueueBlocked:
		return LadoTaskBlocked, t.Queue.BlockedReason
	case entryState == LadoQueueFailed:
		return LadoTaskFailed, LadoFailedAttemptsExhausted
	case entryState == LadoQueueDispatching:
		return LadoTaskDispatching, ""
	case entryState == LadoQueueQueued || entryState == LadoQueueWaiting:
		return LadoTaskQueued, t.Queue.WaitingReason
	case entryState == LadoQueueDispatched:
		// Task reads carry a dispatched entry's execution; without it, the
		// entry still says a session runs.
		return LadoTaskRunning, ""
	default:
		// No entry, or one that was superseded or cancelled.
		return LadoTaskReviewRequired, LadoReviewApprovedBeforeQueue
	}
}

// taskState is the state an execution gives its task. A PR decides once
// there is one: a run stopped at its deadline while its PR opened still has
// the PR to review, as the run time limit stops once a PR is open. A merge
// without an integration approval counts only for an execution dispatched
// under integration approval.
func (e LadoExecution) taskState() (LadoTaskState, string) {
	switch {
	case e.PR.Merged && e.IntegrationGated && !e.Integration.MergedHeadApproved:
		return LadoTaskCompleted, LadoCompletedWithoutApproval
	case e.PR.Merged:
		return LadoTaskCompleted, ""
	case e.PR.Closed:
		return LadoTaskClosedUnmerged, ""
	case e.PR.URL == "" && !e.TimedOutAt.IsZero():
		return LadoTaskFailed, LadoFailedTimedOut
	case e.PR.URL == "" && e.SessionTerminated:
		return LadoTaskFailed, LadoFailedEndedWithoutPR
	case e.PR.URL == "":
		return LadoTaskRunning, ""
	}
	// The PR is open: its current head decides. A record of another head
	// says nothing about this one.
	v := e.Verification
	switch {
	case e.Integration.HeadApproved && e.Integration.StatusState == LadoIntegrationStatusFailed:
		return LadoTaskIntegrating, LadoIntegratingStatusNotPosted
	case e.Integration.HeadApproved:
		return LadoTaskIntegrating, ""
	case v.HeadSHA == "" || v.HeadSHA != e.PR.HeadSHA:
		return LadoTaskValidating, LadoValidatingVerifying
	case v.Outcome == LadoVerifyPass || v.Outcome == LadoVerifyConfirm:
		return LadoTaskIntegrationPending, ""
	case v.Reason != "" && (v.Outcome == LadoVerifyWait || v.Outcome == LadoVerifyFail):
		// A record that waits or fails names its first such item.
		return LadoTaskValidating, v.Reason
	default:
		return LadoTaskValidating, LadoValidatingVerifying
	}
}

// LadoTaskMarker is the hidden marker LADO appends to the body of an issue it
// creates for a task. After an interrupted request, LADO finds the issue by
// its marker (docs/LJY-PRD/TDD.md §5.1).
func LadoTaskMarker(id LadoTaskID) string {
	return "<!-- lado-task:" + string(id) + " -->"
}

// SplitLadoTaskText splits New Task composer text into an issue title, the
// first non-blank line, and a body, the lines after it.
func SplitLadoTaskText(text string) (title, body string) {
	text = strings.TrimLeft(strings.ReplaceAll(text, "\r\n", "\n"), " \t\n")
	first, rest, _ := strings.Cut(text, "\n")
	lines := strings.Split(rest, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	return strings.TrimSpace(first), strings.TrimRight(strings.Join(lines, "\n"), " \t\n")
}

// LadoIssueSnapshot is an issue as LADO pins it: its canonical id, title and
// body (docs/LJY-PRD/TDD.md §5.1). A plan request also pins the comments a
// plan may draw on (NewLadoIssueSnapshot). Its hash shows whether an issue
// changed.
type LadoIssueSnapshot struct {
	ID    IssueID `json:"id"`
	Title string  `json:"title"`
	Body  string  `json:"body"`
	// Comments and OmittedComments are left out of the JSON when empty, so a
	// snapshot without comments keeps the hash it had before comments were
	// pinned.
	Comments        []LadoSnapshotComment `json:"comments,omitempty"`
	OmittedComments int                   `json:"omitted_comments,omitempty"`
}

// CanonicalJSON is the snapshot's stored form: the fields in declaration
// order, with HTML characters left as they are. Changing it changes every
// snapshot hash.
func (s LadoIssueSnapshot) CanonicalJSON() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Encoding strings, an int and a slice of string structs cannot fail.
	_ = enc.Encode(s)
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// SHA256 returns the hex sha256 of the canonical JSON.
func (s LadoIssueSnapshot) SHA256() string {
	sum := sha256.Sum256(s.CanonicalJSON())
	return hex.EncodeToString(sum[:])
}

// LadoIssueRef returns an issue's canonical id, in the form AO stores in
// sessions.issue_id: "github:owner/repo#N", "gitlab:group/project#N", plus
// "@host" for self-managed GitLab.
func LadoIssueRef(id TrackerID) IssueID {
	native := strings.TrimSpace(id.Native)
	if native == "" {
		return ""
	}
	provider := id.Provider
	if provider == "" {
		provider = TrackerProviderGitHub
	}
	ref := string(provider) + ":" + native
	if host := strings.TrimSpace(id.Host); host != "" {
		ref += "@" + host
	}
	return IssueID(ref)
}

// ParseLadoIssueRef parses a canonical issue id back into a tracker id. It
// accepts only the exact form LadoIssueRef produces.
func ParseLadoIssueRef(ref IssueID) (TrackerID, error) {
	provider, repo, host, number, err := parseLadoRef(string(ref), true)
	if err != nil {
		return TrackerID{}, fmt.Errorf("invalid issue ref %q: %w", string(ref), err)
	}
	return TrackerID{Provider: provider, Native: repo + "#" + number, Host: host}, nil
}

// LadoRepoRef returns a repository's canonical id: the canonical issue id
// form without the issue number, such as "github:owner/repo" or
// "gitlab:group/project@host".
func LadoRepoRef(repo TrackerRepo) string {
	native := strings.TrimSpace(repo.Native)
	if native == "" {
		return ""
	}
	provider := repo.Provider
	if provider == "" {
		provider = TrackerProviderGitHub
	}
	ref := string(provider) + ":" + native
	if host := strings.TrimSpace(repo.Host); host != "" {
		ref += "@" + host
	}
	return ref
}

// ParseLadoRepoRef parses a canonical repository id back. It accepts only
// the exact form LadoRepoRef produces.
func ParseLadoRepoRef(ref string) (TrackerRepo, error) {
	provider, repo, host, _, err := parseLadoRef(ref, false)
	if err != nil {
		return TrackerRepo{}, fmt.Errorf("invalid repository ref %q: %w", ref, err)
	}
	return TrackerRepo{Provider: provider, Native: repo, Host: host}, nil
}

// LadoIssueRepo returns the repository an issue belongs to.
func LadoIssueRepo(id TrackerID) TrackerRepo {
	native := id.Native
	if hash := strings.LastIndexByte(native, '#'); hash >= 0 {
		native = native[:hash]
	}
	return TrackerRepo{Provider: id.Provider, Native: native, Host: id.Host}
}

// parseLadoRef reads "provider:path[#number][@host]". withNumber says
// whether the "#number" part is required (issue refs) or refused
// (repository refs).
func parseLadoRef(raw string, withNumber bool) (provider TrackerProvider, repo, host, number string, err error) {
	name, rest, ok := strings.Cut(raw, ":")
	if !ok || strings.TrimSpace(raw) != raw {
		return "", "", "", "", errors.New("want provider:path")
	}
	provider = TrackerProvider(name)
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		rest, host = rest[:at], rest[at+1:]
		if host == "" || provider != TrackerProviderGitLab {
			return "", "", "", "", errors.New("only self-managed GitLab refs name a host")
		}
	}
	repo = rest
	if hash := strings.LastIndexByte(rest, '#'); hash >= 0 {
		if !withNumber {
			return "", "", "", "", errors.New("a repository ref has no #number")
		}
		repo, number = rest[:hash], rest[hash+1:]
		if n, convErr := strconv.Atoi(number); convErr != nil || n <= 0 || strconv.Itoa(n) != number {
			return "", "", "", "", errors.New("bad issue number")
		}
	} else if withNumber {
		return "", "", "", "", errors.New("missing #number")
	}
	segments := strings.Split(repo, "/")
	switch provider {
	case TrackerProviderGitHub:
		if len(segments) != 2 {
			return "", "", "", "", errors.New("want owner/repo")
		}
	case TrackerProviderGitLab:
		if len(segments) < 2 {
			return "", "", "", "", errors.New("want group/project")
		}
	default:
		return "", "", "", "", errors.New("unknown provider")
	}
	for _, segment := range segments {
		if segment == "" || strings.ContainsAny(segment, "#@ \t\r\n") {
			return "", "", "", "", errors.New("bad path segment")
		}
	}
	return provider, repo, host, number, nil
}

// LadoIssueNumber returns the issue number of a tracker id, or 0 when the id
// has none.
func LadoIssueNumber(id TrackerID) int {
	hash := strings.LastIndexByte(id.Native, '#')
	if hash < 0 {
		return 0
	}
	n, err := strconv.Atoi(id.Native[hash+1:])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// LadoTrackerRepoFromOrigin returns the tracker repository a project's origin
// names. LADO supports github.com, gitlab.com and self-managed GitLab; GitHub
// Enterprise is out because AO's GitHub API base is fixed to api.github.com.
// The host drops any port, because an SSH port says nothing about the API.
func LadoTrackerRepoFromOrigin(origin string) (TrackerRepo, error) {
	identity, err := ParseRepositoryIdentity(origin)
	if err != nil {
		return TrackerRepo{}, err
	}
	host := identity.Host
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	host = strings.ToLower(host)
	native := identity.Namespace + "/" + identity.Name
	if RepositoryProvider(host) == "github" {
		if host != "github.com" && host != "www.github.com" {
			return TrackerRepo{}, fmt.Errorf("repository host %s: LADO supports GitHub on github.com only", host)
		}
		return TrackerRepo{Provider: TrackerProviderGitHub, Native: native}, nil
	}
	if host == "gitlab.com" || host == "www.gitlab.com" {
		host = ""
	}
	return TrackerRepo{Provider: TrackerProviderGitLab, Native: native, Host: host}, nil
}

// LadoSameRepo reports whether two tracker repositories are the same one.
// Repository paths compare without case, as GitHub and GitLab treat them.
func LadoSameRepo(a, b TrackerRepo) bool {
	return a.Provider == b.Provider && strings.EqualFold(a.Host, b.Host) && strings.EqualFold(a.Native, b.Native)
}
