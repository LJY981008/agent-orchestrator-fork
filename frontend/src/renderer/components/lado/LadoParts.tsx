// LADO fork: parallel parts (docs/LJY-PRD/TDD.md §5.4). The plan view lists a
// plan's parts by phase, the parts of one phase running at once, then the
// integration; the task view shows how far each part of the running task got,
// with its session and the merge into the integration branch. A workspace
// run across repositories (#64) has an integration branch in each, and a
// part's merge can conflict in one of them. Both say how big each part is
// (#69): the plan view warns about parts over the sizing limits, and the task
// view shows each part's steps, compactions and cost.
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useLadoTaskParts, useLadoTaskUsage } from "../../hooks/useLado";
import type { LadoPlan, LadoPlanPart, LadoTask, LadoTaskPart, LadoTaskPartState, LadoTaskUsage } from "../../lib/lado";
import { ladoErrorText, ladoPartStateText, ladoPartWaitingText } from "../../lib/lado-errors";
import { ladoCostText } from "../../lib/lado-usage";
import type { MessageKey } from "../../i18n/messages";
import { useNavigateToSession } from "../../lib/navigate-to-session";
import { cn } from "../../lib/utils";
import { type LadoTaskStatusTone, ladoTaskToneDotClass } from "./lado-task-status";

/** Parts grouped by their display phase, in the plan's order; phase 0 (a cycle) last. */
function byPhase<T extends { phase: number }>(parts: readonly T[]): [number, T[]][] {
	const phases = new Map<number, T[]>();
	for (const part of parts) {
		const list = phases.get(part.phase) ?? [];
		list.push(part);
		phases.set(part.phase, list);
	}
	return [...phases.entries()].sort(([a], [b]) => (a === 0 ? 1 : b === 0 ? -1 : a - b));
}

function stepNumbers(steps: readonly number[]): string {
	return steps.join(", ");
}

function phaseTitle(t: TFunction, phase: number, count: number): string {
	const title = t("lado.view.phase", { number: phase });
	return count > 1 ? `${title} · ${t("lado.view.phaseAtOnce")}` : title;
}

const warningClass = "text-caption leading-4 text-[var(--color-status-in-review)]";

const partWarningKeys: Record<LadoPlanPart["warnings"][number]["code"], MessageKey> = {
	part_many_steps: "lado.view.partWarning.part_many_steps",
	part_many_paths: "lado.view.partWarning.part_many_paths",
	part_many_files: "lado.view.partWarning.part_many_files",
};

/**
 * A plan's parts by phase, then the integration that follows them; or, for a
 * large plan without parts, the warning that one worker does it all. A small
 * plan split into parts, and a part over the sizing limits, are warned about
 * the same way; none of these warnings blocks the approval.
 */
export function LadoPlanParts({ content }: { content: LadoPlan["plan"] }) {
	const { t } = useTranslation();
	if (content.parts.length === 0) {
		return content.warnings.includes("unsplit_large_plan") ? (
			<p className={warningClass} role="note" data-testid="lado-plan-unsplit">
				{t("lado.view.planWarning.unsplit_large_plan")}
			</p>
		) : null;
	}
	return (
		<section className="flex flex-col gap-2" aria-label={t("lado.view.parts")} data-testid="lado-plan-parts">
			<h4 className="text-xs font-semibold text-foreground">{t("lado.view.parts")}</h4>
			<p className="text-caption leading-4 text-settings-muted">{t("lado.view.partsNote")}</p>
			{content.warnings.includes("split_small_plan") ? (
				<p className={warningClass} role="note" data-testid="lado-plan-split-small">
					{t("lado.view.planWarning.split_small_plan")}
				</p>
			) : null}
			<ol className="flex flex-col gap-2">
				{byPhase(content.parts).map(([phase, parts]) => (
					<li key={phase} className="flex flex-col gap-1" data-testid="lado-plan-phase" data-phase={phase}>
						<span className="text-2xs font-medium text-muted-foreground">{phaseTitle(t, phase, parts.length)}</span>
						<ul className="flex flex-col divide-y divide-[var(--color-border-settings-dialog)] overflow-hidden rounded-md border border-[var(--color-border-settings-dialog)]">
							{parts.map((part) => (
								<PlanPartRow key={part.id} part={part} />
							))}
						</ul>
					</li>
				))}
				<li className="flex flex-col gap-1" data-testid="lado-plan-integration">
					<span className="text-2xs font-medium text-muted-foreground">{t("lado.view.integrationPhase")}</span>
					<p className="text-xs leading-5 text-foreground">
						{content.integrationSteps.length > 0
							? t("lado.view.integrationSteps", { steps: stepNumbers(content.integrationSteps) })
							: t("lado.view.integrationNoSteps")}
					</p>
				</li>
			</ol>
		</section>
	);
}

