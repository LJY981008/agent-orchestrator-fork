// LADO fork: the conflicts of the managed local setup at Global
// (docs/LJY-PRD/HARNESS.md §10), worst first. A blocking conflict stops LADO
// tasks until the item is turned off or fixed; a warning can be accepted for
// the item's current content, and the acceptance revoked; a note needs
// nothing. Each names the item it is about, and editing that item or turning
// it off at Global is one action away.
import { agentLabel } from "@aoagents/product-ui";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ladoErrorText, ladoSetupConflictText, ladoSetupKindLabel } from "../../../lib/lado-errors";
import {
	useLadoSetupChange,
	type LadoSetupChangeRequest,
	type LadoSetupConflict,
	type LadoSetupInventory,
} from "../../../lib/lado-setup";
import { cn } from "../../../lib/utils";
import { SettingsRow } from "../../settings/SettingsRow";
import { SettingsSection } from "../../settings/SettingsSection";
import { Button } from "../../ui/button";
import { LadoRuleImpactDialog } from "../LadoRuleImpactDialog";
import { LadoSetupDriftDialog } from "./LadoSetupDriftReview";
import { LadoSetupItemEditor } from "./LadoSetupItemEditor";
import { ladoSetupConflictsBySeverity, ladoSetupSeverityDotClass, ladoSetupSeverityKeys, ladoSetupShortPath } from "./lado-setup-view";

function conflictKey(conflict: LadoSetupConflict): string {
	return [conflict.code, conflict.itemId, conflict.path, conflict.detail].join("\n");
}

