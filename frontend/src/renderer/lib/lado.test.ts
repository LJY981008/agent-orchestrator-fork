import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "./api-client";
import { aoBridge } from "./bridge";
import {
	approveLadoPlan,
	changeLadoCeiling,
	claimLadoKnowledge,
	fetchLadoDispatchSettings,
	fetchLadoEffective,
	fetchLadoExecution,
	fetchLadoKnowledge,
	fetchLadoTaskTimings,
	fetchLadoTimings,
	importLadoKnowledge,
	LadoApiError,
	ladoTaskTimingsQueryKey,
	ladoTaskViewQueryKey,
	setLadoMode,
	updateLadoDispatchSettings,
	updateLadoProjectSettings,
	type LadoExecution,
	type LadoRule,
} from "./lado";

const { approvePlanMock, changeRuleMock, setLadoModeMock } = vi.hoisted(() => ({
	approvePlanMock: vi.fn(),
	changeRuleMock: vi.fn(),
	setLadoModeMock: vi.fn(),
}));

vi.mock("./api-client", () => ({
	apiClient: { POST: vi.fn(), GET: vi.fn(), PATCH: vi.fn(), PUT: vi.fn() },
	apiErrorCode: (error: unknown) => (error as { code?: string } | undefined)?.code,
	apiErrorDetails: (error: unknown) => (error as { details?: Record<string, unknown> } | undefined)?.details,
	apiErrorMessage: (error: unknown, fallback: string) => (error as { message?: string } | undefined)?.message ?? fallback,
}));

vi.mock("./bridge", () => ({
	aoBridge: { lado: { presence: { approvePlan: approvePlanMock, changeRule: changeRuleMock, setLadoMode: setLadoModeMock } } },
}));

const approval = { id: "a-1", revisionId: "lado-plan-1", taskId: "lado-task-1", sha256: "a".repeat(64), approvedAt: "2026-10-20T10:00:00Z" };
const policySha = "c".repeat(64);
const post = apiClient.POST as unknown as ReturnType<typeof vi.fn>;
const get = apiClient.GET as unknown as ReturnType<typeof vi.fn>;
const patch = apiClient.PATCH as unknown as ReturnType<typeof vi.fn>;
const put = apiClient.PUT as unknown as ReturnType<typeof vi.fn>;

// LADO fork: approvals go through the desktop app's confirmation
// (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md).
describe("approveLadoPlan", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("returns the approval the desktop app made after the developer confirmed", async () => {
		approvePlanMock.mockResolvedValue({ status: "approved", approval });
		await expect(approveLadoPlan("lado-plan-1", "a".repeat(64), policySha)).resolves.toEqual(approval);
		// Main reads the rules itself; only the revision id crosses.
		expect(approvePlanMock).toHaveBeenCalledWith("lado-plan-1");
		expect(post).not.toHaveBeenCalled();
	});

	it("resolves to null when the developer cancelled", async () => {
		approvePlanMock.mockResolvedValue({ status: "cancelled" });
		await expect(approveLadoPlan("lado-plan-1", "a".repeat(64), policySha)).resolves.toBeNull();
		expect(post).not.toHaveBeenCalled();
	});

	it("keeps the daemon's error code when the approval was refused", async () => {
		approvePlanMock.mockResolvedValue({ status: "error", code: "LADO_PLAN_SUPERSEDED", message: "A newer version exists." });
		const error = await approveLadoPlan("lado-plan-1", "a".repeat(64), policySha).catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(LadoApiError);
		expect((error as LadoApiError).code).toBe("LADO_PLAN_SUPERSEDED");
	});

	it("sends the rule snapshot hash when approving in a browser", async () => {
		approvePlanMock.mockResolvedValue(null);
		post.mockResolvedValue({ data: approval, error: undefined });
		await expect(approveLadoPlan("lado-plan-1", "a".repeat(64), policySha)).resolves.toEqual(approval);
		expect(post).toHaveBeenCalledWith("/api/v1/lado/plans/{revisionId}/approve", {
			params: { path: { revisionId: "lado-plan-1" } },
			body: { sha256: "a".repeat(64), policySha256: policySha },
		});
		expect(aoBridge.lado?.presence?.approvePlan).toHaveBeenCalled();
	});
});

const ceiling = { id: "agent.permission.ceiling", version: 4, ceiling: { mode: "auto" } } as LadoRule;

