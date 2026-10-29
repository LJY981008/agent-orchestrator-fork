// LADO fork: how a task's card and view name its state (docs/LJY-PRD/TDD.md
// §3.2): one status line with a dot, and a second line for what a queued task
// waits for, why a task needs the human again or failed, or what its plan
// worker does (§5.1).
import type { TFunction } from "i18next";
import { LADO_MAX_ATTEMPTS, type LadoPlanWorker, type LadoTask } from "../../lib/lado";
import { ladoPlanFailureText, ladoPlanWaitingText, ladoPlanWorkerPhaseText, ladoReasonText } from "../../lib/lado-errors";
import { formatResetTime } from "../settings/CodexAccountDetails";

export type LadoTaskStatusTone = "muted" | "attention" | "ready" | "working" | "validating" | "merged";

/** The states a run's pull request gives its task. */
export type LadoRunState = Extract<LadoTask["state"], "validating" | "integration_pending" | "integrating" | "completed" | "closed_unmerged">;

/**
 * How the task's card, its view and the run's panel name a run's pull
 * request state: LADO verifying it takes the validating color (DESIGN.md
 * §5, U8 of #15), waiting for the
 * integration approval takes the needs-you color, an approved head reads as
 * ready, and work merged without that approval needs the human's attention.
 */
export function ladoRunStatus(state: LadoRunState, reason: string | undefined, t: TFunction): { label: string; tone: LadoTaskStatusTone } {
	switch (state) {
		case "validating":
			return { label: t("lado.task.state.validating"), tone: "validating" };
		case "integration_pending":
			return { label: t("lado.task.state.integrationPending"), tone: "attention" };
		case "integrating":
			return { label: t("lado.task.state.integrating"), tone: "ready" };
		case "completed":
			return { label: t("lado.task.state.completed"), tone: reason === "merged_without_approval" ? "attention" : "merged" };
		case "closed_unmerged":
			return { label: t("lado.task.state.closedUnmerged"), tone: "muted" };
	}
}

/**
 * The task's plan worker while its request is open (docs/LJY-PRD/TDD.md
 * §5.1): waiting for its turn, or designing, reviewing or fixing the plan,
 * also after it pinned a revision with problems.
 */
export function ladoOpenPlanWorker(task: LadoTask): LadoPlanWorker | undefined {
	const worker = task.plan.worker;
	if (!worker) return undefined;
	switch (worker.phase) {
		case "done":
		case "failed":
		case "replaced":
			return undefined;
		default:
			return worker;
	}
}

/**
 * An open plan worker's status line: waiting for its turn is LADO's to
 * handle, so it is muted; writing the plan is an agent at work.
 */
export function ladoPlanWorkerStatus(worker: LadoPlanWorker, t: TFunction): { label: string; tone: LadoTaskStatusTone } {
	return worker.phase === "waiting"
		? { label: t("lado.task.state.planWaiting"), tone: "muted" }
		: { label: t("lado.task.state.planWriting"), tone: "working" };
}

/** Why an open plan worker waits, or what it does; null when it waits for no reason it names. */
export function ladoPlanWorkerReason(worker: LadoPlanWorker, t: TFunction): string | null {
	if (worker.phase === "waiting") return ladoPlanWaitingText(t, worker.waitingReason) || null;
	return ladoPlanWorkerPhaseText(t, worker.phase);
}

/**
 * The status line while a new plan is on its way: its plan worker's, or for a
 * request made before plan workers, Plan requested. Undefined when no plan
 * is on its way.
 */
function planComingStatus(task: LadoTask, t: TFunction): { label: string; tone: LadoTaskStatusTone } | undefined {
	const worker = ladoOpenPlanWorker(task);
	if (worker) return ladoPlanWorkerStatus(worker, t);
	return task.plan.requestPending ? { label: t("lado.task.state.planRequested"), tone: "muted" } : undefined;
}

/** The second line while a new plan is on its way; null for a request made before plan workers. */
function planComingReason(task: LadoTask, t: TFunction): string | null {
	const worker = ladoOpenPlanWorker(task);
	return worker ? ladoPlanWorkerReason(worker, t) : null;
}

/** Whether a new plan is on its way: an open plan worker, or a pending request. */
function planComing(task: LadoTask): boolean {
	return ladoOpenPlanWorker(task) !== undefined || task.plan.requestPending;
}

/**
 * The card's one status line. A plan waiting for review is the human's next
 * decision, so it takes the needs-you color; an approved plan waiting in the
 * queue reads as ready, and one whose worker LADO is starting as working. An
 * approval that needs review again (its rules or its issue changed, or it was
 * never queued), whose rules block the run (blocked), or whose run failed
 * needs the human again, unless a new plan it waits for is on its way. A
 * run's pull request reads as ladoRunStatus names it.
 */
