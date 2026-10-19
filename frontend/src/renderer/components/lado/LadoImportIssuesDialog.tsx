// LADO fork: pick open issues of the project's repositories to work on as
// tasks (docs/LJY-PRD/TDD.md §5.1). In a workspace the issues come grouped by
// repository. Importing writes nothing to the tracker.
import { X } from "lucide-react";
import { Fragment, useState } from "react";
import { useTranslation } from "react-i18next";
import { useImportLadoTasks, useLadoProjectIssues, useLadoRepositories } from "../../hooks/useLado";
import type { LadoIssue, LadoRepository } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";

export function LadoImportIssuesDialog({
	open,
	projectId,
	onOpenChange,
	onImported,
}: {
	open: boolean;
	projectId: string;
	onOpenChange: (open: boolean) => void;
	onImported?: () => void;
}) {
	const { t } = useTranslation();
	const issues = useLadoProjectIssues(projectId, open);
	const repositories = useLadoRepositories(projectId, open);
	const importTasks = useImportLadoTasks(projectId);
	const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());

	const handleOpenChange = (next: boolean) => {
		if (importTasks.isPending && !next) return;
		if (!next) {
			setSelected(new Set());
			importTasks.reset();
		}
		onOpenChange(next);
	};

	const toggle = (ref: string, checked: boolean) => {
		setSelected((previous) => {
			const next = new Set(previous);
			if (checked) next.add(ref);
			else next.delete(ref);
			return next;
		});
	};

	const submit = () => {
		if (selected.size === 0 || importTasks.isPending) return;
		importTasks.mutate([...selected], {
			onSuccess: () => {
				handleOpenChange(false);
				onImported?.();
			},
		});
	};

	const list = issues.data?.issues ?? [];
	const failures = issues.data?.failures ?? [];
	// Repository headings help only where an issue number alone is ambiguous.
	const grouped = (repositories.data?.length ?? 0) > 1;
	const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";
	return (
		<Dialog open={open} onOpenChange={handleOpenChange}>
			<DialogContent showCloseButton={false} className={cn(settingsDialogContentClass, "w-[min(520px,calc(100vw-24px))]")}>
				<DialogClose asChild>
					<button
						type="button"
						disabled={importTasks.isPending}
						className="settings-dialog-close-button settings-close-button"
						aria-label={t("lado.import.close")}
					>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("lado.import.title")}</DialogTitle>
					<DialogDescription className="text-control leading-5 text-settings-muted">
						{t("lado.import.description")}
					</DialogDescription>
				</div>
				<div className={cn(settingsDialogBodyClass, "px-5 pt-0 pb-3")}>
					{issues.isPending ? (
						<p className="text-control text-settings-muted" role="status">{t("lado.import.loading")}</p>
					) : issues.isError ? (
						<p className="text-caption leading-4 text-error" role="alert">{ladoErrorText(t, issues.error)}</p>
					) : (
						<>
							{failures.map((failure) => (
								<p key={failure.repository.ref} className="text-caption leading-4 text-error" role="alert">
									{t("lado.import.repositoryFailed", { name: failure.repository.name, reason: failure.problem })}
								</p>
							))}
							{list.length === 0 ? (
								failures.length === 0 ? <p className="text-control text-settings-muted">{t("lado.import.empty")}</p> : null
							) : (
								<ul className="overflow-hidden rounded-md border border-[var(--color-border-settings-dialog)]" aria-label={t("lado.import.title")}>
									{groupByRepository(list).map(({ repository, issues: repositoryIssues }) => (
										<Fragment key={repository.ref}>
											{grouped ? (
												<li className="flex items-baseline gap-2 border-b border-[var(--color-border-settings-dialog)] bg-muted/40 px-3 py-1.5 text-2xs">
													<span className="font-medium text-foreground">{repository.name}</span>
													<span className="truncate font-mono text-muted-foreground">{repository.path}</span>
												</li>
											) : null}
											{repositoryIssues.map((issue) => (
												<IssueRow
													key={issue.ref}
													issue={issue}
													checked={selected.has(issue.ref)}
													disabled={importTasks.isPending}
													onCheckedChange={(checked) => toggle(issue.ref, checked)}
												/>
											))}
										</Fragment>
									))}
								</ul>
							)}
						</>
					)}
					{importTasks.error ? (
						<p role="alert" className="text-caption leading-4 text-error">{ladoErrorText(t, importTasks.error)}</p>
					) : null}
				</div>
				<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
					<DialogClose asChild>
						<Button type="button" variant="footer" className={compactButtonClass} disabled={importTasks.isPending}>
							{t("lado.import.cancel")}
						</Button>
					</DialogClose>
					<Button
						type="button"
						variant="footer-primary"
						className={compactButtonClass}
						disabled={selected.size === 0 || importTasks.isPending}
						onClick={submit}
					>
						{importTasks.isPending ? t("lado.import.importing") : t("lado.import.import")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}

function IssueRow({
	issue,
	checked,
	disabled,
	onCheckedChange,
}: {
	issue: LadoIssue;
	checked: boolean;
	disabled: boolean;
	onCheckedChange: (checked: boolean) => void;
}) {
	const id = `lado-import-${issue.ref}`;
	return (
		<li className="border-b border-[var(--color-border-settings-dialog)] last:border-b-0">
			<label htmlFor={id} className="flex min-h-9 cursor-pointer items-center gap-3 px-3 py-2 hover:bg-interactive-hover">
				<Checkbox id={id} checked={checked} disabled={disabled} onCheckedChange={(next) => onCheckedChange(next === true)} />
				<span className="shrink-0 font-mono text-2xs tabular-nums text-muted-foreground">{`#${issue.number}`}</span>
				<span className="min-w-0 flex-1 truncate text-control text-foreground" title={issue.title}>{issue.title}</span>
			</label>
		</li>
	);
}

// Issues keep the daemon's order within each repository, and repositories
// keep the order in which their first issue came.
function groupByRepository(issues: LadoIssue[]): { repository: LadoRepository; issues: LadoIssue[] }[] {
	const groups = new Map<string, { repository: LadoRepository; issues: LadoIssue[] }>();
	for (const issue of issues) {
		const group = groups.get(issue.repository.ref);
		if (group) group.issues.push(issue);
		else groups.set(issue.repository.ref, { repository: issue.repository, issues: [issue] });
	}
	return [...groups.values()];
}
