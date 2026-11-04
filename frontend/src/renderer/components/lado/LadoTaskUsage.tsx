// LADO fork: the task view's Usage tab (issue #42): what the task's sessions
// spend now. The totals (estimated cost at API prices, tokens, the subagents'
// share, the elapsed time), the task's alerts, and each plan and execution
// session's cost, tokens, context and compactions. The Timings tab measures
// where the time went; this tab watches the spending, so it reads again every
// minute while it is open and on the task's watch and alert events. Both name
// sessions with the same role labels and show the same elapsed time once the
// task merged; the investigation metrics stay on the Timings tab.
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useLadoAlerts, useLadoTaskUsage, useMarkLadoAlertsSeen } from "../../hooks/useLado";
import type { LadoAlert, LadoSessionUsage, LadoTaskUsageSummary, LadoUsageTotals } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { ladoAlertText, ladoCostText, ladoTokensText } from "../../lib/lado-usage";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../ui/table";
import { ladoDurationText, roleKeys } from "./LadoTaskTimings";

const endKeys = {
	merged: "lado.usage.end.merged",
	cancelled: "lado.usage.end.cancelled",
	closed: "lado.usage.end.closed",
	in_progress: "lado.usage.end.inProgress",
} as const satisfies Record<LadoTaskUsageSummary["endReason"], string>;

export function LadoTaskUsage({ projectId, taskId }: { projectId: string; taskId: string }) {
	const { t } = useTranslation();
	const usage = useLadoTaskUsage(taskId);
	if (usage.isPending) {
		return <p className="text-control text-settings-muted" role="status">{t("common.loading")}</p>;
	}
	if (usage.isError) {
		return <p className="text-caption leading-4 text-error" role="alert">{ladoErrorText(t, usage.error)}</p>;
	}
	return (
		<div className="flex flex-col gap-5" data-testid="lado-task-usage">
			<Summary summary={usage.data.task} />
			<Alerts projectId={projectId} taskId={taskId} />
			<Sessions sessions={usage.data.sessions} />
		</div>
	);
}

function tokenTotal(totals: LadoUsageTotals): number {
	return totals.inputTokens + totals.outputTokens;
}

function Summary({ summary }: { summary: LadoTaskUsageSummary }) {
	const { t, i18n } = useTranslation();
	const locale = i18n.language;
	const totals = summary.totals;
	const share = totals.costNanos > 0 ? Math.round((totals.subagentCostNanos / totals.costNanos) * 100) : 0;
	return (
		<section className="flex flex-col gap-2" aria-label={t("lado.usage.summary")}>
			<div className="flex min-w-0 items-baseline gap-3">
				<h4 className="text-xs font-semibold text-foreground">{t("lado.usage.summary")}</h4>
				<span className="ml-auto flex items-center gap-1.5 text-xs text-muted-foreground" data-testid="lado-usage-end">
					{summary.inProgress ? (
						<span className="size-1.5 rounded-full bg-[var(--color-status-working)]" aria-hidden="true" />
					) : null}
					{t(endKeys[summary.endReason])}
				</span>
			</div>
			<dl className="grid grid-cols-2 gap-px overflow-hidden rounded-md border border-[var(--color-border-settings-dialog)] bg-[var(--color-border-settings-dialog)] sm:grid-cols-4">
				<Stat label={t("lado.usage.cost")} value={ladoCostText(totals.costNanos, locale)} testId="lado-usage-cost" />
				<Stat
					label={t("lado.usage.tokens")}
					value={ladoTokensText(tokenTotal(totals), locale)}
					detail={t("lado.usage.tokensDetail", {
						input: ladoTokensText(totals.inputTokens, locale),
						cached: ladoTokensText(totals.cachedInputTokens, locale),
						output: ladoTokensText(totals.outputTokens, locale),
					})}
					testId="lado-usage-tokens"
				/>
				<Stat
					label={t("lado.usage.subagents")}
					value={ladoCostText(totals.subagentCostNanos, locale)}
					detail={t("lado.usage.subagentsShare", { percent: share })}
					testId="lado-usage-subagents"
				/>
				<Stat
					label={t("lado.usage.elapsed")}
					value={ladoDurationText(t, summary.elapsedMs)}
					detail={summary.inProgress ? t("lado.usage.inProgress") : undefined}
					testId="lado-usage-elapsed"
				/>
			</dl>
			<p className="text-caption leading-4 text-settings-muted" data-testid="lado-usage-estimate-note">{t("lado.usage.estimateNote")}</p>
			{summary.sessionsMissing > 0 ? (
				<p className="text-caption leading-4 text-settings-muted">{t("lado.usage.sessionsMissing", { sessions: summary.sessionsMissing })}</p>
			) : null}
			{summary.incomplete ? <p className="text-caption leading-4 text-settings-muted">{t("lado.usage.incomplete")}</p> : null}
		</section>
	);
}

