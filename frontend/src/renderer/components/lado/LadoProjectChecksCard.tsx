// LADO fork: Project settings → LADO → Checks (docs/LJY-PRD/TDD.md §8.2). The
// commands LADO runs on each head of a run's pull request, and how long each
// may run. The daemon runs them in the run's worktree outside any agent
// sandbox, so saving them goes through the desktop app's confirmation; a
// cancelled confirmation keeps the edits. The time limit saves at once.
import { Plus, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useUpdateLadoCheckTimeout, useUpdateLadoProjectChecks } from "../../hooks/useLado";
import { LadoApiError, type LadoCheckCommand, type LadoProjectSettings } from "../../lib/lado";
import { ladoCheckCommandProblemText, ladoErrorText } from "../../lib/lado-errors";
import { SettingsOptionMenu, type SettingsOption } from "../settings/SettingsOptionMenu";
import { SettingsRow } from "../settings/SettingsRow";
import { SettingsSection } from "../settings/SettingsSection";
import { Button } from "../ui/button";

/** The most check commands the daemon takes (domain.LadoCheckCommandsMax). */
export const LADO_MAX_CHECK_COMMANDS = 10;

/** The time limits on offer, in minutes; the daemon takes 5 to 120. */
const timeLimitChoices: readonly number[] = [5, 10, 15, 30, 60, 90, 120];

type Row = { key: number; name: string; command: string };

// Rows keep their key while others are added or removed, so an input keeps
// its focus and text.
let nextRowKey = 0;

function rowsOf(commands: readonly LadoCheckCommand[]): Row[] {
	return commands.map(({ name, command }) => ({ key: nextRowKey++, name, command }));
}

/** The rows a save sends: a row left blank is not sent; names and commands lose surrounding space. */
function filledRows(rows: readonly Row[]): Row[] {
	return rows
		.map((row) => ({ ...row, name: row.name.trim(), command: row.command.trim() }))
		.filter((row) => row.name !== "" || row.command !== "");
}

function sameCommands(a: readonly LadoCheckCommand[], b: readonly LadoCheckCommand[]): boolean {
	return a.length === b.length && a.every((entry, index) => entry.name === b[index].name && entry.command === b[index].command);
}

