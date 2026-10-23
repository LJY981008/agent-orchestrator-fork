// LADO fork: what LADO's change events refresh beyond the Plan lane
// (docs/LJY-PRD/TDD.md §4.3). The shared event transport notes each event's
// payload here and flushes the batch with its own invalidation, so it only
// carries hook lines (TDD §1.2).
import type { QueryKey } from "@tanstack/react-query";
import {
	ladoEffectiveProjectQueryKey,
	ladoEffectiveQueryKey,
	ladoExecutionQueryKey,
	ladoQueueQueryRoot,
	ladoRuleQueryRoot,
	ladoRulesQueryRoot,
	ladoTasksQueryKey,
	ladoTaskViewQueryKey,
	ladoTaskViewQueryRoot,
} from "./lado";

type LadoEventPayload =
	| { kind?: unknown; projectId?: unknown; taskId?: unknown; sessionId?: unknown; isTerminated?: unknown }
	| undefined;
/** The change event's own routing fields: its type, and the project and session it names. */
type LadoEventEnvelope = { type?: unknown; projectId?: unknown; sessionId?: unknown } | undefined;

// AO's PR events. With a session ending, they move what an execution's task
// shows (running, completed, failed, closed unmerged), and LADO's own tables
// do not change then.
const prEventTypes = new Set(["pr_created", "pr_updated", "pr_session_changed"]);

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
	// A task change can be its queue entry's.
	let queueChanged = false;
	// Runs that started or timed out, or whose session ended or PR changed;
	// the projects whose tasks they move; and the tasks LADO named.
	const executions = new Set<string>();
	const executionProjects = new Set<string>();
	const executionTasks = new Set<string>();
	return {
		note(payload: LadoEventPayload, event?: LadoEventEnvelope): boolean {
			if (payload?.kind === "lado_task" && typeof payload.projectId === "string" && payload.projectId) {
				if (typeof payload.taskId === "string" && payload.taskId) {
					tasks.set(`${payload.projectId}\0${payload.taskId}`, { projectId: payload.projectId, taskId: payload.taskId });
				}
				queueChanged = true;
				return true;
			}
			// Rules are global; the daemon sends one event for each LADO project a
			// change reaches.
			if (payload?.kind === "lado_rules") {
				rulesChanged = true;
				if (typeof payload.projectId === "string" && payload.projectId) ruleProjects.add(payload.projectId);
				return true;
			}
			// A run started, or LADO stopped it at its deadline.
			if (payload?.kind === "lado_execution" && typeof payload.projectId === "string" && payload.projectId) {
				executionProjects.add(payload.projectId);
				if (typeof payload.sessionId === "string" && payload.sessionId) executions.add(payload.sessionId);
				if (typeof payload.taskId === "string" && payload.taskId) executionTasks.add(payload.taskId);
				return true;
			}
			// Not LADO's own event, but it can end a run or change its PR. Only a
			// project's open lanes and a session's open run panel refetch.
			if (typeof event?.projectId === "string" && event.projectId && typeof event.sessionId === "string" && event.sessionId) {
				const ended = event.type === "session_updated" && payload?.isTerminated === true;
				if (ended || (typeof event.type === "string" && prEventTypes.has(event.type))) {
					executionProjects.add(event.projectId);
					executions.add(event.sessionId);
				}
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
			if (queueChanged) invalidate(ladoQueueQueryRoot);
			queueChanged = false;
			// The run's panel, and its task's lanes, board card and open view.
			for (const sessionId of executions) invalidate(ladoExecutionQueryKey(sessionId));
			executions.clear();
			for (const projectId of executionProjects) invalidate(ladoTasksQueryKey(projectId));
			executionProjects.clear();
			for (const taskId of executionTasks) invalidate(ladoTaskViewQueryKey(taskId));
			executionTasks.clear();
		},
	};
}
