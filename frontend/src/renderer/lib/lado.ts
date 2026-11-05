// LADO fork: daemon calls for repository connections, issue-backed tasks, plans,
// approvals, rules, runs, their verification and integration approval
// (docs/LJY-PRD/TDD.md §5.1-§5.3, §7, §8 and §13, docs/LJY-PRD/HARNESS.md
// §3-§8 and §14). LADO routes live on this computer's
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
// Plan workers (docs/LJY-PRD/TDD.md §5.1): where a request's worker is, a
// draft's review and what making a revision took.
export type LadoPlanWorker = components["schemas"]["LadoPlanWorkerResponse"];
export type LadoPlanWorkerPhase = LadoPlanWorker["phase"];
export type LadoPlanCritique = components["schemas"]["LadoPlanCritiqueResponse"];
export type LadoPlanFinding = components["schemas"]["LadoPlanFindingResponse"];
export type LadoPlanMetrics = components["schemas"]["LadoPlanMetricsResponse"];
export type LadoPlanReviewSettings = components["schemas"]["LadoPlanReviewSettings"];
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
export type LadoTaskParts = components["schemas"]["LadoTaskPartsResponse"];
export type LadoTaskPart = components["schemas"]["LadoTaskPartResponse"];
export type LadoTaskPartState = LadoTaskPart["state"];
export type LadoTaskPartWaitingReason = NonNullable<LadoTaskPart["waitingReason"]>;
export type LadoPlanPart = components["schemas"]["LadoPlanPartResponse"];
export type LadoTaskTimings = components["schemas"]["LadoTaskTimingsResponse"];
export type LadoTaskTimeline = components["schemas"]["LadoTaskTimelineResponse"];
export type LadoTimingPoint = components["schemas"]["LadoTimingPointResponse"];
export type LadoTimingStage = LadoTimingPoint["stage"];
export type LadoSessionMetrics = components["schemas"]["LadoSessionMetricsResponse"];
export type LadoSessionRole = LadoSessionMetrics["role"];
export type LadoTimingsSummary = components["schemas"]["LadoTimingsSummaryResponse"];
export type LadoTaskUsage = components["schemas"]["LadoTaskUsageResponse"];
export type LadoTaskUsageSummary = components["schemas"]["LadoTaskUsageSummaryResponse"];
export type LadoSessionUsage = components["schemas"]["LadoSessionUsageResponse"];
export type LadoUsageTotals = components["schemas"]["LadoUsageTotalsResponse"];
export type LadoProjectUsage = components["schemas"]["LadoProjectUsageResponse"];
export type LadoRelay = components["schemas"]["LadoRelayResponse"];
export type LadoRelayState = LadoRelay["state"];
export type LadoAlert = components["schemas"]["LadoAlertResponse"];
export type LadoUnseenAlertCounts = components["schemas"]["LadoUnseenAlertCountsResponse"];
export type LadoAlertKind = LadoAlert["kind"];
export type LadoWatchSettings = components["schemas"]["LadoWatchSettingsResponse"];
export type LadoWatchSettingsResolved = components["schemas"]["LadoWatchSettingsResolved"];
export type LadoWatchSettingsValues = components["schemas"]["LadoWatchSettingsValues"];
export type LadoQueueItem = components["schemas"]["LadoQueueItemResponse"];
export type LadoQueueState = LadoQueueItem["state"];
export type LadoQueueReason = NonNullable<LadoQueueItem["reason"]>;
export type LadoExecution = components["schemas"]["LadoExecutionResponse"];
export type LadoExecutionPR = components["schemas"]["LadoExecutionPRResponse"];
export type LadoExecutionResolve = components["schemas"]["LadoExecutionResolveResponse"];
export type LadoDispatchSettings = components["schemas"]["LadoDispatchSettingsResponse"];
export type LadoDispatchSettingsInput = components["schemas"]["UpdateLadoDispatchSettingsRequest"];
/** A maximum run time the daemon accepts, in minutes; 0 means no limit. */
export type LadoRunMinutes = LadoDispatchSettings["maxRunMinutes"];
type LadoProjectSettingsInput = components["schemas"]["UpdateLadoProjectSettingsRequest"];
/** What LADO verified on a run's pull request head (docs/LJY-PRD/TDD.md §8). */
export type LadoVerification = components["schemas"]["LadoVerificationResponse"];
export type LadoVerifyItem = components["schemas"]["LadoVerifyItemResponse"];
export type LadoVerifyItemDetails = components["schemas"]["LadoVerifyItemDetailsResponse"];
export type LadoVerifyKind = LadoVerifyItem["kind"];
export type LadoVerifyOutcome = LadoVerifyItem["outcome"];
/** An item's code; a plain pass has none. */
export type LadoVerifyCode = NonNullable<LadoVerifyItem["code"]>;
export type LadoVerifyCommandRun = components["schemas"]["LadoVerifyCommandRunResponse"];
export type LadoEvidenceCriterion = components["schemas"]["LadoEvidenceCriterionResponse"];
/** The current head's integration approval, with how posting its status went. */
export type LadoIntegrationApproval = components["schemas"]["LadoIntegrationApprovalResponse"];
export type LadoIntegrationResult = components["schemas"]["LadoIntegrationApproveResponse"];
export type LadoRunChecksResult = components["schemas"]["LadoRunChecksResponse"];
export type LadoProjectProtection = components["schemas"]["LadoProjectProtectionResponse"];
export type LadoBranchProtection = components["schemas"]["LadoBranchProtectionResponse"];
export type LadoCheckCommand = components["schemas"]["LadoCheckCommandBody"];
type LadoIntegrationInput = components["schemas"]["ApproveLadoIntegrationRequest"];
/** A project's knowledge folder: its notes, its owner and the folders it can import from (issue #24). */
export type LadoKnowledge = components["schemas"]["LadoKnowledgeResponse"];
export type LadoKnowledgeNote = components["schemas"]["LadoKnowledgeNoteResponse"];
export type LadoKnowledgeNoteState = LadoKnowledgeNote["state"];
export type LadoKnowledgeImportable = components["schemas"]["LadoKnowledgeImportableResponse"];
export type LadoKnowledgeImportResult = components["schemas"]["LadoKnowledgeImportResponse"];
export type LadoKnowledgeImportSkip = components["schemas"]["LadoKnowledgeImportSkipResponse"];
export type LadoKnowledgeImportSkipReason = LadoKnowledgeImportSkip["reason"];
/** Where an import reads notes from: Claude Code's memory of a path, or another knowledge folder. */
export type LadoKnowledgeImportInput = components["schemas"]["ImportLadoKnowledgeRequest"];

