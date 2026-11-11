// LADO fork: production database changes in the app (issue #97,
// docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md). Each
// repository's settings (the SQL paths, the command, its time limit and the
// names of its secrets), the board's summary of the SQL that waits, a run's
// SQL files with their state, a file's approved content, and applying a
// pull request's SQL of one phase by hand. The settings and a hand-applied
// run run a command against the production database outside any agent
// sandbox, so in the desktop app the main process confirms both and signs
// them; a secret's value goes to the daemon and never comes back.
import type { components } from "../../api/schema";
import { apiClient } from "./api-client";
import { aoBridge } from "./bridge";
import { LadoApiError, ladoError, type LadoExecution, type LadoVerification, type LadoVerifyItem } from "./lado";

export type LadoDBChangeSettings = components["schemas"]["LadoDBChangeSettingsResponse"];
export type LadoDBChangeRepoSettings = components["schemas"]["LadoDBChangeRepoSettingsResponse"];
export type LadoDBChangeSettingsInput = components["schemas"]["UpdateLadoDBChangeSettingsRequest"];
export type LadoDBChangeSummary = components["schemas"]["LadoDBChangeSummaryResponse"];
export type LadoDBChangeBoardState = LadoDBChangeSummary["state"];
export type LadoDBChangeExecution = components["schemas"]["LadoDBChangeExecutionResponse"];
export type LadoDBChangePR = components["schemas"]["LadoDBChangePRResponse"];
export type LadoDBChangeFile = components["schemas"]["LadoDBChangeFileResponse"];
export type LadoDBChangeFileStatus = LadoDBChangeFile["status"];
export type LadoDBChangeModified = components["schemas"]["LadoDBChangeModifiedResponse"];
export type LadoDBChangeRun = components["schemas"]["LadoDBChangeRunResponse"];
export type LadoDBChangeRunState = LadoDBChangeRun["state"];
export type LadoDBChangeFileContent = components["schemas"]["LadoDBChangeFileContentResponse"];
export type LadoDBChangePhase = components["schemas"]["LadoDBChangeFileRefBody"]["phase"];

/** The limits the daemon holds the settings to (domain.ValidateLadoDBChangeSettings). */
export const LADO_DB_CHANGE_MAX_PATHS = 10;
export const LADO_DB_CHANGE_MAX_SECRETS = 10;
/** The command's time limit per file the daemon uses for 0. */
export const LADO_DB_CHANGE_DEFAULT_TIMEOUT = 10;
/** A secret name the daemon takes (domain: ^[A-Z_][A-Z0-9_]{0,63}$). */
export const LADO_DB_CHANGE_SECRET_NAME = /^[A-Z_][A-Z0-9_]{0,63}$/;

/** Every database change query starts with this root. */
export const ladoDBChangesQueryRoot = ["lado", "db-changes"] as const;
/** Under one root per project, so a database change event refreshes the settings and the board's summary. */
export const ladoDBChangeProjectQueryKey = (projectId: string) => ["lado", "db-changes", "project", projectId] as const;
export const ladoDBChangeSettingsQueryKey = (projectId: string) => ["lado", "db-changes", "project", projectId, "settings"] as const;
export const ladoDBChangeSummariesQueryKey = (projectId: string) => ["lado", "db-changes", "project", projectId, "summaries"] as const;
/** A run's SQL files; a file's content sits under it, though it never changes for a blob. */
export const ladoDBChangeExecutionQueryKey = (sessionId: string) => ["lado", "db-changes", "execution", sessionId] as const;
export const ladoDBChangeFileQueryKey = (sessionId: string, prUrl: string, blobSha: string) =>
	["lado", "db-changes", "execution", sessionId, "file", prUrl, blobSha] as const;

/** Every repository's settings; one that never saved any has the defaults and sha256 "". */
export async function fetchLadoDBChangeSettings(projectId: string): Promise<LadoDBChangeSettings> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/db-changes/settings", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not load the database change settings");
	return data;
}

/**
 * Changes one repository's settings. In the desktop app the main process
 * reads them again, names the change without any secret's value, asks the
 * developer and signs it against the settings it read; null means the
 * developer cancelled and nothing changed. In a browser the call goes to the
 * daemon directly with base, which accepts it only in development.
 */
