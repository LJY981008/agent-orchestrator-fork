// LADO fork: the desktop Background settings (docs/LJY-PRD/TDD.md §10), Start
// at login and Keep agents running after quit. The keep-running choice lives
// under the AO state directory; AO_KEEP_DAEMON, when set, still wins.
import { readFileSync } from "node:fs";
import { mkdir, rename, writeFile } from "node:fs/promises";
import path from "node:path";
import type { IpcMain } from "electron";
import {
	LADO_DESKTOP_GET_CHANNEL,
	LADO_DESKTOP_SET_CHANNEL,
	type LadoDesktopSettings,
	type LadoDesktopSettingsPatch,
} from "../shared/lado-desktop";
import { keepDaemonAlive } from "./daemon-owner";
import { readStartAtLogin, startAtLoginSupported, writeStartAtLogin, type StartAtLoginDeps } from "./lado-autostart";

type LadoDesktopPrefs = { keepRunning?: boolean };

export type LadoDesktopDeps = StartAtLoginDeps & {
	/** AO state directory (~/.ao, or its dev subdirectory); null when unknown. */
	stateDir: () => string | null;
	/** The keep-running answer until the user saves a choice. */
	fallbackKeep: boolean;
};

export type LadoDesktop = {
	keepDaemonDefault(): boolean;
	get(): Promise<LadoDesktopSettings>;
	set(patch: unknown): Promise<LadoDesktopSettings>;
};

function prefsPath(stateDir: string): string {
	return path.join(stateDir, "lado", "desktop.json");
}

// Synchronous so the daemon spawn and the exit handler can read it in place.
function readPrefs(stateDir: string | null): LadoDesktopPrefs {
	if (!stateDir) return {};
	try {
		const parsed: unknown = JSON.parse(readFileSync(prefsPath(stateDir), "utf8"));
		const keepRunning = (parsed as LadoDesktopPrefs | null)?.keepRunning;
		return typeof keepRunning === "boolean" ? { keepRunning } : {};
	} catch {
		return {};
	}
}

async function writePrefs(stateDir: string, prefs: LadoDesktopPrefs): Promise<void> {
	const file = prefsPath(stateDir);
	await mkdir(path.dirname(file), { recursive: true, mode: 0o750 });
	const tmp = `${file}.${process.pid}-${Date.now()}.tmp`;
	await writeFile(tmp, `${JSON.stringify(prefs, null, 2)}\n`, { mode: 0o600 });
	await rename(tmp, file);
}

function sanitizePatch(patch: unknown): LadoDesktopSettingsPatch {
	if (!patch || typeof patch !== "object") return {};
	const input = patch as Record<string, unknown>;
	return {
		...(typeof input.startAtLogin === "boolean" ? { startAtLogin: input.startAtLogin } : {}),
		...(typeof input.keepRunning === "boolean" ? { keepRunning: input.keepRunning } : {}),
	};
}

export function createLadoDesktop(deps: LadoDesktopDeps): LadoDesktop {
	let prefs: LadoDesktopPrefs | null = null;
	let queue: Promise<unknown> = Promise.resolve();
	const loadedPrefs = () => (prefs ??= readPrefs(deps.stateDir()));
	const keepDaemonDefault = () => loadedPrefs().keepRunning ?? deps.fallbackKeep;
	const setByEnvironment = () => Boolean(deps.env.AO_KEEP_DAEMON?.trim());

	const get = async (): Promise<LadoDesktopSettings> => {
		const supported = startAtLoginSupported(deps);
		return {
			startAtLogin: { supported, enabled: supported ? await readStartAtLogin(deps) : false },
			keepRunning: { enabled: keepDaemonAlive(deps.env, keepDaemonDefault()), setByEnvironment: setByEnvironment() },
		};
	};

	const apply = async (patch: LadoDesktopSettingsPatch): Promise<LadoDesktopSettings> => {
		if (patch.startAtLogin !== undefined && startAtLoginSupported(deps)) {
			await writeStartAtLogin(deps, patch.startAtLogin);
		}
		const stateDir = deps.stateDir();
		if (patch.keepRunning !== undefined && stateDir && !setByEnvironment()) {
			const next = { ...loadedPrefs(), keepRunning: patch.keepRunning };
			await writePrefs(stateDir, next);
			prefs = next;
		}
		return get();
	};

	// Serialize writes so two quick toggles cannot interleave.
	const set = (patch: unknown): Promise<LadoDesktopSettings> => {
		const run = queue.then(
			() => apply(sanitizePatch(patch)),
			() => apply(sanitizePatch(patch)),
		);
		queue = run.catch(() => undefined);
		return run;
	};

	return { keepDaemonDefault, get, set };
}

export function registerLadoDesktopIpc(ipcMain: Pick<IpcMain, "handle">, desktop: LadoDesktop): void {
	ipcMain.handle(LADO_DESKTOP_GET_CHANNEL, () => desktop.get());
	ipcMain.handle(LADO_DESKTOP_SET_CHANNEL, (_event, patch: unknown) => desktop.set(patch));
}
