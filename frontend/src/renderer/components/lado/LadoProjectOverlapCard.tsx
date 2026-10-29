// LADO fork: Project settings → LADO → Overlapping work (docs/LJY-PRD/TDD.md
// §5.4). Whether a task waits while a running task of the project may change
// the same files, and the globs whose overlap never holds work back, between
// tasks and between a task's parts. Both change without a presence proof and
// save at once; the globs are edited in a dialog, one per line.
import { X } from "lucide-react";
import { useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useUpdateLadoProjectOverlap } from "../../hooks/useLado";
import { useRestoreFocusOnClose } from "../../hooks/useRestoreFocusOnClose";
import type { LadoProjectSettings } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
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
import { Switch } from "../ui/switch";

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

/** The most globs the daemon takes. */
const MAX_IGNORE = 50;

/** Each non-blank line, trimmed. */
function globLines(text: string): string[] {
	return text
		.split(/\r?\n/)
		.map((line) => line.trim())
		.filter((line) => line !== "");
}

export function LadoProjectOverlapCard({ projectId, settings }: { projectId: string; settings: LadoProjectSettings }) {
	const { t } = useTranslation();
	const update = useUpdateLadoProjectOverlap(projectId);
	// Each opening mounts the dialog again, from the saved globs.
	const [editing, setEditing] = useState({ key: 0, open: false });
	const wait = update.isPending && update.variables?.overlapWait !== undefined ? update.variables.overlapWait : settings.overlapWait;
	return (
		<SettingsSection title={t("lado.project.overlap")} grouped testId="lado-project-overlap">
			<SettingsRow
				label={t("lado.project.overlapWait")}
				description={
					<>
						{t("lado.project.overlapWaitHint")}
						{update.isError && !editing.open ? (
							<span role="alert" className="block text-error">
								{ladoErrorText(t, update.error)}
							</span>
						) : null}
					</>
				}
			>
				<Switch
					aria-label={t("lado.project.overlapWait")}
					checked={wait}
					disabled={update.isPending}
					onCheckedChange={(on) => update.mutate({ overlapWait: on })}
				/>
			</SettingsRow>
			<SettingsRow
				label={t("lado.project.overlapIgnore")}
				description={
					<>
						{t("lado.project.overlapIgnoreHint")}{" "}
						<span className="tabular-nums" data-testid="lado-overlap-ignore-count">
							{settings.overlapIgnoreIsDefault
								? t("lado.project.overlapIgnoreDefault", { count: settings.overlapIgnore.length })
								: t("lado.project.overlapIgnoreCount", { count: settings.overlapIgnore.length })}
						</span>
					</>
				}
			>
				<Button
					type="button"
					variant="ghost"
					size="sm"
					aria-label={t("lado.project.overlapIgnore")}
					className="text-settings-muted hover:text-settings-label"
					disabled={update.isPending}
					onClick={() => {
						update.reset();
						setEditing((current) => ({ key: current.key + 1, open: true }));
					}}
				>
					<span className="text-sm">{t("lado.project.overlapIgnoreEdit")}</span>
				</Button>
			</SettingsRow>
			<OverlapIgnoreDialog
				key={`overlap-${editing.key}`}
				open={editing.open}
				settings={settings}
				update={update}
				onOpenChange={(open) => setEditing((current) => ({ ...current, open }))}
			/>
		</SettingsSection>
	);
}

function OverlapIgnoreDialog({
	open,
	settings,
	update,
	onOpenChange,
}: {
	open: boolean;
	settings: LadoProjectSettings;
	update: ReturnType<typeof useUpdateLadoProjectOverlap>;
	onOpenChange: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const fieldId = useId();
	const hintId = useId();
	const fieldRef = useRef<HTMLTextAreaElement>(null);
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const [text, setText] = useState(settings.overlapIgnore.join("\n"));
	const lines = globLines(text);
	const busy = update.isPending;
	const tooMany = lines.length > MAX_IGNORE;
	const dirty = lines.join("\n") !== settings.overlapIgnore.join("\n");
	const close = () => onOpenChange(false);
	const submit = (event: FormEvent) => {
		event.preventDefault();
		if (busy || tooMany || !dirty) return;
		update.mutate({ overlapIgnore: lines }, { onSuccess: close });
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
				data-testid="lado-overlap-ignore"
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					fieldRef.current?.focus();
				}}
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<DialogClose asChild>
					<button type="button" disabled={busy} className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("lado.project.overlapIgnore")}</DialogTitle>
				</div>
				<form className="flex min-h-0 flex-1 flex-col" onSubmit={submit}>
					{/* The div scrolls; a fieldset as the scroll container spills under the footer. */}
					<div className={cn(settingsDialogBodyClass, "min-w-0 gap-1.5 px-5 pt-0 pb-4")}>
						<div className="flex items-baseline justify-between gap-3">
							<label htmlFor={fieldId} className="settings-field-label">
								{t("lado.project.overlapIgnoreLabel")}
							</label>
							<span className={cn("text-caption leading-4 tabular-nums", tooMany ? "text-error" : "text-settings-muted")}>
								{t("lado.project.overlapIgnoreCount", { count: lines.length })}
							</span>
						</div>
						<p id={hintId} className="text-caption leading-4 text-settings-muted">
							{t("lado.project.overlapIgnoreDialogHint", { max: MAX_IGNORE })}
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
								aria-describedby={hintId}
								onChange={(event) => {
									setText(event.target.value);
									if (update.error) update.reset();
								}}
								className="settings-field-control min-h-(--size-textarea-min) resize-none overflow-y-auto rounded-md! py-2.5"
							/>
						</div>
						{update.isError ? (
							<p role="alert" className="text-caption leading-4 text-error">
								{ladoErrorText(t, update.error)}
							</p>
						) : null}
					</div>
					<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
						{settings.overlapIgnoreIsDefault ? null : (
							<Button
								type="button"
								variant="footer"
								className={cn(compactButtonClass, "mr-auto")}
								disabled={busy}
								onClick={() => update.mutate({ overlapIgnoreDefaults: true }, { onSuccess: close })}
							>
								{t("lado.project.overlapIgnoreReset")}
							</Button>
						)}
						<Button type="button" variant="footer" className={compactButtonClass} disabled={busy} onClick={close}>
							{t("confirm.cancel")}
						</Button>
						<Button type="submit" variant="footer-primary" className={compactButtonClass} disabled={busy || !dirty || tooMany}>
							{t("lado.project.overlapIgnoreSave")}
						</Button>
					</div>
				</form>
			</DialogContent>
		</Dialog>
	);
}
