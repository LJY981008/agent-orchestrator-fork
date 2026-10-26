// LADO fork: one tool's card in Settings → Local setup (docs/LJY-PRD/HARNESS.md
// §9). What LADO manages and what it can or cannot adopt is grouped by kind,
// each entry with its state in one line and where it lives. A managed item
// has its Global switch, which the desktop app confirms for a kind that can
// run commands, and a menu. What LADO never adopts or leaves to the tool
// waits in a collapsed group under the card.
import { agentLabel } from "@aoagents/product-ui";
import { ChevronDown, MoreHorizontal, Trash2 } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ladoErrorText } from "../../../lib/lado-errors";
import {
	useLadoSetupChange,
	type LadoSetupChangeRequest,
	type LadoSetupEntry,
	type LadoSetupInventory,
	type LadoSetupTool,
} from "../../../lib/lado-setup";
import { cn } from "../../../lib/utils";
import { ConfirmDialog } from "../../ConfirmDialog";
import { SettingsRow } from "../../settings/SettingsRow";
import { SettingsSection } from "../../settings/SettingsSection";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../../ui/dropdown-menu";
import { Switch } from "../../ui/switch";
import { LadoRuleImpactDialog } from "../LadoRuleImpactDialog";
import {
	compareEntries,
	ladoSetupGroupKind,
	ladoSetupGroupLabel,
	ladoSetupKinds,
	ladoSetupManagedKeys,
	ladoSetupOtherEntries,
	ladoSetupShortPath,
	ladoSetupShownEntries,
	ladoSetupStatusText,
} from "./lado-setup-view";

type Deleting = { open: boolean; entry: LadoSetupEntry | null; error: unknown };

export function LadoSetupItemList({ tool, inventory }: { tool: LadoSetupTool; inventory: LadoSetupInventory }) {
	const { t } = useTranslation();
	const change = useLadoSetupChange();
	const [changing, setChanging] = useState<string | null>(null);
	const [failure, setFailure] = useState<{ itemId: string; error: unknown } | null>(null);
	const [deleting, setDeleting] = useState<Deleting>({ open: false, entry: null, error: null });
	const [otherOpen, setOtherOpen] = useState(false);

	const shown = ladoSetupShownEntries(inventory, tool).sort(compareEntries);
	const other = ladoSetupOtherEntries(inventory, tool).sort(compareEntries);
	const rootErrors = inventory.roots.filter((root) => root.tool === tool && root.error);
	if (shown.length === 0 && other.length === 0 && rootErrors.length === 0) return null;

	const context = {
		managed: ladoSetupManagedKeys(inventory),
		globalOff: new Set(inventory.items.filter((item) => item.global === "off").map((item) => item.itemId)),
	};
	const groups = [...ladoSetupKinds, "" as const]
		.map((kind) => ({ kind, entries: shown.filter((entry) => ladoSetupGroupKind(entry.kind) === kind) }))
		.filter((group) => group.entries.length > 0);

	// Every change runs as a dry run first (useLadoSetupChange); one at a time.
	const run = (itemId: string, request: LadoSetupChangeRequest) => {
		if (change.isPending) return;
		setChanging(itemId);
		setFailure(null);
		change
			.change(request)
			.catch((error: unknown) => setFailure({ itemId, error }))
			.finally(() => setChanging(null));
	};

	const remove = () => {
		const entry = deleting.entry;
		if (!entry || change.isPending) return;
		setChanging(entry.itemId);
		setDeleting((current) => ({ ...current, error: null }));
		change
			.change({ op: "delete", itemId: entry.itemId })
			// A delete that reaches approved tasks continues in the impact dialog.
			.then(() => setDeleting((current) => ({ ...current, open: false })))
			.catch((error: unknown) => setDeleting((current) => ({ ...current, error })))
			.finally(() => setChanging(null));
	};

	const row = (entry: LadoSetupEntry) => (
		<EntryRow
			key={`${entry.location}:${entry.path}`}
			entry={entry}
			status={ladoSetupStatusText(t, entry, context)}
			path={ladoSetupShortPath(entry.path, inventory)}
			busy={changing === entry.itemId}
			error={entry.itemId && failure?.itemId === entry.itemId ? failure.error : undefined}
			globalOn={!context.globalOff.has(entry.itemId)}
			onGlobalChange={(on) =>
				run(entry.itemId, { op: "binding", input: { itemId: entry.itemId, scope: "global", scopeId: "", state: on ? "on" : "off" } })
			}
			onDelete={() => setDeleting({ open: true, entry, error: null })}
		/>
	);

	return (
		<>
			<div className="flex w-full flex-col gap-(--size-settings-section-inner-gap)" data-testid="lado-setup-tool" data-tool={tool}>
				<SettingsSection title={agentLabel(tool)} grouped>
					{rootErrors.map((root) => (
						<p key={root.location} role="alert" className="settings-content-row text-control leading-4 text-error">
							{t("lado.setup.list.rootError", { path: ladoSetupShortPath(root.path, inventory), error: root.error })}
						</p>
					))}
					{groups.map((group) => (
						<KindGroup key={group.kind || "other"} label={ladoSetupGroupLabel(t, group.kind)} kind={group.kind}>
							{group.entries.map(row)}
						</KindGroup>
					))}
					{groups.length === 0 && rootErrors.length === 0 ? (
						<p className="settings-content-row text-control leading-4 text-settings-muted">{t("lado.setup.list.nothing")}</p>
					) : null}
				</SettingsSection>
				{other.length > 0 ? (
					<section className="flex w-full flex-col items-stretch gap-(--size-settings-section-inner-gap)">
						<button
							type="button"
							aria-expanded={otherOpen}
							onClick={() => setOtherOpen((open) => !open)}
							className="flex h-5 w-fit items-center gap-1.5 rounded-sm px-(--settings-row-inset) text-foreground"
						>
							<span className="text-sm font-medium leading-5">{t("lado.setup.list.otherGroup")}</span>
							<span className="text-sm leading-5 tabular-nums text-settings-muted">{other.length}</span>
							<ChevronDown
								aria-hidden="true"
								className={cn("size-icon-sm text-settings-muted transition-transform duration-fast", otherOpen && "rotate-180")}
							/>
						</button>
						{otherOpen ? <div className="settings-grouped-rows flex w-full flex-col">{other.map(row)}</div> : null}
					</section>
				) : null}
			</div>
			<LadoRuleImpactDialog
				impact={change.pending?.result.impact ?? null}
				busy={change.isPending}
				error={change.pending ? change.error : null}
				title={t("lado.setup.impact.title")}
				onConfirm={() => void change.confirm().catch(() => undefined)}
				onCancel={change.cancel}
			/>
			<ConfirmDialog
				open={deleting.open}
				destructive
				title={t("lado.setup.delete.title", { name: deleting.entry?.name ?? "" })}
				description={t("lado.setup.delete.description")}
				confirmLabel={t("lado.setup.delete.action")}
				busy={change.isPending}
				error={deleting.error ? ladoErrorText(t, deleting.error) : null}
				onConfirm={remove}
				onOpenChange={(open) => {
					if (!open) setDeleting((current) => ({ ...current, open: false }));
				}}
			/>
		</>
	);
}

