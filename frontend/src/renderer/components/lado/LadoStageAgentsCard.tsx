// LADO fork: the model and effort of each LADO stage's workers (issue #103),
// the global defaults in Settings → Runs and a project's overrides in
// Project settings → LADO. Two cards: the plan worker and its review run
// before an approval, so a change there touches no approval and applies from
// the next plan request; the execution, integration and resolve workers run
// what an approval fixed, so a change there sends approved, waiting tasks
// back for review (a global one in every project), while a run already
// started keeps its approved models. A stage without a value follows what it
// inherits, and the project card names what its workers run with now and
// where that comes from. A value is a model, with an effort only next to a
// model; an empty effort leaves the choice to AO. A global value names its
// harness; a project's runs the harness the project gives the stage. Each
// change saves at once, the whole scope together.
import { agentLabel } from "@aoagents/product-ui";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { type ReactNode, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { agentModelsQueryKey, agentModelsQueryOptions, refreshAgentModels } from "../../hooks/useAgentModelsQuery";
import { useLadoProjectStageAgents, useLadoStageAgents, useUpdateLadoStageAgents } from "../../hooks/useLado";
import { agentModelDisplayLabel, claudeChoiceModel } from "../../lib/agent-model-choices";
import { ladoErrorText } from "../../lib/lado-errors";
import {
	LADO_EXECUTION_STAGES,
	LADO_PLAN_STAGES,
	LADO_STAGE_HARNESSES,
	ladoProjectStageValues,
	ladoSameStageValues,
	ladoWithStageValue,
	type LadoProjectStageAgent,
	type LadoStage,
	type LadoStageAgent,
	type LadoStageAgentsByStage,
	type LadoStageHarness,
} from "../../lib/lado-stage-agents";
import { cn } from "../../lib/utils";
import { AgentModelCombobox } from "../settings/AgentModelCombobox";
import { formatEffortLabel } from "../settings/EffortPicker";
import { SettingsOptionMenu, type SettingsOption } from "../settings/SettingsOptionMenu";
import { SettingsRow } from "../settings/SettingsRow";
import { SettingsSection } from "../settings/SettingsSection";
import { MENU_TRIGGER_CHROME } from "../ui/option-menu";
import { Button } from "../ui/button";
import { ladoModelEffortText, ladoStageLabel, ladoStageSourceLabel } from "./lado-stage-agents";

/** The effort menu's value for an empty effort, which leaves the choice to AO. */
const AO_DEFAULT_EFFORT = "ao-default";

const inheritKeys = {
	plan: "lado.stageAgents.inherit.plan",
	critic: "lado.stageAgents.inherit.critic",
	execution: "lado.stageAgents.inherit.execution",
	integration: "lado.stageAgents.inherit.integration",
	resolve: "lado.stageAgents.inherit.resolve",
} as const satisfies Record<LadoStage, string>;

/** The two cards. Without a project they edit the global values. */
export function LadoStageAgentsCard({ projectId }: { projectId?: string }) {
	return projectId ? <ProjectStageAgents projectId={projectId} /> : <GlobalStageAgents />;
}

function GlobalStageAgents() {
	const settings = useLadoStageAgents();
	return <StageAgentsState pending={settings.isPending} error={settings.error} saved={settings.data?.stages} />;
}

function ProjectStageAgents({ projectId }: { projectId: string }) {
	const settings = useLadoProjectStageAgents(projectId);
	const view = settings.data;
	const rows = useMemo(() => new Map((view?.stages ?? []).map((row) => [row.stage, row])), [view]);
	return (
		<StageAgentsState
			projectId={projectId}
			pending={settings.isPending}
			error={settings.error}
			saved={view ? ladoProjectStageValues(view) : undefined}
			rows={rows}
		/>
	);
}

function StageAgentsState({
	projectId,
	pending,
	error,
	saved,
	rows,
}: {
	projectId?: string;
	pending: boolean;
	error: unknown;
	saved: LadoStageAgentsByStage | undefined;
	rows?: Map<LadoStage, LadoProjectStageAgent>;
}) {
	const { t } = useTranslation();
	if (pending || !saved) {
		return (
			<SettingsSection title={t("lado.stageAgents.planTitle")} grouped testId="lado-stage-agents-plan">
				{pending ? (
					<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
						{t("common.loading")}
					</p>
				) : (
					<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
						{ladoErrorText(t, error)}
					</p>
				)}
			</SettingsSection>
		);
	}
	return <StageAgentsCards projectId={projectId} saved={saved} rows={rows} />;
}

function StageAgentsCards({
	projectId,
	saved,
	rows,
}: {
	projectId?: string;
	saved: LadoStageAgentsByStage;
	rows?: Map<LadoStage, LadoProjectStageAgent>;
}) {
	const { t } = useTranslation();
	const update = useUpdateLadoStageAgents(projectId);
	const [changed, setChanged] = useState<LadoStage | null>(null);
	// A global value's harness picked before it has a model.
	const [picked, setPicked] = useState<Partial<Record<LadoStage, LadoStageHarness>>>({});
	// A change shows while it saves; a failed save falls back to the saved values.
	const shown = update.isPending && update.variables ? update.variables : saved;

	const save = (stage: LadoStage, value: LadoStageAgent | undefined) => {
		if (update.isPending) return;
		const next = ladoWithStageValue(saved, stage, value);
		if (ladoSameStageValues(next, saved)) return;
		setChanged(stage);
		update.mutate(next);
	};

	const row = (stage: LadoStage) => {
		const value = shown[stage];
		const projectRow = rows?.get(stage);
		const harness = projectId ? (projectRow?.harness ?? "") : (value?.harness ?? picked[stage] ?? "claude-code");
		return (
			<StageRow
				key={stage}
				stage={stage}
				value={value}
				harness={harness}
				catalogScope={projectId ?? ""}
				disabled={update.isPending}
				onSave={(next) => save(stage, next)}
				onHarnessChange={
					projectId
						? undefined
						: (next) => {
								setPicked((current) => ({ ...current, [stage]: next }));
								// A model belongs to its harness's list, so another harness starts over.
								if (value && value.harness !== next) save(stage, undefined);
							}
				}
				description={
					<>
						{projectRow ? <ProjectStageNotes row={projectRow} /> : !value ? <span className="block">{t(inheritKeys[stage])}</span> : null}
						{update.isError && changed === stage ? (
							<span role="alert" className="block text-error">
								{ladoErrorText(t, update.error)}
							</span>
						) : null}
					</>
				}
			/>
		);
	};

	return (
		<>
			<SettingsSection
				title={t("lado.stageAgents.planTitle")}
				grouped
				testId="lado-stage-agents-plan"
				toolbar={
					<p className="px-(--settings-row-inset) pb-(--size-settings-section-inner-gap) text-control leading-4 text-settings-muted" data-testid="lado-stage-agents-plan-note">
						{t("lado.stageAgents.planNote")}
					</p>
				}
			>
				{LADO_PLAN_STAGES.map(row)}
			</SettingsSection>
			<SettingsSection
				title={t("lado.stageAgents.executionTitle")}
				grouped
				testId="lado-stage-agents-execution"
				toolbar={
					<p className="px-(--settings-row-inset) pb-(--size-settings-section-inner-gap) text-control leading-4 text-settings-muted" data-testid="lado-stage-agents-execution-note">
						{projectId ? t("lado.stageAgents.executionNote") : t("lado.stageAgents.executionNoteGlobal")}
					</p>
				}
			>
				{LADO_EXECUTION_STAGES.map(row)}
			</SettingsSection>
		</>
	);
}

/**
 * What a project's stage runs with now and where that comes from, then why
 * its own or the global value does not apply, or may fail to start.
 */
function ProjectStageNotes({ row }: { row: LadoProjectStageAgent }) {
	const { t } = useTranslation();
	const effective = row.effective;
	return (
		<>
			{!row.harness ? <span className="block">{t("lado.stageAgents.noHarness")}</span> : null}
			{row.project && !row.projectApplies ? (
				<span className="block text-warning" data-testid="lado-stage-project-mismatch">
					{t("lado.stageAgents.projectMismatch", {
						harness: agentLabel(row.project.harness),
						stageHarness: row.harness ? agentLabel(row.harness) : t("lado.stageAgents.noAgent"),
					})}
				</span>
			) : null}
			{row.harness ? (
				<span className="block" data-testid="lado-stage-effective">
					{t("lado.stageAgents.effective", {
						value: ladoModelEffortText(t, effective.model, effective.effort),
						source: ladoStageSourceLabel(t, effective.source),
					})}
				</span>
			) : null}
			{row.global && row.globalWarning === "harness_mismatch" ? (
				<span className="block" data-testid="lado-stage-global-warning" data-warning={row.globalWarning}>
					{t("lado.stageAgents.globalMismatch", { harness: agentLabel(row.global.harness), model: row.global.model })}
				</span>
			) : null}
			{row.global && row.globalWarning === "model_unavailable" ? (
				<span className="block text-warning" data-testid="lado-stage-global-warning" data-warning={row.globalWarning}>
					{t("lado.stageAgents.globalUnavailable", { model: row.global.model })}
				</span>
			) : null}
		</>
	);
}

/** The efforts the catalog offers for a model; a Claude alias takes its family's newest model's. */
function catalogEfforts(models: { id: string; label: string; efforts?: string[] }[], model: string, harness: string): string[] {
	let selected = models.find((item) => item.id === model);
	if (!selected?.efforts?.length && harness === "claude-code") selected = claudeChoiceModel(models)?.(model) ?? selected;
	return (selected?.efforts ?? []).filter((effort) => effort && effort.toLowerCase() !== "default");
}

function StageRow({
	stage,
	value,
	harness,
	catalogScope,
	disabled,
	onSave,
	onHarnessChange,
	description,
}: {
	stage: LadoStage;
	value: LadoStageAgent | undefined;
	/** The harness whose model list the row offers; empty when the project names none. */
	harness: string;
	catalogScope: string;
	disabled: boolean;
	onSave: (value: LadoStageAgent | undefined) => void;
	/** A global row picks its harness. */
	onHarnessChange?: (harness: LadoStageHarness) => void;
	description: ReactNode;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const stageLabel = ladoStageLabel(t, stage);
	const query = useQuery(agentModelsQueryOptions(harness, catalogScope));
	const catalog = query.data;
	// Every model is an explicit pick here: none stands for "follow the default".
	const models = useMemo(
		() =>
			(catalog?.models ?? []).map((item) => ({
				...item,
				label: agentModelDisplayLabel(harness, item.label),
				isDefault: false,
				defaultEffort: undefined,
			})),
		[catalog?.models, harness],
	);
	const model = value?.model ?? "";
	const effort = value?.effort ?? "";
	const efforts = catalogEfforts(models, model, harness);
	// A saved effort the list no longer offers still shows as itself.
	const effortChoices = effort && !efforts.includes(effort) ? [...efforts, effort] : efforts;
	const effortOptions: SettingsOption<string>[] = [
		{ value: AO_DEFAULT_EFFORT, label: t("lado.stageAgents.aoDefaultEffort") },
		...effortChoices.map((choice) => ({ value: choice, label: formatEffortLabel(choice, t) })),
	];
	const valueHarness = (value?.harness ?? harness) as LadoStageHarness;
	// A project's kept value for another harness than the stage runs: the
	// stage skips it, the list is the stage harness's, and the daemon refuses
	// a changed effort on it. Picking a model or resetting replaces it.
	const otherHarness = !onHarnessChange && !!value && value.harness !== harness;
	const pickModel = (next: string) => {
		if (!next || !harness) return;
		// An effort the new model does not offer goes; AO picks one instead.
		const kept = effort && catalogEfforts(models, next, harness).includes(effort) ? effort : "";
		onSave({ harness: harness as LadoStageHarness, model: next, effort: kept });
	};
	const harnessOptions: SettingsOption<LadoStageHarness>[] = LADO_STAGE_HARNESSES.map((item) => ({ value: item, label: agentLabel(item) }));
	const refresh = async () => {
		const refreshed = await refreshAgentModels(harness, catalogScope);
		queryClient.setQueryData(agentModelsQueryKey(harness, catalogScope), refreshed);
	};

	return (
		<SettingsRow label={stageLabel} description={description} data-testid={`lado-stage-row-${stage}`}>
			<div className="flex max-w-[36rem] flex-wrap items-center justify-end gap-2">
				{onHarnessChange ? (
					<SettingsOptionMenu
						aria-label={t("lado.stageAgents.harnessLabel", { stage: stageLabel })}
						value={valueHarness}
						options={harnessOptions}
						disabled={disabled}
						onChange={onHarnessChange}
					/>
				) : null}
				<AgentModelCombobox
					aria-label={t("lado.stageAgents.modelLabel", { stage: stageLabel })}
					value={model}
					models={models}
					emptyLabel={t("lado.stageAgents.followDefault")}
					allowCustom={catalog?.allowCustom}
					customModelEntry={catalog?.customModelEntry ?? (catalog?.allowCustom ? "direct" : "none")}
					agentLabel={harness}
					agentId={harness}
					onRefresh={harness ? refresh : undefined}
					refreshing={catalog?.refreshState === "queued" || catalog?.refreshState === "refreshing"}
					refreshError={catalog?.refreshError}
					retryAt={catalog?.retryAt}
					disabled={disabled || !harness || (query.isFetching && !catalog)}
					onChange={pickModel}
					onCustom={pickModel}
					triggerClassName={cn(MENU_TRIGGER_CHROME, "w-fit")}
					compact
					menuAlign="end"
				/>
				{/* An effort goes with a model: without one AO decides, so there is nothing to pick. */}
				<SettingsOptionMenu
					aria-label={t("lado.stageAgents.effortLabel", { stage: stageLabel })}
					value={effort || AO_DEFAULT_EFFORT}
					options={effortOptions}
					disabled={disabled || !model || otherHarness}
					onChange={(next) => {
						if (!value) return;
						onSave({ harness: value.harness, model: value.model, effort: next === AO_DEFAULT_EFFORT ? "" : next });
					}}
				/>
				{value ? (
					<Button
						type="button"
						variant="ghost"
						size="sm"
						aria-label={t("lado.stageAgents.resetLabel", { stage: stageLabel })}
						className="text-settings-muted hover:text-settings-label"
						disabled={disabled}
						onClick={() => onSave(undefined)}
					>
						<span className="text-sm">{t("lado.stageAgents.reset")}</span>
					</Button>
				) : null}
			</div>
		</SettingsRow>
	);
}
