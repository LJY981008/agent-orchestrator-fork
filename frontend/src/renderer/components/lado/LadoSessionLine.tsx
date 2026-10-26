// LADO fork: the LADO line on a run's board card: the plan revision and the
// rules hash its worker runs under (docs/LJY-PRD/HARNESS.md §12). It reads the
// project's tasks the board already loads, so a project out of LADO mode and
// a session LADO did not start show nothing.
import { useTranslation } from "react-i18next";
import { useLadoModeState, useLadoTasks } from "../../hooks/useLado";
import { ladoTaskForSession } from "../../lib/lado";
import { isOrchestratorSession, STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../../types/workspace";

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

export function LadoSessionLine({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const projectId = ladoSessionProjectId(session);
	const { ladoMode } = useLadoModeState(projectId);
	const tasks = useLadoTasks(projectId ?? "", ladoMode);
	const execution = ladoMode && tasks.data ? ladoTaskForSession(tasks.data, session.id)?.execution : undefined;
	if (!execution) return null;
	// The task list omits the revision once a newer one is pinned, and the
	// rules hash for the run of an older approval; the session's inspector
	// has the run's full snapshot.
	const revision = execution.planRevisionNumber;
	const rules = execution.policySha256?.slice(0, 7);
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
		</p>
	);
}