function Stat({ label, value, detail, testId }: { label: string; value: string; detail?: string; testId: string }) {
	return (
		<div className="flex min-w-0 flex-col gap-0.5 bg-surface px-3 py-2">
			<dt className="truncate text-2xs text-muted-foreground">{label}</dt>
			<dd className="truncate text-sm font-semibold tabular-nums text-foreground" data-testid={testId}>{value}</dd>
			{detail ? <dd className="truncate text-2xs tabular-nums text-muted-foreground" title={detail}>{detail}</dd> : null}
		</div>
	);
}

function Alerts({ projectId, taskId }: { projectId: string; taskId: string }) {
	const { t, i18n } = useTranslation();
	const alerts = useLadoAlerts(projectId, { taskId });
	const markSeen = useMarkLadoAlertsSeen(projectId);
	const unseen = (alerts.data ?? []).filter((alert) => !alert.seenAt).map((alert) => alert.id);
	return (
		<section className="flex flex-col gap-2" aria-label={t("lado.usage.alerts")}>
			<div className="flex min-w-0 items-center gap-3">
				<h4 className="text-xs font-semibold text-foreground">{t("lado.usage.alerts")}</h4>
				{unseen.length > 1 ? (
					<Button
						type="button"
						variant="ghost"
						size="sm"
						className="ml-auto h-6 px-2 text-muted-foreground hover:text-foreground"
						disabled={markSeen.isPending}
						onClick={() => markSeen.mutate(unseen)}
					>
						<span className="text-2xs">{t("lado.usage.alertsSeenAll")}</span>
					</Button>
				) : null}
			</div>
			{alerts.isPending ? (
				<p className="text-xs text-muted-foreground" role="status">{t("common.loading")}</p>
			) : alerts.isError ? (
				<p className="text-caption leading-4 text-error" role="alert">{ladoErrorText(t, alerts.error)}</p>
			) : alerts.data.length === 0 ? (
				<p className="text-xs text-muted-foreground">{t("lado.usage.noAlerts")}</p>
			) : (
				<ul className="flex flex-col divide-y divide-[var(--color-border-settings-dialog)] overflow-hidden rounded-md border border-[var(--color-border-settings-dialog)]">
					{alerts.data.map((alert) => (
						<AlertRow
							key={alert.id}
							alert={alert}
							text={ladoAlertText(t, alert, i18n.language)}
							time={new Date(alert.createdAt).toLocaleString(i18n.language, { dateStyle: "medium", timeStyle: "short" })}
							busy={markSeen.isPending}
							onSeen={() => markSeen.mutate([alert.id])}
						/>
					))}
				</ul>
			)}
			{markSeen.isError ? <p className="text-caption leading-4 text-error" role="alert">{ladoErrorText(t, markSeen.error)}</p> : null}
		</section>
	);
}

function AlertRow({
	alert,
	text,
	time,
	busy,
	onSeen,
}: {
	alert: LadoAlert;
	text: string;
	time: string;
	busy: boolean;
	onSeen: () => void;
}) {
	const { t } = useTranslation();
	const seen = Boolean(alert.seenAt);
	return (
		<li
			className="flex min-w-0 items-center gap-2 px-3 py-2 text-xs leading-5"
			data-testid="lado-usage-alert"
			data-alert-id={alert.id}
			data-seen={seen}
		>
			<span
				className={cn("size-1.5 shrink-0 rounded-full", seen ? "bg-[var(--color-status-idle)]" : "bg-[var(--color-status-in-review)]")}
				aria-hidden="true"
			/>
			<span className={cn("min-w-0 flex-1 truncate", seen ? "text-muted-foreground" : "text-foreground")} title={text}>
				{text}
			</span>
			{alert.sessionId ? (
				<span className="hidden shrink-0 font-mono text-2xs text-passive sm:inline" title={alert.sessionId}>{alert.sessionId}</span>
			) : null}
			<span className="shrink-0 tabular-nums text-2xs text-muted-foreground">{time}</span>
			{seen ? null : (
				<Button
					type="button"
					variant="ghost"
					size="sm"
					className="h-6 shrink-0 px-2 text-muted-foreground hover:text-foreground"
					aria-label={t("lado.usage.alertSeenLabel", { alert: text })}
					disabled={busy}
					onClick={onSeen}
				>
					<span className="text-2xs">{t("lado.usage.alertSeen")}</span>
				</Button>
			)}
		</li>
	);
}

