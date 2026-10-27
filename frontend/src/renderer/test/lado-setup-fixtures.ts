// LADO fork: local setup as the daemon sends it, for the Settings → Local
// setup tests, and a fake daemon behind the mocked apiClient.
import type { Mock } from "vitest";
import type {
	LadoSetupAdoption,
	LadoSetupAdoptionPreview,
	LadoSetupAdoptResult,
	LadoSetupChangeResult,
	LadoSetupConflict,
	LadoSetupContent,
	LadoSetupEntry,
	LadoSetupInventory,
	LadoSetupItem,
	LadoSetupItemDetail,
	LadoSetupItemState,
} from "../lib/lado-setup";

export const SETUP_SHA = "a".repeat(64);
export const CLAUDE_DIR = "/home/dev/.claude";
export const CLAUDE_JSON = "/home/dev/.claude.json";
export const CODEX_HOME = "/home/dev/.codex";
export const MANAGED = "/home/dev/.ao/lado/harness";

export function setupEntry(fields: Partial<LadoSetupEntry> & Pick<LadoSetupEntry, "tool" | "name" | "status">): LadoSetupEntry {
	return {
		defaultAction: "",
		itemId: "",
		keptInPlace: [],
		kind: "",
		location: fields.tool === "codex" ? "codex_home" : "claude_dir",
		path: "",
		projectId: "",
		reason: "",
		sha256: "",
		size: 0,
		tracked: false,
		...fields,
	};
}

export function setupItem(fields: Partial<LadoSetupItemState> & Pick<LadoSetupItemState, "itemId" | "kind" | "name">): LadoSetupItemState {
	const on = (fields.global ?? "on") === "on";
	return {
		global: "on",
		installedSha256: "",
		on,
		project: "inherit",
		sha256: SETUP_SHA,
		source: { scope: "global", scopeId: "", state: on ? "on" : "off" },
		task: "inherit",
		tool: "claude-code",
		version: 1,
		versionId: `${fields.itemId}-v1`,
		...fields,
	} as LadoSetupItemState;
}

export function setupInventory(fields: Partial<LadoSetupInventory> = {}): LadoSetupInventory {
	return {
		adoptions: [],
		drift: [],
		entries: [],
		items: [],
		os: "linux",
		projects: [],
		roots: [
			{ tool: "claude-code", location: "claude_dir", path: CLAUDE_DIR, found: true, error: "" },
			{ tool: "claude-code", location: "claude_json", path: CLAUDE_JSON, found: true, error: "" },
			{ tool: "codex", location: "codex_home", path: CODEX_HOME, found: true, error: "" },
		],
		scannedAt: "2026-10-26T05:32:00Z",
		verified: { "claude-code": true, codex: true },
		...fields,
	};
}

export function setupConflict(fields: Partial<LadoSetupConflict> & Pick<LadoSetupConflict, "code" | "severity">): LadoSetupConflict {
	return { acceptanceId: "", contentSha256: SETUP_SHA, detail: "", details: {}, itemId: "", path: "", ...fields };
}

export function setupAdoption(fields: Partial<LadoSetupAdoption> & Pick<LadoSetupAdoption, "id">): LadoSetupAdoption {
	return {
		actor: "human",
		createdAt: "2026-10-26T05:00:00Z",
		errorMessage: "",
		itemCount: 1,
		manifestSha256: SETUP_SHA,
		os: "linux",
		planSha256: SETUP_SHA,
		state: "completed",
		...fields,
	};
}

export function adoptionPreview(fields: Partial<LadoSetupAdoptionPreview> = {}): LadoSetupAdoptionPreview {
	return {
		conflicts: [],
		entries: [],
		os: "linux",
		planSha256: "b".repeat(64),
		preconditions: { ok: true, os: "linux", processes: [], processesIgnored: false, sessions: [], unverified: [] },
		rewrites: [],
		warnings: [],
		...fields,
	};
}

