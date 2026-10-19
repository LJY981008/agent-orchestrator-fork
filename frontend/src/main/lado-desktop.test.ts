// @vitest-environment node
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LADO_DESKTOP_GET_CHANNEL, LADO_DESKTOP_SET_CHANNEL } from "../shared/lado-desktop";
import { createLadoDesktop, registerLadoDesktopIpc, type LadoDesktop, type LadoDesktopDeps } from "./lado-desktop";

const stateDirs: string[] = [];
afterEach(async () => {
	for (const dir of stateDirs.splice(0)) await rm(dir, { recursive: true, force: true });
});

function loginItemApp(openAtLogin = false) {
	return {
		getLoginItemSettings: vi.fn(() => ({ openAtLogin }) as Electron.LoginItemSettings),
		setLoginItemSettings: vi.fn(),
	};
}

async function deps(overrides: Partial<LadoDesktopDeps> = {}): Promise<LadoDesktopDeps> {
	const stateDir = await mkdtemp(path.join(tmpdir(), "lado-desktop-"));
	stateDirs.push(stateDir);
	return {
		app: loginItemApp(),
		platform: "darwin",
		packaged: true,
		env: {},
		homedir: stateDir,
		execPath: "/Applications/Agent Orchestrator.app/Contents/MacOS/agent-orchestrator",
		stateDir: () => stateDir,
		fallbackKeep: true,
		...overrides,
	};
}

function prefsFile(d: LadoDesktopDeps): string {
	return path.join(d.stateDir() ?? "", "lado", "desktop.json");
}

describe("createLadoDesktop", () => {
	it("keeps the daemon by the build default until the user saves a choice", async () => {
		const d = await deps({ fallbackKeep: true });
		const desktop = createLadoDesktop(d);
		expect(desktop.keepDaemonDefault()).toBe(true);

		const next = await desktop.set({ keepRunning: false });
		expect(next.keepRunning).toEqual({ enabled: false, setByEnvironment: false });
		expect(desktop.keepDaemonDefault()).toBe(false);
		expect(JSON.parse(await readFile(prefsFile(d), "utf8"))).toEqual({ keepRunning: false });

		// A later launch reads the saved choice.
		expect(createLadoDesktop(d).keepDaemonDefault()).toBe(false);
	});

	it("uses the build default when the saved file is missing or corrupt", async () => {
		const d = await deps({ fallbackKeep: false });
		expect(createLadoDesktop(d).keepDaemonDefault()).toBe(false);
		await mkdir(path.dirname(prefsFile(d)), { recursive: true });
		await writeFile(prefsFile(d), "{not json");
		expect(createLadoDesktop(d).keepDaemonDefault()).toBe(false);
	});

	it("reports AO_KEEP_DAEMON as the source and never overrides it", async () => {
		const d = await deps({ env: { AO_KEEP_DAEMON: "0" } });
		const view = await createLadoDesktop(d).set({ keepRunning: true });
		expect(view.keepRunning).toEqual({ enabled: false, setByEnvironment: true });
		await expect(readFile(prefsFile(d), "utf8")).rejects.toThrow();
	});

	it("toggles the OS login item only where it is supported", async () => {
		const app = loginItemApp(false);
		const desktop = createLadoDesktop(await deps({ app }));
		expect((await desktop.get()).startAtLogin).toEqual({ supported: true, enabled: false });
		await desktop.set({ startAtLogin: true });
		expect(app.setLoginItemSettings).toHaveBeenCalledWith({ openAtLogin: true });

		const devApp = loginItemApp(false);
		const dev = createLadoDesktop(await deps({ app: devApp, packaged: false }));
		expect((await dev.get()).startAtLogin).toEqual({ supported: false, enabled: false });
		await dev.set({ startAtLogin: true });
		expect(devApp.setLoginItemSettings).not.toHaveBeenCalled();
	});

	it("ignores values that are not booleans", async () => {
		const app = loginItemApp(false);
		const d = await deps({ app });
		const desktop = createLadoDesktop(d);
		await desktop.set({ startAtLogin: "yes", keepRunning: 1 });
		await desktop.set(null);
		expect(app.setLoginItemSettings).not.toHaveBeenCalled();
		await expect(readFile(prefsFile(d), "utf8")).rejects.toThrow();
	});
});

describe("registerLadoDesktopIpc", () => {
	it("serves get and set on the LADO channels", async () => {
		const handlers = new Map<string, (...args: unknown[]) => unknown>();
		const ipcMain = {
			handle: (channel: string, handler: (...args: unknown[]) => unknown) => {
				handlers.set(channel, handler);
			},
		};
		const view = {
			startAtLogin: { supported: true, enabled: false },
			keepRunning: { enabled: true, setByEnvironment: false },
		};
		const desktop: LadoDesktop = {
			keepDaemonDefault: () => true,
			get: vi.fn(async () => view),
			set: vi.fn(async () => view),
		};
		registerLadoDesktopIpc(ipcMain as unknown as Parameters<typeof registerLadoDesktopIpc>[0], desktop);

		expect(await handlers.get(LADO_DESKTOP_GET_CHANNEL)?.({})).toBe(view);
		expect(await handlers.get(LADO_DESKTOP_SET_CHANNEL)?.({}, { keepRunning: false })).toBe(view);
		expect(desktop.set).toHaveBeenCalledWith({ keepRunning: false });
	});
});
