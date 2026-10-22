// LADO fork: creating and editing a rule (docs/LJY-PRD/HARNESS.md §12). The
// daemon validates the rule; its error codes are shown in the developer's
// language under the field they are about. An edit starts from the version
// the developer opened, so a newer one stops it until they reload.
import { useQueryClient } from "@tanstack/react-query";
import { Check, X } from "lucide-react";
import { useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useRestoreFocusOnClose } from "../../hooks/useRestoreFocusOnClose";
import { fetchLadoRule, LadoApiError, ladoRulesQueryRoot, type LadoRule, type LadoUpdateRuleInput } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { ladoRuleName, useLadoRuleChange, type LadoRuleChangeRequest } from "../../lib/lado-rules";
import { cn } from "../../lib/utils";
import { SETTINGS_MENU_ITEM, SETTINGS_MENU_SURFACE, SettingsMenuTrigger } from "../settings/SettingsMenuTrigger";
import { Button } from "../ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Switch } from "../ui/switch";
import { LadoRuleImpactDialog } from "./LadoRuleImpactDialog";

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

type Field = "name" | "category" | "instruction" | "conflicts";

// The daemon's validation codes by the field they are about
// (backend/internal/service/ladopolicy/service.go).
const fieldOfCode: Partial<Record<string, Field>> = {
	LADO_RULE_NAME_REQUIRED: "name",
	LADO_RULE_NAME_TOO_LONG: "name",
	LADO_RULE_CATEGORY_TOO_LONG: "category",
	LADO_RULE_INSTRUCTION_REQUIRED: "instruction",
	LADO_RULE_INSTRUCTION_TOO_LONG: "instruction",
	LADO_RULE_CONFLICT_UNKNOWN: "conflicts",
	LADO_RULE_CONFLICT_SELF: "conflicts",
	LADO_RULE_CONFLICT_PROTECTED: "conflicts",
	LADO_RULE_CONFLICTS_TOO_MANY: "conflicts",
};

type Draft = { name: string; category: string; instruction: string; conflicts: string[] };

function draftOf(rule: LadoRule | undefined): Draft {
	return {
		name: rule?.name ?? "",
		category: rule?.category ?? "",
		instruction: rule?.instruction ?? "",
		conflicts: [...(rule?.conflictsWith ?? [])].sort(),
	};
}

/** The fields an edit changes; the daemon adds a version only when the content does. */
function changedFields(rule: LadoRule, draft: Draft): Omit<LadoUpdateRuleInput, "baseVersion" | "ceiling" | "dryRun"> {
	const before = draftOf(rule);
	const conflictsChanged =
		draft.conflicts.length !== before.conflicts.length || draft.conflicts.some((id, index) => id !== before.conflicts[index]);
	return {
		...(draft.name !== before.name ? { name: draft.name } : {}),
		...(draft.category !== before.category ? { category: draft.category } : {}),
		...(draft.instruction !== before.instruction ? { instruction: draft.instruction } : {}),
		...(conflictsChanged ? { conflictsWith: draft.conflicts } : {}),
	};
}

/**
 * Creates a rule when rule is undefined, otherwise edits it. rules names the
 * rules a new or edited rule can conflict with; protected rules are always
 * on, so they are not offered. A rule created for one project (projectId) is
 * on there only; the daemon leaves it off everywhere else. Mount it again for
 * each opening.
 */
