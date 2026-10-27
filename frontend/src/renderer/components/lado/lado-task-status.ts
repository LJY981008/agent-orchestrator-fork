// LADO fork: how a task's card and view name its state (docs/LJY-PRD/TDD.md
// §3.2): one status line with a dot, and a second line for what a queued task
// waits for or why a task needs the human again or failed.
import type { TFunction } from "i18next";
import { LADO_MAX_ATTEMPTS, type LadoTask } from "../../lib/lado";
import { ladoReasonText } from "../../lib/lado-errors";
import { formatResetTime } from "../settings/CodexAccountDetails";

export type LadoTaskStatusTone = "muted" | "attention" | "ready" | "working" | "merged";

/**
 * The card's one status line. A plan waiting for review is the human's next
 * decision, so it takes the needs-you color; an approved plan waiting in the
 * queue reads as ready, and one whose worker LADO is starting as working. An
 * approval that needs review again (its rules or its issue changed, or it was
 * never queued), whose rules block the run (blocked), or whose run failed
 * needs the human again, unless a new plan it waits for is requested. A
 * pull request LADO verifies reads as working; one waiting for the
 * integration approval takes the needs-you color, an approved head as ready,
 * and work merged without that approval needs the human's attention.
 */
export function ladoTaskStatus(task: LadoTask, t: TFunction): { label: string; tone: LadoTaskStatusTone } {
	switch (task.state) {
		case "closed_unmerged":
			// After a run, the work closed without a merge, whichever closed first.
			return task.execution
				? { label: t("lado.task.state.closedUnmerged"), tone: "muted" }
				: { label: t("lado.task.state.closed"), tone: "muted" };
		case "approved":
			return { label: t("lado.task.state.approved"), tone: "ready" };
		case "queued":
			return { label: t("lado.task.state.queued"), tone: "ready" };
		case "dispatching":
			return { label: t("lado.task.state.dispatching"), tone: "working" };
		case "running":
			return { label: t("lado.task.state.running"), tone: "working" };
		case "validating":
			return { label: t("lado.task.state.validating"), tone: "working" };
		case "integration_pending":
			return { label: t("lado.task.state.integrationPending"), tone: "attention" };
		case "integrating":
			return { label: t("lado.task.state.integrating"), tone: "ready" };
		case "completed":
			return task.stateReason === "merged_without_approval"
				? { label: t("lado.task.state.completed"), tone: "attention" }
				: { label: t("lado.task.state.completed"), tone: "merged" };
		case "review_required":
			if (task.plan.requestPending) return { label: t("lado.task.state.planRequested"), tone: "muted" };
			// Only changed rules make it a rules review; the second line says why otherwise.
			return task.stateReason && task.stateReason !== "rules_changed"
				? { label: t("lado.task.state.approveAgain"), tone: "attention" }
				: { label: t("lado.task.state.reviewRequired"), tone: "attention" };
		case "blocked":
			if (task.plan.requestPending) return { label: t("lado.task.state.planRequested"), tone: "muted" };
			return { label: t("lado.task.state.blocked"), tone: "attention" };
		case "failed":
			if (task.plan.requestPending) return { label: t("lado.task.state.planRequested"), tone: "muted" };
			return { label: t("lado.task.state.failed"), tone: "attention" };
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

/**
 * The card's second line: what a queued task waits for and which attempt
 * starts it, why a task needs the human again or failed, what LADO's
 * verification of its pull request waits for or found, why an approved head
 * has no status yet, or that its work merged without approval. Null when the
 * status line says it all, or when a new plan is on its way.
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
			else if (reason) parts.push(ladoReasonText(t, reason));
			const attempts = task.queue?.attempts ?? 0;
			if (attempts > 0) parts.push(t("lado.lane.attempt", { attempt: attempts, max: LADO_MAX_ATTEMPTS }));
			return parts.length > 0 ? parts.join(" · ") : null;
		}
		case "review_required":
			if (task.plan.requestPending || reason === "rules_changed") return null;
			return reason ? ladoReasonText(t, reason) : null;
		case "blocked":
		case "failed":
			if (task.plan.requestPending) return null;
			return reason ? ladoReasonText(t, reason) : null;
		case "validating":
		case "integrating":
			return reason ? ladoReasonText(t, reason) : null;
		case "completed":
			return reason === "merged_without_approval" ? ladoReasonText(t, reason) : null;
		default:
			return null;
	}
}

export const ladoTaskToneDotClass: Record<LadoTaskStatusTone, string> = {
	muted: "bg-[var(--color-status-idle)]",
	attention: "bg-[var(--color-status-needs-you)]",
	ready: "bg-[var(--color-status-ready)]",
	working: "bg-[var(--color-status-working)]",
	merged: "bg-[var(--color-status-merged)]",
};
