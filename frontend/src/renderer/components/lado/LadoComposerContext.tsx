// LADO fork: the New Task composer's LADO row. It says which repository the
// issue goes to, lets the developer choose one in a workspace with several,
// and offers importing open issues instead (docs/LJY-PRD/TDD.md §5.1).
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLadoRepositories } from "../../hooks/useLado";
import { ladoErrorText } from "../../lib/lado-errors";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";

const rememberedKey = (projectId: string) => `lado.issueRepository.${projectId}`;

function readRemembered(projectId: string): string {
	try {
		return window.localStorage.getItem(rememberedKey(projectId)) ?? "";
	} catch {
		return "";
	}
}

/**
 * The repository a new issue goes to. A project with one repository needs no
 * choice; in a workspace the last choice for the project comes back.
 */
export function useLadoIssueRepository(projectId: string, enabled: boolean) {
	const query = useLadoRepositories(projectId, enabled);
	const repositories = query.data ?? [];
	const [chosen, setChosen] = useState(() => readRemembered(projectId));
	useEffect(() => setChosen(readRemembered(projectId)), [projectId]);
	const selected = repositories.length === 1
		? repositories[0].ref
		: repositories.some((repository) => repository.ref === chosen) ? chosen : "";
	const choose = (ref: string) => {
		setChosen(ref);
		try {
			window.localStorage.setItem(rememberedKey(projectId), ref);
		} catch {
			// Remembering the choice is a convenience.
		}
	};
	return { query, repositories, selected, choose, needsChoice: repositories.length > 1 && selected === "" };
}

export type LadoIssueRepository = ReturnType<typeof useLadoIssueRepository>;

export function LadoComposerContext({
	issueRepository,
	disabled,
	onImport,
}: {
	issueRepository: LadoIssueRepository;
	disabled: boolean;
	onImport: () => void;
}) {
	const { t } = useTranslation();
	const { query, repositories, selected, choose } = issueRepository;
	return (
		<div className="flex min-w-0 items-center gap-2 px-4 pt-3 text-2xs text-muted-foreground">
			{query.isError ? (
				<span className="min-w-0 flex-1 truncate text-destructive" role="alert" title={ladoErrorText(t, query.error)}>
					{ladoErrorText(t, query.error)}
				</span>
			) : repositories.length > 1 ? (
				<div className="flex min-w-0 flex-1 items-center gap-1">
					<span className="shrink-0">{t("lado.task.issueRepository")}</span>
					<Select value={selected} onValueChange={choose} disabled={disabled}>
						<SelectTrigger
							size="auto"
							aria-label={t("lado.task.issueRepository")}
							className="h-6 min-w-0 gap-1 rounded-md bg-transparent px-1.5 py-0 text-2xs font-medium text-foreground hover:bg-interactive-hover"
						>
							<SelectValue placeholder={t("lado.task.chooseRepository")} />
						</SelectTrigger>
						<SelectContent>
							{repositories.map((repository) => (
								<SelectItem key={repository.ref} value={repository.ref} title={repository.path}>
									{repository.name}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</div>
			) : (
				<span className="min-w-0 flex-1 truncate">
					{repositories.length === 1
						? t("lado.task.createsIssueIn", { repository: repositories[0].path })
						: t("lado.task.createsIssue")}
				</span>
			)}
			<button
				type="button"
				className="shrink-0 rounded-md px-1.5 py-0.5 font-medium text-foreground outline-none hover:bg-interactive-hover focus-visible:outline-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50"
				disabled={disabled}
				onClick={onImport}
			>
				{t("lado.task.importIssues")}
			</button>
		</div>
	);
}