export function adoptResult(fields: Partial<LadoSetupAdoptResult> = {}): LadoSetupAdoptResult {
	return {
		adoptionId: "",
		applied: false,
		items: [],
		preview: adoptionPreview(),
		state: "",
		verification: [],
		warnings: [],
		...fields,
	};
}

export function setupChangeResult(fields: Partial<LadoSetupChangeResult> = {}): LadoSetupChangeResult {
	return {
		actor: "",
		applied: false,
		changeSha256: "c".repeat(64),
		conflictsAdded: [],
		conflictsRemoved: [],
		impact: { projects: [], tasks: [] },
		item: {
			adoptionId: "",
			createdAt: "2026-10-26T05:00:00Z",
			id: "item-1",
			kind: "instructions",
			live: true,
			name: "CLAUDE.md",
			originPath: "",
			tool: "claude-code",
		},
		needsPresence: false,
		op: "binding",
		pins: 0,
		warnings: [],
		...fields,
	};
}

/** A managed item as the item routes answer with it. */
export function setupItemRecord(fields: Partial<LadoSetupItem> & Pick<LadoSetupItem, "id" | "kind" | "name">): LadoSetupItem {
	return { adoptionId: "", createdAt: "2026-10-26T05:00:00Z", live: true, originPath: "", tool: "claude-code", ...fields };
}

/** An item's content: a file's text by default. */
export function setupContent(fields: Partial<LadoSetupContent> = {}): LadoSetupContent {
	const text = fields.text ?? "";
	return { contentKind: "file", executable: false, files: [], sha256: SETUP_SHA, size: text.length, text, ...fields };
}

/** An item with its versions, oldest first; content undefined is a version LADO could not read. */
export function setupItemDetail(
	item: LadoSetupItem,
	versions: Array<{ content?: LadoSetupContent; actor?: string; createdAt?: string }>,
	fields: Partial<LadoSetupItemDetail> = {},
): LadoSetupItemDetail {
	return {
		item,
		managedPath: `${MANAGED}/claude/${item.name}`,
		versions: versions.map((entry, index) => ({
			...(entry.content ? { content: entry.content } : {}),
			version: {
				actor: entry.actor ?? "human",
				contentKind: entry.content?.contentKind ?? "file",
				createdAt: entry.createdAt ?? `2026-10-2${index}T05:00:00Z`,
				executable: false,
				id: `${item.id}-v${index + 1}`,
				itemId: item.id,
				sha256: SETUP_SHA,
				size: entry.content?.size ?? 0,
				version: index + 1,
			},
		})),
		warnings: [],
		...fields,
	};
}

/** What the fake daemon answers a call with: data, or an error envelope. */
export type DaemonAnswer = { data?: unknown; error?: { code: string; message: string; details?: Record<string, unknown> } };

/** Handlers by "METHOD path", given the call's params and body. */
export type DaemonRoutes = Record<string, (init: { params?: { path?: Record<string, string>; query?: Record<string, unknown> }; body?: unknown }) => DaemonAnswer | Promise<DaemonAnswer>>;

type MockedClient = Record<"GET" | "POST" | "PUT" | "PATCH" | "DELETE", unknown>;

/** Answers the mocked apiClient's calls from the routes; any other call fails like a missing route. */
export function routeDaemon(client: MockedClient, routes: DaemonRoutes): void {
	for (const method of ["GET", "POST", "PUT", "PATCH", "DELETE"] as const) {
		(client[method] as Mock).mockImplementation(async (path: string, init?: Parameters<DaemonRoutes[string]>[0]) => {
			const route = routes[`${method} ${path}`];
			if (!route) return { error: { code: "NOT_FOUND", message: `no route for ${method} ${path}` } };
			return route(init ?? {});
		});
	}
}

/** The calls the mocked apiClient got for one method and path, with their params and body. */
export function daemonCalls(client: MockedClient, method: keyof MockedClient, path: string): Array<{ params?: unknown; body?: unknown }> {
	return (client[method] as Mock).mock.calls.filter((call) => call[0] === path).map((call) => (call[1] ?? {}) as { params?: unknown; body?: unknown });
}
