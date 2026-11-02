// LADO fork: whether a chat session has Claude in Chrome, for the composer's
// @browser mention. The daemon answers with the value the running controller
// started with, or what its next start gets.
import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { ladoClaudeChromeSessionQueryKey } from "../lib/lado-chrome";
import type { BrowserMentionState } from "../components/chat/browserMention";

export type LadoSessionClaudeChrome = components["schemas"]["LadoSessionClaudeChromeResponse"];

export async function fetchLadoSessionClaudeChrome(sessionId: string): Promise<LadoSessionClaudeChrome> {
	const { data, error } = await apiClient.GET("/api/v1/lado/sessions/{sessionId}/claude-chrome", {
		params: { path: { sessionId } },
	});
	if (error || !data) throw error ?? new Error("Could not read the session's Claude in Chrome state");
	return data;
}

/** The mention state for the composer, or undefined when this session has no @browser. */
export function browserMentionState(chrome: LadoSessionClaudeChrome | undefined): BrowserMentionState | undefined {
	if (!chrome?.supported) return undefined;
	return {
		enabled: chrome.enabled,
		settingOn: chrome.settingOn,
		kind: chrome.kind === "lado_worker" ? "lado_worker" : "chat",
	};
}

/**
 * The @browser state of a Claude Code chat session. Undefined while loading,
 * on an error, for a session that cannot use it, or when `enabled` is false.
 */
export function useLadoClaudeChrome(sessionId: string | undefined, enabled = true): BrowserMentionState | undefined {
	const query = useQuery({
		queryKey: ladoClaudeChromeSessionQueryKey(sessionId ?? ""),
		queryFn: () => fetchLadoSessionClaudeChrome(sessionId as string),
		enabled: enabled && Boolean(sessionId),
		// The owner turns the setting on in Settings and comes back to the chat.
		refetchOnWindowFocus: true,
		retry: false,
	});
	if (!enabled || query.isError) return undefined;
	return browserMentionState(query.data);
}
