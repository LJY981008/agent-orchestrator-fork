// LADO fork: the task view's Rules tab (docs/LJY-PRD/HARNESS.md §5-§9 and
// §12, PRD HAR-005): what applies to the task's next run, what keeps it from
// running, whether the rules and the local setup are still the ones its
// approval fixed, the warnings accepted for it, and the task's own overrides
// of rules and setup items.
import { agentLabel } from "@aoagents/product-ui";
import { ChevronDown } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useLadoEffective, useLadoRules } from "../../hooks/useLado";
import { useLadoSetupConflicts } from "../../hooks/useLadoSetup";
import type { MessageKey } from "../../i18n/messages";
import type { LadoEffective, LadoPolicyProblem, LadoTask } from "../../lib/lado";
import { ladoErrorText, ladoSetupConflictText } from "../../lib/lado-errors";
import { ladoProblemText, ladoRuleName, useLadoRuleChange } from "../../lib/lado-rules";
import { cn } from "../../lib/utils";
import { SettingsSection } from "../settings/SettingsSection";
import { LadoAORulesSection, LadoEffectiveRuleRow, type LadoOverrideState } from "./LadoProjectRulesSection";
import { LadoRuleImpactDialog } from "./LadoRuleImpactDialog";
import { ladoCeilingModeLabel } from "./LadoRuleRows";
import { LadoSetupScopeCard } from "./setup/LadoSetupScopeRows";

type Severity = LadoPolicyProblem["severity"];

// Blocking takes the needs-you color, a warning the caution color; each also
// says its severity in words.
const severityDotClass: Record<Severity, string> = {
	blocking: "bg-[var(--color-status-needs-you)]",
	warning: "bg-[var(--color-status-in-review)]",
	info: "bg-[var(--color-status-idle)]",
};

const severityKeys: Record<Severity, MessageKey> = {
	blocking: "lado.policy.severity.blocking",
	warning: "lado.policy.severity.warning",
	info: "lado.policy.severity.info",
};

export function LadoTaskRules({ projectId, task }: { projectId: string; task: LadoTask }) {
	const { t } = useTranslation();
	const effective = useLadoEffective(projectId, task.id);
	const change = useLadoRuleChange();
	const [changing, setChanging] = useState<string | null>(null);
	const [failure, setFailure] = useState<{ ruleId: string; error: unknown } | null>(null);
	const data = effective.data;
	// A cancelled task or a closed issue takes no new overrides.
	const open = task.state !== "cancelled" && task.state !== "closed_unmerged";

	if (effective.isPending) {
		return (
			<p className="text-control text-settings-muted" role="status">
				{t("common.loading")}
			</p>
		);
	}
	if (!data) {
		return (
			<p className="text-caption leading-4 text-error" role="alert">
				{ladoErrorText(t, effective.error)}
			</p>
		);
	}

	const setOverride = (ruleId: string, state: LadoOverrideState) => {
		if (change.isPending) return;
		setChanging(ruleId);
		setFailure(null);
		change
			.change({ kind: "binding", input: { ruleId, scope: "task", scopeId: task.id, state } })
			.catch((error: unknown) => setFailure({ ruleId, error }))
			.finally(() => setChanging(null));
	};

	const mode = ladoCeilingModeLabel(t, data.permissionMode);
	return (
		<div className="flex flex-col gap-(--size-settings-section-gap)" data-testid="lado-task-rules">
			{/* At the inset of the card rows below. */}
			<div className="flex flex-col gap-3 px-(--settings-row-inset)" data-testid="lado-task-rules-summary">
				<p className="text-sm text-foreground">
					{data.agent.harness
						? t("lado.task.rules.runsWith", { agent: agentLabel(data.agent.harness), mode })
						: t("lado.task.rules.noAgent", { mode })}
				</p>
				<ApprovalComparison effective={data} task={task} />
				<ProblemList effective={data} />
				<AcceptedWarnings effective={data} projectId={projectId} taskId={task.id} />
			</div>
			<SettingsSection title={t("lado.task.rules.title")} grouped>
				{data.rules.map((rule) => (
					<LadoEffectiveRuleRow
						key={rule.ruleId}
						rule={rule}
						scope="task"
						ceiling={data.permissionCeiling}
						busy={changing === rule.ruleId}
						readOnly={!open}
						error={failure?.ruleId === rule.ruleId ? failure.error : undefined}
						onChange={(state) => setOverride(rule.ruleId, state)}
					/>
				))}
			</SettingsSection>
			<LadoSetupScopeCard title={t("lado.setup.task.title")} scope="task" scopeId={task.id} items={data.setup.items} showTool={false} readOnly={!open} />
			<LadoAORulesSection rules={data.aoRules} />
			<WorkerText prompt={data.prompt} />
			<LadoRuleImpactDialog
				impact={change.pending?.impact ?? null}
				busy={change.isPending}
				error={change.pending ? change.error : null}
				onConfirm={() => void change.confirm().catch(() => undefined)}
				onCancel={change.cancel}
			/>
		</div>
	);
}

