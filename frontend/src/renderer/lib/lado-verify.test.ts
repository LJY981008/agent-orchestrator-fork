import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "./api-client";
import {
	approveLadoIntegration,
	changeLadoProtectedPaths,
	fetchLadoCheckLog,
	fetchLadoProtection,
	LadoApiError,
	mergeLadoPR,
	runLadoChecks,
	updateLadoCheckTimeout,
	updateLadoProjectChecks,
	type LadoExecution,
	type LadoIntegrationResult,
	type LadoProjectSettings,
	type LadoRule,
} from "./lado";

const { approveIntegrationMock, changeProjectChecksMock, changeRuleMock } = vi.hoisted(() => ({
	approveIntegrationMock: vi.fn(),
	changeProjectChecksMock: vi.fn(),
	changeRuleMock: vi.fn(),
}));

vi.mock("./api-client", () => ({
	apiClient: { POST: vi.fn(), GET: vi.fn(), PATCH: vi.fn(), PUT: vi.fn() },
	apiErrorCode: (error: unknown) => (error as { code?: string } | undefined)?.code,
	apiErrorDetails: (error: unknown) => (error as { details?: Record<string, unknown> } | undefined)?.details,
	apiErrorMessage: (error: unknown, fallback: string) => (error as { message?: string } | undefined)?.message ?? fallback,
}));

vi.mock("./bridge", () => ({
	aoBridge: {
		lado: {
			presence: {
				approveIntegration: approveIntegrationMock,
				changeProjectChecks: changeProjectChecksMock,
				changeRule: changeRuleMock,
			},
		},
	},
}));

const post = apiClient.POST as unknown as ReturnType<typeof vi.fn>;
const get = apiClient.GET as unknown as ReturnType<typeof vi.fn>;
const patch = apiClient.PATCH as unknown as ReturnType<typeof vi.fn>;
const put = apiClient.PUT as unknown as ReturnType<typeof vi.fn>;

const HEAD = "1234567890abcdef1234567890abcdef12345678";
const VERIFY_SHA = "9".repeat(64);
const PR_URL = "https://github.com/acme/demo/pull/7";

const preview: LadoIntegrationResult = {
	confirm: ["outside_plan"],
	verification: {
		computedAt: "2026-10-27T00:05:00Z",
		evidence: [],
		headSha: HEAD,
		items: [{ kind: "paths", outcome: "confirm", code: "outside_plan", details: { outsidePlan: ["docs/a.md"], outsideCount: 1 } }],
		outcome: "confirm",
		prUrl: PR_URL,
		sha256: VERIFY_SHA,
	},
	verificationSha256: VERIFY_SHA,
};
const approved: LadoIntegrationResult = {
	...preview,
	approval: {
		acknowledged: ["outside_plan"],
		actor: "human:dev-presence",
		approvedAt: "2026-10-27T00:06:00Z",
		headSha: HEAD,
		id: "lado-integration-1",
		targetBranch: "main",
		statusAttempts: 0,
		statusState: "pending",
		verificationSha256: VERIFY_SHA,
	},
};
const execution = {
	sessionId: "demo-7",
	pr: { closed: false, merged: false, number: 7, url: PR_URL, headSha: HEAD, targetBranch: "main" },
} as unknown as LadoExecution;
const settings: LadoProjectSettings = {
	projectId: "demo",
	ladoMode: true,
	maxConcurrent: 1,
	checkCommands: [{ name: "test", command: "npm test" }],
	checkCommandsSha256: "0".repeat(64),
	checkTimeoutMinutes: 30,
};

beforeEach(() => {
	vi.clearAllMocks();
});