export function LadoRuleEditorDialog({
	open,
	rule,
	rules,
	projectId,
	onOpenChange,
}: {
	open: boolean;
	rule?: LadoRule;
	rules: readonly LadoRule[];
	projectId?: string;
	onOpenChange: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const change = useLadoRuleChange();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const nameRef = useRef<HTMLInputElement>(null);
	const [base, setBase] = useState(rule);
	const [draft, setDraft] = useState(() => draftOf(rule));
	const [on, setOn] = useState(true);
	const [reloading, setReloading] = useState(false);
	const [reloadError, setReloadError] = useState<unknown>(null);
	const nameId = useId();
	const categoryId = useId();
	const instructionId = useId();
	const conflictsId = useId();
	const onId = useId();

	const busy = change.isPending || reloading;
	const dirty = base ? Object.keys(changedFields(base, draft)).length > 0 : true;
	// A failed apply after the impact dialog's confirm shows in that dialog.
	const error = change.pending ? null : change.error;
	const code = error instanceof LadoApiError ? error.code : undefined;
	const field = code ? fieldOfCode[code] : undefined;
	const stale = code === "LADO_RULE_CHANGED";
	const fieldError = (name: Field) => (error && field === name ? ladoErrorText(t, error) : null);
	const formError = error && !field && !stale ? ladoErrorText(t, error) : null;
	const candidates = rules.filter(
		(candidate) =>
			!candidate.protected && candidate.id !== base?.id && (!candidate.archived || draft.conflicts.includes(candidate.id)),
	);

	const close = () => onOpenChange(false);
	const edit = (next: Partial<Draft>) => {
		setDraft((current) => ({ ...current, ...next }));
		if (change.error) change.reset();
	};

	const submit = (event: FormEvent) => {
		event.preventDefault();
		if (busy || !dirty) return;
		const request: LadoRuleChangeRequest = base
			? { kind: "update", ruleId: base.id, input: { baseVersion: base.version, ...changedFields(base, draft) } }
			: {
					kind: "create",
					input: {
						name: draft.name,
						category: draft.category,
						instruction: draft.instruction,
						conflictsWith: draft.conflicts,
						...(projectId
							? { scope: "project" as const, scopeId: projectId, on: true }
							: { scope: "global" as const, scopeId: "", on }),
					},
				};
		change
			.change(request)
			.then((outcome) => {
				if (outcome.status === "applied") close();
			})
			.catch(() => undefined);
	};

	// A newer version stopped the edit: start again from it.
	const reload = async () => {
		if (!base) return;
		setReloading(true);
		setReloadError(null);
		try {
			const detail = await fetchLadoRule(base.id);
			setBase(detail.rule);
			setDraft(draftOf(detail.rule));
			change.reset();
			void queryClient.invalidateQueries({ queryKey: ladoRulesQueryRoot });
		} catch (failure) {
			setReloadError(failure);
		} finally {
			setReloading(false);
		}
	};

	const describedBy = (id: string, message: string | null) => (message ? `${id}-error` : undefined);

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
				data-testid="lado-rule-editor"
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					nameRef.current?.focus();
				}}
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<DialogClose asChild>
					<button
						type="button"
						disabled={busy}
						className="settings-dialog-close-button settings-close-button"
						aria-label={t("common.close")}
					>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">
						{base
							? t("lado.rules.editor.editTitle")
							: projectId
								? t("lado.rules.project.newRule")
								: t("lado.rules.newRule")}
					</DialogTitle>
				</div>
				<form className="flex min-h-0 flex-1 flex-col" onSubmit={submit}>
					<fieldset disabled={busy} className={cn(settingsDialogBodyClass, "min-w-0 gap-(--size-settings-section-inner-gap) px-5 pt-0 pb-4")}>
						{stale ? (
							<div className="flex items-center justify-between gap-3">
								<p className="text-caption leading-4 text-[var(--color-status-needs-you)]" role="status">
									{ladoErrorText(t, reloadError ?? error)}
								</p>
								<Button
									type="button"
									variant="ghost"
									size="sm"
									className="shrink-0 text-settings-muted hover:text-settings-label"
									onClick={() => void reload()}
								>
									{t("lado.rules.editor.reload")}
								</Button>
							</div>
						) : null}
						<EditorField id={nameId} label={t("lado.rules.editor.name")} error={fieldError("name")}>
							<input
								ref={nameRef}
								id={nameId}
								value={draft.name}
								onChange={(event) => edit({ name: event.target.value })}
								aria-invalid={fieldError("name") ? true : undefined}
								aria-describedby={describedBy(nameId, fieldError("name"))}
								autoComplete="off"
								className="settings-field-control h-(--size-settings-action-height) rounded-md!"
							/>
						</EditorField>
						<EditorField id={categoryId} label={t("lado.rules.editor.category")} error={fieldError("category")}>
							<input
								id={categoryId}
								value={draft.category}
								onChange={(event) => edit({ category: event.target.value })}
								placeholder={t("lado.rules.editor.categoryPlaceholder")}
								aria-invalid={fieldError("category") ? true : undefined}
								aria-describedby={describedBy(categoryId, fieldError("category"))}
								autoComplete="off"
								className="settings-field-control h-(--size-settings-action-height) rounded-md!"
							/>
						</EditorField>
						<EditorField id={instructionId} label={t("lado.rules.editor.instruction")} error={fieldError("instruction")}>
							<textarea
								id={instructionId}
								value={draft.instruction}
								onChange={(event) => edit({ instruction: event.target.value })}
								placeholder={t("lado.rules.editor.instructionPlaceholder")}
								aria-invalid={fieldError("instruction") ? true : undefined}
								aria-describedby={describedBy(instructionId, fieldError("instruction"))}
								className="settings-field-control min-h-(--size-textarea-min) resize-none overflow-y-auto rounded-md! py-2.5"
							/>
						</EditorField>
						<EditorField id={conflictsId} label={t("lado.rules.editor.conflicts")} error={fieldError("conflicts")}>
							<ConflictsMenu
								id={conflictsId}
								value={draft.conflicts}
								candidates={candidates}
								error={fieldError("conflicts")}
								onChange={(conflicts) => edit({ conflicts })}
							/>
						</EditorField>
						{base || projectId ? null : (
							<div className="flex items-center justify-between gap-3">
								<label htmlFor={onId} className="settings-field-label">
									{t("lado.rules.editor.onEverywhere")}
								</label>
								<Switch id={onId} checked={on} onCheckedChange={setOn} />
							</div>
						)}
					</fieldset>
					<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
						{formError ? (
							<p role="alert" className="mr-auto text-caption leading-4 text-error">
								{formError}
							</p>
						) : null}
						<Button type="button" variant="footer" className={compactButtonClass} disabled={busy} onClick={close}>
							{t("confirm.cancel")}
						</Button>
						<Button type="submit" variant="footer-primary" className={compactButtonClass} disabled={busy || !dirty}>
							{change.isPending
								? t("lado.rules.editor.saving")
								: base
									? t("lado.rules.editor.save")
									: t("lado.rules.editor.create")}
						</Button>
					</div>
				</form>
				<LadoRuleImpactDialog
					impact={change.pending?.impact ?? null}
					busy={change.isPending}
					error={change.pending ? change.error : null}
					onConfirm={() => {
						change
							.confirm()
							.then((outcome) => {
								if (outcome.status === "applied") close();
							})
							.catch(() => undefined);
					}}
					onCancel={change.cancel}
				/>
			</DialogContent>
		</Dialog>
	);
}

