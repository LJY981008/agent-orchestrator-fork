// LADO fork: the Plan lane, LADO's tasks in front of the session board
// (docs/LJY-PRD/TDD.md §3.3). A task card is not a session card: it stands for
// an issue that has no session yet. DESIGN.md's decisions log records the card.
import { Ban } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCancelLadoTask, useLadoRepositories, useLadoTasks } from "../../hooks/useLado";
import { formatTimeCompact } from "../../lib/format-time";
import { ladoIssueLabel, type LadoRepository, type LadoTask } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { ConfirmDialog } from "../ConfirmDialog";
import { ProductExternalLink } from "../ProductExternalLink";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

export function LadoPlanLane({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const tasks = useLadoTasks(projectId);
	const repositories = useLadoRepositories(projectId);
	const cancelTask = useCancelLadoTask(projectId);
	const [cancelling, setCancelling] = useState<LadoTask | null>(null);
	const visible = orderTasks(tasks.data ?? []);
	const label = t("lado.lane.plan");

	return (
		<section
			aria-label={t("lado.lane.aria", { label })}
			className="flex min-w-0 flex-col overflow-hidden"
			data-testid="lado-plan-lane"
		>
			<div className="flex h-12 shrink-0 items-center gap-2.5 px-4">
				<span className="size-[var(--size-swatch)] rounded-full bg-[var(--color-status-idle)]" aria-hidden="true" />
				<span className="text-xs font-medium text-foreground">{label}</span>
				<span className="ml-auto tabular-nums text-xs leading-none text-passive">{visible.length}</span>
			</div>
			<div className="board-scrollbar min-h-0 flex-1 overflow-y-auto pl-3 pr-2 pb-3 pt-3">
				{tasks.isError ? (
					<p className="px-1 text-2xs text-destructive" role="alert">{ladoErrorText(t, tasks.error)}</p>
				) : visible.length === 0 && tasks.isSuccess ? (
					<p className="px-1 text-pretty text-2xs leading-5 text-muted-foreground">{t("lado.lane.empty")}</p>
				) : (
					<div className="flex min-h-full flex-col gap-2.5" role="list">
						{visible.map((task) => (
							<LadoTaskCard
								key={task.id}
								task={task}
								repositories={repositories.data ?? []}
								onCancel={() => setCancelling(task)}
							/>
						))}
					</div>
				)}
			</div>
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

function LadoTaskCard({
	task,
	repositories,
	onCancel,
}: {
	task: LadoTask;
	repositories: readonly LadoRepository[];
	onCancel: () => void;
}) {
	const { t } = useTranslation();
	const stateLabel = task.state === "closed_unmerged" ? t("lado.task.state.closed") : t("lado.task.state.draft");
	const issueLabel = ladoIssueLabel(task.issue.number, task.repository, repositories);
	return (
		<div
			role="listitem"
			className="group relative w-full rounded-lg border border-border bg-surface text-left"
			data-testid="lado-task-card"
			data-task-id={task.id}
		>
			<div className="px-3.5 pb-2.5 pt-2.5">
				<div className="flex min-w-0 items-center gap-2.5">
					<div
						className="min-w-0 flex-1 line-clamp-2 overflow-hidden text-balance text-sm-md font-semibold leading-tight tracking-tight text-foreground"
						title={task.issue.title}
					>
						{task.issue.title}
					</div>
					<Tooltip>
						<TooltipTrigger asChild>
							<button
								type="button"
								aria-label={t("lado.task.cancel")}
								className="-mr-1 grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground opacity-0 outline-none transition-opacity duration-[120ms] hover:bg-interactive-hover hover:text-destructive focus-visible:opacity-100 focus-visible:outline-2 focus-visible:outline-ring group-hover:opacity-100"
								onClick={onCancel}
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
			<div className="flex min-w-0 items-center gap-3 border-t border-border px-3.5 py-2.5">
				<span className="min-w-0 truncate text-2xs font-medium text-muted-foreground" data-testid="lado-task-state">
					{stateLabel}
				</span>
				<span className="ml-auto shrink-0 tabular-nums text-2xs text-muted-foreground">
					{formatTimeCompact(task.createdAt)}
				</span>
			</div>
		</div>
	);
}