// LADO fork: the integration approval goes through the desktop app's
// confirmation (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md).
describe("approveLadoIntegration", () => {
	it("returns what the desktop app approved after the developer confirmed", async () => {
		approveIntegrationMock.mockResolvedValue({ status: "approved", integration: approved });
		await expect(approveLadoIntegration("demo-7")).resolves.toEqual(approved);
		// Main reads the run and the dry run itself; only the session id crosses.
		expect(approveIntegrationMock).toHaveBeenCalledWith("demo-7");
		expect(get).not.toHaveBeenCalled();
		expect(post).not.toHaveBeenCalled();
	});

	it("resolves to null when cancelled and keeps a refusal's code", async () => {
		approveIntegrationMock.mockResolvedValue({ status: "cancelled" });
		await expect(approveLadoIntegration("demo-7")).resolves.toBeNull();

		approveIntegrationMock.mockResolvedValue({
			status: "error",
			code: "LADO_INTEGRATION_NOT_READY",
			message: "LADO is still verifying this head.",
			details: { reasons: ["checks_pending"] },
		});
		const error = await approveLadoIntegration("demo-7").catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(LadoApiError);
		expect(error).toMatchObject({ code: "LADO_INTEGRATION_NOT_READY", details: { reasons: ["checks_pending"] } });
		expect(post).not.toHaveBeenCalled();
	});

	it("sends the dry run, then the approval with its values, in a browser", async () => {
		approveIntegrationMock.mockResolvedValue(null);
		get.mockResolvedValue({ data: execution, error: undefined });
		post.mockResolvedValueOnce({ data: preview, error: undefined }).mockResolvedValueOnce({ data: approved, error: undefined });
		await expect(approveLadoIntegration("demo-7")).resolves.toEqual(approved);
		expect(get).toHaveBeenCalledWith("/api/v1/lado/executions/{sessionId}", { params: { path: { sessionId: "demo-7" } } });
		expect(post.mock.calls).toEqual([
			[
				"/api/v1/lado/executions/{sessionId}/integration-approve",
				{ params: { path: { sessionId: "demo-7" } }, body: { prUrl: PR_URL, headSha: HEAD, dryRun: true } },
			],
			[
				"/api/v1/lado/executions/{sessionId}/integration-approve",
				{
					params: { path: { sessionId: "demo-7" } },
					body: { prUrl: PR_URL, headSha: HEAD, verificationSha256: VERIFY_SHA, acknowledge: ["outside_plan"] },
				},
			],
		]);
	});

	it("says a run has no pull request to integrate before it asks the daemon, in a browser", async () => {
		approveIntegrationMock.mockResolvedValue(null);
		get.mockResolvedValue({ data: { ...execution, pr: { ...execution.pr, merged: true } }, error: undefined });
		await expect(approveLadoIntegration("demo-7")).rejects.toMatchObject({ code: "LADO_EXECUTION_NO_PR" });
		get.mockResolvedValue({ data: undefined, error: { code: "LADO_EXECUTION_NOT_FOUND" } });
		await expect(approveLadoIntegration("demo-7")).rejects.toMatchObject({ code: "LADO_EXECUTION_NOT_FOUND" });
		expect(post).not.toHaveBeenCalled();
	});
});

describe("check commands", () => {
	const BASE = "1".repeat(64);

	it("changes the commands through the desktop app, and directly in a browser", async () => {
		const commands = [{ name: "lint", command: "npm run lint" }];
		changeProjectChecksMock.mockResolvedValue({ status: "changed", settings });
		await expect(updateLadoProjectChecks("demo", commands, BASE)).resolves.toEqual(settings);
		expect(changeProjectChecksMock).toHaveBeenCalledWith("demo", commands);
		expect(put).not.toHaveBeenCalled();

		changeProjectChecksMock.mockResolvedValue({ status: "cancelled" });
		await expect(updateLadoProjectChecks("demo", commands, BASE)).resolves.toBeNull();
		changeProjectChecksMock.mockResolvedValue({
			status: "error",
			code: "LADO_CHECK_COMMANDS_INVALID",
			message: "Invalid.",
			details: { index: 0, field: "name", reason: "invalid" },
		});
		await expect(updateLadoProjectChecks("demo", commands, BASE)).rejects.toMatchObject({
			code: "LADO_CHECK_COMMANDS_INVALID",
			details: { index: 0, field: "name", reason: "invalid" },
		});

		changeProjectChecksMock.mockResolvedValue(null);
		put.mockResolvedValue({ data: settings, error: undefined });
		await expect(updateLadoProjectChecks("demo", commands, BASE)).resolves.toEqual(settings);
		expect(put).toHaveBeenCalledWith("/api/v1/lado/projects/{projectId}/settings", {
			params: { path: { projectId: "demo" } },
			// The change names the list the card started from (U7 of #15).
			body: { checkCommands: commands, checkCommandsBase: BASE },
		});
	});

	it("changes the time limit without the desktop confirmation", async () => {
		put.mockResolvedValue({ data: { ...settings, checkTimeoutMinutes: 45 }, error: undefined });
		await expect(updateLadoCheckTimeout("demo", 45)).resolves.toMatchObject({ checkTimeoutMinutes: 45 });
		expect(put).toHaveBeenCalledWith("/api/v1/lado/projects/{projectId}/settings", {
			params: { path: { projectId: "demo" } },
			body: { checkTimeoutMinutes: 45 },
		});
		expect(changeProjectChecksMock).not.toHaveBeenCalled();
	});

	it("runs the checks again and reads a run's output as text", async () => {
		post.mockResolvedValue({ data: { headSha: HEAD, commands: ["test"] }, error: undefined });
		await expect(runLadoChecks("demo-7")).resolves.toEqual({ headSha: HEAD, commands: ["test"] });
		expect(post).toHaveBeenCalledWith("/api/v1/lado/executions/{sessionId}/checks/run", { params: { path: { sessionId: "demo-7" } } });

		get.mockResolvedValue({ data: "ok 12 tests\n", error: undefined });
		await expect(fetchLadoCheckLog("demo-7", "lado-check-1")).resolves.toBe("ok 12 tests\n");
		expect(get).toHaveBeenCalledWith("/api/v1/lado/executions/{sessionId}/checks/{checkRunId}/log", {
			params: { path: { sessionId: "demo-7", checkRunId: "lado-check-1" } },
			parseAs: "text",
		});
		get.mockResolvedValue({ data: "", error: undefined });
		await expect(fetchLadoCheckLog("demo-7", "lado-check-2")).resolves.toBe("");
		get.mockResolvedValue({ data: undefined, error: { code: "LADO_CHECK_RUN_NOT_FOUND", message: "No such run." } });
		await expect(fetchLadoCheckLog("demo-7", "missing")).rejects.toMatchObject({ code: "LADO_CHECK_RUN_NOT_FOUND" });
	});
});

