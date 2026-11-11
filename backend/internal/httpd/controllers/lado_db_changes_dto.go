package controllers

// LADO fork: DTOs of the production database changes tied to the
// integration approval (#97, docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md).
// Times are RFC 3339. No DTO carries a secret's value: requests set and
// remove values by name, responses list only the names whose values LADO
// stores.

import "time"

// LadoDBChangeFileParams name one SQL file of an execution by its PR and blob.
type LadoDBChangeFileParams struct {
	SessionID string `path:"sessionId" description:"AO session id of the worker."`
	PRURL     string `query:"prUrl" required:"true" description:"The execution's PR the file belongs to."`
	BlobSHA   string `query:"blobSha" required:"true" description:"The file's blob, as the execution's database changes list it; LADO serves no other blob."`
}

// LadoDBChangeRepoSettingsResponse is one repository's production database
// change settings.
type LadoDBChangeRepoSettingsResponse struct {
	Repo              string     `json:"repo" description:"The child repository's folder in a workspace project; empty in a single-repository project."`
	Name              string     `json:"name" description:"The repository's display name."`
	Enabled           bool       `json:"enabled"`
	Paths             []string   `json:"paths" description:"Globs of the SQL files, relative to the repository's root."`
	Command           string     `json:"command" description:"The line the shell runs for each file, with {file} for its path."`
	TimeoutMinutes    int        `json:"timeoutMinutes" description:"The command's time limit for one file; 0 is the default of 10 minutes."`
	SecretNames       []string   `json:"secretNames" description:"The environment variables the command gets from the repository's secrets file."`
	StoredSecretNames []string   `json:"storedSecretNames" description:"The secret names whose values LADO stores; the values never leave the daemon."`
	BaselineCommit    string     `json:"baselineCommit,omitempty" description:"The default branch's commit whose matching files LADO recorded as already applied when the settings were turned on or their paths changed."`
	EnabledAt         *time.Time `json:"enabledAt,omitempty"`
	UpdatedAt         *time.Time `json:"updatedAt,omitempty" description:"Omitted for a repository that never saved settings."`
	SHA256            string     `json:"sha256" description:"The hash of the repository's saved settings, which a change sends as base; empty for a repository that never saved any."`
}

// LadoDBChangeSettingsResponse is a project's production database change
// settings: one entry per repository that may have them, the project's root
// or each child repository of a workspace.
type LadoDBChangeSettingsResponse struct {
	ProjectID    string                             `json:"projectId"`
	Repositories []LadoDBChangeRepoSettingsResponse `json:"repositories"`
	SHA256       string                             `json:"sha256" description:"The hash of every repository's settings, in order."`
}

// LadoDBChangeSecretsRequest sets secret values by name and removes others.
type LadoDBChangeSecretsRequest struct {
	Set    map[string]string `json:"set,omitempty" description:"Values to store by secret name; each name must be one of secretNames. The values are written to a file under ~/.ao only and never returned."`
	Remove []string          `json:"remove,omitempty" description:"Secret names whose stored values are deleted."`
}

// UpdateLadoDBChangeSettingsRequest replaces one repository's production
// database change settings. It needs a human-presence proof in
// X-LADO-Presence over these exact bytes for project.db_changes on
// <projectId>:<repo>, which the desktop app adds after the developer
// confirms.
type UpdateLadoDBChangeSettingsRequest struct {
	Repo           string                     `json:"repo" description:"The child repository's folder in a workspace project; empty in a single-repository project."`
	Enabled        bool                       `json:"enabled"`
	Paths          []string                   `json:"paths" description:"1 to 10 globs relative to the repository, none absolute or with a '..' segment."`
	Command        string                     `json:"command" description:"One line of at most 2000 characters with {file}."`
	TimeoutMinutes int                        `json:"timeoutMinutes,omitempty" minimum:"0" maximum:"120" description:"1 to 120; 0 or omitted is the default of 10."`
	SecretNames    []string                   `json:"secretNames" description:"At most 10 environment variable names of ^[A-Z_][A-Z0-9_]{0,63}$; names LADO withholds or sets for every command, such as PATH or GIT_SSH_COMMAND, are refused."`
	Secrets        LadoDBChangeSecretsRequest `json:"secrets,omitempty"`
	Base           string                     `json:"base,omitempty" description:"The repository's sha256 from the settings the change started from; empty for a repository that never saved any. Other settings refuse the change with LADO_DB_CHANGE_SETTINGS_CHANGED."`
}

// LadoDBChangeSummaryResponse is one PR's database change state for the
// board.
type LadoDBChangeSummaryResponse struct {
	TaskID    string `json:"taskId"`
	SessionID string `json:"sessionId"`
	PRURL     string `json:"prUrl"`
	Repo      string `json:"repo"`
	State     string `json:"state" enum:"apply_waiting,applying,apply_failed,preflight_blocked,merge_failed,after_deploy_pending,merged_unapplied" description:"apply_waiting: approved SQL waits for LADO's loop; preflight_blocked: LADO did not apply the SQL since the PR could not merge; merged_unapplied: the PR merged outside LADO with pre-merge SQL not applied; after_deploy_pending: after-deploy SQL waits for the developer."`
	Pending   int    `json:"pending" description:"How many of the PR's files are not applied yet."`
	Failed    int    `json:"failed" description:"How many of the PR's files failed their last application."`
	Message   string `json:"message,omitempty" description:"The last stop's reason, one line without secrets."`
}