// LADO fork: the permission ceiling, like an approval, needs the human.
describe("changeLadoCeiling", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("returns the rule the desktop app changed after the developer confirmed", async () => {
		const changed = { ...ceiling, version: 5, ceiling: { mode: "bypass-permissions" } } as LadoRule;
		changeRuleMock.mockResolvedValue({ status: "changed", rule: changed });
		await expect(changeLadoCeiling("agent.permission.ceiling", "bypass-permissions")).resolves.toEqual(changed);
		expect(changeRuleMock).toHaveBeenCalledWith("agent.permission.ceiling", "bypass-permissions");
		expect(patch).not.toHaveBeenCalled();
	});

	it("resolves to null when cancelled and keeps a refusal's code", async () => {
		changeRuleMock.mockResolvedValueOnce({ status: "cancelled" });
		await expect(changeLadoCeiling("agent.permission.ceiling", "accept-edits")).resolves.toBeNull();
		changeRuleMock.mockResolvedValueOnce({ status: "error", code: "LADO_RULE_CHANGED", message: "The rule changed." });
		const error = await changeLadoCeiling("agent.permission.ceiling", "accept-edits").catch((caught: unknown) => caught);
		expect((error as LadoApiError).code).toBe("LADO_RULE_CHANGED");
		expect(patch).not.toHaveBeenCalled();
	});

	it("patches the active version directly in a browser", async () => {
		changeRuleMock.mockResolvedValue(null);
		get.mockResolvedValue({ data: { rule: ceiling, versions: [] }, error: undefined });
		const changed = { ...ceiling, version: 5, ceiling: { mode: "accept-edits" } } as LadoRule;
		patch.mockResolvedValue({ data: { applied: true, impact: { projects: [], tasks: [] }, rule: changed }, error: undefined });
		await expect(changeLadoCeiling("agent.permission.ceiling", "accept-edits")).resolves.toEqual(changed);
		expect(patch).toHaveBeenCalledWith("/api/v1/lado/rules/{ruleId}", {
			params: { path: { ruleId: "agent.permission.ceiling" } },
			body: { baseVersion: 4, ceiling: { mode: "accept-edits" } },
		});
	});
});

describe("fetchLadoEffective", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("names exactly one of the project and the task", async () => {
		get.mockResolvedValue({ data: { snapshotSha256: policySha }, error: undefined });
		await fetchLadoEffective("demo");
		await fetchLadoEffective("demo", "lado-task-1");
		expect(get.mock.calls).toEqual([
			["/api/v1/lado/effective", { params: { query: { projectId: "demo" } } }],
			["/api/v1/lado/effective", { params: { query: { taskId: "lado-task-1" } } }],
		]);
	});
});

// LADO fork: the runs and the dispatch settings (step 6).
describe("runs and dispatch settings", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("reads a session's run, and null for a session LADO did not start", async () => {
		const execution = { sessionId: "demo-7", attempt: 1 } as LadoExecution;
		get.mockResolvedValueOnce({ data: execution, error: undefined });
		await expect(fetchLadoExecution("demo-7")).resolves.toEqual(execution);
		expect(get).toHaveBeenCalledWith("/api/v1/lado/executions/{sessionId}", { params: { path: { sessionId: "demo-7" } } });

		get.mockResolvedValueOnce({ data: undefined, error: { code: "LADO_EXECUTION_NOT_FOUND", message: "This session is not a LADO execution" } });
		await expect(fetchLadoExecution("demo-8")).resolves.toBeNull();

		get.mockResolvedValueOnce({ data: undefined, error: { code: "LADO_EXECUTION_LOAD_FAILED", message: "Failed to load the execution" } });
		const error = await fetchLadoExecution("demo-9").catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(LadoApiError);
		expect((error as LadoApiError).code).toBe("LADO_EXECUTION_LOAD_FAILED");
	});

	it("reads the dispatch settings and saves all three together", async () => {
		const settings = { maxConcurrent: 2, maxRunMinutes: 480, holdOnMemoryTight: true };
		get.mockResolvedValue({ data: settings, error: undefined });
		await expect(fetchLadoDispatchSettings()).resolves.toEqual(settings);
		expect(get).toHaveBeenCalledWith("/api/v1/lado/dispatch-settings");

		const saved = { ...settings, maxRunMinutes: 0, updatedAt: "2026-10-23T10:00:00Z" };
		put.mockResolvedValue({ data: saved, error: undefined });
		await expect(updateLadoDispatchSettings({ maxConcurrent: 2, maxRunMinutes: 0, holdOnMemoryTight: true })).resolves.toEqual(saved);
		expect(put).toHaveBeenCalledWith("/api/v1/lado/dispatch-settings", {
			body: { maxConcurrent: 2, maxRunMinutes: 0, holdOnMemoryTight: true },
		});
	});

	it("keeps the daemon's code when the settings are refused", async () => {
		put.mockResolvedValue({
			data: undefined,
			error: { code: "LADO_DISPATCH_SETTINGS_INVALID", message: "maxConcurrent must be 1 to 8, got 9", details: { min: 1, max: 8 } },
		});
		const error = await updateLadoDispatchSettings({ maxConcurrent: 9, maxRunMinutes: 480, holdOnMemoryTight: true }).catch(
			(caught: unknown) => caught,
		);
		expect((error as LadoApiError).code).toBe("LADO_DISPATCH_SETTINGS_INVALID");
		expect((error as LadoApiError).details).toEqual({ min: 1, max: 8 });
	});

	it("changes a project's cap without the desktop confirmation", async () => {
		const settings = { projectId: "demo", ladoMode: true, maxConcurrent: 3 };
		put.mockResolvedValue({ data: settings, error: undefined });
		await expect(updateLadoProjectSettings("demo", { maxConcurrent: 3 })).resolves.toEqual(settings);
		expect(put).toHaveBeenCalledWith("/api/v1/lado/projects/{projectId}/settings", {
			params: { path: { projectId: "demo" } },
			body: { maxConcurrent: 3 },
		});
		expect(setLadoModeMock).not.toHaveBeenCalled();
	});
});

