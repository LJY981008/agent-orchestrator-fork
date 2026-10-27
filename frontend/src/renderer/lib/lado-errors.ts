// LADO fork: LADO's daemon errors, connection check problems, rules problems,
// local setup's reasons, conflicts and warnings, check command and protected
// path problems, and the reasons behind a task's or a queue entry's state in
// the developer's language. The daemon
// answers in English with a stable code; the app translates the code and
// fills in the details the daemon sends.
import { agentLabel } from "@aoagents/product-ui";
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
	// The desktop app's main process could not read the daemon's answer; a
	// request it sent can still have been made.
	LADO_PRESENCE_ANSWER_UNREADABLE: "lado.error.presenceAnswerUnreadable",
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
	// Local setup (step 7).
	LADO_SETUP_UNAVAILABLE: "lado.error.setupUnavailable",
	LADO_SETUP_NOT_FOUND: "lado.error.setupNotFound",
	LADO_SETUP_SCOPE_INVALID: "lado.error.setupScopeInvalid",
	LADO_SETUP_ADOPTION_INVALID: "lado.error.setupAdoptionInvalid",
	LADO_SETUP_NOT_ADOPTABLE: "lado.error.setupNotAdoptable",
	LADO_SETUP_CHANGED: "lado.error.setupChanged",
	LADO_SETUP_OS_UNVERIFIED: "lado.error.setupOsUnverified",
	LADO_SETUP_TOOL_RUNNING: "lado.error.setupToolRunning",
	LADO_SETUP_ADOPTION_FAILED: "lado.error.setupAdoptionFailed",
	LADO_SETUP_HOOK_PATH_MOVED: "lado.error.setupHookPathMoved",
	LADO_SETUP_ALREADY_RESTORED: "lado.error.setupAlreadyRestored",
	LADO_SETUP_RESTORE_FAILED: "lado.error.setupRestoreFailed",
	LADO_SETUP_DRIFT_UNREADABLE: "lado.error.setupDriftUnreadable",
	LADO_SETUP_ITEM_CHANGED: "lado.error.setupItemChanged",
	LADO_SETUP_CHANGE_INVALID: "lado.error.setupChangeInvalid",
	LADO_SETUP_CONTENT_INVALID: "lado.error.setupContentInvalid",
	LADO_SETUP_LOGIN_KEY: "lado.error.setupLoginKey",
	LADO_SETUP_ITEM_EXISTS: "lado.error.setupItemExists",
	LADO_SETUP_FOLDER_ITEM: "lado.error.setupFolderItem",
	LADO_SETUP_CONFLICT_NOT_FOUND: "lado.error.setupConflictNotFound",
	LADO_SETUP_CONFLICT_NOT_ACCEPTABLE: "lado.error.setupConflictNotAcceptable",
	LADO_SETUP_HOME_FAILED: "lado.error.setupHomeFailed",
	LADO_SETUP_CODEX_FAILED: "lado.error.setupCodexFailed",
	LADO_SETUP_LOAD_FAILED: "lado.error.setupLoadFailed",
	LADO_SETUP_DRIFT_PENDING: "lado.error.setupDriftPending",
	LADO_SETUP_PATH_USED_IN_PLACE: "lado.error.setupPathUsedInPlace",
	LADO_SETUP_PAIR_REQUIRED: "lado.error.setupPairRequired",
	LADO_SETUP_ROOT_IS_LINK: "lado.error.setupRootIsLink",
	// The desktop app's main process, before it reaches the daemon.
	LADO_SETUP_ADOPTION_ID_INVALID: "lado.error.setupNotFound",
	// The app sends the setup routes' bodies as the API defines them.
	INVALID_JSON: "lado.error.internal",
	// Verification, check commands, protected paths, the integration approval
	// and the merge gate (step 8).
	LADO_INTEGRATION_NOT_READY: "lado.error.integrationNotReady",
	LADO_INTEGRATION_HEAD_CHANGED: "lado.error.integrationHeadChanged",
	LADO_VERIFICATION_CHANGED: "lado.error.verificationChanged",
	LADO_INTEGRATION_ALREADY_APPROVED: "lado.error.integrationAlreadyApproved",
	LADO_INTEGRATION_ACK_MISMATCH: "lado.error.integrationAckMismatch",
	LADO_VERIFY_UNAVAILABLE: "lado.error.verifyUnavailable",
	LADO_VERIFY_FAILED: "lado.error.internal",
	LADO_EXECUTION_NO_PR: "lado.error.executionNoPr",
	LADO_CHECK_RUN_NOT_FOUND: "lado.error.checkRunNotFound",
	LADO_CHECK_COMMANDS_INVALID: "lado.error.checkCommandsInvalid",
	LADO_PROTECTED_PATHS_INVALID: "lado.error.protectedPathsInvalid",
	LADO_INTEGRATION_APPROVAL_REQUIRED: "lado.error.integrationApprovalRequired",
	LADO_MERGE_GATE_FAILED: "lado.error.mergeGateFailed",
	// The desktop app's main process, before it reaches the daemon.
	LADO_SESSION_ID_INVALID: "lado.error.executionNotFound",
};

