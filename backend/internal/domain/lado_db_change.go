package domain

// LADO fork: production database changes tied to the integration approval
// (#97, docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md). A
// project, or each child repository of a workspace, may name the SQL files it
// applies to its production database by hand. A PR head that adds such a file
// gets a db_changes item in its verification record, so the integration
// approval signs the files' blobs; once approved, LADO applies the pre-merge
// files and only then posts lado/integration and merges. These are the types
// every part of that work shares.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// LadoDBChangePhase is when a SQL file is applied: before the merge, or after
// the deploy, by the developer's button, for SQL that cannot be undone.
type LadoDBChangePhase string

// Phases.
const (
	LadoDBChangePreMerge    LadoDBChangePhase = "pre_merge"
	LadoDBChangeAfterDeploy LadoDBChangePhase = "after_deploy"
)

// LadoDBChangeAfterDeployMarker is the line that, as a file's first line that
// is not blank, puts the file in the after_deploy phase. It compares without
// case and around surrounding spaces.
const LadoDBChangeAfterDeployMarker = "-- lado:after-deploy"

// LadoDBChangePhaseOf returns the phase of a SQL file's content: after_deploy
// when its first line that is not blank is LadoDBChangeAfterDeployMarker,
// pre_merge otherwise. A byte order mark at the start is skipped.
func LadoDBChangePhaseOf(content string) LadoDBChangePhase {
	content = strings.TrimPrefix(content, "\ufeff")
	for line := range strings.Lines(content) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.EqualFold(line, LadoDBChangeAfterDeployMarker) {
			return LadoDBChangeAfterDeploy
		}
		return LadoDBChangePreMerge
	}
	return LadoDBChangePreMerge
}

// LadoDBChangeSettings are one repository's production database change
// settings. Repo is the child repository's folder in a workspace project and
// "" in a single-repository project. Paths are globs relative to the
// repository's root; Command is the line the shell runs for each file, with
// {file} for the file's path; TimeoutMinutes 0 is LadoDBChangeTimeoutDefault.
// SecretNames are the environment variables the command gets from the secrets
// file; their values never sit here. BaselineCommit is the default branch's
// commit whose matching files LADO recorded as already applied when the
// settings were turned on or their paths changed.
type LadoDBChangeSettings struct {
	ProjectID      ProjectID
	Repo           string
	Enabled        bool
	Paths          []string
	Command        string
	TimeoutMinutes int
	SecretNames    []string
	BaselineCommit string
	EnabledAt      time.Time
	UpdatedAt      time.Time
}

// Timeout is the command's time limit for one file.
func (s LadoDBChangeSettings) Timeout() time.Duration {
	minutes := s.TimeoutMinutes
	if minutes <= 0 {
		minutes = LadoDBChangeTimeoutDefault
	}
	return time.Duration(minutes) * time.Minute
}