export function LadoSetupConflictsCard({ conflicts, inventory }: { conflicts: readonly LadoSetupConflict[]; inventory: LadoSetupInventory }) {
	const { t } = useTranslation();
	const change = useLadoSetupChange();
	const [changing, setChanging] = useState<string | null>(null);
	const [failure, setFailure] = useState<{ key: string; error: unknown } | null>(null);
	// Each opening mounts the editor or the drift review again, so it starts
	// from the item as it is then.
	const [editing, setEditing] = useState<{ key: number; open: boolean; view: "edit" | "drift"; itemId: string | null }>({
		key: 0,
		open: false,
		view: "edit",
		itemId: null,
	});

	const items = new Map(inventory.items.map((item) => [item.itemId, item]));
	// A managed copy changed by hand stops an edit until it is kept or reverted.
	const edited = new Map(inventory.drift.filter((drift) => drift.kind === "edited").map((drift) => [drift.itemId, drift]));
	const open = (view: "edit" | "drift", itemId: string) => setEditing((current) => ({ key: current.key + 1, open: true, view, itemId }));
	const run = (key: string, request: LadoSetupChangeRequest) => {
		if (change.isPending) return;
		setChanging(key);
		setFailure(null);
		change
			.change(request)
			.catch((error: unknown) => setFailure({ key, error }))
			.finally(() => setChanging(null));
	};

	return (
		<>
			{/* The editor, the drift review and the impact dialog stay open when the last conflict goes away. */}
			{conflicts.length > 0 ? (
				<SettingsSection title={t("lado.setup.conflicts.title")} grouped testId="lado-setup-conflicts">
					{ladoSetupConflictsBySeverity(conflicts).map((conflict) => {
						const key = conflictKey(conflict);
						const item = conflict.itemId ? items.get(conflict.itemId) : undefined;
						// The item it is about, or where setup without an item lives.
						const target = item
							? [agentLabel(item.tool), ladoSetupKindLabel(t, item.kind), item.name].join(" · ")
							: ladoSetupShortPath(conflict.path, inventory);
						const accepted = Boolean(conflict.acceptanceId);
						const canTurnOff = item?.global === "on";
						const canAccept = conflict.severity === "warning" && !accepted && Boolean(item);
						// Revoking needs no item: the acceptance names its own.
						const canRevoke = conflict.severity === "warning" && accepted;
						const busy = changing === key;
						const error = failure?.key === key ? failure.error : undefined;
						return (
							<SettingsRow
								key={key}
								data-testid="lado-setup-conflict"
								data-code={conflict.code}
								data-severity={conflict.severity}
								label={
									<span className="flex min-w-0 items-baseline gap-2">
										<span
											className={cn("size-1.5 shrink-0 translate-y-[-1px] rounded-full", ladoSetupSeverityDotClass[conflict.severity])}
											aria-hidden="true"
										/>
										<span className="min-w-0">
											<span className="font-medium">{t(ladoSetupSeverityKeys[conflict.severity])}</span>
											<span className="text-settings-muted"> · </span>
											{ladoSetupConflictText(t, conflict)}
										</span>
									</span>
								}
								description={
									<>
										<span className="block truncate" title={conflict.path || undefined}>
											{accepted ? `${target} · ${t("lado.setup.conflicts.accepted")}` : target}
										</span>
										{error ? (
											<span role="alert" className="block text-error">
												{ladoErrorText(t, error)}
											</span>
										) : null}
									</>
								}
							>
								{item || canTurnOff || canAccept || canRevoke ? (
									<div className="flex items-center gap-1">
										{item ? (
											<Button
												type="button"
												variant="ghost"
												size="sm"
												className="text-settings-muted hover:text-settings-label"
												aria-label={t("lado.setup.conflicts.editLabel", { name: item.name })}
												onClick={() => open("edit", item.itemId)}
											>
												<span className="text-control">{t("lado.rules.edit")}</span>
											</Button>
										) : null}
										{canTurnOff && item ? (
											<Button
												type="button"
												variant="ghost"
												size="sm"
												className="text-settings-muted hover:text-settings-label"
												aria-label={t("lado.setup.conflicts.turnOffLabel", { name: item.name })}
												disabled={busy}
												onClick={() =>
													run(key, { op: "binding", input: { itemId: item.itemId, scope: "global", scopeId: "", state: "off" } })
												}
											>
												<span className="text-control">{t("lado.setup.conflicts.turnOff")}</span>
											</Button>
										) : null}
										{canAccept ? (
											<Button
												type="button"
												variant="ghost"
												size="sm"
												className="text-settings-muted hover:text-settings-label"
												aria-label={t("lado.setup.conflicts.acceptLabel", { name: item?.name ?? "" })}
												disabled={busy}
												onClick={() =>
													run(key, {
														op: "accept",
														input: { itemId: conflict.itemId, code: conflict.code, detail: conflict.detail, scope: "global", scopeId: "" },
													})
												}
											>
												<span className="text-control">{t("lado.setup.conflicts.accept")}</span>
											</Button>
										) : null}
										{canRevoke ? (
											<Button
												type="button"
												variant="ghost"
												size="sm"
												className="text-settings-muted hover:text-settings-label"
												aria-label={t("lado.setup.conflicts.revokeLabel", { name: item?.name ?? target })}
												disabled={busy}
												onClick={() => run(key, { op: "revoke", acceptanceId: conflict.acceptanceId })}
											>
												<span className="text-control">{t("lado.setup.conflicts.revoke")}</span>
											</Button>
										) : null}
									</div>
								) : null}
							</SettingsRow>
						);
					})}
				</SettingsSection>
			) : null}
			{editing.view === "edit" ? (
				<LadoSetupItemEditor
					key={editing.key}
					itemId={editing.itemId}
					open={editing.open}
					onOpenChange={(next) => setEditing((current) => ({ ...current, open: next }))}
					onReviewDrift={editing.itemId && edited.has(editing.itemId) ? () => open("drift", editing.itemId ?? "") : undefined}
				/>
			) : (
				<LadoSetupDriftDialog
					key={editing.key}
					drift={(editing.itemId && edited.get(editing.itemId)) || null}
					latestVersion={editing.itemId ? items.get(editing.itemId)?.version : undefined}
					open={editing.open}
					onOpenChange={(next) => setEditing((current) => ({ ...current, open: next }))}
				/>
			)}
			<LadoRuleImpactDialog
				impact={change.pending?.result.impact ?? null}
				busy={change.isPending}
				error={change.pending ? change.error : null}
				title={t("lado.setup.impact.title")}
				onConfirm={() => void change.confirm().catch(() => undefined)}
				onCancel={change.cancel}
			/>
		</>
	);
}
