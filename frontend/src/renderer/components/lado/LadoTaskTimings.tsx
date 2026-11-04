// LADO fork: the task view's Timings tab (issue #34, step 0 "measure first"):
// when the task reached each stage from intake to merge and how long each
// stage took after the one before, and what each plan and run session spent
// reading and searching before its first edit. LADO derives the stages from
// what it already keeps; times inferred for runs from before it recorded them
// are marked as estimates.
import { useQuery } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import {
	fetchLadoTaskTimings,
	type LadoSessionMetrics,
	type LadoSessionRole,
	type LadoTaskTimeline,
	type LadoTimingPoint,
	type LadoTimingStage,
	ladoTaskTimingsQueryKey,
} from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";

const stageKeys = {
	received: "lado.timings.stage.received",
	plan_requested: "lado.timings.stage.plan_requested",
	revision: "lado.timings.stage.revision",
	approved: "lado.timings.stage.approved",
	run_started: "lado.timings.stage.run_started",
	pr_opened: "lado.timings.stage.pr_opened",
	verified: "lado.timings.stage.verified",
	integration_approved: "lado.timings.stage.integration_approved",
	merged: "lado.timings.stage.merged",
} as const satisfies Record<LadoTimingStage, string>;

/** Each session role's label; the Usage tab names sessions the same way. */
export const roleKeys = {
	designer: "lado.timings.role.designer",
	critic: "lado.timings.role.critic",
	worker: "lado.timings.role.worker",
	part: "lado.timings.role.part",
	merge: "lado.timings.role.merge",
	integration: "lado.timings.role.integration",
} as const satisfies Record<LadoSessionRole, string>;

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** A duration in its two largest units: "45 s", "12 min", "3 h 5 min", "2 d 4 h". */
export function ladoDurationText(t: TFunction, ms: number): string {
	const value = Math.max(0, ms);
	if (value < MINUTE) return t("lado.timings.duration.seconds", { seconds: Math.round(value / 1000) });
	if (value < HOUR) return t("lado.timings.duration.minutes", { minutes: Math.floor(value / MINUTE) });
	if (value < DAY) {
		return t("lado.timings.duration.hours", { hours: Math.floor(value / HOUR), minutes: Math.floor((value % HOUR) / MINUTE) });
	}
	return t("lado.timings.duration.days", { days: Math.floor(value / DAY), hours: Math.floor((value % DAY) / HOUR) });
}

/** The task's timings; under the task view's key, so LADO's task events refresh them. */
function useLadoTaskTimings(taskId: string) {
	return useQuery({
		queryKey: ladoTaskTimingsQueryKey(taskId),
		queryFn: () => fetchLadoTaskTimings(taskId),
		refetchOnMount: "always",
		retry: false,
	});
}

export function LadoTaskTimings({ taskId }: { taskId: string }) {
	const { t } = useTranslation();
	const timings = useLadoTaskTimings(taskId);
	if (timings.isPending) {
		return <p className="text-control text-settings-muted" role="status">{t("common.loading")}</p>;
	}
	if (timings.isError) {
		return <p className="text-caption leading-4 text-error" role="alert">{ladoErrorText(t, timings.error)}</p>;
	}
	return (
		<div className="flex flex-col gap-5" data-testid="lado-task-timings">
			<Stages timeline={timings.data.timeline} />
			<Sessions sessions={timings.data.sessions} />
		</div>
	);
}

