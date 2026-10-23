// LADO fork: Settings → Runs (docs/LJY-PRD/TDD.md §5.3). How many approved
// tasks of LADO projects run at once, how long one may run, and whether queued
// ones wait while memory is tight. A change saves at once, all three values
// together; each project also has its own cap (Project settings → LADO).
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useLadoDispatchSettings, useUpdateLadoDispatchSettings } from "../../hooks/useLado";
import type { LadoDispatchSettingsInput, LadoRunMinutes } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { Switch } from "../ui/switch";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

/** The caps on concurrent runs the daemon accepts, overall and per project: 1 to 8. */
export const ladoConcurrentRunOptions: SettingsOption<string>[] = Array.from({ length: 8 }, (_, index) => {
	const value = String(index + 1);
	return { value, label: value };
});

/** The maximum run times on offer, in minutes; 0 means no limit. */
const runMinuteChoices: readonly LadoRunMinutes[] = [0, 60, 120, 240, 480, 720, 1440];

type RunsSetting = keyof LadoDispatchSettingsInput;

export function RunsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const settings = useLadoDispatchSettings();
	const update = useUpdateLadoDispatchSettings();
	// The row whose change failed shows the daemon's answer under it.
	const [changed, setChanged] = useState<RunsSetting | null>(null);
	const saved = settings.data;
	// A change shows while it saves; a failed save falls back to the saved values.
	const shown = update.isPending && update.variables ? update.variables : saved;

	const save = <K extends RunsSetting>(setting: K, value: LadoDispatchSettingsInput[K]) => {
		if (!saved || update.isPending || saved[setting] === value) return;
		const next: LadoDispatchSettingsInput = {
			maxConcurrent: saved.maxConcurrent,
			maxRunMinutes: saved.maxRunMinutes,
			holdOnMemoryTight: saved.holdOnMemoryTight,
		};
		next[setting] = value;
		setChanged(setting);
		update.mutate(next);
	};

	const failure = (setting: RunsSetting) =>
		update.isError && changed === setting ? (
			<span role="alert" className="block text-error">
				{ladoErrorText(t, update.error)}
			</span>
		) : undefined;

	const runTimeOptions: SettingsOption<string>[] = runMinuteChoices.map((minutes) => ({
		value: String(minutes),
		label: minutes === 0 ? t("lado.runs.maxRunTimeOff") : t("lado.runs.hours", { count: minutes / 60 }),
	}));

	return (
		<SettingsSection
			title={t("lado.runs.title")}
			sectionId="runs"
			titleHidden={titleHidden}
			grouped={Boolean(shown)}
			toolbar={
				<p className="px-(--settings-row-inset) pb-(--size-settings-section-inner-gap) text-control leading-4 text-settings-muted">
					{t("lado.runs.note")}
				</p>
			}
		>
			{settings.isPending ? (
				<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
					{t("common.loading")}
				</p>
			) : !shown ? (
				<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
					{ladoErrorText(t, settings.error)}
				</p>
			) : (
				<>
					<SettingsRow label={t("lado.runs.maxConcurrent")} description={failure("maxConcurrent")}>
						<SettingsOptionMenu
							aria-label={t("lado.runs.maxConcurrent")}
							value={String(shown.maxConcurrent)}
							options={ladoConcurrentRunOptions}
							disabled={update.isPending}
							onChange={(value) => save("maxConcurrent", Number(value))}
						/>
					</SettingsRow>
					<SettingsRow label={t("lado.runs.maxRunTime")} description={failure("maxRunMinutes")}>
						<SettingsOptionMenu
							aria-label={t("lado.runs.maxRunTime")}
							value={String(shown.maxRunMinutes)}
							options={runTimeOptions}
							disabled={update.isPending}
							onChange={(value) => {
								const minutes = runMinuteChoices.find((choice) => String(choice) === value);
								if (minutes !== undefined) save("maxRunMinutes", minutes);
							}}
						/>
					</SettingsRow>
					<SettingsRow
						label={t("lado.runs.holdOnMemoryTight")}
						description={
							<>
								{t("lado.runs.holdOnMemoryTightHint")}
								{failure("holdOnMemoryTight")}
							</>
						}
					>
						<Switch
							aria-label={t("lado.runs.holdOnMemoryTight")}
							checked={shown.holdOnMemoryTight}
							disabled={update.isPending}
							onCheckedChange={(on) => save("holdOnMemoryTight", on)}
						/>
					</SettingsRow>
				</>
			)}
		</SettingsSection>
	);
}
