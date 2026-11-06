// LADO fork (#22): Project settings → Scripts → Workspace folders. A local
// workspace project copies its folders that are not Git repositories into
// every session workspace; this card edits what is left out of that copy and
// what happens to a symbolic link that points outside the workspace. Both save
// at once to the project's config.workspaceAssets; the patterns are edited in
// a dialog, one per line.
import { useMutation, useQueryClient, type UseMutationResult } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { useRestoreFocusOnClose } from "../hooks/useRestoreFocusOnClose";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { cn } from "../lib/utils";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { SettingsRow } from "./settings/SettingsRow";
import { SettingsSection } from "./settings/SettingsSection";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";

type Project = components["schemas"]["Project"];
type WorkspaceAssetsConfig = components["schemas"]["DomainWorkspaceAssetsConfig"];
type AbsoluteLinks = "skip" | "keep";

/**
 * The patterns the daemon leaves out when a project sets none. A copy for
 * display only: it must match backend/internal/domain/lado_workspace_assets.go
 * DefaultWorkspaceAssetExcludes. The app never saves it, so a project that
 * uses the defaults follows the daemon's list when it changes.
 */
export const DEFAULT_WORKSPACE_ASSET_EXCLUDES: readonly string[] = [
	".venv",
	"venv",
	"node_modules",
	"__pycache__",
	".cache",
	".pytest_cache",
	".mypy_cache",
	".ruff_cache",
	".tox",
	".next",
	".turbo",
	".parcel-cache",
];

/** The most patterns the daemon takes (domain.WorkspaceAssetExcludeMaxItems). */
const MAX_EXCLUDE = 100;

/** How many patterns the row lists before the count speaks for the rest. */
const LISTED = 12;

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

/** What the card saves: exclude null keeps the daemon's defaults (no exclude key). */
export type WorkspaceAssetsChoice = { exclude: string[] | null; absoluteLinks: AbsoluteLinks };

/** Whether the project copies workspace folders into its sessions. */
export function hasWorkspaceAssets(project: Pick<Project, "kind">): boolean {
	return project.kind === "workspace";
}

/** The project's stored choice; a null or missing exclude list means the defaults. */
function storedChoice(project: Project): WorkspaceAssetsChoice {
	const stored = project.config?.workspaceAssets;
	return {
		exclude: Array.isArray(stored?.exclude) ? stored.exclude : null,
		absoluteLinks: stored?.absoluteLinks === "keep" ? "keep" : "skip",
	};
}

/**
 * The config.workspaceAssets to save, or undefined to drop the key when both
 * settings are the defaults. An unedited list sends no exclude key, so the
 * daemon's defaults apply; an empty list is sent as [] and copies everything.
 */
export function workspaceAssetsBody(choice: WorkspaceAssetsChoice): WorkspaceAssetsConfig | undefined {
	if (choice.exclude === null && choice.absoluteLinks === "skip") return undefined;
	// The generated type marks exclude required (the daemon's field has no
	// omitempty so [] round-trips), but the daemon reads a missing key as the defaults.
	return { ...(choice.exclude !== null && { exclude: choice.exclude }), absoluteLinks: choice.absoluteLinks } as WorkspaceAssetsConfig;
}

/** Each non-blank line, trimmed. */
function patternLines(text: string): string[] {
	return text
		.split(/\r?\n/)
		.map((line) => line.trim())
		.filter((line) => line !== "");
}

