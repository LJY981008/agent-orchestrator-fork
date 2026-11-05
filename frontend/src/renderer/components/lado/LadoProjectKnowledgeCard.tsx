// LADO fork: Project settings → LADO → Project knowledge (issue #24,
// docs/LJY-PRD/adr/0010-project-knowledge-store.md). The project's knowledge
// folder, the notes LADO sessions wrote there and whether each still matches
// the base branch. A folder another repository wrote is not read or written
// until the developer takes it over. Notes come in from Claude Code's memory
// of a project path or from another knowledge folder, an archived project's
// too; the source is only read.
import { Check, Copy, X } from "lucide-react";
import { useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useClaimLadoKnowledge, useImportLadoKnowledge, useLadoKnowledge } from "../../hooks/useLado";
import { useRestoreFocusOnClose } from "../../hooks/useRestoreFocusOnClose";
import type { MessageKey } from "../../i18n/messages";
import { aoBridge } from "../../lib/bridge";
import type { LadoKnowledge, LadoKnowledgeImportResult, LadoKnowledgeNote, LadoKnowledgeNoteState } from "../../lib/lado";
import { ladoErrorText, ladoKnowledgeSkipText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
import { ConfirmDialog } from "../ConfirmDialog";
import { SettingsOptionMenu, type SettingsOption } from "../settings/SettingsOptionMenu";
import { SettingsRow } from "../settings/SettingsRow";
import { SettingsSection } from "../settings/SettingsSection";
import { Button } from "../ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { Input } from "../ui/input";

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

/** How many notes the card lists before "Show all". */
const NOTES_SHOWN = 10;

type ImportSource = "claude_memory" | "project";

const kindKeys: Partial<Record<string, MessageKey>> = {
	map: "lado.knowledge.kind.map",
	decision: "lado.knowledge.kind.decision",
	pitfall: "lado.knowledge.kind.pitfall",
	research: "lado.knowledge.kind.research",
	convention: "lado.knowledge.kind.convention",
};

const stateKeys: Record<LadoKnowledgeNoteState, MessageKey> = {
	fresh: "lado.knowledge.state.fresh",
	stale: "lado.knowledge.state.stale",
	unchecked: "lado.knowledge.state.unchecked",
};

/** Notes to check first, then by slug as the daemon sorts them. */
function orderedNotes(notes: readonly LadoKnowledgeNote[]): LadoKnowledgeNote[] {
	return [...notes].sort((a, b) => Number(b.state === "stale") - Number(a.state === "stale"));
}

export function LadoProjectKnowledgeCard({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const knowledge = useLadoKnowledge(projectId);
	const importer = useImportLadoKnowledge(projectId);
	// Each opening mounts the dialog again, empty.
	const [dialog, setDialog] = useState<{ key: number; source: ImportSource | null }>({ key: 0, source: null });
	const [result, setResult] = useState<LadoKnowledgeImportResult | null>(null);
	const data = knowledge.data;
	const stale = data?.notes.filter((note) => note.state === "stale").length ?? 0;
	const openImport = (source: ImportSource) => {
		importer.reset();
		setResult(null);
		setDialog((current) => ({ key: current.key + 1, source }));
	};

	return (
		<div className="flex w-full flex-col gap-(--size-settings-section-gap)" data-testid="lado-project-knowledge">
			<SettingsSection
				title={t("lado.knowledge.title")}
				grouped={Boolean(data)}
				action={
					data && !data.foreign ? (
						<span className="text-xs tabular-nums text-settings-muted" data-testid="lado-knowledge-counts">
							{t("lado.knowledge.noteCount", { count: data.notes.length })}
							{stale > 0 ? ` · ${t("lado.knowledge.staleCount", { count: stale })}` : null}
						</span>
					) : undefined
				}
			>
				{knowledge.isPending ? (
					<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
						{t("common.loading")}
					</p>
				) : !data ? (
					<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
						{ladoErrorText(t, knowledge.error)}
					</p>
				) : (
					<KnowledgeRows projectId={projectId} knowledge={data} />
				)}
			</SettingsSection>
			{data && !data.foreign ? (
				<div className="flex w-full flex-col gap-(--size-settings-section-inner-gap)">
					<SettingsSection title={t("lado.knowledge.importTitle")} grouped testId="lado-knowledge-import">
						<SettingsRow label={t("lado.knowledge.importClaude")} description={t("lado.knowledge.importClaudeHint")}>
							<ImportButton label={t("lado.knowledge.importClaude")} disabled={importer.isPending} onClick={() => openImport("claude_memory")} />
						</SettingsRow>
						<SettingsRow
							label={t("lado.knowledge.importProject")}
							description={data.importable.length > 0 ? t("lado.knowledge.importProjectHint") : t("lado.knowledge.importProjectNone")}
						>
							<ImportButton
								label={t("lado.knowledge.importProject")}
								disabled={importer.isPending || data.importable.length === 0}
								onClick={() => openImport("project")}
							/>
						</SettingsRow>
					</SettingsSection>
					{result ? <ImportReport result={result} /> : null}
				</div>
			) : null}
			{data ? (
				<KnowledgeImportDialog
					key={`import-${dialog.key}`}
					source={dialog.source}
					knowledge={data}
					importer={importer}
					onDone={setResult}
					onClose={() => setDialog((current) => ({ ...current, source: null }))}
				/>
			) : null}
		</div>
	);
}

function ImportButton({ label, disabled, onClick }: { label: string; disabled: boolean; onClick: () => void }) {
	const { t } = useTranslation();
	return (
		<Button
			type="button"
			variant="ghost"
			size="sm"
			aria-label={label}
			className="text-settings-muted hover:text-settings-label"
			disabled={disabled}
			onClick={onClick}
		>
			<span className="text-sm">{t("lado.knowledge.importAction")}</span>
		</Button>
	);
}

function KnowledgeRows({ projectId, knowledge }: { projectId: string; knowledge: LadoKnowledge }) {
	const { t } = useTranslation();
	const [showAll, setShowAll] = useState(false);
	const notes = orderedNotes(knowledge.notes);
	const shown = showAll ? notes : notes.slice(0, NOTES_SHOWN);
	return (
		<>
			<SettingsRow
				label={t("lado.knowledge.folder")}
				description={
					<>
						<span className="block break-all font-mono" data-testid="lado-knowledge-dir">
							{knowledge.dir}
						</span>
						{knowledge.truncated && !knowledge.foreign ? <span className="block">{t("lado.knowledge.truncated")}</span> : null}
					</>
				}
			>
				<CopyPathButton path={knowledge.dir} />
			</SettingsRow>
			{knowledge.foreign ? (
				<ForeignRow projectId={projectId} knowledge={knowledge} />
			) : notes.length === 0 ? (
				<SettingsRow label={t("lado.knowledge.empty")} description={t("lado.knowledge.emptyHint")} />
			) : (
				<>
					{shown.map((note) => (
						<NoteRow key={note.slug} note={note} />
					))}
					{notes.length > shown.length ? (
						<SettingsRow label={t("lado.knowledge.moreNotes", { count: notes.length - shown.length })}>
							<Button
								type="button"
								variant="ghost"
								size="sm"
								className="text-settings-muted hover:text-settings-label"
								onClick={() => setShowAll(true)}
							>
								<span className="text-sm">{t("lado.knowledge.showAll")}</span>
							</Button>
						</SettingsRow>
					) : null}
				</>
			)}
		</>
	);
}

function CopyPathButton({ path }: { path: string }) {
	const { t } = useTranslation();
	const [copied, setCopied] = useState(false);
	const copy = async () => {
		await aoBridge.clipboard.writeText(path);
		setCopied(true);
		window.setTimeout(() => setCopied(false), 1_500);
	};
	return (
		<Button
			type="button"
			variant="ghost"
			size="sm"
			className="text-settings-muted hover:text-settings-label"
			aria-label={t("lado.knowledge.copyPath")}
			onClick={() => void copy()}
		>
			{copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
			<span className="text-sm">{copied ? t("lado.knowledge.copied") : t("lado.knowledge.copy")}</span>
		</Button>
	);
}

function NoteRow({ note }: { note: LadoKnowledgeNote }) {
	const { t } = useTranslation();
	const kindKey = kindKeys[note.kind];
	return (
		<SettingsRow
			data-testid="lado-knowledge-note"
			data-state={note.state}
			label={note.title || note.slug}
			description={
				<>
					<span className="font-mono">{note.slug}</span>
					{" · "}
					{kindKey ? t(kindKey) : note.kind}
					{" · "}
					<span className="tabular-nums">{t("lado.knowledge.revision", { revision: note.revision })}</span>
				</>
			}
		>
			<span className="flex items-center gap-1.5 text-sm text-settings-label">
				{note.state === "unchecked" ? null : (
					<span
						aria-hidden="true"
						className={cn(
							"size-1.5 shrink-0 rounded-full",
							note.state === "stale" ? "bg-[var(--color-status-in-review)]" : "bg-[var(--color-status-ready)]",
						)}
					/>
				)}
				<span className={cn(note.state === "unchecked" && "text-settings-muted")}>{t(stateKeys[note.state])}</span>
			</span>
		</SettingsRow>
	);
}

function ForeignRow({ projectId, knowledge }: { projectId: string; knowledge: LadoKnowledge }) {
	const { t } = useTranslation();
	const claim = useClaimLadoKnowledge(projectId);
	const [confirming, setConfirming] = useState(false);
	const ownerPath = knowledge.owner?.path ?? "";
	return (
		<>
			<SettingsRow
				data-testid="lado-knowledge-foreign"
				label={
					<span className="flex items-center gap-1.5">
						<span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-[var(--color-status-in-review)]" />
						{t("lado.knowledge.foreign")}
					</span>
				}
				description={
					<>
						<span className="block">{t("lado.knowledge.foreignHint")}</span>
						<span className="block">
							{t("lado.knowledge.foreignOwner")} <span className="break-all font-mono">{ownerPath}</span>
						</span>
					</>
				}
			>
				<Button
					type="button"
					variant="ghost"
					size="sm"
					className="text-settings-muted hover:text-settings-label"
					disabled={claim.isPending}
					onClick={() => {
						claim.reset();
						setConfirming(true);
					}}
				>
					<span className="text-sm">{t("lado.knowledge.claim")}</span>
				</Button>
			</SettingsRow>
			<ConfirmDialog
				open={confirming}
				title={t("lado.knowledge.claimTitle")}
				description={t("lado.knowledge.claimBody", { path: ownerPath })}
				confirmLabel={t("lado.knowledge.claimConfirm")}
				busy={claim.isPending}
				error={claim.isError ? ladoErrorText(t, claim.error) : null}
				onConfirm={() => claim.mutate(undefined, { onSuccess: () => setConfirming(false) })}
				onOpenChange={setConfirming}
			/>
		</>
	);
}

function ImportReport({ result }: { result: LadoKnowledgeImportResult }) {
	const { t } = useTranslation();
	return (
		<div
			role="status"
			className="flex flex-col gap-1 px-(--settings-row-inset) text-control leading-4 text-settings-muted"
			data-testid="lado-knowledge-import-result"
		>
			<p>
				{result.imported.length > 0
					? t("lado.knowledge.imported", { count: result.imported.length, notes: result.imported.join(", ") })
					: t("lado.knowledge.importedNone")}
			</p>
			{result.skipped.length > 0 ? (
				<>
					<p>{t("lado.knowledge.skipped", { count: result.skipped.length })}</p>
					<ul className="flex flex-col gap-0.5">
						{result.skipped.map((skip) => (
							<li key={`${skip.name}\n${skip.reason}`} data-testid="lado-knowledge-skip">
								<span className="break-all font-mono">{skip.name}</span> — {ladoKnowledgeSkipText(t, skip.reason)}
							</li>
						))}
					</ul>
				</>
			) : null}
		</div>
	);
}

function KnowledgeImportDialog({
	source,
	knowledge,
	importer,
	onDone,
	onClose,
}: {
	source: ImportSource | null;
	knowledge: LadoKnowledge;
	importer: ReturnType<typeof useImportLadoKnowledge>;
	onDone: (result: LadoKnowledgeImportResult) => void;
	onClose: () => void;
}) {
	const { t } = useTranslation();
	const fieldId = useId();
	const hintId = useId();
	const [path, setPath] = useState("");
	const [fromProjectId, setFromProjectId] = useState(knowledge.importable[0]?.projectId ?? "");
	const busy = importer.isPending;
	const open = source !== null;
	const pathRef = useRef<HTMLInputElement>(null);
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const from = knowledge.importable.find((item) => item.projectId === fromProjectId);
	const options: SettingsOption<string>[] = knowledge.importable.map((item) => ({
		value: item.projectId,
		label: `${item.projectId} · ${t("lado.knowledge.noteCount", { count: item.notes })}`,
		group: item.registered ? t("lado.knowledge.registered") : t("lado.knowledge.notRegistered"),
	}));
	const title = source === "project" ? t("lado.knowledge.importProject") : t("lado.knowledge.importClaude");
	const submit = (event: FormEvent) => {
		event.preventDefault();
		if (busy || !source) return;
		if (source === "project" && !from) return;
		const input = source === "project" ? { source, fromProjectId } : { source, path };
		importer.mutate(input, {
			onSuccess: (result) => {
				onDone(result);
				onClose();
			},
		});
	};
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				if (!next && !busy) onClose();
			}}
		>
			<DialogContent
				showCloseButton={false}
				aria-describedby={hintId}
				className={settingsDialogContentClass}
				data-testid="lado-knowledge-import-dialog"
				onOpenAutoFocus={(event) => {
					// The path is the one field to fill; the folder dialog keeps Radix's first focus.
					if (source !== "claude_memory") return;
					event.preventDefault();
					pathRef.current?.focus();
				}}
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<DialogClose asChild>
					<button type="button" disabled={busy} className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{title}</DialogTitle>
				</div>
				<form className="flex min-h-0 flex-1 flex-col" onSubmit={submit}>
					<div className={cn(settingsDialogBodyClass, "min-w-0 gap-1.5 px-5 pt-0 pb-4")}>
						{source === "project" ? (
							<>
								<span id={fieldId} className="settings-field-label">
									{t("lado.knowledge.importFrom")}
								</span>
								<p id={hintId} className="text-caption leading-4 text-settings-muted">
									{t("lado.knowledge.importProjectDialogHint")}
								</p>
								<SettingsOptionMenu
									aria-label={t("lado.knowledge.importFrom")}
									value={fromProjectId}
									options={options}
									disabled={busy}
									menuAlign="start"
									onChange={(value) => {
										setFromProjectId(value);
										if (importer.error) importer.reset();
									}}
								/>
								{from?.ownerPath ? (
									<p className="text-caption leading-4 text-settings-muted" data-testid="lado-knowledge-import-owner">
										{t("lado.knowledge.foreignOwner")} <span className="break-all font-mono">{from.ownerPath}</span>
									</p>
								) : null}
							</>
						) : (
							<>
								<label htmlFor={fieldId} className="settings-field-label">
									{t("lado.knowledge.claudePath")}
								</label>
								<p id={hintId} className="text-caption leading-4 text-settings-muted">
									{t("lado.knowledge.claudePathHint")}
								</p>
								<div className="font-mono">
									<Input
										ref={pathRef}
										id={fieldId}
										value={path}
										spellCheck={false}
										autoComplete="off"
										disabled={busy}
										placeholder={t("lado.knowledge.claudePathPlaceholder")}
										aria-describedby={hintId}
										onChange={(event) => {
											setPath(event.target.value);
											if (importer.error) importer.reset();
										}}
										className="settings-field-control"
									/>
								</div>
							</>
						)}
						{importer.isError ? (
							<p role="alert" className="text-caption leading-4 text-error">
								{ladoErrorText(t, importer.error)}
							</p>
						) : null}
					</div>
					<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
						<Button type="button" variant="footer" className={compactButtonClass} disabled={busy} onClick={onClose}>
							{t("confirm.cancel")}
						</Button>
						<Button
							type="submit"
							variant="footer-primary"
							className={compactButtonClass}
							disabled={busy || (source === "project" && !from)}
						>
							{busy ? t("lado.knowledge.importing") : t("lado.knowledge.importAction")}
						</Button>
					</div>
				</form>
			</DialogContent>
		</Dialog>
	);
}
