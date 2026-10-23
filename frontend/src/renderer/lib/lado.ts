// LADO fork: daemon calls for repository connections, issue-backed tasks, plans,
// approvals, rules, the queue and its runs (docs/LJY-PRD/TDD.md §5.1-§5.3 and
// §13, docs/LJY-PRD/HARNESS.md §3-§8). LADO routes live on this computer's
// daemon only; remote hosts never serve them.
import type { QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import type { LadoCeilingMode } from "../../shared/lado-presence";
import { apiClient, apiErrorCode, apiErrorDetails, apiErrorMessage } from "./api-client";
import { aoBridge } from "./bridge";

export type { LadoCeilingMode } from "../../shared/lado-presence";

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
export type LadoRule = components["schemas"]["LadoRuleResponse"];
export type LadoRuleVersion = components["schemas"]["LadoRuleVersionResponse"];
export type LadoRuleDetail = components["schemas"]["LadoRuleDetailResponse"];
export type LadoRuleOverride = components["schemas"]["LadoRuleOverrideResponse"];
export type LadoRuleStrength = LadoRule["strength"];
export type LadoRuleScope = components["schemas"]["CreateLadoRuleRequest"]["scope"];
export type LadoRuleChangeResult = components["schemas"]["LadoRuleChangeResponse"];
export type LadoRuleImpact = components["schemas"]["LadoRuleImpactResponse"];
export type LadoCreateRuleInput = components["schemas"]["CreateLadoRuleRequest"];
export type LadoUpdateRuleInput = components["schemas"]["UpdateLadoRuleRequest"];
export type LadoBindingInput = components["schemas"]["SetLadoBindingRequest"];
export type LadoEffective = components["schemas"]["LadoEffectiveResponse"];
export type LadoEffectiveRule = components["schemas"]["LadoEffectiveRuleResponse"];
export type LadoPolicyProblem = components["schemas"]["LadoPolicyProblemResponse"];
export type LadoTaskState = LadoTask["state"];
/** Why a task needs review, is blocked or failed, or what its queued plan waits for. */
export type LadoTaskStateReason = NonNullable<LadoTask["stateReason"]>;
export type LadoTaskQueue = components["schemas"]["LadoTaskQueueResponse"];
export type LadoTaskExecution = components["schemas"]["LadoTaskExecutionResponse"];
export type LadoQueueItem = components["schemas"]["LadoQueueItemResponse"];
export type LadoQueueState = LadoQueueItem["state"];
export type LadoQueueReason = NonNullable<LadoQueueItem["reason"]>;
export type LadoExecution = components["schemas"]["LadoExecutionResponse"];
export type LadoExecutionPR = components["schemas"]["LadoExecutionPRResponse"];
export type LadoDispatchSettings = components["schemas"]["LadoDispatchSettingsResponse"];
export type LadoDispatchSettingsInput = components["schemas"]["UpdateLadoDispatchSettingsRequest"];
/** A maximum run time the daemon accepts, in minutes; 0 means no limit. */
export type LadoRunMinutes = LadoDispatchSettings["maxRunMinutes"];
type LadoProjectSettingsInput = components["schemas"]["UpdateLadoProjectSettingsRequest"];

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
export const ladoTasksQueryRoot = ["lado", "tasks"] as const;
export const ladoTaskViewQueryRoot = ["lado", "task"] as const;
export const ladoRulesQueryRoot = ["lado", "rules"] as const;
export const ladoRulesQueryKey = (includeArchived: boolean) => ["lado", "rules", includeArchived] as const;
export const ladoRuleQueryRoot = ["lado", "rule"] as const;
export const ladoRuleQueryKey = (ruleId: string) => ["lado", "rule", ruleId] as const;
export const ladoEffectiveQueryRoot = ["lado", "effective"] as const;
/** A project's rules, and its tasks' rules, share the project's prefix. */
export const ladoEffectiveProjectQueryKey = (projectId: string) => ["lado", "effective", projectId] as const;
export const ladoEffectiveQueryKey = (projectId: string, taskId: string) => ["lado", "effective", projectId, taskId] as const;
export const ladoQueueQueryRoot = ["lado", "queue"] as const;
/** One project's queue, or every project's without an id. */
export const ladoQueueQueryKey = (projectId?: string) => ["lado", "queue", projectId ?? ""] as const;
export const ladoExecutionQueryKey = (sessionId: string) => ["lado", "execution", sessionId] as const;
export const ladoDispatchSettingsQueryKey = ["lado", "dispatch-settings"] as const;

/**
 * Refreshes what a rule change can reach: the rules, every effective view,
 * and the tasks, whose approvals can need review again.
 */
export function invalidateLadoRuleQueries(queryClient: QueryClient): void {
	for (const queryKey of [ladoRulesQueryRoot, ladoRuleQueryRoot, ladoEffectiveQueryRoot, ladoTasksQueryRoot, ladoTaskViewQueryRoot]) {
		void queryClient.invalidateQueries({ queryKey });
	}
}

export async function fetchLadoProjectSettings(projectId: string): Promise<LadoProjectSettings> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/settings", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not read the project's LADO settings");
	return data;
}

