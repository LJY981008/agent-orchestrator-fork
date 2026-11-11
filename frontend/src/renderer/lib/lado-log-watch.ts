// LADO fork: the production log watch in the app (issue #87). A project's
// Docker hosts and the containers LADO follows on each, its error rules, its
// caps and its automatic plan requests, the TLS copies LADO keeps, a check of
// a saved host, candidates read from the project's .gitlab-ci.yml, and the
// incidents LADO recorded. The daemon only reads from each host's Engine API.
// The settings card edits a draft that the developer saves in one PUT: the CI
// import only fills the draft, so nothing reaches the daemon before Save.
import type { components } from "../../api/schema";
import { apiClient } from "./api-client";
import { ladoError, type LadoTask } from "./lado";

export type LadoLogWatch = components["schemas"]["LadoLogWatchResponse"];
export type LadoLogSource = components["schemas"]["LadoLogSourceResponse"];
export type LadoLogTarget = components["schemas"]["LadoLogTargetResponse"];
export type LadoLogTargetKind = LadoLogTarget["kind"];
export type LadoLogSourceState = components["schemas"]["LadoLogSourceStatusResponse"]["state"];
export type LadoLogWatchInput = components["schemas"]["UpdateLadoLogWatchRequest"];
export type LadoLogTLSImport = components["schemas"]["ImportLadoLogTLSRequest"];
export type LadoLogWatchCheck = components["schemas"]["LadoLogWatchCheckResponse"];
export type LadoLogCIImport = components["schemas"]["LadoLogCIImportResponse"];
export type LadoLogCIFile = components["schemas"]["LadoLogCIFileResponse"];
export type LadoLogCIHost = components["schemas"]["LadoLogCIHostResponse"];
export type LadoLogFingerprint = components["schemas"]["LadoLogFingerprintResponse"];

/** The target kinds in the order the card offers them. */
export const ladoLogTargetKinds: readonly LadoLogTargetKind[] = ["compose_project", "compose_service", "name", "label"];

/** The limits the daemon holds the settings to (domain.LadoLogMax*). */
export const LADO_LOG_MAX_SOURCES = 10;
export const LADO_LOG_MAX_TARGETS = 10;

/** How often the open settings card reads the sources' states, which only the daemon's memory holds. */
export const LADO_LOG_WATCH_STATUS_REFRESH_MS = 10_000;

/** Under one root per project, so a log watch event refreshes the settings and the incidents. */
export const ladoLogWatchProjectQueryKey = (projectId: string) => ["lado", "log-watch", projectId] as const;
export const ladoLogWatchQueryKey = (projectId: string) => ["lado", "log-watch", projectId, "settings"] as const;
export const ladoLogIncidentsQueryKey = (projectId: string) => ["lado", "log-watch", projectId, "incidents"] as const;

/** The project's log watch; the defaults when it never saved any. */
export async function fetchLadoLogWatch(projectId: string): Promise<LadoLogWatch> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/log-watch", { params: { path: { projectId } } });
	if (error) throw ladoError(error, "Could not load the log watch");
	return data;
}

/** Replaces the project's log watch; a source or target left out is deleted. */
export async function updateLadoLogWatch(projectId: string, body: LadoLogWatchInput): Promise<LadoLogWatch> {
	const { data, error } = await apiClient.PUT("/api/v1/lado/projects/{projectId}/log-watch", {
		params: { path: { projectId } },
		body,
	});
	if (error) throw ladoError(error, "Could not save the log watch");
	return data;
}

/** Copies a saved source's CA, and its client certificate and key, under ~/.ao. */
export async function importLadoLogTLS(projectId: string, sourceId: string, body: LadoLogTLSImport): Promise<LadoLogWatch> {
	const { data, error } = await apiClient.POST("/api/v1/lado/projects/{projectId}/log-watch/sources/{sourceId}/tls", {
		params: { path: { projectId, sourceId } },
		body,
	});
	if (error) throw ladoError(error, "Could not import the certificates");
	return data;
}

/** Deletes LADO's copies of a source's TLS files. */
export async function deleteLadoLogTLS(projectId: string, sourceId: string): Promise<LadoLogWatch> {
	const { data, error } = await apiClient.DELETE("/api/v1/lado/projects/{projectId}/log-watch/sources/{sourceId}/tls", {
		params: { path: { projectId, sourceId } },
	});
	if (error) throw ladoError(error, "Could not delete the certificates");
	return data;
}

/** Checks a saved source; a failed connection is ok false, not an error. */
export async function checkLadoLogSource(projectId: string, sourceId: string): Promise<LadoLogWatchCheck> {
	const { data, error } = await apiClient.POST("/api/v1/lado/projects/{projectId}/log-watch/check", {
		params: { path: { projectId } },
		body: { sourceId },
	});
	if (error) throw ladoError(error, "Could not check the log source");
	return data;
}

