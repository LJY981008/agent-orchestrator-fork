// LADO fork: adopting local setup (docs/LJY-PRD/HARNESS.md §9). The developer
// picks the entries and whether each moves into LADO's managed folder or
// stays in place; the daemon's preview shows where they go, the commands it
// rewrites, the conflicts they bring and what holds the adoption back.
// Adopting removes the developer's files, so it ends in the desktop app's
// confirmation, and the dialog then says what moved and whether the checks
// passed, or where a stopped adoption left things.
import { agentLabel } from "@aoagents/product-ui";
import { X } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useAdoptLadoSetup, useLadoSetupAdoptionPreview } from "../../../hooks/useLadoSetup";
import { useRestoreFocusOnClose } from "../../../hooks/useRestoreFocusOnClose";
import type { MessageKey } from "../../../i18n/messages";
import { LadoApiError } from "../../../lib/lado";
import { ladoErrorText, ladoSetupConflictText, ladoSetupKindLabel, ladoSetupWarningText } from "../../../lib/lado-errors";
import type {
	LadoSetupAdoptEntry,
	LadoSetupAdoptionPreview,
	LadoSetupAdoptResult,
	LadoSetupConflict,
	LadoSetupEntry,
	LadoSetupInventory,
} from "../../../lib/lado-setup";
import { cn } from "../../../lib/utils";
import { SettingsOptionMenu } from "../../settings/SettingsOptionMenu";
import { SettingsRow } from "../../settings/SettingsRow";
import { Button } from "../../ui/button";
import { Checkbox } from "../../ui/checkbox";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../../ui/dialog";
import {
	ladoSetupAdoptable,
	ladoSetupConflictsBySeverity,
	ladoSetupEntryKey,
	ladoSetupOSLabel,
	ladoSetupRootedPath,
	ladoSetupSeverityDotClass,
	ladoSetupSeverityKeys,
	ladoSetupShortPath,
} from "./lado-setup-view";

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

// A command, or a setting left in place, that names a moved file LADO cannot
// rewrite stops the adoption. Any other blocking conflict stays the same
// whether the entry moves or not, so it shows in the caution tone and does not
// stop it.
const STOPPING_CONFLICTS = new Set(["LADO_SETUP_HOOK_PATH_MOVED", "LADO_SETUP_PATH_USED_IN_PLACE"]);
const DIRECT_USE_STOPS = "LADO_SETUP_DIRECT_USE_STOPS";

// Each preview scans again, so a change of the choices waits for a pause.
const PREVIEW_DELAY_MS = 300;

// Codex reads either of these from its folder: the override when it has text.
const CODEX_INSTRUCTIONS = ["AGENTS.md", "AGENTS.override.md"];

const checkKeys: Partial<Record<string, MessageKey>> = {
	managed_copy: "lado.setup.check.managedCopy",
	codex_override: "lado.setup.check.codexOverride",
	home: "lado.setup.check.home",
};

const stageKeys: Partial<Record<string, MessageKey>> = {
	backup: "lado.setup.stage.backup",
	copy: "lado.setup.stage.copy",
	remove: "lado.setup.stage.remove",
	record: "lado.setup.stage.record",
};

type Action = LadoSetupAdoptEntry["action"];
type Choice = { selected: boolean; action: Action };
type Outcome =
	| { kind: "adopted"; result: LadoSetupAdoptResult; entries: LadoSetupAdoptEntry[] }
	| { kind: "stopped"; error: LadoApiError };

function defaultAction(entry: LadoSetupEntry): Action {
	return entry.defaultAction === "keep" ? "keep" : "move";
}

/** The value as it was once it stopped changing for a while. */
function useSettled(value: string, delayMs: number): string {
	const [settled, setSettled] = useState(value);
	useEffect(() => {
		if (value === settled) return;
		const timer = window.setTimeout(() => setSettled(value), delayMs);
		return () => window.clearTimeout(timer);
	}, [value, settled, delayMs]);
	return settled;
}

/**
 * Open while the developer adopts. Every adoptable entry starts chosen with
 * its default action, or only the one only names (its tool/kind/name key),
 * as a new entry found by drift is adopted. Mount it again for each opening
 * so it starts from the inventory as it is then.
 */
