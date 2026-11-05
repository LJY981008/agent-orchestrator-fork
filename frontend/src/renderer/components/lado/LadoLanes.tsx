// LADO fork: the Plan and Queued lanes, LADO's tasks in front of the session
// board (docs/LJY-PRD/TDD.md §3.3). Plan holds the tasks the human acts on
// next (Draft, Planned, approved tasks whose rules need review or block the
// run, and failed runs); Queued holds the approved ones LADO acts on next,
// waiting or starting. Once a worker starts, its session card on the board
// stands for the task, so the task leaves the lanes. A task card is not a
// session card: it stands for an issue that has no session yet. DESIGN.md's
// decisions log records the lanes, the card and the task view. A card also
// shows the task's estimated cost and its unseen alerts (issue #42), and a
// click on a LADO alert's notification opens its task here.
import { Ban } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCancelLadoTask, useLadoRepositories, useLadoTasks } from "../../hooks/useLado";
import { formatTimeCompact } from "../../lib/format-time";
import { ladoIssueLabel, type LadoRepository, type LadoTask } from "../../lib/lado";
import { subscribeLadoTaskOpenRequests, takeLadoTaskOpenRequest } from "../../lib/lado-alert-notifications";
import { ladoErrorText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
import { ConfirmDialog } from "../ConfirmDialog";
import { ProductExternalLink } from "../ProductExternalLink";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { ladoOpenPlanWorker, ladoTaskReason, ladoTaskStatus, ladoTaskToneDotClass } from "./lado-task-status";
import { LadoCardWatch, type LadoTaskWatch, useLadoBoardWatch } from "./LadoCardWatch";
import { LadoPartWorkerCardLink } from "./LadoPartWorkerLink";
import { LadoPlanWorkerCardLink } from "./LadoPlanWorkerStatus";
import { LadoTaskDialog, type LadoTaskTab } from "./LadoTaskDialog";

export function LadoLanes({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const tasks = useLadoTasks(projectId);
	const repositories = useLadoRepositories(projectId);
	const cancelTask = useCancelLadoTask(projectId);
	const [cancelling, setCancelling] = useState<LadoTask | null>(null);
	const [opened, setOpened] = useState<{ taskId: string; tab: LadoTaskTab } | null>(null);
	const watchOf = useLadoBoardWatch(projectId);
	// A click on a LADO alert's notification asks for its task, here or once
	// this board shows. The task need not be in the lanes: its run's card is
	// on the board then.
	useEffect(() => {
		const take = () => {
			const request = takeLadoTaskOpenRequest(projectId);
			if (request) setOpened({ taskId: request.taskId, tab: request.tab });
		};
		take();
		return subscribeLadoTaskOpenRequests(take);
	}, [projectId]);
	const all = (tasks.data ?? []).filter(inLanes);
	const queued = orderQueue(all.filter(isQueued));
	const planned = orderTasks(all.filter((task) => !isQueued(task)));
	const cardProps = {
		repositories: repositories.data ?? [],
		onCancel: setCancelling,
		onOpen: (task: LadoTask, tab: LadoTaskTab = "plan") => setOpened({ taskId: task.id, tab }),
	};

	return (
		<>
			<LadoLane
				label={t("lado.lane.plan")}
				testId="lado-plan-lane"
				count={planned.length}
				error={tasks.isError ? ladoErrorText(t, tasks.error) : null}
				empty={tasks.isSuccess && planned.length === 0 ? t("lado.lane.empty") : null}
				overlays={
					<>
						<ConfirmDialog
					open={cancelling !== null}
					title={t("lado.task.cancelTitle")}
					description={t("lado.task.cancelDescription")}
					confirmLabel={t("lado.task.cancel")}
					cancelLabel={t("lado.task.keep")}
					destructive
					busy={cancelTask.isPending}
					error={cancelTask.error ? ladoErrorText(t, cancelTask.error) : null}
					onConfirm={() => {
						if (!cancelling) return;
						cancelTask.mutate(cancelling.id, { onSuccess: () => setCancelling(null) });
					}}
					onOpenChange={(open) => {
						if (open) return;
						setCancelling(null);
						cancelTask.reset();
					}}
						/>
						<LadoTaskDialog
							projectId={projectId}
							taskId={opened?.taskId ?? null}
							initialTab={opened?.tab}
							repositories={repositories.data ?? []}
							onOpenChange={(open) => {
								if (!open) setOpened(null);
							}}
						/>
					</>
				}
			>
				{planned.map((task) => <LadoTaskCard key={task.id} task={task} watch={watchOf(task.id)} {...cardProps} />)}
			</LadoLane>
			<LadoLane
				label={t("lado.lane.queued")}
				testId="lado-queued-lane"
				count={queued.length}
				error={null}
				empty={tasks.isSuccess && queued.length === 0 ? t("lado.lane.queuedEmpty") : null}
			>
				{queued.map((task) => <LadoTaskCard key={task.id} task={task} watch={watchOf(task.id)} {...cardProps} />)}
			</LadoLane>
		</>
	);
}

function LadoLane({
	label,
	testId,
	count,
	error,
	empty,
	overlays,
	children,
}: {
	label: string;
	testId: string;
	count: number;
	error: string | null;
	empty: string | null;
	/** Dialogs the lane owns; they render outside the card list. */
	overlays?: ReactNode;
	children: ReactNode;
}) {
	const { t } = useTranslation();
	return (
		<section aria-label={t("lado.lane.aria", { label })} className="flex min-w-0 flex-col overflow-hidden" data-testid={testId}>
			<div className="flex h-12 shrink-0 items-center gap-2.5 px-4">
				<span className="size-[var(--size-swatch)] rounded-full bg-[var(--color-status-idle)]" aria-hidden="true" />
				<span className="text-xs font-medium text-foreground">{label}</span>
				<span className="ml-auto tabular-nums text-xs leading-none text-passive">{count}</span>
			</div>
			<div className="board-scrollbar min-h-0 flex-1 overflow-y-auto pl-3 pr-2 pb-3 pt-3">
				{error ? (
					<p className="px-1 text-2xs text-destructive" role="alert">{error}</p>
				) : empty ? (
					<p className="px-1 text-pretty text-2xs leading-5 text-muted-foreground">{empty}</p>
				) : null}
				<div className="flex min-h-full flex-col gap-2.5" role="list">
					{children}
				</div>
			</div>
			{overlays}
		</section>
	);
}

// The states of a task whose worker started: its session card carries it,
// with its pull request's verification and integration.
const startedStates = new Set<LadoTask["state"]>(["running", "validating", "integration_pending", "integrating", "completed"]);

// The daemon leaves cancelled tasks out; the lanes also leave out the tasks
// whose worker started. A failed run stays, to be approved again, and so
// does an issue closed before any run.
function inLanes(task: LadoTask): boolean {
	if (runningParts(task)) return true;
	if (startedStates.has(task.state)) return false;
	return task.state !== "closed_unmerged" || task.execution === undefined;
}

// Approved plans LADO starts next; approved is what an older daemon sends.
function isQueued(task: LadoTask): boolean {
	return task.state === "queued" || task.state === "dispatching" || task.state === "approved" || runningParts(task);
}

// A task whose parts run before its integration worker starts has no session
// card of its own yet, so it stays in the Queued lane, its card saying how
// many parts are merged and linking the part workers (docs/LJY-PRD/TDD.md
// §3.3, §5.4). The queue entry says so whatever run the task last had: a
// run of an earlier approval may still come as its execution (issue #61).
// An older daemon sends no queue state; there no execution means no
// integration worker yet. Once that worker starts, the entry is dispatched
// and its session card carries the task.
function runningParts(task: LadoTask): boolean {
	return (
		task.state === "running" &&
		task.parts !== undefined &&
		(task.queue?.state === "running_parts" || task.execution === undefined)
	);
}

// Like the session lanes, newest first; tasks whose issue closed go last.
function orderTasks(tasks: LadoTask[]): LadoTask[] {
	return [...tasks].sort((left, right) => {
		const closed = Number(left.state === "closed_unmerged") - Number(right.state === "closed_unmerged");
		return closed || right.createdAt.localeCompare(left.createdAt);
	});
}

// In the order the dispatcher takes them: the oldest approval first. The
// times are compared as instants, since the daemon trims fractional seconds.
function orderQueue(tasks: LadoTask[]): LadoTask[] {
	const enqueued = (task: LadoTask) => Date.parse(task.queue?.enqueuedAt ?? "") || 0;
	return [...tasks].sort((left, right) => enqueued(left) - enqueued(right));
}

function LadoTaskCard({
	task,
	watch,
	repositories,
	onCancel,
	onOpen,
}: {
	task: LadoTask;
	watch: LadoTaskWatch;
	repositories: readonly LadoRepository[];
	onCancel: (task: LadoTask) => void;
	onOpen: (task: LadoTask, tab?: LadoTaskTab) => void;
}) {
	const { t, i18n } = useTranslation();
	const status = ladoTaskStatus(task, t);
	const reason = ladoTaskReason(task, t, i18n.language);
	const worker = ladoOpenPlanWorker(task);
	const parts = runningParts(task);
	const issueLabel = ladoIssueLabel(task.issue.number, task.repository, repositories);
	return (
		<div
			role="listitem"
			className="group relative w-full cursor-pointer rounded-lg border border-border bg-surface text-left"
			data-testid="lado-task-card"
			data-task-id={task.id}
			onClick={(event) => {
				// The issue link, the session link and the cancel action are controls of their own.
				if ((event.target as HTMLElement).closest("a,button")) return;
				onOpen(task);
			}}
		>
			<div className="px-3.5 pb-2.5 pt-2.5">
				<div className="flex min-w-0 items-center gap-2.5">
					<button
						type="button"
						className="min-w-0 flex-1 rounded-sm text-left outline-none focus-visible:outline-2 focus-visible:outline-ring"
						title={task.issue.title}
						aria-label={t("lado.task.open", { issue: issueLabel })}
						onClick={() => onOpen(task)}
					>
						{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
						<span className="line-clamp-2 overflow-hidden text-balance text-sm-md font-semibold leading-tight tracking-tight text-foreground">
							{task.issue.title}
						</span>
					</button>
					<Tooltip>
						<TooltipTrigger asChild>
							<button
								type="button"
								aria-label={t("lado.task.cancel")}
								className="-mr-1 grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground opacity-0 outline-none transition-opacity duration-[120ms] hover:bg-interactive-hover hover:text-destructive focus-visible:opacity-100 focus-visible:outline-2 focus-visible:outline-ring group-hover:opacity-100"
								onClick={() => onCancel(task)}
							>
								<Ban className="size-icon-2xs" aria-hidden="true" />
							</button>
						</TooltipTrigger>
						<TooltipContent>{t("lado.task.cancel")}</TooltipContent>
					</Tooltip>
				</div>
				<div className="mt-1.5 flex min-w-0 items-center gap-1.5 font-mono text-2xs text-muted-foreground">
					<ProductExternalLink
						href={task.issue.url}
						ariaLabel={t("lado.task.openIssue", { issue: issueLabel })}
						className="truncate hover:text-foreground"
					>
						{issueLabel}
					</ProductExternalLink>
					<LadoCardWatch watch={watch} className="ml-auto" onOpenAlerts={() => onOpen(task, "usage")} />
				</div>
			</div>
			<div className="border-t border-border px-3.5 py-2.5">
				<div className="flex min-w-0 items-center gap-2">
					<span className={cn("size-1.5 shrink-0 rounded-full", ladoTaskToneDotClass[status.tone])} aria-hidden="true" />
					<span className="min-w-0 truncate text-2xs font-medium text-muted-foreground" data-testid="lado-task-state">
						{status.label}
					</span>
					<span className="ml-auto shrink-0 tabular-nums text-2xs text-muted-foreground">{formatTimeCompact(task.createdAt)}</span>
				</div>
				{/* Under the label, past the dot and its gap; a plan or part worker's session at the trailing edge. */}
				{worker?.designerSessionId || parts ? (
					<div className="mt-1 flex min-w-0 items-center gap-2 pl-3.5">
						{reason ? (
							<p className="min-w-0 truncate text-2xs text-muted-foreground" title={reason} data-testid="lado-task-reason">
								{reason}
							</p>
						) : null}
						{worker?.designerSessionId ? <LadoPlanWorkerCardLink task={task} worker={worker} /> : <LadoPartWorkerCardLink task={task} />}
					</div>
				) : reason ? (
					<p className="mt-1 truncate pl-3.5 text-2xs text-muted-foreground" title={reason} data-testid="lado-task-reason">
						{reason}
					</p>
				) : null}
			</div>
		</div>
	);
}
