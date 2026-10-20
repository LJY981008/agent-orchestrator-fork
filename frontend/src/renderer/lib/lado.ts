// LADO fork: daemon calls for repository connections, issue-backed tasks, plans
// and approvals (docs/LJY-PRD/TDD.md §5.1, §5.2 and §13). LADO routes live on
// this computer's daemon only; remote hosts never serve them.
import type { components } from "../../api/schema";
import { apiClient, apiErrorCode, apiErrorDetails, apiErrorMessage } from "./api-client";
import { aoBridge } from "./bridge";

export type LadoProjectSettings = components["schemas"]["LadoProjectSettingsResponse"];
export type LadoTask = components["schemas"]["LadoTaskResponse"];
export type LadoIssue = components["schemas"]["LadoIssueResponse"];
export type LadoProjectIssues = components["schemas"]["LadoProjectIssuesResponse"];
export type LadoRepository = components["schemas"]["LadoRepositoryResponse"];
export type LadoConnectionCheck = components["schemas"]["LadoConnectionCheckResponse"];
export type LadoConnectionRequest = components["schemas"]["CreateLadoConnectionRequest"];
export type LadoClonePlacement = NonNullable<LadoConnectionRequest["placement"]>;
export type LadoConnection = components["schemas"]["LadoConnectionResponse"];
export type LadoImportResult = components["schemas"]["ImportLadoTasksResponse"];
export type LadoTaskView = components["schemas"]["LadoTaskViewResponse"];
export type LadoIssueComment = components["schemas"]["LadoIssueCommentResponse"];
export type LadoPlan = components["schemas"]["LadoPlanResponse"];
export type LadoPlanRevision = components["schemas"]["LadoPlanRevisionResponse"];
export type LadoPlanProblem = components["schemas"]["LadoPlanProblemResponse"];
export type LadoPlanRequest = components["schemas"]["LadoPlanRequestResponse"];
export type LadoPlanApproval = components["schemas"]["LadoPlanApprovalResponse"];

/**
 * A failed LADO call. It keeps the daemon's error code and details, so the
 * app can say what went wrong in the developer's language (lado-errors.ts).
 */
export class LadoApiError extends Error {
	constructor(
		message: string,
		readonly code?: string,
		readonly details?: Record<string, unknown>,
	) {
		super(message);
		this.name = "LadoApiError";
	}
}

function ladoError(error: unknown, fallback: string): LadoApiError {
	return new LadoApiError(apiErrorMessage(error, fallback), apiErrorCode(error), apiErrorDetails(error));
}

/** Every LADO query key starts with this root, so one invalidation reaches all. */
export const ladoQueryRoot = ["lado"] as const;
export const ladoProjectSettingsQueryKey = (projectId: string) => ["lado", "settings", projectId] as const;
export const ladoTasksQueryKey = (projectId: string) => ["lado", "tasks", projectId] as const;
export const ladoProjectIssuesQueryKey = (projectId: string) => ["lado", "issues", projectId] as const;
export const ladoProjectRepositoriesQueryKey = (projectId: string) => ["lado", "repositories", projectId] as const;
export const ladoTaskViewQueryKey = (taskId: string) => ["lado", "task", taskId] as const;
/** Plan revisions are pinned, but whether one is the latest or approved changes. */
export const ladoPlanQueryRoot = ["lado", "plan"] as const;
export const ladoPlanQueryKey = (revisionId: string) => ["lado", "plan", revisionId] as const;

export async function fetchLadoProjectSettings(projectId: string): Promise<LadoProjectSettings> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/settings", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not read the project's LADO settings");
	return data;
}

export async function fetchLadoTasks(projectId: string): Promise<LadoTask[]> {
	const { data, error } = await apiClient.GET("/api/v1/lado/tasks", {
		params: { query: { projectId } },
	});
	if (error) throw ladoError(error, "Could not load tasks");
	return data?.tasks ?? [];
}

export async function createLadoTask(input: {
	projectId: string;
	repository?: string;
	text: string;
	agent?: string;
	model?: string;
}): Promise<LadoTask> {
	const { data, error } = await apiClient.POST("/api/v1/lado/tasks", {
		body: {
			projectId: input.projectId,
			...(input.repository ? { repository: input.repository } : {}),
			text: input.text,
			...(input.agent ? { agent: input.agent } : {}),
			...(input.model ? { model: input.model } : {}),
		},
	});
	if (error) throw ladoError(error, "Could not create the issue");
	return data;
}

