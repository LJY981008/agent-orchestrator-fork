// LADO fork: a rule's versions (docs/LJY-PRD/HARNESS.md §4). A version never
// changes and every edit adds one, so the list is the rule's whole history:
// who made each version, when, and what it said. An older version of a rule
// the developer edits can be restored: the editor opens with its text, and
// saving adds it as the newest version.
import type { TFunction } from "i18next";
import { X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useLadoRule } from "../../hooks/useLado";
import { useRestoreFocusOnClose } from "../../hooks/useRestoreFocusOnClose";
import type { MessageKey } from "../../i18n/messages";
import type { LadoRule, LadoRuleVersion } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { ladoRuleName } from "../../lib/lado-rules";
import { cn } from "../../lib/utils";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { Button } from "../ui/button";
import { ladoCategoryLabel, ladoCeilingModeLabel } from "./LadoRuleRows";

// Who made a version (backend/internal/service/ladopolicy/service.go).
const actorKeys: Partial<Record<string, MessageKey>> = {
	lado: "lado.rules.actor.lado",
	api: "lado.rules.actor.api",
	human: "lado.rules.actor.human",
	"human:dev-presence": "lado.rules.actor.devPresence",
};

function actorLabel(t: TFunction, actor: string): string {
	const key = actorKeys[actor];
	return key ? t(key) : actor;
}

/**
 * Open while rule is set and open is true; rules names the rules a version
 * conflicts with. onRestore, when set, offers to restore an older version of
 * a rule that is neither protected nor archived. Mount it again for each
 * opening.
 */
export function LadoRuleHistoryDialog({
	rule,
	open,
	rules,
	onOpenChange,
	onRestore,
}: {
	rule: LadoRule | null;
	open: boolean;
	rules: readonly LadoRule[];
	onOpenChange: (open: boolean) => void;
	onRestore?: (version: LadoRuleVersion) => void;
}) {
	const { t, i18n } = useTranslation();
	const detail = useLadoRule(rule?.id ?? null);
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const [selected, setSelected] = useState<string | null>(null);
	const versions = detail.data?.versions ?? [];
	const current = detail.data?.rule.version ?? rule?.version;
	const shown = versions.find((version) => version.id === selected) ?? versions.at(-1);
	const ruleId = rule?.id ?? "";
	const names = new Map(rules.map((entry) => [entry.id, ladoRuleName(t, entry.id, entry.name)]));
	const time = (iso: string) => new Date(iso).toLocaleString(i18n.language, { dateStyle: "medium", timeStyle: "short" });
	const none = t("lado.rules.none");
	const restorable = onRestore !== undefined && rule !== null && !rule.protected && !rule.archived && shown !== undefined && shown.number !== current;

	return (
		<Dialog open={open && rule !== null} onOpenChange={onOpenChange}>
			<DialogContent
				showCloseButton={false}
				aria-describedby={undefined}
				className={settingsDialogContentClass}
				data-testid="lado-rule-history"
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<DialogClose asChild>
					<button type="button" className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">
						{t("lado.rules.history.title", { name: rule ? ladoRuleName(t, rule.id, rule.name) : "" })}
					</DialogTitle>
				</div>
				<div className={cn(settingsDialogBodyClass, "px-5 pt-0 pb-5")}>
					{detail.isPending ? (
						<p className="text-control text-settings-muted" role="status">
							{t("common.loading")}
						</p>
					) : detail.isError ? (
						<p className="text-caption leading-4 text-error" role="alert">
							{ladoErrorText(t, detail.error)}
						</p>
					) : (
						<>
							<ul aria-label={t("lado.rules.history.versions")} className="settings-grouped-rows flex w-full flex-col">
								{[...versions].reverse().map((version) => (
									<li key={version.id}>
										<button
											type="button"
											aria-pressed={version.id === shown?.id}
											onClick={() => setSelected(version.id)}
											className={cn(
												"flex w-full items-center justify-between gap-3 px-(--settings-row-inset) py-2.5 text-left outline-none transition-colors duration-fast hover:bg-interactive-hover focus-visible:bg-interactive-hover",
												version.id === shown?.id && "bg-interactive-active",
											)}
										>
											<span className="flex min-w-0 items-baseline gap-2">
												<span className="text-sm text-settings-label">{t("lado.rules.history.version", { number: version.number })}</span>
												{version.number === current ? (
													<span className="text-xs text-settings-muted">{t("lado.rules.history.current")}</span>
												) : null}
											</span>
											<span className="shrink-0 text-xs tabular-nums text-settings-muted">
												{time(version.createdAt)} · {actorLabel(t, version.actor)}
											</span>
										</button>
									</li>
								))}
							</ul>
							{shown ? (
								<dl
									aria-label={t("lado.rules.history.version", { number: shown.number })}
									className="settings-grouped-rows flex w-full flex-col"
									data-testid="lado-rule-version"
								>
									<VersionEntry label={t("lado.rules.editor.name")}>{ladoRuleName(t, ruleId, shown.name)}</VersionEntry>
									<VersionEntry label={t("lado.rules.editor.category")}>
										{shown.category.trim() ? ladoCategoryLabel(t, shown.category) : none}
									</VersionEntry>
									{shown.ceiling ? (
										<VersionEntry label={t("lado.rules.builtin.permissionCeiling.name")}>
											{ladoCeilingModeLabel(t, shown.ceiling.mode)}
										</VersionEntry>
									) : null}
									<VersionEntry label={t("lado.rules.editor.instruction")}>{shown.instruction.trim() || none}</VersionEntry>
									<VersionEntry label={t("lado.rules.editor.conflicts")}>
										{shown.conflictsWith.length === 0
											? none
											: shown.conflictsWith.map((conflict) => names.get(conflict) ?? conflict).join(", ")}
									</VersionEntry>
								</dl>
							) : null}
							{restorable ? (
								<div className="flex justify-end">
									<Button
										type="button"
										variant="ghost"
										size="sm"
										className="text-settings-muted hover:text-settings-label"
										onClick={() => onRestore(shown)}
									>
										{t("lado.rules.history.restore")}
									</Button>
								</div>
							) : null}
						</>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}

function VersionEntry({ label, children }: { label: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-1 px-(--settings-row-inset) py-2.5">
			<dt className="text-xs text-settings-muted">{label}</dt>
			<dd className="whitespace-pre-wrap break-words text-sm text-settings-label">{children}</dd>
		</div>
	);
}