function PlanPartRow({ part }: { part: LadoPlanPart }) {
	const { t } = useTranslation();
	return (
		<li className="flex min-w-0 flex-col gap-0.5 px-3 py-2 text-xs leading-5" data-testid="lado-plan-part" data-part-id={part.id}>
			<div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
				<span className="font-mono text-foreground">{part.id}</span>
				<span className="min-w-0 truncate font-medium text-foreground">{part.title}</span>
				<span className="text-muted-foreground">{t("lado.view.partSteps", { steps: stepNumbers(part.steps) })}</span>
				{part.dependsOn.length > 0 ? (
					<span className="text-muted-foreground">{t("lado.view.partAfter", { parts: part.dependsOn.join(", ") })}</span>
				) : null}
			</div>
			<span className="break-all font-mono text-2xs text-muted-foreground">{part.affectedPaths.join("  ")}</span>
			{part.warnings.map((warning) => (
				<p key={warning.code} className={warningClass} role="note" data-testid="lado-plan-part-warning" data-code={warning.code}>
					{t(partWarningKeys[warning.code], { value: warning.value, limit: warning.limit })}
				</p>
			))}
		</li>
	);
}

/** What a part's worker sessions used: their compactions and cost added up. */
type PartMeasure = { compactions: number; costNanos: number };

/**
 * Each part's measure from the task's usage. Only the sessions of the run the
 * parts view shows count, its retries included, so an earlier approval's run
 * of a part with the same id does not add to it. Without that run's queue
 * entry no part has a measure, and the rows show their steps alone.
 */
function partMeasures(usage: LadoTaskUsage | undefined, queueEntryId: string | undefined): Map<string, PartMeasure> | null {
	if (!usage || !queueEntryId) return null;
	const measures = new Map<string, PartMeasure>();
	for (const session of usage.sessions) {
		if (session.role !== "part" || !session.partId || session.queueEntryId !== queueEntryId) continue;
		const measure = measures.get(session.partId) ?? { compactions: 0, costNanos: 0 };
		measure.compactions += session.compactions;
		measure.costNanos += session.totals.costNanos;
		measures.set(session.partId, measure);
	}
	return measures;
}

const partTone: Record<LadoTaskPartState, LadoTaskStatusTone> = {
	waiting: "muted",
	running: "working",
	done: "working",
	merging: "working",
	conflict: "working",
	merged: "merged",
	failed: "attention",
};

/**
 * How far each part of a task's parts run got, by phase: its state and why it
 * waits, its worker's session, the commits it started from and was merged
 * at, the merge worker of a conflict, and last the integration worker that
 * opens the pull request.
 */
export function LadoPartsProgress({ task, onClose }: { task: LadoTask; onClose: () => void }) {
	const { t } = useTranslation();
	const parts = useLadoTaskParts(task.id, Boolean(task.parts));
	const usage = useLadoTaskUsage(task.id, Boolean(task.parts));
	const navigateToSession = useNavigateToSession();
	const open = (sessionId: string) => {
		onClose();
		navigateToSession(task.projectId, sessionId);
	};
	if (!task.parts) return null;
	if (parts.isError) {
		return (
			<p className="text-caption leading-4 text-error" role="alert">
				{ladoErrorText(t, parts.error)}
			</p>
		);
	}
	const data = parts.data;
	const integration = task.execution;
	// A run in one repository keeps the one branch line it always had.
	const manyRepos = (data?.repos?.length ?? 0) > 1;
	const measures = partMeasures(usage.isError ? undefined : usage.data, data?.queueEntryId);
	return (
		<section className="flex flex-col gap-2" aria-label={t("lado.parts.title")} data-testid="lado-parts-progress">
			<div className="flex min-w-0 items-baseline gap-2">
				<h4 className="text-xs font-semibold text-foreground">{t("lado.parts.title")}</h4>
				<span className="text-2xs tabular-nums text-muted-foreground">
					{t("lado.task.parts", { merged: task.parts.merged, total: task.parts.total })}
				</span>
				{data?.branch ? (
					<span className="ml-auto min-w-0 truncate font-mono text-2xs text-muted-foreground" title={data.branch}>
						{data.branch} {data.headSha && !manyRepos ? data.headSha.slice(0, 7) : ""}
					</span>
				) : null}
			</div>
			{manyRepos && data?.repos ? (
				<ul className="flex flex-col gap-0.5 text-2xs text-muted-foreground" aria-label={t("lado.parts.repos")} data-testid="lado-parts-repos">
					{data.repos.map((repo) => (
						<li key={repo.repo} className="flex min-w-0 items-center gap-2" data-testid="lado-parts-repo" data-repo={repo.repo}>
							<span className="font-mono text-foreground">{repo.repo}</span>
							<span className="font-mono">
								{repo.baseSha.slice(0, 7)}
								{repo.headSha !== repo.baseSha ? ` → ${repo.headSha.slice(0, 7)}` : ""}
							</span>
							{repo.headSha === repo.baseSha ? <span>{t("lado.parts.repoUnchanged")}</span> : null}
						</li>
					))}
				</ul>
			) : null}
			{!data ? (
				<p className="text-caption leading-4 text-settings-muted" role="status">
					{t("common.loading")}
				</p>
			) : (
				<ol className="flex flex-col gap-2">
					{byPhase(data.parts).map(([phase, rows]) => (
						<li key={phase} className="flex flex-col gap-1">
							<span className="text-2xs font-medium text-muted-foreground">{phaseTitle(t, phase, rows.length)}</span>
							<ul className="flex flex-col divide-y divide-[var(--color-border-settings-dialog)] overflow-hidden rounded-md border border-[var(--color-border-settings-dialog)]">
								{rows.map((part) => (
									<PartProgressRow
										key={part.id}
										part={part}
										measure={measures ? (measures.get(part.id) ?? { compactions: 0, costNanos: 0 }) : null}
										onOpen={open}
									/>
								))}
							</ul>
						</li>
					))}
					<li className="flex flex-col gap-1" data-testid="lado-parts-integration">
						<span className="text-2xs font-medium text-muted-foreground">{t("lado.view.integrationPhase")}</span>
						<div className="flex min-w-0 items-center gap-2 text-xs leading-5">
							<span
								className={cn("size-1.5 shrink-0 rounded-full", ladoTaskToneDotClass[integration ? "working" : "muted"])}
								aria-hidden="true"
							/>
							{integration ? (
								<SessionLink label={t("lado.parts.integrationWorker")} onClick={() => open(integration.sessionId)} />
							) : (
								<span className="text-settings-muted">{t("lado.parts.integrationWaiting")}</span>
							)}
						</div>
					</li>
				</ol>
			)}
		</section>
	);
}

