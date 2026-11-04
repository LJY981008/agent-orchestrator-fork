// LADO fork: the worker watch settings (issue #42), the global defaults and a
// project's overrides in one card. The compact window LADO gives the Claude
// Code sessions of its projects, the cost and compaction thresholds past
// which it alerts, how long a worker may rest before it counts as stalled,
// and whether LADO sends its two wake messages. A project's value can follow
// the global one, and the global one the built-in default. The values reach
// sessions as they start, so a change applies from the next run.
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useLadoWatchSettings, useUpdateLadoWatchSettings } from "../../hooks/useLado";
import type { LadoWatchSettingsResolved, LadoWatchSettingsValues } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { ladoCentsText, ladoTokensText } from "../../lib/lado-usage";
import { SettingsOptionMenu, type SettingsOption } from "../settings/SettingsOptionMenu";
import { SettingsRow } from "../settings/SettingsRow";
import { SettingsSection } from "../settings/SettingsSection";

type WatchSetting = keyof LadoWatchSettingsValues;
type NumberSetting = Exclude<WatchSetting, "wakeEnabled">;

/** The choices each value offers; a value saved outside them still shows as itself. */
const choices: Record<NumberSetting, readonly number[]> = {
	compactWindow: [100_000, 150_000, 200_000, 300_000, 400_000, 500_000, 750_000, 1_000_000],
	taskCostCents: [10_00, 25_00, 50_00, 100_00, 200_00, 500_00],
	sessionCostCents: [5_00, 10_00, 20_00, 30_00, 50_00, 100_00],
	sessionCompactions: [1, 2, 3, 4, 5, 8, 10],
	stallMinutes: [30, 60, 120, 180, 240, 480],
};

/** The menu value of a null setting, which follows what the scope inherits. */
const INHERIT = "inherit";

const labelKeys = {
	compactWindow: "lado.watch.compactWindow",
	taskCostCents: "lado.watch.taskCost",
	sessionCostCents: "lado.watch.sessionCost",
	sessionCompactions: "lado.watch.sessionCompactions",
	stallMinutes: "lado.watch.stall",
	wakeEnabled: "lado.watch.wake",
} as const satisfies Record<WatchSetting, string>;

const hintKeys = {
	compactWindow: "lado.watch.compactWindowHint",
	taskCostCents: "lado.watch.taskCostHint",
	sessionCostCents: "lado.watch.sessionCostHint",
	sessionCompactions: "lado.watch.sessionCompactionsHint",
	stallMinutes: "lado.watch.stallHint",
	wakeEnabled: "lado.watch.wakeHint",
} as const satisfies Record<WatchSetting, string>;

const settingOrder: readonly WatchSetting[] = [
	"compactWindow",
	"taskCostCents",
	"sessionCostCents",
	"sessionCompactions",
	"stallMinutes",
	"wakeEnabled",
];

/**
 * The watch settings card. Without a project it edits the global row; with
 * one, its first row picks the project's overrides or the global defaults.
 */
export function LadoWatchSettingsCard({ projectId }: { projectId?: string }) {
	const { t } = useTranslation();
	const [scope, setScope] = useState<"project" | "global">(projectId ? "project" : "global");
	const scopeProjectId = scope === "project" ? projectId : undefined;
	const scopeOptions: SettingsOption<"project" | "global">[] = [
		{ value: "project", label: t("lado.watch.scopeProject") },
		{ value: "global", label: t("lado.watch.scopeGlobal") },
	];
	return (
		<SettingsSection
			title={t("lado.watch.title")}
			grouped
			testId="lado-watch-settings"
			toolbar={
				<p className="px-(--settings-row-inset) pb-(--size-settings-section-inner-gap) text-control leading-4 text-settings-muted" data-testid="lado-watch-note">
					{t("lado.watch.note")}
				</p>
			}
		>
			{projectId ? (
				<SettingsRow label={t("lado.watch.scope")} description={t("lado.watch.scopeHint")}>
					<SettingsOptionMenu aria-label={t("lado.watch.scope")} value={scope} options={scopeOptions} onChange={setScope} />
				</SettingsRow>
			) : null}
			<WatchRows key={scope} projectId={scopeProjectId} />
		</SettingsSection>
	);
}

function WatchRows({ projectId }: { projectId?: string }) {
	const { t, i18n } = useTranslation();
	const settings = useLadoWatchSettings(projectId);
	const update = useUpdateLadoWatchSettings(projectId);
	const [changed, setChanged] = useState<WatchSetting | null>(null);
	const saved = settings.data;
	// A change shows while it saves; a failed save falls back to the saved values.
	const shown: LadoWatchSettingsValues | undefined = update.isPending && update.variables ? update.variables : saved?.values;

	if (settings.isPending) {
		return (
			<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
				{t("common.loading")}
			</p>
		);
	}
	if (!saved || !shown) {
		return (
			<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
				{ladoErrorText(t, settings.error)}
			</p>
		);
	}

	const save = (setting: WatchSetting, value: number | boolean | null) => {
		if (update.isPending || shown[setting] === value) return;
		setChanged(setting);
		update.mutate({ ...saved.values, [setting]: value });
	};

	const valueText = (setting: WatchSetting, value: number | boolean): string => {
		const locale = i18n.language;
		switch (setting) {
			case "compactWindow":
				return t("lado.watch.tokens", { tokens: ladoTokensText(Number(value), locale) });
			case "taskCostCents":
			case "sessionCostCents":
				return ladoCentsText(Number(value), locale);
			case "sessionCompactions":
				return t("lado.watch.times", { times: Number(value) });
			case "stallMinutes": {
				const minutes = Number(value);
				return minutes % 60 === 0 ? t("lado.watch.hours", { hours: minutes / 60 }) : t("lado.watch.minutes", { minutes });
			}
			case "wakeEnabled":
				return value ? t("lado.watch.on") : t("lado.watch.off");
		}
	};

	const inherited = (setting: WatchSetting, from: LadoWatchSettingsResolved) =>
		projectId
			? t("lado.watch.inheritGlobal", { value: valueText(setting, from[setting]) })
			: t("lado.watch.inheritDefault", { value: valueText(setting, from[setting]) });

	const options = (setting: WatchSetting): SettingsOption<string>[] => {
		const values: readonly (number | boolean)[] =
			setting === "wakeEnabled"
				? [true, false]
				: [...new Set([...choices[setting], ...(typeof shown[setting] === "number" ? [shown[setting] as number] : [])])].sort((a, b) => a - b);
		return [
			{ value: INHERIT, label: inherited(setting, saved.inherited) },
			...values.map((value) => ({ value: String(value), label: valueText(setting, value) })),
		];
	};

	const choose = (setting: WatchSetting, value: string) => {
		if (value === INHERIT) save(setting, null);
		else if (setting === "wakeEnabled") save(setting, value === "true");
		else save(setting, Number(value));
	};

	return (
		<>
			{settingOrder.map((setting) => (
				<SettingsRow
					key={setting}
					label={t(labelKeys[setting])}
					description={
						<>
							{t(hintKeys[setting])}
							{update.isError && changed === setting ? (
								<span role="alert" className="block text-error">
									{ladoErrorText(t, update.error)}
								</span>
							) : null}
						</>
					}
				>
					<SettingsOptionMenu
						aria-label={t(labelKeys[setting])}
						value={shown[setting] === null ? INHERIT : String(shown[setting])}
						options={options(setting)}
						disabled={update.isPending}
						onChange={(value) => choose(setting, value)}
					/>
				</SettingsRow>
			))}
		</>
	);
}