export async function updateLadoDBChangeSettings(
	projectId: string,
	input: LadoDBChangeSettingsInput,
): Promise<LadoDBChangeSettings | null> {
	// The bridge answers null when there is no desktop main process to ask.
	const result =
		(await aoBridge.lado?.presence?.changeDbChanges({
			projectId,
			repo: input.repo,
			enabled: input.enabled,
			paths: input.paths,
			command: input.command,
			timeoutMinutes: input.timeoutMinutes ?? 0,
			secretNames: input.secretNames,
			secrets: input.secrets,
		})) ?? null;
	if (result) {
		if (result.status === "cancelled") return null;
		if (result.status === "error") throw new LadoApiError(result.message, result.code, result.details);
		return result.settings;
	}
	const { data, error } = await apiClient.PUT("/api/v1/lado/projects/{projectId}/db-changes/settings", {
		params: { path: { projectId } },
		body: input,
	});
	if (error) throw ladoError(error, "Could not save the database change settings");
	return data;
}

/** The project's pull requests whose SQL waits or failed, for the board. */
export async function fetchLadoProjectDBChanges(projectId: string): Promise<LadoDBChangeSummary[]> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/db-changes", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not load the database changes");
	return data.summaries ?? [];
}

/** A run's pull requests with their SQL files, the applied files they change, and their runs. */
export async function fetchLadoExecutionDBChanges(sessionId: string): Promise<LadoDBChangeExecution> {
	const { data, error } = await apiClient.GET("/api/v1/lado/executions/{sessionId}/db-changes", {
		params: { path: { sessionId } },
	});
	if (error) throw ladoError(error, "Could not load the run's database changes");
	return data;
}

/** One SQL file of the run as its blob holds it, cut at 256 KiB. */
export async function fetchLadoDBChangeFile(sessionId: string, prUrl: string, blobSha: string): Promise<LadoDBChangeFileContent> {
	const { data, error } = await apiClient.GET("/api/v1/lado/executions/{sessionId}/db-changes/file", {
		params: { path: { sessionId }, query: { prUrl, blobSha } },
	});
	if (error) throw ladoError(error, "Could not load the SQL file");
	return data;
}

/**
 * Applies a pull request's SQL of one phase: a retry, the pre-merge SQL of a
 * pull request merged outside LADO, or the after-deploy SQL. In the desktop
 * app the main process reads the run's files from the daemon, names them,
 * asks the developer and signs them; null means the developer cancelled. In
 * a browser the call names the files the panel shows, and the daemon accepts
 * it only in development.
 */
export async function applyLadoDBChanges(
	sessionId: string,
	pr: Pick<LadoDBChangePR, "prUrl" | "headSha" | "files">,
	phase: LadoDBChangePhase,
): Promise<LadoDBChangeRun | null> {
	const result = (await aoBridge.lado?.presence?.applyDbChanges({ sessionId, prUrl: pr.prUrl, phase })) ?? null;
	if (result) {
		if (result.status === "cancelled") return null;
		if (result.status === "error") throw new LadoApiError(result.message, result.code, result.details);
		return result.run;
	}
	const files = pr.files.filter((file) => file.phase === phase).map(({ path, blobSha }) => ({ path, blobSha, phase }));
	const { data, error } = await apiClient.POST("/api/v1/lado/executions/{sessionId}/db-changes/apply", {
		params: { path: { sessionId } },
		body: { prUrl: pr.prUrl, headSha: pr.headSha, phase, files },
	});
	if (error) throw ladoError(error, "Could not apply the SQL");
	return data;
}

/** A secret as the card edits it: its name, and a new value only when the developer typed one. */
export type LadoDBChangeSecretDraft = { key: number; name: string; value: string };

/** A repository's settings as the card edits them. */
export type LadoDBChangeDraft = {
	enabled: boolean;
	/** One glob per line. */
	paths: string;
	command: string;
	timeoutMinutes: number;
	secrets: LadoDBChangeSecretDraft[];
};

