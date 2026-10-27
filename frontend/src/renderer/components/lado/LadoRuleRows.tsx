// LADO fork: the rows of Settings → Rules (docs/LJY-PRD/HARNESS.md §12). A
// protected rule is always on and only the permission ceiling's mode changes;
// its menu only opens the history. Any other rule has its Global switch and a
// menu for the rest, where turning it off, archiving it and deleting it stay
// three separate actions.
import type { TFunction } from "i18next";
import { Archive, ArchiveRestore, History, MoreHorizontal, Pencil, Trash2 } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { LADO_CEILING_MODES, type LadoCeilingMode } from "../../../shared/lado-presence";
import type { MessageKey } from "../../i18n/messages";
import type { LadoRule } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import {
	LADO_RULE_PERMISSION_CEILING,
	LADO_RULE_PROTECTED_PATHS,
	ladoRuleDescription,
	ladoRuleName,
	ladoStrengthLabel,
} from "../../lib/lado-rules";
import { SettingsOptionMenu } from "../settings/SettingsOptionMenu";
import { SettingsRow } from "../settings/SettingsRow";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Switch } from "../ui/switch";

// The categories of LADO's built-in rules (backend/internal/service/ladopolicy/builtins.go).
const categoryKeys: Partial<Record<string, MessageKey>> = {
	workflow: "lado.rules.category.workflow",
	tools: "lado.rules.category.tools",
	git: "lado.rules.category.git",
};

// The permission modes as AO's project settings name them.
const ceilingModeKeys: Record<LadoCeilingMode, MessageKey> = {
	"accept-edits": "settings.project.permissionAcceptEdits",
	auto: "settings.project.permissionAuto",
	"bypass-permissions": "settings.project.permissionBypass",
};

/** A category as the rules list groups it: case and surrounding space do not matter. */
export function ladoCategoryKey(category: string): string {
	return category.trim().toLocaleLowerCase();
}

/** A category's name: LADO's own are translated, the developer's show as typed. */
export function ladoCategoryLabel(t: TFunction, category: string): string {
	const trimmed = category.trim();
	if (!trimmed) return t("lado.rules.category.none");
	const key = categoryKeys[ladoCategoryKey(trimmed)];
	return key ? t(key) : trimmed;
}

export function ladoCeilingModeLabel(t: TFunction, mode: LadoCeilingMode): string {
	return t(ceilingModeKeys[mode]);
}

/** Where a rule is forced on or off, counted: "On in 1 project", "Off in 2 tasks". */
export function ladoOverrideSummary(t: TFunction, rule: LadoRule): string[] {
	const count = (state: "force_on" | "force_off", scope: "project" | "task") =>
		rule.overrides.filter((override) => override.state === state && override.scope === scope).length;
	const parts: [number, (count: number) => string][] = [
		[count("force_on", "project"), (n) => t("lado.rules.overrides.onProjects", { count: n })],
		[count("force_on", "task"), (n) => t("lado.rules.overrides.onTasks", { count: n })],
		[count("force_off", "project"), (n) => t("lado.rules.overrides.offProjects", { count: n })],
		[count("force_off", "task"), (n) => t("lado.rules.overrides.offTasks", { count: n })],
	];
	return parts.filter(([n]) => n > 0).map(([n, label]) => label(n));
}

/** What a failed change of this row says, right under the row's own text. */
function withFailure(t: TFunction, detail: ReactNode, error: unknown): ReactNode {
	if (!error) return detail;
	return (
		<>
			{detail}
			<span role="alert" className="block text-error">
				{ladoErrorText(t, error)}
			</span>
		</>
	);
}

/**
 * A protected rule: always on, so it has no switch, and its menu only opens
 * the history, where the permission ceiling's and the protected paths'
 * changes show. The ceiling keeps one control, its mode, and the protected
 * paths theirs, an editor of the paths; the desktop app confirms either
 * change before it is made.
 */
export function LadoProtectedRuleRow({
	rule,
	busy,
	error,
	onCeilingChange,
	onEditPaths,
	onHistory,
}: {
	rule: LadoRule;
	busy: boolean;
	error?: unknown;
	onCeilingChange: (mode: LadoCeilingMode) => void;
	onEditPaths: () => void;
	onHistory: () => void;
}) {
	const { t } = useTranslation();
	const name = ladoRuleName(t, rule.id, rule.name);
	return (
		<SettingsRow
			data-testid="lado-rule-row"
			data-rule-id={rule.id}
			label={name}
			description={withFailure(t, ladoRuleDescription(t, rule.id, rule.name), error)}
		>
			<div className="flex items-center gap-2">
				{rule.id === LADO_RULE_PERMISSION_CEILING ? (
					<SettingsOptionMenu<LadoCeilingMode | "">
						aria-label={name}
						value={rule.ceiling?.mode ?? ""}
						options={LADO_CEILING_MODES.map((mode) => ({ value: mode, label: ladoCeilingModeLabel(t, mode) }))}
						placeholder={t("lado.rules.ceilingPlaceholder")}
						disabled={busy}
						triggerClassName="w-fit"
						onChange={(mode) => {
							if (mode && mode !== rule.ceiling?.mode) onCeilingChange(mode);
						}}
					/>
				) : rule.id === LADO_RULE_PROTECTED_PATHS ? (
					<>
						<span className="text-sm tabular-nums text-settings-muted">
							{t("lado.rules.protectedPaths.count", { count: rule.paths?.globs.length ?? 0 })}
						</span>
						<Button
							type="button"
							variant="ghost"
							size="sm"
							aria-label={t("lado.protectedPaths.title")}
							className="text-settings-muted hover:text-settings-label"
							disabled={busy}
							onClick={onEditPaths}
						>
							<span className="text-control">{t("lado.rules.edit")}</span>
						</Button>
					</>
				) : (
					<span className="text-sm text-settings-muted">{t("lado.rules.alwaysOn")}</span>
				)}
				<RowMenu name={name}>
					<HistoryItem onSelect={onHistory} />
				</RowMenu>
			</div>
		</SettingsRow>
	);
}

