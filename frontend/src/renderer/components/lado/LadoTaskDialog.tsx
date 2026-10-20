// LADO fork: the task view (docs/LJY-PRD/TDD.md §3.3, §5.1 and §5.2): the
// issue and its comments as they are now, the pinned plan revisions, and the
// developer's decisions on them: request a plan, use a comment as the plan,
// request changes, approve. Approving goes through the desktop app's native
// confirmation (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md).
import { X } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { useApproveLadoPlan, useLadoPlan, useLadoTaskView, useRequestLadoPlan } from "../../hooks/useLado";
import { formatTimeCompact } from "../../lib/format-time";
import { ladoIssueLabel, type LadoIssueComment, type LadoPlan, type LadoRepository, type LadoTaskView } from "../../lib/lado";
import { ladoErrorText, ladoPlanProblemText } from "../../lib/lado-errors";
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

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

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
					<TaskViewBody projectId={projectId} taskId={taskId} view={data} />
				)}
			</DialogContent>
		</Dialog>
	);
}

function TaskViewBody({ projectId, taskId, view }: { projectId: string; taskId: string; view: LadoTaskView }) {
	const { t } = useTranslation();
	const requestPlan = useRequestLadoPlan(projectId, taskId);
	const approve = useApproveLadoPlan(projectId, taskId);
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
	const task = view.task;
	const open = task.state !== "cancelled" && task.state !== "closed_unmerged";
	const pending = task.plan.requestPending;
	const shown = plan.data;
	const canApprove =
		open && !pending && shown !== undefined && shown.latest && shown.revision.valid && !shown.approved && !approve.isPending;
	const mutationError = requestPlan.error ?? approve.error;

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
				<section aria-label={t("lado.view.plan")} className="settings-thin-scrollbar flex min-h-0 flex-col gap-4 overflow-y-auto p-5">
					<div className="flex min-w-0 items-center gap-3">
						<h3 className="text-sm font-semibold text-foreground">{t("lado.view.plan")}</h3>
						{revisions.length > 1 ? (
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
					{view.task.state === "cancelled" ? (
						<p className="text-control text-settings-muted">{t("lado.view.cancelled")}</p>
					) : null}
					{view.issueChangedSincePlan ? (
						<p className="text-caption leading-4 text-[var(--color-status-needs-you)]" role="status">{t("lado.view.issueChanged")}</p>
					) : null}
					{pending ? (
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
				</section>
			</div>
			<div className={cn(settingsDialogFooterClass, "gap-2 p-4")}>
				{mutationError ? (
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
						<Button type="button" variant="footer" className={compactButtonClass} disabled={pending || requestPlan.isPending} onClick={() => setChanging(true)}>
							{t("lado.view.requestChanges")}
						</Button>
						{shown?.approved ? (
							<span className="text-control font-medium text-[var(--color-status-ready)]">{t("lado.view.approved")}</span>
						) : (
							<Button
								type="button"
								variant="footer-primary"
								className={compactButtonClass}
								disabled={!canApprove}
								onClick={() => shown && approve.mutate({ revisionId: shown.revision.id, sha256: shown.revision.sha256 })}
							>
								{approve.isPending ? t("lado.view.approving") : t("lado.view.approve")}
							</Button>
						)}
					</>
				)}
			</div>
		</>
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
