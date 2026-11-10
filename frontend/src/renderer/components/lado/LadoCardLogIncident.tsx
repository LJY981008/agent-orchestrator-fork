// LADO fork: what the board and the task view say of a task the production
// log watch created (issue #87). The board reads the project's incidents once
// for all its cards, only while a log task is in the lanes; the log watch
// events refresh them. A card shows "Production error · n×" while its task is
// open; the task view also lists the instances that logged the error and lets
// the developer ignore it, so it creates no further issue.
import { TriangleAlert } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useLadoLogIncidents, useSetLadoLogFingerprintIgnored } from "../../hooks/useLado";
import { formatTimeCompact } from "../../lib/format-time";
import type { LadoTask } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { ladoLogIncidentsByTask, type LadoLogFingerprint } from "../../lib/lado-log-watch";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";

/** Each task's fingerprint from the project's incidents; read only while enabled. */
export function useLadoBoardLogIncidents(projectId: string, enabled: boolean): (taskId: string) => LadoLogFingerprint | undefined {
	const incidents = useLadoLogIncidents(projectId, enabled);
	return useMemo(() => {
		const byTask = ladoLogIncidentsByTask(incidents.data);
		return (taskId: string) => byTask.get(taskId);
	}, [incidents.data]);
}

/** The card's mark: "Production error", with how often LADO saw it once the incidents are read. */
export function LadoCardLogIncident({ count, className }: { count?: number; className?: string }) {
	const { t } = useTranslation();
	const label = count === undefined ? t("lado.card.logIncidentNoCount") : t("lado.card.logIncident", { count });
	return (
		<span
			className={cn("inline-flex min-w-0 shrink items-center gap-1 font-sans text-2xs font-medium text-[var(--color-status-exited)]", className)}
			title={t("lado.card.logIncidentHint")}
			data-testid="lado-card-log-incident"
		>
			<TriangleAlert className="size-icon-2xs shrink-0" aria-hidden="true" />
			<span className="truncate tabular-nums">{label}</span>
		</span>
	);
}

/** The task view's incident: its count, service, instances, and the ignore action. */
export function LadoTaskLogIncident({ projectId, task }: { projectId: string; task: Pick<LadoTask, "id" | "source"> }) {
	const { t } = useTranslation();
	const incidents = useLadoLogIncidents(projectId, task.source === "log");
	const setIgnored = useSetLadoLogFingerprintIgnored(projectId);
	if (task.source !== "log") return null;
	const fingerprint = ladoLogIncidentsByTask(incidents.data).get(task.id);
	if (!fingerprint) {
		return incidents.isError ? (
			<p className="text-caption leading-4 text-error" role="alert">
				{ladoErrorText(t, incidents.error)}
			</p>
		) : null;
	}
	const ignored = setIgnored.isPending && setIgnored.variables ? setIgnored.variables.ignored : fingerprint.ignored;
	return (
		<div className="flex flex-col gap-2 rounded-md border border-[var(--color-status-exited)] px-3 py-2.5" data-testid="lado-task-log-incident">
			<div className="flex min-w-0 items-center gap-2">
				<LadoCardLogIncident count={fingerprint.count} className="text-xs" />
				<Button
					type="button"
					variant="outline"
					size="sm"
					className="ml-auto shrink-0"
					disabled={setIgnored.isPending}
					data-testid="lado-log-ignore"
					onClick={() => setIgnored.mutate({ fingerprintId: fingerprint.id, ignored: !ignored })}
				>
					<span className="text-control">{ignored ? t("lado.logIncident.unignore") : t("lado.logIncident.ignore")}</span>
				</Button>
			</div>
			<dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-caption leading-4">
				<dt className="text-settings-muted">{t("lado.logIncident.service")}</dt>
				<dd className="truncate font-mono text-foreground">{fingerprint.service}</dd>
				<dt className="text-settings-muted">{t("lado.logIncident.exception")}</dt>
				<dd className="truncate font-mono text-foreground" title={fingerprint.location}>
					{fingerprint.exceptionKind}
					{fingerprint.location ? ` · ${fingerprint.location}` : ""}
				</dd>
				<dt className="text-settings-muted">{t("lado.logIncident.seen")}</dt>
				<dd className="text-foreground">
					{t("lado.logIncident.seenRange", {
						first: formatTimeCompact(fingerprint.firstSeenAt),
						last: formatTimeCompact(fingerprint.lastSeenAt),
					})}
				</dd>
				<dt className="text-settings-muted">{t("lado.logIncident.instances")}</dt>
				<dd className="text-foreground" data-testid="lado-log-instances">
					{t("lado.logIncident.instanceCount", { count: fingerprint.instances.length })}
				</dd>
			</dl>
			{fingerprint.instances.length > 0 ? (
				<ul className="flex flex-col gap-0.5 text-caption leading-4">
					{fingerprint.instances.map((instance) => (
						<li key={instance.instance} className="flex min-w-0 items-center gap-2" data-testid="lado-log-instance">
							<span className="min-w-0 truncate font-mono text-foreground">{instance.instance}</span>
							{instance.image ? <span className="min-w-0 truncate font-mono text-settings-muted">{instance.image}</span> : null}
							<span className="ml-auto shrink-0 tabular-nums text-settings-muted">{t("lado.logIncident.times", { count: instance.count })}</span>
						</li>
					))}
				</ul>
			) : null}
			{ignored ? <p className="text-caption leading-4 text-settings-muted">{t("lado.logIncident.ignoredHint")}</p> : null}
			{setIgnored.isError ? (
				<p className="text-caption leading-4 text-error" role="alert">
					{ladoErrorText(t, setIgnored.error)}
				</p>
			) : null}
		</div>
	);
}