/**
 * Turns a project's LADO mode on or off. Turning it on is a plain call, which
 * the daemon refuses while tracker intake is on. Turning it off lets the
 * project's workers start without an approved plan, so in the desktop app the
 * main process asks the developer to confirm and signs the change; null means
 * the developer cancelled and nothing changed. In a browser the call goes to
 * the daemon directly, which accepts it only in development
 * (AO_LADO_DEV_PRESENCE=1). Queued work waits while the mode is off; running
 * workers go on.
 */
export async function setLadoMode(projectId: string, on: boolean): Promise<LadoProjectSettings | null> {
	if (!on) {
		// The bridge answers null when there is no desktop main process to ask.
		const result = (await aoBridge.lado?.presence?.setLadoMode(projectId, false)) ?? null;
		if (result) {
			if (result.status === "cancelled") return null;
			if (result.status === "error") throw new LadoApiError(result.message, result.code, result.details);
			return result.settings;
		}
	}
	return putLadoProjectSettings(projectId, { ladoMode: on }, on ? "Could not turn on LADO mode" : "Could not turn off LADO mode");
}

/** Changes how many executions run at once in the project; it needs no confirmation. */
export async function updateLadoProjectSettings(projectId: string, input: { maxConcurrent: number }): Promise<LadoProjectSettings> {
	return putLadoProjectSettings(projectId, { maxConcurrent: input.maxConcurrent }, "Could not change the project's LADO settings");
}

