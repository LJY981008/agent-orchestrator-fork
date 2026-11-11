// LADO fork: a run's production database changes in its verification
// section (issue #97, docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md).
// For each pull request: its SQL files with their phase (before the merge or
// after the deploy) and whether LADO applied them, each file's approved
// content on request, the applied files the head changes (LADO never runs
// them again), whether the plan names the change, and where LADO's run of the
// SQL stands with why it stopped: a preflight that found the pull request
// could not merge, a failed file with its one-line message (without secrets),
// or a merge that failed after the SQL. The buttons apply a phase's SQL by
// hand: again after a failure or a held preflight, the pre-merge SQL of a
// pull request merged outside LADO, or the after-deploy SQL once the code is
// deployed. The desktop app confirms each; a cancelled confirmation changes
// nothing. Shown only while a record of the run names SQL files.
import type { TFunction } from "i18next";
import { ChevronDown } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useApplyLadoDBChanges, useLadoDBChangeFile, useLadoExecutionDBChanges } from "../../hooks/useLado";
import type { MessageKey } from "../../i18n/messages";
import type { LadoExecution, LadoVerification } from "../../lib/lado";
import {
	ladoDBChangeAction,
	ladoDBChangeLatestRun,
	ladoExecutionHasDBChanges,
	type LadoDBChangeAction,
	type LadoDBChangeFile,
	type LadoDBChangeFileStatus,
	type LadoDBChangePhase,
	type LadoDBChangePR,
	type LadoDBChangeRun,
} from "../../lib/lado-db-changes";
import { ladoErrorText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";

const phaseKeys: Record<LadoDBChangePhase, MessageKey> = {
	pre_merge: "lado.dbChange.phase.pre_merge",
	after_deploy: "lado.dbChange.phase.after_deploy",
};

const fileStatusKeys: Record<LadoDBChangeFileStatus, MessageKey> = {
	pending: "lado.dbChange.file.pending",
	applying: "lado.dbChange.file.applying",
	applied: "lado.dbChange.file.applied",
	failed: "lado.dbChange.file.failed",
	interrupted: "lado.dbChange.file.interrupted",
};

const fileStatusDotClass: Record<LadoDBChangeFileStatus, string> = {
	pending: "bg-[var(--color-status-idle)]",
	applying: "bg-[var(--color-status-working)]",
	applied: "bg-[var(--color-status-ready)]",
	failed: "bg-[var(--color-status-exited)]",
	interrupted: "bg-[var(--color-status-needs-you)]",
};

const actionKeys: Record<LadoDBChangeAction, MessageKey> = {
	retry: "lado.dbChange.action.retry",
	after_deploy: "lado.dbChange.action.afterDeploy",
	merged_unapplied: "lado.dbChange.action.mergedUnapplied",
};

/** Where a run of the SQL stands, in the developer's words; awaiting_human says what it waits for. */
export function ladoDBChangeRunText(t: TFunction, run: LadoDBChangeRun): string {
	switch (run.state) {
		case "awaiting_human":
			if (run.trigger === "merged_unapplied") return t("lado.dbChange.run.mergedUnapplied");
			if (run.trigger === "after_merge") return t("lado.dbChange.run.afterDeploy");
			return t("lado.dbChange.run.preflightBlocked");
		case "queued":
			return t("lado.dbChange.run.queued");
		case "applying":
			return t("lado.dbChange.run.applying");
		case "failed":
			return t("lado.dbChange.run.failed");
		case "applied":
			return t("lado.dbChange.run.applied");
		case "merging":
			return t("lado.dbChange.run.merging");
		case "merge_failed":
			return t("lado.dbChange.run.mergeFailed");
		case "done":
			return t("lado.dbChange.run.done");
	}
	return run.state;
}

/** The record of the pull request's head, to read whether the plan names the change. */
function recordOf(execution: LadoExecution, pr: LadoDBChangePR): LadoVerification | undefined {
	const tracked = (execution.trackedPrs ?? []).find((entry) => entry.url === pr.prUrl)?.record;
	if (tracked) return tracked;
	return execution.verification?.headSha === pr.headSha ? execution.verification : undefined;
}

export function LadoDBChangePanel({ execution, projectId }: { execution: LadoExecution; projectId: string }) {
	const { t } = useTranslation();
	const shown = ladoExecutionHasDBChanges(execution);
	const changes = useLadoExecutionDBChanges(execution.sessionId, shown);
	if (!shown) return null;
	const prs = changes.data?.prs ?? [];
	return (
		<div className="flex min-w-0 flex-col gap-2 pl-3.5" data-testid="lado-db-change-panel">
			<p className="text-xs font-medium text-foreground">{t("lado.dbChange.title")}</p>
			{changes.isPending ? (
				<p className="text-xs text-muted-foreground" role="status">
					{t("common.loading")}
				</p>
			) : changes.isError ? (
				<p className="text-xs text-error" role="alert">
					{ladoErrorText(t, changes.error)}
				</p>
			) : prs.length === 0 ? (
				<p className="text-xs text-muted-foreground">{t("lado.dbChange.none")}</p>
			) : (
				<ul className="flex flex-col gap-3">
					{prs.map((pr) => (
						<PRChanges
							key={`${pr.prUrl}@${pr.headSha}`}
							projectId={projectId}
							sessionId={execution.sessionId}
							pr={pr}
							named={prs.length > 1 || Boolean(pr.repo)}
							planned={recordOf(execution, pr)?.items.find((item) => item.kind === "db_changes")?.details.planned}
						/>
					))}
				</ul>
			)}
		</div>
	);
}

/** One pull request's SQL: its runs, its files, the applied files it changes, and its buttons. */
function PRChanges({
	projectId,
	sessionId,
	pr,
	named,
	planned,
}: {
	projectId: string;
	sessionId: string;
	pr: LadoDBChangePR;
	named: boolean;
	planned: boolean | undefined;
}) {
	const { t } = useTranslation();
	const apply = useApplyLadoDBChanges(projectId, sessionId);
	const runs = (["pre_merge", "after_deploy"] as const).flatMap((kind) => {
		const run = ladoDBChangeLatestRun(pr, kind);
		return run ? [run] : [];
	});
	const actions = runs.flatMap((run) => {
		const action = ladoDBChangeAction(run);
		return action ? [{ action, phase: run.kind }] : [];
	});
	return (
		<li className="flex min-w-0 flex-col gap-1.5" data-testid="lado-db-change-pr" data-pr={pr.prUrl}>
			{named ? (
				<p className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
					{pr.repo ? <span className="truncate font-mono text-foreground">{pr.repo}</span> : null}
					<span className="truncate" title={pr.prUrl}>
						{pr.prUrl}
					</span>
				</p>
			) : null}
			{runs.map((run) => (
				<div key={run.id} className="flex min-w-0 flex-col gap-0.5" data-testid="lado-db-change-run" data-state={run.state} data-kind={run.kind}>
					<p className={cn("text-xs", run.state === "failed" || run.state === "merge_failed" ? "text-error" : "text-foreground")}>
						{ladoDBChangeRunText(t, run)}
					</p>
					{run.error && run.state !== "done" ? (
						<p className="break-words text-xs text-muted-foreground" data-testid="lado-db-change-run-error">
							{run.error}
						</p>
					) : null}
				</div>
			))}
			{planned === false ? (
				<p className="text-xs text-[var(--color-status-needs-you)]" data-testid="lado-db-change-unplanned">
					{t("lado.dbChange.unplanned")}
				</p>
			) : null}
			{pr.files.length > 0 ? (
				<ul className="flex flex-col gap-1" data-testid="lado-db-change-files">
					{pr.files.map((file) => (
						<FileRow key={`${file.path}@${file.blobSha}`} sessionId={sessionId} prUrl={pr.prUrl} file={file} />
					))}
				</ul>
			) : null}
			{pr.modified.length > 0 ? (
				<div className="flex min-w-0 flex-col gap-0.5 text-xs" data-testid="lado-db-change-modified">
					<p className="text-[var(--color-status-needs-you)]">{t("lado.dbChange.modified", { count: pr.modified.length })}</p>
					<ul className="flex flex-col">
						{pr.modified.map((entry) => (
							<li key={entry.path} className="truncate text-muted-foreground" title={entry.path}>
								<span className="font-mono">{entry.path}</span>
								{entry.deleted ? ` · ${t("lado.dbChange.deleted")}` : null}
							</li>
						))}
					</ul>
				</div>
			) : null}
			{actions.length > 0 ? (
				<div className="flex flex-wrap items-center gap-2 pt-0.5">
					{actions.map(({ action, phase }) => (
						<Button
							key={action}
							type="button"
							variant="outline"
							size="sm"
							disabled={apply.isPending}
							data-testid={`lado-db-change-${action}`}
							onClick={() => apply.mutate({ pr, phase })}
						>
							{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
							<span className="text-xs">{apply.isPending && apply.variables?.phase === phase ? t("lado.dbChange.applying") : t(actionKeys[action])}</span>
						</Button>
					))}
				</div>
			) : null}
			{apply.error ? (
				<p className="text-caption leading-4 text-error" role="alert">
					{ladoErrorText(t, apply.error)}
				</p>
			) : null}
		</li>
	);
}

/** One SQL file: its path, phase and state, its last message, and its content folded until asked for. */
function FileRow({ sessionId, prUrl, file }: { sessionId: string; prUrl: string; file: LadoDBChangeFile }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const content = useLadoDBChangeFile(sessionId, prUrl, file.blobSha, open);
	return (
		<li className="flex min-w-0 flex-col gap-0.5" data-testid="lado-db-change-file" data-status={file.status} data-phase={file.phase}>
			<div className="flex min-w-0 items-center gap-1.5 text-xs">
				<span className={cn("size-1.5 shrink-0 rounded-full", fileStatusDotClass[file.status])} aria-hidden="true" />
				<span className="min-w-0 truncate font-mono text-foreground" title={file.path}>
					{file.path}
				</span>
				{/* Spaces for the text read aloud; the flex gap lays the row out. */}
				{" "}
				<span aria-hidden="true" className="text-passive">·</span>{" "}
				<span className="shrink-0 text-muted-foreground">{t(phaseKeys[file.phase])}</span>{" "}
				<span aria-hidden="true" className="text-passive">·</span>{" "}
				<span className="shrink-0 text-muted-foreground">{t(fileStatusKeys[file.status])}</span>
				<button
					type="button"
					aria-expanded={open}
					aria-label={t("lado.dbChange.viewFile", { path: file.path })}
					onClick={() => setOpen((current) => !current)}
					className="ml-auto flex shrink-0 items-center gap-1 rounded-sm text-muted-foreground outline-none hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
				>
					{/* The app styles buttons outside Tailwind's layers, so the type sits on the span. */}
					<span className="text-xs">{t("lado.dbChange.view")}</span>
					<ChevronDown aria-hidden="true" className={cn("size-icon-2xs transition-transform duration-fast", open && "rotate-180")} />
				</button>
			</div>
			{file.lastMessage && file.status !== "applied" ? (
				<p className="break-words pl-3 text-xs text-muted-foreground" data-testid="lado-db-change-message">
					{file.lastMessage}
				</p>
			) : null}
			{file.status === "interrupted" ? (
				<p className="pl-3 text-xs text-[var(--color-status-needs-you)]">{t("lado.dbChange.interrupted")}</p>
			) : null}
			{open ? (
				content.isPending ? (
					<p className="text-xs text-muted-foreground" role="status">
						{t("common.loading")}
					</p>
				) : content.isError ? (
					<p className="text-xs text-error" role="alert">
						{ladoErrorText(t, content.error)}
					</p>
				) : (
					<>
						<pre
							className="settings-thin-scrollbar max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md bg-(--color-bg-secondary) px-2.5 py-2 font-mono text-2xs leading-4 text-foreground"
							data-testid="lado-db-change-content"
						>
							{content.data.content}
						</pre>
						{content.data.truncated ? <p className="text-xs text-muted-foreground">{t("lado.dbChange.truncated")}</p> : null}
					</>
				)
			) : null}
		</li>
	);
}
