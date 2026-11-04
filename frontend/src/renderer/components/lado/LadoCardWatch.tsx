// LADO fork: what the board's cards say of the worker watch (issue #42): the
// task's estimated cost in one line, and how many of its alerts nobody marked
// seen. The board reads the project's usage totals and unseen alerts once for
// all its cards; the watch and alert events refresh them, so a seen alert in
// any window takes the badge down.
import { Bell } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useLadoAlerts, useLadoProjectUsage } from "../../hooks/useLado";
import { ladoCostText, ladoUnseenAlertCounts } from "../../lib/lado-usage";
import { cn } from "../../lib/utils";

/** A task's watch on the board: its cost in nanodollars when its sessions spent any, and its unseen alerts. */
export type LadoTaskWatch = { costNanos?: number; unseen: number };

/** Each task's watch, from the project's usage totals and unseen alerts. */
export function useLadoBoardWatch(projectId: string, enabled = true): (taskId: string) => LadoTaskWatch {
	const usage = useLadoProjectUsage(projectId, enabled);
	const alerts = useLadoAlerts(projectId, { unseen: true }, enabled);
	return useMemo(() => {
		const costs = new Map<string, number>();
		for (const task of usage.data?.tasks ?? []) {
			if (task.totals.costNanos > 0) costs.set(task.taskId, task.totals.costNanos);
		}
		const unseen = ladoUnseenAlertCounts(alerts.data);
		return (taskId: string) => ({ costNanos: costs.get(taskId), unseen: unseen.get(taskId) ?? 0 });
	}, [usage.data, alerts.data]);
}

/** The task's cost, then its unseen alerts' badge; nothing without either. */
export function LadoCardWatch({
	watch,
	className,
	onOpenAlerts,
}: {
	watch: LadoTaskWatch;
	className?: string;
	/** Opens the task's alerts; without it the badge only shows. */
	onOpenAlerts?: () => void;
}) {
	const { t, i18n } = useTranslation();
	if (watch.costNanos === undefined && watch.unseen === 0) return null;
	const label = t("lado.card.alerts", { alerts: watch.unseen });
	// The app styles buttons outside Tailwind's layers, so the type sits on the span.
	const badge = (
		<span className="inline-flex items-center gap-0.5 text-2xs font-medium text-[var(--color-status-in-review)]">
			<Bell className="size-icon-2xs" aria-hidden="true" />
			<span className="tabular-nums">{watch.unseen}</span>
		</span>
	);
	const badgeClass = "inline-flex shrink-0 items-center rounded-sm";
	return (
		<span className={cn("inline-flex min-w-0 shrink-0 items-center gap-1.5", className)} data-testid="lado-card-watch">
			{watch.costNanos !== undefined ? (
				<span
					className="shrink-0 font-sans tabular-nums text-2xs text-muted-foreground"
					title={t("lado.card.costHint")}
					data-testid="lado-card-cost"
				>
					{ladoCostText(watch.costNanos, i18n.language)}
				</span>
			) : null}
			{watch.unseen > 0 ? (
				onOpenAlerts ? (
					<button
						type="button"
						className={cn(badgeClass, "outline-none hover:opacity-80 focus-visible:outline-2 focus-visible:outline-ring")}
						aria-label={label}
						title={label}
						data-testid="lado-card-alerts"
						onClick={onOpenAlerts}
					>
						{badge}
					</button>
				) : (
					<span className={badgeClass} role="img" aria-label={label} title={label} data-testid="lado-card-alerts">
						{badge}
					</span>
				)
			) : null}
		</span>
	);
}
