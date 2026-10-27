// LADO fork: editing the protected paths (docs/LJY-PRD/HARNESS.md §14). One
// path or pattern per line; blank lines do not count. A line LADO would
// refuse is named with its problem before anything is sent. Saving goes
// through the desktop app's confirmation, which names the paths added and
// removed; a cancelled confirmation keeps the edits.
import { X } from "lucide-react";
import { useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useChangeLadoProtectedPaths } from "../../hooks/useLado";
import { useRestoreFocusOnClose } from "../../hooks/useRestoreFocusOnClose";
import { LadoApiError } from "../../lib/lado";
import { ladoErrorText, ladoProtectedPathProblemText } from "../../lib/lado-errors";
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

/** The most paths the rule holds (domain.LadoProtectedPathsMax). */
const MAX_PROTECTED_PATHS = 50;

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

type PathLine = { line: number; glob: string };

/** Each non-blank line's path, without surrounding space, with its line number. */
function pathLines(text: string): PathLine[] {
	return text
		.split(/\r?\n/)
		.map((source, index) => ({ line: index + 1, glob: source.trim() }))
		.filter((entry) => entry.glob !== "");
}

function hasControlCharacter(glob: string): boolean {
	return [...glob].some((character) => {
		const code = character.charCodeAt(0);
		return code < 0x20 || (code >= 0x7f && code <= 0x9f);
	});
}

/** Whether Go's path.Match accepts the pattern: every character class closes and has well-formed ranges. */
function validPattern(glob: string): boolean {
	for (let index = 0; index < glob.length; index++) {
		if (glob[index] !== "[") continue;
		index++;
		if (glob[index] === "^") index++;
		for (let ranges = 0; ; ranges++) {
			if (index >= glob.length) return false;
			if (glob[index] === "]" && ranges > 0) break;
			if (glob[index] === "-" || glob[index] === "]") return false;
			index++;
			if (glob[index] === "-") {
				index++;
				if (index >= glob.length || glob[index] === "-" || glob[index] === "]") return false;
				index++;
			}
		}
	}
	return true;
}

/**
 * Whether LADO takes the path as a plan's affected_paths entry
 * (backend/internal/domain validLadoAffectedPath): relative with /
 * separators, without a .. segment, a drive letter or a control character,
 * and a valid pattern.
 */
function validPath(glob: string): boolean {
	if (glob.startsWith("/") || glob.includes("\\") || hasControlCharacter(glob)) return false;
	if (glob.length >= 2 && glob[1] === ":") return false;
	if (glob.split("/").includes("..")) return false;
	return validPattern(glob);
}

/**
 * Open while open is true; mount it again for each opening so it starts from
 * the rule's paths as they are then.
 */
