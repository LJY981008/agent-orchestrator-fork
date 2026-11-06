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
	Runs       []LadoVerifyCommandRunResponse `json:"runs,omitempty" description:"commands: each check command's newest run on the head."`
	Dirty      []string                       `json:"dirty,omitempty" description:"commands: for worktree_dirty, the first 5 changed or untracked paths of the worktree, relative to its root (the new name of a rename)."`
	DirtyCount int                            `json:"dirtyCount,omitempty" description:"commands: for worktree_dirty, how many paths keep the worktree from being clean."`
	// commit_messages
	Commits []LadoVerifyCommitResponse `json:"commits,omitempty" description:"commit_messages: the commits after the merge-base."`
	PRTitle *LadoVerifyTextResponse    `json:"prTitle,omitempty" description:"commit_messages: the PR title."`
	// paths
	Protected    []string `json:"protected,omitempty" description:"paths: changed paths a protected glob matches that the plan does not name."`
	OutsidePlan  []string `json:"outsidePlan,omitempty" description:"paths: the first 50 changed paths no affected_paths entry matches."`
	OutsideCount int      `json:"outsideCount,omitempty" description:"paths: how many changed paths are outside the plan."`
	// base_branch and protection
	Branch         string                    `json:"branch,omitempty" description:"base_branch and protection: the base branch."`
	Remote         string                    `json:"remote,omitempty" description:"base_branch: the base branch's remote."`
	Moved          []LadoReflogEntryResponse `json:"moved,omitempty" description:"base_branch: local moves of the base branch since the run started that the remote does not hold."`
	AlreadyOnBase  bool                      `json:"alreadyOnBase,omitempty" description:"base_branch: the remote base held the head before any approval."`
	DispatchCommit string                    `json:"dispatchCommit,omitempty" description:"base_branch: the local base branch's commit when the run was dispatched; omitted when it had none or LADO did not record it."`
	Deleted        bool                      `json:"deleted,omitempty" description:"base_branch: the local base branch existed at dispatch and is gone."`
	Advanced       bool                      `json:"advanced,omitempty" description:"base_branch: the remote base branch moved past the head's merge base."`
	AdvancedTip    string                    `json:"advancedTip,omitempty" description:"base_branch: the remote base branch's commit LADO compared the head with."`
	Overlap        []string                  `json:"overlap,omitempty" description:"base_branch: the first files changed on the remote base branch since the head's merge base that the head changed or the plan names."`
	OverlapCount   int                       `json:"overlapCount,omitempty" description:"base_branch: how many files changed on the remote base branch overlap the head or the plan."`
	PRRepo         string                    `json:"prRepo,omitempty" description:"base_branch: for pr_repo_not_in_workspace, the PR's repository, as provider:[host/]owner/name."`
	DefaultBranch  string                    `json:"defaultBranch,omitempty" description:"base_branch: for base_not_default, the default branch of the PR's repository."`
	DispatchBranch string                    `json:"dispatchBranch,omitempty" description:"base_branch: for base_not_default, the base branch the dispatcher chose in the issue's repository."`
	// protection
	Provider       string   `json:"provider,omitempty" enum:"github,gitlab" description:"protection: the repository's host."`
	Repo           string   `json:"repo,omitempty" description:"protection: the repository, as owner/name."`
	Missing        []string `json:"missing,omitempty" description:"protection: what the base branch's rules lack. GitHub: pull_request, required_status_checks:lado/integration (or required_status_checks:lado/integration:any_source when the rules take the check from one app only, which LADO's status cannot satisfy), non_fast_forward, deletion. GitLab: protected_branch, push_access (someone may push), force_push, pipeline_must_succeed (Pipelines must succeed is off), skipped_pipeline (skipped pipelines count as successful), integration_status (LADO cannot post its status: the token is below Developer, lacks the api scope, or LADO's last post failed)."`
	RequiredChecks []string `json:"requiredChecks,omitempty" description:"protection: the checks the rules require."`
	ErrorClass     string   `json:"errorClass,omitempty" description:"protection: the class of a failed read of the rules, which the record's hash covers: http_<status>, rate_limited, transport or other."`
	Error          string   `json:"error,omitempty" description:"protection: why the rules could not be read, without secrets; outside the record's hash."`
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
	Code    string                        `json:"code,omitempty" enum:"not_required,verifying,checks_pending,checks_failed,none_reported,commands_running,worktree_not_at_head,worktree_dirty,worktree_changed,commands_failed,command_timed_out,worktree_missing,no_tests,none_configured,head_unavailable,commit_messages_failed,commit_messages_unclear,protected_path,outside_plan,base_moved,base_deleted,reflog_off,already_on_base,no_local_branch,base_advanced,base_overlap,pr_repo_not_in_workspace,base_not_default,unprotected,protection_unknown,unprotected_gitlab,review_pending,changes_requested,review_inconclusive" description:"Omitted for a plain pass. not_required: the item does not apply to the execution; verifying: LADO could not compute the item yet; worktree_dirty: the worktree is at the head but has uncommitted changes or untracked files before the head's first check command, so the commands wait (details.dirty lists them); worktree_changed: the worktree changed since LADO's last check command on the head; head_unavailable: the head is not in the local repository and LADO could not fetch it; base_deleted: the local base branch that existed at dispatch is gone; base_advanced: the remote base branch moved past the head's merge base without touching its files; base_overlap: it moved with a change to files the head changed or the plan names, so the head must take in the base; pr_repo_not_in_workspace: the PR is open in a repository that is none of the workspace's child repositories, so LADO cannot verify it; base_not_default: the PR's repository is not the issue's and the PR's target or the dispatch's base branch is not that repository's default branch."`
	Details LadoVerifyItemDetailsResponse `json:"details"`
}