async function putLadoProjectSettings(
	projectId: string,
	body: LadoProjectSettingsInput,
	fallback: string,
): Promise<LadoProjectSettings> {
	const { data, error } = await apiClient.PUT("/api/v1/lado/projects/{projectId}/settings", {
		params: { path: { projectId } },
		body,
	});
	if (error) throw ladoError(error, fallback);
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
 * Approves one plan revision together with the rules that apply to it. In the
 * desktop app the main process reads the task's rules, asks the developer to
 * confirm, then signs and sends the approval itself
 * (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md); null means the
 * developer cancelled. In a browser there is no main process: the call sends
 * policySha256, the effective rules' snapshotSha256, and the daemon accepts it
 * only in development (AO_LADO_DEV_PRESENCE=1).
 */
export async function approveLadoPlan(
	revisionId: string,
	sha256: string,
	policySha256: string,
): Promise<LadoPlanApproval | null> {
	// The bridge answers null when there is no desktop main process to ask.
	const result = (await aoBridge.lado?.presence?.approvePlan(revisionId)) ?? null;
	if (result) {
		if (result.status === "cancelled") return null;
		if (result.status === "error") throw new LadoApiError(result.message, result.code, result.details);
		return result.approval;
	}
	const { data, error } = await apiClient.POST("/api/v1/lado/plans/{revisionId}/approve", {
		params: { path: { revisionId } },
		body: { sha256, policySha256 },
	});
	if (error) throw ladoError(error, "Could not approve the plan");
	return data;
}

export async function fetchLadoRules(includeArchived = false): Promise<LadoRule[]> {
	const { data, error } = await apiClient.GET("/api/v1/lado/rules", {
		params: { query: { includeArchived } },
	});
	if (error) throw ladoError(error, "Could not load the rules");
	return data?.rules ?? [];
}

/** One rule with every version, oldest first. */
export async function fetchLadoRule(ruleId: string): Promise<LadoRuleDetail> {
	const { data, error } = await apiClient.GET("/api/v1/lado/rules/{ruleId}", {
		params: { path: { ruleId } },
	});
	if (error) throw ladoError(error, "Could not load the rule");
	return data;
}

/** With dryRun the daemon only says what the change would reach. */
export async function createLadoRule(input: LadoCreateRuleInput): Promise<LadoRuleChangeResult> {
	const { data, error } = await apiClient.POST("/api/v1/lado/rules", { body: input });
	if (error) throw ladoError(error, "Could not create the rule");
	return data;
}

/**
 * Edits, archives or unarchives a rule from its active version. The
 * permission ceiling's mode goes through changeLadoCeiling, except as a dry
 * run, which needs no confirmation.
 */
export async function updateLadoRule(ruleId: string, input: LadoUpdateRuleInput): Promise<LadoRuleChangeResult> {
	const { data, error } = await apiClient.PATCH("/api/v1/lado/rules/{ruleId}", {
		params: { path: { ruleId } },
		body: input,
	});
	if (error) throw ladoError(error, "Could not change the rule");
	return data;
}

/** Deletes a user rule that no approval used; the daemon refuses any other. */
export async function deleteLadoRule(ruleId: string): Promise<void> {
	const { error } = await apiClient.DELETE("/api/v1/lado/rules/{ruleId}", {
		params: { path: { ruleId } },
	});
	if (error) throw ladoError(error, "Could not delete the rule");
}

/** Turns a rule on or off at Global, or forces or inherits it for a project or task. */
export async function setLadoBinding(input: LadoBindingInput): Promise<LadoRuleChangeResult> {
	const { data, error } = await apiClient.PUT("/api/v1/lado/bindings", { body: input });
	if (error) throw ladoError(error, "Could not change where the rule applies");
	return data;
}

/**
 * The rules that apply to a project, or to one of its tasks at its latest
 * plan revision. The daemon takes exactly one of the two ids.
 */
export async function fetchLadoEffective(projectId: string, taskId?: string): Promise<LadoEffective> {
	const { data, error } = await apiClient.GET("/api/v1/lado/effective", {
		params: { query: taskId ? { taskId } : { projectId } },
	});
	if (error) throw ladoError(error, "Could not load the rules that apply");
	return data;
}

/**
 * Changes the permission ceiling's mode. Like an approval, the desktop app's
 * main process reads the rule, asks the developer to confirm, and signs the
 * change; null means the developer cancelled. In a browser the call reads the
 * active version and patches it directly, which the daemon accepts only in
 * development.
 */
export async function changeLadoCeiling(ruleId: string, mode: LadoCeilingMode): Promise<LadoRule | null> {
	const result = (await aoBridge.lado?.presence?.changeRule(ruleId, mode)) ?? null;
	if (result) {
		if (result.status === "cancelled") return null;
		if (result.status === "error") throw new LadoApiError(result.message, result.code, result.details);
		return result.rule;
	}
	const { rule } = await fetchLadoRule(ruleId);
	const changed = await updateLadoRule(ruleId, { baseVersion: rule.version, ceiling: { mode } });
	return changed.rule ?? rule;
}

/** The open queue entries, of one project or of every project, in the order the dispatcher takes them. */
export async function fetchLadoQueue(projectId?: string): Promise<LadoQueueItem[]> {
	const { data, error } = await apiClient.GET("/api/v1/lado/queue", {
		params: { query: projectId ? { projectId } : {} },
	});
	if (error) throw ladoError(error, "Could not load the queue");
	return data?.entries ?? [];
}

/**
 * The execution a worker session runs: its task, plan, rule snapshot, base
 * and run. Null for a session LADO did not start.
 */
export async function fetchLadoExecution(sessionId: string): Promise<LadoExecution | null> {
	const { data, error } = await apiClient.GET("/api/v1/lado/executions/{sessionId}", {
		params: { path: { sessionId } },
	});
	if (error) {
		if (apiErrorCode(error) === "LADO_EXECUTION_NOT_FOUND") return null;
		throw ladoError(error, "Could not load the run");
	}
	return data;
}

/** The cap on running executions, their maximum run time and the memory hold. */
export async function fetchLadoDispatchSettings(): Promise<LadoDispatchSettings> {
	const { data, error } = await apiClient.GET("/api/v1/lado/dispatch-settings");
	if (error) throw ladoError(error, "Could not load the run settings");
	return data;
}

/** Replaces all three dispatch settings; it needs no confirmation. */
export async function updateLadoDispatchSettings(input: LadoDispatchSettingsInput): Promise<LadoDispatchSettings> {
	const { data, error } = await apiClient.PUT("/api/v1/lado/dispatch-settings", {
		body: {
			maxConcurrent: input.maxConcurrent,
			maxRunMinutes: input.maxRunMinutes,
			holdOnMemoryTight: input.holdOnMemoryTight,
		},
	});
	if (error) throw ladoError(error, "Could not save the run settings");
	return data;
}
