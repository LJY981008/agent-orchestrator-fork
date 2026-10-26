// LADO fork: Project settings → LADO (docs/LJY-PRD/TDD.md §5.3 and §9). The
// project's LADO mode and how many of its approved tasks run at once. The
// daemon refuses turning the mode on while tracker intake is on; turning it
// off asks the desktop app to confirm, and a cancelled confirmation leaves it
// on. The cap saves at once, also while the mode is off.
import { useTranslation } from "react-i18next";
import { useLadoProjectSettings, useSetLadoMode, useUpdateLadoProjectSettings } from "../../hooks/useLado";
import { LadoApiError } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { ladoConcurrentRunOptions } from "../settings/RunsSection";
import { SettingsOptionMenu } from "../settings/SettingsOptionMenu";
import { SettingsRow } from "../settings/SettingsRow";
import { SettingsSection } from "../settings/SettingsSection";
import { Switch } from "../ui/switch";

export function LadoProjectSection({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const settings = useLadoProjectSettings(projectId);
	const setMode = useSetLadoMode(projectId);
	const setCap = useUpdateLadoProjectSettings(projectId);
	const data = settings.data;
	const busy = setMode.isPending || setCap.isPending;
	// The switch shows only the mode the daemon holds, so it stays on while the
	// desktop app asks and after a cancel. The cap shows a change while it saves.
	const cap = setCap.isPending && setCap.variables ? setCap.variables.maxConcurrent : data?.maxConcurrent;

	const changeMode = (on: boolean) => {
		if (busy) return;
		setCap.reset();
		setMode.mutate(on);
	};
	// Without the desktop app, the daemon answers a browser's turn-off with the
	// code whose text asks to approve; this row turns a mode off.
	const modeErrorText = (error: unknown) =>
		error instanceof LadoApiError && error.code === "LADO_PRESENCE_UNAVAILABLE"
			? t("lado.project.modeOffUnavailable")
			: ladoErrorText(t, error);
	// The daemon refuses a cap out of range with the dispatch settings' code,
	// whose text also asks for a run time; this page has none to choose.
	const capErrorText = (error: unknown) =>
		error instanceof LadoApiError && error.code === "LADO_DISPATCH_SETTINGS_INVALID"
			? t("lado.project.maxConcurrentInvalid", { min: String(error.details?.min ?? ""), max: String(error.details?.max ?? "") })
			: ladoErrorText(t, error);
	const changeCap = (value: string) => {
		const maxConcurrent = Number(value);
		if (busy || maxConcurrent === data?.maxConcurrent) return;
		setMode.reset();
		setCap.mutate({ maxConcurrent });
	};

	if (settings.isPending) {
		return (
			<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
				{t("common.loading")}
			</p>
		);
	}
	if (!data) {
		return (
			<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
				{ladoErrorText(t, settings.error)}
			</p>
		);
	}
	return (
		<SettingsSection title={t("lado.project.title")} titleHidden grouped>
			<SettingsRow
				label={t("lado.project.mode")}
				description={
					<>
						{t("lado.project.modeHint")}
						{setMode.isError ? (
							<span role="alert" className="block text-error">
								{modeErrorText(setMode.error)}
							</span>
						) : null}
					</>
				}
			>
				<Switch aria-label={t("lado.project.mode")} checked={data.ladoMode} disabled={busy} onCheckedChange={changeMode} />
			</SettingsRow>
			<SettingsRow
				label={t("lado.project.maxConcurrent")}
				description={
					setCap.isError ? (
						<span role="alert" className="block text-error">
							{capErrorText(setCap.error)}
						</span>
					) : undefined
				}
			>
				<SettingsOptionMenu
					aria-label={t("lado.project.maxConcurrent")}
					value={String(cap)}
					options={ladoConcurrentRunOptions}
					disabled={busy}
					onChange={changeCap}
				/>
			</SettingsRow>
		</SettingsSection>
	);
}