// LADO_EXECUTION_FIXED also refuses restoring a run LADO stopped for good;
// its reason says why, and an agent or interface change has none.
const restoreKeys: Partial<Record<string, LadoErrorKey>> = {
	timed_out: "lado.error.restoreTimedOut",
	task_cancelled: "lado.error.restoreCancelled",
};

// An adoption or a restore that stopped says by its stage, and an adoption
// by its state, what happened to the developer's files.
const setupStageKeys: Partial<Record<string, Partial<Record<string, LadoErrorKey>>>> = {
	LADO_SETUP_ADOPTION_FAILED: { partial: "lado.error.setupAdoptionPartial", record: "lado.error.setupAdoptionNotRecorded" },
	LADO_SETUP_RESTORE_FAILED: { manifest: "lado.error.setupRestoreBackupUnreadable", record: "lado.error.setupRestoreNotRecorded" },
};

function errorKey(code: string, details: Record<string, unknown> | undefined): MessageKey | undefined {
	// A problem with the whole list or the time limit says it all; one with a
	// row names the row. One from a newer daemon shows the daemon's own words.
	if (code === "LADO_CHECK_COMMANDS_INVALID") {
		const problem = checkCommandProblemKey(details);
		return problem && wholeCheckProblems.has(problem) ? problem : problem && errorKeys[code];
	}
	if (code === "LADO_PROTECTED_PATHS_INVALID") {
		if (typeof details?.rule === "string") return "lado.error.protectedPathsRule";
		const problem = typeof details?.reason === "string" ? protectedPathProblemKeys[details.reason] : undefined;
		return problem === "lado.protectedPaths.problem.tooMany" ? problem : problem && errorKeys[code];
	}
	if (code === "LADO_EXECUTION_FIXED" && typeof details?.reason === "string") {
		return restoreKeys[details.reason] ?? errorKeys[code];
	}
	// An adoption LADO could not record puts back what it removed; what it
	// could not put back the developer restores from the adoption.
	if (code === "LADO_SETUP_ADOPTION_FAILED" && Array.isArray(details?.putBackFailed) && details.putBackFailed.length > 0) {
		return "lado.error.setupAdoptionPutBackFailed";
	}
	const stages = setupStageKeys[code];
	if (stages) {
		const state = details?.state === "partial" ? stages.partial : undefined;
		return state ?? (typeof details?.stage === "string" ? stages[details.stage] : undefined) ?? errorKeys[code];
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
// Local setup's tools, kinds and reasons read in the developer's language, and
// its lists of sessions, programs and tools read as one line.
function detailValues(t: TFunction, code: string, details: Record<string, unknown> | undefined) {
	const text = (name: string) => {
		const value = details?.[name];
		return typeof value === "string" || typeof value === "number" ? String(value) : "";
	};
	const list = (name: string, show: (item: Record<string, unknown>) => string) => {
		const values = details?.[name];
		return Array.isArray(values)
			? values.flatMap((value) => (typeof value === "object" && value !== null ? [show(value as Record<string, unknown>)] : []))
			: [];
	};
	const field = (item: Record<string, unknown>, name: string) => {
		const value = item[name];
		return typeof value === "string" || typeof value === "number" ? String(value) : "";
	};
	const sessions = list("sessions", (session) => `${field(session, "id")} (${agentLabel(field(session, "tool"))})`);
	const processes = list("processes", (program) => `${field(program, "name")} (PID ${field(program, "pid")})`);
	const putBackFailed = details?.putBackFailed;
	const paths = Array.isArray(putBackFailed) ? putBackFailed.filter((path): path is string => typeof path === "string") : [];
	const toolIds = details?.tools;
	const tools = Array.isArray(toolIds)
		? toolIds.filter((tool): tool is string => typeof tool === "string").map((tool) => agentLabel(tool))
		: [];
	const setup = code.startsWith("LADO_SETUP_");
	// A row's number counts from one; what holds an integration back reads as
	// the task reasons it names.
	const index = details?.index;
	const reasons = details?.reasons;
	return {
		number: typeof index === "number" ? String(index + 1) : "",
		problem:
			code === "LADO_PROTECTED_PATHS_INVALID" ? ladoProtectedPathProblemText(t, details ?? {}) : ladoCheckCommandProblemText(t, details ?? {}),
		reasons: Array.isArray(reasons)
			? reasons.filter((reason): reason is string => typeof reason === "string").map((reason) => ladoReasonText(t, reason)).join("; ")
			: "",
		head: text("currentHeadSha").slice(0, 7),
		reason: setup ? ladoSetupReasonText(t, text("reason"), details) : text("reason"),
		min: text("min"),
		max: text("max"),
		issue: text("issue"),
		repository: text("repository"),
		comment: text("comment"),
		rule: text("rule"),
		ceiling: modeLabel(t, text("ceiling")),
		tool: text("tool") && agentLabel(text("tool")),
		kind: ladoSetupKindLabel(t, text("kind")),
		name: text("name"),
		stage: text("stage"),
		item: text("item"),
		key: text("key"),
		path: text("path"),
		sessions: sessions.join(", "),
		processes: processes.join(", "),
		running: [...sessions, ...processes].join(", "),
		tools: tools.join(", "),
		pair: text("pair"),
		paths: paths.join(", "),
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
		// A reason the daemon left out leaves no trailing space.
		return key ? t(key, detailValues(t, error.code, error.details)).trim() : error.message;
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
	// Local setup's conflicts are rules problems as well.
	if (!key) return setupConflictKeys[problem.code] ? ladoSetupConflictText(t, problem) : problem.code;
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
	// Validating, Integrating and Completed (step 8).
	verifying: "lado.reason.verifying",
	checks_pending: "lado.reason.checks_pending",
	checks_failed: "lado.reason.checks_failed",
	commands_running: "lado.reason.commands_running",
	worktree_not_at_head: "lado.reason.worktree_not_at_head",
	commands_failed: "lado.reason.commands_failed",
	command_timed_out: "lado.reason.command_timed_out",
	worktree_missing: "lado.reason.worktree_missing",
	commit_messages_failed: "lado.reason.commit_messages_failed",
	protected_path: "lado.reason.protected_path",
	review_pending: "lado.reason.review_pending",
	changes_requested: "lado.reason.changes_requested",
	status_not_posted: "lado.reason.status_not_posted",
	merged_without_approval: "lado.reason.merged_without_approval",
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

type LadoSetupKindKey = Extract<MessageKey, `lado.setup.kind.${string}`>;

// The kinds of setup items (backend/internal/domain/lado_setup.go).
const setupKindKeys: Partial<Record<string, LadoSetupKindKey>> = {
	instructions: "lado.setup.kind.instructions",
	setting: "lado.setup.kind.setting",
	skill: "lado.setup.kind.skill",
	agent: "lado.setup.kind.agent",
	command: "lado.setup.kind.command",
	output_style: "lado.setup.kind.outputStyle",
	hook_script: "lado.setup.kind.hookScript",
	mcp_server: "lado.setup.kind.mcpServer",
};

/** A setup item's kind in the developer's language; one from a newer daemon shows as is. */
export function ladoSetupKindLabel(t: TFunction, kind: string): string {
	const key = setupKindKeys[kind];
	return key ? t(key) : kind;
}

type LadoSetupReasonKey = Extract<MessageKey, `lado.setup.reason.${string}`>;

// Why the inventory does not adopt an entry (backend/internal/domain/lado_setup.go),
// why an adoption refuses one, and why a change's request or content does
// not fit (backend/internal/service/ladosetup/changes.go).
const setupReasonKeys: Partial<Record<string, LadoSetupReasonKey>> = {
	credentials: "lado.setup.reason.credentials",
	login_key: "lado.setup.reason.loginKey",
	app_state: "lado.setup.reason.appState",
	plugins_stay: "lado.setup.reason.pluginsStay",
	tool_state: "lado.setup.reason.toolState",
	no_tui_path: "lado.setup.reason.noTuiPath",
	deprecated: "lado.setup.reason.deprecated",
	inline_table: "lado.setup.reason.inlineTable",
	literal_credentials: "lado.setup.reason.literalCredentials",
	name_unsupported: "lado.setup.reason.nameUnsupported",
	unknown_location: "lado.setup.reason.unknownLocation",
	worktrees_do_not_load: "lado.setup.reason.worktreesDoNotLoad",
	ao_sets_per_session: "lado.setup.reason.aoSetsPerSession",
	repo_tracked: "lado.setup.reason.repoTracked",
	managed_settings: "lado.setup.reason.managedSettings",
	symlink: "lado.setup.reason.symlink",
	unreadable: "lado.setup.reason.unreadable",
	not_found: "lado.setup.reason.notFound",
	already_managed: "lado.setup.reason.alreadyManaged",
	unknown: "lado.setup.reason.unknown",
	missing: "lado.setup.reason.missing",
	not_taken: "lado.setup.reason.notTaken",
	invalid: "lado.setup.reason.invalid",
	not_for_tool: "lado.setup.reason.notForTool",
	not_created: "lado.setup.reason.notCreated",
	not_text: "lado.setup.reason.notText",
	too_large: "lado.setup.reason.tooLarge",
	not_json: "lado.setup.reason.notJson",
	not_object: "lado.setup.reason.notObject",
	front_matter: "lado.setup.reason.frontMatter",
	server_shape: "lado.setup.reason.serverShape",
	root_is_link: "lado.setup.reason.rootIsLink",
	provider_login_env: "lado.setup.reason.providerLoginEnv",
};

/**
 * Why LADO does not adopt an entry, or refuses a change, as one sentence in
 * the developer's language, with the field and limit the daemon names. None
 * reads empty; a reason from a newer daemon shows as is.
 */
export function ladoSetupReasonText(t: TFunction, reason: string | undefined, details?: Record<string, unknown>): string {
	if (!reason) return "";
	const key = setupReasonKeys[reason];
	if (!key) return reason;
	const detail = (name: string) => {
		const value = details?.[name];
		return typeof value === "string" || typeof value === "number" ? String(value) : "";
	};
	return t(key, { field: detail("field"), limit: detail("limit") });
}

type LadoSetupConflictKey = Extract<MessageKey, `lado.setup.conflict.${string}`>;

// The conflict checks of local setup (backend/internal/service/ladosetup/conflicts.go).
const setupConflictKeys: Partial<Record<string, LadoSetupConflictKey>> = {
	LADO_SETUP_MCP_NAME_TAKEN: "lado.setup.conflict.mcpNameTaken",
	LADO_SETUP_SKILL_NAME_TAKEN: "lado.setup.conflict.skillNameTaken",
	LADO_SETUP_ENV_RESERVED: "lado.setup.conflict.envReserved",
	LADO_SETUP_DENIES_AO: "lado.setup.conflict.deniesAo",
	LADO_SETUP_HOOK_PATH_MOVED: "lado.setup.conflict.hookPathMoved",
	LADO_SETUP_DENIES_GIT: "lado.setup.conflict.deniesGit",
	LADO_SETUP_HOOK_MAY_BLOCK_TOOLS: "lado.setup.conflict.hookMayBlockTools",
	LADO_SETUP_HOOK_ADDS_PROMPT_TEXT: "lado.setup.conflict.hookAddsPromptText",
	LADO_SETUP_DEFAULT_MODE_ABOVE_CEILING: "lado.setup.conflict.defaultModeAboveCeiling",
	LADO_SETUP_HOOK_SHARES_AO_EVENT: "lado.setup.conflict.hookSharesAoEvent",
	LADO_SETUP_PROJECT_OVERRIDES: "lado.setup.conflict.projectOverrides",
	LADO_SETUP_PLUGIN_HOOKS_UNREADABLE: "lado.setup.conflict.pluginHooksUnreadable",
	LADO_SETUP_PATH_USED_IN_PLACE: "lado.setup.conflict.pathUsedInPlace",
	LADO_SETUP_ALLOWS_PROTECTED: "lado.setup.conflict.allowsProtected",
};

// Claude Code's own names of the permission modes a defaultMode can ask for.
const claudeModes: Partial<Record<string, string>> = {
	acceptEdits: "accept-edits",
	auto: "auto",
	bypassPermissions: "bypass-permissions",
};

/**
 * A setup conflict in the developer's language, with the key, rule, event,
 * matcher, path and modes it names, and the plugin whose hook it is. A code
 * this app does not know yet shows as is.
 */
export function ladoSetupConflictText(t: TFunction, conflict: { code: string; details?: Record<string, string> }): string {
	const key = setupConflictKeys[conflict.code];
	if (!key) return conflict.code;
	const detail = (name: string) => conflict.details?.[name] ?? "";
	const mode = detail("mode");
	const text = t(key, {
		key: detail("key"),
		rule: detail("rule"),
		event: detail("event"),
		matcher: detail("matcher"),
		path: detail("path"),
		plugin: detail("plugin"),
		commands: detail("commands"),
		mode: modeLabel(t, claudeModes[mode] ?? mode),
		ceiling: modeLabel(t, detail("ceiling")),
	});
	const plugin = detail("plugin");
	return plugin && conflict.code !== "LADO_SETUP_PLUGIN_HOOKS_UNREADABLE"
		? `${text} ${t("lado.setup.conflict.fromPlugin", { plugin })}`
		: text;
}

type LadoSetupRestoreKey = Extract<MessageKey, `lado.setup.restore.${string}`>;

const setupRestoreKeys: Partial<Record<string, LadoSetupRestoreKey>> = {
	restored: "lado.setup.restore.restored",
	restored_beside: "lado.setup.restore.restoredBeside",
	key_kept: "lado.setup.restore.keyKept",
	failed: "lado.setup.restore.failed",
	skipped_deleted: "lado.setup.restore.skippedDeleted",
};

/** What a restore did with one original, as its report says; a status from a newer daemon shows as is. */
export function ladoSetupRestoreStatusText(t: TFunction, status: string): string {
	const key = setupRestoreKeys[status];
	return key ? t(key) : status;
}

const setupRestoreNoteKeys: Partial<Record<string, LadoSetupRestoreKey>> = {
	kept_in_lado: "lado.setup.restore.keptInLado",
};

/** A note on one restored original, such as the later versions LADO keeps; none reads empty. */
export function ladoSetupRestoreNoteText(t: TFunction, note: string | undefined): string {
	if (!note) return "";
	const key = setupRestoreNoteKeys[note];
	return key ? t(key) : note;
}

type LadoSetupWarningKey = Extract<MessageKey, `lado.setup.warning.${string}`>;

// The warnings of adoption, restore and changes.
const setupWarningKeys: Partial<Record<string, LadoSetupWarningKey>> = {
	LADO_SETUP_DIRECT_USE_STOPS: "lado.setup.warning.directUseStops",
	LADO_SETUP_BASELINE_FAILED: "lado.setup.warning.baselineFailed",
	LADO_SETUP_MANAGED_WRITE_FAILED: "lado.setup.warning.managedWriteFailed",
	LADO_SETUP_PREVIEW_INCOMPLETE: "lado.setup.warning.previewIncomplete",
};

/** A warning the daemon sent with a setup answer; one from a newer daemon shows as is. */
export function ladoSetupWarningText(t: TFunction, code: string): string {
	const key = setupWarningKeys[code];
	return key ? t(key) : code;
}

type LadoCheckProblemKey = Extract<MessageKey, `lado.checkCommand.problem.${string}`>;

// LADO_CHECK_COMMANDS_INVALID's details: the field and the reason
// (backend/internal/domain ValidateLadoCheckCommands); too_many has no field,
// and the time limit is a field of its own.
const checkCommandProblemKeys: Partial<Record<string, LadoCheckProblemKey>> = {
	"name:required": "lado.checkCommand.problem.nameRequired",
	"name:too_long": "lado.checkCommand.problem.nameTooLong",
	"name:invalid": "lado.checkCommand.problem.nameInvalid",
	"name:duplicate": "lado.checkCommand.problem.nameDuplicate",
	"command:required": "lado.checkCommand.problem.commandRequired",
	"command:too_long": "lado.checkCommand.problem.commandTooLong",
	"command:invalid": "lado.checkCommand.problem.commandInvalid",
	":too_many": "lado.checkCommand.problem.tooMany",
	"checkTimeoutMinutes:invalid": "lado.checkCommand.problem.timeoutInvalid",
};

// The problems that are not about one row.
const wholeCheckProblems = new Set<MessageKey>(["lado.checkCommand.problem.tooMany", "lado.checkCommand.problem.timeoutInvalid"]);

function checkCommandProblemKey(details: Record<string, unknown> | undefined): LadoCheckProblemKey | undefined {
	const field = typeof details?.field === "string" ? details.field : "";
	return typeof details?.reason === "string" ? checkCommandProblemKeys[`${field}:${details.reason}`] : undefined;
}

/**
 * What is wrong with a check command, the list or the time limit, from the
 * details of LADO_CHECK_COMMANDS_INVALID ({index, field, reason}), as the
 * row or the time limit shows it. A problem from a newer daemon reads empty.
 */
export function ladoCheckCommandProblemText(t: TFunction, details: Record<string, unknown>): string {
	const key = checkCommandProblemKey(details);
	return key ? t(key) : "";
}

type LadoProtectedPathProblemKey = Extract<MessageKey, `lado.protectedPaths.problem.${string}`>;

// LADO_PROTECTED_PATHS_INVALID's reasons (backend/internal/service/ladopolicy).
const protectedPathProblemKeys: Partial<Record<string, LadoProtectedPathProblemKey>> = {
	required: "lado.protectedPaths.problem.required",
	too_many: "lado.protectedPaths.problem.tooMany",
	invalid: "lado.protectedPaths.problem.invalid",
};

/**
 * What is wrong with a protected path, from the details of
 * LADO_PROTECTED_PATHS_INVALID ({index, reason}). A reason from a newer
 * daemon reads empty.
 */
export function ladoProtectedPathProblemText(t: TFunction, details: Record<string, unknown>): string {
	const key = typeof details.reason === "string" ? protectedPathProblemKeys[details.reason] : undefined;
	return key ? t(key) : "";
}
