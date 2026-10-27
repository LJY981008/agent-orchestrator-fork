import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { createElement, type PropsWithChildren } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "./api-client";
import { useScanLadoSetup } from "../hooks/useLadoSetup";
import { LadoApiError, type LadoRuleImpact } from "./lado";
import {
	adoptLadoSetup,
	fetchLadoSetupConflicts,
	fetchLadoSetupItems,
	ladoSetupConflictsQueryKey,
	ladoSetupItemsQueryKey,
	ladoSetupPresenceRequest,
	previewLadoSetupAdoption,
	restoreLadoSetup,
	useLadoSetupChange,
	type LadoSetupAdoptEntry,
	type LadoSetupAdoptResult,
	type LadoSetupChangeRequest,
	type LadoSetupChangeResult,
	type LadoSetupRestoreResult,
} from "./lado-setup";

const { adoptSetupMock, restoreSetupMock, changeSetupMock } = vi.hoisted(() => ({
	adoptSetupMock: vi.fn(),
	restoreSetupMock: vi.fn(),
	changeSetupMock: vi.fn(),
}));

vi.mock("./api-client", () => ({
	apiClient: { POST: vi.fn(), GET: vi.fn(), PATCH: vi.fn(), PUT: vi.fn(), DELETE: vi.fn() },
	apiErrorCode: (error: unknown) => (error as { code?: string } | undefined)?.code,
	apiErrorDetails: (error: unknown) => (error as { details?: Record<string, unknown> } | undefined)?.details,
	apiErrorMessage: (error: unknown, fallback: string) => (error as { message?: string } | undefined)?.message ?? fallback,
}));

vi.mock("./bridge", () => ({
	aoBridge: { lado: { presence: { adoptSetup: adoptSetupMock, restoreSetup: restoreSetupMock, changeSetup: changeSetupMock } } },
}));

const get = apiClient.GET as unknown as ReturnType<typeof vi.fn>;
const post = apiClient.POST as unknown as ReturnType<typeof vi.fn>;
const put = apiClient.PUT as unknown as ReturnType<typeof vi.fn>;
const del = apiClient.DELETE as unknown as ReturnType<typeof vi.fn>;

const SHA = "a".repeat(64);
const noImpact: LadoRuleImpact = { projects: [], tasks: [] };
const impact: LadoRuleImpact = {
	projects: [{ id: "demo", name: "Demo" }],
	tasks: [{ id: "lado-task-1", projectId: "demo", title: "Add login", issueRef: "github:acme/demo#12" }],
};
// What a setup change refreshes: the setup views, every effective view, the
// task lists and the open task view.
const changeKeys = [["lado", "setup"], ["lado", "effective"], ["lado", "tasks"], ["lado", "task"]];

function changeResult(overrides: Partial<LadoSetupChangeResult> = {}): LadoSetupChangeResult {
	return {
		actor: "",
		applied: false,
		changeSha256: SHA,
		conflictsAdded: [],
		conflictsRemoved: [],
		impact: noImpact,
		item: {
			adoptionId: "",
			createdAt: "2026-10-26T00:00:00Z",
			id: "item-1",
			kind: "mcp_server",
			live: true,
			name: "search",
			originPath: "",
			tool: "claude-code",
		},
		needsPresence: false,
		op: "binding",
		pins: 0,
		warnings: [],
		...overrides,
	};
}

const applied = changeResult({ applied: true, actor: "human" });

function answer<T>(data: T) {
	return { data, error: undefined };
}

function refusal(code: string, details?: Record<string, unknown>) {
	return { data: undefined, error: { code, message: `The daemon's ${code}`, details } };
}

function renderChange() {
	const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
	const invalidate = vi.spyOn(queryClient, "invalidateQueries");
	const wrapper = ({ children }: PropsWithChildren) => createElement(QueryClientProvider, { client: queryClient }, children);
	const { result } = renderHook(() => useLadoSetupChange(), { wrapper });
	return { result, invalidate };
}

function invalidatedKeys(invalidate: { mock: { calls: unknown[][] } }) {
	return invalidate.mock.calls.map(([filters]) => (filters as { queryKey: unknown }).queryKey);
}

