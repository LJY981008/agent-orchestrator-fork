// LADO desktop "Background" settings shared by Electron main, preload and the
// renderer (docs/LJY-PRD/TDD.md §10).

export type LadoDesktopSettings = {
	/** Start AO when the user logs in. Unsupported in dev builds. */
	startAtLogin: { supported: boolean; enabled: boolean };
	/** Keep the daemon and its sessions running after the app quits. */
	keepRunning: { enabled: boolean; setByEnvironment: boolean };
};

export type LadoDesktopSettingsPatch = {
	startAtLogin?: boolean;
	keepRunning?: boolean;
};

export const LADO_DESKTOP_GET_CHANNEL = "lado:desktop:get";
export const LADO_DESKTOP_SET_CHANNEL = "lado:desktop:set";
