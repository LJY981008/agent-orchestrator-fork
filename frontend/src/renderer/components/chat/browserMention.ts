/**
 * The `@browser` mention: Claude Code's Claude in Chrome integration.
 *
 * Picking a row puts a chip in the composer. The chip shows `@browser` (or one of
 * its actions), but what reaches the agent is an English sentence that names the
 * Claude in Chrome tools and the exact steps for the action. Whether Chrome is
 * actually connected is something only the agent can find out, so every sentence
 * also says what to do when the tools are missing.
 */

import { scoreMatch, type Suggestion } from "./composerSuggest";

/** Whether this chat's controller started with Claude in Chrome, and why not. */
export interface BrowserMentionState {
	/** The running controller was started with Claude in Chrome on. */
	enabled: boolean;
	/** The daemon setting that applies to this kind of session is on. */
	settingOn: boolean;
	/** Which setting applies: chat sessions or LADO workers. */
	kind: "chat" | "lado_worker";
}

/** One `@browser` row: what is shown, and what the chip sends. */
export interface BrowserMention {
	value: string;
	chip: string;
	detail: string;
	text: string;
}

const COMMON =
	"(Claude in Chrome: use the claude-in-chrome skill and its mcp__claude-in-chrome__* tools; call tabs_context_mcp first and work in a new tab. If those tools are missing or no browser is connected, stop and tell me to open Chrome with the Claude extension signed in to the same claude.ai account as this agent, then reconnect at https://clau.de/chrome/reconnect.)";

export const BROWSER_MENTIONS: readonly BrowserMention[] = [
	{
		value: "browser",
		chip: "@browser",
		detail: "Claude in Chrome",
		text: `Chrome ${COMMON}`,
	},
	{
		value: "browser:new_tab",
		chip: "@browser:new_tab",
		detail: "Open a new tab",
		text: `a new Chrome tab (open it with tabs_create_mcp, then navigate) ${COMMON}`,
	},
	{
		value: "browser:screenshot",
		chip: "@browser:screenshot",
		detail: "Screenshot into session artifacts",
		text: `a Chrome screenshot (computer tool, action screenshot, save_to_disk: true; copy the saved file into this session's Session Artifacts folder and tell me its path) ${COMMON}`,
	},
	{
		value: "browser:gif",
		chip: "@browser:gif",
		detail: "Record a GIF into session artifacts",
		text: `a GIF recording of the Chrome tab (gif_creator: start_recording, screenshot, the steps, screenshot, stop_recording, then export with download: true and a unique filename; move the GIF from Chrome's download folder into this session's Session Artifacts folder and tell me its path) ${COMMON}`,
	},
	{
		value: "browser:read_page",
		chip: "@browser:read_page",
		detail: "Read the page",
		text: `the open Chrome page's content (read it with read_page or get_page_text) ${COMMON}`,
	},
];

/**
 * What the composer says instead of inserting the chip when this chat's
 * controller runs without Claude in Chrome. Undefined when it runs with it.
 */
export function browserOffNotice(state: BrowserMentionState): string | undefined {
	if (state.enabled) return undefined;
	if (!state.settingOn && state.kind === "lado_worker") {
		return "Claude in Chrome is off for LADO workers. Turn it on in Settings › General › Claude in Chrome; it applies to workers that start afterwards.";
	}
	if (!state.settingOn) {
		return "Claude in Chrome is off for chat sessions. Turn it on in Settings › General › Claude in Chrome, then start a new chat or run /clear.";
	}
	return "This chat started before Claude in Chrome was turned on. Start a new chat or run /clear to use it.";
}

/**
 * Rank the `@browser` rows for an `@` query.
 *
 * A bare `@` offers only `@browser`, so the worktree files below it stay in view.
 * A typed query matches every row, with `@browser` first among equals.
 */
export function rankBrowserMentions(query: string): Suggestion[] {
	const needle = query.trim().toLowerCase();
	const rows = needle === "" ? BROWSER_MENTIONS.slice(0, 1) : BROWSER_MENTIONS;
	return rows
		.flatMap((mention, index) => {
			const rank = scoreMatch(mention.value, needle, 0);
			return rank === null ? [] : [{ mention, index, rank }];
		})
		.sort((a, b) => a.rank - b.rank || a.index - b.index)
		.map(({ mention }) => ({
			kind: "mention" as const,
			value: mention.value,
			label: mention.chip,
			detail: mention.detail,
			wire: mention.text,
		}));
}

/** The row for a mention value, if it is one. */
export function browserMention(value: string): BrowserMention | undefined {
	return BROWSER_MENTIONS.find((mention) => mention.value === value);
}
