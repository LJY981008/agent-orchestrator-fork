// LADO fork: Project settings → LADO → Production database changes (issue
// #97, docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md). For
// each repository (each child repository of a workspace): whether LADO
// applies the pull request's SQL files when the developer approves its
// integration, the globs of those files, the command that applies one file,
// its time limit, and the secrets the command gets as environment variables.
// The command runs against the production database outside any agent
// sandbox, so saving goes through the desktop app's confirmation, which names
// the change without any secret's value; a cancelled confirmation keeps the
// edits. A value typed here goes to the daemon once and is never shown again:
// the card only says which names have one stored.
import type { TFunction } from "i18next";
import { Plus, ShieldAlert, Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useLadoDBChangeSettings, useUpdateLadoDBChangeSettings } from "../../hooks/useLado";
import type { MessageKey } from "../../i18n/messages";
import { LadoApiError } from "../../lib/lado";
import {
	LADO_DB_CHANGE_MAX_PATHS,
	LADO_DB_CHANGE_MAX_SECRETS,
	LADO_DB_CHANGE_SECRET_NAME,
	ladoDBChangeDirty,
	ladoDBChangeDraft,
	ladoDBChangeInput,
	ladoDBChangeMissingSecrets,
	ladoDBChangeProblemField,
	ladoDBChangeProblems,
	ladoDBChangeSecretKey,
	type LadoDBChangeDraft,
	type LadoDBChangeProblem,
	type LadoDBChangeRepoSettings,
} from "../../lib/lado-db-changes";
import { ladoErrorText } from "../../lib/lado-errors";
import { SettingsOptionMenu, type SettingsOption } from "../settings/SettingsOptionMenu";
import { SettingsRow } from "../settings/SettingsRow";
import { SettingsSection } from "../settings/SettingsSection";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";

/** The time limits on offer, in minutes; the daemon takes 1 to 120. */
const timeLimitChoices: readonly number[] = [5, 10, 15, 30, 60, 90, 120];

const fieldKeys: Partial<Record<string, MessageKey>> = {
	repo: "lado.dbChanges.field.repo",
	paths: "lado.dbChanges.field.paths",
	command: "lado.dbChanges.field.command",
	timeoutMinutes: "lado.dbChanges.field.timeoutMinutes",
	secretNames: "lado.dbChanges.field.secretNames",
};

const reasonKeys: Partial<Record<string, MessageKey>> = {
	required: "lado.dbChanges.problem.required",
	too_many: "lado.dbChanges.problem.tooMany",
	too_long: "lado.dbChanges.problem.tooLong",
	invalid: "lado.dbChanges.problem.invalid",
	duplicate: "lado.dbChanges.problem.duplicate",
	parent_dir: "lado.dbChanges.problem.parentDir",
	absolute: "lado.dbChanges.problem.absolute",
	no_file_placeholder: "lado.dbChanges.problem.noFilePlaceholder",
	control_character: "lado.dbChanges.problem.controlCharacter",
	bidi_control: "lado.dbChanges.problem.bidiControl",
	out_of_range: "lado.dbChanges.problem.outOfRange",
	reserved: "lado.dbChanges.problem.reserved",
};

/** One refused setting in the developer's language; a reason this app does not know reads as is. */
export function ladoDBChangeProblemText(t: TFunction, problem: LadoDBChangeProblem): string {
	const { name, index } = ladoDBChangeProblemField(problem.field);
	const fieldKey = fieldKeys[name];
	const label = fieldKey ? t(fieldKey) : name;
	const field = index === undefined ? label : t("lado.dbChanges.fieldItem", { field: label, number: index + 1 });
	const reasonKey = reasonKeys[problem.reason];
	return t("lado.dbChanges.problemLine", { field, reason: reasonKey ? t(reasonKey) : problem.reason });
}