/** The hosts and compose projects the project's .gitlab-ci.yml files name; nothing is saved. */
export async function importLadoLogCI(projectId: string): Promise<LadoLogCIImport> {
	const { data, error } = await apiClient.POST("/api/v1/lado/projects/{projectId}/log-watch/import-ci", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not read .gitlab-ci.yml");
	return data;
}

/** The incidents LADO recorded: every one with an open task, and the newest others. */
export async function fetchLadoLogIncidents(projectId: string): Promise<LadoLogFingerprint[]> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/log-watch/incidents", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not load the incidents");
	return data.fingerprints;
}

/** Ignores a fingerprint, so it creates no issue, or stops ignoring it. */
export async function setLadoLogFingerprintIgnored(projectId: string, fingerprintId: number, ignored: boolean): Promise<LadoLogFingerprint> {
	const { data, error } = await apiClient.POST("/api/v1/lado/projects/{projectId}/log-watch/fingerprints/{fingerprintId}/ignore", {
		params: { path: { projectId, fingerprintId: String(fingerprintId) } },
		body: { ignored },
	});
	if (error) throw ladoError(error, "Could not change the ignore list");
	return data;
}

/** A target as the card edits it. A saved one is keyed by its id, so a refresh keeps its inputs. */
export type LadoLogTargetDraft = {
	key: string;
	id?: string;
	kind: LadoLogTargetKind;
	value: string;
	composeProject: string;
	service: string;
	/** The repository its issues go to; empty uses the project's. */
	repository: string;
	paused: boolean;
};

/** A source as the card edits it; one without an id is not saved yet. */
export type LadoLogSourceDraft = {
	key: string;
	id?: string;
	displayName: string;
	endpoint: string;
	tls: boolean;
	paused: boolean;
	targets: LadoLogTargetDraft[];
};

/** The settings as the card edits them; the patterns are one per line. */
export type LadoLogWatchDraft = {
	enabled: boolean;
	repository: string;
	include: string;
	exclude: string;
	hourlyIssueCap: number;
	autoPlan: boolean;
	autoPlanDailyCap: number;
	autoPlanMinCount: number;
	disconnectAlertMinutes: number;
	sources: LadoLogSourceDraft[];
};

let nextDraftKey = 0;
/** A key for a source or target that is not saved yet. */
export function ladoLogNewKey(): string {
	return `new-${nextDraftKey++}`;
}

/** The draft of saved settings. */
export function ladoLogWatchDraft(saved: LadoLogWatch): LadoLogWatchDraft {
	return {
		enabled: saved.enabled,
		repository: saved.repository,
		include: saved.include.join("\n"),
		exclude: saved.exclude.join("\n"),
		hourlyIssueCap: saved.hourlyIssueCap,
		autoPlan: saved.autoPlan,
		autoPlanDailyCap: saved.autoPlanDailyCap,
		autoPlanMinCount: saved.autoPlanMinCount,
		disconnectAlertMinutes: saved.disconnectAlertMinutes,
		sources: saved.sources.map((source) => ({
			key: source.id,
			id: source.id,
			displayName: source.displayName,
			endpoint: source.endpoint,
			tls: source.tls,
			paused: source.paused,
			targets: source.targets.map((target) => ({
				key: target.id,
				id: target.id,
				kind: target.kind,
				value: target.value,
				composeProject: target.composeProject ?? "",
				service: target.service ?? "",
				repository: target.repository ?? "",
				paused: target.paused,
			})),
		})),
	};
}

function patterns(text: string): string[] {
	return text
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line !== "");
}

/** What a save sends: text loses surrounding space, blank pattern lines are dropped. */
export function ladoLogWatchInput(draft: LadoLogWatchDraft): LadoLogWatchInput {
	return {
		enabled: draft.enabled,
		...(draft.repository ? { repository: draft.repository } : {}),
		include: patterns(draft.include),
		exclude: patterns(draft.exclude),
		hourlyIssueCap: draft.hourlyIssueCap,
		autoPlan: draft.autoPlan,
		autoPlanDailyCap: draft.autoPlanDailyCap,
		autoPlanMinCount: draft.autoPlanMinCount,
		disconnectAlertMinutes: draft.disconnectAlertMinutes,
		sources: draft.sources.map((source) => ({
			...(source.id ? { id: source.id } : {}),
			kind: "docker_tcp" as const,
			displayName: source.displayName.trim(),
			endpoint: source.endpoint.trim(),
			tls: source.tls,
			paused: source.paused,
			targets: source.targets.map((target) => {
				const composeProject = target.kind === "compose_service" ? target.composeProject.trim() : "";
				const service = target.service.trim();
				return {
					...(target.id ? { id: target.id } : {}),
					kind: target.kind,
					value: target.value.trim(),
					...(composeProject ? { composeProject } : {}),
					...(service ? { service } : {}),
					...(target.repository ? { repository: target.repository } : {}),
					paused: target.paused,
				};
			}),
		})),
	};
}

/** Whether two drafts save the same settings. */
export function ladoLogWatchSameInput(left: LadoLogWatchDraft, right: LadoLogWatchDraft): boolean {
	return JSON.stringify(ladoLogWatchInput(left)) === JSON.stringify(ladoLogWatchInput(right));
}