function Sessions({ sessions }: { sessions: LadoSessionUsage[] }) {
	const { t } = useTranslation();
	return (
		<section className="flex flex-col gap-2" aria-label={t("lado.usage.sessions")}>
			<h4 className="text-xs font-semibold text-foreground">{t("lado.usage.sessions")}</h4>
			{sessions.length === 0 ? (
				<p className="text-xs text-muted-foreground">{t("lado.timings.noSessions")}</p>
			) : (
				<div className="overflow-hidden rounded-md border border-[var(--color-border-settings-dialog)]">
					<Table className="text-xs">
						<TableHeader>
							<TableRow>
								<TableHead className="h-8 px-3 text-2xs">{t("lado.usage.column.role")}</TableHead>
								<TableHead className="h-8 px-2 text-2xs">{t("lado.usage.column.part")}</TableHead>
								<TableHead className="h-8 px-2 text-right text-2xs">{t("lado.usage.column.attempt")}</TableHead>
								<TableHead className="h-8 px-2 text-right text-2xs">{t("lado.usage.column.cost")}</TableHead>
								<TableHead className="h-8 px-2 text-right text-2xs">{t("lado.usage.column.tokens")}</TableHead>
								<TableHead className="h-8 px-2 text-right text-2xs">{t("lado.usage.column.subagents")}</TableHead>
								<TableHead className="h-8 px-2 text-right text-2xs">{t("lado.usage.column.context")}</TableHead>
								<TableHead className="h-8 px-3 text-right text-2xs">{t("lado.usage.column.compactions")}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{sessions.map((session) => (
								<SessionRow key={session.sessionId} session={session} t={t} />
							))}
						</TableBody>
					</Table>
				</div>
			)}
		</section>
	);
}

function SessionRow({ session, t }: { session: LadoSessionUsage; t: TFunction }) {
	const { i18n } = useTranslation();
	const locale = i18n.language;
	const totals = session.totals;
	const context =
		session.contextUsed > 0
			? session.contextWindow > 0
				? t("lado.usage.contextOf", {
						used: ladoTokensText(session.contextUsed, locale),
						window: ladoTokensText(session.contextWindow, locale),
					})
				: ladoTokensText(session.contextUsed, locale)
			: "–";
	return (
		<TableRow data-testid="lado-session-usage" data-session-id={session.sessionId} data-role={session.role}>
			<TableCell className="px-3 py-1.5">
				<span className="flex min-w-0 flex-col">
					<span className="flex items-baseline gap-1.5">
						<span className="font-medium text-foreground">{t(roleKeys[session.role])}</span>
						{session.usageMissing ? (
							<span className="text-2xs text-passive" title={t("lado.usage.missingHint")} data-testid="lado-session-usage-missing">
								{t("lado.usage.missing")}
							</span>
						) : session.incomplete ? (
							<span className="text-2xs text-passive" title={t("lado.usage.incompleteHint")}>{t("lado.usage.partial")}</span>
						) : null}
					</span>
					<span className="max-w-48 truncate font-mono text-2xs text-passive" title={session.sessionId}>{session.sessionId}</span>
				</span>
			</TableCell>
			<TableCell className="px-2 py-1.5 font-mono text-2xs text-muted-foreground">{session.partId || "–"}</TableCell>
			<TableCell className="px-2 py-1.5 text-right tabular-nums text-muted-foreground">{session.attempt > 0 ? session.attempt : "–"}</TableCell>
			<TableCell className="px-2 py-1.5 text-right tabular-nums text-foreground">{ladoCostText(totals.costNanos, locale)}</TableCell>
			<TableCell className="px-2 py-1.5 text-right tabular-nums text-muted-foreground">{ladoTokensText(tokenTotal(totals), locale)}</TableCell>
			<TableCell className="px-2 py-1.5 text-right tabular-nums text-muted-foreground">{ladoCostText(totals.subagentCostNanos, locale)}</TableCell>
			<TableCell className="px-2 py-1.5 text-right tabular-nums text-muted-foreground">{context}</TableCell>
			<TableCell className="px-3 py-1.5 text-right tabular-nums text-muted-foreground" data-testid="lado-session-compactions">
				{session.compactions}
			</TableCell>
		</TableRow>
	);
}
