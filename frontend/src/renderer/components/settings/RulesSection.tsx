// LADO fork: Settings → Rules (docs/LJY-PRD/HARNESS.md §12 and §14, PRD §6
// Rules manager). Protected rules are always on; only the permission
// ceiling's mode and the protected paths change, through the desktop app's
// confirmation. Every other rule is turned
// on or off for all LADO projects here, edited, archived or deleted, and a
// change that reaches approved tasks names them before it is made.
import { useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { ChevronDown, Plus } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useDeleteLadoRule, useLadoRules } from "../../hooks/useLado";
import { LadoApiError, ladoRulesQueryRoot, type LadoRule } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { ladoRuleName, useLadoRuleChange, type LadoRuleChangeRequest } from "../../lib/lado-rules";
import { cn } from "../../lib/utils";
import { ConfirmDialog } from "../ConfirmDialog";
import { LadoRuleEditorDialog } from "../lado/LadoRuleEditorDialog";
import { LadoRuleHistoryDialog } from "../lado/LadoRuleHistoryDialog";
import { LadoProtectedPathsDialog } from "../lado/LadoProtectedPathsDialog";
import { LadoRuleImpactDialog } from "../lado/LadoRuleImpactDialog";
import { ladoCategoryKey, ladoCategoryLabel, LadoProtectedRuleRow, LadoRuleRow, type LadoRuleRowActions } from "../lado/LadoRuleRows";
import { Button } from "../ui/button";
import { SettingsSearch } from "./SettingsSearch";
import { SettingsPageTitleContext, SettingsSection } from "./SettingsSection";

type RuleGroup = { key: string; label: string; rules: LadoRule[] };

function matchesSearch(t: TFunction, rule: LadoRule, search: string): boolean {
	if (!search) return true;
	return [ladoRuleName(t, rule.id, rule.name), rule.name, ladoCategoryLabel(t, rule.category), rule.instruction]
		.join("\n")
		.toLocaleLowerCase()
		.includes(search);
}

/** One card per category, in the order the rules were made; rules without one come last. */
function groupByCategory(t: TFunction, rules: readonly LadoRule[]): RuleGroup[] {
	const groups = new Map<string, RuleGroup>();
	for (const rule of rules) {
		const key = ladoCategoryKey(rule.category);
		const group = groups.get(key) ?? { key, label: ladoCategoryLabel(t, rule.category), rules: [] };
		group.rules.push(rule);
		groups.set(key, group);
	}
	return [...groups.values()].sort((a, b) => Number(a.key === "") - Number(b.key === ""));
}

type Opening = { key: number; open: boolean; rule: LadoRule | null };