/** The protected rule whose globs keep workers away from protected paths. */
export const LADO_PROTECTED_PATHS_RULE_ID = "paths.protected";

/** The change kinds a plan declares in change_kinds, in display order. */
export const LADO_CHANGE_KINDS = ["db_schema", "external_api", "auth", "data_deletion"] as const;
export type LadoChangeKind = (typeof LADO_CHANGE_KINDS)[number];

/** Whether a rule holds a plan worker role's instructions, which reach plan workers only. */
export function ladoIsPlanRole(rule: { enforcement: readonly string[] }): boolean {
	return rule.enforcement.includes("plan_role");
}

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

/** A failed daemon call as a LadoApiError, with fallback when the daemon sent no message. */
export function ladoError(error: unknown, fallback: string): LadoApiError {
	return new LadoApiError(apiErrorMessage(error, fallback), apiErrorCode(error), apiErrorDetails(error));
}

/** Every LADO query key starts with this root, so one invalidation reaches all. */
export const ladoQueryRoot = ["lado"] as const;
export const ladoProjectSettingsQueryKey = (projectId: string) => ["lado", "settings", projectId] as const;
export const ladoTasksQueryKey = (projectId: string) => ["lado", "tasks", projectId] as const;
export const ladoProjectIssuesQueryKey = (projectId: string) => ["lado", "issues", projectId] as const;
export const ladoProjectRepositoriesQueryKey = (projectId: string) => ["lado", "repositories", projectId] as const;
export const ladoTaskViewQueryKey = (taskId: string) => ["lado", "task", taskId] as const;
/** Under the task view's key, so whatever refreshes the task refreshes its parts. */
export const ladoTaskPartsQueryKey = (taskId: string) => ["lado", "task", taskId, "parts"] as const;
/** Under the task view's key too, so a task event refreshes the times it recorded. */
export const ladoTaskTimingsQueryKey = (taskId: string) => ["lado", "task", taskId, "timings"] as const;
export const ladoTimingsQueryKey = (projectId: string, limit?: number) => ["lado", "timings", projectId, limit ?? null] as const;
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
export const ladoExecutionQueryRoot = ["lado", "execution"] as const;
export const ladoExecutionQueryKey = (sessionId: string) => ["lado", "execution", sessionId] as const;
/** A run's logs share the session's prefix; the run's state is part of the key, so a run that finished reads its output again. */
export const ladoCheckLogSessionQueryKey = (sessionId: string) => ["lado", "check-log", sessionId] as const;
export const ladoCheckLogQueryKey = (sessionId: string, checkRunId: string, status = "") => ["lado", "check-log", sessionId, checkRunId, status] as const;
export const ladoProtectionQueryKey = (projectId: string) => ["lado", "protection", projectId] as const;
export const ladoKnowledgeQueryKey = (projectId: string) => ["lado", "knowledge", projectId] as const;
export const ladoDispatchSettingsQueryKey = ["lado", "dispatch-settings"] as const;
/** Under the task view's key, so LADO's task events refresh it as well. */
export const ladoTaskUsageQueryKey = (taskId: string) => ["lado", "task", taskId, "usage"] as const;
/**
 * A task's relays of developer instructions (issue #49). Not under the task
 * view's key: LADO's task events refresh it by name (lado-events.ts).
 */
