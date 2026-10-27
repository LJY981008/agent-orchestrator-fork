package controllers

// LADO fork: DTOs for verification, the integration approval, the check
// commands' runs and the protection of a project's base branches (step 8
// plan §15; docs/LJY-PRD/TDD.md §7, §8 and §13).

import "time"

// LadoCheckRunLogParams name one check run of an execution.
type LadoCheckRunLogParams struct {
	SessionID  string `path:"sessionId" description:"AO session id of the worker."`
	CheckRunID string `path:"checkRunId" description:"The check run, as the commands item's details name it."`
}

// LadoProjectProtectionQuery asks for a fresh read of the rules.
type LadoProjectProtectionQuery struct {
	Refresh bool `query:"refresh,omitempty" description:"Read the rules from the host past LADO's ten-minute cache."`
}

// LadoVerifyCheckResponse is one CI check recorded for the head.
type LadoVerifyCheckResponse struct {
	Name     string `json:"name"`
	Status   string `json:"status" description:"AO's status of the check: passed, failed, cancelled, skipped, queued, in_progress or unknown."`
	Required bool   `json:"required" description:"The base branch's rules require the check. When they name none, every check counts as required."`
}

// LadoVerifyCommandRunResponse is the newest run of one of the project's
// check commands on the head.
type LadoVerifyCommandRunResponse struct {
	Name       string `json:"name"`
	CheckRunID string `json:"checkRunId,omitempty" description:"The run, for its log; omitted while the command has not run on the head."`
	Status     string `json:"status,omitempty" enum:"running,passed,failed,timed_out,cancelled,error" description:"Omitted while the command has not run on the head. A cancelled run runs again."`
	ExitCode   *int   `json:"exitCode" description:"null until the command exited."`
	DurationMS int64  `json:"durationMs" description:"How long the finished run took; 0 while it runs."`
}

// LadoVerifyCommitResponse is one commit after the merge-base, judged by the
// Korean commit message validator.
type LadoVerifyCommitResponse struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Result  string `json:"result" enum:"ok,not_korean,unclear,body_unclear" description:"body_unclear: the subject is Korean and the body reads as English."`
}

// LadoVerifyTextResponse is the PR title, judged like a commit subject: it
// becomes the squash commit's subject.
type LadoVerifyTextResponse struct {
	Text   string `json:"text"`
	Result string `json:"result" enum:"ok,not_korean,unclear"`
}

// LadoReflogEntryResponse is one entry of the local base branch's reflog
// that the remote base branch does not hold.
type LadoReflogEntryResponse struct {
	Selector string `json:"selector" description:"The entry's reflog selector."`
	Commit   string `json:"commit"`
	Subject  string `json:"subject" description:"The reflog entry's message."`
}

// LadoEvidenceRefResponse is one record an evidence entry names.
type LadoEvidenceRefResponse struct {
	Kind string `json:"kind" enum:"pr_check,review_run,artifact_version,lado_check_run,commit,claim,none" description:"claim: the entry matched no record of the head."`
	Key  string `json:"key" description:"pr_check: the check's name; lado_check_run: the check run id; commit: the commit; claim: what the entry named."`
	SHA  string `json:"sha" description:"The head for a check or a check run, the commit for a commit; empty for a claim."`
}

// LadoEvidenceCriterionResponse is the evidence of one acceptance criterion
// of the approved plan.
type LadoEvidenceCriterionResponse struct {
	Index int                       `json:"index" description:"The criterion's 1-based position in the plan's acceptanceCriteria."`
	State string                    `json:"state" enum:"linked,stale,claimed,inferred" description:"linked: an entry names a record of the head; stale: the evidence file describes another head; claimed: entries, none of them a record; inferred: the file does not list the criterion."`
	Refs  []LadoEvidenceRefResponse `json:"refs"`
}