// Secret rows keep their key while others are added or removed, so an input
// keeps its focus and text.
let nextSecretKey = 0;

/** A new secret row's key. */
export function ladoDBChangeSecretKey(): number {
	return nextSecretKey++;
}

/** The draft of a repository's saved settings; values are never read back. */
export function ladoDBChangeDraft(repo: LadoDBChangeRepoSettings): LadoDBChangeDraft {
	return {
		enabled: repo.enabled,
		paths: repo.paths.join("\n"),
		command: repo.command,
		timeoutMinutes: repo.timeoutMinutes || LADO_DB_CHANGE_DEFAULT_TIMEOUT,
		secrets: repo.secretNames.map((name) => ({ key: ladoDBChangeSecretKey(), name, value: "" })),
	};
}

/** The globs a draft names, without blank lines or surrounding space. */
function draftPaths(draft: LadoDBChangeDraft): string[] {
	return draft.paths
		.split(/\r?\n/)
		.map((line) => line.trim())
		.filter(Boolean);
}

/** The secret names a draft keeps, without blank rows or surrounding space. */
function draftSecretNames(draft: LadoDBChangeDraft): string[] {
	return draft.secrets.map((secret) => secret.name.trim()).filter(Boolean);
}

/**
 * What a save sends for the repository: new values for the secrets the
 * developer typed one for. A name left out loses its stored value too.
 */
export function ladoDBChangeInput(repo: LadoDBChangeRepoSettings, draft: LadoDBChangeDraft): LadoDBChangeSettingsInput {
	const set: Record<string, string> = {};
	for (const secret of draft.secrets) {
		const name = secret.name.trim();
		if (name && secret.value !== "") set[name] = secret.value;
	}
	return {
		repo: repo.repo,
		base: repo.sha256,
		enabled: draft.enabled,
		paths: draftPaths(draft),
		command: draft.command.trim(),
		timeoutMinutes: draft.timeoutMinutes,
		secretNames: draftSecretNames(draft),
		...(Object.keys(set).length > 0 ? { secrets: { set } } : {}),
	};
}

/** Whether the draft changes anything of the repository's saved settings. */
export function ladoDBChangeDirty(repo: LadoDBChangeRepoSettings, draft: LadoDBChangeDraft): boolean {
	const paths = draftPaths(draft);
	const names = draftSecretNames(draft);
	return (
		draft.enabled !== repo.enabled ||
		draft.command.trim() !== repo.command ||
		draft.timeoutMinutes !== (repo.timeoutMinutes || LADO_DB_CHANGE_DEFAULT_TIMEOUT) ||
		paths.length !== repo.paths.length ||
		paths.some((path, index) => path !== repo.paths[index]) ||
		names.length !== repo.secretNames.length ||
		names.some((name, index) => name !== repo.secretNames[index]) ||
		draft.secrets.some((secret) => secret.value !== "")
	);
}

/** The secret names the settings name that have no stored value; the command cannot run without them. */
export function ladoDBChangeMissingSecrets(repo: LadoDBChangeRepoSettings): string[] {
	return repo.secretNames.filter((name) => !repo.storedSecretNames.includes(name));
}

/** One refused setting: the field, such as paths[1] or secretNames[0], and the daemon's reason. */
export type LadoDBChangeProblem = { field: string; reason: string };

/** The problems a LADO_DB_CHANGE_INVALID refusal names; empty for any other error. */
export function ladoDBChangeProblems(error: unknown): LadoDBChangeProblem[] {
	if (!(error instanceof LadoApiError) || error.code !== "LADO_DB_CHANGE_INVALID") return [];
	const problems = error.details?.problems;
	if (!Array.isArray(problems)) return [];
	return problems.flatMap((problem) => {
		if (typeof problem !== "object" || problem === null) return [];
		const { field, reason } = problem as Record<string, unknown>;
		return typeof field === "string" && typeof reason === "string" ? [{ field, reason }] : [];
	});
}

/** A problem's field without its index ("paths[1]" is paths, 1), so the card names the row. */
export function ladoDBChangeProblemField(field: string): { name: string; index?: number } {
	const match = /^([A-Za-z]+)\[(\d+)\]$/.exec(field);
	return match ? { name: match[1], index: Number(match[2]) } : { name: field };
}