export const ladoTaskRelaysQueryKey = (taskId: string) => ["lado", "task-relays", taskId] as const;
export const ladoProjectUsageQueryKey = (projectId: string) => ["lado", "usage", projectId] as const;
/** Every alert list of a project: the board's unseen ones and each task's. */
export const ladoAlertsProjectQueryKey = (projectId: string) => ["lado", "alerts", projectId] as const;
/** Under the project's alerts key, so the alert events and a seen mark refresh it. */
export const ladoUnseenAlertCountsQueryKey = (projectId: string) => ["lado", "alerts", projectId, "unseen-counts"] as const;
export const ladoAlertsQueryKey = (projectId: string, filter: LadoAlertFilter) =>
	["lado", "alerts", projectId, filter.taskId ?? "", filter.unseen === true] as const;
export const ladoWatchSettingsQueryRoot = ["lado", "watch-settings"] as const;
/** The global row's key without a project. */
export const ladoWatchSettingsQueryKey = (projectId?: string) => ["lado", "watch-settings", projectId ?? ""] as const;

/** Which of a project's alerts a list reads. */
export type LadoAlertFilter = { taskId?: string; unseen?: boolean };

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

/**
 * Replaces the project's check commands. The daemon runs them in each run's
 * worktree outside any agent sandbox, so in the desktop app the main process
 * names the change, asks the developer to confirm and signs it; null means
 * the developer cancelled. In a browser the call goes to the daemon directly,
 * which accepts it only in development.
 */
export async function updateLadoProjectChecks(
	projectId: string,
	commands: LadoCheckCommand[],
	base: string,
): Promise<LadoProjectSettings | null> {
	// The bridge answers null when there is no desktop main process to ask.
	const result = (await aoBridge.lado?.presence?.changeProjectChecks(projectId, commands)) ?? null;
	if (result) {
		if (result.status === "cancelled") return null;
		if (result.status === "error") throw new LadoApiError(result.message, result.code, result.details);
		return result.settings;
	}
	// The change names the list it starts from, the one the card shows; the
	// daemon refuses it once the list moved on.
	return putLadoProjectSettings(projectId, { checkCommands: commands, checkCommandsBase: base }, "Could not change the check commands");
}

/** Changes how long each check command may run, in minutes; it needs no confirmation. */
export async function updateLadoCheckTimeout(projectId: string, minutes: number): Promise<LadoProjectSettings> {
	return putLadoProjectSettings(projectId, { checkTimeoutMinutes: minutes }, "Could not change the check time limit");
}

/** Changes how many executions run at once in the project; it needs no confirmation. */
export async function updateLadoProjectSettings(projectId: string, input: { maxConcurrent: number }): Promise<LadoProjectSettings> {
	return putLadoProjectSettings(projectId, { maxConcurrent: input.maxConcurrent }, "Could not change the project's LADO settings");
}

/** Whether a task waits for another's overlapping run, and the globs whose overlap does not count. */
export type LadoProjectOverlapInput = { overlapWait?: boolean; overlapIgnore?: string[]; overlapIgnoreDefaults?: boolean };

