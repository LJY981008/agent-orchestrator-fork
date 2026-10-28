package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// LadoVerifyFormat names the canonical JSON of a verification record. The
// integration approval signs its sha256, so the form never changes under
// this name.
const LadoVerifyFormat = "lado-verify/1"

// LadoVerifyKind is one thing LADO verifies about a PR head
// (docs/LJY-PRD/TDD.md §8).
type LadoVerifyKind string

// Verification items, in the order a record lists them.
const (
	LadoVerifyRequiredChecks LadoVerifyKind = "required_checks"
	LadoVerifyCommands       LadoVerifyKind = "commands"
	LadoVerifyCommitMessages LadoVerifyKind = "commit_messages"
	LadoVerifyPaths          LadoVerifyKind = "paths"
	LadoVerifyBaseBranch     LadoVerifyKind = "base_branch"
	LadoVerifyProtection     LadoVerifyKind = "protection"
	LadoVerifyReview         LadoVerifyKind = "review"
	LadoVerifyEvidence       LadoVerifyKind = "evidence"
)

// LadoVerifyKinds returns the verification items in record order.
func LadoVerifyKinds() []LadoVerifyKind {
	return []LadoVerifyKind{
		LadoVerifyRequiredChecks, LadoVerifyCommands, LadoVerifyCommitMessages, LadoVerifyPaths,
		LadoVerifyBaseBranch, LadoVerifyProtection, LadoVerifyReview, LadoVerifyEvidence,
	}
}

// LadoVerifyOutcome is what one item, or a whole record, says about a head.
type LadoVerifyOutcome string

// Outcomes. A record fails when any item fails, else waits when any waits,
// else needs confirming when any does, and passes otherwise; info never
// counts.
const (
	LadoVerifyPass    LadoVerifyOutcome = "pass"
	LadoVerifyWait    LadoVerifyOutcome = "wait"
	LadoVerifyFail    LadoVerifyOutcome = "fail"
	LadoVerifyConfirm LadoVerifyOutcome = "confirm"
	LadoVerifyInfo    LadoVerifyOutcome = "info"
)

// Item codes. They are stable strings, sent to the renderer, which shows them
// in the developer's language. A plain pass has none; a waiting or failing
// item's code is also the Validating task's reason.
const (
	// LadoCodeNotRequired means the item does not apply to the execution
	// (info), for any kind.
	LadoCodeNotRequired = "not_required"

	// required_checks
	LadoCodeChecksPending = "checks_pending" // wait
	LadoCodeChecksFailed  = "checks_failed"  // fail
	LadoCodeNoneReported  = "none_reported"  // pass: no check was required or recorded

	// commands
	LadoCodeCommandsRunning   = "commands_running"     // wait
	LadoCodeWorktreeNotAtHead = "worktree_not_at_head" // wait
	// LadoCodeWorktreeChanged means the worktree changed since LADO's last
	// check command on the head ended, as with an edit the worker did not
	// commit: the next command waits rather than test it.
	LadoCodeWorktreeChanged = "worktree_changed"  // wait
	LadoCodeCommandsFailed  = "commands_failed"   // fail
	LadoCodeCommandTimedOut = "command_timed_out" // fail
	LadoCodeWorktreeMissing = "worktree_missing"  // fail
	LadoCodeNoTests         = "no_tests"          // confirm: no command and no required check
	LadoCodeNoneConfigured  = "none_configured"   // pass: no command, but required checks

	// commit_messages, paths and evidence: LadoCodeHeadUnavailable means the
	// head is not in the local repository and LADO could not fetch it from
	// the PR's host, so the items git reads wait.
	LadoCodeHeadUnavailable = "head_unavailable" // wait

	// commit_messages
	LadoCodeCommitMessagesFailed  = "commit_messages_failed"  // fail
	LadoCodeCommitMessagesUnclear = "commit_messages_unclear" // confirm

	// paths
	LadoCodeProtectedPath = "protected_path" // fail
	LadoCodeOutsidePlan   = "outside_plan"   // confirm

	// base_branch
	LadoCodeBaseMoved = "base_moved" // confirm
	// LadoCodeBaseDeleted means the local base branch, which existed when
	// the execution was dispatched, is gone, with its reflog.
	LadoCodeBaseDeleted   = "base_deleted"    // confirm
	LadoCodeReflogOff     = "reflog_off"      // confirm
	LadoCodeAlreadyOnBase = "already_on_base" // confirm
	LadoCodeNoLocalBranch = "no_local_branch" // pass

	// protection
	LadoCodeUnprotected       = "unprotected"        // confirm
	LadoCodeProtectionUnknown = "protection_unknown" // confirm
	LadoCodeUnprotectedGitLab = "unprotected_gitlab" // confirm

	// review
	LadoCodeReviewPending      = "review_pending"      // wait
	LadoCodeChangesRequested   = "changes_requested"   // fail
	LadoCodeReviewInconclusive = "review_inconclusive" // confirm
)

