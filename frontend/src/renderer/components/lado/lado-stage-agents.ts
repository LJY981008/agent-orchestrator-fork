// LADO fork: how the app words a stage's model and effort (issue #103), in
// the settings card, the run's inspector, the task's Usage and Rules tabs and
// the board card's LADO line. An empty model is the agent's own default and
// an empty effort the one AO picks for the model when the worker starts.
import type { TFunction } from "i18next";
import type { MessageKey } from "../../i18n/messages";
import { type LadoStage, type LadoStageSource, ladoStageLabelKeys } from "../../lib/lado-stage-agents";
import { formatEffortLabel } from "../settings/EffortPicker";

const sourceKeys = {
	project: "lado.stageAgents.source.project",
	global: "lado.stageAgents.source.global",
	plan_stage: "lado.stageAgents.source.planStage",
	execution_stage: "lado.stageAgents.source.executionStage",
	task: "lado.stageAgents.source.task",
	orchestrator: "lado.stageAgents.source.orchestrator",
	worker: "lado.stageAgents.source.worker",
} as const satisfies Record<LadoStageSource, MessageKey>;

export function ladoStageLabel(t: TFunction, stage: LadoStage): string {
	return t(ladoStageLabelKeys[stage]);
}

/** Where a stage's value comes from; a source from a newer daemon shows as sent, an empty one as nothing. */
export function ladoStageSourceLabel(t: TFunction, source: string): string {
	return Object.hasOwn(sourceKeys, source) ? t(sourceKeys[source as LadoStageSource]) : source;
}

/** The effort in words: a known level translated, the AO default when empty. */
export function ladoEffortText(t: TFunction, effort: string | undefined): string {
	return effort ? formatEffortLabel(effort, t) : t("lado.stageAgents.aoDefaultEffort");
}

/** "model · effort", with the agent's and AO's defaults named; neither set reads as the agent's default alone. */
export function ladoModelEffortText(t: TFunction, model: string | undefined, effort: string | undefined): string {
	if (!model && !effort) return t("lado.stageAgents.agentDefaultModel");
	return `${model || t("lado.stageAgents.agentDefaultModel")} · ${ladoEffortText(t, effort)}`;
}