/** Changes the project's overlap wait or the globs whose overlap does not count; neither needs presence. */
export async function updateLadoProjectOverlap(projectId: string, input: LadoProjectOverlapInput): Promise<LadoProjectSettings> {
	return putLadoProjectSettings(projectId, input, "Could not change the project's LADO settings");
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

/** How many times LADO tries to start a task's worker before the task fails. */
export const LADO_MAX_ATTEMPTS = 3;

/**
 * How many resolve workers in a row may fail for one PR before LADO stops and
 * asks the developer (backend domain.LadoMaxResolveAttempts).
 */
export const LADO_MAX_RESOLVE_ATTEMPTS = 2;

// One map per task list, shared by every board card that reads the list.
const tasksBySession = new WeakMap<readonly LadoTask[], Map<string, LadoTask>>();

/** The task whose newest execution runs in the session, if LADO started it. */
export function ladoTaskForSession(tasks: readonly LadoTask[], sessionId: string): LadoTask | undefined {
	let bySession = tasksBySession.get(tasks);
	if (!bySession) {
		bySession = new Map();
		for (const task of tasks) if (task.execution) bySession.set(task.execution.sessionId, task);
		tasksBySession.set(tasks, bySession);
	}
	return bySession.get(sessionId);
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

/** The parts run of the task's newest approval: its integration branch and each part. */
export async function fetchLadoTaskParts(taskId: string): Promise<LadoTaskParts> {
	const { data, error } = await apiClient.GET("/api/v1/lado/tasks/{taskId}/parts", {
		params: { path: { taskId } },
	});
	if (error) throw ladoError(error, "Could not load the task's parts");
	return data;
}

/**
 * How long the task spent between its stages, and what each of its plan and
 * run sessions spent reading and searching before its first edit.
 */
export async function fetchLadoTaskTimings(taskId: string): Promise<LadoTaskTimings> {
	const { data, error } = await apiClient.GET("/api/v1/lado/tasks/{taskId}/timings", {
		params: { path: { taskId } },
	});
	if (error) throw ladoError(error, "Could not load the task's timings");
	return data;
}

/**
 * A project's baseline: its newest tasks' timings, the median and longest
 * time between adjacent stages, and each session role's median investigation.
 * Without a limit the daemon sums up its default number of tasks.
 */
export async function fetchLadoTimings(projectId: string, limit?: number): Promise<LadoTimingsSummary> {
	const { data, error } = await apiClient.GET("/api/v1/lado/timings", {
		params: { query: limit === undefined ? { projectId } : { projectId, limit } },
	});
	if (error) throw ladoError(error, "Could not load the project's timings");
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

/**
 * Replaces the protected paths' globs. Like the ceiling, the desktop app's
 * main process reads the rule, asks the developer to confirm and signs the
 * change; null means the developer cancelled. In a browser the call reads
 * the active version and patches it directly, which the daemon accepts only
 * in development.
 */
export async function changeLadoProtectedPaths(globs: string[]): Promise<LadoRule | null> {
	const result = (await aoBridge.lado?.presence?.changeRule(LADO_PROTECTED_PATHS_RULE_ID, { globs })) ?? null;
	if (result) {
		if (result.status === "cancelled") return null;
		if (result.status === "error") throw new LadoApiError(result.message, result.code, result.details);
		return result.rule;
	}
	const { rule } = await fetchLadoRule(LADO_PROTECTED_PATHS_RULE_ID);
	const changed = await updateLadoRule(LADO_PROTECTED_PATHS_RULE_ID, { baseVersion: rule.version, paths: { globs } });
	return changed.rule ?? rule;
}

/**
 * The execution a worker session runs: its task, plan, rule snapshot, base,
 * run, pull request, the verification of its current head and that head's
 * integration approval. Null for a session LADO did not start.
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

/**
 * Approves the integration of a run's pull request head (docs/LJY-PRD/TDD.md
 * §7.1). In the desktop app the main process reads the run, sends the dry
 * run, asks the developer to confirm what it verified, then signs and sends
 * the approval itself; null means the developer cancelled. In a browser the
 * call sends the dry run and then the approval with the dry run's values,
 * which the daemon accepts only in development (AO_LADO_DEV_PRESENCE=1).
 */
export async function approveLadoIntegration(sessionId: string): Promise<LadoIntegrationResult | null> {
	// The bridge answers null when there is no desktop main process to ask.
	const result = (await aoBridge.lado?.presence?.approveIntegration(sessionId)) ?? null;
	if (result) {
		if (result.status === "cancelled") return null;
		if (result.status === "error") throw new LadoApiError(result.message, result.code, result.details);
		return result.integration;
	}
	const execution = await fetchLadoExecution(sessionId);
	if (!execution) throw new LadoApiError("This session is not a LADO run.", "LADO_EXECUTION_NOT_FOUND", { sessionId });
	const { pr } = execution;
	if (!pr || pr.merged || pr.closed) {
		throw new LadoApiError("This run has no open pull request to integrate.", "LADO_EXECUTION_NO_PR", { sessionId });
	}
	const preview = await postLadoIntegration(sessionId, { prUrl: pr.url, headSha: pr.headSha ?? "", dryRun: true });
	return postLadoIntegration(sessionId, {
		prUrl: preview.verification.prUrl,
		headSha: preview.verification.headSha,
		verificationSha256: preview.verificationSha256,
		acknowledge: preview.confirm,
	});
}

async function postLadoIntegration(sessionId: string, body: LadoIntegrationInput): Promise<LadoIntegrationResult> {
	const { data, error } = await apiClient.POST("/api/v1/lado/executions/{sessionId}/integration-approve", {
		params: { path: { sessionId } },
		body,
	});
	if (error) throw ladoError(error, "Could not approve the integration");
	return data;
}

/** Runs every check command again for the run's current head; the run's verification waits for them. */
export async function runLadoChecks(sessionId: string): Promise<LadoRunChecksResult> {
	const { data, error } = await apiClient.POST("/api/v1/lado/executions/{sessionId}/checks/run", {
		params: { path: { sessionId } },
	});
	if (error) throw ladoError(error, "Could not run the checks again");
	return data;
}

/**
 * Posts the lado/integration status of the run's approved current head again,
 * its attempts started over, as after the token was fixed. It needs no
 * confirmation: the head is approved already.
 */
export async function repostLadoIntegrationStatus(sessionId: string): Promise<LadoIntegrationApproval> {
	const { data, error } = await apiClient.POST("/api/v1/lado/executions/{sessionId}/integration-status", {
		params: { path: { sessionId } },
	});
	if (error) throw ladoError(error, "Could not post the status again");
	return data;
}

/** A check command run's combined output, its last 1 MiB; it can be empty. */
export async function fetchLadoCheckLog(sessionId: string, checkRunId: string): Promise<string> {
	const { data, error } = await apiClient.GET("/api/v1/lado/executions/{sessionId}/checks/{checkRunId}/log", {
		params: { path: { sessionId, checkRunId } },
		parseAs: "text",
	});
	if (error) throw ladoError(error, "Could not load the check's output");
	return data ?? "";
}

/**
 * Whether each of the project's repositories protects its base branch as
 * LADO needs (docs/LJY-PRD/TDD.md §7.1). With refresh the daemon reads the
 * rules from the host again instead of keeping its cached read.
 */
export async function fetchLadoProtection(projectId: string, refresh = false): Promise<LadoProjectProtection> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/protection", {
		params: { path: { projectId }, query: refresh ? { refresh: true } : {} },
	});
	if (error) throw ladoError(error, "Could not read the branch protection");
	return data;
}