// LadoVerifyItem is one item of a verification record. Details is the JSON
// of the kind's details struct, such as LadoCommandsDetails.
type LadoVerifyItem struct {
	Kind    LadoVerifyKind    `json:"kind"`
	Outcome LadoVerifyOutcome `json:"outcome"`
	Code    string            `json:"code"`
	Details json.RawMessage   `json:"details"`
}

// NewLadoVerifyItem builds an item from the kind's details struct, nil for
// none. It refuses an unknown kind or outcome, a waiting, failing or
// confirming item without a code, and details of another kind. Lists in the
// details encode as [] when nil.
func NewLadoVerifyItem(kind LadoVerifyKind, outcome LadoVerifyOutcome, code string, details any) (LadoVerifyItem, error) {
	if !slices.Contains(LadoVerifyKinds(), kind) {
		return LadoVerifyItem{}, fmt.Errorf("unknown verification item %q", kind)
	}
	switch outcome {
	case LadoVerifyPass, LadoVerifyInfo:
	case LadoVerifyWait, LadoVerifyFail, LadoVerifyConfirm:
		if code == "" {
			return LadoVerifyItem{}, fmt.Errorf("verification item %s: a %s outcome needs a code", kind, outcome)
		}
	default:
		return LadoVerifyItem{}, fmt.Errorf("verification item %s: unknown outcome %q", kind, outcome)
	}
	item := LadoVerifyItem{Kind: kind, Outcome: outcome, Code: code, Details: json.RawMessage("{}")}
	if details == nil {
		return item, nil
	}
	detailsKind, normalized := normalizeLadoVerifyDetails(details)
	if detailsKind != kind {
		return LadoVerifyItem{}, fmt.Errorf("verification item %s: details of type %T", kind, details)
	}
	data, err := ladoCanonicalJSON(normalized)
	if err != nil {
		return LadoVerifyItem{}, fmt.Errorf("verification item %s: %w", kind, err)
	}
	item.Details = data
	return item, nil
}

