// LADO fork: LADO's daemon errors, connection check problems, rules problems
// and the reasons behind a task's or a queue entry's state in the developer's
// language. The daemon answers in English with a stable code; the app
// translates the code and fills in the details the daemon sends.
import type { TFunction } from "i18next";
import type { MessageKey } from "../i18n/messages";
import { LadoApiError, type LadoConnectionCheck, type LadoQueueReason, type LadoTaskStateReason } from "./lado";

type LadoErrorKey = Extract<MessageKey, `lado.error.${string}`>;
type LadoProblemKey = Extract<MessageKey, `lado.problem.${string}`>;

const errorKeys: Partial<Record<string, LadoErrorKey>> = {
	PROJECT_NOT_FOUND: "lado.error.projectNotFound",
	LADO_MODE_OFF: "lado.error.modeOff",
	LADO_REPOSITORY_NOT_CONNECTED: "lado.error.repositoryNotConnected",
	LADO_REPOSITORY_UNSUPPORTED: "lado.error.repositoryUnsupported",
	LADO_REPOSITORY_REQUIRED: "lado.error.repositoryRequired",
	LADO_REPOSITORY_REF_INVALID: "lado.error.repositoryRefInvalid",
	LADO_REPOSITORY_NOT_IN_PROJECT: "lado.error.repositoryNotInProject",
	LADO_TRACKER_UNAVAILABLE: "lado.error.trackerUnavailable",
	LADO_TASK_TITLE_REQUIRED: "lado.error.titleRequired",
	LADO_TASK_TITLE_TOO_LONG: "lado.error.titleTooLong",
	LADO_TASK_BODY_TOO_LONG: "lado.error.bodyTooLong",
	LADO_ISSUE_NOT_CREATED: "lado.error.issueNotCreated",
	LADO_ISSUE_CREATE_FAILED: "lado.error.issueCreateFailed",
	LADO_ISSUE_CREATE_UNCONFIRMED: "lado.error.issueCreateUnconfirmed",
	LADO_TASK_LINK_FAILED: "lado.error.taskLinkFailed",
	LADO_TASK_NOT_FOUND: "lado.error.taskNotFound",
	LADO_IMPORT_EMPTY: "lado.error.importEmpty",
	LADO_IMPORT_TOO_LARGE: "lado.error.importTooLarge",
	LADO_ISSUE_REF_INVALID: "lado.error.issueRefInvalid",
	LADO_ISSUE_NOT_IN_REPOSITORY: "lado.error.issueNotInRepository",
	LADO_ISSUE_READ_FAILED: "lado.error.issueReadFailed",
	LADO_TASK_CREATE_FAILED: "lado.error.internal",
	LADO_TASKS_LOAD_FAILED: "lado.error.internal",
	LADO_TASK_LOAD_FAILED: "lado.error.internal",
	LADO_TASK_CANCEL_FAILED: "lado.error.internal",
	LADO_SETTINGS_LOAD_FAILED: "lado.error.internal",
	PROJECT_LOAD_FAILED: "lado.error.internal",
	LADO_REPOSITORY_URL_INVALID: "lado.error.repositoryUrlInvalid",
	LADO_CONNECTION_NOT_READY: "lado.error.connectionNotReady",
	LADO_DESTINATION_REQUIRED: "lado.error.destinationRequired",
	LADO_PLACEMENT_INVALID: "lado.error.placementInvalid",
	LADO_WORKSPACE_NAME_REQUIRED: "lado.error.workspaceNameRequired",
	LADO_WORKSPACE_NAME_INVALID: "lado.error.workspaceNameInvalid",
	LADO_WORKSPACE_REQUIRED: "lado.error.workspaceRequired",
	LADO_WORKSPACE_FOLDER_EXISTS: "lado.error.workspaceFolderExists",
	LADO_NOT_A_WORKSPACE: "lado.error.notAWorkspace",
	LADO_WORKSPACE_ROOT_INVALID: "lado.error.workspaceRootInvalid",
	LADO_CREDENTIAL_STORE_FAILED: "lado.error.credentialStoreFailed",
	LADO_SSH_KEY_INVALID: "lado.error.sshKeyInvalid",
	LADO_SSH_KEY_SETUP_FAILED: "lado.error.sshKeySetupFailed",
	// AO's clone and workspace registration, reached through a connection.
	CLONE_DESTINATION_EXISTS: "lado.error.cloneDestinationExists",
	CLONE_DESTINATION_UNAVAILABLE: "lado.error.cloneDestinationUnavailable",
	CLONE_EMPTY_REPOSITORY: "lado.error.cloneEmptyRepository",
	GIT_CLONE_FAILED: "lado.error.gitCloneFailed",
	GIT_NOT_FOUND: "lado.error.gitNotFound",
	PATH_ALREADY_REGISTERED: "lado.error.pathAlreadyRegistered",
	WORKSPACE_PARENT_COMMIT_FAILED: "lado.error.workspaceCommitFailed",
	WORKSPACE_CHILD_RESERVED_NAME: "lado.error.workspaceReservedName",
	// Plans and approvals (step 4b).
	LADO_TASK_CANCELLED: "lado.error.taskCancelled",
	LADO_ISSUE_CLOSED: "lado.error.issueClosed",
	LADO_PLAN_NOTE_TOO_LONG: "lado.error.planNoteTooLong",
	LADO_PLAN_REQUEST_AMBIGUOUS: "lado.error.planRequestAmbiguous",
	LADO_PLAN_NOTHING_TO_CHANGE: "lado.error.planNothingToChange",
	LADO_PLAN_SOURCE_COMMENT_UNKNOWN: "lado.error.planSourceCommentUnknown",
	LADO_ORCHESTRATOR_UNAVAILABLE: "lado.error.orchestratorUnavailable",
	LADO_PLAN_CHANGED: "lado.error.planChanged",
	LADO_PLAN_SUPERSEDED: "lado.error.planSuperseded",
	LADO_PLAN_INVALID: "lado.error.planInvalid",
	LADO_PLAN_BLOB_MISSING: "lado.error.planBlobMissing",
	LADO_PLAN_NOT_FOUND: "lado.error.planNotFound",
	LADO_PRESENCE_REQUIRED: "lado.error.presenceRequired",
	LADO_PRESENCE_UNAVAILABLE: "lado.error.presenceUnavailable",
	LADO_PRESENCE_INVALID: "lado.error.presenceInvalid",
	LADO_PRESENCE_EXPIRED: "lado.error.presenceExpired",
	LADO_PRESENCE_REPLAYED: "lado.error.presenceReplayed",
	// The desktop app's main process, before it reaches the daemon.
	LADO_PRESENCE_UNTRUSTED_SENDER: "lado.error.presenceInvalid",
	LADO_PLAN_REVISION_ID_INVALID: "lado.error.planNotFound",
	SERVICE_UNAVAILABLE: "lado.error.unreachable",
	LADO_SNAPSHOT_SAVE_FAILED: "lado.error.internal",
	LADO_PLAN_REQUEST_SAVE_FAILED: "lado.error.internal",
	LADO_PLAN_APPROVE_FAILED: "lado.error.internal",
	LADO_PLANS_LOAD_FAILED: "lado.error.internal",
	// Rules (step 5).
	LADO_RULE_NOT_FOUND: "lado.error.ruleNotFound",
	LADO_RULE_NAME_REQUIRED: "lado.error.ruleNameRequired",
	LADO_RULE_NAME_TOO_LONG: "lado.error.ruleNameTooLong",
	LADO_RULE_CATEGORY_TOO_LONG: "lado.error.ruleCategoryTooLong",
	LADO_RULE_INSTRUCTION_REQUIRED: "lado.error.ruleInstructionRequired",
	LADO_RULE_INSTRUCTION_TOO_LONG: "lado.error.ruleInstructionTooLong",
	LADO_RULE_CONFLICT_UNKNOWN: "lado.error.ruleConflictUnknown",
	LADO_RULE_CONFLICT_SELF: "lado.error.ruleConflictSelf",
	LADO_RULE_CONFLICT_PROTECTED: "lado.error.ruleConflictProtected",
	LADO_RULE_CONFLICTS_TOO_MANY: "lado.error.ruleConflictsTooMany",
	LADO_RULE_UPDATE_EMPTY: "lado.error.ruleUpdateEmpty",
	LADO_RULE_CEILING_INVALID: "lado.error.ruleCeilingInvalid",
	LADO_RULE_CHANGED: "lado.error.ruleChanged",
	LADO_RULE_ARCHIVED: "lado.error.ruleArchived",
	LADO_RULE_PROTECTED: "lado.error.ruleProtected",
	LADO_RULE_BUILTIN: "lado.error.ruleBuiltin",
	LADO_RULE_REFERENCED: "lado.error.ruleReferenced",
	LADO_BINDING_SCOPE_INVALID: "lado.error.bindingScopeInvalid",
	LADO_BINDING_STATE_INVALID: "lado.error.bindingStateInvalid",
	LADO_SCOPE_NOT_FOUND: "lado.error.scopeNotFound",
	LADO_EFFECTIVE_SCOPE_REQUIRED: "lado.error.effectiveScopeRequired",
	LADO_EFFECTIVE_SCOPE_INVALID: "lado.error.effectiveScopeInvalid",
	LADO_POLICY_SHA_REQUIRED: "lado.error.policyShaRequired",
	LADO_RULES_UNAVAILABLE: "lado.error.rulesUnavailable",
	LADO_RULES_CHANGED: "lado.error.rulesChanged",
	LADO_RULES_LOAD_FAILED: "lado.error.internal",
	LADO_RULES_SAVE_FAILED: "lado.error.internal",
	// The queue, its runs and the dispatch settings (step 6).
	LADO_TASK_ALREADY_QUEUED: "lado.error.taskAlreadyQueued",
	LADO_TASK_RUNNING: "lado.error.taskRunning",
	LADO_APPROVAL_ABOVE_CEILING: "lado.error.approvalAboveCeiling",
	LADO_EXECUTION_FIXED: "lado.error.executionFixed",
	LADO_EXECUTION_NOT_FOUND: "lado.error.executionNotFound",
	LADO_DISPATCH_SETTINGS_INVALID: "lado.error.dispatchSettingsInvalid",
	LADO_SETTINGS_EMPTY: "lado.error.settingsEmpty",
	LADO_TRACKER_INTAKE_ENABLED: "lado.error.trackerIntakeEnabled",
	// The desktop app's main process, before it reaches the daemon.
	LADO_PROJECT_ID_INVALID: "lado.error.projectNotFound",
	LADO_QUEUE_LOAD_FAILED: "lado.error.internal",
	LADO_EXECUTION_LOAD_FAILED: "lado.error.internal",
	LADO_DISPATCH_SETTINGS_LOAD_FAILED: "lado.error.internal",
	LADO_DISPATCH_SETTINGS_UPDATE_FAILED: "lado.error.internal",
	LADO_SETTINGS_UPDATE_FAILED: "lado.error.internal",
	// A queue entry's error when the tracker no longer has the task's issue.
	LADO_ISSUE_NOT_FOUND: "lado.error.issueNotFound",
	// The app sends includeArchived as a boolean; anything else is a bug.
	INVALID_QUERY: "lado.error.internal",
};

