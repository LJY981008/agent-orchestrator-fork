// LADO fork: editing a managed setup item in the app (docs/LJY-PRD/HARNESS.md
// §9; owner decision 3 of the step 7 plan). A text item is edited here: the
// developer reviews the difference and what the daemon's dry run says about
// conflicts, a change that reaches approved tasks names them first, and one
// that can run commands or widen access ends in the desktop app's
// confirmation. An edit starts from the version the developer opened, so a
// newer one stops it until they reload, which keeps their edits on top of it.
// A folder item's files are changed in the managed folder instead, and the
// change comes back through drift.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLadoSetupItem } from "../../../hooks/useLadoSetup";
import { useRestoreFocusOnClose } from "../../../hooks/useRestoreFocusOnClose";
import { LadoApiError } from "../../../lib/lado";
import { ladoErrorText } from "../../../lib/lado-errors";
import {
	fetchLadoSetupItem,
	ladoSetupItemQueryKey,
	previewLadoSetupChange,
	useLadoSetupChange,
	type LadoSetupChangeRequest,
	type LadoSetupItemDetail,
} from "../../../lib/lado-setup";
import { cn } from "../../../lib/utils";
import { Button } from "../../ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../../ui/dialog";
import { LadoRuleImpactDialog } from "../LadoRuleImpactDialog";
import {
	LadoSetupChangeReview,
	LadoSetupContentField,
	LadoSetupFileList,
	ladoSetupContentValue,
	ladoSetupWideDialogClass,
} from "./LadoSetupContent";
import { ladoSetupFolderContent } from "./lado-setup-view";

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

// Where the daemon's refusals of an edit belong: under the content, or the
// stale notice for a newer version.
const contentCodes = new Set(["LADO_SETUP_CONTENT_INVALID", "LADO_SETUP_LOGIN_KEY"]);
const ITEM_CHANGED = "LADO_SETUP_ITEM_CHANGED";
// The managed copy was changed by hand: the daemon refuses an edit until that
// change is kept or reverted, which the drift review does.
const DRIFT_PENDING = "LADO_SETUP_DRIFT_PENDING";

function codeOf(error: unknown): string | undefined {
	return error instanceof LadoApiError ? error.code : undefined;
}

type Base = { version: number; text: string };

/** The latest version as an edit starts from it; null while it is unknown or not text. */
function baseOf(detail: LadoSetupItemDetail | undefined): Base | null {
	const latest = detail?.versions.at(-1);
	if (!latest?.content) return null;
	return { version: latest.version.version, text: latest.content.text };
}

/**
 * Open while itemId is set and open is true. Mount it again for each opening,
 * so it starts from the item as it is then. onReviewDrift opens the drift
 * review of the item instead, when its managed copy was changed by hand.
 */