// The board's states from the most to the least urgent: SQL a merge outside
// LADO left out, then failures, then what waits for the developer, then what
// LADO does on its own.
const boardStateOrder: readonly LadoDBChangeBoardState[] = [
	"merged_unapplied",
	"apply_failed",
	"merge_failed",
	"preflight_blocked",
	"after_deploy_pending",
	"applying",
	"apply_waiting",
];

/** The most urgent of a task's or a session's summaries, one per pull request. */
export function ladoDBChangeUrgent(summaries: readonly LadoDBChangeSummary[]): LadoDBChangeSummary | undefined {
	let urgent: LadoDBChangeSummary | undefined;
	for (const summary of summaries) {
		if (!urgent || boardStateOrder.indexOf(summary.state) < boardStateOrder.indexOf(urgent.state)) urgent = summary;
	}
	return urgent;
}

/** Each task's and each session's most urgent summary. */
export function ladoDBChangeSummariesBy(summaries: readonly LadoDBChangeSummary[] | undefined): {
	byTask: Map<string, LadoDBChangeSummary>;
	bySession: Map<string, LadoDBChangeSummary>;
} {
	const group = (key: (summary: LadoDBChangeSummary) => string) => {
		const all = new Map<string, LadoDBChangeSummary[]>();
		for (const summary of summaries ?? []) {
			const id = key(summary);
			if (!id) continue;
			all.set(id, [...(all.get(id) ?? []), summary]);
		}
		const urgent = new Map<string, LadoDBChangeSummary>();
		for (const [id, list] of all) {
			const one = ladoDBChangeUrgent(list);
			if (one) urgent.set(id, one);
		}
		return urgent;
	};
	return { byTask: group((summary) => summary.taskId), bySession: group((summary) => summary.sessionId) };
}

/** The db_changes items of a verification record. */
function dbChangeItems(record: LadoVerification | undefined | null): LadoVerifyItem[] {
	return (record?.items ?? []).filter((item) => item.kind === "db_changes");
}

/**
 * Whether a run's pull requests have SQL to show: a record's db_changes item
 * names files, or applied files the head changes. A repository whose settings
 * are off has no such item, so its runs never read the database changes.
 */
export function ladoExecutionHasDBChanges(execution: Pick<LadoExecution, "verification" | "trackedPrs">): boolean {
	const records = [execution.verification, ...(execution.trackedPrs ?? []).map((pr) => pr.record)];
	return records.some((record) =>
		dbChangeItems(record).some((item) => (item.details.files?.length ?? 0) > 0 || (item.details.modified?.length ?? 0) > 0),
	);
}

/** The run of a kind that stands for the pull request now: the newest. */
export function ladoDBChangeLatestRun(pr: Pick<LadoDBChangePR, "runs">, kind: LadoDBChangePhase): LadoDBChangeRun | undefined {
	let latest: LadoDBChangeRun | undefined;
	for (const run of pr.runs) {
		if (run.kind !== kind) continue;
		if (!latest || run.createdAt.localeCompare(latest.createdAt) > 0) latest = run;
	}
	return latest;
}

/** The hand action a pull request offers for one phase, if any. */
export type LadoDBChangeAction = "retry" | "after_deploy" | "merged_unapplied";

// The run states a developer's button moves on (the daemon takes these only).
const actionStates = new Set<LadoDBChangeRunState>(["awaiting_human", "failed", "merge_failed"]);

/**
 * The button a run asks for: the pre-merge SQL of an approval to try again,
 * the pre-merge SQL of a pull request merged outside LADO, or the
 * after-deploy SQL once the code is deployed. Undefined while LADO acts on
 * its own or the run is done.
 */
export function ladoDBChangeAction(run: LadoDBChangeRun | undefined): LadoDBChangeAction | undefined {
	if (!run || !actionStates.has(run.state)) return undefined;
	if (run.kind === "after_deploy") return "after_deploy";
	return run.trigger === "merged_unapplied" ? "merged_unapplied" : "retry";
}
