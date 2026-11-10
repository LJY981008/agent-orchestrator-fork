// LADO fork: Project settings → LADO → Production log watch (issue #87). The
// project's Docker hosts and the containers LADO follows on each, its extra
// error rules, the hourly issue cap, the automatic plan requests and their
// caps, and how long a host may stay disconnected before LADO tells the
// orchestrator. The card warns that the Engine API grants the whole server,
// so LADO only reads from it, and marks each host without TLS. Every edit
// stays in a draft until Save, which replaces the settings in one PUT; the
// .gitlab-ci.yml import only fills the draft. While the card shows, it reads
// the settings every 10 seconds for the hosts' states, which only the
// daemon's memory holds. The ignored fingerprints can be ignored no more.
import { FileDown, Plus, ShieldAlert } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useLadoLogIncidents,
	useLadoLogWatch,
	useLadoRepositories,
	useSetLadoLogFingerprintIgnored,
	useUpdateLadoLogWatch,
} from "../../hooks/useLado";
import { ladoErrorText } from "../../lib/lado-errors";
import {
	LADO_LOG_MAX_SOURCES,
	LADO_LOG_WATCH_STATUS_REFRESH_MS,
	ladoLogNewKey,
	ladoLogWatchDraft,
	ladoLogWatchInput,
	ladoLogWatchSameInput,
	type LadoLogSourceDraft,
	type LadoLogWatch,
	type LadoLogWatchDraft,
} from "../../lib/lado-log-watch";
import { SettingsOptionMenu, type SettingsOption } from "../settings/SettingsOptionMenu";
import { SettingsRow } from "../settings/SettingsRow";
import { SettingsSection } from "../settings/SettingsSection";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { LadoLogWatchImportDialog } from "./LadoLogWatchImportDialog";
import { LadoLogWatchSource } from "./LadoLogWatchSource";

type NumberSetting = "hourlyIssueCap" | "autoPlanDailyCap" | "autoPlanMinCount" | "disconnectAlertMinutes";

/** The choices each number offers; a value saved outside them still shows as itself. */
const choices: Record<NumberSetting, readonly number[]> = {
	hourlyIssueCap: [1, 2, 3, 5, 10, 20, 50],
	autoPlanDailyCap: [1, 2, 3, 5, 10, 20, 50],
	autoPlanMinCount: [1, 2, 3, 5, 10, 20, 50, 100],
	disconnectAlertMinutes: [5, 10, 15, 30, 60, 120, 240],
};

