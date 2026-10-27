// LADO fork: Settings → Local setup (docs/LJY-PRD/HARNESS.md §9 and §10). The
// developer's Claude Code and Codex setup in one place: what LADO found at the
// last scan, the conflicts of what it manages, what changed outside the app,
// one card per tool, adopting what it can take, new items made here and
// restoring an adoption. Adopted items reach only the sessions AO starts, and
// adoption and new items stay off on an OS where LADO has not checked that
// they do.
import { FolderInput, Plus, RefreshCw } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLadoSetupConflicts, useLadoSetupInventory, useScanLadoSetup } from "../../hooks/useLadoSetup";
import { ladoErrorText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
import { LadoSetupAdoptDialog } from "../lado/setup/LadoSetupAdoptDialog";
import { LadoSetupAdoptionsCard } from "../lado/setup/LadoSetupAdoptionsCard";
import { LadoSetupConflictsCard } from "../lado/setup/LadoSetupConflictsCard";
import { LadoSetupCreateDialog } from "../lado/setup/LadoSetupCreateDialog";
import { LadoSetupDriftCard } from "../lado/setup/LadoSetupDriftReview";
import { LadoSetupItemList } from "../lado/setup/LadoSetupItemList";
import { ladoSetupAdoptable, ladoSetupOSLabel, ladoSetupShownEntries, ladoSetupTools } from "../lado/setup/lado-setup-view";
import { Button } from "../ui/button";
import { formatResetTime } from "./CodexAccountDetails";
import { SettingsSection } from "./SettingsSection";

export function LocalSetupSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t, i18n } = useTranslation();
	const inventory = useLadoSetupInventory();
	const conflicts = useLadoSetupConflicts();
	const scan = useScanLadoSetup();
	const osLineId = useId();
	// Each opening mounts a dialog again, so it starts from the inventory as it is
	// then; only names the one entry an adoption from drift takes.
	const [adopting, setAdopting] = useState<{ key: number; open: boolean; only?: string }>({ key: 0, open: false });
	const [creating, setCreating] = useState({ key: 0, open: false });

	const data = inventory.data;
	const unverified = data ? ladoSetupTools.filter((tool) => !data.verified[tool]) : [];
	const adoptable = data ? ladoSetupAdoptable(data) : [];
	const adoptableHere = adoptable.filter((entry) => data?.verified[entry.tool]);
	const scannedAt = data ? formatResetTime(data.scannedAt, i18n.language) : null;
	const empty =
		data !== undefined &&
		ladoSetupTools.every((tool) => ladoSetupShownEntries(data, tool).length === 0) &&
		data.adoptions.length === 0 &&
		data.roots.every((root) => !root.error);

	const head = (
		<div className="flex flex-col gap-2 px-(--settings-row-inset) pb-(--size-settings-section-inner-gap)">
			<p className="text-control leading-4 text-settings-muted">{t("lado.setup.section.description")}</p>
			<div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
				<span className="text-xs tabular-nums text-settings-muted" title={scannedAt?.full}>
					{scannedAt ? t("lado.setup.section.scannedAt", { time: scannedAt.visible }) : null}
				</span>
				<div className="flex items-center gap-1">
					<Button
						type="button"
						variant="ghost"
						size="sm"
						className="text-settings-muted hover:text-settings-label"
						disabled={scan.isPending || inventory.isPending}
						onClick={() => scan.mutate()}
					>
						<RefreshCw aria-hidden="true" className={cn(scan.isPending && "motion-safe:animate-spin")} />
						<span className="text-control">{scan.isPending ? t("lado.setup.section.scanning") : t("lado.setup.section.scan")}</span>
					</Button>
					{adoptable.length > 0 ? (
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="text-settings-muted hover:text-settings-label"
							disabled={adoptableHere.length === 0}
							aria-describedby={unverified.length > 0 ? osLineId : undefined}
							onClick={() => setAdopting((current) => ({ key: current.key + 1, open: true }))}
						>
							<FolderInput aria-hidden="true" />
							<span className="text-control">{t("lado.setup.section.adopt")}</span>
						</Button>
					) : null}
					{data ? (
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="text-settings-muted hover:text-settings-label"
							// The daemon refuses new items where no tool's delivery was checked.
							disabled={unverified.length === ladoSetupTools.length}
							aria-describedby={unverified.length > 0 ? osLineId : undefined}
							onClick={() => setCreating((current) => ({ key: current.key + 1, open: true }))}
						>
							<Plus aria-hidden="true" />
							<span className="text-control">{t("lado.setup.create.action")}</span>
						</Button>
					) : null}
				</div>
			</div>
			{data && unverified.length > 0 ? (
				<p id={osLineId} className="text-caption leading-4 text-[var(--color-status-in-review)]" data-testid="lado-setup-os">
					{t("lado.setup.section.osUnverified", { os: ladoSetupOSLabel(data.os) })}
				</p>
			) : null}
			{scan.isError ? (
				<p role="alert" className="text-caption leading-4 text-error">
					{ladoErrorText(t, scan.error)}
				</p>
			) : null}
		</div>
	);

	return (
		<>
			<SettingsSection title={t("lado.setup.section.title")} sectionId="localSetup" titleHidden={titleHidden} toolbar={head}>
				{inventory.isPending ? (
					<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
						{t("common.loading")}
					</p>
				) : !data ? (
					<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
						{ladoErrorText(t, inventory.error)}
					</p>
				) : empty ? (
					<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
						{t("lado.setup.section.empty")}
					</p>
				) : (
					<div className="flex w-full flex-col gap-(--size-settings-section-gap)">
						{conflicts.isError ? (
							<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
								{ladoErrorText(t, conflicts.error)}
							</p>
						) : (
							<LadoSetupConflictsCard conflicts={conflicts.data ?? []} inventory={data} />
						)}
						<LadoSetupDriftCard
							inventory={data}
							onAdopt={(only) => setAdopting((current) => ({ key: current.key + 1, open: true, only }))}
						/>
						{ladoSetupTools.map((tool) => (
							<LadoSetupItemList key={tool} tool={tool} inventory={data} />
						))}
						<LadoSetupAdoptionsCard inventory={data} />
					</div>
				)}
			</SettingsSection>
			{data ? (
				<LadoSetupAdoptDialog
					key={adopting.key}
					open={adopting.open}
					inventory={data}
					only={adopting.only}
					onOpenChange={(open) => setAdopting((current) => ({ ...current, open }))}
				/>
			) : null}
			<LadoSetupCreateDialog
				key={`create-${creating.key}`}
				open={creating.open}
				onOpenChange={(open) => setCreating((current) => ({ ...current, open }))}
			/>
		</>
	);
}