// LadoVerificationResponse is what LADO verified about one head of the
// execution's PR (docs/LJY-PRD/TDD.md §8).
type LadoVerificationResponse struct {
	HeadSHA string `json:"headSha"`
	PRURL   string `json:"prUrl"`
	Outcome string `json:"outcome" enum:"pass,wait,fail,confirm" description:"fail if any item fails, else wait if any waits, else confirm if any needs confirming, else pass."`
	Reason  string `json:"reason,omitempty" enum:"verifying,checks_pending,checks_failed,commands_running,worktree_not_at_head,worktree_dirty,worktree_changed,head_unavailable,commands_failed,command_timed_out,worktree_missing,commit_messages_failed,protected_path,base_overlap,pr_repo_not_in_workspace,review_pending,changes_requested" description:"The code of the first item that fails, else of the first that waits; omitted otherwise."`
	SHA256  string `json:"sha256" description:"The record's hash, which an integration approval signs as verificationSha256."`
	// Items follow the record's order: required_checks, commands,
	// commit_messages, paths, base_branch, protection, review, evidence.
	Items      []LadoVerifyItemResponse        `json:"items"`
	Evidence   []LadoEvidenceCriterionResponse `json:"evidence" description:"Each acceptance criterion's evidence, from the evidence item."`
	ComputedAt time.Time                       `json:"computedAt"`
	// UnresolvedReviewThreads is read for the execution view only, outside
	// the record and its hash (LADO #85).
	UnresolvedReviewThreads int `json:"unresolvedReviewThreads,omitempty" description:"How many review threads of the PR are unresolved now and keep it from being ready to merge. Only the execution view fills it; it is outside the record and its hash, and omitted when there are none."`
}

// LadoIntegrationApprovalResponse is the developer's integration approval of
// one head.
type LadoIntegrationApprovalResponse struct {
	ID                 string     `json:"id"`
	HeadSHA            string     `json:"headSha"`
	TargetBranch       string     `json:"targetBranch" description:"The branch the PR merged into when the head was approved; the approval holds for that branch only."`
	VerificationSHA256 string     `json:"verificationSha256" description:"The record hash the approval signed."`
	Acknowledged       []string   `json:"acknowledged" description:"The confirm codes the developer acknowledged, sorted."`
	Actor              string     `json:"actor" description:"human, or human:dev-presence for a proof made without the desktop app's key."`
	ApprovedAt         time.Time  `json:"approvedAt"`
	StatusState        string     `json:"statusState" enum:"pending,posted,failed,not_applicable" description:"How posting the lado/integration commit status went; not_applicable on GitLab approvals given before LADO posted statuses there."`
	StatusAttempts     int        `json:"statusAttempts" description:"Attempts to post the status so far; LADO stops after 12."`
	StatusPostedAt     *time.Time `json:"statusPostedAt,omitempty"`
	StatusError        string     `json:"statusError,omitempty" description:"Why the last post failed, without secrets."`
}

// ApproveLadoIntegrationPR is one PR head of a task's integration approval.
type ApproveLadoIntegrationPR struct {
	PRURL              string   `json:"prUrl" minLength:"1"`
	HeadSHA            string   `json:"headSha" minLength:"40" description:"The head the developer approves; another live head refuses the approval."`
	VerificationSHA256 string   `json:"verificationSha256,omitempty" description:"The hash of the head's record the developer saw, from the dry run; another record refuses the approval. A dry run needs none."`
	Acknowledge        []string `json:"acknowledge,omitempty" description:"The head record's confirm codes the developer acknowledged, from the dry run's confirm. A dry run needs none."`
}

