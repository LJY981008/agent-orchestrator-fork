// LADO fork: Project settings → Rules (docs/LJY-PRD/HARNESS.md §3 and §12,
// PRD HAR-003). Each rule follows Global unless the project forces it on or
// off; protected rules are always on. The project's AO instructions are
// listed as they are: LADO includes them and cannot turn them off.
import type { TFunction } from "i18next";
import { Plus } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useLadoEffective, useLadoRules } from "../../hooks/useLado";
import type { MessageKey } from "../../i18n/messages";
import type { LadoCeilingMode, LadoEffective, LadoEffectiveRule } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { LADO_RULE_PERMISSION_CEILING, ladoRuleName, useLadoRuleChange } from "../../lib/lado-rules";
import { SettingsOptionMenu } from "../settings/SettingsOptionMenu";
import { SettingsRow } from "../settings/SettingsRow";
import { SettingsSection } from "../settings/SettingsSection";
import { Button } from "../ui/button";
import { LadoRuleEditorDialog } from "./LadoRuleEditorDialog";
import { LadoRuleImpactDialog } from "./LadoRuleImpactDialog";
import { ladoCeilingModeLabel } from "./LadoRuleRows";

/** Where a row overrides rules: a project's settings, or one task. */
export type LadoOverrideScope = "project" | "task";
export type LadoOverrideState = LadoEffectiveRule["project"];
type LadoAORule = LadoEffective["aoRules"][number];

const overrideStates: readonly LadoOverrideState[] = ["inherit", "force_on", "force_off"];

const overrideKeys: Record<LadoOverrideState, MessageKey> = {
	inherit: "lado.rules.override.inherit",
	force_on: "lado.rules.override.on",
	force_off: "lado.rules.override.off",
};

const aoKindKeys: Record<LadoAORule["kind"], MessageKey> = {
	worker_instructions: "lado.rules.ao.workerInstructions",
	worker_instructions_file: "lado.rules.ao.workerInstructionsFile",
	orchestrator_instructions: "lado.rules.ao.orchestratorInstructions",
};

/**
 * What applies and which scope decided it: "On · from Global", "Off · set
 * here". The permission ceiling's state is its mode.
 */
export function ladoEffectiveStateLabel(
	t: TFunction,
	rule: LadoEffectiveRule,
	scope: LadoOverrideScope,
	ceiling: LadoCeilingMode,
): string {
	const state =
		rule.ruleId === LADO_RULE_PERMISSION_CEILING
			? ladoCeilingModeLabel(t, ceiling)
			: rule.on
				? t("lado.rules.state.on")
				: t("lado.rules.state.off");
	const source =
		rule.source.scope === scope
			? t("lado.rules.source.here")
			: rule.source.scope === "project"
				? t("lado.rules.source.project")
				: t("lado.rules.source.global");
	return `${state} · ${source}`;
}

/**
 * One rule as it applies to a project or a task, with the select that
 * inherits it or forces it on or off there. A protected rule says it is
 * always on instead.
 */
export function LadoEffectiveRuleRow({
	rule,
	scope,
	ceiling,
	busy,
	readOnly = false,
	error,
	onChange,
}: {
	rule: LadoEffectiveRule;
	scope: LadoOverrideScope;
	ceiling: LadoCeilingMode;
	busy: boolean;
	readOnly?: boolean;
	error?: unknown;
	onChange: (state: LadoOverrideState) => void;
}) {
	const { t } = useTranslation();
	const name = ladoRuleName(t, rule.ruleId, rule.name);
	const detail = ladoEffectiveStateLabel(t, rule, scope, ceiling);
	return (
		<SettingsRow
			data-testid="lado-effective-rule"
			data-rule-id={rule.ruleId}
			label={name}
			description={
				error ? (
					<>
						{detail}
						<span role="alert" className="block text-error">
							{ladoErrorText(t, error)}
						</span>
					</>
				) : (
					detail
				)
			}
		>
			{rule.protected ? (
				<span className="text-sm text-settings-muted">{t("lado.rules.alwaysOn")}</span>
			) : (
				<SettingsOptionMenu<LadoOverrideState>
					aria-label={scope === "project" ? t("lado.rules.project.override", { name }) : t("lado.rules.task.override", { name })}
					value={rule[scope]}
					options={overrideStates.map((state) => ({ value: state, label: t(overrideKeys[state]) }))}
					disabled={busy || readOnly}
					triggerClassName="w-fit"
					onChange={(state) => {
						if (state !== rule[scope]) onChange(state);
					}}
				/>
			)}
		</SettingsRow>
	);
}