/** What keeps the run from starting, then what to know about it, worst first. */
function ProblemList({ effective }: { effective: LadoEffective }) {
	const { t } = useTranslation();
	if (effective.problems.length === 0) return null;
	return (
		<ul aria-label={t("lado.task.rules.problems")} className="flex flex-col gap-1.5">
			{effective.problems.map((problem, index) => (
				<li
					key={`${problem.code}-${problem.rules.join(",")}-${index}`}
					className="flex min-w-0 items-baseline gap-2 text-caption leading-4"
					data-testid="lado-policy-problem"
					data-severity={problem.severity}
				>
					<span className={cn("size-1.5 shrink-0 translate-y-[-1px] rounded-full", severityDotClass[problem.severity])} aria-hidden="true" />
					<span className="min-w-0 text-foreground">
						<span className="font-medium">{t(severityKeys[problem.severity])}</span>
						<span className="text-muted-foreground"> · </span>
						{ladoProblemText(t, problem, effective.rules)}
					</span>
				</li>
			))}
		</ul>
	);
}

/**
 * Whether the rules are still the ones the approval fixed. Without a valid
 * approval there is nothing to compare.
 */
function ApprovalComparison({ effective, task }: { effective: LadoEffective; task: LadoTask }) {
	const { t } = useTranslation();
	const approved = effective.approved;
	const changes = approved && !approved.matches ? [...approved.added, ...approved.removed, ...approved.changed] : [];
	const setupNames = useSetupChangeNames(effective);
	// A removed rule can be archived since, so the current rules may not name it.
	const unnamed = changes.some((ruleId) => !effective.rules.some((rule) => rule.ruleId === ruleId));
	const allRules = useLadoRules(true, unnamed);
	if (!approved) {
		if (!task.plan.approvedRevisionId) return null;
		return <ComparisonLine tone="attention" text={t("lado.task.rules.approvedBeforeRules")} />;
	}
	if (approved.matches) {
		// Rules approved with a blocking problem keep the run from starting
		// (Blocked): approved, but not ready.
		return approved.blocking ? (
			<ComparisonLine tone="attention" text={t("lado.task.rules.approvedBlocking")} />
		) : (
			<ComparisonLine tone="ready" text={t("lado.task.rules.approvedMatches")} />
		);
	}
	const names = new Map<string, string>();
	for (const rule of allRules.data ?? []) names.set(rule.id, ladoRuleName(t, rule.id, rule.name));
	for (const rule of effective.rules) names.set(rule.ruleId, ladoRuleName(t, rule.ruleId, rule.name));
	const list = (ids: readonly string[]) => ids.map((ruleId) => names.get(ruleId) ?? ruleId).join(", ");
	const lines = [
		{ key: "added", ids: approved.added, label: (rules: string) => t("lado.task.rules.added", { rules }) },
		{ key: "removed", ids: approved.removed, label: (rules: string) => t("lado.task.rules.removed", { rules }) },
		{ key: "changed", ids: approved.changed, label: (rules: string) => t("lado.task.rules.changed", { rules }) },
	];
	const setupLines = [
		{ key: "setupAdded", ids: approved.setupAdded, label: (items: string) => t("lado.setup.task.added", { items }) },
		{ key: "setupRemoved", ids: approved.setupRemoved, label: (items: string) => t("lado.setup.task.removed", { items }) },
		{ key: "setupChanged", ids: approved.setupChanged, label: (items: string) => t("lado.setup.task.changed", { items }) },
	].filter((line) => line.ids.length > 0);
	const listed = lines.filter((line) => line.ids.length > 0);
	return (
		<div className="flex flex-col gap-1">
			<ComparisonLine tone="attention" text={t("lado.task.rules.approvedChanged")} />
			{listed.map((line) => (
				<p key={line.key} className="pl-3.5 text-caption leading-4 text-muted-foreground">
					{line.label(list(line.ids))}
				</p>
			))}
			{setupLines.map((line) => (
				<p key={line.key} className="pl-3.5 text-caption leading-4 text-muted-foreground" data-testid="lado-approval-setup">
					{line.label(line.ids.map(setupNames).join(", "))}
				</p>
			))}
			{/* The snapshot also holds the agent, its mode, the AO instructions and AO's build. */}
			{listed.length === 0 && setupLines.length === 0 ? (
				<p className="pl-3.5 text-caption leading-4 text-muted-foreground">{t("lado.task.rules.changedOutsideRules")}</p>
			) : null}
		</div>
	);
}