function PartProgressRow({
	part,
	measure,
	onOpen,
}: {
	part: LadoTaskPart;
	measure: PartMeasure | null;
	onOpen: (sessionId: string) => void;
}) {
	const { t, i18n } = useTranslation();
	const waiting = ladoPartWaitingText(t, part.waitingReason, part.waitingDetail);
	const why = waiting || (part.state === "failed" ? part.error : "");
	const repos = part.repos ?? [];
	const manyRepos = repos.length > 1;
	return (
		<li className="flex min-w-0 flex-col gap-0.5 px-3 py-2 text-xs leading-5" data-testid="lado-part" data-part-id={part.id} data-state={part.state}>
			<div className="flex min-w-0 flex-wrap items-center gap-x-2">
				<span className={cn("size-1.5 shrink-0 rounded-full", ladoTaskToneDotClass[partTone[part.state]])} aria-hidden="true" />
				<span className="font-mono text-foreground">{part.id}</span>
				<span className="min-w-0 truncate text-foreground">{part.title}</span>
				<span className="font-medium text-foreground" role="status">
					{ladoPartStateText(t, part.state)}
				</span>
				{part.attempts > 1 ? <span className="tabular-nums text-muted-foreground">{t("lado.parts.attempt", { attempt: part.attempts })}</span> : null}
				<span className="ml-auto flex shrink-0 items-center gap-2">
					{part.sessionId ? <SessionLink label={t("lado.view.openSession")} onClick={() => onOpen(part.sessionId ?? "")} /> : null}
					{part.mergeSessionId ? <SessionLink label={t("lado.parts.mergeWorker")} onClick={() => onOpen(part.mergeSessionId ?? "")} /> : null}
				</span>
			</div>
			<span className="pl-3.5 text-2xs tabular-nums text-muted-foreground" data-testid="lado-part-measure">
				{measure
					? t("lado.parts.measure", {
							steps: part.steps.length,
							compactions: measure.compactions,
							cost: ladoCostText(measure.costNanos, i18n.language),
						})
					: t("lado.parts.measureSteps", { steps: part.steps.length })}
			</span>
			{why ? (
				<span className="truncate pl-3.5 text-settings-muted" title={why}>
					{why}
				</span>
			) : null}
			{part.mergeRepo ? (
				<span className="pl-3.5 text-settings-muted" data-testid="lado-part-merge-repo">
					{t("lado.parts.conflictRepo", { repo: part.mergeRepo })}
				</span>
			) : null}
			{manyRepos ? (
				<ul className="flex flex-col pl-3.5 text-2xs text-muted-foreground">
					{repos.map((repo) => (
						<li key={repo.repo} className="flex min-w-0 items-center gap-2" data-testid="lado-part-repo" data-repo={repo.repo}>
							<span className="font-mono text-foreground">{repo.repo}</span>
							{repo.empty ? (
								<span>{t("lado.parts.repoUnchanged")}</span>
							) : repo.startSha ? (
								<span className="font-mono">
									{repo.startSha.slice(0, 7)}
									{repo.mergedSha ? ` → ${repo.mergedSha.slice(0, 7)}` : ""}
								</span>
							) : null}
						</li>
					))}
				</ul>
			) : part.startSha ? (
				<span className="pl-3.5 font-mono text-2xs text-muted-foreground">
					{part.startSha.slice(0, 7)}
					{part.mergedSha ? ` → ${part.mergedSha.slice(0, 7)}` : ""}
				</span>
			) : null}
		</li>
	);
}

function SessionLink({ label, onClick }: { label: string; onClick: () => void }) {
	return (
		<button
			type="button"
			className="rounded-sm text-foreground underline-offset-2 outline-none hover:underline focus-visible:outline-2 focus-visible:outline-ring"
			onClick={onClick}
		>
			{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
			<span className="text-xs leading-5">{label}</span>
		</button>
	);
}
