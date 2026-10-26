// LADO fork: the managed local setup of one project or one task
// (docs/LJY-PRD/HARNESS.md §9 and §12, plan §15). Each item follows Global
// unless the project or the task forces it on or off, like a rule; turning
// an item that can run commands on goes through the desktop app's
// confirmation. The files a project's repository tracks are listed as they
// are: the tools read them from each task's worktree, so a change to them is
// a task of its own.
import { agentLabel } from "@aoagents/product-ui";
import type { TFunction } from "i18next";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useLadoSetupInventory, useLadoSetupItems } from "../../../hooks/useLadoSetup";
import type { MessageKey } from "../../../i18n/messages";
import { ladoErrorText, ladoSetupKindLabel } from "../../../lib/lado-errors";
import { useLadoSetupChange, type LadoSetupItemState } from "../../../lib/lado-setup";
import { useUiStore } from "../../../stores/ui-store";
import { SettingsOptionMenu } from "../../settings/SettingsOptionMenu";
import { SettingsRow } from "../../settings/SettingsRow";
import { SettingsSection } from "../../settings/SettingsSection";
import { Button } from "../../ui/button";
import { LadoRuleImpactDialog } from "../LadoRuleImpactDialog";
import { compareItems, ladoSetupSize } from "./lado-setup-view";

type Scope = "project" | "task";
type Override = LadoSetupItemState["project"];

const overrides: readonly Override[] = ["inherit", "force_on", "force_off"];

const overrideKeys: Record<Override, MessageKey> = {
	inherit: "lado.rules.override.inherit",
	force_on: "lado.rules.override.on",
	force_off: "lado.rules.override.off",
};

/** What applies and which scope decided it, as a rule says it: "On · from Global", "Off · set here". */
function stateLabel(t: TFunction, item: LadoSetupItemState, scope: Scope): string {
	const state = item.on ? t("lado.rules.state.on") : t("lado.rules.state.off");
	const source =
		item.source.scope === scope
			? t("lado.rules.source.here")
			: item.source.scope === "project"
				? t("lado.rules.source.project")
				: t("lado.rules.source.global");
	return `${state} · ${source}`;
}

/**
 * One card of managed items for a project or a task, each with the select
 * that inherits it or forces it on or off there. showTool names each item's
 * tool, for a project, where both tools' items are listed.
 */
export function LadoSetupScopeCard({
	title,
	scope,
	scopeId,
	items,
	showTool,
	readOnly = false,
}: {
	title: string;
	scope: Scope;
	scopeId: string;
	items: readonly LadoSetupItemState[];
	showTool: boolean;
	readOnly?: boolean;
}) {
	const { t } = useTranslation();
	const change = useLadoSetupChange();
	const [changing, setChanging] = useState<string | null>(null);
	const [failure, setFailure] = useState<{ itemId: string; error: unknown } | null>(null);
	if (items.length === 0) return null;

	// Every change runs as a dry run first (useLadoSetupChange); one at a time.
	const setOverride = (item: LadoSetupItemState, state: Override) => {
		if (change.isPending) return;
		setChanging(item.itemId);
		setFailure(null);
		change
			.change({ op: "binding", input: { itemId: item.itemId, scope, scopeId, state } })
			.catch((error: unknown) => setFailure({ itemId: item.itemId, error }))
			.finally(() => setChanging(null));
	};

	return (
		<>
			<SettingsSection title={title} grouped testId={`lado-setup-${scope}`}>
				{[...items].sort(compareItems).map((item) => {
					const detail = [...(showTool ? [agentLabel(item.tool)] : []), ladoSetupKindLabel(t, item.kind), stateLabel(t, item, scope)].join(" · ");
					const error = failure?.itemId === item.itemId ? failure.error : undefined;
					return (
						<SettingsRow
							key={item.itemId}
							data-testid="lado-setup-scope-item"
							data-item-id={item.itemId}
							label={item.name}
							description={
								<>
									{detail}
									{error ? (
										<span role="alert" className="block text-error">
											{ladoErrorText(t, error)}
										</span>
									) : null}
								</>
							}
						>
							<SettingsOptionMenu<Override>
								aria-label={scope === "project" ? t("lado.rules.project.override", { name: item.name }) : t("lado.rules.task.override", { name: item.name })}
								value={item[scope]}
								options={overrides.map((state) => ({ value: state, label: t(overrideKeys[state]) }))}
								disabled={changing === item.itemId || readOnly}
								triggerClassName="w-fit"
								onChange={(state) => {
									if (state !== item[scope]) setOverride(item, state);
								}}
							/>
						</SettingsRow>
					);
				})}
			</SettingsSection>
			<LadoRuleImpactDialog
				impact={change.pending?.result.impact ?? null}
				busy={change.isPending}
				error={change.pending ? change.error : null}
				title={t("lado.setup.impact.title")}
				onConfirm={() => void change.confirm().catch(() => undefined)}
				onCancel={change.cancel}
			/>
		</>
	);
}

/** Project settings → Rules: the managed items of both tools for this project. Nothing shows before any is managed. */
export function LadoSetupProjectSection({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const items = useLadoSetupItems({ projectId });
	if (items.isError) {
		return (
			<SettingsSection title={t("lado.setup.project.title")}>
				<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
					{ladoErrorText(t, items.error)}
				</p>
			</SettingsSection>
		);
	}
	return <LadoSetupScopeCard title={t("lado.setup.project.title")} scope="project" scopeId={projectId} items={items.data ?? []} showTool />;
}

/**
 * The setup files this project's repository tracks, read only, with their
 * size. The tools read them from each task's worktree, so Edit starts a task
 * that changes one: the project's New Task composer, its first line naming
 * the file.
 */
export function LadoSetupRepositorySection({ projectId }: { projectId: string }) {
	const { t, i18n } = useTranslation();
	const inventory = useLadoSetupInventory();
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const data = inventory.data;
	if (!data) return null;
	const files = data.entries
		.filter((entry) => entry.location === "project" && entry.projectId === projectId && entry.status === "repo_tracked")
		.sort((a, b) => a.name.localeCompare(b.name));
	const scanError = data.projects.find((project) => project.id === projectId)?.error ?? "";
	if (files.length === 0 && !scanError) return null;
	return (
		<SettingsSection
			title={t("lado.setup.project.repoTitle")}
			grouped
			testId="lado-setup-repository"
			toolbar={<p className="px-(--settings-row-inset) text-control leading-4 text-settings-muted">{t("lado.setup.project.repoNote")}</p>}
		>
			{scanError ? (
				<p role="alert" className="settings-content-row text-control leading-4 text-error">
					{t("lado.setup.project.repoError", { error: scanError })}
				</p>
			) : null}
			{files.map((file) => {
				const detail = [
					agentLabel(file.tool),
					...(file.kind ? [ladoSetupKindLabel(t, file.kind)] : []),
					// A file deleted in the main checkout is still tracked, without its content.
					...(file.sha256 ? [ladoSetupSize(file.size, i18n.language)] : []),
				].join(" · ");
				return (
					<SettingsRow
						key={file.path}
						data-testid="lado-setup-repo-file"
						data-path={file.path}
						label={<span className="break-all font-mono text-control">{file.name}</span>}
						description={detail}
					>
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="text-settings-muted hover:text-settings-label"
							aria-label={t("lado.setup.project.editLabel", { path: file.name })}
							onClick={() => requestNewTask(projectId, undefined, t("lado.setup.project.taskTitle", { path: file.name }))}
						>
							<span className="text-control">{t("lado.rules.edit")}</span>
						</Button>
					</SettingsRow>
				);
			})}
		</SettingsSection>
	);
}
