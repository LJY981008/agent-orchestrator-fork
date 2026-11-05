// LADO fork: the board card's link to a task's live part workers
// (docs/LJY-PRD/TDD.md §3.3, §5.4). While a task runs its parts it has no
// session card of its own, so its card in the Queued lane opens the part
// workers that work now: one link for one worker, a menu for several.
import { ChevronDown } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useLadoTaskParts } from "../../hooks/useLado";
import type { LadoTask, LadoTaskPart } from "../../lib/lado";
import { useNavigateToSession } from "../../lib/navigate-to-session";
import { cn } from "../../lib/utils";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";

type LivePartWorker = { part: LadoTaskPart; sessionId: string; merge: boolean };

/**
 * The sessions that work now, by the parts' states only: a running part's
 * worker and a conflicted part's merge worker.
 */
function livePartWorkers(parts: readonly LadoTaskPart[]): LivePartWorker[] {
	const live: LivePartWorker[] = [];
	for (const part of parts) {
		if (part.state === "running" && part.sessionId) live.push({ part, sessionId: part.sessionId, merge: false });
		else if (part.state === "conflict" && part.mergeSessionId) live.push({ part, sessionId: part.mergeSessionId, merge: true });
	}
	return live;
}

const linkClass =
	"ml-auto shrink-0 rounded-sm text-muted-foreground underline-offset-2 outline-none hover:text-foreground hover:underline focus-visible:outline-2 focus-visible:outline-ring";

/** Nothing while the parts load or when no part worker runs. */
export function LadoPartWorkerCardLink({ task }: { task: LadoTask }) {
	const { t } = useTranslation();
	const navigateToSession = useNavigateToSession();
	const parts = useLadoTaskParts(task.id, true);
	const live = livePartWorkers(parts.data?.parts ?? []);
	if (live.length === 0) return null;
	if (live.length === 1) {
		const [worker] = live;
		return (
			<button
				type="button"
				className={linkClass}
				aria-label={t("lado.task.partWorker.openNamed", { part: worker.part.id })}
				onClick={() => navigateToSession(task.projectId, worker.sessionId)}
			>
				{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
				<span className="text-2xs">{t("lado.task.partWorker.open")}</span>
			</button>
		);
	}
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<button type="button" className={cn(linkClass, "flex items-center gap-0.5")}>
					<span className="text-2xs">{t("lado.task.partWorker.menu", { count: live.length })}</span>
					<ChevronDown className="size-icon-2xs" aria-hidden="true" />
				</button>
			</DropdownMenuTrigger>
			{/* React sends the menu's clicks up through the portal to the card, which would open the task view. */}
			<DropdownMenuContent align="end" onClick={(event) => event.stopPropagation()}>
				{live.map((worker) => (
					<DropdownMenuItem key={`${worker.part.id}:${worker.sessionId}`} onSelect={() => navigateToSession(task.projectId, worker.sessionId)}>
						<span className="min-w-0 truncate">
							<span className="font-mono">{worker.part.id}</span>
							{" · "}
							{worker.part.title}
							{worker.merge ? ` · ${t("lado.parts.mergeWorker")}` : ""}
						</span>
					</DropdownMenuItem>
				))}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
