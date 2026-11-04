import { describe, expect, it } from "vitest";
import { CHAT_UNAVAILABLE_COMMAND_NAMES, chatUnavailableCommandNotice } from "./chatCommands";

describe("chatUnavailableCommandNotice", () => {
	it("covers the commands the ACP adapter leaves out of Chat", () => {
		expect([...CHAT_UNAVAILABLE_COMMAND_NAMES].sort()).toEqual([
			"chrome",
			"clear",
			"config",
			"cost",
			"doctor",
			"exit",
			"keybindings-help",
			"login",
			"logout",
			"model",
			"output-style:new",
			"permissions",
			"release-notes",
			"resume",
			"status",
			"terminal-setup",
			"theme",
			"todos",
			"vim",
		]);
	});

	it("explains a whole-message command, with or without arguments", () => {
		expect(chatUnavailableCommandNotice("/cost", [])).toBe("/cost runs only in the agent's terminal view, not in Chat.");
		expect(chatUnavailableCommandNotice("  /resume abc  ", [])).toMatch(/^\/resume runs only/);
		expect(chatUnavailableCommandNotice("/output-style:new", [])).toMatch(/^\/output-style:new runs only/);
	});

	it("points at the controls below the message box for model and permissions", () => {
		expect(chatUnavailableCommandNotice("/model opus", [])).toMatch(/Choose it below the message box\.$/);
		expect(chatUnavailableCommandNotice("/permissions", [])).toMatch(/Choose it below the message box\.$/);
	});

	it("points /chrome at the setting and @browser", () => {
		expect(chatUnavailableCommandNotice("/chrome", [])).toMatch(
			/In Chat, Claude in Chrome is set in Settings › General › Claude in Chrome; type @browser to use it\.$/,
		);
	});

	it("lets through what is not a terminal-only command", () => {
		expect(chatUnavailableCommandNotice("/review", [])).toBeUndefined();
		expect(chatUnavailableCommandNotice("what does /cost show", [])).toBeUndefined();
		expect(chatUnavailableCommandNotice("/costs", [])).toBeUndefined();
		expect(chatUnavailableCommandNotice("/toString", [])).toBeUndefined();
		expect(chatUnavailableCommandNotice("", [])).toBeUndefined();
	});

	it("lets through a command the provider reports", () => {
		expect(chatUnavailableCommandNotice("/cost", [{ name: "cost" }])).toBeUndefined();
	});
});
