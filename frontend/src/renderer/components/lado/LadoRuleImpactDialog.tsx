// LADO fork: what a rule change reaches before it is made
// (docs/LJY-PRD/HARNESS.md §14). A task approved under the rules as they are
// now needs a new approval once they change, so the developer sees those
// tasks first and changes the rules or not.
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { LadoRuleImpact } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { ConfirmDialog } from "../ConfirmDialog";

/** "acme/demo#12" from "github:acme/demo#12" or "gitlab:group/project#7@host". */
function issueLabel(issueRef: string): string {
	return issueRef.slice(issueRef.indexOf(":") + 1).replace(/@.*$/, "");
}

/**
 * Open while impact is set. Confirming applies the change; cancelling, closing
 * or Escape drops it. error is a failed apply, shown in place. title names
 * what changes when it is not the rules, such as the local setup.
 */
export function LadoRuleImpactDialog({
	impact,
	busy,
	error,
	title,
	onConfirm,
	onCancel,
}: {
	impact: LadoRuleImpact | null;
	busy: boolean;
	error?: unknown;
	title?: string;
	onConfirm: () => void;
	onCancel: () => void;
}) {
	const { t } = useTranslation();
	// The last impact stays on screen while the dialog closes.
	const [shown, setShown] = useState(impact);
	if (impact && impact !== shown) setShown(impact);
	const tasks = shown?.tasks ?? [];
	return (
		<ConfirmDialog
			open={impact !== null}
			title={title ?? t("lado.rules.impact.title")}
			description={t("lado.rules.impact.description")}
			confirmLabel={t("lado.rules.impact.change")}
			busy={busy}
			error={error ? ladoErrorText(t, error) : null}
			onConfirm={onConfirm}
			onOpenChange={(open) => {
				if (!open) onCancel();
			}}
		>
			<ul aria-label={t("lado.rules.impact.tasks")} className="settings-grouped-rows flex w-full flex-col">
				{tasks.map((task) => (
					<li key={task.id} className="settings-row-bar" data-testid="lado-impact-task">
						<span className="min-w-0 truncate text-sm text-settings-label" title={task.title}>
							{task.title}
						</span>
						<span className="shrink-0 font-mono text-xs text-settings-muted">{issueLabel(task.issueRef)}</span>
					</li>
				))}
			</ul>
		</ConfirmDialog>
	);
}