// SHA256 is the hex sha256 of the settings as a change compares them: every
// field but the times. A change sends the hash of the settings it started
// from, and LADO refuses it once they moved on (ErrLadoDBChangeSettingsChanged).
func (s LadoDBChangeSettings) SHA256() string {
	data, err := ladoCanonicalJSON(struct {
		ProjectID      ProjectID `json:"projectId"`
		Repo           string    `json:"repo"`
		Enabled        bool      `json:"enabled"`
		Paths          []string  `json:"paths"`
		Command        string    `json:"command"`
		TimeoutMinutes int       `json:"timeoutMinutes"`
		SecretNames    []string  `json:"secretNames"`
		BaselineCommit string    `json:"baselineCommit"`
	}{s.ProjectID, s.Repo, s.Enabled, ladoOrEmpty(s.Paths), s.Command, s.TimeoutMinutes, ladoOrEmpty(s.SecretNames), s.BaselineCommit})
	if err != nil {
		// Strings, bools and ints cannot fail to encode.
		panic(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// LadoDBChangeSettingsListSHA256 is the hex sha256 of a project's settings,
// in the order given: the hash of a settings view.
func LadoDBChangeSettingsListSHA256(settings []LadoDBChangeSettings) string {
	h := sha256.New()
	for _, s := range settings {
		h.Write([]byte(s.SHA256()))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// LadoDBChangeSettingsUpdate is a change of one repository's settings. Base
// is the SHA256 of the repository's settings it started from, "" for a
// repository without settings yet. Secrets sets and removes secret values;
// the names stay in SecretNames.
type LadoDBChangeSettingsUpdate struct {
	Repo           string
	Enabled        bool
	Paths          []string
	Command        string
	TimeoutMinutes int
	SecretNames    []string
	Secrets        LadoDBChangeSecretsUpdate
	Base           string
}

// LadoDBChangeSecretsUpdate sets secret values by name and removes others.
type LadoDBChangeSecretsUpdate struct {
	Set    map[string]string
	Remove []string
}

// Settings returns the update as the settings of repo in project, without
// times or baseline.
func (u LadoDBChangeSettingsUpdate) Settings(project ProjectID) LadoDBChangeSettings {
	return LadoDBChangeSettings{
		ProjectID: project, Repo: u.Repo, Enabled: u.Enabled, Paths: u.Paths, Command: u.Command,
		TimeoutMinutes: u.TimeoutMinutes, SecretNames: u.SecretNames,
	}
}

// LadoDBChangeSettingsView is a project's settings as the app shows them:
// one entry per repository that may have settings (the project's root, or
// each child repository of a workspace), and the hash of the settings.
type LadoDBChangeSettingsView struct {
	Repositories []LadoDBChangeSettingsRepo
	SHA256       string
}

// LadoDBChangeSettingsRepo is one repository's settings with its display name
// and the names of the secrets whose values are stored, never the values.
type LadoDBChangeSettingsRepo struct {
	LadoDBChangeSettings
	Name              string
	StoredSecretNames []string
}

// Limits of the settings.
const (
	LadoDBChangePathsMax       = 10
	LadoDBChangePathMaxChars   = 256
	LadoDBChangeCommandMax     = 2000
	LadoDBChangeTimeoutMin     = 1
	LadoDBChangeTimeoutMax     = 120
	LadoDBChangeTimeoutDefault = 10
	LadoDBChangeSecretsMax     = 10
	// LadoDBChangeFilePlaceholder is what the command names the file by.
	LadoDBChangeFilePlaceholder = "{file}"
)

// Reasons a setting is refused (LadoDBChangeProblem).
const (
	LadoDBChangeProblemRequired       = "required"
	LadoDBChangeProblemTooMany        = "too_many"
	LadoDBChangeProblemTooLong        = "too_long"
	LadoDBChangeProblemInvalid        = "invalid"
	LadoDBChangeProblemDuplicate      = "duplicate"
	LadoDBChangeProblemParentDir      = "parent_dir"
	LadoDBChangeProblemAbsolute       = "absolute"
	LadoDBChangeProblemNoPlaceholder  = "no_file_placeholder"
	LadoDBChangeProblemControl        = "control_character"
	LadoDBChangeProblemBidi           = "bidi_control"
	LadoDBChangeProblemOutOfRange     = "out_of_range"
	LadoDBChangeProblemReservedSecret = "reserved"
)

// LadoDBChangeProblem is one thing wrong with a setting: the field, such as
// "paths[1]" or "secretNames[0]", and the reason (LadoDBChangeProblem*).
type LadoDBChangeProblem struct {
	Field  string
	Reason string
}

// LadoDBChangeInvalidError is a refused setting with every problem found.
// The services report it as LADO_DB_CHANGE_INVALID.
type LadoDBChangeInvalidError struct {
	Problems []LadoDBChangeProblem
}

func (e *LadoDBChangeInvalidError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.Field + ": " + p.Reason
	}
	return "invalid database change settings: " + strings.Join(parts, ", ")
}

// ladoDBChangeSecretPattern is a secret's environment variable name.
var ladoDBChangeSecretPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// The environment variables a secret may not be named: the ones LADO's check
// command runner withholds from the daemon's environment
// (ladoverify.withheldPrefixes, withheldSuffixes, withheldNames) and the ones
// a command's shell and git need as LADO sets them.
var (
	ladoDBChangeReservedPrefixes = []string{"AO_", "GCM_", "GH_", "GITHUB_", "GITLAB_", "GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"}
	ladoDBChangeReservedSuffixes = []string{"ASKPASS", "_TOKEN", "_API_KEY"}
	ladoDBChangeReservedNames    = []string{
		"SSH_AUTH_SOCK", "GIT_SSH", "GIT_SSH_COMMAND", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_TERMINAL_PROMPT",
		"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "COMSPEC", "TEMP", "TMP", "TMPDIR", "GH_CONFIG_DIR",
	}
)

// LadoDBChangeReservedEnv reports whether name, compared without case, is an
// environment variable a secret may not set: one LADO withholds from or sets
// for every command it runs, or one the shell needs as it is.
func LadoDBChangeReservedEnv(name string) bool {
	upper := strings.ToUpper(name)
	return slices.ContainsFunc(ladoDBChangeReservedPrefixes, func(p string) bool { return strings.HasPrefix(upper, p) }) ||
		slices.ContainsFunc(ladoDBChangeReservedSuffixes, func(s string) bool { return strings.HasSuffix(upper, s) }) ||
		slices.Contains(ladoDBChangeReservedNames, upper)
}

// ValidateLadoDBChangeSettings returns every problem of a repository's
// settings, nil when there is none. Repo is "" or a folder inside the
// workspace. Paths are 1 to LadoDBChangePathsMax globs relative to the
// repository, none absolute or with a ".." segment, a backslash or a control
// character; Command is one line of at most LadoDBChangeCommandMax characters
// with {file}; TimeoutMinutes is 0 (the default) or 1 to 120; SecretNames are
// at most LadoDBChangeSecretsMax distinct names of ^[A-Z_][A-Z0-9_]{0,63}$,
// none reserved (LadoDBChangeReservedEnv). Settings that are turned off may
// leave the paths and the command empty; what they hold is still checked.
func ValidateLadoDBChangeSettings(s LadoDBChangeSettings) []LadoDBChangeProblem {
	var problems []LadoDBChangeProblem
	add := func(field, reason string) {
		problems = append(problems, LadoDBChangeProblem{Field: field, Reason: reason})
	}
	if s.Repo != "" && !validLadoRepoRelative(s.Repo) {
		add("repo", LadoDBChangeProblemInvalid)
	}

	switch {
	case len(s.Paths) == 0 && s.Enabled:
		add("paths", LadoDBChangeProblemRequired)
	case len(s.Paths) > LadoDBChangePathsMax:
		add("paths", LadoDBChangeProblemTooMany)
	}
	for i, glob := range s.Paths {
		field := fmt.Sprintf("paths[%d]", i)
		if reason := ladoDBChangeGlobProblem(glob); reason != "" {
			add(field, reason)
		} else if slices.Contains(s.Paths[:i], glob) {
			add(field, LadoDBChangeProblemDuplicate)
		}
	}

	switch {
	case strings.TrimSpace(s.Command) == "":
		if s.Enabled {
			add("command", LadoDBChangeProblemRequired)
		}
	case utf8.RuneCountInString(s.Command) > LadoDBChangeCommandMax:
		add("command", LadoDBChangeProblemTooLong)
	default:
		if reason, _, _, found := ladoCommandControl(s.Command); found {
			if reason == LadoCheckCommandBidi {
				add("command", LadoDBChangeProblemBidi)
			} else {
				add("command", LadoDBChangeProblemControl)
			}
		} else if !strings.Contains(s.Command, LadoDBChangeFilePlaceholder) {
			add("command", LadoDBChangeProblemNoPlaceholder)
		}
	}

	if s.TimeoutMinutes != 0 && (s.TimeoutMinutes < LadoDBChangeTimeoutMin || s.TimeoutMinutes > LadoDBChangeTimeoutMax) {
		add("timeoutMinutes", LadoDBChangeProblemOutOfRange)
	}

	if len(s.SecretNames) > LadoDBChangeSecretsMax {
		add("secretNames", LadoDBChangeProblemTooMany)
	}
	for i, name := range s.SecretNames {
		field := fmt.Sprintf("secretNames[%d]", i)
		switch {
		case !ladoDBChangeSecretPattern.MatchString(name):
			add(field, LadoDBChangeProblemInvalid)
		case LadoDBChangeReservedEnv(name):
			add(field, LadoDBChangeProblemReservedSecret)
		case slices.Contains(s.SecretNames[:i], name):
			add(field, LadoDBChangeProblemDuplicate)
		}
	}
	return problems
}

// ladoDBChangeGlobProblem is the reason a path glob is refused, "" for none.
func ladoDBChangeGlobProblem(glob string) string {
	switch {
	case strings.TrimSpace(glob) == "":
		return LadoDBChangeProblemRequired
	case utf8.RuneCountInString(glob) > LadoDBChangePathMaxChars:
		return LadoDBChangeProblemTooLong
	case ladoLogHasControl(glob) || strings.ContainsFunc(glob, func(r rune) bool { return slices.Contains(ladoBidiControls, r) }):
		return LadoDBChangeProblemControl
	case strings.HasPrefix(glob, "/") || (len(glob) >= 2 && glob[1] == ':'):
		return LadoDBChangeProblemAbsolute
	case strings.ContainsRune(glob, '\\') || glob != strings.TrimSpace(glob):
		return LadoDBChangeProblemInvalid
	}
	for _, segment := range strings.Split(glob, "/") {
		if segment == ".." {
			return LadoDBChangeProblemParentDir
		}
	}
	return ""
}

// ErrLadoDBChangeSettingsChanged means a repository's settings are no longer
// the ones a change started from. The services report it as
// LADO_DB_CHANGE_SETTINGS_CHANGED.
var ErrLadoDBChangeSettingsChanged = errors.New("the repository's database change settings changed")

// ErrLadoDBChangeBaselineFailed means LADO could not read the default
// branch's files to record them as applied, so the settings were not saved.
// The services report it as LADO_DB_CHANGE_BASELINE_FAILED.
var ErrLadoDBChangeBaselineFailed = errors.New("the database change baseline could not be recorded")

// ErrLadoDBChangePending means an approved head still has pre-merge SQL that
// LADO did not apply, so it may not merge yet. The services report it as
// LADO_DB_CHANGE_PENDING (LadoDBChangePendingError).
var ErrLadoDBChangePending = errors.New("the head's pre-merge SQL is not applied yet")

// ErrLadoDBChangeNotFound means no database change run or file matches the
// request. The services report it as LADO_DB_CHANGE_NOT_FOUND.
var ErrLadoDBChangeNotFound = errors.New("no such database change")

// ErrLadoDBChangeRunState means the run is not in a state the request may
// change, as a retry of a run that is applying. The services report it as
// LADO_DB_CHANGE_RUN_STATE.
var ErrLadoDBChangeRunState = errors.New("the database change run is not in a state the request may change")

// LadoDBChangePendingError is ErrLadoDBChangePending for one PR head with the
// pre-merge files not applied yet.
type LadoDBChangePendingError struct {
	PRURL   string
	HeadSHA string
	Files   []LadoDBChangeFileRef
}

func (e *LadoDBChangePendingError) Error() string {
	return fmt.Sprintf("%s at %s: %d pre-merge SQL file(s) not applied yet", e.PRURL, e.HeadSHA, len(e.Files))
}

// Is makes errors.Is(err, ErrLadoDBChangePending) hold.
func (e *LadoDBChangePendingError) Is(target error) bool { return target == ErrLadoDBChangePending }

// LadoDBChangeInput is what LADO knows of a PR head when it looks for its SQL
// files: the project and repository, the repository's local folder, the merge
// base and the head, and the approved plan.
type LadoDBChangeInput struct {
	ProjectID ProjectID
	Repo      string
	Dir       string
	Base      string
	Head      string
	Plan      LadoPlan
}

// LadoDBChangeFileRef is one SQL file a head adds or changes: its path in the
// repository, its blob and its phase. It is part of the db_changes item, so
// field order and names are the record format.
type LadoDBChangeFileRef struct {
	Path    string            `json:"path"`
	BlobSHA string            `json:"blobSha"`
	Phase   LadoDBChangePhase `json:"phase"`
}

// LadoDBChangeModified is an applied SQL file the head changes or deletes:
// the blob LADO applied and the head's blob, empty when deleted. It is part of
// the db_changes item too.
type LadoDBChangeModified struct {
	Path           string `json:"path"`
	AppliedBlobSHA string `json:"appliedBlobSha"`
	HeadBlobSHA    string `json:"headBlobSha"`
	Deleted        bool   `json:"deleted"`
}

// LadoDBChangeRunKind is what a run applies: a head's pre-merge files or its
// after-deploy files.
type LadoDBChangeRunKind string

// Run kinds.
const (
	LadoDBChangeRunPreMerge    LadoDBChangeRunKind = "pre_merge"
	LadoDBChangeRunAfterDeploy LadoDBChangeRunKind = "after_deploy"
)

// Phase is the phase of the files the run applies.
func (k LadoDBChangeRunKind) Phase() LadoDBChangePhase {
	if k == LadoDBChangeRunAfterDeploy {
		return LadoDBChangeAfterDeploy
	}
	return LadoDBChangePreMerge
}

// LadoDBChangeTrigger is why a run exists: an integration approval, a PR
// merged outside LADO with its pre-merge files not applied, or a merge after
// which after-deploy files wait for the developer.
type LadoDBChangeTrigger string

// Triggers.
const (
	LadoDBChangeTriggerApproval        LadoDBChangeTrigger = "approval"
	LadoDBChangeTriggerMergedUnapplied LadoDBChangeTrigger = "merged_unapplied"
	LadoDBChangeTriggerAfterMerge      LadoDBChangeTrigger = "after_merge"
)

// LadoDBChangeRunState is where a run is.
type LadoDBChangeRunState string

// Run states. awaiting_human waits for the developer's button (a preflight
// that stopped, files merged outside LADO, after-deploy files); queued waits
// for LADO's loop; applying runs the files; failed and merge_failed stopped
// and wait for a retry; applied has every file applied, merging asks AO to
// merge, and done is final.
const (
	LadoDBChangeRunAwaitingHuman LadoDBChangeRunState = "awaiting_human"
	LadoDBChangeRunQueued        LadoDBChangeRunState = "queued"
	LadoDBChangeRunApplying      LadoDBChangeRunState = "applying"
	LadoDBChangeRunFailed        LadoDBChangeRunState = "failed"
	LadoDBChangeRunApplied       LadoDBChangeRunState = "applied"
	LadoDBChangeRunMerging       LadoDBChangeRunState = "merging"
	LadoDBChangeRunMergeFailed   LadoDBChangeRunState = "merge_failed"
	LadoDBChangeRunDone          LadoDBChangeRunState = "done"
)

// LadoDBChangeRunStates are every run state.
func LadoDBChangeRunStates() []LadoDBChangeRunState {
	return []LadoDBChangeRunState{
		LadoDBChangeRunAwaitingHuman, LadoDBChangeRunQueued, LadoDBChangeRunApplying, LadoDBChangeRunFailed,
		LadoDBChangeRunApplied, LadoDBChangeRunMerging, LadoDBChangeRunMergeFailed, LadoDBChangeRunDone,
	}
}

// Retryable reports whether the developer may queue the run again.
func (s LadoDBChangeRunState) Retryable() bool {
	return s == LadoDBChangeRunAwaitingHuman || s == LadoDBChangeRunFailed || s == LadoDBChangeRunMergeFailed
}

// LadoDBChangeRun is one head's application of its pre-merge or after-deploy
// files (UNIQUE per PR, head and kind).
type LadoDBChangeRun struct {
	ID                 string
	ProjectID          ProjectID
	Repo               string
	ExecutionSessionID SessionID
	TaskID             LadoTaskID
	PRURL              string
	HeadSHA            string
	Kind               LadoDBChangeRunKind
	Trigger            LadoDBChangeTrigger
	State              LadoDBChangeRunState
	ApprovalID         string
	Attempts           int
	// Error is the last stop's reason, one line without secrets.
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// LadoDBChangeResult is how one file's application went.
type LadoDBChangeResult string

// Results. running is left by a daemon that stopped mid-command, until the
// next start marks it interrupted; baseline marks a file that was on the
// default branch when the settings were turned on, recorded as applied
// without running.
const (
	LadoDBChangeResultRunning     LadoDBChangeResult = "running"
	LadoDBChangeResultApplied     LadoDBChangeResult = "applied"
	LadoDBChangeResultFailed      LadoDBChangeResult = "failed"
	LadoDBChangeResultInterrupted LadoDBChangeResult = "interrupted"
	LadoDBChangeResultBaseline    LadoDBChangeResult = "baseline"
)

// CountsApplied reports whether the result makes the file applied: applied
// or baseline.
func (r LadoDBChangeResult) CountsApplied() bool {
	return r == LadoDBChangeResultApplied || r == LadoDBChangeResultBaseline
}

// LadoDBChangeApplication is one application of one SQL file, or a baseline
// row. ExitCode is nil until the command exited; OutputSHA256 names the blob
// with the end of its redacted output; Message is one line without secrets.
type LadoDBChangeApplication struct {
	ID                 string
	ProjectID          ProjectID
	Repo               string
	Path               string
	BlobSHA            string
	Phase              LadoDBChangePhase
	Result             LadoDBChangeResult
	RunID              string
	ExecutionSessionID SessionID
	TaskID             LadoTaskID
	PRURL              string
	HeadSHA            string
	ExitCode           *int
	OutputSHA256       string
	Message            string
	Actor              string
	StartedAt          time.Time
	FinishedAt         *time.Time
}

// Statuses of a file in a view (LadoDBChangeFileView.Status).
const (
	LadoDBChangeFilePending     = "pending"
	LadoDBChangeFileApplying    = "applying"
	LadoDBChangeFileApplied     = "applied"
	LadoDBChangeFileFailed      = "failed"
	LadoDBChangeFileInterrupted = "interrupted"
)

// LadoDBChangeFileView is one file of a PR head with how far it got.
type LadoDBChangeFileView struct {
	LadoDBChangeFileRef
	Status       string
	AppliedAt    *time.Time
	Attempts     int
	LastMessage  string
	OutputSHA256 string
}

// LadoDBChangePRView is one PR head's SQL files, the applied files it
// changes, and its runs.
type LadoDBChangePRView struct {
	PRURL    string
	HeadSHA  string
	Repo     string
	Files    []LadoDBChangeFileView
	Modified []LadoDBChangeModified
	Runs     []LadoDBChangeRun
}

// LadoDBChangeExecutionView is an execution's database changes, per PR.
type LadoDBChangeExecutionView struct {
	SessionID SessionID
	TaskID    LadoTaskID
	PRs       []LadoDBChangePRView
}

// LadoDBChangeBoardState is what a task's card shows of its database changes.
type LadoDBChangeBoardState string

// Board states.
const (
	LadoDBChangeBoardApplyWaiting       LadoDBChangeBoardState = "apply_waiting"
	LadoDBChangeBoardApplying           LadoDBChangeBoardState = "applying"
	LadoDBChangeBoardApplyFailed        LadoDBChangeBoardState = "apply_failed"
	LadoDBChangeBoardPreflightBlocked   LadoDBChangeBoardState = "preflight_blocked"
	LadoDBChangeBoardMergeFailed        LadoDBChangeBoardState = "merge_failed"
	LadoDBChangeBoardAfterDeployPending LadoDBChangeBoardState = "after_deploy_pending"
	LadoDBChangeBoardMergedUnapplied    LadoDBChangeBoardState = "merged_unapplied"
)

// LadoDBChangeSummary is one PR's database change state for the board.
type LadoDBChangeSummary struct {
	TaskID    LadoTaskID
	SessionID SessionID
	PRURL     string
	Repo      string
	State     LadoDBChangeBoardState
	Pending   int
	Failed    int
	Message   string
}

// LadoDBChangeFileContentMax caps the bytes of a file's content a view
// returns.
const LadoDBChangeFileContentMax = 256 << 10

// LadoDBChangeFileContent is a SQL file's content at a blob, cut at
// LadoDBChangeFileContentMax.
type LadoDBChangeFileContent struct {
	Path      string
	BlobSHA   string
	Content   string
	Truncated bool
}

// LadoDBChangeApplyRequest is the developer's request, with presence, to
// apply a PR head's files of one phase: the files the developer confirmed.
type LadoDBChangeApplyRequest struct {
	SessionID SessionID
	PRURL     string
	HeadSHA   string
	Phase     LadoDBChangePhase
	Files     []LadoDBChangeFileRef
}

// LadoDBChangeApplySubject is the presence subject of an apply request:
// "<session>@<head>:<phase>".
func LadoDBChangeApplySubject(session SessionID, head string, phase LadoDBChangePhase) string {
	return string(session) + "@" + head + ":" + string(phase)
}

// LadoDBChangeSettingsSubject is the presence subject of a settings change:
// "<project>:<repo>".
func LadoDBChangeSettingsSubject(project ProjectID, repo string) string {
	return string(project) + ":" + repo
}

// LadoMergeReadiness is what AO's merge path reads of a PR before it merges:
// its merge blockers (MergeBlocker*), the host's mergeability, the host's
// review decision and whether it merged already.
type LadoMergeReadiness struct {
	Blockers     []string
	Mergeability Mergeability
	Review       ReviewDecision
	Merged       bool
}

// LadoDBChangeStopperReviewRequired stops a database change run whose PR
// still lacks a review the host requires (GitHub's REVIEW_REQUIRED, GitLab's
// approvals_required). AO's merge blockers leave it to the host, which then
// refuses the merge after the SQL applied; LADO's status cannot supply it.
const LadoDBChangeStopperReviewRequired = "review_required"

// Audit actions of database changes. Their payloads name files, blobs and
// hashes, never a secret's value or a command's output.
const (
	LadoAuditDBChangesChanged = "project.db_changes_changed"
	LadoAuditDBChangeBaseline = "lado.db_change_baseline"
	LadoAuditDBChangeApplied  = "lado.db_change_applied"
	LadoAuditDBChangeFailed   = "lado.db_change_failed"
)

// The database change notice lines, after LadoNoticePrefix. Paths and
// reasons come from the repository and the host, so they are cleaned to one
// line without backticks before they go in.
const (
	ladoNoticeDBChangeFailed           = "%s운영 DB SQL 적용 실패 — %s, 병합하지 않았습니다. 앱에서 확인 후 다시 시도"
	ladoNoticeDBChangeMergeFailed      = "%s운영 DB SQL 은 적용됐지만 병합 실패 — 앱에서 병합하거나 다시 시도"
	ladoNoticeDBChangePreflightBlocked = "%s운영 DB SQL 적용 보류 — 병합할 수 없습니다(%s). SQL 은 적용하지 않았습니다"
	ladoNoticeDBChangeAfterDeploy      = "%s배포 뒤 적용할 SQL %d개 남음 — 배포가 끝나면 앱에서 적용"
	ladoNoticeDBChangeMergedOutside    = "%sLADO 밖에서 병합됨 — 병합 전 SQL %d개가 적용되지 않았습니다. 앱에서 적용"
	ladoNoticeDBChangeUnknown          = "%s운영 DB SQL 적용 여부 확인 불가 — 앱에서 확인"
)

// ladoDBNoticeRef is the "#<issue> " that starts a notice, "" without an
// issue number.
func ladoDBNoticeRef(issue int) string {
	if issue <= 0 {
		return ""
	}
	return fmt.Sprintf("#%d ", issue)
}

// LadoNoticeDBChangeFailed is the line for a SQL file whose application
// failed, so LADO did not merge.
func LadoNoticeDBChangeFailed(issue int, path string) string {
	return ladoNoticeLine(ladoNoticeDBChangeFailed, ladoDBNoticeRef(issue), ladoLogNoticeText(path))
}

// LadoNoticeDBChangeMergeFailed is the line for a PR whose pre-merge SQL was
// applied but whose merge failed.
func LadoNoticeDBChangeMergeFailed(issue int) string {
	return ladoNoticeLine(ladoNoticeDBChangeMergeFailed, ladoDBNoticeRef(issue))
}

// LadoNoticeDBChangePreflightBlocked is the line for a run the preflight
// stopped before any SQL ran, with its reasons, such as merge blockers.
func LadoNoticeDBChangePreflightBlocked(issue int, reasons []string) string {
	cleaned := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason = ladoLogNoticeText(reason); reason != "" {
			cleaned = append(cleaned, reason)
		}
	}
	text := strings.Join(cleaned, ", ")
	if text == "" {
		text = "알 수 없는 이유"
	}
	return ladoNoticeLine(ladoNoticeDBChangePreflightBlocked, ladoDBNoticeRef(issue), text)
}

// LadoNoticeDBChangeAfterDeploy is the line for a merged PR whose
// after-deploy SQL waits for the developer.
func LadoNoticeDBChangeAfterDeploy(issue, files int) string {
	return ladoNoticeLine(ladoNoticeDBChangeAfterDeploy, ladoDBNoticeRef(issue), files)
}

// LadoNoticeDBChangeMergedOutside is the line for a PR merged outside LADO
// with pre-merge SQL not applied.
func LadoNoticeDBChangeMergedOutside(issue, files int) string {
	return ladoNoticeLine(ladoNoticeDBChangeMergedOutside, ladoDBNoticeRef(issue), files)
}

// LadoNoticeDBChangeUnknown is the line for a merged PR whose SQL LADO could
// not check for an hour.
func LadoNoticeDBChangeUnknown(issue int) string {
	return ladoNoticeLine(ladoNoticeDBChangeUnknown, ladoDBNoticeRef(issue))
}

// LadoDBChangeMergeStoppers returns the blockers of a readiness that stop
// LADO from applying a head's SQL, since the merge could not follow: merged,
// closed, draft, failing CI, changes requested, unresolved comments, a
// conflict, and a review the host requires
// (LadoDBChangeStopperReviewRequired, last). What LADO's own lado/integration status, not posted yet, causes
// passes: pending, unknown or missing CI, a head mismatch the live read
// reports, a partial review and a not_mergeable that is no conflict
// (blocked, unstable, unknown). Nil means LADO may apply the SQL.
func LadoDBChangeMergeStoppers(r LadoMergeReadiness) []string {
	var stoppers []string
	for _, blocker := range r.Blockers {
		switch blocker {
		case MergeBlockerMerged, MergeBlockerClosed, MergeBlockerDraft, MergeBlockerCIFailing,
			MergeBlockerChangesRequested, MergeBlockerUnresolvedComments:
			if !slices.Contains(stoppers, blocker) {
				stoppers = append(stoppers, blocker)
			}
		case MergeBlockerNotMergeable:
			if r.Mergeability == MergeConflicting && !slices.Contains(stoppers, blocker) {
				stoppers = append(stoppers, blocker)
			}
		}
	}
	if r.Review == ReviewRequired {
		stoppers = append(stoppers, LadoDBChangeStopperReviewRequired)
	}
	if r.Merged && !slices.Contains(stoppers, MergeBlockerMerged) {
		stoppers = append([]string{MergeBlockerMerged}, stoppers...)
	}
	return stoppers
}