const turnOff: LadoSetupChangeRequest = {
	op: "binding",
	input: { itemId: "item-1", scope: "global", scopeId: "", state: "off" },
};
const createServer: LadoSetupChangeRequest = {
	op: "create",
	input: { tool: "claude-code", kind: "mcp_server", name: "search", content: { type: "http", url: "https://search" } },
};

// LADO fork: changes of the managed setup run as a dry run first; one that can
// run commands or widen access goes through the desktop app's confirmation
// (docs/LJY-PRD/HARNESS.md §9.5 and §14).
describe("useLadoSetupChange", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("makes a change that needs no confirmation at once, after its dry run", async () => {
		put.mockResolvedValueOnce(answer(changeResult())).mockResolvedValueOnce(answer(applied));
		const { result, invalidate } = renderChange();

		let outcome: unknown;
		await act(async () => {
			outcome = await result.current.change(turnOff);
		});

		expect(outcome).toEqual({ status: "applied", result: applied });
		expect(put.mock.calls).toEqual([
			["/api/v1/lado/setup/bindings", { body: { ...turnOff.input, dryRun: true } }],
			["/api/v1/lado/setup/bindings", { body: turnOff.input }],
		]);
		expect(changeSetupMock).not.toHaveBeenCalled();
		expect(result.current.pending).toBeNull();
		expect(invalidatedKeys(invalidate)).toEqual(changeKeys);
	});

	it("sends a change that needs confirmation to the desktop app, which signs it", async () => {
		post.mockResolvedValueOnce(answer(changeResult({ op: "create", needsPresence: true })));
		const made = changeResult({ op: "create", applied: true, actor: "human" });
		changeSetupMock.mockResolvedValueOnce({ status: "changed", change: made });
		const { result, invalidate } = renderChange();

		let outcome: unknown;
		await act(async () => {
			outcome = await result.current.change(createServer);
		});

		expect(outcome).toEqual({ status: "applied", result: made });
		// Only the dry run went to the daemon from here; main sends the change.
		expect(post.mock.calls).toEqual([["/api/v1/lado/setup/items", { body: { ...createServer.input, dryRun: true } }]]);
		expect(changeSetupMock).toHaveBeenCalledWith({ method: "POST", path: "/api/v1/lado/setup/items", body: createServer.input });
		expect(invalidatedKeys(invalidate)).toEqual(changeKeys);
	});

	it("changes nothing when the developer cancels the desktop confirmation", async () => {
		post.mockResolvedValueOnce(answer(changeResult({ op: "create", needsPresence: true })));
		changeSetupMock.mockResolvedValueOnce({ status: "cancelled" });
		const { result, invalidate } = renderChange();

		let outcome: unknown;
		await act(async () => {
			outcome = await result.current.change(createServer);
		});

		expect(outcome).toEqual({ status: "cancelled" });
		expect(post).toHaveBeenCalledTimes(1);
		expect(invalidate).not.toHaveBeenCalled();
	});

	it("sends the change to the daemon itself without a desktop app", async () => {
		post.mockResolvedValueOnce(answer(changeResult({ op: "create", needsPresence: true }))).mockResolvedValueOnce(answer(applied));
		changeSetupMock.mockResolvedValueOnce(null);
		const { result } = renderChange();

		let outcome: unknown;
		await act(async () => {
			outcome = await result.current.change(createServer);
		});

		// The daemon accepts it only with the development presence.
		expect(outcome).toEqual({ status: "applied", result: applied });
		expect(post).toHaveBeenLastCalledWith("/api/v1/lado/setup/items", { body: createServer.input });
	});

	it("asks before a change that reaches approved tasks, then confirms through the desktop app", async () => {
		const preview = changeResult({ needsPresence: true, impact });
		put.mockResolvedValueOnce(answer(preview));
		const turnOn: LadoSetupChangeRequest = {
			op: "binding",
			input: { itemId: "item-1", scope: "project", scopeId: "demo", state: "force_on" },
		};
		changeSetupMock.mockResolvedValueOnce({ status: "changed", change: applied });
		const { result, invalidate } = renderChange();

		let outcome: unknown;
		await act(async () => {
			outcome = await result.current.change(turnOn);
		});
		expect(outcome).toEqual({ status: "confirm", result: preview });
		expect(result.current.pending).toEqual({ request: turnOn, result: preview });
		expect(changeSetupMock).not.toHaveBeenCalled();
		expect(invalidate).not.toHaveBeenCalled();

		await act(async () => {
			outcome = await result.current.confirm();
		});
		expect(outcome).toEqual({ status: "applied", result: applied });
		// The confirmation reuses the dry run: one call reached the daemon from here.
		expect(put).toHaveBeenCalledTimes(1);
		expect(changeSetupMock).toHaveBeenCalledWith({ method: "PUT", path: "/api/v1/lado/setup/bindings", body: turnOn.input });
		expect(result.current.pending).toBeNull();
		expect(invalidatedKeys(invalidate)).toEqual(changeKeys);
	});

	it("drops a pending change on cancel", async () => {
		put.mockResolvedValueOnce(answer(changeResult({ impact })));
		const { result } = renderChange();
		await act(async () => {
			await result.current.change(turnOff);
		});
		expect(result.current.pending).not.toBeNull();

		act(() => result.current.cancel());
		expect(result.current.pending).toBeNull();
		expect(put).toHaveBeenCalledTimes(1);
		await expect(result.current.confirm()).resolves.toEqual({ status: "cancelled" });
	});

	it("rejects with the daemon's error code and details", async () => {
		put.mockResolvedValueOnce(refusal("LADO_SETUP_NOT_FOUND"));
		const refused = renderChange();
		let error: unknown;
		await act(async () => {
			error = await refused.result.current.change(turnOff).catch((caught: unknown) => caught);
		});
		expect(error).toBeInstanceOf(LadoApiError);
		expect(error).toMatchObject({ code: "LADO_SETUP_NOT_FOUND", message: "The daemon's LADO_SETUP_NOT_FOUND" });
		// A change the desktop app sent can have been made before its answer went
		// missing, so a failure refreshes the views like a change does.
		expect(invalidatedKeys(refused.invalidate)).toEqual(changeKeys);

		// The desktop app's refusal reads the same way.
		post.mockResolvedValueOnce(answer(changeResult({ op: "create", needsPresence: true })));
		changeSetupMock.mockResolvedValueOnce({
			status: "error",
			code: "LADO_PRESENCE_EXPIRED",
			message: "The confirmation expired.",
			details: { op: "create" },
		});
		const expired = renderChange();
		await act(async () => {
			error = await expired.result.current.change(createServer).catch((caught: unknown) => caught);
		});
		expect(error).toBeInstanceOf(LadoApiError);
		expect(error).toMatchObject({ code: "LADO_PRESENCE_EXPIRED", message: "The confirmation expired.", details: { op: "create" } });

		post.mockResolvedValueOnce(answer(changeResult({ op: "create", needsPresence: true })));
		changeSetupMock.mockResolvedValueOnce({
			status: "error",
			code: "LADO_PRESENCE_ANSWER_UNREADABLE",
			message: "The change was sent, but the AO daemon's answer could not be read.",
		});
		const unread = renderChange();
		await act(async () => {
			error = await unread.result.current.change(createServer).catch((caught: unknown) => caught);
		});
		expect(error).toMatchObject({ code: "LADO_PRESENCE_ANSWER_UNREADABLE" });
		expect(invalidatedKeys(unread.invalidate)).toEqual(changeKeys);
	});

	it("deletes with a dry run query, through the desktop app only when the dry run asks for it", async () => {
		del.mockResolvedValueOnce(answer(changeResult({ op: "delete" }))).mockResolvedValueOnce(answer(applied));
		const plain = renderChange();
		await act(async () => {
			await plain.result.current.change({ op: "delete", itemId: "item-1" });
		});
		expect(del.mock.calls).toEqual([
			["/api/v1/lado/setup/items/{itemId}", { params: { path: { itemId: "item-1" }, query: { dryRun: true } } }],
			["/api/v1/lado/setup/items/{itemId}", { params: { path: { itemId: "item-1" }, query: {} } }],
		]);
		expect(changeSetupMock).not.toHaveBeenCalled();

		// Deleting an item that can run commands is confirmed like any change to it.
		del.mockClear();
		del.mockResolvedValueOnce(answer(changeResult({ op: "delete", needsPresence: true })));
		changeSetupMock.mockResolvedValueOnce({ status: "changed", change: applied });
		const confirmed = renderChange();
		await act(async () => {
			await confirmed.result.current.change({ op: "delete", itemId: "item-1" });
		});
		expect(del).toHaveBeenCalledTimes(1);
		expect(changeSetupMock).toHaveBeenCalledWith({ method: "DELETE", path: "/api/v1/lado/setup/items/item-1", body: {} });
	});
});

