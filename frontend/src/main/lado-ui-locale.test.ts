// @vitest-environment node
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { DEFAULT_LOCALE, DEFAULT_UI_SETTINGS } from "../shared/ui-locale";
import { coerceLadoDesktopUiSettings, LADO_DESKTOP_LOCALE, LADO_DESKTOP_UI_SETTINGS } from "./lado-ui-locale";
import { readUiSettings, UI_SETTINGS_FILE_NAME, writeUiSettings } from "./ui-settings";

// LADO fork: the desktop app starts in Korean; English stays the fallback.
describe("LADO desktop UI language", () => {
	let dir: string;
	beforeEach(async () => {
		dir = await mkdtemp(path.join(os.tmpdir(), "lado-ui-locale-"));
	});
	afterEach(async () => {
		await rm(dir, { recursive: true, force: true });
	});

	it("starts in Korean and keeps the shared defaults English", () => {
		expect(LADO_DESKTOP_LOCALE).toBe("ko");
		expect(LADO_DESKTOP_UI_SETTINGS).toEqual({ ...DEFAULT_UI_SETTINGS, locale: "ko" });
		expect(DEFAULT_LOCALE).toBe("en");
		expect(DEFAULT_UI_SETTINGS.locale).toBe("en");
	});

	it("keeps a stored language and fills a missing or unknown one with Korean", () => {
		expect(coerceLadoDesktopUiSettings({ locale: "en" }).locale).toBe("en");
		expect(coerceLadoDesktopUiSettings({ locale: "ja" }).locale).toBe("ja");
		expect(coerceLadoDesktopUiSettings({}).locale).toBe("ko");
		expect(coerceLadoDesktopUiSettings({ locale: "xx" }).locale).toBe("ko");
		expect(coerceLadoDesktopUiSettings(null)).toEqual(LADO_DESKTOP_UI_SETTINGS);
		expect(coerceLadoDesktopUiSettings({ soundNotificationsEnabled: false })).toEqual({
			...LADO_DESKTOP_UI_SETTINGS,
			soundNotificationsEnabled: false,
		});
	});

	it("reads Korean from a settings file that has no language yet", async () => {
		await writeFile(path.join(dir, UI_SETTINGS_FILE_NAME), JSON.stringify({ soundNotificationsEnabled: false }), "utf8");
		expect(await readUiSettings(dir)).toEqual({ ...LADO_DESKTOP_UI_SETTINGS, soundNotificationsEnabled: false });
	});

	it("never turns an explicit English choice back into Korean on a later write", async () => {
		await writeUiSettings(dir, { locale: "en" });
		await writeUiSettings(dir, { soundNotificationsEnabled: false });
		expect(await readUiSettings(dir)).toEqual({ ...DEFAULT_UI_SETTINGS, locale: "en", soundNotificationsEnabled: false });
	});

	it("writes Korean into a new settings file when another setting changes first", async () => {
		await writeUiSettings(dir, { terminalShell: { kind: "pwsh" } });
		expect(await readUiSettings(dir)).toEqual({ ...LADO_DESKTOP_UI_SETTINGS, terminalShell: { kind: "pwsh" } });
	});
});