// LadoVerifyItemDetailsResponse is an item's details. Which fields appear
// depends on the item's kind; an omitted field is empty, false or 0.
type LadoVerifyItemDetailsResponse struct {
	// required_checks
	Required    []string                  `json:"required,omitempty" description:"required_checks: the check names the base branch's rules require."`
	RulesetRead bool                      `json:"rulesetRead,omitempty" description:"required_checks: LADO read the rules, now or before."`
	Checks      []LadoVerifyCheckResponse `json:"checks,omitempty" description:"required_checks: every check recorded for the head, lado/integration left out."`
	// commands
	Runs []LadoVerifyCommandRunResponse `json:"runs,omitempty" description:"commands: each check command's newest run on the head."`
	// commit_messages
	Commits []LadoVerifyCommitResponse `json:"commits,omitempty" description:"commit_messages: the commits after the merge-base."`
	PRTitle *LadoVerifyTextResponse    `json:"prTitle,omitempty" description:"commit_messages: the PR title."`
	// paths
	Protected    []string `json:"protected,omitempty" description:"paths: changed paths a protected glob matches that the plan does not name."`
	OutsidePlan  []string `json:"outsidePlan,omitempty" description:"paths: the first 50 changed paths no affected_paths entry matches."`
	OutsideCount int      `json:"outsideCount,omitempty" description:"paths: how many changed paths are outside the plan."`
	// base_branch and protection
	Branch        string                    `json:"branch,omitempty" description:"base_branch and protection: the base branch."`
	Remote        string                    `json:"remote,omitempty" description:"base_branch: the base branch's remote."`
	Moved         []LadoReflogEntryResponse `json:"moved,omitempty" description:"base_branch: local moves of the base branch since the run started that the remote does not hold."`
	AlreadyOnBase bool                      `json:"alreadyOnBase,omitempty" description:"base_branch: the remote base held the head before any approval."`
	// protection
	Provider       string   `json:"provider,omitempty" enum:"github,gitlab" description:"protection: the repository's host."`
	Repo           string   `json:"repo,omitempty" description:"protection: the repository, as owner/name."`
	Missing        []string `json:"missing,omitempty" description:"protection: what the base branch's rules lack: pull_request, required_status_checks:lado/integration, non_fast_forward, deletion."`
	RequiredChecks []string `json:"requiredChecks,omitempty" description:"protection: the checks the rules require."`
	Error          string   `json:"error,omitempty" description:"protection: why the rules could not be read, without secrets."`
	// review
	Rounds  int    `json:"rounds,omitempty" description:"review: the review rounds LADO started for the execution, at most 3."`
	RunID   string `json:"runId,omitempty" description:"review: the review run that decides."`
	Verdict string `json:"verdict,omitempty" description:"review: that run's verdict, approved or changes_requested."`
	Harness string `json:"harness,omitempty" description:"review: the reviewer harness."`
	Note    string `json:"note,omitempty" description:"review: what LADO noted, such as AO choosing the reviewer."`
	// evidence
	FileVersion int                             `json:"fileVersion,omitempty" description:"evidence: the pinned version of lado/evidence.md; omitted without one."`
	FileSHA256  string                          `json:"fileSha256,omitempty"`
	FileHead    string                          `json:"fileHead,omitempty" description:"evidence: the head the file says it describes."`
	Criteria    []LadoEvidenceCriterionResponse `json:"criteria,omitempty" description:"evidence: each acceptance criterion's evidence."`
}

// LadoVerifyItemResponse is one item of a verification record, in the
// record's fixed order.
type LadoVerifyItemResponse struct {
	Kind    string                        `json:"kind" enum:"required_checks,commands,commit_messages,paths,base_branch,protection,review,evidence"`
	Outcome string                        `json:"outcome" enum:"pass,wait,fail,confirm,info" description:"info never decides; evidence is always info."`
	Code    string                        `json:"code,omitempty" enum:"not_required,verifying,checks_pending,checks_failed,none_reported,commands_running,worktree_not_at_head,commands_failed,command_timed_out,worktree_missing,no_tests,none_configured,commit_messages_failed,commit_messages_unclear,protected_path,outside_plan,base_moved,reflog_off,already_on_base,no_local_branch,unprotected,protection_unknown,unprotected_gitlab,review_pending,changes_requested,review_inconclusive" description:"Omitted for a plain pass. not_required: the item does not apply to the execution; verifying: LADO could not compute the item yet."`
	Details LadoVerifyItemDetailsResponse `json:"details"`
}

// LadoVerificationResponse is what LADO verified about one head of the
// execution's PR (docs/LJY-PRD/TDD.md §8).
type LadoVerificationResponse struct {
	HeadSHA string `json:"headSha"`
	PRURL   string `json:"prUrl"`
	Outcome string `json:"outcome" enum:"pass,wait,fail,confirm" description:"fail if any item fails, else wait if any waits, else confirm if any needs confirming, else pass."`
	Reason  string `json:"reason,omitempty" enum:"verifying,checks_pending,checks_failed,commands_running,worktree_not_at_head,commands_failed,command_timed_out,worktree_missing,commit_messages_failed,protected_path,review_pending,changes_requested" description:"The code of the first item that fails, else of the first that waits; omitted otherwise."`
	SHA256  string `json:"sha256" description:"The record's hash, which an integration approval signs as verificationSha256."`
	// Items follow the record's order: required_checks, commands,
	// commit_messages, paths, base_branch, protection, review, evidence.
	Items      []LadoVerifyItemResponse        `json:"items"`
	Evidence   []LadoEvidenceCriterionResponse `json:"evidence" description:"Each acceptance criterion's evidence, from the evidence item."`
	ComputedAt time.Time                       `json:"computedAt"`
}

