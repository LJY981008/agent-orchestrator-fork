// LADO fork: what changed in the tools' folders and LADO's managed folder
// outside the app (docs/LJY-PRD/HARNESS.md §9, plan decision n). A managed
// copy edited by hand changes no session until the developer keeps it as a
// new version, which the desktop app confirms for a kind that can run
// commands, or reverts it to the latest version. A new entry at a tool's own
// location is never moved by itself: it is offered for adoption.
import { agentLabel } from "@aoagents/product-ui";
import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useRestoreFocusOnClose } from "../../../hooks/useRestoreFocusOnClose";
import { ladoErrorText, ladoSetupKindLabel, ladoSetupReasonText } from "../../../lib/lado-errors";
import {
	previewLadoSetupChange,
	useLadoSetupChange,
	type LadoSetupDrift,
	type LadoSetupInventory,
} from "../../../lib/lado-setup";
import { cn } from "../../../lib/utils";
import { SettingsRow } from "../../settings/SettingsRow";
import { SettingsSection } from "../../settings/SettingsSection";
import { Button } from "../../ui/button";
import { Dialog, DialogClose, DialogContent, DialogTitle, settingsDialogBodyClass, settingsDialogFooterClass, settingsDialogHeaderClass } from "../../ui/dialog";
import { LadoRuleImpactDialog } from "../LadoRuleImpactDialog";
import { LadoSetupChangeReview, ladoSetupWideDialogClass } from "./LadoSetupContent";
import { ladoSetupAdoptable, ladoSetupEntryKey, ladoSetupShortPath } from "./lado-setup-view";

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

function driftKey(drift: LadoSetupDrift): string {
	return `${drift.kind}:${drift.path}`;
}

/**
 * The drift of the last scan, when there is any: managed copies changed by
 * hand, each with Review, and new entries at the tools' own locations, each
 * with Adopt…, which onAdopt opens for that entry alone.
 */
export function LadoSetupDriftCard({ inventory, onAdopt }: { inventory: LadoSetupInventory; onAdopt: (entryKey: string) => void }) {
	const { t } = useTranslation();
	// Each opening mounts the review again, so it previews the copy as it is then.
	const [reviewing, setReviewing] = useState<{ key: number; drift: LadoSetupDrift | null; open: boolean }>({ key: 0, drift: null, open: false });
	if (inventory.drift.length === 0) return null;

	const versions = new Map(inventory.items.map((item) => [item.itemId, item.version]));

	// What an adoption can take now; a new entry whose name a managed item
	// holds, or on an OS LADO has not checked, is not among them.
	const adoptable = new Set(
		ladoSetupAdoptable(inventory)
			.filter((entry) => inventory.verified[entry.tool])
			.map(ladoSetupEntryKey),
	);
	return (
		<>
			<SettingsSection title={t("lado.setup.drift.title")} grouped testId="lado-setup-drift">
				{inventory.drift.map((drift) => {
					const key = ladoSetupEntryKey({ tool: drift.tool, kind: drift.itemKind, name: drift.name });
					const state =
						drift.kind === "edited"
							? t("lado.setup.drift.edited")
							: drift.itemId
								? `${t("lado.setup.drift.newLocal")} · ${ladoSetupReasonText(t, "already_managed")}`
								: t("lado.setup.drift.newLocal");
					return (
						<SettingsRow
							key={driftKey(drift)}
							data-testid="lado-setup-drift-row"
							data-kind={drift.kind}
							data-path={drift.path}
							label={drift.name}
							description={
								<>
									<span className="block">{[agentLabel(drift.tool), ladoSetupKindLabel(t, drift.itemKind), state].join(" · ")}</span>
									<span className="block truncate font-mono text-xs" title={drift.path}>
										{ladoSetupShortPath(drift.path, inventory)}
									</span>
								</>
							}
						>
							{drift.kind === "edited" ? (
								<Button
									type="button"
									variant="ghost"
									size="sm"
									className="text-settings-muted hover:text-settings-label"
									aria-label={t("lado.setup.drift.reviewLabel", { name: drift.name })}
									onClick={() => setReviewing((current) => ({ key: current.key + 1, drift, open: true }))}
								>
									<span className="text-control">{t("lado.setup.drift.review")}</span>
								</Button>
							) : adoptable.has(key) ? (
								<Button
									type="button"
									variant="ghost"
									size="sm"
									className="text-settings-muted hover:text-settings-label"
									aria-label={t("lado.setup.drift.adoptLabel", { name: drift.name })}
									onClick={() => onAdopt(key)}
								>
									<span className="text-control">{t("lado.setup.section.adopt")}</span>
								</Button>
							) : null}
						</SettingsRow>
					);
				})}
			</SettingsSection>
			<LadoSetupDriftDialog
				key={reviewing.key}
				drift={reviewing.drift}
				latestVersion={reviewing.drift ? versions.get(reviewing.drift.itemId) : undefined}
				open={reviewing.open}
				onOpenChange={(open) => setReviewing((current) => ({ ...current, open }))}
			/>
		</>
	);
}