/**
 * A setup item's name in the approval comparison: from the setup now or the
 * approved snapshot, and, for an enabledPlugins setting whose version stayed,
 * that the installed plugins changed.
 */
function useSetupChangeNames(effective: LadoEffective): (itemId: string) => string {
	const { t } = useTranslation();
	const approved = new Map((effective.approved?.setup ?? []).map((item) => [item.itemId, item]));
	const now = new Map(effective.setup.items.map((item) => [item.itemId, item]));
	return (itemId) => {
		const before = approved.get(itemId);
		const current = now.get(itemId);
		const name = current?.name ?? before?.name ?? itemId;
		return before && current && before.versionId === current.versionId && before.installedSha256 !== current.installedSha256
			? t("lado.setup.task.pluginsChanged", { name })
			: name;
	};
}

/**
 * The setup warnings an acceptance covers for this task, which leave the
 * problems; the conflicts are read for their wording only when there are any.
 */
function AcceptedWarnings({ effective, projectId, taskId }: { effective: LadoEffective; projectId: string; taskId: string }) {
	const { t } = useTranslation();
	const accepted = effective.setup.accepted;
	const conflicts = useLadoSetupConflicts({ projectId, taskId }, accepted.length > 0);
	if (accepted.length === 0) return null;
	const names = new Map(effective.setup.items.map((item) => [item.itemId, item.name]));
	const worded = new Map((conflicts.data ?? []).filter((conflict) => conflict.acceptanceId).map((conflict) => [conflict.acceptanceId, conflict]));
	return (
		<ul aria-label={t("lado.setup.task.accepted")} className="flex flex-col gap-1">
			{accepted.map((acceptance) => {
				const conflict = worded.get(acceptance.acceptanceId);
				const text = conflict ? ladoSetupConflictText(t, conflict) : acceptance.code;
				const item = names.get(acceptance.itemId) ?? acceptance.itemId;
				return (
					<li key={acceptance.acceptanceId} className="text-caption leading-4 text-muted-foreground" data-testid="lado-setup-accepted">
						{[t("lado.setup.conflicts.accepted"), text, item].join(" · ")}
					</li>
				);
			})}
		</ul>
	);
}

function ComparisonLine({ tone, text }: { tone: "ready" | "attention"; text: string }) {
	return (
		<p className="flex min-w-0 items-baseline gap-2 text-caption leading-4 text-foreground" data-testid="lado-approval-rules" data-tone={tone}>
			<span
				className={cn(
					"size-1.5 shrink-0 translate-y-[-1px] rounded-full",
					tone === "ready" ? "bg-[var(--color-status-ready)]" : "bg-[var(--color-status-needs-you)]",
				)}
				aria-hidden="true"
			/>
			<span className="min-w-0">{text}</span>
		</p>
	);
}

/** The rule text the worker gets, as prose: collapsed until asked for. */
function WorkerText({ prompt }: { prompt: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	return (
		<section className="flex flex-col gap-(--size-settings-section-inner-gap)">
			<button
				type="button"
				aria-expanded={open}
				onClick={() => setOpen((current) => !current)}
				className="flex h-5 w-fit items-center gap-1.5 rounded-sm px-(--settings-row-inset) text-foreground"
			>
				{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
				<span className="text-sm font-medium leading-5">{t("lado.task.rules.workerText")}</span>
				<ChevronDown
					aria-hidden="true"
					className={cn("size-icon-sm text-settings-muted transition-transform duration-fast", open && "rotate-180")}
				/>
			</button>
			{open ? (
				prompt ? (
					<div
						className="whitespace-pre-wrap break-words rounded-md bg-(--color-bg-secondary) px-(--settings-row-inset) py-2.5 text-control leading-5 text-settings-label"
						data-testid="lado-worker-text"
					>
						{prompt}
					</div>
				) : (
					<p className="px-(--settings-row-inset) text-control text-settings-muted">{t("lado.task.rules.noWorkerText")}</p>
				)
			) : null}
		</section>
	);
}
