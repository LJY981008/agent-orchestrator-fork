// LADO fork: the LADO line on a run's board card: the plan revision and the
// rules hash its worker runs under (docs/LJY-PRD/HARNESS.md §12), then where
// LADO's verification and integration of its PR stand. It reads the
// project's tasks the board already loads, so a project out of LADO mode and
// a session LADO did not start show nothing.
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useLadoModeState, useLadoTasks } from "../../hooks/useLado";
import { type LadoTask, ladoTaskForSession } from "../../lib/lado";
import { ladoReasonText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
import { isOrchestratorSession, STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../../types/workspace";
import { ladoTaskStatus, ladoTaskToneDotClass } from "./lado-task-status";

// A product name, the same in every language.
const LADO = "LADO";

/**
 * The project whose LADO tasks can name the session. LADO lives on this
 * computer's daemon and starts only workers, so a remote, cloud, standalone
 * or orchestrator session has none.
 */
export function ladoSessionProjectId(session: WorkspaceSession, hostId = session.hostId): string | undefined {
	if (hostId || session.cloud || session.workspaceId === STANDALONE_WORKSPACE_ID || isOrchestratorSession(session)) return undefined;
	return session.workspaceId;
}

/**
 * What the card's own status line cannot say about the run's PR: that LADO
 * verifies it, waits for the integration approval or integrates it, or that
 * it merged without that approval. Empty for every other state.
 */
function runWord(task: LadoTask, t: TFunction): string {
	switch (task.state) {
		case "validating":
		case "integration_pending":
		case "integrating":
			return ladoTaskStatus(task, t).label;
		case "completed":
			return task.stateReason === "merged_without_approval" ? ladoReasonText(t, task.stateReason) : "";
		default:
			return "";
	}
}

export function LadoSessionLine({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const projectId = ladoSessionProjectId(session);
	const { ladoMode } = useLadoModeState(projectId);
	const tasks = useLadoTasks(projectId ?? "", ladoMode);
	const task = ladoMode && tasks.data ? ladoTaskForSession(tasks.data, session.id) : undefined;
	const execution = task?.execution;
	if (!task || !execution) return null;
	// The task list omits the revision once a newer one is pinned, and the
	// rules hash for the run of an older approval; the session's inspector
	// has the run's full snapshot.
	const revision = execution.planRevisionNumber;
	const rules = execution.policySha256?.slice(0, 7);
	const word = runWord(task, t);
	return (
		<p className="min-w-0 truncate py-0.5 text-2xs text-muted-foreground" data-testid="lado-session-line">
			<span className="font-medium">{LADO}</span>
			{revision !== undefined ? ` · ${t("lado.card.plan", { number: revision })}` : null}
			{rules ? (
				<>
					{` · ${t("lado.card.rules")} `}
					<span className="font-mono">{rules}</span>
				</>
			) : null}
			{word ? (
				<>
					{" · "}
					{/* Only the integration approval is the human's turn; the card keeps its one status color otherwise. */}
					{task.state === "integration_pending" ? (
						<span
							className={cn("mr-1 inline-block size-1.5 rounded-full align-middle", ladoTaskToneDotClass.attention)}
							aria-hidden="true"
							data-testid="lado-session-line-dot"
						/>
					) : null}
					{word}
				</>
			) : null}
		</p>
	);
}
