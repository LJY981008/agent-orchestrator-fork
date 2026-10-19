// @vitest-environment node
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
	linuxAutostartEntry,
	linuxAutostartPath,
	readStartAtLogin,
	startAtLoginSupported,
	writeStartAtLogin,
	type StartAtLoginDeps,
} from "./lado-autostart";

const homes: string[] = [];
afterEach(async () => {
	for (const home of homes.splice(0)) await rm(home, { recursive: true, force: true });
});

function loginItemApp(openAtLogin = false) {
	return {
		getLoginItemSettings: vi.fn(() => ({ openAtLogin }) as Electron.LoginItemSettings),
		setLoginItemSettings: vi.fn(),
	};
}

async function deps(overrides: Partial<StartAtLoginDeps> = {}): Promise<StartAtLoginDeps> {
	const homedir = await mkdtemp(path.join(tmpdir(), "lado-autostart-"));
	homes.push(homedir);
	return {
		app: loginItemApp(),
		platform: "linux",
		packaged: true,
		env: {},
		homedir,
		execPath: "/opt/Agent Orchestrator/agent-orchestrator",
		...overrides,
	};
}

describe("startAtLoginSupported", () => {
	it.each(["win32", "darwin", "linux"] as const)("supports packaged %s builds", async (platform) => {
		expect(startAtLoginSupported(await deps({ platform }))).toBe(true);
	});

	it("never registers a dev build", async () => {
		expect(startAtLoginSupported(await deps({ packaged: false }))).toBe(false);
	});

	it("does not support other platforms", async () => {
		expect(startAtLoginSupported(await deps({ platform: "freebsd" }))).toBe(false);
	});
});

describe("Windows and macOS login items", () => {
	it.each(["win32", "darwin"] as const)("reads and writes the OS login item on %s", async (platform) => {
		const app = loginItemApp(true);
		const d = await deps({ platform, app });

		expect(await readStartAtLogin(d)).toBe(true);
		await writeStartAtLogin(d, false);
		expect(app.setLoginItemSettings).toHaveBeenCalledWith({ openAtLogin: false });
	});
});

describe("Linux XDG autostart", () => {
	it("writes one desktop entry under XDG_CONFIG_HOME and removes it when turned off", async () => {
		const d = await deps();
		const configHome = path.join(d.homedir, "xdg");
		d.env = { XDG_CONFIG_HOME: configHome };
		const file = path.join(configHome, "autostart", "agent-orchestrator.desktop");
		expect(linuxAutostartPath(d.env, d.homedir)).toBe(file);

		expect(await readStartAtLogin(d)).toBe(false);
		await writeStartAtLogin(d, true);
		expect(await readStartAtLogin(d)).toBe(true);
		const entry = await readFile(file, "utf8");
		expect(entry).toContain("[Desktop Entry]");
		expect(entry).toContain("Type=Application");
		expect(entry).toContain('Exec="/opt/Agent Orchestrator/agent-orchestrator"');

		await writeStartAtLogin(d, false);
		expect(await readStartAtLogin(d)).toBe(false);
		await expect(readFile(file, "utf8")).rejects.toThrow();
		// Turning it off again is a no-op.
		await expect(writeStartAtLogin(d, false)).resolves.toBeUndefined();
	});

	it("falls back to ~/.config and starts the AppImage when running from one", async () => {
		const d = await deps({ env: { APPIMAGE: "/home/u/Apps/AO.AppImage" } });
		await writeStartAtLogin(d, true);
		const entry = await readFile(path.join(d.homedir, ".config", "autostart", "agent-orchestrator.desktop"), "utf8");
		expect(entry).toContain('Exec="/home/u/Apps/AO.AppImage"');
	});

	it("never uses Electron's login item API on Linux", async () => {
		const d = await deps();
		await writeStartAtLogin(d, true);
		expect(d.app.setLoginItemSettings).not.toHaveBeenCalled();
	});
});

describe("linuxAutostartEntry", () => {
	it("quotes the command as the Desktop Entry spec requires", () => {
		// Inside quotes: \" \` \$ escaped, a literal backslash written as four, % as %%.
		expect(linuxAutostartEntry('/opt/a"b$c`d\\e%f')).toContain('Exec="/opt/a\\"b\\$c\\`d\\\\\\\\e%%f"');
	});
});