/** The endpoint's port, when it has one. */
export function ladoLogEndpointPort(endpoint: string): number | undefined {
	const match = /:(\d{1,5})\/?$/.exec(endpoint.trim());
	return match ? Number(match[1]) : undefined;
}

/** A new, empty target. */
export function ladoLogNewTarget(): LadoLogTargetDraft {
	return { key: ladoLogNewKey(), kind: "compose_project", value: "", composeProject: "", service: "", repository: "", paused: false };
}

/**
 * The repository a CI file suggests for a compose project it deploys, or ""
 * when the guess is not sure. The file's repository must be one the project
 * offers, and either the file deploys only that compose project or the
 * compose project is named exactly like the file's repository folder. So a
 * back repository whose file deploys back and front fills back only, and
 * front stays for the developer to choose.
 */
export function ladoLogCISuggestedRepository(file: LadoLogCIFile, composeProject: string, refs: readonly string[]): string {
	const repository = file.repository ?? "";
	if (repository === "" || !refs.includes(repository)) return "";
	const only = file.composeProjects.length === 1 && file.composeProjects[0] === composeProject;
	const folder = file.repo.split("/").filter(Boolean).pop() ?? "";
	return only || (folder !== "" && composeProject === folder) ? repository : "";
}

/**
 * A new, unsaved source from a CI candidate: its endpoint (empty while the
 * developer has to fill it in), its display name, TLS for the TLS port 2376,
 * and a compose project target for each compose project picked, with the
 * repository the file suggests for it among the project's repositories.
 */
export function ladoLogSourceFromCI(
	host: LadoLogCIHost,
	composeProjects: readonly string[],
	file?: LadoLogCIFile,
	refs: readonly string[] = [],
): LadoLogSourceDraft {
	return {
		key: ladoLogNewKey(),
		displayName: host.displayName,
		endpoint: host.endpoint,
		tls: ladoLogEndpointPort(host.endpoint) === 2376,
		paused: false,
		targets: composeProjects.map((value) => ({
			...ladoLogNewTarget(),
			value,
			repository: file ? ladoLogCISuggestedRepository(file, value, refs) : "",
		})),
	};
}

/** The repository a target's issues go to: its own, else the project's; "" when neither is set. */
export function ladoLogTargetRepository(draft: Pick<LadoLogWatchDraft, "repository">, target: Pick<LadoLogTargetDraft, "repository">): string {
	return target.repository || draft.repository;
}

/**
 * Whether a target needs a repository it lacks, as the daemon checks on
 * save: the project has two repositories or more, the watch is on, neither
 * the source nor the target is paused, and neither the target nor the
 * project names a repository. Turning the watch off or pausing saves anyway.
 */
export function ladoLogTargetNeedsRepository(
	draft: Pick<LadoLogWatchDraft, "enabled" | "repository">,
	source: Pick<LadoLogSourceDraft, "paused">,
	target: Pick<LadoLogTargetDraft, "paused" | "repository">,
	repositoryCount: number,
): boolean {
	return repositoryCount >= 2 && draft.enabled && !source.paused && !target.paused && ladoLogTargetRepository(draft, target) === "";
}

/** A CI candidate's identity in the import list: its file, then its endpoint or what LADO could not resolve. */
export function ladoLogCIHostKey(file: LadoLogCIFile, host: LadoLogCIHost): string {
	return `${file.repo}\0${file.path}\0${host.variable ?? ""}\0${host.endpoint || host.unresolved || host.displayName}`;
}

// The states of a log task whose incident no longer needs attention.
const settledStates = new Set<LadoTask["state"]>(["completed", "cancelled", "closed_unmerged"]);
// The issue states of a closed issue; GitHub reports a not-planned close as cancelled.
const closedIssueStates = new Set<LadoTask["issue"]["state"]>(["done", "cancelled"]);

/**
 * Whether the board marks the task as a production incident: LADO's log watch
 * created it, it neither completed, was cancelled, nor closed unmerged, and
 * its issue is not closed.
 */
export function ladoLogIncidentShown(task: { source?: LadoTask["source"]; state: LadoTask["state"]; issue?: Pick<LadoTask["issue"], "state"> }): boolean {
	return task.source === "log" && !settledStates.has(task.state) && !(task.issue && closedIssueStates.has(task.issue.state));
}

/** Each task's fingerprint; when two name one task, the one seen more often. */
export function ladoLogIncidentsByTask(fingerprints: readonly LadoLogFingerprint[] | undefined): Map<string, LadoLogFingerprint> {
	const byTask = new Map<string, LadoLogFingerprint>();
	for (const fingerprint of fingerprints ?? []) {
		if (!fingerprint.taskId) continue;
		const known = byTask.get(fingerprint.taskId);
		if (!known || fingerprint.count > known.count) byTask.set(fingerprint.taskId, fingerprint);
	}
	return byTask;
}