export type LadoRuleRowActions = {
	onGlobalChange: (on: boolean) => void;
	onEdit: () => void;
	onHistory: () => void;
	onArchive: () => void;
	onUnarchive: () => void;
	onDelete: () => void;
};

/**
 * A rule that is not protected. Its one line under the name: what a built-in
 * does, the category when no card heading names it (Archived mixes them),
 * how strongly LADO applies it, and where it is forced on or off.
 */
export function LadoRuleRow({
	rule,
	busy,
	error,
	showCategory = false,
	actions,
}: {
	rule: LadoRule;
	busy: boolean;
	error?: unknown;
	showCategory?: boolean;
	actions: LadoRuleRowActions;
}) {
	const { t } = useTranslation();
	const name = ladoRuleName(t, rule.id, rule.name);
	const description = ladoRuleDescription(t, rule.id, rule.name);
	const meta = [
		...(showCategory ? [ladoCategoryLabel(t, rule.category)] : []),
		ladoStrengthLabel(t, rule.strength),
		...ladoOverrideSummary(t, rule),
	].join(" · ");
	const detail = description ? <>{description} {meta}</> : meta;
	return (
		<SettingsRow data-testid="lado-rule-row" data-rule-id={rule.id} label={name} description={withFailure(t, detail, error)}>
			<div className="flex items-center gap-2">
				{rule.archived ? null : (
					<Switch
						aria-label={t("lado.rules.global", { name })}
						checked={rule.global === "on"}
						disabled={busy}
						onCheckedChange={actions.onGlobalChange}
					/>
				)}
				<LadoRuleMenu rule={rule} name={name} busy={busy} actions={actions} />
			</div>
		</SettingsRow>
	);
}

/** A row's menu button and its menu. */
function RowMenu({ name, busy = false, children }: { name: string; busy?: boolean; children: ReactNode }) {
	const { t } = useTranslation();
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild disabled={busy}>
				<button
					type="button"
					aria-label={t("lado.rules.actions", { name })}
					className="settings-close-button shrink-0 focus-visible:bg-(--color-bg-settings-row-hover) focus-visible:text-settings-title data-[state=open]:bg-(--color-bg-settings-row-hover) data-[state=open]:text-settings-title"
				>
					<MoreHorizontal aria-hidden="true" className="size-4" />
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="min-w-40">
				{children}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function HistoryItem({ onSelect }: { onSelect: () => void }) {
	const { t } = useTranslation();
	return (
		<DropdownMenuItem onSelect={onSelect}>
			<History aria-hidden="true" />
			{t("lado.rules.showHistory")}
		</DropdownMenuItem>
	);
}

function LadoRuleMenu({ rule, name, busy, actions }: { rule: LadoRule; name: string; busy: boolean; actions: LadoRuleRowActions }) {
	const { t } = useTranslation();
	// LADO's own rules are never deleted, so their menu does not offer it.
	const deletable = !rule.builtin;
	return (
		<RowMenu name={name} busy={busy}>
			{rule.archived ? (
				<DropdownMenuItem onSelect={actions.onUnarchive}>
					<ArchiveRestore aria-hidden="true" />
					{t("lado.rules.unarchive")}
				</DropdownMenuItem>
			) : (
				<>
					<DropdownMenuItem onSelect={actions.onEdit}>
						<Pencil aria-hidden="true" />
						{t("lado.rules.edit")}
					</DropdownMenuItem>
					<HistoryItem onSelect={actions.onHistory} />
					<DropdownMenuItem onSelect={actions.onArchive}>
						<Archive aria-hidden="true" />
						{t("lado.rules.archive")}
					</DropdownMenuItem>
				</>
			)}
			{deletable ? (
				<>
					<DropdownMenuSeparator />
					<DropdownMenuItem
						className="text-destructive focus:text-destructive [&_svg]:text-destructive focus:[&_svg]:text-destructive"
						onSelect={actions.onDelete}
					>
						<Trash2 aria-hidden="true" />
						{t("lado.rules.delete")}
					</DropdownMenuItem>
				</>
			) : null}
		</RowMenu>
	);
}