// normalizeLadoVerifyDetails returns the kind a details struct belongs to and
// the struct with every nil list made empty. Another type has no kind.
func normalizeLadoVerifyDetails(details any) (LadoVerifyKind, any) {
	switch d := details.(type) {
	case LadoRequiredChecksDetails:
		d.Required, d.Checks = ladoOrEmpty(d.Required), ladoOrEmpty(d.Checks)
		return LadoVerifyRequiredChecks, d
	case LadoCommandsDetails:
		d.Runs = ladoOrEmpty(d.Runs)
		return LadoVerifyCommands, d
	case LadoCommitMessagesDetails:
		d.Commits = ladoOrEmpty(d.Commits)
		return LadoVerifyCommitMessages, d
	case LadoPathsDetails:
		d.Protected, d.OutsidePlan = ladoOrEmpty(d.Protected), ladoOrEmpty(d.OutsidePlan)
		return LadoVerifyPaths, d
	case LadoBaseBranchDetails:
		d.Moved = ladoOrEmpty(d.Moved)
		return LadoVerifyBaseBranch, d
	case LadoProtectionDetails:
		d.Missing, d.RequiredChecks = ladoOrEmpty(d.Missing), ladoOrEmpty(d.RequiredChecks)
		return LadoVerifyProtection, d
	case LadoReviewDetails:
		return LadoVerifyReview, d
	case LadoEvidenceDetails:
		// Copy the criteria: the caller's slice stays as it was.
		criteria := make([]LadoEvidenceCriterion, len(d.Criteria))
		for i, criterion := range d.Criteria {
			criterion.Refs = ladoOrEmpty(criterion.Refs)
			criteria[i] = criterion
		}
		d.Criteria = criteria
		return LadoVerifyEvidence, d
	default:
		return "", nil
	}
}

// ladoOrEmpty returns an empty slice for nil, so the list encodes as [].
func ladoOrEmpty[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// ladoCanonicalJSON encodes v the way LADO's hashed forms are: fields in
// declaration order, compact, with HTML characters left as they are.
func ladoCanonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// The details of each kind. Field order is the format: a field added, moved
// or renamed changes every record hash.

// LadoRequiredChecksDetails are the details of required_checks: the check
// names the base branch's ruleset requires, whether the ruleset was read, and
// every check recorded for the head.
type LadoRequiredChecksDetails struct {
	Required    []string          `json:"required"`
	RulesetRead bool              `json:"rulesetRead"`
	Checks      []LadoVerifyCheck `json:"checks"`
}

// LadoVerifyCheck is one CI check recorded for the head.
type LadoVerifyCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Required bool   `json:"required"`
}

// LadoCommandsDetails are the details of commands: the newest run of each
// project check command for the head.
type LadoCommandsDetails struct {
	Runs []LadoVerifyCommandRun `json:"runs"`
}

// LadoVerifyCommandRun is one check command's run for the head. CommandSHA256
// is the hash of the command's text (LadoCheckCommandSHA256): a run counts
// for a command only with the same name and text, so a changed command
// changes the record. CheckRunID is empty and ExitCode nil while the command
// has not run or not exited.
type LadoVerifyCommandRun struct {
	Name          string `json:"name"`
	CommandSHA256 string `json:"commandSha256"`
	CheckRunID    string `json:"checkRunId"`
	Status        string `json:"status"`
	ExitCode      *int   `json:"exitCode"`
	DurationMS    int64  `json:"durationMs"`
}

// LadoCheckCommandSHA256 is the hex sha256 of a check command's text.
func LadoCheckCommandSHA256(command string) string {
	sum := sha256.Sum256([]byte(command))
	return hex.EncodeToString(sum[:])
}

// LadoCommitMessagesDetails are the details of commit_messages: the verdict
// on each commit after the merge-base and on the PR's title, which becomes a
// squash commit's subject.
type LadoCommitMessagesDetails struct {
	Commits []LadoVerifyCommit `json:"commits"`
	PRTitle LadoVerifyText     `json:"prTitle"`
}

// LadoVerifyCommit is one commit with the validator's result.
type LadoVerifyCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Result  string `json:"result"`
}

// LadoVerifyText is a text with the validator's result.
type LadoVerifyText struct {
	Text   string `json:"text"`
	Result string `json:"result"`
}

// LadoPathsDetails are the details of paths: the changed paths a protected
// glob matches and the plan does not name, and the changed paths outside the
// plan's affected_paths, the first of them and how many there are.
type LadoPathsDetails struct {
	Protected    []string `json:"protected"`
	OutsidePlan  []string `json:"outsidePlan"`
	OutsideCount int      `json:"outsideCount"`
}

