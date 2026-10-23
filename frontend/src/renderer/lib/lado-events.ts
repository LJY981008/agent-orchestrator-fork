// LADO fork: what LADO's change events refresh beyond the Plan lane
// (docs/LJY-PRD/TDD.md §4.3). The shared event transport notes each event's
// payload here and flushes the batch with its own invalidation, so it only
// carries hook lines (TDD §1.2).
import type { QueryKey } from "@tanstack/react-query";
import {
	ladoEffectiveProjectQueryKey,
	ladoEffectiveQueryKey,
	ladoRuleQueryRoot,
	ladoRulesQueryRoot,
	ladoTasksQueryKey,
	ladoTaskViewQueryRoot,
} from "./lado";

type LadoEventPayload = { kind?: unknown; projectId?: unknown; taskId?: unknown } | undefined;

/**
 * Collects LADO refreshes between flushes. note() reads one change event's
 * payload and says whether it was LADO's, which leaves the workspaces alone;
 * flush() invalidates what the noted events reach.
 */
export function createLadoEventBatch() {
	// Whether rules changed, and the LADO projects the change reaches.
	let rulesChanged = false;
	const ruleProjects = new Set<string>();
	// Tasks whose effective rules a task change can change: its agent, its approval.
	const tasks = new Map<string, { projectId: string; taskId: string }>();
	return {
		note(payload: LadoEventPayload): boolean {
			if (payload?.kind === "lado_task" && typeof payload.projectId === "string" && payload.projectId) {
				if (typeof payload.taskId === "string" && payload.taskId) {
					tasks.set(`${payload.projectId}\0${payload.taskId}`, { projectId: payload.projectId, taskId: payload.taskId });
				}
				return true;
			}
			// Rules are global; the daemon sends one event for each LADO project a
			// change reaches.
			if (payload?.kind === "lado_rules") {
				rulesChanged = true;
				if (typeof payload.projectId === "string" && payload.projectId) ruleProjects.add(payload.projectId);
				return true;
			}
			return false;
		},
		flush(invalidate: (queryKey: QueryKey) => void): void {
			// A rule change can send approved tasks back to review: refresh the
			// rules, what applies in each project reached, its lanes, and the
			// open task view, whose key does not name its project.
			if (rulesChanged) {
				invalidate(ladoRulesQueryRoot);
				invalidate(ladoRuleQueryRoot);
				if (ruleProjects.size > 0) invalidate(ladoTaskViewQueryRoot);
				rulesChanged = false;
			}
			for (const projectId of ruleProjects) {
				invalidate(ladoEffectiveProjectQueryKey(projectId));
				invalidate(ladoTasksQueryKey(projectId));
			}
			ruleProjects.clear();
			// The task's open Rules tab.
			for (const { projectId, taskId } of tasks.values()) invalidate(ladoEffectiveQueryKey(projectId, taskId));
			tasks.clear();
		},
	};
}