// LADO fork: turning LADO mode off lets workers start without approved plans,
// so it goes through the desktop app's confirmation; turning it on does not.
describe("setLadoMode", () => {
	const on = { projectId: "demo", ladoMode: true, maxConcurrent: 1 };
	const off = { projectId: "demo", ladoMode: false, maxConcurrent: 1 };

	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("turns LADO mode on with a plain call", async () => {
		put.mockResolvedValue({ data: on, error: undefined });
		await expect(setLadoMode("demo", true)).resolves.toEqual(on);
		expect(put).toHaveBeenCalledWith("/api/v1/lado/projects/{projectId}/settings", {
			params: { path: { projectId: "demo" } },
			body: { ladoMode: true },
		});
		expect(setLadoModeMock).not.toHaveBeenCalled();
	});

	it("keeps the daemon's refusal of turning it on", async () => {
		put.mockResolvedValue({
			data: undefined,
			error: { code: "LADO_TRACKER_INTAKE_ENABLED", message: "Turn off tracker intake for this project first." },
		});
		const error = await setLadoMode("demo", true).catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(LadoApiError);
		expect((error as LadoApiError).code).toBe("LADO_TRACKER_INTAKE_ENABLED");
	});

	it("turns it off through the desktop app first", async () => {
		setLadoModeMock.mockResolvedValue({ status: "changed", settings: off });
		await expect(setLadoMode("demo", false)).resolves.toEqual(off);
		// Main reads the project itself; only the id and the mode cross.
		expect(setLadoModeMock).toHaveBeenCalledWith("demo", false);
		expect(put).not.toHaveBeenCalled();
	});

	it("changes nothing when the developer cancels", async () => {
		setLadoModeMock.mockResolvedValue({ status: "cancelled" });
		await expect(setLadoMode("demo", false)).resolves.toBeNull();
		expect(put).not.toHaveBeenCalled();
	});

	it("keeps the code of a refusal from the desktop app", async () => {
		setLadoModeMock.mockResolvedValue({
			status: "error",
			code: "LADO_PRESENCE_EXPIRED",
			message: "The confirmation expired.",
			details: { projectId: "demo" },
		});
		const error = await setLadoMode("demo", false).catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(LadoApiError);
		expect(error).toMatchObject({ code: "LADO_PRESENCE_EXPIRED", details: { projectId: "demo" } });
		expect(put).not.toHaveBeenCalled();
	});

	it("turns it off directly in a browser", async () => {
		setLadoModeMock.mockResolvedValue(null);
		put.mockResolvedValue({ data: off, error: undefined });
		await expect(setLadoMode("demo", false)).resolves.toEqual(off);
		expect(setLadoModeMock).toHaveBeenCalledWith("demo", false);
		expect(put).toHaveBeenCalledWith("/api/v1/lado/projects/{projectId}/settings", {
			params: { path: { projectId: "demo" } },
			body: { ladoMode: false },
		});
	});
});

