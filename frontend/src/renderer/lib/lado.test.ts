import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "./api-client";
import { aoBridge } from "./bridge";
import { approveLadoPlan, LadoApiError } from "./lado";

const { approvePlanMock } = vi.hoisted(() => ({ approvePlanMock: vi.fn() }));

vi.mock("./api-client", () => ({
	apiClient: { POST: vi.fn(), GET: vi.fn() },
	apiErrorCode: (error: unknown) => (error as { code?: string } | undefined)?.code,
	apiErrorDetails: (error: unknown) => (error as { details?: Record<string, unknown> } | undefined)?.details,
	apiErrorMessage: (error: unknown, fallback: string) => (error as { message?: string } | undefined)?.message ?? fallback,
}));

vi.mock("./bridge", () => ({
	aoBridge: { lado: { presence: { approvePlan: approvePlanMock } } },
}));

const approval = { id: "a-1", revisionId: "lado-plan-1", taskId: "lado-task-1", sha256: "a".repeat(64), approvedAt: "2026-10-20T10:00:00Z" };
const post = apiClient.POST as unknown as ReturnType<typeof vi.fn>;

// LADO fork: approvals go through the desktop app's confirmation
// (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md).
describe("approveLadoPlan", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("returns the approval the desktop app made after the developer confirmed", async () => {
		approvePlanMock.mockResolvedValue({ status: "approved", approval });
		await expect(approveLadoPlan("lado-plan-1", "a".repeat(64))).resolves.toEqual(approval);
		expect(approvePlanMock).toHaveBeenCalledWith("lado-plan-1");
		expect(post).not.toHaveBeenCalled();
	});

	it("resolves to null when the developer cancelled", async () => {
		approvePlanMock.mockResolvedValue({ status: "cancelled" });
		await expect(approveLadoPlan("lado-plan-1", "a".repeat(64))).resolves.toBeNull();
		expect(post).not.toHaveBeenCalled();
	});

	it("keeps the daemon's error code when the approval was refused", async () => {
		approvePlanMock.mockResolvedValue({ status: "error", code: "LADO_PLAN_SUPERSEDED", message: "A newer version exists." });
		const error = await approveLadoPlan("lado-plan-1", "a".repeat(64)).catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(LadoApiError);
		expect((error as LadoApiError).code).toBe("LADO_PLAN_SUPERSEDED");
	});

	it("calls the daemon directly when there is no desktop main process", async () => {
		approvePlanMock.mockResolvedValue(null);
		post.mockResolvedValue({ data: approval, error: undefined });
		await expect(approveLadoPlan("lado-plan-1", "a".repeat(64))).resolves.toEqual(approval);
		expect(post).toHaveBeenCalledWith("/api/v1/lado/plans/{revisionId}/approve", {
			params: { path: { revisionId: "lado-plan-1" } },
			body: { sha256: "a".repeat(64) },
		});
		expect(aoBridge.lado?.presence?.approvePlan).toHaveBeenCalled();
	});
});