describe("ladoSetupPresenceRequest", () => {
	it("names the route and body main confirms, for the changes that can need it", () => {
		expect(ladoSetupPresenceRequest({ op: "edit", itemId: "item 1", input: { baseVersion: 2, content: "text" } })).toEqual({
			method: "PATCH",
			path: "/api/v1/lado/setup/items/item%201",
			body: { baseVersion: 2, content: "text" },
		});
		expect(ladoSetupPresenceRequest({ op: "drift", itemId: "item-1", input: { action: "keep", sha256: SHA } })).toEqual({
			method: "POST",
			path: "/api/v1/lado/setup/items/item-1/drift",
			body: { action: "keep", sha256: SHA },
		});
		expect(ladoSetupPresenceRequest({ op: "delete", itemId: "item-1" })).toEqual({
			method: "DELETE",
			path: "/api/v1/lado/setup/items/item-1",
			body: {},
		});
		// Turning off and inheriting go the same way as turning on.
		expect(ladoSetupPresenceRequest({ op: "binding", input: { itemId: "item-1", scope: "project", scopeId: "demo", state: "inherit" } })).toEqual({
			method: "PUT",
			path: "/api/v1/lado/setup/bindings",
			body: { itemId: "item-1", scope: "project", scopeId: "demo", state: "inherit" },
		});
		for (const request of [
			{ op: "revoke", acceptanceId: "acceptance-1" },
			{ op: "accept", input: { itemId: "item-1", code: "LADO_SETUP_DENIES_GIT", scope: "global" } },
		] as LadoSetupChangeRequest[]) {
			expect(ladoSetupPresenceRequest(request), request.op).toBeNull();
		}
	});
});