// LadoBaseBranchDetails are the details of base_branch: the base branch and
// remote, the local reflog entries since the execution started that the
// remote branch does not hold, whether the head was on the remote base
// already (docs/LJY-PRD/TDD.md §7.3), the local branch's commit when the
// execution was dispatched (empty when it had none or LADO did not record
// it) and whether that branch is gone now.
type LadoBaseBranchDetails struct {
	Branch         string            `json:"branch"`
	Remote         string            `json:"remote"`
	Moved          []LadoReflogEntry `json:"moved"`
	AlreadyOnBase  bool              `json:"alreadyOnBase"`
	DispatchCommit string            `json:"dispatchCommit"`
	Deleted        bool              `json:"deleted"`
}

// LadoReflogEntry is one reflog entry of the local base branch.
type LadoReflogEntry struct {
	Selector string `json:"selector"`
	Commit   string `json:"commit"`
	Subject  string `json:"subject"`
}

// LadoProtectionDetails are the details of protection: what the base
// branch's remote rules lack (docs/LJY-PRD/TDD.md §7.1), the checks they
// require, and the class of a read that failed (LadoProtectionError*). The
// class is stable from one read to the next, so the record's hash is; the
// failed read's message stays outside it (LadoVerification.ProtectionError).
type LadoProtectionDetails struct {
	Provider       string   `json:"provider"`
	Repo           string   `json:"repo"`
	Branch         string   `json:"branch"`
	Missing        []string `json:"missing"`
	RequiredChecks []string `json:"requiredChecks"`
	ErrorClass     string   `json:"errorClass"`
}

// Classes of a failed read of a branch's rules: the host's HTTP status, as
// "http_404", a rate limit, a request that did not reach the host or got no
// answer, and anything else.
const (
	LadoProtectionErrorHTTPPrefix  = "http_"
	LadoProtectionErrorRateLimited = "rate_limited"
	LadoProtectionErrorTransport   = "transport"
	LadoProtectionErrorOther       = "other"
)

// LadoReviewDetails are the details of review: the rounds LADO started, the
// run that decides and its verdict, the reviewer used, and a note such as a
// reviewer the plan named that AO does not know.
type LadoReviewDetails struct {
	Rounds  int    `json:"rounds"`
	RunID   string `json:"runId"`
	Verdict string `json:"verdict"`
	Harness string `json:"harness"`
	Note    string `json:"note"`
}

// LadoEvidenceDetails are the details of evidence: the pinned evidence file's
// version, sha256 and the head it describes, and what each acceptance
// criterion links to.
type LadoEvidenceDetails struct {
	FileVersion int                     `json:"fileVersion"`
	FileSHA256  string                  `json:"fileSha256"`
	FileHead    string                  `json:"fileHead"`
	Criteria    []LadoEvidenceCriterion `json:"criteria"`
}

// LadoEvidenceCriterion is one acceptance criterion, by its 1-based index in
// the plan, with its evidence state and the records its entries resolved to.
type LadoEvidenceCriterion struct {
	Index int               `json:"index"`
	State LadoEvidenceState `json:"state"`
	Refs  []LadoEvidenceRef `json:"refs"`
}

// LadoEvidenceRef is one record an evidence entry names.
type LadoEvidenceRef struct {
	Kind LadoEvidenceRefKind `json:"kind"`
	Key  string              `json:"key"`
	SHA  string              `json:"sha"`
}

// LadoVerification is what LADO verified about one head of an execution's PR
// (docs/LJY-PRD/TDD.md §8). It is evidence about that head, like a check
// run: a new head needs a new record. Items follow LadoVerifyKinds.
type LadoVerification struct {
	ExecutionSessionID SessionID
	HeadSHA            string
	PRURL              string
	Items              []LadoVerifyItem
	// Notified lists the items whose failure the worker was told about once,
	// and LadoNotifiedWorktreeBehind once the worker was asked to pull a
	// head made on the host. It, ProtectionError and ComputedAt stay out of
	// the content and its hash.
	Notified []string
	// ProtectionError is the message of a failed read of the base branch's
	// rules, without secrets; the protection item holds only its class.
	ProtectionError string
	ComputedAt      time.Time
}

