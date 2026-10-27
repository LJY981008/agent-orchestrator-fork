// LADO fork: a managed setup item's versions (docs/LJY-PRD/HARNESS.md §9). A
// version never changes and every edit, kept drift and adoption adds one, so
// the list is the item's whole history: who made each version, when, and
// what it held: the text of a file or a value, or a folder's files.
import type { TFunction } from "i18next";
import { X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useLadoSetupItem } from "../../../hooks/useLadoSetup";
import { useRestoreFocusOnClose } from "../../../hooks/useRestoreFocusOnClose";
import type { MessageKey } from "../../../i18n/messages";
import { ladoErrorText } from "../../../lib/lado-errors";
import { cn } from "../../../lib/utils";
import { Dialog, DialogClose, DialogContent, DialogTitle, settingsDialogBodyClass, settingsDialogHeaderClass } from "../../ui/dialog";
import { LadoSetupFileList, ladoSetupWideDialogClass } from "./LadoSetupContent";
import { ladoSetupFolderContent } from "./lado-setup-view";

// Who made a version (backend/internal/service/ladosetup/changes.go): the
// same actors as a rule's.
const actorKeys: Partial<Record<string, MessageKey>> = {
	lado: "lado.rules.actor.lado",
	api: "lado.rules.actor.api",
	human: "lado.rules.actor.human",
	"human:dev-presence": "lado.rules.actor.devPresence",
};

function actorLabel(t: TFunction, actor: string): string {
	const key = actorKeys[actor];
	return key ? t(key) : actor;
}

/** Open while itemId is set and open is true. Mount it again for each opening. */
export function LadoSetupHistoryDialog({
	itemId,
	open,
	onOpenChange,
}: {
	itemId: string | null;
	open: boolean;
	onOpenChange: (open: boolean) => void;
}) {
	const { t, i18n } = useTranslation();
	const detail = useLadoSetupItem(itemId);
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const [selected, setSelected] = useState<string | null>(null);
	const item = detail.data?.item;
	const versions = detail.data?.versions ?? [];
	const latest = versions.at(-1)?.version.id;
	const shown = versions.find((entry) => entry.version.id === selected) ?? versions.at(-1);
	const time = (iso: string) => new Date(iso).toLocaleString(i18n.language, { dateStyle: "medium", timeStyle: "short" });
	const content = shown?.content;

	return (
		<Dialog open={open && itemId !== null} onOpenChange={onOpenChange}>
			<DialogContent
				showCloseButton={false}
				aria-describedby={undefined}
				className={ladoSetupWideDialogClass}
				data-testid="lado-setup-history"
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<DialogClose asChild>
					<button type="button" className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("lado.setup.history.title", { name: item?.name ?? "" })}</DialogTitle>
				</div>
				<div className={cn(settingsDialogBodyClass, "min-w-0 px-5 pt-0 pb-5")}>
					{detail.isPending ? (
						<p className="text-control text-settings-muted" role="status">
							{t("common.loading")}
						</p>
					) : detail.isError ? (
						<p className="text-caption leading-4 text-error" role="alert">
							{ladoErrorText(t, detail.error)}
						</p>
					) : (
						<>
							<ul aria-label={t("lado.rules.history.versions")} className="settings-grouped-rows flex w-full flex-col">
								{[...versions].reverse().map(({ version }) => (
									<li key={version.id} data-testid="lado-setup-version">
										<button
											type="button"
											aria-pressed={version.id === shown?.version.id}
											onClick={() => setSelected(version.id)}
											className={cn(
												"flex w-full items-center justify-between gap-3 px-(--settings-row-inset) py-2.5 text-left outline-none transition-colors duration-fast hover:bg-interactive-hover focus-visible:bg-interactive-hover",
												version.id === shown?.version.id && "bg-interactive-active",
											)}
										>
											<span className="flex min-w-0 items-baseline gap-2">
												<span className="text-sm text-settings-label">{t("lado.rules.history.version", { number: version.version })}</span>
												{version.id === latest && item?.live ? (
													<span className="text-xs text-settings-muted">{t("lado.rules.history.current")}</span>
												) : null}
											</span>
											<span className="shrink-0 text-xs tabular-nums text-settings-muted">
												{time(version.createdAt)} · {actorLabel(t, version.actor)}
											</span>
										</button>
									</li>
								))}
							</ul>
							{shown ? (
								<section
									aria-label={t("lado.rules.history.version", { number: shown.version.version })}
									className="flex min-w-0 flex-col gap-1.5"
									data-testid="lado-setup-version-content"
								>
									{!content ? (
										<p className="text-caption leading-4 text-error">{t("lado.setup.history.unreadable")}</p>
									) : item && ladoSetupFolderContent(item.kind, content) ? (
										<LadoSetupFileList files={content.files} label={t("lado.setup.editor.files")} />
									) : content.text ? (
										<pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-md bg-(--color-bg-secondary) px-(--settings-row-inset) py-2.5 font-mono text-xs leading-5 text-settings-label">
											{content.text}
										</pre>
									) : (
										<p className="text-control leading-4 text-settings-muted">
											{content.size > 0 ? t("lado.setup.editor.notText") : t("lado.setup.history.empty")}
										</p>
									)}
								</section>
							) : null}
						</>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}
