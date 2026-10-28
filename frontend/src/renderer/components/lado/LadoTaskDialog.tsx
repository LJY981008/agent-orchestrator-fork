// LADO fork: the task view (docs/LJY-PRD/TDD.md §3.3, §5.1 and §5.2): the
// issue and its comments as they are now, the pinned plan revisions, and the
// developer's decisions on them: request a plan, use a comment as the plan,
// request changes, approve. Approving goes through the desktop app's native
// confirmation (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md)
// and fixes the rules that apply at that moment; the Rules tab shows them.
// Once approved, the footer follows the queue and the run: what the queued
// plan waits for, then the worker's session and PR, or why the run failed,
// and once LADO verified the PR's head, its integration approval and merge.
import { useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { X } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { useApproveLadoPlan, useLadoPlan, useLadoTasks, useLadoTaskView, useRequestLadoPlan } from "../../hooks/useLado";
import { formatTimeCompact } from "../../lib/format-time";
import {
	fetchLadoEffective,
	LadoApiError,
	ladoEffectiveQueryKey,
	ladoIssueLabel,
	type LadoIssueComment,
	type LadoPlan,
	type LadoRepository,
	type LadoTask,
	type LadoTaskView,
} from "../../lib/lado";
import { ladoChangeKindLabel, ladoErrorText, ladoPlanProblemText, ladoQueueErrorText, ladoReasonText } from "../../lib/lado-errors";
import { useNavigateToSession } from "../../lib/navigate-to-session";
import { cn } from "../../lib/utils";
import { ChatMarkdown } from "../chat/ChatMarkdown";
import { ProductExternalLink } from "../ProductExternalLink";
import { Button } from "../ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogTitle,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../ui/tabs";
import { ladoOpenPlanWorker, ladoTaskReason, ladoTaskStatus, ladoTaskToneDotClass } from "./lado-task-status";
import { LadoPlanReview } from "./LadoPlanReview";
import { LadoPlanWorkerStatus } from "./LadoPlanWorkerStatus";
import { LadoTaskRules } from "./LadoTaskRules";
import { LadoIntegrationAction, LadoRepostStatus } from "./LadoVerificationSection";

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

/** The states of a task whose worker runs, or whose PR LADO verifies, integrates or saw merged. */
const runStates = new Set<LadoTask["state"]>(["running", "validating", "integration_pending", "integrating", "completed"]);

/** The daemon refused an approval because the rules changed after they were read. */
function rulesChanged(error: unknown): boolean {
	return error instanceof LadoApiError && error.code === "LADO_RULES_CHANGED";
}

export function LadoTaskDialog({
	projectId,
	taskId,
	repositories,
	onOpenChange,
}: {
	projectId: string;
	taskId: string | null;
	repositories: readonly LadoRepository[];
	onOpenChange: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const view = useLadoTaskView(taskId);
	const data = view.data;
	const title = data?.task.issue.title ?? "";
	const issueLabel = data ? ladoIssueLabel(data.task.issue.number, data.task.repository, repositories) : "";
	return (
		<Dialog open={taskId !== null} onOpenChange={onOpenChange}>
			<DialogContent
				showCloseButton={false}
				aria-describedby={undefined}
				className={cn(settingsDialogContentClass, "h-[min(780px,calc(100svh-48px))] w-[min(1080px,calc(100vw-24px))]")}
				data-testid="lado-task-dialog"
			>
				<DialogClose asChild>
					<button type="button" className="settings-dialog-close-button settings-close-button" aria-label={t("lado.view.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "gap-1 p-5 pr-12")}>
					{data ? (
						<ProductExternalLink
							href={data.task.issue.url}
							ariaLabel={t("lado.task.openIssue", { issue: issueLabel })}
							className="w-fit font-mono text-2xs text-muted-foreground hover:text-foreground"
						>
							{issueLabel}
						</ProductExternalLink>
					) : null}
					<DialogTitle className="settings-dialog-title text-base">{title}</DialogTitle>
				</div>
				{view.isPending ? (
					<p className="p-5 text-control text-settings-muted" role="status">{t("common.loading")}</p>
				) : view.isError || !data || !taskId ? (
					<p className="p-5 text-caption leading-4 text-error" role="alert">{ladoErrorText(t, view.error)}</p>
				) : (
					<TaskViewBody projectId={projectId} taskId={taskId} view={data} onClose={() => onOpenChange(false)} />
				)}
			</DialogContent>
		</Dialog>
	);
}

function TaskViewBody({
	projectId,
	taskId,
	view,
	onClose,
}: {
	projectId: string;
	taskId: string;
	view: LadoTaskView;
	onClose: () => void;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const requestPlan = useRequestLadoPlan(projectId, taskId);
	const approve = useApproveLadoPlan(projectId, taskId);
	const [tab, setTab] = useState("plan");
	const [readingRules, setReadingRules] = useState(false);
	const [rulesReadError, setRulesReadError] = useState<unknown>(null);
	const revisions = view.history.revisions;
	const latest = revisions.at(-1);
	const [selected, setSelected] = useState<string | null>(latest?.id ?? null);
	// A new revision arriving while the view is open becomes the one shown.
	useEffect(() => {
		setSelected(latest?.id ?? null);
	}, [latest?.id]);
	const plan = useLadoPlan(selected);
	const [changing, setChanging] = useState(false);
	const [note, setNote] = useState("");
	const noteId = useId();
	// The project's task list follows the worker's session and PR, which do
	// not refresh the view; the view reads the issue again only when it opens.
	const listed = useLadoTasks(projectId).data?.find((candidate) => candidate.id === taskId);
	const task = listed ?? view.task;
	const open = task.state !== "cancelled" && task.state !== "closed_unmerged";
	// A plan worker stays open while it fixes the problems of the revision it
	// pinned, so a new plan is still on its way then.
	const worker = task.plan.worker;
	const workerShown = ladoOpenPlanWorker(task) ?? (worker?.phase === "failed" ? worker : undefined);
	const pending = task.plan.requestPending || ladoOpenPlanWorker(task) !== undefined;
	const shown = plan.data;
	// An approved plan is approved again when the rules or the issue changed
	// since, or when its run failed. When the rules it was approved with block
	// the run, approving again would sign the same rules; fixing the problems
	// (agent or rules) changes them, and the task then needs review, so a
	// blocked plan is neither ready nor approvable.
	const reapprove = task.state === "review_required" || task.state === "failed";
	const blocked = task.state === "blocked";
	const queueing = task.state === "queued" || task.state === "dispatching";
	// LADO runs one worker per task: while its session lives, while LADO
	// verifies or integrates its PR, or once its work merged, the daemon
	// approves no plan of the task.
	const runHolds = runStates.has(task.state) || (task.execution !== undefined && !task.execution.sessionEnded);
	const approving = readingRules || approve.isPending;
	const approvable = open && !pending && shown !== undefined && shown.latest && shown.revision.valid && (!shown.approved || reapprove);
	const canApprove = approvable && !runHolds && !approving;
	const runLine = task.state === "failed" || (task.execution !== undefined && (runStates.has(task.state) || task.state === "closed_unmerged"));
	const notice = approvalNotice(task, view, t);
	const mutationError = requestPlan.error ?? approve.error ?? rulesReadError;
	const staleRules = rulesChanged(approve.error);

	// The approval fixes the rules that apply now, so they are read again here
	// rather than taken from the Rules tab. The desktop app's main process reads
	// and signs them itself; the hash is what a browser sends.
	const approvePlan = async (target: LadoPlan) => {
		approve.reset();
		setRulesReadError(null);
		setReadingRules(true);
		let policySha256: string;
		try {
			const rules = await queryClient.fetchQuery({
				queryKey: ladoEffectiveQueryKey(projectId, taskId),
				queryFn: () => fetchLadoEffective(projectId, taskId),
				staleTime: 0,
			});
			policySha256 = rules.snapshotSha256;
		} catch (error) {
			setRulesReadError(error);
			return;
		} finally {
			setReadingRules(false);
		}
		approve.mutate(
			{ revisionId: target.revision.id, sha256: target.revision.sha256, policySha256 },
			{
				onError: (error) => {
					// Read the rules as they are now, for the developer to check again.
					if (rulesChanged(error)) {
						void queryClient.invalidateQueries({ queryKey: ladoEffectiveQueryKey(projectId, taskId), refetchType: "all" });
					}
				},
			},
		);
	};

	const sendChanges = () => {
		const text = note.trim();
		if (!text) return;
		requestPlan.mutate(
			{ note: text },
			{
				onSuccess: () => {
					setChanging(false);
					setNote("");
				},
			},
		);
	};

	return (
		<>
			<div className="grid min-h-0 flex-1 grid-cols-1 border-t border-[var(--color-border-settings-dialog)] md:grid-cols-2 md:divide-x md:divide-[var(--color-border-settings-dialog)]">
				<IssuePane
					view={view}
					canUseComments={open}
					busy={requestPlan.isPending}
					onUseAsPlan={(comment) => requestPlan.mutate({ sourceCommentId: comment.id })}
				/>
				<Tabs value={tab} onValueChange={setTab} asChild>
					<section className="settings-thin-scrollbar flex min-h-0 flex-col gap-4 overflow-y-auto p-5">
						<div className="flex min-w-0 items-center gap-3">
							<TabsList className="h-8 shrink-0">
								{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
								<TabsTrigger value="plan">
									<span className="text-xs font-medium">{t("lado.view.plan")}</span>
								</TabsTrigger>
								<TabsTrigger value="rules">
									<span className="text-xs font-medium">{t("lado.rules.title")}</span>
								</TabsTrigger>
							</TabsList>
							{tab !== "plan" ? null : revisions.length > 1 ? (
								<Select value={selected ?? undefined} onValueChange={setSelected}>
									<SelectTrigger size="sm" className="ml-auto min-w-0 px-2.5 text-xs" aria-label={t("lado.view.plan")}>
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										{[...revisions].reverse().map((revision) => (
											<SelectItem key={revision.id} value={revision.id}>
												{revision.id === latest?.id
													? t("lado.view.revisionLatest", { number: revision.number })
													: t("lado.view.revision", { number: revision.number })}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							) : latest ? (
								<span className="ml-auto text-xs text-muted-foreground">{t("lado.view.revision", { number: latest.number })}</span>
							) : null}
						</div>
						<TabsContent value="plan" className="flex flex-none flex-col gap-4">
							{view.task.state === "cancelled" ? (
								<p className="text-control text-settings-muted">{t("lado.view.cancelled")}</p>
							) : null}
							{notice ? (
								<p className="text-caption leading-4 text-[var(--color-status-needs-you)]" role="status">{notice}</p>
							) : null}
							{approvable && runHolds ? (
								<p className="text-caption leading-4 text-settings-muted" role="status">{t("lado.error.taskRunning")}</p>
							) : null}
							{view.issueChangedSincePlan ? (
								<p className="text-caption leading-4 text-[var(--color-status-needs-you)]" role="status">{t("lado.view.issueChanged")}</p>
							) : null}
							{workerShown ? (
								<LadoPlanWorkerStatus task={task} worker={workerShown} onClose={onClose} />
							) : pending ? (
								<p className="text-control text-settings-muted" role="status">{t("lado.view.waitingForPlan")}</p>
							) : task.plan.requestError ? (
								<p className="text-caption leading-4 text-error" role="alert">
									{t("lado.view.requestFailed", { reason: task.plan.requestError })}
								</p>
							) : null}
							{shown && !shown.latest ? (
								<p className="text-caption leading-4 text-settings-muted">{t("lado.error.planSuperseded")}</p>
							) : null}
							{!latest ? (
								<p className="text-control text-settings-muted">{t("lado.view.noPlan")}</p>
							) : plan.isError ? (
								<p className="text-caption leading-4 text-error" role="alert">{ladoErrorText(t, plan.error)}</p>
							) : shown ? (
								<PlanContent plan={shown} approvals={view.history.approvals} />
							) : null}
							{changing ? (
								<div className="flex flex-col gap-2">
									<label htmlFor={noteId} className="text-xs font-medium text-foreground">{t("lado.view.changeNote")}</label>
									<textarea
										id={noteId}
										value={note}
										onChange={(event) => setNote(event.target.value)}
										placeholder={t("lado.view.changeNotePlaceholder")}
										className="settings-field-control min-h-(--size-textarea-min) resize-none rounded-md! py-2.5"
										disabled={requestPlan.isPending}
									/>
								</div>
							) : null}
						</TabsContent>
						<TabsContent value="rules" className="flex-none">
							<LadoTaskRules projectId={projectId} task={task} />
						</TabsContent>
					</section>
				</Tabs>
			</div>
			<div className={cn(settingsDialogFooterClass, "gap-2 p-4")}>
				{runLine ? <RunStatus task={task} onClose={onClose} /> : null}
				{staleRules ? (
					<p role="status" className="mr-auto text-caption leading-4 text-[var(--color-status-needs-you)]">{t("lado.view.rulesJustChanged")}</p>
				) : mutationError ? (
					<p role="alert" className="mr-auto text-caption leading-4 text-error">{ladoErrorText(t, mutationError)}</p>
				) : null}
				{!open ? null : changing ? (
					<>
						<Button type="button" variant="footer" className={compactButtonClass} disabled={requestPlan.isPending} onClick={() => setChanging(false)}>
							{t("lado.view.cancelChanges")}
						</Button>
						<Button type="button" variant="footer-primary" className={compactButtonClass} disabled={!note.trim() || requestPlan.isPending} onClick={sendChanges}>
							{requestPlan.isPending ? t("lado.view.requestingPlan") : t("lado.view.sendChanges")}
						</Button>
					</>
				) : !latest ? (
					<Button
						type="button"
						variant="footer-primary"
						className={compactButtonClass}
						disabled={pending || requestPlan.isPending}
						onClick={() => requestPlan.mutate({})}
					>
						{requestPlan.isPending ? t("lado.view.requestingPlan") : t("lado.view.requestPlan")}
					</Button>
				) : (
					<>
						<Button
							type="button"
							variant="footer"
							className={compactButtonClass}
							disabled={pending || requestPlan.isPending}
							onClick={() => {
								setChanging(true);
								setTab("plan");
							}}
						>
							{t("lado.view.requestChanges")}
						</Button>
						{queueing ? (
							<QueueStatus task={task} />
						) : shown?.approved && !reapprove && !blocked ? (
							<span className="text-control font-medium text-[var(--color-status-ready)]">{t("lado.view.approved")}</span>
						) : (
							<Button
								type="button"
								variant="footer-primary"
								className={compactButtonClass}
								disabled={!canApprove}
								onClick={() => shown && void approvePlan(shown)}
							>
								{approving ? t("lado.view.approving") : t("lado.view.approve")}
							</Button>
						)}
					</>
				)}
			</div>
		</>
	);
}

/**
 * Why an approved plan needs the developer again. Changed rules and rules that
 * block the run point to the Rules tab, and a base workers cannot start from
 * to Request changes, since only a new plan fixes it; the queue's other
 * reasons say themselves, except an edited issue, which the view already
 * reports.
 */
function approvalNotice(task: LadoTask, view: LadoTaskView, t: TFunction): string | null {
	const reason = task.stateReason;
	if (task.state === "review_required") {
		if (!reason || reason === "rules_changed") return t("lado.view.rulesNeedReview");
		return reason === "issue_edited" && view.issueChangedSincePlan ? null : ladoReasonText(t, reason);
	}
	if (task.state === "blocked") {
		if (!reason || reason === "blocking_snapshot") return t("lado.view.rulesBlocked");
		return reason === "base_ref_mismatch" ? t("lado.view.baseRefMismatch") : ladoReasonText(t, reason);
	}
	return null;
}

/** Where an approved plan waits, in place of the approval it already has. */
function QueueStatus({ task }: { task: LadoTask }) {
	const { t, i18n } = useTranslation();
	const status = ladoTaskStatus(task, t);
	const reason = ladoTaskReason(task, t, i18n.language);
	return (
		<span className="flex min-w-0 items-center gap-2 text-control" role="status" data-testid="lado-queue-status">
			<span className={cn("size-1.5 shrink-0 rounded-full", ladoTaskToneDotClass[status.tone])} aria-hidden="true" />
			<span className="shrink-0 font-medium text-foreground">{status.label}</span>
			{reason ? (
				<>
					{/* Spaces for the text read aloud; the flex gap lays the row out. */}
					{" "}
					<span aria-hidden="true" className="text-passive">·</span>{" "}
					<span className="min-w-0 truncate text-settings-muted">{reason}</span>
				</>
			) : null}
		</span>
	);
}

/**
 * The run above the footer's actions: its state and why (it failed, what
 * LADO's verification of the PR waits for or found, or that the work merged
 * without approval), the worker's session and PR, and the decision on the
 * PR's head (approve its integration, then merge it), with what the last
 * attempt ran into, or that the approved head has no status yet, under it.
 * Opening the session leaves the board for it.
 */
function RunStatus({ task, onClose }: { task: LadoTask; onClose: () => void }) {
	const { t, i18n } = useTranslation();
	const navigateToSession = useNavigateToSession();
	// The run's own state, also while a new plan for the task is on its way.
	const failed = task.state === "failed";
	const status = failed ? { label: t("lado.task.state.failed"), tone: "attention" as const } : ladoTaskStatus(task, t);
	// An approved head whose status LADO could not post yet gets its own line.
	const notPosted = task.state === "integrating" && task.stateReason === "status_not_posted";
	const reason = failed ? ladoReasonText(t, task.stateReason) : notPosted ? "" : (ladoTaskReason(task, t, i18n.language) ?? "");
	const error = failed && task.queue?.error ? ladoQueueErrorText(t, task.queue.error) : "";
	const execution = task.execution;
	const pr = execution?.pr && !execution.pr.merged && !execution.pr.closed ? execution.pr : undefined;
	const action = !pr ? null : task.state === "integration_pending" ? "approve" : task.state === "integrating" ? "merge" : null;
	// Spaces for the text read aloud; the flex gap lays the row out.
	const separator = (
		<>
			{" "}
			<span aria-hidden="true" className="text-passive">·</span>{" "}
		</>
	);
	return (
		<div className="flex min-w-0 basis-full flex-col gap-1 text-caption leading-4" data-testid="lado-run-status">
			<div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
				{/* Only the state is announced; the session and the PR are controls. */}
				<span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1" role="status">
					<span className={cn("size-1.5 shrink-0 rounded-full", ladoTaskToneDotClass[status.tone])} aria-hidden="true" />
					<span className="font-medium text-foreground">{status.label}</span>
					{reason ? (
						<>
							{separator}
							<span className="text-settings-muted">{reason}</span>
						</>
					) : null}
				</span>
				{execution ? (
					<>
						{separator}
						<button
							type="button"
							className="rounded-sm text-foreground underline-offset-2 outline-none hover:underline focus-visible:outline-2 focus-visible:outline-ring"
							onClick={() => {
								onClose();
								navigateToSession(task.projectId, execution.sessionId);
							}}
						>
							{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
							<span className="text-caption leading-4">{t("lado.view.openSession")}</span>
						</button>
					</>
				) : null}
				{execution?.pr ? (
					<>
						{separator}
						<ProductExternalLink
							href={execution.pr.url}
							ariaLabel={t("lado.view.openPR", { number: execution.pr.number })}
							className="font-mono text-settings-muted hover:text-foreground"
						>
							{t("lado.view.prNumber", { number: execution.pr.number })}
						</ProductExternalLink>
					</>
				) : null}
				{execution && pr && action ? (
					<span className="ml-auto">
						<LadoIntegrationAction
							projectId={task.projectId}
							sessionId={execution.sessionId}
							action={action}
							pr={pr}
							headSha={pr.headSha}
							variant="footer-primary"
							className={compactButtonClass}
						/>
					</span>
				) : null}
			</div>
			{notPosted ? (
				<div className="flex min-w-0 flex-col items-start gap-0.5 pl-3.5">
					<p className="text-[var(--color-status-in-review)]" data-testid="lado-run-not-posted">
						{ladoReasonText(t, "status_not_posted")}
					</p>
					{execution && pr ? <LadoRepostStatus projectId={task.projectId} sessionId={execution.sessionId} /> : null}
				</div>
			) : null}
			{/* Under the label, past the dot and its gap; the whole text on hover. */}
			{error ? (
				<p className="truncate pl-3.5 text-settings-muted" title={error} data-testid="lado-run-error">
					{error}
				</p>
			) : null}
		</div>
	);
}

function IssuePane({
	view,
	canUseComments,
	busy,
	onUseAsPlan,
}: {
	view: LadoTaskView;
	canUseComments: boolean;
	busy: boolean;
	onUseAsPlan: (comment: LadoIssueComment) => void;
}) {
	const { t } = useTranslation();
	return (
		<section aria-label={t("lado.view.issue")} className="settings-thin-scrollbar flex min-h-0 flex-col gap-4 overflow-y-auto p-5">
			{view.issueReadError ? (
				<p className="text-caption leading-4 text-error" role="alert">
					{t("lado.view.issueReadFailed", { reason: view.issueReadError })}
				</p>
			) : view.issue ? (
				view.issue.body.trim() ? (
					<ChatMarkdown text={view.issue.body} safeOrigin />
				) : (
					<p className="text-control text-settings-muted">{t("lado.view.noBody")}</p>
				)
			) : null}
			<div className="flex flex-col gap-2">
				<h3 className="text-sm font-semibold text-foreground">
					{t("lado.view.comments")} <span className="tabular-nums font-normal text-passive">{view.comments.length}</span>
				</h3>
				{view.comments.length === 0 ? (
					<p className="text-control text-settings-muted">{t("lado.view.noComments")}</p>
				) : (
					<ul className="overflow-hidden rounded-md border border-[var(--color-border-settings-dialog)]">
						{view.comments.map((comment) => (
							<li
								key={comment.id}
								className="flex flex-col gap-2 border-b border-[var(--color-border-settings-dialog)] px-3 py-2.5 last:border-b-0"
								data-testid="lado-issue-comment"
								data-comment-id={comment.id}
							>
								<div className="flex min-w-0 items-center gap-2 text-2xs text-muted-foreground">
									<span className="truncate font-medium text-foreground">{comment.author}</span>
									<span className="shrink-0 tabular-nums">{formatTimeCompact(comment.createdAt)}</span>
									{comment.included && canUseComments ? (
										<Button
											type="button"
											variant="ghost"
											size="sm"
											className="ml-auto h-6 px-2 text-2xs"
											disabled={busy}
											onClick={() => onUseAsPlan(comment)}
										>
											{t("lado.view.useAsPlan")}
										</Button>
									) : null}
								</div>
								<div className={cn(!comment.included && "opacity-70")}>
									<ChatMarkdown text={comment.body} safeOrigin muted={!comment.included} />
								</div>
								<CommentNote comment={comment} />
							</li>
						))}
					</ul>
				)}
			</div>
		</section>
	);
}

// Why a comment is left out of plan requests, if it is.
function CommentNote({ comment }: { comment: LadoIssueComment }) {
	const { t } = useTranslation();
	const note = comment.byLado
		? t("lado.view.commentByLado")
		: !comment.authorTrusted
			? t("lado.view.commentUntrusted")
			: !comment.included
				? t("lado.view.commentOmitted")
				: null;
	return note ? <p className="text-2xs text-passive">{note}</p> : null;
}

const riskKeys = {
	low: "lado.view.risk.low",
	medium: "lado.view.risk.medium",
	high: "lado.view.risk.high",
} as const;

function PlanContent({ plan, approvals }: { plan: LadoPlan; approvals: LadoTaskView["history"]["approvals"] }) {
	const { t } = useTranslation();
	const { revision, plan: content } = plan;
	const source = content.planSource?.startsWith("comment:")
		? t("lado.view.sourceComment", { comment: content.planSource.slice("comment:".length) })
		: content.planSource === "issue"
			? t("lado.view.sourceIssue")
			: null;
	const olderApproved = plan.latest && !plan.approved && approvals.some((approval) => approval.revisionId !== revision.id);
	const riskKey = content.risk ? riskKeys[content.risk as keyof typeof riskKeys] : undefined;
	const risk = riskKey ? t(riskKey) : content.risk;
	return (
		<div className="flex flex-col gap-4" data-testid="lado-plan" data-revision-id={revision.id}>
			{source || revision.source === "user" ? (
				<p className="text-2xs text-muted-foreground">
					{[source, revision.source === "user" ? t("lado.view.revisionEdited") : null].filter(Boolean).join(" · ")}
				</p>
			) : null}
			{olderApproved ? <p className="text-caption leading-4 text-settings-muted">{t("lado.view.approvedOlder")}</p> : null}
			{!revision.valid ? (
				<div className="flex flex-col gap-1.5" role="alert">
					<h4 className="text-xs font-semibold text-[var(--color-status-needs-you)]">{t("lado.view.problems")}</h4>
					<ul className="flex flex-col gap-1 text-caption leading-4 text-foreground">
						{revision.problems.map((problem, index) => (
							<li key={`${problem.code}-${problem.field ?? ""}-${index}`}>
								{ladoPlanProblemText(t, problem)}
								{problem.detail ? <span className="ml-1.5 break-all font-mono text-muted-foreground">{problem.detail}</span> : null}
							</li>
						))}
					</ul>
				</div>
			) : null}
			<dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-xs">
				{content.baseRef ? <PlanFact label={t("lado.view.baseRef")} value={content.baseRef} mono /> : null}
				{risk ? <PlanFact label={t("lado.view.risk")} value={risk} /> : null}
				{content.permissionCeiling ? <PlanFact label={t("lado.view.permission")} value={content.permissionCeiling} mono /> : null}
				{content.changeKinds.length > 0 ? (
					<PlanFact label={t("lado.view.changeKinds")} value={content.changeKinds.map((kind) => ladoChangeKindLabel(t, kind)).join(", ")} />
				) : null}
				<PlanFact
					label={t("lado.view.review")}
					value={
						content.reviewRequired
							? content.reviewer
								? t("lado.view.reviewRequiredBy", { reviewer: content.reviewer })
								: t("lado.view.reviewRequired")
							: t("lado.view.reviewNotRequired")
					}
				/>
			</dl>
			<PlanList title={t("lado.view.steps")} items={content.steps} ordered />
			<PlanList title={t("lado.view.acceptance")} items={content.acceptanceCriteria} />
			<PlanList title={t("lado.view.affectedPaths")} items={content.affectedPaths} mono />
			<PlanList title={t("lado.view.testCommands")} items={content.testCommands} mono empty={t("lado.view.noTestCommands")} />
			<LadoPlanReview plan={plan} />
			{content.body ? <ChatMarkdown text={content.body} safeOrigin /> : null}
		</div>
	);
}

function PlanFact({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
	return (
		<>
			<dt className="text-muted-foreground">{label}</dt>
			<dd className={cn("min-w-0 break-words text-foreground", mono && "font-mono")}>{value}</dd>
		</>
	);
}

function PlanList({
	title,
	items,
	ordered = false,
	mono = false,
	empty,
}: {
	title: string;
	items: string[];
	ordered?: boolean;
	mono?: boolean;
	empty?: string;
}) {
	if (items.length === 0 && !empty) return null;
	const List = ordered ? "ol" : "ul";
	return (
		<div className="flex flex-col gap-1.5">
			<h4 className="text-xs font-semibold text-foreground">{title}</h4>
			{items.length === 0 ? (
				<p className="text-xs text-muted-foreground">{empty}</p>
			) : (
				<List className={cn("flex flex-col gap-1 pl-5 text-xs leading-5 text-foreground", ordered ? "list-decimal" : "list-disc", mono && "font-mono")}>
					{items.map((item, index) => (
						<li key={`${index}-${item}`} className="break-words">{item}</li>
					))}
				</List>
			)}
		</div>
	);
}
