import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { createElement, type PropsWithChildren } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createFullAppI18n } from "../i18n/all-messages";
import { apiClient } from "./api-client";
import type { LadoRule, LadoRuleImpact } from "./lado";
import { ladoProblemText, ladoRuleDescription, ladoRuleName, ladoStrengthLabel, useLadoRuleChange } from "./lado-rules";

const { changeRuleMock } = vi.hoisted(() => ({ changeRuleMock: vi.fn() }));

vi.mock("./api-client", () => ({
	apiClient: { POST: vi.fn(), GET: vi.fn(), PATCH: vi.fn(), PUT: vi.fn() },
	apiErrorCode: (error: unknown) => (error as { code?: string } | undefined)?.code,
	apiErrorDetails: (error: unknown) => (error as { details?: Record<string, unknown> } | undefined)?.details,
	apiErrorMessage: (error: unknown, fallback: string) => (error as { message?: string } | undefined)?.message ?? fallback,
}));

vi.mock("./bridge", () => ({
	aoBridge: { lado: { presence: { approvePlan: vi.fn(), changeRule: changeRuleMock } } },
}));

const put = apiClient.PUT as unknown as ReturnType<typeof vi.fn>;
const patch = apiClient.PATCH as unknown as ReturnType<typeof vi.fn>;

const rule = { id: "team.review", name: "Ask for a review", version: 2, global: "off" } as LadoRule;
const noImpact: LadoRuleImpact = { projects: [], tasks: [] };
const impact: LadoRuleImpact = {
	projects: [{ id: "demo", name: "Demo" }],
	tasks: [{ id: "lado-task-1", projectId: "demo", title: "Add login", issueRef: "github:acme/demo#12" }],
};
const binding = { ruleId: "team.review", scope: "global", scopeId: "", state: "off" } as const;
const changeKeys = [["lado", "rules"], ["lado", "rule"], ["lado", "effective"], ["lado", "tasks"], ["lado", "task"]];

function answer(applied: boolean, reach: LadoRuleImpact) {
	return { data: { applied, impact: reach, rule }, error: undefined };
}

function renderChange() {
	const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
	const invalidate = vi.spyOn(queryClient, "invalidateQueries");
	const wrapper = ({ children }: PropsWithChildren) => createElement(QueryClientProvider, { client: queryClient }, children);
	const { result } = renderHook(() => useLadoRuleChange(), { wrapper });
	return { result, invalidate };
}

function invalidatedKeys(invalidate: { mock: { calls: unknown[][] } }) {
	return invalidate.mock.calls.map(([filters]) => (filters as { queryKey: unknown }).queryKey);
}

