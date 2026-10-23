// LADO fork: a LADO run's active snapshot in its session's inspector
// (docs/LJY-PRD/HARNESS.md §12, PRD HAR-005). It shows what the approval
// fixed, never the rules now: the agent and the mode it started in, the
// permission ceiling, the rules that were on and the text the worker got, the
// plan revision, how far the base moved, and when LADO stops the run.
import { agentLabel, InspectorSection } from "@aoagents/product-ui";
import { ChevronDown } from "lucide-react";
import { type ReactNode, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLadoExecution } from "../../hooks/useLado";
import { formatTimeCompact } from "../../lib/format-time";
import { LadoApiError, ladoTimeUntil, type LadoExecution } from "../../lib/lado";
import { ladoErrorText, ladoReasonText } from "../../lib/lado-errors";
import { ladoRuleName } from "../../lib/lado-rules";
import { cn } from "../../lib/utils";
import type { WorkspaceSession } from "../../types/workspace";
import { ladoCeilingModeLabel } from "./LadoRuleRows";
import { ladoSessionProjectId } from "./LadoSessionLine";

export function LadoExecutionPanel({ session, hostId }: { session: WorkspaceSession; hostId?: string }) {
	const { t } = useTranslation();
	const execution = useLadoExecution(session.id, ladoSessionProjectId(session, hostId) !== undefined);
	// Every run has its snapshot; an answer without one is not a run to show.
	if (execution.data?.snapshot) return <ExecutionSnapshot execution={execution.data} />;
	// Most sessions are not LADO runs, so nothing shows while the run loads or
	// when the daemon cannot be reached; a daemon that answers for the run
	// with an error says why.
	if (execution.error instanceof LadoApiError && execution.error.code) {
		return (
			<InspectorSection title={t("lado.run.title")}>
				<p className="text-xs leading-normal text-error" role="alert">
					{ladoErrorText(t, execution.error)}
				</p>
			</InspectorSection>
		);
	}
	return null;
}

function ExecutionSnapshot({ execution }: { execution: LadoExecution }) {
	const { t, i18n } = useTranslation();
	const { snapshot, plan, base } = execution;
	const rules = snapshot.rules.map((rule) => ladoRuleName(t, rule.id, rule.name));
	// An unknown count reads as no move.
	const ahead = base.commitsAhead ?? 0;
	// Once the session ended without LADO stopping it, the deadline is moot.
	const until = execution.deadlineAt && !execution.sessionEnded ? ladoTimeUntil(execution.deadlineAt, i18n.language) : null;
	const deadline = execution.timedOutAt ? ladoReasonText(t, "timed_out") : until ? t("lado.run.stops", { time: until }) : null;
	return (
		<InspectorSection title={t("lado.run.title")}>
			<div className="flex flex-col gap-2.5" data-testid="lado-execution-panel">
				<dl className="grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-4 gap-y-1.5 text-sm">
					<Fact label={t("lado.run.agent")}>
						{agentLabel(snapshot.agent)}
						{snapshot.model ? (
							<>
								{" "}
								<span className="font-mono text-xs text-muted-foreground">{snapshot.model}</span>
							</>
						) : null}
					</Fact>
					<Fact label={t("lado.run.mode")}>{ladoCeilingModeLabel(t, snapshot.permissionMode)}</Fact>
					<Fact label={t("lado.view.permission")}>{ladoCeilingModeLabel(t, snapshot.permissionCeiling)}</Fact>
					<Fact label={t("lado.view.plan")}>
						{t("lado.run.revisionApproved", { number: plan.number, time: formatTimeCompact(plan.approvedAt) })}
					</Fact>
					<Fact label={t("lado.rules.title")}>
						{rules.length > 0 ? rules.join(", ") : t("lado.run.noRules")}
						{/* The hash the run's board card names. */}
						<span className="block font-mono text-xs text-muted-foreground">{snapshot.sha256.slice(0, 7)}</span>
					</Fact>
					{ahead > 0 ? <Fact label={t("lado.run.base")}>{t("lado.run.baseAhead", { count: ahead })}</Fact> : null}
					{deadline ? <Fact label={t("lado.run.deadline")}>{deadline}</Fact> : null}
				</dl>
				<WorkerText prompt={snapshot.prompt} />
			</div>
		</InspectorSection>
	);
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
	return (
		<>
			<dt className="text-muted-foreground">{label}</dt>
			<dd className="min-w-0 break-words text-foreground">{children}</dd>
		</>
	);
}

/** The rule text the worker's system prompt got: collapsed until asked for. */
function WorkerText({ prompt }: { prompt: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	return (
		<div className="flex flex-col gap-1.5">
			<button
				type="button"
				aria-expanded={open}
				onClick={() => setOpen((current) => !current)}
				className="flex h-6 w-fit items-center gap-1.5 rounded-sm text-muted-foreground outline-none hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
			>
				{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
				<span className="text-sm">{t("lado.task.rules.workerText")}</span>
				<ChevronDown aria-hidden="true" className={cn("size-icon-2xs transition-transform duration-fast", open && "rotate-180")} />
			</button>
			{open ? (
				prompt ? (
					<div
						className="whitespace-pre-wrap break-words rounded-md bg-(--color-bg-secondary) px-3 py-2 text-xs leading-5 text-foreground"
						data-testid="lado-run-worker-text"
					>
						{prompt}
					</div>
				) : (
					<p className="text-xs text-muted-foreground">{t("lado.task.rules.noWorkerText")}</p>
				)
			) : null}
		</div>
	);
}