/**
 * A project's knowledge folder (docs/LJY-PRD/adr/0010-project-knowledge-store.md):
 * its notes with their freshness against the base branch, whether the folder
 * belongs to another repository, and the other folders it can import from.
 */
export async function fetchLadoKnowledge(projectId: string): Promise<LadoKnowledge> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/knowledge", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not read the project's knowledge");
	return data;
}

/**
 * Copies notes into the project's knowledge from Claude Code's memory (of the
 * given path, or of the project's path when none is given) or from another
 * knowledge folder. The source is only read; a slug the project has is skipped.
 */
export async function importLadoKnowledge(projectId: string, input: LadoKnowledgeImportInput): Promise<LadoKnowledgeImportResult> {
	const body: LadoKnowledgeImportInput = { source: input.source };
	const path = input.path?.trim();
	if (input.source === "claude_memory" && path) body.path = path;
	if (input.source === "project" && input.fromProjectId) body.fromProjectId = input.fromProjectId;
	const { data, error } = await apiClient.POST("/api/v1/lado/projects/{projectId}/knowledge/imports", {
		params: { path: { projectId } },
		body,
	});
	if (error) throw ladoError(error, "Could not import the notes");
	return data;
}

/**
 * Makes the project the owner of a knowledge folder another repository wrote,
 * so its sessions read and write it again; answers the folder as it is now.
 */