// LADO fork: a rule change that reaches approved tasks asks first
// (docs/LJY-PRD/HARNESS.md §14).
describe("useLadoRuleChange", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("asks before a change that affects approved tasks", async () => {
		put.mockResolvedValueOnce(answer(false, impact));
		const { result, invalidate } = renderChange();

		let outcome: unknown;
		await act(async () => {
			outcome = await result.current.change({ kind: "binding", input: binding });
		});

		expect(outcome).toEqual({ status: "confirm", impact, rule });
		expect(put).toHaveBeenCalledTimes(1);
		expect(put).toHaveBeenCalledWith("/api/v1/lado/bindings", { body: { ...binding, dryRun: true } });
		expect(result.current.pending).toEqual({ request: { kind: "binding", input: binding }, impact, rule });
		expect(invalidate).not.toHaveBeenCalled();

		// Cancelling drops the change; nothing was sent for real.
		act(() => result.current.cancel());
		expect(result.current.pending).toBeNull();
		expect(put).toHaveBeenCalledTimes(1);

		put.mockResolvedValueOnce(answer(false, impact)).mockResolvedValueOnce(answer(true, impact));
		await act(async () => {
			await result.current.change({ kind: "binding", input: binding });
		});
		await act(async () => {
			outcome = await result.current.confirm();
		});

		expect(outcome).toEqual({ status: "applied", rule });
		expect(put).toHaveBeenLastCalledWith("/api/v1/lado/bindings", { body: binding });
		expect(result.current.pending).toBeNull();
		expect(invalidatedKeys(invalidate)).toEqual(changeKeys);
	});

	it("applies a change without approved tasks at once", async () => {
		put.mockResolvedValueOnce(answer(false, noImpact)).mockResolvedValueOnce(answer(true, noImpact));
		const { result, invalidate } = renderChange();

		let outcome: unknown;
		await act(async () => {
			outcome = await result.current.change({ kind: "binding", input: binding });
		});

		expect(outcome).toEqual({ status: "applied", rule });
		expect(put.mock.calls).toEqual([
			["/api/v1/lado/bindings", { body: { ...binding, dryRun: true } }],
			["/api/v1/lado/bindings", { body: binding }],
		]);
		expect(result.current.pending).toBeNull();
		expect(invalidatedKeys(invalidate)).toEqual(changeKeys);
	});

	it("changes the ceiling through the desktop confirmation after a dry run", async () => {
		patch.mockResolvedValue(answer(false, noImpact));
		changeRuleMock.mockResolvedValueOnce({ status: "cancelled" });
		const { result, invalidate } = renderChange();
		const request = { kind: "ceiling", ruleId: "agent.permission.ceiling", baseVersion: 3, mode: "bypass-permissions" } as const;

		let outcome: unknown;
		await act(async () => {
			outcome = await result.current.change(request);
		});

		expect(patch).toHaveBeenCalledWith("/api/v1/lado/rules/{ruleId}", {
			params: { path: { ruleId: "agent.permission.ceiling" } },
			body: { baseVersion: 3, ceiling: { mode: "bypass-permissions" }, dryRun: true },
		});
		expect(changeRuleMock).toHaveBeenCalledWith("agent.permission.ceiling", "bypass-permissions");
		expect(outcome).toEqual({ status: "cancelled" });
		expect(invalidate).not.toHaveBeenCalled();
	});

	it("rejects with the daemon's error and applies nothing", async () => {
		put.mockResolvedValueOnce({ data: undefined, error: { code: "LADO_RULE_PROTECTED", message: "This rule is protected." } });
		const { result } = renderChange();

		let caught: unknown;
		await act(async () => {
			caught = await result.current.change({ kind: "binding", input: binding }).catch((error: unknown) => error);
		});

		expect(caught).toMatchObject({ name: "LadoApiError", code: "LADO_RULE_PROTECTED" });
		expect(put).toHaveBeenCalledTimes(1);
		expect(result.current.pending).toBeNull();
	});
});

describe("rule presentation", () => {
	it("translates built-in names unless the developer renamed the rule", () => {
		const ko = createFullAppI18n("ko").t;
		expect(ladoRuleName(ko, "agent.permission.ceiling", "Permission ceiling")).toBe("권한 상한");
		expect(ladoRuleName(ko, "git.commit.message.korean", "Commit messages in Korean")).toBe("한국어 커밋 메시지");
		expect(ladoRuleName(ko, "git.commit.message.korean", "커밋은 한국어로")).toBe("커밋은 한국어로");
		expect(ladoRuleName(ko, "team.review", "Ask for a review")).toBe("Ask for a review");
		expect(ladoRuleDescription(ko, "plan.approval.required", "Plan approval required")).toBe("LADO는 승인한 계획으로만 작업을 시작합니다.");
		expect(ladoRuleDescription(ko, "team.review", "Ask for a review")).toBeUndefined();
		expect(ladoStrengthLabel(ko, "advisory")).toBe("권고");
	});

	it("names conflicting rules as the app shows them", () => {
		const en = createFullAppI18n("en").t;
		const problem = { code: "LADO_RULES_CONFLICT", severity: "blocking" as const, rules: ["git.commit.message.korean", "team.english"], details: {} };
		expect(
			ladoProblemText(en, problem, [
				{ ruleId: "git.commit.message.korean", name: "Commit messages in Korean" },
				{ ruleId: "team.english", name: "Commits in English" },
			]),
		).toBe("These rules cannot be on together: Commit messages in Korean, Commits in English.");
	});

	it("names the agent in capability problems", () => {
		const en = createFullAppI18n("en").t;
		const problem = (code: string, agent: string) => ({ code, severity: "blocking" as const, rules: [], details: { agent, capability: "prompt" } });
		expect(ladoProblemText(en, problem("LADO_AGENT_CAPABILITY_UNSUPPORTED", "cursor"))).toBe("Cursor cannot apply rule text, which these rules need.");
		expect(ladoProblemText(en, problem("LADO_AGENT_CAPABILITY_DEGRADED", "claude-code"))).toBe("Claude Code applies rule text only partly.");
		// An agent the app does not know keeps its id.
		expect(ladoProblemText(en, problem("LADO_AGENT_CAPABILITY_UNVERIFIED", "my-agent"))).toBe(
			"LADO has not verified that my-agent applies rule text, which these rules need.",
		);
	});
});
