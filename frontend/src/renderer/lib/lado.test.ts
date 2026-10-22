import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "./api-client";
import { aoBridge } from "./bridge";
import { approveLadoPlan, changeLadoCeiling, fetchLadoEffective, LadoApiError, type LadoRule } from "./lado";

const { approvePlanMock, changeRuleMock } = vi.hoisted(() => ({ approvePlanMock: vi.fn(), changeRuleMock: vi.fn() }));

vi.mock("./api-client", () => ({
	apiClient: { POST: vi.fn(), GET: vi.fn(), PATCH: vi.fn() },
	apiErrorCode: (error: unknown) => (error as { code?: string } | undefined)?.code,
	apiErrorDetails: (error: unknown) => (error as { details?: Record<string, unknown> } | undefined)?.details,
	apiErrorMessage: (error: unknown, fallback: string) => (error as { message?: string } | undefined)?.message ?? fallback,
}));

vi.mock("./bridge", () => ({
	aoBridge: { lado: { presence: { approvePlan: approvePlanMock, changeRule: changeRuleMock } } },
}));

const approval = { id: "a-1", revisionId: "lado-plan-1", taskId: "lado-task-1", sha256: "a".repeat(64), approvedAt: "2026-10-20T10:00:00Z" };
const policySha = "c".repeat(64);
const post = apiClient.POST as unknown as ReturnType<typeof vi.fn>;
const get = apiClient.GET as unknown as ReturnType<typeof vi.fn>;
const patch = apiClient.PATCH as unknown as ReturnType<typeof vi.fn>;

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
