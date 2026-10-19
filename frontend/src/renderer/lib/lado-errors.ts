// LADO fork: LADO's daemon errors and connection check problems in the
// developer's language. The daemon answers in English with a stable code; the
// app translates the code and fills in the details the daemon sends.
import type { TFunction } from "i18next";
import type { MessageKey } from "../i18n/messages";
import { LadoApiError, type LadoConnectionCheck } from "./lado";

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
};

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

// The details LADO's messages name; a detail the daemon left out reads empty.
function detailValues(details: Record<string, unknown> | undefined) {
	const text = (name: string) => {
		const value = details?.[name];
		return typeof value === "string" || typeof value === "number" ? String(value) : "";
	};
	return { reason: text("reason"), max: text("max"), issue: text("issue"), repository: text("repository") };
}

/**
 * What to show for a failed LADO call: the daemon's error code in the app's
 * language; the daemon's own message for a code this app does not know yet;
 * and fallback when the daemon could not be reached at all.
 */
export function ladoErrorText(t: TFunction, error: unknown, fallback: LadoErrorKey = "lado.error.unreachable"): string {
	if (error instanceof LadoApiError && error.code) {
		const key = errorKeys[error.code];
		return key ? t(key, detailValues(error.details)) : error.message;
	}
	return t(fallback);
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