describe("adoption and restore", () => {
	const entries: LadoSetupAdoptEntry[] = [{ tool: "claude-code", kind: "skill", name: "review", action: "move" }];
	const adoption = { adoptionId: "adoption-1", applied: true, state: "completed" } as LadoSetupAdoptResult;
	const restore = { adoptionId: "adoption-1", applied: true } as LadoSetupRestoreResult;

	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("previews an adoption without a confirmation", async () => {
		const preview = { planSha256: SHA, entries: [], conflicts: [], rewrites: [], warnings: [] };
		post.mockResolvedValueOnce(answer({ preview }));
		await expect(previewLadoSetupAdoption(entries)).resolves.toBe(preview);
		expect(post).toHaveBeenCalledWith("/api/v1/lado/setup/adoptions", { body: { entries, dryRun: true } });
		expect(adoptSetupMock).not.toHaveBeenCalled();
	});

	it("adopts through the desktop app, which previews and signs the plan itself", async () => {
		adoptSetupMock.mockResolvedValueOnce({ status: "adopted", adoption });
		await expect(adoptLadoSetup(entries, SHA)).resolves.toBe(adoption);
		expect(adoptSetupMock).toHaveBeenCalledWith(entries);
		expect(post).not.toHaveBeenCalled();

		adoptSetupMock.mockResolvedValueOnce({ status: "cancelled" });
		await expect(adoptLadoSetup(entries, SHA)).resolves.toBeNull();

		adoptSetupMock.mockResolvedValueOnce({
			status: "error",
			code: "LADO_SETUP_TOOL_RUNNING",
			message: "Close them.",
			details: { sessions: [{ id: "demo-1", tool: "claude-code" }], processes: [] },
		});
		await expect(adoptLadoSetup(entries, SHA)).rejects.toMatchObject({
			code: "LADO_SETUP_TOOL_RUNNING",
			details: { sessions: [{ id: "demo-1", tool: "claude-code" }], processes: [] },
		});
		expect(post).not.toHaveBeenCalled();
	});

	it("sends the previewed plan to the daemon without a desktop app", async () => {
		adoptSetupMock.mockResolvedValueOnce(null);
		post.mockResolvedValueOnce(answer(adoption));
		await expect(adoptLadoSetup(entries, SHA)).resolves.toBe(adoption);
		expect(post).toHaveBeenCalledWith("/api/v1/lado/setup/adoptions", { body: { entries, planSha256: SHA } });

		adoptSetupMock.mockResolvedValueOnce(null);
		post.mockResolvedValueOnce(refusal("LADO_SETUP_ADOPTION_FAILED", { stage: "remove", state: "partial" }));
		const error = await adoptLadoSetup(entries, SHA).catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(LadoApiError);
		expect(error).toMatchObject({ code: "LADO_SETUP_ADOPTION_FAILED", details: { stage: "remove", state: "partial" } });
	});

	it("restores through the desktop app, or the daemon without one", async () => {
		restoreSetupMock.mockResolvedValueOnce({ status: "restored", restore });
		await expect(restoreLadoSetup("adoption-1")).resolves.toBe(restore);
		expect(restoreSetupMock).toHaveBeenCalledWith("adoption-1");

		restoreSetupMock.mockResolvedValueOnce({ status: "cancelled" });
		await expect(restoreLadoSetup("adoption-1")).resolves.toBeNull();
		expect(post).not.toHaveBeenCalled();

		restoreSetupMock.mockResolvedValueOnce(null);
		post.mockResolvedValueOnce(answer(restore));
		await expect(restoreLadoSetup("adoption-1")).resolves.toBe(restore);
		expect(post).toHaveBeenCalledWith("/api/v1/lado/setup/adoptions/{adoptionId}/restore", {
			params: { path: { adoptionId: "adoption-1" } },
		});
	});
});