/**
 * Reviews one edited managed copy against its item's latest version
 * (latestVersion, when known), then keeps it as the next version or reverts
 * it. Open while drift is set and open is true; mount it again for each
 * opening.
 */
export function LadoSetupDriftDialog({
	drift,
	latestVersion,
	open,
	onOpenChange,
}: {
	drift: LadoSetupDrift | null;
	latestVersion?: number;
	open: boolean;
	onOpenChange: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const change = useLadoSetupChange();
	const [action, setAction] = useState<"keep" | "revert" | null>(null);
	const itemId = drift?.itemId ?? "";
	const sha256 = drift?.sha256 ?? "";
	// Keeping's dry run shows the copy against the latest version; it changes
	// nothing. It stays out of the setup queries a change refreshes, so the
	// review a keep or revert closes is not previewed again.
	const preview = useQuery({
		queryKey: ["lado-setup-drift-preview", itemId, sha256],
		queryFn: () => previewLadoSetupChange({ op: "drift", itemId, input: { action: "keep", sha256 } }),
		enabled: open && drift !== null && drift.kind === "edited",
		refetchOnWindowFocus: false,
		retry: false,
	});
	const latest = latestVersion;
	const busy = change.isPending;
	// A failed apply after the impact dialog's confirm shows in that dialog.
	const error = change.pending ? null : change.error;

	const close = () => onOpenChange(false);
	const resolve = (next: "keep" | "revert") => {
		if (!drift || busy) return;
		setAction(next);
		change
			.change({ op: "drift", itemId, input: next === "keep" ? { action: "keep", sha256 } : { action: "revert" } })
			.then((outcome) => {
				if (outcome.status === "applied") close();
			})
			.catch(() => undefined);
	};

	const name = drift?.name ?? "";
	return (
		<Dialog
			open={open && drift !== null}
			onOpenChange={(next) => {
				if (!next && busy) return;
				onOpenChange(next);
			}}
		>
			<DialogContent
				showCloseButton={false}
				aria-describedby={undefined}
				className={ladoSetupWideDialogClass}
				data-testid="lado-setup-drift-review"
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<DialogClose asChild>
					<button type="button" disabled={busy} className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("lado.setup.drift.dialogTitle", { name })}</DialogTitle>
				</div>
				<div className={cn(settingsDialogBodyClass, "min-w-0 gap-(--size-settings-section-inner-gap) px-5 pt-0 pb-4")}>
					{drift ? (
						<p className="break-all font-mono text-xs text-settings-muted" title={drift.path}>
							{drift.path}
						</p>
					) : null}
					{preview.isPending ? (
						<p className="text-control text-settings-muted" role="status">
							{t("common.loading")}
						</p>
					) : preview.isError ? (
						<p className="text-caption leading-4 text-error" role="alert">
							{ladoErrorText(t, preview.error)}
						</p>
					) : preview.data && drift ? (
						<>
							{latest !== undefined ? (
								<p className="text-control leading-4 text-settings-muted">{t("lado.setup.drift.description", { version: latest })}</p>
							) : null}
							<LadoSetupChangeReview result={preview.data} kind={drift.itemKind} name={name} presence="lado.setup.drift.needsPresence" />
						</>
					) : null}
				</div>
				<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
					{error ? (
						<p role="alert" className="mr-auto text-caption leading-4 text-error">
							{ladoErrorText(t, error)}
						</p>
					) : null}
					{/* Reverting throws the hand edits away, so it is worded by what it writes. */}
					<Button
						type="button"
						variant="footer"
						className={cn(compactButtonClass, "text-destructive")}
						disabled={busy || !drift}
						onClick={() => resolve("revert")}
					>
						{busy && action === "revert"
							? t("lado.setup.drift.reverting")
							: latest !== undefined
								? t("lado.setup.drift.revertTo", { version: latest })
								: t("lado.setup.drift.revert")}
					</Button>
					<Button
						type="button"
						variant="footer-primary"
						className={compactButtonClass}
						disabled={busy || !preview.data}
						onClick={() => resolve("keep")}
					>
						{busy && action === "keep"
							? t("lado.setup.drift.keeping")
							: latest !== undefined
								? t("lado.setup.drift.keepAs", { version: latest + 1 })
								: t("lado.setup.drift.keep")}
					</Button>
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
