// LADO fork: the Claude in Chrome setting (issue #40). The daemon puts
// CLAUDE_CODE_ENABLE_CFC into each Claude Code Chat controller it starts, from
// this setting: one switch for the chats a person works in, one for the
// workers LADO starts itself. A change applies to the next controller start.
import type { components } from "../../api/schema";
import { apiClient } from "./api-client";
import { aoBridge } from "./bridge";
import { LadoApiError, ladoError } from "./lado";

export type LadoClaudeChromeSettings = components["schemas"]["LadoClaudeChromeSettingsResponse"];

/** The setting and every session's Claude in Chrome state share this prefix. */
export const ladoClaudeChromeQueryRoot = ["lado", "claude-chrome"] as const;
export const ladoClaudeChromeSettingsQueryKey = ["lado", "claude-chrome", "settings"] as const;
export const ladoClaudeChromeSessionQueryKey = (sessionId: string) => ["lado", "claude-chrome", "session", sessionId] as const;

export async function fetchLadoClaudeChrome(): Promise<LadoClaudeChromeSettings> {
	const { data, error } = await apiClient.GET("/api/v1/lado/claude-chrome");
	if (error) throw ladoError(error, "Could not read the Claude in Chrome setting");
	return data;
}

/** Whether next turns on anything current has off. */
export function ladoClaudeChromeTurnsOn(current: LadoClaudeChromeSettings, next: LadoClaudeChromeSettings): boolean {
	return (next.chatSessions && !current.chatSessions) || (next.ladoWorkers && !current.ladoWorkers);
}

/**
 * Replaces the Claude in Chrome setting. Turning parts off is a plain call.
 * Turning anything on lets agents control the Chrome the developer is signed
 * in to, so in the desktop app the main process asks the developer to confirm
 * and signs the change; null means the developer cancelled and nothing
 * changed. In a browser the call goes to the daemon directly, which accepts
 * turning on only in development (AO_LADO_DEV_PRESENCE=1).
 */
export async function updateLadoClaudeChrome(
	current: LadoClaudeChromeSettings,
	next: LadoClaudeChromeSettings,
): Promise<LadoClaudeChromeSettings | null> {
	const body = { chatSessions: next.chatSessions, ladoWorkers: next.ladoWorkers };
	if (ladoClaudeChromeTurnsOn(current, next)) {
		// The bridge answers null when there is no desktop main process to ask.
		const result = (await aoBridge.lado?.presence?.changeClaudeChrome?.(body)) ?? null;
		if (result) {
			if (result.status === "cancelled") return null;
			if (result.status === "error") throw new LadoApiError(result.message, result.code, result.details);
			return result.settings;
		}
	}
	const { data, error } = await apiClient.PUT("/api/v1/lado/claude-chrome", { body });
	if (error) throw ladoError(error, "Could not change the Claude in Chrome setting");
	return data;
}
