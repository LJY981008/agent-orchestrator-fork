// LADO fork: the adoptions of local setup and their restore
// (docs/LJY-PRD/HARNESS.md §9). Each adoption says when it ran, how many items
// it took and whether it is under way, finished, stopped partway or was
// restored. Before a restore it names the Claude Code and Codex sessions and
// programs that hold it back, with Check again. Restore puts the originals
// back through the desktop app's confirmation, and its report says what went
// where, including a copy put beside something new.
import { agentLabel } from "@aoagents/product-ui";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useLadoSetupAdoption, useRestoreLadoSetup } from "../../../hooks/useLadoSetup";
import { ladoErrorText, ladoSetupRestoreNoteText, ladoSetupRestoreStatusText, ladoSetupWarningText } from "../../../lib/lado-errors";
import type { LadoSetupAdoption, LadoSetupInventory, LadoSetupRestoreResult } from "../../../lib/lado-setup";
import { formatResetTime } from "../../settings/CodexAccountDetails";
import { SettingsRow } from "../../settings/SettingsRow";
import { SettingsSection } from "../../settings/SettingsSection";
import { Button } from "../../ui/button";
import { ladoSetupRootedPath } from "./lado-setup-view";

/** Newest first; the daemon lists them oldest first. */
function newestFirst(adoptions: readonly LadoSetupAdoption[]): LadoSetupAdoption[] {
	return [...adoptions].sort((a, b) => b.createdAt.localeCompare(a.createdAt) || b.id.localeCompare(a.id));
}

export function LadoSetupAdoptionsCard({ inventory }: { inventory: LadoSetupInventory }) {
	const { t } = useTranslation();
	const restore = useRestoreLadoSetup();
	const [restoring, setRestoring] = useState<string | null>(null);
	const [reports, setReports] = useState<Record<string, LadoSetupRestoreResult>>({});
	const [failure, setFailure] = useState<{ adoptionId: string; error: unknown } | null>(null);
	if (inventory.adoptions.length === 0) return null;

	const restoreNow = (adoption: LadoSetupAdoption) => {
		if (restore.isPending) return;
		setRestoring(adoption.id);
		setFailure(null);
		restore
			.mutateAsync(adoption.id)
			.then((result) => {
				// null: the developer cancelled the desktop confirmation.
				if (result) setReports((current) => ({ ...current, [adoption.id]: result }));
			})
			.catch((error: unknown) => setFailure({ adoptionId: adoption.id, error }))
			.finally(() => setRestoring(null));
	};

	return (
		<SettingsSection title={t("lado.setup.adoptions.title")} grouped testId="lado-setup-adoptions">
			{newestFirst(inventory.adoptions).map((adoption) => (
				<AdoptionRow
					key={adoption.id}
					adoption={adoption}
					inventory={inventory}
					report={reports[adoption.id]}
					error={failure?.adoptionId === adoption.id ? failure.error : undefined}
					restoring={restoring === adoption.id}
					busy={restore.isPending}
					onRestore={() => restoreNow(adoption)}
				/>
			))}
		</SettingsSection>
	);
}

