// LADO fork: Project settings → Orchestrator memo (issue #50). The handoff note
// the orchestrator writes before /clear and every new orchestrator
// conversation receives. A person reads it, edits it, and restores an earlier
// revision here. Shown for every local project, LADO mode or not.
import { useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	invalidateProjectMemo,
	projectMemoChars,
	projectMemoQueryKey,
	useProjectMemo,
	useProjectMemoRevision,
	useRestoreProjectMemo,
	useSaveProjectMemo,
	type ProjectMemo,
	type ProjectMemoRevisionSummary,
	type ProjectMemoView,
} from "../../hooks/useProjectMemo";
import { apiErrorCode, apiErrorMessage } from "../../lib/api-client";
import { cn } from "../../lib/utils";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

/** Why a memo request failed, in the person's words. */
export function projectMemoErrorText(t: TFunction, error: unknown, maxChars: string): string {
	switch (apiErrorCode(error)) {
		case "PROJECT_MEMO_TOO_LONG":
			return t("projectMemo.error.tooLong", { max: maxChars });
		case "PROJECT_MEMO_CONFLICT":
			return t("projectMemo.error.conflict");
		case "PROJECT_MEMO_INVALID":
			return t("projectMemo.error.invalid", { reason: apiErrorMessage(error, "") });
		case "PROJECT_NOT_FOUND":
			return t("projectMemo.error.projectNotFound");
		case "PROJECT_MEMO_NOT_FOUND":
			return t("projectMemo.error.revisionNotFound");
		default:
			return apiErrorMessage(error, t("projectMemo.error.generic"));
	}
}

function sourceLabel(t: TFunction, source: ProjectMemo["source"]): string {
	return source === "clear" ? t("projectMemo.source.clear") : t("projectMemo.source.user");
}

/** The person's unsaved text and the revision it was started from ("" when there was no memo). */
type Draft = { text: string; base: string };

export function ProjectMemoSection({ projectId }: { projectId: string }) {
	const { t, i18n } = useTranslation();
	const queryClient = useQueryClient();
	const view = useProjectMemo(projectId);
	const save = useSaveProjectMemo(projectId);
	const restore = useRestoreProjectMemo(projectId);
	const [draft, setDraft] = useState<Draft | null>(null);
	const [problem, setProblem] = useState<string | null>(null);
	const [saved, setSaved] = useState(false);
	const fieldId = useId();
	const hintId = useId();
	const problemId = useId();

	const time = (iso: string) => new Date(iso).toLocaleString(i18n.language, { dateStyle: "medium", timeStyle: "short" });
	const number = (value: number) => new Intl.NumberFormat(i18n.language).format(value);

	if (view.isPending) {
		return (
			<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
				{t("common.loading")}
			</p>
		);
	}
	if (!view.data) {
		return (
			<div className="space-y-2 px-(--settings-row-inset) text-sm" role="alert">
				<p className="text-error">{t("projectMemo.error.load", { reason: apiErrorMessage(view.error, "") })}</p>
				<button className="text-settings-label underline underline-offset-2" onClick={() => void view.refetch()} type="button">
					{t("settings.project.retry")}
				</button>
			</div>
		);
	}

	const data: ProjectMemoView = view.data;
	const memo = data.memo;
	const currentRevision = memo?.revision ?? "";
	const text = draft?.text ?? memo?.body ?? "";
	const dirty = draft !== null && draft.text !== (memo?.body ?? "");
	const chars = projectMemoChars(text);
	const tooLong = chars > data.maxChars;
	const busy = save.isPending || restore.isPending;

	// A conflict means a /clear wrote a new memo while the person edited. Read
	// it, and move the draft onto it: the text stays, and saving again replaces
	// the new memo on purpose.
	const failed = async (error: unknown) => {
		setProblem(projectMemoErrorText(t, error, number(data.maxChars)));
		if (apiErrorCode(error) !== "PROJECT_MEMO_CONFLICT") return;
		await invalidateProjectMemo(queryClient, projectId);
		const latest = queryClient.getQueryData<ProjectMemoView>(projectMemoQueryKey(projectId));
		setDraft((current) => (current ? { ...current, base: latest?.memo?.revision ?? "" } : current));
	};

	const saveDraft = () => {
		if (!draft || busy || tooLong) return;
		setProblem(null);
		setSaved(false);
		save.mutate(
			{ body: draft.text, baseRevision: draft.base },
			{
				onSuccess: () => {
					setDraft(null);
					setSaved(true);
				},
				onError: (error) => void failed(error),
			},
		);
	};

	const restoreRevision = (revision: string) => {
		if (busy || dirty) return;
		setProblem(null);
		setSaved(false);
		restore.mutate(
			{ revision, baseRevision: currentRevision },
			{
				onSuccess: () => {
					setDraft(null);
					setSaved(true);
				},
				onError: (error) => void failed(error),
			},
		);
	};

	return (
		<div className="flex w-full flex-col gap-(--size-settings-section-gap)" data-testid="project-memo-section">
			<SettingsSection
				title={t("projectMemo.current")}
				grouped
				testId="project-memo-current"
				toolbar={
					<p className="px-(--settings-row-inset) pb-(--size-settings-section-inner-gap) text-control leading-4 text-settings-muted">
						{t("projectMemo.description")}
					</p>
				}
			>
				<div className="settings-content-row flex flex-col gap-2">
					<div className="flex min-h-5 flex-wrap items-center gap-2 text-control leading-4 text-settings-muted" data-testid="project-memo-meta">
						{memo ? (
							<>
								<span>{t("projectMemo.updated", { time: time(memo.updatedAt), source: sourceLabel(t, memo.source) })}</span>
								{memo.stale ? (
									<Badge variant="warning" data-testid="project-memo-stale">
										{t("projectMemo.stale", { count: data.staleAfterDays })}
									</Badge>
								) : null}
							</>
						) : (
							<span>{t("projectMemo.empty")}</span>
						)}
					</div>
					<label htmlFor={fieldId} className="sr-only">
						{t("projectMemo.label")}
					</label>
					<textarea
						id={fieldId}
						value={text}
						rows={14}
						spellCheck={false}
						autoComplete="off"
						disabled={busy}
						aria-invalid={tooLong || problem ? true : undefined}
						aria-describedby={problem ? `${hintId} ${problemId}` : hintId}
						onChange={(event) => {
							const next = event.target.value;
							setSaved(false);
							setDraft((current) => ({ text: next, base: current?.base ?? currentRevision }));
						}}
						className="settings-field-control min-h-(--size-textarea-min) resize-y overflow-y-auto rounded-md! py-2.5"
					/>
					<div className="flex flex-wrap items-center justify-between gap-2">
						<span
							id={hintId}
							data-testid="project-memo-count"
							className={cn("text-caption leading-4 tabular-nums", tooLong ? "text-error" : "text-settings-muted")}
						>
							{t("projectMemo.count", { chars: number(chars), max: number(data.maxChars) })}
						</span>
						<div className="flex items-center gap-2">
							{saved && !dirty ? (
								<span role="status" className="text-caption leading-4 text-settings-muted">
									{t("projectMemo.saved")}
								</span>
							) : null}
							<Button
								type="button"
								variant="ghost"
								size="sm"
								className="text-settings-muted hover:text-settings-label"
								disabled={busy || draft === null}
								onClick={() => {
									setDraft(null);
									setProblem(null);
								}}
							>
								<span className="text-control">{t("projectMemo.discard")}</span>
							</Button>
							<Button type="button" size="sm" disabled={busy || !dirty || tooLong} onClick={saveDraft}>
								<span className="text-control">{save.isPending ? t("projectMemo.saving") : t("projectMemo.save")}</span>
							</Button>
						</div>
					</div>
					{tooLong ? (
						<p className="text-caption leading-4 text-error">{t("projectMemo.error.tooLong", { max: number(data.maxChars) })}</p>
					) : null}
					{problem ? (
						<p id={problemId} role="alert" className="text-caption leading-4 text-error">
							{problem}
						</p>
					) : null}
				</div>
			</SettingsSection>
			<SettingsSection
				title={t("projectMemo.revisions.title")}
				grouped={data.revisions.length > 0}
				testId="project-memo-revisions"
				toolbar={
					<p className="px-(--settings-row-inset) pb-(--size-settings-section-inner-gap) text-control leading-4 text-settings-muted">
						{dirty ? t("projectMemo.revisions.dirty") : t("projectMemo.revisions.description")}
					</p>
				}
			>
				{data.revisions.length === 0 ? (
					<p className="px-(--settings-row-inset) text-sm text-settings-muted">{t("projectMemo.revisions.empty")}</p>
				) : (
					data.revisions.map((revision) => (
						<ProjectMemoRevisionRow
							key={revision.revision}
							projectId={projectId}
							revision={revision}
							time={time(revision.updatedAt)}
							chars={t("projectMemo.chars", { count: revision.chars, chars: number(revision.chars) })}
							restoring={restore.isPending && restore.variables?.revision === revision.revision}
							disabled={busy || dirty}
							onRestore={() => restoreRevision(revision.revision)}
						/>
					))
				)}
			</SettingsSection>
		</div>
	);
}