/** One kind's entries under a quiet heading; dividers separate the entries, not the heading. */
function KindGroup({ label, kind, children }: { label: string; kind: string; children: ReactNode }) {
	const labelId = useId();
	return (
		<div role="group" aria-labelledby={labelId} data-testid="lado-setup-kind" data-kind={kind || "other"} className="flex flex-col">
			<p id={labelId} className="px-(--settings-row-inset) pt-2.5 text-xs font-medium leading-4 text-settings-muted">
				{label}
			</p>
			<div className="flex flex-col divide-y divide-border">{children}</div>
		</div>
	);
}

/**
 * An entry: its name, its state in one line and where it lives. A managed
 * item adds its Global switch and its menu; a failed change shows under it.
 */
function EntryRow({
	entry,
	status,
	path,
	busy,
	error,
	globalOn,
	onGlobalChange,
	onDelete,
}: {
	entry: LadoSetupEntry;
	status: string;
	path: string;
	busy: boolean;
	error?: unknown;
	globalOn: boolean;
	onGlobalChange: (on: boolean) => void;
	onDelete: () => void;
}) {
	const { t } = useTranslation();
	const managed = entry.status === "managed" && Boolean(entry.itemId);
	return (
		<SettingsRow
			data-testid="lado-setup-entry"
			data-path={entry.path}
			data-status={entry.status}
			label={entry.name}
			description={
				<>
					<span className="block">{status}</span>
					<span className="block truncate font-mono text-xs" title={entry.path}>
						{path}
					</span>
					{error ? (
						<span role="alert" className="block text-error">
							{ladoErrorText(t, error)}
						</span>
					) : null}
				</>
			}
		>
			{managed ? (
				<div className="flex items-center gap-2">
					<Switch
						aria-label={t("lado.setup.list.global", { name: entry.name })}
						checked={globalOn}
						disabled={busy}
						onCheckedChange={onGlobalChange}
					/>
					<DropdownMenu>
						<DropdownMenuTrigger asChild disabled={busy}>
							<button
								type="button"
								aria-label={t("lado.setup.list.actions", { name: entry.name })}
								className="settings-close-button shrink-0 focus-visible:bg-(--color-bg-settings-row-hover) focus-visible:text-settings-title data-[state=open]:bg-(--color-bg-settings-row-hover) data-[state=open]:text-settings-title"
							>
								<MoreHorizontal aria-hidden="true" className="size-4" />
							</button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="min-w-40">
							<DropdownMenuItem
								className="text-destructive focus:text-destructive [&_svg]:text-destructive focus:[&_svg]:text-destructive"
								onSelect={onDelete}
							>
								<Trash2 aria-hidden="true" />
								{t("lado.setup.delete.action")}
							</DropdownMenuItem>
						</DropdownMenuContent>
					</DropdownMenu>
				</div>
			) : null}
		</SettingsRow>
	);
}