function AdoptionRow({
	adoption,
	inventory,
	report,
	error,
	restoring,
	busy,
	onRestore,
}: {
	adoption: LadoSetupAdoption;
	inventory: LadoSetupInventory;
	report: LadoSetupRestoreResult | undefined;
	error: unknown;
	restoring: boolean;
	busy: boolean;
	onRestore: () => void;
}) {
	const { t, i18n } = useTranslation();
	// An adoption under way is not restored until it ends.
	const restorable = !adoption.restoredAt && adoption.state !== "pending";
	// What holds a restore back now, as the daemon reads it for this adoption.
	const detail = useLadoSetupAdoption(restorable ? adoption.id : null);
	const preconditions = detail.data?.preconditions;
	const running = preconditions
		? [
				...preconditions.sessions.map((session) => `${t("lado.setup.adopt.session", { id: session.id })} (${agentLabel(session.tool)})`),
				...preconditions.processes.map((program) => t("lado.setup.adopt.process", { name: program.name, pid: program.pid })),
			]
		: [];
	const at = formatResetTime(adoption.createdAt, i18n.language);
	const restoredAt = formatResetTime(adoption.restoredAt, i18n.language);
	const state = restoredAt
		? t("lado.setup.adoptions.restored", { time: restoredAt.visible })
		: adoption.state === "pending"
			? t("lado.setup.adoptions.pending")
			: adoption.state === "partial"
				? t("lado.setup.adoptions.partial")
				: t("lado.setup.adoptions.completed");
	return (
		<div className="flex flex-col" data-testid="lado-setup-adoption" data-adoption-id={adoption.id}>
			<SettingsRow
				label={
					<span className="tabular-nums" title={at?.full}>
						{t("lado.setup.adoptions.adoptedAt", { time: at?.visible ?? adoption.createdAt })}
					</span>
				}
				description={
					<>
						<span className="block" title={adoption.state === "partial" ? adoption.errorMessage || undefined : restoredAt?.full}>
							{`${t("lado.setup.adoptions.items", { count: adoption.itemCount })} · ${state}`}
						</span>
						{restorable && running.length > 0 ? (
							<span className="block text-[var(--color-status-in-review)]" data-testid="lado-setup-adoption-running">
								{t("lado.setup.adoptions.running", { running: running.join(", ") })}
							</span>
						) : null}
						{error ? (
							<span role="alert" className="block text-error">
								{ladoErrorText(t, error)}
							</span>
						) : null}
					</>
				}
			>
				{restorable ? (
					<div className="flex items-center gap-1">
						{running.length > 0 ? (
							<Button
								type="button"
								variant="ghost"
								size="sm"
								className="text-settings-muted hover:text-settings-label"
								disabled={detail.isFetching}
								onClick={() => void detail.refetch()}
							>
								<span className="text-control">{t("lado.setup.adopt.checkAgain")}</span>
							</Button>
						) : null}
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="text-settings-muted hover:text-settings-label"
							aria-label={t("lado.setup.adoptions.restoreLabel", { time: at?.visible ?? adoption.createdAt })}
							disabled={busy || running.length > 0}
							onClick={onRestore}
						>
							<span className="text-control">{restoring ? t("lado.setup.adoptions.restoring") : t("lado.setup.adoptions.restore")}</span>
						</Button>
					</div>
				) : null}
			</SettingsRow>
			{report ? <RestoreReport report={report} inventory={inventory} /> : null}
		</div>
	);
}

/** What a restore did with each original, by its path under its tool folder. */
function RestoreReport({ report, inventory }: { report: LadoSetupRestoreResult; inventory: LadoSetupInventory }) {
	const { t } = useTranslation();
	return (
		<div className="settings-content-row flex flex-col gap-2 pt-0" data-testid="lado-setup-restore-report">
			<ul aria-label={t("lado.setup.adoptions.report")} className="flex flex-col gap-1.5">
				{report.report.entries.map((entry, index) => {
					const path = ladoSetupRootedPath(entry, entry.path, inventory);
					const beside = entry.beside ? ladoSetupRootedPath(entry, entry.beside, inventory) : null;
					return (
						<li
							key={`${entry.root}-${entry.path}-${index}`}
							className="flex min-w-0 flex-col text-caption leading-4"
							data-testid="lado-setup-restored-entry"
							data-status={entry.status}
						>
							<span className="min-w-0 truncate font-mono text-settings-label" title={path.full}>
								{path.short}
							</span>
							<span className={entry.status === "failed" ? "text-error" : "text-settings-muted"}>
								{[
									ladoSetupRestoreStatusText(t, entry.status),
									entry.status === "failed" ? entry.error : "",
									ladoSetupRestoreNoteText(t, entry.note),
								]
									.filter(Boolean)
									.join(" · ")}
							</span>
							{beside ? (
								<span className="min-w-0 truncate text-settings-muted" title={beside.full}>
									{t("lado.setup.adoptions.beside")} <span className="font-mono">{beside.short}</span>
								</span>
							) : null}
						</li>
					);
				})}
			</ul>
			{report.warnings.map((code) => (
				<p key={code} className="text-caption leading-4 text-[var(--color-status-in-review)]">
					{ladoSetupWarningText(t, code)}
				</p>
			))}
		</div>
	);
}