// LADO_EXECUTION_FIXED also refuses restoring a run LADO stopped for good;
// its reason says why, and an agent or interface change has none.
const restoreKeys: Partial<Record<string, LadoErrorKey>> = {
	timed_out: "lado.error.restoreTimedOut",
	task_cancelled: "lado.error.restoreCancelled",
};

function errorKey(code: string, details: Record<string, unknown> | undefined): LadoErrorKey | undefined {
	if (code === "LADO_EXECUTION_FIXED" && typeof details?.reason === "string") {
		return restoreKeys[details.reason] ?? errorKeys[code];
	}
	return errorKeys[code];
}

const problemKeys: Partial<Record<string, LadoProblemKey>> = {
	token_missing: "lado.problem.tokenMissing",
	token_unusable: "lado.problem.tokenUnusable",
	token_rejected: "lado.problem.tokenRejected",
	token_check_failed: "lado.problem.tokenCheckFailed",
	key_home_unknown: "lado.problem.keyHomeUnknown",
	key_path_relative: "lado.problem.keyPathRelative",
	key_missing: "lado.problem.keyMissing",
	key_not_a_file: "lado.problem.keyNotAFile",
	host_key_untrusted: "lado.problem.hostKeyUntrusted",
	key_refused: "lado.problem.keyRefused",
	ssh_failed: "lado.problem.sshFailed",
};