// LadoNotifiedWorktreeBehind is the Notified entry of the one message that
// asks a worker whose worktree is behind a head made on the host to pull it.
const LadoNotifiedWorktreeBehind = "worktree_behind"

// Overall returns the record's outcome: fail if any item fails, else wait if
// any waits, else confirm if any needs confirming, else pass. The reason is
// the code of the first failing item, else of the first waiting one, else
// empty. Info items do not count.
func (v LadoVerification) Overall() (LadoVerifyOutcome, string) {
	waiting, confirming := false, false
	var waitCode string
	for _, item := range v.Items {
		switch item.Outcome {
		case LadoVerifyFail:
			return LadoVerifyFail, item.Code
		case LadoVerifyWait:
			if !waiting {
				waiting, waitCode = true, item.Code
			}
		case LadoVerifyConfirm:
			confirming = true
		}
	}
	switch {
	case waiting:
		return LadoVerifyWait, waitCode
	case confirming:
		return LadoVerifyConfirm, ""
	default:
		return LadoVerifyPass, ""
	}
}

// Content is the record's canonical JSON, the format LadoVerifyFormat names:
// the format, the PR, the head and the items, never times or Notified.
func (v LadoVerification) Content() ([]byte, error) {
	items := make([]LadoVerifyItem, len(v.Items))
	for i, item := range v.Items {
		if len(item.Details) == 0 {
			item.Details = json.RawMessage("{}")
		}
		items[i] = item
	}
	return ladoCanonicalJSON(struct {
		Format  string           `json:"format"`
		PRURL   string           `json:"prUrl"`
		HeadSHA string           `json:"headSha"`
		Items   []LadoVerifyItem `json:"items"`
	}{LadoVerifyFormat, v.PRURL, v.HeadSHA, items})
}

// SHA256 returns the hex sha256 of the content, which an integration
// approval signs.
func (v LadoVerification) SHA256() (string, error) {
	content, err := v.Content()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}

// ConfirmCodes returns the codes of the items that need confirming, sorted
// and without duplicates: what an integration approval must acknowledge.
func (v LadoVerification) ConfirmCodes() []string {
	codes := []string{}
	for _, item := range v.Items {
		if item.Outcome == LadoVerifyConfirm {
			codes = append(codes, item.Code)
		}
	}
	slices.Sort(codes)
	return slices.Compact(codes)
}

// LadoCheckRunStatus is the state of one run of a project check command.
type LadoCheckRunStatus string

// Check run states. Every state but running is final.
const (
	LadoCheckRunRunning   LadoCheckRunStatus = "running"
	LadoCheckRunPassed    LadoCheckRunStatus = "passed"
	LadoCheckRunFailed    LadoCheckRunStatus = "failed"
	LadoCheckRunTimedOut  LadoCheckRunStatus = "timed_out"
	LadoCheckRunCancelled LadoCheckRunStatus = "cancelled"
	LadoCheckRunError     LadoCheckRunStatus = "error"
)

// LadoCheckRun is one run of a project check command on an execution's head
// in its worktree (docs/LJY-PRD/TDD.md §8.2). ExitCode is nil until the
// command exited; LogSHA256 names the blob with the end of its output.
// WorktreeSHA256 is the hash of the worktree's git status after the run
// ended (LadoWorktreeStatusSHA256), empty when LADO could not read it: the
// next command on the head runs only while the status is still that.
type LadoCheckRun struct {
	ID                 string
	ExecutionSessionID SessionID
	HeadSHA            string
	Name               string
	Command            string
	Status             LadoCheckRunStatus
	ExitCode           *int
	LogSHA256          string
	ErrorMessage       string
	WorktreeSHA256     string
	StartedAt          time.Time
	FinishedAt         time.Time
}