export function RulesSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const rules = useLadoRules(true);
	const change = useLadoRuleChange();
	const deleteRule = useDeleteLadoRule();
	const [query, setQuery] = useState("");
	const [changing, setChanging] = useState<string | null>(null);
	const [failure, setFailure] = useState<{ ruleId: string; error: unknown } | null>(null);
	// Each opening mounts its dialog again, so it starts from the rule as it is then.
	const [editor, setEditor] = useState<Opening>({ key: 0, open: false, rule: null });
	const [history, setHistory] = useState<Opening>({ key: 0, open: false, rule: null });
	const [deleting, setDeleting] = useState<{ open: boolean; rule: LadoRule | null }>({ open: false, rule: null });
	// The protected paths as they were when the editor opened.
	const [pathsEditor, setPathsEditor] = useState<{ key: number; open: boolean; globs: string[] }>({ key: 0, open: false, globs: [] });
	const [archivedOpen, setArchivedOpen] = useState(false);

	const all = rules.data ?? [];
	const search = query.trim().toLocaleLowerCase();
	const shown = all.filter((rule) => matchesSearch(t, rule, search));
	const protectedRules = shown.filter((rule) => rule.protected);
	const activeRules = shown.filter((rule) => !rule.protected && !rule.archived);
	const archivedRules = shown.filter((rule) => !rule.protected && rule.archived);

	// Every change runs as a dry run first (useLadoRuleChange); one at a time.
	const run = (rule: LadoRule, request: LadoRuleChangeRequest) => {
		if (change.isPending) return;
		setChanging(rule.id);
		setFailure(null);
		change
			.change(request)
			.catch((error: unknown) => {
				setFailure({ ruleId: rule.id, error });
				// A newer version stopped the change: show the latest one.
				if (error instanceof LadoApiError && error.code === "LADO_RULE_CHANGED") {
					void queryClient.invalidateQueries({ queryKey: ladoRulesQueryRoot });
				}
			})
			.finally(() => setChanging(null));
	};

	const actionsFor = (rule: LadoRule): LadoRuleRowActions => ({
		onGlobalChange: (on) =>
			run(rule, { kind: "binding", input: { ruleId: rule.id, scope: "global", scopeId: "", state: on ? "on" : "off" } }),
		onEdit: () => setEditor((current) => ({ key: current.key + 1, open: true, rule })),
		onHistory: () => setHistory((current) => ({ key: current.key + 1, open: true, rule })),
		onArchive: () => run(rule, { kind: "update", ruleId: rule.id, input: { baseVersion: rule.version, archived: true } }),
		onUnarchive: () => run(rule, { kind: "update", ruleId: rule.id, input: { baseVersion: rule.version, archived: false } }),
		onDelete: () => {
			deleteRule.reset();
			setDeleting({ open: true, rule });
		},
	});
	const failureOf = (rule: LadoRule) => (failure?.ruleId === rule.id ? failure.error : undefined);
	const deleteTarget = deleting.rule;
	const deleteCode = deleteRule.error instanceof LadoApiError ? deleteRule.error.code : undefined;

	return (
		<>
			<SettingsSection
				title={t("lado.rules.title")}
				sectionId="rules"
				titleHidden={titleHidden}
				toolbar={
					<SettingsSearch
						aria-label={t("lado.rules.search")}
						placeholder={t("lado.rules.search")}
						value={query}
						onChange={setQuery}
						trailing={
							<Button
								type="button"
								variant="ghost"
								size="sm"
								className="text-settings-muted hover:text-settings-label"
								onClick={() => setEditor((current) => ({ key: current.key + 1, open: true, rule: null }))}
							>
								<Plus aria-hidden="true" />
								{t("lado.rules.newRule")}
							</Button>
						}
					/>
				}
			>
				{rules.isPending ? (
					<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
						{t("common.loading")}
					</p>
				) : rules.isError ? (
					<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
						{ladoErrorText(t, rules.error)}
					</p>
				) : shown.length === 0 ? (
					<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
						{t("lado.rules.noMatches")}
					</p>
				) : (
					// The cards' headings are categories the developer names, so none
					// of them is hidden for repeating the page title.
					<SettingsPageTitleContext.Provider value={null}>
						<div className="flex w-full flex-col gap-(--size-settings-section-gap)">
							{protectedRules.length > 0 ? (
								<SettingsSection title={t("lado.rules.protected")} grouped>
									{protectedRules.map((rule) => (
										<LadoProtectedRuleRow
											key={rule.id}
											rule={rule}
											busy={changing === rule.id}
											error={failureOf(rule)}
											onCeilingChange={(mode) =>
												run(rule, { kind: "ceiling", ruleId: rule.id, baseVersion: rule.version, mode })
											}
											onEditPaths={() =>
												setPathsEditor((current) => ({ key: current.key + 1, open: true, globs: rule.paths?.globs ?? [] }))
											}
											onHistory={actionsFor(rule).onHistory}
										/>
									))}
								</SettingsSection>
							) : null}
							{groupByCategory(t, activeRules).map((group) => (
								<SettingsSection key={group.key} title={group.label} grouped>
									{group.rules.map((rule) => (
										<LadoRuleRow
											key={rule.id}
											rule={rule}
											busy={changing === rule.id}
											error={failureOf(rule)}
											actions={actionsFor(rule)}
										/>
									))}
								</SettingsSection>
							))}
							{archivedRules.length > 0 ? (
								<section className="flex w-full flex-col items-stretch gap-(--size-settings-section-inner-gap)">
									<button
										type="button"
										aria-expanded={archivedOpen}
										onClick={() => setArchivedOpen((open) => !open)}
										className="flex h-5 w-fit items-center gap-1.5 rounded-sm px-(--settings-row-inset) text-foreground"
									>
										<span className="text-sm font-medium leading-5">{t("lado.rules.archived")}</span>
										<span className="text-sm leading-5 tabular-nums text-settings-muted">{archivedRules.length}</span>
										<ChevronDown
											aria-hidden="true"
											className={cn("size-icon-sm text-settings-muted transition-transform duration-fast", archivedOpen && "rotate-180")}
										/>
									</button>
									{archivedOpen ? (
										<div className="settings-grouped-rows flex w-full flex-col">
											{archivedRules.map((rule) => (
												<LadoRuleRow
													key={rule.id}
													rule={rule}
													busy={changing === rule.id}
													error={failureOf(rule)}
													showCategory
													actions={actionsFor(rule)}
												/>
											))}
										</div>
									) : null}
								</section>
							) : null}
						</div>
					</SettingsPageTitleContext.Provider>
				)}
			</SettingsSection>
			<LadoRuleImpactDialog
				impact={change.pending?.impact ?? null}
				busy={change.isPending}
				error={change.pending ? change.error : null}
				onConfirm={() => void change.confirm().catch(() => undefined)}
				onCancel={change.cancel}
			/>
			<LadoRuleEditorDialog
				key={`editor-${editor.key}`}
				open={editor.open}
				rule={editor.rule ?? undefined}
				rules={all}
				onOpenChange={(open) => setEditor((current) => ({ ...current, open }))}
			/>
			<LadoProtectedPathsDialog
				key={`paths-${pathsEditor.key}`}
				open={pathsEditor.open}
				globs={pathsEditor.globs}
				onOpenChange={(open) => setPathsEditor((current) => ({ ...current, open }))}
			/>
			<LadoRuleHistoryDialog
				key={`history-${history.key}`}
				open={history.open}
				rule={history.rule}
				rules={all}
				onOpenChange={(open) => setHistory((current) => ({ ...current, open }))}
			/>
			<ConfirmDialog
				open={deleting.open}
				destructive
				title={t("lado.rules.deleteTitle", { name: deleteTarget ? ladoRuleName(t, deleteTarget.id, deleteTarget.name) : "" })}
				description={t("lado.rules.deleteDescription")}
				confirmLabel={t("lado.rules.delete")}
				busy={deleteRule.isPending}
				// A rule an approval used is kept for good; the error says to archive it.
				confirmDisabled={deleteCode === "LADO_RULE_REFERENCED"}
				error={deleteRule.error ? ladoErrorText(t, deleteRule.error) : null}
				onConfirm={() => {
					if (!deleteTarget) return;
					deleteRule.mutate(deleteTarget.id, { onSuccess: () => setDeleting((current) => ({ ...current, open: false })) });
				}}
				onOpenChange={(open) => {
					if (!open) setDeleting((current) => ({ ...current, open: false }));
				}}
			/>
		</>
	);
}