function Stages({ timeline }: { timeline: LadoTaskTimeline }) {
	const { t, i18n } = useTranslation();
	const time = (iso: string) => new Date(iso).toLocaleString(i18n.language, { dateStyle: "medium", timeStyle: "short" });
	const estimated = timeline.points.some((point) => point.estimated);
	return (
		<section className="flex flex-col gap-2" aria-label={t("lado.timings.stages")}>
			<div className="flex min-w-0 items-baseline gap-3">
				<h4 className="text-xs font-semibold text-foreground">{t("lado.timings.stages")}</h4>
				{timeline.points.length > 0 ? (
					<span className="ml-auto text-xs tabular-nums text-muted-foreground" data-testid="lado-timings-total">
						{timeline.totalMs > 0
							? t("lado.timings.total", { duration: ladoDurationText(t, timeline.totalMs) })
							: t("lado.timings.notMerged")}
					</span>
				) : null}
			</div>
			{timeline.points.length === 0 ? (
				<p className="text-xs text-muted-foreground">{t("lado.timings.noStages")}</p>
			) : (
				<>
					<ol className="flex flex-col divide-y divide-[var(--color-border-settings-dialog)] overflow-hidden rounded-md border border-[var(--color-border-settings-dialog)]">
						{timeline.points.map((point, index) => (
							<StageRow key={point.stage} point={point} first={index === 0} time={time(point.at)} />
						))}
					</ol>
					<p className="text-caption leading-4 text-settings-muted" data-testid="lado-timings-counts">
						{t("lado.timings.counts", {
							planRequests: timeline.planRequests,
							revisions: timeline.revisions,
							approvals: timeline.approvals,
							runs: timeline.runs,
						})}
					</p>
					{estimated ? <p className="text-caption leading-4 text-settings-muted">{t("lado.timings.estimatedNote")}</p> : null}
				</>
			)}
		</section>
	);
}

function StageRow({ point, first, time }: { point: LadoTimingPoint; first: boolean; time: string }) {
	const { t } = useTranslation();
	return (
		<li
			className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto_minmax(4.5rem,auto)] items-baseline gap-x-3 px-3 py-2 text-xs leading-5"
			data-testid="lado-timing-point"
			data-stage={point.stage}
		>
			<span className="flex min-w-0 items-baseline gap-2">
				<span className="truncate text-foreground">{t(stageKeys[point.stage])}</span>
				{point.estimated ? (
					<span className="shrink-0 text-2xs text-[var(--color-status-in-review)]" data-testid="lado-timing-estimated">
						{t("lado.timings.estimated")}
					</span>
				) : null}
			</span>
			<span className="tabular-nums text-muted-foreground">{time}</span>
			<span className="text-right tabular-nums text-foreground">
				{first ? null : t("lado.timings.sincePrev", { duration: ladoDurationText(t, point.sincePrevMs) })}
			</span>
		</li>
	);
}

function Sessions({ sessions }: { sessions: LadoSessionMetrics[] }) {
	const { t } = useTranslation();
	return (
		<section className="flex flex-col gap-2" aria-label={t("lado.timings.sessions")}>
			<h4 className="text-xs font-semibold text-foreground">{t("lado.timings.sessions")}</h4>
			{sessions.length === 0 ? (
				<p className="text-xs text-muted-foreground">{t("lado.timings.noSessions")}</p>
			) : (
				<>
					<p className="text-caption leading-4 text-settings-muted">{t("lado.timings.sessionsNote")}</p>
					<ul className="flex flex-col divide-y divide-[var(--color-border-settings-dialog)] overflow-hidden rounded-md border border-[var(--color-border-settings-dialog)]">
						{sessions.map((session) => (
							<SessionRow key={session.sessionId} session={session} />
						))}
					</ul>
				</>
			)}
		</section>
	);
}

function SessionRow({ session }: { session: LadoSessionMetrics }) {
	const { t } = useTranslation();
	return (
		<li
			className="flex min-w-0 flex-col gap-0.5 px-3 py-2 text-xs leading-5"
			data-testid="lado-session-metrics"
			data-session-id={session.sessionId}
			data-role={session.role}
		>
			<div className="flex min-w-0 items-baseline gap-2">
				<span className="shrink-0 font-medium text-foreground">{t(roleKeys[session.role])}</span>
				<span className="min-w-0 truncate font-mono text-2xs text-passive" title={session.sessionId}>{session.sessionId}</span>
				<span className="ml-auto shrink-0 tabular-nums text-foreground">
					{t("lado.timings.investigate", { duration: ladoDurationText(t, session.investigateMs) })}
				</span>
			</div>
			<div className="flex min-w-0 items-baseline gap-2 text-caption leading-4 text-settings-muted">
				<span className="min-w-0 tabular-nums">
					{t("lado.timings.calls", {
						toolCalls: session.toolCalls,
						readCalls: session.readCalls,
						searchCalls: session.searchCalls,
						commandCalls: session.commandCalls,
						editCalls: session.editCalls,
					})}
				</span>
				{session.stored ? null : (
					<span className="ml-auto shrink-0 text-passive" title={t("lado.timings.computedHint")} data-testid="lado-session-computed">
						{t("lado.timings.computed")}
					</span>
				)}
			</div>
		</li>
	);
}