export function LadoLogWatchCard({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const watch = useLadoLogWatch(projectId, { refetchInterval: LADO_LOG_WATCH_STATUS_REFRESH_MS });
	return (
		<SettingsSection
			title={t("lado.logWatch.title")}
			grouped
			testId="lado-log-watch-card"
			toolbar={
				<p className="px-(--settings-row-inset) pb-(--size-settings-section-inner-gap) text-control leading-4 text-settings-muted">
					{t("lado.logWatch.description")}
				</p>
			}
		>
			{watch.data ? (
				<LogWatchForm projectId={projectId} saved={watch.data} />
			) : watch.isPending ? (
				<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
					{t("common.loading")}
				</p>
			) : (
				<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
					{ladoErrorText(t, watch.error)}
				</p>
			)}
		</SettingsSection>
	);
}

function LogWatchForm({ projectId, saved }: { projectId: string; saved: LadoLogWatch }) {
	const { t } = useTranslation();
	const update = useUpdateLadoLogWatch(projectId);
	const repositories = useLadoRepositories(projectId);
	const [draft, setDraft] = useState(() => ladoLogWatchDraft(saved));
	// The settings the draft started from. A refresh that changed them (another
	// window, a save) moves the draft too unless the developer has edits; the
	// sources' states change every refresh and leave it alone.
	const [base, setBase] = useState(() => ladoLogWatchDraft(saved));
	const [importing, setImporting] = useState(false);
	const savedDraft = ladoLogWatchDraft(saved);
	if (!ladoLogWatchSameInput(savedDraft, base)) {
		setBase(savedDraft);
		if (ladoLogWatchSameInput(draft, base)) setDraft(savedDraft);
	}

	const busy = update.isPending;
	const dirty = !ladoLogWatchSameInput(draft, savedDraft);
	const edit = (next: LadoLogWatchDraft) => {
		update.reset();
		setDraft(next);
	};
	const set = <K extends keyof LadoLogWatchDraft>(key: K, value: LadoLogWatchDraft[K]) => edit({ ...draft, [key]: value });
	const changeSource = (key: string, next: LadoLogSourceDraft) =>
		edit({ ...draft, sources: draft.sources.map((source) => (source.key === key ? next : source)) });
	const addSources = (sources: LadoLogSourceDraft[]) => edit({ ...draft, sources: [...draft.sources, ...sources] });
	const save = () => {
		if (!dirty || busy) return;
		update.mutate(ladoLogWatchInput(draft), {
			onSuccess: (result) => {
				const next = ladoLogWatchDraft(result);
				setBase(next);
				setDraft(next);
			},
		});
	};

	const numberOptions = (setting: NumberSetting, label: (value: number) => string): SettingsOption<string>[] =>
		[...new Set([...choices[setting], draft[setting]])].sort((a, b) => a - b).map((value) => ({ value: String(value), label: label(value) }));
	const numberRow = (setting: NumberSetting, label: string, hint: string, text: (value: number) => string, disabled = false) => (
		<SettingsRow label={label} description={hint}>
			<SettingsOptionMenu
				aria-label={label}
				value={String(draft[setting])}
				options={numberOptions(setting, text)}
				disabled={busy || disabled}
				onChange={(value) => set(setting, Number(value))}
			/>
		</SettingsRow>
	);

	const repos = repositories.data ?? [];
	const repositoryOptions: SettingsOption<string>[] = [
		...(draft.repository === "" ? [{ value: "", label: t("lado.logWatch.repositoryDefault") }] : []),
		...repos.map((repository) => ({ value: repository.ref, label: repository.name })),
	];
	const existingEndpoints = new Set(draft.sources.map((source) => source.endpoint.trim()).filter(Boolean));
	const room = LADO_LOG_MAX_SOURCES - draft.sources.length;
	const error = update.error;

	return (
		<>
			{!saved.ladoMode ? (
				<p className="settings-content-row text-caption leading-4 text-[var(--color-status-needs-you)]" role="status" data-testid="lado-log-watch-mode-off">
					{t("lado.logWatch.modeOff")}
				</p>
			) : null}
			<SettingsRow label={t("lado.logWatch.enabled")} description={t("lado.logWatch.enabledHint")}>
				<Switch aria-label={t("lado.logWatch.enabled")} checked={draft.enabled} disabled={busy} onCheckedChange={(on) => set("enabled", on)} />
			</SettingsRow>
			{repos.length > 1 ? (
				<SettingsRow label={t("lado.logWatch.repository")} description={t("lado.logWatch.repositoryHint")}>
					<SettingsOptionMenu
						aria-label={t("lado.logWatch.repository")}
						value={draft.repository}
						options={repositoryOptions}
						disabled={busy}
						onChange={(value) => set("repository", value)}
					/>
				</SettingsRow>
			) : null}
			<div className="settings-content-row flex flex-col gap-2">
				<p
					className="flex items-start gap-1.5 rounded-md bg-[var(--color-bg-settings-input)] px-2.5 py-2 text-caption leading-4 text-settings-label"
					role="note"
					data-testid="lado-log-watch-readonly-warning"
				>
					<ShieldAlert className="mt-px size-3.5 shrink-0 text-[var(--color-status-needs-you)]" aria-hidden="true" />
					<span>{t("lado.logWatch.readonlyWarning")}</span>
				</p>
				<div className="flex items-center justify-between gap-2">
					<span className="text-sm font-medium text-settings-label">{t("lado.logWatch.sources")}</span>
					<div className="flex items-center gap-1">
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="text-settings-muted hover:text-settings-label"
							disabled={busy || room <= 0}
							data-testid="lado-log-watch-import-ci"
							onClick={() => setImporting(true)}
						>
							<FileDown aria-hidden="true" />
							<span className="text-control">{t("lado.logWatch.importCI")}</span>
						</Button>
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="text-settings-muted hover:text-settings-label"
							disabled={busy || room <= 0}
							onClick={() =>
								addSources([{ key: ladoLogNewKey(), displayName: "", endpoint: "", tls: true, paused: false, targets: [] }])
							}
						>
							<Plus aria-hidden="true" />
							<span className="text-control">{t("lado.logWatch.addSource")}</span>
						</Button>
					</div>
				</div>
				{draft.sources.length === 0 ? <p className="text-sm leading-5 text-settings-muted">{t("lado.logWatch.sourcesEmpty")}</p> : null}
				{room <= 0 ? <p className="text-caption leading-4 text-settings-muted">{t("lado.logWatch.sourcesLimit", { max: LADO_LOG_MAX_SOURCES })}</p> : null}
			</div>
			{draft.sources.map((source, index) => (
				<LadoLogWatchSource
					key={source.key}
					projectId={projectId}
					index={index}
					source={source}
					saved={source.id ? saved.sources.find((candidate) => candidate.id === source.id) : undefined}
					busy={busy}
					onChange={(next) => changeSource(source.key, next)}
					onRemove={() => edit({ ...draft, sources: draft.sources.filter((candidate) => candidate.key !== source.key) })}
				/>
			))}
			<div className="settings-content-row flex flex-col gap-2">
				<PatternField
					label={t("lado.logWatch.include")}
					hint={t("lado.logWatch.includeHint")}
					value={draft.include}
					disabled={busy}
					onChange={(value) => set("include", value)}
				/>
				<PatternField
					label={t("lado.logWatch.exclude")}
					hint={t("lado.logWatch.excludeHint")}
					value={draft.exclude}
					disabled={busy}
					onChange={(value) => set("exclude", value)}
				/>
			</div>
			{numberRow("hourlyIssueCap", t("lado.logWatch.hourlyIssueCap"), t("lado.logWatch.hourlyIssueCapHint"), (count) =>
				t("lado.logWatch.issuesPerHour", { count }),
			)}
			<SettingsRow label={t("lado.logWatch.autoPlan")} description={t("lado.logWatch.autoPlanHint")}>
				<Switch aria-label={t("lado.logWatch.autoPlan")} checked={draft.autoPlan} disabled={busy} onCheckedChange={(on) => set("autoPlan", on)} />
			</SettingsRow>
			{numberRow(
				"autoPlanDailyCap",
				t("lado.logWatch.autoPlanDailyCap"),
				t("lado.logWatch.autoPlanDailyCapHint"),
				(count) => t("lado.logWatch.plansPerDay", { count }),
				!draft.autoPlan,
			)}
			{numberRow(
				"autoPlanMinCount",
				t("lado.logWatch.autoPlanMinCount"),
				t("lado.logWatch.autoPlanMinCountHint"),
				(count) => t("lado.logWatch.timesAtLeast", { count }),
				!draft.autoPlan,
			)}
			{numberRow("disconnectAlertMinutes", t("lado.logWatch.disconnectMinutes"), t("lado.logWatch.disconnectMinutesHint"), (count) =>
				t("lado.logWatch.minutes", { count }),
			)}
			<IgnoredFingerprints projectId={projectId} />
			<div className="settings-content-row flex flex-col gap-1.5">
				<div className="flex items-center justify-end gap-3">
					{dirty ? <span className="text-caption leading-4 text-settings-muted">{t("lado.logWatch.unsaved")}</span> : null}
					<Button type="button" size="sm" disabled={busy || !dirty} data-testid="lado-log-watch-save" onClick={save}>
						<span className="text-control">{busy ? t("lado.logWatch.saving") : t("lado.logWatch.save")}</span>
					</Button>
				</div>
				{error ? (
					// The daemon's message is in Korean only; the translated
					// line names the setting.
					<p role="alert" className="text-caption leading-4 text-error">
						{ladoErrorText(t, error)}
					</p>
				) : null}
			</div>
			<LadoLogWatchImportDialog
				projectId={projectId}
				open={importing}
				existingEndpoints={existingEndpoints}
				room={room}
				onOpenChange={setImporting}
				onAdd={addSources}
			/>
		</>
	);
}

function PatternField({
	label,
	hint,
	value,
	disabled,
	onChange,
}: {
	label: string;
	hint: string;
	value: string;
	disabled: boolean;
	onChange: (value: string) => void;
}) {
	return (
		<label className="flex flex-col gap-1">
			<span className="settings-field-label">{label}</span>
			<span className="text-caption leading-4 text-settings-muted">{hint}</span>
			{/* The textarea takes its font from here (the app's form reset). */}
			<span className="font-mono">
				<textarea
					value={value}
					rows={2}
					spellCheck={false}
					autoComplete="off"
					disabled={disabled}
					onChange={(event) => onChange(event.target.value)}
					className="settings-field-control min-h-(--size-textarea-min) resize-y overflow-y-auto rounded-md! py-2"
				/>
			</span>
		</label>
	);
}

/** The fingerprints the developer ignored, each of which can be ignored no more. */
function IgnoredFingerprints({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const incidents = useLadoLogIncidents(projectId);
	const setIgnored = useSetLadoLogFingerprintIgnored(projectId);
	const ignored = (incidents.data ?? []).filter((fingerprint) => fingerprint.ignored);
	return (
		<div className="settings-content-row flex flex-col gap-1.5" data-testid="lado-log-watch-ignored">
			<span className="text-sm font-medium text-settings-label">{t("lado.logWatch.ignored")}</span>
			{incidents.isError ? (
				<p className="text-caption leading-4 text-error" role="alert">
					{ladoErrorText(t, incidents.error)}
				</p>
			) : ignored.length === 0 ? (
				<p className="text-caption leading-4 text-settings-muted">{t("lado.logWatch.ignoredEmpty")}</p>
			) : (
				<ul className="flex flex-col gap-1">
					{ignored.map((fingerprint) => (
						<li key={fingerprint.id} className="flex min-w-0 items-center gap-2 text-control" data-testid="lado-log-watch-ignored-item">
							<span className="min-w-0 truncate font-mono text-settings-label" title={fingerprint.location}>
								{fingerprint.service} · {fingerprint.exceptionKind}
							</span>
							<span className="shrink-0 tabular-nums text-caption text-settings-muted">{t("lado.logIncident.times", { count: fingerprint.count })}</span>
							<Button
								type="button"
								variant="ghost"
								size="sm"
								className="ml-auto text-settings-muted hover:text-settings-label"
								disabled={setIgnored.isPending}
								onClick={() => setIgnored.mutate({ fingerprintId: fingerprint.id, ignored: false })}
							>
								<span className="text-control">{t("lado.logWatch.unignore")}</span>
							</Button>
						</li>
					))}
				</ul>
			)}
			{setIgnored.isError ? (
				<p className="text-caption leading-4 text-error" role="alert">
					{ladoErrorText(t, setIgnored.error)}
				</p>
			) : null}
		</div>
	);
}
