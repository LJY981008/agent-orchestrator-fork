import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
	fetchLadoClaudeChrome,
	ladoClaudeChromeQueryRoot,
	ladoClaudeChromeSettingsQueryKey,
	updateLadoClaudeChrome,
	type LadoClaudeChromeSettings,
} from "../../lib/lado-chrome";
import { ladoErrorText } from "../../lib/lado-errors";
import { SettingsRow } from "../settings/SettingsRow";
import { SettingsSection } from "../settings/SettingsSection";
import { Switch } from "../ui/switch";

type ChromeSetting = keyof LadoClaudeChromeSettings;

/**
 * LADO fork: Settings > General > Claude in Chrome (issue #40). Whether the
 * Claude Code Chat sessions a person works in, and the workers LADO starts,
 * can use the Claude extension in the developer's signed-in Chrome. Turning
 * either on goes through the desktop app's native confirmation; each switch
 * shows only the value the daemon holds, so a cancelled confirmation never
 * flips it. A change applies to the next controller start. Nothing shows
 * while the setting loads or when it cannot be read (the daemon is down or
 * predates the setting); the app reports a daemon that is down elsewhere.
 */
export function LadoClaudeChromeSection() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const settings = useQuery({
		queryKey: ladoClaudeChromeSettingsQueryKey,
		queryFn: fetchLadoClaudeChrome,
		retry: false,
	});
	// The row whose change failed shows the answer under it.
	const [changed, setChanged] = useState<ChromeSetting | null>(null);
	const update = useMutation({
		mutationFn: ({ current, next }: { current: LadoClaudeChromeSettings; next: LadoClaudeChromeSettings }) =>
			updateLadoClaudeChrome(current, next),
		onSuccess: (result) => {
			// null: the developer cancelled and nothing changed.
			if (!result) return;
			queryClient.setQueryData(ladoClaudeChromeSettingsQueryKey, result);
			// Every session's state reads the setting for its next start.
			void queryClient.invalidateQueries({ queryKey: ladoClaudeChromeQueryRoot });
		},
	});
	const saved = settings.data;

	const save = (setting: ChromeSetting, on: boolean) => {
		if (!saved || update.isPending || saved[setting] === on) return;
		const current = { chatSessions: saved.chatSessions, ladoWorkers: saved.ladoWorkers };
		setChanged(setting);
		update.mutate({ current, next: { ...current, [setting]: on } });
	};

	const failure = (setting: ChromeSetting) =>
		update.isError && changed === setting ? (
			<span role="alert" className="block text-error">
				{ladoErrorText(t, update.error)}
			</span>
		) : null;

	if (!saved) return null;

	return (
		<SettingsSection
			title={t("lado.settings.claudeChrome.title")}
			sectionId="claude-chrome"
			grouped
			toolbar={
				<p className="px-(--settings-row-inset) pb-(--size-settings-section-inner-gap) text-control leading-4 text-settings-muted">
					{t("lado.settings.claudeChrome.note")}
				</p>
			}
		>
			<SettingsRow
				label={t("lado.settings.claudeChrome.chatSessions")}
				description={
					<>
						{t("lado.settings.claudeChrome.chatSessionsHint")}
						{failure("chatSessions")}
					</>
				}
			>
				<Switch
					aria-label={t("lado.settings.claudeChrome.chatSessions")}
					checked={saved.chatSessions}
					disabled={update.isPending}
					onCheckedChange={(on) => save("chatSessions", on)}
				/>
			</SettingsRow>
			<SettingsRow
				label={t("lado.settings.claudeChrome.ladoWorkers")}
				description={
					<>
						{t("lado.settings.claudeChrome.ladoWorkersHint")}
						{failure("ladoWorkers")}
					</>
				}
			>
				<Switch
					aria-label={t("lado.settings.claudeChrome.ladoWorkers")}
					checked={saved.ladoWorkers}
					disabled={update.isPending}
					onCheckedChange={(on) => save("ladoWorkers", on)}
				/>
			</SettingsRow>
		</SettingsSection>
	);
}