// LadoDBChangeSummaryListResponse is a project's PRs with database changes
// that still need something.
type LadoDBChangeSummaryListResponse struct {
	Summaries []LadoDBChangeSummaryResponse `json:"summaries"`
}

// LadoDBChangeFileRefBody is one SQL file of a PR head: its path, its blob and
// its phase.
type LadoDBChangeFileRefBody struct {
	Path    string `json:"path" minLength:"1"`
	BlobSHA string `json:"blobSha" minLength:"40"`
	Phase   string `json:"phase" enum:"pre_merge,after_deploy" description:"after_deploy: the file's first line that is not blank is '-- lado:after-deploy', so LADO applies it only by the developer's button after the merge."`
}

// LadoDBChangeModifiedResponse is an applied SQL file the head changes or
// deletes; LADO never runs it again.
type LadoDBChangeModifiedResponse struct {
	Path           string `json:"path"`
	AppliedBlobSHA string `json:"appliedBlobSha" description:"The blob LADO applied or recorded as the baseline."`
	HeadBlobSHA    string `json:"headBlobSha" description:"The head's blob; empty when deleted."`
	Deleted        bool   `json:"deleted"`
}

// LadoDBChangeFileResponse is one SQL file of a PR head with how far it got.
type LadoDBChangeFileResponse struct {
	Path         string     `json:"path"`
	BlobSHA      string     `json:"blobSha"`
	Phase        string     `json:"phase" enum:"pre_merge,after_deploy"`
	Status       string     `json:"status" enum:"pending,applying,applied,failed,interrupted" description:"interrupted: the daemon stopped while the command ran, so whether the SQL took effect is unknown; LADO treats the file as not applied."`
	AppliedAt    *time.Time `json:"appliedAt,omitempty"`
	Attempts     int        `json:"attempts"`
	LastMessage  string     `json:"lastMessage,omitempty" description:"The last application's one-line message, without secrets."`
	OutputSHA256 string     `json:"outputSha256,omitempty" description:"The hash of the last application's redacted output."`
}

// LadoDBChangeRunResponse is one head's application of its pre-merge or
// after-deploy files.
type LadoDBChangeRunResponse struct {
	ID         string    `json:"id"`
	Repo       string    `json:"repo"`
	SessionID  string    `json:"sessionId"`
	TaskID     string    `json:"taskId"`
	PRURL      string    `json:"prUrl"`
	HeadSHA    string    `json:"headSha"`
	Kind       string    `json:"kind" enum:"pre_merge,after_deploy"`
	Trigger    string    `json:"trigger" enum:"approval,merged_unapplied,after_merge" description:"approval: the integration approval; merged_unapplied: the PR merged outside LADO with pre-merge SQL not applied; after_merge: after-deploy SQL waits after the merge."`
	State      string    `json:"state" enum:"awaiting_human,queued,applying,failed,applied,merging,merge_failed,done" description:"awaiting_human waits for the developer's button; failed and merge_failed wait for a retry."`
	ApprovalID string    `json:"approvalId,omitempty"`
	Attempts   int       `json:"attempts"`
	Error      string    `json:"error,omitempty" description:"The last stop's reason, one line without secrets, such as the merge blockers a preflight found."`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// LadoDBChangePRResponse is one PR head's SQL files, the applied files it
// changes, and its runs.
type LadoDBChangePRResponse struct {
	PRURL    string                         `json:"prUrl"`
	HeadSHA  string                         `json:"headSha"`
	Repo     string                         `json:"repo"`
	Files    []LadoDBChangeFileResponse     `json:"files"`
	Modified []LadoDBChangeModifiedResponse `json:"modified"`
	Runs     []LadoDBChangeRunResponse      `json:"runs"`
}

// LadoDBChangeExecutionResponse is an execution's database changes, per PR.
type LadoDBChangeExecutionResponse struct {
	SessionID string                   `json:"sessionId"`
	TaskID    string                   `json:"taskId"`
	PRs       []LadoDBChangePRResponse `json:"prs"`
}

// LadoDBChangeFileContentResponse is a SQL file's content at a blob.
type LadoDBChangeFileContentResponse struct {
	Path      string `json:"path"`
	BlobSHA   string `json:"blobSha"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated" description:"LADO cut the content at 256 KiB."`
}

// ApplyLadoDBChangesRequest asks LADO to apply a PR head's files of one phase:
// the after-deploy files, the pre-merge files of a PR merged outside LADO, or
// a retry of a run that stopped. It needs a human-presence proof in
// X-LADO-Presence over these exact bytes for db_change.apply on
// <sessionId>@<headSha>:<phase>, which the desktop app adds after the
// developer confirms the files.
type ApplyLadoDBChangesRequest struct {
	PRURL   string                    `json:"prUrl" minLength:"1"`
	HeadSHA string                    `json:"headSha" minLength:"40"`
	Phase   string                    `json:"phase" enum:"pre_merge,after_deploy"`
	Files   []LadoDBChangeFileRefBody `json:"files" description:"The files the developer confirmed, all of the phase; others than the run's refuse the request."`
}
