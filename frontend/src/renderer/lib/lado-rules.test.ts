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
		expect(ladoRuleName(ko, "setup.change.requires_presence", "Local setup changes need the desktop app's confirmation")).toBe(
			"로컬 설정 변경 확인 필수",
		);
		expect(ladoRuleDescription(ko, "setup.change.requires_presence", "Local setup changes need the desktop app's confirmation")).toBe(
			"명령을 실행하거나 접근을 넓힐 수 있는 로컬 설정 변경은 데스크톱 앱에서 확인합니다.",
		);
		expect(ladoStrengthLabel(ko, "advisory")).toBe("권고");
	});

	it("translates the gates of a pull request unless the developer renamed the rule", () => {
		const ko = createFullAppI18n("ko").t;
		const gates: [string, string, string, string][] = [
			[
				"integration.approval.required",
				"Integration approval required",
				"통합 승인 필수",
				"워커는 풀 리퀘스트를 병합하지 않습니다. 각 head의 통합은 LADO에서 직접 승인합니다.",
			],
			[
				"git.protected_branch.no_direct_write",
				"No direct writes to the base branch",
				"기준 브랜치 직접 쓰기 금지",
				"워커는 기준 브랜치에 푸시하거나 리셋하거나 갱신하지 않습니다. 작업은 풀 리퀘스트로만 기준 브랜치에 들어갑니다.",
			],
			["test.command.must_pass", "Tests must pass", "테스트 통과 필수", "프로젝트의 검사 명령과 필수 CI 검사가 통과해야 풀 리퀘스트 head가 준비됩니다."],
			["paths.protected", "Protected paths", "보호 경로", "승인된 계획이 이름을 댄 경우에만 워커가 보호 경로를 바꿉니다."],
		];
		for (const [ruleId, seedName, name, description] of gates) {
			expect(ladoRuleName(ko, ruleId, seedName)).toBe(name);
			expect(ladoRuleDescription(ko, ruleId, seedName)).toBe(description);
			// A rule renamed after it was seeded keeps the name it was given.
			expect(ladoRuleName(ko, ruleId, "우리 팀 규칙")).toBe("우리 팀 규칙");
			expect(ladoRuleDescription(ko, ruleId, "우리 팀 규칙")).toBeUndefined();
		}
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

	it("names the agent and the rules in capability problems", () => {
		const en = createFullAppI18n("en").t;
		const ko = createFullAppI18n("ko").t;
		const problem = (code: string, agent: string) => ({
			code,
			severity: "blocking" as const,
			rules: ["git.commit.message.korean", "team.review"],
			details: { agent, capability: "prompt" },
		});
		const rules = [
			{ ruleId: "git.commit.message.korean", name: "Commit messages in Korean" },
			{ ruleId: "team.review", name: "Ask for a review" },
		];
		expect(ladoProblemText(en, problem("LADO_AGENT_CAPABILITY_UNSUPPORTED", "cursor"), rules)).toBe(
			"Cursor cannot apply rule text, which these rules need: Commit messages in Korean, Ask for a review.",
		);
		expect(ladoProblemText(en, problem("LADO_AGENT_CAPABILITY_DEGRADED", "claude-code"), rules)).toBe(
			"Claude Code applies rule text only partly for these rules: Commit messages in Korean, Ask for a review.",
		);
		// An agent the app does not know keeps its id.
		expect(ladoProblemText(en, problem("LADO_AGENT_CAPABILITY_UNVERIFIED", "my-agent"), rules)).toBe(
			"LADO has not verified that my-agent applies rule text, which these rules need: Commit messages in Korean, Ask for a review.",
		);
		expect(ladoProblemText(ko, problem("LADO_AGENT_CAPABILITY_UNSUPPORTED", "cursor"), rules)).toBe(
			"Cursor에서는 규칙 텍스트 적용을 지원하지 않습니다. 이 기능이 필요한 규칙: 한국어 커밋 메시지, Ask for a review.",
		);
	});
});