// The details LADO's messages name; a detail the daemon left out reads empty,
// and a permission ceiling reads as AO's project settings name the mode.
function detailValues(t: TFunction, details: Record<string, unknown> | undefined) {
	const text = (name: string) => {
		const value = details?.[name];
		return typeof value === "string" || typeof value === "number" ? String(value) : "";
	};
	return {
		reason: text("reason"),
		min: text("min"),
		max: text("max"),
		issue: text("issue"),
		repository: text("repository"),
		comment: text("comment"),
		rule: text("rule"),
		ceiling: modeLabel(t, text("ceiling")),
	};
}

/**
 * What to show for a failed LADO call: the daemon's error code in the app's
 * language; the daemon's own message for a code this app does not know yet;
 * and fallback when the daemon could not be reached at all.
 */
export function ladoErrorText(t: TFunction, error: unknown, fallback: LadoErrorKey = "lado.error.unreachable"): string {
	if (error instanceof LadoApiError && error.code) {
		const key = errorKey(error.code, error.details);
		return key ? t(key, detailValues(t, error.details)) : error.message;
	}
	return t(fallback);
}

// The codes that lead a queue entry's error and say all there is to say, so
// the app words them; any other error is the daemon's own text.
const queueErrorCodes = new Set(["LADO_ISSUE_NOT_FOUND"]);