export function WorkspaceAssetsSettings({ project }: { project: Project }) {
	const { t } = useTranslation();
	const client = useQueryClient();
	// Each opening mounts the dialog again, from the saved patterns.
	const [editing, setEditing] = useState({ key: 0, open: false });
	const update = useMutation({
		mutationFn: async (choice: WorkspaceAssetsChoice) => {
			// Save over the latest settings, so a change made elsewhere since this
			// screen loaded is kept; only workspaceAssets is replaced.
			const current = await apiClient.GET("/api/v1/projects/{id}", { params: { path: { id: project.id } } });
			if (current.error) throw new Error(apiErrorMessage(current.error));
			if (current.data?.status !== "ok" || !current.data.project) throw new Error(t("settings.project.degraded"));
			const latest = current.data.project as Project;
			const rest = { ...latest.config };
			delete rest.workspaceAssets;
			const assets = workspaceAssetsBody(choice);
			const config = assets ? { ...rest, workspaceAssets: assets } : rest;
			const { error } = await apiClient.PUT("/api/v1/projects/{id}", {
				params: { path: { id: project.id } },
				body: { displayName: latest.name, config },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return { ...latest, config };
		},
		onSuccess: (saved) => {
			client.setQueryData(["project", project.id], saved);
			void client.invalidateQueries({ queryKey: ["project", project.id] });
			void client.invalidateQueries({ queryKey: workspaceQueryKey });
		},
	});
	if (!hasWorkspaceAssets(project)) return null;
	const stored = storedChoice(project);
	const patterns = stored.exclude ?? DEFAULT_WORKSPACE_ASSET_EXCLUDES;
	const links = update.isPending && update.variables ? update.variables.absoluteLinks : stored.absoluteLinks;
	const listed = patterns.slice(0, LISTED).join(", ") + (patterns.length > LISTED ? ", …" : "");
	return (
		<SettingsSection title={t("settings.project.workspaceAssets")} grouped testId="workspace-assets-settings">
			<SettingsRow
				label={t("settings.project.workspaceAssetsExclude")}
				description={
					<>
						<span className="block tabular-nums" data-testid="workspace-assets-exclude-count">
							{stored.exclude === null
								? t("settings.project.workspaceAssetsExcludeDefault", { count: patterns.length })
								: patterns.length === 0
									? t("settings.project.workspaceAssetsExcludeNone")
									: t("settings.project.workspaceAssetsExcludeCount", { count: patterns.length })}
						</span>
						{patterns.length > 0 ? (
							<span className="block truncate font-mono" title={patterns.join(", ")} data-testid="workspace-assets-exclude-list">
								{listed}
							</span>
						) : null}
						<span className="block">{t("settings.project.workspaceAssetsExcludeHint")}</span>
						{update.isError && !editing.open ? (
							<span role="alert" className="block text-error">
								{update.error instanceof Error ? update.error.message : t("settings.project.saveFailed")}
							</span>
						) : null}
					</>
				}
			>
				<Button
					type="button"
					variant="ghost"
					size="sm"
					aria-label={t("settings.project.workspaceAssetsExclude")}
					className="text-settings-muted hover:text-settings-label"
					disabled={update.isPending}
					onClick={() => {
						update.reset();
						setEditing((current) => ({ key: current.key + 1, open: true }));
					}}
				>
					<span className="text-sm">{t("settings.project.workspaceAssetsExcludeEdit")}</span>
				</Button>
			</SettingsRow>
			<SettingsRow label={t("settings.project.workspaceAssetsLinks")} description={t("settings.project.workspaceAssetsLinksHint")}>
				<SettingsOptionMenu<AbsoluteLinks>
					aria-label={t("settings.project.workspaceAssetsLinks")}
					disabled={update.isPending}
					value={links}
					options={[
						{ value: "skip", label: t("settings.project.workspaceAssetsLinksSkip") },
						{ value: "keep", label: t("settings.project.workspaceAssetsLinksKeep") },
					]}
					onChange={(absoluteLinks) => {
						if (absoluteLinks !== stored.absoluteLinks) update.mutate({ exclude: stored.exclude, absoluteLinks });
					}}
				/>
			</SettingsRow>
			<ExcludeDialog
				key={`exclude-${editing.key}`}
				open={editing.open}
				stored={stored}
				update={update}
				onOpenChange={(open) => setEditing((current) => ({ ...current, open }))}
			/>
		</SettingsSection>
	);
}

function ExcludeDialog({
	open,
	stored,
	update,
	onOpenChange,
}: {
	open: boolean;
	stored: WorkspaceAssetsChoice;
	update: UseMutationResult<Project, Error, WorkspaceAssetsChoice>;
	onOpenChange: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const fieldId = useId();
	const hintId = useId();
	const fieldRef = useRef<HTMLTextAreaElement>(null);
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const shown = stored.exclude ?? DEFAULT_WORKSPACE_ASSET_EXCLUDES;
	const [text, setText] = useState(shown.join("\n"));
	const lines = patternLines(text);
	const busy = update.isPending;
	const tooMany = lines.length > MAX_EXCLUDE;
	// Saving the defaults unchanged would pin them to this project, so it waits for an edit.
	const dirty = lines.join("\n") !== shown.join("\n");
	const close = () => onOpenChange(false);
	const submit = (event: FormEvent) => {
		event.preventDefault();
		if (busy || tooMany || !dirty) return;
		update.mutate({ exclude: lines, absoluteLinks: stored.absoluteLinks }, { onSuccess: close });
	};
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				if (!next && busy) return;
				onOpenChange(next);
			}}
		>
			<DialogContent
				showCloseButton={false}
				aria-describedby={undefined}
				className={settingsDialogContentClass}
				data-testid="workspace-assets-exclude-dialog"
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					fieldRef.current?.focus();
				}}
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<DialogClose asChild>
					<button type="button" disabled={busy} className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("settings.project.workspaceAssetsExclude")}</DialogTitle>
				</div>
				<form className="flex min-h-0 flex-1 flex-col" onSubmit={submit}>
					{/* The div scrolls; a fieldset as the scroll container spills under the footer. */}
					<div className={cn(settingsDialogBodyClass, "min-w-0 gap-1.5 px-5 pt-0 pb-4")}>
						<div className="flex items-baseline justify-between gap-3">
							<label htmlFor={fieldId} className="settings-field-label">
								{t("settings.project.workspaceAssetsExcludeLabel")}
							</label>
							<span className={cn("text-caption leading-4 tabular-nums", tooMany ? "text-error" : "text-settings-muted")}>
								{t("settings.project.workspaceAssetsExcludeCount", { count: lines.length })}
							</span>
						</div>
						<p id={hintId} className="text-caption leading-4 text-settings-muted">
							{t("settings.project.workspaceAssetsExcludeDialogHint", { max: MAX_EXCLUDE })}
						</p>
						{/* The textarea takes its font from here (the app's form reset). */}
						<div className="font-mono">
							<textarea
								ref={fieldRef}
								id={fieldId}
								value={text}
								rows={12}
								spellCheck={false}
								autoComplete="off"
								disabled={busy}
								aria-describedby={hintId}
								onChange={(event) => {
									setText(event.target.value);
									if (update.isError) update.reset();
								}}
								className="settings-field-control min-h-(--size-textarea-min) resize-none overflow-y-auto rounded-md! py-2.5"
							/>
						</div>
						{update.isError ? (
							<p role="alert" className="text-caption leading-4 text-error">
								{update.error instanceof Error ? update.error.message : t("settings.project.saveFailed")}
							</p>
						) : null}
					</div>
					<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
						{stored.exclude === null ? null : (
							<Button
								type="button"
								variant="footer"
								className={cn(compactButtonClass, "mr-auto")}
								disabled={busy}
								onClick={() => update.mutate({ exclude: null, absoluteLinks: stored.absoluteLinks }, { onSuccess: close })}
							>
								{t("settings.project.workspaceAssetsExcludeReset")}
							</Button>
						)}
						<Button type="button" variant="footer" className={compactButtonClass} disabled={busy} onClick={close}>
							{t("confirm.cancel")}
						</Button>
						<Button type="submit" variant="footer-primary" className={compactButtonClass} disabled={busy || !dirty || tooMany}>
							{t("settings.project.workspaceAssetsExcludeSave")}
						</Button>
					</div>
				</form>
			</DialogContent>
		</Dialog>
	);
}
