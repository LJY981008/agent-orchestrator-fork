import { describe, expect, it } from "vitest";
import { BROWSER_MENTIONS, browserOffNotice, rankBrowserMentions } from "./browserMention";

describe("rankBrowserMentions", () => {
	it("offers only @browser for an empty query", () => {
		expect(rankBrowserMentions("").map((row) => row.value)).toEqual(["browser"]);
	});

	it("matches every row for a prefix, browser first", () => {
		expect(rankBrowserMentions("br").map((row) => row.value)).toEqual([
			"browser",
			"browser:new_tab",
			"browser:screenshot",
			"browser:gif",
			"browser:read_page",
		]);
	});

	it("matches an action after the colon", () => {
		expect(rankBrowserMentions("screen").map((row) => row.value)).toEqual(["browser:screenshot"]);
		expect(rankBrowserMentions("tab").map((row) => row.value)).toEqual(["browser:new_tab"]);
		expect(rankBrowserMentions("chat")).toEqual([]);
	});

	it("marks rows as mentions that send their sentence", () => {
		const [row] = rankBrowserMentions("browser:gif");
		expect(row).toMatchObject({ kind: "mention", value: "browser:gif", label: "@browser:gif" });
		expect(row?.wire).toBe(BROWSER_MENTIONS.find((mention) => mention.value === "browser:gif")?.text);
	});
});

describe("BROWSER_MENTIONS", () => {
	it("names the Claude in Chrome tools in every sentence", () => {
		for (const mention of BROWSER_MENTIONS) {
			expect(mention.text).toContain("mcp__claude-in-chrome__");
			expect(mention.text).toContain("tabs_context_mcp");
			expect(mention.text).toContain("https://clau.de/chrome/reconnect");
			expect(mention.chip).toBe(`@${mention.value}`);
		}
	});

	it("saves screenshots and GIFs into the session artifacts", () => {
		const text = (value: string) => BROWSER_MENTIONS.find((mention) => mention.value === value)!.text;
		expect(text("browser:screenshot")).toContain("save_to_disk: true");
		expect(text("browser:screenshot")).toContain("Session Artifacts");
		expect(text("browser:gif")).toContain("gif_creator");
		expect(text("browser:gif")).toContain("download: true");
		expect(text("browser:gif")).toContain("Session Artifacts");
		expect(text("browser:new_tab")).toContain("tabs_create_mcp");
		expect(text("browser:read_page")).toMatch(/read_page or get_page_text/);
	});
});

describe("browserOffNotice", () => {
	it("says nothing when Chrome is on", () => {
		expect(browserOffNotice({ enabled: true, settingOn: true, kind: "chat" })).toBeUndefined();
	});

	it("points a chat at the setting and /clear", () => {
		expect(browserOffNotice({ enabled: false, settingOn: false, kind: "chat" })).toBe(
			"Claude in Chrome is off for chat sessions. Turn it on in Settings › General › Claude in Chrome, then start a new chat or run /clear.",
		);
	});

	it("points a LADO worker at the workers setting", () => {
		expect(browserOffNotice({ enabled: false, settingOn: false, kind: "lado_worker" })).toMatch(/off for LADO workers/);
	});

	it("says the chat started before the setting was turned on", () => {
		expect(browserOffNotice({ enabled: false, settingOn: true, kind: "chat" })).toBe(
			"This chat started before Claude in Chrome was turned on. Start a new chat or run /clear to use it.",
		);
	});
});