/** The project's AO instructions, read only: their size, or why a file could not be read. */
export function LadoAORuleRows({ rules }: { rules: readonly LadoAORule[] }) {
	const { t, i18n } = useTranslation();
	const chars = (count: number) => t("lado.rules.ao.chars", { count, chars: new Intl.NumberFormat(i18n.language).format(count) });
	const rows = rules.map((rule) => {
		let detail: ReactNode = chars(rule.chars);
		if (rule.kind === "worker_instructions_file") {
			detail = (
				<>
					<span className="break-all font-mono">{rule.path}</span>
					{" · "}
					{rule.readError ? (
						<span className="text-error">{t("lado.rules.ao.readError", { reason: rule.readError })}</span>
					) : (
						chars(rule.chars)
					)}
				</>
			);
		}
		return <SettingsRow key={rule.kind} data-testid="lado-ao-rule" data-kind={rule.kind} label={t(aoKindKeys[rule.kind])} description={detail} />;
	});
	return <>{rows}</>;
}

/** The AO instructions card, with the one fact that matters: change them in AO, not here. */
export function LadoAORulesSection({ rules }: { rules: readonly LadoAORule[] }) {
	const { t } = useTranslation();
	return (
		<SettingsSection
			title={t("lado.rules.ao.title")}
			grouped={rules.length > 0}
			toolbar={<p className="px-(--settings-row-inset) text-control leading-4 text-settings-muted">{t("lado.rules.ao.note")}</p>}
		>
			{rules.length > 0 ? (
				<LadoAORuleRows rules={rules} />
			) : (
				<p className="px-(--settings-row-inset) text-sm text-settings-muted">{t("lado.rules.ao.none")}</p>
			)}
		</SettingsSection>
	);
}

type Opening = { key: number; open: boolean };

export function LadoProjectRulesSection({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const effective = useLadoEffective(projectId);
	const change = useLadoRuleChange();
	const [changing, setChanging] = useState<string | null>(null);
	const [failure, setFailure] = useState<{ ruleId: string; error: unknown } | null>(null);
	// Each opening mounts the editor again, so it starts empty.
	const [editor, setEditor] = useState<Opening>({ key: 0, open: false });
	// The editor offers every rule as a conflict, so it reads them only while open.
	const rules = useLadoRules(true, editor.open);
	const data = effective.data;

	// Every change runs as a dry run first (useLadoRuleChange); one at a time.
	const setOverride = (rule: LadoEffectiveRule, state: LadoOverrideState) => {
		if (change.isPending) return;
		setChanging(rule.ruleId);
		setFailure(null);
		change
			.change({ kind: "binding", input: { ruleId: rule.ruleId, scope: "project", scopeId: projectId, state } })
			.catch((error: unknown) => setFailure({ ruleId: rule.ruleId, error }))
			.finally(() => setChanging(null));
	};

	return (
		<>
			<div className="flex w-full flex-col gap-(--size-settings-section-gap)">
				<SettingsSection
					title={t("lado.rules.project.title")}
					grouped={Boolean(data)}
					action={
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="text-settings-muted hover:text-settings-label"
							onClick={() => setEditor((current) => ({ key: current.key + 1, open: true }))}
						>
							<Plus aria-hidden="true" />
							{t("lado.rules.project.newRule")}
						</Button>
					}
				>
					{effective.isPending ? (
						<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
							{t("common.loading")}
						</p>
					) : !data ? (
						<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
							{ladoErrorText(t, effective.error)}
						</p>
					) : (
						data.rules.map((rule) => (
							<LadoEffectiveRuleRow
								key={rule.ruleId}
								rule={rule}
								scope="project"
								ceiling={data.permissionCeiling}
								busy={changing === rule.ruleId}
								error={failure?.ruleId === rule.ruleId ? failure.error : undefined}
								onChange={(state) => setOverride(rule, state)}
							/>
						))
					)}
				</SettingsSection>
				{data ? <LadoAORulesSection rules={data.aoRules} /> : null}
			</div>
			<LadoRuleImpactDialog
				impact={change.pending?.impact ?? null}
				busy={change.isPending}
				error={change.pending ? change.error : null}
				onConfirm={() => void change.confirm().catch(() => undefined)}
				onCancel={change.cancel}
			/>
			<LadoRuleEditorDialog
				key={`editor-${editor.key}`}
				open={editor.open}
				rules={rules.data ?? []}
				projectId={projectId}
				onOpenChange={(open) => setEditor((current) => ({ ...current, open }))}
			/>
		</>
	);
}
