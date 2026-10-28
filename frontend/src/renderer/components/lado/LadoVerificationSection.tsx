// LADO fork: what LADO verified on a run's pull request head, in the run's
// inspector under its snapshot (docs/LJY-PRD/TDD.md §8, PRD §6 "Review and
// evidence"): where the integration stands, one row per verification item,
// the check commands with their output, the evidence per acceptance
// criterion, and the developer's next decision on the head: approve its
// integration, which the desktop app confirms, then merge it.
import { InspectorSection } from "@aoagents/product-ui";
import type { TFunction } from "i18next";
import { ChevronDown } from "lucide-react";
import { type ReactNode, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useApproveLadoIntegration,
	useLadoCheckLog,
	useLadoPlan,
	useMergeLadoPR,
	useRepostLadoIntegrationStatus,
	useRunLadoChecks,
} from "../../hooks/useLado";
import type { MessageKey } from "../../i18n/messages";
import type {
	LadoEvidenceCriterion,
	LadoExecution,
	LadoVerification,
	LadoVerifyCommandRun,
	LadoVerifyItem,
	LadoVerifyKind,
	LadoVerifyOutcome,
} from "../../lib/lado";
import { LADO_PROTECTION_ANY_SOURCE, ladoErrorText, ladoProtectionPartText, ladoReasonText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
import { formatDuration } from "../SessionMemoryPanel";
import { Button } from "../ui/button";
import { type LadoRunState, ladoRunStatus, ladoTaskToneDotClass } from "./lado-task-status";

// LADO asks AO's reviewer at most this often per run (domain.LadoReviewRoundCap).
const REVIEW_ROUNDS = 3;
// A one-line list names this many, then how many more.
const LISTED = 3;

// The states of a run with a pull request.
const prRunStates: readonly string[] = ["validating", "integration_pending", "integrating", "completed", "closed_unmerged"];

/**
 * Where the run's pull request stands, as the daemon derives it and sends it
 * with the run (backend/internal/domain/lado_task.go taskState): the panel
 * never works it out again, so it cannot show another state than the task
 * lists do. Null for a run without a PR.
 */
function ladoRunState(execution: LadoExecution): { state: LadoRunState; reason?: string } | null {
	if (!execution.pr || !prRunStates.includes(execution.state)) return null;
	return { state: execution.state as LadoRunState, reason: execution.stateReason || undefined };
}

export function LadoVerificationSection({ execution, projectId }: { execution: LadoExecution; projectId: string }) {
	const { t } = useTranslation();
	const run = ladoRunState(execution);
	const { pr, verification, integration } = execution;
	if (!run || !pr) return null;
	const status = ladoRunStatus(run.state, run.reason, t);
	const open = !pr.merged && !pr.closed;
	const head = verification?.headSha ?? pr.headSha ?? "";
	return (
		<InspectorSection title={t("lado.verify.title")}>
			<div className="flex flex-col gap-2.5" data-testid="lado-verification">
				<div className="flex min-w-0 items-start gap-3">
					{!verification && open && !integration ? (
						<p className="flex min-w-0 flex-1 items-center gap-2 text-sm" role="status">
							<span className={cn("size-1.5 shrink-0 rounded-full", ladoTaskToneDotClass.validating)} aria-hidden="true" />
							<span className="text-muted-foreground">{t("lado.verify.checking")}</span>
						</p>
					) : (
						<p className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-0.5 text-sm" role="status" data-testid="lado-verify-state">
							<span className={cn("size-1.5 shrink-0 rounded-full", ladoTaskToneDotClass[status.tone])} aria-hidden="true" />
							<span className="font-medium text-foreground">{status.label}</span>
							{run.reason ? (
								<>
									{/* Spaces for the text read aloud; the flex gap lays the row out. */}
									{" "}
									<span aria-hidden="true" className="text-passive">·</span>{" "}
									<span className="text-muted-foreground">{ladoReasonText(t, run.reason)}</span>
								</>
							) : null}
						</p>
					)}
					{head ? (
						<span className="shrink-0 pt-0.5 text-xs text-muted-foreground" title={head}>
							{t("lado.verify.head")} <span className="font-mono">{head.slice(0, 7)}</span>
						</span>
					) : null}
				</div>
				{integration?.statusState === "failed" ? (
					<div className="flex min-w-0 flex-col items-start gap-0.5 pl-3.5">
						{integration.statusError ? (
							<p className="w-full truncate text-xs text-muted-foreground" title={integration.statusError}>
								{integration.statusError}
							</p>
						) : null}
						{open ? <LadoRepostStatus projectId={projectId} sessionId={execution.sessionId} /> : null}
					</div>
				) : null}
				{verification ? (
					<ul className="flex flex-col gap-2" data-testid="lado-verify-items">
						{verification.items.map((item) => (
							<VerifyItemRow key={item.kind} item={item} execution={execution} verification={verification} projectId={projectId} canRun={open} />
						))}
					</ul>
				) : null}
				{run.state === "integration_pending" ? (
					<LadoIntegrationAction projectId={projectId} sessionId={execution.sessionId} action="approve" pr={pr} />
				) : run.state === "integrating" && integration ? (
					<LadoIntegrationAction projectId={projectId} sessionId={execution.sessionId} action="merge" pr={pr} headSha={integration.headSha} />
				) : null}
			</div>
		</InspectorSection>
	);
}

const kindKeys: Record<LadoVerifyKind, MessageKey> = {
	required_checks: "lado.verify.item.requiredChecks",
	commands: "lado.verify.item.commands",
	commit_messages: "lado.verify.item.commitMessages",
	paths: "lado.verify.item.paths",
	base_branch: "lado.verify.item.baseBranch",
	protection: "lado.verify.item.protection",
	review: "lado.verify.item.review",
	evidence: "lado.verify.item.evidence",
};

// A pass is ready, a wait is LADO's verification in progress, a failure is
// the worker's to fix (LADO asks it to), and a confirm is the developer's
// call. The dot is not the only sign: each row names its outcome for screen
// readers too (DESIGN.md §6, U5 of #15).
const outcomeDotClass: Record<LadoVerifyOutcome, string> = {
	pass: "bg-[var(--color-status-ready)]",
	wait: "bg-[var(--color-status-validating)]",
	fail: "bg-[var(--color-status-exited)]",
	confirm: "bg-[var(--color-status-needs-you)]",
	info: "bg-[var(--color-status-idle)]",
};

const outcomeKeys: Record<LadoVerifyOutcome, MessageKey> = {
	pass: "lado.verify.outcome.pass",
	wait: "lado.verify.outcome.wait",
	fail: "lado.verify.outcome.fail",
	confirm: "lado.verify.outcome.confirm",
	info: "lado.verify.outcome.info",
};

function VerifyItemRow({
	item,
	execution,
	verification,
	projectId,
	canRun,
}: {
	item: LadoVerifyItem;
	execution: LadoExecution;
	verification: LadoVerification;
	projectId: string;
	canRun: boolean;
}) {
	const { t } = useTranslation();
	// An item that does not apply to this run stays in its place, quieter.
	const quiet = item.code === "not_required";
	const detail = itemDetail(t, item, verification);
	const runs = item.kind === "commands" && !quiet ? (item.details.runs ?? []) : [];
	return (
		<li className="flex min-w-0 flex-col gap-0.5" data-testid="lado-verify-item" data-kind={item.kind} data-outcome={item.outcome}>
			<div className="flex min-w-0 items-center gap-2">
				<span className={cn("size-1.5 shrink-0 rounded-full", outcomeDotClass[item.outcome])} aria-hidden="true" />
				<span className={cn("min-w-0 flex-1 truncate text-sm", quiet ? "text-passive" : "text-foreground")}>
					{t(kindKeys[item.kind])}
					<span className="sr-only">
						{", "}
						{t(outcomeKeys[item.outcome])}
					</span>
				</span>
			</div>
			{detail ? (
				<p className={cn("truncate pl-3.5 text-xs", quiet ? "text-passive" : "text-muted-foreground")} title={detail}>
					{detail}
				</p>
			) : null}
			{item.kind === "protection" && (item.details.missing ?? []).includes(LADO_PROTECTION_ANY_SOURCE) ? (
				<p className="pl-3.5 text-xs text-muted-foreground">{t("lado.protection.anySourceGuide")}</p>
			) : null}
			{runs.length > 0 ? <CommandRuns projectId={projectId} sessionId={execution.sessionId} runs={runs} canRun={canRun} /> : null}
			{item.kind === "evidence" && verification.evidence.length > 0 ? (
				<EvidenceList execution={execution} verification={verification} />
			) : null}
		</li>
	);
}

/** Up to three names, then how many more of the total. */
function nameList(t: TFunction, names: readonly string[], total = names.length): string {
	const shown = names.slice(0, LISTED).join(", ");
	return total > LISTED ? t("lado.verify.more", { names: shown, rest: total - Math.min(names.length, LISTED) }) : shown;
}

const passedCheck = (status: string) => status === "passed" || status === "skipped";
const failedCheck = (status: string) => status === "failed" || status === "cancelled";
const finishedRun = (run: LadoVerifyCommandRun) => run.status === "passed" || run.status === "failed" || run.status === "timed_out" || run.status === "error";

// The worker's reviewer that AO does not know (service/ladoverify/review.go planReviewer).
const unknownReviewer = /^unknown reviewer (.+); AO chose$/;

/**
 * The item's one muted line: what passed, what LADO waits for, what failed
 * or what the developer is asked to confirm, from the item's details. A
 * code this app does not know yet reads as its task reason or as is.
 */
function itemDetail(t: TFunction, item: LadoVerifyItem, verification: LadoVerification): string {
	const { code, details } = item;
	if (code === "not_required") return t("lado.verify.notRequired");
	const fallback = code ? ladoReasonText(t, code) : "";
	switch (item.kind) {
		case "required_checks": {
			if (code === "none_reported") return t("lado.verify.checks.noneReported");
			const checks = details.checks ?? [];
			const required = checks.filter((check) => check.required);
			// A context the ruleset requires that the head has no check for yet waits too.
			const unreported = (details.required ?? []).filter((name) => !checks.some((check) => check.name.toLowerCase() === name.toLowerCase()));
			if (item.outcome === "fail") {
				const failed = required.filter((check) => failedCheck(check.status)).map((check) => check.name);
				return failed.length > 0 ? t("lado.verify.failed", { names: nameList(t, failed) }) : fallback;
			}
			if (item.outcome === "wait") {
				const waiting = [...required.filter((check) => !passedCheck(check.status) && !failedCheck(check.status)).map((check) => check.name), ...unreported];
				return waiting.length > 0 ? t("lado.verify.checks.waiting", { names: nameList(t, waiting) }) : fallback;
			}
			return t("lado.verify.checks.passed", {
				passed: required.filter((check) => passedCheck(check.status)).length,
				count: required.length + unreported.length,
			});
		}
		case "commands": {
			const runs = details.runs ?? [];
			const named = (status: LadoVerifyCommandRun["status"][]) => runs.filter((run) => status.includes(run.status)).map((run) => run.name);
			switch (code) {
				case "none_configured":
					return t("lado.verify.commands.noneConfigured");
				case "no_tests":
					return t("lado.verify.commands.noTests");
				case "commands_running":
					return t("lado.verify.commands.finished", { done: runs.filter(finishedRun).length, count: runs.length });
				case "commands_failed": {
					const failed = named(["failed", "error"]);
					return failed.length > 0 ? t("lado.verify.failed", { names: nameList(t, failed) }) : fallback;
				}
				case "command_timed_out": {
					const late = named(["timed_out"]);
					return late.length > 0 ? t("lado.verify.commands.timedOut", { names: nameList(t, late) }) : fallback;
				}
			}
			if (code) return fallback;
			return t("lado.verify.commands.passed", { passed: named(["passed"]).length, count: runs.length });
		}
		case "commit_messages": {
			const commits = details.commits ?? [];
			const results: string[] = commits.map((commit) => commit.result);
			if (details.prTitle) results.push(details.prTitle.result);
			if (code === "commit_messages_failed") return t("lado.verify.commits.notKorean", { count: results.filter((result) => result === "not_korean").length });
			if (code === "commit_messages_unclear") {
				return t("lado.verify.commits.unclear", { count: results.filter((result) => result === "unclear" || result === "body_unclear").length });
			}
			return code ? fallback : t("lado.verify.commits.passed", { count: commits.length });
		}
		case "paths": {
			if (code === "protected_path") return t("lado.verify.paths.protected", { paths: nameList(t, details.protected ?? []) });
			if (code === "outside_plan") {
				const outside = details.outsidePlan ?? [];
				const count = Math.max(details.outsideCount ?? 0, outside.length);
				return t("lado.verify.paths.outside", { count, paths: nameList(t, outside, count) });
			}
			return code ? fallback : t("lado.verify.paths.passed");
		}
		case "base_branch": {
			const branch = details.branch ?? "";
			switch (code) {
				case "base_moved":
					return t("lado.verify.base.moved", { count: Math.max(1, details.moved?.length ?? 0), branch, remote: details.remote ?? "" });
				case "reflog_off":
					return t("lado.verify.base.reflogOff", { branch });
				case "already_on_base":
					return t("lado.verify.base.alreadyOnBase", { branch });
				case "base_deleted":
					return t("lado.verify.base.deleted", { branch });
				case "no_local_branch":
					return t("lado.verify.base.noLocalBranch", { branch });
			}
			return code ? fallback : t("lado.verify.base.passed", { branch });
		}
		case "protection": {
			switch (code) {
				case "unprotected":
					return t("lado.verify.protection.unprotected", {
						missing: (details.missing ?? []).map((part) => ladoProtectionPartText(t, part)).join(", "),
					});
				case "protection_unknown":
					return details.error ? t("lado.verify.protection.unknownError", { error: details.error }) : t("lado.verify.protection.unknown");
				case "unprotected_gitlab":
					return t("lado.verify.protection.gitlab");
			}
			return code ? fallback : t("lado.verify.protection.passed", { branch: details.branch ?? "" });
		}
		case "review": {
			const parts: string[] = [];
			const rounds = details.rounds ?? 0;
			if (code === "review_pending" || code === "changes_requested") {
				parts.push(fallback);
				if (rounds > 0) parts.push(t("lado.verify.review.round", { round: rounds, max: REVIEW_ROUNDS }));
			} else if (code === "review_inconclusive") parts.push(t("lado.verify.review.inconclusive", { max: REVIEW_ROUNDS }));
			else parts.push(code ? fallback : t("lado.verify.review.passed"));
			const note = details.note ?? "";
			const reviewer = unknownReviewer.exec(note)?.[1];
			if (reviewer) parts.push(t("lado.verify.review.unknownReviewer", { reviewer }));
			else if (note) parts.push(note);
			return parts.filter(Boolean).join(" · ");
		}
		case "evidence": {
			if (!details.fileVersion) return t("lado.verify.evidence.noFile");
			if (details.fileHead && details.fileHead !== verification.headSha) return t("lado.verify.evidence.staleFile");
			const criteria = verification.evidence;
			return t("lado.verify.evidence.linked", { linked: criteria.filter((criterion) => criterion.state === "linked").length, count: criteria.length });
		}
	}
	return fallback;
}

/**
 * Posts the approved head's lado/integration status again, as after its token
 * was fixed: the daemon stops trying after twelve failed posts (S8 of #15).
 */
export function LadoRepostStatus({ projectId, sessionId }: { projectId: string; sessionId: string }) {
	const { t } = useTranslation();
	const repost = useRepostLadoIntegrationStatus(projectId, sessionId);
	return (
		<div className="flex flex-col items-start gap-0.5">
			{/* Pulled left by its padding, so the label lines up with the line above. */}
			<Button
				type="button"
				variant="ghost"
				size="sm"
				className="-ml-2 h-6 px-2 text-muted-foreground hover:text-foreground"
				disabled={repost.isPending}
				onClick={() => repost.mutate()}
				data-testid="lado-repost-status"
			>
				{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
				<span className="text-xs">{repost.isPending ? t("lado.verify.repostingStatus") : t("lado.verify.repostStatus")}</span>
			</Button>
			{repost.error ? (
				<p className="text-caption leading-4 text-error" role="alert">
					{ladoErrorText(t, repost.error)}
				</p>
			) : null}
		</div>
	);
}

/** Runs every check command again on the head; the commands row waits for them. */
function RunAgain({ projectId, sessionId }: { projectId: string; sessionId: string }) {
	const { t } = useTranslation();
	const run = useRunLadoChecks(projectId, sessionId);
	return (
		<div className="flex flex-col items-start gap-0.5">
			{/* Pulled left by its padding, so the label lines up with the commands. */}
			<Button
				type="button"
				variant="ghost"
				size="sm"
				className="-ml-2 h-6 px-2 text-muted-foreground hover:text-foreground"
				disabled={run.isPending}
				onClick={() => run.mutate()}
			>
				{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
				<span className="text-xs">{run.isPending ? t("lado.verify.runningAgain") : t("lado.verify.runAgain")}</span>
			</Button>
			{run.error ? (
				<p className="text-caption leading-4 text-error" role="alert">
					{ladoErrorText(t, run.error)}
				</p>
			) : null}
		</div>
	);
}

const runStatusKeys: Record<NonNullable<LadoVerifyCommandRun["status"]>, MessageKey> = {
	running: "lado.verify.run.running",
	passed: "lado.verify.run.passed",
	failed: "lado.verify.run.failed",
	timed_out: "lado.verify.run.timedOut",
	cancelled: "lado.verify.run.cancelled",
	error: "lado.verify.run.error",
};

function runStatusText(t: TFunction, run: LadoVerifyCommandRun): string {
	if (!run.status) return t("lado.verify.run.notStarted");
	if (run.status === "failed" && run.exitCode !== null) return t("lado.verify.run.exited", { code: run.exitCode });
	return t(runStatusKeys[run.status]);
}

/** The commands that ran on the head, then a quiet way to run them all again while the PR is open. */
function CommandRuns({
	projectId,
	sessionId,
	runs,
	canRun,
}: {
	projectId: string;
	sessionId: string;
	runs: readonly LadoVerifyCommandRun[];
	canRun: boolean;
}) {
	return (
		<div className="flex flex-col gap-1 pt-1 pl-3.5">
			<ul className="flex flex-col gap-1" data-testid="lado-verify-commands">
				{runs.map((run) => (
					<CommandRun key={run.name} sessionId={sessionId} run={run} />
				))}
			</ul>
			{canRun ? <RunAgain projectId={projectId} sessionId={sessionId} /> : null}
		</div>
	);
}

/** One command's state and time, with its output folded until asked for. */
function CommandRun({ sessionId, run }: { sessionId: string; run: LadoVerifyCommandRun }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const log = useLadoCheckLog(sessionId, run.checkRunId, run.status, open);
	return (
		<li className="flex min-w-0 flex-col gap-1" data-testid="lado-verify-command">
			<div className="flex min-w-0 items-center gap-1.5 text-xs">
				<span className="min-w-0 truncate text-foreground">{run.name}</span>
				<span aria-hidden="true" className="text-passive">·</span>
				<span className="shrink-0 text-muted-foreground">{runStatusText(t, run)}</span>
				{run.status && run.status !== "running" && run.durationMs > 0 ? (
					<>
						<span aria-hidden="true" className="text-passive">·</span>
						<span className="shrink-0 tabular-nums text-muted-foreground">{formatDuration(run.durationMs)}</span>
					</>
				) : null}
				{/* Output shows once the command ended: while it runs there is none to read yet. */}
				{run.checkRunId && finishedRun(run) ? (
					<button
						type="button"
						aria-expanded={open}
						aria-label={t("lado.verify.run.outputOf", { name: run.name })}
						onClick={() => setOpen((current) => !current)}
						className="ml-auto flex shrink-0 items-center gap-1 rounded-sm text-muted-foreground outline-none hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
					>
						{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
						<span className="text-xs">{t("lado.verify.run.output")}</span>
						<ChevronDown aria-hidden="true" className={cn("size-icon-2xs transition-transform duration-fast", open && "rotate-180")} />
					</button>
				) : null}
			</div>
			{open ? (
				log.isPending ? (
					<p className="text-xs text-muted-foreground" role="status">{t("common.loading")}</p>
				) : log.isError ? (
					<p className="text-xs text-error" role="alert">{ladoErrorText(t, log.error)}</p>
				) : log.data ? (
					<pre
						className="settings-thin-scrollbar max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md bg-(--color-bg-secondary) px-2.5 py-2 font-mono text-2xs leading-4 text-foreground"
						data-testid="lado-verify-log"
					>
						{log.data}
					</pre>
				) : (
					<p className="text-xs text-muted-foreground">{t("lado.verify.run.noOutput")}</p>
				)
			) : null}
		</li>
	);
}

const evidenceStateKeys: Record<LadoEvidenceCriterion["state"], MessageKey> = {
	linked: "lado.verify.evidence.state.linked",
	claimed: "lado.verify.evidence.state.claimed",
	inferred: "lado.verify.evidence.state.inferred",
	stale: "lado.verify.evidence.state.stale",
};

const refKindKeys: Partial<Record<LadoEvidenceCriterion["refs"][number]["kind"], MessageKey>> = {
	pr_check: "lado.verify.ref.check",
	lado_check_run: "lado.verify.ref.command",
	commit: "lado.verify.ref.commit",
	claim: "lado.verify.ref.claim",
	review_run: "lado.verify.ref.review",
};

/**
 * Each acceptance criterion of the approved plan with what backs it: records
 * LADO matched, the worker's claim alone, nothing (inferred), or an evidence
 * file about another head. Folded until asked for; the criteria's words come
 * from the pinned plan once it opens.
 */
function EvidenceList({ execution, verification }: { execution: LadoExecution; verification: LadoVerification }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const plan = useLadoPlan(open ? execution.plan.revisionId : null);
	const criteria = plan.data?.plan.acceptanceCriteria ?? [];
	// A command's check run reads as the command's name.
	const commandNames = new Map<string, string>();
	for (const item of verification.items) {
		if (item.kind !== "commands") continue;
		for (const run of item.details.runs ?? []) if (run.checkRunId) commandNames.set(run.checkRunId, run.name);
	}
	return (
		<div className="flex flex-col gap-1.5 pt-1 pl-3.5">
			<button
				type="button"
				aria-expanded={open}
				onClick={() => setOpen((current) => !current)}
				className="flex w-fit items-center gap-1 rounded-sm text-muted-foreground outline-none hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
			>
				{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
				<span className="text-xs">{t("lado.verify.evidence.toggle")}</span>
				<ChevronDown aria-hidden="true" className={cn("size-icon-2xs transition-transform duration-fast", open && "rotate-180")} />
			</button>
			{open ? (
				<ol className="flex flex-col gap-1.5" data-testid="lado-verify-evidence">
					{verification.evidence.map((criterion) => (
						<li key={criterion.index} className="flex min-w-0 flex-col gap-0.5 text-xs" data-testid="lado-verify-criterion" data-state={criterion.state}>
							<p className="break-words text-foreground">
								<span className="tabular-nums text-muted-foreground">{criterion.index}.</span>{" "}
								{criteria[criterion.index - 1] ?? t("lado.verify.evidence.criterion", { number: criterion.index })}
							</p>
							<p className="break-words text-muted-foreground">
								{t(evidenceStateKeys[criterion.state])}
								{criterion.refs.map((reference, index) => {
									const key = refKindKeys[reference.kind];
									if (!key) return null;
									return (
										<span key={`${reference.kind}:${reference.key}:${index}`}>
											{" · "}
											{t(key)} <EvidenceRefValue kind={reference.kind} value={refValue(reference, commandNames)} />
										</span>
									);
								})}
							</p>
						</li>
					))}
				</ol>
			) : null}
		</div>
	);
}

function refValue(reference: LadoEvidenceCriterion["refs"][number], commandNames: ReadonlyMap<string, string>): string {
	switch (reference.kind) {
		case "commit":
			return (reference.sha || reference.key).slice(0, 7);
		case "lado_check_run":
			return commandNames.get(reference.key) ?? reference.key;
		default:
			return reference.key;
	}
}

/** Names and SHAs are code-like; a claim is the worker's own words. */
function EvidenceRefValue({ kind, value }: { kind: string; value: string }): ReactNode {
	if (!value) return null;
	return kind === "claim" ? <span className="text-foreground">{value}</span> : <span className="font-mono">{value}</span>;
}

/**
 * The developer's next decision on a run's pull request head: approve its
 * integration, which the desktop app confirms, or merge the approved head
 * through AO's merge route. A cancelled confirmation changes nothing; a
 * refusal shows the daemon's reason under the control.
 */
export function LadoIntegrationAction({
	projectId,
	sessionId,
	action,
	pr,
	headSha,
	variant = "primary",
	className,
}: {
	projectId: string;
	sessionId: string;
	action: "approve" | "merge";
	pr: { number: number; url: string };
	headSha?: string;
	variant?: "primary" | "footer-primary";
	className?: string;
}) {
	const { t } = useTranslation();
	const approve = useApproveLadoIntegration(projectId, sessionId);
	const merge = useMergeLadoPR(projectId, sessionId);
	// A merge that went through leaves the button off until the PR shows it
	// merged and the action leaves (U6 of #15); AO sees the merge on its next
	// read of the PR.
	const merged = action === "merge" && merge.isSuccess;
	const busy = approve.isPending || merge.isPending || merged;
	const error = action === "approve" ? approve.error : merge.error;
	const label =
		action === "approve"
			? approve.isPending
				? t("lado.verify.approving")
				: t("lado.verify.approve")
			: merge.isPending
				? t("lado.verify.merging")
				: merged
					? t("lado.verify.mergeSent")
					: t("lado.verify.merge");
	const footer = variant === "footer-primary";
	return (
		<div className={cn("flex min-w-0 flex-col gap-1", footer ? "items-end" : "items-start")}>
			<Button
				type="button"
				variant={variant}
				size={footer ? "default" : "sm"}
				className={className}
				disabled={busy || (action === "merge" && !headSha)}
				onClick={() => {
					if (action === "approve") approve.mutate();
					else if (headSha) merge.mutate({ number: pr.number, url: pr.url, headSha });
				}}
			>
				{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
				<span className={footer ? "text-sm" : "text-xs font-medium"}>{label}</span>
			</Button>
			{error ? (
				<p className="text-caption leading-4 text-error" role="alert" data-testid="lado-integration-error">
					{ladoErrorText(t, error)}
				</p>
			) : null}
		</div>
	);
}