/**
 * Why a task's last attempt failed, as its queue entry records it: the daemon
 * cleans the text up (one line, no secrets) and leads it with the error code
 * when there is one. LADO's own errors read in the developer's language.
 */
export function ladoQueueErrorText(t: TFunction, error: string): string {
	const code = /^([A-Z][A-Z0-9_]*):/.exec(error)?.[1];
	const key = code && queueErrorCodes.has(code) ? errorKeys[code] : undefined;
	return key ? t(key) : error;
}

/** A failed connection check, translated, with what the tool printed if any. */
export type LadoCheckProblem = { text: string; detail?: string };

function checkProblem(
	t: TFunction,
	code: string | undefined,
	message: string | undefined,
	detail: string | undefined,
	host: string,
): LadoCheckProblem | null {
	if (!code && !message) return null;
	const key = code ? problemKeys[code] : undefined;
	if (!key) return { text: message ?? "" };
	return { text: t(key, { host }), ...(detail ? { detail } : {}) };
}

export function ladoTokenProblem(t: TFunction, check: LadoConnectionCheck): LadoCheckProblem | null {
	return checkProblem(t, check.token.problemCode, check.token.problem, check.token.problemDetail, check.host);
}

export function ladoSSHProblem(t: TFunction, check: LadoConnectionCheck): LadoCheckProblem | null {
	if (check.ssh.ok) return null;
	return checkProblem(t, check.ssh.problemCode, check.ssh.problem, check.ssh.problemDetail, check.host);
}

type LadoPlanProblemKey = Extract<MessageKey, `lado.planProblem.${string}`>;

const planProblemKeys: Partial<Record<string, LadoPlanProblemKey>> = {
	too_large: "lado.planProblem.too_large",
	not_text: "lado.planProblem.not_text",
	missing_front_matter: "lado.planProblem.missing_front_matter",
	invalid_front_matter: "lado.planProblem.invalid_front_matter",
	missing_field: "lado.planProblem.missing_field",
	invalid_field: "lado.planProblem.invalid_field",
	wrong_task: "lado.planProblem.wrong_task",
	wrong_issue: "lado.planProblem.wrong_issue",
	unknown_source_comment: "lado.planProblem.unknown_source_comment",
	wrong_source: "lado.planProblem.wrong_source",
	unknown_base_ref: "lado.planProblem.unknown_base_ref",
};

/**
 * A plan problem in the developer's language. The field stays as the front
 * matter names it, since that is what the plan file says; the offending
 * value, when the daemon sent one, is shown beside it.
 */
export function ladoPlanProblemText(t: TFunction, problem: { code: string; field?: string; detail?: string }): string {
	const key = planProblemKeys[problem.code];
	return key ? t(key, { field: problem.field ?? "" }) : problem.code;
}

type LadoPolicyProblemKey = Extract<MessageKey, `lado.policy.problem.${string}`>;