// LadoIntegrationApprovalResponse is the developer's integration approval of
// one head.
type LadoIntegrationApprovalResponse struct {
	ID                 string     `json:"id"`
	HeadSHA            string     `json:"headSha"`
	VerificationSHA256 string     `json:"verificationSha256" description:"The record hash the approval signed."`
	Acknowledged       []string   `json:"acknowledged" description:"The confirm codes the developer acknowledged, sorted."`
	Actor              string     `json:"actor" description:"human, or human:dev-presence for a proof made without the desktop app's key."`
	ApprovedAt         time.Time  `json:"approvedAt"`
	StatusState        string     `json:"statusState" enum:"pending,posted,failed,not_applicable" description:"How posting the lado/integration commit status went; not_applicable on GitLab, which gets none."`
	StatusAttempts     int        `json:"statusAttempts" description:"Attempts to post the status so far; LADO stops after 12."`
	StatusPostedAt     *time.Time `json:"statusPostedAt,omitempty"`
	StatusError        string     `json:"statusError,omitempty" description:"Why the last post failed, without secrets."`
}

// ApproveLadoIntegrationRequest approves the integration of one head of the
// execution's PR, or previews it with dryRun. A real approval needs a
// human-presence proof in X-LADO-Presence over these exact bytes, for
// <sessionId>@<headSha>, which the desktop app adds after the developer
// confirms.
type ApproveLadoIntegrationRequest struct {
	PRURL              string   `json:"prUrl" minLength:"1"`
	HeadSHA            string   `json:"headSha" minLength:"40" description:"The head the developer approves; another live head refuses the approval."`
	VerificationSHA256 string   `json:"verificationSha256,omitempty" description:"The hash of the record the developer saw, from the dry run; another record refuses the approval. A dry run needs none."`
	Acknowledge        []string `json:"acknowledge,omitempty" description:"The confirm codes the developer acknowledged: exactly the record's, from the dry run's confirm. A dry run needs none."`
	DryRun             bool     `json:"dryRun,omitempty" description:"Read the PR's live head, the base branch and its rules again and answer the head's record, its hash and the codes to confirm, without a presence proof and without approving."`
}

// LadoIntegrationApproveResponse is the head's record as the approval or its
// dry run computed it, with what an approval acknowledges and, once
// approved, the approval.
type LadoIntegrationApproveResponse struct {
	Verification       LadoVerificationResponse         `json:"verification"`
	VerificationSHA256 string                           `json:"verificationSha256"`
	Confirm            []string                         `json:"confirm" description:"The record's confirm codes, sorted: what an approval sends as acknowledge."`
	Approval           *LadoIntegrationApprovalResponse `json:"approval,omitempty" description:"The recorded approval; omitted for a dry run."`
}

// LadoRunChecksResponse is what running the check commands again set off.
type LadoRunChecksResponse struct {
	HeadSHA  string   `json:"headSha" description:"The head the commands run on."`
	Commands []string `json:"commands" description:"The commands that run, in order: all of the project's but those still running, which finish first. Empty when the project has none."`
}

// LadoBranchProtectionResponse is the protection of one repository's base
// branch.
type LadoBranchProtectionResponse struct {
	Repository     string     `json:"repository" description:"Canonical id of the repository, such as github:owner/repo."`
	Branch         string     `json:"branch" description:"The base branch workers start from; empty when LADO could not tell it."`
	State          string     `json:"state" enum:"protected,unprotected,unknown,unsupported" description:"protected: the rules require a pull request, the lado/integration status, no force pushes and no deletion; unknown: they could not be read; unsupported: LADO does not read GitLab's protection, it only detects."`
	Missing        []string   `json:"missing" description:"What the rules lack, for unprotected: pull_request, required_status_checks:lado/integration, non_fast_forward, deletion."`
	RequiredChecks []string   `json:"requiredChecks" description:"The checks the rules require, lado/integration left out."`
	CheckedAt      *time.Time `json:"checkedAt,omitempty" description:"When LADO asked the host."`
	Error          string     `json:"error,omitempty" description:"Why the rules could not be read, without secrets."`
}

// LadoProjectProtectionResponse lists the protection of the base branch of
// each of a project's repositories.
type LadoProjectProtectionResponse struct {
	ProjectID    string                         `json:"projectId"`
	Repositories []LadoBranchProtectionResponse `json:"repositories"`
}