export function LadoProjectChecksCard({ projectId, settings }: { projectId: string; settings: LadoProjectSettings }) {
	const { t } = useTranslation();
	const saveChecks = useUpdateLadoProjectChecks(projectId);
	const setTimeLimit = useUpdateLadoCheckTimeout(projectId);
	const addRef = useRef<HTMLButtonElement>(null);
	const saved = settings.checkCommands;
	const [rows, setRows] = useState(() => rowsOf(saved));
	const [base, setBase] = useState(saved);
	// The row keys of the commands the last save sent, in order: the daemon
	// names a problem by its index there.
	const [sentKeys, setSentKeys] = useState<number[]>([]);
	const [added, setAdded] = useState<number | null>(null);
	// Set when a removed row took the focused button with it.
	const focusAdd = useRef(false);

	useEffect(() => {
		if (!focusAdd.current) return;
		focusAdd.current = false;
		addRef.current?.focus();
	});

	// The saved commands changed elsewhere (another window, a save that ended):
	// the rows follow them unless the developer has edits of their own.
	if (base !== saved) {
		setBase(saved);
		if (sameCommands(filledRows(rows), base)) setRows(rowsOf(saved));
	}

	const filled = filledRows(rows);
	const dirty = !sameCommands(filled, saved);
	const busy = saveChecks.isPending;
	const atLimit = rows.length >= LADO_MAX_CHECK_COMMANDS;

	const edit = (next: Row[]) => {
		saveChecks.reset();
		setRows(next);
	};
	const change = (key: number, field: "name" | "command", value: string) =>
		edit(rows.map((row) => (row.key === key ? { ...row, [field]: value } : row)));
	const add = () => {
		if (atLimit) return;
		const row = { key: nextRowKey++, name: "", command: "" };
		setAdded(row.key);
		edit([...rows, row]);
	};
	const remove = (key: number) => {
		edit(rows.filter((row) => row.key !== key));
		// The removed row's button goes away; the list's own action takes the
		// focus once it renders, enabled again below the limit.
		focusAdd.current = true;
	};
	const save = () => {
		if (!dirty || busy) return;
		setSentKeys(filled.map((row) => row.key));
		saveChecks.mutate(
			filled.map(({ name, command }) => ({ name, command })),
			{
				// null: the developer cancelled the desktop confirmation, and the edits stay.
				onSuccess: (result) => {
					if (result) setRows(rowsOf(result.checkCommands));
				},
			},
		);
	};

	// A problem with one command shows under its row; any other failure under
	// the list. A browser has no desktop app to confirm the change.
	const error = saveChecks.error;
	const details = error instanceof LadoApiError && error.code === "LADO_CHECK_COMMANDS_INVALID" ? error.details : undefined;
	const problemIndex = typeof details?.index === "number" ? details.index : -1;
	const problemText = details ? ladoCheckCommandProblemText(t, details) : "";
	const problemKey = (details?.field === "name" || details?.field === "command") && problemText ? sentKeys[problemIndex] : undefined;
	const listError =
		!error || problemKey !== undefined
			? null
			: error instanceof LadoApiError && error.code === "LADO_PRESENCE_UNAVAILABLE"
				? t("lado.checks.unavailable")
				: ladoErrorText(t, error);

	const shownMinutes = setTimeLimit.isPending && setTimeLimit.variables !== undefined ? setTimeLimit.variables : settings.checkTimeoutMinutes;
	const minuteChoices = timeLimitChoices.includes(settings.checkTimeoutMinutes)
		? timeLimitChoices
		: [...timeLimitChoices, settings.checkTimeoutMinutes].sort((a, b) => a - b);
	const timeLimitOptions: SettingsOption<string>[] = minuteChoices.map((minutes) => ({
		value: String(minutes),
		label: t("lado.checks.minutes", { count: minutes }),
	}));
	const changeTimeLimit = (value: string) => {
		const minutes = Number(value);
		if (setTimeLimit.isPending || minutes === settings.checkTimeoutMinutes) return;
		setTimeLimit.mutate(minutes);
	};

	return (
		<SettingsSection
			title={t("lado.checks.title")}
			grouped
			testId="lado-project-checks"
			toolbar={
				<p className="px-(--settings-row-inset) pb-(--size-settings-section-inner-gap) text-control leading-4 text-settings-muted">
					{t("lado.checks.description")}
				</p>
			}
		>
			<div className="settings-content-row flex flex-col gap-2" data-testid="lado-check-commands">
				{rows.length === 0 ? (
					<p className="text-sm leading-5 text-settings-muted">{t("lado.checks.empty")}</p>
				) : (
					<>
						{/* The column labels; each input also names its row. */}
						<div className="flex items-center gap-2 text-caption leading-4 text-settings-muted" aria-hidden="true">
							<span className="w-40 shrink-0">{t("lado.checks.name")}</span>
							<span className="min-w-0 flex-1">{t("lado.checks.command")}</span>
							<span className="size-7 shrink-0" />
						</div>
						{rows.map((row, index) => {
							const problem = row.key === problemKey ? problemText : "";
							const problemId = `lado-check-problem-${row.key}`;
							const removeLabel = row.name.trim()
								? t("lado.checks.remove", { name: row.name.trim() })
								: t("lado.checks.removeUnnamed", { number: index + 1 });
							return (
								<div key={row.key} className="flex flex-col gap-1" data-testid="lado-check-command">
									<div className="flex items-center gap-2">
										<input
											aria-label={t("lado.checks.nameLabel", { number: index + 1 })}
											aria-invalid={problem && details?.field === "name" ? true : undefined}
											aria-describedby={problem ? problemId : undefined}
											autoComplete="off"
											autoFocus={row.key === added}
											spellCheck={false}
											disabled={busy}
											placeholder={t("lado.checks.namePlaceholder")}
											value={row.name}
											onChange={(event) => change(row.key, "name", event.target.value)}
											className="settings-field-control h-(--size-settings-action-height) w-40 shrink-0 rounded-md!"
										/>
										{/* Inputs take their font from here (the app's form reset). */}
										<div className="min-w-0 flex-1 font-mono">
											<input
												aria-label={t("lado.checks.commandLabel", { number: index + 1 })}
												aria-invalid={problem && details?.field === "command" ? true : undefined}
												aria-describedby={problem ? problemId : undefined}
												autoComplete="off"
												spellCheck={false}
												disabled={busy}
												placeholder={t("lado.checks.commandPlaceholder")}
												value={row.command}
												onChange={(event) => change(row.key, "command", event.target.value)}
												className="settings-field-control h-(--size-settings-action-height) rounded-md!"
											/>
										</div>
										<Button
											type="button"
											variant="ghost"
											size="icon-sm"
											aria-label={removeLabel}
											title={removeLabel}
											disabled={busy}
											onClick={() => remove(row.key)}
											className="size-7 shrink-0 p-0 text-settings-muted hover:text-destructive"
										>
											<Trash2 className="size-3.5" aria-hidden="true" />
										</Button>
									</div>
									{problem ? (
										<p id={problemId} role="alert" className="text-caption leading-4 text-error">
											{problem}
										</p>
									) : null}
								</div>
							);
						})}
					</>
				)}
				<div className="flex items-center justify-between gap-2">
					<div className="flex min-w-0 items-center gap-2">
						<Button
							ref={addRef}
							type="button"
							variant="ghost"
							size="sm"
							className="-ml-2.5 text-settings-muted hover:text-settings-label"
							disabled={busy || atLimit}
							onClick={add}
						>
							<Plus aria-hidden="true" />
							<span className="text-control">{t("lado.checks.add")}</span>
						</Button>
						{atLimit ? <span className="text-caption leading-4 text-settings-muted">{t("lado.checks.limit")}</span> : null}
					</div>
					<Button type="button" size="sm" disabled={busy || !dirty} onClick={save}>
						<span className="text-control">{busy ? t("lado.checks.saving") : t("lado.checks.save")}</span>
					</Button>
				</div>
				{listError ? (
					<p role="alert" className="text-caption leading-4 text-error">
						{listError}
					</p>
				) : null}
			</div>
			<SettingsRow
				label={t("lado.checks.timeLimit")}
				description={
					<>
						{t("lado.checks.timeLimitHint")}
						{setTimeLimit.isError ? (
							<span role="alert" className="block text-error">
								{ladoErrorText(t, setTimeLimit.error)}
							</span>
						) : null}
					</>
				}
			>
				<SettingsOptionMenu
					aria-label={t("lado.checks.timeLimit")}
					value={String(shownMinutes)}
					options={timeLimitOptions}
					disabled={setTimeLimit.isPending}
					onChange={changeTimeLimit}
				/>
			</SettingsRow>
		</SettingsSection>
	);
}