export async function cancelLadoTask(taskId: string): Promise<LadoTask> {
	const { data, error } = await apiClient.POST("/api/v1/lado/tasks/{taskId}/cancel", {
		params: { path: { taskId } },
	});
	if (error) throw ladoError(error, "Could not cancel the task");
	return data;
}

export async function fetchLadoProjectIssues(projectId: string): Promise<LadoProjectIssues> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/issues", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not list the repository's issues");
	return { issues: data?.issues ?? [], failures: data?.failures ?? [] };
}

/** The repositories a project's issues can live in: one, or each workspace repository. */
export async function fetchLadoProjectRepositories(projectId: string): Promise<LadoRepository[]> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/repositories", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not list the project's repositories");
	return data?.repositories ?? [];
}

/**
 * How a card names an issue: "#12" in a project with one repository, and
 * "back#12" in a workspace, where the number alone is ambiguous.
 */
export function ladoIssueLabel(number: number, repositoryRef: string, repositories: readonly LadoRepository[]): string {
	if (repositories.length <= 1) return `#${number}`;
	const repository = repositories.find((candidate) => candidate.ref === repositoryRef);
	const name = repository?.name ?? repositoryRef.slice(repositoryRef.indexOf(":") + 1).replace(/@.*$/, "");
	return `${name}#${number}`;
}

export async function importLadoTasks(projectId: string, issueRefs: string[]): Promise<LadoImportResult> {
	const { data, error } = await apiClient.POST("/api/v1/lado/tasks/imports", {
		body: { projectId, issueRefs },
	});
	if (error) throw ladoError(error, "Could not import the issues");
	return data;
}

export async function checkLadoConnection(input: LadoConnectionRequest): Promise<LadoConnectionCheck> {
	const { data, error } = await apiClient.POST("/api/v1/lado/connections/check", {
		body: { url: input.url, token: input.token, sshKeyPath: input.sshKeyPath },
	});
	if (error) throw ladoError(error, "Could not check the repository");
	return data;
}

/** Creates the connection; the placement fields matter only when connecting clones. */
export async function createLadoConnection(input: LadoConnectionRequest): Promise<LadoConnection> {
	const { data, error } = await apiClient.POST("/api/v1/lado/connections", { body: input });
	if (error) throw ladoError(error, "Could not connect the repository");
	return data;
}

/** The task view: the task, its issue and comments read again, and its plan history. */
export async function fetchLadoTaskView(taskId: string): Promise<LadoTaskView> {
	const { data, error } = await apiClient.GET("/api/v1/lado/tasks/{taskId}", {
		params: { path: { taskId } },
	});
	if (error) throw ladoError(error, "Could not load the task");
	return data;
}

/**
 * Asks the project's orchestrator for a plan. With a comment it converts that
 * comment; with a note it changes the latest revision.
 */
export async function requestLadoPlan(
	taskId: string,
	input: { sourceCommentId?: string; note?: string } = {},
): Promise<LadoPlanRequest> {
	const { data, error } = await apiClient.POST("/api/v1/lado/tasks/{taskId}/plan-requests", {
		params: { path: { taskId } },
		body: {
			...(input.sourceCommentId ? { sourceCommentId: input.sourceCommentId } : {}),
			...(input.note ? { note: input.note } : {}),
		},
	});
	if (error) throw ladoError(error, "Could not request a plan");
	return data;
}

export async function fetchLadoPlan(revisionId: string): Promise<LadoPlan> {
	const { data, error } = await apiClient.GET("/api/v1/lado/plans/{revisionId}", {
		params: { path: { revisionId } },
	});
	if (error) throw ladoError(error, "Could not load the plan");
	return data;
}

/**
 * Approves one plan revision. In the desktop app the main process asks the
 * developer to confirm, then signs and sends the approval itself
 * (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md); null means the
 * developer cancelled. In a browser there is no main process, and the daemon
 * accepts the call only in development (AO_LADO_DEV_PRESENCE=1).
 */
export async function approveLadoPlan(revisionId: string, sha256: string): Promise<LadoPlanApproval | null> {
	// The bridge answers null when there is no desktop main process to ask.
	const result = (await aoBridge.lado?.presence?.approvePlan(revisionId)) ?? null;
	if (result) {
		if (result.status === "cancelled") return null;
		if (result.status === "error") throw new LadoApiError(result.message, result.code, result.details);
		return result.approval;
	}
	const { data, error } = await apiClient.POST("/api/v1/lado/plans/{revisionId}/approve", {
		params: { path: { revisionId } },
		body: { sha256 },
	});
	if (error) throw ladoError(error, "Could not approve the plan");
	return data;
}