describe("protection and merge", () => {
	it("reads the protection, from the host again only when asked", async () => {
		const protection = { projectId: "demo", repositories: [] };
		get.mockResolvedValue({ data: protection, error: undefined });
		await expect(fetchLadoProtection("demo")).resolves.toEqual(protection);
		await fetchLadoProtection("demo", true);
		expect(get.mock.calls).toEqual([
			["/api/v1/lado/projects/{projectId}/protection", { params: { path: { projectId: "demo" }, query: {} } }],
			["/api/v1/lado/projects/{projectId}/protection", { params: { path: { projectId: "demo" }, query: { refresh: true } } }],
		]);
	});

	it("merges through AO's merge route at the approved head and keeps the gate's refusal", async () => {
		post.mockResolvedValue({ data: { ok: true, method: "squash", prNumber: 7 }, error: undefined });
		await mergeLadoPR({ number: 7, url: PR_URL, headSha: HEAD });
		expect(post).toHaveBeenCalledWith("/api/v1/prs/{id}/merge", {
			params: { path: { id: "7" } },
			body: { prUrl: PR_URL, expectedHeadSha: HEAD },
		});

		post.mockResolvedValue({
			data: undefined,
			error: { code: "LADO_INTEGRATION_APPROVAL_REQUIRED", message: "Approve first.", details: { prUrl: PR_URL, headSha: HEAD, sessionId: "demo-7" } },
		});
		await expect(mergeLadoPR({ number: 7, url: PR_URL, headSha: HEAD })).rejects.toMatchObject({
			code: "LADO_INTEGRATION_APPROVAL_REQUIRED",
			details: { sessionId: "demo-7" },
		});
	});
});

describe("changeLadoProtectedPaths", () => {
	const rule = { id: "paths.protected", version: 2, paths: { globs: [".github/**"] } } as unknown as LadoRule;

	it("changes the globs through the desktop app", async () => {
		changeRuleMock.mockResolvedValue({ status: "changed", rule });
		await expect(changeLadoProtectedPaths([".github/**", "infra/**"])).resolves.toEqual(rule);
		expect(changeRuleMock).toHaveBeenCalledWith("paths.protected", { globs: [".github/**", "infra/**"] });
		changeRuleMock.mockResolvedValue({ status: "cancelled" });
		await expect(changeLadoProtectedPaths(["infra/**"])).resolves.toBeNull();
		expect(patch).not.toHaveBeenCalled();
	});

	it("patches the active version directly in a browser", async () => {
		changeRuleMock.mockResolvedValue(null);
		get.mockResolvedValue({ data: { rule, versions: [] }, error: undefined });
		patch.mockResolvedValue({ data: { applied: true, impact: { projects: [], tasks: [] }, rule }, error: undefined });
		await expect(changeLadoProtectedPaths(["infra/**"])).resolves.toEqual(rule);
		expect(patch).toHaveBeenCalledWith("/api/v1/lado/rules/{ruleId}", {
			params: { path: { ruleId: "paths.protected" } },
			body: { baseVersion: 2, paths: { globs: ["infra/**"] } },
		});
	});
});