// LadoWorktreeStatusSHA256 is the hex sha256 of a worktree's
// git status --porcelain output, as LADO compares it between check runs.
func LadoWorktreeStatusSHA256(status string) string {
	sum := sha256.Sum256([]byte(status))
	return hex.EncodeToString(sum[:])
}

// ErrLadoCheckRunFinished means a check run already reached a final state,
// which stays as first recorded.
var ErrLadoCheckRunFinished = errors.New("the check run already finished")

// LadoCheckCommand is one of a project's check commands. The daemon runs it
// outside any agent sandbox, so changing the list needs the desktop app's
// confirmation (LadoPresenceCheckCommands).
type LadoCheckCommand struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// Limits of a project's check commands and their time limit, in minutes.
const (
	LadoCheckCommandsMax     = 10
	LadoCheckNameMaxChars    = 40
	LadoCheckCommandMaxChars = 2000
	LadoCheckTimeoutMin      = 5
	LadoCheckTimeoutMax      = 120
	LadoCheckTimeoutDefault  = 30
)

// Reasons a check command list is refused (LadoCheckCommandProblem). A
// command with a control character (Unicode Cc, as a line break or a tab, or
// a line or paragraph separator)
// or a bidirectional control character is refused: the desktop app shows a
// command as one line of text, and either would make what the developer
// confirms differ from what the shell runs (S2 of #15).
const (
	LadoCheckCommandTooMany   = "too_many"
	LadoCheckCommandRequired  = "required"
	LadoCheckCommandTooLong   = "too_long"
	LadoCheckCommandInvalid   = "invalid"
	LadoCheckCommandDuplicate = "duplicate"
	LadoCheckCommandControl   = "control_character"
	LadoCheckCommandBidi      = "bidi_control"
)

// LadoCheckCommandProblem is the first thing wrong with a check command list:
// the command's index, the field ("name" or "command", empty for the list)
// and the reason (LadoCheckCommand*). For a control character, Position is
// its 1-based place among the command's characters and Character the
// character as U+XXXX.
type LadoCheckCommandProblem struct {
	Index     int
	Field     string
	Reason    string
	Position  int
	Character string
}

// ladoBidiControls are the bidirectional formatting characters: they reorder
// how text shows without changing what it says.
var ladoBidiControls = []rune{'\u061c', '\u200e', '\u200f', '\u202a', '\u202b', '\u202c', '\u202d', '\u202e', '\u2066', '\u2067', '\u2068', '\u2069'}

// ladoCommandControl finds the first control or bidirectional control
// character of a command: its reason, its 1-based place among the command's
// characters and the character.
func ladoCommandControl(command string) (string, int, rune, bool) {
	position := 0
	for _, r := range command {
		position++
		switch {
		case unicode.Is(unicode.Cc, r) || r == '\u2028' || r == '\u2029':
			// The line and paragraph separators break a line as well.
			return LadoCheckCommandControl, position, r, true
		case slices.Contains(ladoBidiControls, r):
			return LadoCheckCommandBidi, position, r, true
		}
	}
	return "", 0, 0, false
}

