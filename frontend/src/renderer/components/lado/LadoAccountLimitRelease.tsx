// LADO fork: the developer's way to release an account usage limit by hand
// (issue #92). When a worker hits a usage limit, LADO holds every new worker
// on that account until the reset. A developer who logged in to another
// account releases it here: a quiet button on what waits on the limit (a
// queued or plan card, the task view, a limited alert) opens a dialog that
// lists the active limits, each with its own release. The daemon keeps the
// reason in its audit log, checks the waits again and resumes the workers
// the limit stopped. The button shows only while a limit holds.
import type { TFunction } from "i18next";
import { X } from "lucide-react";
import { type MouseEvent, useState } from "react";
import { useTranslation } from "react-i18next";
import { useRestoreFocusOnClose } from "../../hooks/useRestoreFocusOnClose";
import { useLadoAccountLimits, useReleaseLadoAccountLimit } from "../../hooks/useLado";
import type { LadoAccountLimit, LadoTask, LadoTaskPart } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
import { formatResetTime } from "../settings/CodexAccountDetails";
import { Button } from "../ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogOverlay,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { ladoOpenPlanWorker } from "./lado-task-status";

/**
 * Whether the task waits on an account usage limit: queued for one, its plan
 * worker waiting for one, or one of its parts waiting for one.
 */
export function ladoTaskAccountLimited(task: LadoTask, parts?: readonly LadoTaskPart[]): boolean {
	const queued = task.state === "queued" || task.state === "dispatching";
	if (queued && task.stateReason === "account_limited") return true;
	const worker = ladoOpenPlanWorker(task);
	if (worker?.phase === "waiting" && worker.waitingReason === "account_limited") return true;
	return (parts ?? []).some((part) => part.waitingReason === "account_limited");
}

const providerNames: Record<LadoAccountLimit["provider"], string> = { claude: "Claude", codex: "Codex" };

/** The limit's account as people say it: "Claude · this device's login", or the named account. */
export function ladoAccountLimitLabel(limit: LadoAccountLimit, t: TFunction): string {
	const device = limit.accountId === `device-${limit.provider}`;
	return `${providerNames[limit.provider]} · ${device ? t("lado.accountLimit.device") : limit.accountId}`;
}

// The dialog renders in a portal, but React sends its clicks up the React
// tree: on a card, they would also open the task.
function stop(event: MouseEvent) {
	event.stopPropagation();
}

/**
 * The release button, shown while an account limit holds; render it only
 * where something waits on a limit, since it reads the limits every 30
 * seconds.
 */
export function LadoAccountLimitRelease({ className }: { className?: string }) {
	const { t } = useTranslation();
	const limits = useLadoAccountLimits();
	const [open, setOpen] = useState(false);
	const active = (limits.data ?? []).filter((limit) => limit.active);
	// The dialog stays while open, so a release that fails can say why.
	if (active.length === 0 && !open) return null;
	return (
		<>
			{active.length > 0 ? (
				<Button
					type="button"
					variant="ghost"
					size="sm"
					className={cn("h-6 shrink-0 px-2 text-muted-foreground hover:text-foreground", className)}
					data-testid="lado-account-limit-release"
					onClick={(event) => {
						// The button sits on a card that opens the task.
						event.stopPropagation();
						setOpen(true);
					}}
				>
					{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
					<span className="text-2xs font-medium">{t("lado.accountLimit.release")}</span>
				</Button>
			) : null}
			<ReleaseDialog open={open} active={active} onOpenChange={setOpen} />
		</>
	);
}

function ReleaseDialog({
	open,
	active,
	onOpenChange,
}: {
	open: boolean;
	active: LadoAccountLimit[];
	onOpenChange: (open: boolean) => void;
}) {
	const { t, i18n } = useTranslation();
	const release = useReleaseLadoAccountLimit();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const busy = release.isPending;
	const close = (next: boolean) => {
		if (busy && !next) return;
		if (!next) release.reset();
		onOpenChange(next);
	};
	return (
		<Dialog open={open} onOpenChange={close}>
			<DialogContent
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(460px,calc(100vw-24px))]")}
				data-testid="lado-account-limit-dialog"
				onClick={stop}
				onCloseAutoFocus={restoreFocusOnClose}
				overlay={<DialogOverlay onClick={stop} />}
			>
				<DialogClose asChild>
					<button
						type="button"
						disabled={busy}
						tabIndex={-1}
						className="settings-dialog-close-button settings-close-button"
						aria-label={t("confirm.close")}
						title={t("confirm.closeEsc")}
					>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("lado.accountLimit.title")}</DialogTitle>
					<DialogDescription asChild>
						<div className="text-control leading-5 text-settings-muted">{t("lado.accountLimit.description")}</div>
					</DialogDescription>
				</div>
				<div className={cn(settingsDialogBodyClass, "px-5 pt-0 pb-3")}>
					{active.length === 0 ? (
						<p className="text-control text-settings-muted" role="status">{t("lado.accountLimit.none")}</p>
					) : (
						<ul className="flex flex-col divide-y divide-[var(--color-border-settings-dialog)] overflow-hidden rounded-md bg-surface">
							{active.map((limit) => {
								const account = ladoAccountLimitLabel(limit, t);
								const reset = formatResetTime(limit.resetAt, i18n.language);
								return (
									<li
										key={limit.accountKey}
										className="flex min-w-0 items-center gap-3 px-3 py-2.5"
										data-testid="lado-account-limit"
										data-account-key={limit.accountKey}
									>
										<span className="flex min-w-0 flex-1 flex-col gap-0.5">
											<span className="truncate text-control font-medium text-foreground" title={account}>{account}</span>
											{reset ? (
												<span className="text-2xs tabular-nums text-muted-foreground" title={reset.full}>
													{t(limit.resetGuessed ? "lado.accountLimit.resetAtGuessed" : "lado.accountLimit.resetAt", { time: reset.visible })}
												</span>
											) : null}
										</span>
										<Button
											type="button"
											variant="footer-primary"
											className="h-8 shrink-0 rounded-[10px] px-4 text-sm"
											aria-label={t("lado.accountLimit.releaseLabel", { account })}
											disabled={busy}
											aria-busy={busy && release.variables?.accountKey === limit.accountKey}
											onClick={() =>
												release.mutate(
													{ accountKey: limit.accountKey, reason: t("lado.accountLimit.reason") },
													{
														onSuccess: () => {
															// Nothing else holds: the dialog has done its work.
															if (active.every((other) => other.accountKey === limit.accountKey)) onOpenChange(false);
														},
													},
												)
											}
										>
											{t("lado.accountLimit.releaseOne")}
										</Button>
									</li>
								);
							})}
						</ul>
					)}
					{release.isError ? (
						<p role="alert" className="mt-2 text-caption leading-4 text-error">{ladoErrorText(t, release.error)}</p>
					) : null}
				</div>
				<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
					<DialogClose asChild>
						<Button type="button" variant="footer" className="h-8 rounded-[10px] px-4 text-sm" disabled={busy}>
							{t("common.close")}
						</Button>
					</DialogClose>
				</div>
			</DialogContent>
		</Dialog>
	);
}