export function LadoSetupAdoptDialog({
	open,
	inventory,
	only,
	onOpenChange,
}: {
	open: boolean;
	inventory: LadoSetupInventory;
	only?: string;
	onOpenChange: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const firstRef = useRef<HTMLButtonElement>(null);
	const allId = useId();
	const candidates = useMemo(
		() => ladoSetupAdoptable(inventory).filter((entry) => Boolean(inventory.verified[entry.tool])),
		[inventory],
	);
	const [choices, setChoices] = useState<Record<string, Choice>>(() =>
		Object.fromEntries(
			candidates.map((entry) => [
				ladoSetupEntryKey(entry),
				{ selected: only === undefined || ladoSetupEntryKey(entry) === only, action: defaultAction(entry) },
			]),
		),
	);
	const adopt = useAdoptLadoSetup();
	const [submitting, setSubmitting] = useState(false);
	const [adoptError, setAdoptError] = useState<unknown>(null);
	const [outcome, setOutcome] = useState<Outcome | null>(null);
	const choiceOf = (entry: LadoSetupEntry): Choice =>
		choices[ladoSetupEntryKey(entry)] ?? { selected: false, action: defaultAction(entry) };
	// A new choice makes a refused adoption's message stale.
	const setChosen = (next: (current: Record<string, Choice>) => Record<string, Choice>) => {
		setAdoptError(null);
		setChoices(next);
	};
	const choose = (entry: LadoSetupEntry, change: Partial<Choice>) =>
		setChosen((current) => ({ ...current, [ladoSetupEntryKey(entry)]: { ...choiceOf(entry), ...change } }));

	const requested: LadoSetupAdoptEntry[] = candidates
		.filter((entry) => choiceOf(entry).selected)
		.map((entry) => ({ tool: entry.tool, kind: entry.kind as LadoSetupAdoptEntry["kind"], name: entry.name, action: choiceOf(entry).action }));
	const requestedKey = JSON.stringify(requested);
	const settledKey = useSettled(requestedKey, PREVIEW_DELAY_MS);
	const settled = useMemo(() => JSON.parse(settledKey) as LadoSetupAdoptEntry[], [settledKey]);

	// The preview waits while the desktop app confirms and stops once done.
	const preview = useLadoSetupAdoptionPreview(settled, open && !submitting && outcome === null);
	// The last preview stays on screen while a newer one loads.
	const [shown, setShown] = useState<LadoSetupAdoptionPreview | undefined>(preview.data);
	if (preview.data && preview.data !== shown) setShown(preview.data);
	const current = settledKey === requestedKey && !preview.isFetching && !preview.isError && preview.data !== undefined;

	const moves = requested.filter((entry) => entry.action === "move").length;
	const conflicts = shown ? ladoSetupConflictsBySeverity(shown.conflicts) : [];
	const unmovable = conflicts.find((conflict) => STOPPING_CONFLICTS.has(conflict.code));
	const running = shown ? [...shown.preconditions.sessions, ...shown.preconditions.processes].length > 0 : false;
	const unverified = shown?.preconditions.unverified ?? [];
	const managedPaths = new Map((shown?.entries ?? []).map((entry) => [ladoSetupEntryKey(entry), entry.managedPath]));

	// Why Adopt is off, in the order the daemon refuses; empty when the
	// preview's own error already says it.
	const hold = (): string | null => {
		if (requested.length === 0) return t("lado.setup.adopt.chooseItems");
		if (moves === 0) return t("lado.setup.adopt.noMove");
		if (!current) return preview.isError ? "" : t("lado.setup.adopt.checking");
		if (unmovable) return ladoErrorText(t, new LadoApiError("", unmovable.code, unmovable.details));
		if (unverified.length > 0) return t("lado.setup.section.osUnverified", { os: ladoSetupOSLabel(shown?.preconditions.os ?? inventory.os) });
		if (running) return t("lado.setup.adopt.closeRunning");
		return null;
	};
	const reason = outcome ? null : hold();
	const busy = submitting || adopt.isPending;

	const adoptNow = () => {
		if (!shown || reason || busy) return;
		setSubmitting(true);
		setAdoptError(null);
		adopt
			.mutateAsync({ entries: settled, planSha256: shown.planSha256 })
			.then((result) => {
				// null: the developer cancelled the desktop confirmation.
				if (result) setOutcome({ kind: "adopted", result, entries: settled });
			})
			.catch((error: unknown) => {
				// A partial adoption moved some setup: restore is the way back, not another try.
				if (error instanceof LadoApiError && error.details?.state === "partial") setOutcome({ kind: "stopped", error });
				else setAdoptError(error);
			})
			.finally(() => setSubmitting(false));
	};

	const close = () => {
		if (!busy) onOpenChange(false);
	};

	const allSelected = candidates.length > 0 && candidates.every((entry) => choiceOf(entry).selected);
	const someSelected = candidates.some((entry) => choiceOf(entry).selected);

	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				if (!next && busy) return;
				onOpenChange(next);
			}}
		>
			<DialogContent
				showCloseButton={false}
				aria-describedby={undefined}
				className={settingsDialogContentClass}
				data-testid="lado-setup-adopt"
				onOpenAutoFocus={(event) => {
					if (!firstRef.current) return;
					event.preventDefault();
					firstRef.current.focus();
				}}
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<DialogClose asChild>
					<button
						type="button"
						disabled={busy}
						className="settings-dialog-close-button settings-close-button"
						aria-label={t("common.close")}
					>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("lado.setup.adopt.title")}</DialogTitle>
				</div>
				{outcome?.kind === "adopted" ? (
					<AdoptedReport outcome={outcome} />
				) : outcome?.kind === "stopped" ? (
					<StoppedReport error={outcome.error} inventory={inventory} />
				) : (
					// The body scrolls; a fieldset does not, so it only disables the choices while adopting.
					<div className={cn(settingsDialogBodyClass, "min-w-0 px-5 pt-0 pb-4")}>
						<fieldset disabled={busy} className="flex min-w-0 flex-col gap-(--size-settings-section-inner-gap)">
							{moves > 0 ? <p className="text-control leading-5 text-settings-label">{t("lado.setup.adopt.warning")}</p> : null}
							<Part title={t("lado.setup.adopt.items")}>
								<ul className="settings-grouped-rows flex w-full flex-col" data-testid="lado-setup-adopt-items">
									<li className="flex items-center gap-(--size-settings-row-icon-gap) px-(--settings-row-inset) py-2.5">
										<Checkbox
											ref={firstRef}
											aria-labelledby={allId}
											checked={allSelected ? true : someSelected ? "indeterminate" : false}
											onCheckedChange={(checked) =>
												setChosen(() =>
													Object.fromEntries(
														candidates.map((entry) => [ladoSetupEntryKey(entry), { ...choiceOf(entry), selected: checked === true }]),
													),
												)
											}
										/>
										<span id={allId} className="text-sm text-settings-label">
											{t("lado.setup.adopt.all")}
										</span>
									</li>
									{candidates.map((entry) => (
										<CandidateRow
											key={ladoSetupEntryKey(entry)}
											entry={entry}
											choice={choiceOf(entry)}
											origin={ladoSetupShortPath(entry.path, inventory)}
											destination={managedPaths.get(ladoSetupEntryKey(entry))}
											inventory={inventory}
											onChange={(change) => choose(entry, change)}
										/>
									))}
								</ul>
							</Part>
							<CodexInstructionsNote candidates={candidates} requested={requested} />
							{preview.isError ? (
								<div className="flex items-center justify-between gap-3">
									<p role="alert" className="text-caption leading-4 text-error">
										{ladoErrorText(t, preview.error)}
									</p>
									<CheckAgain onClick={() => void preview.refetch()} />
								</div>
							) : null}
							{shown && shown.rewrites.length > 0 ? (
								<Part title={t("lado.setup.adopt.rewrites")}>
									<ul className="settings-grouped-rows flex w-full flex-col" data-testid="lado-setup-adopt-rewrites">
										{shown.rewrites.map((rewrite, index) => (
											<li key={`${rewrite.setting}-${rewrite.event}-${rewrite.matcher}-${index}`} className="settings-content-row flex flex-col gap-1">
												<span className="text-sm text-settings-label">
													{[rewrite.setting, rewrite.event, rewrite.matcher].filter(Boolean).join(" · ")}
												</span>
												<Command label={t("lado.setup.adopt.before")} command={rewrite.before} />
												<Command label={t("lado.setup.adopt.after")} command={rewrite.after} />
											</li>
										))}
									</ul>
								</Part>
							) : null}
							{conflicts.length > 0 ? (
								<Part title={t("lado.setup.adopt.conflicts")}>
									<ul className="settings-grouped-rows flex w-full flex-col" data-testid="lado-setup-adopt-conflicts">
										{conflicts.map((conflict, index) => (
											<PreviewConflict key={`${conflict.code}-${conflict.path}-${conflict.detail}-${index}`} conflict={conflict} inventory={inventory} />
										))}
									</ul>
								</Part>
							) : null}
							{shown && running ? (
								<Part
									title={t("lado.setup.adopt.running")}
									action={<CheckAgain onClick={() => void preview.refetch()} />}
								>
									<ul className="settings-grouped-rows flex w-full flex-col" data-testid="lado-setup-adopt-running">
										{shown.preconditions.sessions.map((session) => (
											<li key={`session-${session.id}`} className="settings-row-bar">
												<span className="min-w-0 truncate text-sm text-settings-label">{t("lado.setup.adopt.session", { id: session.id })}</span>
												<span className="shrink-0 text-xs text-settings-muted">{agentLabel(session.tool)}</span>
											</li>
										))}
										{shown.preconditions.processes.map((program) => (
											<li key={`process-${program.pid}`} className="settings-row-bar">
												<span className="min-w-0 truncate text-sm text-settings-label">
													{t("lado.setup.adopt.process", { name: program.name, pid: program.pid })}
												</span>
												<span className="shrink-0 text-xs text-settings-muted">{agentLabel(program.tool)}</span>
											</li>
										))}
									</ul>
								</Part>
							) : null}
							{shown?.preconditions.processesIgnored ? (
								<p className="text-caption leading-4 text-settings-muted">{t("lado.setup.adopt.processesIgnored")}</p>
							) : null}
							{(shown?.warnings ?? [])
								.filter((code) => code !== DIRECT_USE_STOPS)
								.map((code) => (
									<p key={code} className="text-caption leading-4 text-[var(--color-status-in-review)]">
										{ladoSetupWarningText(t, code)}
									</p>
								))}
						</fieldset>
					</div>
				)}
				<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
					{outcome ? (
						<Button type="button" variant="footer-primary" className={compactButtonClass} onClick={close}>
							{t("lado.setup.adopt.done")}
						</Button>
					) : (
						<>
							{adoptError ? (
								<p role="alert" className="mr-auto text-caption leading-4 text-error">
									{ladoErrorText(t, adoptError)}
								</p>
							) : reason ? (
								<p role="status" className="mr-auto text-caption leading-4 text-settings-muted" data-testid="lado-setup-adopt-hold">
									{reason}
								</p>
							) : null}
							<Button type="button" variant="footer" className={compactButtonClass} disabled={busy} onClick={close}>
								{t("confirm.cancel")}
							</Button>
							<Button
								type="button"
								variant="footer-primary"
								className={compactButtonClass}
								disabled={busy || reason !== null}
								onClick={adoptNow}
							>
								{busy ? t("lado.setup.adopt.adopting") : t("lado.setup.adopt.confirm", { count: requested.length })}
							</Button>
						</>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}

/** A part of the dialog's body under its own quiet heading. */
function Part({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
	const headingId = useId();
	return (
		<section aria-labelledby={headingId} className="flex flex-col gap-2">
			<div className="flex min-h-5 items-center justify-between gap-3">
				<h3 id={headingId} className="text-sm font-medium leading-5 text-settings-label">
					{title}
				</h3>
				{action}
			</div>
			{children}
		</section>
	);
}

function CheckAgain({ onClick }: { onClick: () => void }) {
	const { t } = useTranslation();
	return (
		<Button type="button" variant="ghost" size="sm" className="shrink-0 text-settings-muted hover:text-settings-label" onClick={onClick}>
			<span className="text-control">{t("lado.setup.adopt.checkAgain")}</span>
		</Button>
	);
}

/** One adoptable entry: chosen or not, and whether it moves or stays in place. */
function CandidateRow({
	entry,
	choice,
	origin,
	destination,
	inventory,
	onChange,
}: {
	entry: LadoSetupEntry;
	choice: Choice;
	origin: string;
	destination: string | undefined;
	inventory: LadoSetupInventory;
	onChange: (change: Partial<Choice>) => void;
}) {
	const { t } = useTranslation();
	const nameId = useId();
	return (
		<li data-testid="lado-setup-adopt-item" data-name={entry.name} data-tool={entry.tool}>
			<SettingsRow
				leading={
					<Checkbox
						aria-labelledby={nameId}
						checked={choice.selected}
						onCheckedChange={(checked) => onChange({ selected: checked === true })}
					/>
				}
				label={<span id={nameId}>{entry.name}</span>}
				description={
					<>
						<span className="block">{[agentLabel(entry.tool), ladoSetupKindLabel(t, entry.kind)].join(" · ")}</span>
						<span className="block truncate font-mono text-xs" title={entry.path}>
							{origin}
						</span>
						{choice.selected ? (
							choice.action === "move" ? (
								destination ? (
									<span className="block truncate text-xs" title={destination}>
										{t("lado.setup.adopt.movesTo")} <span className="font-mono">{ladoSetupShortPath(destination, inventory)}</span>
									</span>
								) : null
							) : (
								<span className="block text-xs">{t("lado.setup.adopt.keeps")}</span>
							)
						) : null}
					</>
				}
			>
				<SettingsOptionMenu<Action>
					aria-label={t("lado.setup.adopt.action", { name: entry.name })}
					value={choice.action}
					options={[
						{ value: "move", label: t("lado.setup.adopt.move") },
						{ value: "keep", label: t("lado.setup.adopt.keep") },
					]}
					disabled={!choice.selected}
					triggerClassName="w-fit"
					onChange={(action) => onChange({ action })}
				/>
			</SettingsRow>
		</li>
	);
}

function Command({ label, command }: { label: string; command: string }) {
	return (
		<span className="flex min-w-0 items-baseline gap-2 text-xs">
			<span className="w-12 shrink-0 text-settings-muted">{label}</span>
			<code className="min-w-0 break-all font-mono text-settings-label">{command}</code>
		</span>
	);
}

/** A conflict the adoption brings. Only a moved file it cannot rewrite a reference to stops it. */
function PreviewConflict({ conflict, inventory }: { conflict: LadoSetupConflict; inventory: LadoSetupInventory }) {
	const { t } = useTranslation();
	const stops = STOPPING_CONFLICTS.has(conflict.code);
	// Another blocking conflict blocks LADO tasks after the adoption as it does now.
	const tone = stops ? "blocking" : conflict.severity === "blocking" ? "warning" : conflict.severity;
	return (
		<li
			className="settings-content-row flex min-w-0 items-baseline gap-2 text-caption leading-4"
			data-testid="lado-setup-adopt-conflict"
			data-code={conflict.code}
			data-severity={conflict.severity}
			data-tone={tone}
		>
			<span className={cn("size-1.5 shrink-0 translate-y-[-1px] rounded-full", ladoSetupSeverityDotClass[tone])} aria-hidden="true" />
			<span className="min-w-0 text-settings-label">
				<span className="font-medium">{stops ? t("lado.setup.adopt.stopsAdoption") : t(ladoSetupSeverityKeys[conflict.severity])}</span>
				<span className="text-settings-muted"> · </span>
				{ladoSetupConflictText(t, conflict)}
				{conflict.path ? (
					<span className="block truncate font-mono text-settings-muted" title={conflict.path}>
						{ladoSetupShortPath(conflict.path, inventory)}
					</span>
				) : null}
			</span>
		</li>
	);
}

/**
 * Codex reads its own folder's instructions file as well as LADO's: adopting
 * only one of AGENTS.md and AGENTS.override.md gives sessions both, and
 * keeping one in place gives them its text twice.
 */
function CodexInstructionsNote({ candidates, requested }: { candidates: LadoSetupEntry[]; requested: LadoSetupAdoptEntry[] }) {
	const { t } = useTranslation();
	const codexInstructions = (entry: { tool: string; kind: string; name: string }) =>
		entry.tool === "codex" && entry.kind === "instructions" && CODEX_INSTRUCTIONS.includes(entry.name);
	const both = candidates.filter(codexInstructions).length === CODEX_INSTRUCTIONS.length;
	const moved = requested.filter((entry) => codexInstructions(entry) && entry.action === "move").length;
	const kept = requested.some((entry) => codexInstructions(entry) && entry.action === "keep");
	const notes = [
		...(both && moved === 1 ? [t("lado.setup.adopt.codexPair")] : []),
		...(kept ? [t("lado.setup.adopt.codexKeep")] : []),
	];
	if (notes.length === 0) return null;
	return (
		<div className="flex flex-col gap-1" data-testid="lado-setup-adopt-codex-note">
			{notes.map((note) => (
				<p key={note} className="text-caption leading-4 text-[var(--color-status-in-review)]">
					{note}
				</p>
			))}
		</div>
	);
}

/** What an adoption did: how many items moved or stayed, and the checks that found them where sessions get them. */
function AdoptedReport({ outcome }: { outcome: Extract<Outcome, { kind: "adopted" }> }) {
	const { t } = useTranslation();
	const { result, entries } = outcome;
	const names = new Map(result.items.map((item) => [item.itemId, item.name]));
	const failed = result.verification.filter((check) => !check.ok);
	const moved = entries.filter((entry) => entry.action === "move").length;
	return (
		<div className={cn(settingsDialogBodyClass, "min-w-0 gap-3 px-5 pt-0 pb-4")} data-testid="lado-setup-adopted">
			<p role="status" className="text-sm text-settings-label">
				{t("lado.setup.adopt.adopted", { count: result.items.length })}
			</p>
			<p className="text-control leading-5 text-settings-muted">
				{t("lado.setup.adopt.summary", { moved, kept: entries.length - moved })}
			</p>
			{failed.length === 0 ? (
				<p className="text-control leading-5 text-settings-muted">{t("lado.setup.adopt.verified")}</p>
			) : (
				<ul className="flex flex-col gap-1" data-testid="lado-setup-adopt-failed-checks">
					{failed.map((check, index) => (
						<li key={`${check.itemId}-${check.check}-${index}`} className="text-caption leading-4 text-error">
							{t("lado.setup.adopt.checkFailed", {
								check: checkKeys[check.check] ? t(checkKeys[check.check] as MessageKey) : check.check,
								name: names.get(check.itemId) ?? check.itemId,
								error: check.error,
							})}
						</li>
					))}
				</ul>
			)}
			{result.warnings
				.filter((code) => code !== DIRECT_USE_STOPS)
				.map((code) => (
					<p key={code} className="text-caption leading-4 text-[var(--color-status-in-review)]">
						{ladoSetupWarningText(t, code)}
					</p>
				))}
		</div>
	);
}

/** An adoption that stopped while removing the originals: what moved stays managed, and its stage and path say where it stopped. */
function StoppedReport({ error, inventory }: { error: LadoApiError; inventory: LadoSetupInventory }) {
	const { t } = useTranslation();
	const detail = (name: string) => {
		const value = error.details?.[name];
		return typeof value === "string" ? value : "";
	};
	const stage = stageKeys[detail("stage")];
	const root = detail("root");
	const relative = detail("path");
	const path =
		relative && (root === "claude_dir" || root === "claude_json" || root === "codex_home")
			? ladoSetupRootedPath({ root }, relative, inventory)
			: relative
				? { short: relative, full: relative }
				: null;
	return (
		<div className={cn(settingsDialogBodyClass, "min-w-0 gap-3 px-5 pt-0 pb-4")} data-testid="lado-setup-adopt-stopped">
			<p role="alert" className="text-control leading-5 text-error">
				{ladoErrorText(t, error)}
			</p>
			{stage ? (
				<p className="text-control leading-5 text-settings-muted">
					{t("lado.setup.adopt.stoppedAt", { stage: t(stage) })}
					{path ? (
						<span className="block truncate font-mono text-xs" title={path.full}>
							{path.short}
						</span>
					) : null}
				</p>
			) : null}
		</div>
	);
}