// ApproveLadoIntegrationRequest approves the integration of the heads of
// the execution's open PRs together, or previews it with dryRun: prs names
// them, or prUrl and headSha name the one PR of an execution LADO tracks one
// open PR for. A real approval needs a human-presence proof in
// X-LADO-Presence over these exact bytes, for <sessionId>@<heads>: the heads
// joined by "," in the PRs' merge order (one head for one PR), which the
// desktop app adds after the developer confirms.
type ApproveLadoIntegrationRequest struct {
	PRs                []ApproveLadoIntegrationPR `json:"prs,omitempty" description:"The PRs approved together: every open PR LADO tracks for the execution (#64), else LADO_INTEGRATION_PRS_CHANGED; a dry run may name any of them. Leave prUrl, headSha, verificationSha256 and acknowledge out when sending it."`
	PRURL              string                     `json:"prUrl,omitempty" minLength:"1" description:"The PR approved, without prs."`
	HeadSHA            string                     `json:"headSha,omitempty" minLength:"40" description:"The head the developer approves, without prs; another live head refuses the approval."`
	VerificationSHA256 string                     `json:"verificationSha256,omitempty" description:"The hash of the record the developer saw, from the dry run; another record refuses the approval. A dry run needs none."`
	Acknowledge        []string                   `json:"acknowledge,omitempty" description:"The confirm codes the developer acknowledged: exactly the record's, from the dry run's confirm. A dry run needs none."`
	DryRun             bool                       `json:"dryRun,omitempty" description:"Read each PR's live head, the base branch and its rules again and answer each head's record, its hash and the codes to confirm, without a presence proof and without approving."`
}

// LadoIntegrationApprovePRResponse is one PR head's part of an integration
// approval or its dry run.
type LadoIntegrationApprovePRResponse struct {
	PRURL              string                           `json:"prUrl"`
	Verification       LadoVerificationResponse         `json:"verification"`
	VerificationSHA256 string                           `json:"verificationSha256"`
	Confirm            []string                         `json:"confirm" description:"The record's confirm codes, sorted: what an approval sends as this PR's acknowledge."`
	Approval           *LadoIntegrationApprovalResponse `json:"approval,omitempty" description:"The head's approval: recorded now, or recorded before for a head approved already; omitted for a dry run."`
}

// LadoIntegrationApproveResponse is each head's record as the approval or
// its dry run computed it, with what an approval acknowledges and, once
// approved, the approval: the first PR's in merge order in the top fields,
// each PR's in prs.
type LadoIntegrationApproveResponse struct {
	Verification       LadoVerificationResponse           `json:"verification"`
	VerificationSHA256 string                             `json:"verificationSha256"`
	Confirm            []string                           `json:"confirm" description:"The record's confirm codes, sorted: what an approval sends as acknowledge."`
	Approval           *LadoIntegrationApprovalResponse   `json:"approval,omitempty" description:"The recorded approval; omitted for a dry run."`
	PRs                []LadoIntegrationApprovePRResponse `json:"prs,omitempty" description:"Each PR the request named, in merge order; one for a request that named one."`
}

// LadoIntegrationStatusQuery names the PR whose status is posted again.
type LadoIntegrationStatusQuery struct {
	PRURL string `query:"prUrl,omitempty" description:"The open PR LADO tracks for the execution whose approved head's status is posted again; the first in merge order when omitted."`
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
	State          string     `json:"state" enum:"protected,unprotected,unknown,unsupported" description:"protected: on GitHub the rules require a pull request, the lado/integration status, no force pushes and no deletion; on GitLab the branch is protected with push allowed to no one and no force push, the project requires a succeeding pipeline and does not count skipped ones, and LADO can post its status; unknown: they could not be read; unsupported: the GitLab client reads no protection, LADO only detects."`
	Missing        []string   `json:"missing" description:"What the rules lack, for unprotected. GitHub: pull_request, required_status_checks:lado/integration (or required_status_checks:lado/integration:any_source when the rules take the check from one app only, which LADO's status cannot satisfy), non_fast_forward, deletion. GitLab: protected_branch, push_access (someone may push), force_push, pipeline_must_succeed (Pipelines must succeed is off), skipped_pipeline (skipped pipelines count as successful), integration_status (LADO cannot post its status: the token is below Developer, lacks the api scope, or LADO's last post failed)."`
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
