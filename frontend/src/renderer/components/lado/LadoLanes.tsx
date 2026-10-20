// LADO fork: the Plan and Queued lanes, LADO's tasks in front of the session
// board (docs/LJY-PRD/TDD.md §3.3). Plan holds the tasks the human acts on
// next (Draft, Planned); Queued holds the approved ones LADO acts on next. A
// task card is not a session card: it stands for an issue that has no session
// yet. DESIGN.md's decisions log records the lanes, the card and the task view.
import type { TFunction } from "i18next";
import { Ban } from "lucide-react";
import { type ReactNode, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCancelLadoTask, useLadoRepositories, useLadoTasks } from "../../hooks/useLado";
import { formatTimeCompact } from "../../lib/format-time";
import { ladoIssueLabel, type LadoRepository, type LadoTask } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
import { ConfirmDialog } from "../ConfirmDialog";
import { ProductExternalLink } from "../ProductExternalLink";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { LadoTaskDialog } from "./LadoTaskDialog";

export function LadoLanes({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const tasks = useLadoTasks(projectId);
	const repositories = useLadoRepositories(projectId);
	const cancelTask = useCancelLadoTask(projectId);
	const [cancelling, setCancelling] = useState<LadoTask | null>(null);
	const [openTaskId, setOpenTaskId] = useState<string | null>(null);
	const all = tasks.data ?? [];
	const queued = orderTasks(all.filter((task) => task.state === "approved"));
	const planned = orderTasks(all.filter((task) => task.state !== "approved"));
	const cardProps = {
		repositories: repositories.data ?? [],
		onCancel: setCancelling,
		onOpen: (task: LadoTask) => setOpenTaskId(task.id),
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
							taskId={openTaskId}
							repositories={repositories.data ?? []}
							onOpenChange={(open) => {
								if (!open) setOpenTaskId(null);
							}}
						/>
					</>
				}
			>
				{planned.map((task) => <LadoTaskCard key={task.id} task={task} {...cardProps} />)}
			</LadoLane>
			<LadoLane
				label={t("lado.lane.queued")}
				testId="lado-queued-lane"
				count={queued.length}
				error={null}
				empty={tasks.isSuccess && queued.length === 0 ? t("lado.lane.queuedEmpty") : null}
			>
				{queued.map((task) => <LadoTaskCard key={task.id} task={task} {...cardProps} />)}
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

// Like the session lanes, newest first; tasks whose issue closed go last.
function orderTasks(tasks: LadoTask[]): LadoTask[] {
	return [...tasks].sort((left, right) => {
		const closed = Number(left.state === "closed_unmerged") - Number(right.state === "closed_unmerged");
		return closed || right.createdAt.localeCompare(left.createdAt);
	});
}

type TaskStatusTone = "muted" | "attention" | "ready";

/**
 * The card's one status line. A plan waiting for review is the human's next
 * decision, so it takes the needs-you color; an approved plan reads as ready.
 */
export function ladoTaskStatus(task: LadoTask, t: TFunction): { label: string; tone: TaskStatusTone } {
	switch (task.state) {
		case "closed_unmerged":
			return { label: t("lado.task.state.closed"), tone: "muted" };
		case "approved":
			return { label: t("lado.task.state.approved"), tone: "ready" };
		case "planned":
			if (task.plan.requestPending) return { label: t("lado.task.state.planRequested"), tone: "muted" };
			return task.plan.latest?.valid
				? { label: t("lado.task.state.planned"), tone: "attention" }
				: { label: t("lado.task.state.planInvalid"), tone: "attention" };
		default:
			if (task.plan.requestPending) return { label: t("lado.task.state.planRequested"), tone: "muted" };
			if (task.plan.requestError) return { label: t("lado.task.state.planRequestFailed"), tone: "attention" };
			return { label: t("lado.task.state.draft"), tone: "muted" };
	}
}

const toneDotClass: Record<TaskStatusTone, string> = {
	muted: "bg-[var(--color-status-idle)]",
	attention: "bg-[var(--color-status-needs-you)]",
	ready: "bg-[var(--color-status-ready)]",
};

function LadoTaskCard({
	task,
	repositories,
	onCancel,
	onOpen,
}: {
	task: LadoTask;
	repositories: readonly LadoRepository[];
	onCancel: (task: LadoTask) => void;
	onOpen: (task: LadoTask) => void;
}) {
	const { t } = useTranslation();
	const status = ladoTaskStatus(task, t);
	const issueLabel = ladoIssueLabel(task.issue.number, task.repository, repositories);
	return (
		<div
			role="listitem"
			className="group relative w-full cursor-pointer rounded-lg border border-border bg-surface text-left"
			data-testid="lado-task-card"
			data-task-id={task.id}
			onClick={(event) => {
				// The issue link and the cancel action are controls of their own.
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
				</div>
			</div>
			<div className="flex min-w-0 items-center gap-2 border-t border-border px-3.5 py-2.5">
				<span className={cn("size-1.5 shrink-0 rounded-full", toneDotClass[status.tone])} aria-hidden="true" />
				<span className="min-w-0 truncate text-2xs font-medium text-muted-foreground" data-testid="lado-task-state">
					{status.label}
				</span>
				<span className="ml-auto shrink-0 tabular-nums text-2xs text-muted-foreground">{formatTimeCompact(task.createdAt)}</span>
			</div>
		</div>
	);
}