export function LadoSetupItemEditor({
	itemId,
	open,
	onOpenChange,
	onReviewDrift,
}: {
	itemId: string | null;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onReviewDrift?: () => void;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const detail = useLadoSetupItem(itemId);
	const change = useLadoSetupChange();
	const preview = useMutation({ mutationFn: previewLadoSetupChange, retry: false });
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const errorId = useId();
	const [base, setBase] = useState<Base | null>(null);
	const [draft, setDraft] = useState<string | null>(null);
	const [reviewing, setReviewing] = useState(false);
	const [invalidJSON, setInvalidJSON] = useState(false);
	const [reloading, setReloading] = useState(false);
	const [reloadError, setReloadError] = useState<unknown>(null);
	// Set once a reload kept the edits on a newer version, until the next review.
	const [rebased, setRebased] = useState(false);

	// The edit starts from the latest version when the item first arrives;
	// later refetches do not move it, so a newer version stops the save.
	const loaded = baseOf(detail.data);
	if (base === null && loaded) {
		setBase(loaded);
		setDraft(loaded.text);
	}

	const item = detail.data?.item;
	const latest = detail.data?.versions.at(-1);
	const content = latest?.content;
	const folder = item && content ? ladoSetupFolderContent(item.kind, content) : false;
	// Content that is no text, or larger than the daemon shows, cannot be edited here.
	const textless = Boolean(content && !folder && content.text === "" && content.size > 0);
	const editable = Boolean(item && content && !folder && !textless && base && draft !== null);
	const dirty = editable && draft !== base?.text;

	const busy = preview.isPending || change.isPending || reloading;
	// A failed apply after the impact dialog's confirm shows in that dialog.
	const error = (change.pending ? null : change.error) ?? preview.error;
	const stale = codeOf(error) === ITEM_CHANGED;
	const drifted = codeOf(error) === DRIFT_PENDING;
	const contentError = invalidJSON
		? t("lado.setup.editor.invalidJson")
		: error && contentCodes.has(codeOf(error) ?? "")
			? ladoErrorText(t, error)
			: null;
	const formError = error && !stale && !drifted && !contentCodes.has(codeOf(error) ?? "") ? ladoErrorText(t, error) : null;

	const close = () => onOpenChange(false);
	const request = (): LadoSetupChangeRequest | null => {
		if (!itemId || !item || !base || draft === null) return null;
		const value = ladoSetupContentValue(item.kind, draft);
		if (!value.ok) return null;
		return { op: "edit", itemId, input: { baseVersion: base.version, content: value.value } };
	};

	const edit = (next: string) => {
		setDraft(next);
		setInvalidJSON(false);
		if (preview.error) preview.reset();
		if (change.error) change.reset();
	};

	const review = () => {
		if (busy || !dirty || !item || draft === null) return;
		if (!ladoSetupContentValue(item.kind, draft).ok) {
			setInvalidJSON(true);
			return;
		}
		const next = request();
		if (!next) return;
		setRebased(false);
		change.reset();
		preview.mutate(next, { onSuccess: () => setReviewing(true) });
	};

	const save = () => {
		const next = request();
		if (busy || !next) return;
		change
			.change(next)
			.then((outcome) => {
				if (outcome.status === "applied") close();
			})
			.catch((failure: unknown) => {
				// The content was refused: back to it, with the reason under it.
				if (contentCodes.has(codeOf(failure) ?? "")) setReviewing(false);
			});
	};

	// A newer version stopped the edit: continue from it with the edits kept.
	const reload = async () => {
		if (!itemId) return;
		setReloading(true);
		setReloadError(null);
		try {
			const fresh = await fetchLadoSetupItem(itemId);
			queryClient.setQueryData(ladoSetupItemQueryKey(itemId), fresh);
			const next = baseOf(fresh);
			if (next) setBase(next);
			setRebased(true);
			setReviewing(false);
			preview.reset();
			change.reset();
		} catch (failure) {
			setReloadError(failure);
		} finally {
			setReloading(false);
		}
	};

	const name = item?.name ?? "";
	return (
		<Dialog
			open={open && itemId !== null}
			onOpenChange={(next) => {
				if (!next && busy) return;
				onOpenChange(next);
			}}
		>
			<DialogContent
				showCloseButton={false}
				aria-describedby={undefined}
				className={ladoSetupWideDialogClass}
				data-testid="lado-setup-editor"
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<DialogClose asChild>
					<button type="button" disabled={busy} className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("lado.setup.editor.title", { name })}</DialogTitle>
				</div>
				<div className={cn(settingsDialogBodyClass, "min-w-0 gap-(--size-settings-section-inner-gap) px-5 pt-0 pb-4")}>
					{detail.isPending ? (
						<p className="text-control text-settings-muted" role="status">
							{t("common.loading")}
						</p>
					) : !detail.data ? (
						<p className="text-caption leading-4 text-error" role="alert">
							{ladoErrorText(t, detail.error)}
						</p>
					) : !content ? (
						<p className="text-caption leading-4 text-error" role="alert">
							{t("lado.setup.editor.unreadable")}
						</p>
					) : folder ? (
						<>
							<p className="text-control leading-4 text-settings-muted">{t("lado.setup.editor.folderNote")}</p>
							{detail.data.managedPath ? (
								<div className="flex flex-col gap-1">
									<span className="text-xs text-settings-muted">{t("lado.setup.editor.managedFolder")}</span>
									<span className="break-all font-mono text-xs text-settings-label" data-testid="lado-setup-managed-path">
										{detail.data.managedPath}
									</span>
								</div>
							) : null}
							<LadoSetupFileList files={content.files} label={t("lado.setup.editor.files")} />
						</>
					) : textless ? (
						<p className="text-control leading-4 text-settings-muted">{t("lado.setup.editor.notText")}</p>
					) : (
						<>
							{stale ? (
								<div className="flex items-center justify-between gap-3">
									<p className="text-caption leading-4 text-[var(--color-status-needs-you)]" role="status">
										{reloadError ? ladoErrorText(t, reloadError) : t("lado.setup.editor.stale")}
									</p>
									<Button
										type="button"
										variant="ghost"
										size="sm"
										className="shrink-0 text-settings-muted hover:text-settings-label"
										disabled={reloading}
										onClick={() => void reload()}
									>
										<span className="text-control">{t("lado.rules.editor.reload")}</span>
									</Button>
								</div>
							) : drifted ? (
								<div className="flex items-center justify-between gap-3" data-testid="lado-setup-editor-drift">
									<p className="text-caption leading-4 text-[var(--color-status-needs-you)]" role="status">
										{ladoErrorText(t, error)}
									</p>
									{onReviewDrift ? (
										<Button
											type="button"
											variant="ghost"
											size="sm"
											className="shrink-0 text-settings-muted hover:text-settings-label"
											onClick={onReviewDrift}
										>
											<span className="text-control">{t("lado.setup.list.reviewChange")}</span>
										</Button>
									) : null}
								</div>
							) : rebased ? (
								<p className="text-caption leading-4 text-[var(--color-status-needs-you)]" role="status">
									{t("lado.setup.editor.editsKept")}
								</p>
							) : null}
							{reviewing && preview.data && item ? (
								<LadoSetupChangeReview result={preview.data} kind={item.kind} name={name} presence="lado.setup.editor.needsPresence" />
							) : (
								<LadoSetupContentField
									kind={item?.kind ?? ""}
									name={name}
									value={draft ?? ""}
									onChange={edit}
									error={contentError}
									errorId={errorId}
								/>
							)}
						</>
					)}
				</div>
				<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
					{formError ? (
						<p role="alert" className="mr-auto text-caption leading-4 text-error">
							{formError}
						</p>
					) : null}
					{editable ? (
						reviewing ? (
							<>
								<Button type="button" variant="footer" className={compactButtonClass} disabled={busy} onClick={() => setReviewing(false)}>
									{t("lado.setup.editor.back")}
								</Button>
								<Button type="button" variant="footer-primary" className={compactButtonClass} disabled={busy || stale} onClick={save}>
									{change.isPending ? t("lado.rules.editor.saving") : t("lado.rules.editor.save")}
								</Button>
							</>
						) : (
							<>
								<Button type="button" variant="footer" className={compactButtonClass} disabled={busy} onClick={close}>
									{t("confirm.cancel")}
								</Button>
								<Button type="button" variant="footer-primary" className={compactButtonClass} disabled={busy || !dirty || stale} onClick={review}>
									{preview.isPending ? t("lado.setup.editor.reviewing") : t("lado.setup.editor.review")}
								</Button>
							</>
						)
					) : (
						<Button type="button" variant="footer" className={compactButtonClass} onClick={close}>
							{t("common.close")}
						</Button>
					)}
				</div>
				<LadoRuleImpactDialog
					impact={change.pending?.result.impact ?? null}
					busy={change.isPending}
					error={change.pending ? change.error : null}
					title={t("lado.setup.impact.title")}
					onConfirm={() => {
						change
							.confirm()
							.then((outcome) => {
								if (outcome.status === "applied") close();
							})
							.catch(() => undefined);
					}}
					onCancel={change.cancel}
				/>
			</DialogContent>
		</Dialog>
	);
}