function ProjectMemoRevisionRow({
	projectId,
	revision,
	time,
	chars,
	restoring,
	disabled,
	onRestore,
}: {
	projectId: string;
	revision: ProjectMemoRevisionSummary;
	time: string;
	chars: string;
	restoring: boolean;
	disabled: boolean;
	onRestore: () => void;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const body = useProjectMemoRevision(projectId, open ? revision.revision : null);
	const bodyId = useId();
	return (
		<>
			<SettingsRow
				data-testid="project-memo-revision"
				data-revision={revision.revision}
				label={time}
				description={`${sourceLabel(t, revision.source)} · ${chars}`}
			>
				<div className="flex items-center gap-1">
					<Button
						type="button"
						variant="ghost"
						size="sm"
						className="text-settings-muted hover:text-settings-label"
						aria-expanded={open}
						aria-controls={open ? bodyId : undefined}
						aria-label={open ? t("projectMemo.revisions.hideLabel", { time }) : t("projectMemo.revisions.showLabel", { time })}
						onClick={() => setOpen((current) => !current)}
					>
						<span className="text-control">{open ? t("projectMemo.revisions.hide") : t("projectMemo.revisions.show")}</span>
					</Button>
					<Button
						type="button"
						variant="outline"
						size="sm"
						disabled={disabled}
						aria-label={t("projectMemo.revisions.restoreLabel", { time })}
						onClick={onRestore}
					>
						<span className="text-control">{restoring ? t("projectMemo.revisions.restoring") : t("projectMemo.revisions.restore")}</span>
					</Button>
				</div>
			</SettingsRow>
			{open ? (
				<div id={bodyId} className="settings-content-row" data-testid="project-memo-revision-body">
					{body.data ? (
						<pre className="max-h-64 overflow-y-auto whitespace-pre-wrap break-words font-sans text-control leading-5 text-settings-label">
							{body.data.body}
						</pre>
					) : body.isError ? (
						<p role="alert" className="text-caption leading-4 text-error">
							{projectMemoErrorText(t, body.error, "")}
						</p>
					) : (
						<p role="status" className="text-caption leading-4 text-settings-muted">
							{t("common.loading")}
						</p>
					)}
				</div>
			) : null}
		</>
	);
}
