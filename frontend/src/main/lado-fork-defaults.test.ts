// @vitest-environment node
// LADO fork defaults (docs/LJY-PRD/TDD.md §10-§11). These pin the values an
// upstream merge must not quietly restore: where the app updates from, where it
// sends telemetry, and whether it keeps the daemon after the app quits.
import { afterEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_POSTHOG_PROJECT_KEY } from "../shared/posthog-config";
import { DEFAULT_SENTRY_DSN } from "../shared/sentry-config";
import { keepDaemonAlive } from "./daemon-owner";
import { MAC_V2_REPOSITORY } from "./mac-differential-v2-protocol";

const FORK_REPO = "LJY981008/agent-orchestrator-fork";

describe("keepDaemonAlive with the packaged LADO default", () => {
	it("keeps the daemon when AO_KEEP_DAEMON is unset or blank", () => {
		expect(keepDaemonAlive({}, true)).toBe(true);
		expect(keepDaemonAlive({ AO_KEEP_DAEMON: "" }, true)).toBe(true);
		expect(keepDaemonAlive({ AO_KEEP_DAEMON: "   " }, true)).toBe(true);
	});

	it.each(["1", "true", "YES", "on"])("keeps the daemon for truthy value %j", (value) => {
		expect(keepDaemonAlive({ AO_KEEP_DAEMON: value }, true)).toBe(true);
	});

	// Any explicit value outside the truthy allowlist turns the default off, so
	// "off", "no" and typos such as "disable" never keep the daemon by surprise.
	it.each(["0", "false", "off", "No", "disable", "random"])("lets explicit value %j turn it off", (value) => {
		expect(keepDaemonAlive({ AO_KEEP_DAEMON: value }, true)).toBe(false);
	});
});

describe("telemetry defaults", () => {
	it("ships no PostHog project key and no Sentry DSN", () => {
		expect(DEFAULT_POSTHOG_PROJECT_KEY).toBe("");
		expect(DEFAULT_SENTRY_DSN).toBe("");
	});
});

describe("update sources", () => {
	afterEach(() => {
		vi.restoreAllMocks();
		vi.unstubAllGlobals();
		vi.doUnmock("electron");
		vi.resetModules();
	});

	it("accepts macOS differential update metadata only from the fork", () => {
		expect(MAC_V2_REPOSITORY).toBe(FORK_REPO);
	});

	it("reads the desktop version floor from the fork and falls back to the fork's releases page", async () => {
		const openExternal = vi.fn(() => Promise.resolve());
		vi.doMock("electron", () => ({
			app: { isPackaged: true, getVersion: () => "0.1.0", quit: vi.fn() },
			dialog: { showMessageBox: vi.fn(() => Promise.resolve({ response: 0 })) },
			shell: { openExternal },
		}));
		const fetchMock = vi.fn(
			async () => ({ ok: true, status: 200, json: async () => ({ min: "0.2.0" }) }) as unknown as Response,
		);
		vi.stubGlobal("fetch", fetchMock);

		const { checkDesktopVersionFloor } = await import("./desktop-version-floor");
		await checkDesktopVersionFloor();

		expect(fetchMock).toHaveBeenCalledWith(
			`https://raw.githubusercontent.com/${FORK_REPO}/main/desktop-version-floor.json`,
			expect.anything(),
		);
		expect(openExternal).toHaveBeenCalledWith(`https://github.com/${FORK_REPO}/releases/latest`);
	});

	it("lists feature builds from the fork when no bundled app-update.yml names a repo", async () => {
		vi.doMock("electron", () => ({ app: { isPackaged: false, getVersion: () => "0.2.0" } }));
		const fetchMock = vi.fn(async () => ({ ok: true, status: 200, json: async () => [] }) as unknown as Response);
		vi.stubGlobal("fetch", fetchMock);

		const { listFeatureBuilds } = await import("./feature-builds");
		await listFeatureBuilds();

		expect(fetchMock).toHaveBeenCalledWith(
			`https://api.github.com/repos/${FORK_REPO}/releases?per_page=100`,
			expect.anything(),
		);
	});
});