// Issue #34: the task's stage times and its sessions' investigation, and a
// project's baseline over its newest tasks.
describe("LADO timings", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("reads a task's timings under the task view's key", async () => {
		const timings = {
			taskId: "lado-task-1",
			title: "Add login",
			timeline: { points: [], totalMs: 0, planRequests: 0, revisions: 0, approvals: 0, runs: 0 },
			sessions: [],
		};
		get.mockResolvedValueOnce({ data: timings, error: undefined });
		await expect(fetchLadoTaskTimings("lado-task-1")).resolves.toEqual(timings);
		expect(get).toHaveBeenCalledWith("/api/v1/lado/tasks/{taskId}/timings", { params: { path: { taskId: "lado-task-1" } } });
		// Whatever refreshes the task view refreshes its timings.
		expect(ladoTaskTimingsQueryKey("lado-task-1").slice(0, 3)).toEqual([...ladoTaskViewQueryKey("lado-task-1")]);

		get.mockResolvedValueOnce({ data: undefined, error: { code: "LADO_TASK_NOT_FOUND", message: "No such task" } });
		const error = await fetchLadoTaskTimings("lado-task-9").catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(LadoApiError);
		expect((error as LadoApiError).code).toBe("LADO_TASK_NOT_FOUND");
	});

	it("reads a project's baseline, sending the limit only when given", async () => {
		const summary = { projectId: "demo", tasks: [], gaps: [], investigation: [] };
		get.mockResolvedValue({ data: summary, error: undefined });
		await expect(fetchLadoTimings("demo")).resolves.toEqual(summary);
		expect(get).toHaveBeenLastCalledWith("/api/v1/lado/timings", { params: { query: { projectId: "demo" } } });
		await fetchLadoTimings("demo", 50);
		expect(get).toHaveBeenLastCalledWith("/api/v1/lado/timings", { params: { query: { projectId: "demo", limit: 50 } } });

		get.mockResolvedValue({ data: undefined, error: { code: "LADO_INVALID_REQUEST", message: "limit must be 1 to 100" } });
		const error = await fetchLadoTimings("demo", 500).catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(LadoApiError);
		expect((error as LadoApiError).message).toBe("limit must be 1 to 100");
	});
});

// LADO fork: project knowledge (issue #24).
describe("project knowledge", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	const knowledge = { projectId: "demo", dir: "/k/demo", foreign: false, notes: [], truncated: false, importable: [] };
	const path = { params: { path: { projectId: "demo" } } };

	it("reads the folder and keeps the daemon's code on an error", async () => {
		get.mockResolvedValueOnce({ data: knowledge, error: undefined });
		await expect(fetchLadoKnowledge("demo")).resolves.toEqual(knowledge);
		expect(get).toHaveBeenCalledWith("/api/v1/lado/projects/{projectId}/knowledge", path);
		get.mockResolvedValueOnce({ data: undefined, error: { code: "LADO_KNOWLEDGE_PROJECT_NOT_FOUND", message: "no project" } });
		const error = await fetchLadoKnowledge("demo").catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(LadoApiError);
		expect((error as LadoApiError).code).toBe("LADO_KNOWLEDGE_PROJECT_NOT_FOUND");
	});

	it("sends only the fields of the chosen source", async () => {
		const result = { imported: ["a"], skipped: [] };
		post.mockResolvedValue({ data: result, error: undefined });
		await expect(importLadoKnowledge("demo", { source: "claude_memory", path: "  " })).resolves.toEqual(result);
		expect(post).toHaveBeenLastCalledWith("/api/v1/lado/projects/{projectId}/knowledge/imports", { ...path, body: { source: "claude_memory" } });
		await importLadoKnowledge("demo", { source: "claude_memory", path: " /home/u/old ", fromProjectId: "x" });
		expect(post).toHaveBeenLastCalledWith("/api/v1/lado/projects/{projectId}/knowledge/imports", {
			...path,
			body: { source: "claude_memory", path: "/home/u/old" },
		});
		await importLadoKnowledge("demo", { source: "project", fromProjectId: "old-demo", path: "/ignored" });
		expect(post).toHaveBeenLastCalledWith("/api/v1/lado/projects/{projectId}/knowledge/imports", {
			...path,
			body: { source: "project", fromProjectId: "old-demo" },
		});
	});

	it("takes the folder over and answers it as it is now", async () => {
		post.mockResolvedValueOnce({ data: knowledge, error: undefined });
		await expect(claimLadoKnowledge("demo")).resolves.toEqual(knowledge);
		expect(post).toHaveBeenCalledWith("/api/v1/lado/projects/{projectId}/knowledge/claim", path);
		post.mockResolvedValueOnce({ data: undefined, error: { code: "LADO_KNOWLEDGE_FAILED", message: "disk" } });
		await expect(claimLadoKnowledge("demo")).rejects.toMatchObject({ code: "LADO_KNOWLEDGE_FAILED" });
	});
});
