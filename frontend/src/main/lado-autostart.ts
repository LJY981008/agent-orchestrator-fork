// LADO fork: Start at login (docs/LJY-PRD/TDD.md §10). Windows and macOS use
// Electron's login item API. Linux has none, so AO writes one XDG autostart
// entry; that file is OS login integration, not app state (CLAUDE.md).
import { mkdir, rename, rm, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import type { App } from "electron";

export type StartAtLoginDeps = {
	app: Pick<App, "getLoginItemSettings" | "setLoginItemSettings">;
	platform: NodeJS.Platform;
	packaged: boolean;
	env: NodeJS.ProcessEnv;
	homedir: string;
	execPath: string;
};

const AUTOSTART_FILE_NAME = "agent-orchestrator.desktop";

/** Dev builds are never registered: the login item would start a bare Electron. */
export function startAtLoginSupported(deps: StartAtLoginDeps): boolean {
	if (!deps.packaged) return false;
	return deps.platform === "win32" || deps.platform === "darwin" || deps.platform === "linux";
}

export function linuxAutostartPath(env: NodeJS.ProcessEnv, homedir: string): string {
	const configHome = env.XDG_CONFIG_HOME?.trim() || path.join(homedir, ".config");
	return path.join(configHome, "autostart", AUTOSTART_FILE_NAME);
}

// Desktop Entry spec: inside double quotes, escape ", `, $ and \ with a
// backslash. The string-value escape rule applies first, so a literal backslash
// is written as four. A % starts a field code and is written as %%.
function quoteExecArgument(arg: string): string {
	const escaped = arg
		.replace(/\\/g, "\\\\\\\\")
		.replace(/(["`$])/g, "\\$1")
		.replace(/%/g, "%%");
	return `"${escaped}"`;
}

export function linuxAutostartEntry(command: string): string {
	return [
		"[Desktop Entry]",
		"Type=Application",
		"Name=Agent Orchestrator",
		`Exec=${quoteExecArgument(command)}`,
		"Terminal=false",
		"X-GNOME-Autostart-enabled=true",
		"",
	].join("\n");
}

export async function readStartAtLogin(deps: StartAtLoginDeps): Promise<boolean> {
	if (deps.platform !== "linux") return deps.app.getLoginItemSettings().openAtLogin;
	try {
		return (await stat(linuxAutostartPath(deps.env, deps.homedir))).isFile();
	} catch {
		return false;
	}
}

export async function writeStartAtLogin(deps: StartAtLoginDeps, enabled: boolean): Promise<void> {
	if (deps.platform !== "linux") {
		deps.app.setLoginItemSettings({ openAtLogin: enabled });
		return;
	}
	const file = linuxAutostartPath(deps.env, deps.homedir);
	if (!enabled) {
		await rm(file, { force: true });
		return;
	}
	// An AppImage runs from a temporary mount, so start the AppImage file itself.
	const command = deps.env.APPIMAGE?.trim() || deps.execPath;
	await mkdir(path.dirname(file), { recursive: true });
	const tmp = `${file}.${process.pid}.tmp`;
	await writeFile(tmp, linuxAutostartEntry(command), { mode: 0o644 });
	await rename(tmp, file);
}
