// LADO fork: where a task's plan worker stands (docs/LJY-PRD/TDD.md §5.1).
// The task view shows its state with the dot, why it waits or what it does,
// and its designer's and critic's sessions to open, or why the request
// failed; the board card links the session that works now. Opening a session
// leaves the board for it.
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { LadoPlanWorker, LadoTask } from "../../lib/lado";
import { ladoPlanFailureText } from "../../lib/lado-errors";
import { useNavigateToSession } from "../../lib/navigate-to-session";
import { cn } from "../../lib/utils";
import { ladoPlanWorkerReason, ladoPlanWorkerStatus, ladoTaskToneDotClass } from "./lado-task-status";

/** Spaces for the text read aloud; the flex gap lays the row out. */
function Separator() {
	return (
		<>
			{" "}
			<span aria-hidden="true" className="text-passive">·</span>{" "}
		</>
	);
}

function SessionButton({ className, onClick, children }: { className?: string; onClick: () => void; children: ReactNode }) {
	return (
		<button
			type="button"
			className={cn("rounded-sm underline-offset-2 outline-none hover:underline focus-visible:outline-2 focus-visible:outline-ring", className)}
			onClick={onClick}
		>
			{children}
		</button>
	);
}

/**
 * The plan worker's line in the task view: its state and why it waits or
 * what it does, or why the request failed, then its designer's and its
 * critic's sessions, also after they stopped, to read what they did.
 */
export function LadoPlanWorkerStatus({ task, worker, onClose }: { task: LadoTask; worker: LadoPlanWorker; onClose: () => void }) {
	const { t } = useTranslation();
	const navigateToSession = useNavigateToSession();
	const failed = worker.phase === "failed";
	const status = ladoPlanWorkerStatus(worker, t);
	const reason = ladoPlanWorkerReason(worker, t);
	const open = (sessionId: string) => {
		onClose();
		navigateToSession(task.projectId, sessionId);
	};
	const sessions = [
		worker.designerSessionId ? { id: worker.designerSessionId, label: t("lado.planWorker.openDesigner") } : null,
		worker.criticSessionId ? { id: worker.criticSessionId, label: t("lado.planWorker.openCritic") } : null,
	].filter((session) => session !== null);
	return (
		<div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-caption leading-4" data-testid="lado-plan-worker">
			{/* Only the state is announced; the sessions are controls. */}
			{failed ? (
				// The details behind the code, in the daemon's words, on hover.
				<p className="text-error" role="alert" title={task.plan.requestError}>
					{t("lado.view.planWorkerFailed", { reason: ladoPlanFailureText(t, worker.failureCode) })}
				</p>
			) : (
				<span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1" role="status">
					<span className={cn("size-1.5 shrink-0 rounded-full", ladoTaskToneDotClass[status.tone])} aria-hidden="true" />
					<span className="font-medium text-foreground">{status.label}</span>
					{reason ? (
						<>
							<Separator />
							<span className="text-settings-muted">{reason}</span>
						</>
					) : null}
				</span>
			)}
			{sessions.map((session) => (
				<span key={session.id} className="contents">
					<Separator />
					<SessionButton className="text-foreground" onClick={() => open(session.id)}>
						{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
						<span className="text-caption leading-4">{session.label}</span>
					</SessionButton>
				</span>
			))}
		</div>
	);
}

/**
 * The board card's link to the plan worker session that works now: the
 * critic while it reviews the draft, the designer otherwise. Nothing before
 * the designer started.
 */
export function LadoPlanWorkerCardLink({ task, worker }: { task: LadoTask; worker: LadoPlanWorker }) {
	const { t } = useTranslation();
	const navigateToSession = useNavigateToSession();
	const critic = worker.phase === "critiquing" && worker.review === "running" ? worker.criticSessionId : undefined;
	const sessionId = critic ?? worker.designerSessionId;
	if (!sessionId) return null;
	return (
		<SessionButton className="ml-auto shrink-0 text-muted-foreground hover:text-foreground" onClick={() => navigateToSession(task.projectId, sessionId)}>
			{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
			<span className="text-2xs">{critic ? t("lado.planWorker.openCritic") : t("lado.planWorker.openDesigner")}</span>
		</SessionButton>
	);
}
