// LADO fork: the patterns by which LADO finds a draft's risky changes from
// its affected paths (docs/LJY-PRD/TDD.md §5.1): one list per change kind,
// one path or pattern per line. A plan whose affected paths a pattern matches
// is reviewed, however few they are. A line LADO would refuse is named before
// anything is sent; saving keeps the other run settings as they are, and
// asks for no confirmation, like them.
import { X } from "lucide-react";
import { useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useUpdateLadoDispatchSettings } from "../../hooks/useLado";
import { useRestoreFocusOnClose } from "../../hooks/useRestoreFocusOnClose";
import { LADO_CHANGE_KINDS, type LadoChangeKind, type LadoDispatchSettings } from "../../lib/lado";
import { ladoChangeKindLabel, ladoErrorText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
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
import { ladoValidAffectedPath } from "./LadoProtectedPathsDialog";

/** The most patterns a change kind holds (domain.LadoProtectedPathsMax). */
const MAX_PATTERNS = 50;

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

type Texts = Record<LadoChangeKind, string>;

/** Each non-blank line, without surrounding space, with its line number. */
function patternLines(text: string): { line: number; glob: string }[] {
	return text
		.split(/\r?\n/)
		.map((source, index) => ({ line: index + 1, glob: source.trim() }))
		.filter((entry) => entry.glob !== "");
}

function textsOf(settings: LadoDispatchSettings): Texts {
	const patterns = settings.planReview.patterns ?? {};
	return Object.fromEntries(LADO_CHANGE_KINDS.map((kind) => [kind, (patterns[kind] ?? []).join("\n")])) as Texts;
}

/**
 * Open while open is true; mount it again for each opening so it starts from
 * the saved settings.
 */
export function LadoPlanReviewPatternsDialog({
	open,
	settings,
	onOpenChange,
}: {
	open: boolean;
	settings: LadoDispatchSettings;
	onOpenChange: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const update = useUpdateLadoDispatchSettings();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const firstRef = useRef<HTMLTextAreaElement>(null);
	const [texts, setTexts] = useState<Texts>(() => textsOf(settings));
	const baseId = useId();
	const hintId = useId();

	const saved = textsOf(settings);
	const lines = Object.fromEntries(LADO_CHANGE_KINDS.map((kind) => [kind, patternLines(texts[kind])])) as Record<
		LadoChangeKind,
		{ line: number; glob: string }[]
	>;
	const dirty = LADO_CHANGE_KINDS.some((kind) => {
		const before = patternLines(saved[kind]).map((entry) => entry.glob);
		const after = lines[kind].map((entry) => entry.glob);
		return before.length !== after.length || before.some((glob, index) => glob !== after[index]);
	});
	const problemsOf = (kind: LadoChangeKind) => {
		const label = ladoChangeKindLabel(t, kind);
		return [
			...lines[kind].filter((entry) => !ladoValidAffectedPath(entry.glob)).map((entry) => t("lado.reviewPatterns.line", { kind: label, line: entry.line })),
			...(lines[kind].length > MAX_PATTERNS ? [t("lado.reviewPatterns.tooMany", { kind: label })] : []),
		];
	};
	const invalid = LADO_CHANGE_KINDS.some((kind) => problemsOf(kind).length > 0);
	const busy = update.isPending;

	const close = () => onOpenChange(false);
	const submit = (event: FormEvent) => {
		event.preventDefault();
		if (busy || !dirty || invalid) return;
		const patterns = Object.fromEntries(LADO_CHANGE_KINDS.map((kind) => [kind, lines[kind].map((entry) => entry.glob)]));
		update.mutate(
			{
				maxConcurrent: settings.maxConcurrent,
				maxRunMinutes: settings.maxRunMinutes,
				holdOnMemoryTight: settings.holdOnMemoryTight,
				maxPlanWorkers: settings.maxPlanWorkers,
				planReview: { ...settings.planReview, patterns },
				maxWorkers: settings.maxWorkers,
				holdOnDiskLow: settings.holdOnDiskLow,
				minFreeDiskGb: settings.minFreeDiskGb,
			},
			{ onSuccess: close },
		);
	};

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
				data-testid="lado-review-patterns"
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					firstRef.current?.focus();
				}}
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<DialogClose asChild>
					<button type="button" disabled={busy} className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("lado.runs.reviewPatterns")}</DialogTitle>
				</div>
				<form className="flex min-h-0 flex-1 flex-col" onSubmit={submit}>
					{/* The div scrolls; a fieldset as the scroll container spills under the footer. */}
					<div className={cn(settingsDialogBodyClass, "min-w-0 gap-(--size-settings-section-inner-gap) px-5 pt-0 pb-4")}>
						<p id={hintId} className="text-caption leading-4 text-settings-muted">
							{t("lado.reviewPatterns.hint")}
						</p>
						{LADO_CHANGE_KINDS.map((kind, index) => {
							const fieldId = `${baseId}-${kind}`;
							const problemsId = `${fieldId}-problems`;
							const problems = problemsOf(kind);
							return (
								<div key={kind} className="flex flex-col gap-1.5">
									<label htmlFor={fieldId} className="settings-field-label">
										{ladoChangeKindLabel(t, kind)}
									</label>
									{/* The textarea takes its font from here (the app's form reset). */}
									<div className="font-mono">
										<textarea
											ref={index === 0 ? firstRef : undefined}
											id={fieldId}
											value={texts[kind]}
											rows={4}
											spellCheck={false}
											autoComplete="off"
											disabled={busy}
											aria-invalid={problems.length > 0 ? true : undefined}
											aria-describedby={problems.length > 0 ? `${hintId} ${problemsId}` : hintId}
											onChange={(event) => {
												const value = event.target.value;
												setTexts((current) => ({ ...current, [kind]: value }));
												if (update.error) update.reset();
											}}
											className="settings-field-control min-h-(--size-textarea-min) resize-none overflow-y-auto rounded-md! py-2.5"
											data-testid={`lado-review-patterns-${kind}`}
										/>
									</div>
									{problems.length > 0 ? (
										<ul id={problemsId} className="flex flex-col gap-0.5">
											{problems.map((problem) => (
												<li key={problem} className="text-caption leading-4 text-error">
													{problem}
												</li>
											))}
										</ul>
									) : null}
								</div>
							);
						})}
						{update.error ? (
							<p role="alert" className="text-caption leading-4 text-error">
								{ladoErrorText(t, update.error)}
							</p>
						) : null}
					</div>
					<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
						<Button type="button" variant="footer" className={compactButtonClass} disabled={busy} onClick={close}>
							{t("confirm.cancel")}
						</Button>
						<Button type="submit" variant="footer-primary" className={compactButtonClass} disabled={busy || !dirty || invalid}>
							{busy ? t("lado.rules.editor.saving") : t("lado.rules.editor.save")}
						</Button>
					</div>
				</form>
			</DialogContent>
		</Dialog>
	);
}
