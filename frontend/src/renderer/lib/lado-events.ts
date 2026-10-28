// LADO fork: what LADO's change events refresh beyond the Plan lane
// (docs/LJY-PRD/TDD.md §4.3). The shared event transport notes each event's
// payload here and flushes the batch with its own invalidation, so it only
// carries hook lines (TDD §1.2).
import type { QueryClient, QueryKey } from "@tanstack/react-query";
import {
	ladoCheckLogSessionQueryKey,
	ladoEffectiveProjectQueryKey,
	ladoEffectiveQueryKey,
	ladoExecutionQueryKey,
	ladoRuleQueryRoot,
	ladoRulesQueryRoot,
	ladoTaskForSession,
	ladoTasksQueryKey,
	ladoTaskViewQueryKey,
	ladoTaskViewQueryRoot,
	type LadoExecution,
	type LadoTask,
} from "./lado";
import { ladoSetupQueryRoot } from "./lado-setup";

type LadoEventPayload =
	| {
			kind?: unknown;
			projectId?: unknown;
			taskId?: unknown;
			sessionId?: unknown;
			isTerminated?: unknown;
			fromSession?: unknown;
	  }
	| undefined;
/** The change event's own routing fields: its type, and the project and session it names. */
type LadoEventEnvelope = { type?: unknown; projectId?: unknown; sessionId?: unknown } | undefined;

// AO's PR events. With a session ending or coming back, they move what an
// execution's task shows (running, completed, failed, closed unmerged), and
// LADO's own tables do not change then.
const prEventTypes = new Set(["pr_created", "pr_updated", "pr_session_changed"]);

/**
 * Whether the board's task list and the session's run panel show the run in
 * the session as ended; empty when neither shows a run there. Every update of
 * a session says whether it ended, so comparing keeps a live run's activity
 * from refetching LADO's views.
 */
function shownRunEnds(queryClient: Pick<QueryClient, "getQueryData">, projectId: string, sessionId: string): boolean[] {
	const tasks = queryClient.getQueryData<LadoTask[]>(ladoTasksQueryKey(projectId));
	const task = tasks ? ladoTaskForSession(tasks, sessionId) : undefined;
	const run = queryClient.getQueryData<LadoExecution | null>(ladoExecutionQueryKey(sessionId));
	return [task?.execution?.sessionEnded, run?.sessionEnded].filter((ended) => ended !== undefined);
}

/**
 * Collects LADO refreshes between flushes. note() reads one change event's
 * payload and says whether it was LADO's, which leaves the workspaces alone;
 * flush() invalidates what the noted events reach. The query client tells
 * which sessions run LADO's work as the open views show them.
 */
export function createLadoEventBatch(queryClient: Pick<QueryClient, "getQueryData">) {
	// Whether rules changed, and the LADO projects the change reaches.
	let rulesChanged = false;
	const ruleProjects = new Set<string>();
	// Whether local setup changed, and the LADO projects the change reaches.
	let setupChanged = false;
	const setupProjects = new Set<string>();
	// Tasks whose effective rules a task change can change: its agent, its approval.
	const tasks = new Map<string, { projectId: string; taskId: string }>();
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
				return true;
			}
			// Rules are global; the daemon sends one event for each LADO project a
			// change reaches.
			if (payload?.kind === "lado_rules") {
				rulesChanged = true;
				if (typeof payload.projectId === "string" && payload.projectId) ruleProjects.add(payload.projectId);
				return true;
			}
			// Local setup applies like rules: one event for each LADO project a
			// managed item, version, binding or acceptance reaches.
			if (payload?.kind === "lado_setup") {
				setupChanged = true;
				if (typeof payload.projectId === "string" && payload.projectId) setupProjects.add(payload.projectId);
				return true;
			}
			// A run started, or LADO stopped it at its deadline.
			if (payload?.kind === "lado_execution" && typeof payload.projectId === "string" && payload.projectId) {
				executionProjects.add(payload.projectId);
				if (typeof payload.sessionId === "string" && payload.sessionId) executions.add(payload.sessionId);
				if (typeof payload.taskId === "string" && payload.taskId) executionTasks.add(payload.taskId);
				return true;
			}
			// Not LADO's own event, but it can end a run, bring it back when AO
			// restores its session (a failed run runs again), or change its PR.
			// Only a project's open lanes and a session's open run panel refetch.
			if (typeof event?.projectId === "string" && event.projectId && typeof event.sessionId === "string" && event.sessionId) {
				const isTerminated = payload?.isTerminated;
				const moved =
					event.type === "session_updated" &&
					typeof isTerminated === "boolean" &&
					shownRunEnds(queryClient, event.projectId, event.sessionId).some((ended) => ended !== isTerminated);
				if (moved || (typeof event.type === "string" && prEventTypes.has(event.type))) {
					executionProjects.add(event.projectId);
					executions.add(event.sessionId);
					// A PR that moved to another session also leaves its old run.
					if (typeof payload?.fromSession === "string" && payload.fromSession) executions.add(payload.fromSession);
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
			// A setup change can send approved tasks back to review as a rule
			// change does: refresh the inventory, the items, conflicts and
			// adoptions of every scope, and what applies in each project reached.
			if (setupChanged) {
				invalidate(ladoSetupQueryRoot);
				if (setupProjects.size > 0) invalidate(ladoTaskViewQueryRoot);
				setupChanged = false;
			}
			for (const projectId of setupProjects) {
				invalidate(ladoEffectiveProjectQueryKey(projectId));
				invalidate(ladoTasksQueryKey(projectId));
			}
			setupProjects.clear();
			// The task's open Rules tab.
			for (const { projectId, taskId } of tasks.values()) invalidate(ladoEffectiveQueryKey(projectId, taskId));
			tasks.clear();
			// The run's panel with its check output, and its task's lanes, board
			// card and open view.
			for (const sessionId of executions) {
				invalidate(ladoExecutionQueryKey(sessionId));
				invalidate(ladoCheckLogSessionQueryKey(sessionId));
			}
			executions.clear();
			for (const projectId of executionProjects) invalidate(ladoTasksQueryKey(projectId));
			executionProjects.clear();
			for (const taskId of executionTasks) invalidate(ladoTaskViewQueryKey(taskId));
			executionTasks.clear();
		},
	};
}
