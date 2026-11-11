// LADO fork: what the board says of a task whose production SQL waits or
// failed (issue #97). The board reads the project's summaries once for all
// its cards, only for a project in LADO mode; database change events refresh
// them. A pull request merged outside LADO without its pre-merge SQL gives
// the card a red edge, after-deploy SQL that waits for the developer an
// amber one, and the other states only the small badge.
import { Database } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useLadoProjectDBChanges } from "../../hooks/useLado";
import type { MessageKey } from "../../i18n/messages";
import { type LadoDBChangeBoardState, type LadoDBChangeSummary, ladoDBChangeSummariesBy } from "../../lib/lado-db-changes";
import { cn } from "../../lib/utils";

/** Each task's and each session's most urgent summary; read only while enabled. */
export function useLadoBoardDBChanges(
	projectId: string,
	enabled: boolean,
): { ofTask: (taskId: string) => LadoDBChangeSummary | undefined; ofSession: (sessionId: string) => LadoDBChangeSummary | undefined } {
	const summaries = useLadoProjectDBChanges(projectId, enabled);
	return useMemo(() => {
		const { byTask, bySession } = ladoDBChangeSummariesBy(summaries.data);
		return { ofTask: (taskId: string) => byTask.get(taskId), ofSession: (sessionId: string) => bySession.get(sessionId) };
	}, [summaries.data]);
}

/** The card's edge for a summary: red for SQL a merge left out, amber for after-deploy SQL; none otherwise. */
export function ladoDBChangeBorderClass(summary: LadoDBChangeSummary | undefined): string | undefined {
	if (summary?.state === "merged_unapplied") return "border-[var(--color-status-exited)]";
	if (summary?.state === "after_deploy_pending") return "border-[var(--color-status-in-review)]";
	return undefined;
}

const stateKeys: Record<LadoDBChangeBoardState, MessageKey> = {
	apply_waiting: "lado.card.db.apply_waiting",
	applying: "lado.card.db.applying",
	apply_failed: "lado.card.db.apply_failed",
	preflight_blocked: "lado.card.db.preflight_blocked",
	merge_failed: "lado.card.db.merge_failed",
	after_deploy_pending: "lado.card.db.after_deploy_pending",
	merged_unapplied: "lado.card.db.merged_unapplied",
};

// Failures and SQL a merge left out read as failures, what waits for the
// developer as caution, and what LADO does on its own stays quiet.
const stateTextClass: Record<LadoDBChangeBoardState, string> = {
	apply_waiting: "text-muted-foreground",
	applying: "text-[var(--color-status-working)]",
	apply_failed: "text-[var(--color-status-exited)]",
	preflight_blocked: "text-[var(--color-status-in-review)]",
	merge_failed: "text-[var(--color-status-exited)]",
	after_deploy_pending: "text-[var(--color-status-in-review)]",
	merged_unapplied: "text-[var(--color-status-exited)]",
};

/** The card's mark: the state of the task's most urgent SQL, with the stop's reason as its hint. */
export function LadoDBChangeBadge({ summary, className }: { summary: LadoDBChangeSummary | undefined; className?: string }) {
	const { t } = useTranslation();
	if (!summary) return null;
	const label = t(stateKeys[summary.state]);
	return (
		<span
			className={cn("inline-flex min-w-0 shrink items-center gap-1 font-sans text-2xs font-medium", stateTextClass[summary.state], className)}
			title={summary.message || label}
			data-testid="lado-card-db-change"
			data-state={summary.state}
		>
			<Database className="size-icon-2xs shrink-0" aria-hidden="true" />
			<span className="truncate">{label}</span>
		</span>
	);
}