export async function claimLadoKnowledge(projectId: string): Promise<LadoKnowledge> {
	const { data, error } = await apiClient.POST("/api/v1/lado/projects/{projectId}/knowledge/claim", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not take over the knowledge folder");
	return data;
}

/**
 * Merges a run's pull request at its approved head through AO's merge route;
 * LADO's merge gate lets it through only with an integration approval of that
 * head (docs/LJY-PRD/TDD.md §7.2).
 */
export async function mergeLadoPR(pr: { number: number; url: string; headSha: string }): Promise<void> {
	const { error } = await apiClient.POST("/api/v1/prs/{id}/merge", {
		params: { path: { id: String(pr.number) } },
		body: { prUrl: pr.url, expectedHeadSha: pr.headSha },
	});
	if (error) throw ladoError(error, "Could not merge the pull request");
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

/**
 * What a task's sessions spent: the totals with the elapsed time, and each
 * plan and execution session's cost, tokens, context and compactions.
 */
export async function fetchLadoTaskUsage(taskId: string): Promise<LadoTaskUsage> {
	const { data, error } = await apiClient.GET("/api/v1/lado/tasks/{taskId}/usage", {
		params: { path: { taskId } },
	});
	if (error) throw ladoError(error, "Could not load the task's usage");
	return data;
}

/** The developer instructions LADO relayed to a task's workers, newest first (issue #49). */
export async function fetchLadoTaskRelays(taskId: string): Promise<LadoRelay[]> {
	const { data, error } = await apiClient.GET("/api/v1/lado/tasks/{taskId}/relays", {
		params: { path: { taskId } },
	});
	if (error) throw ladoError(error, "Could not load the task's relays");
	return data.relays;
}

/** The usage totals of each of a project's tasks, for its board. */
export async function fetchLadoProjectUsage(projectId: string): Promise<LadoProjectUsage> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/usage", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not load the project's usage");
	return data;
}

/** How many alerts of each of a project's tasks nobody marked seen, over all of them. */
export async function fetchLadoUnseenAlertCounts(projectId: string): Promise<LadoUnseenAlertCounts> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/alerts/unseen-counts", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not load the alerts");
	return data;
}

/** A project's alerts, newest first: one task's, or the ones nobody marked seen. */
export async function fetchLadoAlerts(projectId: string, filter: LadoAlertFilter = {}, limit?: number): Promise<LadoAlert[]> {
	const query: { taskId?: string; unseen?: boolean; limit?: number } = {};
	if (filter.taskId) query.taskId = filter.taskId;
	if (filter.unseen) query.unseen = true;
	if (limit !== undefined) query.limit = limit;
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/alerts", {
		params: { path: { projectId }, query },
	});
	if (error) throw ladoError(error, "Could not load the alerts");
	return data.alerts;
}

/** Marks an alert seen; an alert seen before keeps its first time. */
export async function markLadoAlertSeen(alertId: number): Promise<LadoAlert> {
	const { data, error } = await apiClient.POST("/api/v1/lado/alerts/{alertId}/seen", {
		params: { path: { alertId: String(alertId) } },
	});
	if (error) throw ladoError(error, "Could not mark the alert seen");
	return data;
}

/** The worker watch settings: the global row without a project, else the project's over it. */
export async function fetchLadoWatchSettings(projectId?: string): Promise<LadoWatchSettings> {
	const { data, error } = projectId
		? await apiClient.GET("/api/v1/lado/projects/{projectId}/watch-settings", { params: { path: { projectId } } })
		: await apiClient.GET("/api/v1/lado/watch-settings");
	if (error) throw ladoError(error, "Could not load the watch settings");
	return data;
}

/** Replaces the scope's row; a null value follows what it inherits. */
export async function updateLadoWatchSettings(projectId: string | undefined, values: LadoWatchSettingsValues): Promise<LadoWatchSettings> {
	const body = {
		compactWindow: values.compactWindow,
		taskCostCents: values.taskCostCents,
		sessionCostCents: values.sessionCostCents,
		sessionCompactions: values.sessionCompactions,
		stallMinutes: values.stallMinutes,
		wakeEnabled: values.wakeEnabled,
	};
	const { data, error } = projectId
		? await apiClient.PUT("/api/v1/lado/projects/{projectId}/watch-settings", { params: { path: { projectId } }, body })
		: await apiClient.PUT("/api/v1/lado/watch-settings", { body });
	if (error) throw ladoError(error, "Could not save the watch settings");
	return data;
}