// LadoCheckCommandsSHA256 is the hex sha256 of a check command list as its
// canonical JSON (an empty list for nil): a change of the list sends the hash
// of the list it started from, and LADO refuses it once the list moved on.
func LadoCheckCommandsSHA256(commands []LadoCheckCommand) string {
	data, err := ladoCanonicalJSON(ladoOrEmpty(commands))
	if err != nil {
		// Strings cannot fail to encode.
		panic(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ErrLadoCheckCommandsChanged means a project's check commands are no longer
// the list a change started from. The services report it as
// LADO_CHECK_COMMANDS_CHANGED.
var ErrLadoCheckCommandsChanged = errors.New("the project's check commands changed")

var ladoCheckNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]*$`)

// ValidateLadoCheckCommands returns the first problem of a check command
// list, nil when it has none: at most LadoCheckCommandsMax commands, each
// named with 1 to 40 letters, digits, spaces, dots, underscores or dashes
// starting with a letter or digit, names unique without case, and each
// command 1 to 2000 characters without a control character (NUL, a line
// break, a tab) or a bidirectional control character. Too many commands name
// the first one over the limit.
func ValidateLadoCheckCommands(commands []LadoCheckCommand) *LadoCheckCommandProblem {
	if len(commands) > LadoCheckCommandsMax {
		return &LadoCheckCommandProblem{Index: LadoCheckCommandsMax, Reason: LadoCheckCommandTooMany}
	}
	for i, command := range commands {
		problem := func(field, reason string) *LadoCheckCommandProblem {
			return &LadoCheckCommandProblem{Index: i, Field: field, Reason: reason}
		}
		switch {
		case command.Name == "":
			return problem("name", LadoCheckCommandRequired)
		case utf8.RuneCountInString(command.Name) > LadoCheckNameMaxChars:
			return problem("name", LadoCheckCommandTooLong)
		case !ladoCheckNamePattern.MatchString(command.Name):
			return problem("name", LadoCheckCommandInvalid)
		case slices.ContainsFunc(commands[:i], func(c LadoCheckCommand) bool { return strings.EqualFold(c.Name, command.Name) }):
			return problem("name", LadoCheckCommandDuplicate)
		case strings.TrimSpace(command.Command) == "":
			return problem("command", LadoCheckCommandRequired)
		case utf8.RuneCountInString(command.Command) > LadoCheckCommandMaxChars:
			return problem("command", LadoCheckCommandTooLong)
		}
		if reason, position, r, found := ladoCommandControl(command.Command); found {
			p := problem("command", reason)
			p.Position, p.Character = position, fmt.Sprintf("U+%04X", r)
			return p
		}
	}
	return nil
}

// LadoIntegrationStatusState is how posting the integration commit status of
// an approved head went (docs/LJY-PRD/TDD.md §7.1).
type LadoIntegrationStatusState string

// Status states. GitLab has no such status: not applicable.
const (
	LadoIntegrationStatusPending       LadoIntegrationStatusState = "pending"
	LadoIntegrationStatusPosted        LadoIntegrationStatusState = "posted"
	LadoIntegrationStatusFailed        LadoIntegrationStatusState = "failed"
	LadoIntegrationStatusNotApplicable LadoIntegrationStatusState = "not_applicable"
)

// LadoIntegrationStatusContext is the commit status context LADO posts on an
// approved head; a ruleset that requires it lets only approved heads merge.
const LadoIntegrationStatusContext = "lado/integration"

// LadoIntegrationStatusMaxAttempts caps the attempts to post the status.
const LadoIntegrationStatusMaxAttempts = 12

// LadoIntegrationApproval is the developer's approval, with human presence,
// to integrate one head of an execution's PR into one target branch
// (docs/LJY-PRD/TDD.md §7.1). It signs the verification record's sha256 and
// acknowledges its confirm codes. Approvals are per PR, head and target
// branch (S5, S6 of #15) and never deleted.
type LadoIntegrationApproval struct {
	ID                 string
	ExecutionSessionID SessionID
	PRURL              string
	HeadSHA            string
	TargetBranch       string
	VerificationSHA256 string
	Acknowledged       []string
	ProofNonce         string
	Actor              string
	ApprovedAt         time.Time
	StatusState        LadoIntegrationStatusState
	StatusAttempts     int
	StatusPostedAt     time.Time
	// StatusError is the last failed post's error, without secrets.
	StatusError string
}

// ErrLadoIntegrationAlreadyApproved means the PR's head already has an
// integration approval for the target branch. The services report it as
// LADO_INTEGRATION_ALREADY_APPROVED.
var ErrLadoIntegrationAlreadyApproved = errors.New("the head already has an integration approval")

// ErrLadoVerificationChanged means the head's stored verification record no
// longer has the sha256 an integration approval signs. The services report it
// as LADO_VERIFICATION_CHANGED.
var ErrLadoVerificationChanged = errors.New("the head's verification record changed")

// LadoApprovalFor returns the approval of approvals that holds for target,
// the branch the PR merges into now: the one approved for that branch,
// compared without case as hosts compare branch names. An approval
// recorded without a target holds for any.
func LadoApprovalFor(approvals []LadoIntegrationApproval, target string) (LadoIntegrationApproval, bool) {
	for _, approval := range approvals {
		if approval.TargetBranch == "" || strings.EqualFold(approval.TargetBranch, target) {
			return approval, true
		}
	}
	return LadoIntegrationApproval{}, false
}

// LadoEvidencePath is where a worker writes its evidence file, relative to
// its session's artifact directory (docs/LJY-PRD/TDD.md §8.6).
const LadoEvidencePath = "lado/evidence.md"

// LadoEvidenceState is what an acceptance criterion's evidence amounts to
// (docs/LJY-PRD/TDD.md §8.6).
type LadoEvidenceState string

// Evidence states. Linked: an entry resolved to a record of the head. Stale:
// the evidence file describes another head. Claimed: entries, none resolved.
// Inferred: the file does not list the criterion. File marks the pinned
// evidence file itself, criterion 0.
const (
	LadoEvidenceLinked   LadoEvidenceState = "linked"
	LadoEvidenceStale    LadoEvidenceState = "stale"
	LadoEvidenceClaimed  LadoEvidenceState = "claimed"
	LadoEvidenceInferred LadoEvidenceState = "inferred"
	LadoEvidenceFile     LadoEvidenceState = "file"
)

// LadoEvidenceRefKind is the kind of record an evidence link points to.
type LadoEvidenceRefKind string

// Evidence record kinds. Claim is an entry that matched no record; none is a
// criterion without entries.
const (
	LadoEvidenceRefPRCheck         LadoEvidenceRefKind = "pr_check"
	LadoEvidenceRefReviewRun       LadoEvidenceRefKind = "review_run"
	LadoEvidenceRefArtifactVersion LadoEvidenceRefKind = "artifact_version"
	LadoEvidenceRefCheckRun        LadoEvidenceRefKind = "lado_check_run"
	LadoEvidenceRefCommit          LadoEvidenceRefKind = "commit"
	LadoEvidenceRefClaim           LadoEvidenceRefKind = "claim"
	LadoEvidenceRefNone            LadoEvidenceRefKind = "none"
)

// LadoEvidenceLink ties one acceptance criterion of an execution's head to a
// record (docs/LJY-PRD/TDD.md §8.6). Criterion is the 1-based index into the
// plan's acceptance_criteria; criterion 0 with state file records the pinned
// evidence file, its ref "<session>/<path>@<version>" and its sha256.
type LadoEvidenceLink struct {
	ID                 string
	ExecutionSessionID SessionID
	HeadSHA            string
	Criterion          int
	State              LadoEvidenceState
	RefKind            LadoEvidenceRefKind
	RefKey             string
	SHA                string
	CreatedAt          time.Time
}

// LadoExecutionReview is one review round LADO started for an execution's
// head through AO's review service (docs/LJY-PRD/TDD.md §8.5). CancelledAt
// is when LADO cancelled a run that stalled.
type LadoExecutionReview struct {
	ReviewRunID        string
	ExecutionSessionID SessionID
	HeadSHA            string
	Round              int
	CreatedAt          time.Time
	CancelledAt        time.Time
}

// LadoReviewRoundCap is how many review rounds LADO starts for one execution.
const LadoReviewRoundCap = 3

// LadoReviewStallAfter is how long a LADO review run may run before LADO
// cancels it; the round still counts.
const LadoReviewStallAfter = 30 * time.Minute