export function ladoTaskStatus(task: LadoTask, t: TFunction): { label: string; tone: LadoTaskStatusTone } {
	const coming = planComingStatus(task, t);
	switch (task.state) {
		case "closed_unmerged":
			// After a run, the work closed without a merge, whichever closed first.
			return task.execution ? ladoRunStatus(task.state, task.stateReason, t) : { label: t("lado.task.state.closed"), tone: "muted" };
		case "approved":
			return { label: t("lado.task.state.approved"), tone: "ready" };
		case "queued":
			return { label: t("lado.task.state.queued"), tone: "ready" };
		case "dispatching":
			return { label: t("lado.task.state.dispatching"), tone: "working" };
		case "running":
			return { label: t("lado.task.state.running"), tone: "working" };
		case "validating":
		case "integration_pending":
		case "integrating":
		case "completed":
			return ladoRunStatus(task.state, task.stateReason, t);
		case "review_required":
			if (coming) return coming;
			// Only changed rules make it a rules review; the second line says why otherwise.
			return task.stateReason && task.stateReason !== "rules_changed"
				? { label: t("lado.task.state.approveAgain"), tone: "attention" }
				: { label: t("lado.task.state.reviewRequired"), tone: "attention" };
		case "blocked":
			return coming ?? { label: t("lado.task.state.blocked"), tone: "attention" };
		case "failed":
			return coming ?? { label: t("lado.task.state.failed"), tone: "attention" };
		case "planned":
			if (coming) return coming;
			return task.plan.latest?.valid
				? { label: t("lado.task.state.planned"), tone: "attention" }
				: { label: t("lado.task.state.planInvalid"), tone: "attention" };
		default:
			if (coming) return coming;
			if (task.plan.requestError || task.plan.worker?.phase === "failed") return { label: t("lado.task.state.planRequestFailed"), tone: "attention" };
			return { label: t("lado.task.state.draft"), tone: "muted" };
	}
}

/**
 * The card's second line: what a queued task waits for and which attempt
 * starts it, why a task needs the human again or failed, what LADO's
 * verification of its pull request waits for or found, why an approved head
 * has no status yet, or that its work merged without approval. While a new
 * plan is on its way, why its plan worker waits or what it does instead, and
 * for a task without a plan, why its plan worker failed. Null when the status
 * line says it all.
 */
export function ladoTaskReason(task: LadoTask, t: TFunction, locale: string): string | null {
	const reason = task.stateReason;
	switch (task.state) {
		case "queued":
		case "dispatching": {
			const parts: string[] = [];
			// Only a retry waits for a time of its own; the other waits end when a
			// slot, memory or the issue frees up. The clock time stays true on a
			// card nobody refreshes, where "in 2 min." would not.
			const retryAt = reason === "retry" ? formatResetTime(task.queue?.nextAttemptAt, locale) : null;
			if (retryAt) parts.push(t("lado.lane.retryAt", { time: retryAt.visible }));
			else if (reason === "paths_overlap" && task.queue?.waitingDetail) {
				parts.push(t("lado.reason.paths_overlapWith", { issue: ladoIssueLabel(task.queue.waitingDetail) }));
			} else if (reason) parts.push(ladoReasonText(t, reason));
			const attempts = task.queue?.attempts ?? 0;
			if (attempts > 0) parts.push(t("lado.lane.attempt", { attempt: attempts, max: LADO_MAX_ATTEMPTS }));
			return parts.length > 0 ? parts.join(" · ") : null;
		}
		case "review_required":
			if (planComing(task)) return planComingReason(task, t);
			if (reason === "rules_changed") return null;
			return reason ? ladoReasonText(t, reason) : null;
		case "blocked":
		case "failed":
			if (planComing(task)) return planComingReason(task, t);
			return reason ? ladoReasonText(t, reason) : null;
		case "planned":
			return planComing(task) ? planComingReason(task, t) : null;
		case "draft":
			if (planComing(task)) return planComingReason(task, t);
			return task.plan.worker?.phase === "failed" ? ladoPlanFailureText(t, task.plan.worker.failureCode) || null : null;
		case "running":
			// A task split into parts says how many are merged.
			return task.parts ? t("lado.task.parts", { merged: task.parts.merged, total: task.parts.total }) : null;
		case "validating":
		case "integrating":
			return reason ? ladoReasonText(t, reason) : null;
		case "completed":
			return reason === "merged_without_approval" ? ladoReasonText(t, reason) : null;
		default:
			return null;
	}
}

/** An issue ref as people say it: "#7" for "github:acme/app#7". */
export function ladoIssueLabel(ref: string): string {
	const hash = ref.lastIndexOf("#");
	return hash >= 0 ? ref.slice(hash) : ref;
}

export const ladoTaskToneDotClass: Record<LadoTaskStatusTone, string> = {
	muted: "bg-[var(--color-status-idle)]",
	attention: "bg-[var(--color-status-needs-you)]",
	ready: "bg-[var(--color-status-ready)]",
	working: "bg-[var(--color-status-working)]",
	validating: "bg-[var(--color-status-validating)]",
	merged: "bg-[var(--color-status-merged)]",
};
