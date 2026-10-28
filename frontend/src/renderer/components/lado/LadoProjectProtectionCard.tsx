// LADO fork: Project settings → LADO → Branch protection (docs/LJY-PRD/TDD.md
// §7.1). Whether each repository's base branch holds the rules that make
// LADO's integration approval the way in: a required pull request, the
// lado/integration status check, and no force pushes or deletion. A branch
// without them is only watched: LADO detects a merge without approval. The
// daemon keeps its reads for a while; Check again asks the host now.
import type { TFunction } from "i18next";
import { RefreshCw } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";
import { useLadoProtection, useRefreshLadoProtection } from "../../hooks/useLado";
import type { MessageKey } from "../../i18n/messages";
import type { LadoBranchProtection } from "../../lib/lado";
import { LADO_PROTECTION_ANY_SOURCE, ladoErrorText, ladoProtectionPartText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
import { formatResetTime } from "../settings/CodexAccountDetails";
import { SettingsRow } from "../settings/SettingsRow";
import { SettingsSection } from "../settings/SettingsSection";
import { Button } from "../ui/button";

type ProtectionState = LadoBranchProtection["state"];

const stateKeys: Record<ProtectionState, MessageKey> = {
	protected: "lado.protection.state.protected",
	unprotected: "lado.protection.state.unprotected",
	unknown: "lado.protection.state.unknown",
	// LADO does not read GitLab's protection, so the branch counts as not protected.
	unsupported: "lado.protection.state.unprotected",
};

/** "acme/demo" from "github:acme/demo" or "gitlab:group/project@host". */
function repositoryLabel(repository: string): string {
	return repository.slice(repository.indexOf(":") + 1).replace(/@.*$/, "");
}

/** The one muted line under a repository: what is missing, why it could not be read, or that LADO only detects. */
function protectionDetail(t: TFunction, branch: LadoBranchProtection): string | undefined {
	switch (branch.state) {
		case "protected":
			return undefined;
		case "unprotected":
			return branch.missing.length > 0
				? [
						t("lado.protection.missing", { parts: branch.missing.map((part) => ladoProtectionPartText(t, part)).join(", ") }),
						// A check from one app only cannot be LADO's status (Q3 of #15).
						...(branch.missing.includes(LADO_PROTECTION_ANY_SOURCE) ? [t("lado.protection.anySourceGuide")] : []),
					].join(" ")
				: undefined;
		case "unknown":
			return branch.error ? t("lado.protection.error", { error: branch.error }) : undefined;
		case "unsupported":
			return t("lado.protection.gitlab");
	}
}

/** The oldest read: what the card shows is at least that fresh. */
function oldestCheck(repositories: readonly LadoBranchProtection[]): string | undefined {
	return repositories
		.map((branch) => branch.checkedAt)
		.filter((at): at is string => Boolean(at))
		.sort()[0];
}

export function LadoProjectProtectionCard({ projectId }: { projectId: string }) {
	const { t, i18n } = useTranslation();
	const protection = useLadoProtection(projectId);
	const refresh = useRefreshLadoProtection(projectId);
	const repositories = protection.data?.repositories ?? [];
	const checked = formatResetTime(oldestCheck(repositories), i18n.language);
	// How to protect a branch helps only where GitHub's rules are missing or unreadable.
	const showGuide = repositories.some((branch) => branch.state === "unprotected" || branch.state === "unknown");
	const checking = refresh.isPending;

	return (
		<div className="flex w-full flex-col gap-(--size-settings-section-inner-gap)" data-testid="lado-project-protection">
			<SettingsSection
				title={t("lado.protection.title")}
				grouped={repositories.length > 0}
				action={
					<div className="flex items-center gap-1">
						{checked ? (
							<span className="text-xs tabular-nums text-settings-muted" title={checked.full}>
								{t("lado.protection.checkedAt", { time: checked.visible })}
							</span>
						) : null}
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="text-settings-muted hover:text-settings-label"
							disabled={checking || protection.isPending}
							onClick={() => refresh.mutate()}
						>
							<RefreshCw aria-hidden="true" className={cn(checking && "motion-safe:animate-spin")} />
							<span className="text-control">{checking ? t("lado.protection.checking") : t("lado.protection.checkAgain")}</span>
						</Button>
					</div>
				}
			>
				{protection.isPending ? (
					<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
						{t("common.loading")}
					</p>
				) : !protection.data ? (
					<p className="px-(--settings-row-inset) text-xs text-error" role="alert">
						{ladoErrorText(t, protection.error)}
					</p>
				) : repositories.length === 0 ? (
					<p className="px-(--settings-row-inset) text-sm text-settings-muted" role="status">
						{t("lado.protection.none")}
					</p>
				) : (
					repositories.map((branch) => <ProtectionRow key={`${branch.repository}\n${branch.branch}`} branch={branch} />)
				)}
			</SettingsSection>
			{showGuide ? (
				<p className="px-(--settings-row-inset) text-control leading-4 text-settings-muted" data-testid="lado-protection-guide">
					<Trans i18nKey="lado.protection.guide" components={{ code: <span className="font-mono" /> }} />
				</p>
			) : null}
			{refresh.isError ? (
				<p role="alert" className="px-(--settings-row-inset) text-caption leading-4 text-error">
					{ladoErrorText(t, refresh.error)}
				</p>
			) : null}
		</div>
	);
}

function ProtectionRow({ branch }: { branch: LadoBranchProtection }) {
	const { t } = useTranslation();
	const ready = branch.state === "protected";
	return (
		<SettingsRow
			data-testid="lado-protection-repository"
			data-state={branch.state}
			label={
				<span className="min-w-0">
					<span className="font-mono">{repositoryLabel(branch.repository)}</span>
					{branch.branch ? (
						<>
							{" · "}
							<span className="font-mono">{branch.branch}</span>
						</>
					) : null}
				</span>
			}
			description={protectionDetail(t, branch)}
		>
			<span className="flex items-center gap-1.5 text-sm text-settings-label">
				<span
					aria-hidden="true"
					className={cn(
						"size-1.5 shrink-0 rounded-full",
						ready ? "bg-[var(--color-status-ready)]" : "bg-[var(--color-status-in-review)]",
					)}
				/>
				{t(stateKeys[branch.state])}
			</span>
		</SettingsRow>
	);
}