export function LadoDBChangeSettingsCard({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const settings = useLadoDBChangeSettings(projectId);
	const repositories = settings.data?.repositories ?? [];
	return (
		<SettingsSection
			title={t("lado.dbChanges.title")}
			grouped
			testId="lado-db-changes-card"
			toolbar={
				<p className="px-(--settings-row-inset) pb-(--size-settings-section-inner-gap) text-control leading-4 text-settings-muted">
					{t("lado.dbChanges.description")}
				</p>
			}
		>
			{settings.data ? (
				repositories.length > 0 ? (
					repositories.map((repo) => (
						<RepoSettings key={repo.repo} projectId={projectId} repo={repo} named={repositories.length > 1} />
					))
				) : (
					<p className="settings-content-row text-sm leading-5 text-settings-muted">{t("lado.dbChanges.noRepositories")}</p>
				)
			) : settings.isPending ? (
				<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
					{t("common.loading")}
				</p>
			) : (
				<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
					{ladoErrorText(t, settings.error)}
				</p>
			)}
		</SettingsSection>
	);
}

/** One repository's settings: the switch, then its files, command, time limit and secrets while it is on. */
function RepoSettings({ projectId, repo, named }: { projectId: string; repo: LadoDBChangeRepoSettings; named: boolean }) {
	const { t } = useTranslation();
	const update = useUpdateLadoDBChangeSettings(projectId);
	const [draft, setDraft] = useState(() => ladoDBChangeDraft(repo));
	// The saved settings the draft started from. When they change elsewhere
	// (another window, a save that ended), the draft follows them unless the
	// developer has edits of their own, or a save was refused because of
	// that change.
	const [base, setBase] = useState(repo);
	const [followSaved, setFollowSaved] = useState(false);
	if (base.sha256 !== repo.sha256 || base.storedSecretNames.join("\0") !== repo.storedSecretNames.join("\0")) {
		setBase(repo);
		if (followSaved || !ladoDBChangeDirty(base, draft)) setDraft(ladoDBChangeDraft(repo));
		setFollowSaved(false);
	}

	const busy = update.isPending;
	const dirty = ladoDBChangeDirty(repo, draft);
	const edit = (next: LadoDBChangeDraft) => {
		update.reset();
		setFollowSaved(false);
		setDraft(next);
	};
	const set = <K extends keyof LadoDBChangeDraft>(key: K, value: LadoDBChangeDraft[K]) => edit({ ...draft, [key]: value });
	const save = () => {
		if (!dirty || busy) return;
		update.mutate(ladoDBChangeInput(repo, draft), {
			// null: the developer cancelled the desktop confirmation, and the edits stay.
			onSuccess: (result) => {
				const saved = result?.repositories.find((entry) => entry.repo === repo.repo);
				if (!saved) return;
				setBase(saved);
				setDraft(ladoDBChangeDraft(saved));
			},
			onError: (failure) => {
				if (failure instanceof LadoApiError && failure.code === "LADO_DB_CHANGE_SETTINGS_CHANGED") setFollowSaved(true);
			},
		});
	};

	const id = `lado-db-changes-${repo.repo || "root"}`;
	const label = named ? t("lado.dbChanges.enabledFor", { repo: repo.name || repo.repo }) : t("lado.dbChanges.enabled");
	const pathCount = draft.paths.split(/\r?\n/).filter((line) => line.trim()).length;
	const missing = repo.enabled ? ladoDBChangeMissingSecrets(repo) : [];
	const minuteChoices = timeLimitChoices.includes(draft.timeoutMinutes)
		? timeLimitChoices
		: [...timeLimitChoices, draft.timeoutMinutes].sort((a, b) => a - b);
	const timeLimitOptions: SettingsOption<string>[] = minuteChoices.map((minutes) => ({
		value: String(minutes),
		label: t("lado.dbChanges.minutes", { count: minutes }),
	}));

	// Problems with a field show under the form, each naming its field; a
	// browser has no desktop app to confirm the change.
	const error = update.error;
	const problems = ladoDBChangeProblems(error);
	const errorText =
		!error || problems.length > 0
			? null
			: error instanceof LadoApiError && error.code === "LADO_PRESENCE_UNAVAILABLE"
				? t("lado.dbChanges.unavailable")
				: ladoErrorText(t, error);

	return (
		<>
			<SettingsRow label={label} description={t("lado.dbChanges.enabledHint")}>
				<Switch aria-label={label} checked={draft.enabled} disabled={busy} onCheckedChange={(on) => set("enabled", on)} />
			</SettingsRow>
			{draft.enabled ? (
				<>
					<div className="settings-content-row flex flex-col gap-3" data-testid="lado-db-changes-repo" data-repo={repo.repo}>
						<p
							className="flex items-start gap-1.5 rounded-md bg-[var(--color-bg-settings-input)] px-2.5 py-2 text-caption leading-4 text-settings-label"
							role="note"
						>
							<ShieldAlert className="mt-px size-3.5 shrink-0 text-[var(--color-status-needs-you)]" aria-hidden="true" />
							<span>{t("lado.dbChanges.warning")}</span>
						</p>
						<div className="flex flex-col gap-1">
							<label htmlFor={`${id}-paths`} className="text-caption leading-4 text-settings-muted">
								{t("lado.dbChanges.paths")}
							</label>
							{/* The textarea takes its font from here (the app's form reset). */}
							<div className="font-mono">
								<textarea
									id={`${id}-paths`}
									rows={2}
									autoComplete="off"
									spellCheck={false}
									disabled={busy}
									placeholder={t("lado.dbChanges.pathsPlaceholder")}
									value={draft.paths}
									onChange={(event) => set("paths", event.target.value)}
									className="settings-field-control min-h-(--size-textarea-min) resize-none rounded-md! py-2"
								/>
							</div>
							<p className="text-caption leading-4 text-settings-muted">
								{t("lado.dbChanges.pathsHint", { max: LADO_DB_CHANGE_MAX_PATHS })}
								{pathCount > LADO_DB_CHANGE_MAX_PATHS ? (
									<span className="block text-error">{t("lado.dbChanges.pathsTooMany", { max: LADO_DB_CHANGE_MAX_PATHS })}</span>
								) : null}
							</p>
						</div>
						<div className="flex flex-col gap-1">
							<label htmlFor={`${id}-command`} className="text-caption leading-4 text-settings-muted">
								{t("lado.dbChanges.command")}
							</label>
							{/* Inputs take their font from here (the app's form reset). */}
							<div className="font-mono">
								<input
									id={`${id}-command`}
									autoComplete="off"
									spellCheck={false}
									disabled={busy}
									placeholder={t("lado.dbChanges.commandPlaceholder")}
									value={draft.command}
									onChange={(event) => set("command", event.target.value)}
									className="settings-field-control h-(--size-settings-action-height) rounded-md!"
								/>
							</div>
							<p className="text-caption leading-4 text-settings-muted">{t("lado.dbChanges.commandHint")}</p>
						</div>
						<SecretRows repo={repo} draft={draft} busy={busy} onChange={(secrets) => set("secrets", secrets)} id={id} />
						{repo.baselineCommit ? (
							<p className="text-caption leading-4 text-settings-muted" data-testid="lado-db-changes-baseline">
								{t("lado.dbChanges.baseline")} <span className="font-mono">{repo.baselineCommit.slice(0, 7)}</span>
							</p>
						) : null}
					</div>
					<SettingsRow label={t("lado.dbChanges.timeLimit")} description={t("lado.dbChanges.timeLimitHint")}>
						<SettingsOptionMenu
							aria-label={t("lado.dbChanges.timeLimit")}
							value={String(draft.timeoutMinutes)}
							options={timeLimitOptions}
							disabled={busy}
							onChange={(value) => set("timeoutMinutes", Number(value))}
						/>
					</SettingsRow>
				</>
			) : null}
			{dirty || error || missing.length > 0 ? (
				<div className="settings-content-row flex flex-col gap-2">
					{missing.length > 0 && !dirty ? (
						<p className="text-caption leading-4 text-[var(--color-status-needs-you)]" role="status" data-testid="lado-db-changes-missing">
							{t("lado.dbChanges.secretsMissing", { names: missing.join(", ") })}
						</p>
					) : null}
					{dirty ? (
						<div className="flex items-center justify-end gap-2">
							<Button type="button" variant="ghost" size="sm" disabled={busy} onClick={() => edit(ladoDBChangeDraft(repo))}>
								<span className="text-control">{t("lado.dbChanges.discard")}</span>
							</Button>
							<Button type="button" size="sm" disabled={busy} onClick={save} data-testid="lado-db-changes-save">
								<span className="text-control">{busy ? t("lado.dbChanges.saving") : t("lado.dbChanges.save")}</span>
							</Button>
						</div>
					) : null}
					{problems.length > 0 ? (
						<ul role="alert" className="flex flex-col gap-0.5 text-caption leading-4 text-error" data-testid="lado-db-changes-problems">
							{problems.map((problem, index) => (
								<li key={`${problem.field}:${problem.reason}:${index}`}>{ladoDBChangeProblemText(t, problem)}</li>
							))}
						</ul>
					) : errorText ? (
						<p role="alert" className="text-caption leading-4 text-error">
							{errorText}
						</p>
					) : null}
				</div>
			) : null}
		</>
	);
}

/**
 * The secrets' names and, for each, a new value or none. A stored value is
 * never shown: the field only says one is stored, and typing replaces it.
 */
function SecretRows({
	repo,
	draft,
	busy,
	id,
	onChange,
}: {
	repo: LadoDBChangeRepoSettings;
	draft: LadoDBChangeDraft;
	busy: boolean;
	id: string;
	onChange: (secrets: LadoDBChangeDraft["secrets"]) => void;
}) {
	const { t } = useTranslation();
	const atLimit = draft.secrets.length >= LADO_DB_CHANGE_MAX_SECRETS;
	const change = (key: number, field: "name" | "value", value: string) =>
		onChange(draft.secrets.map((secret) => (secret.key === key ? { ...secret, [field]: value } : secret)));
	return (
		<div className="flex flex-col gap-1" data-testid="lado-db-changes-secrets">
			<span className="text-caption leading-4 text-settings-muted">{t("lado.dbChanges.secrets")}</span>
			{draft.secrets.map((secret, index) => {
				const name = secret.name.trim();
				const stored = name !== "" && repo.storedSecretNames.includes(name) && repo.secretNames.includes(name);
				const badName = name !== "" && !LADO_DB_CHANGE_SECRET_NAME.test(name);
				const removeLabel = name ? t("lado.dbChanges.secretRemove", { name }) : t("lado.dbChanges.secretRemoveUnnamed", { number: index + 1 });
				return (
					<div key={secret.key} className="flex flex-col gap-0.5" data-testid="lado-db-changes-secret">
						<div className="flex items-center gap-2">
							<div className="w-48 shrink-0 font-mono">
								<input
									aria-label={t("lado.dbChanges.secretName", { number: index + 1 })}
									aria-invalid={badName ? true : undefined}
									autoComplete="off"
									spellCheck={false}
									disabled={busy}
									placeholder={t("lado.dbChanges.secretNamePlaceholder")}
									value={secret.name}
									onChange={(event) => change(secret.key, "name", event.target.value.toUpperCase())}
									className="settings-field-control h-(--size-settings-action-height) rounded-md!"
								/>
							</div>
							<div className="min-w-0 flex-1 font-mono">
								<input
									id={`${id}-secret-${secret.key}`}
									type="password"
									aria-label={t("lado.dbChanges.secretValue", { number: index + 1 })}
									autoComplete="new-password"
									spellCheck={false}
									disabled={busy}
									placeholder={stored ? t("lado.dbChanges.secretStored") : t("lado.dbChanges.secretValuePlaceholder")}
									value={secret.value}
									onChange={(event) => change(secret.key, "value", event.target.value)}
									className="settings-field-control h-(--size-settings-action-height) rounded-md!"
									data-stored={stored ? "true" : undefined}
								/>
							</div>
							<Button
								type="button"
								variant="ghost"
								size="icon-sm"
								aria-label={removeLabel}
								title={removeLabel}
								disabled={busy}
								onClick={() => onChange(draft.secrets.filter((entry) => entry.key !== secret.key))}
								className="size-7 shrink-0 p-0 text-settings-muted hover:text-destructive"
							>
								<Trash2 className="size-3.5" aria-hidden="true" />
							</Button>
						</div>
						{badName ? <p className="text-caption leading-4 text-error">{t("lado.dbChanges.secretNameInvalid")}</p> : null}
					</div>
				);
			})}
			<div className="flex items-center gap-2">
				<Button
					type="button"
					variant="ghost"
					size="sm"
					className="-ml-2.5 text-settings-muted hover:text-settings-label"
					disabled={busy || atLimit}
					onClick={() => onChange([...draft.secrets, { key: ladoDBChangeSecretKey(), name: "", value: "" }])}
				>
					<Plus aria-hidden="true" />
					<span className="text-control">{t("lado.dbChanges.secretAdd")}</span>
				</Button>
			</div>
			<p className="text-caption leading-4 text-settings-muted">{t("lado.dbChanges.secretsHint")}</p>
		</div>
	);
}