// The problems a rule resolution reports (backend/internal/service/ladopolicy/resolve.go).
const policyProblemKeys: Partial<Record<string, LadoPolicyProblemKey>> = {
	LADO_AGENT_NOT_CHOSEN: "lado.policy.problem.agentNotChosen",
	LADO_AGENT_CAPABILITY_UNSUPPORTED: "lado.policy.problem.capabilityUnsupported",
	LADO_AGENT_CAPABILITY_UNVERIFIED: "lado.policy.problem.capabilityUnverified",
	LADO_AGENT_CAPABILITY_DEGRADED: "lado.policy.problem.capabilityDegraded",
	LADO_CODEX_ASKS_APPROVAL: "lado.policy.problem.codexAsksApproval",
	LADO_RULES_CONFLICT: "lado.policy.problem.rulesConflict",
	LADO_AO_RULES_FILE_UNREADABLE: "lado.policy.problem.aoRulesFileUnreadable",
	LADO_CEILING_MISSING: "lado.policy.problem.ceilingMissing",
	LADO_PLAN_CEILING_CLAMPED: "lado.policy.problem.planCeilingClamped",
};

const capabilityKeys: Partial<Record<string, MessageKey>> = {
	prompt: "lado.policy.capability.prompt",
	permission_mode: "lado.policy.capability.permissionMode",
};

// The permission modes as AO's project settings name them.
const modeKeys: Partial<Record<string, MessageKey>> = {
	"accept-edits": "settings.project.permissionAcceptEdits",
	auto: "settings.project.permissionAuto",
	"bypass-permissions": "settings.project.permissionBypass",
};

function modeLabel(t: TFunction, mode: string): string {
	const key = modeKeys[mode];
	return key ? t(key) : mode;
}

/** How a problem names rules and agents; both default to their ids. */
export type LadoProblemLabels = { rule?: (ruleId: string) => string; agent?: (harness: string) => string };

/**
 * A rules problem in the developer's language, with the modes, capability,
 * file and rules it names. A code this app does not know yet shows as is.
 */
export function ladoPolicyProblemText(
	t: TFunction,
	problem: { code: string; rules?: string[]; details?: Record<string, string> },
	labels: LadoProblemLabels = {},
): string {
	const key = policyProblemKeys[problem.code];
	if (!key) return problem.code;
	const detail = (name: string) => problem.details?.[name] ?? "";
	const label = (keys: Partial<Record<string, MessageKey>>, value: string) => {
		const labelKey = keys[value];
		return labelKey ? t(labelKey) : value;
	};
	const agent = detail("agent");
	return t(key, {
		agent: agent && labels.agent ? labels.agent(agent) : agent,
		capability: label(capabilityKeys, detail("capability")),
		mode: label(modeKeys, detail("mode")),
		plan: label(modeKeys, detail("plan")),
		ceiling: label(modeKeys, detail("ceiling")),
		path: detail("path"),
		reason: detail("reason"),
		rules: (problem.rules ?? []).map((ruleId) => labels.rule?.(ruleId) ?? ruleId).join(", "),
	});
}

type LadoReasonKey = Extract<MessageKey, `lado.reason.${string}`>;

// Why a task needs review, is blocked or failed, or what its queued plan
// waits for (backend/internal/domain/lado_queue.go). Keyed by the API's enums,
// so a reason the daemon adds fails the typecheck until it has words here.
const reasonKeys: Record<LadoTaskStateReason | LadoQueueReason, LadoReasonKey> = {
	rules_changed: "lado.reason.rules_changed",
	issue_edited: "lado.reason.issue_edited",
	plan_superseded: "lado.reason.plan_superseded",
	approved_before_queue: "lado.reason.approved_before_queue",
	blocking_snapshot: "lado.reason.blocking_snapshot",
	base_ref_mismatch: "lado.reason.base_ref_mismatch",
	agent_unsupported: "lado.reason.agent_unsupported",
	attempts_exhausted: "lado.reason.attempts_exhausted",
	timed_out: "lado.reason.timed_out",
	ended_without_pr: "lado.reason.ended_without_pr",
	slot_global: "lado.reason.slot_global",
	slot_project: "lado.reason.slot_project",
	memory_tight: "lado.reason.memory_tight",
	issue_unreadable: "lado.reason.issue_unreadable",
	retry: "lado.reason.retry",
	lado_mode_off: "lado.reason.lado_mode_off",
	run_live: "lado.reason.run_live",
};

/**
 * Why a task or a queue entry is where it is, in the developer's language: a
 * task's stateReason or an entry's reason. None reads empty; a reason from a
 * newer daemon shows as is.
 */
export function ladoReasonText(t: TFunction, reason: string | undefined): string {
	if (!reason) return "";
	const key = (reasonKeys as Partial<Record<string, LadoReasonKey>>)[reason];
	return key ? t(key) : reason;
}