export function LadoProtectedPathsDialog({
	open,
	globs,
	onOpenChange,
}: {
	open: boolean;
	globs: readonly string[];
	onOpenChange: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const change = useChangeLadoProtectedPaths();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const fieldRef = useRef<HTMLTextAreaElement>(null);
	const [text, setText] = useState(() => globs.join("\n"));
	// The lines the last save sent, in order: the daemon names a problem by its index there.
	const [sent, setSent] = useState<PathLine[]>([]);
	const fieldId = useId();
	const hintId = useId();
	const problemsId = useId();

	const lines = pathLines(text);
	const dirty = lines.length !== globs.length || lines.some((entry, index) => entry.glob !== globs[index]);
	const problems = [
		...lines
			.filter((entry) => !validPath(entry.glob))
			.map((entry) => t("lado.protectedPaths.line", { line: entry.line, problem: t("lado.protectedPaths.problem.invalid") })),
		...(lines.length === 0 ? [t("lado.protectedPaths.problem.required")] : []),
		...(lines.length > MAX_PROTECTED_PATHS ? [t("lado.protectedPaths.problem.tooMany")] : []),
	];
	const busy = change.isPending;

	// The answer that refused the save: a refused path names its line, a
	// browser has no desktop app to confirm the change.
	const error = change.error;
	const refusedLine =
		error instanceof LadoApiError && error.code === "LADO_PROTECTED_PATHS_INVALID" && typeof error.details?.index === "number"
			? sent[error.details.index]
			: undefined;
	const refusedProblem = refusedLine && error instanceof LadoApiError ? ladoProtectedPathProblemText(t, error.details ?? {}) : "";
	const saveError = !error
		? null
		: refusedLine && refusedProblem
			? t("lado.protectedPaths.line", { line: refusedLine.line, problem: refusedProblem })
			: error instanceof LadoApiError && error.code === "LADO_PRESENCE_UNAVAILABLE"
				? t("lado.protectedPaths.unavailable")
				: ladoErrorText(t, error);

	const close = () => onOpenChange(false);
	const submit = (event: FormEvent) => {
		event.preventDefault();
		if (busy || !dirty || problems.length > 0) return;
		setSent(lines);
		change.mutate(
			lines.map((entry) => entry.glob),
			{
				// null: the developer cancelled the desktop confirmation, and the edits stay.
				onSuccess: (rule) => {
					if (rule) close();
				},
			},
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
				data-testid="lado-protected-paths"
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					fieldRef.current?.focus();
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
					<DialogTitle className="settings-dialog-title text-base">{t("lado.protectedPaths.title")}</DialogTitle>
				</div>
				<form className="flex min-h-0 flex-1 flex-col" onSubmit={submit}>
					{/* The div scrolls; a fieldset as the scroll container spills under the footer. */}
					<div className={cn(settingsDialogBodyClass, "min-w-0 gap-1.5 px-5 pt-0 pb-4")}>
						<div className="flex items-baseline justify-between gap-3">
							<label htmlFor={fieldId} className="settings-field-label">
								{t("lado.protectedPaths.label")}
							</label>
							<span className="text-caption leading-4 tabular-nums text-settings-muted" data-testid="lado-protected-path-count">
								{t("lado.rules.protectedPaths.count", { count: lines.length })}
							</span>
						</div>
						<p id={hintId} className="text-caption leading-4 text-settings-muted">
							{t("lado.protectedPaths.hint")}
						</p>
						{/* The textarea takes its font from here (the app's form reset). */}
						<div className="font-mono">
							<textarea
								ref={fieldRef}
								id={fieldId}
								value={text}
								rows={12}
								spellCheck={false}
								autoComplete="off"
								disabled={busy}
								aria-invalid={problems.length > 0 ? true : undefined}
								aria-describedby={problems.length > 0 ? `${hintId} ${problemsId}` : hintId}
								onChange={(event) => {
									setText(event.target.value);
									if (change.error) change.reset();
								}}
								className="settings-field-control min-h-(--size-textarea-min) resize-none overflow-y-auto rounded-md! py-2.5"
							/>
						</div>
						{problems.length > 0 ? (
							<ul id={problemsId} className="flex flex-col gap-0.5" data-testid="lado-protected-path-problems">
								{problems.map((problem) => (
									<li key={problem} className="text-caption leading-4 text-error">
										{problem}
									</li>
								))}
							</ul>
						) : null}
						{saveError ? (
							<p role="alert" className="text-caption leading-4 text-error">
								{saveError}
							</p>
						) : null}
					</div>
					<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
						<Button type="button" variant="footer" className={compactButtonClass} disabled={busy} onClick={close}>
							{t("confirm.cancel")}
						</Button>
						<Button
							type="submit"
							variant="footer-primary"
							className={compactButtonClass}
							disabled={busy || !dirty || problems.length > 0}
						>
							{busy ? t("lado.rules.editor.saving") : t("lado.protectedPaths.save")}
						</Button>
					</div>
				</form>
			</DialogContent>
		</Dialog>
	);
}
