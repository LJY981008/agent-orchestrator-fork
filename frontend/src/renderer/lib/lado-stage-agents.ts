// LADO fork: the model and effort of each LADO stage's workers (issue #103).
// Five stages: the plan worker and its review run before an approval, the
// execution, integration and resolve workers run what the approval fixed.
// A global value per stage is the default, a project's value overrides it;
// a stage with neither follows what it inherits (the plan stage's or the
// execution stage's final value, else the project orchestrator's or worker's
// model). A value names its harness; a stage that runs another skips it. A
// PUT replaces the whole scope: a stage left out follows the default.
import type { components } from "../../api/schema";
import type { MessageKey } from "../i18n/messages";
import { apiClient } from "./api-client";
import { ladoError } from "./lado";

export type LadoStageAgent = components["schemas"]["LadoStageAgentResponse"];
export type LadoStageAgentsByStage = components["schemas"]["LadoStageAgentsByStage"];
export type LadoStageAgents = components["schemas"]["LadoStageAgentsResponse"];
export type LadoProjectStageAgents = components["schemas"]["LadoProjectStageAgentsResponse"];
export type LadoProjectStageAgent = components["schemas"]["LadoProjectStageAgentResponse"];
export type LadoStageResolved = components["schemas"]["LadoStageResolvedResponse"];
export type LadoStage = LadoProjectStageAgent["stage"];
export type LadoStageSource = LadoStageResolved["source"];
export type LadoStageHarness = LadoStageAgent["harness"];

/** The stages in the order the daemon lists them. */
export const LADO_STAGES: readonly LadoStage[] = ["plan", "critic", "execution", "integration", "resolve"];
/** The stages that run before an approval; changing them never touches one. */
export const LADO_PLAN_STAGES: readonly LadoStage[] = ["plan", "critic"];
/** The stages an approval fixes; changing them sends approved, waiting tasks back for review. */
export const LADO_EXECUTION_STAGES: readonly LadoStage[] = ["execution", "integration", "resolve"];
/** The harnesses a stage value may name. */
export const LADO_STAGE_HARNESSES: readonly LadoStageHarness[] = ["claude-code", "codex"];
/** The message key of each stage's name, for the settings card and a refused value's error. */
export const ladoStageLabelKeys = {
	plan: "lado.stageAgents.stage.plan",
	critic: "lado.stageAgents.stage.critic",
	execution: "lado.stageAgents.stage.execution",
	integration: "lado.stageAgents.stage.integration",
	resolve: "lado.stageAgents.stage.resolve",
} as const satisfies Record<LadoStage, MessageKey>;

/** Every stage-agent view, the global one and each project's, under one root. */
export const ladoStageAgentsQueryRoot = ["lado", "stage-agents"] as const;
/** The global values without a project. */
export const ladoStageAgentsQueryKey = (projectId?: string) => ["lado", "stage-agents", projectId ?? ""] as const;

/** The global value of each stage; a missing stage follows the default. */
export async function fetchLadoStageAgents(): Promise<LadoStageAgents> {
	const { data, error } = await apiClient.GET("/api/v1/lado/stage-agents");
	if (error) throw ladoError(error, "Could not load the stage models");
	return data;
}

/** Each stage of a project: its value, the global one, which applies and what runs. */
export async function fetchLadoProjectStageAgents(projectId: string): Promise<LadoProjectStageAgents> {
	const { data, error } = await apiClient.GET("/api/v1/lado/projects/{projectId}/stage-agents", {
		params: { path: { projectId } },
	});
	if (error) throw ladoError(error, "Could not load the project's stage models");
	return data;
}

/** Replaces the global values. */
export async function updateLadoStageAgents(stages: LadoStageAgentsByStage): Promise<LadoStageAgents> {
	const { data, error } = await apiClient.PUT("/api/v1/lado/stage-agents", { body: { stages } });
	if (error) throw ladoError(error, "Could not save the stage models");
	return data;
}

/** Replaces the project's values. */
export async function updateLadoProjectStageAgents(projectId: string, stages: LadoStageAgentsByStage): Promise<LadoProjectStageAgents> {
	const { data, error } = await apiClient.PUT("/api/v1/lado/projects/{projectId}/stage-agents", {
		params: { path: { projectId } },
		body: { stages },
	});
	if (error) throw ladoError(error, "Could not save the project's stage models");
	return data;
}

/** The project's own values of a view, as a PUT sends them back. */
export function ladoProjectStageValues(view: LadoProjectStageAgents): LadoStageAgentsByStage {
	const stages: LadoStageAgentsByStage = {};
	for (const row of view.stages) {
		if (row.project) stages[row.stage] = row.project;
	}
	return stages;
}

/** The values with one stage set, or cleared to follow the default. */
export function ladoWithStageValue(
	stages: LadoStageAgentsByStage,
	stage: LadoStage,
	value: LadoStageAgent | undefined,
): LadoStageAgentsByStage {
	const next: LadoStageAgentsByStage = {};
	for (const name of LADO_STAGES) {
		const current = name === stage ? value : stages[name];
		if (current) next[name] = { harness: current.harness, model: current.model, effort: current.effort };
	}
	return next;
}

/** Whether two scopes hold the same values. */
export function ladoSameStageValues(a: LadoStageAgentsByStage, b: LadoStageAgentsByStage): boolean {
	return LADO_STAGES.every((stage) => {
		const left = a[stage];
		const right = b[stage];
		if (!left || !right) return !left && !right;
		return left.harness === right.harness && left.model === right.model && left.effort === right.effort;
	});
}