/** Label above the control, the daemon's message for it right below. */
function EditorField({ id, label, error, children }: { id: string; label: string; error: string | null; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-1.5">
			<label htmlFor={id} className="settings-field-label">
				{label}
			</label>
			{children}
			{error ? (
				<p id={`${id}-error`} role="alert" className="text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</div>
	);
}

/** A checkbox menu of the rules this one cannot be on together with. */
function ConflictsMenu({
	id,
	value,
	candidates,
	error,
	onChange,
}: {
	id: string;
	value: readonly string[];
	candidates: readonly LadoRule[];
	error: string | null;
	onChange: (value: string[]) => void;
}) {
	const { t } = useTranslation();
	const names = new Map(candidates.map((candidate) => [candidate.id, ladoRuleName(t, candidate.id, candidate.name)]));
	const summary = value.length === 0 ? t("lado.rules.none") : value.map((ruleId) => names.get(ruleId) ?? ruleId).join(", ");
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<SettingsMenuTrigger
					id={id}
					aria-label={t("lado.rules.editor.conflicts")}
					aria-invalid={error ? true : undefined}
					aria-describedby={error ? `${id}-error` : undefined}
					className="w-fit max-w-full self-start"
				>
					<span className="min-w-0 truncate">{summary}</span>
				</SettingsMenuTrigger>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className={SETTINGS_MENU_SURFACE}>
				{candidates.length === 0 ? (
					<p className="px-3 py-2 text-xs text-settings-muted">{t("lado.rules.editor.noOtherRules")}</p>
				) : (
					candidates.map((candidate) => {
						const checked = value.includes(candidate.id);
						return (
							<DropdownMenuItem
								key={candidate.id}
								role="menuitemcheckbox"
								aria-checked={checked}
								className={SETTINGS_MENU_ITEM}
								onSelect={(event) => {
									// Several rules can be picked, so the menu stays open.
									event.preventDefault();
									onChange(checked ? value.filter((ruleId) => ruleId !== candidate.id) : [...value, candidate.id].sort());
								}}
							>
								<Check aria-hidden="true" className={cn("size-icon-sm shrink-0", checked ? "opacity-100" : "opacity-0")} />
								<span className="min-w-0 truncate">{names.get(candidate.id)}</span>
							</DropdownMenuItem>
						);
					})
				)}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