describe("setup queries", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("keys and asks for each scope: Global, a project, a task", async () => {
		expect(ladoSetupItemsQueryKey()).toEqual(["lado", "setup", "items", "", ""]);
		expect(ladoSetupItemsQueryKey({ projectId: "demo" })).toEqual(["lado", "setup", "items", "demo", ""]);
		expect(ladoSetupConflictsQueryKey({ projectId: "demo", taskId: "lado-task-1" })).toEqual([
			"lado",
			"setup",
			"conflicts",
			"demo",
			"lado-task-1",
		]);

		get.mockResolvedValueOnce(answer({ items: [] })).mockResolvedValueOnce(answer({ conflicts: [] }));
		await expect(fetchLadoSetupItems()).resolves.toEqual([]);
		await expect(fetchLadoSetupConflicts({ taskId: "lado-task-1" })).resolves.toEqual([]);
		expect(get.mock.calls).toEqual([
			["/api/v1/lado/setup/items", { params: { query: {} } }],
			["/api/v1/lado/setup/conflicts", { params: { query: { taskId: "lado-task-1" } } }],
		]);

		get.mockResolvedValueOnce(refusal("LADO_SETUP_SCOPE_INVALID", { projectId: "demo", taskId: "lado-task-9" }));
		await expect(fetchLadoSetupItems({ projectId: "demo", taskId: "lado-task-9" })).rejects.toMatchObject({
			code: "LADO_SETUP_SCOPE_INVALID",
			details: { projectId: "demo", taskId: "lado-task-9" },
		});
	});
});

describe("scanning again", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("shows the new inventory and loads the conflicts, items and effective views again", async () => {
		const inventory = { scannedAt: "2026-10-27T00:00:00Z" };
		post.mockResolvedValueOnce(answer(inventory));
		const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		const wrapper = ({ children }: PropsWithChildren) => createElement(QueryClientProvider, { client: queryClient }, children);
		const { result } = renderHook(() => useScanLadoSetup(), { wrapper });

		await act(async () => {
			await result.current.mutateAsync();
		});

		expect(post).toHaveBeenCalledWith("/api/v1/lado/setup/scan");
		expect(queryClient.getQueryData(["lado", "setup", "inventory"])).toEqual(inventory);
		// Not the inventory again, which the scan answered.
		expect(invalidatedKeys(invalidate)).toEqual([
			["lado", "setup", "conflicts"],
			["lado", "setup", "items"],
			["lado", "effective"],
		]);
	});
});
