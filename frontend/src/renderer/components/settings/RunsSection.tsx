// LADO fork: Settings → Runs (docs/LJY-PRD/TDD.md §5.3). How many approved
// tasks of LADO projects run at once, how long one may run, and whether queued
// ones wait while memory is tight; then, in their own card, how many plan
// requests run their plan workers at once and when a small plan skips its
// review (§5.1). A change saves at once, all values together; each project
// also has its own cap (Project settings → LADO).
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useLadoDispatchSettings, useUpdateLadoDispatchSettings } from "../../hooks/useLado";
import type { LadoDispatchSettings, LadoRunMinutes } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { LadoPlanReviewPatternsDialog } from "../lado/LadoPlanReviewPatternsDialog";
import { Button } from "../ui/button";
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

/** The affected paths a plan may name and skip its review: 1 to 20. */
const reviewMaxPathChoices = Array.from({ length: 20 }, (_, index) => index + 1);

/** The values a change sends, all of them every time. */
type RunsValues = Pick<LadoDispatchSettings, "maxConcurrent" | "maxRunMinutes" | "holdOnMemoryTight" | "maxPlanWorkers" | "planReview">;

/** The row a change comes from, to put a refusal under it. */
type RunsSetting = "maxConcurrent" | "maxRunMinutes" | "holdOnMemoryTight" | "maxPlanWorkers" | "reviewSkip" | "reviewMaxPaths";

export function RunsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const settings = useLadoDispatchSettings();
	const update = useUpdateLadoDispatchSettings();
	// The row whose change failed shows the daemon's answer under it.
	const [changed, setChanged] = useState<RunsSetting | null>(null);
	// The values a change sends, shown while it saves.
	const [sending, setSending] = useState<RunsValues | null>(null);
	// Each opening mounts the patterns dialog again, from the saved patterns.
	const [patterns, setPatterns] = useState({ key: 0, open: false });
	const saved = settings.data;
	// A change shows while it saves; a failed save falls back to the saved values.
	const shown: RunsValues | undefined = update.isPending && sending ? sending : saved;

	const save = (setting: RunsSetting, change: Partial<RunsValues>) => {
		if (!saved || update.isPending) return;
		const current: RunsValues = {
			maxConcurrent: saved.maxConcurrent,
			maxRunMinutes: saved.maxRunMinutes,
			holdOnMemoryTight: saved.holdOnMemoryTight,
			maxPlanWorkers: saved.maxPlanWorkers,
			planReview: saved.planReview,
		};
		const next = { ...current, ...change };
		if (JSON.stringify(next) === JSON.stringify(current)) return;
		setChanged(setting);
		setSending(next);
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

	const reviewMaxPathOptions: SettingsOption<string>[] = reviewMaxPathChoices.map((limit) => ({
		value: String(limit),
		label: t("lado.runs.reviewSkipMaxPathsOption", { limit }),
	}));

	return (
		<>
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
								onChange={(value) => save("maxConcurrent", { maxConcurrent: Number(value) })}
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
									if (minutes !== undefined) save("maxRunMinutes", { maxRunMinutes: minutes });
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
								onCheckedChange={(on) => save("holdOnMemoryTight", { holdOnMemoryTight: on })}
							/>
						</SettingsRow>
					</>
				)}
			</SettingsSection>
			{shown && saved ? (
				<SettingsSection title={t("lado.runs.planWorkers")} grouped testId="lado-plan-worker-settings">
					<SettingsRow
						label={t("lado.runs.maxPlanWorkers")}
						description={
							<>
								{t("lado.runs.maxPlanWorkersHint")}
								{failure("maxPlanWorkers")}
							</>
						}
					>
						<SettingsOptionMenu
							aria-label={t("lado.runs.maxPlanWorkers")}
							value={String(shown.maxPlanWorkers)}
							options={ladoConcurrentRunOptions}
							disabled={update.isPending}
							onChange={(value) => save("maxPlanWorkers", { maxPlanWorkers: Number(value) })}
						/>
					</SettingsRow>
					<SettingsRow
						label={t("lado.runs.reviewSkip")}
						description={
							<>
								{t("lado.runs.reviewSkipHint")}
								{failure("reviewSkip")}
							</>
						}
					>
						<Switch
							aria-label={t("lado.runs.reviewSkip")}
							checked={shown.planReview.skip}
							disabled={update.isPending}
							onCheckedChange={(skip) => save("reviewSkip", { planReview: { ...saved.planReview, skip } })}
						/>
					</SettingsRow>
					<SettingsRow label={t("lado.runs.reviewSkipMaxPaths")} description={failure("reviewMaxPaths")}>
						{/* Without the skip every draft is reviewed, so the size does not count. */}
						<SettingsOptionMenu
							aria-label={t("lado.runs.reviewSkipMaxPaths")}
							value={String(shown.planReview.maxPaths)}
							options={reviewMaxPathOptions}
							disabled={update.isPending || !shown.planReview.skip}
							onChange={(value) => save("reviewMaxPaths", { planReview: { ...saved.planReview, maxPaths: Number(value) } })}
						/>
					</SettingsRow>
					<SettingsRow label={t("lado.runs.reviewPatterns")} description={t("lado.runs.reviewPatternsHint")}>
						<Button
							type="button"
							variant="ghost"
							size="sm"
							aria-label={t("lado.runs.reviewPatterns")}
							className="text-settings-muted hover:text-settings-label"
							disabled={update.isPending || !shown.planReview.skip}
							onClick={() => setPatterns((current) => ({ key: current.key + 1, open: true }))}
						>
							<span className="text-sm">{t("lado.runs.reviewPatternsEdit")}</span>
						</Button>
					</SettingsRow>
				</SettingsSection>
			) : null}
			{saved ? (
				<LadoPlanReviewPatternsDialog
					key={`patterns-${patterns.key}`}
					open={patterns.open}
					settings={saved}
					onOpenChange={(open) => setPatterns((current) => ({ ...current, open }))}
				/>
			) : null}
		</>
	);
}
