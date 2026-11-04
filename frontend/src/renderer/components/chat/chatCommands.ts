/**
 * Claude Code commands that do nothing useful in Chat.
 *
 * Chat talks to the agent through its ACP adapter, which drops the commands that
 * only work in the interactive terminal. Typed into Chat they would reach the
 * model as ordinary text and waste a turn, so the composer says where they work
 * instead of sending them. A provider that does report one of these names keeps
 * it: the guard only covers what the provider left out.
 */

import type { ChatSkill } from "../../types/conversation";

const TERMINAL_ONLY = (name: string) => `/${name} runs only in the agent's terminal view, not in Chat.`;

const CHAT_UNAVAILABLE_COMMANDS: Readonly<Record<string, string>> = {
	clear: TERMINAL_ONLY("clear"),
	cost: TERMINAL_ONLY("cost"),
	login: TERMINAL_ONLY("login"),
	logout: TERMINAL_ONLY("logout"),
	"release-notes": TERMINAL_ONLY("release-notes"),
	todos: TERMINAL_ONLY("todos"),
	"keybindings-help": TERMINAL_ONLY("keybindings-help"),
	"output-style:new": TERMINAL_ONLY("output-style:new"),
	model: `${TERMINAL_ONLY("model")} Choose it below the message box.`,
	permissions: `${TERMINAL_ONLY("permissions")} Choose it below the message box.`,
	config: TERMINAL_ONLY("config"),
	status: TERMINAL_ONLY("status"),
	doctor: TERMINAL_ONLY("doctor"),
	"terminal-setup": TERMINAL_ONLY("terminal-setup"),
	vim: TERMINAL_ONLY("vim"),
	theme: TERMINAL_ONLY("theme"),
	exit: TERMINAL_ONLY("exit"),
	resume: TERMINAL_ONLY("resume"),
	chrome: `${TERMINAL_ONLY("chrome")} In Chat, Claude in Chrome is set in Settings › General › Claude in Chrome; type @browser to use it.`,
};

/** The names the guard covers, for tests and documentation. */
export const CHAT_UNAVAILABLE_COMMAND_NAMES: readonly string[] = Object.keys(CHAT_UNAVAILABLE_COMMANDS);

/**
 * The notice for a message that is only a terminal-only command, or undefined
 * when it should be sent.
 *
 * Only a whole message of `/name` or `/name arguments` counts: a command
 * mentioned inside prose is the user's text. A name the provider reports as one
 * of its own skills is sent as usual.
 */
export function chatUnavailableCommandNotice(
	body: string,
	skills: readonly Pick<ChatSkill, "name">[],
): string | undefined {
	const match = /^\/(\S+)(?:\s[\s\S]*)?$/.exec(body.trim());
	if (!match) return undefined;
	const name = match[1]!;
	if (!Object.hasOwn(CHAT_UNAVAILABLE_COMMANDS, name)) return undefined;
	if (skills.some((skill) => skill.name === name)) return undefined;
	return CHAT_UNAVAILABLE_COMMANDS[name];
}
